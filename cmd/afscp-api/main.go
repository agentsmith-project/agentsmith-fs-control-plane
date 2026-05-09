package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/api"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/apiapp"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/config"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/observability"
)

const (
	commandName = "afscp-api"
)

var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout io.Writer, stderr io.Writer) int {
	return newCommand(stdout, stderr).run(args)
}

type command struct {
	stdout             io.Writer
	stderr             io.Writer
	loadConfig         func() (config.Config, error)
	newNeutralShell    func() http.Handler
	newInternalRuntime func(config.Config) (apiRuntime, error)
	serve              func(string, http.Handler) error
}

type apiRuntime struct {
	Handler http.Handler
	Close   func() error
}

func newCommand(stdout io.Writer, stderr io.Writer) command {
	return command{
		stdout:     stdout,
		stderr:     stderr,
		loadConfig: config.LoadFromEnv,
		newNeutralShell: func() http.Handler {
			return api.NewNeutralShellWithLogger(observability.NewJSONLogger(stderr, nil))
		},
		newInternalRuntime: func(cfg config.Config) (apiRuntime, error) {
			runtime, err := apiapp.NewRuntimeFromConfig(cfg, apiapp.Options{
				Logger: observability.NewJSONLogger(stderr, nil),
			})
			if err != nil {
				return apiRuntime{}, err
			}
			return apiRuntime{
				Handler: runtime.Handler,
				Close:   runtime.Close,
			}, nil
		},
		serve: http.ListenAndServe,
	}
}

func (cmd command) run(args []string) int {
	flags := flag.NewFlagSet(commandName, flag.ContinueOnError)
	flags.SetOutput(cmd.stderr)

	showVersion := flags.Bool("version", false, "print version")
	dryRun := flags.Bool("dry-run", false, "construct configured API runtime without serving")
	serve := flags.Bool("serve", false, "serve configured API runtime")
	listen := flags.String("listen", "", "listen address for API runtime")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	listenExplicit := flagWasSet(flags, "listen")

	if *showVersion {
		fmt.Fprintf(cmd.stdout, "%s %s\n", commandName, version)
		return 0
	}

	if !*dryRun && !*serve {
		return 0
	}

	cfg, err := cmd.loadConfig()
	if err != nil {
		fmt.Fprintf(cmd.stderr, "%s: invalid config: %s\n", commandName, safeError(err))
		return 2
	}

	listenAddr := cfg.ListenAddr
	if listenExplicit {
		listenAddr = *listen
	}

	listenAddr, err = validateListenAddress(listenAddr)
	if err != nil {
		if listenExplicit {
			fmt.Fprintf(cmd.stderr, "%s: invalid --listen: %s\n", commandName, safeError(err))
			return 2
		}
		fmt.Fprintf(cmd.stderr, "%s: invalid config listen address: %s\n", commandName, safeError(err))
		return 2
	}

	if cfg.API.Mode == "internal" {
		return cmd.runInternal(cfg, listenAddr, *dryRun)
	}

	handler := cmd.newNeutralShell()

	if *dryRun {
		fmt.Fprintf(cmd.stdout, "%s neutral shell configured for %s\n", commandName, listenAddr)
		return 0
	}

	if err := cmd.serve(listenAddr, handler); err != nil {
		fmt.Fprintf(cmd.stderr, "%s: %s\n", commandName, safeError(err))
		return 1
	}

	return 0
}

func (cmd command) runInternal(cfg config.Config, listenAddr string, dryRun bool) int {
	runtime, err := cmd.newInternalRuntime(cfg)
	if err != nil {
		fmt.Fprintf(cmd.stderr, "%s: configure internal runtime: %s\n", commandName, safeError(err))
		return 2
	}
	if runtime.Handler == nil {
		err = errors.New("internal runtime handler is required")
		if runtime.Close != nil {
			err = errors.Join(err, runtime.Close())
		}
		fmt.Fprintf(cmd.stderr, "%s: configure internal runtime: %s\n", commandName, safeError(err))
		return 2
	}

	if dryRun {
		if runtime.Close != nil {
			if err := runtime.Close(); err != nil {
				fmt.Fprintf(cmd.stderr, "%s: close internal runtime: %s\n", commandName, safeError(err))
				return 1
			}
		}
		fmt.Fprintf(cmd.stdout, "%s internal shell configured for %s\n", commandName, listenAddr)
		return 0
	}

	err = cmd.serve(listenAddr, runtime.Handler)
	if runtime.Close != nil {
		err = errors.Join(err, runtime.Close())
	}
	if err != nil {
		fmt.Fprintf(cmd.stderr, "%s: %s\n", commandName, safeError(err))
		return 1
	}
	return 0
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	redacted, _ := observability.RedactString(err.Error())
	return redacted
}

func flagWasSet(flags *flag.FlagSet, name string) bool {
	wasSet := false
	flags.Visit(func(flag *flag.Flag) {
		if flag.Name == name {
			wasSet = true
		}
	})
	return wasSet
}

func validateListenAddress(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", fmt.Errorf("address is required")
	}

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("must be host:port: %w", err)
	}
	if strings.TrimSpace(host) == "" {
		return "", fmt.Errorf("host is required")
	}
	if strings.TrimSpace(port) == "" {
		return "", fmt.Errorf("port is required")
	}

	return addr, nil
}
