package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/capability"
)

const (
	CapabilityStorage                = string(capability.Storage)
	CapabilityJVS                    = string(capability.JVS)
	CapabilityWebDAVExport           = string(capability.WebDAVExport)
	CapabilityWorkloadMount          = string(capability.WorkloadMount)
	CapabilityWorkloadMountBinding   = string(capability.WorkloadMountBinding)
	CapabilityWorkloadMountDiscovery = string(capability.WorkloadMountDiscovery)
	CapabilityWorkloadTeardownPlan   = string(capability.WorkloadTeardownPlan)
	CapabilityRepoTemplate           = string(capability.RepoTemplate)
	CapabilityRepoPurge              = string(capability.RepoPurge)
)

var requiredReadinessCapabilities = []string{
	CapabilityStorage,
	CapabilityJVS,
	CapabilityWebDAVExport,
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
	Enabled                 bool   `json:"enabled"`
	Ready                   bool   `json:"ready"`
	Gated                   bool   `json:"gated"`
	Reason                  string `json:"reason"`
	RequiredForServiceReady bool   `json:"required_for_service_ready"`
	RequiredForDefaultGA    bool   `json:"required_for_default_ga"`
	OptionalGated           bool   `json:"optional_gated"`
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
			CapabilityStorage:                disabled,
			CapabilityJVS:                    disabled,
			CapabilityWebDAVExport:           disabled,
			CapabilityWorkloadMountBinding:   disabled,
			CapabilityWorkloadMountDiscovery: disabled,
			CapabilityWorkloadTeardownPlan:   disabled,
			CapabilityRepoTemplate:           disabled,
			CapabilityRepoPurge:              disabled,
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

func ReadinessFromCapabilityMatrix(matrix capability.Matrix) ReadinessResponse {
	entries := matrix.Entries()
	capabilities := make(map[string]CapabilityGate, len(entries))
	requiredCapabilities := make([]string, 0, len(entries))
	for _, entry := range entries {
		id := string(entry.ID)
		capabilities[id] = CapabilityGate{
			Enabled:                 entry.Status.Enabled,
			Ready:                   entry.Status.Ready,
			Gated:                   entry.Status.Gated,
			Reason:                  entry.Status.Reason,
			RequiredForServiceReady: entry.Requirement.RequiredForServiceReady,
			RequiredForDefaultGA:    entry.Requirement.RequiredForDefaultGA,
			OptionalGated:           entry.Requirement.OptionalGated,
		}
		if entry.Requirement.RequiredForServiceReady {
			requiredCapabilities = append(requiredCapabilities, id)
		}
	}

	return ReadinessResponse{
		Status:               "not_ready",
		Ready:                false,
		Capabilities:         capabilities,
		RequiredCapabilities: requiredCapabilities,
	}
}

func effectiveReadiness(readiness ReadinessResponse) ReadinessResponse {
	requiredCapabilities := readinessRequiredCapabilities(readiness)
	requiredSet := map[string]bool{}
	for _, capability := range requiredCapabilities {
		requiredSet[capability] = true
	}
	capabilities := make(map[string]CapabilityGate, len(readiness.Capabilities)+len(requiredCapabilities))
	for capability, gate := range readiness.Capabilities {
		gate.RequiredForServiceReady = requiredSet[capability]
		gate.OptionalGated = !gate.RequiredForServiceReady && (gate.OptionalGated || gate.Gated)
		capabilities[capability] = gate
	}

	ready := true
	for _, capability := range requiredCapabilities {
		gate, ok := capabilities[capability]
		if !ok {
			capabilities[capability] = CapabilityGate{
				Enabled:                 false,
				Ready:                   false,
				Gated:                   true,
				Reason:                  "missing_required_capability",
				RequiredForServiceReady: true,
			}
			ready = false
			continue
		}
		gate.RequiredForServiceReady = true
		gate.OptionalGated = false
		capabilities[capability] = gate
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
	required := make([]string, 0, len(readiness.Capabilities))
	for capability, gate := range readiness.Capabilities {
		if gate.RequiredForServiceReady {
			required = append(required, capability)
		}
	}
	if len(required) > 0 {
		sort.Strings(required)
		return required
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
