package restorereconcile

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRestoreReconciliationDecisionDeniesDangerousWritesUntilSafe(t *testing.T) {
	run := Run{ID: "rrun_123", Mode: ModeReconciling}
	if !DangerousWritesBlocked(run) {
		t.Fatalf("DangerousWritesBlocked(%#v) = false, want true", run)
	}
	if DangerousWritesBlocked(Run{ID: "rrun_123", Mode: ModeCompleted}) {
		t.Fatal("completed reconciliation run must not block dangerous writes")
	}
}

func TestRestoreReconciliationDecisionPurgedStoragePresentDoesNotResurrect(t *testing.T) {
	decision := DecideObservation(Observation{
		RepoID:                 "repo_purged01",
		ExpectedRepoStatus:     RepoStatusPurged,
		ObservedStoragePresent: true,
		EvidenceRef:            "restore-reconciliation://run/rrun_123/repo/repo_purged01",
	})
	if decision.Result != ObservationResultMismatch || decision.Reason != ReasonPurgedRepoStoragePresent {
		t.Fatalf("decision = %#v, want purged-storage-present mismatch", decision)
	}
	if decision.AllowsResurrection {
		t.Fatalf("decision = %#v, must never allow purged repo resurrection", decision)
	}
}

func TestRestoreReconciliationEvidenceRedactsSensitiveMaterial(t *testing.T) {
	evidence, err := RedactedEvidence(Observation{
		RunID:              "rrun_123",
		RepoID:             "repo_123",
		ObservedGeneration: "snapshot-generation-123",
		EvidenceRef:        "docs/runbooks/restore.md#rrun-123",
	})
	if err != nil {
		t.Fatalf("RedactedEvidence: %v", err)
	}
	rendered := strings.ToLower(evidence.ObservedGeneration)
	for _, forbidden := range []string{"/raw/root", ".jvs", "token=secret", "secret"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("evidence leaked %q: %#v", forbidden, evidence)
		}
	}
}

func TestRestoreReconciliationRejectsSecretShapedEvidenceRefsAndMarkers(t *testing.T) {
	observation := Observation{
		RunID:              "rrun_123",
		RepoID:             "repo_123",
		ObservedGeneration: "snapshot-ok",
		ObservedSnapshotID: "/raw/root/.jvs/snapshot",
		EvidenceRef:        "SecretRef:prod/token/password",
	}
	if _, err := RedactedEvidence(observation); err == nil {
		t.Fatal("RedactedEvidence succeeded, want secret-shaped marker/evidence rejected before audit/store")
	}
}

func TestRestoreReconciliationCleanObservationRequiresMarkersAndEvidence(t *testing.T) {
	target := Target{
		RunID:                     "rrun_123",
		RepoID:                    "repo_123",
		NamespaceID:               "ns_123",
		ExpectedRepoStatus:        RepoStatusActive,
		ExpectedStorageGeneration: "gen-1",
		ExpectedSnapshotID:        "snapshot-1",
		ExpectedTombstoneMarker:   "none",
		ExpectedPurgeMarker:       "none",
	}
	observation := Observation{
		RunID:                   "rrun_123",
		RepoID:                  "repo_123",
		NamespaceID:             "ns_123",
		ExpectedRepoStatus:      RepoStatusActive,
		ObservedStoragePresent:  true,
		ObservedGeneration:      "gen-2",
		ObservedSnapshotID:      "snapshot-1",
		ObservedTombstoneMarker: "none",
		ObservedPurgeMarker:     "none",
		Result:                  ObservationResultClean,
		EvidenceRef:             "restore-reconciliation://run/rrun_123/repo/repo_123",
	}
	decision := DecideObservationForTarget(target, observation)
	if decision.Result != ObservationResultMismatch || decision.Reason != ReasonMetadataStorageMismatch {
		t.Fatalf("decision = %#v, want marker mismatch", decision)
	}
	observation.ObservedGeneration = "gen-1"
	observation.EvidenceRef = ""
	decision = DecideObservationForTarget(target, observation)
	if decision.Result != ObservationResultMismatch || decision.Reason == "" {
		t.Fatalf("decision = %#v, want missing-evidence mismatch", decision)
	}
}

func TestRestoreReconciliationRunOnceCompletesCleanRun(t *testing.T) {
	store := &fakeStore{
		run: Run{ID: "rrun_123", Mode: ModeReconciling},
		targets: []Target{
			{RunID: "rrun_123", RepoID: "repo_123", NamespaceID: "ns_123", ExpectedRepoStatus: RepoStatusActive, ExpectedStorageGeneration: "gen-1", ExpectedSnapshotID: "snapshot-1", ExpectedTombstoneMarker: "none", ExpectedPurgeMarker: "none"},
		},
		observations: []Observation{
			{RunID: "rrun_123", RepoID: "repo_123", NamespaceID: "ns_123", ExpectedRepoStatus: RepoStatusActive, ObservedStoragePresent: true, ObservedGeneration: "gen-1", ObservedSnapshotID: "snapshot-1", ObservedTombstoneMarker: "none", ObservedPurgeMarker: "none", Result: ObservationResultClean, EvidenceRef: "restore-reconciliation://clean/repo_123"},
		},
	}
	result, err := NewRunner(Config{Store: store, ExplicitlyEnabled: true, Clock: fixedClock}).RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Completed != 1 || store.completedRunID != "rrun_123" {
		t.Fatalf("result/store = %#v/%q, want completed clean run", result, store.completedRunID)
	}
}

func TestRestoreReconciliationRunOnceFailsClosedWhenTargetSetIsEmptyOrMissingObservation(t *testing.T) {
	tests := []struct {
		name         string
		targets      []Target
		observations []Observation
	}{
		{name: "no targets"},
		{
			name:    "missing observation",
			targets: []Target{{RunID: "rrun_123", RepoID: "repo_123", NamespaceID: "ns_123", ExpectedRepoStatus: RepoStatusActive, ExpectedStorageGeneration: "gen-1", ExpectedSnapshotID: "snapshot-1", ExpectedTombstoneMarker: "none", ExpectedPurgeMarker: "none"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{run: Run{ID: "rrun_123", Mode: ModeReconciling}, targets: tt.targets, observations: tt.observations}
			result, err := NewRunner(Config{Store: store, ExplicitlyEnabled: true, Clock: fixedClock}).RunOnce(context.Background())
			if err == nil {
				t.Fatalf("RunOnce succeeded with result %#v, want fail closed", result)
			}
			if store.completedRunID != "" {
				t.Fatalf("completed run = %q, want no completion", store.completedRunID)
			}
		})
	}
}

func TestRestoreReconciliationRunOnceObservedMarkerMismatchBlocksAndDoesNotComplete(t *testing.T) {
	target := Target{
		RunID:                     "rrun_123",
		RepoID:                    "repo_123",
		NamespaceID:               "ns_123",
		ExpectedRepoStatus:        RepoStatusActive,
		ExpectedStorageGeneration: "status:active",
		ExpectedSnapshotID:        "repo:repo_123",
		ExpectedTombstoneMarker:   "none",
		ExpectedPurgeMarker:       "none",
	}
	store := &fakeStore{
		run:     Run{ID: "rrun_123", Mode: ModeReconciling},
		targets: []Target{target},
		observations: []Observation{{
			RunID:                   target.RunID,
			RepoID:                  target.RepoID,
			NamespaceID:             target.NamespaceID,
			ExpectedRepoStatus:      target.ExpectedRepoStatus,
			ObservedStoragePresent:  true,
			ObservedGeneration:      "status:archived",
			ObservedSnapshotID:      "repo:repo_123",
			ObservedTombstoneMarker: "none",
			ObservedPurgeMarker:     "none",
			Result:                  ObservationResultClean,
			EvidenceRef:             "restore-reconciliation://run/rrun_123/repo/repo_123",
		}},
	}

	result, err := NewRunner(Config{Store: store, ExplicitlyEnabled: true, Owner: "restore-reconcile-worker", AuditEventID: func() string { return "evt_restore_reconcile" }, Clock: fixedClock}).RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Blocked != 1 || store.completedRunID != "" || len(store.mismatchCommits) != 1 {
		t.Fatalf("result/completed/commits = %#v/%q/%#v, want mismatch block without complete", result, store.completedRunID, store.mismatchCommits)
	}
	if store.mismatchCommits[0].Reason != ReasonMetadataStorageMismatch {
		t.Fatalf("commit reason = %q, want metadata/storage mismatch", store.mismatchCommits[0].Reason)
	}
}

func TestRestoreReconciliationRunOnceMismatchBlocksAndMarksIntervention(t *testing.T) {
	store := &fakeStore{
		run: Run{ID: "rrun_123", Mode: ModeReconciling},
		targets: []Target{
			{RunID: "rrun_123", RepoID: "repo_123", NamespaceID: "ns_123", ExpectedRepoStatus: RepoStatusActive, ExpectedStorageGeneration: "gen-1", ExpectedSnapshotID: "snapshot-1", ExpectedTombstoneMarker: "none", ExpectedPurgeMarker: "none"},
		},
		observations: []Observation{
			{RunID: "rrun_123", RepoID: "repo_123", NamespaceID: "ns_123", Result: ObservationResultMismatch, Reason: ReasonMetadataStorageMismatch, EvidenceRef: "restore-reconciliation://mismatch/repo_123"},
		},
	}
	result, err := NewRunner(Config{Store: store, ExplicitlyEnabled: true, Owner: "restore-reconcile-worker", AuditEventID: func() string { return "evt_restore_reconcile" }, Clock: fixedClock}).RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Blocked != 1 || len(store.mismatchCommits) != 1 {
		t.Fatalf("result/commits = %#v/%#v, want mismatch blocked intervention", result, store.mismatchCommits)
	}
	if got := store.mismatchCommits[0]; got.RepoID != "repo_123" || got.Audit.EventID == "" {
		t.Fatalf("mismatch commit = %#v, want repo intervention with audit", got)
	}
}

func TestRunOnceRestoreReconciliationOnlyRunsWhenExplicitlyEnabled(t *testing.T) {
	store := &fakeStore{run: Run{ID: "rrun_123", Mode: ModeReconciling}}
	result, err := NewRunner(Config{Store: store, ExplicitlyEnabled: false}).RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce disabled: %v", err)
	}
	if result.Scanned != 0 || store.listCalls != 0 {
		t.Fatalf("disabled result/list calls = %#v/%d, want no-op", result, store.listCalls)
	}
}

func TestRestoreReconciliationRunOncePurgedRepoNeverResurrects(t *testing.T) {
	observation := Observation{RunID: "rrun_123", RepoID: "repo_purged01", ExpectedRepoStatus: RepoStatusPurged, ObservedStoragePresent: true, EvidenceRef: "restore-reconciliation://purged/repo_purged01"}
	store := &fakeStore{run: Run{ID: "rrun_123", Mode: ModeReconciling}, targets: []Target{{RunID: "rrun_123", RepoID: "repo_purged01", NamespaceID: "ns_123", ExpectedRepoStatus: RepoStatusPurged, ExpectedStorageGeneration: "purged", ExpectedSnapshotID: "snapshot-1", ExpectedTombstoneMarker: "tombstone-1", ExpectedPurgeMarker: "purge-1"}}, observations: []Observation{observation}}
	result, err := NewRunner(Config{Store: store, ExplicitlyEnabled: true, Owner: "restore-reconcile-worker", AuditEventID: func() string { return "evt_restore_reconcile" }, Clock: fixedClock}).RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Blocked != 1 || store.resurrectCalls != 0 {
		t.Fatalf("result/resurrect = %#v/%d, want blocked with no resurrection", result, store.resurrectCalls)
	}
	if len(store.mismatchCommits) != 1 || store.mismatchCommits[0].Reason != ReasonPurgedRepoStoragePresent {
		t.Fatalf("mismatch commits = %#v, want purged storage intervention", store.mismatchCommits)
	}
}

func fixedClock() time.Time {
	return time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
}

type fakeStore struct {
	run             Run
	targets         []Target
	observations    []Observation
	listCalls       int
	completedRunID  string
	mismatchCommits []MismatchCommit
	resurrectCalls  int
}

func (store *fakeStore) ActiveRun(context.Context) (Run, error) {
	store.listCalls++
	return store.run, nil
}

func (store *fakeStore) ListObservations(context.Context, string) ([]Observation, error) {
	return store.observations, nil
}

func (store *fakeStore) ListTargets(context.Context, string) ([]Target, error) {
	return store.targets, nil
}

func (store *fakeStore) ObserveTarget(_ context.Context, target Target) (Observation, error) {
	for _, observation := range store.observations {
		if observation.RepoID == target.RepoID {
			return observation, nil
		}
	}
	return Observation{}, ErrObservationMissing
}

func (store *fakeStore) CompleteRun(context.Context, string, time.Time) error {
	store.completedRunID = store.run.ID
	return nil
}

func (store *fakeStore) CommitMismatch(_ context.Context, commit MismatchCommit) error {
	store.mismatchCommits = append(store.mismatchCommits, commit)
	return nil
}
