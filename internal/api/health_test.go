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

func readyCapabilities() map[string]CapabilityGate {
	ready := CapabilityGate{Enabled: true, Ready: true, Gated: false}
	return map[string]CapabilityGate{
		CapabilityStorage:       ready,
		CapabilityJVS:           ready,
		CapabilityWebDAVExport:  ready,
		CapabilityWorkloadMount: ready,
	}
}
