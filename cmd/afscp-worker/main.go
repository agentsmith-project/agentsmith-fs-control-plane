package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/worker"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/workerapp"
)

const (
	commandName         = "afscp-worker"
	defaultLoopInterval = 2 * time.Second
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(newCommand(os.Stdout, os.Stderr).runContext(ctx, os.Args[1:]))
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
	return cmd.runContext(context.Background(), args)
}

func (cmd command) runContext(ctx context.Context, args []string) int {
	if ctx == nil {
		ctx = context.Background()
	}
	flags := flag.NewFlagSet(commandName, flag.ContinueOnError)
	flags.SetOutput(cmd.stderr)

	showVersion := flags.Bool("version", false, "print version")
	runOnce := flags.Bool("run-once", false, "run one bounded worker pass")
	loop := flags.Bool("loop", false, "run worker passes until interrupted")
	interval := flags.Duration("interval", defaultLoopInterval, "worker loop interval")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(cmd.stderr, "%s: unexpected argument %q\n", commandName, audit.RedactString(flags.Arg(0)))
		return 2
	}
	intervalSet := false
	flags.Visit(func(flag *flag.Flag) {
		if flag.Name == "interval" {
			intervalSet = true
		}
	})

	if *showVersion {
		fmt.Fprintf(cmd.stdout, "%s %s\n", commandName, version)
		return 0
	}

	if *loop && *runOnce {
		fmt.Fprintf(cmd.stderr, "%s: --loop cannot be combined with --run-once\n", commandName)
		return 2
	}
	if intervalSet && !*loop {
		fmt.Fprintf(cmd.stderr, "%s: --interval requires --loop\n", commandName)
		return 2
	}
	if (*loop || intervalSet) && *interval <= 0 {
		fmt.Fprintf(cmd.stderr, "%s: --interval must be positive\n", commandName)
		return 2
	}

	if *loop {
		return cmd.runLoop(ctx, *interval)
	}

	if !*runOnce {
		return 0
	}

	return cmd.runOnce(ctx)
}

func (cmd command) runOnce(ctx context.Context) int {
	runner, err := cmd.newRunner()
	if err != nil {
		fmt.Fprintf(cmd.stderr, "%s: invalid worker config: %v\n", commandName, audit.RedactString(err.Error()))
		return 2
	}
	if runner == nil {
		fmt.Fprintf(cmd.stderr, "%s: invalid worker config: runner is required\n", commandName)
		return 2
	}
	result, err := runner.RunOnce(ctx)
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

func (cmd command) runLoop(ctx context.Context, interval time.Duration) int {
	for {
		if err := ctx.Err(); err != nil {
			return 0
		}
		if code := cmd.runLoopOnce(ctx); code != 0 {
			return code
		}
		if !waitForNextLoop(ctx, interval) {
			return 0
		}
	}
}

func (cmd command) runLoopOnce(ctx context.Context) int {
	runner, err := cmd.newRunner()
	if err != nil {
		fmt.Fprintf(cmd.stderr, "%s: invalid worker config: %v\n", commandName, audit.RedactString(err.Error()))
		return 0
	}
	if runner == nil {
		fmt.Fprintf(cmd.stderr, "%s: invalid worker config: runner is required\n", commandName)
		return 0
	}

	result, err := runner.RunOnce(ctx)
	if encodeErr := json.NewEncoder(cmd.stdout).Encode(result.Summary()); encodeErr != nil {
		fmt.Fprintf(cmd.stderr, "%s: encode loop summary: %v\n", commandName, encodeErr)
		return 1
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			return 0
		}
		fmt.Fprintf(cmd.stderr, "%s: loop failed: %s\n", commandName, audit.RedactString(err.Error()))
	}
	return 0
}

func waitForNextLoop(ctx context.Context, interval time.Duration) bool {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
