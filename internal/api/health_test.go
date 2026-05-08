package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthHandlerReturnsOK(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)

	HealthHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected application/json Content-Type, got %q", got)
	}

	var body HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("health response did not decode: %v", err)
	}
	if body.Status != "ok" {
		t.Fatalf("expected ok health status, got %q", body.Status)
	}
}

func TestNeutralReadinessHandlerReportsNotReadyAndDisabledGates(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)

	ReadinessHandler(NeutralReadiness()).ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d", http.StatusServiceUnavailable, rec.Code)
	}

	var body ReadinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("readiness response did not decode: %v", err)
	}
	if body.Ready {
		t.Fatalf("neutral API shell must not report ready")
	}
	if body.Status != "not_ready" {
		t.Fatalf("expected not_ready status, got %q", body.Status)
	}

	for _, capability := range []string{
		CapabilityStorage,
		CapabilityJVS,
		CapabilityWebDAVExport,
		CapabilityWorkloadMount,
	} {
		state, ok := body.Capabilities[capability]
		if !ok {
			t.Fatalf("missing capability gate %q", capability)
		}
		if state.Enabled {
			t.Fatalf("capability %q must be disabled in neutral shell", capability)
		}
		if state.Ready {
			t.Fatalf("capability %q must not be ready in neutral shell", capability)
		}
		if !state.Gated {
			t.Fatalf("capability %q must be explicitly gated", capability)
		}
	}
}

func TestReadinessHandlerFailsClosedWhenRequiredCapabilityIsNotEffectiveReady(t *testing.T) {
	for _, tc := range []struct {
		name         string
		capabilities map[string]CapabilityGate
	}{
		{
			name: "missing",
			capabilities: func() map[string]CapabilityGate {
				capabilities := readyCapabilities()
				delete(capabilities, CapabilityStorage)
				return capabilities
			}(),
		},
		{
			name: "disabled",
			capabilities: func() map[string]CapabilityGate {
				capabilities := readyCapabilities()
				capabilities[CapabilityStorage] = CapabilityGate{Enabled: false, Ready: true, Gated: false}
				return capabilities
			}(),
		},
		{
			name: "unready",
			capabilities: func() map[string]CapabilityGate {
				capabilities := readyCapabilities()
				capabilities[CapabilityStorage] = CapabilityGate{Enabled: true, Ready: false, Gated: false}
				return capabilities
			}(),
		},
		{
			name: "gated",
			capabilities: func() map[string]CapabilityGate {
				capabilities := readyCapabilities()
				capabilities[CapabilityStorage] = CapabilityGate{Enabled: true, Ready: true, Gated: true}
				return capabilities
			}(),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
			readiness := ReadinessResponse{
				Status:       "ready",
				Ready:        true,
				Capabilities: tc.capabilities,
			}

			ReadinessHandler(readiness).ServeHTTP(rec, req)

			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("expected status %d, got %d", http.StatusServiceUnavailable, rec.Code)
			}

			var body ReadinessResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("readiness response did not decode: %v", err)
			}
			if body.Ready {
				t.Fatalf("readiness must be derived as false when required capability is %s", tc.name)
			}
			if body.Status != "not_ready" {
				t.Fatalf("expected normalized not_ready status, got %q", body.Status)
			}
			storage, ok := body.Capabilities[CapabilityStorage]
			if !ok {
				t.Fatalf("normalized payload missing required capability %q", CapabilityStorage)
			}
			if storage.Enabled && storage.Ready && !storage.Gated {
				t.Fatalf("storage gate unexpectedly normalized as effective ready: %#v", storage)
			}
		})
	}
}

func TestReadinessHandlerDerivesReadyWhenAllRequiredCapabilitiesAreEffectiveReady(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	readiness := ReadinessResponse{
		Status:       "not_ready",
		Ready:        false,
		Capabilities: readyCapabilities(),
	}

	ReadinessHandler(readiness).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var body ReadinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("readiness response did not decode: %v", err)
	}
	if !body.Ready {
		t.Fatalf("readiness must be derived as true when all required capabilities are effective ready")
	}
	if body.Status != "ready" {
		t.Fatalf("expected normalized ready status, got %q", body.Status)
	}
}

func TestReadinessHandlerUsesInjectedRequiredCapabilities(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	readiness := ReadinessResponse{
		Status:               "not_ready",
		Ready:                false,
		RequiredCapabilities: []string{CapabilityStorage, CapabilityJVS},
		Capabilities: map[string]CapabilityGate{
			CapabilityStorage:      {Enabled: true, Ready: true, Gated: false},
			CapabilityJVS:          {Enabled: true, Ready: true, Gated: false},
			CapabilityWebDAVExport: {Enabled: true, Ready: false, Gated: true, Reason: "handler_not_implemented"},
			CapabilityWorkloadMount: {
				Enabled: true,
				Ready:   false,
				Gated:   true,
				Reason:  "handler_not_implemented",
			},
		},
	}

	ReadinessHandler(readiness).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var body ReadinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("readiness response did not decode: %v", err)
	}
	if !body.Ready {
		t.Fatalf("readiness must be true when only non-required capabilities are gated")
	}
	if gate := body.Capabilities[CapabilityWebDAVExport]; !gate.Enabled || gate.Ready || !gate.Gated || gate.Reason != "handler_not_implemented" {
		t.Fatalf("webdav gate = %#v, want gated not implemented", gate)
	}
}

func TestReadinessHandlerSerializesSeparateRequirementSemantics(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	readiness := ReadinessResponse{
		Status:               "not_ready",
		Ready:                false,
		RequiredCapabilities: []string{CapabilityStorage},
		Capabilities: map[string]CapabilityGate{
			CapabilityStorage:      {Enabled: true, Ready: true},
			CapabilityJVS:          {Enabled: true, Ready: true},
			CapabilityWebDAVExport: {Enabled: true, Ready: false, Gated: true, Reason: "webdav_not_ready"},
			CapabilityWorkloadMount: {
				Enabled: false,
				Ready:   false,
				Gated:   true,
				Reason:  "mount_not_configured",
			},
		},
	}

	ReadinessHandler(readiness).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var body struct {
		Capabilities map[string]map[string]any `json:"capabilities"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("readiness response did not decode: %v", err)
	}
	if got := readinessBoolField(t, body.Capabilities[CapabilityStorage], "required_for_service_ready"); !got {
		t.Fatalf("storage required_for_service_ready = false, want true")
	}
	if got := readinessBoolField(t, body.Capabilities[CapabilityStorage], "optional_gated"); got {
		t.Fatalf("storage optional_gated = true, want false")
	}
	if got := readinessBoolField(t, body.Capabilities[CapabilityJVS], "required_for_service_ready"); got {
		t.Fatalf("jvs required_for_service_ready = true, want false")
	}
	if got := readinessBoolField(t, body.Capabilities[CapabilityWebDAVExport], "optional_gated"); !got {
		t.Fatalf("webdav optional_gated = false, want true for non-service-required gated capability")
	}
	if got := readinessBoolField(t, body.Capabilities[CapabilityWorkloadMount], "required_for_default_ga"); got {
		t.Fatalf("workload mount required_for_default_ga = true, want false by default")
	}
}

func readinessBoolField(t *testing.T, fields map[string]any, name string) bool {
	t.Helper()
	raw, ok := fields[name]
	if !ok {
		t.Fatalf("capability fields %#v missing %q", fields, name)
	}
	got, ok := raw.(bool)
	if !ok {
		t.Fatalf("capability field %q = %#v, want bool", name, raw)
	}
	return got
}

func readyCapabilities() map[string]CapabilityGate {
	ready := CapabilityGate{Enabled: true, Ready: true, Gated: false}
	return map[string]CapabilityGate{
		CapabilityStorage:       ready,
		CapabilityJVS:           ready,
		CapabilityWebDAVExport:  ready,
		CapabilityWorkloadMount: ready,
	}
}
