package restorereconcile

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
)

type Mode string

const (
	ModeReconciling                 Mode = "reconciling"
	ModeBlockedOperatorIntervention Mode = "blocked_operator_intervention"
	ModeCompleted                   Mode = "completed"
)

type RepoStatus string

const (
	RepoStatusActive                       RepoStatus = "active"
	RepoStatusArchived                     RepoStatus = "archived"
	RepoStatusTombstoned                   RepoStatus = "tombstoned"
	RepoStatusPurged                       RepoStatus = "purged"
	RepoStatusOperatorInterventionRequired RepoStatus = "operator_intervention_required"
)

type ObservationResult string

const (
	ObservationResultClean    ObservationResult = "clean"
	ObservationResultMismatch ObservationResult = "mismatch"
)

const (
	ReasonMetadataStorageMismatch  = "metadata_storage_mismatch"
	ReasonPurgedRepoStoragePresent = "purged_repo_storage_present"
	ReasonMissingEvidence          = "missing_reconciliation_evidence"
)

type Run struct {
	ID   string
	Mode Mode
}

type Target struct {
	RunID                     string
	RepoID                    string
	NamespaceID               string
	ExpectedRepoStatus        RepoStatus
	ExpectedStorageGeneration string
	ExpectedSnapshotID        string
	ExpectedTombstoneMarker   string
	ExpectedPurgeMarker       string
}

type Observation struct {
	RunID                   string
	RepoID                  string
	NamespaceID             string
	ExpectedRepoStatus      RepoStatus
	ObservedStoragePresent  bool
	ObservedGeneration      string
	ObservedSnapshotID      string
	ObservedTombstoneMarker string
	ObservedPurgeMarker     string
	Result                  ObservationResult
	Reason                  string
	EvidenceRef             string
}

type Decision struct {
	Result             ObservationResult
	Reason             string
	AllowsResurrection bool
}

type Evidence struct {
	RunID              string
	RepoID             string
	ObservedGeneration string
	EvidenceRef        string
}

type MismatchCommit struct {
	RunID       string
	RepoID      string
	NamespaceID string
	Reason      string
	EvidenceRef string
	Audit       audit.Event
	Now         time.Time
}

type Store interface {
	ActiveRun(context.Context) (Run, error)
	ListTargets(context.Context, string) ([]Target, error)
	ObserveTarget(context.Context, Target) (Observation, error)
	CompleteRun(context.Context, string, time.Time) error
	CommitMismatch(context.Context, MismatchCommit) error
}

var (
	ErrTargetSetEmpty     = errors.New("restore reconciliation target set is empty")
	ErrObservationMissing = errors.New("restore reconciliation observation is missing")
	ErrSensitiveEvidence  = errors.New("restore reconciliation evidence contains sensitive material")
)

type Config struct {
	Store             Store
	ExplicitlyEnabled bool
	Owner             string
	AuditEventID      func() string
	Clock             func() time.Time
}

type Runner struct {
	config Config
}

type Result struct {
	Scanned   int
	Completed int
	Blocked   int
	Skipped   int
	Failed    int
}

func NewRunner(config Config) Runner {
	return Runner{config: config}
}

func DangerousWritesBlocked(run Run) bool {
	return run.Mode == ModeReconciling || run.Mode == ModeBlockedOperatorIntervention
}

func DecideObservation(observation Observation) Decision {
	if observation.ExpectedRepoStatus == RepoStatusPurged && observation.ObservedStoragePresent {
		return Decision{Result: ObservationResultMismatch, Reason: ReasonPurgedRepoStoragePresent}
	}
	if observation.Result == ObservationResultMismatch {
		reason := strings.TrimSpace(observation.Reason)
		if reason == "" {
			reason = ReasonMetadataStorageMismatch
		}
		return Decision{Result: ObservationResultMismatch, Reason: reason}
	}
	return Decision{Result: ObservationResultClean}
}

func DecideObservationForTarget(target Target, observation Observation) Decision {
	if observation.RunID == "" || observation.RepoID == "" {
		return Decision{Result: ObservationResultMismatch, Reason: ReasonMissingEvidence}
	}
	if observation.ExpectedRepoStatus == "" {
		observation.ExpectedRepoStatus = target.ExpectedRepoStatus
	}
	if observation.ExpectedRepoStatus == RepoStatusPurged && observation.ObservedStoragePresent {
		return Decision{Result: ObservationResultMismatch, Reason: ReasonPurgedRepoStoragePresent}
	}
	if observation.Result == ObservationResultMismatch {
		return DecideObservation(observation)
	}
	if strings.TrimSpace(observation.EvidenceRef) == "" ||
		strings.TrimSpace(observation.ObservedGeneration) == "" ||
		strings.TrimSpace(observation.ObservedSnapshotID) == "" ||
		strings.TrimSpace(observation.ObservedTombstoneMarker) == "" ||
		strings.TrimSpace(observation.ObservedPurgeMarker) == "" {
		return Decision{Result: ObservationResultMismatch, Reason: ReasonMissingEvidence}
	}
	if observation.ObservedGeneration != target.ExpectedStorageGeneration ||
		observation.ObservedSnapshotID != target.ExpectedSnapshotID ||
		observation.ObservedTombstoneMarker != target.ExpectedTombstoneMarker ||
		observation.ObservedPurgeMarker != target.ExpectedPurgeMarker {
		return Decision{Result: ObservationResultMismatch, Reason: ReasonMetadataStorageMismatch}
	}
	return Decision{Result: ObservationResultClean}
}

func RedactedEvidence(observation Observation) (Evidence, error) {
	for _, value := range []string{observation.EvidenceRef, observation.ObservedGeneration, observation.ObservedSnapshotID, observation.ObservedTombstoneMarker, observation.ObservedPurgeMarker} {
		if containsSensitiveMaterial(value) {
			return Evidence{}, ErrSensitiveEvidence
		}
	}
	return Evidence{
		RunID:              strings.TrimSpace(observation.RunID),
		RepoID:             strings.TrimSpace(observation.RepoID),
		ObservedGeneration: redact(observation.ObservedGeneration),
		EvidenceRef:        strings.TrimSpace(observation.EvidenceRef),
	}, nil
}

func (runner Runner) RunOnce(ctx context.Context) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !runner.config.ExplicitlyEnabled {
		return Result{}, nil
	}
	if runner.config.Store == nil {
		return Result{}, errors.New("restore reconciliation store is required")
	}
	run, err := runner.config.Store.ActiveRun(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Result{Skipped: 1}, nil
		}
		return Result{Failed: 1}, err
	}
	if run.Mode != ModeReconciling {
		if run.Mode == ModeBlockedOperatorIntervention {
			return Result{Blocked: 1}, nil
		}
		return Result{Skipped: 1}, nil
	}
	targets, err := runner.config.Store.ListTargets(ctx, run.ID)
	if err != nil {
		return Result{Failed: 1}, err
	}
	if len(targets) == 0 {
		return Result{Failed: 1}, ErrTargetSetEmpty
	}
	result := Result{Scanned: len(targets)}
	now := time.Now().UTC()
	if runner.config.Clock != nil {
		now = runner.config.Clock().UTC()
	}
	for _, target := range targets {
		observation, err := runner.config.Store.ObserveTarget(ctx, target)
		if err != nil {
			if errors.Is(err, ErrObservationMissing) {
				err = fmt.Errorf("%w: %s/%s", err, target.RunID, target.RepoID)
			}
			result.Failed++
			return result, err
		}
		decision := DecideObservationForTarget(target, observation)
		if decision.Result != ObservationResultMismatch {
			continue
		}
		evidence, err := RedactedEvidence(observation)
		if err != nil {
			result.Failed++
			return result, err
		}
		commit := MismatchCommit{
			RunID:       run.ID,
			RepoID:      observation.RepoID,
			NamespaceID: observation.NamespaceID,
			Reason:      decision.Reason,
			EvidenceRef: evidence.EvidenceRef,
			Audit:       newAuditEvent(runner.config, observation, decision, now),
			Now:         now,
		}
		if err := runner.config.Store.CommitMismatch(ctx, commit); err != nil {
			result.Failed++
			return result, err
		}
		result.Blocked++
	}
	if result.Blocked == 0 {
		if err := runner.config.Store.CompleteRun(ctx, run.ID, now); err != nil {
			result.Failed++
			return result, err
		}
		result.Completed = 1
	}
	return result, nil
}

func newAuditEvent(config Config, observation Observation, decision Decision, now time.Time) audit.Event {
	owner := strings.TrimSpace(config.Owner)
	if owner == "" {
		owner = "restore-reconcile-worker"
	}
	eventID := ""
	if config.AuditEventID != nil {
		eventID = strings.TrimSpace(config.AuditEventID())
	}
	if eventID == "" {
		eventID = "evt_restore_reconcile_" + strings.TrimPrefix(observation.RepoID, "repo_")
	}
	return audit.NewEvent(audit.Event{
		EventID:         eventID,
		Type:            audit.EventTypeRestoreReconciliation,
		Time:            now,
		CallerService:   owner,
		AuthorizedActor: audit.Actor{Type: "service", ID: owner},
		CorrelationID:   "corr_restore_reconcile_" + strings.TrimPrefix(observation.RepoID, "repo_"),
		Resource:        audit.Resource{Type: "repo", ID: observation.RepoID, NamespaceID: observation.NamespaceID},
		Outcome:         audit.OutcomeFailed,
		Reason:          "restore_reconciliation_mismatch",
		Details: map[string]any{
			"run_id":       observation.RunID,
			"repo_id":      observation.RepoID,
			"reason":       decision.Reason,
			"evidence_ref": redact(observation.EvidenceRef),
		},
	})
}

func containsSensitiveMaterial(value string) bool {
	lower := strings.ToLower(value)
	for _, forbidden := range []string{"/raw/root", ".jvs", "metadata_url", "secretref", "token", "password", "credential"} {
		if strings.Contains(lower, forbidden) {
			return true
		}
	}
	return false
}

func redact(value string) string {
	value = strings.ReplaceAll(value, "/raw/root", "[REDACTED]")
	value = strings.ReplaceAll(value, ".jvs", "[REDACTED]")
	value = strings.ReplaceAll(value, "token=secret", "token=[REDACTED]")
	value = strings.ReplaceAll(value, "secret", "[REDACTED]")
	return value
}
