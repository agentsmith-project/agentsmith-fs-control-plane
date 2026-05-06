package api

import (
	"context"
	"encoding/json"
	"net/http"
)

const (
	CapabilityStorage       = "storage"
	CapabilityJVS           = "jvs"
	CapabilityWebDAVExport  = "webdav_export"
	CapabilityWorkloadMount = "workload_mount"
)

var requiredReadinessCapabilities = []string{
	CapabilityStorage,
	CapabilityJVS,
	CapabilityWebDAVExport,
	CapabilityWorkloadMount,
}

type HealthResponse struct {
	Status string `json:"status"`
}

type ReadinessResponse struct {
	Status               string                    `json:"status"`
	Ready                bool                      `json:"ready"`
	Capabilities         map[string]CapabilityGate `json:"capabilities"`
	RequiredCapabilities []string                  `json:"-"`
}

type CapabilityGate struct {
	Enabled bool   `json:"enabled"`
	Ready   bool   `json:"ready"`
	Gated   bool   `json:"gated"`
	Reason  string `json:"reason"`
}

func HealthHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = writeJSON(w, http.StatusOK, HealthResponse{Status: "ok"})
	})
}

func NeutralReadiness() ReadinessResponse {
	disabled := CapabilityGate{
		Enabled: false,
		Ready:   false,
		Gated:   true,
		Reason:  "neutral_api_shell",
	}

	return ReadinessResponse{
		Status: "not_ready",
		Ready:  false,
		Capabilities: map[string]CapabilityGate{
			CapabilityStorage:       disabled,
			CapabilityJVS:           disabled,
			CapabilityWebDAVExport:  disabled,
			CapabilityWorkloadMount: disabled,
		},
	}
}

func ReadinessHandler(readiness ReadinessResponse) http.Handler {
	return ReadinessHandlerFunc(func(context.Context) ReadinessResponse {
		return readiness
	})
}

func ReadinessHandlerFunc(provider func(context.Context) ReadinessResponse) http.Handler {
	if provider == nil {
		provider = func(context.Context) ReadinessResponse {
			return NeutralReadiness()
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		readiness := effectiveReadiness(provider(r.Context()))
		status := http.StatusOK
		if !readiness.Ready {
			status = http.StatusServiceUnavailable
		}

		_ = writeJSON(w, status, readiness)
	})
}

func effectiveReadiness(readiness ReadinessResponse) ReadinessResponse {
	requiredCapabilities := readinessRequiredCapabilities(readiness)
	capabilities := make(map[string]CapabilityGate, len(readiness.Capabilities)+len(requiredCapabilities))
	for capability, gate := range readiness.Capabilities {
		capabilities[capability] = gate
	}

	ready := true
	for _, capability := range requiredCapabilities {
		gate, ok := capabilities[capability]
		if !ok {
			capabilities[capability] = CapabilityGate{
				Enabled: false,
				Ready:   false,
				Gated:   true,
				Reason:  "missing_required_capability",
			}
			ready = false
			continue
		}
		if !gate.Enabled || !gate.Ready || gate.Gated {
			ready = false
		}
	}

	readiness.Capabilities = capabilities
	readiness.Ready = ready
	if ready {
		readiness.Status = "ready"
	} else {
		readiness.Status = "not_ready"
	}
	return readiness
}

func readinessRequiredCapabilities(readiness ReadinessResponse) []string {
	if readiness.RequiredCapabilities != nil {
		return readiness.RequiredCapabilities
	}
	return requiredReadinessCapabilities
}

func writeJSON(w http.ResponseWriter, status int, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, err = w.Write(body)
	return err
}
