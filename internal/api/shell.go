package api

import (
	"net/http"
	"strings"
)

func NewNeutralShell() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/healthz", HealthHandler())
	mux.Handle("/readyz", ReadinessHandler(NeutralReadiness()))
	mux.Handle("/", neutralFallbackHandler())
	return mux
}

func CapabilityDeniedHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		envelope := NewErrorEnvelope(
			CodeCapabilityDenied,
			"storage-backed API capabilities are disabled in neutral shell",
			false,
			CorrelationIDFromRequest(r),
			nil,
			map[string]any{
				"disabled_capabilities": []string{
					CapabilityStorage,
					CapabilityJVS,
					CapabilityWebDAVExport,
					CapabilityWorkloadMount,
				},
			},
		)

		_ = WriteErrorEnvelope(w, http.StatusForbidden, envelope)
	})
}

func PathDeniedHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		envelope := NewErrorEnvelope(
			CodePathDenied,
			"route is not available",
			false,
			CorrelationIDFromRequest(r),
			nil,
			map[string]any{"route": "unmatched"},
		)

		_ = WriteErrorEnvelope(w, http.StatusNotFound, envelope)
	})
}

func neutralFallbackHandler() http.Handler {
	capabilityDenied := CapabilityDeniedHandler()
	pathDenied := PathDeniedHandler()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/v1" || strings.HasPrefix(r.URL.Path, "/internal/v1/") {
			capabilityDenied.ServeHTTP(w, r)
			return
		}

		pathDenied.ServeHTTP(w, r)
	})
}
