package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/exportaccess"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/pathresolver"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/repoaccess"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
)

type ExportStore interface {
	CreateOrReuseExport(ctx context.Context, request exportaccess.CreateRequest) (exportaccess.CreateResult, error)
	GetExportSession(ctx context.Context, exportID string) (exportaccess.Session, error)
	RevokeExport(ctx context.Context, request exportaccess.RevokeRequest) (exportaccess.RevokeResult, error)
}

type ExportHandlerConfig struct {
	RepoReader        RepoReader
	NamespaceReader   NamespaceReader
	BindingReader     NamespaceVolumeBindingReader
	VolumeReader      VolumeReader
	FenceReader       RepoFenceReader
	Store             ExportStore
	PrincipalResolver PrincipalResolver
	AllowedCallers    AllowedCallerPolicy
	OperationID       OperationIDGenerator
	ExportID          func() string
	Password          func() string
	EventID           func() string
	Now               func() time.Time
	PublicBaseURL     string
	AuditSink         audit.Sink
}

type createExportRequest struct {
	Mode       string `json:"mode"`
	TTLSeconds int    `json:"ttl_seconds,omitempty"`
}

type exportCanonicalRequest struct {
	RepoID     string `json:"repo_id"`
	Mode       string `json:"mode"`
	TTLSeconds int    `json:"ttl_seconds"`
}

func ExportHandler(config ExportHandlerConfig) http.Handler {
	routes := exportRoutes()
	leaf := exportLeafHandler{
		routes:          routes,
		repoReader:      config.RepoReader,
		namespaceReader: config.NamespaceReader,
		bindingReader:   config.BindingReader,
		volumeReader:    config.VolumeReader,
		fenceReader:     config.FenceReader,
		store:           config.Store,
		operationID:     config.OperationID,
		exportID:        config.ExportID,
		password:        config.Password,
		eventID:         config.EventID,
		now:             config.Now,
		publicBaseURL:   config.PublicBaseURL,
		sink:            config.AuditSink,
	}
	return AuthGateWithAuditSink(leaf, config.PrincipalResolver, exportRouteResolver{routes: routes}, config.AllowedCallers, config.AuditSink)
}

func exportRoutes() []RouteMetadata {
	ids := []string{"createExport", "getExport", "revokeExport"}
	routes := make([]RouteMetadata, 0, len(ids))
	for _, id := range ids {
		if route, ok := RouteMetadataByOperationID(id); ok {
			routes = append(routes, route)
		}
	}
	return routes
}

type exportRouteResolver struct{ routes []RouteMetadata }

func (resolver exportRouteResolver) ResolveRouteClass(r *http.Request) (RouteMetadata, bool) {
	if r == nil || r.URL == nil {
		return RouteMetadata{}, false
	}
	method := strings.ToUpper(strings.TrimSpace(r.Method))
	for _, route := range resolver.routes {
		if route.Method != method {
			continue
		}
		if _, ok := RoutePathParams(route.Path, r.URL.Path); ok {
			return route, true
		}
	}
	return RouteMetadata{}, false
}

type exportLeafHandler struct {
	routes          []RouteMetadata
	repoReader      RepoReader
	namespaceReader NamespaceReader
	bindingReader   NamespaceVolumeBindingReader
	volumeReader    VolumeReader
	fenceReader     RepoFenceReader
	store           ExportStore
	operationID     OperationIDGenerator
	exportID        func() string
	password        func() string
	eventID         func() string
	now             func() time.Time
	publicBaseURL   string
	sink            audit.Sink
}

func (handler exportLeafHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestContext, ok := RequestContextFromRequest(r)
	if !ok {
		writeExportError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	route, params, ok := handler.routeForRequest(r)
	if !ok {
		writeExportError(w, r, http.StatusNotFound, CodePathDenied, "route is not available", false)
		return
	}
	switch route.OperationID {
	case "createExport":
		handler.create(w, r, route, params, requestContext)
	case "getExport":
		handler.get(w, r, route, params, requestContext)
	case "revokeExport":
		handler.revoke(w, r, route, params, requestContext)
	default:
		writeExportError(w, r, http.StatusNotFound, CodePathDenied, "route is not available", false)
	}
}

func (handler exportLeafHandler) create(w http.ResponseWriter, r *http.Request, route RouteMetadata, params map[string]string, requestContext auth.RequestContext) {
	if handler.repoReader == nil || handler.namespaceReader == nil || handler.bindingReader == nil || handler.volumeReader == nil || handler.fenceReader == nil || handler.store == nil {
		writeExportError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	namespaceID, ok := requestNamespace(w, r, route, requestContext, handler.sink)
	if !ok {
		return
	}
	repoID := strings.TrimSpace(params["repoId"])
	if err := pathresolver.ValidateID(pathresolver.RepoID, repoID); err != nil {
		writeValidationErrorWithAudit(w, r, route, requestContext, CodeInvalidID, http.StatusBadRequest, "invalid repo id", []string{"invalid_repo_id"}, handler.sink)
		return
	}
	body, err := decodeCreateExportRequest(r)
	if err != nil {
		writeValidationErrorWithAudit(w, r, route, requestContext, CodeInvalidID, http.StatusBadRequest, "invalid export request", []string{"invalid_request_body"}, handler.sink)
		return
	}
	mode, ok := parseExportMode(body.Mode)
	if !ok {
		writeValidationErrorWithAudit(w, r, route, requestContext, CodeInvalidID, http.StatusBadRequest, "invalid export mode", []string{"invalid_export_mode"}, handler.sink)
		return
	}
	repo, namespace, binding, volume, held, ok := handler.loadCreateMetadata(w, r, route, requestContext, namespaceID, repoID)
	if !ok {
		return
	}
	decision := repoaccess.Admit(repoaccess.Request{Repo: repo, Namespace: namespace, Binding: binding, HeldRepoFences: repoAccessFencesFromStore(held), Intent: repoaccess.IntentExportCreate, Mode: repoAccessMode(mode)})
	if !decision.Allowed {
		writeSavePointAdmissionDenied(w, r, route, requestContext, decision, handler.sink)
		return
	}
	if !exportPolicyEnabled(binding) || !volumeWebDAVCapable(volume) {
		writePolicyDeniedErrorWithAudit(w, r, route, requestContext, CodeRepoLifecycleInvalidState, http.StatusConflict, false, "webdav export is not enabled for this namespace or volume", []string{"webdav_export_not_enabled"}, handler.sink)
		return
	}
	maxTTL, ok := exportPolicyMaxSessionSeconds(binding)
	if !ok {
		writePolicyDeniedErrorWithAudit(w, r, route, requestContext, CodeRepoLifecycleInvalidState, http.StatusConflict, false, "webdav export policy is invalid", []string{"webdav_export_policy_invalid"}, handler.sink)
		return
	}
	ttlSeconds, err := exportaccess.ResolveTTLSeconds(body.TTLSeconds, maxTTL)
	if err != nil {
		writeValidationErrorWithAudit(w, r, route, requestContext, CodeInvalidID, http.StatusBadRequest, "invalid export ttl", []string{"invalid_ttl_seconds"}, handler.sink)
		return
	}
	now := handler.currentTime()
	exportID := handler.newExportID()
	if err := pathresolver.ValidateID(pathresolver.ExportID, exportID); err != nil {
		writeExportError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	password := handler.newPassword()
	if strings.TrimSpace(password) == "" {
		writeExportError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	verifier, err := exportaccess.NewRandomPasswordVerifier(password)
	if err != nil {
		writeExportError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	expiresAt := now.Add(time.Duration(ttlSeconds) * time.Second)
	session := exportaccess.Session{
		ID:                     exportID,
		NamespaceID:            namespaceID,
		RepoID:                 repoID,
		Protocol:               exportaccess.ProtocolWebDAV,
		Mode:                   mode,
		Status:                 sessionstate.ExportStatusActive,
		ExpiresAt:              expiresAt,
		CreatedByCallerService: requestContext.CallerService,
		CreatedByActor:         exportaccess.Actor{Type: requestContext.Actor.Type, ID: requestContext.Actor.ID},
		CreatedAt:              now,
		UpdatedAt:              now,
	}
	canonical := exportCanonicalRequest{RepoID: repoID, Mode: string(mode), TTLSeconds: ttlSeconds}
	summary := map[string]any{"export_id": exportID, "namespace_id": namespaceID, "repo_id": repoID, "protocol": string(exportaccess.ProtocolWebDAV), "mode": string(mode), "ttl_seconds": ttlSeconds, "expires_at": expiresAt.Format(time.RFC3339)}
	record, err := handler.succeededExportOperation(requestContext, route, namespaceID, repoID, exportID, canonical, summary, operations.OperationExportCreate, operations.OperationPhaseExportCreateCommitted, now)
	if err != nil {
		writeExportError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	event := handler.auditEvent(requestContext, audit.EventTypeExportCreate, record, audit.OutcomeSucceeded, "export credential issued", map[string]any{"export_id": exportID, "repo_id": repoID, "mode": string(mode), "ttl_seconds": ttlSeconds})
	result, err := handler.store.CreateOrReuseExport(r.Context(), exportaccess.CreateRequest{Session: session, Verifier: verifier, Operation: record, Audit: event})
	if err != nil {
		handler.writeStoreError(w, r, err)
		return
	}
	envelope := operationEnvelopeFromRecord(result.Operation)
	envelope.Result = map[string]any{"export": result.Session}
	if !result.Reused {
		envelope.Result["access"] = exportaccess.Access{
			URL:       handler.exportURL(result.Session.ID),
			Auth:      exportaccess.BasicAuth{Type: "basic", Username: result.Session.ID, Password: password},
			Mode:      result.Session.Mode,
			ExpiresAt: result.Session.ExpiresAt,
		}
	}
	_ = writeJSON(w, http.StatusAccepted, envelope)
}

func (handler exportLeafHandler) get(w http.ResponseWriter, r *http.Request, route RouteMetadata, params map[string]string, requestContext auth.RequestContext) {
	if handler.store == nil {
		writeExportError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	namespaceID, ok := requestNamespace(w, r, route, requestContext, handler.sink)
	if !ok {
		return
	}
	session, ok := handler.readExport(w, r, route, params["exportId"])
	if !ok {
		return
	}
	if !handler.exportInRequestNamespace(w, r, route, requestContext, session, namespaceID) {
		return
	}
	_ = writeJSON(w, http.StatusOK, session)
}

func (handler exportLeafHandler) revoke(w http.ResponseWriter, r *http.Request, route RouteMetadata, params map[string]string, requestContext auth.RequestContext) {
	if handler.store == nil {
		writeExportError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	namespaceID, ok := requestNamespace(w, r, route, requestContext, handler.sink)
	if !ok {
		return
	}
	session, ok := handler.readExport(w, r, route, params["exportId"])
	if !ok {
		return
	}
	if !handler.exportInRequestNamespace(w, r, route, requestContext, session, namespaceID) {
		return
	}
	now := handler.currentTime()
	canonical := map[string]string{"export_id": session.ID}
	record, err := handler.succeededExportOperation(requestContext, route, namespaceID, session.RepoID, session.ID, canonical, map[string]any{"export_id": session.ID, "repo_id": session.RepoID, "target_status": string(sessionstate.ExportStatusRevoking)}, operations.OperationExportRevoke, operations.OperationPhaseExportRevokeCommitted, now)
	if err != nil {
		writeExportError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	event := handler.auditEvent(requestContext, audit.EventTypeExportRevoke, record, audit.OutcomeSucceeded, "export credential disabled; draining active access", map[string]any{"export_id": session.ID, "repo_id": session.RepoID, "target_status": string(sessionstate.ExportStatusRevoking)})
	result, err := handler.store.RevokeExport(r.Context(), exportaccess.RevokeRequest{ExportID: session.ID, NamespaceID: namespaceID, Operation: record, Audit: event, Now: now})
	if err != nil {
		handler.writeStoreError(w, r, err)
		return
	}
	envelope := operationEnvelopeFromRecord(result.Operation)
	envelope.Result = map[string]any{"export": result.Session}
	_ = writeJSON(w, http.StatusAccepted, envelope)
}

func (handler exportLeafHandler) routeForRequest(r *http.Request) (RouteMetadata, map[string]string, bool) {
	if r == nil || r.URL == nil {
		return RouteMetadata{}, nil, false
	}
	method := strings.ToUpper(strings.TrimSpace(r.Method))
	for _, route := range handler.routes {
		if route.Method != method {
			continue
		}
		if params, ok := RoutePathParams(route.Path, r.URL.Path); ok {
			return route, params, true
		}
	}
	return RouteMetadata{}, nil, false
}

func (handler exportLeafHandler) loadCreateMetadata(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext, namespaceID, repoID string) (resources.Repo, resources.Namespace, resources.NamespaceVolumeBinding, resources.Volume, []fences.Fence, bool) {
	repo, err := handler.repoReader.GetRepoInNamespace(r.Context(), namespaceID, repoID)
	if err != nil {
		writeExportMetadataError(w, r, route, requestContext, err, handler.sink)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, resources.Volume{}, nil, false
	}
	namespace, err := handler.namespaceReader.GetNamespace(r.Context(), namespaceID)
	if err != nil {
		writeExportMetadataError(w, r, route, requestContext, err, handler.sink)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, resources.Volume{}, nil, false
	}
	binding, err := handler.bindingReader.GetNamespaceVolumeBinding(r.Context(), namespaceID)
	if err != nil {
		writeExportMetadataError(w, r, route, requestContext, err, handler.sink)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, resources.Volume{}, nil, false
	}
	volume, err := handler.volumeReader.GetVolume(r.Context(), repo.VolumeID)
	if err != nil {
		writeExportMetadataError(w, r, route, requestContext, err, handler.sink)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, resources.Volume{}, nil, false
	}
	held, err := handler.fenceReader.ListHeldRepoFences(r.Context(), repoID)
	if err != nil {
		writeExportMetadataError(w, r, route, requestContext, err, handler.sink)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, resources.Volume{}, nil, false
	}
	return repo, namespace, binding, volume, held, true
}

func (handler exportLeafHandler) readExport(w http.ResponseWriter, r *http.Request, route RouteMetadata, exportID string) (exportaccess.Session, bool) {
	exportID = strings.TrimSpace(exportID)
	if err := pathresolver.ValidateID(pathresolver.ExportID, exportID); err != nil {
		writeValidationError(w, r, route, CodeInvalidID, http.StatusBadRequest, "invalid export id", []string{"invalid_export_id"})
		return exportaccess.Session{}, false
	}
	session, err := handler.store.GetExportSession(r.Context(), exportID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeExportError(w, r, http.StatusNotFound, CodeOperationNotFound, "export was not found", false)
			return exportaccess.Session{}, false
		}
		writeExportError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
		return exportaccess.Session{}, false
	}
	return session, true
}

func (handler exportLeafHandler) exportInRequestNamespace(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext, session exportaccess.Session, namespaceID string) bool {
	if session.NamespaceID == namespaceID {
		return true
	}
	writeValidationErrorWithAudit(w, r, route, requestContext, CodeResourceNamespaceMismatch, http.StatusBadRequest, "request namespace does not match export", []string{"namespace_mismatch"}, handler.sink)
	return false
}

func (handler exportLeafHandler) succeededExportOperation(requestContext auth.RequestContext, route RouteMetadata, namespaceID, repoID, exportID string, canonical any, summary map[string]any, typ operations.OperationType, phase string, now time.Time) (operations.OperationRecord, error) {
	if handler.operationID == nil {
		return operations.OperationRecord{}, errors.New("operation id generator is required")
	}
	operationID := strings.TrimSpace(handler.operationID())
	if operationID == "" {
		return operations.OperationRecord{}, errors.New("operation id is required")
	}
	requestHash, err := operations.HashRequest(canonical)
	if err != nil {
		return operations.OperationRecord{}, err
	}
	finished := now
	return operations.OperationRecord{
		ID:                  operationID,
		Type:                typ,
		State:               operations.OperationStateSucceeded,
		Phase:               phase,
		Attempt:             1,
		IdempotencyScope:    operations.NewIdempotencyScope(requestContext.CallerService, namespaceID, typ, requestContext.IdempotencyKey).String(),
		IdempotencyKey:      requestContext.IdempotencyKey,
		RequestHash:         requestHash,
		CorrelationID:       requestContext.CorrelationID,
		CallerService:       requestContext.CallerService,
		AuthorizedActor:     operations.Actor{Type: requestContext.Actor.Type, ID: requestContext.Actor.ID},
		Resource:            operations.ResourceRef{Type: "export", ID: exportID},
		NamespaceID:         namespaceID,
		RepoID:              repoID,
		ExportID:            exportID,
		ExternalResourceIDs: map[string]string{},
		InputSummary:        cloneAnyMap(summary),
		CreatedAt:           now,
		StartedAt:           &finished,
		FinishedAt:          &finished,
	}, nil
}

func (handler exportLeafHandler) auditEvent(requestContext auth.RequestContext, eventType audit.EventType, record operations.OperationRecord, outcome audit.Outcome, reason string, details map[string]any) audit.Event {
	return audit.Event{
		EventID:         handler.newEventID(),
		Type:            eventType,
		Time:            record.CreatedAt,
		CallerService:   requestContext.CallerService,
		AuthorizedActor: audit.Actor{Type: requestContext.Actor.Type, ID: requestContext.Actor.ID},
		CorrelationID:   requestContext.CorrelationID,
		OperationID:     record.ID,
		Resource:        audit.Resource{Type: "export", ID: record.ExportID, NamespaceID: record.NamespaceID},
		Outcome:         outcome,
		Reason:          reason,
		Details:         details,
	}
}

func (handler exportLeafHandler) writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, operations.ErrIdempotencyConflict) {
		writeOperationIntakeHTTPError(w, r, &OperationIntakeError{Code: CodeIdempotencyConflict, Status: http.StatusConflict, Retryable: false, Message: "idempotency key conflicts with a different request"})
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		writeExportError(w, r, http.StatusNotFound, CodeOperationNotFound, "export was not found", false)
		return
	}
	writeExportError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
}

func (handler exportLeafHandler) currentTime() time.Time {
	if handler.now != nil {
		return handler.now().UTC()
	}
	return time.Now().UTC()
}

func (handler exportLeafHandler) newExportID() string {
	if handler.exportID != nil {
		return strings.TrimSpace(handler.exportID())
	}
	return exportaccess.GenerateExportID()
}

func (handler exportLeafHandler) newPassword() string {
	if handler.password != nil {
		return strings.TrimSpace(handler.password())
	}
	return exportaccess.GeneratePassword()
}

func (handler exportLeafHandler) newEventID() string {
	if handler.eventID != nil {
		return strings.TrimSpace(handler.eventID())
	}
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "evt_export"
	}
	return "evt_" + base64.RawURLEncoding.EncodeToString(b[:])
}

func (handler exportLeafHandler) exportURL(exportID string) string {
	base := strings.TrimRight(strings.TrimSpace(handler.publicBaseURL), "/")
	if base == "" {
		return "/e/" + exportID + "/"
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return base + "/e/" + exportID + "/"
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/e/" + exportID + "/"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func decodeCreateExportRequest(r *http.Request) (createExportRequest, error) {
	var body createExportRequest
	if err := decodeStrictJSON(r, &body); err != nil {
		return body, err
	}
	body.Mode = strings.TrimSpace(body.Mode)
	return body, nil
}

func parseExportMode(raw string) (sessionstate.AccessMode, bool) {
	switch sessionstate.AccessMode(strings.TrimSpace(raw)) {
	case sessionstate.AccessModeReadOnly:
		return sessionstate.AccessModeReadOnly, true
	case sessionstate.AccessModeReadWrite:
		return sessionstate.AccessModeReadWrite, true
	default:
		return "", false
	}
}

func repoAccessMode(mode sessionstate.AccessMode) repoaccess.Mode {
	if mode == sessionstate.AccessModeReadOnly {
		return repoaccess.ModeReadOnly
	}
	return repoaccess.ModeReadWrite
}

func exportPolicyEnabled(binding resources.NamespaceVolumeBinding) bool {
	return boolPolicy(binding.ExportPolicy, "webdav_enabled")
}

func exportPolicyMaxSessionSeconds(binding resources.NamespaceVolumeBinding) (int, bool) {
	value, ok := binding.ExportPolicy["max_session_seconds"]
	if !ok {
		return 0, false
	}
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		if typed != float64(int(typed)) {
			return 0, false
		}
		return int(typed), true
	default:
		return 0, false
	}
}

func volumeWebDAVCapable(volume resources.Volume) bool {
	return volume.Status == resources.VolumeStatusActive && boolPolicy(volume.Capabilities, "webdav_export")
}

func writeExportMetadataError(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext, err error, sink audit.Sink) {
	if errors.Is(err, sql.ErrNoRows) {
		writePolicyDeniedErrorWithAudit(w, r, route, requestContext, CodeRepoNotFound, http.StatusNotFound, false, "export metadata was not found", []string{"export_metadata_not_found"}, sink)
		return
	}
	writePolicyDeniedErrorWithAudit(w, r, route, requestContext, CodeStorageUnavailable, http.StatusServiceUnavailable, true, "durable metadata store is unavailable", []string{"export_metadata_unavailable"}, sink)
}

func writeExportError(w http.ResponseWriter, r *http.Request, status int, code ErrorCode, message string, retryable bool) {
	envelope := NewErrorEnvelope(code, message, retryable, CorrelationIDFromRequest(r), nil, nil)
	_ = WriteErrorEnvelope(w, status, envelope)
}
