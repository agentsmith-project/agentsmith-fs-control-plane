package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/observability"
)

func TestNeutralShellRoutesHealthAndReadiness(t *testing.T) {
	handler := NewNeutralShell()

	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("expected health status %d, got %d", http.StatusOK, health.Code)
	}

	readiness := httptest.NewRecorder()
	handler.ServeHTTP(readiness, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if readiness.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected readiness status %d, got %d", http.StatusServiceUnavailable, readiness.Code)
	}
}

func TestNeutralShellDeniesKnownInternalRoutesWithoutReadingRequestBody(t *testing.T) {
	handler := NewNeutralShell()

	for _, route := range InternalV1RouteMetadata() {
		t.Run(route.OperationID, func(t *testing.T) {
			path := concretePathForRoute(t, route.Path)
			if metadata, ok := RouteMetadataForRequest(httptest.NewRequest(route.Method, path, nil)); !ok {
				t.Fatalf("test route is not registered: %s %s", route.Method, path)
			} else if metadata.OperationID != route.OperationID {
				t.Fatalf("test route operation ID = %q, want %q", metadata.OperationID, route.OperationID)
			}

			body := &trackingReadCloser{payload: []byte("body-secret")}
			req := httptest.NewRequest(route.Method, path, body)
			req.Header.Set(HeaderCorrelationID, "corr_shell")
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if body.reads != 0 {
				t.Fatalf("neutral shell read request body %d time(s)", body.reads)
			}
			if rec.Code != http.StatusForbidden {
				t.Fatalf("expected status %d, got %d: %s", http.StatusForbidden, rec.Code, rec.Body.String())
			}

			var envelope ErrorEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("error envelope did not decode: %v", err)
			}
			if envelope.Error.Code != CodeCapabilityDenied {
				t.Fatalf("expected %s, got %s", CodeCapabilityDenied, envelope.Error.Code)
			}
			if envelope.Error.CorrelationID != "corr_shell" {
				t.Fatalf("expected correlation id corr_shell, got %q", envelope.Error.CorrelationID)
			}
			if envelope.Error.OperationID != nil {
				t.Fatalf("storage shell must not invent operation ids, got %q", *envelope.Error.OperationID)
			}

			disabled, ok := envelope.Error.Details["disabled_capabilities"].([]any)
			if !ok {
				t.Fatalf("missing disabled_capabilities detail: %#v", envelope.Error.Details)
			}
			if len(disabled) != 4 {
				t.Fatalf("expected 4 disabled capabilities, got %#v", disabled)
			}
		})
	}
}

func TestNeutralShellUnknownRoutesReturnStablePathDeniedEnvelope(t *testing.T) {
	handler := NewNeutralShell()

	for _, tc := range []struct {
		name   string
		method string
		path   string
	}{
		{name: "external route", method: http.MethodGet, path: "/not-a-route"},
		{name: "internal root", method: http.MethodPost, path: "/internal/v1"},
		{name: "unknown internal path", method: http.MethodGet, path: "/internal/v1/not-a-route"},
		{name: "known path wrong method", method: http.MethodDelete, path: "/internal/v1/repos"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &trackingReadCloser{payload: []byte("body-secret")}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, body)
			req.Header.Set(HeaderCorrelationID, "corr_unknown")

			handler.ServeHTTP(rec, req)

			if body.reads != 0 {
				t.Fatalf("neutral shell read request body %d time(s)", body.reads)
			}
			if rec.Code != http.StatusNotFound {
				t.Fatalf("expected status %d, got %d: %s", http.StatusNotFound, rec.Code, rec.Body.String())
			}

			var envelope ErrorEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("error envelope did not decode: %v", err)
			}
			if envelope.Error.Code != CodePathDenied {
				t.Fatalf("expected %s, got %s", CodePathDenied, envelope.Error.Code)
			}
			if envelope.Error.CorrelationID != "corr_unknown" {
				t.Fatalf("expected correlation id corr_unknown, got %q", envelope.Error.CorrelationID)
			}
			if envelope.Error.OperationID != nil {
				t.Fatalf("path denied shell must not invent operation ids, got %q", *envelope.Error.OperationID)
			}
		})
	}
}

func TestNeutralShellWithLoggerEmitsStructuredDeniedRequestLogs(t *testing.T) {
	for _, tc := range []struct {
		name      string
		method    string
		path      string
		wantCode  int
		wantEvent string
		wantRoute string
		wantOpID  string
	}{
		{
			name:      "capability denied",
			method:    http.MethodPost,
			path:      "/internal/v1/repos/repo_123:archive?token=query-token",
			wantCode:  http.StatusForbidden,
			wantEvent: "afscp.request.capability_denied",
			wantRoute: "/internal/v1/repos/{repoId}:archive",
			wantOpID:  "archiveRepo",
		},
		{
			name:      "external path denied",
			method:    http.MethodGet,
			path:      "/not-a-route?token=query-token",
			wantCode:  http.StatusNotFound,
			wantEvent: "afscp.request.path_denied",
			wantRoute: "unmatched",
		},
		{
			name:      "internal path denied",
			method:    http.MethodDelete,
			path:      "/internal/v1/repos?token=query-token",
			wantCode:  http.StatusNotFound,
			wantEvent: "afscp.request.path_denied",
			wantRoute: "unmatched",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			handler := NewNeutralShellWithLogger(observability.NewJSONLogger(&logs, nil))
			body := &trackingReadCloser{payload: []byte("body-secret")}
			req := httptest.NewRequest(tc.method, tc.path, body)
			req.Header.Set(HeaderCorrelationID, "corr_log")
			req.Header.Set("Authorization", "Bearer request-authorization-token")
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if body.reads != 0 {
				t.Fatalf("neutral shell read request body %d time(s)", body.reads)
			}
			if rec.Code != tc.wantCode {
				t.Fatalf("expected status %d, got %d: %s", tc.wantCode, rec.Code, rec.Body.String())
			}

			entry := decodeSingleStructuredLogEntry(t, logs.Bytes())
			if got, want := entry["event"], tc.wantEvent; got != want {
				t.Fatalf("event = %#v, want %#v in %#v", got, want, entry)
			}
			if got, want := entry["level"], slog.LevelWarn.String(); got != want {
				t.Fatalf("level = %#v, want %#v", got, want)
			}
			if got, want := entry["correlation_id"], "corr_log"; got != want {
				t.Fatalf("correlation_id = %#v, want %#v", got, want)
			}
			if got, want := entry["method"], tc.method; got != want {
				t.Fatalf("method = %#v, want %#v", got, want)
			}
			if got, want := entry["path"], strings.Split(tc.path, "?")[0]; got != want {
				t.Fatalf("path = %#v, want %#v", got, want)
			}
			if got, want := entry["route"], tc.wantRoute; got != want {
				t.Fatalf("route = %#v, want %#v", got, want)
			}
			if tc.wantOpID == "" {
				if got, ok := entry["operation_id"]; ok {
					t.Fatalf("operation_id = %#v, want absent", got)
				}
			} else if got, want := entry["operation_id"], tc.wantOpID; got != want {
				t.Fatalf("operation_id = %#v, want %#v", got, want)
			}

			rendered := logs.String()
			for _, leaked := range []string{"request-authorization-token", "query-token"} {
				if strings.Contains(rendered, leaked) {
					t.Fatalf("denied request log leaked %q in %s", leaked, rendered)
				}
			}
		})
	}
}

func TestNeutralShellWithAuditSinkEmitsDeniedEventsWithoutSensitiveRequestData(t *testing.T) {
	for _, tc := range []struct {
		name         string
		method       string
		path         string
		wantStatus   int
		wantType     audit.EventType
		wantResource string
	}{
		{
			name:         "capability denied",
			method:       http.MethodPost,
			path:         "/internal/v1/repos/repo_123:archive?token=query-token",
			wantStatus:   http.StatusForbidden,
			wantType:     audit.EventTypeCapabilityDenied,
			wantResource: "/internal/v1/repos/{repoId}:archive",
		},
		{
			name:         "path denied",
			method:       http.MethodGet,
			path:         "/not-a-route?token=query-token",
			wantStatus:   http.StatusNotFound,
			wantType:     audit.EventTypePathDenied,
			wantResource: "unmatched",
		},
		{
			name:         "raw direct mount canary path denied",
			method:       http.MethodPost,
			path:         "/internal/v1/repos/repo_123/raw-mount-command?token=query-secret",
			wantStatus:   http.StatusNotFound,
			wantType:     audit.EventTypePathDenied,
			wantResource: "unmatched",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := &fakeAuditSink{}
			handler := NewNeutralShellWithAuditSink(sink)
			body := &trackingReadCloser{payload: []byte("body-secret")}
			req := httptest.NewRequest(tc.method, tc.path, body)
			req.Header.Set(HeaderCorrelationID, "corr_audit")
			req.Header.Set("Authorization", "Bearer request-authorization-token")

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if body.reads != 0 {
				t.Fatalf("neutral shell read request body %d time(s)", body.reads)
			}
			if rec.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", tc.wantStatus, rec.Code, rec.Body.String())
			}
			var envelope ErrorEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("error envelope did not decode: %v", err)
			}
			if tc.wantType == audit.EventTypePathDenied && envelope.Error.Code != CodePathDenied {
				t.Fatalf("error code = %s, want %s", envelope.Error.Code, CodePathDenied)
			}
			if len(sink.events) != 1 {
				t.Fatalf("expected one audit event, got %d", len(sink.events))
			}

			event := sink.events[0]
			if event.Type != tc.wantType {
				t.Fatalf("event Type = %q, want %q", event.Type, tc.wantType)
			}
			if event.Outcome != audit.OutcomeDenied {
				t.Fatalf("event Outcome = %q, want %q", event.Outcome, audit.OutcomeDenied)
			}
			if event.CorrelationID != "corr_audit" {
				t.Fatalf("event CorrelationID = %q, want corr_audit", event.CorrelationID)
			}
			if event.OperationID != "" {
				t.Fatalf("denied audit event OperationID = %q, want empty", event.OperationID)
			}
			if event.Resource.Type != "route" {
				t.Fatalf("event Resource.Type = %q, want route", event.Resource.Type)
			}
			if event.Resource.ID != tc.wantResource {
				t.Fatalf("event Resource.ID = %q, want %q", event.Resource.ID, tc.wantResource)
			}
			if got, want := event.Details["method"], tc.method; got != want {
				t.Fatalf("event Details[method] = %#v, want %#v", got, want)
			}
			if got, want := event.Details["path"], strings.Split(tc.path, "?")[0]; got != want {
				t.Fatalf("event Details[path] = %#v, want %#v", got, want)
			}

			responseBody := rec.Body.String()
			rendered := auditEventString(t, event)
			for _, leaked := range []string{"request-authorization-token", "query-token", "query-secret", "body-secret"} {
				if strings.Contains(responseBody, leaked) {
					t.Fatalf("denied response leaked %q in %s", leaked, responseBody)
				}
				if strings.Contains(rendered, leaked) {
					t.Fatalf("denied audit event leaked %q in %s", leaked, rendered)
				}
			}
		})
	}
}

func TestNeutralShellAuditSinkFailurePreservesDeniedResponse(t *testing.T) {
	sink := &fakeAuditSink{err: errors.New("audit sink unavailable")}
	handler := NewNeutralShellWithAuditSink(sink)
	req := httptest.NewRequest(http.MethodGet, "/not-a-route?token=query-token", &trackingReadCloser{payload: []byte("body-secret")})
	req.Header.Set(HeaderCorrelationID, "corr_audit_fail")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d: %s", http.StatusNotFound, rec.Code, rec.Body.String())
	}
	var envelope ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("error envelope did not decode: %v", err)
	}
	if envelope.Error.Code != CodePathDenied {
		t.Fatalf("expected %s, got %s", CodePathDenied, envelope.Error.Code)
	}
	if len(sink.events) != 1 {
		t.Fatalf("expected one attempted audit event, got %d", len(sink.events))
	}
}

func TestNeutralShellWithLoggerEmitsStructuredHealthLog(t *testing.T) {
	var logs bytes.Buffer
	handler := NewNeutralShellWithLogger(observability.NewJSONLogger(&logs, nil))
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set(HeaderCorrelationID, "corr_health")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}
	entry := decodeSingleStructuredLogEntry(t, logs.Bytes())
	if got, want := entry["event"], "afscp.health"; got != want {
		t.Fatalf("event = %#v, want %#v", got, want)
	}
	if got, want := entry["correlation_id"], "corr_health"; got != want {
		t.Fatalf("correlation_id = %#v, want %#v", got, want)
	}
	if got, want := entry["route"], "/healthz"; got != want {
		t.Fatalf("route = %#v, want %#v", got, want)
	}
	if got, want := entry["path"], "/healthz"; got != want {
		t.Fatalf("path = %#v, want %#v", got, want)
	}
}

type trackingReadCloser struct {
	reads   int
	payload []byte
	sent    bool
}

func (b *trackingReadCloser) Read(p []byte) (int, error) {
	b.reads++
	if len(b.payload) > 0 && !b.sent {
		b.sent = true
		return copy(p, b.payload), nil
	}
	return 0, io.EOF
}

func (b *trackingReadCloser) Close() error {
	return nil
}

func concretePathForRoute(t *testing.T, routePath string) string {
	t.Helper()

	replacements := map[string]string{
		"{volumeId}":       "vol_123",
		"{namespaceId}":    "ns_123",
		"{repoId}":         "repo_123",
		"{templateId}":     "template_123",
		"{exportId}":       "export_123",
		"{mountBindingId}": "wmb_123",
		"{operationId}":    "op_123",
	}
	path := routePath
	for marker, value := range replacements {
		path = strings.ReplaceAll(path, marker, value)
	}
	if strings.Contains(path, "{") || strings.Contains(path, "}") {
		t.Fatalf("route path still contains unresolved template marker: %s", routePath)
	}
	return path
}

func decodeSingleStructuredLogEntry(t *testing.T, body []byte) map[string]any {
	t.Helper()

	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		t.Fatal("expected one structured log entry, got empty output")
	}
	if bytes.Count(trimmed, []byte("\n")) != 0 {
		t.Fatalf("expected one structured log entry, got %q", string(body))
	}

	var entry map[string]any
	if err := json.Unmarshal(trimmed, &entry); err != nil {
		t.Fatalf("structured log did not decode as JSON: %v: %s", err, string(body))
	}
	return entry
}

type fakeAuditSink struct {
	events []audit.Event
	err    error
}

func (sink *fakeAuditSink) Emit(_ context.Context, event audit.Event) error {
	sink.events = append(sink.events, event)
	return sink.err
}

func auditEventString(t *testing.T, event audit.Event) string {
	t.Helper()

	body, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal audit event: %v", err)
	}
	return string(body)
}
