package exportgateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

	for _, method := range []string{http.MethodPut, http.MethodDelete, "MOVE", "COPY"} {
		t.Run(method+" denied", func(t *testing.T) {
			body := strings.NewReader("mutate")
			rec := env.request(method, "/e/"+testExportID+"/hello.txt", body, "http://files.example.test/e/"+testExportID+"/copy.txt")
			if rec.Code != http.StatusForbidden && rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want deny, body %q", rec.Code, rec.Body.String())
			}
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

type gatewayTestEnv struct {
	handler     http.Handler
	store       *fakeGatewayStore
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
	handler, err := NewHandler(Config{
		Store:        store,
		VolumeRoots:  map[string]string{testVolumeID: volumeRoot},
		Prefix:       "/e/",
		Now:          fixedGatewayNow,
		HeartbeatTTL: testHeartbeatTTL,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return &gatewayTestEnv{handler: handler, store: store, volumeRoot: volumeRoot, payloadRoot: payloadRoot}
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
