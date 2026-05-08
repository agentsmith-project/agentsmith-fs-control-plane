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

func TestCapabilityAdmissionOperationCoverageContract(t *testing.T) {
	tests := []struct {
		capabilityID      ID
		wantDefaultGA     bool
		wantAdmissionOps  []operations.OperationType
		wantOptionalGated bool
	}{
		{
			capabilityID:     WebDAVExport,
			wantDefaultGA:    true,
			wantAdmissionOps: []operations.OperationType{operations.OperationExportCreate},
		},
		{
			capabilityID:      WorkloadMount,
			wantAdmissionOps:  []operations.OperationType{operations.OperationMountBindingCreate},
			wantOptionalGated: true,
		},
		{
			capabilityID:      RepoTemplate,
			wantAdmissionOps:  []operations.OperationType{operations.OperationTemplateCreate, operations.OperationTemplateClone},
			wantOptionalGated: true,
		},
		{
			capabilityID:      RepoPurge,
			wantAdmissionOps:  []operations.OperationType{operations.OperationRepoPurge},
			wantOptionalGated: true,
		},
	}

	for _, tt := range tests {
		t.Run(string(tt.capabilityID), func(t *testing.T) {
			got := AdmissionOperationTypesForCapability(tt.capabilityID)
			if !operationTypeSlicesEqual(got, tt.wantAdmissionOps) {
				t.Fatalf("AdmissionOperationTypesForCapability(%s) = %#v, want %#v", tt.capabilityID, got, tt.wantAdmissionOps)
			}
			if gotDefaultGA := RequiredForDefaultGA(tt.capabilityID); gotDefaultGA != tt.wantDefaultGA {
				t.Fatalf("RequiredForDefaultGA(%s) = %v, want %v", tt.capabilityID, gotDefaultGA, tt.wantDefaultGA)
			}
			if tt.wantOptionalGated && RequiredForDefaultGA(tt.capabilityID) {
				t.Fatalf("%s optional gated contract cannot be default GA required", tt.capabilityID)
			}
		})
	}
}

func operationTypeSlicesEqual(got, want []operations.OperationType) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
