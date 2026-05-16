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

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/projectionguard"
)

const (
	workspaceMain          = "main"
	schemaVersionV048      = 1
	directContractV1       = "jvs.afscp.direct.v1"
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

type RepoCloneSummary struct {
	SourceRepoID          string
	TargetRepoID          string
	SavePointsMode        string
	SavePointsCopiedCount int
	RuntimeStateCopied    bool
	Workspace             string
}

type DirectTarget struct {
	ControlRoot string
	Home        string
}

type DirectSaveSummary struct {
	SavePointID   string
	HistoryHeadID string
	Message       string
	CreatedAt     string
}

type DirectSavePointSummary struct {
	SavePointID string
	Message     string
	CreatedAt   string
	HistoryHead bool
}

type DirectListSummary struct {
	HistoryHeadID string
	SavePoints    []DirectSavePointSummary
}

type DirectRestoreSummary struct {
	RestoredSavePointID string
	PreviousHeadID      string
	NewHeadID           string
}

type DirectStatusSummary struct {
	HistoryHeadID   string
	MetadataState   string
	ActiveOperation string
	Recovery        string
}

type DirectDoctorSummary struct {
	RepoID        string
	Healthy       bool
	FindingCount  int
	MetadataState string
	Journal       string
	Recovery      string
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

func (runner *Runner) VerifyAFSCPDirectCapability(ctx context.Context) error {
	if runner == nil {
		return ErrInvalidConfig
	}
	checks := []struct {
		name     string
		args     []string
		required []string
	}{
		{name: "afscp --help", args: []string{"afscp", "--help"}, required: []string{"jvs afscp", "--control-root", "--home", "--json"}},
		{name: "afscp save --help", args: []string{"afscp", "save", "--help"}, required: []string{"save", "--message", "--control-root", "--home", "--json"}},
		{name: "afscp list --help", args: []string{"afscp", "list", "--help"}, required: []string{"list", "--control-root", "--home", "--json"}},
		{name: "afscp restore --help", args: []string{"afscp", "restore", "--help"}, required: []string{"restore", "--save-point", "--control-root", "--home", "--json"}},
		{name: "afscp status --help", args: []string{"afscp", "status", "--help"}, required: []string{"status", "--control-root", "--home", "--json"}},
		{name: "afscp doctor --help", args: []string{"afscp", "doctor", "--help"}, required: []string{"doctor", "--control-root", "--home", "--json"}},
	}
	for _, check := range checks {
		result, err := runner.commandRunner.RunJVSCommand(ctx, CommandSpec{
			Path: runner.binaryPath,
			Args: check.args,
			Dir:  runner.cwd,
		})
		if err != nil {
			return commandFailedError(check.name, err)
		}
		result = runner.capResult(result)
		if result.ExitCode != 0 {
			return fmt.Errorf("%w: %s", ErrCommandFailed, check.name)
		}
		help := string(result.Stdout) + "\n" + string(result.Stderr)
		for _, required := range check.required {
			if !strings.Contains(help, required) {
				return fmt.Errorf("%w: afscp direct capability", ErrCommandFailed)
			}
		}
	}
	return nil
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

func (runner *Runner) DirectSave(ctx context.Context, target DirectTarget, message string) (DirectSaveSummary, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return DirectSaveSummary{}, fmt.Errorf("%w: save message", ErrInvalidArgument)
	}
	envelope, err := runner.runAFSCPDirectJSON(ctx, target, "save", []string{"save", "--message", message, "--json"})
	if err != nil {
		return DirectSaveSummary{}, err
	}
	savePointID, okSavePoint := directRequiredID(envelope.Data, "save_point_id")
	historyHead, okHistoryHead := directRequiredID(envelope.Data, "history_head")
	createdAt, okCreatedAt := stringFromMap(envelope.Data, "created_at")
	outMessage, okMessage := stringFromMap(envelope.Data, "message")
	if !okSavePoint || !okHistoryHead || historyHead != savePointID || !okCreatedAt || strings.TrimSpace(createdAt) == "" || !okMessage {
		return DirectSaveSummary{}, fmt.Errorf("%w: afscp save", ErrInvalidEnvelope)
	}
	return DirectSaveSummary{
		SavePointID:   savePointID,
		HistoryHeadID: historyHead,
		Message:       safeHistoryMessageText(outMessage),
		CreatedAt:     safeSummaryText(createdAt),
	}, nil
}

func (runner *Runner) DirectList(ctx context.Context, target DirectTarget) (DirectListSummary, error) {
	envelope, err := runner.runAFSCPDirectJSON(ctx, target, "list", []string{"list", "--json"})
	if err != nil {
		return DirectListSummary{}, err
	}
	rawSavePoints, ok := envelope.Data["save_points"].([]any)
	if !ok {
		return DirectListSummary{}, fmt.Errorf("%w: afscp list", ErrInvalidEnvelope)
	}
	historyHead, okHistoryHead, _ := optionalIDPresenceFromMap(envelope.Data, "history_head")
	if !okHistoryHead {
		return DirectListSummary{}, fmt.Errorf("%w: afscp list", ErrInvalidEnvelope)
	}
	savePoints := make([]DirectSavePointSummary, 0, len(rawSavePoints))
	for _, raw := range rawSavePoints {
		item, ok := raw.(map[string]any)
		if !ok {
			return DirectListSummary{}, fmt.Errorf("%w: afscp list", ErrInvalidEnvelope)
		}
		id, okID := directRequiredID(item, "save_point_id")
		message, okMessage := optionalStringFromMap(item, "message")
		createdAt, okCreatedAt := optionalStringFromMap(item, "created_at")
		itemHistoryHead, okItemHistoryHead := optionalBoolFromMap(item, "history_head")
		if !okID || !okMessage || !okCreatedAt || !okItemHistoryHead {
			return DirectListSummary{}, fmt.Errorf("%w: afscp list", ErrInvalidEnvelope)
		}
		savePoints = append(savePoints, DirectSavePointSummary{
			SavePointID: id,
			Message:     safeHistoryMessageText(message),
			CreatedAt:   safeSummaryText(createdAt),
			HistoryHead: itemHistoryHead,
		})
	}
	return DirectListSummary{HistoryHeadID: historyHead, SavePoints: savePoints}, nil
}

func (runner *Runner) DirectRestore(ctx context.Context, target DirectTarget, savePointID string) (DirectRestoreSummary, error) {
	savePointID = strings.TrimSpace(savePointID)
	if !safeOpaqueID(savePointID) {
		return DirectRestoreSummary{}, fmt.Errorf("%w: save point id", ErrInvalidArgument)
	}
	envelope, err := runner.runAFSCPDirectJSON(ctx, target, "restore", []string{"restore", "--save-point", savePointID, "--json"})
	if err != nil {
		return DirectRestoreSummary{}, err
	}
	restored, okRestored := directRequiredID(envelope.Data, "restored_save_point_id")
	previousHead, okPreviousHead, _ := optionalIDPresenceFromMap(envelope.Data, "previous_head")
	newHead, okNewHead := directRequiredID(envelope.Data, "new_head")
	if !okRestored || restored != savePointID || !okPreviousHead || !okNewHead || newHead != restored {
		return DirectRestoreSummary{}, fmt.Errorf("%w: afscp restore", ErrInvalidEnvelope)
	}
	return DirectRestoreSummary{
		RestoredSavePointID: restored,
		PreviousHeadID:      previousHead,
		NewHeadID:           newHead,
	}, nil
}

func (runner *Runner) DirectStatus(ctx context.Context, target DirectTarget) (DirectStatusSummary, error) {
	envelope, err := runner.runAFSCPDirectJSON(ctx, target, "status", []string{"status", "--json"})
	if err != nil {
		return DirectStatusSummary{}, err
	}
	historyHead, okHistoryHead, _ := optionalIDPresenceFromMap(envelope.Data, "history_head")
	metadataState, okMetadataState := requiredSafeSummaryFromMap(envelope.Data, "metadata_state")
	activeOperation, okActiveOperation := requiredSafeSummaryFromMap(envelope.Data, "active_operation")
	recovery, okRecovery := requiredSafeSummaryFromMap(envelope.Data, "recovery")
	if !okHistoryHead || !okMetadataState || !okActiveOperation || !okRecovery {
		return DirectStatusSummary{}, fmt.Errorf("%w: afscp status", ErrInvalidEnvelope)
	}
	return DirectStatusSummary{HistoryHeadID: historyHead, MetadataState: metadataState, ActiveOperation: activeOperation, Recovery: recovery}, nil
}

func (runner *Runner) DirectDoctor(ctx context.Context, target DirectTarget) (DirectDoctorSummary, error) {
	envelope, err := runner.runAFSCPDirectJSON(ctx, target, "doctor", []string{"doctor", "--json"})
	if err != nil {
		return DirectDoctorSummary{}, err
	}
	repoID, okRepoID := directRequiredID(envelope.Data, "repo_id")
	healthy, okHealthy := boolFromMap(envelope.Data, "healthy")
	findings, okFindings := envelope.Data["findings"].([]any)
	metadataState, okMetadataState := requiredSafeSummaryFromMap(envelope.Data, "metadata_state")
	journal, okJournal := requiredSafeSummaryFromMap(envelope.Data, "journal")
	recovery, okRecovery := requiredSafeSummaryFromMap(envelope.Data, "recovery")
	if !okRepoID || !okHealthy || !healthy || !okFindings || !okMetadataState || !okJournal || !okRecovery {
		return DirectDoctorSummary{}, fmt.Errorf("%w: afscp doctor", ErrInvalidEnvelope)
	}
	return DirectDoctorSummary{RepoID: repoID, Healthy: healthy, FindingCount: len(findings), MetadataState: metadataState, Journal: journal, Recovery: recovery}, nil
}

func (runner *Runner) runAFSCPDirectJSON(ctx context.Context, target DirectTarget, operation string, operationArgs []string) (afscpDirectEnvelope, error) {
	if runner == nil {
		return afscpDirectEnvelope{}, ErrInvalidConfig
	}
	if err := validateDirectTarget(target); err != nil {
		return afscpDirectEnvelope{}, err
	}
	args := []string{"afscp", "--control-root", target.ControlRoot, "--home", target.Home}
	args = append(args, operationArgs...)
	result, err := runner.commandRunner.RunJVSCommand(ctx, CommandSpec{Path: runner.binaryPath, Args: args, Dir: runner.cwd})
	if err != nil {
		return afscpDirectEnvelope{}, commandFailedError("afscp "+operation, err)
	}
	result = runner.capResult(result)
	if result.ExitCode != 0 {
		if commandErr, ok := directCommandErrorFromResult(operation, result); ok {
			commandErr.ExitCode = result.ExitCode
			return afscpDirectEnvelope{}, commandErr
		}
		return afscpDirectEnvelope{}, fmt.Errorf("%w: afscp %s", ErrCommandFailed, operation)
	}
	parsed, err := decodeAFSCPDirectEnvelope(result.Stdout)
	if err != nil {
		return afscpDirectEnvelope{}, fmt.Errorf("%w: afscp %s", ErrInvalidEnvelope, operation)
	}
	if parsed.Command != operation {
		return afscpDirectEnvelope{}, fmt.Errorf("%w: afscp %s", ErrInvalidEnvelope, operation)
	}
	if commandErr, ok := directCommandErrorFromEnvelope(operation, 0, parsed); ok {
		return afscpDirectEnvelope{}, commandErr
	}
	if !parsed.OK || parsed.Status != "succeeded" || parsed.Error != nil || parsed.Data == nil {
		return afscpDirectEnvelope{}, fmt.Errorf("%w: afscp %s", ErrInvalidEnvelope, operation)
	}
	if _, ok := forbiddenDirectFieldInValue(parsed.Data); ok {
		return afscpDirectEnvelope{}, fmt.Errorf("%w: afscp %s", ErrInvalidEnvelope, operation)
	}
	return parsed, nil
}

func validateDirectTarget(target DirectTarget) error {
	if err := validateCleanAbsolute(target.ControlRoot, true); err != nil {
		return fmt.Errorf("%w: control root", ErrInvalidArgument)
	}
	if err := validateCleanAbsolute(target.Home, true); err != nil {
		return fmt.Errorf("%w: home", ErrInvalidArgument)
	}
	if rootsOverlap(target.ControlRoot, target.Home) {
		return fmt.Errorf("%w: direct target roots overlap", ErrInvalidArgument)
	}
	return nil
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

type afscpDirectEnvelope struct {
	Contract  string                    `json:"contract"`
	Command   string                    `json:"command"`
	OK        bool                      `json:"ok"`
	Status    string                    `json:"status"`
	Data      map[string]any            `json:"data"`
	Error     *afscpDirectEnvelopeError `json:"error,omitempty"`
	Retryable bool                      `json:"retryable,omitempty"`
}

type afscpDirectEnvelopeError struct {
	Code      string `json:"code"`
	Message   string `json:"message,omitempty"`
	Retryable bool   `json:"retryable,omitempty"`
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

func decodeAFSCPDirectEnvelope(stdout []byte) (afscpDirectEnvelope, error) {
	decoder := json.NewDecoder(bytes.NewReader(stdout))
	decoder.DisallowUnknownFields()
	var parsed afscpDirectEnvelope
	if err := decoder.Decode(&parsed); err != nil {
		return afscpDirectEnvelope{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return afscpDirectEnvelope{}, ErrInvalidEnvelope
	}
	if parsed.Contract != directContractV1 {
		return afscpDirectEnvelope{}, ErrInvalidEnvelope
	}
	if parsed.Command == "" || !knownDirectStatus(parsed.Status) {
		return afscpDirectEnvelope{}, ErrInvalidEnvelope
	}
	if parsed.OK {
		if parsed.Status != "succeeded" || parsed.Data == nil || parsed.Error != nil {
			return afscpDirectEnvelope{}, ErrInvalidEnvelope
		}
		return parsed, nil
	}
	if parsed.Error == nil || !safeOpaqueID(parsed.Error.Code) {
		return afscpDirectEnvelope{}, ErrInvalidEnvelope
	}
	return parsed, nil
}

func knownDirectStatus(status string) bool {
	switch status {
	case "accepted", "running", "succeeded", "failed", "recovery_required":
		return true
	default:
		return false
	}
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

func directCommandErrorFromResult(operation string, result CommandResult) (*CommandError, bool) {
	for _, output := range [][]byte{result.Stdout, result.Stderr} {
		parsed, err := decodeAFSCPDirectEnvelope(output)
		if err != nil {
			continue
		}
		if commandErr, ok := directCommandErrorFromEnvelope(operation, result.ExitCode, parsed); ok {
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

func directCommandErrorFromEnvelope(operation string, exitCode int, parsed afscpDirectEnvelope) (*CommandError, bool) {
	if parsed.Contract != directContractV1 ||
		parsed.Command != operation ||
		parsed.OK ||
		parsed.Error == nil ||
		!safeOpaqueID(parsed.Error.Code) {
		return nil, false
	}
	return &CommandError{Command: "afscp " + operation, ExitCode: exitCode, Code: parsed.Error.Code}, true
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

func directRequiredID(object map[string]any, key string) (string, bool) {
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

func optionalIDFromMap(object map[string]any, key string) (string, bool) {
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

func optionalIDPresenceFromMap(object map[string]any, key string) (string, bool, bool) {
	value, ok := object[key]
	if !ok {
		return "", false, false
	}
	if value == nil || value == "" {
		return "", true, true
	}
	text, _ := value.(string)
	if !safeOpaqueID(text) {
		return "", false, true
	}
	return text, true, true
}

func stringFromMap(object map[string]any, key string) (string, bool) {
	value, ok := object[key]
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	return text, ok
}

func optionalStringFromMap(object map[string]any, key string) (string, bool) {
	value, ok := object[key]
	if !ok || value == nil {
		return "", true
	}
	text, ok := value.(string)
	return text, ok
}

func boolFromMap(object map[string]any, key string) (bool, bool) {
	value, ok := object[key]
	if !ok {
		return false, false
	}
	boolean, ok := value.(bool)
	return boolean, ok
}

func optionalBoolFromMap(object map[string]any, key string) (bool, bool) {
	value, ok := object[key]
	if !ok || value == nil {
		return false, true
	}
	boolean, ok := value.(bool)
	return boolean, ok
}

func requiredSafeSummaryFromMap(object map[string]any, key string) (string, bool) {
	value, ok := object[key]
	if !ok {
		return "", false
	}
	return safeDirectSummaryValue(value)
}

func safeDirectSummaryValue(value any) (string, bool) {
	switch typed := value.(type) {
	case nil:
		return "none", true
	case string:
		text := safeSummaryText(typed)
		return text, text != "" && text != "redacted"
	case bool:
		if typed {
			return "true", true
		}
		return "false", true
	case map[string]any:
		for _, key := range []string{"status", "state", "operation", "mode"} {
			if text, ok := optionalStringFromMap(typed, key); ok && text != "" {
				safe := safeSummaryText(text)
				return safe, safe != "" && safe != "redacted"
			}
		}
		if len(typed) == 0 {
			return "none", true
		}
		return "present", true
	case []any:
		if len(typed) == 0 {
			return "none", true
		}
		return "present", true
	default:
		return "", false
	}
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

func forbiddenDirectFieldInValue(value any) (string, bool) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if forbiddenDirectResultField(key) {
				return key, true
			}
			if field, ok := forbiddenDirectFieldInValue(child); ok {
				return field, true
			}
		}
	case []any:
		for _, child := range typed {
			if field, ok := forbiddenDirectFieldInValue(child); ok {
				return field, true
			}
		}
	}
	return "", false
}

func forbiddenDirectResultField(key string) bool {
	return projectionguard.ForbiddenJVSInternalField(key)
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
