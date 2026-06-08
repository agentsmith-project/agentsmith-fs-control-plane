package postgres

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRecordSavePointCreateRecoveryCapabilityUpsertsShortLivedHeartbeat(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	exec := &fakeExecutor{}
	st := &Store{exec: exec}

	if err := st.RecordSavePointCreateRecoveryCapability(context.Background(), " worker-a ", now, now.Add(30*time.Second)); err != nil {
		t.Fatalf("RecordSavePointCreateRecoveryCapability: %v", err)
	}
	if exec.execCalls != 1 || len(exec.args) != 4 {
		t.Fatalf("exec calls/args = %d/%#v, want one heartbeat upsert", exec.execCalls, exec.args)
	}
	if exec.args[0] != savePointCreateRecoveryCapability || exec.args[1] != "worker-a" {
		t.Fatalf("args = %#v, want capability and trimmed owner", exec.args)
	}
	assertSQLContainsInOrder(t, exec.query,
		"INSERT INTO worker_capability_heartbeats",
		"ON CONFLICT (capability) DO UPDATE",
		"expires_at",
	)
}

func TestRecordSavePointCreateRecoveryCapabilityRejectsInvalidHeartbeat(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		owner     string
		expiresAt time.Time
	}{
		{name: "missing owner", owner: " \t", expiresAt: now.Add(time.Second)},
		{name: "expired", owner: "worker-a", expiresAt: now},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{}
			st := &Store{exec: exec}
			if err := st.RecordSavePointCreateRecoveryCapability(context.Background(), tt.owner, now, tt.expiresAt); err == nil {
				t.Fatal("RecordSavePointCreateRecoveryCapability succeeded, want validation error")
			}
			if exec.execCalls != 0 {
				t.Fatalf("exec calls = %d, want fail before SQL", exec.execCalls)
			}
		})
	}
}

func TestSavePointCreateRecoveryCapabilityReadyScopesLiveCapability(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	exec := &fakeExecutor{row: fakeRow{values: []any{true}}}
	st := &Store{exec: exec}

	ready, err := st.SavePointCreateRecoveryCapabilityReady(context.Background(), now)
	if err != nil {
		t.Fatalf("SavePointCreateRecoveryCapabilityReady: %v", err)
	}
	if !ready {
		t.Fatal("ready = false, want true")
	}
	if exec.queryRowCalls != 1 || len(exec.args) != 2 || exec.args[0] != savePointCreateRecoveryCapability || exec.args[1] != now {
		t.Fatalf("query calls/args = %d/%#v, want scoped readiness query", exec.queryRowCalls, exec.args)
	}
	assertSQLContainsInOrder(t, exec.query,
		"SELECT EXISTS",
		"FROM worker_capability_heartbeats",
		"capability = $1",
		"observed_at <= $2",
		"expires_at > $2",
	)
	for _, forbidden := range []string{"operations", "repos", "export_sessions", "workload_mount_bindings"} {
		if strings.Contains(exec.query, forbidden) {
			t.Fatalf("capability readiness query includes unrelated table %q: %s", forbidden, exec.query)
		}
	}
}
