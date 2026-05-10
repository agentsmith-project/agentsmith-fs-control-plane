package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
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
	"base_revision",
	"head_revision",
	"generation",
	"fence_marker",
	"summary_json",
	"blockers_json",
	"stale",
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
	summaryJSON, err := marshalRestorePlanSummary(plan.Summary)
	if err != nil {
		panic(err)
	}
	blockersJSON, err := marshalRestorePlanBlockers(plan.Blockers)
	if err != nil {
		panic(err)
	}
	return []any{
		plan.ID,
		plan.NamespaceID,
		plan.RepoID,
		plan.PreviewOperationID,
		plan.SourceSavePointID,
		plan.BaseRevision,
		plan.HeadRevision,
		plan.Generation,
		plan.FenceMarker,
		summaryJSON,
		blockersJSON,
		plan.Stale,
		string(plan.Status),
		plan.CreatedAt.UTC(),
		plan.UpdatedAt.UTC(),
	}
}

func scanRestorePlan(row rowScanner) (restoreplan.Plan, error) {
	var plan restoreplan.Plan
	if err := scanRestorePlanPrefix(row, &plan); err != nil {
		return restoreplan.Plan{}, err
	}
	if err := plan.Validate(); err != nil {
		return restoreplan.Plan{}, err
	}
	return plan, nil
}

func scanRestorePlanPrefix(row rowScanner, plan *restoreplan.Plan, extra ...any) error {
	var status string
	var summaryJSON, blockersJSON []byte
	dest := []any{
		&plan.ID,
		&plan.NamespaceID,
		&plan.RepoID,
		&plan.PreviewOperationID,
		&plan.SourceSavePointID,
		&plan.BaseRevision,
		&plan.HeadRevision,
		&plan.Generation,
		&plan.FenceMarker,
		&summaryJSON,
		&blockersJSON,
		&plan.Stale,
		&status,
		&plan.CreatedAt,
		&plan.UpdatedAt,
	}
	dest = append(dest, extra...)
	if err := row.Scan(dest...); err != nil {
		return err
	}
	summary, err := unmarshalRestorePlanSummary(summaryJSON)
	if err != nil {
		return err
	}
	blockers, err := unmarshalRestorePlanBlockers(blockersJSON)
	if err != nil {
		return err
	}
	plan.Summary = summary
	plan.Blockers = blockers
	plan.Status = restoreplan.Status(status)
	return nil
}

func marshalRestorePlanSummary(summary restoreplan.Summary) ([]byte, error) {
	if err := summary.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(restoreplan.SummaryMap(summary))
}

func marshalRestorePlanBlockers(blockers []restoreplan.Blocker) ([]byte, error) {
	if err := restoreplan.ValidateBlockers(blockers); err != nil {
		return nil, err
	}
	return json.Marshal(restoreplan.BlockersList(blockers))
}

func unmarshalRestorePlanSummary(data []byte) (restoreplan.Summary, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return restoreplan.Summary{}, err
	}
	return restoreplan.SummaryFromMap(raw)
}

func unmarshalRestorePlanBlockers(data []byte) ([]restoreplan.Blocker, error) {
	var raw []any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return restoreplan.BlockersFromList(raw)
}
