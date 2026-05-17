package repoexec

import (
	"testing"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/jvsrunner"
)

func TestDirectDoctorAllowsOnlyCurrentCleanupWarningShape(t *testing.T) {
	validCleanupWarning := jvsrunner.DirectDoctorFindingSummary{Severity: "warning", Message: "direct restore cleanup pending"}
	tests := []struct {
		name   string
		doctor jvsrunner.DirectDoctorSummary
		want   bool
	}{
		{
			name:   "healthy clean",
			doctor: jvsrunner.DirectDoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, MetadataState: "ready", Journal: "clean", Recovery: "none"},
			want:   true,
		},
		{
			name:   "current cleanup warning",
			doctor: jvsrunner.DirectDoctorSummary{RepoID: "jvs_repo_alpha", Healthy: false, FindingCount: 1, Findings: []jvsrunner.DirectDoctorFindingSummary{validCleanupWarning}, MetadataState: "ready", Journal: "clean", Recovery: "cleanup_pending"},
			want:   true,
		},
		{
			name:   "cleanup pending without warning finding",
			doctor: jvsrunner.DirectDoctorSummary{RepoID: "jvs_repo_alpha", Healthy: false, FindingCount: 0, MetadataState: "ready", Journal: "clean", Recovery: "cleanup_pending"},
			want:   false,
		},
		{
			name:   "cleanup pending healthy true",
			doctor: jvsrunner.DirectDoctorSummary{RepoID: "jvs_repo_alpha", Healthy: true, FindingCount: 1, Findings: []jvsrunner.DirectDoctorFindingSummary{validCleanupWarning}, MetadataState: "ready", Journal: "clean", Recovery: "cleanup_pending"},
			want:   false,
		},
		{
			name:   "cleanup pending error finding",
			doctor: jvsrunner.DirectDoctorSummary{RepoID: "jvs_repo_alpha", Healthy: false, FindingCount: 1, Findings: []jvsrunner.DirectDoctorFindingSummary{{Code: "JVS_METADATA_INVALID", Severity: "error", Message: "direct restore cleanup pending"}}, MetadataState: "ready", Journal: "clean", Recovery: "cleanup_pending"},
			want:   false,
		},
		{
			name:   "cleanup pending unrelated warning",
			doctor: jvsrunner.DirectDoctorSummary{RepoID: "jvs_repo_alpha", Healthy: false, FindingCount: 1, Findings: []jvsrunner.DirectDoctorFindingSummary{{Severity: "warning", Message: "metadata warning"}}, MetadataState: "ready", Journal: "clean", Recovery: "cleanup_pending"},
			want:   false,
		},
		{
			name:   "cleanup pending extra warning",
			doctor: jvsrunner.DirectDoctorSummary{RepoID: "jvs_repo_alpha", Healthy: false, FindingCount: 2, Findings: []jvsrunner.DirectDoctorFindingSummary{validCleanupWarning, validCleanupWarning}, MetadataState: "ready", Journal: "clean", Recovery: "cleanup_pending"},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := directDoctorAllowsMutation(tt.doctor); got != tt.want {
				t.Fatalf("directDoctorAllowsMutation(%#v) = %v, want %v", tt.doctor, got, tt.want)
			}
		})
	}
}
