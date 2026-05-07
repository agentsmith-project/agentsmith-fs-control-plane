package workloadmount

import (
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
)

func TestValidateMountPathCorpus(t *testing.T) {
	valid := []string{
		"/mnt/repo",
		"/workspace/data-1",
		"/var/lib/app/data",
	}
	for _, path := range valid {
		t.Run("valid "+path, func(t *testing.T) {
			if err := ValidateMountPath(path); err != nil {
				t.Fatalf("ValidateMountPath(%q) error = %v, want nil", path, err)
			}
		})
	}

	invalid := []struct {
		name string
		path string
	}{
		{name: "empty", path: ""},
		{name: "whitespace prefixed", path: " repo"},
		{name: "relative", path: "repo"},
		{name: "root", path: "/"},
		{name: "empty segment", path: "/mnt//repo"},
		{name: "dot segment", path: "/mnt/."},
		{name: "dot dot segment", path: "/mnt/.."},
		{name: "traversal", path: "/mnt/repo/../secret"},
		{name: "backslash", path: "/mnt\\repo"},
		{name: "proc root", path: "/proc"},
		{name: "proc child", path: "/proc/1"},
		{name: "sys root", path: "/sys"},
		{name: "dev root", path: "/dev"},
		{name: "run secrets", path: "/run/secrets"},
		{name: "var run secrets", path: "/var/run/secrets"},
		{name: "newline", path: "/mnt/repo\nsecret"},
		{name: "control char", path: "/mnt/repo\x01secret"},
	}
	for _, tt := range invalid {
		t.Run("invalid "+tt.name, func(t *testing.T) {
			if err := ValidateMountPath(tt.path); err == nil {
				t.Fatalf("ValidateMountPath(%q) error = nil, want rejection", tt.path)
			}
		})
	}
}

func TestBindingValidateRejectsInvalidMountPathBeforeStatusAndLease(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	binding := Binding{
		ID:             "wmb_123",
		NamespaceID:    "ns_123",
		RepoID:         "repo_123",
		VolumeID:       "vol_123",
		MountPath:      "/proc",
		Status:         sessionstate.MountStatus("accepted-by-status-would-be-bad"),
		LeaseSeconds:   0,
		LeaseExpiresAt: now.Add(time.Hour),
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	err := binding.Validate()
	if err == nil {
		t.Fatal("Binding.Validate() error = nil, want invalid mount path")
	}
	if !strings.Contains(err.Error(), "mount_path") {
		t.Fatalf("Binding.Validate() error = %v, want mount_path rejection before status/lease validation", err)
	}
}
