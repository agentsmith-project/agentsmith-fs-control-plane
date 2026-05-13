package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/volumebootstrap"
)

func TestRunEnsureParsesExplicitVolumeSpecFromEnv(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	runner := &fakeBootstrapRunner{
		ensureResult: volumebootstrap.Result{
			SchemaVersion: volumebootstrap.ResultSchemaVersion,
			Action:        volumebootstrap.ActionEnsure,
			Status:        volumebootstrap.ResultStatusReady,
			VolumeID:      "vol_default",
			OperationID:   "op_volume_bootstrap",
		},
	}
	cmd := newCommand(&stdout, &stderr)
	cmd.lookupEnv = envLookup(map[string]string{
		"AFSCP_POSTGRES_DSN": "postgres://user:secret-token@db/afscp",
		"AFSCP_VOLUME_BOOTSTRAP_SPEC": `{
			"volume_id":"vol_default",
			"backend":"juicefs",
			"isolation_class":"shared",
			"status":"active",
			"capabilities":{
				"webdav_export":true,
				"workload_mount":true,
				"jvs_external_control_root":true,
				"directory_quota":false
			}
		}`,
	})
	cmd.newRunner = func(_ context.Context, dsn string, _ volumebootstrap.Config) (bootstrapRunner, error) {
		if dsn != "postgres://user:secret-token@db/afscp" {
			t.Fatalf("dsn = %q, want env dsn", dsn)
		}
		return runner, nil
	}

	code := cmd.run([]string{"--ensure"})
	if code != 0 {
		t.Fatalf("run returned %d, want 0; stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	if runner.ensureCalls != 1 || runner.ensureSpec.VolumeID != "vol_default" || runner.ensureSpec.Backend != resources.VolumeBackendJuiceFS || runner.ensureSpec.Status != resources.VolumeStatusActive {
		t.Fatalf("ensure calls/spec = %d/%#v, want parsed default volume spec", runner.ensureCalls, runner.ensureSpec)
	}
	if !strings.Contains(stdout.String(), `"status":"ready"`) || !strings.Contains(stdout.String(), `"volume_id":"vol_default"`) {
		t.Fatalf("stdout = %q, want redacted ready JSON", stdout.String())
	}
}

func TestRunRequiresActionFlag(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	constructed := 0
	cmd := newCommand(&stdout, &stderr)
	cmd.newRunner = func(context.Context, string, volumebootstrap.Config) (bootstrapRunner, error) {
		constructed++
		return &fakeBootstrapRunner{}, nil
	}

	code := cmd.run(nil)
	if code != 2 {
		t.Fatalf("run returned %d, want 2", code)
	}
	if constructed != 0 {
		t.Fatalf("runner constructed %d times, want 0", constructed)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "--ensure") || !strings.Contains(stderr.String(), "--check") {
		t.Fatalf("stderr = %q, want action flag diagnostic", stderr.String())
	}
}

func TestRunEnsureRequiresPostgresDSN(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := newCommand(&stdout, &stderr)
	cmd.lookupEnv = envLookup(map[string]string{
		"AFSCP_VOLUME_BOOTSTRAP_SPEC": explicitVolumeSpecJSON("vol_default"),
	})

	code := cmd.run([]string{"--ensure"})
	if code != 2 {
		t.Fatalf("run returned %d, want 2", code)
	}
	if !strings.Contains(stdout.String(), `"code":"missing_postgres_dsn"`) {
		t.Fatalf("stdout = %q, want missing dsn summary", stdout.String())
	}
	if !strings.Contains(stderr.String(), "AFSCP_VOLUME_BOOTSTRAP_POSTGRES_DSN") {
		t.Fatalf("stderr = %q, want missing dsn diagnostic", stderr.String())
	}
}

func TestRunEnsureRequiresVolumeID(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	constructed := 0
	cmd := newCommand(&stdout, &stderr)
	cmd.lookupEnv = envLookup(map[string]string{
		"AFSCP_POSTGRES_DSN":          "postgres://user:secret-token@db/afscp",
		"AFSCP_VOLUME_BOOTSTRAP_SPEC": explicitVolumeSpecJSON(""),
	})
	cmd.newRunner = func(context.Context, string, volumebootstrap.Config) (bootstrapRunner, error) {
		constructed++
		return &fakeBootstrapRunner{}, nil
	}

	code := cmd.run([]string{"--ensure"})
	if code != 2 {
		t.Fatalf("run returned %d, want 2", code)
	}
	if constructed != 0 {
		t.Fatalf("runner constructed %d times, want 0", constructed)
	}
	if !strings.Contains(stdout.String(), `"code":"missing_volume_id"`) {
		t.Fatalf("stdout = %q, want missing volume id summary", stdout.String())
	}
}

func TestRunRedactsSecretsFromConfigureErrors(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := newCommand(&stdout, &stderr)
	cmd.lookupEnv = envLookup(map[string]string{
		"AFSCP_POSTGRES_DSN":          "postgres://user:secret-token@db/afscp",
		"AFSCP_VOLUME_BOOTSTRAP_SPEC": explicitVolumeSpecJSON("vol_default"),
	})
	cmd.newRunner = func(context.Context, string, volumebootstrap.Config) (bootstrapRunner, error) {
		return nil, errors.New("connect postgres://user:secret-token@db/afscp failed")
	}

	code := cmd.run([]string{"--ensure"})
	if code != 2 {
		t.Fatalf("run returned %d, want 2", code)
	}
	all := stdout.String() + stderr.String()
	for _, leaked := range []string{"secret-token", "postgres://user:secret-token@db/afscp"} {
		if strings.Contains(all, leaked) {
			t.Fatalf("output leaked %q: stdout=%q stderr=%q", leaked, stdout.String(), stderr.String())
		}
	}
}

func envLookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

func explicitVolumeSpecJSON(volumeID string) string {
	return `{"volume_id":"` + volumeID + `","backend":"juicefs","isolation_class":"shared","status":"active","capabilities":{"webdav_export":true,"workload_mount":true,"jvs_external_control_root":true,"directory_quota":false}}`
}

type fakeBootstrapRunner struct {
	ensureCalls  int
	checkCalls   int
	closeCalls   int
	ensureSpec   volumebootstrap.VolumeSpec
	checkSpec    volumebootstrap.VolumeSpec
	ensureResult volumebootstrap.Result
	checkResult  volumebootstrap.Result
	ensureErr    error
	checkErr     error
	closeErr     error
}

func (runner *fakeBootstrapRunner) Ensure(_ context.Context, spec volumebootstrap.VolumeSpec) (volumebootstrap.Result, error) {
	runner.ensureCalls++
	runner.ensureSpec = spec
	return runner.ensureResult, runner.ensureErr
}

func (runner *fakeBootstrapRunner) Check(_ context.Context, spec volumebootstrap.VolumeSpec) (volumebootstrap.Result, error) {
	runner.checkCalls++
	runner.checkSpec = spec
	return runner.checkResult, runner.checkErr
}

func (runner *fakeBootstrapRunner) Close() error {
	runner.closeCalls++
	return runner.closeErr
}
