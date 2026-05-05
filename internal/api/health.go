package api

import (
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
	Status       string                    `json:"status"`
	Ready        bool                      `json:"ready"`
	Capabilities map[string]CapabilityGate `json:"capabilities"`
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
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		readiness := effectiveReadiness(readiness)
		status := http.StatusOK
		if !readiness.Ready {
			status = http.StatusServiceUnavailable
		}

		_ = writeJSON(w, status, readiness)
	})
}

func effectiveReadiness(readiness ReadinessResponse) ReadinessResponse {
	capabilities := make(map[string]CapabilityGate, len(readiness.Capabilities)+len(requiredReadinessCapabilities))
	for capability, gate := range readiness.Capabilities {
		capabilities[capability] = gate
	}

	ready := true
	for _, capability := range requiredReadinessCapabilities {
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
