package contractcheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/api"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
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

func TestOperatorRepairContractDefinesAllowlistPreconditionsAuditAndForbiddenSQL(t *testing.T) {
	body := readRepoFileForContractTest(t, "docs/contracts/operator-repair-v1.md")
	requireContractPhrases(t, body,
		"terminalize_unsupported_intervention_as_failed",
		"operator_admin",
		"operator_intervention_required",
		"OPERATION_RECOVERY_REQUIRED",
		"reason",
		"evidence_ref",
		"affected_ids",
		"before_state",
		"after_state",
		"audit",
		"arbitrary SQL",
		"generic state rewrite",
	)
}

func TestOperatorRepairContractIsLinkedFromContractsReadme(t *testing.T) {
	body := readRepoFileForContractTest(t, "docs/contracts/README.md")
	requireContractPhrases(t, body, "operator-repair-v1.md")
}

func TestRestoreReconciliationContractDefinesModeDenialCredentialPurgeMismatch(t *testing.T) {
	body := readRepoFileForContractTest(t, "docs/contracts/restore-reconciliation-v1.md")
	requireContractPhrases(t, body,
		"after-restore safety mode",
		"not JVS restore-run",
		"reconciling",
		"blocked_operator_intervention",
		"completed",
		"dangerous writes",
		"durable target set",
		"every target repo",
		"snapshot",
		"generation",
		"tombstone marker",
		"purge marker",
		"no WebDAV credential reissue",
		"purged repo",
		"must not resurrect",
		"remain purged",
		"metadata/storage mismatch",
		"non-purged metadata/storage mismatch",
		"operator_intervention_required",
		"purged metadata/storage mismatch",
		"without resurrecting or moving the repo out of purged state",
		"audit",
		"scripts/verify-ga-release.sh",
	)
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

func TestVerifyFilesCatchesOpenAPIExtraRouteOperation(t *testing.T) {
	openapi := strings.Replace(validOpenAPI, `
paths:
`, `
paths:
  /internal/v1/unregistered:
    get:
      operationId: unregisteredOperation
      parameters:
        - $ref: "#/components/parameters/CorrelationId"
        - $ref: "#/components/parameters/CallerService"
        - $ref: "#/components/parameters/NamespaceId"
`, 1)
	paths := writeContractFixture(t, contractFixture{
		openapi: openapi,
		schema:  validSchema,
		docs:    validDocs,
		draft:   validDocs,
	})

	findings, err := VerifyFiles(paths.openapi, paths.schema, paths.docs, paths.draft)
	if err != nil {
		t.Fatalf("VerifyFiles returned error: %v", err)
	}

	assertHasFinding(t, findings, CodeOpenAPIRouteOperationExtra)
}

func TestVerifyFilesCatchesCreateExportResponseSchemaDrift(t *testing.T) {
	openapi := strings.Replace(validOpenAPI,
		`                $ref: "#/components/schemas/ExportCreateOperationEnvelope"`,
		`                $ref: "#/components/schemas/OperationEnvelope"`,
		1,
	)
	if openapi == validOpenAPI {
		t.Fatal("test fixture did not contain createExport response schema ref")
	}
	paths := writeContractFixture(t, contractFixture{
		openapi: openapi,
		schema:  validSchema,
		docs:    validDocs,
		draft:   validDocs,
	})

	findings, err := VerifyFiles(paths.openapi, paths.schema, paths.docs, paths.draft)
	if err != nil {
		t.Fatalf("VerifyFiles returned error: %v", err)
	}

	assertHasFinding(t, findings, CodeOpenAPIResponseSchemaMismatch)
}

func TestVerifyFilesCatchesOpenAPIRawDirectMountAccessSingleTokenCases(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		operationID string
	}{
		{name: "raw token", path: "/internal/v1/repos/{repoId}:raw", operationID: "inspectRepo"},
		{name: "direct token", path: "/internal/v1/repos/{repoId}:direct", operationID: "inspectRepo"},
		{name: "juicefs token", path: "/internal/v1/repos/{repoId}:juicefs", operationID: "inspectRepo"},
		{name: "break glass token", path: "/internal/v1/repos/{repoId}:break-glass", operationID: "inspectRepo"},
		{name: "mount command token", path: "/internal/v1/repos/{repoId}:mount-command", operationID: "inspectRepo"},
		{name: "compact raw mount command", path: "/internal/v1/repos/{repoId}:probe", operationID: "rawmountcommand"},
		{name: "compact direct mount", path: "/internal/v1/repos/{repoId}:probe", operationID: "directmount"},
		{name: "compact break glass", path: "/internal/v1/repos/{repoId}:probe", operationID: "breakglass"},
		{name: "compact mount command", path: "/internal/v1/repos/{repoId}:probe", operationID: "mountcommand"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			openapi := strings.Replace(validOpenAPI, `
paths:
`, `
paths:
  `+tt.path+`:
    get:
      operationId: `+tt.operationID+`
      parameters:
        - $ref: "#/components/parameters/CorrelationId"
        - $ref: "#/components/parameters/CallerService"
        - $ref: "#/components/parameters/NamespaceId"
`, 1)
			paths := writeContractFixture(t, contractFixture{
				openapi: openapi,
				schema:  validSchema,
				docs:    validDocs,
				draft:   validDocs,
			})

			findings, err := VerifyFiles(paths.openapi, paths.schema, paths.docs, paths.draft)
			if err != nil {
				t.Fatalf("VerifyFiles returned error: %v", err)
			}

			assertFindingCount(t, findings, CodeOpenAPIRawDirectMountAccessForbidden, 1)
		})
	}
}

func TestForbiddenOpenAPIRawDirectMountTokensCoversCompactSingleTokens(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  []string
	}{
		{name: "raw mount command", value: "rawmountcommand", want: []string{"rawmountcommand"}},
		{name: "direct mount", value: "directmount", want: []string{"directmount"}},
		{name: "break glass", value: "breakglass", want: []string{"breakglass"}},
		{name: "mount command", value: "mountcommand", want: []string{"mountcommand"}},
		{name: "workload mount binding allowed", value: "createWorkloadMountBinding", want: nil},
		{name: "workload mount path allowed", value: "/internal/v1/workload-mount-bindings/{mountBindingId}", want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := forbiddenOpenAPIRawDirectMountTokens(tt.value)
			if !sameStrings(got, tt.want) {
				t.Fatalf("forbidden tokens for %q = %#v, want %#v", tt.value, got, tt.want)
			}
		})
	}
}

func TestVerifyFilesCatchesOpenAPIMissingRouteOperation(t *testing.T) {
	openapi := strings.Replace(validOpenAPI, `
  /internal/v1/repos:
    get:
      operationId: listRepos
      parameters:
        - $ref: "#/components/parameters/CorrelationId"
        - $ref: "#/components/parameters/CallerService"
        - $ref: "#/components/parameters/NamespaceId"
`, "", 1)
	paths := writeContractFixture(t, contractFixture{
		openapi: openapi,
		schema:  validSchema,
		docs:    validDocs,
		draft:   validDocs,
	})

	findings, err := VerifyFiles(paths.openapi, paths.schema, paths.docs, paths.draft)
	if err != nil {
		t.Fatalf("VerifyFiles returned error: %v", err)
	}

	assertHasFinding(t, findings, CodeOpenAPIRouteOperationMissing)
}

func TestVerifyFilesCatchesOpenAPIRouteOperationIDMismatch(t *testing.T) {
	openapi := strings.Replace(validOpenAPI, "operationId: listRepos", "operationId: listRepositories", 1)
	paths := writeContractFixture(t, contractFixture{
		openapi: openapi,
		schema:  validSchema,
		docs:    validDocs,
		draft:   validDocs,
	})

	findings, err := VerifyFiles(paths.openapi, paths.schema, paths.docs, paths.draft)
	if err != nil {
		t.Fatalf("VerifyFiles returned error: %v", err)
	}

	assertHasFinding(t, findings, CodeOpenAPIRouteOperationIDMismatch)
}

func TestVerifyFilesCatchesOpenAPIRouteMethodPathDrift(t *testing.T) {
	openapi := strings.Replace(validOpenAPI, `
  /internal/v1/repos:
    get:
      operationId: listRepos
`, `
  /internal/v1/repos:search:
    post:
      operationId: listRepos
`, 1)
	paths := writeContractFixture(t, contractFixture{
		openapi: openapi,
		schema:  validSchema,
		docs:    validDocs,
		draft:   validDocs,
	})

	findings, err := VerifyFiles(paths.openapi, paths.schema, paths.docs, paths.draft)
	if err != nil {
		t.Fatalf("VerifyFiles returned error: %v", err)
	}

	assertHasFinding(t, findings, CodeOpenAPIRouteOperationMissing)
	assertHasFinding(t, findings, CodeOpenAPIRouteOperationExtra)
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

func TestVerifyFilesCatchesSchemaRawCredentialMachineFields(t *testing.T) {
	schema := strings.Replace(validSchema, `
    "AllowedCaller": {
`, `
    "StorageLeak": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "metadata_url": { "type": "string" },
        "nested": {
          "type": "array",
          "items": {
            "type": "object",
            "properties": {
              "bucket_secret_key": { "type": "string" }
            }
          }
        },
        "composed": {
          "allOf": [
            {
              "type": "object",
              "properties": {
                "aws_secret_access_key": { "type": "string" }
              }
            }
          ],
          "oneOf": [
            {
              "type": "object",
              "properties": {
                "raw_mount_command": { "type": "string" }
              }
            },
            { "type": "null" }
          ]
        }
      }
    },
    "AllowedCaller": {
`, 1)
	paths := writeContractFixture(t, contractFixture{
		openapi: validOpenAPI,
		schema:  schema,
		docs:    validDocs,
		draft:   validDocs,
	})

	findings, err := VerifyFiles(paths.openapi, paths.schema, paths.docs, paths.draft)
	if err != nil {
		t.Fatalf("VerifyFiles returned error: %v", err)
	}

	assertFindingCount(t, findings, CodeSchemaRawCredentialFieldForbidden, 4)
}

func TestVerifyFilesCatchesOpenAPISchemaRawCredentialMachineFields(t *testing.T) {
	openapi := strings.Replace(validOpenAPI, `
components:
`, `
components:
  schemas:
    StorageLeak:
      type: object
      additionalProperties: false
      properties:
        metadata_url:
          type: string
        nested:
          type: array
          items:
            type: object
            properties:
              bucket_secret_key:
                type: string
        composed:
          allOf:
            - type: object
              properties:
                aws_secret_access_key:
                  type: string
          anyOf:
            - type: object
              properties:
                raw_mount_command:
                  type: string
            - type: "null"
`, 1)
	paths := writeContractFixture(t, contractFixture{
		openapi: openapi,
		schema:  validSchema,
		docs:    validDocs,
		draft:   validDocs,
	})

	findings, err := VerifyFiles(paths.openapi, paths.schema, paths.docs, paths.draft)
	if err != nil {
		t.Fatalf("VerifyFiles returned error: %v", err)
	}

	assertFindingCount(t, findings, CodeOpenAPISchemaRawCredentialFieldForbidden, 4)
}

func TestVerifyFilesAllowsOrchestratorSecretRefAndIgnoresSchemaDescriptions(t *testing.T) {
	schema := strings.Replace(validSchema, `"export_id": { "type": "string" }`, `"export_id": { "type": "string", "description": "Do not expose metadata_url, bucket_secret_key, aws_secret_access_key, or raw_mount_command here." }`, 1)
	schema = strings.Replace(schema, `
    "AllowedCaller": {
`, `
    "OrchestratorMountPlan": {
      "type": "object",
      "additionalProperties": false,
      "required": ["secret_ref"],
      "properties": {
        "secret_ref": { "type": "string" }
      }
    },
    "AllowedCaller": {
`, 1)
	paths := writeContractFixture(t, contractFixture{
		openapi: validOpenAPI,
		schema:  schema,
		docs:    validDocs,
		draft:   validDocs,
	})

	findings, err := VerifyFiles(paths.openapi, paths.schema, paths.docs, paths.draft)
	if err != nil {
		t.Fatalf("VerifyFiles returned error: %v", err)
	}

	assertFindingCount(t, findings, CodeSchemaRawCredentialFieldForbidden, 0)
}

func TestVerifyFilesCatchesSchemaEnumGoParityDrift(t *testing.T) {
	driftedSchema := validSchema
	driftedSchema = strings.Replace(driftedSchema, `        "CALLER_NOT_ALLOWED",`+"\n", "", 1)
	driftedSchema = strings.Replace(driftedSchema, `        "operation_inspector",`+"\n", "", 1)
	driftedSchema = strings.Replace(driftedSchema, `        "repo_create",`+"\n", "", 1)

	paths := writeContractFixture(t, contractFixture{
		openapi: validOpenAPI,
		schema:  driftedSchema,
		docs:    validDocs,
		draft:   validDocs,
	})

	findings, err := VerifyFiles(paths.openapi, paths.schema, paths.docs, paths.draft)
	if err != nil {
		t.Fatalf("VerifyFiles returned error: %v", err)
	}

	assertHasFinding(t, findings, CodeSchemaErrorCodeEnumGoDrift)
	assertHasFinding(t, findings, CodeSchemaCallerRoleEnumGoDrift)
	assertHasFinding(t, findings, CodeSchemaOperationTypeEnumGoDrift)
	assertFindingCount(t, findings, CodeSchemaErrorCodeEnumGoDrift, 1)
	assertFindingCount(t, findings, CodeSchemaCallerRoleEnumGoDrift, 1)
	assertFindingCount(t, findings, CodeSchemaOperationTypeEnumGoDrift, 1)
}

func TestVerifyFilesCatchesVolumeHealthFindingCodeEnumDrift(t *testing.T) {
	driftedSchema := strings.Replace(validSchema, `        "BACKEND_PROBE_MISSING",`+"\n", "", 1)
	paths := writeContractFixture(t, contractFixture{
		openapi: validOpenAPI,
		schema:  driftedSchema,
		docs:    validDocs,
		draft:   validDocs,
	})

	findings, err := VerifyFiles(paths.openapi, paths.schema, paths.docs, paths.draft)
	if err != nil {
		t.Fatalf("VerifyFiles returned error: %v", err)
	}

	assertHasFinding(t, findings, "schema.volume_health_finding_code_enum_go_drift")
}

func TestVerifyFilesCatchesVolumeHealthFindingCodeRefInvalid(t *testing.T) {
	driftedSchema := strings.Replace(validSchema, `"code": { "$ref": "#/$defs/VolumeHealthFindingCode" }`, `"code": { "type": "string" }`, 1)
	paths := writeContractFixture(t, contractFixture{
		openapi: validOpenAPI,
		schema:  driftedSchema,
		docs:    validDocs,
		draft:   validDocs,
	})

	findings, err := VerifyFiles(paths.openapi, paths.schema, paths.docs, paths.draft)
	if err != nil {
		t.Fatalf("VerifyFiles returned error: %v", err)
	}

	assertHasFinding(t, findings, CodeSchemaVolumeHealthFindingCodeRefInvalid)
}

func TestVerifyFilesCatchesAllowedCallerRolesUsingGlobalCallerRole(t *testing.T) {
	driftedSchema := strings.Replace(validSchema, `"items": { "$ref": "#/$defs/NamespaceBindingCallerRole" }`, `"items": { "$ref": "#/$defs/CallerRole" }`, 1)
	paths := writeContractFixture(t, contractFixture{
		openapi: validOpenAPI,
		schema:  driftedSchema,
		docs:    validDocs,
		draft:   validDocs,
	})

	findings, err := VerifyFiles(paths.openapi, paths.schema, paths.docs, paths.draft)
	if err != nil {
		t.Fatalf("VerifyFiles returned error: %v", err)
	}

	assertHasFinding(t, findings, CodeSchemaAllowedCallerRoleRefInvalid)
}

func TestVerifyFilesCatchesNamespaceBindingCallerRoleForbiddenRoles(t *testing.T) {
	driftedSchema := strings.Replace(validSchema,
		`"NamespaceBindingCallerRole": {
      "type": "string",
      "enum": [
        "namespace_admin",`,
		`"NamespaceBindingCallerRole": {
      "type": "string",
      "enum": [
        "volume_admin",
        "namespace_admin",`,
		1,
	)
	paths := writeContractFixture(t, contractFixture{
		openapi: validOpenAPI,
		schema:  driftedSchema,
		docs:    validDocs,
		draft:   validDocs,
	})

	findings, err := VerifyFiles(paths.openapi, paths.schema, paths.docs, paths.draft)
	if err != nil {
		t.Fatalf("VerifyFiles returned error: %v", err)
	}

	assertHasFinding(t, findings, CodeSchemaNamespaceBindingCallerRoleForbidden)
	assertHasFinding(t, findings, CodeSchemaNamespaceBindingCallerRoleEnumGoDrift)
}

func TestVerifyFilesCatchesQuotaSchemaSemanticsMissing(t *testing.T) {
	for _, tt := range []struct {
		name                   string
		directoryQuotaProperty string
		quotaDefaultProperty   string
	}{
		{
			name:                   "descriptions missing",
			directoryQuotaProperty: `"directory_quota": { "type": "boolean" }`,
			quotaDefaultProperty:   `"quota_bytes_default": { "type": "integer", "minimum": 0 }`,
		},
		{
			name:                   "integration enables semantics missing",
			directoryQuotaProperty: `"directory_quota": { "type": "boolean", "description": "directory_quota is a selected volume capability for directory quota enforcement; quota_bytes_default remains a policy record and enforcement hook and is not enforced unless this selected volume capability supports directory quota enforcement." }`,
			quotaDefaultProperty:   `"quota_bytes_default": { "type": "integer", "minimum": 0, "description": "quota_bytes_default is a namespace binding policy record and enforcement hook, not enforced unless the selected volume capability directory_quota supports directory quota enforcement." }`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			schema := validSchemaWithQuotaDefs(tt.directoryQuotaProperty, tt.quotaDefaultProperty)
			paths := writeContractFixture(t, contractFixture{
				openapi: validOpenAPI,
				schema:  schema,
				docs:    validDocsWithQuotaSemantics,
				draft:   validDocsWithQuotaSemantics,
			})

			findings, err := VerifyFiles(paths.openapi, paths.schema, paths.docs, paths.draft)
			if err != nil {
				t.Fatalf("VerifyFiles returned error: %v", err)
			}

			assertFindingCount(t, findings, CodeSchemaQuotaSemanticsMissing, 2)
		})
	}
}

func TestVerifyFilesCatchesQuotaEnforcedSchemaField(t *testing.T) {
	schema := validSchemaWithQuotaDefs(
		`"directory_quota": { "type": "boolean", "description": "directory_quota is a selected volume capability for directory quota enforcement; quota_bytes_default remains a policy record and enforcement hook and is not enforced unless this selected volume capability supports directory quota enforcement." },
            "quota_enforced": { "type": "boolean" }`,
		`"quota_bytes_default": { "type": "integer", "minimum": 0, "description": "quota_bytes_default is a namespace binding policy record and enforcement hook, not enforced unless the selected volume capability directory_quota supports directory quota enforcement." }`,
	)
	paths := writeContractFixture(t, contractFixture{
		openapi: validOpenAPI,
		schema:  schema,
		docs:    validDocsWithQuotaSemantics,
		draft:   validDocsWithQuotaSemantics,
	})

	findings, err := VerifyFiles(paths.openapi, paths.schema, paths.docs, paths.draft)
	if err != nil {
		t.Fatalf("VerifyFiles returned error: %v", err)
	}

	assertHasFinding(t, findings, CodeSchemaQuotaEnforcedForbidden)
}

func TestVerifyRouteOperationTypeMappingCatchesMissingMutatingRoute(t *testing.T) {
	routes := []api.RouteMetadata{
		{Method: "POST", Path: "/internal/v1/repos", OperationID: "createRepo", Class: auth.RouteClassNamespaceBound, Mutating: true},
	}

	findings := verifyRouteOperationTypeMapping("routes", "", routes, nil)

	assertHasFinding(t, findings, CodeGoRouteOperationTypeMissing)
}

func TestVerifyRouteOperationTypeMappingCatchesExtraReadOrUnknownRoute(t *testing.T) {
	routes := []api.RouteMetadata{
		{Method: "GET", Path: "/internal/v1/repos", OperationID: "listRepos", Class: auth.RouteClassNamespaceBound, Mutating: false},
		{Method: "POST", Path: "/internal/v1/repos", OperationID: "createRepo", Class: auth.RouteClassNamespaceBound, Mutating: true},
	}
	routeTypes := map[string]operations.OperationType{
		"listRepos":  operations.OperationRepoCreate,
		"unknownOp":  operations.OperationRepoCreate,
		"createRepo": operations.OperationRepoCreate,
	}

	findings := verifyRouteOperationTypeMapping("routes", "", routes, routeTypes)

	assertHasFinding(t, findings, CodeGoRouteOperationTypeNonMutating)
	assertHasFinding(t, findings, CodeGoRouteOperationTypeUnknownRoute)
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
	assertHasFinding(t, findings, CodeSchemaOperationRecordTypeEnumInvalid)
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

func TestVerifyFilesCatchesCoreTestProductSpecificFixtureNames(t *testing.T) {
	root := t.TempDir()
	paths := writeRepoContractFixture(t, root, contractFixture{
		openapi: validOpenAPI,
		schema:  validSchema,
		docs:    validDocs,
		draft:   validDocs,
	})
	writeFile(t, filepath.Join(root, "internal", "operations", "types.go"), `package operations

type OperationEnvelope struct {
	OperationID string `+"`json:\"operation_id\"`"+`
	OperationType string `+"`json:\"operation_type\"`"+`
	OperationState string `+"`json:\"operation_state\"`"+`
}
`)
	writeFile(t, filepath.Join(root, "internal", "api", "operation.go"), `package api

type OperationEnvelope struct {
	OperationID string `+"`json:\"operation_id\"`"+`
	OperationType string `+"`json:\"operation_type\"`"+`
	OperationState string `+"`json:\"operation_state\"`"+`
	Resource any `+"`json:\"resource\"`"+`
	Result any `+"`json:\"result\"`"+`
	Error any `+"`json:\"error\"`"+`
}
`)
	writeFile(t, filepath.Join(root, "internal", "api", "boundary_test.go"), `package api

import _ "github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"

func TestAgentsmithSandboxFixtureNames(t *testing.T) {
	_ = "agentsmith-api"
	_ = "agentsmith-gateway"
	_ = "agentsmith-orchestrator"
	_ = "agentsmith"
	_ = `+"`agentsmith`"+`
	_ = "sandbox-orchestrator"
	_ = "sandbox-manager"
}
`)
	writeFile(t, filepath.Join(root, "test", "README.md"), "legacy path: internal/api/agentsmith_afscp_e2e_test.go\nlegacy type: AgentSmithSandbox\n")

	findings, err := VerifyFiles(paths.openapi, paths.schema, paths.docs, paths.draft)
	if err != nil {
		t.Fatalf("VerifyFiles returned error: %v", err)
	}

	assertHasFinding(t, findings, CodeGoCoreTestProductSpecificFixtureForbidden)
	assertNoFindingMessageContains(t, findings, "github.com/agentsmith-project", CodeGoCoreTestProductSpecificFixtureForbidden)
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

func TestVerifyFilesCatchesDocsRoleMatrixDrift(t *testing.T) {
	docs := `
# Contract

` + "`X-AFSCP-Namespace-Id`" + ` is required for namespace-bound requests.

The flat ` + "`OperationEnvelope`" + ` API response is separate from the durable ` + "`OperationRecord`" + ` boundary.

| Role | Endpoint Groups |
| --- | --- |
| ` + "`repo_admin`" + ` | repo create/get/list |
| ` + "`operator_admin`" + ` | operation inspection and operational repair |
`

	paths := writeContractFixture(t, contractFixture{
		openapi: validOpenAPI,
		schema:  validSchema,
		docs:    docs,
		draft:   docs,
	})

	findings, err := VerifyFiles(paths.openapi, paths.schema, paths.docs, paths.draft)
	if err != nil {
		t.Fatalf("VerifyFiles returned error: %v", err)
	}

	assertHasFinding(t, findings, CodeDocsCallerRoleMissing)
	assertHasFinding(t, findings, CodeDocsOperationInspectorScopeMissing)
	assertHasFinding(t, findings, CodeDocsOperatorAdminScopeMissing)
}

func TestVerifyFilesCatchesDocsQuotaSemanticsMissing(t *testing.T) {
	for _, tt := range []struct {
		name            string
		docs            string
		draft           string
		wantFindingPath func(contractPaths) string
	}{
		{
			name:            "api contract missing",
			docs:            validDocs,
			draft:           validDocsWithQuotaSemantics,
			wantFindingPath: func(paths contractPaths) string { return paths.docs },
		},
		{
			name:            "api draft missing",
			docs:            validDocsWithQuotaSemantics,
			draft:           "# Draft\n\n`quota_bytes_default` is shown near `directory_quota`.\n",
			wantFindingPath: func(paths contractPaths) string { return paths.draft },
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			paths := writeContractFixture(t, contractFixture{
				openapi: validOpenAPI,
				schema:  validSchemaWithQuotaDefs(quotaDirectoryDescription, quotaDefaultDescription),
				docs:    tt.docs,
				draft:   tt.draft,
			})

			findings, err := VerifyFiles(paths.openapi, paths.schema, paths.docs, paths.draft)
			if err != nil {
				t.Fatalf("VerifyFiles returned error: %v", err)
			}

			assertHasFindingInFile(t, findings, CodeDocsQuotaSemanticsMissing, tt.wantFindingPath(paths))
		})
	}
}

func TestVerifyCoreProductDocsCatchesProductSpecificTerms(t *testing.T) {
	root := t.TempDir()
	readmePath := filepath.Join(root, "README.md")
	gatePath := filepath.Join(root, "docs", "DEVELOPMENT_GOVERNANCE.md")
	handoffPath := filepath.Join(root, "docs", "DEVELOPER_HANDOFF.md")
	agentHandoffPath := filepath.Join(root, "docs", "AGENTSMITH_AFSCP_EXTERNAL_HANDOFF.md")
	siblingPath := filepath.Join(root, "docs", "SIBLING_REPO_AFSCP_ADOPTION_RECOMMENDATIONS.md")
	researchPath := filepath.Join(root, "docs", "research", "agentsmith-workspace-storage-technical-design.md")
	localDevPath := filepath.Join(root, "docs", "runbooks", "local-dev-handoff.md")
	writeFile(t, readmePath, "AFSCP core must not bind GA to AgentSmith or Sandbox Manager.\n")
	writeFile(t, gatePath, "Required reviewer: Client Connector Owner. The orchestrator v2 contract is accepted.\n")
	writeFile(t, handoffPath, "External owner review is required.\n")
	writeFile(t, agentHandoffPath, "AgentSmith handoff remains caller-specific.\n")
	writeFile(t, siblingPath, "sandbox-manager adoption remains external.\n")
	writeFile(t, researchPath, "workspace storage and file library research copied from /home/percy/works/mbos-v1/improve-agentsmith-fs.\n")
	writeFile(t, localDevPath, "sandbox manager local handoff for /home/percy/works/mbos-v1/mbos-sandbox-v1.\n")
	writeFile(t, filepath.Join(root, "docs", "adr", "0001-create-afscp.md"), "GitHub org path github.com/agentsmith-project/agentsmith-fs-control-plane is allowed.\n")
	writeFile(t, filepath.Join(root, "docs", "JVS_SMOKE_EVIDENCE_2026-05-05-v0.4.8.md"), "Release: https://github.com/agentsmith-project/jvs/releases/tag/v0.4.8\n")

	findings := verifyCoreProductDocs(root)

	assertHasFindingInFile(t, findings, CodeDocsProductSpecificTermForbidden, readmePath)
	assertHasFindingInFile(t, findings, CodeDocsProductSpecificTermForbidden, gatePath)
	assertHasFindingInFile(t, findings, CodeDocsProductSpecificTermForbidden, handoffPath)
	assertHasFindingInFile(t, findings, CodeDocsProductSpecificTermForbidden, agentHandoffPath)
	assertHasFindingInFile(t, findings, CodeDocsProductSpecificTermForbidden, siblingPath)
	assertHasFindingInFile(t, findings, CodeDocsProductSpecificTermForbidden, researchPath)
	assertHasFindingInFile(t, findings, CodeDocsProductSpecificTermForbidden, localDevPath)
	assertNoFindingMessageContains(t, findings, "github.com/agentsmith-project/agentsmith-fs-control-plane", CodeDocsProductSpecificTermForbidden)
	assertNoFindingMessageContains(t, findings, "github.com/agentsmith-project/jvs", CodeDocsProductSpecificTermForbidden)
	for _, finding := range findings {
		if finding.File == "" {
			t.Fatalf("finding should include file path, got %+v", finding)
		}
		if !strings.Contains(finding.Message, "AgentSmith") &&
			!strings.Contains(finding.Message, "agentsmith") &&
			!strings.Contains(finding.Message, "sandbox manager") &&
			!strings.Contains(finding.Message, "sandbox-manager") &&
			!strings.Contains(finding.Message, "mbos-sandbox") &&
			!strings.Contains(finding.Message, "client connector owner") &&
			!strings.Contains(finding.Message, "external owner review") &&
			!strings.Contains(finding.Message, "orchestrator v2 contract") &&
			!strings.Contains(finding.Message, "file library") &&
			!strings.Contains(finding.Message, "workspace storage") &&
			!strings.Contains(finding.Message, "local sibling repo path") {
			t.Fatalf("finding message should name the product-specific term, got %+v", finding)
		}
	}
}

func TestVerifyCoreProductDocsRejectsExternalAdoptionEvidence(t *testing.T) {
	root := t.TempDir()
	readinessPath := filepath.Join(root, "docs", "READINESS_EVIDENCE.md")
	riskPath := filepath.Join(root, "docs", "RISK_REGISTER.md")
	writeFile(t, readinessPath, "| G-001 | closed | `docs/INTEGRATION_GUIDE.md` |\n")
	writeFile(t, riskPath, "| R-012 | risk | `docs/INTEGRATION_GUIDE.md` |\n")

	findings := verifyCoreProductDocs(root)

	assertFindingCount(t, findings, CodeDocsExternalAdoptionEvidenceForbidden, 2)
	assertHasFindingInFile(t, findings, CodeDocsExternalAdoptionEvidenceForbidden, readinessPath)
	assertHasFindingInFile(t, findings, CodeDocsExternalAdoptionEvidenceForbidden, riskPath)
}

func TestVerifyCoreProductDocsRejectsHumanManagedGAGates(t *testing.T) {
	root := t.TempDir()
	readinessPath := filepath.Join(root, "docs", "READINESS_EVIDENCE.md")
	riskPath := filepath.Join(root, "docs", "RISK_REGISTER.md")
	gaGatesPath := filepath.Join(root, "docs", "GA_RELEASE_GATES.md")
	developmentGovernancePath := filepath.Join(root, "docs", "DEVELOPMENT_GOVERNANCE.md")
	productRequirementsPath := filepath.Join(root, "docs", "PRODUCT_REQUIREMENTS.md")
	mvpPlanPath := filepath.Join(root, "docs", "MVP_PLAN.md")
	writeFile(t, filepath.Join(root, "README.md"), "GA release gate: pass `scripts/verify-ga-release.sh`; its exit code is the GA decision.\n")
	writeFile(t, readinessPath, strings.Join([]string{
		"Final GA acceptance is blocked pending human sign-off.",
		"Release blocker closes only after owner acceptance and security acceptance.",
		"Generated-client acceptance and platform acceptance are required closed conditions.",
		"Deployment drills and runbook drills remain TBD release blockers.",
		"Allowed runtime states: operator_intervention_required, operator_admin, break_glass_admin.",
		"Allowed product semantics: caller approval reference, operation manual, and operator repair.",
	}, "\n"))
	writeFile(t, riskPath, "| Gate | Status |\n| --- | --- |\n| GA release gate | in_review |\n| GA release gate | open |\n| GA release gate | pending |\n")
	writeFile(t, gaGatesPath, strings.Join([]string{
		"The GA release gate requires owner approval.",
		"Manual review, security approval, owner approval, and generated-client approval are not independent GA gate conditions.",
		"Runtime operator controls remain product behavior, not GA release workflow.",
	}, "\n"))
	writeFile(t, developmentGovernancePath, strings.Join([]string{
		"GA release closure requires manual approval.",
		"Owner roles identify who maintains the contract area. They do not add manual GA approval conditions.",
	}, "\n"))
	writeFile(t, productRequirementsPath, strings.Join([]string{
		"GA-blocking residual risk can close through residual-risk acceptance.",
		"Non-waivable GA blockers cannot be bypassed by manual approval or subjective risk exception.",
		"Purge requests include a caller approval reference as runtime safety data.",
	}, "\n"))
	writeFile(t, mvpPlanPath, "GA-blocking risks in `docs/RISK_REGISTER.md` are closed or have approved residual-risk acceptance under `docs/DEVELOPMENT_GOVERNANCE.md`.\n")

	findings := verifyCoreProductDocs(root)

	assertFindingCount(t, findings, CodeDocsHumanGAGateForbidden, 11)
	assertHasFindingInFile(t, findings, CodeDocsHumanGAGateForbidden, readinessPath)
	assertHasFindingInFile(t, findings, CodeDocsHumanGAGateForbidden, riskPath)
	assertHasFindingInFile(t, findings, CodeDocsHumanGAGateForbidden, gaGatesPath)
	assertHasFindingInFile(t, findings, CodeDocsHumanGAGateForbidden, developmentGovernancePath)
	assertHasFindingInFile(t, findings, CodeDocsHumanGAGateForbidden, productRequirementsPath)
	assertHasFindingInFile(t, findings, CodeDocsHumanGAGateForbidden, mvpPlanPath)
	assertNoFindingMessageContains(t, findings, "operator_intervention_required", CodeDocsHumanGAGateForbidden)
	assertNoFindingMessageContains(t, findings, "caller approval reference", CodeDocsHumanGAGateForbidden)
	assertNoFindingMessageContains(t, findings, "operation manual", CodeDocsHumanGAGateForbidden)
	assertNoFindingMessageContains(t, findings, "not independent GA gate conditions", CodeDocsHumanGAGateForbidden)
	assertNoFindingMessageContains(t, findings, "do not add manual GA approval conditions", CodeDocsHumanGAGateForbidden)
	assertNoFindingMessageContains(t, findings, "cannot be bypassed", CodeDocsHumanGAGateForbidden)
}

func TestFindHumanManagedGAGateFindingsAllowsNegatedGovernanceAndRuntimeSafetySemantics(t *testing.T) {
	findings := findHumanManagedGAGateFindings("docs/GA_RELEASE_GATES.md", strings.Join([]string{
		"Manual review, generated-client approval, security approval, and owner approval are not independent GA gate conditions.",
		"Owner roles do not add manual GA approval conditions.",
		"GA-blocking risks cannot be bypassed by manual approval or subjective risk exception.",
		"Final mode requires no open seed gaps.",
		"Runtime operator controls remain product behavior, not GA release workflow.",
		"Allowed product semantics: caller approval reference, purge approval reference, operation manual, and operator repair.",
	}, "\n"))

	if len(findings) > 0 {
		t.Fatalf("expected negated governance and runtime safety semantics to pass, got findings: %+v", findings)
	}
}

func TestFindHumanManagedGAGateFindingsDoesNotLetNoOpenSeedNegateApprovalGate(t *testing.T) {
	findings := findHumanManagedGAGateFindings("docs/GA_RELEASE_GATES.md", "Final mode requires owner/security approval and no open seed gaps.")

	assertHasFinding(t, findings, CodeDocsHumanGAGateForbidden)
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

func TestOperationStateMachineContractCoversEveryOperationType(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	path := filepath.Join(repoRoot, "docs", "contracts", "operation-state-machine-v1.md")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read operation state machine contract: %v", err)
	}
	text := string(body)
	inventory := markdownSection(t, text, "## Operation Type Inventory")
	sideEffect := markdownSection(t, text, "## Side Effect And Replay Boundary")
	terminalDecision := markdownSection(t, text, "## Failed vs Operator Intervention Decision")

	for _, operationType := range operations.OperationTypes() {
		value := "`" + operationType.String() + "`"
		if !strings.Contains(inventory, value) {
			t.Fatalf("operation inventory missing %s", value)
		}
		if !strings.Contains(sideEffect, value) {
			t.Fatalf("side-effect/replay boundary missing %s", value)
		}
		if !strings.Contains(terminalDecision, value) {
			t.Fatalf("terminal decision table missing %s", value)
		}
	}
}

func TestOperationTerminalizationContractRequiresSideEffectReplayAndTerminalDecision(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	path := filepath.Join(repoRoot, "docs", "contracts", "operation-state-machine-v1.md")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read operation state machine contract: %v", err)
	}
	text := string(body)
	for _, heading := range []string{
		"## Operation Type Inventory",
		"## Side Effect And Replay Boundary",
		"## Failed vs Operator Intervention Decision",
	} {
		if !strings.Contains(text, heading) {
			t.Fatalf("operation state machine contract missing heading %q", heading)
		}
	}
	for _, required := range []string{
		"operation_type",
		"side_effect_boundary",
		"idempotent_replay",
		"failed",
		"operator_intervention_required",
		"ambiguous_external_state",
		"capability_disabled_or_unsupported",
		"migration_cutover",
		"recovery-only",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("operation terminalization contract missing vocabulary %q", required)
		}
	}
}

func TestPullRequestTemplateGovernanceGuardCatchesMissingOrIncompleteTemplate(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "missing body",
			body: "",
			want: prTemplateRequiredGovernanceChecklistLabels(),
		},
		{
			name: "partial body",
			body: "Team/reviewer IDs or links: TBD\n\nPrecise test commands: TBD\n",
			want: []string{
				"worker/subagent ownership",
				"TDD red/green evidence",
				"GA release verification",
				"main-agent provenance",
				"risk/gate impact",
				"product-agnostic boundary check",
				"package/module naming review",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := missingPRTemplateGovernanceChecklistItems(tt.body)
			if !sameStrings(got, tt.want) {
				t.Fatalf("missingPRTemplateGovernanceChecklistItems() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestCurrentRepoPullRequestTemplateHasGovernanceEvidenceChecklist(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	path := filepath.Join(repoRoot, ".github", "pull_request_template.md")

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("PR template must exist at %s: %v", path, err)
	}
	if missing := missingPRTemplateGovernanceChecklistItems(string(body)); len(missing) > 0 {
		t.Fatalf("PR template missing governance/evidence checklist item(s): %s", strings.Join(missing, ", "))
	}
}

func TestCurrentRepoCoreTestsDoNotLeakCallerProductFixtureVocabulary(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	globalForbidden := []struct {
		name    string
		pattern *regexp.Regexp
	}{
		{name: "caller role fixture", pattern: regexp.MustCompile(`\bworkspace_owner\b`)},
		{name: "repo kind fixture", pattern: regexp.MustCompile(`\bKind\s*[:=]\s*"workspace"`)},
	}
	boundaryForbidden := []struct {
		name    string
		pattern *regexp.Regexp
	}{
		{name: "product-like repo fixture", pattern: regexp.MustCompile(`\brepo_project\b`)},
		{name: "product-like caller fixture", pattern: regexp.MustCompile(`\bproduct-caller\b`)},
		{name: "product-like mount fixture", pattern: regexp.MustCompile(`/workspace/data-1\b`)},
	}

	var failures []string
	for _, path := range coreTestFixtureGuardPaths(repoRoot) {
		if isCoreTestFixtureGuardSelfTest(repoRoot, path) {
			continue
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile returned error: %v", err)
		}
		for _, fixture := range globalForbidden {
			if fixture.pattern.Match(body) {
				failures = append(failures, filepath.ToSlash(path)+": "+fixture.name)
			}
		}
	}
	for _, path := range currentRepoCoreFixtureBoundaryGuardPaths(repoRoot) {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile returned error: %v", err)
		}
		for _, fixture := range boundaryForbidden {
			if fixture.pattern.Match(body) {
				failures = append(failures, filepath.ToSlash(path)+": "+fixture.name)
			}
		}
	}
	if len(failures) > 0 {
		t.Fatalf("core tests must use generic illegal caller/kind fixtures, got product vocabulary leak(s): %s", strings.Join(failures, "; "))
	}
}

func TestCurrentRepoInternalREADMEImplementationStatusIsCurrent(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	path := filepath.Join(repoRoot, "internal", "README.md")

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("internal README must exist at %s: %v", path, err)
	}

	forbidden := []string{
		"save/restore/template workers and handlers remain absent",
		"Still intentionally absent:\nJVS save/restore/template execution",
		"save/restore and\ntemplate endpoint handlers beyond intake/admission",
	}
	for _, phrase := range forbidden {
		if strings.Contains(string(body), phrase) {
			t.Fatalf("internal README has stale implementation status phrase %q", phrase)
		}
	}
}

func TestCurrentRepoActiveDocsHaveCurrentImplementationStatus(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	paths := []string{
		filepath.Join(repoRoot, "README.md"),
		filepath.Join(repoRoot, "api", "schemas", "README.md"),
		filepath.Join(repoRoot, "api", "openapi", "README.md"),
		filepath.Join(repoRoot, "docs", "API_CONTRACT_DRAFT.md"),
		filepath.Join(repoRoot, "docs", "DEVELOPER_HANDOFF.md"),
		filepath.Join(repoRoot, "docs", "HANDOFF.md"),
		filepath.Join(repoRoot, "docs", "JVS_INTEGRATION.md"),
		filepath.Join(repoRoot, "docs", "REVIEW_CHECKLIST.md"),
		filepath.Join(repoRoot, "docs", "PRE_DEV_COMPLETION.md"),
		filepath.Join(repoRoot, "docs", "runbooks", "ga-runbooks.md"),
		filepath.Join(repoRoot, "docs", "OPERATIONS_AND_MIGRATION.md"),
	}
	contractPaths, err := filepath.Glob(filepath.Join(repoRoot, "docs", "contracts", "*.md"))
	if err != nil {
		t.Fatalf("Glob returned error: %v", err)
	}
	if len(contractPaths) == 0 {
		t.Fatal("expected active contract docs under docs/contracts/*.md")
	}
	paths = append(paths, contractPaths...)
	gateDefinitionPaths := []string{
		filepath.Join(repoRoot, "docs", "GA_RELEASE_GATES.md"),
		filepath.Join(repoRoot, "docs", "DEVELOPMENT_GOVERNANCE.md"),
		filepath.Join(repoRoot, "docs", "PRODUCT_REQUIREMENTS.md"),
	}
	forbidden := []string{
		"GA pre-dev",
		"GA pre-dev narrative draft",
		"GA pre-dev review draft",
		"pre-dev runbook draft for implementation handoff",
		"Service skeleton work may start",
		"Before endpoint handlers depend",
		"before endpoint handlers",
		"before endpoint implementation",
		"Endpoint implementation must wait",
		"before service implementation begins",
		"before service implementation",
		"service implementation begins",
	}
	currentBaselinePhrases := []string{
		"GA implementation-baseline",
		"GA implementation baseline",
		"current implementation baseline",
		"after the implementation baseline",
	}

	for _, path := range paths {
		t.Run(filepath.ToSlash(path), func(t *testing.T) {
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile returned error: %v", err)
			}
			text := string(body)
			normalizedText := strings.Join(strings.Fields(text), " ")
			for _, phrase := range forbidden {
				if strings.Contains(text, phrase) {
					t.Fatalf("%s has stale current implementation status phrase %q", path, phrase)
				}
			}
			if !strings.Contains(text, "docs/READINESS_EVIDENCE.md") {
				t.Fatalf("%s must cite docs/READINESS_EVIDENCE.md for current readiness governance", path)
			}
			if !strings.Contains(text, "scripts/verify-ga-release.sh") {
				t.Fatalf("%s must point GA release decisions at scripts/verify-ga-release.sh", path)
			}
			hasCurrentBaseline := false
			for _, phrase := range currentBaselinePhrases {
				if strings.Contains(normalizedText, phrase) {
					hasCurrentBaseline = true
					break
				}
			}
			if !hasCurrentBaseline {
				t.Fatalf("%s must describe GA implementation-baseline or current implementation baseline status", path)
			}
		})
	}

	for _, path := range gateDefinitionPaths {
		t.Run(filepath.ToSlash(path), func(t *testing.T) {
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile returned error: %v", err)
			}
			text := string(body)
			for _, phrase := range forbidden {
				if strings.Contains(text, phrase) {
					t.Fatalf("%s has stale current implementation status phrase %q", path, phrase)
				}
			}
			if !strings.Contains(text, "scripts/verify-ga-release.sh") {
				t.Fatalf("%s must point GA release decisions at scripts/verify-ga-release.sh", path)
			}
			if filepath.Base(path) == "GA_RELEASE_GATES.md" {
				normalizedText := strings.Join(strings.Fields(text), " ")
				if !strings.Contains(normalizedText, "seed/baseline") ||
					!strings.Contains(normalizedText, "final mode") ||
					!strings.Contains(normalizedText, "no open seed gaps") {
					t.Fatalf("%s must document the seed/final evidence boundary for scripts/verify-ga-release.sh", path)
				}
			}
		})
	}
}

func TestCurrentRepoEntryDocsDocumentSeedFinalEvidenceBoundary(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	for _, path := range []string{
		filepath.Join(repoRoot, "README.md"),
		filepath.Join(repoRoot, "docs", "DEVELOPER_HANDOFF.md"),
	} {
		t.Run(filepath.ToSlash(path), func(t *testing.T) {
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("entry doc must exist at %s: %v", path, err)
			}
			normalizedText := strings.Join(strings.Fields(string(body)), " ")
			for _, required := range []string{
				"seed/baseline",
				"not final GA release acceptance",
				"final mode",
				"no open seed gaps",
				"scripts/verify-ga-release.sh",
			} {
				if !strings.Contains(normalizedText, required) {
					t.Fatalf("%s must document seed/final evidence boundary phrase %q", path, required)
				}
			}
		})
	}
}

func TestCurrentRepoReadinessEvidenceHasCurrentImplementationStatus(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	path := filepath.Join(repoRoot, "docs", "READINESS_EVIDENCE.md")

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readiness evidence must exist at %s: %v", path, err)
	}
	text := string(body)
	normalizedText := strings.Join(strings.Fields(text), " ")

	if !strings.Contains(text, "scripts/verify-ga-release.sh") {
		t.Fatalf("%s must point GA release decisions at scripts/verify-ga-release.sh", path)
	}
	for _, required := range []string{
		"-mode seed",
		"seed/baseline",
		"not final GA release acceptance",
		"final mode",
		"no open seed gaps",
	} {
		if !strings.Contains(normalizedText, required) {
			t.Fatalf("%s must document seed/final evidence boundary phrase %q", path, required)
		}
	}
	forbidden := []string{
		"GA pre-dev",
		"GA pre-dev narrative draft",
		"GA pre-dev review draft",
	}
	for _, phrase := range forbidden {
		if strings.Contains(text, phrase) {
			t.Fatalf("%s has stale readiness status phrase %q", path, phrase)
		}
	}
	boundary := "Reference consumer adoption notes can inform compatibility work, but no first consumer or sibling repository acceptance is an AFSCP gate or release blocker."
	if !strings.Contains(normalizedText, boundary) {
		t.Fatalf("%s must state first/reference consumer adoption is not an AFSCP gate or release blocker", path)
	}
}

func TestCurrentRepoGAVerificationScriptsAreAuthoritative(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	releasePath := filepath.Join(repoRoot, "scripts", "verify-ga-release.sh")
	baselinePath := filepath.Join(repoRoot, "scripts", "verify-ga-baseline.sh")
	readmePath := filepath.Join(repoRoot, "scripts", "README.md")

	releaseBody, err := os.ReadFile(releasePath)
	if err != nil {
		t.Fatalf("authoritative seed/baseline convergence gate must exist at %s: %v", releasePath, err)
	}
	releaseText := string(releaseBody)
	for _, required := range []string{
		"git diff --check",
		"bash -n scripts/verify-ga-release.sh",
		"bash -n scripts/verify-ga-baseline.sh",
		"AFSCP_RELEASE_INTENT",
		"final_candidate",
		"docs/release-evidence/ga-release-selector.json",
		"-selector",
		"go test -count=1 ./internal/contractcheck",
		"bash scripts/verify-ga-baseline.sh",
	} {
		if !strings.Contains(releaseText, required) {
			t.Fatalf("%s must run %q", releasePath, required)
		}
	}
	if !releaseScriptRunsEvidenceManifestVerifier(releaseText) {
		t.Fatalf("%s must run evidence manifest verifier as a non-comment seed/baseline convergence command", releasePath)
	}
	for _, forbidden := range []string{
		"mbos-sandbox",
		"improve-agentsmith",
		"../",
	} {
		if strings.Contains(releaseText, forbidden) {
			t.Fatalf("%s must remain repo-local and not reference sibling project token %q", releasePath, forbidden)
		}
	}
	if _, err := os.Stat(baselinePath); err != nil {
		t.Fatalf("baseline script must remain at %s: %v", baselinePath, err)
	}
	for _, path := range []string{releasePath, baselinePath} {
		cmd := exec.Command("bash", "-n", path)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("bash -n %s failed: %v\n%s", path, err, string(output))
		}
	}
	baselineBody, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatalf("baseline script must be readable at %s: %v", baselinePath, err)
	}
	baselineText := string(baselineBody)
	for _, required := range []string{
		"git diff --check",
		"go test -count=1 ./...",
		"go run ./cmd/afscp-contract-verify",
	} {
		if !strings.Contains(baselineText, required) {
			t.Fatalf("%s must run baseline check %q", baselinePath, required)
		}
	}

	readmeBody, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("scripts README must exist at %s: %v", readmePath, err)
	}
	readmeText := string(readmeBody)
	if !strings.Contains(readmeText, "scripts/verify-ga-release.sh") ||
		!strings.Contains(strings.ToLower(readmeText), "authoritative") ||
		!strings.Contains(readmeText, "-mode seed") ||
		!strings.Contains(readmeText, "seed/baseline") ||
		!strings.Contains(readmeText, "final mode") ||
		!strings.Contains(readmeText, "no open seed gaps") ||
		!strings.Contains(strings.ToLower(readmeText), "release-only governance checks") ||
		!strings.Contains(readmeText, "scripts/verify-ga-baseline.sh") {
		t.Fatalf("%s must document release-only governance checks, baseline checks, and scripts/verify-ga-release.sh as the authoritative seed/baseline convergence gate with a final-mode boundary", readmePath)
	}
}

func TestGAVerificationScriptManifestVerifierGuardIgnoresComments(t *testing.T) {
	commentOnly := `
#!/usr/bin/env bash
# run go run ./cmd/afscp-evidence-verify -mode seed -manifest docs/release-evidence/ga-manifest.json
run echo not-the-verifier
`
	if releaseScriptRunsEvidenceManifestVerifier(commentOnly) {
		t.Fatal("comment-only evidence verifier reference counted as an active convergence gate command")
	}

	active := `
#!/usr/bin/env bash
run go run ./cmd/afscp-evidence-verify -mode "$mode" -manifest docs/release-evidence/ga-manifest.json "${selector_args[@]}"
`
	if !releaseScriptRunsEvidenceManifestVerifier(active) {
		t.Fatal("active evidence verifier command was not recognized")
	}

	suppressed := `
#!/usr/bin/env bash
run go run ./cmd/afscp-evidence-verify -mode seed -manifest docs/release-evidence/ga-manifest.json || true
`
	if releaseScriptRunsEvidenceManifestVerifier(suppressed) {
		t.Fatal("evidence verifier command with failure-swallowing suffix counted as authoritative")
	}
}

func TestReleaseScriptConvergenceSelectorStaysSeed(t *testing.T) {
	root := writeGAReleaseScriptBehaviorFixture(t, "convergence_seed")
	output := runGAReleaseScriptFixture(t, root, nil, true)
	if strings.Contains(output, "-mode final") {
		t.Fatalf("convergence selector must not force final mode, log:\n%s", output)
	}
	if !strings.Contains(output, "-mode seed") {
		t.Fatalf("convergence selector must keep seed mode, log:\n%s", output)
	}
}

func TestReleaseScriptFinalIntentRequiresFinalCandidateSelector(t *testing.T) {
	root := writeGAReleaseScriptBehaviorFixture(t, "convergence_seed")
	output := runGAReleaseScriptFixture(t, root, []string{"AFSCP_RELEASE_INTENT=final_candidate"}, false)
	if !strings.Contains(output, "final_candidate") || !strings.Contains(output, "selector") {
		t.Fatalf("final intent with non-final selector should hard fail with selector/final_candidate message, log:\n%s", output)
	}
	if strings.Contains(output, "-mode final") {
		t.Fatalf("script must fail before invoking final verifier for non-final selector, log:\n%s", output)
	}
}

func releaseScriptRunsEvidenceManifestVerifier(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(trimmed, "run go run ./cmd/afscp-evidence-verify") &&
			strings.Contains(trimmed, "-mode") &&
			(strings.Contains(trimmed, "docs/release-evidence/ga-manifest.json") || strings.Contains(trimmed, "$manifest_path")) &&
			!strings.Contains(trimmed, "|| true") {
			return true
		}
	}
	return false
}

func writeGAReleaseScriptBehaviorFixture(t *testing.T, selectorIntent string) string {
	t.Helper()

	sourcePath := filepath.Join("..", "..", "scripts", "verify-ga-release.sh")
	body, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read %s: %v", sourcePath, err)
	}

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "scripts", "verify-ga-release.sh"), string(body))
	writeFile(t, filepath.Join(root, "scripts", "verify-ga-baseline.sh"), "#!/usr/bin/env bash\nexit 0\n")
	writeFile(t, filepath.Join(root, "docs", "release-evidence", "ga-manifest.json"), "{}\n")
	writeFile(t, filepath.Join(root, "docs", "release-evidence", "ga-release-selector.json"), `{"release_intent":"`+selectorIntent+`"}`+"\n")
	binDir := filepath.Join(root, "bin")
	writeFile(t, filepath.Join(binDir, "git"), "#!/usr/bin/env bash\nprintf 'git %s\\n' \"$*\" >> \"$GA_SCRIPT_LOG\"\nexit 0\n")
	writeFile(t, filepath.Join(binDir, "go"), `#!/usr/bin/env bash
printf 'go %s\n' "$*" >> "$GA_SCRIPT_LOG"
if [[ "$*" == *"-selector-intent"* ]]; then
  printf '%s\n' "$FAKE_SELECTOR_INTENT"
fi
exit 0
`)
	if err := os.Chmod(filepath.Join(root, "scripts", "verify-ga-release.sh"), 0o700); err != nil {
		t.Fatalf("chmod release script: %v", err)
	}
	if err := os.Chmod(filepath.Join(root, "scripts", "verify-ga-baseline.sh"), 0o700); err != nil {
		t.Fatalf("chmod baseline script: %v", err)
	}
	if err := os.Chmod(filepath.Join(binDir, "git"), 0o700); err != nil {
		t.Fatalf("chmod fake git: %v", err)
	}
	if err := os.Chmod(filepath.Join(binDir, "go"), 0o700); err != nil {
		t.Fatalf("chmod fake go: %v", err)
	}
	return root
}

func runGAReleaseScriptFixture(t *testing.T, root string, env []string, wantSuccess bool) string {
	t.Helper()

	logPath := filepath.Join(root, "ga-script.log")
	cmd := exec.Command("bash", "scripts/verify-ga-release.sh")
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"PATH="+filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GA_SCRIPT_LOG="+logPath,
		"FAKE_SELECTOR_INTENT=convergence_seed",
	)
	cmd.Env = append(cmd.Env, env...)
	output, err := cmd.CombinedOutput()
	logBody, _ := os.ReadFile(logPath)
	combined := string(output) + string(logBody)
	if wantSuccess && err != nil {
		t.Fatalf("release script fixture failed: %v\n%s", err, combined)
	}
	if !wantSuccess && err == nil {
		t.Fatalf("release script fixture unexpectedly succeeded:\n%s", combined)
	}
	return combined
}

func TestCurrentRepoGAReleaseWorkflowRunsAuthoritativeScript(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	workflowPath := filepath.Join(repoRoot, ".github", "workflows", "ga-release.yml")

	body, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("repo-local seed/baseline convergence workflow must exist at %s: %v", workflowPath, err)
	}
	text := string(body)
	for _, required := range []string{
		"Seed/Baseline Convergence Gate",
		"actions/checkout",
		"actions/setup-go",
		"Run seed/baseline convergence gate",
		"bash scripts/verify-ga-release.sh",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("%s must include %q", workflowPath, required)
		}
	}
}

func TestCurrentRepoEntryDocsHaveQuotaSemantics(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	paths := []string{
		filepath.Join(repoRoot, "docs", "PRODUCT_REQUIREMENTS.md"),
		filepath.Join(repoRoot, "docs", "GA_PRE_DEV_READINESS.md"),
		filepath.Join(repoRoot, "docs", "PRODUCT_BOUNDARY.md"),
		filepath.Join(repoRoot, "docs", "RISK_REGISTER.md"),
	}

	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile returned error: %v", err)
			}
			text := string(body)
			if !hasQuotaSemantics(text) {
				t.Fatalf("%s must state quota_bytes_default/directory_quota policy record, enforcement hook, not enforced, and integration enables semantics", path)
			}
			if !strings.Contains(text, "corresponding volume integration explicitly enables directory quota enforcement") {
				t.Fatalf("%s must require the corresponding volume integration to explicitly enable directory quota enforcement", path)
			}
		})
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

func assertHasFindingInFile(t *testing.T, findings []Finding, code, file string) {
	t.Helper()

	for _, finding := range findings {
		if finding.Code == code && finding.File == file {
			return
		}
	}
	t.Fatalf("expected finding code %q in file %q in %+v", code, file, findings)
}

func assertFindingCount(t *testing.T, findings []Finding, code string, want int) {
	t.Helper()

	count := 0
	for _, finding := range findings {
		if finding.Code == code {
			count++
		}
	}
	if count != want {
		t.Fatalf("expected finding code %q count %d, got %d in %+v", code, want, count, findings)
	}
}

func assertNoFindingMessageContains(t *testing.T, findings []Finding, needle, code string) {
	t.Helper()

	for _, finding := range findings {
		if finding.Code == code && contains(finding.Message, needle) {
			t.Fatalf("did not expect finding code %q mentioning %q: %+v", code, needle, finding)
		}
	}
}

func missingPRTemplateGovernanceChecklistItems(body string) []string {
	type requiredItem struct {
		label   string
		pattern *regexp.Regexp
	}
	items := []requiredItem{
		{
			label:   "team/reviewer IDs or links",
			pattern: regexp.MustCompile(`(?is)team.*/.*reviewer.*ids?.*links?|reviewer.*ids?.*links?`),
		},
		{
			label:   "worker/subagent ownership",
			pattern: regexp.MustCompile(`(?is)worker.*/.*subagent.*ownership|subagent.*ownership|worker.*ownership`),
		},
		{
			label:   "TDD red/green evidence",
			pattern: regexp.MustCompile(`(?is)tdd.*red.*green|red.*green.*tdd`),
		},
		{
			label:   "precise test commands",
			pattern: regexp.MustCompile(`(?is)precise.*test.*commands|test.*commands.*precise`),
		},
		{
			label:   "GA release verification",
			pattern: regexp.MustCompile(`(?is)scripts/verify-ga-release\.sh`),
		},
		{
			label:   "main-agent provenance",
			pattern: regexp.MustCompile(`(?is)main agent.*did not directly write.*code.*/.*docs|main agent.*did not directly write.*docs.*/.*code`),
		},
		{
			label:   "risk/gate impact",
			pattern: regexp.MustCompile(`(?is)risk.*/.*gate.*impact|gate.*/.*risk.*impact`),
		},
		{
			label:   "product-agnostic boundary check",
			pattern: regexp.MustCompile(`(?is)product-agnostic.*boundary.*check|product agnostic.*boundary.*check`),
		},
		{
			label:   "package/module naming review",
			pattern: regexp.MustCompile(`(?is)package.*/.*module.*naming.*review|package.*name.*review|module.*name.*review`),
		},
	}

	var missing []string
	for _, item := range items {
		if !item.pattern.MatchString(body) {
			missing = append(missing, item.label)
		}
	}
	return missing
}

func prTemplateRequiredGovernanceChecklistLabels() []string {
	return []string{
		"team/reviewer IDs or links",
		"worker/subagent ownership",
		"TDD red/green evidence",
		"precise test commands",
		"GA release verification",
		"main-agent provenance",
		"risk/gate impact",
		"product-agnostic boundary check",
		"package/module naming review",
	}
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func currentRepoCoreFixtureBoundaryGuardPaths(repoRoot string) []string {
	relPaths := []string{
		"internal/api/operation_test.go",
		"internal/operations/types_test.go",
		"internal/operations/idempotency_test.go",
		"internal/store/contracts_test.go",
		"internal/pathresolver/pathresolver_test.go",
		"internal/pathresolver/testcorpus/corpus.go",
		"internal/workloadmount/workloadmount_test.go",
	}

	paths := make([]string, 0, len(relPaths))
	for _, rel := range relPaths {
		paths = append(paths, filepath.Join(repoRoot, filepath.FromSlash(rel)))
	}
	return paths
}

func contains(s, needle string) bool {
	for i := 0; i+len(needle) <= len(s); i++ {
		if s[i:i+len(needle)] == needle {
			return true
		}
	}
	return needle == ""
}

func markdownSection(t *testing.T, text, heading string) string {
	t.Helper()
	start := strings.Index(text, heading)
	if start < 0 {
		t.Fatalf("missing markdown section %q", heading)
	}
	rest := text[start+len(heading):]
	next := strings.Index(rest, "\n## ")
	if next >= 0 {
		rest = rest[:next]
	}
	return rest
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

func readRepoFileForContractTest(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRootForContractTest(t), path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

func requireContractPhrases(t *testing.T, body string, phrases ...string) {
	t.Helper()
	for _, phrase := range phrases {
		if !strings.Contains(body, phrase) {
			t.Fatalf("contract missing phrase %q", phrase)
		}
	}
}

func repoRootForContractTest(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd
		}
		next := filepath.Dir(wd)
		if next == wd {
			t.Fatal("repo root with go.mod not found")
		}
		wd = next
	}
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

func validSchemaWithQuotaDefs(directoryQuotaProperty, quotaDefaultProperty string) string {
	return strings.Replace(validSchema, `
    "AllowedCaller": {
      "type": "object",
      "additionalProperties": false,
      "required": ["caller_service", "roles"],
      "properties": {
        "caller_service": { "type": "string" },
        "roles": {
          "type": "array",
          "minItems": 1,
          "uniqueItems": true,
          "items": { "$ref": "#/$defs/NamespaceBindingCallerRole" }
        }
      }
    }
`, `
    "AllowedCaller": {
      "type": "object",
      "additionalProperties": false,
      "required": ["caller_service", "roles"],
      "properties": {
        "caller_service": { "type": "string" },
        "roles": {
          "type": "array",
          "minItems": 1,
          "uniqueItems": true,
          "items": { "$ref": "#/$defs/NamespaceBindingCallerRole" }
        }
      }
    },
    "Volume": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "capabilities": {
          "type": "object",
          "additionalProperties": false,
          "properties": {
            `+directoryQuotaProperty+`
          }
        }
      }
    },
    "NamespaceVolumeBinding": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        `+quotaDefaultProperty+`
      }
    }
`, 1)
}

var validOpenAPI = validRouteParityOpenAPI()

func validRouteParityOpenAPI() string {
	var builder strings.Builder
	builder.WriteString(`
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
  schemas:
    OperationEnvelope:
      type: object
    ExportCreateOperationEnvelope:
      type: object
paths:
`)
	for _, route := range api.InternalV1RouteMetadata() {
		builder.WriteString("  ")
		builder.WriteString(route.Path)
		builder.WriteString(":\n    ")
		builder.WriteString(strings.ToLower(route.Method))
		builder.WriteString(":\n      operationId: ")
		builder.WriteString(route.OperationID)
		builder.WriteString("\n      parameters:\n")
		if isMutatingMethod(route.Method) {
			builder.WriteString(`        - $ref: "#/components/parameters/IdempotencyKey"
`)
		}
		builder.WriteString(`        - $ref: "#/components/parameters/CorrelationId"
        - $ref: "#/components/parameters/CallerService"
`)
		if isNamespaceBoundOperation(openAPIOperation{OperationID: route.OperationID}) {
			builder.WriteString(`        - $ref: "#/components/parameters/NamespaceId"
`)
		}
		if isMutatingMethod(route.Method) {
			builder.WriteString(`        - $ref: "#/components/parameters/ActorType"
        - $ref: "#/components/parameters/ActorId"
`)
		}
		if route.OperationID == "createExport" {
			builder.WriteString(`      responses:
        "202":
          description: accepted
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/ExportCreateOperationEnvelope"
`)
		}
	}
	return builder.String()
}

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
    },
    "OperationType": {
      "type": "string",
      "enum": [
        "volume_ensure",
        "namespace_upsert",
        "namespace_disable",
        "namespace_volume_binding_put",
        "repo_create",
        "repo_archive",
        "repo_restore_archived",
        "repo_delete",
        "repo_restore_tombstoned",
        "repo_purge",
        "save_point_create",
        "restore_preview",
        "restore_preview_discard",
        "restore_run",
        "template_create",
        "template_clone",
        "export_create",
        "export_revoke",
        "export_session_reconcile",
        "mount_binding_create",
        "mount_binding_status_update",
        "mount_binding_heartbeat",
        "mount_binding_release",
        "mount_binding_revoke",
        "migration_cutover"
      ]
    },
    "VolumeHealthFindingCode": {
      "type": "string",
      "enum": [
        "VOLUME_DISABLED",
        "VOLUME_DEGRADED",
        "CAPABILITY_NOT_READY",
        "BACKEND_PROBE_MISSING",
        "BACKEND_PROBE_FAILED",
        "BACKEND_PROBE_ERROR"
      ]
    },
    "VolumeHealth": {
      "type": "object",
      "additionalProperties": false,
      "required": ["volume_id", "status", "checked_at", "findings"],
      "properties": {
        "volume_id": { "type": "string" },
        "status": { "type": "string", "enum": ["healthy", "degraded", "unavailable"] },
        "checked_at": { "type": "string" },
        "findings": {
          "type": "array",
          "items": {
            "type": "object",
            "additionalProperties": false,
            "required": ["code", "message", "severity"],
            "properties": {
              "code": { "$ref": "#/$defs/VolumeHealthFindingCode" },
              "message": { "type": "string" },
              "severity": { "type": "string", "enum": ["info", "warning", "critical"] }
            }
          }
        }
      }
    },
    "ErrorCode": {
      "type": "string",
      "enum": [
        "AUTHENTICATION_FAILED",
        "CALLER_NOT_ALLOWED",
        "ROLE_NOT_ALLOWED",
        "NAMESPACE_NOT_FOUND",
        "NAMESPACE_DISABLED",
        "RESOURCE_NAMESPACE_MISMATCH",
        "INVALID_ID",
        "PATH_DENIED",
        "CAPABILITY_DENIED",
        "IDEMPOTENCY_CONFLICT",
        "REPO_ALREADY_EXISTS",
        "REPO_NOT_FOUND",
        "VOLUME_NOT_FOUND",
        "OPERATION_NOT_FOUND",
        "ACTIVE_WRITER_SESSIONS",
        "WRITER_SESSION_FENCE_HELD",
        "STALE_WRITER_SESSION_UNCERTAIN",
        "RESTORE_DIRTY_STATE",
        "JVS_COMMAND_FAILED",
        "JVS_DOCTOR_FAILED",
        "SOURCE_DIRTY_AFTER_TEMPLATE_SAVE",
        "VOLUME_MISMATCH_REQUIRES_IMPORT",
        "EXPORT_EXPIRED",
        "EXPORT_REVOKED",
        "MOUNT_BINDING_TERMINAL",
        "REPO_LIFECYCLE_INVALID_STATE",
        "REPO_LIFECYCLE_FENCE_HELD",
        "ACTIVE_SESSIONS_BLOCK_LIFECYCLE",
        "STALE_SESSION_BLOCKS_LIFECYCLE",
        "REPO_ARCHIVED",
        "REPO_TOMBSTONED",
        "REPO_PURGED",
        "PURGE_CONFIRMATION_REQUIRED",
        "PURGE_RETENTION_NOT_MET",
        "PURGE_REQUIRES_OPERATOR_APPROVAL",
        "OPERATION_RECOVERY_REQUIRED"
      ]
    },
    "CallerRole": {
      "type": "string",
      "enum": [
        "volume_admin",
        "namespace_admin",
        "repo_admin",
        "repo_lifecycle_admin",
        "restore_admin",
        "template_admin",
        "export_admin",
        "mount_admin",
        "operation_inspector",
        "orchestrator_mount",
        "migration_admin",
        "operator_admin",
        "break_glass_admin"
      ]
    },
    "NamespaceBindingCallerRole": {
      "type": "string",
      "enum": [
        "namespace_admin",
        "repo_admin",
        "repo_lifecycle_admin",
        "restore_admin",
        "template_admin",
        "export_admin",
        "mount_admin",
        "operation_inspector",
        "orchestrator_mount",
        "migration_admin"
      ]
    },
    "AllowedCaller": {
      "type": "object",
      "additionalProperties": false,
      "required": ["caller_service", "roles"],
      "properties": {
        "caller_service": { "type": "string" },
        "roles": {
          "type": "array",
          "minItems": 1,
          "uniqueItems": true,
          "items": { "$ref": "#/$defs/NamespaceBindingCallerRole" }
        }
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

## GA Role Matrix

| Role | Endpoint Groups |
| --- | --- |
| ` + "`volume_admin`" + ` | volume ensure/health |
| ` + "`namespace_admin`" + ` | namespace create/disable and volume binding update |
| ` + "`repo_admin`" + ` | repo create/get/list |
| ` + "`repo_lifecycle_admin`" + ` | repo lifecycle |
| ` + "`restore_admin`" + ` | restore preview/run |
| ` + "`template_admin`" + ` | repo template create/clone |
| ` + "`export_admin`" + ` | export create/get/revoke |
| ` + "`mount_admin`" + ` | workload mount binding create/get/revoke |
| ` + "`operation_inspector`" + ` | namespace-scoped operation inspection of redacted records |
| ` + "`orchestrator_mount`" + ` | orchestrator plan and mount status |
| ` + "`migration_admin`" + ` | migration tooling |
| ` + "`operator_admin`" + ` | global/operator inspection and repair |
| ` + "`break_glass_admin`" + ` | approved break-glass flows |
`

const quotaDirectoryDescription = `"directory_quota": { "type": "boolean", "description": "directory_quota is a selected volume capability for directory quota enforcement; quota_bytes_default remains a policy record and enforcement hook and is not enforced unless this selected volume capability supports directory quota enforcement and the volume integration explicitly enables it." }`

const quotaDefaultDescription = `"quota_bytes_default": { "type": "integer", "minimum": 0, "description": "quota_bytes_default is a namespace binding policy record and enforcement hook, not enforced unless the selected volume capability directory_quota supports directory quota enforcement and the volume integration explicitly enables it." }`

const validDocsWithQuotaSemantics = validDocs + `

## Quota Semantics

` + "`quota_bytes_default`" + ` is a policy record and enforcement hook, not enforced unless the selected volume capability ` + "`directory_quota`" + ` supports directory quota enforcement and the volume integration explicitly enables it.
`
