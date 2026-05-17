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
	"time"
	"unicode/utf8"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/projectionguard"
)

const (
	workspaceMain               = "main"
	schemaVersionV048           = 1
	directContractV1            = "jvs.afscp.direct.v1"
	defaultMaxOutputBytes       = 64 * 1024
	maxSafeRepoIDLength         = 128
	maxHistoryMessageRunes      = 512
	directPurposeTemplateSource = "template_source"
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
	Purpose     string
	CreatedAt   string
}

type HistorySummary struct {
	Workspace         string
	NewestSavePointID string
	SavePoints        []SavePointSummary
}

type DirectTarget struct {
	ControlRoot string
	Home        string
}

type DirectSaveSummary struct {
	SavePointID   string
	HistoryHeadID string
	Message       string
	Purpose       string
	CreatedAt     string
	CloneEvidence []CloneEvidence
}

type CloneEvidence struct {
	Operation  string `json:"operation"`
	Phase      string `json:"phase"`
	Engine     string `json:"engine"`
	Status     string `json:"status"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	DurationMs int64  `json:"duration_ms"`
}

func (evidence CloneEvidence) String() string {
	return fmt.Sprintf("{operation:%s engine:%s status:%s duration_ms:%d}", evidence.Operation, evidence.Engine, evidence.Status, evidence.DurationMs)
}

type DirectSavePointSummary struct {
	SavePointID string
	Message     string
	Purpose     string
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
	CloneEvidence       []CloneEvidence
}

type DirectCloneSummary struct {
	SourceRepoID          string
	TargetRepoID          string
	SavePointID           string
	SavePointsMode        string
	SavePointsCopiedCount int
	RuntimeStateCopied    bool
	Workspace             string
	CloneEvidence         []CloneEvidence
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
	Findings      []DirectDoctorFindingSummary
	MetadataState string
	Journal       string
	Recovery      string
}

type DirectDoctorFindingSummary struct {
	Code      string
	Severity  string
	Message   string
	Retryable bool
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
		{name: "afscp save --help", args: []string{"afscp", "save", "--help"}, required: []string{"save", "--message", "--purpose", "--control-root", "--home", "--json"}},
		{name: "afscp list --help", args: []string{"afscp", "list", "--help"}, required: []string{"list", "--control-root", "--home", "--json"}},
		{name: "afscp restore --help", args: []string{"afscp", "restore", "--help"}, required: []string{"restore", "--save-point", "--control-root", "--home", "--json"}},
		{name: "afscp clone --help", args: []string{"afscp", "clone", "--help"}, required: []string{"clone", "--target-control-root", "--target-home", "--control-root", "--home", "--json"}},
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

func (runner *Runner) DirectSave(ctx context.Context, target DirectTarget, message string) (DirectSaveSummary, error) {
	return runner.DirectSaveWithPurpose(ctx, target, message, "")
}

func (runner *Runner) DirectSaveWithPurpose(ctx context.Context, target DirectTarget, message string, purpose string) (DirectSaveSummary, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return DirectSaveSummary{}, fmt.Errorf("%w: save message", ErrInvalidArgument)
	}
	purpose = strings.TrimSpace(purpose)
	if purpose != "" && purpose != directPurposeTemplateSource {
		return DirectSaveSummary{}, fmt.Errorf("%w: save purpose", ErrInvalidArgument)
	}
	args := []string{"save", "--message", message}
	if purpose != "" {
		args = append(args, "--purpose", purpose)
	}
	args = append(args, "--json")
	envelope, err := runner.runAFSCPDirectJSON(ctx, target, "save", args)
	if err != nil {
		return DirectSaveSummary{}, err
	}
	savePointID, okSavePoint := directRequiredID(envelope.Data, "save_point_id")
	historyHead, okHistoryHead := directRequiredID(envelope.Data, "history_head")
	createdAt, okCreatedAt := stringFromMap(envelope.Data, "created_at")
	outMessage, okMessage := stringFromMap(envelope.Data, "message")
	outPurpose, okPurpose := optionalStringFromMap(envelope.Data, "purpose")
	cloneEvidence, okCloneEvidence := cloneEvidenceFromDirectData(envelope.Data, "save")
	if !okSavePoint || !okHistoryHead || historyHead != savePointID || !okCreatedAt || strings.TrimSpace(createdAt) == "" || !okMessage || !okPurpose || !okCloneEvidence {
		return DirectSaveSummary{}, fmt.Errorf("%w: afscp save", ErrInvalidEnvelope)
	}
	return DirectSaveSummary{
		SavePointID:   savePointID,
		HistoryHeadID: historyHead,
		Message:       safeHistoryMessageText(outMessage),
		Purpose:       safeSummaryText(outPurpose),
		CreatedAt:     safeSummaryText(createdAt),
		CloneEvidence: cloneEvidence,
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
		purpose, okPurpose := optionalStringFromMap(item, "purpose")
		createdAt, okCreatedAt := optionalStringFromMap(item, "created_at")
		itemHistoryHead, okItemHistoryHead := optionalBoolFromMap(item, "history_head")
		if !okID || !okMessage || !okPurpose || !okCreatedAt || !okItemHistoryHead {
			return DirectListSummary{}, fmt.Errorf("%w: afscp list", ErrInvalidEnvelope)
		}
		savePoints = append(savePoints, DirectSavePointSummary{
			SavePointID: id,
			Message:     safeHistoryMessageText(message),
			Purpose:     safeSummaryText(purpose),
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
	cloneEvidence, okCloneEvidence := cloneEvidenceFromDirectData(envelope.Data, "restore")
	if !okRestored || restored != savePointID || !okPreviousHead || !okNewHead || newHead != restored || !okCloneEvidence {
		return DirectRestoreSummary{}, fmt.Errorf("%w: afscp restore", ErrInvalidEnvelope)
	}
	return DirectRestoreSummary{
		RestoredSavePointID: restored,
		PreviousHeadID:      previousHead,
		NewHeadID:           newHead,
		CloneEvidence:       cloneEvidence,
	}, nil
}

func (runner *Runner) DirectClone(ctx context.Context, source DirectTarget, target DirectTarget, savePointID string) (DirectCloneSummary, error) {
	savePointID = strings.TrimSpace(savePointID)
	if savePointID != "" && !safeOpaqueID(savePointID) {
		return DirectCloneSummary{}, fmt.Errorf("%w: save point id", ErrInvalidArgument)
	}
	if err := validateDirectTarget(source); err != nil {
		return DirectCloneSummary{}, err
	}
	if err := validateDirectTarget(target); err != nil {
		return DirectCloneSummary{}, err
	}
	if rootsOverlap(source.ControlRoot, target.ControlRoot) || rootsOverlap(source.ControlRoot, target.Home) || rootsOverlap(source.Home, target.ControlRoot) || rootsOverlap(source.Home, target.Home) {
		return DirectCloneSummary{}, fmt.Errorf("%w: source and target roots overlap", ErrInvalidArgument)
	}
	args := []string{"clone", "--target-control-root", target.ControlRoot, "--target-home", target.Home, "--json"}
	if savePointID != "" {
		args = append(args, "--save-point", savePointID)
	}
	envelope, err := runner.runAFSCPDirectJSON(ctx, source, "clone", args)
	if err != nil {
		return DirectCloneSummary{}, err
	}
	sourceRepoID, okSource := directRequiredID(envelope.Data, "source_repo_id")
	targetRepoID, okTarget := directRequiredID(envelope.Data, "target_repo_id")
	clonedSavePointID, okSavePoint := directRequiredID(envelope.Data, "save_point_id")
	copiedCount, countOK := intData(envelope.Data["save_points_copied_count"])
	cloneEvidence, okCloneEvidence := cloneEvidenceFromDirectData(envelope.Data, "clone")
	if !okSource || !okTarget || !okSavePoint || (savePointID != "" && clonedSavePointID != savePointID) || !countOK || copiedCount != 1 || !okCloneEvidence {
		return DirectCloneSummary{}, fmt.Errorf("%w: afscp clone", ErrInvalidEnvelope)
	}
	return DirectCloneSummary{
		SourceRepoID:          sourceRepoID,
		TargetRepoID:          targetRepoID,
		SavePointID:           clonedSavePointID,
		SavePointsMode:        "main",
		SavePointsCopiedCount: copiedCount,
		RuntimeStateCopied:    false,
		Workspace:             workspaceMain,
		CloneEvidence:         cloneEvidence,
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
	findingSummaries, okFindingSummaries := directDoctorFindingsFromData(findings)
	if !okRepoID || !okHealthy || !okFindings || !okFindingSummaries || !okMetadataState || !okJournal || !okRecovery {
		return DirectDoctorSummary{}, fmt.Errorf("%w: afscp doctor", ErrInvalidEnvelope)
	}
	return DirectDoctorSummary{RepoID: repoID, Healthy: healthy, FindingCount: len(findingSummaries), Findings: findingSummaries, MetadataState: metadataState, Journal: journal, Recovery: recovery}, nil
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

func cloneEvidenceFromDirectData(data map[string]any, operation string) ([]CloneEvidence, bool) {
	raw, ok := data["clone_evidence"]
	if !ok {
		return nil, false
	}
	items, ok := raw.([]any)
	if !ok || len(items) == 0 {
		return nil, false
	}
	out := make([]CloneEvidence, 0, len(items))
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok {
			return nil, false
		}
		evidence, ok := cloneEvidenceFromMap(item, operation)
		if !ok {
			return nil, false
		}
		out = append(out, evidence)
	}
	return out, true
}

func directDoctorFindingsFromData(items []any) ([]DirectDoctorFindingSummary, bool) {
	out := make([]DirectDoctorFindingSummary, 0, len(items))
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok {
			return nil, false
		}
		finding, ok := directDoctorFindingFromMap(item)
		if !ok {
			return nil, false
		}
		out = append(out, finding)
	}
	return out, true
}

func directDoctorFindingFromMap(item map[string]any) (DirectDoctorFindingSummary, bool) {
	allowed := map[string]bool{
		"code":      true,
		"severity":  true,
		"message":   true,
		"retryable": true,
	}
	for key := range item {
		if !allowed[key] {
			return DirectDoctorFindingSummary{}, false
		}
	}
	code, okCode := optionalStringFromMap(item, "code")
	severity, okSeverity := requiredSafeSummaryFromMap(item, "severity")
	message, okMessage := stringFromMap(item, "message")
	retryable, okRetryable := boolFromMap(item, "retryable")
	if !okCode || !okSeverity || !okMessage || !okRetryable {
		return DirectDoctorFindingSummary{}, false
	}
	if strings.TrimSpace(code) != "" {
		code = safeSummaryText(code)
		if code == "" || code == "redacted" {
			return DirectDoctorFindingSummary{}, false
		}
	}
	message, okMessage = safeDoctorFindingMessageText(message)
	if !okMessage {
		return DirectDoctorFindingSummary{}, false
	}
	return DirectDoctorFindingSummary{Code: code, Severity: severity, Message: message, Retryable: retryable}, true
}

func safeDoctorFindingMessageText(value string) (string, bool) {
	text := strings.TrimSpace(value)
	if text == "" || len(text) > 256 {
		return "", false
	}
	if projectionguard.ContainsForbiddenJVSInternalText(text) || containsSensitiveSummaryText(text) || containsAbsolutePath(text) || containsShellFragment(strings.ToLower(text)) {
		return "", false
	}
	for i := 0; i < len(text); i++ {
		b := text[i]
		if b < 0x20 || b == 0x7f {
			return "", false
		}
	}
	return text, true
}

func cloneEvidenceFromMap(item map[string]any, expectedOperation string) (CloneEvidence, bool) {
	allowed := map[string]bool{
		"operation":   true,
		"phase":       true,
		"engine":      true,
		"status":      true,
		"started_at":  true,
		"finished_at": true,
		"duration_ms": true,
	}
	for key := range item {
		if !allowed[key] {
			return CloneEvidence{}, false
		}
	}
	operation, okOperation := requiredCloneEvidenceToken(item, "operation")
	phase, okPhase := requiredCloneEvidenceToken(item, "phase")
	engine, okEngine := requiredCloneEvidenceToken(item, "engine")
	status, okStatus := requiredCloneEvidenceToken(item, "status")
	startedAt, startedTime, okStartedAt := requiredCloneEvidenceTimestamp(item, "started_at")
	finishedAt, finishedTime, okFinishedAt := requiredCloneEvidenceTimestamp(item, "finished_at")
	durationMs, okDuration := int64Data(item["duration_ms"])
	if !okOperation || operation != expectedOperation || !okPhase || !okEngine || !okStatus || status != "succeeded" || !okStartedAt || !okFinishedAt || finishedTime.Before(startedTime) || !okDuration || durationMs < 0 {
		return CloneEvidence{}, false
	}
	return CloneEvidence{
		Operation:  operation,
		Phase:      phase,
		Engine:     engine,
		Status:     status,
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
		DurationMs: durationMs,
	}, true
}

func requiredCloneEvidenceToken(item map[string]any, key string) (string, bool) {
	value, ok := item[key]
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	if !ok {
		return "", false
	}
	return safeCloneEvidenceToken(text)
}

func requiredCloneEvidenceTimestamp(item map[string]any, key string) (string, time.Time, bool) {
	value, ok := item[key]
	if !ok {
		return "", time.Time{}, false
	}
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) != text || text == "" || projectionguard.ContainsForbiddenJVSInternalText(text) || containsSensitiveSummaryText(text) {
		return "", time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return "", time.Time{}, false
	}
	return text, parsed, true
}

func safeCloneEvidenceToken(value string) (string, bool) {
	if projectionguard.ContainsForbiddenJVSInternalText(value) || containsSensitiveSummaryText(value) || containsAbsolutePath(value) || containsShellFragment(strings.ToLower(value)) {
		return "", false
	}
	text := safeSummaryText(value)
	return text, text != "" && text != "redacted"
}

func containsSensitiveSummaryText(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{"password", "passwd", "secret", "token", "credential", "authorization", "raw_argv", "rawargv"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func int64Data(value any) (int64, bool) {
	const maxInt64Float = float64(1<<63 - 1)
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		if v < 0 || v > maxInt64Float || v != float64(int64(v)) {
			return 0, false
		}
		return int64(v), true
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
