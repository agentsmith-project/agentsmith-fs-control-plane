package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/api"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/config"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/observability"
)

const (
	commandName = "afscp-api"
	version     = "dev"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout io.Writer, stderr io.Writer) int {
	return newCommand(stdout, stderr).run(args)
}

type command struct {
	stdout          io.Writer
	stderr          io.Writer
	loadConfig      func() (config.Config, error)
	newNeutralShell func() http.Handler
	serve           func(string, http.Handler) error
}

func newCommand(stdout io.Writer, stderr io.Writer) command {
	return command{
		stdout:     stdout,
		stderr:     stderr,
		loadConfig: config.LoadFromEnv,
		newNeutralShell: func() http.Handler {
			return api.NewNeutralShellWithLogger(observability.NewJSONLogger(stderr, nil))
		},
		serve: http.ListenAndServe,
	}
}

func (cmd command) run(args []string) int {
	flags := flag.NewFlagSet(commandName, flag.ContinueOnError)
	flags.SetOutput(cmd.stderr)

	showVersion := flags.Bool("version", false, "print version")
	dryRun := flags.Bool("dry-run", false, "construct neutral API shell without serving")
	serve := flags.Bool("serve", false, "serve neutral API shell")
	listen := flags.String("listen", "", "listen address for neutral API shell")
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
		fmt.Fprintf(cmd.stderr, "%s: invalid config: %v\n", commandName, err)
		return 2
	}

	listenAddr := cfg.ListenAddr
	if listenExplicit {
		listenAddr = *listen
	}

	listenAddr, err = validateListenAddress(listenAddr)
	if err != nil {
		if listenExplicit {
			fmt.Fprintf(cmd.stderr, "%s: invalid --listen: %v\n", commandName, err)
			return 2
		}
		fmt.Fprintf(cmd.stderr, "%s: invalid config listen address: %v\n", commandName, err)
		return 2
	}

	handler := cmd.newNeutralShell()

	if *dryRun {
		fmt.Fprintf(cmd.stdout, "%s neutral shell configured for %s\n", commandName, listenAddr)
		return 0
	}

	if err := cmd.serve(listenAddr, handler); err != nil {
		fmt.Fprintf(cmd.stderr, "%s: %v\n", commandName, err)
		return 1
	}

	return 0
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
