package audit

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/observability"
)

func TestOutboxStatusWireValuesAreStable(t *testing.T) {
	statuses := map[OutboxStatus]string{
		OutboxStatusPending:    "pending",
		OutboxStatusDelivering: "delivering",
		OutboxStatusDelivered:  "delivered",
		OutboxStatusRetryWait:  "retry_wait",
		OutboxStatusFailed:     "failed",
	}

	for status, want := range statuses {
		if got := status.String(); got != want {
			t.Fatalf("%#v String() = %q, want %q", status, got, want)
		}
		if !status.Valid() {
			t.Fatalf("%#v Valid() = false, want true", status)
		}
	}

	if OutboxStatus("wedged").Valid() {
		t.Fatal(`OutboxStatus("wedged").Valid() = true, want false`)
	}
}

func TestNewOutboxRecordSanitizesPayloadAndStartsPending(t *testing.T) {
	now := outboxTestTime()
	eventTime := now.Add(-time.Minute)
	input := Event{
		EventID:       "evt_outbox_1",
		Type:          EventTypeExportCreate,
		Time:          eventTime,
		CallerService: "product-caller",
		Resource: Resource{
			Type: "repo",
			ID:   "repo_alpha",
			Path: "/payload --token path-token redis://:metadata-secret@metadata:6379/1",
		},
		Outcome: OutcomeSucceeded,
		Reason:  `created with token=reason-token Authorization: Bearer reason-bearer`,
		Details: map[string]any{
			"repo_id":  "repo_alpha",
			"password": "detail-password",
			"message":  `finished --token detail-token`,
		},
	}

	record, err := NewOutboxRecord(input, now)
	if err != nil {
		t.Fatalf("NewOutboxRecord returned error: %v", err)
	}

	if record.EventID != "evt_outbox_1" {
		t.Fatalf("EventID = %q, want evt_outbox_1", record.EventID)
	}
	if record.EventType != EventTypeExportCreate {
		t.Fatalf("EventType = %q, want %q", record.EventType, EventTypeExportCreate)
	}
	if !record.EventTime.Equal(eventTime) {
		t.Fatalf("EventTime = %v, want %v", record.EventTime, eventTime)
	}
	if record.Status != OutboxStatusPending {
		t.Fatalf("Status = %q, want %q", record.Status, OutboxStatusPending)
	}
	if record.DeliveryAttempt != 0 {
		t.Fatalf("DeliveryAttempt = %d, want 0", record.DeliveryAttempt)
	}
	if record.NextRetryAt != nil || record.LastError != "" || record.DeliveredAt != nil {
		t.Fatalf("new pending record has terminal/retry fields: %#v", record)
	}
	if !record.CreatedAt.Equal(now) || !record.UpdatedAt.Equal(now) {
		t.Fatalf("CreatedAt/UpdatedAt = %v/%v, want %v", record.CreatedAt, record.UpdatedAt, now)
	}

	var payload map[string]any
	if err := json.Unmarshal(record.PayloadJSON, &payload); err != nil {
		t.Fatalf("payload is not JSON object: %v", err)
	}
	if payload["event_id"] != "evt_outbox_1" {
		t.Fatalf("payload event_id = %#v, want evt_outbox_1", payload["event_id"])
	}
	rendered := strings.ToLower(string(record.PayloadJSON))
	for _, forbidden := range []string{
		"path-token",
		"metadata-secret",
		"reason-token",
		"reason-bearer",
		"detail-password",
		"detail-token",
		"redis://",
	} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("outbox payload leaked %q in %s", forbidden, string(record.PayloadJSON))
		}
	}
	if !strings.Contains(string(record.PayloadJSON), observability.Redacted) {
		t.Fatalf("payload missing redaction marker: %s", string(record.PayloadJSON))
	}
	if input.Details["password"] != "detail-password" {
		t.Fatalf("NewOutboxRecord mutated input event details: %#v", input.Details)
	}
}

func TestMarkDeliveringClaimsPendingAndRetryWaitRecords(t *testing.T) {
	now := outboxTestTime()
	pending := newValidOutboxRecord(t)
	retryAt := now.Add(-5 * time.Minute)
	retryWait := pending
	retryWait.Status = OutboxStatusRetryWait
	retryWait.DeliveryAttempt = 2
	retryWait.NextRetryAt = &retryAt
	retryWait.LastError = "previous failure"

	tests := []struct {
		name        string
		record      OutboxRecord
		wantAttempt int
	}{
		{name: "pending", record: pending, wantAttempt: 1},
		{name: "retry wait", record: retryWait, wantAttempt: 3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			updatedAt := now.Add(time.Minute)
			updated, err := MarkDelivering(tc.record, "worker-a", updatedAt)
			if err != nil {
				t.Fatalf("MarkDelivering returned error: %v", err)
			}

			if updated.Status != OutboxStatusDelivering {
				t.Fatalf("Status = %q, want %q", updated.Status, OutboxStatusDelivering)
			}
			if updated.DeliveryAttempt != tc.wantAttempt {
				t.Fatalf("DeliveryAttempt = %d, want %d", updated.DeliveryAttempt, tc.wantAttempt)
			}
			if updated.NextRetryAt != nil || updated.LastError != "" {
				t.Fatalf("delivering record kept retry/error fields: %#v", updated)
			}
			if !updated.UpdatedAt.Equal(updatedAt) {
				t.Fatalf("UpdatedAt = %v, want %v", updated.UpdatedAt, updatedAt)
			}
			if !updated.CreatedAt.Equal(tc.record.CreatedAt) {
				t.Fatalf("CreatedAt = %v, want original %v", updated.CreatedAt, tc.record.CreatedAt)
			}
			if tc.record.Status != pending.Status && tc.record.Status != retryWait.Status {
				t.Fatalf("MarkDelivering mutated input record: %#v", tc.record)
			}
		})
	}
}

func TestMarkDeliveringRejectsNonClaimableStatuses(t *testing.T) {
	now := outboxTestTime()
	record := newValidOutboxRecord(t)

	for _, status := range []OutboxStatus{
		OutboxStatusDelivering,
		OutboxStatusDelivered,
		OutboxStatusFailed,
	} {
		t.Run(status.String(), func(t *testing.T) {
			record := record
			record.Status = status
			_, err := MarkDelivering(record, "worker-a", now.Add(time.Minute))
			assertOutboxError(t, err, ErrInvalidOutboxTransition)
		})
	}
}

func TestMarkDeliveredOnlyAllowsDeliveringAndStopsRetry(t *testing.T) {
	now := outboxTestTime()
	record := newValidOutboxRecord(t)
	delivering, err := MarkDelivering(record, "worker-a", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("MarkDelivering returned error: %v", err)
	}

	deliveredAt := now.Add(2 * time.Minute)
	delivered, err := MarkDelivered(delivering, deliveredAt)
	if err != nil {
		t.Fatalf("MarkDelivered returned error: %v", err)
	}

	if delivered.Status != OutboxStatusDelivered {
		t.Fatalf("Status = %q, want %q", delivered.Status, OutboxStatusDelivered)
	}
	if !sameOutboxTimePtr(delivered.DeliveredAt, deliveredAt) {
		t.Fatalf("DeliveredAt = %v, want %v", delivered.DeliveredAt, deliveredAt)
	}
	if delivered.NextRetryAt != nil || delivered.LastError != "" {
		t.Fatalf("delivered record should not retry: %#v", delivered)
	}
	if delivering.DeliveredAt != nil || delivering.Status != OutboxStatusDelivering {
		t.Fatalf("MarkDelivered mutated input record: %#v", delivering)
	}

	for _, transition := range []struct {
		name string
		call func(OutboxRecord) error
	}{
		{name: "delivering", call: func(r OutboxRecord) error {
			_, err := MarkDelivering(r, "worker-a", now.Add(3*time.Minute))
			return err
		}},
		{name: "delivered", call: func(r OutboxRecord) error {
			_, err := MarkDelivered(r, now.Add(3*time.Minute))
			return err
		}},
		{name: "failed", call: func(r OutboxRecord) error {
			_, err := MarkDeliveryFailed(r, DeliveryFailure{
				MaxAttempts: 3,
				Backoff:     time.Minute,
				LastError:   "late failure",
				Now:         now.Add(3 * time.Minute),
			})
			return err
		}},
	} {
		t.Run("terminal delivered rejects "+transition.name, func(t *testing.T) {
			assertOutboxError(t, transition.call(delivered), ErrInvalidOutboxTransition)
		})
	}
}

func TestMarkDeliveredRejectsNonDeliveringStatuses(t *testing.T) {
	now := outboxTestTime()
	record := newValidOutboxRecord(t)

	for _, status := range []OutboxStatus{
		OutboxStatusPending,
		OutboxStatusRetryWait,
		OutboxStatusDelivered,
		OutboxStatusFailed,
	} {
		t.Run(status.String(), func(t *testing.T) {
			record := record
			record.Status = status
			_, err := MarkDelivered(record, now.Add(time.Minute))
			assertOutboxError(t, err, ErrInvalidOutboxTransition)
		})
	}
}

func TestMarkDeliveryFailedRetriesUntilMaxAttemptsThenFails(t *testing.T) {
	now := outboxTestTime()
	base := newValidOutboxRecord(t)

	delivering, err := MarkDelivering(base, "worker-a", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("MarkDelivering returned error: %v", err)
	}
	retry, err := MarkDeliveryFailed(delivering, DeliveryFailure{
		MaxAttempts: 3,
		Backoff:     5 * time.Minute,
		LastError:   "downstream token=delivery-token failed",
		Now:         now.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("MarkDeliveryFailed retry returned error: %v", err)
	}
	if retry.Status != OutboxStatusRetryWait {
		t.Fatalf("Status = %q, want %q", retry.Status, OutboxStatusRetryWait)
	}
	if !sameOutboxTimePtr(retry.NextRetryAt, now.Add(7*time.Minute)) {
		t.Fatalf("NextRetryAt = %v, want %v", retry.NextRetryAt, now.Add(7*time.Minute))
	}
	if strings.Contains(retry.LastError, "delivery-token") || !strings.Contains(retry.LastError, observability.Redacted) {
		t.Fatalf("LastError was not redacted: %q", retry.LastError)
	}
	if delivering.Status != OutboxStatusDelivering || delivering.NextRetryAt != nil || delivering.LastError != "" {
		t.Fatalf("MarkDeliveryFailed mutated input delivering record: %#v", delivering)
	}

	retryDelivering, err := MarkDelivering(retry, "worker-a", now.Add(8*time.Minute))
	if err != nil {
		t.Fatalf("MarkDelivering retry returned error: %v", err)
	}
	retryDelivering.DeliveryAttempt = 3
	failed, err := MarkDeliveryFailed(retryDelivering, DeliveryFailure{
		MaxAttempts: 3,
		Backoff:     5 * time.Minute,
		LastError:   "permanent failure",
		Now:         now.Add(9 * time.Minute),
	})
	if err != nil {
		t.Fatalf("MarkDeliveryFailed terminal returned error: %v", err)
	}
	if failed.Status != OutboxStatusFailed {
		t.Fatalf("Status = %q, want %q", failed.Status, OutboxStatusFailed)
	}
	if failed.NextRetryAt != nil || failed.DeliveredAt != nil {
		t.Fatalf("failed record should not retry or be delivered: %#v", failed)
	}
	if failed.LastError != "permanent failure" {
		t.Fatalf("LastError = %q, want permanent failure", failed.LastError)
	}

	for _, transition := range []struct {
		name string
		call func(OutboxRecord) error
	}{
		{name: "delivering", call: func(r OutboxRecord) error {
			_, err := MarkDelivering(r, "worker-a", now.Add(10*time.Minute))
			return err
		}},
		{name: "delivered", call: func(r OutboxRecord) error {
			_, err := MarkDelivered(r, now.Add(10*time.Minute))
			return err
		}},
		{name: "failed", call: func(r OutboxRecord) error {
			_, err := MarkDeliveryFailed(r, DeliveryFailure{
				MaxAttempts: 3,
				Backoff:     time.Minute,
				LastError:   "late failure",
				Now:         now.Add(10 * time.Minute),
			})
			return err
		}},
	} {
		t.Run("terminal failed rejects "+transition.name, func(t *testing.T) {
			assertOutboxError(t, transition.call(failed), ErrInvalidOutboxTransition)
		})
	}
}

func TestOutboxTransitionsRejectInvalidInput(t *testing.T) {
	now := outboxTestTime()
	valid := newValidOutboxRecord(t)

	tests := []struct {
		name  string
		err   error
		cause error
		field string
	}{
		{
			name: "new record missing event id",
			err: func() error {
				event := newValidAuditEvent()
				event.EventID = " "
				_, err := NewOutboxRecord(event, now)
				return err
			}(),
			cause: ErrInvalidOutboxRequest,
			field: "event_id",
		},
		{
			name: "new record missing now",
			err: func() error {
				_, err := NewOutboxRecord(newValidAuditEvent(), time.Time{})
				return err
			}(),
			cause: ErrInvalidOutboxRequest,
			field: "now",
		},
		{
			name: "missing event id",
			err: func() error {
				record := valid
				record.EventID = ""
				_, err := MarkDelivering(record, "worker-a", now)
				return err
			}(),
			cause: ErrInvalidOutboxRecord,
			field: "event_id",
		},
		{
			name: "missing status",
			err: func() error {
				record := valid
				record.Status = ""
				_, err := MarkDelivering(record, "worker-a", now)
				return err
			}(),
			cause: ErrInvalidOutboxRecord,
			field: "status",
		},
		{
			name: "missing event type",
			err: func() error {
				record := valid
				record.EventType = ""
				_, err := MarkDelivering(record, "worker-a", now)
				return err
			}(),
			cause: ErrInvalidOutboxRecord,
			field: "event_type",
		},
		{
			name: "zero event time",
			err: func() error {
				record := valid
				record.EventTime = time.Time{}
				_, err := MarkDelivering(record, "worker-a", now)
				return err
			}(),
			cause: ErrInvalidOutboxRecord,
			field: "event_time",
		},
		{
			name: "nil payload json",
			err: func() error {
				record := valid
				record.PayloadJSON = nil
				_, err := MarkDelivering(record, "worker-a", now)
				return err
			}(),
			cause: ErrInvalidOutboxRecord,
			field: "payload_json",
		},
		{
			name: "empty payload json",
			err: func() error {
				record := valid
				record.PayloadJSON = json.RawMessage("")
				_, err := MarkDelivering(record, "worker-a", now)
				return err
			}(),
			cause: ErrInvalidOutboxRecord,
			field: "payload_json",
		},
		{
			name: "invalid payload json",
			err: func() error {
				record := valid
				record.PayloadJSON = json.RawMessage(`{`)
				_, err := MarkDelivering(record, "worker-a", now)
				return err
			}(),
			cause: ErrInvalidOutboxRecord,
			field: "payload_json",
		},
		{
			name: "array payload json",
			err: func() error {
				record := valid
				record.PayloadJSON = json.RawMessage(`[]`)
				_, err := MarkDelivering(record, "worker-a", now)
				return err
			}(),
			cause: ErrInvalidOutboxRecord,
			field: "payload_json",
		},
		{
			name: "string payload json",
			err: func() error {
				record := valid
				record.PayloadJSON = json.RawMessage(`"text"`)
				_, err := MarkDelivering(record, "worker-a", now)
				return err
			}(),
			cause: ErrInvalidOutboxRecord,
			field: "payload_json",
		},
		{
			name: "missing transition now",
			err: func() error {
				_, err := MarkDelivering(valid, "worker-a", time.Time{})
				return err
			}(),
			cause: ErrInvalidOutboxRequest,
			field: "now",
		},
		{
			name: "non-positive max attempts",
			err: func() error {
				delivering := valid
				delivering.Status = OutboxStatusDelivering
				delivering.DeliveryAttempt = 1
				_, err := MarkDeliveryFailed(delivering, DeliveryFailure{
					MaxAttempts: 0,
					Backoff:     time.Minute,
					LastError:   "failure",
					Now:         now,
				})
				return err
			}(),
			cause: ErrInvalidOutboxRequest,
			field: "max_attempts",
		},
		{
			name: "negative attempt",
			err: func() error {
				record := valid
				record.DeliveryAttempt = -1
				_, err := MarkDelivering(record, "worker-a", now)
				return err
			}(),
			cause: ErrInvalidOutboxRecord,
			field: "delivery_attempt",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertOutboxErrorField(t, tc.err, tc.cause, tc.field)
		})
	}
}

func newValidOutboxRecord(t *testing.T) OutboxRecord {
	t.Helper()

	record, err := NewOutboxRecord(newValidAuditEvent(), outboxTestTime())
	if err != nil {
		t.Fatalf("NewOutboxRecord returned error: %v", err)
	}
	return record
}

func newValidAuditEvent() Event {
	now := outboxTestTime()
	return NewEvent(Event{
		EventID:       "evt_outbox_valid",
		Type:          EventTypeRepoPurge,
		Time:          now.Add(-time.Minute),
		CallerService: "product-caller",
		Resource:      Resource{Type: "repo", ID: "repo_alpha", NamespaceID: "ns_alpha"},
		Outcome:       OutcomeSucceeded,
		Reason:        "purge queued",
		Details: map[string]any{
			"repo_id": "repo_alpha",
		},
	})
}

func outboxTestTime() time.Time {
	return time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
}

func sameOutboxTimePtr(got *time.Time, want time.Time) bool {
	return got != nil && got.Equal(want)
}

func assertOutboxError(t *testing.T, err error, cause error) {
	t.Helper()

	if !errors.Is(err, cause) {
		t.Fatalf("error = %v, want cause %v", err, cause)
	}
	var outboxErr *OutboxError
	if !errors.As(err, &outboxErr) {
		t.Fatalf("error = %T %[1]v, want *OutboxError", err)
	}
}

func assertOutboxErrorField(t *testing.T, err error, cause error, field string) {
	t.Helper()

	assertOutboxError(t, err, cause)
	var outboxErr *OutboxError
	if !errors.As(err, &outboxErr) {
		t.Fatalf("error = %T %[1]v, want *OutboxError", err)
	}
	if outboxErr.Field != field {
		t.Fatalf("Field = %q, want %q", outboxErr.Field, field)
	}
}
