package contractcheck

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyFilesCatchesOpenAPIGuardrailFailures(t *testing.T) {
	paths := writeContractFixture(t, contractFixture{
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
      name: X-Wrong-Namespace
      in: header
paths:
  /internal/v1/volumes/{volumeId}:ensure:
    post:
      operationId: ensureVolume
      parameters:
        - $ref: "#/components/parameters/IdempotencyKey"
        - $ref: "#/components/parameters/CorrelationId"
        - $ref: "#/components/parameters/CallerService"
        - $ref: "#/components/parameters/ActorType"
        - $ref: "#/components/parameters/ActorId"
  /internal/v1/repos:
    get:
      operationId: listRepos
      parameters:
        - $ref: "#/components/parameters/CorrelationId"
        - $ref: "#/components/parameters/CallerService"
    post:
      operationId: createRepo
      parameters:
        - $ref: "#/components/parameters/IdempotencyKey"
        - $ref: "#/components/parameters/CorrelationId"
        - $ref: "#/components/parameters/CallerService"
        - $ref: "#/components/parameters/NamespaceId"
        - $ref: "#/components/parameters/ActorType"
`,
		schema: validSchema,
		docs:   validDocs,
		draft:  validDocs,
	})

	findings, err := VerifyFiles(paths.openapi, paths.schema, paths.docs, paths.draft)
	if err != nil {
		t.Fatalf("VerifyFiles returned error: %v", err)
	}

	assertHasFinding(t, findings, CodeOpenAPINamespaceParameterInvalid)
	assertHasFinding(t, findings, CodeOpenAPINamespaceParameterMissing)
	assertHasFinding(t, findings, CodeOpenAPIMutatingHeaderMissing)
	assertNoFindingMessageContains(t, findings, "ensureVolume", CodeOpenAPINamespaceParameterMissing)
}

func TestVerifyFilesIgnoresParameterRefsOutsideParametersBlock(t *testing.T) {
	paths := writeContractFixture(t, contractFixture{
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
      description: "Documentation mentions #/components/parameters/NamespaceId but does not declare it."
      parameters:
        - $ref: "#/components/parameters/CorrelationId"
        - $ref: "#/components/parameters/CallerService"
    post:
      operationId: createRepo
      description: |
        Examples mention #/components/parameters/IdempotencyKey,
        #/components/parameters/CorrelationId, #/components/parameters/CallerService,
        #/components/parameters/NamespaceId, #/components/parameters/ActorType,
        and #/components/parameters/ActorId without declaring them as parameters.
      responses:
        "202":
          description: accepted
`,
		schema: validSchema,
		docs:   validDocs,
		draft:  validDocs,
	})

	findings, err := VerifyFiles(paths.openapi, paths.schema, paths.docs, paths.draft)
	if err != nil {
		t.Fatalf("VerifyFiles returned error: %v", err)
	}

	assertHasFinding(t, findings, CodeOpenAPINamespaceParameterMissing)
	assertHasFinding(t, findings, CodeOpenAPIMutatingHeaderMissing)
}

func TestVerifyFilesParsesQuotedIndentedOpenAPIPaths(t *testing.T) {
	paths := writeContractFixture(t, contractFixture{
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
    "/internal/v1/repos":
      get:
        operationId: listRepos
        parameters:
          - $ref: "#/components/parameters/CorrelationId"
          - $ref: "#/components/parameters/CallerService"
`,
		schema: validSchema,
		docs:   validDocs,
		draft:  validDocs,
	})

	findings, err := VerifyFiles(paths.openapi, paths.schema, paths.docs, paths.draft)
	if err != nil {
		t.Fatalf("VerifyFiles returned error: %v", err)
	}

	assertHasFinding(t, findings, CodeOpenAPINamespaceParameterMissing)
}

func TestVerifyFilesFailsWhenOpenAPIScannerFindsNoOperations(t *testing.T) {
	paths := writeContractFixture(t, contractFixture{
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
  {}
`,
		schema: validSchema,
		docs:   validDocs,
		draft:  validDocs,
	})

	findings, err := VerifyFiles(paths.openapi, paths.schema, paths.docs, paths.draft)
	if err != nil {
		t.Fatalf("VerifyFiles returned error: %v", err)
	}

	assertHasFinding(t, findings, "openapi.operations_missing")
}

func TestVerifyFilesOnlyExemptsKnownVolumeGlobalOperations(t *testing.T) {
	paths := writeContractFixture(t, contractFixture{
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
  /internal/v1/volumes/{volumeId}/repos:
    get:
      operationId: listVolumeRepos
      parameters:
        - $ref: "#/components/parameters/CorrelationId"
        - $ref: "#/components/parameters/CallerService"
`,
		schema: validSchema,
		docs:   validDocs,
		draft:  validDocs,
	})

	findings, err := VerifyFiles(paths.openapi, paths.schema, paths.docs, paths.draft)
	if err != nil {
		t.Fatalf("VerifyFiles returned error: %v", err)
	}

	assertHasFinding(t, findings, CodeOpenAPINamespaceParameterMissing)
}

func TestVerifyFilesCatchesSchemaGuardrailFailures(t *testing.T) {
	paths := writeContractFixture(t, contractFixture{
		openapi: validOpenAPI,
		schema: `
{
  "$defs": {
    "ExportSession": {
      "required": ["export_id", "created_by_caller_service", "created_at"]
    },
    "OperationEnvelope": {
      "required": ["operation", "operation_id", "operation_state", "resource"],
      "properties": {
        "operation": { "type": "object" },
        "operation_id": { "type": "string" },
        "operation_state": { "type": "string" },
        "resource": { "type": "object" }
      }
    }
  }
}
`,
		docs:  validDocs,
		draft: validDocs,
	})

	findings, err := VerifyFiles(paths.openapi, paths.schema, paths.docs, paths.draft)
	if err != nil {
		t.Fatalf("VerifyFiles returned error: %v", err)
	}

	assertHasFinding(t, findings, CodeSchemaExportSessionRequiredMissing)
	assertHasFinding(t, findings, CodeSchemaOperationEnvelopeRequiredMissing)
	assertHasFinding(t, findings, CodeSchemaOperationEnvelopeNestedOperation)
}

func TestVerifyFilesCatchesSchemaPropertyAndAdditionalPropertiesFailures(t *testing.T) {
	paths := writeContractFixture(t, contractFixture{
		openapi: validOpenAPI,
		schema: `
{
  "$defs": {
    "ExportSession": {
      "required": [
        "created_by_caller_service",
        "created_by_actor",
        "created_at",
        "revoked_at",
        "last_accessed_at"
      ],
      "properties": {
        "created_by_caller_service": { "type": "string" },
        "created_by_actor": { "type": "string" },
        "created_at": { "type": "string" }
      }
    },
    "OperationEnvelope": {
      "required": ["operation_id", "operation_state", "resource", "result", "error"],
      "additionalProperties": true,
      "properties": {
        "operation": { "type": "object" },
        "operation_id": { "type": "string" },
        "operation_state": { "type": "string" },
        "resource": { "type": "object" },
        "result": { "type": ["object", "null"] }
      }
    }
  }
}
`,
		docs:  validDocs,
		draft: validDocs,
	})

	findings, err := VerifyFiles(paths.openapi, paths.schema, paths.docs, paths.draft)
	if err != nil {
		t.Fatalf("VerifyFiles returned error: %v", err)
	}

	assertHasFinding(t, findings, "schema.export_session_property_missing")
	assertHasFinding(t, findings, "schema.export_session_additional_properties_invalid")
	assertHasFinding(t, findings, "schema.operation_envelope_property_missing")
	assertHasFinding(t, findings, "schema.operation_envelope_additional_properties_invalid")
	assertHasFinding(t, findings, CodeSchemaOperationEnvelopeNestedOperation)
}

func TestVerifyFilesCatchesOperationRecordRequiredAndPropertyDrift(t *testing.T) {
	paths := writeContractFixture(t, contractFixture{
		openapi: validOpenAPI,
		schema: `
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
    },
    "OperationRecord": {
      "additionalProperties": true,
      "required": ["operation_id", "operation_type", "operation_state"],
      "properties": {
        "operation_id": { "type": "string" },
        "operation_type": { "type": "string" },
        "operation_state": { "type": "string" }
      }
    }
  }
}
`,
		docs:  validDocs,
		draft: validDocs,
	})

	findings, err := VerifyFiles(paths.openapi, paths.schema, paths.docs, paths.draft)
	if err != nil {
		t.Fatalf("VerifyFiles returned error: %v", err)
	}

	assertHasFinding(t, findings, CodeSchemaOperationRecordRequiredMissing)
	assertHasFinding(t, findings, CodeSchemaOperationRecordPropertyMissing)
	assertHasFinding(t, findings, CodeSchemaOperationRecordAdditionalPropertiesInvalid)
}

func TestVerifyFilesCatchesGoOperationDTOAmbiguityWhenRepoSourceIsAvailable(t *testing.T) {
	root := t.TempDir()
	paths := writeRepoContractFixture(t, root, contractFixture{
		openapi: validOpenAPI,
		schema:  validSchema,
		docs:    validDocs,
		draft:   validDocs,
	})
	writeFile(t, filepath.Join(root, "internal", "operations", "types.go"), `package operations

type OperationEnvelope struct {
	Operation any `+"`json:\"operation\"`"+`
}
`)
	writeFile(t, filepath.Join(root, "internal", "api", "operation.go"), `package api

type OperationEnvelope struct {
	Operation any `+"`json:\"operation\"`"+`
}
`)

	findings, err := VerifyFiles(paths.openapi, paths.schema, paths.docs, paths.draft)
	if err != nil {
		t.Fatalf("VerifyFiles returned error: %v", err)
	}

	assertHasFinding(t, findings, CodeGoOperationsOperationEnvelopeAmbiguous)
	assertHasFinding(t, findings, CodeGoAPIOperationEnvelopeNestedOperation)
}

func TestVerifyFilesCatchesDocsGuardrailFailures(t *testing.T) {
	paths := writeContractFixture(t, contractFixture{
		openapi: validOpenAPI,
		schema:  validSchema,
		docs:    "# Contract\n\nMutating responses return a standard envelope.\n",
		draft:   "# Draft\n\nRequests include headers.\n",
	})

	findings, err := VerifyFiles(paths.openapi, paths.schema, paths.docs, paths.draft)
	if err != nil {
		t.Fatalf("VerifyFiles returned error: %v", err)
	}

	assertHasFinding(t, findings, CodeDocsOperationBoundaryMissing)
	assertHasFinding(t, findings, CodeDocsNamespaceHeaderMissing)
}

func TestCurrentRepoContractsPass(t *testing.T) {
	repoRoot := filepath.Join("..", "..")

	findings, err := VerifyFiles(
		filepath.Join(repoRoot, "api", "openapi", "internal-v1.openapi.yaml"),
		filepath.Join(repoRoot, "api", "schemas", "afscp-internal-v1.schema.json"),
		filepath.Join(repoRoot, "docs", "contracts", "afscp-internal-api-v1.md"),
		filepath.Join(repoRoot, "docs", "API_CONTRACT_DRAFT.md"),
	)
	if err != nil {
		t.Fatalf("VerifyFiles returned error: %v", err)
	}
	if len(findings) > 0 {
		t.Fatalf("expected current repo contracts to pass, got findings: %+v", findings)
	}
}

func assertHasFinding(t *testing.T, findings []Finding, code string) {
	t.Helper()

	for _, finding := range findings {
		if finding.Code == code {
			return
		}
	}
	t.Fatalf("expected finding code %q in %+v", code, findings)
}

func assertNoFindingMessageContains(t *testing.T, findings []Finding, needle, code string) {
	t.Helper()

	for _, finding := range findings {
		if finding.Code == code && contains(finding.Message, needle) {
			t.Fatalf("did not expect finding code %q mentioning %q: %+v", code, needle, finding)
		}
	}
}

func contains(s, needle string) bool {
	for i := 0; i+len(needle) <= len(s); i++ {
		if s[i:i+len(needle)] == needle {
			return true
		}
	}
	return needle == ""
}

type contractFixture struct {
	openapi string
	schema  string
	docs    string
	draft   string
}

type contractPaths struct {
	openapi string
	schema  string
	docs    string
	draft   string
}

func writeContractFixture(t *testing.T, fixture contractFixture) contractPaths {
	t.Helper()

	dir := t.TempDir()
	return writeRepoContractFixture(t, dir, fixture)
}

func writeRepoContractFixture(t *testing.T, dir string, fixture contractFixture) contractPaths {
	t.Helper()

	paths := contractPaths{
		openapi: filepath.Join(dir, "api", "openapi", "internal-v1.openapi.yaml"),
		schema:  filepath.Join(dir, "api", "schemas", "afscp-internal-v1.schema.json"),
		docs:    filepath.Join(dir, "docs", "contracts", "afscp-internal-api-v1.md"),
		draft:   filepath.Join(dir, "docs", "API_CONTRACT_DRAFT.md"),
	}

	writeFile(t, paths.openapi, fixture.openapi)
	writeFile(t, paths.schema, fixture.schema)
	writeFile(t, paths.docs, fixture.docs)
	writeFile(t, paths.draft, fixture.draft)

	return paths
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

const validOpenAPI = `
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
  /internal/v1/volumes/{volumeId}:ensure:
    post:
      operationId: ensureVolume
      parameters:
        - $ref: "#/components/parameters/IdempotencyKey"
        - $ref: "#/components/parameters/CorrelationId"
        - $ref: "#/components/parameters/CallerService"
        - $ref: "#/components/parameters/ActorType"
        - $ref: "#/components/parameters/ActorId"
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

const validSchema = `
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

const validDocs = `
# Contract

` + "`X-AFSCP-Namespace-Id`" + ` is required for namespace-bound requests.

## Response Shape Boundary

Mutating resource endpoints return the flat ` + "`OperationEnvelope`" + ` API response.
The durable ` + "`OperationRecord`" + ` is stored internally and returned only by operation inspection.
`
