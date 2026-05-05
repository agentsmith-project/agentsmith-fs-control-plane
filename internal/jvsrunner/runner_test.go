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
	testPayloadRoot = "/srv/afscp/volumes/vol_default/afscp/namespaces/ns_alpha/repos/repo_alpha/payload"
	testControlRoot = "/srv/afscp/volumes/vol_default/afscp/namespaces/ns_alpha/repos/repo_alpha/control"
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
		"jvs/repo",
		`jvs\repo`,
		"jvs repo",
		"jvs\trepo",
		"jvs\nrepo",
		"jvs=repo",
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
		{name: "unsafe repo id", stdout: func(t *testing.T) []byte {
			return doctorStdoutWith(t, func(env map[string]any) { env["data"].(map[string]any)["repo_id"] = "jvs/repo" })
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
