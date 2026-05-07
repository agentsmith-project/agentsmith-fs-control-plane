package worker

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auditdelivery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/exportreconcile"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/recovery"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/workloadmount"
)

func TestRunOnceRejectsEmptyConfigBeforeRunnerCalls(t *testing.T) {
	result, err := New(Config{}).RunOnce(context.Background())
	if err == nil {
		t.Fatal("RunOnce succeeded, want invalid config error")
	}
	if result.Summary() != (Summary{}) {
		t.Fatalf("summary = %#v, want zero", result.Summary())
	}
}

func TestRunOnceExecutesConfiguredRunnersInFixedOrderWithContext(t *testing.T) {
	ctx := context.WithValue(context.Background(), workerContextKey("test"), "ctx")
	order := []string{}
	export := &fakeExportReconcileRunner{name: "export", order: &order, result: exportreconcile.Result{Scanned: 2, Terminalized: 1}}
	op := &fakeOperationRunner{name: "operation", order: &order, result: recovery.OperationBatchResult{Scanned: 3, Claimed: 1}}
	mountStale := &fakeWorkloadMountStaleRunner{name: "mount_stale", order: &order, result: workloadmount.StaleLeaseResult{Scanned: 4, KeptBlocked: 4}}
	stale := &fakeStaleRunner{name: "stale", order: &order, result: auditdelivery.StaleRecoveryResult{Recovered: 2, RetryWait: 1}}
	delivery := &fakeDeliveryRunner{name: "delivery", order: &order, result: auditdelivery.BatchResult{Claimed: 4, Delivered: 3}}

	result, err := New(Config{ExportSessionReconcile: export, OperationRecovery: op, WorkloadMountStale: mountStale, AuditStaleRecovery: stale, AuditDelivery: delivery}).RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if strings.Join(order, ",") != "export,mount_stale,operation,stale,delivery" {
		t.Fatalf("order = %#v, want export mount-stale operation stale delivery", order)
	}
	if export.ctx != ctx || op.ctx != ctx || mountStale.ctx != ctx || stale.ctx != ctx || delivery.ctx != ctx {
		t.Fatal("runner did not receive request context")
	}
	summary := result.Summary()
	if summary.ExportSessionReconcile.Scanned != 2 || summary.ExportSessionReconcile.Terminalized != 1 || summary.Operation.Scanned != 3 || summary.Operation.Claimed != 1 || summary.WorkloadMountStale.Scanned != 4 || summary.WorkloadMountStale.KeptBlocked != 4 || summary.AuditStale.Recovered != 2 || summary.AuditDelivery.Delivered != 3 {
		t.Fatalf("summary = %#v, want aggregate counts", summary)
	}
}

func TestRunOnceSkipsOperationRecoveryWhenSessionEvidencePrerequisiteFails(t *testing.T) {
	exportErr := errors.New("export evidence stale")
	mountErr := errors.New("mount evidence stale")
	tests := []struct {
		name          string
		config        func(*[]string) Config
		wantOrder     string
		wantErr       error
		wantErrDetail string
	}{
		{
			name: "export reconcile",
			config: func(order *[]string) Config {
				export := &fakeExportReconcileRunner{name: "export", order: order, result: exportreconcile.Result{Scanned: 1, Failed: 1}, err: exportErr}
				op := &fakeOperationRunner{name: "operation", order: order}
				return Config{ExportSessionReconcile: export, OperationRecovery: op}
			},
			wantOrder:     "export",
			wantErr:       exportErr,
			wantErrDetail: "export session reconcile: export evidence stale",
		},
		{
			name: "workload mount stale scan",
			config: func(order *[]string) Config {
				export := &fakeExportReconcileRunner{name: "export", order: order}
				mountStale := &fakeWorkloadMountStaleRunner{name: "mount_stale", order: order, result: workloadmount.StaleLeaseResult{Scanned: 1, Failed: 1}, err: mountErr}
				op := &fakeOperationRunner{name: "operation", order: order}
				return Config{ExportSessionReconcile: export, WorkloadMountStale: mountStale, OperationRecovery: op}
			},
			wantOrder:     "export,mount_stale",
			wantErr:       mountErr,
			wantErrDetail: "workload mount stale lease scan: mount evidence stale",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			order := []string{}
			result, err := New(tt.config(&order)).RunOnce(context.Background())
			if err == nil {
				t.Fatal("RunOnce succeeded, want prerequisite error")
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("RunOnce error = %v, want wrapped prerequisite error", err)
			}
			if !strings.Contains(err.Error(), tt.wantErrDetail) {
				t.Fatalf("RunOnce error = %v, want visible detail %q", err, tt.wantErrDetail)
			}
			if strings.Contains(err.Error(), "operation recovery") {
				t.Fatalf("RunOnce error = %v, want no operation recovery error", err)
			}

			if strings.Join(order, ",") != tt.wantOrder {
				t.Fatalf("order = %#v, want %s", order, tt.wantOrder)
			}
			if result.Summary().Operation != (OperationSummary{}) {
				t.Fatalf("operation summary = %#v, want zero", result.Summary().Operation)
			}
		})
	}
}

func TestRunOnceAllowsNilComponentRunners(t *testing.T) {
	order := []string{}
	delivery := &fakeDeliveryRunner{name: "delivery", order: &order, result: auditdelivery.BatchResult{Claimed: 1, Delivered: 1}}

	result, err := New(Config{AuditDelivery: delivery}).RunOnce(nil)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if strings.Join(order, ",") != "delivery" {
		t.Fatalf("order = %#v, want only delivery", order)
	}
	if delivery.ctx == nil {
		t.Fatal("nil ctx did not fall back to context.Background")
	}
	if summary := result.Summary(); summary.AuditDelivery.Claimed != 1 || summary.AuditDelivery.Delivered != 1 {
		t.Fatalf("summary = %#v, want delivery counts", summary)
	}
}

func TestRunOnceAllowsOnlyExportSessionReconcileRunner(t *testing.T) {
	order := []string{}
	export := &fakeExportReconcileRunner{name: "export", order: &order, result: exportreconcile.Result{Scanned: 1, Reused: 1}}

	result, err := New(Config{ExportSessionReconcile: export}).RunOnce(nil)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if strings.Join(order, ",") != "export" {
		t.Fatalf("order = %#v, want only export", order)
	}
	if export.ctx == nil {
		t.Fatal("nil ctx did not fall back to context.Background")
	}
	if summary := result.Summary(); summary.ExportSessionReconcile.Scanned != 1 || summary.ExportSessionReconcile.Reused != 1 {
		t.Fatalf("summary = %#v, want export reconcile counts", summary)
	}
}

func TestRunOnceKeepsPartialResultButStopsAuditDeliveryAfterStaleRecoveryError(t *testing.T) {
	opErr := errors.New("operation failed")
	staleErr := errors.New("stale failed")
	order := []string{}
	op := &fakeOperationRunner{name: "operation", order: &order, result: recovery.OperationBatchResult{Scanned: 5, Failed: 1}, err: opErr}
	stale := &fakeStaleRunner{name: "stale", order: &order, result: auditdelivery.StaleRecoveryResult{Failed: 1}, err: staleErr}
	delivery := &fakeDeliveryRunner{name: "delivery", order: &order, result: auditdelivery.BatchResult{Claimed: 2, Delivered: 2}}

	result, err := New(Config{OperationRecovery: op, AuditStaleRecovery: stale, AuditDelivery: delivery}).RunOnce(context.Background())
	if !errors.Is(err, opErr) || !errors.Is(err, staleErr) {
		t.Fatalf("RunOnce error = %v, want joined operation and stale errors", err)
	}
	if strings.Join(order, ",") != "operation,stale" {
		t.Fatalf("order = %#v, want operation then stale only", order)
	}
	summary := result.Summary()
	if summary.Operation.Failed != 1 || summary.AuditStale.Failed != 1 || summary.AuditDelivery.Delivered != 0 {
		t.Fatalf("summary = %#v, want partial plus later results", summary)
	}
}

func TestRunOnceStopsAfterContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	order := []string{}
	op := &fakeOperationRunner{name: "operation", order: &order, after: cancel, err: context.Canceled}
	stale := &fakeStaleRunner{name: "stale", order: &order}
	delivery := &fakeDeliveryRunner{name: "delivery", order: &order}

	_, err := New(Config{OperationRecovery: op, AuditStaleRecovery: stale, AuditDelivery: delivery}).RunOnce(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunOnce error = %v, want context canceled", err)
	}
	if strings.Join(order, ",") != "operation" {
		t.Fatalf("order = %#v, want stop after cancelled context", order)
	}
}

func TestRunOncePreCancelledContextDoesNotCallAnyRunner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	order := []string{}
	op := &fakeOperationRunner{name: "operation", order: &order}
	stale := &fakeStaleRunner{name: "stale", order: &order}
	delivery := &fakeDeliveryRunner{name: "delivery", order: &order}

	result, err := New(Config{OperationRecovery: op, AuditStaleRecovery: stale, AuditDelivery: delivery}).RunOnce(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunOnce error = %v, want context canceled", err)
	}
	if len(order) != 0 {
		t.Fatalf("order = %#v, want no runner calls", order)
	}
	if result.Summary() != (Summary{}) {
		t.Fatalf("summary = %#v, want zero partial result", result.Summary())
	}
}

func TestRunOnceStopsWhenRunnerReturnsContextErrorWithoutCancellingParent(t *testing.T) {
	order := []string{}
	op := &fakeOperationRunner{name: "operation", order: &order, result: recovery.OperationBatchResult{Scanned: 1, Failed: 1}, err: errors.Join(errors.New("wrapped"), context.Canceled)}
	stale := &fakeStaleRunner{name: "stale", order: &order}
	delivery := &fakeDeliveryRunner{name: "delivery", order: &order}

	result, err := New(Config{OperationRecovery: op, AuditStaleRecovery: stale, AuditDelivery: delivery}).RunOnce(context.Background())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunOnce error = %v, want context canceled", err)
	}
	if strings.Join(order, ",") != "operation" {
		t.Fatalf("order = %#v, want stop after runner context error", order)
	}
	if result.Summary().Operation.Scanned != 1 || result.Summary().Operation.Failed != 1 {
		t.Fatalf("summary = %#v, want operation partial result", result.Summary())
	}
}

func TestWorkerPackageDoesNotImportIntegrationPackages(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob package files: %v", err)
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
			for _, forbidden := range []string{"/jvs", "/webdav", "/mount", "/storage", "/postgres"} {
				if strings.Contains(path, forbidden) {
					t.Fatalf("%s imports forbidden integration package %q", name, path)
				}
			}
		}
	}
}

type workerContextKey string

type fakeExportReconcileRunner struct {
	name   string
	order  *[]string
	ctx    context.Context
	result exportreconcile.Result
	err    error
	after  func()
}

func (runner *fakeExportReconcileRunner) RunOnce(ctx context.Context) (exportreconcile.Result, error) {
	*runner.order = append(*runner.order, runner.name)
	runner.ctx = ctx
	if runner.after != nil {
		runner.after()
	}
	return runner.result, runner.err
}

type fakeOperationRunner struct {
	name   string
	order  *[]string
	ctx    context.Context
	result recovery.OperationBatchResult
	err    error
	after  func()
}

func (runner *fakeOperationRunner) RunOnce(ctx context.Context) (recovery.OperationBatchResult, error) {
	*runner.order = append(*runner.order, runner.name)
	runner.ctx = ctx
	if runner.after != nil {
		runner.after()
	}
	return runner.result, runner.err
}

type fakeWorkloadMountStaleRunner struct {
	name   string
	order  *[]string
	ctx    context.Context
	result workloadmount.StaleLeaseResult
	err    error
	after  func()
}

func (runner *fakeWorkloadMountStaleRunner) RunOnce(ctx context.Context) (workloadmount.StaleLeaseResult, error) {
	*runner.order = append(*runner.order, runner.name)
	runner.ctx = ctx
	if runner.after != nil {
		runner.after()
	}
	return runner.result, runner.err
}

type fakeStaleRunner struct {
	name   string
	order  *[]string
	ctx    context.Context
	result auditdelivery.StaleRecoveryResult
	err    error
}

func (runner *fakeStaleRunner) RunOnce(ctx context.Context) (auditdelivery.StaleRecoveryResult, error) {
	*runner.order = append(*runner.order, runner.name)
	runner.ctx = ctx
	return runner.result, runner.err
}

type fakeDeliveryRunner struct {
	name   string
	order  *[]string
	ctx    context.Context
	result auditdelivery.BatchResult
	err    error
}

func (runner *fakeDeliveryRunner) RunOnce(ctx context.Context) (auditdelivery.BatchResult, error) {
	*runner.order = append(*runner.order, runner.name)
	runner.ctx = ctx
	return runner.result, runner.err
}
