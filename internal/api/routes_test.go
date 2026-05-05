package api

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
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
