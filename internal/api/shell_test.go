package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
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

func TestNeutralShellDeniesStoragePathsWithoutReadingRequestBody(t *testing.T) {
	handler := NewNeutralShell()

	for _, tc := range []struct {
		name   string
		method string
		path   string
	}{
		{name: "repo create", method: http.MethodPost, path: "/internal/v1/repos"},
		{name: "internal v1 root", method: http.MethodPost, path: "/internal/v1"},
		{name: "repo save point", method: http.MethodPost, path: "/internal/v1/repos/repo_123/save-points"},
		{name: "webdav export", method: http.MethodPost, path: "/internal/v1/repos/repo_123/exports"},
		{name: "workload mount", method: http.MethodPost, path: "/internal/v1/repos/repo_123/workload-mount-bindings"},
		{name: "orchestrator mount plan", method: http.MethodGet, path: "/internal/v1/workload-mount-bindings/wmb_123/orchestrator-plan"},
		{name: "volume health", method: http.MethodGet, path: "/internal/v1/volumes/vol_123/health"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &trackingReadCloser{}
			req := httptest.NewRequest(tc.method, tc.path, body)
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

func TestNeutralShellUnknownRouteReturnsStableEnvelope(t *testing.T) {
	handler := NewNeutralShell()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/not-a-route", nil)
	req.Header.Set(HeaderCorrelationID, "corr_unknown")

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, rec.Code)
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
}

type trackingReadCloser struct {
	reads int
}

func (b *trackingReadCloser) Read(_ []byte) (int, error) {
	b.reads++
	return 0, io.EOF
}

func (b *trackingReadCloser) Close() error {
	return nil
}
