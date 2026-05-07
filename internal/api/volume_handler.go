package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/pathresolver"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

type VolumeHealthReader interface {
	GetVolume(ctx context.Context, volumeID string) (resources.Volume, error)
}

type VolumeBackendHealthProbe interface {
	CheckVolumeBackendHealth(ctx context.Context, volume resources.Volume) (VolumeBackendHealthResult, error)
}

type VolumeBackendHealthResult struct {
	Healthy bool
}

type VolumeHealthFindingCode string

const (
	VolumeHealthFindingVolumeDisabled      VolumeHealthFindingCode = "VOLUME_DISABLED"
	VolumeHealthFindingVolumeDegraded      VolumeHealthFindingCode = "VOLUME_DEGRADED"
	VolumeHealthFindingCapabilityNotReady  VolumeHealthFindingCode = "CAPABILITY_NOT_READY"
	VolumeHealthFindingBackendProbeMissing VolumeHealthFindingCode = "BACKEND_PROBE_MISSING"
	VolumeHealthFindingBackendProbeFailed  VolumeHealthFindingCode = "BACKEND_PROBE_FAILED"
	VolumeHealthFindingBackendProbeError   VolumeHealthFindingCode = "BACKEND_PROBE_ERROR"
)

func VolumeHealthFindingCodeStrings() []string {
	return []string{
		string(VolumeHealthFindingVolumeDisabled),
		string(VolumeHealthFindingVolumeDegraded),
		string(VolumeHealthFindingCapabilityNotReady),
		string(VolumeHealthFindingBackendProbeMissing),
		string(VolumeHealthFindingBackendProbeFailed),
		string(VolumeHealthFindingBackendProbeError),
	}
}

type EnsureVolumeHandlerConfig struct {
	IntakeStore       OperationIntakeStore
	PrincipalResolver PrincipalResolver
	DeploymentPolicy  AllowedCallerPolicy
	OperationID       OperationIDGenerator
	Now               func() time.Time
	AuditSink         audit.Sink
}

type VolumeHealthHandlerConfig struct {
	Reader            VolumeHealthReader
	BackendProbe      VolumeBackendHealthProbe
	PrincipalResolver PrincipalResolver
	DeploymentPolicy  AllowedCallerPolicy
	Now               func() time.Time
	AuditSink         audit.Sink
}

type VolumeHealthResponse struct {
	VolumeID  string                `json:"volume_id"`
	Status    string                `json:"status"`
	CheckedAt string                `json:"checked_at"`
	Findings  []VolumeHealthFinding `json:"findings"`
}

type VolumeHealthFinding struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
}

type ensureVolumeRequestBodyDTO struct {
	VolumeID       string                         `json:"volume_id"`
	Backend        resources.VolumeBackend        `json:"backend"`
	IsolationClass resources.VolumeIsolationClass `json:"isolation_class"`
	Status         resources.VolumeStatus         `json:"status"`
	Capabilities   map[string]any                 `json:"capabilities"`
}

func EnsureVolumeHandler(config EnsureVolumeHandlerConfig) http.Handler {
	route, _ := RouteMetadataByOperationID("ensureVolume")
	leaf := ensureVolumeLeafHandler{route: route, intakeStore: config.IntakeStore, operationID: config.OperationID, now: config.Now, sink: config.AuditSink}
	return AuthGateWithAuditSink(leaf, config.PrincipalResolver, ensureVolumeRouteResolver{route: route}, config.DeploymentPolicy, config.AuditSink)
}

func VolumeHealthHandler(config VolumeHealthHandlerConfig) http.Handler {
	route, _ := RouteMetadataByOperationID("getVolumeHealth")
	leaf := volumeHealthLeafHandler{route: route, reader: config.Reader, backendProbe: config.BackendProbe, now: config.Now, sink: config.AuditSink}
	return AuthGateWithAuditSink(leaf, config.PrincipalResolver, ensureVolumeRouteResolver{route: route}, config.DeploymentPolicy, config.AuditSink)
}

type volumeHealthLeafHandler struct {
	route        RouteMetadata
	reader       VolumeHealthReader
	backendProbe VolumeBackendHealthProbe
	now          func() time.Time
	sink         audit.Sink
}

func (handler volumeHealthLeafHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestContext, ok := RequestContextFromRequest(r)
	if !ok || handler.reader == nil {
		writeOperationIntakeHTTPError(w, r, internalOperationIntakeError())
		return
	}
	volumeID, ok := ensureVolumePathVolumeID(r, handler.route)
	if !ok {
		writeOperationIntakeHTTPError(w, r, internalOperationIntakeError())
		return
	}
	if err := pathresolver.ValidateID(pathresolver.VolumeID, volumeID); err != nil {
		writeEnsureVolumeValidationError(w, r, handler.route, requestContext, CodeInvalidID, "invalid volume id", []string{"invalid_volume_id"}, handler.sink)
		return
	}
	if strings.TrimSpace(r.Header.Get(auth.HeaderNamespaceID)) != "" {
		writeEnsureVolumeValidationError(w, r, handler.route, requestContext, CodeResourceNamespaceMismatch, "volume-global operation must not include namespace header", []string{"namespace_header_not_allowed"}, handler.sink)
		return
	}
	volume, err := handler.reader.GetVolume(r.Context(), volumeID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			envelope := NewErrorEnvelope(CodeVolumeNotFound, "volume was not found", false, CorrelationIDFromRequest(r), nil, nil)
			_ = WriteErrorEnvelope(w, http.StatusNotFound, envelope)
			return
		}
		envelope := NewErrorEnvelope(CodeStorageUnavailable, "durable metadata store is unavailable", true, CorrelationIDFromRequest(r), nil, nil)
		_ = WriteErrorEnvelope(w, http.StatusServiceUnavailable, envelope)
		return
	}
	if err := volume.Validate(); err != nil || volume.ID != volumeID {
		envelope := NewErrorEnvelope(CodeStorageUnavailable, "durable metadata store is unavailable", true, CorrelationIDFromRequest(r), nil, nil)
		_ = WriteErrorEnvelope(w, http.StatusServiceUnavailable, envelope)
		return
	}
	checkedAt := time.Now().UTC()
	if handler.now != nil {
		checkedAt = handler.now().UTC()
	}
	probeConfigured := handler.backendProbe != nil
	probeResult := VolumeBackendHealthResult{}
	var probeErr error
	if probeConfigured {
		probeResult, probeErr = handler.backendProbe.CheckVolumeBackendHealth(r.Context(), volume)
	}
	_ = writeJSON(w, http.StatusOK, volumeHealthFromVolume(volume, checkedAt, probeConfigured, probeResult, probeErr))
}

func volumeHealthFromVolume(volume resources.Volume, checkedAt time.Time, backendProbeConfigured bool, backendProbeResult VolumeBackendHealthResult, backendProbeErr error) VolumeHealthResponse {
	status := "healthy"
	findings := []VolumeHealthFinding{}
	if volume.Status == resources.VolumeStatusDisabled {
		status = "unavailable"
		findings = append(findings, volumeHealthFinding(VolumeHealthFindingVolumeDisabled, "volume is disabled", "critical"))
	} else if volume.Status == resources.VolumeStatusDegraded {
		status = "degraded"
		findings = append(findings, volumeHealthFinding(VolumeHealthFindingVolumeDegraded, "volume metadata reports degraded status", "warning"))
	}
	for _, capability := range []string{"webdav_export", "workload_mount", "jvs_external_control_root"} {
		if got, _ := volume.Capabilities[capability].(bool); !got {
			if status == "healthy" {
				status = "degraded"
			}
			findings = append(findings, volumeHealthFinding(VolumeHealthFindingCapabilityNotReady, capability+" capability is not available", "warning"))
		}
	}
	if !backendProbeConfigured {
		if status == "healthy" {
			status = "degraded"
		}
		findings = append(findings, volumeHealthFinding(VolumeHealthFindingBackendProbeMissing, "backend health probe is not configured", "warning"))
	} else if backendProbeErr != nil {
		status = "unavailable"
		findings = append(findings, volumeHealthFinding(VolumeHealthFindingBackendProbeError, "backend health probe could not complete", "critical"))
	} else if !backendProbeResult.Healthy {
		status = "unavailable"
		findings = append(findings, volumeHealthFinding(VolumeHealthFindingBackendProbeFailed, "backend health probe did not pass", "critical"))
	}
	return VolumeHealthResponse{VolumeID: volume.ID, Status: status, CheckedAt: checkedAt.Format(time.RFC3339), Findings: findings}
}

func volumeHealthFinding(code VolumeHealthFindingCode, message, severity string) VolumeHealthFinding {
	return VolumeHealthFinding{Code: string(code), Message: message, Severity: severity}
}

type ensureVolumeRouteResolver struct{ route RouteMetadata }

func (resolver ensureVolumeRouteResolver) ResolveRouteClass(r *http.Request) (RouteMetadata, bool) {
	if r == nil || r.URL == nil || strings.ToUpper(strings.TrimSpace(r.Method)) != resolver.route.Method {
		return RouteMetadata{}, false
	}
	if _, ok := RoutePathParams(resolver.route.Path, r.URL.Path); !ok {
		return RouteMetadata{}, false
	}
	return resolver.route, true
}

type ensureVolumeLeafHandler struct {
	route       RouteMetadata
	intakeStore OperationIntakeStore
	operationID OperationIDGenerator
	now         func() time.Time
	sink        audit.Sink
}

func (handler ensureVolumeLeafHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestContext, ok := RequestContextFromRequest(r)
	if !ok {
		writeOperationIntakeHTTPError(w, r, internalOperationIntakeError())
		return
	}
	volumeID, ok := ensureVolumePathVolumeID(r, handler.route)
	if !ok {
		writeOperationIntakeHTTPError(w, r, internalOperationIntakeError())
		return
	}
	if err := pathresolver.ValidateID(pathresolver.VolumeID, volumeID); err != nil {
		writeEnsureVolumeValidationError(w, r, handler.route, requestContext, CodeInvalidID, "invalid volume id", []string{"invalid_volume_id"}, handler.sink)
		return
	}
	if strings.TrimSpace(r.Header.Get(auth.HeaderNamespaceID)) != "" {
		writeEnsureVolumeValidationError(w, r, handler.route, requestContext, CodeResourceNamespaceMismatch, "volume-global operation must not include namespace header", []string{"namespace_header_not_allowed"}, handler.sink)
		return
	}
	body, err := decodeEnsureVolumeRequest(r)
	if err != nil {
		writeEnsureVolumeValidationError(w, r, handler.route, requestContext, CodeInvalidID, "invalid volume ensure request", []string{"invalid_request_body"}, handler.sink)
		return
	}
	if body.VolumeID != volumeID {
		writeEnsureVolumeValidationError(w, r, handler.route, requestContext, CodeInvalidID, "request volume does not match route volume", []string{"volume_mismatch"}, handler.sink)
		return
	}
	now := time.Now().UTC()
	if handler.now != nil {
		now = handler.now()
	}
	volume := body.toResource(now)
	if err := volume.Validate(); err != nil {
		writeEnsureVolumeValidationError(w, r, handler.route, requestContext, CodeInvalidID, "invalid volume ensure request", []string{"invalid_volume"}, handler.sink)
		return
	}
	envelope, err := CreateOrReuseOperationIntake(r.Context(), OperationIntakeConfig{Store: handler.intakeStore}, OperationIntakeRequest{
		RequestContext:      requestContext,
		Route:               handler.route,
		Resource:            operations.ResourceRef{Type: "volume", ID: volumeID},
		CanonicalRequest:    body,
		InputSummary:        volumeInputSummary(volume),
		Phase:               operations.OperationPhaseVolumeEnsureValidate,
		GenerateOperationID: handler.operationID,
		Now:                 handler.now,
	})
	if err != nil {
		writeOperationIntakeHTTPError(w, r, err)
		return
	}
	_ = writeJSON(w, http.StatusOK, envelope)
}

func ensureVolumePathVolumeID(r *http.Request, route RouteMetadata) (string, bool) {
	if r == nil || r.URL == nil {
		return "", false
	}
	params, ok := RoutePathParams(route.Path, r.URL.Path)
	if !ok {
		return "", false
	}
	volumeID := params["volumeId"]
	return volumeID, volumeID != ""
}

func decodeEnsureVolumeRequest(r *http.Request) (ensureVolumeRequestBodyDTO, error) {
	var body ensureVolumeRequestBodyDTO
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return ensureVolumeRequestBodyDTO{}, err
	}
	if strings.TrimSpace(body.VolumeID) == "" {
		return ensureVolumeRequestBodyDTO{}, errors.New("missing volume_id")
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return ensureVolumeRequestBodyDTO{}, errors.New("multiple json values")
	} else if !errors.Is(err, io.EOF) {
		return ensureVolumeRequestBodyDTO{}, err
	}
	return body, nil
}

func (body ensureVolumeRequestBodyDTO) toResource(now time.Time) resources.Volume {
	return resources.Volume{
		ID:             body.VolumeID,
		Backend:        body.Backend,
		IsolationClass: body.IsolationClass,
		Status:         body.Status,
		Capabilities:   cloneAnyMap(body.Capabilities),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

func volumeInputSummary(volume resources.Volume) map[string]any {
	return map[string]any{
		"volume_id":       volume.ID,
		"backend":         string(volume.Backend),
		"isolation_class": string(volume.IsolationClass),
		"status":          string(volume.Status),
		"capabilities":    cloneAnyMap(volume.Capabilities),
	}
}

func writeEnsureVolumeValidationError(w http.ResponseWriter, r *http.Request, route RouteMetadata, requestContext auth.RequestContext, code ErrorCode, message string, labels []string, sink audit.Sink) {
	writeValidationErrorWithAudit(w, r, route, requestContext, code, http.StatusBadRequest, message, labels, sink)
}
