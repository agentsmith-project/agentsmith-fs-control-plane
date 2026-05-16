package jvsrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	testPayloadRoot       = "/srv/afscp/volumes/vol_default/afscp/namespaces/ns_alpha/repos/repo_alpha/payload"
	testControlRoot       = "/srv/afscp/volumes/vol_default/afscp/namespaces/ns_alpha/repos/repo_alpha/control"
	testDirectHome        = "/home/afscp/runtime/ns_alpha/repo_alpha"
	testTargetPayloadRoot = "/srv/afscp/volumes/vol_default/afscp/namespaces/ns_alpha/repos/repo_clone/payload"
	testTargetControlRoot = "/srv/afscp/volumes/vol_default/afscp/namespaces/ns_alpha/repos/repo_clone/control"
)

func TestRunnerPublicSurfaceExcludesLegacySaveHistoryAndStrictDoctor(t *testing.T) {
	t.Parallel()

	runnerType := reflect.TypeOf((*Runner)(nil))
	for _, method := range []string{"DoctorStrict", "Save", "History"} {
		if _, ok := runnerType.MethodByName(method); ok {
			t.Fatalf("Runner exposes legacy public JVS method %s; active AFSCP calls must use direct afscp methods", method)
		}
	}
	for _, method := range []string{"DirectSave", "DirectList", "DirectRestore", "DirectClone", "DirectStatus", "DirectDoctor"} {
		if _, ok := runnerType.MethodByName(method); !ok {
			t.Fatalf("Runner is missing direct AFSCP method %s", method)
		}
	}
}

func TestInitUsesFixedCommandAndParsesEnvelope(t *testing.T) {
	t.Parallel()

	commandRunner := &fakeCommandRunner{
		result: CommandResult{
			Stdout: initSuccessStdout(t),
		},
	}
	runner := newTestRunner(t, commandRunner)

	summary, err := runner.Init(context.Background(), testPayloadRoot, testControlRoot)
	if err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	if summary != (InitSummary{RepoID: "jvs_repo_alpha", Workspace: "main"}) {
		t.Fatalf("summary = %#v", summary)
	}

	want := CommandSpec{
		Path: "/opt/afscp/bin/jvs",
		Args: []string{
			"init",
			testPayloadRoot,
			"--control-root",
			testControlRoot,
			"--workspace",
			"main",
			"--json",
		},
		Dir: "/var/lib/afscp/jvs-cwd",
	}
	if !reflect.DeepEqual(commandRunner.calls, []CommandSpec{want}) {
		t.Fatalf("calls mismatch:\n got: %#v\nwant: %#v", commandRunner.calls, []CommandSpec{want})
	}
	assertNoForbiddenJVSFlags(t, commandRunner.calls[0].Args)
	assertSummaryDoesNotLeakPaths(t, summary)
}

func TestAFSCPDirectMethodsUseTargetSelectorAndNoLegacyFlags(t *testing.T) {
	t.Parallel()

	target := DirectTarget{ControlRoot: testControlRoot, Home: testDirectHome}
	tests := []struct {
		name    string
		stdout  []byte
		call    func(*Runner) (any, error)
		wantArg []string
		check   func(t *testing.T, summary any)
	}{
		{
			name:    "save",
			stdout:  directSaveSuccessStdout(t),
			wantArg: []string{"afscp", "--control-root", testControlRoot, "--home", testDirectHome, "save", "--message", "checkpoint before restore", "--json"},
			call: func(r *Runner) (any, error) {
				return r.DirectSave(context.Background(), target, " checkpoint before restore ")
			},
			check: func(t *testing.T, summary any) {
				got := summary.(DirectSaveSummary)
				if got.SavePointID != "sp_001" || got.HistoryHeadID != "sp_001" || got.Message != "checkpoint before restore" || got.CreatedAt != "2026-05-05T12:00:00Z" {
					t.Fatalf("summary = %#v", got)
				}
				assertCloneEvidenceSummary(t, got.CloneEvidence, "save", []string{"save_point_payload"})
			},
		},
		{
			name:    "list",
			stdout:  directListSuccessStdout(t),
			wantArg: []string{"afscp", "--control-root", testControlRoot, "--home", testDirectHome, "list", "--json"},
			call: func(r *Runner) (any, error) {
				return r.DirectList(context.Background(), target)
			},
			check: func(t *testing.T, summary any) {
				got := summary.(DirectListSummary)
				if got.HistoryHeadID != "sp_002" || len(got.SavePoints) != 2 || got.SavePoints[0].SavePointID != "sp_002" || !got.SavePoints[0].HistoryHead || got.SavePoints[1].Message != "first" {
					t.Fatalf("summary = %#v", got)
				}
			},
		},
		{
			name:    "restore",
			stdout:  directRestoreSuccessStdout(t),
			wantArg: []string{"afscp", "--control-root", testControlRoot, "--home", testDirectHome, "restore", "--save-point", "sp_001", "--json"},
			call: func(r *Runner) (any, error) {
				return r.DirectRestore(context.Background(), target, "sp_001")
			},
			check: func(t *testing.T, summary any) {
				got := summary.(DirectRestoreSummary)
				if got.RestoredSavePointID != "sp_001" || got.PreviousHeadID != "sp_002" || got.NewHeadID != "sp_001" {
					t.Fatalf("summary = %#v", got)
				}
				assertCloneEvidenceSummary(t, got.CloneEvidence, "restore", []string{"restore_staging"})
			},
		},
		{
			name:    "clone",
			stdout:  directCloneSuccessStdout(t),
			wantArg: []string{"afscp", "--control-root", testControlRoot, "--home", testDirectHome, "clone", "--target-control-root", testTargetControlRoot, "--target-home", testTargetPayloadRoot, "--json", "--save-point", "sp_001"},
			call: func(r *Runner) (any, error) {
				return r.DirectClone(context.Background(), target, DirectTarget{ControlRoot: testTargetControlRoot, Home: testTargetPayloadRoot}, "sp_001")
			},
			check: func(t *testing.T, summary any) {
				got := summary.(DirectCloneSummary)
				if got.SourceRepoID != "jvs_repo_alpha" || got.TargetRepoID != "jvs_repo_clone" || got.SavePointID != "sp_001" || got.SavePointsMode != "main" || got.SavePointsCopiedCount != 1 || got.RuntimeStateCopied || got.Workspace != "main" {
					t.Fatalf("summary = %#v", got)
				}
				assertCloneEvidenceSummary(t, got.CloneEvidence, "clone", []string{"clone_target_home", "clone_target_snapshot"})
			},
		},
		{
			name:    "status",
			stdout:  directStatusSuccessStdout(t),
			wantArg: []string{"afscp", "--control-root", testControlRoot, "--home", testDirectHome, "status", "--json"},
			call: func(r *Runner) (any, error) {
				return r.DirectStatus(context.Background(), target)
			},
			check: func(t *testing.T, summary any) {
				got := summary.(DirectStatusSummary)
				if got.HistoryHeadID != "sp_002" || got.MetadataState != "clean" || got.ActiveOperation != "none" || got.Recovery != "none" {
					t.Fatalf("summary = %#v", got)
				}
			},
		},
		{
			name:    "doctor",
			stdout:  directDoctorSuccessStdout(t),
			wantArg: []string{"afscp", "--control-root", testControlRoot, "--home", testDirectHome, "doctor", "--json"},
			call: func(r *Runner) (any, error) {
				return r.DirectDoctor(context.Background(), target)
			},
			check: func(t *testing.T, summary any) {
				got := summary.(DirectDoctorSummary)
				if got.RepoID != "jvs_repo_alpha" || !got.Healthy || got.FindingCount != 0 || got.MetadataState != "clean" || got.Journal != "clean" || got.Recovery != "none" {
					t.Fatalf("summary = %#v", got)
				}
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			commandRunner := &fakeCommandRunner{result: CommandResult{Stdout: tt.stdout}}
			runner := newTestRunner(t, commandRunner)
			summary, err := tt.call(runner)
			if err != nil {
				t.Fatalf("direct call returned error: %v", err)
			}
			tt.check(t, summary)

			want := CommandSpec{Path: "/opt/afscp/bin/jvs", Args: tt.wantArg, Dir: "/var/lib/afscp/jvs-cwd"}
			if !reflect.DeepEqual(commandRunner.calls, []CommandSpec{want}) {
				t.Fatalf("calls mismatch:\n got: %#v\nwant: %#v", commandRunner.calls, []CommandSpec{want})
			}
			assertAFSCPDirectArgvHasNoLegacyTokens(t, commandRunner.calls[0].Args)
			assertSummaryDoesNotLeakPaths(t, summary)
		})
	}
}

func TestAFSCPDirectRejectsUnsafeTargetsAndArguments(t *testing.T) {
	t.Parallel()

	runner := newTestRunner(t, &fakeCommandRunner{result: CommandResult{Stdout: directSaveSuccessStdout(t)}})
	goodTarget := DirectTarget{ControlRoot: testControlRoot, Home: testDirectHome}
	tests := []struct {
		name string
		call func() error
	}{
		{name: "missing control", call: func() error {
			_, err := runner.DirectSave(context.Background(), DirectTarget{Home: testDirectHome}, "checkpoint")
			return err
		}},
		{name: "relative control", call: func() error {
			_, err := runner.DirectList(context.Background(), DirectTarget{ControlRoot: "control", Home: testDirectHome})
			return err
		}},
		{name: "unclean control", call: func() error {
			_, err := runner.DirectStatus(context.Background(), DirectTarget{ControlRoot: "/srv/afscp/../afscp/control", Home: testDirectHome})
			return err
		}},
		{name: "root control", call: func() error {
			_, err := runner.DirectDoctor(context.Background(), DirectTarget{ControlRoot: "/", Home: testDirectHome})
			return err
		}},
		{name: "missing home", call: func() error {
			_, err := runner.DirectSave(context.Background(), DirectTarget{ControlRoot: testControlRoot}, "checkpoint")
			return err
		}},
		{name: "relative home", call: func() error {
			_, err := runner.DirectList(context.Background(), DirectTarget{ControlRoot: testControlRoot, Home: "home"})
			return err
		}},
		{name: "unclean home", call: func() error {
			_, err := runner.DirectStatus(context.Background(), DirectTarget{ControlRoot: testControlRoot, Home: "/srv/afscp/../home"})
			return err
		}},
		{name: "root home", call: func() error {
			_, err := runner.DirectDoctor(context.Background(), DirectTarget{ControlRoot: testControlRoot, Home: "/"})
			return err
		}},
		{name: "same control and home", call: func() error {
			_, err := runner.DirectSave(context.Background(), DirectTarget{ControlRoot: testControlRoot, Home: testControlRoot}, "checkpoint")
			return err
		}},
		{name: "home inside control", call: func() error {
			_, err := runner.DirectList(context.Background(), DirectTarget{ControlRoot: testControlRoot, Home: testControlRoot + "/home"})
			return err
		}},
		{name: "control inside home", call: func() error {
			_, err := runner.DirectStatus(context.Background(), DirectTarget{ControlRoot: testDirectHome + "/control", Home: testDirectHome})
			return err
		}},
		{name: "empty save message", call: func() error {
			_, err := runner.DirectSave(context.Background(), goodTarget, " \t")
			return err
		}},
		{name: "unsafe restore save point", call: func() error {
			_, err := runner.DirectRestore(context.Background(), goodTarget, "sp/secret")
			return err
		}},
		{name: "unsafe clone save point", call: func() error {
			_, err := runner.DirectClone(context.Background(), goodTarget, DirectTarget{ControlRoot: testTargetControlRoot, Home: testTargetPayloadRoot}, "sp/secret")
			return err
		}},
		{name: "clone target overlaps source", call: func() error {
			_, err := runner.DirectClone(context.Background(), goodTarget, DirectTarget{ControlRoot: testControlRoot + "/target", Home: testTargetPayloadRoot}, "")
			return err
		}},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := tt.call(); err == nil {
				t.Fatal("direct call succeeded, want error")
			}
		})
	}
}

func TestAFSCPDirectFailsClosedForMalformedJSONAndOldEnvelopeFields(t *testing.T) {
	t.Parallel()

	target := DirectTarget{ControlRoot: testControlRoot, Home: testDirectHome}
	tests := []struct {
		name   string
		stdout []byte
	}{
		{name: "malformed json", stdout: []byte(`{"contract":`)},
		{name: "trailing garbage", stdout: append(directSaveSuccessStdout(t), []byte(" /srv/afscp/secret")...)},
		{name: "multiple json values", stdout: append(directSaveSuccessStdout(t), directSaveSuccessStdout(t)...)},
		{name: "old envelope", stdout: saveSuccessStdout(t)},
		{name: "old envelope ok false", stdout: jvsErrorStdout(t, "save", testControlRoot, "E_SOURCE_DIRTY")},
		{name: "missing contract", stdout: directSaveStdoutWith(t, func(env map[string]any) { delete(env, "contract") })},
		{name: "wrong contract", stdout: directSaveStdoutWith(t, func(env map[string]any) { env["contract"] = "jvs.afscp.preview.v1" })},
		{name: "wrong command", stdout: directSaveStdoutWith(t, func(env map[string]any) { env["command"] = "list" })},
		{name: "unknown status", stdout: directSaveStdoutWith(t, func(env map[string]any) { env["status"] = "partial" })},
		{name: "accepted success not final", stdout: directSaveStdoutWith(t, func(env map[string]any) { env["status"] = "accepted" })},
		{name: "running success not final", stdout: directSaveStdoutWith(t, func(env map[string]any) { env["status"] = "running" })},
		{name: "recovery required success not final", stdout: directSaveStdoutWith(t, func(env map[string]any) { env["status"] = "recovery_required" })},
		{name: "succeeded with ok false", stdout: directSaveStdoutWith(t, func(env map[string]any) { env["ok"] = false })},
		{name: "ok status with error object", stdout: directSaveStdoutWith(t, func(env map[string]any) { env["error"] = map[string]any{"code": "E_SOURCE_DIRTY"} })},
		{name: "missing data", stdout: directSaveStdoutWith(t, func(env map[string]any) { delete(env, "data") })},
		{name: "save missing history head", stdout: directSaveStdoutWith(t, func(env map[string]any) { delete(env["data"].(map[string]any), "history_head") })},
		{name: "save history head mismatch", stdout: directSaveStdoutWith(t, func(env map[string]any) { env["data"].(map[string]any)["history_head"] = "sp_other" })},
		{name: "old operation result status ok envelope", stdout: oldDirectSaveStdout(t)},
		{name: "error status missing code", stdout: directErrorStdoutWith(t, "save", func(env map[string]any) { env["error"] = map[string]any{"message": "/srv/afscp/secret"} })},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			runner := newTestRunner(t, &fakeCommandRunner{result: CommandResult{Stdout: tt.stdout}})
			_, err := runner.DirectSave(context.Background(), target, "checkpoint")
			if err == nil {
				t.Fatal("DirectSave succeeded, want fail closed")
			}
			assertErrorDoesNotLeak(t, err)
		})
	}
}

func TestAFSCPDirectRejectsForbiddenInternalFields(t *testing.T) {
	t.Parallel()

	target := DirectTarget{ControlRoot: testControlRoot, Home: testDirectHome}
	forbiddenFields := []string{
		"checksum",
		"checksum_sha256",
		"digest",
		"content_digest",
		"capacity",
		"capacity_bytes",
		"tree_scan",
		"tree_scan_result",
		"file_count",
		"payload_tree",
		"payload_tree_scan",
		"payload_file_count",
		"sync",
		"sync_state",
		"hash",
		"proof",
		"internal_path",
		"internal_paths",
		"payload_root_hash",
		"content_root_hash",
		"control_root",
		"control-root",
		"control_root_path",
		"home",
		"home_path",
		"raw_command",
		"raw command",
		"command",
		"save_profile",
		"expected_folder_evidence",
	}

	for _, field := range forbiddenFields {
		field := field
		t.Run(field, func(t *testing.T) {
			t.Parallel()

			stdout := directSaveStdoutWith(t, func(env map[string]any) {
				env["data"].(map[string]any)[field] = "/srv/afscp/secret"
			})
			runner := newTestRunner(t, &fakeCommandRunner{result: CommandResult{Stdout: stdout}})
			_, err := runner.DirectSave(context.Background(), target, "checkpoint")
			if err == nil {
				t.Fatal("DirectSave accepted forbidden internal field, want error")
			}
			assertErrorDoesNotLeak(t, err)
		})
	}

	t.Run("nested control root", func(t *testing.T) {
		t.Parallel()

		stdout := directListStdoutWith(t, func(env map[string]any) {
			savePoints := env["data"].(map[string]any)["save_points"].([]map[string]any)
			savePoints[0]["control_root"] = testControlRoot
		})
		runner := newTestRunner(t, &fakeCommandRunner{result: CommandResult{Stdout: stdout}})
		_, err := runner.DirectList(context.Background(), target)
		if err == nil {
			t.Fatal("DirectList accepted nested forbidden internal field, want error")
		}
		assertErrorDoesNotLeak(t, err)
	})

	t.Run("restore plan lifecycle fields", func(t *testing.T) {
		t.Parallel()

		stdout := directRestoreStdoutWith(t, func(env map[string]any) {
			data := env["data"].(map[string]any)
			data["plan_id"] = "plan_legacy"
			data["restore_plan_id"] = "plan_legacy"
			data["run_command"] = "jvs restore --run plan_legacy"
		})
		runner := newTestRunner(t, &fakeCommandRunner{result: CommandResult{Stdout: stdout}})
		_, err := runner.DirectRestore(context.Background(), target, "sp_001")
		if err == nil {
			t.Fatal("DirectRestore accepted legacy restore plan fields, want error")
		}
		assertErrorDoesNotLeak(t, err)
	})
}

func TestAFSCPDirectCommandErrorsMapCodeAndRedactDetails(t *testing.T) {
	t.Parallel()

	target := DirectTarget{ControlRoot: testControlRoot, Home: testDirectHome}
	runner := newTestRunner(t, &fakeCommandRunner{result: CommandResult{
		ExitCode: 37,
		Stdout:   directErrorStdout(t, "save", "E_RECOVERY_BLOCKING"),
		Stderr:   []byte("token=secret /srv/afscp/raw"),
	}})

	_, err := runner.DirectSave(context.Background(), target, "checkpoint")
	assertJVSCommandError(t, err, "afscp save", 37, "E_RECOVERY_BLOCKING")

	exitZeroRunner := newTestRunner(t, &fakeCommandRunner{result: CommandResult{
		Stdout: directErrorStdout(t, "doctor", "E_REPO_BUSY"),
	}})
	_, err = exitZeroRunner.DirectDoctor(context.Background(), target)
	assertJVSCommandError(t, err, "afscp doctor", 0, "E_REPO_BUSY")
}

func TestAFSCPDirectListRedactsPathLikeMessages(t *testing.T) {
	t.Parallel()

	target := DirectTarget{ControlRoot: testControlRoot, Home: testDirectHome}
	stdout := directListStdoutWith(t, func(env map[string]any) {
		savePoints := env["data"].(map[string]any)["save_points"].([]map[string]any)
		savePoints[0]["message"] = "copied from /srv/afscp/secret"
	})
	runner := newTestRunner(t, &fakeCommandRunner{result: CommandResult{Stdout: stdout}})

	summary, err := runner.DirectList(context.Background(), target)
	if err != nil {
		t.Fatalf("DirectList returned error: %v", err)
	}
	if got := summary.SavePoints[0].Message; got != "redacted" {
		t.Fatalf("message = %q, want redacted", got)
	}
	assertSummaryDoesNotLeakPaths(t, summary)
}

func TestVerifyAFSCPDirectCapabilityUsesAFSCPHelpAndRequiresDirectContract(t *testing.T) {
	t.Parallel()

	help := "Usage:\n  jvs afscp --control-root <control> --home <home> save --message <message> --json\n  jvs afscp --control-root <control> --home <home> list --json\n  jvs afscp --control-root <control> --home <home> restore --save-point <id> --json\n  jvs afscp --control-root <control> --home <home> clone --target-control-root <target-control> --target-home <target-home> --json\n  jvs afscp --control-root <control> --home <home> status --json\n  jvs afscp --control-root <control> --home <home> doctor --json\n"
	commandRunner := &fakeCommandRunner{result: CommandResult{Stdout: []byte(help)}}
	runner := newTestRunner(t, commandRunner)

	if err := runner.VerifyAFSCPDirectCapability(context.Background()); err != nil {
		t.Fatalf("VerifyAFSCPDirectCapability returned error: %v", err)
	}
	want := []CommandSpec{
		{Path: "/opt/afscp/bin/jvs", Args: []string{"afscp", "--help"}, Dir: "/var/lib/afscp/jvs-cwd"},
		{Path: "/opt/afscp/bin/jvs", Args: []string{"afscp", "save", "--help"}, Dir: "/var/lib/afscp/jvs-cwd"},
		{Path: "/opt/afscp/bin/jvs", Args: []string{"afscp", "list", "--help"}, Dir: "/var/lib/afscp/jvs-cwd"},
		{Path: "/opt/afscp/bin/jvs", Args: []string{"afscp", "restore", "--help"}, Dir: "/var/lib/afscp/jvs-cwd"},
		{Path: "/opt/afscp/bin/jvs", Args: []string{"afscp", "clone", "--help"}, Dir: "/var/lib/afscp/jvs-cwd"},
		{Path: "/opt/afscp/bin/jvs", Args: []string{"afscp", "status", "--help"}, Dir: "/var/lib/afscp/jvs-cwd"},
		{Path: "/opt/afscp/bin/jvs", Args: []string{"afscp", "doctor", "--help"}, Dir: "/var/lib/afscp/jvs-cwd"},
	}
	if !reflect.DeepEqual(commandRunner.calls, want) {
		t.Fatalf("calls mismatch:\n got: %#v\nwant: %#v", commandRunner.calls, want)
	}

	missingHome := &fakeCommandRunner{result: CommandResult{Stdout: []byte("Usage:\n  jvs afscp --control-root <control> list --json\n")}}
	if err := newTestRunner(t, missingHome).VerifyAFSCPDirectCapability(context.Background()); err == nil {
		t.Fatal("VerifyAFSCPDirectCapability succeeded without paired selector help, want fail-closed error")
	}
}

func TestInitCommandErrorEnvelopeFromNonZeroStdout(t *testing.T) {
	t.Parallel()

	runner := newTestRunner(t, &fakeCommandRunner{result: CommandResult{
		ExitCode: 19,
		Stdout:   jvsErrorStdout(t, "init", testControlRoot, "E_SOURCE_DIRTY"),
		Stderr:   []byte("payload_root=/srv/afscp/secret/payload"),
	}})

	_, err := runner.Init(context.Background(), testPayloadRoot, testControlRoot)
	assertJVSCommandError(t, err, "init", 19, "E_SOURCE_DIRTY")
}

func TestAFSCPDirectPropagatesContextDeadlineWithoutLeakingRunnerError(t *testing.T) {
	t.Parallel()

	runner := newTestRunner(t, &fakeCommandRunner{err: errors.Join(context.DeadlineExceeded, errors.New("token=secret /srv/afscp/raw"))})

	_, err := runner.DirectSave(context.Background(), DirectTarget{ControlRoot: testControlRoot, Home: testDirectHome}, "checkpoint")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("DirectSave error = %v, want context deadline", err)
	}
	if !errors.Is(err, ErrCommandFailed) {
		t.Fatalf("DirectSave error = %v, want command failed wrapper", err)
	}
	assertErrorDoesNotLeak(t, err)
}

func TestOSCommandRunnerPropagatesContextDeadline(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	result, err := (osCommandRunner{maxOutputBytes: 1024}).RunJVSCommand(ctx, CommandSpec{
		Path: "/bin/sh",
		Args: []string{"-c", "sleep 1"},
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RunJVSCommand error = %v result=%#v, want context deadline", err, result)
	}
}

func TestNewRejectsUnsafeConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config Config
	}{
		{name: "empty binary", config: Config{BinaryPath: "", CWD: "/var/lib/afscp/jvs-cwd", CommandRunner: &fakeCommandRunner{}}},
		{name: "relative binary", config: Config{BinaryPath: "jvs", CWD: "/var/lib/afscp/jvs-cwd", CommandRunner: &fakeCommandRunner{}}},
		{name: "root binary", config: Config{BinaryPath: "/", CWD: "/var/lib/afscp/jvs-cwd", CommandRunner: &fakeCommandRunner{}}},
		{name: "unclean binary", config: Config{BinaryPath: "/opt/../opt/jvs", CWD: "/var/lib/afscp/jvs-cwd", CommandRunner: &fakeCommandRunner{}}},
		{name: "empty cwd", config: Config{BinaryPath: "/opt/afscp/bin/jvs", CWD: "", CommandRunner: &fakeCommandRunner{}}},
		{name: "relative cwd", config: Config{BinaryPath: "/opt/afscp/bin/jvs", CWD: "work", CommandRunner: &fakeCommandRunner{}}},
		{name: "root cwd", config: Config{BinaryPath: "/opt/afscp/bin/jvs", CWD: "/", CommandRunner: &fakeCommandRunner{}}},
		{name: "unclean cwd", config: Config{BinaryPath: "/opt/afscp/bin/jvs", CWD: "/var/lib/../lib/afscp", CommandRunner: &fakeCommandRunner{}}},
		{name: "bad output limit", config: Config{BinaryPath: "/opt/afscp/bin/jvs", CWD: "/var/lib/afscp/jvs-cwd", CommandRunner: &fakeCommandRunner{}, MaxOutputBytes: -1}},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := New(tt.config); err == nil {
				t.Fatal("New succeeded, want error")
			}
		})
	}
}

func TestInitFailsClosedForInvalidEnvelope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		stdout func(t *testing.T) []byte
		result CommandResult
		err    error
	}{
		{name: "nonzero", result: CommandResult{ExitCode: 1, Stdout: []byte(`{"ok":false}`), Stderr: []byte("password=secret /srv/afscp/raw")}},
		{name: "runner error", err: errors.New("exec failed token=secret /srv/afscp/raw")},
		{name: "malformed json", result: CommandResult{Stdout: []byte(`{"ok":`)}},
		{name: "trailing garbage", stdout: func(t *testing.T) []byte { return append(initSuccessStdout(t), []byte(" /srv/afscp/secret")...) }},
		{name: "multiple json", stdout: func(t *testing.T) []byte { return append(initSuccessStdout(t), initSuccessStdout(t)...) }},
		{name: "ok false", stdout: func(t *testing.T) []byte {
			return initStdoutWith(t, func(env map[string]any) { env["ok"] = false; env["error"] = map[string]any{"message": "secret raw"} })
		}},
		{name: "missing schema version", stdout: func(t *testing.T) []byte {
			return initStdoutWith(t, func(env map[string]any) { delete(env, "schema_version") })
		}},
		{name: "wrong schema version", stdout: func(t *testing.T) []byte {
			return initStdoutWith(t, func(env map[string]any) { env["schema_version"] = 2 })
		}},
		{name: "string schema version", stdout: func(t *testing.T) []byte {
			return initStdoutWith(t, func(env map[string]any) { env["schema_version"] = "jvs/current" })
		}},
		{name: "missing repo id", stdout: func(t *testing.T) []byte {
			return initStdoutWith(t, func(env map[string]any) { delete(env["data"].(map[string]any), "repo_id") })
		}},
		{name: "wrong workspace", stdout: func(t *testing.T) []byte {
			return initStdoutWith(t, func(env map[string]any) { env["data"].(map[string]any)["workspace"] = "dev" })
		}},
		{name: "missing top-level workspace", stdout: func(t *testing.T) []byte {
			return initStdoutWith(t, func(env map[string]any) { delete(env, "workspace") })
		}},
		{name: "wrong top-level workspace", stdout: func(t *testing.T) []byte {
			return initStdoutWith(t, func(env map[string]any) { env["workspace"] = "dev" })
		}},
		{name: "missing command", stdout: func(t *testing.T) []byte {
			return initStdoutWith(t, func(env map[string]any) { delete(env, "command") })
		}},
		{name: "command mismatch", stdout: func(t *testing.T) []byte {
			return initStdoutWith(t, func(env map[string]any) { env["command"] = "doctor" })
		}},
		{name: "missing repo root", stdout: func(t *testing.T) []byte {
			return initStdoutWith(t, func(env map[string]any) { delete(env, "repo_root") })
		}},
		{name: "wrong repo root", stdout: func(t *testing.T) []byte {
			return initStdoutWith(t, func(env map[string]any) { env["repo_root"] = "/srv/afscp/volumes/vol_default/other/control" })
		}},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.stdout != nil {
				tt.result.Stdout = tt.stdout(t)
			}
			runner := newTestRunner(t, &fakeCommandRunner{result: tt.result, err: tt.err})
			_, err := runner.Init(context.Background(), testPayloadRoot, testControlRoot)
			if err == nil {
				t.Fatal("Init succeeded, want error")
			}
			assertErrorDoesNotLeak(t, err)
		})
	}
}

func TestInitRejectsUnsafeRepoID(t *testing.T) {
	t.Parallel()

	longID := strings.Repeat("a", 129)
	tests := []string{
		"",
		"_jvs_repo",
		"-jvs-repo",
		".jvs.repo",
		":jvs:repo",
		"jvs/repo",
		`jvs\repo`,
		"jvs repo",
		"jvs\trepo",
		"jvs\nrepo",
		"jvs=repo",
		"jvs;repo",
		"jvs[repo",
		"jvs_repo_/srv/afscp/secret",
		longID,
	}

	for _, repoID := range tests {
		repoID := repoID
		t.Run(strconv.Quote(repoID), func(t *testing.T) {
			t.Parallel()

			runner := newTestRunner(t, &fakeCommandRunner{
				result: CommandResult{Stdout: initStdoutWith(t, func(env map[string]any) {
					env["data"].(map[string]any)["repo_id"] = repoID
				})},
			})
			_, err := runner.Init(context.Background(), testPayloadRoot, testControlRoot)
			if err == nil {
				t.Fatal("Init succeeded, want error")
			}
			assertErrorDoesNotLeak(t, err)
		})
	}
}

func TestInitAcceptsSchemaAlignedRepoIDs(t *testing.T) {
	t.Parallel()

	tests := []string{
		"jvs_repo_alpha",
		"jvs-template-alpha",
		"550e8400-e29b-41d4-a716-446655440000",
	}
	for _, repoID := range tests {
		repoID := repoID
		t.Run(repoID, func(t *testing.T) {
			t.Parallel()

			runner := newTestRunner(t, &fakeCommandRunner{
				result: CommandResult{Stdout: initStdoutWith(t, func(env map[string]any) {
					env["data"].(map[string]any)["repo_id"] = repoID
				})},
			})
			summary, err := runner.Init(context.Background(), testPayloadRoot, testControlRoot)
			if err != nil {
				t.Fatalf("Init returned error: %v", err)
			}
			if summary.RepoID != repoID {
				t.Fatalf("repo id = %q, want %q", summary.RepoID, repoID)
			}
		})
	}
}

func TestRunnerRejectsRawPathArguments(t *testing.T) {
	t.Parallel()

	runner := newTestRunner(t, &fakeCommandRunner{})
	tests := []struct {
		name string
		call func() error
	}{
		{name: "init relative payload", call: func() error {
			_, err := runner.Init(context.Background(), "payload", "/srv/afscp/control")
			return err
		}},
		{name: "init unclean control", call: func() error {
			_, err := runner.Init(context.Background(), "/srv/afscp/payload", "/srv/afscp/../afscp/control")
			return err
		}},
		{name: "init same payload and control", call: func() error {
			_, err := runner.Init(context.Background(), "/srv/afscp/repo", "/srv/afscp/repo")
			return err
		}},
		{name: "init payload inside control", call: func() error {
			_, err := runner.Init(context.Background(), "/srv/afscp/repo/control/payload", "/srv/afscp/repo/control")
			return err
		}},
		{name: "init control inside payload", call: func() error {
			_, err := runner.Init(context.Background(), "/srv/afscp/repo/payload", "/srv/afscp/repo/payload/control")
			return err
		}},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := tt.call(); err == nil {
				t.Fatal("call succeeded, want error")
			}
		})
	}
}

func TestNewJVSPrimitivesFailClosedForInvalidEnvelope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		stdout []byte
		call   func(*Runner) error
	}{
		{name: "direct clone negative copied count", stdout: directCloneStdoutWith(t, func(env map[string]any) { env["data"].(map[string]any)["save_points_copied_count"] = -1 }), call: func(r *Runner) error {
			_, err := r.DirectClone(context.Background(), DirectTarget{ControlRoot: testControlRoot, Home: testDirectHome}, DirectTarget{ControlRoot: testTargetControlRoot, Home: testTargetPayloadRoot}, "sp_001")
			return err
		}},
		{name: "direct save missing clone evidence", stdout: directSaveStdoutWith(t, func(env map[string]any) { delete(env["data"].(map[string]any), "clone_evidence") }), call: func(r *Runner) error {
			_, err := r.DirectSave(context.Background(), DirectTarget{ControlRoot: testControlRoot, Home: testDirectHome}, "checkpoint")
			return err
		}},
		{name: "direct restore missing clone evidence", stdout: directRestoreStdoutWith(t, func(env map[string]any) { delete(env["data"].(map[string]any), "clone_evidence") }), call: func(r *Runner) error {
			_, err := r.DirectRestore(context.Background(), DirectTarget{ControlRoot: testControlRoot, Home: testDirectHome}, "sp_001")
			return err
		}},
		{name: "direct clone missing clone evidence", stdout: directCloneStdoutWith(t, func(env map[string]any) { delete(env["data"].(map[string]any), "clone_evidence") }), call: func(r *Runner) error {
			_, err := r.DirectClone(context.Background(), DirectTarget{ControlRoot: testControlRoot, Home: testDirectHome}, DirectTarget{ControlRoot: testTargetControlRoot, Home: testTargetPayloadRoot}, "sp_001")
			return err
		}},
		{name: "direct save clone evidence rejects path field", stdout: directSaveStdoutWith(t, func(env map[string]any) {
			evidence := env["data"].(map[string]any)["clone_evidence"].([]map[string]any)
			evidence[0]["source_path"] = testControlRoot
		}), call: func(r *Runner) error {
			_, err := r.DirectSave(context.Background(), DirectTarget{ControlRoot: testControlRoot, Home: testDirectHome}, "checkpoint")
			return err
		}},
		{name: "direct restore clone evidence rejects negative duration", stdout: directRestoreStdoutWith(t, func(env map[string]any) {
			evidence := env["data"].(map[string]any)["clone_evidence"].([]map[string]any)
			evidence[0]["duration_ms"] = -1
		}), call: func(r *Runner) error {
			_, err := r.DirectRestore(context.Background(), DirectTarget{ControlRoot: testControlRoot, Home: testDirectHome}, "sp_001")
			return err
		}},
		{name: "direct clone evidence rejects missing started at", stdout: directCloneStdoutWith(t, func(env map[string]any) {
			evidence := env["data"].(map[string]any)["clone_evidence"].([]map[string]any)
			delete(evidence[0], "started_at")
		}), call: func(r *Runner) error {
			_, err := r.DirectClone(context.Background(), DirectTarget{ControlRoot: testControlRoot, Home: testDirectHome}, DirectTarget{ControlRoot: testTargetControlRoot, Home: testTargetPayloadRoot}, "sp_001")
			return err
		}},
		{name: "direct clone evidence rejects missing finished at", stdout: directCloneStdoutWith(t, func(env map[string]any) {
			evidence := env["data"].(map[string]any)["clone_evidence"].([]map[string]any)
			delete(evidence[0], "finished_at")
		}), call: func(r *Runner) error {
			_, err := r.DirectClone(context.Background(), DirectTarget{ControlRoot: testControlRoot, Home: testDirectHome}, DirectTarget{ControlRoot: testTargetControlRoot, Home: testTargetPayloadRoot}, "sp_001")
			return err
		}},
		{name: "direct clone evidence rejects missing status", stdout: directCloneStdoutWith(t, func(env map[string]any) {
			evidence := env["data"].(map[string]any)["clone_evidence"].([]map[string]any)
			delete(evidence[0], "status")
		}), call: func(r *Runner) error {
			_, err := r.DirectClone(context.Background(), DirectTarget{ControlRoot: testControlRoot, Home: testDirectHome}, DirectTarget{ControlRoot: testTargetControlRoot, Home: testTargetPayloadRoot}, "sp_001")
			return err
		}},
		{name: "direct clone evidence rejects missing engine", stdout: directCloneStdoutWith(t, func(env map[string]any) {
			evidence := env["data"].(map[string]any)["clone_evidence"].([]map[string]any)
			delete(evidence[0], "engine")
		}), call: func(r *Runner) error {
			_, err := r.DirectClone(context.Background(), DirectTarget{ControlRoot: testControlRoot, Home: testDirectHome}, DirectTarget{ControlRoot: testTargetControlRoot, Home: testTargetPayloadRoot}, "sp_001")
			return err
		}},
		{name: "direct clone evidence rejects missing phase", stdout: directCloneStdoutWith(t, func(env map[string]any) {
			evidence := env["data"].(map[string]any)["clone_evidence"].([]map[string]any)
			delete(evidence[0], "phase")
		}), call: func(r *Runner) error {
			_, err := r.DirectClone(context.Background(), DirectTarget{ControlRoot: testControlRoot, Home: testDirectHome}, DirectTarget{ControlRoot: testTargetControlRoot, Home: testTargetPayloadRoot}, "sp_001")
			return err
		}},
		{name: "direct clone evidence rejects raw argv field", stdout: directCloneStdoutWith(t, func(env map[string]any) {
			evidence := env["data"].(map[string]any)["clone_evidence"].([]map[string]any)
			evidence[0]["raw_argv"] = []string{"jvs", "afscp", "clone"}
		}), call: func(r *Runner) error {
			_, err := r.DirectClone(context.Background(), DirectTarget{ControlRoot: testControlRoot, Home: testDirectHome}, DirectTarget{ControlRoot: testTargetControlRoot, Home: testTargetPayloadRoot}, "sp_001")
			return err
		}},
		{name: "direct clone evidence rejects secret field", stdout: directCloneStdoutWith(t, func(env map[string]any) {
			evidence := env["data"].(map[string]any)["clone_evidence"].([]map[string]any)
			evidence[0]["secret_ref"] = "credential_alpha"
		}), call: func(r *Runner) error {
			_, err := r.DirectClone(context.Background(), DirectTarget{ControlRoot: testControlRoot, Home: testDirectHome}, DirectTarget{ControlRoot: testTargetControlRoot, Home: testTargetPayloadRoot}, "sp_001")
			return err
		}},
		{name: "direct clone evidence rejects internal path field", stdout: directCloneStdoutWith(t, func(env map[string]any) {
			evidence := env["data"].(map[string]any)["clone_evidence"].([]map[string]any)
			evidence[0]["internal_path"] = testControlRoot
		}), call: func(r *Runner) error {
			_, err := r.DirectClone(context.Background(), DirectTarget{ControlRoot: testControlRoot, Home: testDirectHome}, DirectTarget{ControlRoot: testTargetControlRoot, Home: testTargetPayloadRoot}, "sp_001")
			return err
		}},
		{name: "direct clone save point mismatch", stdout: directCloneStdoutWith(t, func(env map[string]any) { env["data"].(map[string]any)["save_point_id"] = "sp_other" }), call: func(r *Runner) error {
			_, err := r.DirectClone(context.Background(), DirectTarget{ControlRoot: testControlRoot, Home: testDirectHome}, DirectTarget{ControlRoot: testTargetControlRoot, Home: testTargetPayloadRoot}, "sp_001")
			return err
		}},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			runner := newTestRunner(t, &fakeCommandRunner{result: CommandResult{Stdout: tt.stdout}})
			err := tt.call(runner)
			if err == nil {
				t.Fatal("call succeeded, want fail closed")
			}
			assertErrorDoesNotLeak(t, err)
		})
	}
}

func TestNewJVSPrimitivesFailClosedForCommandAndJSONFailures(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		call func(*Runner) error
	}{
		{name: "direct clone", call: func(r *Runner) error {
			_, err := r.DirectClone(context.Background(), DirectTarget{ControlRoot: testControlRoot, Home: testDirectHome}, DirectTarget{ControlRoot: testTargetControlRoot, Home: testTargetPayloadRoot}, "sp_001")
			return err
		}},
	}
	failures := []struct {
		name   string
		result CommandResult
		err    error
	}{
		{name: "nonzero", result: CommandResult{ExitCode: 1, Stderr: []byte("password=secret /srv/afscp/raw")}},
		{name: "malformed", result: CommandResult{Stdout: []byte(`{"ok":`)}},
		{name: "ok false", result: CommandResult{Stdout: directErrorStdout(t, "clone", "E_SOURCE_DIRTY")}},
		{name: "runner error", err: errors.New("exec failed token=secret /srv/afscp/raw")},
	}
	for _, c := range cases {
		c := c
		for _, failure := range failures {
			failure := failure
			t.Run(c.name+" "+failure.name, func(t *testing.T) {
				t.Parallel()
				runner := newTestRunner(t, &fakeCommandRunner{result: failure.result, err: failure.err})
				err := c.call(runner)
				if err == nil {
					t.Fatal("call succeeded, want fail closed")
				}
				assertErrorDoesNotLeak(t, err)
			})
		}
	}
}

func TestBoundedBufferCapsCapturedOutput(t *testing.T) {
	t.Parallel()

	buffer := newBoundedBuffer(4)
	if _, err := buffer.Write([]byte("secret-output")); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if got := string(buffer.Bytes()); got != "secr" {
		t.Fatalf("buffer = %q, want capped prefix", got)
	}
}

func newTestRunner(t *testing.T, commandRunner CommandRunner) *Runner {
	t.Helper()

	runner, err := New(Config{
		BinaryPath:    "/opt/afscp/bin/jvs",
		CWD:           "/var/lib/afscp/jvs-cwd",
		CommandRunner: commandRunner,
	})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	return runner
}

func assertNoForbiddenJVSFlags(t *testing.T, args []string) {
	t.Helper()

	forbidden := map[string]bool{
		"--repo":           true,
		"--repair-runtime": true,
	}
	for _, arg := range args {
		if forbidden[arg] {
			t.Fatalf("args contain forbidden flag %q: %#v", arg, args)
		}
	}
}

func assertAFSCPDirectArgvHasNoLegacyTokens(t *testing.T, args []string) {
	t.Helper()

	forbiddenArgs := map[string]bool{
		"--workspace":       true,
		"--direct":          true,
		"--discard-unsaved": true,
		"--strict":          true,
		"--repair-runtime":  true,
		"history":           true,
		"recovery":          true,
		"discard":           true,
	}
	for i, arg := range args {
		if forbiddenArgs[arg] {
			t.Fatalf("direct argv contains forbidden legacy token %q: %#v", arg, args)
		}
		if arg == "restore" && i+1 < len(args) && args[i+1] == "--run" {
			t.Fatalf("direct argv contains forbidden legacy restore --run shape: %#v", args)
		}
	}
	if len(args) < 6 || args[0] != "afscp" || args[1] != "--control-root" || args[3] != "--home" {
		t.Fatalf("direct argv missing paired afscp control/home selector: %#v", args)
	}
}

func assertSummaryDoesNotLeakPaths(t *testing.T, summary any) {
	t.Helper()

	rendered := strings.ToLower(fmt.Sprint(summary))
	for _, leaked := range []string{"/srv/afscp", "control", "payload", "password", "credential"} {
		if strings.Contains(rendered, leaked) {
			t.Fatalf("summary leaked %q: %#v", leaked, summary)
		}
	}
}

func assertErrorDoesNotLeak(t *testing.T, err error) {
	t.Helper()

	rendered := strings.ToLower(err.Error())
	for _, leaked := range []string{"/srv/afscp", "secret", "password", "credential_ref", "token="} {
		if strings.Contains(rendered, leaked) {
			t.Fatalf("error leaked %q: %v", leaked, err)
		}
	}
}

func assertJVSCommandError(t *testing.T, err error, command string, exitCode int, code string) {
	t.Helper()

	if err == nil {
		t.Fatal("call succeeded, want command error")
	}
	if !errors.Is(err, ErrCommandFailed) {
		t.Fatalf("errors.Is(err, ErrCommandFailed) = false for %v", err)
	}
	var commandErr *CommandError
	if !errors.As(err, &commandErr) {
		t.Fatalf("errors.As(err, *CommandError) = false for %T %v", err, err)
	}
	if commandErr.Command != command || commandErr.ExitCode != exitCode || commandErr.Code != code {
		t.Fatalf("command error = %#v, want command=%q exit=%d code=%q", commandErr, command, exitCode, code)
	}
	assertErrorDoesNotLeak(t, err)
}

func initSuccessStdout(t *testing.T) []byte {
	t.Helper()
	return initStdoutWith(t, nil)
}

func initStdoutWith(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	env := baseEnvelope("init")
	data := env["data"].(map[string]any)
	data["repo_id"] = "jvs_repo_alpha"
	data["payload_root"] = testPayloadRoot
	if mutate != nil {
		mutate(env)
	}
	return mustJSON(t, env)
}

func doctorSuccessStdout(t *testing.T) []byte {
	t.Helper()
	return doctorStdoutWith(t, nil)
}

func doctorStdoutWith(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	env := baseEnvelope("doctor")
	data := env["data"].(map[string]any)
	data["repo_id"] = "jvs_repo_alpha"
	data["healthy"] = true
	data["checks"] = []map[string]any{{"name": "control_root", "path": testControlRoot}}
	if mutate != nil {
		mutate(env)
	}
	return mustJSON(t, env)
}

func saveSuccessStdout(t *testing.T) []byte {
	t.Helper()
	return saveStdoutWith(t, nil)
}

func directSaveSuccessStdout(t *testing.T) []byte {
	t.Helper()
	return directSaveStdoutWith(t, nil)
}

func directSaveStdoutWith(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	env := directEnvelope("save")
	data := env["data"].(map[string]any)
	data["save_point_id"] = "sp_001"
	data["created_at"] = "2026-05-05T12:00:00Z"
	data["message"] = "checkpoint before restore"
	data["history_head"] = "sp_001"
	data["clone_evidence"] = []map[string]any{directCloneEvidence("save", "save_point_payload", 42)}
	if mutate != nil {
		mutate(env)
	}
	return mustJSON(t, env)
}

func directListSuccessStdout(t *testing.T) []byte {
	t.Helper()
	return directListStdoutWith(t, nil)
}

func directListStdoutWith(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	env := directEnvelope("list")
	data := env["data"].(map[string]any)
	data["history_head"] = "sp_002"
	data["save_points"] = []map[string]any{
		{"save_point_id": "sp_002", "message": "second", "created_at": "2026-05-05T12:01:00Z", "history_head": true},
		{"save_point_id": "sp_001", "message": "first", "created_at": "2026-05-05T12:00:00Z"},
	}
	if mutate != nil {
		mutate(env)
	}
	return mustJSON(t, env)
}

func directRestoreSuccessStdout(t *testing.T) []byte {
	t.Helper()
	return directRestoreStdoutWith(t, nil)
}

func directRestoreStdoutWith(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	env := directEnvelope("restore")
	data := env["data"].(map[string]any)
	data["restored_save_point_id"] = "sp_001"
	data["previous_head"] = "sp_002"
	data["new_head"] = "sp_001"
	data["clone_evidence"] = []map[string]any{directCloneEvidence("restore", "restore_staging", 17)}
	if mutate != nil {
		mutate(env)
	}
	return mustJSON(t, env)
}

func directCloneSuccessStdout(t *testing.T) []byte {
	t.Helper()
	return directCloneStdoutWith(t, nil)
}

func directCloneStdoutWith(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	env := directEnvelope("clone")
	data := env["data"].(map[string]any)
	data["source_repo_id"] = "jvs_repo_alpha"
	data["target_repo_id"] = "jvs_repo_clone"
	data["save_point_id"] = "sp_001"
	data["save_points_copied_count"] = 1
	data["clone_evidence"] = []map[string]any{
		directCloneEvidence("clone", "clone_target_home", 23),
		directCloneEvidence("clone", "clone_target_snapshot", 11),
	}
	if mutate != nil {
		mutate(env)
	}
	return mustJSON(t, env)
}

func directCloneEvidence(operation, phase string, durationMs int64) map[string]any {
	return map[string]any{
		"operation":   operation,
		"phase":       phase,
		"engine":      "juicefs_clone",
		"status":      "succeeded",
		"started_at":  "2026-05-05T12:00:00Z",
		"finished_at": "2026-05-05T12:00:01Z",
		"duration_ms": durationMs,
	}
}

func assertCloneEvidenceSummary(t *testing.T, got []CloneEvidence, operation string, phases []string) {
	t.Helper()
	if len(got) != len(phases) {
		t.Fatalf("clone evidence = %#v, want %d items", got, len(phases))
	}
	for idx, phase := range phases {
		item := got[idx]
		if item.Operation != operation || item.Phase != phase || item.Engine != "juicefs_clone" || item.Status != "succeeded" {
			t.Fatalf("clone evidence[%d] = %#v, want %s/%s juicefs_clone succeeded", idx, item, operation, phase)
		}
		if item.StartedAt == "" || item.FinishedAt == "" || item.DurationMs < 0 {
			t.Fatalf("clone evidence[%d] timing = %#v, want operator-safe timing", idx, item)
		}
	}
}

func directStatusSuccessStdout(t *testing.T) []byte {
	t.Helper()
	return directStatusStdoutWith(t, nil)
}

func directStatusStdoutWith(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	env := directEnvelope("status")
	data := env["data"].(map[string]any)
	data["history_head"] = "sp_002"
	data["metadata_state"] = "clean"
	data["active_operation"] = "none"
	data["recovery"] = "none"
	if mutate != nil {
		mutate(env)
	}
	return mustJSON(t, env)
}

func directDoctorSuccessStdout(t *testing.T) []byte {
	t.Helper()
	return directDoctorStdoutWith(t, nil)
}

func directDoctorStdoutWith(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	env := directEnvelope("doctor")
	data := env["data"].(map[string]any)
	data["repo_id"] = "jvs_repo_alpha"
	data["healthy"] = true
	data["findings"] = []map[string]any{}
	data["metadata_state"] = "clean"
	data["journal"] = "clean"
	data["recovery"] = "none"
	if mutate != nil {
		mutate(env)
	}
	return mustJSON(t, env)
}

func directErrorStdout(t *testing.T, operation, code string) []byte {
	t.Helper()
	return directErrorStdoutWith(t, operation, func(env map[string]any) {
		env["retryable"] = true
		env["error"] = map[string]any{
			"code":      code,
			"message":   "raw message mentions /srv/afscp/secret and token=secret",
			"retryable": true,
		}
	})
}

func directErrorStdoutWith(t *testing.T, operation string, mutate func(map[string]any)) []byte {
	t.Helper()
	env := map[string]any{
		"contract": "jvs.afscp.direct.v1",
		"command":  operation,
		"ok":       false,
		"status":   "failed",
		"data":     nil,
	}
	if mutate != nil {
		mutate(env)
	}
	return mustJSON(t, env)
}

func saveStdoutWith(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	env := baseEnvelope("save")
	data := env["data"].(map[string]any)
	data["save_point_id"] = "sp_001"
	data["workspace"] = "main"
	data["message"] = "checkpoint before restore"
	data["created_at"] = "2026-05-05T12:00:00Z"
	data["newest_save_point"] = "sp_001"
	data["unsaved_changes"] = false
	if mutate != nil {
		mutate(env)
	}
	return mustJSON(t, env)
}

func historySuccessStdout(t *testing.T) []byte {
	t.Helper()
	return historyStdoutWith(t, nil)
}

func historyStdoutWith(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	env := baseEnvelope("history")
	data := env["data"].(map[string]any)
	data["workspace"] = "main"
	data["newest_save_point"] = "sp_002"
	data["truncated"] = false
	data["limit"] = 100
	data["current_pointer"] = "sp_002"
	data["save_points"] = []map[string]any{
		{"save_point_id": "sp_002", "message": "second", "created_at": "2026-05-05T12:01:00Z"},
		{"save_point_id": "sp_001", "message": "first", "created_at": "2026-05-05T12:00:00Z"},
	}
	if mutate != nil {
		mutate(env)
	}
	return mustJSON(t, env)
}

func baseEnvelope(command string) map[string]any {
	return map[string]any{
		"schema_version": 1,
		"command":        command,
		"repo_root":      testControlRoot,
		"workspace":      "main",
		"ok":             true,
		"data": map[string]any{
			"workspace": "main",
		},
		"error": nil,
	}
}

func directEnvelope(operation string) map[string]any {
	return map[string]any{
		"contract": "jvs.afscp.direct.v1",
		"command":  operation,
		"ok":       true,
		"status":   "succeeded",
		"data":     map[string]any{},
		"error":    nil,
	}
}

func oldDirectSaveStdout(t *testing.T) []byte {
	t.Helper()
	env := map[string]any{
		"contract":  "jvs.afscp.direct.v1",
		"operation": "save",
		"status":    "ok",
		"result": map[string]any{
			"save_point_id":     "sp_001",
			"created_at":        "2026-05-05T12:00:00Z",
			"newest_save_point": "sp_001",
			"unsaved_changes":   false,
		},
	}
	return mustJSON(t, env)
}

func jvsErrorStdout(t *testing.T, command, repoRoot, code string) []byte {
	t.Helper()
	env := baseEnvelope(command)
	env["repo_root"] = repoRoot
	env["ok"] = false
	env["error"] = map[string]any{
		"code":    code,
		"message": "raw message mentions /srv/afscp/secret and token=secret",
	}
	return mustJSON(t, env)
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	out, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	return out
}

type fakeCommandRunner struct {
	calls  []CommandSpec
	result CommandResult
	err    error
}

func (runner *fakeCommandRunner) RunJVSCommand(_ context.Context, spec CommandSpec) (CommandResult, error) {
	runner.calls = append(runner.calls, spec)
	return runner.result, runner.err
}
