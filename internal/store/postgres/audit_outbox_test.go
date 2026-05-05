package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
)

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
