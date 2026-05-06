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
)

const (
	testPayloadRoot       = "/srv/afscp/volumes/vol_default/afscp/namespaces/ns_alpha/repos/repo_alpha/payload"
	testControlRoot       = "/srv/afscp/volumes/vol_default/afscp/namespaces/ns_alpha/repos/repo_alpha/control"
	testTargetPayloadRoot = "/srv/afscp/volumes/vol_default/afscp/namespaces/ns_alpha/repos/repo_clone/payload"
	testTargetControlRoot = "/srv/afscp/volumes/vol_default/afscp/namespaces/ns_alpha/repos/repo_clone/control"
)

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

func TestDoctorStrictUsesFixedCommandAndParsesEnvelope(t *testing.T) {
	t.Parallel()

	commandRunner := &fakeCommandRunner{
		result: CommandResult{
			Stdout: doctorSuccessStdout(t),
		},
	}
	runner := newTestRunner(t, commandRunner)

	summary, err := runner.DoctorStrict(context.Background(), testControlRoot)
	if err != nil {
		t.Fatalf("DoctorStrict returned error: %v", err)
	}
	if summary != (DoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, Workspace: "main"}) {
		t.Fatalf("summary = %#v", summary)
	}

	want := CommandSpec{
		Path: "/opt/afscp/bin/jvs",
		Args: []string{
			"--control-root",
			testControlRoot,
			"--workspace",
			"main",
			"doctor",
			"--strict",
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

func TestSaveUsesFixedCommandAndParsesEnvelope(t *testing.T) {
	t.Parallel()

	commandRunner := &fakeCommandRunner{result: CommandResult{Stdout: saveSuccessStdout(t)}}
	runner := newTestRunner(t, commandRunner)

	summary, err := runner.Save(context.Background(), testControlRoot, " checkpoint before restore ")
	if err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	if summary.SavePointID != "sp_001" || summary.NewestSavePointID != "sp_001" || summary.Workspace != "main" || summary.UnsavedChanges {
		t.Fatalf("summary = %#v", summary)
	}
	want := CommandSpec{Path: "/opt/afscp/bin/jvs", Args: []string{"--control-root", testControlRoot, "--workspace", "main", "save", "--message", "checkpoint before restore", "--json"}, Dir: "/var/lib/afscp/jvs-cwd"}
	if !reflect.DeepEqual(commandRunner.calls, []CommandSpec{want}) {
		t.Fatalf("calls mismatch:\n got: %#v\nwant: %#v", commandRunner.calls, []CommandSpec{want})
	}
	assertNoForbiddenJVSFlags(t, commandRunner.calls[0].Args)
	assertSummaryDoesNotLeakPaths(t, summary)
}

func TestSaveReportsUnsavedChangesWithoutFailing(t *testing.T) {
	t.Parallel()

	commandRunner := &fakeCommandRunner{result: CommandResult{Stdout: saveStdoutWith(t, func(env map[string]any) {
		env["data"].(map[string]any)["unsaved_changes"] = true
	})}}
	runner := newTestRunner(t, commandRunner)

	summary, err := runner.Save(context.Background(), testControlRoot, "checkpoint")
	if err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	if !summary.UnsavedChanges || summary.SavePointID != "sp_001" {
		t.Fatalf("summary = %#v, want unsaved_changes reported", summary)
	}
}

func TestHistoryUsesFixedCommandAndParsesEnvelope(t *testing.T) {
	t.Parallel()

	commandRunner := &fakeCommandRunner{result: CommandResult{Stdout: historySuccessStdout(t)}}
	runner := newTestRunner(t, commandRunner)

	summary, err := runner.History(context.Background(), testControlRoot)
	if err != nil {
		t.Fatalf("History returned error: %v", err)
	}
	if summary.NewestSavePointID != "sp_002" || len(summary.SavePoints) != 2 || summary.SavePoints[0].SavePointID != "sp_001" {
		t.Fatalf("summary = %#v", summary)
	}
	want := CommandSpec{Path: "/opt/afscp/bin/jvs", Args: []string{"--control-root", testControlRoot, "--workspace", "main", "history", "--json"}, Dir: "/var/lib/afscp/jvs-cwd"}
	if !reflect.DeepEqual(commandRunner.calls, []CommandSpec{want}) {
		t.Fatalf("calls mismatch:\n got: %#v\nwant: %#v", commandRunner.calls, []CommandSpec{want})
	}
	assertSummaryDoesNotLeakPaths(t, summary)
}

func TestHistoryAllowsEmptyHistory(t *testing.T) {
	t.Parallel()

	commandRunner := &fakeCommandRunner{result: CommandResult{Stdout: historyStdoutWith(t, func(env map[string]any) {
		data := env["data"].(map[string]any)
		delete(data, "newest_save_point")
		data["save_points"] = []map[string]any{}
	})}}
	runner := newTestRunner(t, commandRunner)

	summary, err := runner.History(context.Background(), testControlRoot)
	if err != nil {
		t.Fatalf("History returned error: %v", err)
	}
	if summary.NewestSavePointID != "" || len(summary.SavePoints) != 0 {
		t.Fatalf("summary = %#v, want empty history", summary)
	}
}

func TestRestorePreviewRunDiscardAndRecoveryStatusUseFixedCommandsAndParseEnvelope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		call    func(*Runner) (any, error)
		stdout  []byte
		wantArg []string
		check   func(t *testing.T, summary any)
	}{
		{
			name:    "preview",
			stdout:  restorePreviewSuccessStdout(t),
			wantArg: []string{"--control-root", testControlRoot, "--workspace", "main", "restore", "sp_001", "--json"},
			call: func(r *Runner) (any, error) {
				return r.RestorePreview(context.Background(), testControlRoot, "sp_001")
			},
			check: func(t *testing.T, summary any) {
				got := summary.(RestorePreviewSummary)
				if got.PlanID != "plan_001" || got.SourceSavePointID != "sp_001" || !got.RunCommandPresent {
					t.Fatalf("summary = %#v", got)
				}
			},
		},
		{
			name:    "run",
			stdout:  restoreRunSuccessStdout(t),
			wantArg: []string{"--control-root", testControlRoot, "--workspace", "main", "restore", "--run", "plan_001", "--json"},
			call: func(r *Runner) (any, error) {
				return r.RestoreRun(context.Background(), testControlRoot, "plan_001")
			},
			check: func(t *testing.T, summary any) {
				got := summary.(RestoreRunSummary)
				if got.PlanID != "plan_001" || got.RestoredSavePointID != "sp_001" {
					t.Fatalf("summary = %#v", got)
				}
			},
		},
		{
			name:    "discard",
			stdout:  restoreDiscardSuccessStdout(t),
			wantArg: []string{"--control-root", testControlRoot, "--workspace", "main", "restore", "discard", "plan_001", "--json"},
			call: func(r *Runner) (any, error) {
				return r.RestoreDiscard(context.Background(), testControlRoot, "plan_001")
			},
			check: func(t *testing.T, summary any) {
				got := summary.(RestoreDiscardSummary)
				if got.PlanID != "plan_001" || !got.PlanDiscarded {
					t.Fatalf("summary = %#v", got)
				}
			},
		},
		{
			name:    "recovery status",
			stdout:  recoveryStatusSuccessStdout(t),
			wantArg: []string{"--control-root", testControlRoot, "--workspace", "main", "recovery", "status", "--json"},
			call: func(r *Runner) (any, error) {
				return r.RecoveryStatus(context.Background(), testControlRoot)
			},
			check: func(t *testing.T, summary any) {
				got := summary.(RecoveryStatusSummary)
				if got.RestoreState != "pending_restore_preview" || got.ActivePlanID != "plan_001" || !got.Blocking || got.ActiveRecoveryPlanID != "recovery_001" {
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
				t.Fatalf("call returned error: %v", err)
			}
			tt.check(t, summary)
			want := CommandSpec{Path: "/opt/afscp/bin/jvs", Args: tt.wantArg, Dir: "/var/lib/afscp/jvs-cwd"}
			if !reflect.DeepEqual(commandRunner.calls, []CommandSpec{want}) {
				t.Fatalf("calls mismatch:\n got: %#v\nwant: %#v", commandRunner.calls, []CommandSpec{want})
			}
			assertNoForbiddenJVSFlags(t, commandRunner.calls[0].Args)
			assertSummaryDoesNotLeakPaths(t, summary)
		})
	}
}

func TestRecoveryStatusAllowsIdleWithoutRestoreState(t *testing.T) {
	t.Parallel()

	commandRunner := &fakeCommandRunner{result: CommandResult{Stdout: recoveryStatusStdoutWith(t, func(env map[string]any) {
		data := env["data"].(map[string]any)
		delete(data, "restore_state")
		data["plans"] = []map[string]any{}
	})}}
	runner := newTestRunner(t, commandRunner)

	summary, err := runner.RecoveryStatus(context.Background(), testControlRoot)
	if err != nil {
		t.Fatalf("RecoveryStatus returned error: %v", err)
	}
	if summary.RestoreState != "idle" || summary.ActivePlanID != "" || summary.ActiveRecoveryPlanID != "" {
		t.Fatalf("summary = %#v, want idle", summary)
	}
}

func TestRecoveryStatusParsesSingleActiveRecoveryPlan(t *testing.T) {
	t.Parallel()

	commandRunner := &fakeCommandRunner{result: CommandResult{Stdout: recoveryStatusStdoutWith(t, func(env map[string]any) {
		data := env["data"].(map[string]any)
		delete(data, "restore_state")
		data["plans"] = []map[string]any{{
			"plan_id":         "recovery_003",
			"restore_plan_id": "plan_003",
			"status":          "active",
			"operation":       "resume",
			"source":          "/srv/afscp/secret/source",
		}}
	})}}
	runner := newTestRunner(t, commandRunner)

	summary, err := runner.RecoveryStatus(context.Background(), testControlRoot)
	if err != nil {
		t.Fatalf("RecoveryStatus returned error: %v", err)
	}
	if summary.RestoreState != "active_recovery" || !summary.Blocking || summary.ActiveRecoveryPlanID != "recovery_003" || summary.ActivePlanID != "plan_003" || summary.Workspace != "main" {
		t.Fatalf("summary = %#v", summary)
	}
	assertSummaryDoesNotLeakPaths(t, summary)
}

func TestRecoveryStatusParsesStaleRestorePreview(t *testing.T) {
	t.Parallel()

	commandRunner := &fakeCommandRunner{result: CommandResult{Stdout: recoveryStatusStdoutWith(t, func(env map[string]any) {
		env["data"].(map[string]any)["restore_state"] = map[string]any{
			"state":            "stale_restore_preview",
			"blocking":         true,
			"plan_id":          "plan_002",
			"recovery_plan_id": "recovery_002",
		}
	})}}
	runner := newTestRunner(t, commandRunner)

	summary, err := runner.RecoveryStatus(context.Background(), testControlRoot)
	if err != nil {
		t.Fatalf("RecoveryStatus returned error: %v", err)
	}
	if summary.RestoreState != "stale_restore_preview" || summary.ActivePlanID != "plan_002" || summary.ActiveRecoveryPlanID != "recovery_002" || !summary.Blocking {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestRepoCloneUsesFixedCommandAndParsesEnvelope(t *testing.T) {
	t.Parallel()

	commandRunner := &fakeCommandRunner{result: CommandResult{Stdout: repoCloneSuccessStdout(t)}}
	runner := newTestRunner(t, commandRunner)

	summary, err := runner.RepoClone(context.Background(), testControlRoot, testTargetPayloadRoot, testTargetControlRoot)
	if err != nil {
		t.Fatalf("RepoClone returned error: %v", err)
	}
	if summary.SourceRepoID != "jvs_repo_alpha" || summary.TargetRepoID != "jvs_repo_clone" || summary.SavePointsMode != "main" || summary.SavePointsCopiedCount != 2 || summary.RuntimeStateCopied {
		t.Fatalf("summary = %#v", summary)
	}
	want := CommandSpec{Path: "/opt/afscp/bin/jvs", Args: []string{"--control-root", testControlRoot, "--workspace", "main", "repo", "clone", testTargetPayloadRoot, "--target-control-root", testTargetControlRoot, "--save-points", "main", "--json"}, Dir: "/var/lib/afscp/jvs-cwd"}
	if !reflect.DeepEqual(commandRunner.calls, []CommandSpec{want}) {
		t.Fatalf("calls mismatch:\n got: %#v\nwant: %#v", commandRunner.calls, []CommandSpec{want})
	}
	assertNoForbiddenJVSFlags(t, commandRunner.calls[0].Args)
	assertSummaryDoesNotLeakPaths(t, summary)
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
			return initStdoutWith(t, func(env map[string]any) { env["schema_version"] = "jvs/v0.4.8" })
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

func TestDoctorStrictFailsClosedForInvalidEnvelope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		stdout func(t *testing.T) []byte
		result CommandResult
	}{
		{name: "nonzero", result: CommandResult{ExitCode: 1, Stderr: []byte("credential_ref=secret /srv/afscp/raw")}},
		{name: "malformed json", result: CommandResult{Stdout: []byte(`{"ok":`)}},
		{name: "trailing garbage", stdout: func(t *testing.T) []byte { return append(doctorSuccessStdout(t), []byte(" /srv/afscp/secret")...) }},
		{name: "multiple json", stdout: func(t *testing.T) []byte { return append(doctorSuccessStdout(t), doctorSuccessStdout(t)...) }},
		{name: "ok false", stdout: func(t *testing.T) []byte {
			return doctorStdoutWith(t, func(env map[string]any) { env["ok"] = false; env["error"] = map[string]any{"message": "secret raw"} })
		}},
		{name: "missing schema version", stdout: func(t *testing.T) []byte {
			return doctorStdoutWith(t, func(env map[string]any) { delete(env, "schema_version") })
		}},
		{name: "wrong schema version", stdout: func(t *testing.T) []byte {
			return doctorStdoutWith(t, func(env map[string]any) { env["schema_version"] = 2 })
		}},
		{name: "string schema version", stdout: func(t *testing.T) []byte {
			return doctorStdoutWith(t, func(env map[string]any) { env["schema_version"] = "jvs/v0.4.8" })
		}},
		{name: "unhealthy", stdout: func(t *testing.T) []byte {
			return doctorStdoutWith(t, func(env map[string]any) { env["data"].(map[string]any)["healthy"] = false })
		}},
		{name: "missing healthy", stdout: func(t *testing.T) []byte {
			return doctorStdoutWith(t, func(env map[string]any) { delete(env["data"].(map[string]any), "healthy") })
		}},
		{name: "missing repo id", stdout: func(t *testing.T) []byte {
			return doctorStdoutWith(t, func(env map[string]any) { delete(env["data"].(map[string]any), "repo_id") })
		}},
		{name: "unsafe repo id semicolon", stdout: func(t *testing.T) []byte {
			return doctorStdoutWith(t, func(env map[string]any) { env["data"].(map[string]any)["repo_id"] = "jvs;repo" })
		}},
		{name: "unsafe repo id equals", stdout: func(t *testing.T) []byte {
			return doctorStdoutWith(t, func(env map[string]any) { env["data"].(map[string]any)["repo_id"] = "bad=id" })
		}},
		{name: "unsafe repo id slash", stdout: func(t *testing.T) []byte {
			return doctorStdoutWith(t, func(env map[string]any) { env["data"].(map[string]any)["repo_id"] = "bad/id" })
		}},
		{name: "unsafe repo id backslash", stdout: func(t *testing.T) []byte {
			return doctorStdoutWith(t, func(env map[string]any) { env["data"].(map[string]any)["repo_id"] = `bad\id` })
		}},
		{name: "unsafe repo id whitespace", stdout: func(t *testing.T) []byte {
			return doctorStdoutWith(t, func(env map[string]any) { env["data"].(map[string]any)["repo_id"] = "bad id" })
		}},
		{name: "unsafe repo id control", stdout: func(t *testing.T) []byte {
			return doctorStdoutWith(t, func(env map[string]any) { env["data"].(map[string]any)["repo_id"] = "bad\nid" })
		}},
		{name: "wrong workspace", stdout: func(t *testing.T) []byte {
			return doctorStdoutWith(t, func(env map[string]any) { env["data"].(map[string]any)["workspace"] = "dev" })
		}},
		{name: "missing top-level workspace", stdout: func(t *testing.T) []byte {
			return doctorStdoutWith(t, func(env map[string]any) { delete(env, "workspace") })
		}},
		{name: "wrong top-level workspace", stdout: func(t *testing.T) []byte {
			return doctorStdoutWith(t, func(env map[string]any) { env["workspace"] = "dev" })
		}},
		{name: "missing command", stdout: func(t *testing.T) []byte {
			return doctorStdoutWith(t, func(env map[string]any) { delete(env, "command") })
		}},
		{name: "command mismatch", stdout: func(t *testing.T) []byte {
			return doctorStdoutWith(t, func(env map[string]any) { env["command"] = "init" })
		}},
		{name: "missing repo root", stdout: func(t *testing.T) []byte {
			return doctorStdoutWith(t, func(env map[string]any) { delete(env, "repo_root") })
		}},
		{name: "wrong repo root", stdout: func(t *testing.T) []byte {
			return doctorStdoutWith(t, func(env map[string]any) { env["repo_root"] = "/srv/afscp/volumes/vol_default/other/control" })
		}},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.stdout != nil {
				tt.result.Stdout = tt.stdout(t)
			}
			runner := newTestRunner(t, &fakeCommandRunner{result: tt.result})
			_, err := runner.DoctorStrict(context.Background(), testControlRoot)
			if err == nil {
				t.Fatal("DoctorStrict succeeded, want error")
			}
			assertErrorDoesNotLeak(t, err)
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
		{name: "doctor relative control", call: func() error {
			_, err := runner.DoctorStrict(context.Background(), "control")
			return err
		}},
		{name: "save empty message", call: func() error {
			_, err := runner.Save(context.Background(), testControlRoot, " \t")
			return err
		}},
		{name: "restore preview empty save point", call: func() error {
			_, err := runner.RestorePreview(context.Background(), testControlRoot, "")
			return err
		}},
		{name: "restore run unsafe plan id", call: func() error {
			_, err := runner.RestoreRun(context.Background(), testControlRoot, "plan/id")
			return err
		}},
		{name: "repo clone target payload inside source control", call: func() error {
			_, err := runner.RepoClone(context.Background(), testControlRoot, testControlRoot+"/payload", testTargetControlRoot)
			return err
		}},
		{name: "repo clone target roots overlap", call: func() error {
			_, err := runner.RepoClone(context.Background(), testControlRoot, testTargetPayloadRoot, testTargetPayloadRoot+"/control")
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
		{name: "save missing id", stdout: saveStdoutWith(t, func(env map[string]any) { delete(env["data"].(map[string]any), "save_point_id") }), call: func(r *Runner) error {
			_, err := r.Save(context.Background(), testControlRoot, "checkpoint")
			return err
		}},
		{name: "save missing newest", stdout: saveStdoutWith(t, func(env map[string]any) { delete(env["data"].(map[string]any), "newest_save_point") }), call: func(r *Runner) error {
			_, err := r.Save(context.Background(), testControlRoot, "checkpoint")
			return err
		}},
		{name: "save newest mismatch", stdout: saveStdoutWith(t, func(env map[string]any) { env["data"].(map[string]any)["newest_save_point"] = "sp_other" }), call: func(r *Runner) error {
			_, err := r.Save(context.Background(), testControlRoot, "checkpoint")
			return err
		}},
		{name: "save missing unsaved changes", stdout: saveStdoutWith(t, func(env map[string]any) { delete(env["data"].(map[string]any), "unsaved_changes") }), call: func(r *Runner) error {
			_, err := r.Save(context.Background(), testControlRoot, "checkpoint")
			return err
		}},
		{name: "save invalid unsaved changes", stdout: saveStdoutWith(t, func(env map[string]any) { env["data"].(map[string]any)["unsaved_changes"] = "false" }), call: func(r *Runner) error {
			_, err := r.Save(context.Background(), testControlRoot, "checkpoint")
			return err
		}},
		{name: "history truncated", stdout: historyStdoutWith(t, func(env map[string]any) { env["data"].(map[string]any)["truncated"] = true }), call: func(r *Runner) error {
			_, err := r.History(context.Background(), testControlRoot)
			return err
		}},
		{name: "history unsafe save point id", stdout: historyStdoutWith(t, func(env map[string]any) {
			env["data"].(map[string]any)["save_points"] = []map[string]any{{"save_point_id": "sp/secret"}}
		}), call: func(r *Runner) error {
			_, err := r.History(context.Background(), testControlRoot)
			return err
		}},
		{name: "preview changed files", stdout: restorePreviewStdoutWith(t, func(env map[string]any) { env["data"].(map[string]any)["files_changed"] = true }), call: func(r *Runner) error {
			_, err := r.RestorePreview(context.Background(), testControlRoot, "sp_001")
			return err
		}},
		{name: "preview missing files changed", stdout: restorePreviewStdoutWith(t, func(env map[string]any) { delete(env["data"].(map[string]any), "files_changed") }), call: func(r *Runner) error {
			_, err := r.RestorePreview(context.Background(), testControlRoot, "sp_001")
			return err
		}},
		{name: "preview missing history changed", stdout: restorePreviewStdoutWith(t, func(env map[string]any) { delete(env["data"].(map[string]any), "history_changed") }), call: func(r *Runner) error {
			_, err := r.RestorePreview(context.Background(), testControlRoot, "sp_001")
			return err
		}},
		{name: "preview missing run command", stdout: restorePreviewStdoutWith(t, func(env map[string]any) { delete(env["data"].(map[string]any), "run_command") }), call: func(r *Runner) error {
			_, err := r.RestorePreview(context.Background(), testControlRoot, "sp_001")
			return err
		}},
		{name: "run unsaved changes", stdout: restoreRunStdoutWith(t, func(env map[string]any) { env["data"].(map[string]any)["unsaved_changes"] = true }), call: func(r *Runner) error {
			_, err := r.RestoreRun(context.Background(), testControlRoot, "plan_001")
			return err
		}},
		{name: "run missing files changed", stdout: restoreRunStdoutWith(t, func(env map[string]any) { delete(env["data"].(map[string]any), "files_changed") }), call: func(r *Runner) error {
			_, err := r.RestoreRun(context.Background(), testControlRoot, "plan_001")
			return err
		}},
		{name: "run missing history changed", stdout: restoreRunStdoutWith(t, func(env map[string]any) { delete(env["data"].(map[string]any), "history_changed") }), call: func(r *Runner) error {
			_, err := r.RestoreRun(context.Background(), testControlRoot, "plan_001")
			return err
		}},
		{name: "run missing unsaved changes", stdout: restoreRunStdoutWith(t, func(env map[string]any) { delete(env["data"].(map[string]any), "unsaved_changes") }), call: func(r *Runner) error {
			_, err := r.RestoreRun(context.Background(), testControlRoot, "plan_001")
			return err
		}},
		{name: "run missing source and restored save point", stdout: restoreRunStdoutWith(t, func(env map[string]any) {
			data := env["data"].(map[string]any)
			delete(data, "source_save_point")
			delete(data, "restored_save_point")
		}), call: func(r *Runner) error {
			_, err := r.RestoreRun(context.Background(), testControlRoot, "plan_001")
			return err
		}},
		{name: "discard changed history", stdout: restoreDiscardStdoutWith(t, func(env map[string]any) { env["data"].(map[string]any)["history_changed"] = true }), call: func(r *Runner) error {
			_, err := r.RestoreDiscard(context.Background(), testControlRoot, "plan_001")
			return err
		}},
		{name: "discard missing files changed", stdout: restoreDiscardStdoutWith(t, func(env map[string]any) { delete(env["data"].(map[string]any), "files_changed") }), call: func(r *Runner) error {
			_, err := r.RestoreDiscard(context.Background(), testControlRoot, "plan_001")
			return err
		}},
		{name: "discard missing history changed", stdout: restoreDiscardStdoutWith(t, func(env map[string]any) { delete(env["data"].(map[string]any), "history_changed") }), call: func(r *Runner) error {
			_, err := r.RestoreDiscard(context.Background(), testControlRoot, "plan_001")
			return err
		}},
		{name: "recovery unknown state", stdout: recoveryStatusStdoutWith(t, func(env map[string]any) { env["data"].(map[string]any)["restore_state"] = "mystery" }), call: func(r *Runner) error {
			_, err := r.RecoveryStatus(context.Background(), testControlRoot)
			return err
		}},
		{name: "recovery unsafe active plan", stdout: recoveryStatusStdoutWith(t, func(env map[string]any) {
			env["data"].(map[string]any)["restore_state"] = map[string]any{"state": "pending_restore_preview", "plan_id": "plan/secret"}
		}), call: func(r *Runner) error {
			_, err := r.RecoveryStatus(context.Background(), testControlRoot)
			return err
		}},
		{name: "recovery pending missing restore plan id", stdout: recoveryStatusStdoutWith(t, func(env map[string]any) {
			env["data"].(map[string]any)["restore_state"] = map[string]any{"state": "pending_restore_preview", "blocking": true}
		}), call: func(r *Runner) error {
			_, err := r.RecoveryStatus(context.Background(), testControlRoot)
			return err
		}},
		{name: "recovery multiple active plans", stdout: recoveryStatusStdoutWith(t, func(env map[string]any) {
			data := env["data"].(map[string]any)
			delete(data, "restore_state")
			data["plans"] = []map[string]any{{"plan_id": "recovery_1", "status": "active"}, {"plan_id": "recovery_2", "status": "active"}}
		}), call: func(r *Runner) error {
			_, err := r.RecoveryStatus(context.Background(), testControlRoot)
			return err
		}},
		{name: "clone runtime copied", stdout: repoCloneStdoutWith(t, func(env map[string]any) { env["data"].(map[string]any)["runtime_state_copied"] = true }), call: func(r *Runner) error {
			_, err := r.RepoClone(context.Background(), testControlRoot, testTargetPayloadRoot, testTargetControlRoot)
			return err
		}},
		{name: "clone missing runtime copied", stdout: repoCloneStdoutWith(t, func(env map[string]any) { delete(env["data"].(map[string]any), "runtime_state_copied") }), call: func(r *Runner) error {
			_, err := r.RepoClone(context.Background(), testControlRoot, testTargetPayloadRoot, testTargetControlRoot)
			return err
		}},
		{name: "clone negative copied count", stdout: repoCloneStdoutWith(t, func(env map[string]any) { env["data"].(map[string]any)["save_points_copied_count"] = -1 }), call: func(r *Runner) error {
			_, err := r.RepoClone(context.Background(), testControlRoot, testTargetPayloadRoot, testTargetControlRoot)
			return err
		}},
		{name: "clone target control mismatch", stdout: repoCloneStdoutWith(t, func(env map[string]any) {
			env["data"].(map[string]any)["target_control_root"] = "/srv/afscp/secret/control"
		}), call: func(r *Runner) error {
			_, err := r.RepoClone(context.Background(), testControlRoot, testTargetPayloadRoot, testTargetControlRoot)
			return err
		}},
		{name: "clone envelope root mismatch", stdout: repoCloneStdoutWith(t, func(env map[string]any) { env["repo_root"] = testControlRoot }), call: func(r *Runner) error {
			_, err := r.RepoClone(context.Background(), testControlRoot, testTargetPayloadRoot, testTargetControlRoot)
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
		{name: "save", call: func(r *Runner) error {
			_, err := r.Save(context.Background(), testControlRoot, "checkpoint")
			return err
		}},
		{name: "history", call: func(r *Runner) error { _, err := r.History(context.Background(), testControlRoot); return err }},
		{name: "restore preview", call: func(r *Runner) error {
			_, err := r.RestorePreview(context.Background(), testControlRoot, "sp_001")
			return err
		}},
		{name: "restore run", call: func(r *Runner) error {
			_, err := r.RestoreRun(context.Background(), testControlRoot, "plan_001")
			return err
		}},
		{name: "restore discard", call: func(r *Runner) error {
			_, err := r.RestoreDiscard(context.Background(), testControlRoot, "plan_001")
			return err
		}},
		{name: "recovery status", call: func(r *Runner) error { _, err := r.RecoveryStatus(context.Background(), testControlRoot); return err }},
		{name: "repo clone", call: func(r *Runner) error {
			_, err := r.RepoClone(context.Background(), testControlRoot, testTargetPayloadRoot, testTargetControlRoot)
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
		{name: "ok false", result: CommandResult{Stdout: mustJSON(t, map[string]any{"schema_version": 1, "command": "save", "repo_root": testControlRoot, "workspace": "main", "ok": false, "data": map[string]any{}, "error": map[string]any{"message": "token=secret"}})}},
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
		{"save_point_id": "sp_001", "message": "first"},
		{"save_point_id": "sp_002", "message": "second"},
	}
	if mutate != nil {
		mutate(env)
	}
	return mustJSON(t, env)
}

func restorePreviewSuccessStdout(t *testing.T) []byte {
	t.Helper()
	return restorePreviewStdoutWith(t, nil)
}

func restorePreviewStdoutWith(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	env := baseEnvelope("restore")
	data := env["data"].(map[string]any)
	data["workspace"] = "main"
	data["mode"] = "preview"
	data["plan_id"] = "plan_001"
	data["source_save_point"] = "sp_001"
	data["run_command"] = "jvs restore --run plan_001"
	data["files_changed"] = false
	data["history_changed"] = false
	if mutate != nil {
		mutate(env)
	}
	return mustJSON(t, env)
}

func restoreRunSuccessStdout(t *testing.T) []byte {
	t.Helper()
	return restoreRunStdoutWith(t, nil)
}

func restoreRunStdoutWith(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	env := baseEnvelope("restore")
	data := env["data"].(map[string]any)
	data["workspace"] = "main"
	data["mode"] = "run"
	data["plan_id"] = "plan_001"
	data["restored_save_point"] = "sp_001"
	data["files_changed"] = true
	data["history_changed"] = false
	data["unsaved_changes"] = false
	if mutate != nil {
		mutate(env)
	}
	return mustJSON(t, env)
}

func restoreDiscardSuccessStdout(t *testing.T) []byte {
	t.Helper()
	return restoreDiscardStdoutWith(t, nil)
}

func restoreDiscardStdoutWith(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	env := baseEnvelope("restore")
	data := env["data"].(map[string]any)
	data["workspace"] = "main"
	data["mode"] = "discard"
	data["plan_id"] = "plan_001"
	data["plan_discarded"] = true
	data["files_changed"] = false
	data["history_changed"] = false
	if mutate != nil {
		mutate(env)
	}
	return mustJSON(t, env)
}

func recoveryStatusSuccessStdout(t *testing.T) []byte {
	t.Helper()
	return recoveryStatusStdoutWith(t, nil)
}

func recoveryStatusStdoutWith(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	env := baseEnvelope("recovery status")
	data := env["data"].(map[string]any)
	data["workspace"] = "main"
	data["restore_state"] = map[string]any{
		"state":                    "pending_restore_preview",
		"blocking":                 true,
		"plan_id":                  "plan_001",
		"recovery_plan_id":         "recovery_001",
		"message":                  "pending preview",
		"recommended_next_command": "/srv/afscp/secret/run",
	}
	data["plans"] = []map[string]any{}
	if mutate != nil {
		mutate(env)
	}
	return mustJSON(t, env)
}

func repoCloneSuccessStdout(t *testing.T) []byte {
	t.Helper()
	return repoCloneStdoutWith(t, nil)
}

func repoCloneStdoutWith(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	env := baseEnvelope("repo clone")
	env["repo_root"] = testTargetControlRoot
	data := env["data"].(map[string]any)
	data["workspace"] = "main"
	data["source_repo_id"] = "jvs_repo_alpha"
	data["target_repo_id"] = "jvs_repo_clone"
	data["target_folder"] = testTargetPayloadRoot
	data["target_control_root"] = testTargetControlRoot
	data["save_points_mode"] = "main"
	data["save_points_copied_count"] = 2
	data["runtime_state_copied"] = false
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
