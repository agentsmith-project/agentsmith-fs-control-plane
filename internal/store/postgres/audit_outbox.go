package postgres

import (
	"context"

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

func auditOutboxInsertSQL() string {
	return "INSERT INTO audit_outbox (" + stringsJoin(auditOutboxColumns) + ") VALUES (" + placeholders(1, len(auditOutboxColumns)) + ")"
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
