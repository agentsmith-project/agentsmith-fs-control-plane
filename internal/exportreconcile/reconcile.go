package exportreconcile

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/exportaccess"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
)

const defaultLimit = 10

type Store interface {
	ListExportSessionsForTerminalReconcile(ctx context.Context, now time.Time, limit int) ([]exportaccess.Session, error)
	ReconcileExportSessionTerminal(ctx context.Context, request exportaccess.ReconcileRequest) (exportaccess.ReconcileResult, error)
}

type Config struct {
	Store        Store
	Owner        string
	Limit        int
	Now          time.Time
	Clock        func() time.Time
	AuditEventID func() string
}

type Runner struct {
	config Config
}

type Result struct {
	Scanned      int
	Terminalized int
	Reused       int
	Skipped      int
	RaceLost     int
	Failed       int
}

func New(config Config) Runner {
	return Runner{config: config}
}

func (runner Runner) RunOnce(ctx context.Context) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	config, now, err := runner.validatedConfig()
	if err != nil {
		return Result{}, err
	}

	sessions, err := config.Store.ListExportSessionsForTerminalReconcile(ctx, now, config.Limit)
	if err != nil {
		return Result{}, err
	}
	result := Result{Scanned: len(sessions)}
	if result.Scanned > config.Limit {
		result.Scanned = config.Limit
	}
	for idx, session := range sessions {
		if idx >= config.Limit {
			break
		}
		target, reason, ok := targetStatus(session, now)
		if !ok {
			result.Skipped++
			continue
		}
		request, err := reconcileRequest(config, session, target, reason, now)
		if err != nil {
			result.Failed++
			return result, err
		}
		reconciled, err := config.Store.ReconcileExportSessionTerminal(ctx, request)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				result.RaceLost++
				continue
			}
			result.Failed++
			return result, fmt.Errorf("reconcile export session %q: %w", session.ID, err)
		}
		if reconciled.Reused {
			result.Reused++
		} else {
			result.Terminalized++
		}
	}
	return result, nil
}

func (runner Runner) validatedConfig() (Config, time.Time, error) {
	config := runner.config
	if config.Store == nil {
		return Config{}, time.Time{}, errors.New("export session reconcile store is required")
	}
	config.Owner = strings.TrimSpace(config.Owner)
	if config.Owner == "" {
		return Config{}, time.Time{}, errors.New("export session reconcile owner is required")
	}
	if config.Limit <= 0 {
		config.Limit = defaultLimit
	}
	now := config.Now
	if now.IsZero() && config.Clock != nil {
		now = config.Clock()
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return config, now.UTC(), nil
}

func targetStatus(session exportaccess.Session, now time.Time) (sessionstate.ExportStatus, string, bool) {
	if session.ActiveRequestCount != 0 || session.ActiveWriteCount != 0 {
		return "", "", false
	}
	switch {
	case session.Status == sessionstate.ExportStatusRevoking:
		return sessionstate.ExportStatusRevoked, "export session drained after revoke", true
	case session.Status == sessionstate.ExportStatusActive && !session.ExpiresAt.After(now):
		return sessionstate.ExportStatusExpired, "export session expired with no active requests", true
	default:
		return "", "", false
	}
}

func reconcileRequest(config Config, session exportaccess.Session, target sessionstate.ExportStatus, reason string, now time.Time) (exportaccess.ReconcileRequest, error) {
	key := session.ID + ":" + string(target)
	operationID := stableID("op_exportreconcile_", key)
	requestHash, err := operations.HashRequest(map[string]any{
		"export_id":     session.ID,
		"namespace_id":  session.NamespaceID,
		"target_status": string(target),
	})
	if err != nil {
		return exportaccess.ReconcileRequest{}, err
	}
	started := now
	finished := now
	record := operations.OperationRecord{
		ID:                  operationID,
		Type:                operations.OperationExportSessionReconcile,
		State:               operations.OperationStateSucceeded,
		Phase:               operations.OperationPhaseExportSessionReconcileCommitted,
		Attempt:             1,
		IdempotencyScope:    operations.NewIdempotencyScope(config.Owner, session.NamespaceID, operations.OperationExportSessionReconcile, key).String(),
		IdempotencyKey:      key,
		RequestHash:         requestHash,
		CorrelationID:       "corr_" + operationID,
		CallerService:       config.Owner,
		AuthorizedActor:     operations.Actor{Type: "service", ID: config.Owner},
		Resource:            operations.ResourceRef{Type: "export", ID: session.ID},
		NamespaceID:         session.NamespaceID,
		RepoID:              session.RepoID,
		ExportID:            session.ID,
		ExternalResourceIDs: map[string]string{},
		InputSummary: map[string]any{
			"export_id":     session.ID,
			"repo_id":       session.RepoID,
			"target_status": string(target),
		},
		CreatedAt:  now,
		StartedAt:  &started,
		FinishedAt: &finished,
	}
	eventID := ""
	if config.AuditEventID != nil {
		eventID = strings.TrimSpace(config.AuditEventID())
	}
	if eventID == "" {
		eventID = stableID("evt_exportreconcile_", key)
	}
	event := audit.NewEvent(audit.Event{
		EventID:         eventID,
		Type:            audit.EventTypeExportSessionReconcile,
		Time:            now,
		CallerService:   config.Owner,
		AuthorizedActor: audit.Actor{Type: "service", ID: config.Owner},
		CorrelationID:   record.CorrelationID,
		OperationID:     record.ID,
		Resource:        audit.Resource{Type: "export", ID: session.ID, NamespaceID: session.NamespaceID},
		Outcome:         audit.OutcomeSucceeded,
		Reason:          reason,
		Details: map[string]any{
			"export_id":     session.ID,
			"repo_id":       session.RepoID,
			"target_status": string(target),
		},
	})
	return exportaccess.ReconcileRequest{
		ExportID:           session.ID,
		NamespaceID:        session.NamespaceID,
		TargetStatus:       target,
		ObservedAt:         now,
		StatusReason:       reason,
		ActiveRequestCount: 0,
		ActiveWriteCount:   0,
		Operation:          record,
		Audit:              event,
	}, nil
}

func stableID(prefix, key string) string {
	sum := sha256.Sum256([]byte(key))
	return prefix + hex.EncodeToString(sum[:8])
}
