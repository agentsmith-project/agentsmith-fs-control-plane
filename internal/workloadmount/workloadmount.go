package workloadmount

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/pathresolver"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
)

const (
	DefaultLeaseSeconds = 3600
	MinLeaseSeconds     = 60
	MaxLeaseSeconds     = 86400
	MaxReasonLength     = 1024
)

type Binding struct {
	ID                   string
	NamespaceID          string
	RepoID               string
	VolumeID             string
	MountPath            string
	ReadOnly             bool
	Status               sessionstate.MountStatus
	LeaseSeconds         int
	LeaseExpiresAt       time.Time
	LastHeartbeatAt      *time.Time
	LastObservedAt       *time.Time
	ConfirmedUnmountedAt *time.Time
	UnableToWriteAt      *time.Time
	TerminalObservedAt   *time.Time
	StatusReason         string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type Plan struct {
	MountBindingID      string         `json:"mount_binding_id"`
	VolumeID            string         `json:"volume_id"`
	PayloadVolumeSubdir string         `json:"payload_volume_subdir"`
	MountPath           string         `json:"mount_path"`
	ReadOnly            bool           `json:"read_only"`
	SecretRef           SecretRef      `json:"secret_ref"`
	SecurityPolicy      SecurityPolicy `json:"security_policy"`
}

type SecretRef struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

type SecurityPolicy struct {
	RunAsNonRoot             bool `json:"run_as_non_root"`
	AllowPrivileged          bool `json:"allow_privileged"`
	JVSControlOutsidePayload bool `json:"jvs_control_outside_payload"`
}

type CommitRequest struct {
	Binding Binding
	Status  sessionstate.MountStatus
	Reason  string
	Now     time.Time
}

func (binding Binding) Validate() error {
	if err := pathresolver.ValidateID(pathresolver.WorkloadMountBindingID, binding.ID); err != nil {
		return err
	}
	if err := pathresolver.ValidateID(pathresolver.NamespaceID, binding.NamespaceID); err != nil {
		return err
	}
	if err := pathresolver.ValidateID(pathresolver.RepoID, binding.RepoID); err != nil {
		return err
	}
	if err := pathresolver.ValidateID(pathresolver.VolumeID, binding.VolumeID); err != nil {
		return err
	}
	if err := ValidateMountPath(binding.MountPath); err != nil {
		return err
	}
	if !ValidStatus(binding.Status) {
		return fmt.Errorf("invalid workload mount status %q", binding.Status)
	}
	if binding.LeaseSeconds < MinLeaseSeconds || binding.LeaseSeconds > MaxLeaseSeconds {
		return fmt.Errorf("invalid workload mount lease seconds")
	}
	if binding.LeaseExpiresAt.IsZero() || binding.CreatedAt.IsZero() || binding.UpdatedAt.IsZero() {
		return fmt.Errorf("workload mount timestamps must be set")
	}
	return nil
}

func ValidateMountPath(path string) error {
	if path == "" || strings.TrimSpace(path) != path {
		return fmt.Errorf("mount_path must be a non-empty absolute container path")
	}
	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("mount_path must be absolute")
	}
	if path == "/" {
		return fmt.Errorf("mount_path must not be root")
	}
	if strings.Contains(path, "\\") {
		return fmt.Errorf("mount_path must not contain backslashes")
	}
	for _, r := range path {
		if unicode.IsControl(r) {
			return fmt.Errorf("mount_path must not contain control characters")
		}
	}
	parts := strings.Split(path, "/")
	for _, part := range parts[1:] {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("mount_path must be clean")
		}
	}
	clean := "/" + strings.Join(parts[1:], "/")
	if clean != path {
		return fmt.Errorf("mount_path must be clean")
	}
	for _, reserved := range []string{"/proc", "/sys", "/dev", "/run/secrets", "/var/run/secrets"} {
		if path == reserved || strings.HasPrefix(path, reserved+"/") {
			return fmt.Errorf("mount_path uses a runtime reserved path")
		}
	}
	return nil
}

func ValidateSecretRef(ref SecretRef) error {
	if !validSecretRefDNSLabel(ref.Namespace, 63) || !validSecretRefDNSSubdomain(ref.Name, 253) {
		return fmt.Errorf("workload mount secret_ref must contain a valid namespace and name")
	}
	return nil
}

func validSecretRefDNSSubdomain(value string, maxLen int) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxLen {
		return false
	}
	for _, part := range strings.Split(value, ".") {
		if !validSecretRefDNSLabel(part, 63) {
			return false
		}
	}
	return true
}

func validSecretRefDNSLabel(value string, maxLen int) bool {
	if value == "" || len(value) > maxLen {
		return false
	}
	for idx, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' && idx > 0 && idx < len(value)-1:
		default:
			return false
		}
	}
	return true
}

func ValidStatus(status sessionstate.MountStatus) bool {
	switch status {
	case sessionstate.MountStatusIssued,
		sessionstate.MountStatusPending,
		sessionstate.MountStatusActive,
		sessionstate.MountStatusReleasing,
		sessionstate.MountStatusReleased,
		sessionstate.MountStatusRevoked,
		sessionstate.MountStatusExpired,
		sessionstate.MountStatusFailed:
		return true
	default:
		return false
	}
}

func ValidOrchestratorStatus(status sessionstate.MountStatus) bool {
	switch status {
	case sessionstate.MountStatusPending,
		sessionstate.MountStatusActive,
		sessionstate.MountStatusReleased,
		sessionstate.MountStatusRevoked,
		sessionstate.MountStatusExpired,
		sessionstate.MountStatusFailed:
		return true
	default:
		return false
	}
}

func Terminal(status sessionstate.MountStatus) bool {
	switch status {
	case sessionstate.MountStatusReleased,
		sessionstate.MountStatusRevoked,
		sessionstate.MountStatusExpired,
		sessionstate.MountStatusFailed:
		return true
	default:
		return false
	}
}

type PlanFreshnessDecision string

const (
	PlanFreshnessAllowIssuance  PlanFreshnessDecision = "allow_issuance"
	PlanFreshnessStaleIssuance  PlanFreshnessDecision = "stale_issuance"
	PlanFreshnessAllowTeardown  PlanFreshnessDecision = "allow_teardown"
	PlanFreshnessNoOrdinaryPlan PlanFreshnessDecision = "no_ordinary_plan"
)

func BindingPlanFreshnessDecision(binding Binding, now time.Time) PlanFreshnessDecision {
	switch binding.Status {
	case sessionstate.MountStatusIssued,
		sessionstate.MountStatusPending,
		sessionstate.MountStatusActive:
		if binding.LeaseExpiresAt.IsZero() || !binding.LeaseExpiresAt.After(now) {
			return PlanFreshnessStaleIssuance
		}
		return PlanFreshnessAllowIssuance
	case sessionstate.MountStatusReleasing:
		return PlanFreshnessAllowTeardown
	default:
		return PlanFreshnessNoOrdinaryPlan
	}
}

func ClonePolicy(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
