package api

import (
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

type EnsureVolumeHandlerConfig struct {
	IntakeStore       OperationIntakeStore
	PrincipalResolver PrincipalResolver
	DeploymentPolicy  AllowedCallerPolicy
	OperationID       OperationIDGenerator
	Now               func() time.Time
	AuditSink         audit.Sink
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
