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
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auditdelivery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/config"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/recovery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/restoreplan"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/worker"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/workerapp"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/workloadmount"
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

func TestCommandRunOnceCanUseWorkerAppFactoryWithInjectedStore(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := newCommand(&stdout, &stderr)
	cmd.newRunner = func() (runOnceRunner, error) {
		return workerapp.NewRunOnceRunner(workerapp.Options{
			Source: config.MapSource{
				"AFSCP_WORKER_OPERATION_RECOVERY_ENABLED": "true",
				"AFSCP_POSTGRES_DSN":                      "postgres://worker:password@db/afscp",
				"AFSCP_WORKER_OWNER":                      "worker-a",
			},
			StoreFactory: func(context.Context, string) (workerapp.StoreHandle, error) {
				return workerapp.StoreHandle{Store: &cmdWorkerAppStore{}}, nil
			},
			Clock:        func() time.Time { return time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC) },
			AuditEventID: func() string { return "evt_namespace" },
		})
	}

	code := cmd.run([]string{"--run-once"})
	if code != 0 {
		t.Fatalf("run returned %d, want 0; stderr %q", code, stderr.String())
	}
	var summary worker.Summary
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatalf("stdout is not JSON summary: %q: %v", stdout.String(), err)
	}
	if summary.Operation.Scanned != 0 || summary.Operation.Failed != 0 {
		t.Fatalf("summary = %#v, want empty successful operation pass", summary)
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

func TestCommandRunOnceOperationRecoveryCountErrorOutputsSummaryAndExitsOne(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	runner := &fakeRunOnceRunner{
		result: worker.Result{OperationRecovery: recovery.OperationBatchResult{Unsupported: 1}},
		err:    errors.New("operation recovery incomplete: unsupported=1"),
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
	if summary.Operation.Unsupported != 1 {
		t.Fatalf("summary = %#v, want unsupported=1", summary)
	}
	if !strings.Contains(stderr.String(), "run-once failed") || !strings.Contains(stderr.String(), "operation recovery incomplete") {
		t.Fatalf("stderr = %q, want run-once incomplete error", stderr.String())
	}
}

func TestCommandLoopRunsUntilContextCancelledAndContinuesAfterFailure(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := &fakeRunOnceRunner{
		onRun: func(call int, _ context.Context) (worker.Result, error) {
			switch call {
			case 1:
				return worker.Result{AuditDelivery: auditdelivery.BatchResult{Claimed: 1, Failed: 1}}, errors.New("delivery failed token=secret-token")
			case 2:
				cancel()
				return worker.Result{OperationRecovery: recovery.OperationBatchResult{Scanned: 2}}, nil
			default:
				t.Fatalf("unexpected loop call %d", call)
				return worker.Result{}, nil
			}
		},
	}
	factoryCalls := 0
	cmd := newCommand(&stdout, &stderr)
	cmd.newRunner = func() (runOnceRunner, error) {
		factoryCalls++
		return runner, nil
	}

	code := cmd.runContext(ctx, []string{"--loop", "--interval=1ns"})
	if code != 0 {
		t.Fatalf("run returned %d, want 0; stderr %q", code, stderr.String())
	}
	if factoryCalls != 2 || runner.calls != 2 {
		t.Fatalf("factory/runner calls = %d/%d, want 2/2", factoryCalls, runner.calls)
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("stdout lines = %d, want 2: %q", len(lines), stdout.String())
	}
	var first worker.Summary
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("first loop summary is not JSON: %q: %v", lines[0], err)
	}
	if first.AuditDelivery.Claimed != 1 || first.AuditDelivery.Failed != 1 {
		t.Fatalf("first summary = %#v, want audit failure counts", first)
	}
	var second worker.Summary
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("second loop summary is not JSON: %q: %v", lines[1], err)
	}
	if second.Operation.Scanned != 2 {
		t.Fatalf("second summary = %#v, want operation scanned count", second)
	}
	if !strings.Contains(stderr.String(), "loop failed") || strings.Contains(stderr.String(), "secret-token") {
		t.Fatalf("stderr = %q, want redacted loop failure", stderr.String())
	}
}

func TestCommandLoopContinuesAfterFactoryError(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := &fakeRunOnceRunner{
		onRun: func(int, context.Context) (worker.Result, error) {
			cancel()
			return worker.Result{OperationRecovery: recovery.OperationBatchResult{Scanned: 1}}, nil
		},
	}
	factoryCalls := 0
	cmd := newCommand(&stdout, &stderr)
	cmd.newRunner = func() (runOnceRunner, error) {
		factoryCalls++
		if factoryCalls == 1 {
			return nil, errors.New("open postgres://worker:password=secret-password@db/afscp failed")
		}
		return runner, nil
	}

	code := cmd.runContext(ctx, []string{"--loop", "--interval=1ns"})
	if code != 0 {
		t.Fatalf("run returned %d, want 0; stderr %q", code, stderr.String())
	}
	if factoryCalls != 2 || runner.calls != 1 {
		t.Fatalf("factory/runner calls = %d/%d, want 2/1", factoryCalls, runner.calls)
	}
	var summary worker.Summary
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatalf("stdout is not JSON summary: %q: %v", stdout.String(), err)
	}
	if summary.Operation.Scanned != 1 {
		t.Fatalf("summary = %#v, want operation scanned count", summary)
	}
	for _, leaked := range []string{"postgres://worker", "secret-password"} {
		if strings.Contains(stderr.String(), leaked) {
			t.Fatalf("stderr = %q leaked %q", stderr.String(), leaked)
		}
	}
	if !strings.Contains(stderr.String(), "invalid worker config") {
		t.Fatalf("stderr = %q, want config error", stderr.String())
	}
}

func TestCommandLoopFlagContract(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "conflicting modes", args: []string{"--loop", "--run-once"}, want: "--loop cannot be combined with --run-once"},
		{name: "interval without loop", args: []string{"--interval=1s"}, want: "--interval requires --loop"},
		{name: "non-positive interval", args: []string{"--loop", "--interval=0s"}, want: "--interval must be positive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			factoryCalls := 0
			cmd := newCommand(&stdout, &stderr)
			cmd.newRunner = func() (runOnceRunner, error) {
				factoryCalls++
				return &fakeRunOnceRunner{}, nil
			}

			code := cmd.run(tc.args)
			if code != 2 {
				t.Fatalf("run returned %d, want 2", code)
			}
			if factoryCalls != 0 {
				t.Fatalf("factory calls = %d, want 0", factoryCalls)
			}
			if stdout.String() != "" {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
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
	t.Setenv("AFSCP_WORKER_OPERATION_RECOVERY_ENABLED", "")
	t.Setenv("AFSCP_POSTGRES_DSN", "")
	t.Setenv("AFSCP_DATABASE_URL", "")
	t.Setenv("AFSCP_WORKER_OWNER", "")

	code := newCommand(&stdout, &stderr).run([]string{"--run-once"})
	if code != 2 {
		t.Fatalf("run returned %d, want default factory config error", code)
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "operation recovery") {
		t.Fatalf("stderr = %q, want operation recovery config error", stderr.String())
	}
}

func TestCommandRunOnceConfigErrorsRedactDSN(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := newCommand(&stdout, &stderr)
	cmd.newRunner = func() (runOnceRunner, error) {
		return nil, errors.New("open postgres://worker:password=secret-password@db/afscp failed")
	}

	code := cmd.run([]string{"--run-once"})
	if code != 2 {
		t.Fatalf("run returned %d, want 2", code)
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	for _, leaked := range []string{"postgres://worker", "secret-password"} {
		if strings.Contains(stderr.String(), leaked) {
			t.Fatalf("stderr = %q leaked %q", stderr.String(), leaked)
		}
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
	onRun  func(int, context.Context) (worker.Result, error)
}

type cmdWorkerAppStore struct{}

func (store *cmdWorkerAppStore) ListNamespaceUpsertOperationsForRecovery(context.Context, time.Time, int) ([]operations.OperationRecord, error) {
	return nil, nil
}

func (store *cmdWorkerAppStore) ListNamespaceDisableOperationsForRecovery(context.Context, time.Time, int) ([]operations.OperationRecord, error) {
	return nil, nil
}

func (store *cmdWorkerAppStore) ListVolumeEnsureOperationsForRecovery(context.Context, time.Time, int) ([]operations.OperationRecord, error) {
	return nil, nil
}

func (store *cmdWorkerAppStore) ListNamespaceVolumeBindingPutOperationsForRecovery(context.Context, time.Time, int) ([]operations.OperationRecord, error) {
	return nil, nil
}

func (store *cmdWorkerAppStore) ListWorkloadMountBindingOperationsForRecovery(context.Context, time.Time, int) ([]operations.OperationRecord, error) {
	return nil, nil
}

func (store *cmdWorkerAppStore) ListRepoCreateOperationsForRecovery(context.Context, time.Time, int) ([]operations.OperationRecord, error) {
	return nil, nil
}

func (store *cmdWorkerAppStore) ListRepoLifecycleOperationsForRecovery(context.Context, time.Time, int) ([]operations.OperationRecord, error) {
	return nil, nil
}

func (store *cmdWorkerAppStore) ListRepoPurgeOperationsForRecovery(context.Context, time.Time, int) ([]operations.OperationRecord, error) {
	return nil, nil
}

func (store *cmdWorkerAppStore) ListSavePointCreateOperationsForRecovery(context.Context, time.Time, int) ([]operations.OperationRecord, error) {
	return nil, nil
}

func (store *cmdWorkerAppStore) ListTemplateCreateOperationsForRecovery(context.Context, time.Time, int) ([]operations.OperationRecord, error) {
	return nil, nil
}

func (store *cmdWorkerAppStore) ListTemplateCloneOperationsForRecovery(context.Context, time.Time, int) ([]operations.OperationRecord, error) {
	return nil, nil
}

func (store *cmdWorkerAppStore) ListRestorePreviewOperationsForRecovery(context.Context, time.Time, int) ([]operations.OperationRecord, error) {
	return nil, nil
}

func (store *cmdWorkerAppStore) ListRestorePreviewDiscardOperationsForRecovery(context.Context, time.Time, int) ([]operations.OperationRecord, error) {
	return nil, nil
}

func (store *cmdWorkerAppStore) ListRestoreRunOperationsForRecovery(context.Context, time.Time, int) ([]operations.OperationRecord, error) {
	return nil, nil
}

func (store *cmdWorkerAppStore) AcquireNamespaceUpsertOperationLease(context.Context, string, operations.LeaseRequest) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, errors.New("unexpected acquire")
}

func (store *cmdWorkerAppStore) AcquireNamespaceDisableOperationLease(context.Context, string, operations.LeaseRequest) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, errors.New("unexpected acquire")
}

func (store *cmdWorkerAppStore) AcquireVolumeEnsureOperationLease(context.Context, string, operations.LeaseRequest) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, errors.New("unexpected acquire")
}

func (store *cmdWorkerAppStore) AcquireNamespaceVolumeBindingPutOperationLease(context.Context, string, operations.LeaseRequest) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, errors.New("unexpected acquire")
}

func (store *cmdWorkerAppStore) AcquireWorkloadMountBindingOperationLease(context.Context, string, operations.LeaseRequest) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, errors.New("unexpected acquire")
}

func (store *cmdWorkerAppStore) AcquireRepoCreateOperationLease(context.Context, string, operations.LeaseRequest) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, errors.New("unexpected acquire")
}

func (store *cmdWorkerAppStore) AcquireRepoLifecycleOperationLease(context.Context, string, operations.LeaseRequest) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, errors.New("unexpected acquire")
}

func (store *cmdWorkerAppStore) AcquireRepoPurgeOperationLease(context.Context, string, operations.LeaseRequest) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, errors.New("unexpected acquire")
}

func (store *cmdWorkerAppStore) AcquireSavePointCreateOperationLease(context.Context, string, operations.LeaseRequest) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, errors.New("unexpected acquire")
}

func (store *cmdWorkerAppStore) AcquireTemplateCreateOperationLease(context.Context, string, operations.LeaseRequest) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, errors.New("unexpected acquire")
}

func (store *cmdWorkerAppStore) AcquireTemplateCloneOperationLease(context.Context, string, operations.LeaseRequest) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, errors.New("unexpected acquire")
}

func (store *cmdWorkerAppStore) AcquireRestorePreviewOperationLease(context.Context, string, operations.LeaseRequest) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, errors.New("unexpected acquire")
}

func (store *cmdWorkerAppStore) AcquireRestorePreviewDiscardOperationLease(context.Context, string, operations.LeaseRequest) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, errors.New("unexpected acquire")
}

func (store *cmdWorkerAppStore) AcquireRestoreRunOperationLease(context.Context, string, operations.LeaseRequest) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, errors.New("unexpected acquire")
}

func (store *cmdWorkerAppStore) RenewOperationLease(context.Context, string, operations.LeaseRequest) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, errors.New("unexpected renew")
}

func (store *cmdWorkerAppStore) CommitNamespaceUpsertWithLease(context.Context, resources.Namespace, operations.SanitizedOperationRecord, string, time.Time, audit.Event) (resources.Namespace, operations.OperationRecord, error) {
	return resources.Namespace{}, operations.OperationRecord{}, errors.New("unexpected commit")
}

func (store *cmdWorkerAppStore) CommitNamespaceDisableWithLease(context.Context, resources.Namespace, operations.SanitizedOperationRecord, string, time.Time, audit.Event) (resources.Namespace, operations.OperationRecord, error) {
	return resources.Namespace{}, operations.OperationRecord{}, errors.New("unexpected commit")
}

func (store *cmdWorkerAppStore) CommitVolumeEnsureWithLease(context.Context, resources.Volume, operations.SanitizedOperationRecord, string, time.Time, audit.Event) (resources.Volume, operations.OperationRecord, error) {
	return resources.Volume{}, operations.OperationRecord{}, errors.New("unexpected commit")
}

func (store *cmdWorkerAppStore) CommitNamespaceVolumeBindingPutWithLease(context.Context, resources.NamespaceVolumeBinding, operations.SanitizedOperationRecord, string, time.Time, audit.Event) (resources.NamespaceVolumeBinding, operations.OperationRecord, error) {
	return resources.NamespaceVolumeBinding{}, operations.OperationRecord{}, errors.New("unexpected commit")
}

func (store *cmdWorkerAppStore) CommitWorkloadMountBindingCreateWithLease(context.Context, workloadmount.Binding, operations.SanitizedOperationRecord, string, time.Time, audit.Event) (workloadmount.Binding, operations.OperationRecord, error) {
	return workloadmount.Binding{}, operations.OperationRecord{}, errors.New("unexpected commit")
}

func (store *cmdWorkerAppStore) CommitWorkloadMountBindingStatusWithLease(context.Context, string, sessionstate.MountStatus, string, time.Time, *time.Time, operations.SanitizedOperationRecord, string, time.Time, audit.Event) (workloadmount.Binding, operations.OperationRecord, error) {
	return workloadmount.Binding{}, operations.OperationRecord{}, errors.New("unexpected commit")
}

func (store *cmdWorkerAppStore) CommitWorkloadMountBindingHeartbeatWithLease(context.Context, string, operations.SanitizedOperationRecord, string, time.Time, audit.Event) (workloadmount.Binding, operations.OperationRecord, error) {
	return workloadmount.Binding{}, operations.OperationRecord{}, errors.New("unexpected commit")
}

func (store *cmdWorkerAppStore) CommitWorkloadMountBindingReleaseWithLease(context.Context, string, operations.SanitizedOperationRecord, string, time.Time, audit.Event) (workloadmount.Binding, operations.OperationRecord, error) {
	return workloadmount.Binding{}, operations.OperationRecord{}, errors.New("unexpected commit")
}

func (store *cmdWorkerAppStore) CommitWorkloadMountBindingRevokeWithLease(context.Context, string, operations.SanitizedOperationRecord, string, time.Time, audit.Event) (workloadmount.Binding, operations.OperationRecord, error) {
	return workloadmount.Binding{}, operations.OperationRecord{}, errors.New("unexpected commit")
}

func (store *cmdWorkerAppStore) CommitRepoCreateSucceededWithLease(context.Context, resources.Repo, operations.SanitizedOperationRecord, string, time.Time, audit.Event, string) (resources.Repo, operations.OperationRecord, error) {
	return resources.Repo{}, operations.OperationRecord{}, errors.New("unexpected commit")
}

func (store *cmdWorkerAppStore) CommitRepoCreateFailedWithLease(context.Context, operations.SanitizedOperationRecord, string, time.Time, audit.Event, string) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, errors.New("unexpected commit")
}

func (store *cmdWorkerAppStore) CommitRepoLifecycleSucceededWithLease(context.Context, resources.Repo, operations.SanitizedOperationRecord, string, time.Time, audit.Event, string) (resources.Repo, operations.OperationRecord, error) {
	return resources.Repo{}, operations.OperationRecord{}, errors.New("unexpected commit")
}

func (store *cmdWorkerAppStore) CommitRepoLifecycleFailedWithLease(context.Context, operations.SanitizedOperationRecord, string, time.Time, audit.Event, string) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, errors.New("unexpected commit")
}

func (store *cmdWorkerAppStore) CommitRepoPurgeSucceededWithLease(context.Context, resources.Repo, operations.SanitizedOperationRecord, string, time.Time, audit.Event, string) (resources.Repo, operations.OperationRecord, error) {
	return resources.Repo{}, operations.OperationRecord{}, errors.New("unexpected commit")
}

func (store *cmdWorkerAppStore) CommitRepoPurgeFailedWithLease(context.Context, operations.SanitizedOperationRecord, string, time.Time, audit.Event, string) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, errors.New("unexpected commit")
}

func (store *cmdWorkerAppStore) UpdateSavePointCreateProgressWithLease(context.Context, operations.SanitizedOperationRecord, string, time.Time) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, errors.New("unexpected progress update")
}

func (store *cmdWorkerAppStore) CommitSavePointCreateSucceededWithLease(context.Context, operations.SanitizedOperationRecord, string, time.Time, audit.Event) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, errors.New("unexpected commit")
}

func (store *cmdWorkerAppStore) CommitSavePointCreateFailedWithLease(context.Context, operations.SanitizedOperationRecord, string, time.Time, audit.Event) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, errors.New("unexpected commit")
}

func (store *cmdWorkerAppStore) CommitTemplateCreateSucceededWithLease(context.Context, resources.Repo, string, string, string, operations.SanitizedOperationRecord, string, time.Time, audit.Event) (resources.Repo, operations.OperationRecord, error) {
	return resources.Repo{}, operations.OperationRecord{}, errors.New("unexpected commit")
}

func (store *cmdWorkerAppStore) MarkTemplateCreateWriterFencedWithLease(context.Context, fences.Fence, operations.SanitizedOperationRecord, string, time.Time) (fences.Fence, operations.OperationRecord, error) {
	return fences.Fence{}, operations.OperationRecord{}, errors.New("unexpected mark")
}

func (store *cmdWorkerAppStore) CommitTemplateCreateFailedWithLease(context.Context, operations.SanitizedOperationRecord, string, time.Time, audit.Event) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, errors.New("unexpected commit")
}

func (store *cmdWorkerAppStore) CommitTemplateCloneSucceededWithLease(context.Context, resources.Repo, operations.SanitizedOperationRecord, string, time.Time, audit.Event) (resources.Repo, operations.OperationRecord, error) {
	return resources.Repo{}, operations.OperationRecord{}, errors.New("unexpected commit")
}

func (store *cmdWorkerAppStore) CommitTemplateCloneFailedWithLease(context.Context, operations.SanitizedOperationRecord, string, time.Time, audit.Event) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, errors.New("unexpected commit")
}

func (store *cmdWorkerAppStore) UpdateRestorePreviewPreflightWithLease(context.Context, operations.SanitizedOperationRecord, string, time.Time) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, errors.New("unexpected progress update")
}

func (store *cmdWorkerAppStore) CommitRestorePreviewSucceededWithLease(context.Context, restoreplan.Plan, operations.SanitizedOperationRecord, string, time.Time, audit.Event) (restoreplan.Plan, operations.OperationRecord, error) {
	return restoreplan.Plan{}, operations.OperationRecord{}, errors.New("unexpected commit")
}

func (store *cmdWorkerAppStore) CommitRestorePreviewFailedWithLease(context.Context, operations.SanitizedOperationRecord, string, time.Time, audit.Event) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, errors.New("unexpected commit")
}

func (store *cmdWorkerAppStore) MarkRestorePreviewDiscardingWithLease(context.Context, restoreplan.Plan, operations.SanitizedOperationRecord, string, time.Time) (restoreplan.Plan, operations.OperationRecord, error) {
	return restoreplan.Plan{}, operations.OperationRecord{}, errors.New("unexpected progress update")
}

func (store *cmdWorkerAppStore) CommitRestorePreviewDiscardSucceededWithLease(context.Context, operations.SanitizedOperationRecord, string, time.Time, audit.Event) (restoreplan.Plan, operations.OperationRecord, error) {
	return restoreplan.Plan{}, operations.OperationRecord{}, errors.New("unexpected commit")
}

func (store *cmdWorkerAppStore) CommitRestorePreviewDiscardFailedWithLease(context.Context, operations.SanitizedOperationRecord, string, time.Time, audit.Event) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, errors.New("unexpected commit")
}

func (store *cmdWorkerAppStore) MarkRestoreRunWriterFencedWithLease(context.Context, fences.Fence, operations.SanitizedOperationRecord, string, time.Time) (fences.Fence, operations.OperationRecord, error) {
	return fences.Fence{}, operations.OperationRecord{}, errors.New("unexpected progress update")
}

func (store *cmdWorkerAppStore) MarkRestoreRunConsumingWithLease(context.Context, operations.SanitizedOperationRecord, string, time.Time) (restoreplan.Plan, operations.OperationRecord, error) {
	return restoreplan.Plan{}, operations.OperationRecord{}, errors.New("unexpected progress update")
}

func (store *cmdWorkerAppStore) CommitRestoreRunSucceededWithLease(context.Context, operations.SanitizedOperationRecord, string, time.Time, audit.Event) (restoreplan.Plan, operations.OperationRecord, error) {
	return restoreplan.Plan{}, operations.OperationRecord{}, errors.New("unexpected commit")
}

func (store *cmdWorkerAppStore) CommitRestoreRunStalePreviewWithLease(context.Context, restoreplan.Plan, operations.SanitizedOperationRecord, string, time.Time, audit.Event) (restoreplan.Plan, operations.OperationRecord, error) {
	return restoreplan.Plan{}, operations.OperationRecord{}, errors.New("unexpected commit")
}

func (store *cmdWorkerAppStore) CommitRestoreRunFailedWithLease(context.Context, operations.SanitizedOperationRecord, string, time.Time, audit.Event) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, errors.New("unexpected commit")
}

func (store *cmdWorkerAppStore) CommitOperationWithLease(context.Context, operations.SanitizedOperationRecord, string, time.Time, audit.Event) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, errors.New("unexpected generic operation commit")
}

func (store *cmdWorkerAppStore) GetOperation(context.Context, string) (operations.OperationRecord, error) {
	return operations.OperationRecord{}, errors.New("unexpected operation read")
}

func (store *cmdWorkerAppStore) GetRestorePlanByPreviewOperation(context.Context, string) (restoreplan.Plan, error) {
	return restoreplan.Plan{}, errors.New("unexpected restore plan read")
}

func (store *cmdWorkerAppStore) GetActiveRestorePlanByRepo(context.Context, string) (restoreplan.Plan, error) {
	return restoreplan.Plan{}, errors.New("unexpected restore plan read")
}

func (store *cmdWorkerAppStore) GetRepoInNamespace(context.Context, string, string) (resources.Repo, error) {
	return resources.Repo{}, errors.New("unexpected metadata read")
}

func (store *cmdWorkerAppStore) GetNamespace(context.Context, string) (resources.Namespace, error) {
	return resources.Namespace{}, errors.New("unexpected metadata read")
}

func (store *cmdWorkerAppStore) GetNamespaceVolumeBinding(context.Context, string) (resources.NamespaceVolumeBinding, error) {
	return resources.NamespaceVolumeBinding{}, errors.New("unexpected metadata read")
}

func (store *cmdWorkerAppStore) GetVolume(context.Context, string) (resources.Volume, error) {
	return resources.Volume{}, errors.New("unexpected metadata read")
}

func (store *cmdWorkerAppStore) ListHeldRepoFences(context.Context, string) ([]fences.Fence, error) {
	return nil, errors.New("unexpected fence read")
}

func (store *cmdWorkerAppStore) CreateRepoFence(context.Context, fences.Fence) error {
	return errors.New("unexpected fence create")
}

func (store *cmdWorkerAppStore) ListExportSessionsByRepo(context.Context, string) ([]sessionstate.ExportSession, error) {
	return nil, errors.New("unexpected session read")
}

func (store *cmdWorkerAppStore) ListWorkloadMountBindingsByRepo(context.Context, string) ([]sessionstate.WorkloadMountBinding, error) {
	return nil, errors.New("unexpected session read")
}

func (store *cmdWorkerAppStore) ListEarlierNonTerminalRepoLifecycleOperations(context.Context, string, string, time.Time) ([]operations.OperationRecord, error) {
	return nil, errors.New("unexpected lifecycle read")
}

func (store *cmdWorkerAppStore) ListDueAuditOutboxRecords(context.Context, time.Time, int) ([]audit.OutboxRecord, error) {
	return nil, nil
}

func (store *cmdWorkerAppStore) ClaimDueAuditOutboxRecords(context.Context, string, time.Time, int) ([]audit.OutboxRecord, error) {
	return nil, nil
}

func (store *cmdWorkerAppStore) RecoverStaleAuditOutboxRecords(context.Context, string, time.Duration, int, audit.DeliveryFailure) ([]audit.OutboxRecord, error) {
	return nil, nil
}

func (store *cmdWorkerAppStore) MarkAuditOutboxDelivered(context.Context, string, time.Time) error {
	return nil
}

func (store *cmdWorkerAppStore) MarkAuditOutboxDeliveryFailed(context.Context, string, audit.DeliveryFailure) error {
	return nil
}

func (runner *fakeRunOnceRunner) RunOnce(ctx context.Context) (worker.Result, error) {
	runner.calls++
	runner.ctx = ctx
	if runner.onRun != nil {
		return runner.onRun(runner.calls, ctx)
	}
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
