package contractcheck

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/api"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

const (
	CodeOpenAPINamespaceParameterInvalid         = "openapi.namespace_id_parameter_invalid"
	CodeOpenAPINamespaceParameterMissing         = "openapi.namespace_id_parameter_missing"
	CodeOpenAPIMutatingHeaderMissing             = "openapi.mutating_header_missing"
	CodeOpenAPIOperationsMissing                 = "openapi.operations_missing"
	CodeOpenAPIRawDirectMountAccessForbidden     = "openapi.raw_direct_mount_access_forbidden"
	CodeOpenAPIRouteOperationExtra               = "openapi.route_operation_extra"
	CodeOpenAPIRouteOperationMissing             = "openapi.route_operation_missing"
	CodeOpenAPIRouteOperationIDMismatch          = "openapi.route_operation_id_mismatch"
	CodeOpenAPISchemaRawCredentialFieldForbidden = "openapi.schema_raw_credential_field_forbidden"

	CodeSchemaExportSessionRequiredMissing                 = "schema.export_session_required_missing"
	CodeSchemaExportSessionPropertyMissing                 = "schema.export_session_property_missing"
	CodeSchemaExportSessionAdditionalPropertiesInvalid     = "schema.export_session_additional_properties_invalid"
	CodeSchemaOperationEnvelopeRequiredMissing             = "schema.operation_envelope_required_missing"
	CodeSchemaOperationEnvelopePropertyMissing             = "schema.operation_envelope_property_missing"
	CodeSchemaOperationEnvelopeAdditionalPropertiesInvalid = "schema.operation_envelope_additional_properties_invalid"
	CodeSchemaOperationEnvelopeNestedOperation             = "schema.operation_envelope_nested_operation"
	CodeSchemaOperationRecordRequiredMissing               = "schema.operation_record_required_missing"
	CodeSchemaOperationRecordPropertyMissing               = "schema.operation_record_property_missing"
	CodeSchemaOperationRecordAdditionalPropertiesInvalid   = "schema.operation_record_additional_properties_invalid"
	CodeSchemaOperationRecordNullableInvalid               = "schema.operation_record_nullable_invalid"
	CodeSchemaOperationRecordTypeEnumInvalid               = "schema.operation_record_type_enum_invalid"
	CodeSchemaErrorCodeEnumGoDrift                         = "schema.error_code_enum_go_drift"
	CodeSchemaCallerRoleEnumGoDrift                        = "schema.caller_role_enum_go_drift"
	CodeSchemaNamespaceBindingCallerRoleEnumGoDrift        = "schema.namespace_binding_caller_role_enum_go_drift"
	CodeSchemaNamespaceBindingCallerRoleForbidden          = "schema.namespace_binding_caller_role_forbidden"
	CodeSchemaAllowedCallerRoleRefInvalid                  = "schema.allowed_caller_role_ref_invalid"
	CodeSchemaOperationTypeEnumGoDrift                     = "schema.operation_type_enum_go_drift"
	CodeSchemaInvalidJSON                                  = "schema.invalid_json"
	CodeSchemaQuotaSemanticsMissing                        = "schema.quota_semantics_missing"
	CodeSchemaQuotaEnforcedForbidden                       = "schema.quota_enforced_forbidden"
	CodeSchemaRawCredentialFieldForbidden                  = "schema.raw_credential_field_forbidden"

	CodeDocsOperationBoundaryMissing          = "docs.operation_boundary_missing"
	CodeDocsNamespaceHeaderMissing            = "docs.namespace_header_missing"
	CodeDocsCallerRoleMissing                 = "docs.caller_role_missing"
	CodeDocsOperationInspectorScopeMissing    = "docs.operation_inspector_scope_missing"
	CodeDocsOperatorAdminScopeMissing         = "docs.operator_admin_scope_missing"
	CodeDocsProductSpecificTermForbidden      = "docs.product_specific_term_forbidden"
	CodeDocsQuotaSemanticsMissing             = "docs.quota_semantics_missing"
	CodeDocsExternalAdoptionEvidenceForbidden = "docs.external_adoption_evidence_forbidden"

	CodeGoOperationsOperationEnvelopeAmbiguous    = "go.operations_operation_envelope_ambiguous"
	CodeGoAPIOperationEnvelopeMissing             = "go.api_operation_envelope_missing"
	CodeGoAPIOperationEnvelopePropertyMissing     = "go.api_operation_envelope_property_missing"
	CodeGoAPIOperationEnvelopeNestedOperation     = "go.api_operation_envelope_nested_operation"
	CodeGoRouteOperationTypeMissing               = "go.route_operation_type_missing"
	CodeGoRouteOperationTypeUnknownRoute          = "go.route_operation_type_unknown_route"
	CodeGoRouteOperationTypeNonMutating           = "go.route_operation_type_non_mutating"
	CodeGoCoreTestProductSpecificFixtureForbidden = "go.core_test_product_specific_fixture_forbidden"
)

// Finding is a machine-readable contract verifier finding.
type Finding struct {
	Code    string
	File    string
	Line    int
	Message string
}

func (f Finding) String() string {
	location := f.File
	if f.Line > 0 {
		location = fmt.Sprintf("%s:%d", location, f.Line)
	}
	if location == "" {
		location = "contract"
	}
	return fmt.Sprintf("%s: %s: %s", location, f.Code, f.Message)
}

// VerifyFiles checks the AFSCP contract files without using non-standard
// parsers. Read failures are returned as errors; contract mismatches are
// returned as findings.
func VerifyFiles(openAPIPath, schemaPath, apiContractPath, apiDraftPath string) ([]Finding, error) {
	openAPI, err := readContractFile("openapi", openAPIPath)
	if err != nil {
		return nil, err
	}
	schema, err := readContractFile("schema", schemaPath)
	if err != nil {
		return nil, err
	}
	apiContract, err := readContractFile("api contract", apiContractPath)
	if err != nil {
		return nil, err
	}
	apiDraft, err := readContractFile("api draft", apiDraftPath)
	if err != nil {
		return nil, err
	}

	var findings []Finding
	findings = append(findings, verifyOpenAPI(openAPIPath, string(openAPI))...)
	findings = append(findings, verifySchema(schemaPath, string(schema))...)
	findings = append(findings, verifyRouteOperationTypeMapping(openAPIPath, string(openAPI), api.InternalV1RouteMetadata(), operations.RouteOperationTypes())...)
	findings = append(findings, verifyDocs(apiContractPath, apiDraftPath, string(apiContract), string(apiDraft))...)
	if repoRoot, ok := findRepoRoot(schemaPath); ok {
		findings = append(findings, verifyGoDTOBoundary(repoRoot)...)
		findings = append(findings, verifyCoreProductDocs(repoRoot)...)
		findings = append(findings, verifyCoreTestFixtureNames(repoRoot)...)
	}
	return findings, nil
}

func readContractFile(label, path string) ([]byte, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s %q: %w", label, path, err)
	}
	return body, nil
}

func verifyOpenAPI(path, body string) []Finding {
	var findings []Finding

	lines := splitLines(body)
	namespaceBlock, namespaceLine, ok := findYAMLBlock(lines, "NamespaceId", 4)
	if !ok || !hasYAMLScalar(namespaceBlock, "name", "X-AFSCP-Namespace-Id") || !hasYAMLScalar(namespaceBlock, "in", "header") {
		findings = append(findings, Finding{
			Code:    CodeOpenAPINamespaceParameterInvalid,
			File:    path,
			Line:    namespaceLine,
			Message: "components.parameters.NamespaceId must be a reusable header named X-AFSCP-Namespace-Id",
		})
	}

	operations := parseOpenAPIOperations(body)
	if len(operations) == 0 {
		findings = append(findings, Finding{
			Code:    CodeOpenAPIOperationsMissing,
			File:    path,
			Line:    findLine(body, "paths:"),
			Message: "OpenAPI paths must contain at least one operation",
		})
	}

	for _, op := range operations {
		if tokens := forbiddenOpenAPIRawDirectMountTokens(op.Path, op.OperationID); len(tokens) > 0 {
			findings = append(findings, Finding{
				Code:    CodeOpenAPIRawDirectMountAccessForbidden,
				File:    path,
				Line:    op.Line,
				Message: fmt.Sprintf("ordinary/internal v1 OpenAPI must not expose raw/direct/break-glass mount access; %s %s operation %q contains forbidden token(s): %s", strings.ToUpper(op.Method), op.Path, op.operationName(), strings.Join(tokens, ", ")),
			})
		}

		if isNamespaceBoundOperation(op) && !hasParameterRef(op.Body, "NamespaceId") {
			findings = append(findings, Finding{
				Code:    CodeOpenAPINamespaceParameterMissing,
				File:    path,
				Line:    op.Line,
				Message: fmt.Sprintf("%s %s operation %q must include #/components/parameters/NamespaceId", strings.ToUpper(op.Method), op.Path, op.operationName()),
			})
		}

		if isMutatingMethod(op.Method) {
			var missing []string
			for _, header := range []string{"IdempotencyKey", "ActorType", "ActorId", "CorrelationId", "CallerService"} {
				if !hasParameterRef(op.Body, header) {
					missing = append(missing, header)
				}
			}
			if len(missing) > 0 {
				findings = append(findings, Finding{
					Code:    CodeOpenAPIMutatingHeaderMissing,
					File:    path,
					Line:    op.Line,
					Message: fmt.Sprintf("%s %s operation %q is missing mutating request parameter(s): %s", strings.ToUpper(op.Method), op.Path, op.operationName(), strings.Join(missing, ", ")),
				})
			}
		}
	}

	findings = append(findings, verifyOpenAPIRouteParity(path, body, operations)...)
	findings = append(findings, verifyOpenAPISchemaRawCredentialFields(path, body)...)

	return findings
}

func forbiddenOpenAPIRawDirectMountTokens(values ...string) []string {
	delimitedForbidden := []string{"direct", "raw", "juicefs", "break-glass", "mount-command"}
	compactForbidden := []string{"rawmountcommand", "directmount", "breakglass", "mountcommand", "juicefs"}
	seen := make(map[string]bool)
	var found []string
	for _, value := range values {
		forms := []string{
			normalizeForbiddenOpenAPITokenText(value, "-"),
			normalizeForbiddenOpenAPITokenText(value, "_"),
		}
		foundDelimited := false
		for _, token := range delimitedForbidden {
			for _, form := range forms {
				if !containsDelimitedToken(form, token) {
					continue
				}
				if !seen[token] {
					seen[token] = true
					found = append(found, token)
				}
				foundDelimited = true
				break
			}
		}
		if foundDelimited {
			continue
		}
		for _, token := range compactForbidden {
			if !containsCompactForbiddenToken(value, token) {
				continue
			}
			if !seen[token] {
				seen[token] = true
				found = append(found, token)
			}
		}
	}
	sort.Strings(found)
	return found
}

func normalizeForbiddenOpenAPITokenText(value, separator string) string {
	var builder strings.Builder
	var previous byte
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if ch >= 'A' && ch <= 'Z' {
			if i > 0 && isTokenWordByte(previous) && previous != '-' && previous != '_' && previous != '/' && previous != ':' {
				builder.WriteString(separator)
			}
			ch += 'a' - 'A'
		}
		if isTokenWordByte(ch) {
			builder.WriteByte(ch)
		} else {
			builder.WriteString(separator)
		}
		previous = value[i]
	}
	return builder.String()
}

func containsDelimitedToken(value, token string) bool {
	token = strings.ReplaceAll(strings.ToLower(token), "_", "-")
	value = strings.ReplaceAll(strings.ToLower(value), "_", "-")
	for _, part := range strings.FieldsFunc(value, func(r rune) bool { return r == '-' || r == '/' || r == ':' || r == '.' }) {
		if part == token {
			return true
		}
	}
	return strings.Contains("-"+value+"-", "-"+token+"-")
}

func containsCompactForbiddenToken(value, token string) bool {
	compact := compactLowerAlnum(value)
	if token == "mountcommand" {
		compact = strings.ReplaceAll(compact, "rawmountcommand", "")
	}
	return strings.Contains(compact, token)
}

func compactLowerAlnum(value string) string {
	var builder strings.Builder
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if ch >= 'A' && ch <= 'Z' {
			ch += 'a' - 'A'
		}
		if isTokenWordByte(ch) {
			builder.WriteByte(ch)
		}
	}
	return builder.String()
}

func isTokenWordByte(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9')
}

type openAPIRouteKey struct {
	Method string
	Path   string
}

func (key openAPIRouteKey) String() string {
	return key.Method + " " + key.Path
}

func verifyOpenAPIRouteParity(openAPIPath, body string, operations []openAPIOperation) []Finding {
	var findings []Finding

	expectedByKey := make(map[openAPIRouteKey]api.RouteMetadata)
	for _, route := range api.InternalV1RouteMetadata() {
		key := openAPIRouteKey{
			Method: strings.ToUpper(strings.TrimSpace(route.Method)),
			Path:   route.Path,
		}
		expectedByKey[key] = route
	}

	actualByKey := make(map[openAPIRouteKey]openAPIOperation)
	for _, op := range operations {
		key := openAPIRouteKey{
			Method: strings.ToUpper(strings.TrimSpace(op.Method)),
			Path:   op.Path,
		}
		actualByKey[key] = op
	}

	for _, route := range api.InternalV1RouteMetadata() {
		key := openAPIRouteKey{
			Method: strings.ToUpper(strings.TrimSpace(route.Method)),
			Path:   route.Path,
		}
		op, ok := actualByKey[key]
		if !ok {
			findings = append(findings, Finding{
				Code:    CodeOpenAPIRouteOperationMissing,
				File:    openAPIPath,
				Line:    findLine(body, route.Path),
				Message: fmt.Sprintf("OpenAPI paths must include %s operationId %q from internal/api route metadata", key.String(), route.OperationID),
			})
			continue
		}
		if op.OperationID != route.OperationID {
			findings = append(findings, Finding{
				Code:    CodeOpenAPIRouteOperationIDMismatch,
				File:    openAPIPath,
				Line:    op.Line,
				Message: fmt.Sprintf("%s must use operationId %q from internal/api route metadata, got %q", key.String(), route.OperationID, op.OperationID),
			})
		}
	}

	for _, op := range operations {
		key := openAPIRouteKey{
			Method: strings.ToUpper(strings.TrimSpace(op.Method)),
			Path:   op.Path,
		}
		if _, ok := expectedByKey[key]; ok {
			continue
		}
		findings = append(findings, Finding{
			Code:    CodeOpenAPIRouteOperationExtra,
			File:    openAPIPath,
			Line:    op.Line,
			Message: fmt.Sprintf("OpenAPI paths must not include %s operationId %q outside internal/api route metadata", key.String(), op.operationName()),
		})
	}

	return findings
}

func verifySchema(path, body string) []Finding {
	var findings []Finding

	var root map[string]any
	if err := json.Unmarshal([]byte(body), &root); err != nil {
		return []Finding{{
			Code:    CodeSchemaInvalidJSON,
			File:    path,
			Line:    1,
			Message: fmt.Sprintf("schema must be valid JSON: %v", err),
		}}
	}

	defs, _ := root["$defs"].(map[string]any)

	findings = append(findings, verifySchemaEnumParity(path, body, defs, "ErrorCode", apiErrorCodeStrings(), CodeSchemaErrorCodeEnumGoDrift)...)
	findings = append(findings, verifySchemaEnumParity(path, body, defs, "CallerRole", authRoleStrings(), CodeSchemaCallerRoleEnumGoDrift)...)
	findings = append(findings, verifySchemaEnumParity(path, body, defs, "NamespaceBindingCallerRole", namespaceBindingCallerRoleStrings(), CodeSchemaNamespaceBindingCallerRoleEnumGoDrift)...)
	findings = append(findings, verifyNamespaceBindingAllowedCallerRoles(path, body, defs)...)
	findings = append(findings, verifySchemaEnumParity(path, body, defs, "OperationType", operationTypeStrings(), CodeSchemaOperationTypeEnumGoDrift)...)
	findings = append(findings, verifySchemaQuotaSemantics(path, body, root)...)
	findings = append(findings, verifyJSONSchemaRawCredentialFields(path, body, root)...)

	exportSession, _ := defs["ExportSession"].(map[string]any)
	exportSessionFields := []string{
		"created_by_caller_service",
		"created_by_actor",
		"created_at",
		"revoked_at",
		"last_accessed_at",
	}
	exportRequired := requiredSet(exportSession)
	if missing := missingRequired(exportRequired, exportSessionFields); len(missing) > 0 {
		findings = append(findings, Finding{
			Code:    CodeSchemaExportSessionRequiredMissing,
			File:    path,
			Line:    findLine(body, `"ExportSession"`),
			Message: "ExportSession required must include " + strings.Join(missing, ", "),
		})
	}
	if missing := missingProperties(exportSession, requiredAndExpectedFields(exportRequired, exportSessionFields)); len(missing) > 0 {
		findings = append(findings, Finding{
			Code:    CodeSchemaExportSessionPropertyMissing,
			File:    path,
			Line:    findLine(body, `"ExportSession"`),
			Message: "ExportSession properties must define required field(s) " + strings.Join(missing, ", "),
		})
	}
	if !hasAdditionalPropertiesFalse(exportSession) {
		findings = append(findings, Finding{
			Code:    CodeSchemaExportSessionAdditionalPropertiesInvalid,
			File:    path,
			Line:    findLine(body, `"ExportSession"`),
			Message: "ExportSession must set additionalProperties to false",
		})
	}

	operationEnvelope, _ := defs["OperationEnvelope"].(map[string]any)
	operationEnvelopeFields := []string{
		"operation_id",
		"operation_state",
		"resource",
		"result",
		"error",
	}
	envelopeRequired := requiredSet(operationEnvelope)
	if missing := missingRequired(envelopeRequired, operationEnvelopeFields); len(missing) > 0 {
		findings = append(findings, Finding{
			Code:    CodeSchemaOperationEnvelopeRequiredMissing,
			File:    path,
			Line:    findLine(body, `"OperationEnvelope"`),
			Message: "OperationEnvelope required must include flat API fields " + strings.Join(missing, ", "),
		})
	}
	if missing := missingProperties(operationEnvelope, requiredAndExpectedFields(envelopeRequired, operationEnvelopeFields)); len(missing) > 0 {
		findings = append(findings, Finding{
			Code:    CodeSchemaOperationEnvelopePropertyMissing,
			File:    path,
			Line:    findLine(body, `"OperationEnvelope"`),
			Message: "OperationEnvelope properties must define required field(s) " + strings.Join(missing, ", "),
		})
	}
	if !hasAdditionalPropertiesFalse(operationEnvelope) {
		findings = append(findings, Finding{
			Code:    CodeSchemaOperationEnvelopeAdditionalPropertiesInvalid,
			File:    path,
			Line:    findLine(body, `"OperationEnvelope"`),
			Message: "OperationEnvelope must set additionalProperties to false",
		})
	}
	envelopeProperties := propertiesSet(operationEnvelope)
	if envelopeRequired["operation"] || envelopeProperties["operation"] {
		findings = append(findings, Finding{
			Code:    CodeSchemaOperationEnvelopeNestedOperation,
			File:    path,
			Line:    findLine(body, `"OperationEnvelope"`),
			Message: "OperationEnvelope must not define or require a nested operation object",
		})
	}

	operationRecord, _ := defs["OperationRecord"].(map[string]any)
	if operationRecord != nil {
		operationRecordFields := []string{
			"operation_id",
			"operation_type",
			"operation_state",
			"phase",
			"attempt",
			"lease_owner",
			"lease_expires_at",
			"idempotency_scope",
			"idempotency_key",
			"request_hash",
			"correlation_id",
			"caller_service",
			"authorized_actor",
			"resource",
			"namespace_id",
			"repo_id",
			"template_id",
			"export_id",
			"mount_binding_id",
			"session_fence_id",
			"external_resource_ids",
			"input_summary",
			"jvs_json_output",
			"verification_result",
			"compensation_status",
			"error",
			"created_at",
			"started_at",
			"finished_at",
		}
		recordRequired := requiredSet(operationRecord)
		if missing := missingRequired(recordRequired, operationRecordFields); len(missing) > 0 {
			findings = append(findings, Finding{
				Code:    CodeSchemaOperationRecordRequiredMissing,
				File:    path,
				Line:    findLine(body, `"OperationRecord"`),
				Message: "OperationRecord required must include durable boundary field(s) " + strings.Join(missing, ", "),
			})
		}
		if missing := missingProperties(operationRecord, requiredAndExpectedFields(recordRequired, operationRecordFields)); len(missing) > 0 {
			findings = append(findings, Finding{
				Code:    CodeSchemaOperationRecordPropertyMissing,
				File:    path,
				Line:    findLine(body, `"OperationRecord"`),
				Message: "OperationRecord properties must define required field(s) " + strings.Join(missing, ", "),
			})
		}
		if !hasAdditionalPropertiesFalse(operationRecord) {
			findings = append(findings, Finding{
				Code:    CodeSchemaOperationRecordAdditionalPropertiesInvalid,
				File:    path,
				Line:    findLine(body, `"OperationRecord"`),
				Message: "OperationRecord must set additionalProperties to false",
			})
		}
		nullableFields := []string{
			"lease_owner",
			"lease_expires_at",
			"namespace_id",
			"repo_id",
			"template_id",
			"export_id",
			"mount_binding_id",
			"session_fence_id",
			"jvs_json_output",
			"verification_result",
			"compensation_status",
			"error",
			"started_at",
			"finished_at",
		}
		if invalid := nonNullableProperties(operationRecord, nullableFields); len(invalid) > 0 {
			findings = append(findings, Finding{
				Code:    CodeSchemaOperationRecordNullableInvalid,
				File:    path,
				Line:    findLine(body, `"OperationRecord"`),
				Message: "OperationRecord nullable required field(s) must accept null: " + strings.Join(invalid, ", "),
			})
		}
		if !propertyRefEquals(operationRecord, "operation_type", "#/$defs/OperationType") {
			findings = append(findings, Finding{
				Code:    CodeSchemaOperationRecordTypeEnumInvalid,
				File:    path,
				Line:    findLine(body, `"operation_type"`),
				Message: "OperationRecord.operation_type must reference #/$defs/OperationType",
			})
		}
	}

	return findings
}

func verifySchemaQuotaSemantics(path, body string, root map[string]any) []Finding {
	var findings []Finding

	for _, field := range findSchemaProperties(root, "quota_enforced") {
		findings = append(findings, Finding{
			Code:    CodeSchemaQuotaEnforcedForbidden,
			File:    path,
			Line:    findLine(body, `"`+field.Name+`"`),
			Message: "GA schema must not expose quota_enforced; quota enforcement is inferred only from selected volume capability and integration behavior",
		})
	}

	for _, fieldName := range []string{"quota_bytes_default", "directory_quota"} {
		for _, field := range findSchemaProperties(root, fieldName) {
			description, _ := field.Schema["description"].(string)
			if hasQuotaSemantics(description) {
				continue
			}
			findings = append(findings, Finding{
				Code:    CodeSchemaQuotaSemanticsMissing,
				File:    path,
				Line:    findLine(body, `"`+field.Name+`"`),
				Message: field.Name + " description must state quota_bytes_default/directory_quota policy record, enforcement hook, not enforced, and integration enables semantics",
			})
		}
	}

	return findings
}

func verifyJSONSchemaRawCredentialFields(path, body string, root map[string]any) []Finding {
	var findings []Finding
	for _, field := range findJSONSchemaRawCredentialMachineNames(root) {
		findings = append(findings, Finding{
			Code:    CodeSchemaRawCredentialFieldForbidden,
			File:    path,
			Line:    findLine(body, `"`+field.Name+`"`),
			Message: fmt.Sprintf("schema machine contract field %s must not expose raw credentials or direct storage details; forbidden token(s): %s", field.Location, strings.Join(field.Tokens, ", ")),
		})
	}
	return findings
}

func verifyOpenAPISchemaRawCredentialFields(path, body string) []Finding {
	var findings []Finding
	for _, field := range findOpenAPISchemaRawCredentialMachineNames(body) {
		findings = append(findings, Finding{
			Code:    CodeOpenAPISchemaRawCredentialFieldForbidden,
			File:    path,
			Line:    field.Line,
			Message: fmt.Sprintf("OpenAPI schema machine contract field %s must not expose raw credentials or direct storage details; forbidden token(s): %s", field.Location, strings.Join(field.Tokens, ", ")),
		})
	}
	return findings
}

type rawCredentialMachineField struct {
	Name     string
	Location string
	Tokens   []string
	Line     int
}

func findJSONSchemaRawCredentialMachineNames(root map[string]any) []rawCredentialMachineField {
	var found []rawCredentialMachineField
	seen := make(map[string]bool)
	scanJSONSchemaMachineNode(root, "#", &found, seen)
	return found
}

func scanJSONSchemaMachineNode(value any, location string, found *[]rawCredentialMachineField, seen map[string]bool) {
	schema, ok := value.(map[string]any)
	if !ok {
		return
	}

	for _, defsKey := range []string{"$defs", "definitions"} {
		defs, _ := schema[defsKey].(map[string]any)
		for name, child := range defs {
			childLocation := location + "/" + defsKey + "/" + name
			appendRawCredentialMachineField(name, childLocation, 0, found, seen)
			scanJSONSchemaMachineNode(child, childLocation, found, seen)
		}
	}

	if components, _ := schema["components"].(map[string]any); components != nil {
		if schemas, _ := components["schemas"].(map[string]any); schemas != nil {
			for name, child := range schemas {
				childLocation := location + "/components/schemas/" + name
				appendRawCredentialMachineField(name, childLocation, 0, found, seen)
				scanJSONSchemaMachineNode(child, childLocation, found, seen)
			}
		}
	}

	if properties, _ := schema["properties"].(map[string]any); properties != nil {
		for name, child := range properties {
			childLocation := location + "/properties/" + name
			appendRawCredentialMachineField(name, childLocation, 0, found, seen)
			scanJSONSchemaMachineNode(child, childLocation, found, seen)
		}
	}

	if required, _ := schema["required"].([]any); required != nil {
		for _, value := range required {
			name, ok := value.(string)
			if ok {
				appendRawCredentialMachineField(name, location+"/required/"+name, 0, found, seen)
			}
		}
	}

	if dependentSchemas, _ := schema["dependentSchemas"].(map[string]any); dependentSchemas != nil {
		for name, child := range dependentSchemas {
			childLocation := location + "/dependentSchemas/" + name
			appendRawCredentialMachineField(name, childLocation, 0, found, seen)
			scanJSONSchemaMachineNode(child, childLocation, found, seen)
		}
	}

	for _, key := range []string{"items", "contains", "additionalProperties", "unevaluatedItems", "unevaluatedProperties", "not", "if", "then", "else"} {
		if child, ok := schema[key]; ok {
			scanJSONSchemaMachineNode(child, location+"/"+key, found, seen)
		}
	}

	for _, key := range []string{"allOf", "oneOf", "anyOf", "prefixItems"} {
		children, _ := schema[key].([]any)
		for i, child := range children {
			scanJSONSchemaMachineNode(child, fmt.Sprintf("%s/%s/%d", location, key, i), found, seen)
		}
	}
}

func appendRawCredentialMachineField(name, location string, line int, found *[]rawCredentialMachineField, seen map[string]bool) {
	tokens := forbiddenRawCredentialMachineTokens(name)
	if len(tokens) == 0 {
		return
	}
	key := fmt.Sprintf("%s:%d:%s", location, line, strings.Join(tokens, ","))
	if seen[key] {
		return
	}
	seen[key] = true
	*found = append(*found, rawCredentialMachineField{
		Name:     name,
		Location: location,
		Tokens:   tokens,
		Line:     line,
	})
}

func forbiddenRawCredentialMachineTokens(value string) []string {
	type forbiddenToken struct {
		label   string
		compact string
	}
	forbidden := []forbiddenToken{
		{label: "metadata_url", compact: "metadataurl"},
		{label: "bucket_secret_key", compact: "bucketsecretkey"},
		{label: "aws_secret_access_key", compact: "awssecretaccesskey"},
		{label: "secret_access_key", compact: "secretaccesskey"},
		{label: "raw_mount_command", compact: "rawmountcommand"},
		{label: "direct_mount_command", compact: "directmountcommand"},
		{label: "direct_mount", compact: "directmount"},
		{label: "mount_command", compact: "mountcommand"},
		{label: "juicefs", compact: "juicefs"},
	}

	compact := compactLowerAlnum(value)
	seen := make(map[string]bool)
	var found []string
	for _, token := range forbidden {
		if !strings.Contains(compact, token.compact) {
			continue
		}
		if seen[token.label] {
			continue
		}
		seen[token.label] = true
		found = append(found, token.label)
	}
	sort.Strings(found)
	return found
}

type yamlFrame struct {
	Key    string
	Indent int
}

func findOpenAPISchemaRawCredentialMachineNames(body string) []rawCredentialMachineField {
	lines := splitLines(body)
	var stack []yamlFrame
	var found []rawCredentialMachineField
	seen := make(map[string]bool)

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := leadingSpaces(line)
		for len(stack) > 0 && indent <= stack[len(stack)-1].Indent {
			stack = stack[:len(stack)-1]
		}

		if requiredName, ok := parseYAMLListScalar(line); ok && yamlParentKey(stack) == "required" && isOpenAPISchemaYAMLContext(stack) {
			appendRawCredentialMachineField(requiredName, yamlMachineLocation(stack, requiredName), i+1, &found, seen)
			continue
		}

		key, value, hasKey := parseYAMLKeyValue(line)
		if !hasKey {
			continue
		}

		parent := yamlParentKey(stack)
		switch {
		case parent == "schemas" && isOpenAPIComponentsSchemasYAMLContext(stack):
			appendRawCredentialMachineField(key, yamlMachineLocation(stack, key), i+1, &found, seen)
		case parent == "properties" && isOpenAPISchemaYAMLContext(stack):
			appendRawCredentialMachineField(key, yamlMachineLocation(stack, key), i+1, &found, seen)
		case key == "$ref" && isOpenAPISchemaYAMLContext(stack):
			if refName := schemaRefMachineName(yamlScalarValue(value)); refName != "" {
				appendRawCredentialMachineField(refName, yamlMachineLocation(stack, refName), i+1, &found, seen)
			}
		}

		stack = append(stack, yamlFrame{Key: key, Indent: indent})
	}

	return found
}

func parseYAMLListScalar(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "- ") {
		return "", false
	}
	value := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
	if value == "" || strings.Contains(value, ":") {
		return "", false
	}
	return yamlScalarValue(value), true
}

func yamlParentKey(stack []yamlFrame) string {
	if len(stack) == 0 {
		return ""
	}
	return stack[len(stack)-1].Key
}

func isOpenAPIComponentsSchemasYAMLContext(stack []yamlFrame) bool {
	for i := 1; i < len(stack); i++ {
		if stack[i-1].Key == "components" && stack[i].Key == "schemas" {
			return true
		}
	}
	return false
}

func isOpenAPISchemaYAMLContext(stack []yamlFrame) bool {
	if isOpenAPIComponentsSchemasYAMLContext(stack) {
		return true
	}
	for _, frame := range stack {
		switch frame.Key {
		case "schema", "items", "allOf", "oneOf", "anyOf", "not", "additionalProperties", "unevaluatedProperties", "unevaluatedItems":
			return true
		}
	}
	return false
}

func yamlMachineLocation(stack []yamlFrame, name string) string {
	var parts []string
	for _, frame := range stack {
		parts = append(parts, frame.Key)
	}
	parts = append(parts, name)
	return strings.Join(parts, ".")
}

func schemaRefMachineName(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	if hash := strings.LastIndex(ref, "#"); hash >= 0 {
		ref = ref[hash+1:]
	}
	ref = strings.Trim(ref, "/")
	if ref == "" {
		return ""
	}
	parts := strings.Split(ref, "/")
	return parts[len(parts)-1]
}

type schemaProperty struct {
	Name   string
	Schema map[string]any
}

func findSchemaProperties(value any, name string) []schemaProperty {
	var found []schemaProperty
	findSchemaPropertiesInto(value, name, &found)
	return found
}

func findSchemaPropertiesInto(value any, name string, found *[]schemaProperty) {
	switch typed := value.(type) {
	case map[string]any:
		if properties, _ := typed["properties"].(map[string]any); properties != nil {
			if property, _ := properties[name].(map[string]any); property != nil {
				*found = append(*found, schemaProperty{Name: name, Schema: property})
			}
			for _, property := range properties {
				findSchemaPropertiesInto(property, name, found)
			}
		}
		for key, child := range typed {
			if key == "properties" {
				continue
			}
			findSchemaPropertiesInto(child, name, found)
		}
	case []any:
		for _, child := range typed {
			findSchemaPropertiesInto(child, name, found)
		}
	}
}

func hasQuotaSemantics(text string) bool {
	lower := strings.ToLower(text)
	for _, phrase := range []string{"quota_bytes_default", "directory_quota", "policy record", "enforcement hook", "not enforced"} {
		if !strings.Contains(lower, phrase) {
			return false
		}
	}
	return strings.Contains(lower, "integration") &&
		(strings.Contains(lower, "enables") || strings.Contains(lower, "enabled") || strings.Contains(lower, "explicitly enables"))
}

func verifySchemaEnumParity(path, body string, defs map[string]any, defName string, want []string, code string) []Finding {
	def, _ := defs[defName].(map[string]any)
	got := schemaEnumStrings(def)
	if reflect.DeepEqual(got, want) {
		return nil
	}

	return []Finding{{
		Code:    code,
		File:    path,
		Line:    findLine(body, `"`+defName+`"`),
		Message: defName + " enum must match Go constants: " + enumMismatchMessage(got, want),
	}}
}

func verifyNamespaceBindingAllowedCallerRoles(path, body string, defs map[string]any) []Finding {
	var findings []Finding

	allowedCaller, _ := defs["AllowedCaller"].(map[string]any)
	if !arrayItemsRefEquals(allowedCaller, "roles", "#/$defs/NamespaceBindingCallerRole") {
		findings = append(findings, Finding{
			Code:    CodeSchemaAllowedCallerRoleRefInvalid,
			File:    path,
			Line:    findLine(body, `"AllowedCaller"`),
			Message: "AllowedCaller.roles must reference #/$defs/NamespaceBindingCallerRole, not the global CallerRole enum",
		})
	}

	roleDef, _ := defs["NamespaceBindingCallerRole"].(map[string]any)
	values := schemaEnumStrings(roleDef)
	forbidden := map[string]bool{
		string(resources.CallerRoleVolumeAdmin):     true,
		string(resources.CallerRoleOperatorAdmin):   true,
		string(resources.CallerRoleBreakGlassAdmin): true,
	}
	var present []string
	for _, value := range values {
		if forbidden[value] {
			present = append(present, value)
		}
	}
	if len(present) > 0 {
		sort.Strings(present)
		findings = append(findings, Finding{
			Code:    CodeSchemaNamespaceBindingCallerRoleForbidden,
			File:    path,
			Line:    findLine(body, `"NamespaceBindingCallerRole"`),
			Message: "NamespaceBindingCallerRole enum must not include deployment/global roles: " + strings.Join(present, ", "),
		})
	}

	return findings
}

func schemaEnumStrings(def map[string]any) []string {
	if def == nil {
		return nil
	}

	values, _ := def["enum"].([]any)
	items := make([]string, 0, len(values))
	for _, value := range values {
		item, ok := value.(string)
		if ok {
			items = append(items, item)
		}
	}
	return items
}

func apiErrorCodeStrings() []string {
	codes := api.ErrorCodes()
	values := make([]string, len(codes))
	for i, code := range codes {
		values[i] = string(code)
	}
	return values
}

func authRoleStrings() []string {
	roles := auth.CallerRoles()
	values := make([]string, len(roles))
	for i, role := range roles {
		values[i] = string(role)
	}
	return values
}

func namespaceBindingCallerRoleStrings() []string {
	roles := resources.NamespaceBindingCallerRoles()
	values := make([]string, len(roles))
	for i, role := range roles {
		values[i] = string(role)
	}
	return values
}

func operationTypeStrings() []string {
	types := operations.OperationTypes()
	values := make([]string, len(types))
	for i, typ := range types {
		values[i] = string(typ)
	}
	return values
}

func verifyRouteOperationTypeMapping(path, body string, routes []api.RouteMetadata, routeTypes map[string]operations.OperationType) []Finding {
	routeByOperationID := make(map[string]api.RouteMetadata, len(routes))
	knownOperationTypes := make(map[operations.OperationType]bool, len(operations.OperationTypes()))
	for _, typ := range operations.OperationTypes() {
		knownOperationTypes[typ] = true
	}

	var findings []Finding
	for _, route := range routes {
		routeByOperationID[route.OperationID] = route
		if !route.Mutating {
			continue
		}
		typ, ok := routeTypes[route.OperationID]
		if !ok {
			findings = append(findings, Finding{
				Code:    CodeGoRouteOperationTypeMissing,
				File:    path,
				Line:    findLine(body, route.OperationID),
				Message: fmt.Sprintf("mutating route operationId %q must map to a durable operations.OperationType", route.OperationID),
			})
			continue
		}
		if !knownOperationTypes[typ] {
			findings = append(findings, Finding{
				Code:    CodeGoRouteOperationTypeMissing,
				File:    path,
				Line:    findLine(body, route.OperationID),
				Message: fmt.Sprintf("mutating route operationId %q maps to unknown operations.OperationType %q", route.OperationID, typ),
			})
		}
	}

	for operationID, typ := range routeTypes {
		route, ok := routeByOperationID[operationID]
		if !ok {
			findings = append(findings, Finding{
				Code:    CodeGoRouteOperationTypeUnknownRoute,
				File:    path,
				Line:    findLine(body, operationID),
				Message: fmt.Sprintf("route operation type mapping references unknown operationId %q", operationID),
			})
			continue
		}
		if !route.Mutating {
			findings = append(findings, Finding{
				Code:    CodeGoRouteOperationTypeNonMutating,
				File:    path,
				Line:    findLine(body, operationID),
				Message: fmt.Sprintf("route operation type mapping references non-mutating operationId %q with operation type %q", operationID, typ),
			})
		}
	}

	return findings
}

func enumMismatchMessage(got, want []string) string {
	missing, extra := stringListDiff(got, want)
	var parts []string
	if len(missing) > 0 {
		parts = append(parts, "missing "+strings.Join(missing, ", "))
	}
	if len(extra) > 0 {
		parts = append(parts, "extra "+strings.Join(extra, ", "))
	}
	if len(parts) == 0 {
		parts = append(parts, "order differs")
	}
	return strings.Join(parts, "; ")
}

func stringListDiff(got, want []string) ([]string, []string) {
	gotSet := stringSet(got)
	wantSet := stringSet(want)

	var missing []string
	for _, value := range want {
		if !gotSet[value] {
			missing = append(missing, value)
		}
	}

	var extra []string
	for _, value := range got {
		if !wantSet[value] {
			extra = append(extra, value)
		}
	}
	return missing, extra
}

func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

func verifyGoDTOBoundary(repoRoot string) []Finding {
	var findings []Finding

	operationStructs := goStructsInDir(filepath.Join(repoRoot, "internal", "operations"))
	for _, def := range operationStructs {
		if def.Name != "OperationEnvelope" {
			continue
		}
		findings = append(findings, Finding{
			Code:    CodeGoOperationsOperationEnvelopeAmbiguous,
			File:    def.File,
			Line:    def.Line,
			Message: "internal operations must not export OperationEnvelope; use OperationRecordEnvelope or InspectionEnvelope",
		})
	}

	apiStructs := goStructsInDir(filepath.Join(repoRoot, "internal", "api"))
	var apiEnvelope *goStruct
	for i := range apiStructs {
		if apiStructs[i].Name == "OperationEnvelope" {
			apiEnvelope = &apiStructs[i]
			break
		}
	}
	if apiEnvelope == nil {
		findings = append(findings, Finding{
			Code:    CodeGoAPIOperationEnvelopeMissing,
			File:    filepath.Join(repoRoot, "internal", "api"),
			Message: "internal/api must define the flat OperationEnvelope DTO",
		})
		return findings
	}

	required := []string{"operation_id", "operation_state", "resource", "result", "error"}
	if missing := missingGoJSONFields(apiEnvelope.JSONFields, required); len(missing) > 0 {
		findings = append(findings, Finding{
			Code:    CodeGoAPIOperationEnvelopePropertyMissing,
			File:    apiEnvelope.File,
			Line:    apiEnvelope.Line,
			Message: "api OperationEnvelope JSON fields must include " + strings.Join(missing, ", "),
		})
	}
	if apiEnvelope.JSONFields["operation"] {
		findings = append(findings, Finding{
			Code:    CodeGoAPIOperationEnvelopeNestedOperation,
			File:    apiEnvelope.File,
			Line:    apiEnvelope.Line,
			Message: "api OperationEnvelope must not expose a top-level operation object",
		})
	}

	return findings
}

func verifyDocs(apiContractPath, apiDraftPath, apiContract, apiDraft string) []Finding {
	corpus := apiContract + "\n" + apiDraft
	lower := strings.ToLower(corpus)

	var findings []Finding
	if !(strings.Contains(corpus, "OperationEnvelope") &&
		strings.Contains(corpus, "OperationRecord") &&
		strings.Contains(lower, "boundary")) {
		findings = append(findings, Finding{
			Code:    CodeDocsOperationBoundaryMissing,
			File:    apiContractPath + "," + apiDraftPath,
			Message: "docs must mention the OperationEnvelope vs OperationRecord boundary",
		})
	}
	if !(strings.Contains(corpus, "X-AFSCP-Namespace-Id") &&
		strings.Contains(lower, "namespace-bound")) {
		findings = append(findings, Finding{
			Code:    CodeDocsNamespaceHeaderMissing,
			File:    apiContractPath + "," + apiDraftPath,
			Message: "docs must mention the namespace header for namespace-bound requests",
		})
	}
	if missing := missingDocRoles(apiContract, auth.CallerRoles()); len(missing) > 0 {
		findings = append(findings, Finding{
			Code:    CodeDocsCallerRoleMissing,
			File:    apiContractPath,
			Message: "API contract role matrix must mention caller role(s): " + strings.Join(missing, ", "),
		})
	}
	if !(strings.Contains(corpus, "`operation_inspector`") &&
		strings.Contains(lower, "namespace-scoped operation inspection") &&
		strings.Contains(lower, "redacted")) {
		findings = append(findings, Finding{
			Code:    CodeDocsOperationInspectorScopeMissing,
			File:    apiContractPath + "," + apiDraftPath,
			Message: "docs must describe operation_inspector as namespace-scoped redacted operation inspection",
		})
	}
	if !(strings.Contains(corpus, "`operator_admin`") &&
		strings.Contains(lower, "global/operator inspection") &&
		strings.Contains(lower, "repair")) {
		findings = append(findings, Finding{
			Code:    CodeDocsOperatorAdminScopeMissing,
			File:    apiContractPath + "," + apiDraftPath,
			Message: "docs must distinguish operator_admin as global/operator inspection and repair",
		})
	}
	for _, doc := range []struct {
		path string
		body string
	}{
		{path: apiContractPath, body: apiContract},
		{path: apiDraftPath, body: apiDraft},
	} {
		if hasQuotaSemantics(doc.body) {
			continue
		}
		findings = append(findings, Finding{
			Code:    CodeDocsQuotaSemanticsMissing,
			File:    doc.path,
			Message: "docs must state quota_bytes_default/directory_quota policy record, enforcement hook, not enforced, and integration enables semantics",
		})
	}
	return findings
}

func verifyCoreProductDocs(repoRoot string) []Finding {
	paths := coreProductDocPaths(repoRoot)
	var findings []Finding
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		findings = append(findings, findProductSpecificDocTerms(path, string(body))...)
	}
	findings = append(findings, verifyCoreGateEvidenceDocs(repoRoot)...)
	return findings
}

func verifyCoreGateEvidenceDocs(repoRoot string) []Finding {
	paths := []string{
		filepath.Join(repoRoot, "docs", "READINESS_EVIDENCE.md"),
		filepath.Join(repoRoot, "docs", "RISK_REGISTER.md"),
	}
	var findings []Finding
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for i, line := range splitLines(string(body)) {
			if !strings.Contains(line, "`docs/INTEGRATION_GUIDE.md`") {
				continue
			}
			findings = append(findings, Finding{
				Code:    CodeDocsExternalAdoptionEvidenceForbidden,
				File:    path,
				Line:    i + 1,
				Message: "`docs/INTEGRATION_GUIDE.md` is external adoption notes and must not be used as GA gate or risk evidence",
			})
		}
	}
	return findings
}

func coreProductDocPaths(repoRoot string) []string {
	paths := []string{
		filepath.Join(repoRoot, "README.md"),
		filepath.Join(repoRoot, "docs", "GA_PRE_DEV_READINESS.md"),
		filepath.Join(repoRoot, "docs", "PRODUCT_REQUIREMENTS.md"),
		filepath.Join(repoRoot, "docs", "PRODUCT_BOUNDARY.md"),
		filepath.Join(repoRoot, "docs", "MVP_PLAN.md"),
		filepath.Join(repoRoot, "docs", "READINESS_EVIDENCE.md"),
		filepath.Join(repoRoot, "docs", "RISK_REGISTER.md"),
		filepath.Join(repoRoot, "docs", "REVIEW_CHECKLIST.md"),
		filepath.Join(repoRoot, "docs", "DEVELOPER_HANDOFF.md"),
		filepath.Join(repoRoot, "docs", "ARCHITECTURE.md"),
		filepath.Join(repoRoot, "docs", "API_CONTRACT_DRAFT.md"),
		filepath.Join(repoRoot, "docs", "DEVELOPMENT_GOVERNANCE.md"),
		filepath.Join(repoRoot, "docs", "PRE_DEV_COMPLETION.md"),
		filepath.Join(repoRoot, "docs", "SECURITY_AND_TENANCY.md"),
		filepath.Join(repoRoot, "docs", "STORAGE_LAYOUT.md"),
		filepath.Join(repoRoot, "docs", "runbooks", "README.md"),
		filepath.Join(repoRoot, "docs", "runbooks", "ga-runbooks.md"),
	}
	paths = append(paths, markdownFilesInDir(filepath.Join(repoRoot, "docs", "adr"))...)
	paths = append(paths, markdownFilesInDir(filepath.Join(repoRoot, "docs", "contracts"))...)
	sort.Strings(paths)
	return paths
}

func markdownFilesInDir(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var paths []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		paths = append(paths, filepath.Join(dir, entry.Name()))
	}
	return paths
}

func verifyCoreTestFixtureNames(repoRoot string) []Finding {
	paths := coreTestFixtureGuardPaths(repoRoot)
	var findings []Finding
	for _, path := range paths {
		if isCoreTestFixtureGuardSelfTest(repoRoot, path) {
			continue
		}
		findings = append(findings, forbiddenCoreTestFixturePathFindings(repoRoot, path)...)
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		findings = append(findings, forbiddenCoreTestFixtureBodyFindings(path, string(body))...)
	}
	return findings
}

func coreTestFixtureGuardPaths(repoRoot string) []string {
	var paths []string
	for _, root := range []string{
		filepath.Join(repoRoot, "internal"),
		filepath.Join(repoRoot, "cmd"),
	} {
		paths = append(paths, goTestFilesInTree(root)...)
	}
	paths = append(paths, markdownFilesInTree(filepath.Join(repoRoot, "test"))...)
	sort.Strings(paths)
	return paths
}

func goTestFilesInTree(root string) []string {
	var paths []string
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	return paths
}

func markdownFilesInTree(root string) []string {
	var paths []string
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	return paths
}

func isCoreTestFixtureGuardSelfTest(repoRoot, path string) bool {
	rel, err := filepath.Rel(repoRoot, path)
	if err != nil {
		return false
	}
	return filepath.ToSlash(rel) == "internal/contractcheck/contractcheck_test.go"
}

func forbiddenCoreTestFixturePathFindings(repoRoot, path string) []Finding {
	rel, err := filepath.Rel(repoRoot, path)
	if err != nil {
		rel = path
	}
	var findings []Finding
	for _, token := range forbiddenCoreTestFixtureTokens() {
		if !strings.Contains(filepath.ToSlash(rel), token) {
			continue
		}
		findings = append(findings, Finding{
			Code:    CodeGoCoreTestProductSpecificFixtureForbidden,
			File:    path,
			Message: fmt.Sprintf("core Go tests and test references must use generic AFSCP caller/runtime fixture names; path contains forbidden token %q", token),
		})
	}
	return findings
}

func forbiddenCoreTestFixtureBodyFindings(path, body string) []Finding {
	var findings []Finding
	for i, line := range splitLines(body) {
		for _, token := range forbiddenCoreTestFixtureLineTokens(line) {
			findings = append(findings, Finding{
				Code:    CodeGoCoreTestProductSpecificFixtureForbidden,
				File:    path,
				Line:    i + 1,
				Message: fmt.Sprintf("core Go tests and test references must use generic AFSCP caller/runtime fixture names; found forbidden token %q", token),
			})
		}
	}
	return findings
}

func forbiddenCoreTestFixtureTokens() []string {
	return []string{
		"agentsmith-api",
		"agentsmith-orchestrator",
		"sandbox-orchestrator",
		"sandbox-manager",
		"agentsmith-gateway",
		"agentsmith_afscp",
		"AgentsmithSandbox",
		"AgentSmithSandbox",
	}
}

func forbiddenCoreTestFixtureLineTokens(line string) []string {
	var tokens []string
	for _, token := range forbiddenCoreTestFixtureTokens() {
		if strings.Contains(line, token) {
			tokens = append(tokens, token)
		}
	}
	if strings.Contains(line, "github.com/agentsmith-project/") {
		return tokens
	}
	if strings.Contains(line, `"agentsmith"`) || strings.Contains(line, "`agentsmith`") {
		tokens = append(tokens, "agentsmith")
	}
	return tokens
}

func findProductSpecificDocTerms(path, body string) []Finding {
	var findings []Finding
	for i, line := range splitLines(body) {
		for _, term := range productSpecificTermsInLine(line) {
			findings = append(findings, Finding{
				Code:    CodeDocsProductSpecificTermForbidden,
				File:    path,
				Line:    i + 1,
				Message: fmt.Sprintf("core AFSCP document must not mention product-specific term %q; move caller-specific context to integration, external handoff, or adoption recommendation docs", term),
			})
		}
	}
	return findings
}

func productSpecificTermsInLine(line string) []string {
	type productTerm struct {
		label string
		match func(string) bool
	}
	lowerLine := strings.ToLower(line)
	terms := []productTerm{
		{label: "AgentSmith", match: func(line string) bool { return strings.Contains(line, "AgentSmith") }},
		{label: "agentsmith", match: containsForbiddenLowerAgentsmith},
		{label: "sandbox-manager", match: func(string) bool { return strings.Contains(lowerLine, "sandbox-manager") }},
		{label: "sandbox manager", match: func(string) bool { return strings.Contains(lowerLine, "sandbox manager") }},
		{label: "first calling product", match: func(string) bool { return strings.Contains(lowerLine, "first calling product") }},
		{label: "calling product owner", match: func(string) bool { return strings.Contains(lowerLine, "calling product owner") }},
		{label: "client connector owner", match: func(string) bool { return strings.Contains(lowerLine, "client connector owner") }},
		{label: "external owner review", match: func(string) bool { return strings.Contains(lowerLine, "external owner review") }},
		{label: "orchestrator owners", match: func(string) bool { return strings.Contains(lowerLine, "orchestrator owners") }},
		{label: "orchestrator v2 contract", match: func(string) bool { return strings.Contains(lowerLine, "orchestrator v2 contract") }},
		{label: "sandbox v2", match: func(string) bool { return strings.Contains(lowerLine, "sandbox v2") }},
		{label: "product confirmation", match: func(string) bool { return strings.Contains(lowerLine, "product confirmation") }},
	}
	var found []string
	for _, term := range terms {
		if term.match(line) {
			found = append(found, term.label)
		}
	}
	return found
}

func containsForbiddenLowerAgentsmith(line string) bool {
	for offset := 0; ; {
		index := strings.Index(line[offset:], "agentsmith")
		if index < 0 {
			return false
		}
		start := offset + index
		after := line[start+len("agentsmith"):]
		before := line[:start]
		if !strings.HasPrefix(after, "-project") && !strings.HasSuffix(before, "agentsmith-project/") {
			return true
		}
		offset = start + len("agentsmith")
	}
}

func missingDocRoles(body string, roles []auth.Role) []string {
	var missing []string
	for _, role := range roles {
		value := string(role)
		if !strings.Contains(body, "`"+value+"`") && !strings.Contains(body, value) {
			missing = append(missing, value)
		}
	}
	return missing
}

type openAPIOperation struct {
	Path        string
	Method      string
	OperationID string
	Body        string
	Line        int
}

func (op openAPIOperation) operationName() string {
	if op.OperationID != "" {
		return op.OperationID
	}
	return op.Method + " " + op.Path
}

func parseOpenAPIOperations(body string) []openAPIOperation {
	lines := splitLines(body)
	var operations []openAPIOperation
	var currentPath string
	var current *openAPIOperation
	inPaths := false
	pathsIndent := -1
	pathIndent := -1
	currentMethodIndent := -1

	flush := func() {
		if current == nil {
			return
		}
		current.OperationID = findOperationID(current.Body)
		operations = append(operations, *current)
		current = nil
	}

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		indent := leadingSpaces(line)
		key, _, hasKey := parseYAMLKeyValue(line)

		if !inPaths && hasKey && key == "paths" {
			inPaths = true
			pathsIndent = indent
			continue
		}
		if !inPaths {
			continue
		}

		if trimmed != "" && indent <= pathsIndent {
			flush()
			inPaths = false
			currentPath = ""
			pathIndent = -1
			currentMethodIndent = -1
			continue
		}

		if hasKey && strings.HasPrefix(key, "/") && indent > pathsIndent && (pathIndent < 0 || indent <= pathIndent) {
			flush()
			currentPath = key
			pathIndent = indent
			currentMethodIndent = -1
			continue
		}
		if currentPath != "" && hasKey && isOpenAPIMethod(key) && indent > pathIndent {
			if current == nil || indent <= currentMethodIndent {
				flush()
				currentMethodIndent = indent
				current = &openAPIOperation{
					Path:   currentPath,
					Method: strings.ToLower(key),
					Line:   i + 1,
					Body:   line + "\n",
				}
				continue
			}
		}
		if current != nil {
			current.Body += line + "\n"
		}
	}
	flush()

	return operations
}

func findOperationID(body string) string {
	for _, line := range splitLines(body) {
		key, value, ok := parseYAMLKeyValue(line)
		if ok && key == "operationId" {
			return yamlScalarValue(value)
		}
	}
	return ""
}

func isNamespaceBoundOperation(op openAPIOperation) bool {
	switch op.OperationID {
	case "ensureVolume", "getVolumeHealth", "getOperation":
		return false
	default:
		return true
	}
}

func isMutatingMethod(method string) bool {
	switch strings.ToLower(method) {
	case "post", "put", "patch", "delete":
		return true
	default:
		return false
	}
}

func isOpenAPIMethod(method string) bool {
	switch strings.ToLower(method) {
	case "get", "post", "put", "patch", "delete", "options", "head", "trace":
		return true
	default:
		return false
	}
}

func hasParameterRef(body, name string) bool {
	target := "#/components/parameters/" + name
	lines := splitLines(body)
	inParameters := false
	parametersIndent := -1

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		indent := leadingSpaces(line)
		key, value, hasKey := parseYAMLKeyValue(line)

		if inParameters {
			if trimmed != "" && indent <= parametersIndent {
				inParameters = false
			} else if hasKey && key == "$ref" && yamlScalarValue(value) == target {
				return true
			}
		}

		if !inParameters && hasKey && key == "parameters" {
			if strings.Contains(value, "$ref") && strings.Contains(value, target) {
				return true
			}
			inParameters = true
			parametersIndent = indent
		}
	}

	return false
}

func parseYAMLKeyValue(line string) (string, string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", "", false
	}
	if strings.HasPrefix(trimmed, "- ") {
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
	}
	if trimmed == "" {
		return "", "", false
	}

	if trimmed[0] == '"' || trimmed[0] == '\'' {
		quote := trimmed[0]
		for i := 1; i < len(trimmed); i++ {
			if trimmed[i] != quote {
				continue
			}
			rest := strings.TrimSpace(trimmed[i+1:])
			if !strings.HasPrefix(rest, ":") {
				return "", "", false
			}
			return trimmed[1:i], strings.TrimSpace(rest[1:]), true
		}
		return "", "", false
	}

	separator := strings.Index(trimmed, ":")
	if strings.HasPrefix(trimmed, "/") {
		separator = strings.LastIndex(trimmed, ":")
	}
	if separator < 0 {
		return "", "", false
	}

	key := strings.TrimSpace(trimmed[:separator])
	if key == "" {
		return "", "", false
	}
	return key, strings.TrimSpace(trimmed[separator+1:]), true
}

func yamlScalarValue(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') {
		quote := value[0]
		for i := 1; i < len(value); i++ {
			if value[i] == quote {
				return value[1:i]
			}
		}
	}
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func splitLines(body string) []string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	return strings.Split(body, "\n")
}

func findYAMLBlock(lines []string, key string, indent int) (string, int, bool) {
	target := strings.Repeat(" ", indent) + key + ":"
	for i, line := range lines {
		if strings.TrimRight(line, " \t") != target {
			continue
		}

		var block []string
		block = append(block, line)
		for j := i + 1; j < len(lines); j++ {
			next := lines[j]
			if strings.TrimSpace(next) != "" && leadingSpaces(next) <= indent {
				break
			}
			block = append(block, next)
		}
		return strings.Join(block, "\n"), i + 1, true
	}
	return "", 0, false
}

func hasYAMLScalar(block, key, want string) bool {
	scalar := regexp.MustCompile(`(?m)^\s+` + regexp.QuoteMeta(key) + `:\s*["']?` + regexp.QuoteMeta(want) + `["']?\s*$`)
	return scalar.MatchString(block)
}

func leadingSpaces(s string) int {
	count := 0
	for _, r := range s {
		if r != ' ' {
			break
		}
		count++
	}
	return count
}

func requiredSet(def map[string]any) map[string]bool {
	required := make(map[string]bool)
	if def == nil {
		return required
	}

	values, _ := def["required"].([]any)
	for _, value := range values {
		name, ok := value.(string)
		if ok {
			required[name] = true
		}
	}
	return required
}

func propertiesSet(def map[string]any) map[string]bool {
	properties := make(map[string]bool)
	if def == nil {
		return properties
	}

	values, _ := def["properties"].(map[string]any)
	for name := range values {
		properties[name] = true
	}
	return properties
}

func hasAdditionalPropertiesFalse(def map[string]any) bool {
	if def == nil {
		return false
	}
	value, ok := def["additionalProperties"].(bool)
	return ok && !value
}

func requiredAndExpectedFields(required map[string]bool, expected []string) []string {
	fields := make(map[string]bool)
	for _, field := range expected {
		fields[field] = true
	}
	for field := range required {
		fields[field] = true
	}

	names := make([]string, 0, len(fields))
	for field := range fields {
		names = append(names, field)
	}
	sort.Strings(names)
	return names
}

func missingRequired(required map[string]bool, fields []string) []string {
	var missing []string
	for _, field := range fields {
		if !required[field] {
			missing = append(missing, field)
		}
	}
	sort.Strings(missing)
	return missing
}

func missingProperties(def map[string]any, fields []string) []string {
	properties := propertiesSet(def)
	var missing []string
	for _, field := range fields {
		if !properties[field] {
			missing = append(missing, field)
		}
	}
	sort.Strings(missing)
	return missing
}

func nonNullableProperties(def map[string]any, fields []string) []string {
	properties := propertiesMap(def)
	var invalid []string
	for _, field := range fields {
		property, ok := properties[field]
		if !ok {
			continue
		}
		if !schemaAllowsNull(property) {
			invalid = append(invalid, field)
		}
	}
	sort.Strings(invalid)
	return invalid
}

func propertiesMap(def map[string]any) map[string]any {
	if def == nil {
		return nil
	}
	properties, _ := def["properties"].(map[string]any)
	return properties
}

func propertyRefEquals(def map[string]any, field, ref string) bool {
	properties := propertiesMap(def)
	property, ok := properties[field].(map[string]any)
	if !ok {
		return false
	}
	got, ok := property["$ref"].(string)
	return ok && got == ref
}

func arrayItemsRefEquals(def map[string]any, field, ref string) bool {
	properties := propertiesMap(def)
	property, ok := properties[field].(map[string]any)
	if !ok {
		return false
	}
	items, ok := property["items"].(map[string]any)
	if !ok {
		return false
	}
	got, ok := items["$ref"].(string)
	return ok && got == ref
}

func schemaAllowsNull(schema any) bool {
	def, ok := schema.(map[string]any)
	if !ok {
		return false
	}

	switch typ := def["type"].(type) {
	case string:
		if typ == "null" {
			return true
		}
	case []any:
		for _, value := range typ {
			if value == "null" {
				return true
			}
		}
	}

	for _, combiner := range []string{"oneOf", "anyOf"} {
		options, _ := def[combiner].([]any)
		for _, option := range options {
			if schemaAllowsNull(option) {
				return true
			}
		}
	}

	return false
}

type goStruct struct {
	Name       string
	File       string
	Line       int
	JSONFields map[string]bool
}

func findRepoRoot(anchorPath string) (string, bool) {
	absolute, err := filepath.Abs(anchorPath)
	if err != nil {
		return "", false
	}

	dir := filepath.Clean(filepath.Dir(absolute))
	for {
		if dir == string(filepath.Separator) {
			return "", false
		}
		if pathExists(filepath.Join(dir, "internal", "operations")) && pathExists(filepath.Join(dir, "internal", "api")) {
			return dir, true
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func goStructsInDir(dir string) []goStruct {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var structs []goStruct
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		path := filepath.Join(dir, name)
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}

		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				structType, ok := typeSpec.Type.(*ast.StructType)
				if !ok {
					continue
				}
				position := fset.Position(typeSpec.Pos())
				structs = append(structs, goStruct{
					Name:       typeSpec.Name.Name,
					File:       path,
					Line:       position.Line,
					JSONFields: jsonFieldsForStruct(structType),
				})
			}
		}
	}

	return structs
}

func jsonFieldsForStruct(structType *ast.StructType) map[string]bool {
	fields := make(map[string]bool)
	for _, field := range structType.Fields.List {
		if len(field.Names) == 0 {
			continue
		}

		for _, name := range jsonFieldNames(field) {
			fields[name] = true
		}
	}
	return fields
}

func jsonFieldNames(field *ast.Field) []string {
	if field.Tag != nil {
		tag := reflect.StructTag(strings.Trim(field.Tag.Value, "`"))
		name := strings.Split(tag.Get("json"), ",")[0]
		if name == "-" {
			return nil
		}
		if name != "" {
			return []string{name}
		}
	}

	names := make([]string, 0, len(field.Names))
	for _, fieldName := range field.Names {
		names = append(names, fieldName.Name)
	}
	return names
}

func missingGoJSONFields(fields map[string]bool, required []string) []string {
	var missing []string
	for _, field := range required {
		if !fields[field] {
			missing = append(missing, field)
		}
	}
	sort.Strings(missing)
	return missing
}

func findLine(body, needle string) int {
	for i, line := range splitLines(body) {
		if strings.Contains(line, needle) {
			return i + 1
		}
	}
	return 0
}
