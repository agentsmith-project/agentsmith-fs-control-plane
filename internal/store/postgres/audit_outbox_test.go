package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store"
)

func TestStoreImplementsAuditOutboxContracts(t *testing.T) {
	var _ store.AuditSink = (*Store)(nil)
	var _ store.AuditOutboxDeliveryStore = (*Store)(nil)
}

func TestAppendAuditEventInsertsPendingOutboxRecord(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 45, 0, 0, time.UTC)
	eventTime := time.Date(2026, 5, 4, 12, 30, 0, 0, time.UTC)
	exec := &fakeExecutor{}
	st := &Store{exec: exec, clock: func() time.Time { return now }}
	event := audit.Event{
		EventID:         "audit-alpha",
		Type:            audit.EventTypeRepoCreate,
		Time:            eventTime,
		CallerService:   "afscp-api",
		AuthorizedActor: audit.Actor{Type: "system", ID: "svc-alpha"},
		CorrelationID:   "corr-alpha",
		OperationID:     "op-alpha",
		Resource:        audit.Resource{Type: "repo", ID: "repo-alpha", Path: "/repo?token=audit-path-secret"},
		Outcome:         audit.OutcomeSucceeded,
		Reason:          "queued token=audit-reason-secret",
		Details:         map[string]any{"authorization": "Bearer audit-detail-secret", "safe": "visible"},
	}

	if err := st.AppendAuditEvent(context.Background(), event); err != nil {
		t.Fatalf("AppendAuditEvent: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"INSERT INTO audit_outbox",
		"audit_event_id", "event_type", "event_time", "payload_json", "delivery_status",
		"delivery_attempt", "next_retry_at", "last_error", "created_at", "updated_at", "delivered_at",
	)
	if len(exec.args) != 11 {
		t.Fatalf("arg count = %d, want 11: %#v", len(exec.args), exec.args)
	}
	if exec.args[0] != "audit-alpha" || exec.args[1] != string(audit.EventTypeRepoCreate) || exec.args[2] != eventTime {
		t.Fatalf("event boundary args = %#v", exec.args[:3])
	}
	payload := mustJSONMap(t, exec.args[3])
	if payload["event_id"] != "audit-alpha" || payload["reason"] == "queued token=audit-reason-secret" {
		t.Fatalf("payload not sanitized or missing event id: %#v", payload)
	}
	if details := payload["details"].(map[string]any); details["authorization"] != "[REDACTED]" || details["safe"] != "visible" {
		t.Fatalf("payload details = %#v, want sanitized authorization and visible safe", details)
	}
	rendered := strings.ToLower(renderArgs(t, exec.args...))
	for _, forbidden := range []string{"audit-path-secret", "audit-reason-secret", "audit-detail-secret"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("AppendAuditEvent args leaked %q in %s", forbidden, rendered)
		}
	}
	if exec.args[4] != string(audit.OutboxStatusPending) || exec.args[5] != 0 {
		t.Fatalf("status/attempt args = %#v/%#v, want pending/0", exec.args[4], exec.args[5])
	}
	if exec.args[6] != nil || exec.args[7] != "" || exec.args[10] != nil {
		t.Fatalf("retry/error/delivered args = %#v/%#v/%#v, want nil/empty/nil", exec.args[6], exec.args[7], exec.args[10])
	}
	if exec.args[8] != now || exec.args[9] != now {
		t.Fatalf("created/updated args = %#v/%#v, want now", exec.args[8], exec.args[9])
	}
}

func TestListDueAuditOutboxRecordsReadsOnlyDueRowsWithStableLimit(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	retryAt := now.Add(-time.Minute)
	createdAt := now.Add(-10 * time.Minute)
	exec := &fakeExecutor{
		rows: fakeRows{rows: []fakeRow{
			{values: auditOutboxRowValues(audit.OutboxRecord{
				EventID:         "audit-alpha",
				EventType:       audit.EventTypeRepoCreate,
				EventTime:       now.Add(-9 * time.Minute),
				PayloadJSON:     []byte(`{"event_id":"audit-alpha"}`),
				Status:          audit.OutboxStatusPending,
				DeliveryAttempt: 0,
				CreatedAt:       createdAt,
				UpdatedAt:       createdAt,
			})},
			{values: auditOutboxRowValues(audit.OutboxRecord{
				EventID:         "audit-beta",
				EventType:       audit.EventTypeExportCreate,
				EventTime:       now.Add(-8 * time.Minute),
				PayloadJSON:     []byte(`{"event_id":"audit-beta"}`),
				Status:          audit.OutboxStatusRetryWait,
				DeliveryAttempt: 2,
				NextRetryAt:     &retryAt,
				LastError:       "retry me",
				CreatedAt:       createdAt.Add(time.Minute),
				UpdatedAt:       createdAt.Add(time.Minute),
			})},
		}},
	}
	st := &Store{exec: exec}

	got, err := st.ListDueAuditOutboxRecords(context.Background(), now, 25)
	if err != nil {
		t.Fatalf("ListDueAuditOutboxRecords: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"SELECT",
		"audit_event_id", "event_type", "event_time", "payload_json", "delivery_status",
		"FROM audit_outbox",
		"WHERE",
		"delivery_status = $1",
		"OR",
		"delivery_status = $2",
		"next_retry_at <= $3",
		"ORDER BY event_time, audit_event_id",
		"LIMIT $4",
	)
	assertAuditOutboxSQLBoundary(t, exec.query)
	wantArgs := []any{string(audit.OutboxStatusPending), string(audit.OutboxStatusRetryWait), now, 25}
	for idx, want := range wantArgs {
		if exec.args[idx] != want {
			t.Fatalf("arg %d = %#v, want %#v", idx+1, exec.args[idx], want)
		}
	}
	if !exec.rows.closed {
		t.Fatal("rows were not closed")
	}
	if len(got) != 2 {
		t.Fatalf("record count = %d, want 2", len(got))
	}
	if got[0].EventID != "audit-alpha" || got[1].EventID != "audit-beta" {
		t.Fatalf("records = %#v, want stable alpha beta", got)
	}
	if got[1].NextRetryAt == nil || !got[1].NextRetryAt.Equal(retryAt) || got[1].LastError != "retry me" {
		t.Fatalf("retry fields = %#v", got[1])
	}
}

func TestListDueAuditOutboxRecordsRejectsInvalidRequestBeforeSQL(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		now   time.Time
		limit int
	}{
		{name: "missing now", now: time.Time{}, limit: 1},
		{name: "zero limit", now: now, limit: 0},
		{name: "negative limit", now: now, limit: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{}
			st := &Store{exec: exec}

			_, err := st.ListDueAuditOutboxRecords(context.Background(), tt.now, tt.limit)
			if !errors.Is(err, audit.ErrInvalidOutboxRequest) {
				t.Fatalf("ListDueAuditOutboxRecords error = %v, want audit.ErrInvalidOutboxRequest", err)
			}
			if exec.query != "" {
				t.Fatalf("query = %q, want no SQL", exec.query)
			}
		})
	}
}

func TestListDueAuditOutboxRecordsPropagatesRowsErrCloseErrAndScanErr(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		rows fakeRows
	}{
		{
			name: "rows err",
			rows: fakeRows{
				rows: []fakeRow{{values: auditOutboxRowValues(auditOutboxRecordFixture(now))}},
				err:  errors.New("rows failed"),
			},
		},
		{
			name: "close err",
			rows: fakeRows{
				rows:     []fakeRow{{values: auditOutboxRowValues(auditOutboxRecordFixture(now))}},
				closeErr: errors.New("close failed"),
			},
		},
		{
			name: "scan err",
			rows: fakeRows{
				rows: []fakeRow{{err: errors.New("scan failed")}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{rows: tt.rows}
			st := &Store{exec: exec}

			_, err := st.ListDueAuditOutboxRecords(context.Background(), now, 1)
			if err == nil {
				t.Fatal("ListDueAuditOutboxRecords succeeded, want error")
			}
			if !exec.rows.closed {
				t.Fatal("rows were not closed after error")
			}
		})
	}
}

func TestListDueAuditOutboxRecordsRejectsInvalidRows(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	valid := auditOutboxRecordFixture(now)
	tests := []struct {
		name   string
		mutate func(audit.OutboxRecord) audit.OutboxRecord
	}{
		{name: "invalid status", mutate: func(record audit.OutboxRecord) audit.OutboxRecord {
			record.Status = audit.OutboxStatus("wedged")
			return record
		}},
		{name: "negative attempt", mutate: func(record audit.OutboxRecord) audit.OutboxRecord {
			record.DeliveryAttempt = -1
			return record
		}},
		{name: "non-object payload", mutate: func(record audit.OutboxRecord) audit.OutboxRecord {
			record.PayloadJSON = []byte(`["not","object"]`)
			return record
		}},
		{name: "missing event_time", mutate: func(record audit.OutboxRecord) audit.OutboxRecord {
			record.EventTime = time.Time{}
			return record
		}},
		{name: "missing created_at", mutate: func(record audit.OutboxRecord) audit.OutboxRecord {
			record.CreatedAt = time.Time{}
			return record
		}},
		{name: "missing updated_at", mutate: func(record audit.OutboxRecord) audit.OutboxRecord {
			record.UpdatedAt = time.Time{}
			return record
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{
				rows: fakeRows{rows: []fakeRow{{values: auditOutboxRowValues(tt.mutate(valid))}}},
			}
			st := &Store{exec: exec}

			_, err := st.ListDueAuditOutboxRecords(context.Background(), now, 1)
			if err == nil {
				t.Fatal("ListDueAuditOutboxRecords succeeded, want validation error")
			}
			if !exec.rows.closed {
				t.Fatal("rows were not closed after validation error")
			}
		})
	}
}

func TestClaimDueAuditOutboxRecordsAtomicallyUpdatesAndReturnsDueRows(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	updated := auditOutboxRecordFixture(now)
	updated.Status = audit.OutboxStatusDelivering
	updated.DeliveryAttempt = 1
	updated.UpdatedAt = now
	exec := &fakeExecutor{
		rows: fakeRows{rows: []fakeRow{{values: auditOutboxRowValues(updated)}}},
	}
	st := &Store{exec: exec}

	got, err := st.ClaimDueAuditOutboxRecords(context.Background(), "deliverer-1", now, 10)
	if err != nil {
		t.Fatalf("ClaimDueAuditOutboxRecords: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"UPDATE audit_outbox",
		"SET delivery_status = $1",
		"delivery_attempt = delivery_attempt + 1",
		"next_retry_at = NULL",
		"last_error = ''",
		"delivered_at = NULL",
		"updated_at = $2",
		"WHERE audit_event_id IN",
		"SELECT audit_event_id",
		"FROM audit_outbox",
		"WHERE",
		"delivery_status = $3",
		"OR",
		"delivery_status = $4",
		"next_retry_at <= $2",
		"ORDER BY event_time, audit_event_id",
		"LIMIT $5",
		"FOR UPDATE SKIP LOCKED",
		"RETURNING",
		"audit_event_id", "event_type", "event_time", "payload_json", "delivery_status",
	)
	assertAuditOutboxSQLBoundary(t, exec.query)
	if strings.Contains(strings.ToLower(exec.query), "delivery_owner") {
		t.Fatalf("claim query must not pretend to persist owner without schema support: %s", exec.query)
	}
	wantArgs := []any{string(audit.OutboxStatusDelivering), now, string(audit.OutboxStatusPending), string(audit.OutboxStatusRetryWait), 10}
	for idx, want := range wantArgs {
		if exec.args[idx] != want {
			t.Fatalf("arg %d = %#v, want %#v", idx+1, exec.args[idx], want)
		}
	}
	if len(got) != 1 || got[0].Status != audit.OutboxStatusDelivering || got[0].DeliveryAttempt != 1 {
		t.Fatalf("claimed = %#v, want delivering attempt 1", got)
	}
}

func TestClaimDueAuditOutboxRecordsRejectsInvalidRequestBeforeSQL(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		owner string
		now   time.Time
		limit int
	}{
		{name: "missing owner", owner: " ", now: now, limit: 1},
		{name: "missing now", owner: "deliverer-1", now: time.Time{}, limit: 1},
		{name: "zero limit", owner: "deliverer-1", now: now, limit: 0},
		{name: "negative limit", owner: "deliverer-1", now: now, limit: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{}
			st := &Store{exec: exec}

			_, err := st.ClaimDueAuditOutboxRecords(context.Background(), tt.owner, tt.now, tt.limit)
			if !errors.Is(err, audit.ErrInvalidOutboxRequest) {
				t.Fatalf("ClaimDueAuditOutboxRecords error = %v, want audit.ErrInvalidOutboxRequest", err)
			}
			if exec.query != "" {
				t.Fatalf("query = %q, want no SQL", exec.query)
			}
		})
	}
}

func TestMarkAuditOutboxDeliveredGuardsDeliveringAndDoesNotMutatePayloadBoundary(t *testing.T) {
	deliveredAt := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	exec := &fakeExecutor{rowsAffected: 1}
	st := &Store{exec: exec}

	if err := st.MarkAuditOutboxDelivered(context.Background(), "audit-alpha", deliveredAt); err != nil {
		t.Fatalf("MarkAuditOutboxDelivered: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"UPDATE audit_outbox SET",
		"delivery_status = $1",
		"delivered_at = $2",
		"updated_at = $2",
		"next_retry_at = NULL",
		"last_error = ''",
		"WHERE audit_event_id = $3",
		"delivery_status = $4",
	)
	assertAuditOutboxSQLBoundary(t, exec.query)
	mutated := strings.ToLower(strings.Split(exec.query, " WHERE ")[0])
	for _, forbidden := range []string{"payload_json", "event_type", "event_time"} {
		if strings.Contains(mutated, forbidden) {
			t.Fatalf("delivered query mutates %q boundary: %s", forbidden, exec.query)
		}
	}
	wantArgs := []any{string(audit.OutboxStatusDelivered), deliveredAt, "audit-alpha", string(audit.OutboxStatusDelivering)}
	for idx, want := range wantArgs {
		if exec.args[idx] != want {
			t.Fatalf("arg %d = %#v, want %#v", idx+1, exec.args[idx], want)
		}
	}
}

func TestMarkAuditOutboxDeliveredRejectsInvalidRequestBeforeSQL(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		eventID string
		now     time.Time
	}{
		{name: "missing event id", eventID: " ", now: now},
		{name: "missing now", eventID: "audit-alpha", now: time.Time{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{}
			st := &Store{exec: exec}

			err := st.MarkAuditOutboxDelivered(context.Background(), tt.eventID, tt.now)
			if !errors.Is(err, audit.ErrInvalidOutboxRequest) {
				t.Fatalf("MarkAuditOutboxDelivered error = %v, want audit.ErrInvalidOutboxRequest", err)
			}
			if exec.query != "" {
				t.Fatalf("query = %q, want no SQL", exec.query)
			}
		})
	}
}

func TestMarkAuditOutboxDeliveredNoRowsWrapsSQLNoRows(t *testing.T) {
	exec := &fakeExecutor{rowsAffected: 0}
	st := &Store{exec: exec}

	err := st.MarkAuditOutboxDelivered(context.Background(), "audit-alpha", time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC))
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("MarkAuditOutboxDelivered error = %v, want sql.ErrNoRows", err)
	}
}

func TestMarkAuditOutboxDeliveryFailedChoosesRetryWaitOrFailedAndRedactsError(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	exec := &fakeExecutor{rowsAffected: 1}
	st := &Store{exec: exec}

	err := st.MarkAuditOutboxDeliveryFailed(context.Background(), "audit-alpha", audit.DeliveryFailure{
		MaxAttempts: 3,
		Backoff:     5 * time.Minute,
		LastError:   "delivery failed with token=audit-secret",
		Now:         now,
	})
	if err != nil {
		t.Fatalf("MarkAuditOutboxDeliveryFailed retry: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"UPDATE audit_outbox SET",
		"delivery_status = CASE WHEN delivery_attempt >= $2 THEN $3 ELSE $4 END",
		"next_retry_at = CASE WHEN delivery_attempt >= $2 THEN NULL ELSE $5 END",
		"last_error = $6",
		"delivered_at = NULL",
		"updated_at = $7",
		"WHERE audit_event_id = $1",
		"delivery_status = $8",
	)
	assertAuditOutboxSQLBoundary(t, exec.query)
	wantArgs := []any{
		"audit-alpha",
		3,
		string(audit.OutboxStatusFailed),
		string(audit.OutboxStatusRetryWait),
		now.Add(5 * time.Minute),
		"delivery failed with token=[REDACTED]",
		now,
		string(audit.OutboxStatusDelivering),
	}
	for idx, want := range wantArgs {
		if exec.args[idx] != want {
			t.Fatalf("retry arg %d = %#v, want %#v", idx+1, exec.args[idx], want)
		}
	}

	err = st.MarkAuditOutboxDeliveryFailed(context.Background(), "audit-alpha", audit.DeliveryFailure{
		MaxAttempts: 1,
		Backoff:     0,
		LastError:   " ",
		Now:         now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("MarkAuditOutboxDeliveryFailed empty error: %v", err)
	}
	if exec.args[1] != 1 || exec.args[5] != "delivery failed" {
		t.Fatalf("failed/default-error args = %#v, want max_attempts 1 and default error", exec.args)
	}
}

func TestMarkAuditOutboxDeliveryFailedRejectsInvalidRequestBeforeSQL(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	validFailure := audit.DeliveryFailure{
		MaxAttempts: 3,
		Backoff:     time.Minute,
		LastError:   "delivery failed",
		Now:         now,
	}
	tests := []struct {
		name    string
		eventID string
		failure audit.DeliveryFailure
	}{
		{name: "missing event id", eventID: " ", failure: validFailure},
		{name: "missing now", eventID: "audit-alpha", failure: func() audit.DeliveryFailure {
			failure := validFailure
			failure.Now = time.Time{}
			return failure
		}()},
		{name: "zero max attempts", eventID: "audit-alpha", failure: func() audit.DeliveryFailure {
			failure := validFailure
			failure.MaxAttempts = 0
			return failure
		}()},
		{name: "negative max attempts", eventID: "audit-alpha", failure: func() audit.DeliveryFailure {
			failure := validFailure
			failure.MaxAttempts = -1
			return failure
		}()},
		{name: "negative backoff", eventID: "audit-alpha", failure: func() audit.DeliveryFailure {
			failure := validFailure
			failure.Backoff = -time.Second
			return failure
		}()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := &fakeExecutor{}
			st := &Store{exec: exec}

			err := st.MarkAuditOutboxDeliveryFailed(context.Background(), tt.eventID, tt.failure)
			if !errors.Is(err, audit.ErrInvalidOutboxRequest) {
				t.Fatalf("MarkAuditOutboxDeliveryFailed error = %v, want audit.ErrInvalidOutboxRequest", err)
			}
			if exec.query != "" {
				t.Fatalf("query = %q, want no SQL", exec.query)
			}
		})
	}
}

func TestMarkAuditOutboxDeliveryFailedNoRowsWrapsSQLNoRows(t *testing.T) {
	exec := &fakeExecutor{rowsAffected: 0}
	st := &Store{exec: exec}

	err := st.MarkAuditOutboxDeliveryFailed(context.Background(), "audit-alpha", audit.DeliveryFailure{
		MaxAttempts: 1,
		Now:         time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC),
	})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("MarkAuditOutboxDeliveryFailed error = %v, want sql.ErrNoRows", err)
	}
}

func auditOutboxRecordFixture(now time.Time) audit.OutboxRecord {
	return audit.OutboxRecord{
		EventID:         "audit-alpha",
		EventType:       audit.EventTypeRepoCreate,
		EventTime:       now.Add(-time.Minute),
		PayloadJSON:     []byte(`{"event_id":"audit-alpha"}`),
		Status:          audit.OutboxStatusPending,
		DeliveryAttempt: 0,
		CreatedAt:       now.Add(-time.Minute),
		UpdatedAt:       now.Add(-time.Minute),
	}
}

func auditOutboxRowValues(record audit.OutboxRecord) []any {
	return []any{
		record.EventID,
		string(record.EventType),
		record.EventTime,
		[]byte(record.PayloadJSON),
		string(record.Status),
		record.DeliveryAttempt,
		timePtrValue(record.NextRetryAt),
		record.LastError,
		record.CreatedAt,
		record.UpdatedAt,
		timePtrValue(record.DeliveredAt),
	}
}

func assertAuditOutboxSQLBoundary(t *testing.T, sql string) {
	t.Helper()
	lower := strings.ToLower(sql)
	for _, forbidden := range []string{"operations", "repo_fences", "jvs", "webdav", "mount", "storage"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("query = %s, must not touch %q", sql, forbidden)
		}
	}
}
