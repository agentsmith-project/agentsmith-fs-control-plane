package projectionguard

import "strings"

var exactJVSInternalFields = map[string]bool{
	"capacity":               true,
	"capacitybytes":          true,
	"command":                true,
	"contentroothash":        true,
	"controlpath":            true,
	"controlroot":            true,
	"controlrootpath":        true,
	"controlvolumesubdir":    true,
	"directmountcommand":     true,
	"expectedfolderevidence": true,
	"filecount":              true,
	"home":                   true,
	"homepath":               true,
	"internalpath":           true,
	"internalpaths":          true,
	"internalroot":           true,
	"internalrootpath":       true,
	"mountcommand":           true,
	"payloadfilecount":       true,
	"payloadroot":            true,
	"payloadroothash":        true,
	"payloadrootpath":        true,
	"payloadtree":            true,
	"payloadtreescan":        true,
	"payloadvolumesubdir":    true,
	"planid":                 true,
	"previewid":              true,
	"previewstate":           true,
	"rawcommand":             true,
	"rawmountcommand":        true,
	"rawpath":                true,
	"recommendednextcommand": true,
	"reporoot":               true,
	"restorecommand":         true,
	"restoreplanid":          true,
	"runcommand":             true,
	"saveprofile":            true,
	"stderr":                 true,
	"stdout":                 true,
	"sync":                   true,
	"syncstate":              true,
	"synctoken":              true,
	"targetcontrolroot":      true,
	"targetfolder":           true,
	"tree":                   true,
	"treescan":               true,
	"treescanresult":         true,
	"workspace":              true,
}

var containsJVSInternalFieldMarkers = []string{
	"checksum",
	"digest",
	"hash",
	"proof",
}

// ForbiddenJVSInternalField reports whether a dynamic result/detail field is
// JVS-internal material that must not cross into product-facing projections.
func ForbiddenJVSInternalField(key string) bool {
	normalized := NormalizeFieldKey(key)
	if normalized == "" {
		return false
	}
	if exactJVSInternalFields[normalized] {
		return true
	}
	for _, marker := range containsJVSInternalFieldMarkers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func NormalizeFieldKey(key string) string {
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
	}, key)
}

func ContainsForbiddenJVSInternalText(value string) bool {
	normalized := strings.ToLower(value)
	for _, fragment := range []string{
		"/srv/afscp",
		"/home/afscp",
		"afscp/namespaces/",
		".jvs",
		"jvs afscp",
		"jvs restore --run",
		"jvs init",
		"jvs doctor",
		"juicefs mount",
		"--control-root",
		"--home",
	} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}
