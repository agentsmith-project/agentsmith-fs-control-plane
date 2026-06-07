package exportgateway

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/exportaccess"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/observability"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/pathresolver"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
	"golang.org/x/net/webdav"
)

const (
	defaultPrefix             = "/e/"
	defaultHeartbeatTTL       = 15 * time.Second
	runtimeObservationTimeout = 5 * time.Second
)

var auditEventCounter uint64

type Store interface {
	GetExportGatewayCredential(ctx context.Context, exportID string) (exportaccess.GatewayCredential, error)
	BeginExportRuntimeRequest(ctx context.Context, request exportaccess.RuntimeRequestBegin) (exportaccess.Session, error)
	HeartbeatExportRuntimeRequest(ctx context.Context, request exportaccess.RuntimeRequestHeartbeat) (exportaccess.Session, error)
	EndExportRuntimeRequest(ctx context.Context, request exportaccess.RuntimeRequestEnd) (exportaccess.Session, error)
}

type Config struct {
	Store            Store
	AuditSink        audit.Sink
	AuditEventID     func() string
	RuntimeRequestID func() (string, error)
	Logger           *slog.Logger
	VolumeRoots      map[string]string
	Prefix           string
	Now              func() time.Time
	HeartbeatTTL     time.Duration
}

type ServerConfig struct {
	ListenAddr  string
	PostgresDSN string
	VolumeRoots map[string]string
	Prefix      string
}

type Handler struct {
	store        Store
	auditSink    audit.Sink
	auditEventID func() string
	requestID    func() (string, error)
	logger       *slog.Logger
	volumeRoots  map[string]string
	prefix       string
	now          func() time.Time
	heartbeatTTL time.Duration
	lockSystem   webdav.LockSystem
}

type gatewayPath struct {
	ExportID string
	Root     bool
	RawChild string
	Segments []string
}

const (
	denyClassAuthzDenied                = "authz_denied"
	denyClassCapabilityDenied           = "capability_denied"
	denyClassSourceInvalid              = "source_invalid"
	denyClassSourceJVSDenied            = "source_jvs_denied"
	denyClassSourceTraversalDenied      = "source_traversal_denied"
	denyClassSourcePolicyDenied         = "source_policy_denied"
	denyClassSourceSymlinkDenied        = "source_symlink_denied"
	denyClassDestinationMissing         = "destination_missing"
	denyClassDestinationHostMismatch    = "destination_host_mismatch"
	denyClassDestinationExportMismatch  = "destination_export_mismatch"
	denyClassDestinationInvalid         = "destination_invalid"
	denyClassDestinationJVSDenied       = "destination_jvs_denied"
	denyClassDestinationTraversalDenied = "destination_traversal_denied"
	denyClassDestinationPolicyDenied    = "destination_policy_denied"
	denyClassDestinationSymlinkDenied   = "destination_symlink_denied"
)

func NewHandler(cfg Config) (http.Handler, error) {
	if cfg.Store == nil {
		return nil, errors.New("export gateway store is required")
	}
	prefix := cfg.Prefix
	if prefix == "" {
		prefix = defaultPrefix
	}
	if !validPrefix(prefix) {
		return nil, errors.New("export gateway prefix must start and end with /")
	}
	if len(cfg.VolumeRoots) == 0 {
		return nil, errors.New("export gateway volume roots are required")
	}
	roots := make(map[string]string, len(cfg.VolumeRoots))
	for volumeID, root := range cfg.VolumeRoots {
		if err := pathresolver.ValidateID(pathresolver.VolumeID, volumeID); err != nil {
			return nil, fmt.Errorf("invalid export gateway volume roots")
		}
		if err := validateVolumeRoot(root); err != nil {
			return nil, fmt.Errorf("invalid export gateway volume roots")
		}
		roots[volumeID] = root
	}
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	heartbeatTTL := cfg.HeartbeatTTL
	if heartbeatTTL <= 0 {
		heartbeatTTL = defaultHeartbeatTTL
	}
	auditEventID := cfg.AuditEventID
	if auditEventID == nil {
		auditEventID = newAuditEventID
	}
	requestID := cfg.RuntimeRequestID
	if requestID == nil {
		requestID = newRuntimeRequestID
	}
	handler := &Handler{
		store:        cfg.Store,
		auditSink:    cfg.AuditSink,
		auditEventID: auditEventID,
		requestID:    requestID,
		logger:       cfg.Logger,
		volumeRoots:  roots,
		prefix:       prefix,
		now:          now,
		heartbeatTTL: heartbeatTTL,
		lockSystem:   webdav.NewMemLS(),
	}
	if handler.logger != nil {
		observability.LogEvent(context.Background(), handler.logger, slog.LevelInfo, "export_gateway_start", "export gateway started", map[string]any{
			"component":               "export_gateway",
			"prefix":                  prefix,
			"volume_roots_configured": len(roots),
		})
	}
	return handler, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rawSourcePath := rawRequestPath(r)
	source, ok := h.parseSourcePath(rawSourcePath)
	if !ok {
		if exportID, ok := exportIDFromRawPath(rawSourcePath, h.prefix); ok {
			h.emitDeniedAudit(r, deniedAudit{
				eventType:  audit.EventTypePathDenied,
				status:     http.StatusNotFound,
				reason:     "path_denied",
				reasonCode: "source_path_invalid",
				denyClass:  classifyRawChildDenyClass(rawSourcePath, h.prefix, exportID, "source"),
				exportID:   exportID,
			})
		}
		http.NotFound(w, r)
		return
	}

	username, password, ok := r.BasicAuth()
	if !ok {
		h.emitDeniedAudit(r, deniedAudit{
			eventType:       audit.EventTypeAuthzDenied,
			status:          http.StatusUnauthorized,
			reason:          "authz_denied",
			reasonCode:      "basic_auth_missing",
			denyClass:       denyClassAuthzDenied,
			exportID:        source.ExportID,
			source:          source,
			credentialFound: boolPtr(false),
		})
		w.Header().Set("WWW-Authenticate", `Basic realm="afscp-export"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if username != source.ExportID {
		h.emitDeniedAudit(r, deniedAudit{
			eventType:       audit.EventTypeAuthzDenied,
			status:          http.StatusForbidden,
			reason:          "authz_denied",
			reasonCode:      "username_export_mismatch",
			denyClass:       denyClassAuthzDenied,
			exportID:        source.ExportID,
			source:          source,
			credentialFound: boolPtr(false),
		})
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	credential, err := h.store.GetExportGatewayCredential(r.Context(), source.ExportID)
	if err != nil {
		h.emitDeniedAudit(r, deniedAudit{
			eventType:       audit.EventTypeAuthzDenied,
			status:          http.StatusForbidden,
			reason:          "authz_denied",
			reasonCode:      "credential_lookup_failed",
			denyClass:       denyClassAuthzDenied,
			exportID:        source.ExportID,
			source:          source,
			credentialFound: boolPtr(false),
		})
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !credential.Verifier.Verify(password) {
		h.emitDeniedAudit(r, deniedAudit{
			eventType:       audit.EventTypeAuthzDenied,
			status:          http.StatusForbidden,
			reason:          "authz_denied",
			reasonCode:      "credential_password_mismatch",
			denyClass:       denyClassAuthzDenied,
			exportID:        source.ExportID,
			source:          source,
			credential:      &credential,
			credentialFound: boolPtr(true),
		})
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !credentialUsable(credential, h.now()) {
		h.emitDeniedAudit(r, deniedAudit{
			eventType:       audit.EventTypeAuthzDenied,
			status:          http.StatusForbidden,
			reason:          "authz_denied",
			reasonCode:      "session_not_usable",
			denyClass:       denyClassAuthzDenied,
			exportID:        source.ExportID,
			source:          source,
			credential:      &credential,
			credentialFound: boolPtr(true),
			extraDetail:     map[string]any{"session_status": string(credential.Session.Status)},
		})
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if credential.Session.ID != source.ExportID {
		h.emitDeniedAudit(r, deniedAudit{
			eventType:       audit.EventTypeAuthzDenied,
			status:          http.StatusForbidden,
			reason:          "authz_denied",
			reasonCode:      "credential_session_mismatch",
			denyClass:       denyClassAuthzDenied,
			exportID:        source.ExportID,
			source:          source,
			credential:      &credential,
			credentialFound: boolPtr(true),
		})
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !methodAllowed(r.Method, credential.Session.Mode) {
		h.emitDeniedAudit(r, deniedAudit{
			eventType:       audit.EventTypeCapabilityDenied,
			status:          http.StatusForbidden,
			reason:          "capability_denied",
			reasonCode:      "method_not_allowed",
			denyClass:       denyClassCapabilityDenied,
			exportID:        source.ExportID,
			source:          source,
			credential:      &credential,
			credentialFound: boolPtr(true),
			methodAllowed:   boolPtr(false),
		})
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	payloadRoot, err := h.payloadRoot(credential)
	if err != nil {
		h.emitDeniedAudit(r, deniedAudit{
			eventType:       audit.EventTypePathDenied,
			status:          http.StatusForbidden,
			reason:          "path_denied",
			reasonCode:      "payload_root_resolve_denied",
			denyClass:       denyClassSourcePolicyDenied,
			exportID:        source.ExportID,
			source:          source,
			credential:      &credential,
			credentialFound: boolPtr(true),
			methodAllowed:   boolPtr(true),
		})
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := h.validatePath(payloadRoot, credential.PayloadVolumeSubdir, source); err != nil {
		h.emitDeniedAudit(r, deniedAudit{
			eventType:       audit.EventTypePathDenied,
			status:          http.StatusForbidden,
			reason:          "path_denied",
			reasonCode:      "source_path_denied",
			denyClass:       classifyPolicyDenyClass(err, "source"),
			exportID:        source.ExportID,
			source:          source,
			credential:      &credential,
			credentialFound: boolPtr(true),
			methodAllowed:   boolPtr(true),
		})
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	dest := gatewayPath{Root: true}
	if methodNeedsDestination(r.Method) {
		parsedDest, denyClass, ok := h.parseDestination(r, source.ExportID)
		if !ok {
			h.emitDeniedAudit(r, deniedAudit{
				eventType:       audit.EventTypePathDenied,
				status:          http.StatusForbidden,
				reason:          "path_denied",
				reasonCode:      "destination_path_denied",
				denyClass:       denyClass,
				exportID:        source.ExportID,
				source:          source,
				credential:      &credential,
				credentialFound: boolPtr(true),
				methodAllowed:   boolPtr(true),
			})
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		dest = parsedDest
		if err := h.validatePath(payloadRoot, credential.PayloadVolumeSubdir, dest); err != nil {
			h.emitDeniedAudit(r, deniedAudit{
				eventType:       audit.EventTypePathDenied,
				status:          http.StatusForbidden,
				reason:          "path_denied",
				reasonCode:      "destination_path_denied",
				denyClass:       classifyPolicyDenyClass(err, "destination"),
				exportID:        source.ExportID,
				source:          source,
				dest:            dest,
				credential:      &credential,
				credentialFound: boolPtr(true),
				methodAllowed:   boolPtr(true),
			})
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}

	fs, err := newNoFollowFileSystem(payloadRoot)
	if err != nil {
		h.emitDeniedAudit(r, deniedAudit{
			eventType:       audit.EventTypePathDenied,
			status:          http.StatusForbidden,
			reason:          "path_denied",
			reasonCode:      "payload_root_open_denied",
			denyClass:       denyClassSourcePolicyDenied,
			exportID:        source.ExportID,
			source:          source,
			credential:      &credential,
			credentialFound: boolPtr(true),
			methodAllowed:   boolPtr(true),
		})
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	mutating := methodMutates(r.Method)
	runtimeRequestID, err := h.requestID()
	if err != nil || strings.TrimSpace(runtimeRequestID) == "" {
		_ = fs.Close()
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := h.beginRuntimeRequest(r.Context(), runtimeRequestID, source.ExportID, mutating); err != nil {
		_ = fs.Close()
		if runtimeObservationAdmissionDenied(err) {
			h.emitDeniedAudit(r, deniedAudit{
				eventType:              audit.EventTypeAuthzDenied,
				status:                 http.StatusForbidden,
				reason:                 "authz_denied",
				reasonCode:             "runtime_admission_denied",
				denyClass:              denyClassAuthzDenied,
				exportID:               source.ExportID,
				source:                 source,
				credential:             &credential,
				credentialFound:        boolPtr(true),
				methodAllowed:          boolPtr(true),
				runtimeAdmissionResult: "denied",
			})
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}
	defer fs.Close()
	stopHeartbeat := h.startHeartbeat(r.Context(), runtimeRequestID, source.ExportID)
	defer stopHeartbeat()

	backendReq := cloneForBackend(r, source, dest)
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), runtimeObservationTimeout)
		defer cancel()
		h.endRuntimeRequest(ctx, runtimeRequestID, source.ExportID, rec.status)
	}()
	backend := &webdav.Handler{
		Prefix:     "/",
		FileSystem: fs,
		LockSystem: h.lockSystem,
	}
	backend.ServeHTTP(rec, backendReq)
}

type deniedAudit struct {
	eventType              audit.EventType
	status                 int
	reason                 string
	reasonCode             string
	denyClass              string
	exportID               string
	source                 gatewayPath
	dest                   gatewayPath
	credential             *exportaccess.GatewayCredential
	credentialFound        *bool
	methodAllowed          *bool
	runtimeAdmissionResult string
	extraDetail            map[string]any
}

func (h *Handler) emitDeniedAudit(r *http.Request, denied deniedAudit) {
	exportID := strings.TrimSpace(denied.exportID)
	if exportID == "" {
		exportID = denied.source.ExportID
	}
	if exportID == "" {
		return
	}
	h.emitDeniedLog(r, exportID, denied)

	if h.auditSink == nil {
		return
	}

	details := map[string]any{
		"method":                requestMethod(r),
		"status":                denied.status,
		"reason_code":           denied.reason,
		"component":             "export_gateway",
		"export_id_fingerprint": exportIDFingerprint(exportID),
	}
	if denied.denyClass != "" {
		details["deny_class"] = denied.denyClass
	}
	if denied.reasonCode != "" {
		details["deny_reason_code"] = denied.reasonCode
	}
	resource := audit.Resource{Type: "export", ID: exportID}
	var callerService string
	var actor audit.Actor
	if denied.credential != nil {
		session := denied.credential.Session
		resource.NamespaceID = session.NamespaceID
		callerService = session.CreatedByCallerService
		actor = audit.Actor{Type: session.CreatedByActor.Type, ID: session.CreatedByActor.ID}
		if session.RepoID != "" {
			details["repo_id"] = session.RepoID
		}
		if session.Mode != "" {
			details["export_mode"] = string(session.Mode)
		}
		if session.Status != "" {
			details["session_status"] = string(session.Status)
		}
	}
	for key, value := range denyEvidenceFields(r, exportID, denied) {
		switch key {
		case "event", "method", "status", "reason_code", "deny_class", "component", "export_id_fingerprint", "namespace_id", "repo_id", "export_mode", "session_status":
			continue
		default:
			details[key] = value
		}
	}
	for key, value := range denied.extraDetail {
		details[key] = value
	}

	event := audit.NewEvent(audit.Event{
		EventID:         strings.TrimSpace(h.auditEventID()),
		Type:            denied.eventType,
		Time:            h.now().UTC(),
		CallerService:   callerService,
		AuthorizedActor: actor,
		CorrelationID:   correlationIDFromRequest(r),
		Resource:        resource,
		Outcome:         audit.OutcomeDenied,
		Reason:          denied.reason,
		Details:         details,
	})
	if strings.TrimSpace(event.EventID) == "" {
		event.EventID = newAuditEventID()
	}

	ctx := context.Background()
	if r != nil && r.Context() != nil {
		ctx = r.Context()
	}
	_ = h.auditSink.Emit(ctx, event)
}

func (h *Handler) emitDeniedLog(r *http.Request, exportID string, denied deniedAudit) {
	if h.logger == nil {
		return
	}
	observability.LogEvent(
		requestContext(r),
		h.logger,
		slog.LevelWarn,
		"export_gateway_denied",
		"export gateway denied WebDAV request",
		denyEvidenceFields(r, exportID, denied),
	)
}

func denyEvidenceFields(r *http.Request, exportID string, denied deniedAudit) map[string]any {
	reasonCode := denied.reasonCode
	if reasonCode == "" {
		reasonCode = denied.reason
	}
	fields := map[string]any{
		"component":             "export_gateway",
		"method":                requestMethod(r),
		"status":                denied.status,
		"reason_code":           reasonCode,
		"export_id_fingerprint": exportIDFingerprint(exportID),
	}
	if denied.denyClass != "" {
		fields["deny_class"] = denied.denyClass
	}
	if denied.credentialFound != nil {
		fields["credential_found"] = *denied.credentialFound
	} else {
		fields["credential_found"] = denied.credential != nil
	}
	if denied.methodAllowed != nil {
		fields["method_allowed"] = *denied.methodAllowed
	} else if denied.credential != nil {
		fields["method_allowed"] = methodAllowed(requestMethod(r), denied.credential.Session.Mode)
	}
	if denied.runtimeAdmissionResult != "" {
		fields["runtime_admission_result"] = denied.runtimeAdmissionResult
	}
	addPathEvidence(fields, "source", denied.source, exportID)
	addPathEvidence(fields, "destination", denied.dest, "")

	if denied.credential != nil {
		session := denied.credential.Session
		if session.NamespaceID != "" {
			fields["namespace_id"] = session.NamespaceID
		}
		if session.RepoID != "" {
			fields["repo_id"] = session.RepoID
		}
		if session.Mode != "" {
			fields["export_mode"] = string(session.Mode)
		}
		if session.Status != "" {
			fields["session_status"] = string(session.Status)
		}
	}
	for key, value := range denied.extraDetail {
		fields[key] = value
	}
	return fields
}

func addPathEvidence(fields map[string]any, prefix string, parsed gatewayPath, exportID string) {
	pathClass, segments, ok := pathEvidence(parsed, exportID)
	if !ok {
		return
	}
	fields[prefix+"_path_class"] = pathClass
	fields[prefix+"_child_segments"] = segments
}

func pathEvidence(parsed gatewayPath, exportID string) (string, int, bool) {
	if parsed.Root {
		return "root", 0, true
	}
	if len(parsed.Segments) > 0 {
		return "child", len(parsed.Segments), true
	}
	if parsed.ExportID != "" || strings.TrimSpace(exportID) != "" {
		return "invalid", 0, true
	}
	return "", 0, false
}

func (h *Handler) beginRuntimeRequest(ctx context.Context, requestID, exportID string, mutating bool) error {
	now := h.now()
	_, err := h.store.BeginExportRuntimeRequest(ctx, exportaccess.RuntimeRequestBegin{
		RequestID:          requestID,
		ExportID:           exportID,
		StartedAt:          now,
		HeartbeatExpiresAt: now.Add(h.heartbeatTTL),
		Write:              mutating,
	})
	return err
}

func runtimeObservationAdmissionDenied(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}

func (h *Handler) endRuntimeRequest(ctx context.Context, requestID, exportID string, status int) {
	var accessedAt *time.Time
	if status < 400 {
		now := h.now()
		accessedAt = &now
	}
	_, _ = h.store.EndExportRuntimeRequest(ctx, exportaccess.RuntimeRequestEnd{
		RequestID:                   requestID,
		ExportID:                    exportID,
		EndedAt:                     h.now(),
		SuccessfulRequestAccessedAt: accessedAt,
	})
}

func (h *Handler) startHeartbeat(ctx context.Context, requestID, exportID string) func() {
	interval := h.heartbeatTTL / 2
	if interval <= 0 {
		interval = h.heartbeatTTL
	}
	stop := make(chan struct{})
	go func() {
		timer := time.NewTimer(interval)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-timer.C:
				now := h.now()
				_, _ = h.store.HeartbeatExportRuntimeRequest(ctx, exportaccess.RuntimeRequestHeartbeat{
					RequestID:          requestID,
					ExportID:           exportID,
					ObservedAt:         now,
					HeartbeatExpiresAt: now.Add(h.heartbeatTTL),
				})
				timer.Reset(interval)
			}
		}
	}()
	return func() {
		close(stop)
	}
}

func (h *Handler) parseSourcePath(rawPath string) (gatewayPath, bool) {
	return parseGatewayPath(rawPath, h.prefix, "")
}

func (h *Handler) parseDestination(r *http.Request, exportID string) (gatewayPath, string, bool) {
	raw := strings.TrimSpace(r.Header.Get("Destination"))
	if raw == "" {
		return gatewayPath{}, denyClassDestinationMissing, false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return gatewayPath{}, denyClassDestinationInvalid, false
	}
	if !sameHost(parsed.Host, r.Host) {
		return gatewayPath{}, denyClassDestinationHostMismatch, false
	}
	if destExportID, ok := exportIDFromRawPath(parsed.EscapedPath(), h.prefix); ok && destExportID != exportID {
		return gatewayPath{}, denyClassDestinationExportMismatch, false
	}
	dest, ok := parseGatewayPath(parsed.EscapedPath(), h.prefix, exportID)
	if !ok {
		return gatewayPath{}, classifyRawChildDenyClass(parsed.EscapedPath(), h.prefix, exportID, "destination"), false
	}
	return dest, "", true
}

func parseGatewayPath(rawPath, prefix, requiredExportID string) (gatewayPath, bool) {
	if !strings.HasPrefix(rawPath, prefix) {
		return gatewayPath{}, false
	}
	rest := strings.TrimPrefix(rawPath, prefix)
	if rest == "" {
		return gatewayPath{}, false
	}
	exportID, child, hasSlash := strings.Cut(rest, "/")
	if !hasSlash {
		return gatewayPath{}, false
	}
	if err := pathresolver.ValidateID(pathresolver.ExportID, exportID); err != nil {
		return gatewayPath{}, false
	}
	if requiredExportID != "" && exportID != requiredExportID {
		return gatewayPath{}, false
	}
	if child == "" {
		return gatewayPath{ExportID: exportID, Root: true}, true
	}
	if strings.HasSuffix(child, "/") {
		child = strings.TrimSuffix(child, "/")
		if child == "" || strings.HasSuffix(child, "/") {
			return gatewayPath{}, false
		}
	}
	plan, err := pathresolver.ResolvePayloadTraversal("afscp/namespaces/ns_probe/repos/repo_probe/payload", child)
	if err != nil {
		return gatewayPath{}, false
	}
	return gatewayPath{
		ExportID: exportID,
		RawChild: child,
		Segments: plan.Segments,
	}, true
}

func (h *Handler) payloadRoot(credential exportaccess.GatewayCredential) (string, error) {
	root, ok := h.volumeRoots[credential.VolumeID]
	if !ok {
		return "", errors.New("unknown volume")
	}
	if err := validateVolumeRoot(root); err != nil {
		return "", err
	}
	if _, err := pathresolver.ResolvePayloadTraversal(credential.PayloadVolumeSubdir, "__afscp_probe__"); err != nil {
		return "", err
	}
	payloadRoot := filepath.Join(root, filepath.FromSlash(credential.PayloadVolumeSubdir))
	clean := filepath.Clean(payloadRoot)
	if clean != payloadRoot || !pathWithin(root, clean) {
		return "", pathresolver.ErrPathEscape
	}
	return clean, nil
}

func (h *Handler) validatePath(root, payloadSubdir string, parsed gatewayPath) error {
	if parsed.Root {
		return nil
	}
	plan, err := pathresolver.ResolvePayloadTraversal(payloadSubdir, parsed.RawChild)
	if err != nil {
		return err
	}
	return pathresolver.ValidateTraversalPlan(plan, lstatInspector{root: root})
}

type lstatInspector struct {
	root string
}

func (inspector lstatInspector) InspectTraversalEntry(segments []string) (pathresolver.TraversalEntry, error) {
	localPath := filepath.Join(append([]string{inspector.root}, segments...)...)
	if !pathWithin(inspector.root, localPath) {
		return pathresolver.TraversalEntry{}, pathresolver.ErrPathEscape
	}
	info, err := os.Lstat(localPath)
	if errors.Is(err, os.ErrNotExist) {
		return pathresolver.TraversalEntry{Exists: false}, nil
	}
	if err != nil {
		return pathresolver.TraversalEntry{}, err
	}
	entry := pathresolver.TraversalEntry{Exists: true, LinkCount: linkCount(info)}
	if info.Mode()&os.ModeSymlink != 0 {
		entry.Symlink = true
		return entry, nil
	}
	switch {
	case info.IsDir():
		entry.Type = pathresolver.EntryDirectory
	case info.Mode().IsRegular():
		entry.Type = pathresolver.EntryFile
	default:
		entry.Type = pathresolver.EntryOther
	}
	return entry, nil
}

func credentialUsable(credential exportaccess.GatewayCredential, now time.Time) bool {
	if credential.Session.Status != sessionstate.ExportStatusActive {
		return false
	}
	if credential.Session.ExpiresAt.IsZero() || !credential.Session.ExpiresAt.After(now) {
		return false
	}
	return credential.Session.Protocol == exportaccess.ProtocolWebDAV
}

func methodAllowed(method string, mode sessionstate.AccessMode) bool {
	switch method {
	case http.MethodOptions, http.MethodHead, http.MethodGet, "PROPFIND":
		return true
	case http.MethodPut, http.MethodDelete, "MKCOL", "MOVE", "COPY", "PROPPATCH", "LOCK", "UNLOCK":
		return mode == sessionstate.AccessModeReadWrite
	default:
		return false
	}
}

func methodMutates(method string) bool {
	switch method {
	case http.MethodPut, http.MethodDelete, "MKCOL", "MOVE", "COPY", "PROPPATCH", "LOCK", "UNLOCK":
		return true
	default:
		return false
	}
}

func methodNeedsDestination(method string) bool {
	return method == "MOVE" || method == "COPY"
}

func cloneForBackend(r *http.Request, source gatewayPath, dest gatewayPath) *http.Request {
	clone := r.Clone(r.Context())
	clone.URL.Path = backendPath(source)
	clone.URL.RawPath = ""
	clone.RequestURI = ""
	if methodNeedsDestination(r.Method) {
		clone.Header = clone.Header.Clone()
		clone.Header.Set("Destination", backendDestination(r, dest))
	}
	return clone
}

func backendDestination(r *http.Request, dest gatewayPath) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return (&url.URL{
		Scheme: scheme,
		Host:   r.Host,
		Path:   backendPath(dest),
	}).String()
}

func backendPath(parsed gatewayPath) string {
	if parsed.Root {
		return "/"
	}
	return "/" + path.Join(parsed.Segments...)
}

func rawRequestPath(r *http.Request) string {
	if r.RequestURI != "" && strings.HasPrefix(r.RequestURI, "/") {
		raw, _, _ := strings.Cut(r.RequestURI, "?")
		return raw
	}
	return r.URL.EscapedPath()
}

func validateVolumeRoot(root string) error {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root || root == string(filepath.Separator) {
		return pathresolver.ErrInvalidPath
	}
	return nil
}

func pathWithin(root, child string) bool {
	rel, err := filepath.Rel(root, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func validPrefix(prefix string) bool {
	return strings.HasPrefix(prefix, "/") && strings.HasSuffix(prefix, "/") &&
		!strings.Contains(prefix, "%") && !strings.Contains(prefix, "\\") &&
		!strings.Contains(prefix, "//") && prefix != "/"
}

func sameHost(left, right string) bool {
	return strings.EqualFold(left, right)
}

func exportIDFromRawPath(rawPath, prefix string) (string, bool) {
	if !strings.HasPrefix(rawPath, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(rawPath, prefix)
	exportID, _, ok := strings.Cut(rest, "/")
	if !ok {
		return "", false
	}
	if err := pathresolver.ValidateID(pathresolver.ExportID, exportID); err != nil {
		return "", false
	}
	return exportID, true
}

func classifyRawChildDenyClass(rawPath, prefix, exportID, scope string) string {
	child, ok := rawChildFromGatewayPath(rawPath, prefix, exportID)
	if !ok {
		return scopedDenyClass(scope, "invalid")
	}
	switch {
	case containsJVSChild(child):
		return scopedDenyClass(scope, "jvs_denied")
	case containsTraversalChild(child):
		return scopedDenyClass(scope, "traversal_denied")
	default:
		return scopedDenyClass(scope, "invalid")
	}
}

func rawChildFromGatewayPath(rawPath, prefix, exportID string) (string, bool) {
	if !strings.HasPrefix(rawPath, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(rawPath, prefix)
	gotExportID, child, ok := strings.Cut(rest, "/")
	if !ok || gotExportID != exportID {
		return "", false
	}
	return child, true
}

func classifyPolicyDenyClass(err error, scope string) string {
	if err != nil && strings.Contains(err.Error(), "symlink component") {
		return scopedDenyClass(scope, "symlink_denied")
	}
	return scopedDenyClass(scope, "policy_denied")
}

func scopedDenyClass(scope, class string) string {
	switch scope {
	case "source":
		switch class {
		case "jvs_denied":
			return denyClassSourceJVSDenied
		case "traversal_denied":
			return denyClassSourceTraversalDenied
		case "symlink_denied":
			return denyClassSourceSymlinkDenied
		case "policy_denied":
			return denyClassSourcePolicyDenied
		default:
			return denyClassSourceInvalid
		}
	case "destination":
		switch class {
		case "jvs_denied":
			return denyClassDestinationJVSDenied
		case "traversal_denied":
			return denyClassDestinationTraversalDenied
		case "symlink_denied":
			return denyClassDestinationSymlinkDenied
		case "policy_denied":
			return denyClassDestinationPolicyDenied
		default:
			return denyClassDestinationInvalid
		}
	default:
		return class
	}
}

func containsJVSChild(child string) bool {
	for _, candidate := range decodedPathCandidates(child) {
		for _, segment := range strings.Split(candidate, "/") {
			if strings.EqualFold(segment, ".jvs") {
				return true
			}
		}
	}
	return false
}

func containsTraversalChild(child string) bool {
	for _, candidate := range decodedPathCandidates(child) {
		for _, segment := range strings.Split(candidate, "/") {
			if segment == ".." {
				return true
			}
		}
	}
	return false
}

func decodedPathCandidates(value string) []string {
	candidates := []string{value}
	current := value
	for range 2 {
		decoded, err := url.PathUnescape(current)
		if err != nil || decoded == current {
			break
		}
		candidates = append(candidates, decoded)
		current = decoded
	}
	return candidates
}

func requestMethod(r *http.Request) string {
	if r == nil {
		return ""
	}
	return r.Method
}

func requestContext(r *http.Request) context.Context {
	if r == nil || r.Context() == nil {
		return context.Background()
	}
	return r.Context()
}

func correlationIDFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	return strings.TrimSpace(r.Header.Get(auth.HeaderCorrelationID))
}

func boolPtr(value bool) *bool {
	return &value
}

func exportIDFingerprint(exportID string) string {
	exportID = strings.TrimSpace(exportID)
	if exportID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(exportID))
	return "sha256:" + hex.EncodeToString(sum[:])[:16]
}

func newAuditEventID() string {
	counter := atomic.AddUint64(&auditEventCounter, 1)
	return fmt.Sprintf("evt_exportgateway_%d_%d", time.Now().UTC().UnixNano(), counter)
}

func newRuntimeRequestID() (string, error) {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "errq_" + hex.EncodeToString(b[:]), nil
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (rec *statusRecorder) WriteHeader(status int) {
	rec.status = status
	rec.ResponseWriter.WriteHeader(status)
}
