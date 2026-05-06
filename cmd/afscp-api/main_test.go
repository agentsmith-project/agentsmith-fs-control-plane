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
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/config"
)

func TestRunVersion(t *testing.T) {
	t.Setenv("AFSCP_STORAGE_ENABLED", "maybe")

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
	t.Setenv("AFSCP_STORAGE_ENABLED", "maybe")

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

func TestRunDryRunUsesConfiguredListenAddress(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var constructed int
	var served int

	cmd := command{
		stdout: &stdout,
		stderr: &stderr,
		loadConfig: func() (config.Config, error) {
			return config.Config{ListenAddr: "127.0.0.1:9090"}, nil
		},
		newNeutralShell: func() http.Handler {
			constructed++
			return http.NewServeMux()
		},
		serve: func(string, http.Handler) error {
			served++
			return nil
		},
	}

	code := cmd.run([]string{"--dry-run"})

	if code != 0 {
		t.Fatalf("run returned %d, want 0", code)
	}
	if constructed != 1 {
		t.Fatalf("neutral shell constructed %d times, want 1", constructed)
	}
	if served != 0 {
		t.Fatalf("server started %d times, want 0", served)
	}
	if got, want := stdout.String(), "afscp-api neutral shell configured for 127.0.0.1:9090\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunDryRunUsesInternalRuntimeWhenExplicitlyConfigured(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var neutralConstructed int
	var internalConstructed int
	var closed int
	var served int

	cmd := command{
		stdout: &stdout,
		stderr: &stderr,
		loadConfig: func() (config.Config, error) {
			return config.Config{
				ListenAddr: "127.0.0.1:9090",
				API:        config.APIConfig{Mode: "internal"},
			}, nil
		},
		newNeutralShell: func() http.Handler {
			neutralConstructed++
			return http.NewServeMux()
		},
		newInternalRuntime: func(cfg config.Config) (apiRuntime, error) {
			internalConstructed++
			if cfg.API.Mode != "internal" {
				t.Fatalf("internal runtime cfg mode = %q, want internal", cfg.API.Mode)
			}
			return apiRuntime{
				Handler: http.NewServeMux(),
				Close: func() error {
					closed++
					return nil
				},
			}, nil
		},
		serve: func(string, http.Handler) error {
			served++
			return nil
		},
	}

	code := cmd.run([]string{"--dry-run"})

	if code != 0 {
		t.Fatalf("run returned %d, want 0", code)
	}
	if neutralConstructed != 0 {
		t.Fatalf("neutral shell constructed %d times, want 0", neutralConstructed)
	}
	if internalConstructed != 1 {
		t.Fatalf("internal runtime constructed %d times, want 1", internalConstructed)
	}
	if closed != 1 {
		t.Fatalf("internal runtime closed %d times, want 1", closed)
	}
	if served != 0 {
		t.Fatalf("server started %d times, want 0", served)
	}
	if got, want := stdout.String(), "afscp-api internal shell configured for 127.0.0.1:9090\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunServeUsesConfiguredListenAddress(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var constructed int
	var served int
	var gotAddr string

	cmd := command{
		stdout: &stdout,
		stderr: &stderr,
		loadConfig: func() (config.Config, error) {
			return config.Config{ListenAddr: "127.0.0.1:9090"}, nil
		},
		newNeutralShell: func() http.Handler {
			constructed++
			return http.NewServeMux()
		},
		serve: func(addr string, _ http.Handler) error {
			served++
			gotAddr = addr
			return nil
		},
	}

	code := cmd.run([]string{"--serve"})

	if code != 0 {
		t.Fatalf("run returned %d, want 0", code)
	}
	if constructed != 1 {
		t.Fatalf("neutral shell constructed %d times, want 1", constructed)
	}
	if served != 1 {
		t.Fatalf("server started %d times, want 1", served)
	}
	if gotAddr != "127.0.0.1:9090" {
		t.Fatalf("server addr = %q, want %q", gotAddr, "127.0.0.1:9090")
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunServeUsesInternalRuntimeAndCloses(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var internalConstructed int
	var closed int
	var gotAddr string
	var gotHandler http.Handler

	cmd := command{
		stdout: &stdout,
		stderr: &stderr,
		loadConfig: func() (config.Config, error) {
			return config.Config{
				ListenAddr: "127.0.0.1:9090",
				API:        config.APIConfig{Mode: "internal"},
			}, nil
		},
		newNeutralShell: func() http.Handler {
			t.Fatal("neutral shell should not be constructed in internal mode")
			return nil
		},
		newInternalRuntime: func(config.Config) (apiRuntime, error) {
			internalConstructed++
			return apiRuntime{
				Handler: http.NewServeMux(),
				Close: func() error {
					closed++
					return nil
				},
			}, nil
		},
		serve: func(addr string, handler http.Handler) error {
			gotAddr = addr
			gotHandler = handler
			return nil
		},
	}

	code := cmd.run([]string{"--serve"})

	if code != 0 {
		t.Fatalf("run returned %d, want 0", code)
	}
	if internalConstructed != 1 {
		t.Fatalf("internal runtime constructed %d times, want 1", internalConstructed)
	}
	if closed != 1 {
		t.Fatalf("internal runtime closed %d times, want 1", closed)
	}
	if gotAddr != "127.0.0.1:9090" {
		t.Fatalf("server addr = %q, want 127.0.0.1:9090", gotAddr)
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

func TestRunInternalRuntimeReportsBootstrapServeAndCloseErrors(t *testing.T) {
	tests := []struct {
		name          string
		args          []string
		bootstrapErr  error
		serveErr      error
		closeErr      error
		wantCode      int
		wantStderr    []string
		wantCloseCall bool
	}{
		{
			name:         "bootstrap error",
			args:         []string{"--dry-run"},
			bootstrapErr: errors.New("bootstrap failed"),
			wantCode:     2,
			wantStderr:   []string{"configure internal runtime", "bootstrap failed"},
		},
		{
			name:          "dry-run close error",
			args:          []string{"--dry-run"},
			closeErr:      errors.New("close failed"),
			wantCode:      1,
			wantStderr:    []string{"close internal runtime", "close failed"},
			wantCloseCall: true,
		},
		{
			name:          "serve error joins close error",
			args:          []string{"--serve"},
			serveErr:      errors.New("serve failed"),
			closeErr:      errors.New("close failed"),
			wantCode:      1,
			wantStderr:    []string{"serve failed", "close failed"},
			wantCloseCall: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			var closed int

			cmd := command{
				stdout: &stdout,
				stderr: &stderr,
				loadConfig: func() (config.Config, error) {
					return config.Config{
						ListenAddr: "127.0.0.1:9090",
						API:        config.APIConfig{Mode: "internal"},
					}, nil
				},
				newNeutralShell: func() http.Handler {
					t.Fatal("neutral shell should not be constructed in internal mode")
					return nil
				},
				newInternalRuntime: func(config.Config) (apiRuntime, error) {
					if tt.bootstrapErr != nil {
						return apiRuntime{}, tt.bootstrapErr
					}
					return apiRuntime{
						Handler: http.NewServeMux(),
						Close: func() error {
							closed++
							return tt.closeErr
						},
					}, nil
				},
				serve: func(string, http.Handler) error {
					return tt.serveErr
				},
			}

			code := cmd.run(tt.args)

			if code != tt.wantCode {
				t.Fatalf("run returned %d, want %d", code, tt.wantCode)
			}
			if got := stdout.String(); tt.closeErr == nil && got != "" {
				t.Fatalf("stdout = %q, want empty", got)
			}
			for _, want := range tt.wantStderr {
				if !strings.Contains(stderr.String(), want) {
					t.Fatalf("stderr = %q, want substring %q", stderr.String(), want)
				}
			}
			if tt.wantCloseCall && closed != 1 {
				t.Fatalf("close called %d times, want 1", closed)
			}
			if !tt.wantCloseCall && closed != 0 {
				t.Fatalf("close called %d times, want 0", closed)
			}
		})
	}
}

func TestRunRedactsSensitiveDetailsFromCommandErrors(t *testing.T) {
	secretErr := errors.New("postgres://api:secret@db/afscp token=store-token Authorization: Bearer bad")
	tests := []struct {
		name               string
		args               []string
		loadConfig         func() (config.Config, error)
		newInternalRuntime func(config.Config) (apiRuntime, error)
		serve              func(string, http.Handler) error
		wantCode           int
	}{
		{
			name: "config error",
			args: []string{"--dry-run"},
			loadConfig: func() (config.Config, error) {
				return config.Config{}, secretErr
			},
			wantCode: 2,
		},
		{
			name: "internal runtime bootstrap error",
			args: []string{"--dry-run"},
			loadConfig: func() (config.Config, error) {
				return config.Config{ListenAddr: "127.0.0.1:9090", API: config.APIConfig{Mode: "internal"}}, nil
			},
			newInternalRuntime: func(config.Config) (apiRuntime, error) {
				return apiRuntime{}, secretErr
			},
			wantCode: 2,
		},
		{
			name: "serve error",
			args: []string{"--serve"},
			loadConfig: func() (config.Config, error) {
				return config.Config{ListenAddr: "127.0.0.1:9090"}, nil
			},
			serve: func(string, http.Handler) error {
				return secretErr
			},
			wantCode: 1,
		},
		{
			name: "close error",
			args: []string{"--dry-run"},
			loadConfig: func() (config.Config, error) {
				return config.Config{ListenAddr: "127.0.0.1:9090", API: config.APIConfig{Mode: "internal"}}, nil
			},
			newInternalRuntime: func(config.Config) (apiRuntime, error) {
				return apiRuntime{
					Handler: http.NewServeMux(),
					Close: func() error {
						return secretErr
					},
				}, nil
			},
			wantCode: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			cmd := command{
				stdout: &stdout,
				stderr: &stderr,
				loadConfig: func() (config.Config, error) {
					if tt.loadConfig != nil {
						return tt.loadConfig()
					}
					return config.Config{ListenAddr: "127.0.0.1:9090"}, nil
				},
				newNeutralShell: func() http.Handler {
					return http.NewServeMux()
				},
				newInternalRuntime: func(cfg config.Config) (apiRuntime, error) {
					if tt.newInternalRuntime != nil {
						return tt.newInternalRuntime(cfg)
					}
					return apiRuntime{Handler: http.NewServeMux()}, nil
				},
				serve: func(addr string, handler http.Handler) error {
					if tt.serve != nil {
						return tt.serve(addr, handler)
					}
					return nil
				},
			}

			code := cmd.run(tt.args)

			if code != tt.wantCode {
				t.Fatalf("run returned %d, want %d", code, tt.wantCode)
			}
			rendered := stderr.String()
			for _, leaked := range []string{"postgres://api", "secret", "store-token", "Bearer bad"} {
				if strings.Contains(rendered, leaked) {
					t.Fatalf("stderr = %q leaked %q", rendered, leaked)
				}
			}
		})
	}
}

func TestRunExplicitListenOverridesConfiguredListenAddress(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var constructed int

	cmd := command{
		stdout: &stdout,
		stderr: &stderr,
		loadConfig: func() (config.Config, error) {
			return config.Config{ListenAddr: ":8080"}, nil
		},
		newNeutralShell: func() http.Handler {
			constructed++
			return http.NewServeMux()
		},
		serve: func(string, http.Handler) error {
			t.Fatal("server should not start during dry-run")
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
	if got, want := stdout.String(), "afscp-api neutral shell configured for 127.0.0.1:0\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunLoadsListenAddressFromEnv(t *testing.T) {
	t.Setenv("AFSCP_LISTEN_ADDR", "127.0.0.1:9091")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var constructed int
	var served int

	cmd := newCommand(&stdout, &stderr)
	cmd.newNeutralShell = func() http.Handler {
		constructed++
		return http.NewServeMux()
	}
	cmd.serve = func(string, http.Handler) error {
		served++
		return nil
	}

	code := cmd.run([]string{"--dry-run"})

	if code != 0 {
		t.Fatalf("run returned %d, want 0", code)
	}
	if constructed != 1 {
		t.Fatalf("neutral shell constructed %d times, want 1", constructed)
	}
	if served != 0 {
		t.Fatalf("server started %d times, want 0", served)
	}
	if got, want := stdout.String(), "afscp-api neutral shell configured for 127.0.0.1:9091\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestRunRejectsInvalidEnvListenBeforeConstructingShell(t *testing.T) {
	t.Setenv("AFSCP_LISTEN_ADDR", ":8080")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var constructed int
	var served int

	cmd := newCommand(&stdout, &stderr)
	cmd.newNeutralShell = func() http.Handler {
		constructed++
		return http.NewServeMux()
	}
	cmd.serve = func(string, http.Handler) error {
		served++
		return nil
	}

	code := cmd.run([]string{"--serve"})

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
	if got := stderr.String(); !strings.Contains(got, "invalid config listen address") {
		t.Fatalf("stderr = %q, want invalid config listen address error", got)
	}
}

func TestRunRejectsInvalidConfigBeforeConstructingShell(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		loadConfig func() (config.Config, error)
		wantError  string
	}{
		{
			name: "dry-run invalid config value",
			args: []string{"--dry-run"},
			loadConfig: func() (config.Config, error) {
				return config.Load(config.MapSource{"AFSCP_STORAGE_ENABLED": "maybe"})
			},
			wantError: "invalid config",
		},
		{
			name: "serve invalid config value",
			args: []string{"--serve"},
			loadConfig: func() (config.Config, error) {
				return config.Load(config.MapSource{"AFSCP_STORAGE_ENABLED": "maybe"})
			},
			wantError: "invalid config",
		},
		{
			name: "dry-run invalid configured listen",
			args: []string{"--dry-run"},
			loadConfig: func() (config.Config, error) {
				return config.Config{ListenAddr: ":8080"}, nil
			},
			wantError: "invalid config listen address",
		},
		{
			name: "serve invalid configured listen",
			args: []string{"--serve"},
			loadConfig: func() (config.Config, error) {
				return config.Config{ListenAddr: ":8080"}, nil
			},
			wantError: "invalid config listen address",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			var constructed int
			var served int

			cmd := command{
				stdout:     &stdout,
				stderr:     &stderr,
				loadConfig: tt.loadConfig,
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
			if got := stderr.String(); !strings.Contains(got, tt.wantError) {
				t.Fatalf("stderr = %q, want %q", got, tt.wantError)
			}
		})
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
				loadConfig: func() (config.Config, error) {
					return config.Config{ListenAddr: "127.0.0.1:9090"}, nil
				},
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
		loadConfig: func() (config.Config, error) {
			return config.Config{ListenAddr: "127.0.0.1:9090"}, nil
		},
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
		loadConfig: func() (config.Config, error) {
			return config.Config{ListenAddr: "127.0.0.1:9090"}, nil
		},
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
		loadConfig: func() (config.Config, error) {
			return config.Config{ListenAddr: "127.0.0.1:8080"}, nil
		},
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

func TestRunConfiguredCapabilitiesDoNotMakeNeutralShellReady(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var gotHandler http.Handler

	cmd := command{
		stdout: &stdout,
		stderr: &stderr,
		loadConfig: func() (config.Config, error) {
			return config.Config{
				ListenAddr: "127.0.0.1:0",
				Capabilities: config.Capabilities{
					Storage: config.Capability{Enabled: true, Ready: true},
					JVS:     config.Capability{Enabled: true, Ready: true},
					WebDAV:  config.Capability{Enabled: true, Ready: true},
					Mount:   config.Capability{Enabled: true, Ready: true},
				},
			}, nil
		},
		newNeutralShell: api.NewNeutralShell,
		serve: func(_ string, handler http.Handler) error {
			gotHandler = handler
			return nil
		},
	}

	code := cmd.run([]string{"--serve"})

	if code != 0 {
		t.Fatalf("run returned %d, want 0", code)
	}
	if gotHandler == nil {
		t.Fatal("server handler is nil")
	}

	rec := httptest.NewRecorder()
	gotHandler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want %d: %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}

	var body api.ReadinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("readiness response did not decode as JSON: %v: %s", err, rec.Body.String())
	}
	if body.Ready {
		t.Fatal("neutral shell reported ready after enabled config capabilities")
	}
	if got := body.Capabilities[api.CapabilityStorage]; got.Enabled || got.Ready || !got.Gated {
		t.Fatalf("storage capability escaped neutral guardrail: %#v", got)
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
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
