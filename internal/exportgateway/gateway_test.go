package exportgateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/exportaccess"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
)

const (
	testExportID  = "export_test123"
	testVolumeID  = "vol_test123"
	testNamespace = "ns_test123"
	testRepo      = "repo_test123"
	testPassword  = "correct horse battery staple"

	testHeartbeatTTL = 17 * time.Second
)

func TestBasicAuthFailureDoesNotLeakCredentialOrPaths(t *testing.T) {
	env := newGatewayTestEnv(t, sessionstate.AccessModeReadOnly, sessionstate.ExportStatusActive)
	env.writePayload(t, "hello.txt", "hello")

	tests := []struct {
		name string
		user string
		pass string
		want int
	}{
		{name: "missing", want: http.StatusUnauthorized},
		{name: "wrong password", user: testExportID, pass: "wrong-secret", want: http.StatusForbidden},
		{name: "username mismatch", user: "export_other123", pass: testPassword, want: http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://files.example.test/e/"+testExportID+"/hello.txt", nil)
			if tt.user != "" {
				req.SetBasicAuth(tt.user, tt.pass)
			}
			rec := httptest.NewRecorder()

			env.handler.ServeHTTP(rec, req)

			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d, body %q", rec.Code, tt.want, rec.Body.String())
			}
			rendered := rec.Body.String() + rec.Header().Get("WWW-Authenticate")
			for _, forbidden := range []string{testPassword, tt.pass, env.payloadRoot, env.volumeRoot} {
				if forbidden != "" && strings.Contains(rendered, forbidden) {
					t.Fatalf("response leaked %q: %q", forbidden, rendered)
				}
			}
			if len(env.store.observations) != 0 {
				t.Fatalf("runtime observations = %d, want 0", len(env.store.observations))
			}
		})
	}
}

func TestInactiveAndExpiredSessionsDenyClosed(t *testing.T) {
	tests := []struct {
		name    string
		status  sessionstate.ExportStatus
		expires time.Time
	}{
		{name: "revoking", status: sessionstate.ExportStatusRevoking},
		{name: "revoked", status: sessionstate.ExportStatusRevoked},
		{name: "expired status", status: sessionstate.ExportStatusExpired},
		{name: "failed", status: sessionstate.ExportStatusFailed},
		{name: "past expiry", status: sessionstate.ExportStatusActive, expires: fixedGatewayNow().Add(-time.Second)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newGatewayTestEnv(t, sessionstate.AccessModeReadOnly, tt.status)
			if !tt.expires.IsZero() {
				env.store.credential.Session.ExpiresAt = tt.expires
			}
			env.writePayload(t, "hello.txt", "hello")

			rec := env.request(http.MethodGet, "/e/"+testExportID+"/hello.txt", nil, "")

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403, body %q", rec.Code, rec.Body.String())
			}
			if len(env.store.observations) != 0 {
				t.Fatalf("runtime observations = %d, want 0", len(env.store.observations))
			}
		})
	}
}

func TestGatewayStoreFailClosedDeniesDisabledNamespaceCredential(t *testing.T) {
	env := newGatewayTestEnv(t, sessionstate.AccessModeReadOnly, sessionstate.ExportStatusActive)
	env.store.getErr = errors.New("credential rejected by namespace or binding predicate")
	env.writePayload(t, "hello.txt", "hello")

	rec := env.request(http.MethodGet, "/e/"+testExportID+"/hello.txt", nil, "")

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body %q", rec.Code, rec.Body.String())
	}
	if len(env.store.observations) != 0 {
		t.Fatalf("runtime observations = %d, want 0", len(env.store.observations))
	}
}

func TestWebDAVMethodPolicyMatchesContract(t *testing.T) {
	tests := []struct {
		method        string
		readOnlyWant  bool
		readWriteWant bool
	}{
		{method: http.MethodOptions, readOnlyWant: true, readWriteWant: true},
		{method: http.MethodHead, readOnlyWant: true, readWriteWant: true},
		{method: http.MethodGet, readOnlyWant: true, readWriteWant: true},
		{method: "PROPFIND", readOnlyWant: true, readWriteWant: true},
		{method: http.MethodPut, readOnlyWant: false, readWriteWant: true},
		{method: http.MethodDelete, readOnlyWant: false, readWriteWant: true},
		{method: "MKCOL", readOnlyWant: false, readWriteWant: true},
		{method: "MOVE", readOnlyWant: false, readWriteWant: true},
		{method: "COPY", readOnlyWant: false, readWriteWant: true},
		{method: "PROPPATCH", readOnlyWant: false, readWriteWant: true},
		{method: "LOCK", readOnlyWant: false, readWriteWant: true},
		{method: "UNLOCK", readOnlyWant: false, readWriteWant: true},
		{method: "BREW", readOnlyWant: false, readWriteWant: false},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			if got := methodAllowed(tt.method, sessionstate.AccessModeReadOnly); got != tt.readOnlyWant {
				t.Fatalf("read-only methodAllowed(%q) = %v, want %v", tt.method, got, tt.readOnlyWant)
			}
			if got := methodAllowed(tt.method, sessionstate.AccessModeReadWrite); got != tt.readWriteWant {
				t.Fatalf("read-write methodAllowed(%q) = %v, want %v", tt.method, got, tt.readWriteWant)
			}
		})
	}
}

func TestReadOnlyMethodPolicy(t *testing.T) {
	env := newGatewayTestEnv(t, sessionstate.AccessModeReadOnly, sessionstate.ExportStatusActive)
	env.writePayload(t, "hello.txt", "hello")

	for _, method := range []string{http.MethodGet, "PROPFIND"} {
		t.Run(method+" allowed", func(t *testing.T) {
			rec := env.request(method, "/e/"+testExportID+"/hello.txt", nil, "")
			if rec.Code >= 400 {
				t.Fatalf("status = %d, want success, body %q", rec.Code, rec.Body.String())
			}
		})
	}

	for _, method := range []string{http.MethodPut, http.MethodDelete, "MKCOL", "MOVE", "COPY", "PROPPATCH", "LOCK", "UNLOCK"} {
		t.Run(method+" denied", func(t *testing.T) {
			env := newGatewayTestEnv(t, sessionstate.AccessModeReadOnly, sessionstate.ExportStatusActive)
			env.writePayload(t, "hello.txt", "hello")

			body := strings.NewReader("mutate")
			rec := env.request(method, "/e/"+testExportID+"/hello.txt", body, "http://files.example.test/e/"+testExportID+"/copy.txt")
			if rec.Code != http.StatusForbidden && rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want deny, body %q", rec.Code, rec.Body.String())
			}
			requireNoRuntimeObservation(t, env)
		})
	}
}

func TestReadWritePutGetAndCopyMoveDestinationPolicy(t *testing.T) {
	env := newGatewayTestEnv(t, sessionstate.AccessModeReadWrite, sessionstate.ExportStatusActive)
	if err := os.MkdirAll(filepath.Join(env.payloadRoot, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}

	put := env.request(http.MethodPut, "/e/"+testExportID+"/docs/hello.txt", strings.NewReader("hello"), "")
	if put.Code >= 400 {
		t.Fatalf("PUT status = %d, want success, body %q", put.Code, put.Body.String())
	}
	get := env.request(http.MethodGet, "/e/"+testExportID+"/docs/hello.txt", nil, "")
	if get.Code != http.StatusOK || get.Body.String() != "hello" {
		t.Fatalf("GET status/body = %d/%q, want 200/hello", get.Code, get.Body.String())
	}

	copyRec := env.request("COPY", "/e/"+testExportID+"/docs/hello.txt", nil, "http://files.example.test/e/"+testExportID+"/docs/copy.txt")
	if copyRec.Code >= 400 {
		t.Fatalf("COPY status = %d, want success, body %q", copyRec.Code, copyRec.Body.String())
	}
	if got := env.readPayload(t, "docs/copy.txt"); got != "hello" {
		t.Fatalf("copied payload = %q, want hello", got)
	}

	for _, tt := range []struct {
		name string
		dest string
	}{
		{name: "cross export", dest: "http://files.example.test/e/export_other123/docs/bad.txt"},
		{name: "cross host", dest: "http://other.example.test/e/" + testExportID + "/docs/bad.txt"},
		{name: "encoded escape", dest: "http://files.example.test/e/" + testExportID + "/docs/%252e%252e/bad.txt"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := env.request("COPY", "/e/"+testExportID+"/docs/hello.txt", nil, tt.dest)
			if rec.Code < 400 {
				t.Fatalf("status = %d, want deny", rec.Code)
			}
		})
	}
}

func TestPathPolicyRejectsUnsafeSourcePaths(t *testing.T) {
	env := newGatewayTestEnv(t, sessionstate.AccessModeReadWrite, sessionstate.ExportStatusActive)

	for _, rawPath := range []string{
		"/e/" + testExportID + "/.jvs",
		"/e/" + testExportID + "/nested/.jvs/config",
		"/e/" + testExportID + "/..",
		"/e/" + testExportID + "/%2e%2e/escape",
		"/e/" + testExportID + "/%252e%252e/escape",
		"/e/" + testExportID + "/unicode%E2%81%84slash",
	} {
		t.Run(rawPath, func(t *testing.T) {
			rec := env.request(http.MethodPut, rawPath, strings.NewReader("bad"), "")
			if rec.Code < 400 {
				t.Fatalf("status = %d, want deny", rec.Code)
			}
			if len(env.store.observations) != 0 {
				t.Fatalf("runtime observations = %d, want 0", len(env.store.observations))
			}
		})
	}
}

func TestSymlinkComponentRejectedBeforeBackend(t *testing.T) {
	env := newGatewayTestEnv(t, sessionstate.AccessModeReadOnly, sessionstate.ExportStatusActive)
	if err := os.MkdirAll(filepath.Join(env.payloadRoot, "links"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(env.payloadRoot, "links", "out")); err != nil {
		t.Fatal(err)
	}

	rec := env.request(http.MethodGet, "/e/"+testExportID+"/links/out/secret.txt", nil, "")

	if rec.Code < 400 {
		t.Fatalf("status = %d, want deny", rec.Code)
	}
	if len(env.store.observations) != 0 {
		t.Fatalf("runtime observations = %d, want 0", len(env.store.observations))
	}
}

func TestSuccessfulGETRecordsRuntimeObservation(t *testing.T) {
	env := newGatewayTestEnv(t, sessionstate.AccessModeReadOnly, sessionstate.ExportStatusActive)
	env.writePayload(t, "hello.txt", "hello")

	rec := env.request(http.MethodGet, "/e/"+testExportID+"/hello.txt", nil, "")
	if rec.Code != http.StatusOK || rec.Body.String() != "hello" {
		t.Fatalf("GET status/body = %d/%q, want 200/hello", rec.Code, rec.Body.String())
	}
	if env.store.recordCalls != 0 {
		t.Fatalf("legacy RecordExportAccess calls = %d, want 0", env.store.recordCalls)
	}
	requireObservation(t, env.store.observations, 0, observationWant{
		requestDelta: 1,
		writeDelta:   0,
		success:      false,
	})
	requireObservation(t, env.store.observations, 1, observationWant{
		requestDelta: -1,
		writeDelta:   0,
		success:      true,
	})
}

func TestReadWritePUTRecordsActiveWriteObservation(t *testing.T) {
	env := newGatewayTestEnv(t, sessionstate.AccessModeReadWrite, sessionstate.ExportStatusActive)
	if err := os.MkdirAll(filepath.Join(env.payloadRoot, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}

	rec := env.request(http.MethodPut, "/e/"+testExportID+"/docs/hello.txt", strings.NewReader("hello"), "")
	if rec.Code >= 400 {
		t.Fatalf("PUT status = %d, want success, body %q", rec.Code, rec.Body.String())
	}
	requireObservation(t, env.store.observations, 0, observationWant{
		requestDelta: 1,
		writeDelta:   1,
		success:      false,
	})
	requireObservation(t, env.store.observations, 1, observationWant{
		requestDelta: -1,
		writeDelta:   -1,
		success:      true,
	})
}

func TestBackendFailureEndsRuntimeObservationWithoutSuccessfulAccess(t *testing.T) {
	env := newGatewayTestEnv(t, sessionstate.AccessModeReadOnly, sessionstate.ExportStatusActive)

	rec := env.request(http.MethodGet, "/e/"+testExportID+"/missing.txt", nil, "")
	if rec.Code < 400 {
		t.Fatalf("missing status = %d, want backend failure", rec.Code)
	}
	requireObservation(t, env.store.observations, 0, observationWant{
		requestDelta: 1,
		writeDelta:   0,
		success:      false,
	})
	requireObservation(t, env.store.observations, 1, observationWant{
		requestDelta: -1,
		writeDelta:   0,
		success:      false,
	})
}

func TestStartRuntimeObservationFailureFailsClosedBeforeBackend(t *testing.T) {
	env := newGatewayTestEnv(t, sessionstate.AccessModeReadWrite, sessionstate.ExportStatusActive)
	if err := os.MkdirAll(filepath.Join(env.payloadRoot, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	env.store.runtimeErrs = []error{errors.New("runtime observation unavailable")}

	rec := env.request(http.MethodPut, "/e/"+testExportID+"/docs/blocked.txt", strings.NewReader("blocked"), "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body %q", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(env.payloadRoot, "docs", "blocked.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backend file stat err = %v, want not exist", err)
	}
	requireObservation(t, env.store.observations, 0, observationWant{
		requestDelta: 1,
		writeDelta:   1,
		success:      false,
	})

	rec = env.request(http.MethodPut, "/e/"+testExportID+"/docs/allowed.txt", strings.NewReader("allowed"), "")
	if rec.Code >= 400 {
		t.Fatalf("second PUT status = %d, want success, body %q", rec.Code, rec.Body.String())
	}
	requireObservation(t, env.store.observations, 1, observationWant{
		requestDelta: 1,
		writeDelta:   1,
		success:      false,
	})
	requireObservation(t, env.store.observations, 2, observationWant{
		requestDelta: -1,
		writeDelta:   -1,
		success:      true,
	})
}

func TestEndRuntimeObservationFailureDoesNotChangeBackendResponse(t *testing.T) {
	env := newGatewayTestEnv(t, sessionstate.AccessModeReadOnly, sessionstate.ExportStatusActive)
	env.writePayload(t, "hello.txt", "hello")
	env.store.runtimeErrs = []error{nil, errors.New("runtime observation unavailable")}

	rec := env.request(http.MethodGet, "/e/"+testExportID+"/hello.txt", nil, "")
	if rec.Code != http.StatusOK || rec.Body.String() != "hello" {
		t.Fatalf("GET status/body = %d/%q, want 200/hello", rec.Code, rec.Body.String())
	}
	if len(env.store.runtimeCallErrs) != 2 || env.store.runtimeCallErrs[1] == nil {
		t.Fatalf("runtime call errors = %#v, want end error recorded", env.store.runtimeCallErrs)
	}
}

func TestDeniedRequestsSkipRuntimeObservation(t *testing.T) {
	t.Run("auth", func(t *testing.T) {
		env := newGatewayTestEnv(t, sessionstate.AccessModeReadOnly, sessionstate.ExportStatusActive)
		req := httptest.NewRequest(http.MethodGet, "http://files.example.test/e/"+testExportID+"/hello.txt", nil)
		rec := httptest.NewRecorder()

		env.handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
		if len(env.store.observations) != 0 {
			t.Fatalf("runtime observations = %d, want 0", len(env.store.observations))
		}
	})

	t.Run("method", func(t *testing.T) {
		env := newGatewayTestEnv(t, sessionstate.AccessModeReadOnly, sessionstate.ExportStatusActive)
		rec := env.request(http.MethodPut, "/e/"+testExportID+"/hello.txt", strings.NewReader("bad"), "")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
		if len(env.store.observations) != 0 {
			t.Fatalf("runtime observations = %d, want 0", len(env.store.observations))
		}
	})

	t.Run("path", func(t *testing.T) {
		env := newGatewayTestEnv(t, sessionstate.AccessModeReadWrite, sessionstate.ExportStatusActive)
		rec := env.request(http.MethodPut, "/e/"+testExportID+"/.jvs/config", strings.NewReader("bad"), "")
		if rec.Code < 400 {
			t.Fatalf("status = %d, want deny", rec.Code)
		}
		if len(env.store.observations) != 0 {
			t.Fatalf("runtime observations = %d, want 0", len(env.store.observations))
		}
	})
}

func TestDeniedRequestsEmitAuditWithoutRuntimeObservation(t *testing.T) {
	t.Run("wrong password correct username", func(t *testing.T) {
		env := newGatewayTestEnv(t, sessionstate.AccessModeReadOnly, sessionstate.ExportStatusActive)
		req := httptest.NewRequest(http.MethodGet, "http://files.example.test/e/"+testExportID+"/hello.txt", nil)
		req.SetBasicAuth(testExportID, "wrong-secret")
		req.Header.Set(auth.HeaderCorrelationID, "corr_webdav_denied")
		rec := httptest.NewRecorder()

		env.handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
		requireNoRuntimeObservation(t, env)
		event := requireAuditEvent(t, env, 0, audit.EventTypeAuthzDenied, http.StatusForbidden, "authz_denied")
		if event.CorrelationID != "corr_webdav_denied" {
			t.Fatalf("CorrelationID = %q, want corr_webdav_denied", event.CorrelationID)
		}
		if event.Details["deny_class"] != "authz_denied" {
			t.Fatalf("deny_class = %#v, want authz_denied", event.Details["deny_class"])
		}
		if event.Resource.NamespaceID != testNamespace || event.Details["repo_id"] != testRepo {
			t.Fatalf("event namespace/repo = %q/%#v", event.Resource.NamespaceID, event.Details["repo_id"])
		}
		rendered := renderAuditEvent(t, event)
		if strings.Contains(rendered, "wrong-secret") || strings.Contains(rendered, testPassword) {
			t.Fatalf("audit event leaked WebDAV password material: %s", rendered)
		}
	})

	for _, method := range []string{http.MethodPut, "BREW", "LOCK"} {
		t.Run("capability "+method, func(t *testing.T) {
			env := newGatewayTestEnv(t, sessionstate.AccessModeReadOnly, sessionstate.ExportStatusActive)
			req := httptest.NewRequest(method, "http://files.example.test/e/"+testExportID+"/", strings.NewReader("body-secret-lock-material"))
			req.SetBasicAuth(testExportID, testPassword)
			req.Header.Set("X-Secret-Header", "header-secret-lock-material")
			rec := httptest.NewRecorder()

			env.handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", rec.Code)
			}
			requireNoRuntimeObservation(t, env)
			event := requireAuditEvent(t, env, 0, audit.EventTypeCapabilityDenied, http.StatusForbidden, "capability_denied")
			if event.Details["method"] != method || event.Details["export_mode"] != string(sessionstate.AccessModeReadOnly) {
				t.Fatalf("details = %#v", event.Details)
			}
			if event.Details["deny_class"] != "capability_denied" {
				t.Fatalf("deny_class = %#v, want capability_denied", event.Details["deny_class"])
			}
			rendered := renderAuditEvent(t, event)
			for _, forbidden := range []string{
				testPassword,
				"header-secret-lock-material",
				"body-secret-lock-material",
				env.volumeRoot,
				env.payloadRoot,
			} {
				if strings.Contains(rendered, forbidden) {
					t.Fatalf("audit event leaked %q in %s", forbidden, rendered)
				}
			}
		})
	}

	for _, tt := range []struct {
		name   string
		target string
		want   string
		setup  func(*testing.T, *gatewayTestEnv)
	}{
		{name: "source .jvs", target: "/e/" + testExportID + "/.jvs", want: "source_jvs_denied"},
		{name: "source traversal", target: "/e/" + testExportID + "/%252e%252e/escape", want: "source_traversal_denied"},
		{
			name:   "source symlink",
			target: "/e/" + testExportID + "/links/out/secret.txt",
			want:   "source_symlink_denied",
			setup: func(t *testing.T, env *gatewayTestEnv) {
				t.Helper()
				if err := os.MkdirAll(filepath.Join(env.payloadRoot, "links"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), filepath.Join(env.payloadRoot, "links", "out")); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			env := newGatewayTestEnv(t, sessionstate.AccessModeReadWrite, sessionstate.ExportStatusActive)
			if tt.setup != nil {
				tt.setup(t, env)
			}
			rec := env.request(http.MethodGet, tt.target, nil, "")
			if rec.Code < 400 {
				t.Fatalf("status = %d, want deny", rec.Code)
			}
			requireNoRuntimeObservation(t, env)
			event := requireAuditEvent(t, env, 0, audit.EventTypePathDenied, rec.Code, "path_denied")
			if event.Details["deny_class"] != tt.want {
				t.Fatalf("deny_class = %#v, want %s", event.Details["deny_class"], tt.want)
			}
		})
	}
}

func TestDeniedCopyMoveDestinationEmitsPathAudit(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		destination string
		wantClass   string
	}{
		{name: "missing", method: "COPY", destination: "", wantClass: "destination_missing"},
		{name: "cross host", method: "COPY", destination: "http://other.example.test/e/" + testExportID + "/docs/bad.txt", wantClass: "destination_host_mismatch"},
		{name: "cross export", method: "MOVE", destination: "http://files.example.test/e/export_other123/docs/bad.txt", wantClass: "destination_export_mismatch"},
		{name: ".jvs", method: "COPY", destination: "http://files.example.test/e/" + testExportID + "/.jvs/config", wantClass: "destination_jvs_denied"},
		{name: "encoded traversal", method: "MOVE", destination: "http://files.example.test/e/" + testExportID + "/docs/%252e%252e/bad.txt?token=destination-secret", wantClass: "destination_traversal_denied"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newGatewayTestEnv(t, sessionstate.AccessModeReadWrite, sessionstate.ExportStatusActive)
			env.writePayload(t, "docs/hello.txt", "hello")

			rec := env.request(tt.method, "/e/"+testExportID+"/docs/hello.txt", nil, tt.destination)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", rec.Code)
			}
			requireNoRuntimeObservation(t, env)
			event := requireAuditEvent(t, env, 0, audit.EventTypePathDenied, http.StatusForbidden, "path_denied")
			if event.Details["method"] != tt.method {
				t.Fatalf("method detail = %#v", event.Details["method"])
			}
			if event.Details["deny_class"] != tt.wantClass {
				t.Fatalf("deny_class = %#v, want %s", event.Details["deny_class"], tt.wantClass)
			}
			rendered := renderAuditEvent(t, event)
			for _, forbidden := range []string{"other.example.test", "export_other123", "destination-secret", tt.destination} {
				if forbidden != "" && strings.Contains(rendered, forbidden) {
					t.Fatalf("audit event leaked destination material %q in %s", forbidden, rendered)
				}
			}
		})
	}
}

func TestDeniedAuditSinkFailurePreservesDeniedResponse(t *testing.T) {
	env := newGatewayTestEnv(t, sessionstate.AccessModeReadOnly, sessionstate.ExportStatusActive)
	env.auditSink.err = errors.New("audit outbox unavailable")

	rec := env.request(http.MethodPut, "/e/"+testExportID+"/hello.txt", strings.NewReader("mutate"), "")

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "audit outbox unavailable") {
		t.Fatalf("response leaked audit sink error: %q", rec.Body.String())
	}
	requireNoRuntimeObservation(t, env)
	requireAuditEvent(t, env, 0, audit.EventTypeCapabilityDenied, http.StatusForbidden, "capability_denied")
}

func TestDeniedAuditPayloadDoesNotContainSensitiveWebDAVMaterial(t *testing.T) {
	env := newGatewayTestEnv(t, sessionstate.AccessModeReadWrite, sessionstate.ExportStatusActive)
	req := httptest.NewRequest("COPY", "http://files.example.test/e/"+testExportID+"/docs/%252e%252e/bad.txt?download_secret=query", strings.NewReader("body-file-content-secret"))
	req.SetBasicAuth(testExportID, testPassword)
	req.Header.Set(auth.HeaderAuthorization, "Basic authorization-secret")
	req.Header.Set("Destination", "http://files.example.test/e/"+testExportID+"/copy.txt?destination_secret=query")
	rec := httptest.NewRecorder()

	env.handler.ServeHTTP(rec, req)

	if rec.Code < 400 {
		t.Fatalf("status = %d, want deny", rec.Code)
	}
	event := requireAuditEvent(t, env, 0, audit.EventTypePathDenied, rec.Code, "path_denied")
	rendered := renderAuditEvent(t, event)
	for _, forbidden := range []string{
		testPassword,
		"authorization-secret",
		env.volumeRoot,
		env.payloadRoot,
		filepath.Join(env.payloadRoot, "docs", "bad.txt"),
		"destination_secret",
		"body-file-content-secret",
	} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("audit event leaked %q in %s", forbidden, rendered)
		}
	}
}

type gatewayTestEnv struct {
	handler     http.Handler
	store       *fakeGatewayStore
	auditSink   *fakeAuditSink
	volumeRoot  string
	payloadRoot string
}

func newGatewayTestEnv(t *testing.T, mode sessionstate.AccessMode, status sessionstate.ExportStatus) *gatewayTestEnv {
	t.Helper()
	now := fixedGatewayNow()
	volumeRoot := t.TempDir()
	payloadSubdir := "afscp/namespaces/" + testNamespace + "/repos/" + testRepo + "/payload"
	payloadRoot := filepath.Join(volumeRoot, filepath.FromSlash(payloadSubdir))
	if err := os.MkdirAll(payloadRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	verifier, err := exportaccess.NewPasswordVerifier(testPassword, []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeGatewayStore{
		credential: exportaccess.GatewayCredential{
			Session: exportaccess.Session{
				ID:                     testExportID,
				NamespaceID:            testNamespace,
				RepoID:                 testRepo,
				Protocol:               exportaccess.ProtocolWebDAV,
				Mode:                   mode,
				Status:                 status,
				ExpiresAt:              now.Add(time.Hour),
				CreatedAt:              now.Add(-time.Minute),
				UpdatedAt:              now.Add(-time.Minute),
				CreatedByCallerService: "svc_test",
				CreatedByActor:         exportaccess.Actor{Type: "user", ID: "user_test"},
			},
			Verifier:            verifier,
			VolumeID:            testVolumeID,
			PayloadVolumeSubdir: payloadSubdir,
		},
	}
	auditSink := &fakeAuditSink{}
	handler, err := NewHandler(Config{
		Store:        store,
		AuditSink:    auditSink,
		AuditEventID: func() string { return "evt_exportgateway_test" },
		VolumeRoots:  map[string]string{testVolumeID: volumeRoot},
		Prefix:       "/e/",
		Now:          fixedGatewayNow,
		HeartbeatTTL: testHeartbeatTTL,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return &gatewayTestEnv{handler: handler, store: store, auditSink: auditSink, volumeRoot: volumeRoot, payloadRoot: payloadRoot}
}

func (env *gatewayTestEnv) request(method, target string, body io.Reader, destination string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://files.example.test"+target, body)
	req.SetBasicAuth(testExportID, testPassword)
	if destination != "" {
		req.Header.Set("Destination", destination)
	}
	rec := httptest.NewRecorder()
	env.handler.ServeHTTP(rec, req)
	return rec
}

func (env *gatewayTestEnv) writePayload(t *testing.T, rel, content string) {
	t.Helper()
	path := filepath.Join(env.payloadRoot, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (env *gatewayTestEnv) readPayload(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(env.payloadRoot, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

type fakeGatewayStore struct {
	credential         exportaccess.GatewayCredential
	getErr             error
	recordErr          error
	runtimeErrs        []error
	runtimeCallErrs    []error
	observations       []exportaccess.RuntimeObservation
	recordCalls        int
	lastRecordExportID string
	lastRecordAt       time.Time
}

type fakeAuditSink struct {
	events []audit.Event
	err    error
}

func (sink *fakeAuditSink) Emit(ctx context.Context, event audit.Event) error {
	sink.events = append(sink.events, event)
	return sink.err
}

func (store *fakeGatewayStore) GetExportGatewayCredential(ctx context.Context, exportID string) (exportaccess.GatewayCredential, error) {
	if store.getErr != nil {
		return exportaccess.GatewayCredential{}, store.getErr
	}
	if exportID != store.credential.Session.ID {
		return exportaccess.GatewayCredential{}, errors.New("not found")
	}
	return store.credential, nil
}

func (store *fakeGatewayStore) RecordExportAccess(ctx context.Context, exportID string, accessedAt time.Time) error {
	store.recordCalls++
	store.lastRecordExportID = exportID
	store.lastRecordAt = accessedAt
	return store.recordErr
}

func (store *fakeGatewayStore) RecordExportRuntimeObservation(ctx context.Context, observation exportaccess.RuntimeObservation) (exportaccess.Session, error) {
	store.observations = append(store.observations, observation)
	var err error
	if call := len(store.observations) - 1; call < len(store.runtimeErrs) {
		err = store.runtimeErrs[call]
	}
	store.runtimeCallErrs = append(store.runtimeCallErrs, err)
	if err != nil {
		return exportaccess.Session{}, err
	}
	session := store.credential.Session
	session.ActiveRequestCount += observation.ActiveRequestDelta
	session.ActiveWriteCount += observation.ActiveWriteDelta
	session.LastObservedAt = ptrTime(observation.ObservedAt)
	session.LastGatewayHeartbeatAt = cloneTimePtr(observation.GatewayHeartbeatAt)
	session.GatewayHeartbeatExpiresAt = cloneTimePtr(observation.GatewayHeartbeatExpiresAt)
	if session.ActiveWriteCount == 0 {
		session.WriteDrainedAt = ptrTime(observation.ObservedAt)
	} else {
		session.WriteDrainedAt = nil
	}
	if observation.SuccessfulRequestAccessedAt != nil {
		session.LastAccessedAt = cloneTimePtr(observation.SuccessfulRequestAccessedAt)
	}
	store.credential.Session = session
	return session, nil
}

type observationWant struct {
	requestDelta int
	writeDelta   int
	success      bool
}

func requireObservation(t *testing.T, observations []exportaccess.RuntimeObservation, index int, want observationWant) {
	t.Helper()
	if len(observations) <= index {
		t.Fatalf("runtime observations = %d, want at least %d", len(observations), index+1)
	}
	got := observations[index]
	if got.ExportID != testExportID {
		t.Fatalf("observation[%d].ExportID = %q, want %q", index, got.ExportID, testExportID)
	}
	if !got.ObservedAt.Equal(fixedGatewayNow()) {
		t.Fatalf("observation[%d].ObservedAt = %v, want %v", index, got.ObservedAt, fixedGatewayNow())
	}
	if got.ActiveRequestDelta != want.requestDelta || got.ActiveWriteDelta != want.writeDelta {
		t.Fatalf("observation[%d] deltas = %d/%d, want %d/%d", index, got.ActiveRequestDelta, got.ActiveWriteDelta, want.requestDelta, want.writeDelta)
	}
	requireTimePtr(t, got.GatewayHeartbeatAt, fixedGatewayNow(), "GatewayHeartbeatAt")
	requireTimePtr(t, got.GatewayHeartbeatExpiresAt, fixedGatewayNow().Add(testHeartbeatTTL), "GatewayHeartbeatExpiresAt")
	if want.success {
		requireTimePtr(t, got.SuccessfulRequestAccessedAt, fixedGatewayNow(), "SuccessfulRequestAccessedAt")
	} else if got.SuccessfulRequestAccessedAt != nil {
		t.Fatalf("observation[%d].SuccessfulRequestAccessedAt = %v, want nil", index, *got.SuccessfulRequestAccessedAt)
	}
}

func requireNoRuntimeObservation(t *testing.T, env *gatewayTestEnv) {
	t.Helper()
	if len(env.store.observations) != 0 {
		t.Fatalf("runtime observations = %d, want 0", len(env.store.observations))
	}
}

func requireAuditEvent(t *testing.T, env *gatewayTestEnv, index int, wantType audit.EventType, wantStatus int, wantReason string) audit.Event {
	t.Helper()
	if len(env.auditSink.events) <= index {
		t.Fatalf("audit events = %d, want at least %d", len(env.auditSink.events), index+1)
	}
	event := env.auditSink.events[index]
	if event.EventID == "" {
		t.Fatal("audit event_id is empty")
	}
	if event.Type != wantType {
		t.Fatalf("audit type = %q, want %q", event.Type, wantType)
	}
	if !event.Time.Equal(fixedGatewayNow()) {
		t.Fatalf("audit time = %v, want %v", event.Time, fixedGatewayNow())
	}
	if event.Outcome != audit.OutcomeDenied {
		t.Fatalf("audit outcome = %q, want denied", event.Outcome)
	}
	if event.Resource.Type != "export" || event.Resource.ID != testExportID {
		t.Fatalf("audit resource = %#v", event.Resource)
	}
	if event.Reason != wantReason {
		t.Fatalf("audit reason = %q, want %q", event.Reason, wantReason)
	}
	if event.Details["status"] != wantStatus || event.Details["reason_code"] != wantReason {
		t.Fatalf("audit details = %#v, want status %d reason_code %q", event.Details, wantStatus, wantReason)
	}
	return event
}

func renderAuditEvent(t *testing.T, event audit.Event) string {
	t.Helper()
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}

func requireTimePtr(t *testing.T, got *time.Time, want time.Time, field string) {
	t.Helper()
	if got == nil || !got.Equal(want) {
		t.Fatalf("%s = %v, want %v", field, got, want)
	}
}

func ptrTime(value time.Time) *time.Time {
	return &value
}

func cloneTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func fixedGatewayNow() time.Time {
	return time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
}
