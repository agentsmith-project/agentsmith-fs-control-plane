package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/api"
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

var cliValidOpenAPI = cliValidRouteParityOpenAPI()

func cliValidRouteParityOpenAPI() string {
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
		if cliIsMutatingMethod(route.Method) {
			builder.WriteString(`        - $ref: "#/components/parameters/IdempotencyKey"
`)
		}
		builder.WriteString(`        - $ref: "#/components/parameters/CorrelationId"
        - $ref: "#/components/parameters/CallerService"
`)
		if cliIsNamespaceBoundOperation(route.OperationID) {
			builder.WriteString(`        - $ref: "#/components/parameters/NamespaceId"
`)
		}
		if cliIsMutatingMethod(route.Method) {
			builder.WriteString(`        - $ref: "#/components/parameters/ActorType"
        - $ref: "#/components/parameters/ActorId"
`)
		}
	}
	return builder.String()
}

func cliIsMutatingMethod(method string) bool {
	switch strings.ToLower(method) {
	case "post", "put", "patch", "delete":
		return true
	default:
		return false
	}
}

func cliIsNamespaceBoundOperation(operationID string) bool {
	switch operationID {
	case "ensureVolume", "getVolumeHealth", "getOperation":
		return false
	default:
		return true
	}
}

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
        "STORAGE_UNAVAILABLE",
        "INTERNAL_ERROR",
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

const cliValidDocs = `
# Contract

` + "`X-AFSCP-Namespace-Id`" + ` is required for namespace-bound requests.

The flat ` + "`OperationEnvelope`" + ` API response is separate from the durable ` + "`OperationRecord`" + ` boundary.

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
