package volumebootstrap

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

func TestRunnerEnsureUsesVolumeEnsureLeaseCommitAndIsIdempotent(t *testing.T) {
	now := time.Date(2026, 5, 13, 10, 30, 0, 0, time.UTC)
	store := newFakeBootstrapStore()
	operationIDs := []string{"op_volume_bootstrap_1", "op_volume_bootstrap_2"}
	runner, err := NewRunner(Config{
		Store:           store,
		Owner:           "afscp-volume-bootstrap",
		CallerService:   "afscp-volume-bootstrap",
		AuthorizedActor: operations.Actor{Type: "system", ID: "afscp-volume-bootstrap"},
		Clock:           func() time.Time { return now },
		OperationID: func() string {
			next := operationIDs[0]
			operationIDs = operationIDs[1:]
			return next
		},
		AuditEventID:  func() string { return "evt_volume_bootstrap" },
		LeaseDuration: 5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	spec := bootstrapSpecFixture()

	first, err := runner.Ensure(context.Background(), spec)
	if err != nil {
		t.Fatalf("Ensure first: %v", err)
	}
	if first.Status != ResultStatusReady || first.Action != ActionEnsure || first.VolumeID != spec.VolumeID || first.OperationID != "op_volume_bootstrap_1" || first.OperationReused {
		t.Fatalf("first result = %#v, want fresh ready ensure", first)
	}
	if store.createCalls != 1 || store.acquireCalls != 1 || store.commitCalls != 1 {
		t.Fatalf("store create/acquire/commit = %d/%d/%d, want 1/1/1", store.createCalls, store.acquireCalls, store.commitCalls)
	}
	if store.lastSpec.Scope.OperationType != operations.OperationVolumeEnsure || store.lastSpec.NamespaceID != "" || store.lastSpec.Phase != operations.OperationPhaseVolumeEnsureValidate {
		t.Fatalf("operation spec = %#v, want volume-global volume_ensure validate", store.lastSpec)
	}
	if store.lastLease.Owner != "afscp-volume-bootstrap" || store.lastLease.Duration != 5*time.Minute {
		t.Fatalf("lease request = %#v, want bootstrap owner/duration", store.lastLease)
	}
	if store.volume.ID != spec.VolumeID || store.volume.Status != resources.VolumeStatusActive {
		t.Fatalf("committed volume = %#v, want active default volume", store.volume)
	}
	if store.event.Type != audit.EventTypeVolumeEnsure || store.event.Outcome != audit.OutcomeSucceeded || store.event.Resource.ID != spec.VolumeID {
		t.Fatalf("audit event = %#v, want succeeded volume ensure audit", store.event)
	}

	second, err := runner.Ensure(context.Background(), spec)
	if err != nil {
		t.Fatalf("Ensure second: %v", err)
	}
	if second.Status != ResultStatusReady || second.Action != ActionEnsure || !second.OperationReused || second.OperationID != "op_volume_bootstrap_1" {
		t.Fatalf("second result = %#v, want reused ready ensure", second)
	}
	if store.createCalls != 2 || store.acquireCalls != 1 || store.commitCalls != 1 {
		t.Fatalf("store create/acquire/commit after second = %d/%d/%d, want 2/1/1", store.createCalls, store.acquireCalls, store.commitCalls)
	}
}

func TestRunnerCheckReportsMissingVolume(t *testing.T) {
	now := time.Date(2026, 5, 13, 10, 30, 0, 0, time.UTC)
	runner, err := NewRunner(Config{
		Store:           newFakeBootstrapStore(),
		Owner:           "afscp-volume-bootstrap",
		CallerService:   "afscp-volume-bootstrap",
		AuthorizedActor: operations.Actor{Type: "system", ID: "afscp-volume-bootstrap"},
		Clock:           func() time.Time { return now },
		OperationID:     func() string { return "op_volume_bootstrap" },
		AuditEventID:    func() string { return "evt_volume_bootstrap" },
		LeaseDuration:   time.Minute,
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	result, err := runner.Check(context.Background(), bootstrapSpecFixture())
	if err == nil {
		t.Fatal("Check succeeded, want missing volume error")
	}
	if result.Status != ResultStatusNotReady || len(result.Findings) != 1 || result.Findings[0].Code != FindingVolumeMissing {
		t.Fatalf("result = %#v, want not_ready volume_missing", result)
	}
}

func bootstrapSpecFixture() VolumeSpec {
	return VolumeSpec{
		VolumeID:       "vol_default",
		Backend:        resources.VolumeBackendJuiceFS,
		IsolationClass: resources.VolumeIsolationShared,
		Status:         resources.VolumeStatusActive,
		Capabilities: map[string]any{
			"webdav_export":             true,
			"workload_mount":            true,
			"jvs_external_control_root": true,
			"directory_quota":           false,
		},
	}
}

type fakeBootstrapStore struct {
	createCalls  int
	acquireCalls int
	commitCalls  int
	lastSpec     operations.QueuedOperationSpec
	lastLease    operations.LeaseRequest
	records      map[string]operations.OperationRecord
	scopes       map[string]string
	volume       resources.Volume
	event        audit.Event
}

func newFakeBootstrapStore() *fakeBootstrapStore {
	return &fakeBootstrapStore{records: map[string]operations.OperationRecord{}, scopes: map[string]string{}}
}

func (store *fakeBootstrapStore) CreateOrReuseOperation(_ context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	store.createCalls++
	store.lastSpec = spec
	if operationID, ok := store.scopes[spec.Scope.String()]; ok {
		record := store.records[operationID]
		if record.RequestHash != spec.RequestHash {
			return operations.IdempotencyResolution{}, operations.ErrIdempotencyConflict
		}
		return operations.IdempotencyResolution{Operation: record.Sanitized(), Existing: true, Reused: true}, nil
	}
	record, err := operations.NewQueuedOperationRecord(spec)
	if err != nil {
		return operations.IdempotencyResolution{}, err
	}
	store.records[record.ID] = record
	store.scopes[record.IdempotencyScope] = record.ID
	return operations.IdempotencyResolution{Operation: record.Sanitized()}, nil
}

func (store *fakeBootstrapStore) AcquireVolumeEnsureOperationLease(_ context.Context, operationID string, request operations.LeaseRequest) (operations.OperationRecord, error) {
	store.acquireCalls++
	store.lastLease = request
	record, ok := store.records[operationID]
	if !ok {
		return operations.OperationRecord{}, sql.ErrNoRows
	}
	decision := operations.AcquireLease(record, request)
	if !decision.Allowed {
		if decision.Error != nil {
			return operations.OperationRecord{}, decision.Error
		}
		return operations.OperationRecord{}, errors.New("lease unavailable")
	}
	store.records[operationID] = decision.Record
	return decision.Record, nil
}

func (store *fakeBootstrapStore) CommitVolumeEnsureWithLease(_ context.Context, volume resources.Volume, record operations.SanitizedOperationRecord, _ string, _ time.Time, event audit.Event) (resources.Volume, operations.OperationRecord, error) {
	store.commitCalls++
	store.volume = volume
	store.event = event
	committed := record.Record()
	store.records[committed.ID] = committed
	return volume, committed, nil
}

func (store *fakeBootstrapStore) GetVolume(_ context.Context, volumeID string) (resources.Volume, error) {
	if store.volume.ID == volumeID {
		return store.volume, nil
	}
	return resources.Volume{}, sql.ErrNoRows
}
