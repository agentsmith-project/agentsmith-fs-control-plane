package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/api"
)

func TestRunVersion(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"--version"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run returned %d, want 0", code)
	}
	if got, want := stdout.String(), "afscp-api dev\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunNoArgsIsNoop(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run(nil, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run returned %d, want 0", code)
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunRejectsInvalidListenAddressBeforeConstructingShell(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{
			name: "empty serve listen address",
			args: []string{"--serve", "--listen", ""},
		},
		{
			name: "hostless serve listen address",
			args: []string{"--serve", "--listen", ":8080"},
		},
		{
			name: "hostless dry-run listen address",
			args: []string{"--dry-run", "--listen", ":8080"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			var constructed int
			var served int

			cmd := command{
				stdout: &stdout,
				stderr: &stderr,
				newNeutralShell: func() http.Handler {
					constructed++
					return http.NewServeMux()
				},
				serve: func(string, http.Handler) error {
					served++
					return nil
				},
			}

			code := cmd.run(tt.args)

			if code != 2 {
				t.Fatalf("run returned %d, want 2", code)
			}
			if constructed != 0 {
				t.Fatalf("neutral shell constructed %d times, want 0", constructed)
			}
			if served != 0 {
				t.Fatalf("server started %d times, want 0", served)
			}
			if got := stdout.String(); got != "" {
				t.Fatalf("stdout = %q, want empty", got)
			}
			if got := stderr.String(); !strings.Contains(got, "invalid --listen") {
				t.Fatalf("stderr = %q, want invalid --listen error", got)
			}
		})
	}
}

func TestRunDryRunConstructsNeutralShellWithoutServing(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var constructed int
	var served int

	cmd := command{
		stdout: &stdout,
		stderr: &stderr,
		newNeutralShell: func() http.Handler {
			constructed++
			return http.NewServeMux()
		},
		serve: func(string, http.Handler) error {
			served++
			return nil
		},
	}

	code := cmd.run([]string{"--dry-run", "--listen", "127.0.0.1:0"})

	if code != 0 {
		t.Fatalf("run returned %d, want 0", code)
	}
	if constructed != 1 {
		t.Fatalf("neutral shell constructed %d times, want 1", constructed)
	}
	if served != 0 {
		t.Fatalf("server started %d times, want 0", served)
	}
	if got, want := stdout.String(), "afscp-api neutral shell configured for 127.0.0.1:0\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunServeUsesInjectedServerRunner(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var constructed int
	var served int
	var gotAddr string
	var gotHandler http.Handler

	cmd := command{
		stdout: &stdout,
		stderr: &stderr,
		newNeutralShell: func() http.Handler {
			constructed++
			return http.NewServeMux()
		},
		serve: func(addr string, handler http.Handler) error {
			served++
			gotAddr = addr
			gotHandler = handler
			return nil
		},
	}

	code := cmd.run([]string{"--serve", "--listen", "127.0.0.1:0"})

	if code != 0 {
		t.Fatalf("run returned %d, want 0", code)
	}
	if constructed != 1 {
		t.Fatalf("neutral shell constructed %d times, want 1", constructed)
	}
	if served != 1 {
		t.Fatalf("server started %d times, want 1", served)
	}
	if gotAddr != "127.0.0.1:0" {
		t.Fatalf("server addr = %q, want %q", gotAddr, "127.0.0.1:0")
	}
	if gotHandler == nil {
		t.Fatal("server handler is nil")
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunServeReportsRunnerError(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	wantErr := errors.New("runner failed")

	cmd := command{
		stdout: &stdout,
		stderr: &stderr,
		newNeutralShell: func() http.Handler {
			return http.NewServeMux()
		},
		serve: func(string, http.Handler) error {
			return wantErr
		},
	}

	code := cmd.run([]string{"--serve"})

	if code != 1 {
		t.Fatalf("run returned %d, want 1", code)
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	if got, want := stderr.String(), "afscp-api: runner failed\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestNewCommandWiresStructuredRequestLogsToStderr(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := newCommand(&stdout, &stderr)
	handler := cmd.newNeutralShell()
	req := httptest.NewRequest(http.MethodGet, "/not-a-route?token=query-token", nil)
	req.Header.Set(api.HeaderCorrelationID, "corr_cmd")
	req.Header.Set("Authorization", "Bearer command-authorization-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d: %s", http.StatusNotFound, rec.Code, rec.Body.String())
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}

	trimmed := bytes.TrimSpace(stderr.Bytes())
	if len(trimmed) == 0 {
		t.Fatal("expected structured request log on stderr")
	}

	var entry map[string]any
	if err := json.Unmarshal(trimmed, &entry); err != nil {
		t.Fatalf("stderr did not contain JSON log: %v: %s", err, stderr.String())
	}
	if got, want := entry["event"], "afscp.request.path_denied"; got != want {
		t.Fatalf("event = %#v, want %#v in %#v", got, want, entry)
	}
	if got, want := entry["correlation_id"], "corr_cmd"; got != want {
		t.Fatalf("correlation_id = %#v, want %#v", got, want)
	}
	if got, want := entry["route"], "unmatched"; got != want {
		t.Fatalf("route = %#v, want %#v", got, want)
	}

	rendered := stderr.String()
	for _, leaked := range []string{"command-authorization-token", "query-token"} {
		if strings.Contains(rendered, leaked) {
			t.Fatalf("command request log leaked %q in %s", leaked, rendered)
		}
	}
}
