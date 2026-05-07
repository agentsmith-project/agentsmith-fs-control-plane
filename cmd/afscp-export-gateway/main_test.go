package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/config"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/exportgateway"
)

func TestRunVersion(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"--version"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run returned %d, want 0", code)
	}
	if got, want := stdout.String(), "afscp-export-gateway dev\n"; got != want {
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

func TestRunDryRunValidatesGatewayConfigWithoutServing(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	served := false

	code := runWithDeps(context.Background(), []string{"--dry-run"}, &stdout, &stderr, commandDeps{
		loadConfig: func(config.Source) (config.Config, error) {
			return config.Config{
				Capabilities: config.Capabilities{WebDAV: config.Capability{Enabled: true, Ready: true}},
				ExportGateway: config.ExportGatewayConfig{
					ListenAddr:  "127.0.0.1:9090",
					PostgresDSN: "postgres://gateway:secret@db/afscp",
					Prefix:      "/e/",
					VolumeRoots: map[string]string{"vol_123": "/srv/afscp/volumes/vol_123"},
				},
			}, nil
		},
		serve: func(context.Context, exportgateway.ServerConfig) error {
			served = true
			return nil
		},
	})

	if code != 0 {
		t.Fatalf("run returned %d, stderr %q", code, stderr.String())
	}
	if served {
		t.Fatal("dry-run called serve")
	}
	if !strings.Contains(stdout.String(), "dry-run ok") {
		t.Fatalf("stdout = %q, want dry-run ok", stdout.String())
	}
	if strings.Contains(stderr.String(), "secret") {
		t.Fatalf("stderr leaked dsn secret: %q", stderr.String())
	}
}

func TestRunServeBootstrapsGatewayWithoutConnectingInCommandTest(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	var got exportgateway.ServerConfig

	code := runWithDeps(context.Background(), []string{"--serve", "--listen-addr=127.0.0.1:9191"}, &stdout, &stderr, commandDeps{
		loadConfig: func(config.Source) (config.Config, error) {
			return config.Config{
				Capabilities: config.Capabilities{WebDAV: config.Capability{Enabled: true, Ready: true}},
				ExportGateway: config.ExportGatewayConfig{
					ListenAddr:  "127.0.0.1:9090",
					PostgresDSN: "postgres://gateway:secret@db/afscp",
					Prefix:      "/e/",
					VolumeRoots: map[string]string{"vol_123": "/srv/afscp/volumes/vol_123"},
				},
			}, nil
		},
		serve: func(ctx context.Context, cfg exportgateway.ServerConfig) error {
			got = cfg
			return nil
		},
	})

	if code != 0 {
		t.Fatalf("run returned %d, stderr %q", code, stderr.String())
	}
	if got.ListenAddr != "127.0.0.1:9191" || got.Prefix != "/e/" || got.PostgresDSN != "postgres://gateway:secret@db/afscp" {
		t.Fatalf("server config = %#v", got)
	}
	if got.VolumeRoots["vol_123"] != "/srv/afscp/volumes/vol_123" {
		t.Fatalf("volume roots = %#v", got.VolumeRoots)
	}
}

func TestRunDryRunAndServeRejectUnavailableWebDAVCapability(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		capability config.Capability
	}{
		{name: "dry-run disabled", args: []string{"--dry-run"}, capability: config.Capability{Enabled: false, Ready: false}},
		{name: "dry-run not ready", args: []string{"--dry-run"}, capability: config.Capability{Enabled: true, Ready: false}},
		{name: "serve disabled", args: []string{"--serve"}, capability: config.Capability{Enabled: false, Ready: false}},
		{name: "serve not ready", args: []string{"--serve"}, capability: config.Capability{Enabled: true, Ready: false}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			served := false

			code := runWithDeps(context.Background(), tt.args, &stdout, &stderr, commandDeps{
				loadConfig: func(config.Source) (config.Config, error) {
					cfg := readyGatewayCommandConfig()
					cfg.Capabilities.WebDAV = tt.capability
					return cfg, nil
				},
				serve: func(context.Context, exportgateway.ServerConfig) error {
					served = true
					return nil
				},
			})

			if code == 0 {
				t.Fatal("run succeeded, want unavailable WebDAV capability failure")
			}
			if served {
				t.Fatal("serve called despite unavailable WebDAV capability")
			}
			if strings.Contains(stdout.String(), "dry-run ok") {
				t.Fatalf("stdout = %q, want no dry-run success", stdout.String())
			}
			if !strings.Contains(strings.ToLower(stderr.String()), "webdav") {
				t.Fatalf("stderr = %q, want WebDAV capability context", stderr.String())
			}
		})
	}
}

func TestRunGatewayConfigErrorsAreRedacted(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := runWithDeps(context.Background(), []string{"--dry-run"}, &stdout, &stderr, commandDeps{
		loadConfig: func(config.Source) (config.Config, error) {
			return config.Config{ExportGateway: config.ExportGatewayConfig{
				ListenAddr:  "127.0.0.1:9090",
				PostgresDSN: "postgres://gateway:secret@db/afscp",
				Prefix:      "/e/",
			}}, nil
		},
	})

	if code == 0 {
		t.Fatal("run succeeded, want config failure")
	}
	if strings.Contains(stderr.String(), "secret") {
		t.Fatalf("stderr leaked secret: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "AFSCP_EXPORT_GATEWAY_VOLUME_ROOTS") {
		t.Fatalf("stderr = %q, want volume roots error", stderr.String())
	}
}

func TestRunServeErrorIsRedacted(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := runWithDeps(context.Background(), []string{"--serve"}, &stdout, &stderr, commandDeps{
		loadConfig: func(config.Source) (config.Config, error) {
			return readyGatewayCommandConfig(), nil
		},
		serve: func(context.Context, exportgateway.ServerConfig) error {
			return errors.New("postgres://gateway:secret@db/afscp unavailable")
		},
	})

	if code == 0 {
		t.Fatal("run succeeded, want serve failure")
	}
	if strings.Contains(stderr.String(), "secret") {
		t.Fatalf("stderr leaked secret: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "[REDACTED]") {
		t.Fatalf("stderr = %q, want redaction marker", stderr.String())
	}
}

func TestAuditOutboxSinkAppendsAuditEvent(t *testing.T) {
	store := &fakeAuditAppendStore{}
	event := audit.NewEvent(audit.Event{
		EventID:  "evt_exportgateway_cmd_test",
		Type:     audit.EventTypePathDenied,
		Time:     time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC),
		Resource: audit.Resource{Type: "export", ID: "export_123"},
		Outcome:  audit.OutcomeDenied,
		Reason:   "path_denied",
	})

	if err := (auditOutboxSink{store: store}).Emit(context.Background(), event); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if len(store.events) != 1 || store.events[0].EventID != event.EventID {
		t.Fatalf("events = %#v", store.events)
	}
}

type fakeAuditAppendStore struct {
	events []audit.Event
}

func (store *fakeAuditAppendStore) AppendAuditEvent(ctx context.Context, event audit.Event) error {
	store.events = append(store.events, event)
	return nil
}

func readyGatewayCommandConfig() config.Config {
	return config.Config{
		Capabilities: config.Capabilities{WebDAV: config.Capability{Enabled: true, Ready: true}},
		ExportGateway: config.ExportGatewayConfig{
			ListenAddr:  "127.0.0.1:9090",
			PostgresDSN: "postgres://gateway:secret@db/afscp",
			Prefix:      "/e/",
			VolumeRoots: map[string]string{"vol_123": "/srv/afscp/volumes/vol_123"},
		},
	}
}
