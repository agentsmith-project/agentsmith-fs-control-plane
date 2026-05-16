package api

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
)

func TestRoutePathParamsExtractsOrdinaryAndSuffixParams(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		path    string
		want    map[string]string
	}{
		{
			name:    "suffix action volume",
			pattern: "/internal/v1/volumes/{volumeId}:ensure",
			path:    "/internal/v1/volumes/vol_123:ensure",
			want:    map[string]string{"volumeId": "vol_123"},
		},
		{
			name:    "ordinary namespace",
			pattern: "/internal/v1/namespaces/{namespaceId}/volume-binding",
			path:    "/internal/v1/namespaces/ns_123/volume-binding",
			want:    map[string]string{"namespaceId": "ns_123"},
		},
		{
			name:    "suffix action mount binding",
			pattern: "/internal/v1/workload-mount-bindings/{mountBindingId}:heartbeat",
			path:    "/internal/v1/workload-mount-bindings/wmb_123:heartbeat",
			want:    map[string]string{"mountBindingId": "wmb_123"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := RoutePathParams(tt.pattern, tt.path)
			if !ok {
				t.Fatalf("RoutePathParams(%q, %q) ok = false, want true", tt.pattern, tt.path)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("params = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestRoutePathParamsRejectsNonMatches(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		path    string
	}{
		{name: "empty suffix capture", pattern: "/internal/v1/volumes/{volumeId}:ensure", path: "/internal/v1/volumes/:ensure"},
		{name: "empty ordinary capture", pattern: "/internal/v1/namespaces/{namespaceId}", path: "/internal/v1/namespaces/"},
		{name: "segment count mismatch", pattern: "/internal/v1/namespaces/{namespaceId}/volume-binding", path: "/internal/v1/namespaces/ns_123"},
		{name: "literal mismatch", pattern: "/internal/v1/namespaces/{namespaceId}/volume-binding", path: "/internal/v1/namespaces/ns_123/other-binding"},
		{name: "suffix mismatch", pattern: "/internal/v1/workload-mount-bindings/{mountBindingId}:heartbeat", path: "/internal/v1/workload-mount-bindings/wmb_123:release"},
		{name: "duplicate param name", pattern: "/internal/v1/repos/{resourceId}/exports/{resourceId}", path: "/internal/v1/repos/repo_123/exports/export_123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if params, ok := RoutePathParams(tt.pattern, tt.path); ok {
				t.Fatalf("RoutePathParams ok = true params = %#v, want not matched", params)
			}
			if routePatternMatches(tt.pattern, tt.path) {
				t.Fatalf("routePatternMatches(%q, %q) = true, want false", tt.pattern, tt.path)
			}
		})
	}
}

func TestRouteMetadataForRequestMethodMismatchAndUnknownRoute(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "method mismatch", method: http.MethodGet, path: "/internal/v1/volumes/vol_123:ensure"},
		{name: "unknown route", method: http.MethodGet, path: "/internal/v1/not-a-route/id_123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if metadata, ok := RouteMetadataForRequest(httptest.NewRequest(tt.method, tt.path, nil)); ok {
				t.Fatalf("RouteMetadataForRequest matched %#v, want not matched", metadata)
			}
		})
	}
}

func TestInternalV1RouteMetadataDoesNotExposeRawDirectMountAccess(t *testing.T) {
	for _, route := range InternalV1RouteMetadata() {
		t.Run(route.OperationID, func(t *testing.T) {
			if route.RequiredRole == auth.RoleBreakGlassAdmin {
				t.Fatalf("route %s %s requires break-glass admin; ordinary internal v1 routes must not use break-glass direct access", route.Method, route.Path)
			}

			if tokens := routeRawDirectMountForbiddenTokens(route.Path, route.OperationID); len(tokens) > 0 {
				t.Fatalf("route %s %s operationId %q contains forbidden raw/direct mount token(s): %s", route.Method, route.Path, route.OperationID, strings.Join(tokens, ", "))
			}
		})
	}
}

func TestInternalV1RouteMetadataDoesNotExposeLegacyRestorePlanRoutes(t *testing.T) {
	for _, route := range InternalV1RouteMetadata() {
		switch route.OperationID {
		case "restorePreview", "restorePreviewDiscard", "restoreRun", "restoreAdmit":
			t.Fatalf("legacy restore plan operationId %q remains routed at %s %s", route.OperationID, route.Method, route.Path)
		}
		if strings.Contains(route.Path, "restore-preview") || strings.Contains(route.Path, "restore-run") || strings.Contains(route.Path, "restore:admit") {
			t.Fatalf("legacy restore plan path remains routed: %s %s", route.Method, route.Path)
		}
	}

	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/internal/v1/repos/repo_123/restore-preview", nil),
		httptest.NewRequest(http.MethodPost, "/internal/v1/repos/repo_123/restore-preview:discard", nil),
		httptest.NewRequest(http.MethodPost, "/internal/v1/repos/repo_123/restore-run", nil),
		httptest.NewRequest(http.MethodPost, "/internal/v1/repos/repo_123/restore:admit", nil),
	} {
		if metadata, ok := RouteMetadataForRequest(req); ok {
			t.Fatalf("legacy restore route %s matched metadata %#v, want no route", req.URL.Path, metadata)
		}
	}
}

func TestRouteRawDirectMountForbiddenMatcherCoversCompactSingleTokens(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "raw mount command", value: "rawmountcommand", want: "rawmountcommand"},
		{name: "direct mount", value: "directmount", want: "directmount"},
		{name: "break glass", value: "breakglass", want: "breakglass"},
		{name: "mount command", value: "mountcommand", want: "mountcommand"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := routeRawDirectMountForbiddenTokens(tt.value)
			if len(tokens) != 1 || tokens[0] != tt.want {
				t.Fatalf("route forbidden tokens for %q = %#v, want [%q]", tt.value, tokens, tt.want)
			}
		})
	}
}

func routeRawDirectMountForbiddenTokens(values ...string) []string {
	delimitedTokens := []string{"direct", "raw", "juicefs", "break-glass", "mount-command"}
	compactTokens := []string{"rawmountcommand", "directmount", "breakglass", "mountcommand", "juicefs"}
	seen := make(map[string]bool)
	var found []string
	for _, value := range values {
		normalized := normalizeRouteForbiddenTokenText(value)
		foundDelimited := false
		for _, token := range delimitedTokens {
			if !routeContainsDelimitedForbiddenToken(normalized, token) {
				continue
			}
			if !seen[token] {
				seen[token] = true
				found = append(found, token)
			}
			foundDelimited = true
		}
		if foundDelimited {
			continue
		}
		compact := strings.ReplaceAll(normalized, "-", "")
		for _, token := range compactTokens {
			search := compact
			if token == "mountcommand" {
				search = strings.ReplaceAll(search, "rawmountcommand", "")
			}
			if !strings.Contains(search, token) {
				continue
			}
			if !seen[token] {
				seen[token] = true
				found = append(found, token)
			}
		}
	}
	return found
}

func routeContainsDelimitedForbiddenToken(value, token string) bool {
	for _, part := range strings.FieldsFunc(value, func(r rune) bool { return r == '-' || r == '/' || r == ':' || r == '.' }) {
		if part == token {
			return true
		}
	}
	return strings.Contains("-"+value+"-", "-"+token+"-")
}

func normalizeRouteForbiddenTokenText(value string) string {
	var builder strings.Builder
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if ch >= 'A' && ch <= 'Z' {
			if i > 0 {
				builder.WriteByte('-')
			}
			ch += 'a' - 'A'
		}
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
			builder.WriteByte(ch)
		} else {
			builder.WriteByte('-')
		}
	}
	return strings.Trim(builder.String(), "-")
}

func TestInternalV1RouteMetadataTemplatePathsExtractExpectedParams(t *testing.T) {
	expectedValues := map[string]string{
		"volumeId":       "vol_123",
		"namespaceId":    "ns_123",
		"repoId":         "repo_123",
		"templateId":     "template_123",
		"exportId":       "export_123",
		"mountBindingId": "wmb_123",
		"operationId":    "op_123",
	}

	for _, route := range InternalV1RouteMetadata() {
		if !strings.Contains(route.Path, "{") {
			continue
		}
		t.Run(route.OperationID, func(t *testing.T) {
			path := concretePathForRoute(t, route.Path)
			params, ok := RoutePathParams(route.Path, path)
			if !ok {
				t.Fatalf("RoutePathParams(%q, %q) ok = false, want true", route.Path, path)
			}
			for name, want := range expectedValues {
				if !strings.Contains(route.Path, "{"+name+"}") {
					continue
				}
				if got := params[name]; got != want {
					t.Fatalf("params[%q] = %q, want %q in %#v", name, got, want, params)
				}
			}
			if metadata, ok := RouteMetadataForRequest(httptest.NewRequest(route.Method, path, nil)); !ok || metadata.OperationID != route.OperationID {
				t.Fatalf("RouteMetadataForRequest = %#v/%v, want %s", metadata, ok, route.OperationID)
			}
		})
	}
}
