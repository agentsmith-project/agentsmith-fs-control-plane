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
)

const (
	CodeOpenAPINamespaceParameterInvalid = "openapi.namespace_id_parameter_invalid"
	CodeOpenAPINamespaceParameterMissing = "openapi.namespace_id_parameter_missing"
	CodeOpenAPIMutatingHeaderMissing     = "openapi.mutating_header_missing"
	CodeOpenAPIOperationsMissing         = "openapi.operations_missing"

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
	CodeSchemaErrorCodeEnumGoDrift                         = "schema.error_code_enum_go_drift"
	CodeSchemaCallerRoleEnumGoDrift                        = "schema.caller_role_enum_go_drift"
	CodeSchemaInvalidJSON                                  = "schema.invalid_json"

	CodeDocsOperationBoundaryMissing       = "docs.operation_boundary_missing"
	CodeDocsNamespaceHeaderMissing         = "docs.namespace_header_missing"
	CodeDocsCallerRoleMissing              = "docs.caller_role_missing"
	CodeDocsOperationInspectorScopeMissing = "docs.operation_inspector_scope_missing"
	CodeDocsOperatorAdminScopeMissing      = "docs.operator_admin_scope_missing"

	CodeGoOperationsOperationEnvelopeAmbiguous = "go.operations_operation_envelope_ambiguous"
	CodeGoAPIOperationEnvelopeMissing          = "go.api_operation_envelope_missing"
	CodeGoAPIOperationEnvelopePropertyMissing  = "go.api_operation_envelope_property_missing"
	CodeGoAPIOperationEnvelopeNestedOperation  = "go.api_operation_envelope_nested_operation"
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
	findings = append(findings, verifyDocs(apiContractPath, apiDraftPath, string(apiContract), string(apiDraft))...)
	if repoRoot, ok := findRepoRoot(schemaPath); ok {
		findings = append(findings, verifyGoDTOBoundary(repoRoot)...)
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
	}

	return findings
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
	return findings
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
