package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auditdelivery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/recovery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/worker"
)

func TestRunVersion(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"--version"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run returned %d, want 0", code)
	}
	if got, want := stdout.String(), "afscp-worker dev\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("stderr = %q, want empty", got)
	}
}

func TestCommandVersionAndNoArgsDoNotConstructRunner(t *testing.T) {
	for _, args := range [][]string{{"--version"}, nil} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			factoryCalls := 0
			cmd := newCommand(&stdout, &stderr)
			cmd.newRunner = func() (runOnceRunner, error) {
				factoryCalls++
				return &fakeRunOnceRunner{}, nil
			}

			code := cmd.run(args)
			if code != 0 {
				t.Fatalf("run returned %d, want 0", code)
			}
			if factoryCalls != 0 {
				t.Fatalf("factory calls = %d, want 0", factoryCalls)
			}
		})
	}
}

func TestCommandRunOnceSuccessOutputsJSONSummary(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	runner := &fakeRunOnceRunner{result: worker.Result{
		OperationRecovery: recovery.OperationBatchResult{Scanned: 2, Claimed: 1},
		AuditDelivery:     auditdelivery.BatchResult{Claimed: 3, Delivered: 2},
	}}
	factoryCalls := 0
	cmd := newCommand(&stdout, &stderr)
	cmd.newRunner = func() (runOnceRunner, error) {
		factoryCalls++
		return runner, nil
	}

	code := cmd.run([]string{"--run-once"})
	if code != 0 {
		t.Fatalf("run returned %d, want 0; stderr %q", code, stderr.String())
	}
	if factoryCalls != 1 || runner.calls != 1 {
		t.Fatalf("factory/runner calls = %d/%d, want 1/1", factoryCalls, runner.calls)
	}
	var summary worker.Summary
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatalf("stdout is not JSON summary: %q: %v", stdout.String(), err)
	}
	if summary.Operation.Scanned != 2 || summary.Operation.Claimed != 1 || summary.AuditDelivery.Delivered != 2 {
		t.Fatalf("summary = %#v, want aggregate counts", summary)
	}
	if stderr.String() != "" {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestCommandRunOnceErrorOutputsPartialSummaryAndRedactedFailure(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	runErr := errors.New("delivery failed token=secret-token")
	runner := &fakeRunOnceRunner{
		result: worker.Result{AuditDelivery: auditdelivery.BatchResult{Claimed: 1, Failed: 1}},
		err:    runErr,
	}
	cmd := newCommand(&stdout, &stderr)
	cmd.newRunner = func() (runOnceRunner, error) {
		return runner, nil
	}

	code := cmd.run([]string{"--run-once"})
	if code != 1 {
		t.Fatalf("run returned %d, want 1", code)
	}
	var summary worker.Summary
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatalf("stdout is not JSON summary: %q: %v", stdout.String(), err)
	}
	if summary.AuditDelivery.Claimed != 1 || summary.AuditDelivery.Failed != 1 {
		t.Fatalf("summary = %#v, want partial failure counts", summary)
	}
	if !strings.Contains(stderr.String(), "run-once failed") || strings.Contains(stderr.String(), "secret-token") {
		t.Fatalf("stderr = %q, want short redacted failure", stderr.String())
	}
}

func TestCommandRunOnceFactoryErrorExitsTwoWithoutSummary(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := newCommand(&stdout, &stderr)
	cmd.newRunner = func() (runOnceRunner, error) {
		return nil, errors.New("worker dependencies not configured")
	}

	code := cmd.run([]string{"--run-once"})
	if code != 2 {
		t.Fatalf("run returned %d, want 2", code)
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "invalid worker config") {
		t.Fatalf("stderr = %q, want config error", stderr.String())
	}
}

func TestCommandRunOnceNilRunnerFailsClosed(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := newCommand(&stdout, &stderr)
	cmd.newRunner = func() (runOnceRunner, error) {
		return nil, nil
	}

	code := cmd.run([]string{"--run-once"})
	if code != 2 {
		t.Fatalf("run returned %d, want 2", code)
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "invalid worker config") {
		t.Fatalf("stderr = %q, want config error", stderr.String())
	}
}

func TestCommandDefaultRunOnceFactoryFailsClosed(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := newCommand(&stdout, &stderr).run([]string{"--run-once"})
	if code != 2 {
		t.Fatalf("run returned %d, want default factory config error", code)
	}
}

func TestCommandRejectsPositionalArgsBeforeConstructingRunner(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	factoryCalls := 0
	cmd := newCommand(&stdout, &stderr)
	cmd.newRunner = func() (runOnceRunner, error) {
		factoryCalls++
		return &fakeRunOnceRunner{}, nil
	}

	code := cmd.run([]string{"token=secret-token"})
	if code != 2 {
		t.Fatalf("run returned %d, want 2", code)
	}
	if factoryCalls != 0 {
		t.Fatalf("factory calls = %d, want 0", factoryCalls)
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "unexpected argument") || strings.Contains(stderr.String(), "secret-token") {
		t.Fatalf("stderr = %q, want redacted positional argument error", stderr.String())
	}
}

func TestCommandFlagErrorExitsTwo(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := newCommand(&stdout, &stderr).run([]string{"--unknown"})
	if code != 2 {
		t.Fatalf("run returned %d, want 2", code)
	}
}

func TestWorkerCommandDoesNotImportIntegrationPackages(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob command files: %v", err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s imports: %v", name, err)
		}
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			for _, forbidden := range []string{"/postgres", "/jvs", "/webdav", "/mount", "/storage"} {
				if strings.Contains(path, forbidden) {
					t.Fatalf("%s imports forbidden integration package %q", name, path)
				}
			}
		}
	}
}

type fakeRunOnceRunner struct {
	calls  int
	ctx    context.Context
	result worker.Result
	err    error
}

func (runner *fakeRunOnceRunner) RunOnce(ctx context.Context) (worker.Result, error) {
	runner.calls++
	runner.ctx = ctx
	return runner.result, runner.err
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
