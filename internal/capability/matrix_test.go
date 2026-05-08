package capability

import (
	"testing"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
)

func TestAdmissionCapabilityForOperationTypeIsStable(t *testing.T) {
	tests := []struct {
		operationType operations.OperationType
		want          ID
	}{
		{operationType: operations.OperationExportCreate, want: WebDAVExport},
		{operationType: operations.OperationMountBindingCreate, want: WorkloadMount},
		{operationType: operations.OperationTemplateCreate, want: RepoTemplate},
		{operationType: operations.OperationTemplateClone, want: RepoTemplate},
		{operationType: operations.OperationRepoPurge, want: RepoPurge},
	}

	for _, tt := range tests {
		t.Run(tt.operationType.String(), func(t *testing.T) {
			got, ok := AdmissionCapabilityForOperationType(tt.operationType)
			if !ok {
				t.Fatalf("AdmissionCapabilityForOperationType(%s) missing, want %s", tt.operationType, tt.want)
			}
			if got != tt.want {
				t.Fatalf("AdmissionCapabilityForOperationType(%s) = %s, want %s", tt.operationType, got, tt.want)
			}
		})
	}
}

func TestDefaultGARequirementClassifiesCoreAndOptionalCapabilities(t *testing.T) {
	for _, id := range []ID{Storage, JVS, WebDAVExport} {
		if !RequiredForDefaultGA(id) {
			t.Fatalf("%s RequiredForDefaultGA = false, want true", id)
		}
	}
	for _, id := range []ID{WorkloadMount, RepoTemplate, RepoPurge} {
		if RequiredForDefaultGA(id) {
			t.Fatalf("%s RequiredForDefaultGA = true, want false", id)
		}
	}
}
