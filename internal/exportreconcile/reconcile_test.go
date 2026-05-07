package exportreconcile

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/exportaccess"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
)

func TestRunOnceReconcilesZeroCountRevokingAndExpiredSessions(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{
		candidates: []exportaccess.Session{
			exportSessionCandidate(now, "export_revoke01", sessionstate.ExportStatusRevoking, now.Add(time.Hour)),
			exportSessionCandidate(now, "export_expire01", sessionstate.ExportStatusActive, now.Add(-time.Second)),
		},
	}
	runner := New(Config{
		Store:        store,
		Owner:        "export-worker",
		Limit:        10,
		Clock:        func() time.Time { return now },
		AuditEventID: func() string { return "evt_export_reconcile" },
	})

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Scanned != 2 || result.Terminalized != 2 || result.Failed != 0 {
		t.Fatalf("result = %#v, want two terminalized", result)
	}
	if len(store.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(store.requests))
	}
	if store.requests[0].TargetStatus != sessionstate.ExportStatusRevoked || store.requests[1].TargetStatus != sessionstate.ExportStatusExpired {
		t.Fatalf("target statuses = %s/%s", store.requests[0].TargetStatus, store.requests[1].TargetStatus)
	}
	for _, request := range store.requests {
		if request.ActiveRequestCount != 0 || request.ActiveWriteCount != 0 {
			t.Fatalf("reconcile request counts = %d/%d, want zero", request.ActiveRequestCount, request.ActiveWriteCount)
		}
		if request.Operation.Type != operations.OperationExportSessionReconcile || request.Operation.State != operations.OperationStateSucceeded || request.Operation.Phase != operations.OperationPhaseExportSessionReconcileCommitted {
			t.Fatalf("operation = %#v, want committed export reconcile", request.Operation)
		}
		if request.Operation.CallerService != "export-worker" || request.Operation.Resource.Type != "export" || request.Operation.Resource.ID != request.ExportID {
			t.Fatalf("operation context = %#v, want owner export resource", request.Operation)
		}
		if request.Audit.Type != audit.EventTypeExportSessionReconcile || request.Audit.OperationID != request.Operation.ID || request.Audit.Resource.ID != request.ExportID {
			t.Fatalf("audit = %#v, want matching reconcile audit", request.Audit)
		}
	}
}

func TestRunOnceRecoversStaleRuntimeRequestsBeforeTerminalList(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{
		candidates: []exportaccess.Session{exportSessionCandidate(now, "export_revoke01", sessionstate.ExportStatusRevoking, now.Add(time.Hour))},
		staleRecovery: exportaccess.StaleRuntimeRequestRecoveryResult{
			Recovered:       1,
			RecoveredWrites: 1,
		},
	}
	runner := New(Config{
		Store: store,
		Owner: "export-worker",
		Limit: 10,
		Clock: func() time.Time { return now },
	})

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if got := strings.Join(store.callOrder, ","); got != "recover_stale_runtime_requests,list_terminal_reconcile,reconcile_terminal" {
		t.Fatalf("call order = %q, want stale recovery before terminal reconcile", got)
	}
	if result.RecoveredRuntimeRequests != 1 || result.RecoveredRuntimeWrites != 1 || result.Terminalized != 1 {
		t.Fatalf("result = %#v, want stale runtime recovery evidence and terminal reconcile", result)
	}
	if !store.staleRecoveryNow.Equal(now) || store.staleRecoveryLimit != 10 {
		t.Fatalf("stale recovery args = %v/%d, want %v/10", store.staleRecoveryNow, store.staleRecoveryLimit, now)
	}
}

func TestRunOnceTreatsNoRowsAsRaceLost(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{
		candidates: []exportaccess.Session{exportSessionCandidate(now, "export_race01", sessionstate.ExportStatusRevoking, now.Add(time.Hour))},
		err:        sql.ErrNoRows,
	}
	runner := New(Config{Store: store, Owner: "export-worker", Limit: 10, Clock: func() time.Time { return now }})

	result, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Scanned != 1 || result.RaceLost != 1 || result.Terminalized != 0 || result.Failed != 0 {
		t.Fatalf("result = %#v, want race lost", result)
	}
}

func TestRunOnceValidatesConfig(t *testing.T) {
	_, err := New(Config{}).RunOnce(context.Background())
	if err == nil || !strings.Contains(err.Error(), "store") {
		t.Fatalf("RunOnce error = %v, want store config error", err)
	}
}

type fakeStore struct {
	candidates         []exportaccess.Session
	requests           []exportaccess.ReconcileRequest
	err                error
	callOrder          []string
	staleRecovery      exportaccess.StaleRuntimeRequestRecoveryResult
	staleRecoveryNow   time.Time
	staleRecoveryLimit int
}

func (store *fakeStore) RecoverStaleExportRuntimeRequests(_ context.Context, request exportaccess.StaleRuntimeRequestRecovery) (exportaccess.StaleRuntimeRequestRecoveryResult, error) {
	store.callOrder = append(store.callOrder, "recover_stale_runtime_requests")
	store.staleRecoveryNow = request.Now
	store.staleRecoveryLimit = request.Limit
	return store.staleRecovery, nil
}

func (store *fakeStore) ListExportSessionsForTerminalReconcile(context.Context, time.Time, int) ([]exportaccess.Session, error) {
	store.callOrder = append(store.callOrder, "list_terminal_reconcile")
	out := make([]exportaccess.Session, len(store.candidates))
	copy(out, store.candidates)
	return out, nil
}

func (store *fakeStore) ReconcileExportSessionTerminal(_ context.Context, request exportaccess.ReconcileRequest) (exportaccess.ReconcileResult, error) {
	store.callOrder = append(store.callOrder, "reconcile_terminal")
	store.requests = append(store.requests, request)
	if store.err != nil {
		return exportaccess.ReconcileResult{}, store.err
	}
	session := exportSessionCandidate(request.ObservedAt, request.ExportID, request.TargetStatus, request.ObservedAt)
	session.TerminalObservedAt = &request.ObservedAt
	return exportaccess.ReconcileResult{Session: session, Operation: request.Operation}, nil
}

func exportSessionCandidate(now time.Time, exportID string, status sessionstate.ExportStatus, expiresAt time.Time) exportaccess.Session {
	return exportaccess.Session{
		ID:                     exportID,
		NamespaceID:            "ns_alpha01",
		RepoID:                 "repo_alpha01",
		Protocol:               exportaccess.ProtocolWebDAV,
		Mode:                   sessionstate.AccessModeReadWrite,
		Status:                 status,
		ExpiresAt:              expiresAt,
		CreatedByCallerService: "product-caller",
		CreatedByActor:         exportaccess.Actor{Type: "user", ID: "user_alpha"},
		CreatedAt:              now.Add(-time.Hour),
		UpdatedAt:              now.Add(-time.Minute),
	}
}
