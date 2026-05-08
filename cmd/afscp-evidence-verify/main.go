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
	var mode string
	var selectorPath string
	var selectorIntentPath string
	var checkOnly bool
	flags.StringVar(&mode, "mode", "", "manifest verification mode: seed or final")
	flags.StringVar(&manifestPath, "manifest", "", "release evidence manifest path")
	flags.StringVar(&selectorPath, "selector", "", "release selector path; required for final mode")
	flags.StringVar(&selectorIntentPath, "selector-intent", "", "print release selector intent for the authoritative selector")
	flags.StringVar(&repoRoot, "repo-root", "", "repository root; defaults to the current working directory")
	flags.BoolVar(&checkOnly, "check-only", false, "validate manifest structure without executing required evidence commands")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if selectorIntentPath != "" {
		intent, err := releaseevidence.ReleaseSelectorIntentFile(selectorIntentPath, repoRootOrDefault(repoRoot))
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		fmt.Fprintln(stdout, intent)
		return 0
	}
	if mode != releaseevidence.ManifestModeSeed && mode != releaseevidence.ManifestModeFinal {
		fmt.Fprintln(stderr, "-mode seed|final is required")
		return 2
	}
	if manifestPath == "" {
		fmt.Fprintln(stderr, "-manifest is required")
		return 2
	}
	if mode == releaseevidence.ManifestModeFinal && selectorPath == "" {
		fmt.Fprintf(stderr, "-selector %s is required for -mode final\n", releaseevidence.AuthoritativeReleaseSelectorPath)
		return 2
	}
	if mode == releaseevidence.ManifestModeFinal && checkOnly {
		fmt.Fprintln(stderr, "-check-only validates final structure only and cannot declare final acceptance; run the release script without -check-only to execute required evidence")
		return 2
	}

	findings, err := releaseevidence.VerifyFile(manifestPath, releaseevidence.Options{
		RepoRoot:        repoRoot,
		Mode:            mode,
		SelectorPath:    selectorPath,
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

func repoRootOrDefault(repoRoot string) string {
	if repoRoot != "" {
		return repoRoot
	}
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}
