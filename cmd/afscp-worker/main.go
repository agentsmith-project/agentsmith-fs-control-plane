package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/worker"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/workerapp"
)

const (
	commandName = "afscp-worker"
)

var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout io.Writer, stderr io.Writer) int {
	return newCommand(stdout, stderr).run(args)
}

type runOnceRunner interface {
	RunOnce(context.Context) (worker.Result, error)
}

type command struct {
	stdout    io.Writer
	stderr    io.Writer
	newRunner func() (runOnceRunner, error)
}

func newCommand(stdout io.Writer, stderr io.Writer) command {
	return command{
		stdout: stdout,
		stderr: stderr,
		newRunner: func() (runOnceRunner, error) {
			return workerapp.NewRunOnceRunnerFromEnv()
		},
	}
}

func (cmd command) run(args []string) int {
	flags := flag.NewFlagSet(commandName, flag.ContinueOnError)
	flags.SetOutput(cmd.stderr)

	showVersion := flags.Bool("version", false, "print version")
	runOnce := flags.Bool("run-once", false, "run one bounded worker pass")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(cmd.stderr, "%s: unexpected argument %q\n", commandName, audit.RedactString(flags.Arg(0)))
		return 2
	}

	if *showVersion {
		fmt.Fprintf(cmd.stdout, "%s %s\n", commandName, version)
		return 0
	}

	if !*runOnce {
		return 0
	}

	runner, err := cmd.newRunner()
	if err != nil {
		fmt.Fprintf(cmd.stderr, "%s: invalid worker config: %v\n", commandName, audit.RedactString(err.Error()))
		return 2
	}
	if runner == nil {
		fmt.Fprintf(cmd.stderr, "%s: invalid worker config: runner is required\n", commandName)
		return 2
	}
	result, err := runner.RunOnce(context.Background())
	if encodeErr := json.NewEncoder(cmd.stdout).Encode(result.Summary()); encodeErr != nil {
		fmt.Fprintf(cmd.stderr, "%s: encode run-once summary: %v\n", commandName, encodeErr)
		return 1
	}
	if err != nil {
		fmt.Fprintf(cmd.stderr, "%s: run-once failed: %s\n", commandName, audit.RedactString(err.Error()))
		return 1
	}

	return 0
}
