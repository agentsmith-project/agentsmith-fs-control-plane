package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/contractcheck"
)

func TestRunReturnsZeroWhenContractsPass(t *testing.T) {
	paths := writeCLIFixture(t, cliFixture{
		openapi: cliValidOpenAPI,
		schema:  cliValidSchema,
		docs:    cliValidDocs,
		draft:   cliValidDocs,
	})

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-openapi", paths.openapi,
		"-schema", paths.schema,
		"-api-contract", paths.docs,
		"-api-draft", paths.draft,
	}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no stdout for clean contracts, got %q", stdout.String())
	}
}

func TestRunReturnsOneAndPrintsFindings(t *testing.T) {
	paths := writeCLIFixture(t, cliFixture{
		openapi: `
openapi: 3.1.0
components:
  parameters:
    IdempotencyKey:
      name: Idempotency-Key
      in: header
    CorrelationId:
      name: X-Correlation-Id
      in: header
    ActorType:
      name: X-AFSCP-Actor-Type
      in: header
    ActorId:
      name: X-AFSCP-Actor-Id
      in: header
    CallerService:
      name: X-AFSCP-Caller-Service
      in: header
    NamespaceId:
      name: X-AFSCP-Namespace-Id
      in: header
paths:
  /internal/v1/repos:
    get:
      operationId: listRepos
      parameters:
        - $ref: "#/components/parameters/CorrelationId"
        - $ref: "#/components/parameters/CallerService"
`,
		schema: cliValidSchema,
		docs:   cliValidDocs,
		draft:  cliValidDocs,
	})

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-openapi", paths.openapi,
		"-schema", paths.schema,
		"-api-contract", paths.docs,
		"-api-draft", paths.draft,
	}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("expected exit 1, got %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), contractcheck.CodeOpenAPINamespaceParameterMissing) {
		t.Fatalf("expected stdout to include finding code, got %q", stdout.String())
	}
}

func TestRunReturnsTwoWhenAFileCannotBeRead(t *testing.T) {
	paths := writeCLIFixture(t, cliFixture{
		openapi: cliValidOpenAPI,
		schema:  cliValidSchema,
		docs:    cliValidDocs,
		draft:   cliValidDocs,
	})

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-openapi", filepath.Join(t.TempDir(), "missing.yaml"),
		"-schema", paths.schema,
		"-api-contract", paths.docs,
		"-api-draft", paths.draft,
	}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("expected exit 2, got %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "read") {
		t.Fatalf("expected stderr to describe read error, got %q", stderr.String())
	}
}

type cliFixture struct {
	openapi string
	schema  string
	docs    string
	draft   string
}

type cliPaths struct {
	openapi string
	schema  string
	docs    string
	draft   string
}

func writeCLIFixture(t *testing.T, fixture cliFixture) cliPaths {
	t.Helper()

	dir := t.TempDir()
	paths := cliPaths{
		openapi: filepath.Join(dir, "openapi.yaml"),
		schema:  filepath.Join(dir, "schema.json"),
		docs:    filepath.Join(dir, "contract.md"),
		draft:   filepath.Join(dir, "draft.md"),
	}

	writeCLIFile(t, paths.openapi, fixture.openapi)
	writeCLIFile(t, paths.schema, fixture.schema)
	writeCLIFile(t, paths.docs, fixture.docs)
	writeCLIFile(t, paths.draft, fixture.draft)

	return paths
}

func writeCLIFile(t *testing.T, path, body string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

const cliValidOpenAPI = `
openapi: 3.1.0
components:
  parameters:
    IdempotencyKey:
      name: Idempotency-Key
      in: header
    CorrelationId:
      name: X-Correlation-Id
      in: header
    ActorType:
      name: X-AFSCP-Actor-Type
      in: header
    ActorId:
      name: X-AFSCP-Actor-Id
      in: header
    CallerService:
      name: X-AFSCP-Caller-Service
      in: header
    NamespaceId:
      name: X-AFSCP-Namespace-Id
      in: header
paths:
  /internal/v1/repos:
    get:
      operationId: listRepos
      parameters:
        - $ref: "#/components/parameters/CorrelationId"
        - $ref: "#/components/parameters/CallerService"
        - $ref: "#/components/parameters/NamespaceId"
    post:
      operationId: createRepo
      parameters:
        - $ref: "#/components/parameters/IdempotencyKey"
        - $ref: "#/components/parameters/CorrelationId"
        - $ref: "#/components/parameters/CallerService"
        - $ref: "#/components/parameters/NamespaceId"
        - $ref: "#/components/parameters/ActorType"
        - $ref: "#/components/parameters/ActorId"
`

const cliValidSchema = `
{
  "$defs": {
    "ExportSession": {
      "additionalProperties": false,
      "required": [
        "export_id",
        "created_by_caller_service",
        "created_by_actor",
        "created_at",
        "revoked_at",
        "last_accessed_at"
      ],
      "properties": {
        "export_id": { "type": "string" },
        "created_by_caller_service": { "type": "string" },
        "created_by_actor": { "type": "string" },
        "created_at": { "type": "string" },
        "revoked_at": { "type": ["string", "null"] },
        "last_accessed_at": { "type": ["string", "null"] }
      }
    },
    "OperationEnvelope": {
      "additionalProperties": false,
      "required": ["operation_id", "operation_state", "resource", "result", "error"],
      "properties": {
        "operation_id": { "type": "string" },
        "operation_state": { "type": "string" },
        "resource": { "type": "object" },
        "result": { "type": ["object", "null"] },
        "error": { "type": ["object", "null"] }
      }
    }
  }
}
`

const cliValidDocs = `
# Contract

` + "`X-AFSCP-Namespace-Id`" + ` is required for namespace-bound requests.

The flat ` + "`OperationEnvelope`" + ` API response is separate from the durable ` + "`OperationRecord`" + ` boundary.
`
