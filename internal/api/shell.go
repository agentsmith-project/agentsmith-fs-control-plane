package api

import (
	"log/slog"
	"net/http"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/observability"
)

func NewNeutralShell() http.Handler {
	return NewNeutralShellWithLoggerAndAuditSink(nil, nil)
}

func NewNeutralShellWithLogger(logger *slog.Logger) http.Handler {
	return NewNeutralShellWithLoggerAndAuditSink(logger, nil)
}

func NewNeutralShellWithAuditSink(sink audit.Sink) http.Handler {
	return NewNeutralShellWithLoggerAndAuditSink(nil, sink)
}

func NewNeutralShellWithLoggerAndAuditSink(logger *slog.Logger, sink audit.Sink) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/healthz", requestLogHandler(HealthHandler(), logger, slog.LevelInfo, "afscp.health", "health request", "/healthz", ""))
	mux.Handle("/readyz", requestLogHandler(ReadinessHandler(NeutralReadiness()), logger, slog.LevelInfo, "afscp.readiness", "readiness request", "/readyz", ""))
	mux.Handle("/", neutralFallbackHandler(logger, sink))
	return mux
}

func CapabilityDeniedHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		envelope := NewErrorEnvelope(
			CodeCapabilityDenied,
			"storage-backed API capabilities are disabled in neutral shell",
			false,
			CorrelationIDFromRequest(r),
			nil,
			map[string]any{
				"disabled_capabilities": []string{
					CapabilityStorage,
					CapabilityJVS,
					CapabilityWebDAVExport,
					CapabilityWorkloadMount,
				},
			},
		)

		_ = WriteErrorEnvelope(w, http.StatusForbidden, envelope)
	})
}

func PathDeniedHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		envelope := NewErrorEnvelope(
			CodePathDenied,
			"route is not available",
			false,
			CorrelationIDFromRequest(r),
			nil,
			map[string]any{"route": "unmatched"},
		)

		_ = WriteErrorEnvelope(w, http.StatusNotFound, envelope)
	})
}

func neutralFallbackHandler(logger *slog.Logger, sink audit.Sink) http.Handler {
	capabilityDenied := CapabilityDeniedHandler()
	pathDenied := PathDeniedHandler()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if metadata, ok := RouteMetadataForRequest(r); ok {
			serveDeniedWithRequestLogAndAudit(
				w,
				r,
				capabilityDenied,
				logger,
				sink,
				slog.LevelWarn,
				"afscp.request.capability_denied",
				"capability denied",
				metadata.Path,
				metadata.OperationID,
				audit.EventTypeCapabilityDenied,
				CodeCapabilityDenied,
				map[string]any{
					"disabled_capabilities": []string{
						CapabilityStorage,
						CapabilityJVS,
						CapabilityWebDAVExport,
						CapabilityWorkloadMount,
					},
				},
			)
			return
		}

		serveDeniedWithRequestLogAndAudit(
			w,
			r,
			pathDenied,
			logger,
			sink,
			slog.LevelWarn,
			"afscp.request.path_denied",
			"path denied",
			"unmatched",
			"",
			audit.EventTypePathDenied,
			CodePathDenied,
			nil,
		)
	})
}

func requestLogHandler(next http.Handler, logger *slog.Logger, level slog.Level, event string, message string, route string, operationID string) http.Handler {
	if logger == nil {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveWithRequestLog(w, r, next, logger, level, event, message, route, operationID)
	})
}

func serveWithRequestLog(w http.ResponseWriter, r *http.Request, next http.Handler, logger *slog.Logger, level slog.Level, event string, message string, route string, operationID string) {
	if logger == nil {
		next.ServeHTTP(w, r)
		return
	}

	recorder := &responseStatusRecorder{ResponseWriter: w}
	next.ServeHTTP(recorder, r)

	fields := requestLogFields(r, route, operationID, recorder.statusCode())
	observability.LogEvent(r.Context(), logger, level, event, message, fields)
}

func serveDeniedWithRequestLogAndAudit(
	w http.ResponseWriter,
	r *http.Request,
	next http.Handler,
	logger *slog.Logger,
	sink audit.Sink,
	level slog.Level,
	logEvent string,
	message string,
	route string,
	operationID string,
	auditType audit.EventType,
	code ErrorCode,
	auditDetails map[string]any,
) {
	metadata := RouteMetadata{Path: route, OperationID: operationID}
	recorder := &responseStatusRecorder{ResponseWriter: w}
	next.ServeHTTP(recorder, r)

	status := recorder.statusCode()
	if logger != nil {
		fields := requestLogFields(r, route, operationID, status)
		observability.LogEvent(r.Context(), logger, level, logEvent, message, fields)
	}

	emitDeniedAuditEvent(r.Context(), sink, r, deniedAuditEvent{
		Type:    auditType,
		Route:   metadata,
		Status:  status,
		Code:    code,
		Reason:  message,
		Details: auditDetails,
	})
}

func requestLogFields(r *http.Request, route string, operationID string, status int) map[string]any {
	fields := map[string]any{
		"correlation_id": CorrelationIDFromRequest(r),
		"method":         "",
		"path":           "",
		"route":          route,
		"status":         status,
	}

	if r != nil {
		fields["method"] = r.Method
		if r.URL != nil {
			fields["path"] = r.URL.Path
		}
	}
	if operationID != "" {
		fields["operation_id"] = operationID
	}
	return fields
}

type responseStatusRecorder struct {
	http.ResponseWriter
	status int
}

func (recorder *responseStatusRecorder) WriteHeader(status int) {
	if recorder.status != 0 {
		return
	}
	recorder.status = status
	recorder.ResponseWriter.WriteHeader(status)
}

func (recorder *responseStatusRecorder) Write(body []byte) (int, error) {
	if recorder.status == 0 {
		recorder.status = http.StatusOK
	}
	return recorder.ResponseWriter.Write(body)
}

func (recorder *responseStatusRecorder) statusCode() int {
	if recorder.status == 0 {
		return http.StatusOK
	}
	return recorder.status
}
