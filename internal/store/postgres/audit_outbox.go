package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
)

var auditOutboxColumns = []string{
	"audit_event_id",
	"event_type",
	"event_time",
	"payload_json",
	"delivery_status",
	"delivery_attempt",
	"next_retry_at",
	"last_error",
	"created_at",
	"updated_at",
	"delivered_at",
}

func (store *Store) AppendAuditEvent(ctx context.Context, event audit.Event) error {
	record, err := audit.NewOutboxRecord(event, store.now())
	if err != nil {
		return err
	}
	_, err = store.exec.ExecContext(ctx, auditOutboxInsertSQL(),
		record.EventID,
		string(record.EventType),
		record.EventTime,
		[]byte(record.PayloadJSON),
		string(record.Status),
		record.DeliveryAttempt,
		timePtrArg(record.NextRetryAt),
		record.LastError,
		record.CreatedAt,
		record.UpdatedAt,
		timePtrArg(record.DeliveredAt),
	)
	return err
}

func (store *Store) ListDueAuditOutboxRecords(ctx context.Context, now time.Time, limit int) ([]audit.OutboxRecord, error) {
	if now.IsZero() {
		return nil, auditOutboxInvalidRequest("now", "list time must be set")
	}
	if limit <= 0 {
		return nil, auditOutboxInvalidRequest("limit", "limit must be positive")
	}

	rows, err := store.exec.QueryContext(ctx, auditOutboxListDueSQL(),
		string(audit.OutboxStatusPending),
		string(audit.OutboxStatusRetryWait),
		now,
		limit,
	)
	if err != nil {
		return nil, err
	}
	return scanAuditOutboxRecords(rows)
}

// ClaimDueAuditOutboxRecords is a DB-only at-least-once state claim. The owner is
// validated for caller discipline but is not persisted because the current schema has
// no delivery_owner column, so this does not provide owner fencing.
func (store *Store) ClaimDueAuditOutboxRecords(ctx context.Context, owner string, now time.Time, limit int) ([]audit.OutboxRecord, error) {
	if strings.TrimSpace(owner) == "" {
		return nil, auditOutboxInvalidRequest("owner", "missing delivery owner")
	}
	if now.IsZero() {
		return nil, auditOutboxInvalidRequest("now", "claim time must be set")
	}
	if limit <= 0 {
		return nil, auditOutboxInvalidRequest("limit", "limit must be positive")
	}

	rows, err := store.exec.QueryContext(ctx, auditOutboxClaimDueSQL(),
		string(audit.OutboxStatusDelivering),
		now,
		string(audit.OutboxStatusPending),
		string(audit.OutboxStatusRetryWait),
		limit,
	)
	if err != nil {
		return nil, err
	}
	return scanAuditOutboxRecords(rows)
}

func (store *Store) RecoverStaleAuditOutboxRecords(ctx context.Context, owner string, staleThreshold time.Duration, limit int, failure audit.DeliveryFailure) ([]audit.OutboxRecord, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return nil, auditOutboxInvalidRequest("owner", "missing delivery owner")
	}
	if staleThreshold <= 0 {
		return nil, auditOutboxInvalidRequest("stale_threshold", "stale threshold must be positive")
	}
	if limit <= 0 {
		return nil, auditOutboxInvalidRequest("limit", "limit must be positive")
	}
	if failure.Now.IsZero() {
		return nil, auditOutboxInvalidRequest("now", "recovery time must be set")
	}
	if failure.MaxAttempts <= 0 {
		return nil, auditOutboxInvalidRequest("max_attempts", "max attempts must be positive")
	}
	if failure.Backoff < 0 {
		return nil, auditOutboxInvalidRequest("backoff", "retry backoff cannot be negative")
	}
	lastError := audit.RedactString(strings.TrimSpace(failure.LastError))
	if lastError == "" {
		lastError = "delivery failed"
	}
	staleBefore := failure.Now.Add(-staleThreshold)

	rows, err := store.exec.QueryContext(ctx, auditOutboxRecoverStaleDeliveringSQL(),
		failure.MaxAttempts,
		string(audit.OutboxStatusFailed),
		string(audit.OutboxStatusRetryWait),
		failure.Now.Add(failure.Backoff),
		lastError,
		failure.Now,
		string(audit.OutboxStatusDelivering),
		staleBefore,
		limit,
	)
	if err != nil {
		return nil, err
	}
	return scanAuditOutboxRecords(rows)
}

func (store *Store) MarkAuditOutboxDelivered(ctx context.Context, eventID string, now time.Time) error {
	if strings.TrimSpace(eventID) == "" {
		return auditOutboxInvalidRequest("event_id", "missing audit event id")
	}
	if now.IsZero() {
		return auditOutboxInvalidRequest("now", "delivery transition time must be set")
	}
	result, err := store.exec.ExecContext(ctx, auditOutboxMarkDeliveredSQL(),
		string(audit.OutboxStatusDelivered),
		now,
		eventID,
		string(audit.OutboxStatusDelivering),
	)
	if err != nil {
		return err
	}
	return requireAuditOutboxRowsAffected(result, "mark audit outbox %q delivered", eventID)
}

func (store *Store) MarkAuditOutboxDeliveryFailed(ctx context.Context, eventID string, failure audit.DeliveryFailure) error {
	if strings.TrimSpace(eventID) == "" {
		return auditOutboxInvalidRequest("event_id", "missing audit event id")
	}
	if failure.Now.IsZero() {
		return auditOutboxInvalidRequest("now", "delivery transition time must be set")
	}
	if failure.MaxAttempts <= 0 {
		return auditOutboxInvalidRequest("max_attempts", "max attempts must be positive")
	}
	if failure.Backoff < 0 {
		return auditOutboxInvalidRequest("backoff", "retry backoff cannot be negative")
	}
	lastError := audit.RedactString(strings.TrimSpace(failure.LastError))
	if lastError == "" {
		lastError = "delivery failed"
	}

	result, err := store.exec.ExecContext(ctx, auditOutboxMarkFailedSQL(),
		eventID,
		failure.MaxAttempts,
		string(audit.OutboxStatusFailed),
		string(audit.OutboxStatusRetryWait),
		failure.Now.Add(failure.Backoff),
		lastError,
		failure.Now,
		string(audit.OutboxStatusDelivering),
	)
	if err != nil {
		return err
	}
	return requireAuditOutboxRowsAffected(result, "mark audit outbox %q delivery failed", eventID)
}

func auditOutboxInsertSQL() string {
	return "INSERT INTO audit_outbox (" + stringsJoin(auditOutboxColumns) + ") VALUES (" + placeholders(1, len(auditOutboxColumns)) + ")"
}

func auditOutboxSelectColumnsSQL() string {
	return strings.Join(auditOutboxColumns, ", ")
}

func auditOutboxListDueSQL() string {
	return "SELECT " + auditOutboxSelectColumnsSQL() + " FROM audit_outbox " +
		"WHERE (delivery_status = $1 OR (delivery_status = $2 AND next_retry_at <= $3)) " +
		"ORDER BY event_time, audit_event_id LIMIT $4"
}

func auditOutboxClaimDueSQL() string {
	return "UPDATE audit_outbox " +
		"SET delivery_status = $1, delivery_attempt = delivery_attempt + 1, next_retry_at = NULL, last_error = '', delivered_at = NULL, updated_at = $2 " +
		"WHERE audit_event_id IN (" +
		"SELECT audit_event_id FROM audit_outbox " +
		"WHERE (delivery_status = $3 OR (delivery_status = $4 AND next_retry_at <= $2)) " +
		"ORDER BY event_time, audit_event_id LIMIT $5 FOR UPDATE SKIP LOCKED" +
		") RETURNING " + auditOutboxSelectColumnsSQL()
}

func auditOutboxRecoverStaleDeliveringSQL() string {
	return "UPDATE audit_outbox " +
		"SET delivery_status = CASE WHEN delivery_attempt >= $1 THEN $2 ELSE $3 END, " +
		"next_retry_at = CASE WHEN delivery_attempt >= $1 THEN NULL ELSE $4 END, " +
		"last_error = $5, delivered_at = NULL, updated_at = $6 " +
		"WHERE audit_event_id IN (" +
		"SELECT audit_event_id FROM audit_outbox " +
		"WHERE delivery_status = $7 AND updated_at <= $8 " +
		"ORDER BY updated_at, audit_event_id LIMIT $9 FOR UPDATE SKIP LOCKED" +
		") RETURNING " + auditOutboxSelectColumnsSQL()
}

func auditOutboxMarkDeliveredSQL() string {
	return "UPDATE audit_outbox SET " +
		"delivery_status = $1, delivered_at = $2, updated_at = $2, next_retry_at = NULL, last_error = '' " +
		"WHERE audit_event_id = $3 AND delivery_status = $4"
}

func auditOutboxMarkFailedSQL() string {
	return "UPDATE audit_outbox SET " +
		"delivery_status = CASE WHEN delivery_attempt >= $2 THEN $3 ELSE $4 END, " +
		"next_retry_at = CASE WHEN delivery_attempt >= $2 THEN NULL ELSE $5 END, " +
		"last_error = $6, delivered_at = NULL, updated_at = $7 " +
		"WHERE audit_event_id = $1 AND delivery_status = $8"
}

func scanAuditOutboxRecords(rows rowsScanner) (records []audit.OutboxRecord, err error) {
	defer func() {
		closeErr := rows.Close()
		if err == nil {
			err = closeErr
		}
	}()
	for rows.Next() {
		record, err := scanAuditOutboxRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func scanAuditOutboxRecord(row rowScanner) (audit.OutboxRecord, error) {
	var record audit.OutboxRecord
	var eventType, status string
	var nextRetryAt, deliveredAt sql.NullTime
	var payload []byte
	if err := row.Scan(
		&record.EventID,
		&eventType,
		&record.EventTime,
		&payload,
		&status,
		&record.DeliveryAttempt,
		&nextRetryAt,
		&record.LastError,
		&record.CreatedAt,
		&record.UpdatedAt,
		&deliveredAt,
	); err != nil {
		return audit.OutboxRecord{}, err
	}

	record.EventType = audit.EventType(eventType)
	record.PayloadJSON = append(record.PayloadJSON[:0], payload...)
	record.Status = audit.OutboxStatus(status)
	record.NextRetryAt = nullTimePtr(nextRetryAt)
	record.DeliveredAt = nullTimePtr(deliveredAt)
	if err := validateAuditOutboxRecord(record); err != nil {
		return audit.OutboxRecord{}, err
	}
	return record, nil
}

func validateAuditOutboxRecord(record audit.OutboxRecord) error {
	if strings.TrimSpace(record.EventID) == "" {
		return auditOutboxInvalidRecord("event_id", "missing audit event id")
	}
	if strings.TrimSpace(string(record.EventType)) == "" {
		return auditOutboxInvalidRecord("event_type", "missing audit event type")
	}
	if record.EventTime.IsZero() {
		return auditOutboxInvalidRecord("event_time", "missing audit event time")
	}
	if record.CreatedAt.IsZero() {
		return auditOutboxInvalidRecord("created_at", "missing outbox creation time")
	}
	if record.UpdatedAt.IsZero() {
		return auditOutboxInvalidRecord("updated_at", "missing outbox update time")
	}
	if !record.Status.Valid() {
		return auditOutboxInvalidRecord("delivery_status", fmt.Sprintf("unknown delivery status %q", record.Status))
	}
	if record.DeliveryAttempt < 0 {
		return auditOutboxInvalidRecord("delivery_attempt", "delivery attempt cannot be negative")
	}
	if err := validateAuditOutboxPayload(record.PayloadJSON); err != nil {
		return err
	}
	return nil
}

func validateAuditOutboxPayload(payload []byte) error {
	if len(strings.TrimSpace(string(payload))) == 0 {
		return auditOutboxInvalidRecord("payload_json", "missing payload json")
	}
	var object map[string]any
	if err := json.Unmarshal(payload, &object); err != nil {
		return auditOutboxInvalidRecord("payload_json", "payload json must be a valid object")
	}
	if object == nil {
		return auditOutboxInvalidRecord("payload_json", "payload json must be an object")
	}
	return nil
}

func requireAuditOutboxRowsAffected(result sql.Result, format, eventID string) error {
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return fmt.Errorf("%w: "+format, sql.ErrNoRows, eventID)
	}
	return nil
}

func auditOutboxInvalidRequest(field, reason string) *audit.OutboxError {
	return &audit.OutboxError{
		Code:   audit.OutboxErrorInvalidRequest,
		Field:  field,
		Reason: reason,
		Cause:  audit.ErrInvalidOutboxRequest,
	}
}

func auditOutboxInvalidRecord(field, reason string) *audit.OutboxError {
	return &audit.OutboxError{
		Code:   audit.OutboxErrorInvalidRecord,
		Field:  field,
		Reason: reason,
		Cause:  audit.ErrInvalidOutboxRecord,
	}
}

func stringsJoin(values []string) string {
	out := ""
	for idx, value := range values {
		if idx > 0 {
			out += ", "
		}
		out += value
	}
	return out
}
