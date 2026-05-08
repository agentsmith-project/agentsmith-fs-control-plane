package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/releaseevidence"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("afscp-evidence-verify", flag.ContinueOnError)
	flags.SetOutput(stderr)

	var manifestPath string
	var repoRoot string
	var checkOnly bool
	flags.StringVar(&manifestPath, "manifest", "", "release evidence manifest path")
	flags.StringVar(&repoRoot, "repo-root", "", "repository root; defaults to the current working directory")
	flags.BoolVar(&checkOnly, "check-only", false, "validate manifest structure without executing required evidence commands")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if manifestPath == "" {
		fmt.Fprintln(stderr, "-manifest is required")
		return 2
	}

	findings, err := releaseevidence.VerifyFile(manifestPath, releaseevidence.Options{
		RepoRoot:        repoRoot,
		ExecuteRequired: !checkOnly,
		Stdout:          stdout,
		Stderr:          stderr,
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if len(findings) > 0 {
		for _, finding := range findings {
			fmt.Fprintln(stdout, finding.String())
		}
		return 1
	}
	return 0
}
