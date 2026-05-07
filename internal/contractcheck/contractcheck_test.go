package contractcheck

import (
	"os"
	"path/filepath"
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
	writeFile(t, readmePath, "AFSCP core must not bind GA to AgentSmith or Sandbox Manager.\n")
	writeFile(t, gatePath, "Required reviewer: Client Connector Owner. The orchestrator v2 contract is accepted.\n")
	writeFile(t, handoffPath, "External owner review is required.\n")
	writeFile(t, filepath.Join(root, "docs", "AGENTSMITH_AFSCP_EXTERNAL_HANDOFF.md"), "AgentSmith handoff remains caller-specific.\n")
	writeFile(t, filepath.Join(root, "docs", "SIBLING_REPO_AFSCP_ADOPTION_RECOMMENDATIONS.md"), "sandbox-manager adoption remains external.\n")
	writeFile(t, filepath.Join(root, "docs", "adr", "0001-create-afscp.md"), "GitHub org path github.com/agentsmith-project/agentsmith-fs-control-plane is allowed.\n")

	findings := verifyCoreProductDocs(root)

	assertFindingCount(t, findings, CodeDocsProductSpecificTermForbidden, 5)
	assertHasFindingInFile(t, findings, CodeDocsProductSpecificTermForbidden, readmePath)
	assertHasFindingInFile(t, findings, CodeDocsProductSpecificTermForbidden, gatePath)
	assertHasFindingInFile(t, findings, CodeDocsProductSpecificTermForbidden, handoffPath)
	for _, finding := range findings {
		if finding.File == "" {
			t.Fatalf("finding should include file path, got %+v", finding)
		}
		if !strings.Contains(finding.Message, "AgentSmith") &&
			!strings.Contains(finding.Message, "sandbox manager") &&
			!strings.Contains(finding.Message, "client connector owner") &&
			!strings.Contains(finding.Message, "external owner review") &&
			!strings.Contains(finding.Message, "orchestrator v2 contract") {
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
