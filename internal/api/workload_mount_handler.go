package api

import (
	"context"
	"crypto/rand"
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
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/capability"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/pathresolver"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/repoaccess"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/workloadmount"
)

type WorkloadMountBindingReader interface {
	GetWorkloadMountBinding(ctx context.Context, mountBindingID string) (workloadmount.Binding, error)
}

type WorkloadMountPlanReader interface {
	GetOrchestratorMountPlan(ctx context.Context, namespaceID, mountBindingID string) (workloadmount.Plan, error)
}

type WorkloadMountHandlerConfig struct {
	RepoReader        RepoReader
	NamespaceReader   NamespaceReader
	BindingReader     NamespaceVolumeBindingReader
	VolumeReader      VolumeReader
	FenceReader       RepoFenceReader
	MountReader       WorkloadMountBindingReader
	PlanReader        WorkloadMountPlanReader
	VolumeRoots       map[string]string
	IntakeStore       OperationIntakeStore
	IntakeLookupStore OperationIdempotencyLookupStore
	PrincipalResolver PrincipalResolver
	AllowedCallers    AllowedCallerPolicy
	OperationID       OperationIDGenerator
	MountBindingID    func() string
	EventID           func() string
	Now               func() time.Time
	AdmissionDisabled bool
	AuditSink         audit.Sink
}

type workloadMountBindingResponse struct {
	MountBindingID string                   `json:"mount_binding_id"`
	NamespaceID    string                   `json:"namespace_id"`
	RepoID         string                   `json:"repo_id"`
	VolumeID       string                   `json:"volume_id"`
	MountPath      string                   `json:"mount_path"`
	ReadOnly       bool                     `json:"read_only"`
	Status         sessionstate.MountStatus `json:"status"`
	LeaseExpiresAt time.Time                `json:"lease_expires_at"`
}

type createWorkloadMountRequest struct {
	MountPath    string `json:"mount_path"`
	ReadOnly     bool   `json:"read_only"`
	LeaseSeconds int    `json:"lease_seconds,omitempty"`
}

type updateWorkloadMountStatusRequest struct {
	Status         string `json:"status"`
	ObservedAt     string `json:"observed_at"`
	LeaseExpiresAt string `json:"lease_expires_at,omitempty"`
	Reason         string `json:"reason,omitempty"`
}

type workloadMountStatusCanonicalRequest struct {
	MountBindingID string `json:"mount_binding_id"`
	Status         string `json:"status"`
	ObservedAt     string `json:"observed_at"`
	LeaseExpiresAt string `json:"lease_expires_at,omitempty"`
	Reason         string `json:"reason,omitempty"`
}

func WorkloadMountHandler(config WorkloadMountHandlerConfig) http.Handler {
	routes := workloadMountRoutes()
	lookup := config.IntakeLookupStore
	if lookup == nil {
		if typed, ok := config.IntakeStore.(OperationIdempotencyLookupStore); ok {
			lookup = typed
		}
	}
	leaf := workloadMountLeafHandler{routes: routes, repoReader: config.RepoReader, namespaceReader: config.NamespaceReader, bindingReader: config.BindingReader, volumeReader: config.VolumeReader, fenceReader: config.FenceReader, mountReader: config.MountReader, planReader: config.PlanReader, volumeRoots: cloneVolumeRoots(config.VolumeRoots), intakeStore: config.IntakeStore, lookupStore: lookup, operationID: config.OperationID, mountBindingID: config.MountBindingID, eventID: config.EventID, now: config.Now, admissionDisabled: config.AdmissionDisabled, sink: config.AuditSink}
	return AuthGateWithAuditSink(leaf, config.PrincipalResolver, workloadMountRouteResolver{routes: routes}, config.AllowedCallers, config.AuditSink)
}

func workloadMountRoutes() []RouteMetadata {
	ids := []string{"createWorkloadMountBinding", "getWorkloadMountBinding", "updateWorkloadMountBindingStatus", "getOrchestratorMountPlan", "heartbeatWorkloadMountBinding", "releaseWorkloadMountBinding", "revokeWorkloadMountBinding"}
	routes := make([]RouteMetadata, 0, len(ids))
	for _, id := range ids {
		if route, ok := RouteMetadataByOperationID(id); ok {
			routes = append(routes, route)
		}
	}
	return routes
}

type workloadMountRouteResolver struct{ routes []RouteMetadata }

func (resolver workloadMountRouteResolver) ResolveRouteClass(r *http.Request) (RouteMetadata, bool) {
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

type workloadMountLeafHandler struct {
	routes            []RouteMetadata
	repoReader        RepoReader
	namespaceReader   NamespaceReader
	bindingReader     NamespaceVolumeBindingReader
	volumeReader      VolumeReader
	fenceReader       RepoFenceReader
	mountReader       WorkloadMountBindingReader
	planReader        WorkloadMountPlanReader
	volumeRoots       map[string]string
	intakeStore       OperationIntakeStore
	lookupStore       OperationIdempotencyLookupStore
	operationID       OperationIDGenerator
	mountBindingID    func() string
	eventID           func() string
	now               func() time.Time
	admissionDisabled bool
	sink              audit.Sink
}

func (handler workloadMountLeafHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestContext, ok := RequestContextFromRequest(r)
	if !ok {
		writeWorkloadMountError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	route, params, ok := handler.routeForRequest(r)
	if !ok {
		writeWorkloadMountError(w, r, http.StatusNotFound, CodePathDenied, "route is not available", false)
		return
	}
	switch route.OperationID {
	case "createWorkloadMountBinding":
		handler.create(w, r, route, params, requestContext)
	case "getWorkloadMountBinding":
		handler.get(w, r, route, params, requestContext)
	case "getOrchestratorMountPlan":
		handler.plan(w, r, route, params, requestContext)
	default:
		handler.mutateExisting(w, r, route, params, requestContext)
	}
}

func (handler workloadMountLeafHandler) create(w http.ResponseWriter, r *http.Request, route RouteMetadata, params map[string]string, requestContext auth.RequestContext) {
	namespaceID, ok := requestNamespace(w, r, route, requestContext, handler.sink)
	if !ok {
		return
	}
	repoID := strings.TrimSpace(params["repoId"])
	if err := pathresolver.ValidateID(pathresolver.RepoID, repoID); err != nil {
		writeValidationErrorWithAudit(w, r, route, requestContext, CodeInvalidID, http.StatusBadRequest, "invalid repo id", []string{"invalid_repo_id"}, handler.sink)
		return
	}
	body, err := decodeCreateWorkloadMountRequest(r)
	if err != nil {
		writeValidationErrorWithAudit(w, r, route, requestContext, CodeInvalidID, http.StatusBadRequest, "invalid workload mount request", []string{"invalid_request_body"}, handler.sink)
		return
	}
	if err := workloadmount.ValidateMountPath(body.MountPath); err != nil {
		writeValidationErrorWithAudit(w, r, route, requestContext, CodeInvalidID, http.StatusBadRequest, "invalid mount path", []string{"invalid_mount_path"}, handler.sink)
		return
	}
	leaseSeconds := body.LeaseSeconds
	if leaseSeconds < workloadmount.MinLeaseSeconds || leaseSeconds > workloadmount.MaxLeaseSeconds {
		writeValidationErrorWithAudit(w, r, route, requestContext, CodeInvalidID, http.StatusBadRequest, "invalid lease seconds", []string{"invalid_lease_seconds"}, handler.sink)
		return
	}
	canonical := createWorkloadMountRequest{MountPath: body.MountPath, ReadOnly: body.ReadOnly, LeaseSeconds: leaseSeconds}
	if handler.lookupStore == nil {
		writeWorkloadMountError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	if handler.writeExistingIdempotentOperation(w, r, route, requestContext, namespaceID, canonical) {
		return
	}
	if handler.admissionDisabled {
		writeWorkloadMountAdmissionDisabled(w, r, route, requestContext, handler.sink)
		return
	}
	if handler.repoReader == nil || handler.namespaceReader == nil || handler.bindingReader == nil || handler.volumeReader == nil || handler.fenceReader == nil || handler.intakeStore == nil {
		writeWorkloadMountError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	repo, namespace, binding, volume, held, ok := handler.loadCreateMetadata(w, r, route, requestContext, namespaceID, repoID)
	if !ok {
		return
	}
	mode := repoaccess.ModeReadWrite
	if body.ReadOnly {
		mode = repoaccess.ModeReadOnly
	}
	decision := repoaccess.Admit(repoaccess.Request{Repo: repo, Namespace: namespace, Binding: binding, HeldRepoFences: repoAccessFencesFromStore(held), Intent: repoaccess.IntentWorkloadMount, Mode: mode})
	if !decision.Allowed {
		writeSavePointAdmissionDenied(w, r, route, requestContext, decision, handler.sink)
		return
	}
	if !mountPolicyEnabled(binding) || !volumeWorkloadMountCapable(volume) {
		writePolicyDeniedErrorWithAudit(w, r, route, requestContext, CodeCapabilityDenied, http.StatusForbidden, false, "workload mount is not enabled for this namespace or volume", []string{"workload_mount_not_enabled"}, handler.sink)
		return
	}
	if handler.restoreReconciliationBlocked(w, r, route, requestContext, namespaceID, repoID) {
		return
	}
	now := handler.currentTime()
	mountID := handler.newMountBindingID()
	if err := pathresolver.ValidateID(pathresolver.WorkloadMountBindingID, mountID); err != nil {
		writeWorkloadMountError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	summary := map[string]any{"mount_binding_id": mountID, "namespace_id": namespaceID, "repo_id": repoID, "volume_id": repo.VolumeID, "mount_path": body.MountPath, "read_only": body.ReadOnly, "lease_seconds": leaseSeconds}
	envelope, intakeErr := CreateOrReuseOperationIntake(r.Context(), OperationIntakeConfig{Store: handler.intakeStore}, OperationIntakeRequest{
		RequestContext:      requestContext,
		Route:               route,
		NamespaceID:         namespaceID,
		RepoID:              repoID,
		MountBindingID:      mountID,
		Resource:            operations.ResourceRef{Type: "workload_mount_binding", ID: mountID},
		CanonicalRequest:    canonical,
		InputSummary:        summary,
		Phase:               operations.OperationPhaseMountBindingCreateValidate,
		GenerateOperationID: handler.operationID,
		Now:                 func() time.Time { return now },
	})
	if intakeErr != nil {
		writeOperationIntakeHTTPError(w, r, intakeErr)
		return
	}
	_ = writeJSON(w, http.StatusAccepted, envelope)
}

func (handler workloadMountLeafHandler) get(w http.ResponseWriter, r *http.Request, route RouteMetadata, params map[string]string, requestContext auth.RequestContext) {
	if handler.mountReader == nil {
		writeWorkloadMountError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	namespaceID, ok := requestNamespace(w, r, route, requestContext, handler.sink)
	if !ok {
		return
	}
	binding, ok := handler.readBinding(w, r, route, params["mountBindingId"])
	if !ok {
		return
	}
	if !handler.bindingInRequestNamespace(w, r, route, requestContext, binding, namespaceID) {
		return
	}
	_ = writeJSON(w, http.StatusOK, workloadMountBindingResponseFromBinding(binding))
}

func (handler workloadMountLeafHandler) plan(w http.ResponseWriter, r *http.Request, route RouteMetadata, params map[string]string, requestContext auth.RequestContext) {
	if handler.planReader == nil || handler.mountReader == nil {
		writeWorkloadMountError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	namespaceID, ok := requestNamespace(w, r, route, requestContext, handler.sink)
	if !ok {
		return
	}
	mountBindingID := strings.TrimSpace(params["mountBindingId"])
	if err := pathresolver.ValidateID(pathresolver.WorkloadMountBindingID, mountBindingID); err != nil {
		writeValidationError(w, r, route, CodeInvalidID, http.StatusBadRequest, "invalid mount binding id", []string{"invalid_mount_binding_id"})
		return
	}
	binding, ok := handler.readBinding(w, r, route, mountBindingID)
	if !ok {
		return
	}
	if !handler.bindingInRequestNamespace(w, r, route, requestContext, binding, namespaceID) {
		return
	}
	freshness := workloadmount.BindingPlanFreshnessDecision(binding, handler.currentTime())
	if handler.admissionDisabled && freshness != workloadmount.PlanFreshnessAllowTeardown {
		writeWorkloadMountAdmissionDisabled(w, r, route, requestContext, handler.sink)
		return
	}
	if freshness == workloadmount.PlanFreshnessStaleIssuance {
		writeWorkloadMountStaleLeaseRecoveryRequired(w, r, route, requestContext, handler.sink)
		return
	}
	if handler.restoreReconciliationBlocked(w, r, route, requestContext, namespaceID, binding.RepoID) {
		return
	}
	plan, err := handler.planReader.GetOrchestratorMountPlan(r.Context(), namespaceID, mountBindingID)
	if err != nil {
		handler.writeReadError(w, r, err)
		return
	}
	handler.emitMountPlanAudit(r.Context(), requestContext, route, binding, plan)
	_ = writeJSON(w, http.StatusOK, plan)
}

func (handler workloadMountLeafHandler) mutateExisting(w http.ResponseWriter, r *http.Request, route RouteMetadata, params map[string]string, requestContext auth.RequestContext) {
	namespaceID, ok := requestNamespace(w, r, route, requestContext, handler.sink)
	if !ok {
		return
	}
	mountBindingID := strings.TrimSpace(params["mountBindingId"])
	if err := pathresolver.ValidateID(pathresolver.WorkloadMountBindingID, mountBindingID); err != nil {
		writeValidationError(w, r, route, CodeInvalidID, http.StatusBadRequest, "invalid mount binding id", []string{"invalid_mount_binding_id"})
		return
	}
	canonical, summary, phase, err := handler.mutationCanonical(route.OperationID, mountBindingID, r)
	if err != nil {
		writeValidationErrorWithAudit(w, r, route, requestContext, CodeInvalidID, http.StatusBadRequest, "invalid workload mount request", []string{"invalid_request_body"}, handler.sink)
		return
	}
	if handler.lookupStore == nil {
		writeWorkloadMountError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	if handler.writeExistingIdempotentOperation(w, r, route, requestContext, namespaceID, canonical) {
		return
	}
	if handler.admissionDisabled && workloadMountAdmissionGatedMutation(route) {
		writeWorkloadMountAdmissionDisabled(w, r, route, requestContext, handler.sink)
		return
	}
	if handler.mountReader == nil || handler.intakeStore == nil {
		writeWorkloadMountError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return
	}
	binding, ok := handler.readBinding(w, r, route, mountBindingID)
	if !ok {
		return
	}
	if binding.NamespaceID != namespaceID {
		handler.bindingInRequestNamespace(w, r, route, requestContext, binding, namespaceID)
		return
	}
	if handler.restoreReconciliationBlocked(w, r, route, requestContext, namespaceID, binding.RepoID) {
		return
	}
	if handler.terminalStatusRequiresExportVisible(canonical) && !handler.repoPayloadExportVisible(w, r, route, requestContext, binding.VolumeID, binding.NamespaceID, binding.RepoID) {
		return
	}
	now := handler.currentTime()
	envelope, intakeErr := CreateOrReuseOperationIntake(r.Context(), OperationIntakeConfig{Store: handler.intakeStore}, OperationIntakeRequest{
		RequestContext:      requestContext,
		Route:               route,
		NamespaceID:         namespaceID,
		RepoID:              binding.RepoID,
		MountBindingID:      mountBindingID,
		Resource:            operations.ResourceRef{Type: "workload_mount_binding", ID: mountBindingID},
		CanonicalRequest:    canonical,
		InputSummary:        summary,
		Phase:               phase,
		GenerateOperationID: handler.operationID,
		Now:                 func() time.Time { return now },
	})
	if intakeErr != nil {
		writeOperationIntakeHTTPError(w, r, intakeErr)
		return
	}
	_ = writeJSON(w, http.StatusAccepted, envelope)
}

func (handler workloadMountLeafHandler) terminalStatusRequiresExportVisible(canonical any) bool {
	statusUpdate, ok := canonical.(workloadMountStatusCanonicalRequest)
	if !ok {
		return false
	}
	switch sessionstate.MountStatus(statusUpdate.Status) {
	case sessionstate.MountStatusReleased, sessionstate.MountStatusRevoked:
		return true
	default:
		return false
	}
}

func (handler workloadMountLeafHandler) repoPayloadExportVisible(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext, volumeID, namespaceID, repoID string) bool {
	if err := repoPayloadExportVisible(handler.volumeRoots, volumeID, namespaceID, repoID); err != nil {
		writePolicyDeniedErrorWithAudit(w, r, route, requestContext, CodeExportNotReady, http.StatusConflict, true, "export target is not ready", []string{"repo_payload_not_export_visible"}, handler.sink)
		return false
	}
	return true
}

func workloadMountAdmissionGatedMutation(route RouteMetadata) bool {
	operationType, ok := operations.OperationTypeForRouteOperationID(route.OperationID)
	if !ok {
		return false
	}
	capabilityID, ok := capability.AdmissionCapabilityForOperationType(operationType)
	return ok && capabilityID == capability.WorkloadMountBinding
}

func (handler workloadMountLeafHandler) mutationCanonical(operationID, mountBindingID string, r *http.Request) (any, map[string]any, string, error) {
	summary := map[string]any{"mount_binding_id": mountBindingID}
	switch operationID {
	case "updateWorkloadMountBindingStatus":
		body, err := decodeStatusRequest(r)
		if err != nil {
			return nil, nil, "", err
		}
		status := sessionstate.MountStatus(body.Status)
		if !workloadmount.ValidOrchestratorStatus(status) {
			return nil, nil, "", errors.New("invalid status")
		}
		reason := strings.TrimSpace(body.Reason)
		if len(reason) > workloadmount.MaxReasonLength {
			return nil, nil, "", errors.New("reason too long")
		}
		observedAt, err := parseRequiredRFC3339(body.ObservedAt)
		if err != nil {
			return nil, nil, "", err
		}
		var leaseExpiresAt string
		if strings.TrimSpace(body.LeaseExpiresAt) != "" {
			parsed, err := parseRequiredRFC3339(body.LeaseExpiresAt)
			if err != nil {
				return nil, nil, "", err
			}
			if parsed.Before(observedAt) {
				return nil, nil, "", errors.New("lease_expires_at cannot be before observed_at")
			}
			leaseExpiresAt = parsed.Format(time.RFC3339)
			summary["lease_expires_at"] = leaseExpiresAt
		}
		summary["status"] = string(status)
		summary["observed_at"] = observedAt.Format(time.RFC3339)
		summary["reason"] = reason
		return workloadMountStatusCanonicalRequest{mountBindingID, string(status), observedAt.Format(time.RFC3339), leaseExpiresAt, reason}, summary, operations.OperationPhaseMountBindingStatusValidate, nil
	case "heartbeatWorkloadMountBinding":
		return map[string]string{"mount_binding_id": mountBindingID}, summary, operations.OperationPhaseMountBindingHeartbeatValidate, drainEmptyBody(r)
	case "releaseWorkloadMountBinding":
		return map[string]string{"mount_binding_id": mountBindingID}, summary, operations.OperationPhaseMountBindingReleaseValidate, drainEmptyBody(r)
	case "revokeWorkloadMountBinding":
		return map[string]string{"mount_binding_id": mountBindingID}, summary, operations.OperationPhaseMountBindingRevokeValidate, drainEmptyBody(r)
	default:
		return nil, nil, "", errors.New("unsupported mutation")
	}
}

func (handler workloadMountLeafHandler) routeForRequest(r *http.Request) (RouteMetadata, map[string]string, bool) {
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

func (handler workloadMountLeafHandler) restoreReconciliationBlocked(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext, namespaceID, repoID string) bool {
	gate, ok := handler.intakeStore.(RestoreReconciliationWriteGate)
	if !ok || gate == nil {
		return false
	}
	blocked, err := gate.RestoreReconciliationWriteBlocked(r.Context(), namespaceID, repoID)
	if err != nil {
		writePolicyDeniedErrorWithAudit(w, r, route, requestContext, CodeStorageUnavailable, http.StatusServiceUnavailable, true, "durable metadata store is unavailable", []string{"restore_reconciliation_unavailable"}, handler.sink)
		return true
	}
	if !blocked {
		return false
	}
	writeOperationIntakeHTTPError(w, r, restoreReconciliationActiveIntakeError())
	return true
}

func (handler workloadMountLeafHandler) writeExistingIdempotentOperation(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext, namespaceID string, canonical any) bool {
	if handler.lookupStore == nil {
		return false
	}
	typ, _ := operations.OperationTypeForRouteOperationID(route.OperationID)
	hash, err := operations.HashRequest(canonical)
	if err != nil {
		writeWorkloadMountError(w, r, http.StatusInternalServerError, CodeInternalError, "internal server error", false)
		return true
	}
	scope := operations.NewIdempotencyScope(requestContext.CallerService, namespaceID, typ, requestContext.IdempotencyKey)
	record, err := handler.lookupStore.GetOperationByIdempotencyScope(r.Context(), scope)
	if err != nil {
		return !errors.Is(err, sql.ErrNoRows) && writeWorkloadMountReadError(w, r, err)
	}
	if record.RequestHash != hash {
		writeOperationIntakeHTTPError(w, r, &OperationIntakeError{Code: CodeIdempotencyConflict, Status: http.StatusConflict, Retryable: false, Message: "idempotency key conflicts with a different request"})
		return true
	}
	envelope, projectionErr := operationEnvelopeFromRecord(record)
	if projectionErr != nil {
		writeOperationIntakeHTTPError(w, r, projectionErr)
		return true
	}
	_ = writeJSON(w, http.StatusAccepted, envelope)
	return true
}

func (handler workloadMountLeafHandler) loadCreateMetadata(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext, namespaceID, repoID string) (resources.Repo, resources.Namespace, resources.NamespaceVolumeBinding, resources.Volume, []fences.Fence, bool) {
	repo, err := handler.repoReader.GetRepoInNamespace(r.Context(), namespaceID, repoID)
	if err != nil {
		writeWorkloadMountMetadataError(w, r, route, requestContext, err, handler.sink)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, resources.Volume{}, nil, false
	}
	ns, err := handler.namespaceReader.GetNamespace(r.Context(), namespaceID)
	if err != nil {
		writeWorkloadMountMetadataError(w, r, route, requestContext, err, handler.sink)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, resources.Volume{}, nil, false
	}
	binding, err := handler.bindingReader.GetNamespaceVolumeBinding(r.Context(), namespaceID)
	if err != nil {
		writeWorkloadMountMetadataError(w, r, route, requestContext, err, handler.sink)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, resources.Volume{}, nil, false
	}
	volume, err := handler.volumeReader.GetVolume(r.Context(), repo.VolumeID)
	if err != nil {
		writeWorkloadMountMetadataError(w, r, route, requestContext, err, handler.sink)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, resources.Volume{}, nil, false
	}
	held, err := handler.fenceReader.ListHeldRepoFences(r.Context(), repoID)
	if err != nil {
		writeWorkloadMountMetadataError(w, r, route, requestContext, err, handler.sink)
		return resources.Repo{}, resources.Namespace{}, resources.NamespaceVolumeBinding{}, resources.Volume{}, nil, false
	}
	return repo, ns, binding, volume, held, true
}

func (handler workloadMountLeafHandler) readBinding(w http.ResponseWriter, r *http.Request, route RouteMetadata, mountBindingID string) (workloadmount.Binding, bool) {
	if err := pathresolver.ValidateID(pathresolver.WorkloadMountBindingID, strings.TrimSpace(mountBindingID)); err != nil {
		writeValidationError(w, r, route, CodeInvalidID, http.StatusBadRequest, "invalid mount binding id", []string{"invalid_mount_binding_id"})
		return workloadmount.Binding{}, false
	}
	binding, err := handler.mountReader.GetWorkloadMountBinding(r.Context(), mountBindingID)
	if err != nil {
		handler.writeReadError(w, r, err)
		return workloadmount.Binding{}, false
	}
	return binding, true
}

func (handler workloadMountLeafHandler) bindingInRequestNamespace(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext, binding workloadmount.Binding, namespaceID string) bool {
	if binding.NamespaceID == namespaceID {
		return true
	}
	writeValidationErrorWithAudit(w, r, route, requestContext, CodeResourceNamespaceMismatch, http.StatusBadRequest, "request namespace does not match binding", []string{"namespace_mismatch"}, handler.sink)
	return false
}

func (handler workloadMountLeafHandler) writeReadError(w http.ResponseWriter, r *http.Request, err error) {
	writeWorkloadMountReadError(w, r, err)
}

func (handler workloadMountLeafHandler) currentTime() time.Time {
	if handler.now != nil {
		return handler.now().UTC()
	}
	return time.Now().UTC()
}

func (handler workloadMountLeafHandler) newMountBindingID() string {
	if handler.mountBindingID != nil {
		return strings.TrimSpace(handler.mountBindingID())
	}
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return "wmb_" + hex.EncodeToString(b[:])
}

func (handler workloadMountLeafHandler) newEventID() string {
	if handler.eventID != nil {
		return strings.TrimSpace(handler.eventID())
	}
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return "evt_" + hex.EncodeToString(b[:])
}

func (handler workloadMountLeafHandler) emitMountPlanAudit(ctx context.Context, requestContext auth.RequestContext, route RouteMetadata, binding workloadmount.Binding, plan workloadmount.Plan) {
	if handler.sink == nil {
		return
	}
	eventID := handler.newEventID()
	if eventID == "" {
		return
	}
	event := audit.NewEvent(audit.Event{
		EventID:         eventID,
		Type:            audit.EventTypeMountPlanIssued,
		Time:            handler.currentTime(),
		CallerService:   requestContext.CallerService,
		AuthorizedActor: audit.Actor{Type: requestContext.Actor.Type, ID: requestContext.Actor.ID},
		CorrelationID:   requestContext.CorrelationID,
		Resource:        audit.Resource{Type: "workload_mount_binding", ID: binding.ID, NamespaceID: binding.NamespaceID},
		Outcome:         audit.OutcomeSucceeded,
		Reason:          route.OperationID,
		Details: map[string]any{
			"mount_binding_id": binding.ID,
			"namespace_id":     binding.NamespaceID,
			"repo_id":          binding.RepoID,
			"read_only":        plan.ReadOnly,
		},
	})
	_ = handler.sink.Emit(ctx, event)
}

func requestNamespace(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext, sink audit.Sink) (string, bool) {
	namespaceID := strings.TrimSpace(r.Header.Get(auth.HeaderNamespaceID))
	if namespaceID == "" {
		writeValidationErrorWithAudit(w, r, route, requestContext, CodeResourceNamespaceMismatch, http.StatusBadRequest, "request namespace is required", []string{"missing_namespace_id"}, sink)
		return "", false
	}
	if err := pathresolver.ValidateID(pathresolver.NamespaceID, namespaceID); err != nil {
		writeValidationErrorWithAudit(w, r, route, requestContext, CodeInvalidID, http.StatusBadRequest, "invalid namespace id", []string{"invalid_namespace_id"}, sink)
		return "", false
	}
	return namespaceID, true
}

func decodeCreateWorkloadMountRequest(r *http.Request) (createWorkloadMountRequest, error) {
	var body createWorkloadMountRequest
	if err := decodeStrictJSON(r, &body); err != nil {
		return body, err
	}
	return body, nil
}

func decodeStatusRequest(r *http.Request) (updateWorkloadMountStatusRequest, error) {
	var body updateWorkloadMountStatusRequest
	if err := decodeStrictJSON(r, &body); err != nil {
		return body, err
	}
	body.Status = strings.TrimSpace(body.Status)
	body.ObservedAt = strings.TrimSpace(body.ObservedAt)
	body.LeaseExpiresAt = strings.TrimSpace(body.LeaseExpiresAt)
	body.Reason = strings.TrimSpace(body.Reason)
	return body, nil
}

func parseRequiredRFC3339(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, errors.New("missing timestamp")
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil || parsed.IsZero() {
		return time.Time{}, errors.New("invalid timestamp")
	}
	return parsed.UTC(), nil
}

func decodeStrictJSON(r *http.Request, dest any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return errors.New("multiple json values")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func drainEmptyBody(r *http.Request) error {
	if r.Body == nil {
		return nil
	}
	decoder := json.NewDecoder(r.Body)
	var body map[string]any
	if err := decoder.Decode(&body); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	if len(body) != 0 {
		return errors.New("body must be empty")
	}
	return nil
}

func workloadMountBindingResponseFromBinding(binding workloadmount.Binding) workloadMountBindingResponse {
	return workloadMountBindingResponse{MountBindingID: binding.ID, NamespaceID: binding.NamespaceID, RepoID: binding.RepoID, VolumeID: binding.VolumeID, MountPath: binding.MountPath, ReadOnly: binding.ReadOnly, Status: binding.Status, LeaseExpiresAt: binding.LeaseExpiresAt}
}

func mountPolicyEnabled(binding resources.NamespaceVolumeBinding) bool {
	return boolPolicy(binding.MountPolicy, "workload_mount_enabled") && boolPolicy(binding.MountPolicy, "workload_mount_requires_external_control_root")
}

func volumeWorkloadMountCapable(volume resources.Volume) bool {
	return volume.Status == resources.VolumeStatusActive && boolPolicy(volume.Capabilities, "workload_mount") && boolPolicy(volume.Capabilities, "jvs_external_control_root")
}

func boolPolicy(values map[string]any, key string) bool {
	got, _ := values[key].(bool)
	return got
}

func writeWorkloadMountMetadataError(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext, err error, sink audit.Sink) {
	if errors.Is(err, sql.ErrNoRows) {
		writePolicyDeniedErrorWithAudit(w, r, route, requestContext, CodeRepoNotFound, http.StatusNotFound, false, "workload mount metadata was not found", []string{"workload_mount_metadata_not_found"}, sink)
		return
	}
	writePolicyDeniedErrorWithAudit(w, r, route, requestContext, CodeStorageUnavailable, http.StatusServiceUnavailable, true, "durable metadata store is unavailable", []string{"workload_mount_metadata_unavailable"}, sink)
}

func writeWorkloadMountReadError(w http.ResponseWriter, r *http.Request, err error) bool {
	if errors.Is(err, sql.ErrNoRows) {
		writeWorkloadMountError(w, r, http.StatusNotFound, CodeOperationNotFound, "workload mount binding was not found", false)
		return true
	}
	writeWorkloadMountError(w, r, http.StatusServiceUnavailable, CodeStorageUnavailable, "durable metadata store is unavailable", true)
	return true
}

func writeWorkloadMountAdmissionDisabled(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext, sink audit.Sink) {
	message := "workload mount admission is disabled"
	if route.OperationID == "createWorkloadMountBinding" {
		message = "workload mount create admission is disabled"
	}
	validationErrors := []string{"workload_mount_admission_disabled"}
	var operationID *string
	if route.OperationID != "" {
		operationID = &route.OperationID
	}
	envelope := NewErrorEnvelope(
		CodeCapabilityDenied,
		message,
		false,
		CorrelationIDFromRequest(r),
		operationID,
		map[string]any{"validation_errors": validationErrors},
	)
	_ = WriteErrorEnvelope(w, http.StatusForbidden, envelope)
	emitDeniedAuditEvent(r.Context(), sink, r, deniedAuditEvent{
		Type:             audit.EventTypeCapabilityDenied,
		Route:            route,
		Status:           http.StatusForbidden,
		Code:             CodeCapabilityDenied,
		Reason:           message,
		ValidationErrors: validationErrors,
		RequestContext:   requestContext,
	})
}

func writeWorkloadMountStaleLeaseRecoveryRequired(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext, sink audit.Sink) {
	writePolicyDeniedErrorWithAudit(w, r, route, requestContext, CodeOperationRecoveryRequired, http.StatusConflict, true, "workload mount lease is stale; operator recovery is required", []string{"workload_mount_lease_stale"}, sink)
}

func writeWorkloadMountError(w http.ResponseWriter, r *http.Request, status int, code ErrorCode, message string, retryable bool) {
	envelope := NewErrorEnvelope(code, message, retryable, CorrelationIDFromRequest(r), nil, nil)
	_ = WriteErrorEnvelope(w, status, envelope)
}
