package jvsrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	workspaceMain          = "main"
	schemaVersionV048      = 1
	defaultMaxOutputBytes  = 64 * 1024
	maxSafeRepoIDLength    = 128
	maxHistoryMessageRunes = 512
)

var (
	ErrInvalidConfig   = errors.New("invalid jvs runner config")
	ErrInvalidArgument = errors.New("invalid jvs runner argument")
	ErrCommandFailed   = errors.New("jvs command failed")
	ErrInvalidEnvelope = errors.New("invalid jvs json envelope")
)

type CommandError struct {
	Command  string
	ExitCode int
	Code     string
}

func (err *CommandError) Error() string {
	if err == nil {
		return ErrCommandFailed.Error()
	}
	if err.Code != "" {
		return fmt.Sprintf("%s: %s: %s", ErrCommandFailed, err.Command, err.Code)
	}
	return fmt.Sprintf("%s: %s", ErrCommandFailed, err.Command)
}

func (err *CommandError) Unwrap() error {
	return ErrCommandFailed
}

type Config struct {
	BinaryPath     string
	CWD            string
	CommandRunner  CommandRunner
	MaxOutputBytes int
}

type Runner struct {
	binaryPath     string
	cwd            string
	commandRunner  CommandRunner
	maxOutputBytes int
}

type CommandRunner interface {
	RunJVSCommand(ctx context.Context, spec CommandSpec) (CommandResult, error)
}

type CommandSpec struct {
	Path string
	Args []string
	Dir  string
}

type CommandResult struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
}

type InitSummary struct {
	RepoID    string
	Workspace string
}

type DoctorSummary struct {
	RepoID    string
	Healthy   bool
	Workspace string
}

type RepairActionSummary struct {
	Action  string
	Success bool
	Cleaned int
}

type DoctorRepairRuntimeSummary struct {
	RepoID     string
	Healthy    bool
	Workspace  string
	CleanLocks RepairActionSummary
}

type SaveSummary struct {
	SavePointID       string
	NewestSavePointID string
	Workspace         string
	UnsavedChanges    bool
	CreatedAt         string
}

type SavePointSummary struct {
	SavePointID string
	Message     string
	CreatedAt   string
}

type HistorySummary struct {
	Workspace         string
	NewestSavePointID string
	SavePoints        []SavePointSummary
}

type RestorePreviewSummary struct {
	PlanID            string
	SourceSavePointID string
	BaseRevision      string
	HeadRevision      string
	Generation        string
	ManagedFiles      RestorePreviewManagedFilesSummary
	Workspace         string
	RunCommandPresent bool
}

type RestorePreviewChangeSummary struct {
	Count   int
	Samples []string
}

type RestorePreviewManagedFilesSummary struct {
	Added       RestorePreviewChangeSummary
	Changed     RestorePreviewChangeSummary
	Removed     RestorePreviewChangeSummary
	Destructive bool
}

type RestoreRunSummary struct {
	PlanID              string
	SourceSavePointID   string
	RestoredSavePointID string
	Workspace           string
}

type RestoreDiscardSummary struct {
	PlanID        string
	PlanDiscarded bool
	Workspace     string
}

type RecoveryStatusSummary struct {
	RestoreState         string
	ActivePlanID         string
	ActiveRecoveryPlanID string
	Blocking             bool
	Message              string
	Workspace            string
}

type RepoCloneSummary struct {
	SourceRepoID          string
	TargetRepoID          string
	SavePointsMode        string
	SavePointsCopiedCount int
	RuntimeStateCopied    bool
	Workspace             string
}

func New(config Config) (*Runner, error) {
	if err := validateCleanAbsolute(config.BinaryPath, true); err != nil {
		return nil, fmt.Errorf("%w: binary path", ErrInvalidConfig)
	}
	if err := validateCleanAbsolute(config.CWD, true); err != nil {
		return nil, fmt.Errorf("%w: cwd", ErrInvalidConfig)
	}

	maxOutputBytes := config.MaxOutputBytes
	if maxOutputBytes == 0 {
		maxOutputBytes = defaultMaxOutputBytes
	}
	if maxOutputBytes < 0 {
		return nil, fmt.Errorf("%w: max output bytes", ErrInvalidConfig)
	}

	commandRunner := config.CommandRunner
	if commandRunner == nil {
		commandRunner = osCommandRunner{maxOutputBytes: maxOutputBytes}
	}

	return &Runner{
		binaryPath:     config.BinaryPath,
		cwd:            config.CWD,
		commandRunner:  commandRunner,
		maxOutputBytes: maxOutputBytes,
	}, nil
}

func (runner *Runner) Init(ctx context.Context, payloadRoot, controlRoot string) (InitSummary, error) {
	if runner == nil {
		return InitSummary{}, ErrInvalidConfig
	}
	if err := validateCleanAbsolute(payloadRoot, true); err != nil {
		return InitSummary{}, fmt.Errorf("%w: payload root", ErrInvalidArgument)
	}
	if err := validateCleanAbsolute(controlRoot, true); err != nil {
		return InitSummary{}, fmt.Errorf("%w: control root", ErrInvalidArgument)
	}
	if rootsOverlap(payloadRoot, controlRoot) {
		return InitSummary{}, fmt.Errorf("%w: repo roots overlap", ErrInvalidArgument)
	}

	result, err := runner.commandRunner.RunJVSCommand(ctx, CommandSpec{
		Path: runner.binaryPath,
		Args: []string{
			"init",
			payloadRoot,
			"--control-root",
			controlRoot,
			"--workspace",
			workspaceMain,
			"--json",
		},
		Dir: runner.cwd,
	})
	if err != nil {
		return InitSummary{}, commandFailedError("init", err)
	}
	result = runner.capResult(result)
	if result.ExitCode != 0 {
		if commandErr, ok := commandErrorFromResult("init", controlRoot, result); ok {
			return InitSummary{}, commandErr
		}
		return InitSummary{}, fmt.Errorf("%w: init", ErrCommandFailed)
	}

	envelope, err := decodeEnvelope(result.Stdout)
	if err != nil {
		return InitSummary{}, fmt.Errorf("%w: init", ErrInvalidEnvelope)
	}
	if commandErr, ok := commandErrorFromEnvelope("init", controlRoot, 0, envelope); ok {
		return InitSummary{}, commandErr
	}
	repoID, _ := envelope.Data["repo_id"].(string)
	workspace, _ := envelope.Data["workspace"].(string)
	if !envelope.validFor("init", controlRoot) || workspace != workspaceMain || !safeOpaqueID(repoID) {
		return InitSummary{}, fmt.Errorf("%w: init", ErrInvalidEnvelope)
	}

	return InitSummary{RepoID: repoID, Workspace: workspace}, nil
}

func (runner *Runner) DoctorStrict(ctx context.Context, controlRoot string) (DoctorSummary, error) {
	if runner == nil {
		return DoctorSummary{}, ErrInvalidConfig
	}
	if err := validateCleanAbsolute(controlRoot, true); err != nil {
		return DoctorSummary{}, fmt.Errorf("%w: control root", ErrInvalidArgument)
	}

	result, err := runner.commandRunner.RunJVSCommand(ctx, CommandSpec{
		Path: runner.binaryPath,
		Args: []string{
			"--control-root",
			controlRoot,
			"--workspace",
			workspaceMain,
			"doctor",
			"--strict",
			"--json",
		},
		Dir: runner.cwd,
	})
	if err != nil {
		return DoctorSummary{}, commandFailedError("doctor", err)
	}
	result = runner.capResult(result)
	if result.ExitCode != 0 {
		if commandErr, ok := commandErrorFromResult("doctor", controlRoot, result); ok {
			return DoctorSummary{}, commandErr
		}
		return DoctorSummary{}, fmt.Errorf("%w: doctor", ErrCommandFailed)
	}

	envelope, err := decodeEnvelope(result.Stdout)
	if err != nil {
		return DoctorSummary{}, fmt.Errorf("%w: doctor", ErrInvalidEnvelope)
	}
	if commandErr, ok := commandErrorFromEnvelope("doctor", controlRoot, 0, envelope); ok {
		return DoctorSummary{}, commandErr
	}
	healthy, ok := envelope.Data["healthy"].(bool)
	repoID, _ := envelope.Data["repo_id"].(string)
	workspace, _ := envelope.Data["workspace"].(string)
	if !envelope.validFor("doctor", controlRoot) || !ok || !healthy || !safeOpaqueID(repoID) || workspace != workspaceMain {
		return DoctorSummary{}, fmt.Errorf("%w: doctor", ErrInvalidEnvelope)
	}

	return DoctorSummary{RepoID: repoID, Healthy: true, Workspace: workspace}, nil
}

func (runner *Runner) DoctorRepairRuntime(ctx context.Context, controlRoot string) (DoctorRepairRuntimeSummary, error) {
	if runner == nil {
		return DoctorRepairRuntimeSummary{}, ErrInvalidConfig
	}
	if err := validateCleanAbsolute(controlRoot, true); err != nil {
		return DoctorRepairRuntimeSummary{}, fmt.Errorf("%w: control root", ErrInvalidArgument)
	}

	result, err := runner.commandRunner.RunJVSCommand(ctx, CommandSpec{
		Path: runner.binaryPath,
		Args: []string{
			"--control-root",
			controlRoot,
			"--workspace",
			workspaceMain,
			"doctor",
			"--strict",
			"--repair-runtime",
			"--json",
		},
		Dir: runner.cwd,
	})
	if err != nil {
		return DoctorRepairRuntimeSummary{}, commandFailedError("doctor", err)
	}
	result = runner.capResult(result)
	if result.ExitCode != 0 {
		if commandErr, ok := commandErrorFromResult("doctor", controlRoot, result); ok {
			return DoctorRepairRuntimeSummary{}, commandErr
		}
		return DoctorRepairRuntimeSummary{}, fmt.Errorf("%w: doctor", ErrCommandFailed)
	}

	envelope, err := decodeEnvelope(result.Stdout)
	if err != nil {
		return DoctorRepairRuntimeSummary{}, fmt.Errorf("%w: doctor", ErrInvalidEnvelope)
	}
	if commandErr, ok := commandErrorFromEnvelope("doctor", controlRoot, 0, envelope); ok {
		return DoctorRepairRuntimeSummary{}, commandErr
	}
	healthy, healthyOK := envelope.Data["healthy"].(bool)
	repoID, _ := envelope.Data["repo_id"].(string)
	workspace, _ := envelope.Data["workspace"].(string)
	cleanLocks, cleanLocksOK := repairActionSummary(envelope.Data["repairs"], "clean_locks")
	if !envelope.validFor("doctor", controlRoot) || !healthyOK || !healthy || !safeOpaqueID(repoID) || workspace != workspaceMain || !cleanLocksOK || !cleanLocks.Success {
		return DoctorRepairRuntimeSummary{}, fmt.Errorf("%w: doctor", ErrInvalidEnvelope)
	}

	return DoctorRepairRuntimeSummary{RepoID: repoID, Healthy: true, Workspace: workspace, CleanLocks: cleanLocks}, nil
}

func (runner *Runner) Save(ctx context.Context, controlRoot, message string) (SaveSummary, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return SaveSummary{}, fmt.Errorf("%w: save message", ErrInvalidArgument)
	}
	envelope, err := runner.runControlJSON(ctx, "save", controlRoot, []string{"save", "--message", message, "--json"})
	if err != nil {
		return SaveSummary{}, err
	}
	savePointID, ok := dataSafeID(envelope, "save_point_id")
	workspace, _ := envelope.Data["workspace"].(string)
	newest, newestOK := dataSafeID(envelope, "newest_save_point")
	unsavedChanges, unsavedOK := envelope.Data["unsaved_changes"].(bool)
	createdAt, createdAtOK := envelope.Data["created_at"].(string)
	if !ok || !newestOK || newest != savePointID || workspace != workspaceMain || !unsavedOK || !createdAtOK || strings.TrimSpace(createdAt) == "" {
		return SaveSummary{}, fmt.Errorf("%w: save", ErrInvalidEnvelope)
	}
	return SaveSummary{SavePointID: savePointID, NewestSavePointID: newest, Workspace: workspace, UnsavedChanges: unsavedChanges, CreatedAt: safeSummaryText(createdAt)}, nil
}

func (runner *Runner) History(ctx context.Context, controlRoot string) (HistorySummary, error) {
	envelope, err := runner.runControlJSON(ctx, "history", controlRoot, []string{"history", "--limit", "0", "--json"})
	if err != nil {
		return HistorySummary{}, err
	}
	workspace, _ := envelope.Data["workspace"].(string)
	truncated, truncatedOK := envelope.Data["truncated"].(bool)
	if workspace != workspaceMain || !truncatedOK || truncated {
		return HistorySummary{}, fmt.Errorf("%w: history", ErrInvalidEnvelope)
	}
	rawSavePoints, ok := envelope.Data["save_points"].([]any)
	if !ok {
		return HistorySummary{}, fmt.Errorf("%w: history", ErrInvalidEnvelope)
	}
	savePoints := make([]SavePointSummary, 0, len(rawSavePoints))
	for _, raw := range rawSavePoints {
		item, ok := raw.(map[string]any)
		if !ok {
			return HistorySummary{}, fmt.Errorf("%w: history", ErrInvalidEnvelope)
		}
		if workspace, exists := item["workspace"]; exists && workspace != workspaceMain {
			return HistorySummary{}, fmt.Errorf("%w: history", ErrInvalidEnvelope)
		}
		id, _ := item["save_point_id"].(string)
		if !safeOpaqueID(id) {
			return HistorySummary{}, fmt.Errorf("%w: history", ErrInvalidEnvelope)
		}
		message, ok := item["message"].(string)
		if !ok {
			return HistorySummary{}, fmt.Errorf("%w: history", ErrInvalidEnvelope)
		}
		createdAt, ok := item["created_at"].(string)
		if !ok || strings.TrimSpace(createdAt) == "" {
			return HistorySummary{}, fmt.Errorf("%w: history", ErrInvalidEnvelope)
		}
		savePoints = append(savePoints, SavePointSummary{SavePointID: id, Message: safeHistoryMessageText(message), CreatedAt: safeSummaryText(createdAt)})
	}
	newest := ""
	if len(savePoints) > 0 {
		var newestOK bool
		newest, newestOK = dataSafeID(envelope, "newest_save_point")
		if !newestOK || newest != savePoints[0].SavePointID {
			return HistorySummary{}, fmt.Errorf("%w: history", ErrInvalidEnvelope)
		}
	} else if _, exists := envelope.Data["newest_save_point"]; exists {
		var newestOK bool
		newest, newestOK = optionalDataSafeID(envelope, "newest_save_point")
		if !newestOK {
			return HistorySummary{}, fmt.Errorf("%w: history", ErrInvalidEnvelope)
		}
	}
	return HistorySummary{Workspace: workspace, NewestSavePointID: newest, SavePoints: savePoints}, nil
}

func (runner *Runner) RestorePreview(ctx context.Context, controlRoot, savePointID string) (RestorePreviewSummary, error) {
	if !safeOpaqueID(strings.TrimSpace(savePointID)) {
		return RestorePreviewSummary{}, fmt.Errorf("%w: save point id", ErrInvalidArgument)
	}
	envelope, err := runner.runControlJSON(ctx, "restore", controlRoot, []string{"restore", strings.TrimSpace(savePointID), "--json"})
	if err != nil {
		return RestorePreviewSummary{}, err
	}
	planID, okPlan := dataSafeID(envelope, "plan_id")
	source, okSource := dataSafeID(envelope, "source_save_point")
	base, okBase := dataSafeID(envelope, "expected_newest_save_point")
	head, okHead := dataSafeID(envelope, "history_head")
	generation, okGeneration := dataSafeID(envelope, "expected_folder_evidence")
	managedFiles, okManagedFiles := parseRestorePreviewManagedFiles(envelope.Data["managed_files"])
	workspace, _ := envelope.Data["workspace"].(string)
	runCommand, _ := envelope.Data["run_command"].(string)
	filesChanged, filesOK := envelope.Data["files_changed"].(bool)
	historyChanged, historyOK := envelope.Data["history_changed"].(bool)
	if envelope.Data["mode"] != "preview" || workspace != workspaceMain || !okPlan || !okSource || !okBase || !okHead || !okGeneration || !okManagedFiles || strings.TrimSpace(runCommand) == "" || !filesOK || filesChanged || !historyOK || historyChanged {
		return RestorePreviewSummary{}, fmt.Errorf("%w: restore preview", ErrInvalidEnvelope)
	}
	return RestorePreviewSummary{
		PlanID:            planID,
		SourceSavePointID: source,
		BaseRevision:      base,
		HeadRevision:      head,
		Generation:        generation,
		ManagedFiles:      managedFiles,
		Workspace:         workspace,
		RunCommandPresent: true,
	}, nil
}

func (runner *Runner) RestoreRun(ctx context.Context, controlRoot, planID string) (RestoreRunSummary, error) {
	planID = strings.TrimSpace(planID)
	if !safeOpaqueID(planID) {
		return RestoreRunSummary{}, fmt.Errorf("%w: plan id", ErrInvalidArgument)
	}
	envelope, err := runner.runControlJSON(ctx, "restore", controlRoot, []string{"restore", "--run", planID, "--json"})
	if err != nil {
		return RestoreRunSummary{}, err
	}
	outPlanID, okPlan := dataSafeID(envelope, "plan_id")
	source, okSource, hasSource := optionalDataSafeIDPresence(envelope, "source_save_point")
	restored, okRestored, hasRestored := optionalDataSafeIDPresence(envelope, "restored_save_point")
	workspace, _ := envelope.Data["workspace"].(string)
	_, filesOK := envelope.Data["files_changed"].(bool)
	historyChanged, historyOK := envelope.Data["history_changed"].(bool)
	unsavedChanges, unsavedOK := envelope.Data["unsaved_changes"].(bool)
	if envelope.Data["mode"] != "run" || workspace != workspaceMain || !okPlan || outPlanID != planID || !okSource || !okRestored || (!hasSource && !hasRestored) || !filesOK || !historyOK || historyChanged || !unsavedOK || unsavedChanges {
		return RestoreRunSummary{}, fmt.Errorf("%w: restore run", ErrInvalidEnvelope)
	}
	return RestoreRunSummary{PlanID: outPlanID, SourceSavePointID: source, RestoredSavePointID: restored, Workspace: workspace}, nil
}

func (runner *Runner) RestoreDiscard(ctx context.Context, controlRoot, planID string) (RestoreDiscardSummary, error) {
	planID = strings.TrimSpace(planID)
	if !safeOpaqueID(planID) {
		return RestoreDiscardSummary{}, fmt.Errorf("%w: plan id", ErrInvalidArgument)
	}
	envelope, err := runner.runControlJSON(ctx, "restore discard", controlRoot, []string{"restore", "discard", planID, "--json"})
	if err != nil {
		return RestoreDiscardSummary{}, err
	}
	outPlanID, okPlan := dataSafeID(envelope, "plan_id")
	workspace, _ := envelope.Data["workspace"].(string)
	discarded, discardedOK := envelope.Data["plan_discarded"].(bool)
	filesChanged, filesOK := envelope.Data["files_changed"].(bool)
	historyChanged, historyOK := envelope.Data["history_changed"].(bool)
	if envelope.Data["mode"] != "discard" || workspace != workspaceMain || !okPlan || outPlanID != planID || !discardedOK || !discarded || !filesOK || filesChanged || !historyOK || historyChanged {
		return RestoreDiscardSummary{}, fmt.Errorf("%w: restore discard", ErrInvalidEnvelope)
	}
	return RestoreDiscardSummary{PlanID: outPlanID, PlanDiscarded: true, Workspace: workspace}, nil
}

func (runner *Runner) RecoveryStatus(ctx context.Context, controlRoot string) (RecoveryStatusSummary, error) {
	envelope, err := runner.runControlJSON(ctx, "recovery status", controlRoot, []string{"recovery", "status", "--json"})
	if err != nil {
		return RecoveryStatusSummary{}, err
	}
	workspace, _ := envelope.Data["workspace"].(string)
	if workspace != workspaceMain {
		return RecoveryStatusSummary{}, fmt.Errorf("%w: recovery status", ErrInvalidEnvelope)
	}
	rawPlans, plansOK := envelope.Data["plans"].([]any)
	if !plansOK {
		return RecoveryStatusSummary{}, fmt.Errorf("%w: recovery status", ErrInvalidEnvelope)
	}
	if rawState, exists := envelope.Data["restore_state"]; exists && rawState != nil {
		state, err := parseRestoreStateObject(rawState)
		if err != nil {
			return RecoveryStatusSummary{}, fmt.Errorf("%w: recovery status", ErrInvalidEnvelope)
		}
		state.Workspace = workspace
		if err := validateRecoveryPlans(rawPlans); err != nil {
			return RecoveryStatusSummary{}, fmt.Errorf("%w: recovery status", ErrInvalidEnvelope)
		}
		return state, nil
	}
	if len(rawPlans) == 0 {
		return RecoveryStatusSummary{RestoreState: "idle", Workspace: workspace}, nil
	}
	state, err := parseActiveRecoveryPlans(rawPlans)
	if err != nil {
		return RecoveryStatusSummary{}, fmt.Errorf("%w: recovery status", ErrInvalidEnvelope)
	}
	state.Workspace = workspace
	return state, nil
}

func (runner *Runner) RepoClone(ctx context.Context, sourceControlRoot, targetPayloadRoot, targetControlRoot string) (RepoCloneSummary, error) {
	if runner == nil {
		return RepoCloneSummary{}, ErrInvalidConfig
	}
	for _, item := range []struct {
		name string
		path string
	}{
		{name: "source control root", path: sourceControlRoot},
		{name: "target payload root", path: targetPayloadRoot},
		{name: "target control root", path: targetControlRoot},
	} {
		if err := validateCleanAbsolute(item.path, true); err != nil {
			return RepoCloneSummary{}, fmt.Errorf("%w: %s", ErrInvalidArgument, item.name)
		}
	}
	if rootsOverlap(sourceControlRoot, targetPayloadRoot) || rootsOverlap(sourceControlRoot, targetControlRoot) || rootsOverlap(targetPayloadRoot, targetControlRoot) {
		return RepoCloneSummary{}, fmt.Errorf("%w: repo roots overlap", ErrInvalidArgument)
	}
	envelope, err := runner.runControlJSONExpectRoot(ctx, "repo clone", sourceControlRoot, targetControlRoot, []string{"repo", "clone", targetPayloadRoot, "--target-control-root", targetControlRoot, "--save-points", "main", "--json"})
	if err != nil {
		return RepoCloneSummary{}, err
	}
	sourceRepoID, okSource := dataSafeID(envelope, "source_repo_id")
	targetRepoID, okTarget := dataSafeID(envelope, "target_repo_id")
	savePointsMode, _ := envelope.Data["save_points_mode"].(string)
	targetFolder, _ := envelope.Data["target_folder"].(string)
	outTargetControl, _ := envelope.Data["target_control_root"].(string)
	copiedCount, countOK := intData(envelope.Data["save_points_copied_count"])
	runtimeCopied, runtimeOK := envelope.Data["runtime_state_copied"].(bool)
	if !okSource || !okTarget || savePointsMode != "main" || targetFolder != targetPayloadRoot || outTargetControl != targetControlRoot || !countOK || copiedCount < 0 || !runtimeOK || runtimeCopied {
		return RepoCloneSummary{}, fmt.Errorf("%w: repo clone", ErrInvalidEnvelope)
	}
	return RepoCloneSummary{SourceRepoID: sourceRepoID, TargetRepoID: targetRepoID, SavePointsMode: savePointsMode, SavePointsCopiedCount: copiedCount, RuntimeStateCopied: runtimeCopied, Workspace: workspaceMain}, nil
}

func (runner *Runner) runControlJSON(ctx context.Context, command, controlRoot string, commandArgs []string) (envelope, error) {
	return runner.runControlJSONExpectRoot(ctx, command, controlRoot, controlRoot, commandArgs)
}

func (runner *Runner) runControlJSONExpectRoot(ctx context.Context, command, selectorControlRoot, expectedSuccessRepoRoot string, commandArgs []string) (envelope, error) {
	if runner == nil {
		return envelope{}, ErrInvalidConfig
	}
	if err := validateCleanAbsolute(selectorControlRoot, true); err != nil {
		return envelope{}, fmt.Errorf("%w: control root", ErrInvalidArgument)
	}
	if err := validateCleanAbsolute(expectedSuccessRepoRoot, true); err != nil {
		return envelope{}, fmt.Errorf("%w: envelope repo root", ErrInvalidArgument)
	}
	args := []string{"--control-root", selectorControlRoot, "--workspace", workspaceMain}
	args = append(args, commandArgs...)
	result, err := runner.commandRunner.RunJVSCommand(ctx, CommandSpec{Path: runner.binaryPath, Args: args, Dir: runner.cwd})
	if err != nil {
		return envelope{}, commandFailedError(command, err)
	}
	result = runner.capResult(result)
	if result.ExitCode != 0 {
		if commandErr, ok := commandErrorFromResult(command, expectedSuccessRepoRoot, result, selectorControlRoot); ok {
			return envelope{}, commandErr
		}
		return envelope{}, fmt.Errorf("%w: %s", ErrCommandFailed, command)
	}
	parsed, err := decodeEnvelope(result.Stdout)
	if err != nil {
		return envelope{}, fmt.Errorf("%w: %s", ErrInvalidEnvelope, command)
	}
	if commandErr, ok := commandErrorFromEnvelope(command, expectedSuccessRepoRoot, 0, parsed, selectorControlRoot); ok {
		return envelope{}, commandErr
	}
	if !parsed.validFor(command, expectedSuccessRepoRoot) {
		return envelope{}, fmt.Errorf("%w: %s", ErrInvalidEnvelope, command)
	}
	return parsed, nil
}

func (runner *Runner) capResult(result CommandResult) CommandResult {
	result.Stdout = capBytes(result.Stdout, runner.maxOutputBytes)
	result.Stderr = capBytes(result.Stderr, runner.maxOutputBytes)
	return result
}

type envelope struct {
	SchemaVersion int            `json:"schema_version"`
	Command       string         `json:"command"`
	RepoRoot      string         `json:"repo_root"`
	Workspace     string         `json:"workspace"`
	OK            bool           `json:"ok"`
	Data          map[string]any `json:"data"`
	Error         *envelopeError `json:"error"`
}

type envelopeError struct {
	Code string `json:"code"`
}

func decodeEnvelope(stdout []byte) (envelope, error) {
	decoder := json.NewDecoder(bytes.NewReader(stdout))
	var parsed envelope
	if err := decoder.Decode(&parsed); err != nil {
		return envelope{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return envelope{}, ErrInvalidEnvelope
	}
	if parsed.Data == nil {
		parsed.Data = map[string]any{}
	}
	return parsed, nil
}

func commandErrorFromResult(command, acceptedRepoRoot string, result CommandResult, otherAcceptedRepoRoots ...string) (*CommandError, bool) {
	for _, output := range [][]byte{result.Stdout, result.Stderr} {
		parsed, err := decodeEnvelope(output)
		if err != nil {
			continue
		}
		if commandErr, ok := commandErrorFromEnvelope(command, acceptedRepoRoot, result.ExitCode, parsed, otherAcceptedRepoRoots...); ok {
			return commandErr, true
		}
	}
	return nil, false
}

func commandErrorFromEnvelope(command, acceptedRepoRoot string, exitCode int, parsed envelope, otherAcceptedRepoRoots ...string) (*CommandError, bool) {
	if parsed.SchemaVersion != schemaVersionV048 ||
		parsed.Command != command ||
		parsed.Workspace != workspaceMain ||
		!repoRootMatches(parsed.RepoRoot, acceptedRepoRoot, otherAcceptedRepoRoots...) ||
		parsed.OK ||
		parsed.Error == nil ||
		!safeOpaqueID(parsed.Error.Code) {
		return nil, false
	}
	return &CommandError{Command: command, ExitCode: exitCode, Code: parsed.Error.Code}, true
}

func repoRootMatches(repoRoot, acceptedRepoRoot string, otherAcceptedRepoRoots ...string) bool {
	if repoRoot == acceptedRepoRoot {
		return true
	}
	for _, other := range otherAcceptedRepoRoots {
		if repoRoot == other {
			return true
		}
	}
	return false
}

func (parsed envelope) validFor(command, controlRoot string) bool {
	return parsed.SchemaVersion == schemaVersionV048 &&
		parsed.Command == command &&
		parsed.Workspace == workspaceMain &&
		parsed.RepoRoot == controlRoot &&
		parsed.OK
}

func (parsed envelope) boolData(key string) bool {
	value, _ := parsed.Data[key].(bool)
	return value
}

func dataSafeID(parsed envelope, key string) (string, bool) {
	value, _ := parsed.Data[key].(string)
	if !safeOpaqueID(value) {
		return "", false
	}
	return value, true
}

func optionalDataSafeID(parsed envelope, key string) (string, bool) {
	value, ok := parsed.Data[key]
	if !ok || value == nil || value == "" {
		return "", true
	}
	text, _ := value.(string)
	if !safeOpaqueID(text) {
		return "", false
	}
	return text, true
}

func optionalDataSafeIDPresence(parsed envelope, key string) (string, bool, bool) {
	value, ok := parsed.Data[key]
	if !ok || value == nil || value == "" {
		return "", true, false
	}
	text, _ := value.(string)
	if !safeOpaqueID(text) {
		return "", false, true
	}
	return text, true, true
}

func stringData(value any) string {
	text, _ := value.(string)
	return text
}

func intData(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case float64:
		if v != float64(int(v)) {
			return 0, false
		}
		return int(v), true
	default:
		return 0, false
	}
}

func repairActionSummary(raw any, action string) (RepairActionSummary, bool) {
	items, ok := raw.([]any)
	if !ok {
		return RepairActionSummary{}, false
	}
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok {
			return RepairActionSummary{}, false
		}
		itemAction, _ := item["action"].(string)
		if itemAction != action {
			continue
		}
		success, successOK := item["success"].(bool)
		if !successOK {
			return RepairActionSummary{}, false
		}
		cleaned := 0
		if rawCleaned, exists := item["cleaned"]; exists {
			var cleanedOK bool
			cleaned, cleanedOK = intData(rawCleaned)
			if !cleanedOK || cleaned < 0 {
				return RepairActionSummary{}, false
			}
		}
		return RepairActionSummary{Action: itemAction, Success: success, Cleaned: cleaned}, true
	}
	return RepairActionSummary{}, false
}

func parseRestorePreviewManagedFiles(raw any) (RestorePreviewManagedFilesSummary, bool) {
	object, ok := raw.(map[string]any)
	if !ok {
		return RestorePreviewManagedFilesSummary{}, false
	}
	changed, okChanged := parseRestorePreviewChangeSummary(object["overwrite"])
	removed, okRemoved := parseRestorePreviewChangeSummary(object["delete"])
	added, okAdded := parseRestorePreviewChangeSummary(object["create"])
	if !okChanged || !okRemoved || !okAdded {
		return RestorePreviewManagedFilesSummary{}, false
	}
	return RestorePreviewManagedFilesSummary{
		Added:       added,
		Changed:     changed,
		Removed:     removed,
		Destructive: changed.Count > 0 || removed.Count > 0,
	}, true
}

func parseRestorePreviewChangeSummary(raw any) (RestorePreviewChangeSummary, bool) {
	object, ok := raw.(map[string]any)
	if !ok {
		return RestorePreviewChangeSummary{}, false
	}
	count, ok := intData(object["count"])
	if !ok || count < 0 {
		return RestorePreviewChangeSummary{}, false
	}
	rawSamples, ok := object["samples"].([]any)
	if !ok {
		if count == 0 {
			return RestorePreviewChangeSummary{Count: count, Samples: []string{}}, true
		}
		return RestorePreviewChangeSummary{}, false
	}
	if len(rawSamples) > 10 {
		return RestorePreviewChangeSummary{}, false
	}
	samples := make([]string, 0, len(rawSamples))
	for _, rawSample := range rawSamples {
		sample, ok := rawSample.(string)
		if !ok || !safeDisplayPath(sample) {
			return RestorePreviewChangeSummary{}, false
		}
		samples = append(samples, sample)
	}
	return RestorePreviewChangeSummary{Count: count, Samples: samples}, true
}

func knownRestoreState(state string) bool {
	switch state {
	case "idle", "none", "pending_restore_preview", "stale_restore_preview":
		return true
	default:
		return false
	}
}

func parseRestoreStateObject(raw any) (RecoveryStatusSummary, error) {
	object, ok := raw.(map[string]any)
	if !ok {
		return RecoveryStatusSummary{}, ErrInvalidEnvelope
	}
	state, _ := object["state"].(string)
	if !knownRestoreState(state) || state == "idle" || state == "none" {
		return RecoveryStatusSummary{}, ErrInvalidEnvelope
	}
	blocking, blockingOK := object["blocking"].(bool)
	if !blockingOK {
		return RecoveryStatusSummary{}, ErrInvalidEnvelope
	}
	planID, okPlan := safeRequiredIDFromMap(object, "plan_id")
	recoveryPlanID, okRecovery := safeOptionalIDFromMap(object, "recovery_plan_id")
	if !okPlan || !okRecovery {
		return RecoveryStatusSummary{}, ErrInvalidEnvelope
	}
	return RecoveryStatusSummary{
		RestoreState:         state,
		ActivePlanID:         planID,
		ActiveRecoveryPlanID: recoveryPlanID,
		Blocking:             blocking,
		Message:              safeSummaryText(stringData(object["message"])),
	}, nil
}

func parseActiveRecoveryPlans(rawPlans []any) (RecoveryStatusSummary, error) {
	var active RecoveryStatusSummary
	activeCount := 0
	for _, raw := range rawPlans {
		plan, ok := raw.(map[string]any)
		if !ok {
			return RecoveryStatusSummary{}, ErrInvalidEnvelope
		}
		status := strings.ToLower(stringData(plan["status"]))
		if status != "active" {
			continue
		}
		activeCount++
		if activeCount > 1 {
			return RecoveryStatusSummary{}, ErrInvalidEnvelope
		}
		recoveryPlanID, okPlan := safeRequiredIDFromMap(plan, "plan_id")
		restorePlanID, okRestore := safeOptionalIDFromMap(plan, "restore_plan_id")
		if !okPlan || !okRestore {
			return RecoveryStatusSummary{}, ErrInvalidEnvelope
		}
		active = RecoveryStatusSummary{
			RestoreState:         "active_recovery",
			ActiveRecoveryPlanID: recoveryPlanID,
			ActivePlanID:         restorePlanID,
			Blocking:             true,
		}
	}
	if activeCount != 1 {
		return RecoveryStatusSummary{}, ErrInvalidEnvelope
	}
	return active, nil
}

func validateRecoveryPlans(rawPlans []any) error {
	for _, raw := range rawPlans {
		plan, ok := raw.(map[string]any)
		if !ok {
			return ErrInvalidEnvelope
		}
		if _, ok := safeOptionalIDFromMap(plan, "plan_id"); !ok {
			return ErrInvalidEnvelope
		}
		state, _ := plan["state"].(string)
		if state != "" && !knownRestoreState(state) {
			return ErrInvalidEnvelope
		}
	}
	return nil
}

func safeRequiredIDFromMap(object map[string]any, key string) (string, bool) {
	value, ok := object[key]
	if !ok || value == nil || value == "" {
		return "", false
	}
	text, _ := value.(string)
	if !safeOpaqueID(text) {
		return "", false
	}
	return text, true
}

func safeOptionalIDFromMap(object map[string]any, key string) (string, bool) {
	value, ok := object[key]
	if !ok || value == nil || value == "" {
		return "", true
	}
	text, _ := value.(string)
	if !safeOpaqueID(text) {
		return "", false
	}
	return text, true
}

func safeDisplayPath(value string) bool {
	if len(value) == 0 || len(value) > 256 || strings.TrimSpace(value) != value {
		return false
	}
	if strings.HasPrefix(value, "/") || strings.Contains(value, "\\") || strings.Contains(value, "\x00") {
		return false
	}
	clean := path.Clean(value)
	if clean != value || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return false
	}
	parts := strings.Split(value, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	for i := 0; i < len(value); i++ {
		b := value[i]
		if b < 0x20 || b == 0x7f {
			return false
		}
	}
	lower := strings.ToLower(value)
	return !strings.Contains(lower, ".jvs") && !strings.Contains(lower, "control_root") && !containsShellFragment(lower)
}

func safeSummaryText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) > 128 {
		return "redacted"
	}
	for i := 0; i < len(value); i++ {
		b := value[i]
		if asciiAlphaNum(b) || b == '_' || b == '-' || b == '.' || b == ':' {
			continue
		}
		return "redacted"
	}
	return value
}

func safeHistoryMessageText(value string) string {
	if strings.TrimSpace(value) == "" {
		return value
	}
	if utf8.RuneCountInString(value) > maxHistoryMessageRunes || historyMessageLooksSensitive(value) {
		return "redacted"
	}
	return value
}

func historyMessageLooksSensitive(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{
		".jvs",
		"control_root",
		"payload_root",
		"raw_path",
		"--control-root",
		"--target-control-root",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	if containsAbsolutePath(value) || containsShellFragment(lower) {
		return true
	}
	return false
}

func containsAbsolutePath(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] != '/' {
			continue
		}
		if i == 0 || strings.ContainsRune(" \t\r\n'\"([{<=", rune(value[i-1])) {
			return true
		}
	}
	return false
}

func containsShellFragment(lower string) bool {
	for _, marker := range []string{"&&", "||", "`", "$(", "| sh", "|sh", " sh -c", " bash -c", "rm -rf", "sudo ", "jvs "} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func validateCleanAbsolute(path string, rejectRoot bool) error {
	if path == "" {
		return ErrInvalidArgument
	}
	if !filepath.IsAbs(path) {
		return ErrInvalidArgument
	}
	if filepath.Clean(path) != path {
		return ErrInvalidArgument
	}
	if rejectRoot && path == string(filepath.Separator) {
		return ErrInvalidArgument
	}
	return nil
}

func rootsOverlap(first, second string) bool {
	return first == second || pathContains(first, second) || pathContains(second, first)
}

func pathContains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func safeOpaqueID(id string) bool {
	if len(id) == 0 || len(id) > maxSafeRepoIDLength {
		return false
	}
	for i := 0; i < len(id); i++ {
		b := id[i]
		if i == 0 {
			if !asciiAlphaNum(b) {
				return false
			}
			continue
		}
		if !asciiAlphaNum(b) && b != '_' && b != '.' && b != ':' && b != '-' {
			return false
		}
	}
	return true
}

func asciiAlphaNum(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}

func capBytes(value []byte, limit int) []byte {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit]
}

func commandContextError(err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	case errors.Is(err, context.Canceled):
		return context.Canceled
	default:
		return nil
	}
}

func commandFailedError(command string, err error) error {
	if contextErr := commandContextError(err); contextErr != nil {
		return fmt.Errorf("%w: %s: %w", ErrCommandFailed, command, contextErr)
	}
	return fmt.Errorf("%w: %s", ErrCommandFailed, command)
}

type osCommandRunner struct {
	maxOutputBytes int
}

func (runner osCommandRunner) RunJVSCommand(ctx context.Context, spec CommandSpec) (CommandResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, spec.Path, spec.Args...)
	cmd.Dir = spec.Dir

	stdout := newBoundedBuffer(runner.maxOutputBytes)
	stderr := newBoundedBuffer(runner.maxOutputBytes)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	result := CommandResult{
		Stdout: stdout.Bytes(),
		Stderr: stderr.Bytes(),
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return result, ctxErr
	}
	if err == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	return result, err
}

type boundedBuffer struct {
	limit int
	buf   bytes.Buffer
}

func newBoundedBuffer(limit int) *boundedBuffer {
	return &boundedBuffer{limit: limit}
}

func (buffer *boundedBuffer) Write(p []byte) (int, error) {
	if buffer.limit <= 0 {
		return len(p), nil
	}
	remaining := buffer.limit - buffer.buf.Len()
	if remaining > 0 {
		if len(p) < remaining {
			remaining = len(p)
		}
		_, _ = buffer.buf.Write(p[:remaining])
	}
	return len(p), nil
}

func (buffer *boundedBuffer) Bytes() []byte {
	return append([]byte(nil), buffer.buf.Bytes()...)
}
