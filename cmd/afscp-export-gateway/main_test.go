package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

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
			return config.Config{ExportGateway: config.ExportGatewayConfig{
				ListenAddr:  "127.0.0.1:9090",
				PostgresDSN: "postgres://gateway:secret@db/afscp",
				Prefix:      "/e/",
				VolumeRoots: map[string]string{"vol_123": "/srv/afscp/volumes/vol_123"},
			}}, nil
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
			return config.Config{ExportGateway: config.ExportGatewayConfig{
				ListenAddr:  "127.0.0.1:9090",
				PostgresDSN: "postgres://gateway:secret@db/afscp",
				Prefix:      "/e/",
				VolumeRoots: map[string]string{"vol_123": "/srv/afscp/volumes/vol_123"},
			}}, nil
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
			return config.Config{ExportGateway: config.ExportGatewayConfig{
				ListenAddr:  "127.0.0.1:9090",
				PostgresDSN: "postgres://gateway:secret@db/afscp",
				Prefix:      "/e/",
				VolumeRoots: map[string]string{"vol_123": "/srv/afscp/volumes/vol_123"},
			}}, nil
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
