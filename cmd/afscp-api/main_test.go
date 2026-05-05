package main

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"testing"
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
