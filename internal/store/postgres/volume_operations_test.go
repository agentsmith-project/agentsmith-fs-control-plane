package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

func TestCommitVolumeEnsureWithLeaseAtomicallyUpsertsVolumeUpdatesOperationAndAppendsAudit(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	volume := volumeFixture()
	volume.ID = "vol_123"
	volume.CreatedAt = now
	volume.UpdatedAt = now
	record := operationFixture(now.Add(-time.Hour))
	record.ID = "op-volume"
	record.Type = operations.OperationVolumeEnsure
	record.State = operations.OperationStateSucceeded
	record.Phase = operations.OperationPhaseVolumeEnsureCommitted
	record.Resource = operations.ResourceRef{Type: "volume", ID: volume.ID}
	record.FinishedAt = &now
	exec := &fakeExecutor{row: fakeRow{values: append(volumeRowValues(volume), operationRowValues(record.SanitizedForPersistence().Record())...)}}
	st := &Store{exec: exec}

	gotVolume, gotOperation, err := st.CommitVolumeEnsureWithLease(context.Background(), volume, record.SanitizedForPersistence(), "worker-a", now, volumeEnsureAuditEvent("audit-volume", "op-volume", volume.ID, now))
	if err != nil {
		t.Fatalf("CommitVolumeEnsureWithLease: %v", err)
	}
	if gotVolume.ID != volume.ID || gotOperation.ID != record.ID || gotOperation.State != operations.OperationStateSucceeded {
		t.Fatalf("commit returned volume/operation = %#v/%#v", gotVolume, gotOperation)
	}
	assertSQLContainsInOrder(t, exec.query,
		"WITH updated_operation AS (",
		"UPDATE operations SET",
		"operation_type = 'volume_ensure'",
		"phase = 'validate_volume_ensure'",
		"namespace_id = ''",
		"resource_type = 'volume'",
		"resource_id = $14",
		"RETURNING",
		"), upserted_volume AS (",
		"INSERT INTO volumes",
		"SELECT $19, $20, $21, $22, $23, $24, $25 FROM updated_operation",
		"ON CONFLICT (volume_id) DO UPDATE SET",
		"RETURNING",
		"), inserted_audit AS (",
		"INSERT INTO audit_outbox",
		"FROM updated_operation, upserted_volume",
		") SELECT",
	)
	if exec.queryRowCalls != 1 || exec.execCalls != 0 || exec.queryCalls != 0 {
		t.Fatalf("executor calls queryRow/exec/query = %d/%d/%d, want 1/0/0", exec.queryRowCalls, exec.execCalls, exec.queryCalls)
	}
}

func TestCommitVolumeEnsureWithLeaseRejectsInvalidRequestBeforeSQL(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	volume := volumeFixture()
	volume.ID = "vol_123"
	record := operationFixture(now.Add(-time.Hour))
	record.ID = "op-volume"
	record.Type = operations.OperationVolumeEnsure
	record.State = operations.OperationStateSucceeded
	record.Phase = operations.OperationPhaseVolumeEnsureCommitted
	record.Resource = operations.ResourceRef{Type: "volume", ID: volume.ID}
	event := volumeEnsureAuditEvent("audit-volume", "op-volume", volume.ID, now)

	tests := []struct {
		name   string
		volume resources.Volume
		record operations.OperationRecord
		event  audit.Event
	}{
		{name: "invalid volume", volume: func() resources.Volume {
			edited := volume
			edited.Status = "other"
			return edited
		}(), record: record, event: event},
		{name: "wrong type", volume: volume, record: func() operations.OperationRecord {
			edited := record
			edited.Type = operations.OperationRepoCreate
			return edited
		}(), event: event},
		{name: "wrong state", volume: volume, record: func() operations.OperationRecord {
			edited := record
			edited.State = operations.OperationStateRunning
			return edited
		}(), event: event},
		{name: "wrong terminal phase", volume: volume, record: func() operations.OperationRecord {
			edited := record
			edited.Phase = operations.OperationPhaseVolumeEnsureValidate
			return edited
		}(), event: event},
		{name: "wrong resource", volume: volume, record: func() operations.OperationRecord {
			edited := record
			edited.Resource.ID = "vol_other"
			return edited
		}(), event: event},
		{name: "namespace-bound operation", volume: volume, record: func() operations.OperationRecord {
			edited := record
			edited.NamespaceID = "ns_alpha01"
			return edited
		}(), event: event},
		{name: "wrong audit event type", volume: volume, record: record, event: func() audit.Event {
			edited := event
			edited.Type = audit.EventTypeNamespaceUpsert
			return edited
		}()},
		{name: "wrong audit outcome", volume: volume, record: record, event: func() audit.Event {
			edited := event
			edited.Outcome = audit.OutcomeFailed
			return edited
		}()},
		{name: "audit operation mismatch", volume: volume, record: record, event: func() audit.Event {
			edited := event
			edited.OperationID = "op-other"
			return edited
		}()},
		{name: "audit resource mismatch", volume: volume, record: record, event: func() audit.Event {
			edited := event
			edited.Resource.ID = "vol_other"
			return edited
		}()},
		{name: "audit resource namespace set", volume: volume, record: record, event: func() audit.Event {
			edited := event
			edited.Resource.NamespaceID = "ns_alpha01"
			return edited
		}()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{}
			st := &Store{exec: exec}

			_, _, err := st.CommitVolumeEnsureWithLease(context.Background(), tt.volume, tt.record.SanitizedForPersistence(), "worker-a", now, tt.event)
			if err == nil {
				t.Fatal("CommitVolumeEnsureWithLease succeeded, want error")
			}
			if exec.query != "" {
				t.Fatalf("issued SQL for invalid request: %s", exec.query)
			}
		})
	}
}

func TestCommitVolumeEnsureWithLeaseNoRowsFailsClosed(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	volume := volumeFixture()
	volume.ID = "vol_123"
	record := operationFixture(now.Add(-time.Hour))
	record.ID = "op-volume"
	record.Type = operations.OperationVolumeEnsure
	record.State = operations.OperationStateSucceeded
	record.Phase = operations.OperationPhaseVolumeEnsureCommitted
	record.Resource = operations.ResourceRef{Type: "volume", ID: volume.ID}
	record.FinishedAt = &now
	st := &Store{exec: &fakeExecutor{row: fakeRow{err: sql.ErrNoRows}}}

	_, _, err := st.CommitVolumeEnsureWithLease(context.Background(), volume, record.SanitizedForPersistence(), "worker-a", now, volumeEnsureAuditEvent("audit-volume", "op-volume", volume.ID, now))
	if !errors.Is(err, operations.ErrLeaseUnavailable) || !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("CommitVolumeEnsureWithLease error = %v, want ErrLeaseUnavailable and sql.ErrNoRows", err)
	}
}

func volumeEnsureAuditEvent(eventID, operationID, volumeID string, now time.Time) audit.Event {
	return audit.NewEvent(audit.Event{
		EventID:         eventID,
		Type:            audit.EventTypeVolumeEnsure,
		Time:            now,
		CallerService:   "afscp-api",
		AuthorizedActor: audit.Actor{Type: "system", ID: "svc-alpha"},
		CorrelationID:   "corr-alpha",
		OperationID:     operationID,
		Resource:        audit.Resource{Type: "volume", ID: volumeID},
		Outcome:         audit.OutcomeSucceeded,
		Reason:          "volume_ensure_committed",
		Details:         map[string]any{"volume_id": volumeID, "safe": strings.ToUpper("ok")},
	})
}

var _ = resources.Volume{}
