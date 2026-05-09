package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operatorrepair"
)

func TestOperatorRepairHandlerOperatorAdminTerminalizesUnsupportedInterventionWithAudit(t *testing.T) {
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	store := &fakeOperatorRepairStore{before: operatorRepairAPIEligibleRecord("op_123"), now: now}
	handler := operatorRepairHandlerForTest(store, operatorRepairPolicy(auth.AllowedCaller{CallerService: "ops-service", Kind: auth.CallerKindOperator, Roles: []auth.Role{auth.RoleOperatorAdmin}}), nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, operatorRepairRequest("op_123", "ops-service", validOperatorRepairBody()))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if store.calls != 1 {
		t.Fatalf("store calls = %d, want 1", store.calls)
	}
	if store.last.After.State != operations.OperationStateFailed || store.last.OperationID != "op_123" {
		t.Fatalf("store request = %#v, want failed repair for op_123", store.last)
	}
	if store.last.Event.EventID == "" || store.last.Event.Outcome != audit.OutcomeFailed {
		t.Fatalf("audit event = %#v, want failed event", store.last.Event)
	}
	body := rec.Body.String()
	for _, want := range []string{`"action":"terminalize_unsupported_intervention_as_failed"`, `"operation_id":"op_123"`, `"before":{"state":"operator_intervention_required"`, `"after":{"state":"failed"`, `"repair_outcome":"terminalized_failed"`, `"audit_event_id":`} {
		if !strings.Contains(body, want) {
			t.Fatalf("response missing %s: %s", want, body)
		}
	}
	assertOperatorRepairResponseDoesNotLeak(t, body)
}

func TestOperatorRepairHandlerRejectsProductOperationInspectorBeforeStore(t *testing.T) {
	store := &fakeOperatorRepairStore{before: operatorRepairAPIEligibleRecord("op_123")}
	handler := operatorRepairHandlerForTest(store, operatorRepairPolicy(auth.AllowedCaller{CallerService: "ops-service", Kind: auth.CallerKindProduct, Roles: []auth.Role{auth.RoleOperationInspector}}), nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, operatorRepairRequest("op_123", "ops-service", validOperatorRepairBody()))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s, want 403", rec.Code, rec.Body.String())
	}
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want role denial before store", store.calls)
	}
}

func TestOperatorRepairHandlerRejectsInvalidBodyBeforeStore(t *testing.T) {
	store := &fakeOperatorRepairStore{before: operatorRepairAPIEligibleRecord("op_123")}
	handler := operatorRepairHandlerForTest(store, operatorRepairPolicy(auth.AllowedCaller{CallerService: "ops-service", Kind: auth.CallerKindOperator, Roles: []auth.Role{auth.RoleOperatorAdmin}}), nil)
	tests := []struct {
		name string
		body string
	}{
		{name: "unknown action", body: `{"action":"release_everything","reason":"ok","evidence_ref":"docs/runbooks/ga-runbooks.md#op","affected_ids":{"operation_id":"op_123"}}`},
		{name: "missing reason", body: `{"action":"terminalize_unsupported_intervention_as_failed","evidence_ref":"docs/runbooks/ga-runbooks.md#op","affected_ids":{"operation_id":"op_123"}}`},
		{name: "secret evidence", body: `{"action":"terminalize_unsupported_intervention_as_failed","reason":"ok","evidence_ref":"/var/lib/secret","affected_ids":{"operation_id":"op_123"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, operatorRepairRequest("op_123", "ops-service", tt.body))

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body = %s, want 400", rec.Code, rec.Body.String())
			}
			if store.calls != 0 {
				t.Fatalf("store calls = %d, want invalid body before store", store.calls)
			}
		})
	}
}

func TestOperatorRepairHandlerIdempotentReplayStableWithoutDuplicateAudit(t *testing.T) {
	store := &fakeOperatorRepairStore{before: operatorRepairAPIEligibleRecord("op_123"), alreadyTerminal: true}
	handler := operatorRepairHandlerForTest(store, operatorRepairPolicy(auth.AllowedCaller{CallerService: "ops-service", Kind: auth.CallerKindOperator, Roles: []auth.Role{auth.RoleOperatorAdmin}}), nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, operatorRepairRequest("op_123", "ops-service", validOperatorRepairBody()))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s, want 409", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"audit_event_id":"audit_`) {
		t.Fatalf("already terminal response should not claim a new audit event: %s", rec.Body.String())
	}
}

func TestInternalAPIShellServesOperatorRepairThroughInjectedStore(t *testing.T) {
	store := &fakeOperatorRepairStore{before: operatorRepairAPIEligibleRecord("op_123"), now: time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)}
	handler := NewInternalAPIShell(InternalAPIShellConfig{
		PrincipalResolver: operatorRepairPrincipalResolver(),
		DeploymentGlobalCallers: []auth.AllowedCaller{{
			CallerService: "ops-service",
			Kind:          auth.CallerKindOperator,
			Roles:         []auth.Role{auth.RoleOperatorAdmin},
		}},
		OperatorRepairStore: store,
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, operatorRepairRequest("op_123", "ops-service", validOperatorRepairBody()))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if store.calls != 1 {
		t.Fatalf("store calls = %d, want shell route through repair store", store.calls)
	}
}

func operatorRepairHandlerForTest(store OperatorRepairStore, policy AllowedCallerPolicy, sink audit.Sink) http.Handler {
	return OperatorRepairHandler(OperatorRepairHandlerConfig{
		Store:              store,
		PrincipalResolver:  operatorRepairPrincipalResolver(),
		AllowedCallers:     policy,
		GenerateAuditEvent: func() string { return "audit_123" },
		Now:                func() time.Time { return time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC) },
		AuditSink:          sink,
	})
}

func operatorRepairPrincipalResolver() PrincipalResolver {
	return fakePrincipalResolver{principal: auth.AuthenticatedPrincipal{Subject: "svc:ops-service", CanonicalCallerService: "ops-service"}}
}

func operatorRepairPolicy(callers ...auth.AllowedCaller) AllowedCallerPolicy {
	return fakeAllowedCallerPolicy{callers: callers}
}

func operatorRepairRequest(operationID, callerService, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/operations/"+operationID+":repair", bytes.NewBufferString(body))
	req.Header.Set(auth.HeaderAuthorization, "Bearer test-token")
	req.Header.Set(HeaderCorrelationID, "corr_repair")
	req.Header.Set(auth.HeaderCallerService, callerService)
	req.Header.Set(auth.HeaderIdempotencyKey, "idem_repair")
	req.Header.Set(auth.HeaderActorType, "operator")
	req.Header.Set(auth.HeaderActorID, "ops-user")
	return req
}

func validOperatorRepairBody() string {
	return `{"action":"terminalize_unsupported_intervention_as_failed","reason":"unsupported operation recovery reviewed","evidence_ref":"docs/runbooks/ga-runbooks.md#op-123","affected_ids":{"operation_id":"op_123","repo_id":"repo_123"}}`
}

type fakeOperatorRepairStore struct {
	calls           int
	last            operatorrepair.CommitRequest
	before          operations.OperationRecord
	now             time.Time
	alreadyTerminal bool
}

func (store *fakeOperatorRepairStore) ReadOperationForRepair(_ context.Context, operationID string) (operations.OperationRecord, error) {
	if store.alreadyTerminal {
		record := store.before
		record.State = operations.OperationStateFailed
		return record, nil
	}
	if store.before.ID == "" {
		return operations.OperationRecord{}, operatorrepair.ErrOperationNotRepairable
	}
	if operationID != store.before.ID {
		return operations.OperationRecord{}, operatorrepair.ErrOperationNotRepairable
	}
	return store.before, nil
}

func (store *fakeOperatorRepairStore) CommitOperatorRepairFailed(_ context.Context, request operatorrepair.CommitRequest) (operations.OperationRecord, error) {
	store.calls++
	store.last = request
	if store.alreadyTerminal {
		return operations.OperationRecord{}, operatorrepair.ErrAlreadyTerminal
	}
	return request.After, nil
}

func operatorRepairAPIEligibleRecord(operationID string) operations.OperationRecord {
	record := operationInspectionRecord(operationID, "ns_123")
	record.State = operations.OperationStateOperatorInterventionRequired
	record.Phase = operations.OperationPhaseRepoCreateValidate
	record.Error = &operations.OperationError{
		Code:        "OPERATION_RECOVERY_REQUIRED",
		Message:     "operation recovery is unsupported; operator intervention required",
		OperationID: operationID,
		Details: map[string]any{
			"reason":   "unsupported_operation_recovery",
			"evidence": "worker_recovery_disabled",
			"password": "secret-value",
		},
	}
	record.VerificationResult = map[string]any{"reason": "unsupported_operation_recovery"}
	return record
}

func assertOperatorRepairResponseDoesNotLeak(t *testing.T, body string) {
	t.Helper()
	for _, forbidden := range []string{"secret-value", "/srv/afscp", "Bearer", ".jvs", "password"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("operator repair response leaked %q: %s", forbidden, body)
		}
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("repair response JSON did not unmarshal: %v", err)
	}
}
