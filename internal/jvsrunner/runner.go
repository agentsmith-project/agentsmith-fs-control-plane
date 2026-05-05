package jvsrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	workspaceMain         = "main"
	schemaVersionV048     = 1
	defaultMaxOutputBytes = 64 * 1024
	maxSafeRepoIDLength   = 128
)

var (
	ErrInvalidConfig   = errors.New("invalid jvs runner config")
	ErrInvalidArgument = errors.New("invalid jvs runner argument")
	ErrCommandFailed   = errors.New("jvs command failed")
	ErrInvalidEnvelope = errors.New("invalid jvs json envelope")
)

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
		return InitSummary{}, fmt.Errorf("%w: init", ErrCommandFailed)
	}
	result = runner.capResult(result)
	if result.ExitCode != 0 {
		return InitSummary{}, fmt.Errorf("%w: init", ErrCommandFailed)
	}

	envelope, err := decodeEnvelope(result.Stdout)
	if err != nil {
		return InitSummary{}, fmt.Errorf("%w: init", ErrInvalidEnvelope)
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
		return DoctorSummary{}, fmt.Errorf("%w: doctor", ErrCommandFailed)
	}
	result = runner.capResult(result)
	if result.ExitCode != 0 {
		return DoctorSummary{}, fmt.Errorf("%w: doctor", ErrCommandFailed)
	}

	envelope, err := decodeEnvelope(result.Stdout)
	if err != nil {
		return DoctorSummary{}, fmt.Errorf("%w: doctor", ErrInvalidEnvelope)
	}
	healthy, ok := envelope.Data["healthy"].(bool)
	repoID, _ := envelope.Data["repo_id"].(string)
	workspace, _ := envelope.Data["workspace"].(string)
	if !envelope.validFor("doctor", controlRoot) || !ok || !healthy || !safeOpaqueID(repoID) || workspace != workspaceMain {
		return DoctorSummary{}, fmt.Errorf("%w: doctor", ErrInvalidEnvelope)
	}

	return DoctorSummary{RepoID: repoID, Healthy: true, Workspace: workspace}, nil
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

func (parsed envelope) validFor(command, controlRoot string) bool {
	return parsed.SchemaVersion == schemaVersionV048 &&
		parsed.Command == command &&
		parsed.Workspace == workspaceMain &&
		parsed.RepoRoot == controlRoot &&
		parsed.OK
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
		switch b := id[i]; {
		case b < 0x21 || b > 0x7e:
			return false
		case b == '/' || b == '\\' || b == '=':
			return false
		}
	}
	return true
}

func capBytes(value []byte, limit int) []byte {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit]
}

type osCommandRunner struct {
	maxOutputBytes int
}

func (runner osCommandRunner) RunJVSCommand(ctx context.Context, spec CommandSpec) (CommandResult, error) {
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
