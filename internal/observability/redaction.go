package observability

import (
	"regexp"
	"strings"
)

const Redacted = "[REDACTED]"

const rawSensitiveKeyPattern = `(?:secret[\s_-]?ref|secret|token|password|passwd|api[\s_-]?key|access[\s_-]?key|secret[\s_-]?key|private[\s_-]?key|credential|authorization|metadata[\s_-]?url|webdav[\s_-]?password)`

var (
	rawMetadataURLPattern  = regexp.MustCompile(`(?i)\b(?:redis|mysql|postgres|postgresql|mongodb|etcd|tikv|sqlite|badger)://[^\s,;)"'}]+`)
	rawBearerPattern       = regexp.MustCompile(`(?i)\b(bearer(?:[\s_-]+token)?(?:\s*[:=])?\s+)([A-Za-z0-9._~+/=-]+)`)
	rawAssignmentPattern   = regexp.MustCompile(`(?i)\b([a-z0-9_.-]*` + rawSensitiveKeyPattern + `[a-z0-9_.-]*)(\s*=\s*)(?:"[^"]*"|'[^']*'|[^\s,;]+)`)
	rawJSONColonPattern    = regexp.MustCompile(`(?i)("[^"]*` + rawSensitiveKeyPattern + `[^"]*"\s*:\s*")([^"]*)(")`)
	rawPlainColonPattern   = regexp.MustCompile(`(?i)\b([a-z0-9_.-]*` + rawSensitiveKeyPattern + `[a-z0-9_.-]*)(\s*:\s*)(?:"[^"]*"|'[^']*'|[^\n,;}]+)`)
	rawCLIFlagPattern      = regexp.MustCompile(`(?i)(--[a-z0-9_.-]*` + rawSensitiveKeyPattern + `[a-z0-9_.-]*)(\s+|=)(?:"[^"]*"|'[^']*'|[^\s,;]+)`)
	rawAFSCPRootPattern    = regexp.MustCompile(`(?i)/srv/afscp[^\s,;)"'}]*`)
	rawAFSCPSubdirPattern  = regexp.MustCompile(`(?i)\bafscp/namespaces/[^\s,;)"'}]*/(?:control|payload)\b[^\s,;)"'}]*`)
	rawJVSPathPattern      = regexp.MustCompile(`(?i)(^|[^\w.-])(\.jvs(?:/[^\s,;)"'}]*)?)`)
	rawJVSCommandPattern   = regexp.MustCompile(`(?i)\bjvs\b(?:\s+--(?:control-root|workspace)(?:=|\s+)[^\s,;}]+)*\s+(?:init|doctor|save|history|restore(?:\s+(?:--run|discard))?|recovery\s+status)\b[^\n,;}]*`)
	rawMountCommandPattern = regexp.MustCompile(`(?i)\bjuicefs\s+mount\b[^\n,;}]*`)
)

type Event struct {
	Name   string
	Fields map[string]any
}

func NewEvent(name string, fields map[string]any) Event {
	return Event{
		Name:   strings.TrimSpace(name),
		Fields: RedactFields(fields),
	}
}

func RedactFields(fields map[string]any) map[string]any {
	if fields == nil {
		return nil
	}

	redacted := make(map[string]any, len(fields))
	for key, value := range fields {
		redacted[key] = redactValue(key, value)
	}
	return redacted
}

func RedactString(value string) (string, bool) {
	redacted := value
	redacted = rawMetadataURLPattern.ReplaceAllString(redacted, Redacted)
	redacted = rawAFSCPRootPattern.ReplaceAllString(redacted, Redacted)
	redacted = rawAFSCPSubdirPattern.ReplaceAllString(redacted, Redacted)
	redacted = replaceRawStringSubmatches(redacted, rawJVSPathPattern, func(parts []string) string {
		return parts[1] + Redacted
	})
	redacted = rawJVSCommandPattern.ReplaceAllString(redacted, Redacted)
	redacted = rawMountCommandPattern.ReplaceAllString(redacted, Redacted)
	redacted = replaceRawStringSubmatches(redacted, rawJSONColonPattern, func(parts []string) string {
		return parts[1] + Redacted + parts[3]
	})
	redacted = replaceRawStringSubmatches(redacted, rawCLIFlagPattern, func(parts []string) string {
		return parts[1] + parts[2] + Redacted
	})
	redacted = replaceRawStringSubmatches(redacted, rawAssignmentPattern, func(parts []string) string {
		return parts[1] + parts[2] + Redacted
	})
	redacted = replaceRawStringSubmatches(redacted, rawPlainColonPattern, func(parts []string) string {
		return parts[1] + parts[2] + Redacted
	})
	redacted = replaceRawStringSubmatches(redacted, rawBearerPattern, func(parts []string) string {
		return parts[1] + Redacted
	})
	return redacted, redacted != value
}

func IsSensitiveField(name string) bool {
	normalized := normalizeSensitiveFieldName(name)
	if normalized == "" {
		return false
	}

	for _, marker := range []string{
		"metadataurl",
		"storagebucketurl",
		"objectstoreendpoint",
		"authorization",
		"cookie",
		"apikey",
		"accesskey",
		"secretaccesskey",
		"secretkey",
		"privatekey",
		"password",
		"passwd",
		"secret",
		"secretref",
		"k8ssecret",
		"kubernetessecret",
		"kubernetessecretref",
		"k8ssecretref",
		"kubesecretref",
		"webdavpassword",
		"token",
		"credential",
		"bearer",
		"rawpath",
		"controlroot",
		"payloadroot",
		"controlrootpath",
		"payloadrootpath",
		"reporoot",
		"targetcontrolroot",
		"controlvolumesubdir",
		"payloadvolumesubdir",
		"runcommand",
		"recommendednextcommand",
		"restorecommand",
		"mountcommand",
		"rawmountcommand",
		"directmountcommand",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func redactValue(fieldName string, value any) any {
	if IsSensitiveField(fieldName) {
		return Redacted
	}

	switch typed := value.(type) {
	case map[string]any:
		return RedactFields(typed)
	case map[string]string:
		return redactStringMap(typed)
	case []any:
		redacted := make([]any, len(typed))
		for i, item := range typed {
			redacted[i] = redactValue("", item)
		}
		return redacted
	case []map[string]any:
		redacted := make([]map[string]any, len(typed))
		for i, item := range typed {
			redacted[i] = RedactFields(item)
		}
		return redacted
	case []string:
		redacted := make([]string, len(typed))
		for i, item := range typed {
			if isSensitiveValue(item) {
				redacted[i] = Redacted
				continue
			}
			if value, ok := RedactString(item); ok {
				redacted[i] = value
				continue
			}
			redacted[i] = item
		}
		return redacted
	case string:
		if isSensitiveValue(typed) {
			return Redacted
		}
		if value, ok := RedactString(typed); ok {
			return value
		}
		return typed
	default:
		return value
	}
}

func redactStringMap(fields map[string]string) map[string]string {
	redacted := make(map[string]string, len(fields))
	for key, value := range fields {
		if IsSensitiveField(key) || isSensitiveValue(value) {
			redacted[key] = Redacted
			continue
		}
		if scrubbed, ok := RedactString(value); ok {
			redacted[key] = scrubbed
			continue
		}
		redacted[key] = value
	}
	return redacted
}

func replaceRawStringSubmatches(value string, pattern *regexp.Regexp, replace func([]string) string) string {
	return pattern.ReplaceAllStringFunc(value, func(match string) string {
		parts := pattern.FindStringSubmatch(match)
		if len(parts) == 0 {
			return match
		}
		return replace(parts)
	})
}

func normalizeSensitiveFieldName(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		case r >= '0' && r <= '9':
			return r
		default:
			return -1
		}
	}, strings.TrimSpace(name))
}

func isSensitiveValue(value string) bool {
	fields := strings.Fields(value)
	if len(fields) < 2 {
		return false
	}

	for i := 0; i < len(fields)-1; i++ {
		if strings.EqualFold(strings.TrimSuffix(fields[i], ":"), "bearer") {
			return true
		}
	}
	return false
}
