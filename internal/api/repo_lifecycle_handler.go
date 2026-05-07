package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/pathresolver"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/repoaccess"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

type NamespaceReader interface {
	GetNamespace(ctx context.Context, namespaceID string) (resources.Namespace, error)
}

type RepoFenceReader interface {
	ListHeldRepoFences(ctx context.Context, repoID string) ([]fences.Fence, error)
}

type RepoLifecycleHandlerConfig struct {
	RepoReader        RepoReader
	NamespaceReader   NamespaceReader
	BindingReader     NamespaceVolumeBindingReader
	FenceReader       RepoFenceReader
	IntakeStore       OperationIntakeStore
	IntakeLookupStore OperationIdempotencyLookupStore
	PrincipalResolver PrincipalResolver
	AllowedCallers    AllowedCallerPolicy
	BreakGlassCallers AllowedCallerPolicy
	OperationID       OperationIDGenerator
	Now               func() time.Time
	AuditSink         audit.Sink
}

type lifecycleRequestDTO struct {
	Reason string `json:"reason,omitempty"`
}

type purgeRepoRequestDTO struct {
	Reason                     string `json:"reason"`
	ProductConfirmationRef     string `json:"product_confirmation_ref"`
	RetentionOverrideRequested bool   `json:"retention_override_requested,omitempty"`
	OperatorApprovalRef        string `json:"operator_approval_ref,omitempty"`
}

type repoLifecycleCanonicalRequest struct {
	RepoID string `json:"repo_id"`
	Body   any    `json:"body"`
}

func RepoLifecycleHandler(config RepoLifecycleHandlerConfig) http.Handler {
	routes := repoLifecycleRoutes()
	lookupStore := config.IntakeLookupStore
	if lookupStore == nil {
		if typed, ok := config.IntakeStore.(OperationIdempotencyLookupStore); ok {
			lookupStore = typed
		}
	}
	leaf := repoLifecycleLeafHandler{
		routes:          routes,
		repoReader:      config.RepoReader,
		namespaceReader: config.NamespaceReader,
		bindingReader:   config.BindingReader,
		fenceReader:     config.FenceReader,
		intakeStore:     config.IntakeStore,
		lookupStore:     lookupStore,
		operationID:     config.OperationID,
		now:             config.Now,
		sink:            config.AuditSink,
		allowedCallers:  config.AllowedCallers,
		breakGlass:      config.BreakGlassCallers,
	}
	return AuthGateWithAuditSink(leaf, config.PrincipalResolver, repoLifecycleRouteResolver{routes: routes}, config.AllowedCallers, config.AuditSink)
}

func repoLifecycleRoutes() []RouteMetadata {
	ids := []string{"archiveRepo", "restoreArchivedRepo", "deleteRepo", "restoreTombstonedRepo", "purgeRepo"}
	routes := make([]RouteMetadata, 0, len(ids))
	for _, id := range ids {
		if route, ok := RouteMetadataByOperationID(id); ok {
			routes = append(routes, route)
		}
	}
	return routes
}

type repoLifecycleRouteResolver struct{ routes []RouteMetadata }

func (resolver repoLifecycleRouteResolver) ResolveRouteClass(r *http.Request) (RouteMetadata, bool) {
	if r == nil || r.URL == nil {
		return RouteMetadata{}, false
	}
	method := strings.ToUpper(strings.TrimSpace(r.Method))
	for _, route := range resolver.routes {
		if route.Method == method {
			if _, ok := RoutePathParams(route.Path, r.URL.Path); ok {
				return route, true
			}
		}
	}
	return RouteMetadata{}, false
}

type repoLifecycleLeafHandler struct {
	routes          []RouteMetadata
	repoReader      RepoReader
	namespaceReader NamespaceReader
	bindingReader   NamespaceVolumeBindingReader
	fenceReader     RepoFenceReader
	intakeStore     OperationIntakeStore
	lookupStore     OperationIdempotencyLookupStore
	operationID     OperationIDGenerator
	now             func() time.Time
	sink            audit.Sink
	allowedCallers  AllowedCallerPolicy
	breakGlass      AllowedCallerPolicy
}

func (handler repoLifecycleLeafHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestContext, ok := RequestContextFromRequest(r)
	if !ok {
		writeRepoLifecycleError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	route, ok := handler.routeForRequest(r)
	if !ok {
		writeRepoLifecycleError(w, r, http.StatusNotFound, CodePathDenied, "route is not available", false)
		return
	}
	repoID := repoIDFromLifecycleRoute(r, route)
	if err := pathresolver.ValidateID(pathresolver.RepoID, repoID); err != nil {
		writeRepoLifecycleValidationError(w, r, route, requestContext, CodeInvalidID, "invalid repo id", []string{"invalid_repo_id"}, handler.sink)
		return
	}
	namespaceID := strings.TrimSpace(r.Header.Get(auth.HeaderNamespaceID))
	if namespaceID == "" {
		writeRepoLifecycleValidationError(w, r, route, requestContext, CodeResourceNamespaceMismatch, "request namespace is required", []string{"missing_namespace_id"}, handler.sink)
		return
	}
	if err := pathresolver.ValidateID(pathresolver.NamespaceID, namespaceID); err != nil {
		writeRepoLifecycleValidationError(w, r, route, requestContext, CodeInvalidID, "invalid namespace id", []string{"invalid_namespace_id"}, handler.sink)
		return
	}
	if handler.repoReader == nil || handler.namespaceReader == nil || handler.bindingReader == nil || handler.fenceReader == nil || handler.intakeStore == nil || handler.lookupStore == nil {
		writeRepoLifecycleError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	body, canonicalBody, baseSummary, err := handler.decodeRequest(route.OperationID, r)
	if err != nil {
		writeRepoLifecycleValidationError(w, r, route, requestContext, err.Code, err.Message, []string{err.Label}, handler.sink)
		return
	}
	canonical := repoLifecycleCanonicalRequest{RepoID: repoID, Body: canonicalBody}
	if reused, handled := handler.writeExistingIdempotentOperation(w, r, route, requestContext, namespaceID, canonical); handled {
		_ = reused
		return
	}
	repo, namespace, binding, heldFences, ok := handler.loadMetadata(w, r, route, requestContext, namespaceID, repoID)
	if !ok {
		return
	}
	decision := repoaccess.Admit(repoaccess.Request{
		Repo:           repo,
		Namespace:      namespace,
		Binding:        binding,
		HeldRepoFences: heldFences,
		Intent:         repoLifecycleIntent(route.OperationID),
		Mode:           repoaccess.ModeReadWrite,
	})
	if !decision.Allowed {
		handler.writeAdmissionDenied(w, r, route, requestContext, decision)
		return
	}
	now := handler.currentTime()
	if route.OperationID == "restoreTombstonedRepo" && !restoreTombstonedRetentionActive(repo, now) {
		handler.writePolicyDenied(w, r, route, requestContext, http.StatusConflict, CodeRepoLifecycleInvalidState, false, "repo lifecycle state does not allow this operation", []string{"restore_tombstoned_retention_expired"})
		return
	}
	breakGlassAuthorized := false
	if route.OperationID == "purgeRepo" {
		var policyOK bool
		breakGlassAuthorized, policyOK = handler.admitPurgePolicy(w, r, route, requestContext, repo, binding, body.purge, now)
		if !policyOK {
			return
		}
	}
	summary := cloneAnyMap(baseSummary)
	addLifecyclePolicySnapshot(summary, route.OperationID, repo, binding, body, now, breakGlassAuthorized)
	envelope, intakeErr := CreateOrReuseOperationIntake(r.Context(), OperationIntakeConfig{Store: handler.intakeStore}, OperationIntakeRequest{
		RequestContext:      requestContext,
		Route:               route,
		NamespaceID:         namespaceID,
		RepoID:              repoID,
		Resource:            operations.ResourceRef{Type: "repo", ID: repoID},
		CanonicalRequest:    canonical,
		InputSummary:        summary,
		Phase:               operations.OperationPhaseRepoLifecycleValidate,
		GenerateOperationID: handler.operationID,
		Now:                 func() time.Time { return now },
	})
	if intakeErr != nil {
		writeOperationIntakeHTTPError(w, r, intakeErr)
		return
	}
	_ = writeJSON(w, http.StatusAccepted, envelope)
}

type repoLifecycleBody struct {
	lifecycle lifecycleRequestDTO
	purge     purgeRepoRequestDTO
}

type repoLifecycleValidationError struct {
	Code    ErrorCode
	Message string
	Label   string
}

func (handler repoLifecycleLeafHandler) decodeRequest(operationID string, r *http.Request) (repoLifecycleBody, any, map[string]any, *repoLifecycleValidationError) {
	if operationID == "purgeRepo" {
		body, err := decodePurgeRepoRequest(r)
		if err != nil {
			return repoLifecycleBody{}, nil, nil, &repoLifecycleValidationError{Code: CodeInvalidID, Message: "invalid purge request", Label: "invalid_request_body"}
		}
		summary := purgeRepoInputSummary(body)
		return repoLifecycleBody{purge: body}, body, summary, nil
	}
	body, err := decodeLifecycleRequest(r)
	if err != nil {
		return repoLifecycleBody{}, nil, nil, &repoLifecycleValidationError{Code: CodeInvalidID, Message: "invalid lifecycle request", Label: "invalid_request_body"}
	}
	return repoLifecycleBody{lifecycle: body}, body, lifecycleInputSummary(body), nil
}

func (handler repoLifecycleLeafHandler) writeExistingIdempotentOperation(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext, namespaceID string, canonical any) (OperationEnvelope, bool) {
	operationType, ok := operations.OperationTypeForRouteOperationID(route.OperationID)
	if !ok {
		writeRepoLifecycleError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return OperationEnvelope{}, true
	}
	requestHash, err := operations.HashRequest(canonical)
	if err != nil {
		writeRepoLifecycleError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return OperationEnvelope{}, true
	}
	scope := operations.NewIdempotencyScope(requestContext.CallerService, namespaceID, operationType, requestContext.IdempotencyKey)
	record, err := handler.lookupStore.GetOperationByIdempotencyScope(r.Context(), scope)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return OperationEnvelope{}, false
		}
		handler.writePolicyDenied(w, r, route, requestContext, http.StatusServiceUnavailable, CodeStorageUnavailable, true, "durable metadata store is unavailable", []string{"idempotency_lookup_unavailable"})
		return OperationEnvelope{}, true
	}
	if record.RequestHash != requestHash {
		writeOperationIntakeHTTPError(w, r, &OperationIntakeError{Code: CodeIdempotencyConflict, Status: http.StatusConflict, Retryable: false, Message: "idempotency key conflicts with a different request"})
		return OperationEnvelope{}, true
	}
	envelope := operationEnvelopeFromRecord(record)
	_ = writeJSON(w, http.StatusAccepted, envelope)
	return envelope, true
}

func (handler repoLifecycleLeafHandler) loadMetadata(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext, namespaceID, repoID string) (resources.Repo, resources.Namespace, resources.NamespaceVolumeBinding, []repoaccess.Fence, bool) {
	repo, err := handler.repoReader.GetRepoInNamespace(r.Context(), namespaceID, repoID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			handler.writeMetadataDenied(w, r, route, requestContext, http.StatusNotFound, CodeRepoNotFound, false, "repo was not found", []string{"repo_lifecycle_repo_not_found"}, audit.EventTypePathDenied)
			return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, false
		}
		handler.writePolicyDenied(w, r, route, requestContext, http.StatusServiceUnavailable, CodeStorageUnavailable, true, "durable metadata store is unavailable", []string{"repo_metadata_unavailable"})
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, false
	}
	if repo.NamespaceID != namespaceID {
		handler.writeMetadataDenied(w, r, route, requestContext, http.StatusNotFound, CodeRepoNotFound, false, "repo was not found", []string{"repo_lifecycle_namespace_mismatch"}, audit.EventTypeResourceNamespaceMismatchDenied)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, false
	}
	namespace, err := handler.namespaceReader.GetNamespace(r.Context(), namespaceID)
	if err != nil {
		handler.writeMetadataError(w, r, route, requestContext, err)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, false
	}
	binding, err := handler.bindingReader.GetNamespaceVolumeBinding(r.Context(), namespaceID)
	if err != nil {
		handler.writeMetadataError(w, r, route, requestContext, err)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, false
	}
	heldFences, err := handler.fenceReader.ListHeldRepoFences(r.Context(), repoID)
	if err != nil {
		handler.writePolicyDenied(w, r, route, requestContext, http.StatusServiceUnavailable, CodeStorageUnavailable, true, "durable metadata store is unavailable", []string{"repo_fence_metadata_unavailable"})
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, nil, false
	}
	return repo, namespace, binding, repoAccessFencesFromStore(heldFences), true
}

func repoAccessFencesFromStore(existing []fences.Fence) []repoaccess.Fence {
	out := make([]repoaccess.Fence, len(existing))
	for idx, fence := range existing {
		out[idx] = repoaccess.Fence{
			ID:                fence.ID,
			RepoID:            fence.RepoID,
			Kind:              repoaccess.FenceKind(fence.Kind.String()),
			HolderOperationID: fence.HolderOperationID,
			Status:            repoaccess.FenceStatus(fence.Status.String()),
			ExpiresAt:         fence.ExpiresAt,
			ReleasedAt:        fence.ReleasedAt,
			RecoveredAt:       fence.RecoveredAt,
			CreatedAt:         fence.CreatedAt,
			UpdatedAt:         fence.UpdatedAt,
		}
	}
	return out
}

func (handler repoLifecycleLeafHandler) writeMetadataError(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		handler.writePolicyDenied(w, r, route, requestContext, http.StatusInternalServerError, CodeInternalError, false, "internal server error", []string{"repo_lifecycle_metadata_invariant"})
		return
	}
	handler.writePolicyDenied(w, r, route, requestContext, http.StatusServiceUnavailable, CodeStorageUnavailable, true, "durable metadata store is unavailable", []string{"repo_lifecycle_metadata_unavailable"})
}

func (handler repoLifecycleLeafHandler) admitPurgePolicy(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext, repo resources.Repo, binding resources.NamespaceVolumeBinding, body purgeRepoRequestDTO, now time.Time) (bool, bool) {
	if strings.TrimSpace(body.ProductConfirmationRef) == "" {
		handler.writePolicyDenied(w, r, route, requestContext, http.StatusConflict, CodePurgeConfirmationRequired, false, "purge confirmation is required", []string{"purge_confirmation_required"})
		return false, false
	}
	if strings.TrimSpace(body.Reason) == "" {
		handler.writePolicyDenied(w, r, route, requestContext, http.StatusConflict, CodePurgeConfirmationRequired, false, "purge confirmation is required", []string{"purge_confirmation_required"})
		return false, false
	}
	retentionExpiresAt := repo.Lifecycle.RetentionExpiresAt
	retentionMet := retentionExpiresAt != nil && !now.Before(*retentionExpiresAt)
	if retentionMet {
		return false, true
	}
	if !body.RetentionOverrideRequested {
		handler.writePolicyDenied(w, r, route, requestContext, http.StatusConflict, CodePurgeRetentionNotMet, false, "purge retention has not elapsed", []string{"purge_retention_not_met"})
		return false, false
	}
	if !breakGlassPurgeEnabled(binding) || strings.TrimSpace(body.OperatorApprovalRef) == "" {
		handler.writePolicyDenied(w, r, route, requestContext, http.StatusConflict, CodePurgeRequiresOperatorApproval, false, "operator approval is required for purge override", []string{"purge_requires_operator_approval"})
		return false, false
	}
	breakGlassAuthorized, err := handler.breakGlassAuthorized(r, requestContext)
	if err != nil {
		handler.writeBreakGlassPolicyError(w, r, route, requestContext, err)
		return false, false
	}
	if !breakGlassAuthorized {
		handler.writePolicyDenied(w, r, route, requestContext, http.StatusConflict, CodePurgeRequiresOperatorApproval, false, "operator approval is required for purge override", []string{"purge_requires_operator_approval"})
		return false, false
	}
	return true, true
}

func breakGlassPurgeEnabled(binding resources.NamespaceVolumeBinding) bool {
	value, ok := binding.LifecyclePolicy["break_glass_purge_enabled"].(bool)
	return ok && value
}

func (handler repoLifecycleLeafHandler) writeAdmissionDenied(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext, decision repoaccess.Decision) {
	code := ErrorCode(decision.ErrorFamily)
	status := http.StatusConflict
	if code == CodeInternalError {
		status = http.StatusInternalServerError
	}
	handler.writePolicyDenied(w, r, route, requestContext, status, code, false, repoLifecycleAdmissionMessage(code), []string{"repo_lifecycle_admission_denied"})
}

func (handler repoLifecycleLeafHandler) writePolicyDenied(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext, status int, code ErrorCode, retryable bool, message string, labels []string) {
	writePolicyDeniedErrorWithAudit(w, r, route, requestContext, code, status, retryable, message, labels, handler.sink)
}

func (handler repoLifecycleLeafHandler) writeMetadataDenied(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext, status int, code ErrorCode, retryable bool, message string, labels []string, eventType audit.EventType) {
	writeRepoLifecycleError(w, r, status, code, message, retryable)
	emitDeniedAuditEvent(r.Context(), handler.sink, r, deniedAuditEvent{
		Type:             eventType,
		Route:            route,
		Status:           status,
		Code:             code,
		Reason:           message,
		ValidationErrors: labels,
		RequestContext:   requestContext,
	})
}

func (handler repoLifecycleLeafHandler) writeBreakGlassPolicyError(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext, err error) {
	var policyErr *AllowedCallerPolicyError
	if errors.As(err, &policyErr) && policyErr != nil {
		labels := policyErr.Labels
		if len(labels) == 0 {
			labels = []string{"break_glass_policy_failed"}
		}
		handler.writePolicyDenied(w, r, route, requestContext, policyErr.Status, policyErr.Code, policyErr.Retryable, policyErr.Message, labels)
		return
	}
	handler.writePolicyDenied(w, r, route, requestContext, http.StatusInternalServerError, CodeInternalError, false, "internal server error", []string{"break_glass_policy_failed"})
}

func repoLifecycleAdmissionMessage(code ErrorCode) string {
	switch code {
	case CodeNamespaceDisabled:
		return "namespace is disabled"
	case CodeRepoLifecycleFenceHeld:
		return "repo lifecycle fence is held"
	case CodeWriterSessionFenceHeld:
		return "writer-session fence is held"
	case CodeOperationRecoveryRequired:
		return "operation recovery is required"
	case CodeRepoLifecycleInvalidState:
		return "repo lifecycle state does not allow this operation"
	case CodeRepoArchived:
		return "repo is archived"
	case CodeRepoTombstoned:
		return "repo is tombstoned"
	case CodeRepoPurged:
		return "repo is purged"
	default:
		return "internal server error"
	}
}

func (handler repoLifecycleLeafHandler) routeForRequest(r *http.Request) (RouteMetadata, bool) {
	if r == nil || r.URL == nil || strings.ToUpper(strings.TrimSpace(r.Method)) != http.MethodPost {
		return RouteMetadata{}, false
	}
	for _, route := range handler.routes {
		if _, ok := RoutePathParams(route.Path, r.URL.Path); ok {
			return route, true
		}
	}
	return RouteMetadata{}, false
}

func repoIDFromLifecycleRoute(r *http.Request, route RouteMetadata) string {
	params, ok := RoutePathParams(route.Path, r.URL.Path)
	if !ok {
		return ""
	}
	return strings.TrimSpace(params["repoId"])
}

func repoLifecycleIntent(operationID string) repoaccess.Intent {
	switch operationID {
	case "archiveRepo":
		return repoaccess.IntentLifecycleArchive
	case "restoreArchivedRepo":
		return repoaccess.IntentLifecycleRestoreArchived
	case "deleteRepo":
		return repoaccess.IntentLifecycleDelete
	case "restoreTombstonedRepo":
		return repoaccess.IntentLifecycleRestoreTombstoned
	case "purgeRepo":
		return repoaccess.IntentLifecyclePurge
	default:
		return ""
	}
}

func restoreTombstonedRetentionActive(repo resources.Repo, now time.Time) bool {
	retentionExpiresAt := repo.Lifecycle.RetentionExpiresAt
	return retentionExpiresAt != nil && now.Before(*retentionExpiresAt)
}

func (handler repoLifecycleLeafHandler) breakGlassAuthorized(r *http.Request, requestContext auth.RequestContext) (bool, error) {
	if handler.breakGlass == nil {
		return false, nil
	}
	callers, err := handler.breakGlass.AllowedCallers(r)
	if err != nil {
		return false, err
	}
	return !auth.CallerNotAllowed(requestContext.CallerService, auth.RoleBreakGlassAdmin, callers), nil
}

func addLifecyclePolicySnapshot(summary map[string]any, operationID string, repo resources.Repo, binding resources.NamespaceVolumeBinding, body repoLifecycleBody, now time.Time, breakGlassAuthorized bool) {
	if operationID != "deleteRepo" && operationID != "purgeRepo" {
		return
	}
	snapshot := map[string]any{
		"tombstone_retention_seconds":  lifecyclePolicyNumber(binding, "tombstone_retention_seconds"),
		"retention_expires_at":         nil,
		"retention_met":                false,
		"retention_override_requested": false,
		"break_glass_enabled":          breakGlassPurgeEnabled(binding),
		"operator_approval_present":    false,
		"break_glass_authorized":       breakGlassAuthorized,
	}
	if repo.Lifecycle.RetentionExpiresAt != nil {
		snapshot["retention_expires_at"] = repo.Lifecycle.RetentionExpiresAt.UTC().Format(time.RFC3339Nano)
		snapshot["retention_met"] = !now.Before(*repo.Lifecycle.RetentionExpiresAt)
	}
	if operationID == "purgeRepo" {
		snapshot["retention_override_requested"] = body.purge.RetentionOverrideRequested
		snapshot["operator_approval_present"] = strings.TrimSpace(body.purge.OperatorApprovalRef) != ""
	}
	summary["lifecycle_policy_snapshot"] = snapshot
}

func lifecyclePolicyNumber(binding resources.NamespaceVolumeBinding, key string) any {
	value, ok := binding.LifecyclePolicy[key]
	if !ok {
		return nil
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return typed
	case float64:
		return typed
	default:
		return nil
	}
}

func (handler repoLifecycleLeafHandler) currentTime() time.Time {
	if handler.now != nil {
		return handler.now()
	}
	return time.Now().UTC()
}

func decodeLifecycleRequest(r *http.Request) (lifecycleRequestDTO, error) {
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		return lifecycleRequestDTO{}, err
	}
	if strings.TrimSpace(string(bodyBytes)) == "" {
		return lifecycleRequestDTO{}, nil
	}
	var body lifecycleRequestDTO
	decoder := json.NewDecoder(strings.NewReader(string(bodyBytes)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return lifecycleRequestDTO{}, err
	}
	if len(body.Reason) > 1024 {
		return lifecycleRequestDTO{}, errors.New("reason too long")
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return lifecycleRequestDTO{}, errors.New("multiple json values")
	} else if !errors.Is(err, io.EOF) {
		return lifecycleRequestDTO{}, err
	}
	return body, nil
}

func decodePurgeRepoRequest(r *http.Request) (purgeRepoRequestDTO, error) {
	var body purgeRepoRequestDTO
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return purgeRepoRequestDTO{}, err
	}
	if len(body.Reason) > 1024 || len(body.ProductConfirmationRef) > 256 || len(body.OperatorApprovalRef) > 256 {
		return purgeRepoRequestDTO{}, errors.New("purge field too long")
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return purgeRepoRequestDTO{}, errors.New("multiple json values")
	} else if !errors.Is(err, io.EOF) {
		return purgeRepoRequestDTO{}, err
	}
	return body, nil
}

func lifecycleInputSummary(body lifecycleRequestDTO) map[string]any {
	return map[string]any{"reason_present": strings.TrimSpace(body.Reason) != ""}
}

func purgeRepoInputSummary(body purgeRepoRequestDTO) map[string]any {
	summary := map[string]any{
		"reason_present":               strings.TrimSpace(body.Reason) != "",
		"product_confirmation_present": strings.TrimSpace(body.ProductConfirmationRef) != "",
		"retention_override_requested": body.RetentionOverrideRequested,
		"operator_approval_present":    strings.TrimSpace(body.OperatorApprovalRef) != "",
	}
	if fingerprint := purgeRefFingerprint("product_confirmation_ref", body.ProductConfirmationRef); fingerprint != "" {
		summary["product_confirmation_ref_fingerprint"] = fingerprint
	}
	if fingerprint := purgeRefFingerprint("operator_approval_ref", body.OperatorApprovalRef); fingerprint != "" {
		summary["operator_approval_ref_fingerprint"] = fingerprint
	}
	return summary
}

func purgeRefFingerprint(kind, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("afscp:v1:repo-purge-ref:" + kind + "\x00" + ref))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func writeRepoLifecycleValidationError(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext, code ErrorCode, message string, labels []string, sink audit.Sink) {
	writeValidationErrorWithAudit(w, r, route, requestContext, code, http.StatusBadRequest, message, labels, sink)
}

func writeRepoLifecycleError(w http.ResponseWriter, r *http.Request, status int, code ErrorCode, message string, retryable bool) {
	envelope := NewErrorEnvelope(code, message, retryable, CorrelationIDFromRequest(r), nil, nil)
	_ = WriteErrorEnvelope(w, status, envelope)
}
