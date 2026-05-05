package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/contractcheck"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("afscp-contract-verify", flag.ContinueOnError)
	flags.SetOutput(stderr)

	var openAPIPath string
	var schemaPath string
	var apiContractPath string
	var apiDraftPath string
	flags.StringVar(&openAPIPath, "openapi", "", "OpenAPI contract path")
	flags.StringVar(&schemaPath, "schema", "", "JSON schema contract path")
	flags.StringVar(&apiContractPath, "api-contract", "", "API contract markdown path")
	flags.StringVar(&apiDraftPath, "api-draft", "", "API contract draft markdown path")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if openAPIPath == "" || schemaPath == "" || apiContractPath == "" || apiDraftPath == "" {
		fmt.Fprintln(stderr, "all flags are required: -openapi, -schema, -api-contract, -api-draft")
		return 2
	}

	findings, err := contractcheck.VerifyFiles(openAPIPath, schemaPath, apiContractPath, apiDraftPath)
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
