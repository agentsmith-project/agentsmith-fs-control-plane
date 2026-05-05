package operations

import (
	"fmt"
	"sort"
	"strings"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/observability"
)

const redactedValue = "[REDACTED]"

func RedactValue(value any) (any, RedactionReport) {
	redactor := valueRedactor{}
	redacted := redactor.redact(value, "")
	return redacted, redactor.report()
}

func MergeRedactionReports(reports ...RedactionReport) RedactionReport {
	merged := RedactionReport{}
	seen := map[string]bool{}

	for _, report := range reports {
		if report.Redacted {
			merged.Redacted = true
		}
		for _, field := range report.Fields {
			if field == "" || seen[field] {
				continue
			}
			seen[field] = true
			merged.Fields = append(merged.Fields, field)
		}
	}

	sort.Strings(merged.Fields)
	return merged
}

func RedactExternalResourceIDs(ids map[string]string) (map[string]string, RedactionReport) {
	if ids == nil {
		return nil, RedactionReport{}
	}

	out := make(map[string]string, len(ids))
	fields := make([]string, 0, len(ids))
	for key := range ids {
		out[key] = redactedValue
		fields = append(fields, joinPath("external_resource_ids", key))
	}
	if len(fields) == 0 {
		return out, RedactionReport{}
	}

	return out, RedactionReport{
		Redacted: true,
		Fields:   fields,
	}
}

type valueRedactor struct {
	fields []string
}

func (redactor *valueRedactor) redact(value any, path string) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		return redactor.redactString(typed, path)
	case map[string]any:
		return redactor.redactMap(typed, path)
	case map[string]string:
		converted := make(map[string]any, len(typed))
		for key, value := range typed {
			converted[key] = value
		}
		return redactor.redactMap(converted, path)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = redactor.redact(item, joinPath(path, fmt.Sprintf("[%d]", i)))
		}
		return out
	case []string:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = redactor.redact(item, joinPath(path, fmt.Sprintf("[%d]", i)))
		}
		return out
	default:
		return value
	}
}

func (redactor *valueRedactor) redactMap(values map[string]any, path string) map[string]any {
	out := make(map[string]any, len(values))
	for key, value := range values {
		fieldPath := joinPath(path, key)
		if isSensitiveKey(key) {
			out[key] = redactedValue
			redactor.mark(fieldPath)
			continue
		}
		out[key] = redactor.redact(value, fieldPath)
	}
	return out
}

func (redactor *valueRedactor) redactString(value, path string) string {
	if containsSensitiveText(value) {
		redactor.mark(path)
		return redactedValue
	}
	redacted, ok := observability.RedactString(value)
	if ok {
		redactor.mark(path)
	}
	return redacted
}

func containsSensitiveText(value string) bool {
	normalized := strings.ToLower(value)
	for _, fragment := range []string{"password", "passwd", "secret", "token", "credential"} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func (redactor *valueRedactor) mark(path string) {
	if path == "" {
		path = "$"
	}
	redactor.fields = append(redactor.fields, path)
}

func (redactor *valueRedactor) report() RedactionReport {
	if len(redactor.fields) == 0 {
		return RedactionReport{}
	}
	return MergeRedactionReports(RedactionReport{
		Redacted: true,
		Fields:   redactor.fields,
	})
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(key)
	normalized = strings.NewReplacer("_", "", "-", "", ".", "", " ", "").Replace(normalized)

	sensitiveFragments := []string{
		"password",
		"passwd",
		"secret",
		"token",
		"credential",
		"authorization",
		"accesskey",
		"secretkey",
		"metadataurl",
		"webdavpassword",
		"rawpath",
		"stdout",
		"stderr",
	}
	for _, fragment := range sensitiveFragments {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func joinPath(prefix, name string) string {
	if prefix == "" {
		return name
	}
	if strings.HasPrefix(name, "[") {
		return prefix + name
	}
	return prefix + "." + name
}
