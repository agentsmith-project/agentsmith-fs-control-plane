package postgres

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/restoreplan"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store"
)

func TestStoreImplementsRestorePlanContracts(t *testing.T) {
	var _ store.RestorePlanReader = (*Store)(nil)
	var _ store.RestorePlanWriter = (*Store)(nil)
	var _ store.RestorePlanStore = (*Store)(nil)
}

func TestCreatePendingRestorePlanInsertsLifecycleSourceOfTruth(t *testing.T) {
	exec := &fakeExecutor{}
	st := &Store{exec: exec}
	plan := restorePlanFixture(time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC))

	if err := st.CreatePendingRestorePlan(context.Background(), plan); err != nil {
		t.Fatalf("CreatePendingRestorePlan: %v", err)
	}

	assertSQLContainsInOrder(t, exec.query,
		"INSERT INTO restore_plans",
		"restore_plan_id", "namespace_id", "repo_id", "preview_operation_id",
		"source_save_point_id", "status", "created_at", "updated_at",
	)
	wantArgs := []any{
		"b644aec4-bcb6-4480-b5fa-a283927dd3cd",
		"ns_alpha01",
		"repo_alpha01",
		"op_preview01",
		"sp_001",
		string(restoreplan.StatusPending),
		plan.CreatedAt,
		plan.UpdatedAt,
	}
	if !reflect.DeepEqual(exec.args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", exec.args, wantArgs)
	}
	if strings.Contains(strings.ToLower(exec.query), "operationrecord") || strings.Contains(strings.ToLower(exec.query), "operation_state") {
		t.Fatalf("restore plan insert leaked operation lifecycle columns: %s", exec.query)
	}
}

func TestCreatePendingRestorePlanRejectsNonPendingBeforeSQL(t *testing.T) {
	exec := &fakeExecutor{}
	st := &Store{exec: exec}
	plan := restorePlanFixture(time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC))
	plan.Status = restoreplan.StatusConsuming

	err := st.CreatePendingRestorePlan(context.Background(), plan)
	if err == nil {
		t.Fatal("CreatePendingRestorePlan succeeded, want validation error")
	}
	if exec.query != "" || exec.execCalls != 0 {
		t.Fatalf("query calls/query = %d/%q, want no SQL", exec.execCalls, exec.query)
	}
}

func TestGetRestorePlanByPreviewOperationSelectsExactPreviewOperation(t *testing.T) {
	plan := restorePlanFixture(time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC))
	exec := &fakeExecutor{row: fakeRow{values: restorePlanRowValues(plan)}}
	st := &Store{exec: exec}

	got, err := st.GetRestorePlanByPreviewOperation(context.Background(), "op_preview01")
	if err != nil {
		t.Fatalf("GetRestorePlanByPreviewOperation: %v", err)
	}

	if got.ID != plan.ID || got.PreviewOperationID != "op_preview01" || got.SourceSavePointID != "sp_001" {
		t.Fatalf("plan = %#v, want fixture", got)
	}
	assertSQLContainsInOrder(t, exec.query,
		"SELECT",
		"FROM restore_plans",
		"WHERE preview_operation_id = $1",
	)
	if len(exec.args) != 1 || exec.args[0] != "op_preview01" {
		t.Fatalf("args = %#v, want preview operation id", exec.args)
	}
}

func TestGetActiveRestorePlanByRepoUsesActiveStatuses(t *testing.T) {
	plan := restorePlanFixture(time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC))
	plan.Status = restoreplan.StatusDiscarding
	exec := &fakeExecutor{row: fakeRow{values: restorePlanRowValues(plan)}}
	st := &Store{exec: exec}

	got, err := st.GetActiveRestorePlanByRepo(context.Background(), "repo_alpha01")
	if err != nil {
		t.Fatalf("GetActiveRestorePlanByRepo: %v", err)
	}

	if got.ID != plan.ID || got.Status != restoreplan.StatusDiscarding || !got.Active() {
		t.Fatalf("plan = %#v, want active discarding plan", got)
	}
	assertSQLContainsInOrder(t, exec.query,
		"FROM restore_plans",
		"WHERE repo_id = $1",
		"status IN ('pending', 'consuming', 'discarding', 'operator_intervention_required')",
		"ORDER BY created_at, restore_plan_id",
		"LIMIT 1",
	)
	if strings.Contains(exec.query, "operations") {
		t.Fatalf("active restore plan read must not infer from operations: %s", exec.query)
	}
}

func TestTransitionRestorePlanStatusUsesValidatedCompareAndSwapUpdate(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	returned := restorePlanFixture(now)
	returned.Status = restoreplan.StatusConsuming
	returned.UpdatedAt = now.Add(time.Minute)
	exec := &fakeExecutor{row: fakeRow{values: restorePlanRowValues(returned)}}
	st := &Store{exec: exec}

	got, err := st.TransitionRestorePlanStatus(context.Background(), "b644aec4-bcb6-4480-b5fa-a283927dd3cd", restoreplan.StatusPending, restoreplan.StatusConsuming, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("TransitionRestorePlanStatus: %v", err)
	}

	if got.Status != restoreplan.StatusConsuming || !got.UpdatedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("plan = %#v, want consuming updated plan", got)
	}
	assertSQLContainsInOrder(t, exec.query,
		"UPDATE restore_plans SET",
		"status = $3",
		"updated_at = $4",
		"WHERE restore_plan_id = $1",
		"status = $2",
		"RETURNING",
	)
	wantArgs := []any{"b644aec4-bcb6-4480-b5fa-a283927dd3cd", string(restoreplan.StatusPending), string(restoreplan.StatusConsuming), now.Add(time.Minute)}
	if !reflect.DeepEqual(exec.args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", exec.args, wantArgs)
	}
}

func TestTransitionRestorePlanStatusRejectsInvalidTransitionBeforeSQL(t *testing.T) {
	exec := &fakeExecutor{}
	st := &Store{exec: exec}
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)

	_, err := st.TransitionRestorePlanStatus(context.Background(), "b644aec4-bcb6-4480-b5fa-a283927dd3cd", restoreplan.StatusPending, restoreplan.StatusConsumed, now)
	if err == nil {
		t.Fatal("TransitionRestorePlanStatus succeeded, want validation error")
	}
	if exec.query != "" || exec.queryRowCalls != 0 {
		t.Fatalf("query calls/query = %d/%q, want no SQL", exec.queryRowCalls, exec.query)
	}
}

func TestRestorePlanReadersAndTransitionReturnSQLNoRows(t *testing.T) {
	st := &Store{exec: &fakeExecutor{row: fakeRow{err: sql.ErrNoRows}}}
	if _, err := st.GetRestorePlanByPreviewOperation(context.Background(), "op_missing01"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetRestorePlanByPreviewOperation error = %v, want sql.ErrNoRows", err)
	}
	if _, err := st.GetActiveRestorePlanByRepo(context.Background(), "repo_missing01"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetActiveRestorePlanByRepo error = %v, want sql.ErrNoRows", err)
	}
	if _, err := st.TransitionRestorePlanStatus(context.Background(), "f1380d48-0ad4-4b86-b875-0fb3e27103fa", restoreplan.StatusPending, restoreplan.StatusDiscarding, time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("TransitionRestorePlanStatus error = %v, want sql.ErrNoRows", err)
	}
}

func restorePlanFixture(now time.Time) restoreplan.Plan {
	return restoreplan.Plan{
		ID:                 "b644aec4-bcb6-4480-b5fa-a283927dd3cd",
		NamespaceID:        "ns_alpha01",
		RepoID:             "repo_alpha01",
		PreviewOperationID: "op_preview01",
		SourceSavePointID:  "sp_001",
		Status:             restoreplan.StatusPending,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
}

func restorePlanRowValues(plan restoreplan.Plan) []any {
	return []any{
		plan.ID,
		plan.NamespaceID,
		plan.RepoID,
		plan.PreviewOperationID,
		plan.SourceSavePointID,
		string(plan.Status),
		plan.CreatedAt,
		plan.UpdatedAt,
	}
}
