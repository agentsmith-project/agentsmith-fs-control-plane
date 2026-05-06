package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/pathresolver"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/restoreplan"
)

var restorePlanColumns = []string{
	"restore_plan_id",
	"namespace_id",
	"repo_id",
	"preview_operation_id",
	"source_save_point_id",
	"status",
	"created_at",
	"updated_at",
}

func (store *Store) CreatePendingRestorePlan(ctx context.Context, plan restoreplan.Plan) error {
	if plan.Status != restoreplan.StatusPending {
		return fmt.Errorf("create restore plan requires pending status, got %q", plan.Status)
	}
	if err := plan.Validate(); err != nil {
		return err
	}
	_, err := store.exec.ExecContext(ctx, restorePlanInsertSQL(), restorePlanInsertArgs(plan)...)
	return err
}

func (store *Store) GetRestorePlanByPreviewOperation(ctx context.Context, previewOperationID string) (restoreplan.Plan, error) {
	if err := pathresolver.ValidateID(pathresolver.OperationID, previewOperationID); err != nil {
		return restoreplan.Plan{}, err
	}
	row := store.exec.QueryRowContext(ctx, restorePlanSelectSQL()+" WHERE preview_operation_id = $1", previewOperationID)
	return scanRestorePlan(row)
}

func (store *Store) GetActiveRestorePlanByRepo(ctx context.Context, repoID string) (restoreplan.Plan, error) {
	if err := pathresolver.ValidateID(pathresolver.RepoID, repoID); err != nil {
		return restoreplan.Plan{}, err
	}
	row := store.exec.QueryRowContext(ctx, restorePlanSelectSQL()+" WHERE repo_id = $1 AND status IN ("+restorePlanActiveStatusSQLList()+") ORDER BY created_at, restore_plan_id LIMIT 1", repoID)
	return scanRestorePlan(row)
}

func (store *Store) TransitionRestorePlanStatus(ctx context.Context, restorePlanID string, from, to restoreplan.Status, now time.Time) (restoreplan.Plan, error) {
	if err := restoreplan.ValidateID(restorePlanID); err != nil {
		return restoreplan.Plan{}, err
	}
	if !restoreplan.ValidTransition(from, to) {
		return restoreplan.Plan{}, fmt.Errorf("invalid restore plan transition %q -> %q", from, to)
	}
	if now.IsZero() {
		return restoreplan.Plan{}, fmt.Errorf("transition restore plan status: now must be set")
	}
	row := store.exec.QueryRowContext(ctx, restorePlanTransitionSQL(), restorePlanID, string(from), string(to), now.UTC())
	plan, err := scanRestorePlan(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return restoreplan.Plan{}, fmt.Errorf("%w: transition restore plan %q from %q to %q", sql.ErrNoRows, restorePlanID, from, to)
		}
		return restoreplan.Plan{}, err
	}
	return plan, nil
}

func restorePlanInsertSQL() string {
	return "INSERT INTO restore_plans (" + strings.Join(restorePlanColumns, ", ") + ") VALUES (" + placeholders(1, len(restorePlanColumns)) + ")"
}

func restorePlanSelectSQL() string {
	return "SELECT " + strings.Join(restorePlanColumns, ", ") + " FROM restore_plans"
}

func restorePlanTransitionSQL() string {
	return "UPDATE restore_plans SET status = $3, updated_at = $4 WHERE restore_plan_id = $1 AND status = $2 RETURNING " + strings.Join(restorePlanColumns, ", ")
}

func restorePlanActiveStatusSQLList() string {
	return "'pending', 'consuming', 'discarding', 'operator_intervention_required'"
}

func restorePlanInsertArgs(plan restoreplan.Plan) []any {
	return []any{
		plan.ID,
		plan.NamespaceID,
		plan.RepoID,
		plan.PreviewOperationID,
		plan.SourceSavePointID,
		string(plan.Status),
		plan.CreatedAt.UTC(),
		plan.UpdatedAt.UTC(),
	}
}

func scanRestorePlan(row rowScanner) (restoreplan.Plan, error) {
	var plan restoreplan.Plan
	var status string
	if err := row.Scan(
		&plan.ID,
		&plan.NamespaceID,
		&plan.RepoID,
		&plan.PreviewOperationID,
		&plan.SourceSavePointID,
		&status,
		&plan.CreatedAt,
		&plan.UpdatedAt,
	); err != nil {
		return restoreplan.Plan{}, err
	}
	plan.Status = restoreplan.Status(status)
	if err := plan.Validate(); err != nil {
		return restoreplan.Plan{}, err
	}
	return plan, nil
}
