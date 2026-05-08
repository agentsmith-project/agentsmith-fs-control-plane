package capability

import (
	"testing"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
)

func TestCapabilityMatrixV1RowsMatchHandoffVocabulary(t *testing.T) {
	rows := CapabilityMatrixV1Rows()
	rowsByID := capabilityMatrixV1RowsByID(t)

	tests := []struct {
		id                ID
		surfaceType       SurfaceType
		defaultGARequired bool
		optionalGated     bool
	}{
		{id: NamespaceBinding, surfaceType: SurfaceDurableOperation, defaultGARequired: true},
		{id: VolumePreflight, surfaceType: SurfacePreflight, defaultGARequired: true},
		{id: AdminBootstrap, surfaceType: SurfacePreflight, defaultGARequired: true},
		{id: CallerPolicyReadiness, surfaceType: SurfaceDiscovery, defaultGARequired: true},
		{id: PathRedaction, surfaceType: SurfaceRedaction, defaultGARequired: true},
		{id: RepoCreate, surfaceType: SurfaceDurableOperation, defaultGARequired: true},
		{id: RepoProjection, surfaceType: SurfaceReadProjection, defaultGARequired: true},
		{id: JVSSaveRestore, surfaceType: SurfaceDurableOperation, defaultGARequired: true},
		{id: JVSProjection, surfaceType: SurfaceReadProjection, defaultGARequired: true},
		{id: WebDAVExport, surfaceType: SurfaceDurableOperation, defaultGARequired: true},
		{id: WebDAVProjection, surfaceType: SurfaceReadProjection, defaultGARequired: true},
		{id: OperationRecovery, surfaceType: SurfaceRuntimeSupport, defaultGARequired: true},
		{id: RepoLifecycleRetained, surfaceType: SurfaceDurableOperation, defaultGARequired: true},
		{id: RepoPurge, surfaceType: SurfaceDurableOperation, optionalGated: true},
		{id: RepoTemplate, surfaceType: SurfaceDurableOperation, optionalGated: true},
		{id: WorkloadMountBinding, surfaceType: SurfaceDurableOperation, optionalGated: true},
		{id: WorkloadMountDiscovery, surfaceType: SurfaceDiscovery, optionalGated: true},
		{id: WorkloadTeardownPlan, surfaceType: SurfaceDiscovery, optionalGated: true},
	}

	allowedSurfaceTypes := map[SurfaceType]bool{
		SurfaceDurableOperation: true,
		SurfaceReadProjection:   true,
		SurfacePreflight:        true,
		SurfaceDiscovery:        true,
		SurfaceRedaction:        true,
		SurfaceRuntimeSupport:   true,
	}

	for _, tt := range tests {
		t.Run(string(tt.id), func(t *testing.T) {
			row, ok := rowsByID[tt.id]
			if !ok {
				t.Fatalf("CapabilityMatrixV1Rows missing %s", tt.id)
			}
			if row.SurfaceType != tt.surfaceType {
				t.Fatalf("%s SurfaceType = %q, want %q", tt.id, row.SurfaceType, tt.surfaceType)
			}
			if row.DefaultGARequired != tt.defaultGARequired {
				t.Fatalf("%s DefaultGARequired = %v, want %v", tt.id, row.DefaultGARequired, tt.defaultGARequired)
			}
			if row.OptionalGated != tt.optionalGated {
				t.Fatalf("%s OptionalGated = %v, want %v", tt.id, row.OptionalGated, tt.optionalGated)
			}
			if !allowedSurfaceTypes[row.SurfaceType] {
				t.Fatalf("%s SurfaceType = %q, want one of the handoff v1 surface vocabulary", tt.id, row.SurfaceType)
			}
			if row.SurfaceType != SurfaceDurableOperation && len(row.AdmissionOperationTypes) != 0 {
				t.Fatalf("%s has AdmissionOperationTypes on non-durable surface %q: %#v", tt.id, row.SurfaceType, row.AdmissionOperationTypes)
			}
		})
	}
	if got, want := len(rows), len(tests); got != want {
		t.Fatalf("CapabilityMatrixV1Rows length = %d, want exactly documented vocabulary length %d", got, want)
	}
	for index, tt := range tests {
		if rows[index].ID != tt.id {
			t.Fatalf("CapabilityMatrixV1Rows[%d].ID = %s, want handoff order %s", index, rows[index].ID, tt.id)
		}
	}
}

func TestCapabilityMatrixV1ExcludesLegacyCoarseCapabilities(t *testing.T) {
	rows := capabilityMatrixV1RowsByID(t)
	for _, legacyID := range []ID{Storage, JVS, WorkloadMount} {
		if _, ok := rows[legacyID]; ok {
			t.Fatalf("CapabilityMatrixV1Rows includes legacy compatibility id %s", legacyID)
		}
	}
}

func TestCapabilityMatrixV1SplitsJVSAndWebDAVProjectionFacets(t *testing.T) {
	rows := capabilityMatrixV1RowsByID(t)

	jvsDurable := rows[JVSSaveRestore]
	if !operationTypeSlicesEqual(jvsDurable.AdmissionOperationTypes, []operations.OperationType{
		operations.OperationSavePointCreate,
		operations.OperationRestorePreviewDiscard,
		operations.OperationRestoreRun,
	}) {
		t.Fatalf("%s AdmissionOperationTypes = %#v, want save point create, restore preview discard, and restore run only", JVSSaveRestore, jvsDurable.AdmissionOperationTypes)
	}
	if operationTypeSliceContains(jvsDurable.AdmissionOperationTypes, operations.OperationRestorePreview) {
		t.Fatalf("%s must not mix restore preview projection into durable admission operations", JVSSaveRestore)
	}
	jvsProjection := rows[JVSProjection]
	if jvsProjection.SurfaceType != SurfaceReadProjection || len(jvsProjection.AdmissionOperationTypes) != 0 {
		t.Fatalf("%s row = %#v, want read projection with no admission operations", JVSProjection, jvsProjection)
	}

	webDAVDurable := rows[WebDAVExport]
	if !operationTypeSlicesEqual(webDAVDurable.AdmissionOperationTypes, []operations.OperationType{
		operations.OperationExportCreate,
		operations.OperationExportRevoke,
	}) {
		t.Fatalf("%s AdmissionOperationTypes = %#v, want export create and revoke only", WebDAVExport, webDAVDurable.AdmissionOperationTypes)
	}
	webDAVProjection := rows[WebDAVProjection]
	if webDAVProjection.SurfaceType != SurfaceReadProjection || len(webDAVProjection.AdmissionOperationTypes) != 0 {
		t.Fatalf("%s row = %#v, want read projection with no admission operations", WebDAVProjection, webDAVProjection)
	}
}

func TestCapabilityMatrixV1SplitsWorkloadBindingDiscoveryAndTeardownPlanFacets(t *testing.T) {
	rows := capabilityMatrixV1RowsByID(t)

	binding := rows[WorkloadMountBinding]
	if binding.SurfaceType != SurfaceDurableOperation || !binding.OptionalGated || binding.DefaultGARequired {
		t.Fatalf("%s row = %#v, want optional durable binding facet", WorkloadMountBinding, binding)
	}
	wantBindingAdmission := []operations.OperationType{
		operations.OperationMountBindingCreate,
		operations.OperationMountBindingStatusUpdate,
		operations.OperationMountBindingHeartbeat,
		operations.OperationMountBindingRelease,
		operations.OperationMountBindingRevoke,
	}
	if !operationTypeSlicesEqual(binding.AdmissionOperationTypes, wantBindingAdmission) {
		t.Fatalf("%s AdmissionOperationTypes = %#v, want %#v", WorkloadMountBinding, binding.AdmissionOperationTypes, wantBindingAdmission)
	}

	for _, id := range []ID{WorkloadMountDiscovery, WorkloadTeardownPlan} {
		row := rows[id]
		if row.SurfaceType != SurfaceDiscovery || !row.OptionalGated || row.DefaultGARequired {
			t.Fatalf("%s row = %#v, want optional discovery facet", id, row)
		}
		if len(row.AdmissionOperationTypes) != 0 {
			t.Fatalf("%s AdmissionOperationTypes = %#v, want none", id, row.AdmissionOperationTypes)
		}
	}
}

func TestCapabilityMatrixV1WebDAVExportAdmissionIncludesCreateAndRevoke(t *testing.T) {
	row, ok := CapabilityMatrixV1Row(WebDAVExport)
	if !ok {
		t.Fatalf("CapabilityMatrixV1Rows missing %s", WebDAVExport)
	}
	for _, operationType := range []operations.OperationType{operations.OperationExportCreate, operations.OperationExportRevoke} {
		if !operationTypeSliceContains(row.AdmissionOperationTypes, operationType) {
			t.Fatalf("%s AdmissionOperationTypes = %#v, want %s", WebDAVExport, row.AdmissionOperationTypes, operationType)
		}
		got, ok := AdmissionCapabilityForOperationType(operationType)
		if !ok {
			t.Fatalf("AdmissionCapabilityForOperationType(%s) missing", operationType)
		}
		if got != WebDAVExport {
			t.Fatalf("AdmissionCapabilityForOperationType(%s) = %s, want %s", operationType, got, WebDAVExport)
		}
	}
}

func TestAdmissionCapabilityForOperationTypeCoversEveryMatrixAdmissionOperation(t *testing.T) {
	for _, row := range CapabilityMatrixV1Rows() {
		t.Run(string(row.ID), func(t *testing.T) {
			for _, operationType := range row.AdmissionOperationTypes {
				got, ok := AdmissionCapabilityForOperationType(operationType)
				if !ok {
					t.Fatalf("AdmissionCapabilityForOperationType(%s) missing for %s", operationType, row.ID)
				}
				if got != row.ID {
					t.Fatalf("AdmissionCapabilityForOperationType(%s) = %s, want %s", operationType, got, row.ID)
				}
			}
			if got := AdmissionOperationTypesForCapability(row.ID); !operationTypeSlicesEqual(got, row.AdmissionOperationTypes) {
				t.Fatalf("AdmissionOperationTypesForCapability(%s) = %#v, want row admission ops %#v", row.ID, got, row.AdmissionOperationTypes)
			}
		})
	}
}

func TestCapabilityMatrixV1TeardownOnlyPlanDoesNotBypassNewMutationDenial(t *testing.T) {
	row, ok := CapabilityMatrixV1Row(WorkloadTeardownPlan)
	if !ok {
		t.Fatalf("CapabilityMatrixV1Rows missing %s", WorkloadTeardownPlan)
	}
	if len(row.AdmissionOperationTypes) != 0 {
		t.Fatalf("%s AdmissionOperationTypes = %#v, want no admission gate for the teardown-only plan facet", WorkloadTeardownPlan, row.AdmissionOperationTypes)
	}
	if got := AdmissionOperationTypesForCapability(WorkloadTeardownPlan); len(got) != 0 {
		t.Fatalf("AdmissionOperationTypesForCapability(%s) = %#v, want none", WorkloadTeardownPlan, got)
	}
	if got := TeardownOperationTypesForCapability(WorkloadTeardownPlan); len(got) != 0 {
		t.Fatalf("TeardownOperationTypesForCapability(%s) = %#v, want none; release/revoke API mutations are binding admissions", WorkloadTeardownPlan, got)
	}
	for _, operationType := range []operations.OperationType{
		operations.OperationMountBindingCreate,
		operations.OperationMountBindingStatusUpdate,
		operations.OperationMountBindingHeartbeat,
		operations.OperationMountBindingRelease,
		operations.OperationMountBindingRevoke,
	} {
		got, ok := AdmissionCapabilityForOperationType(operationType)
		if !ok {
			t.Fatalf("AdmissionCapabilityForOperationType(%s) missing", operationType)
		}
		if got != WorkloadMountBinding {
			t.Fatalf("AdmissionCapabilityForOperationType(%s) = %s, want %s; teardown plan must not bypass new mutation denial", operationType, got, WorkloadMountBinding)
		}
	}
}

func TestAdmissionCapabilityForOperationTypeIsStable(t *testing.T) {
	tests := []struct {
		operationType operations.OperationType
		want          ID
	}{
		{operationType: operations.OperationNamespaceUpsert, want: NamespaceBinding},
		{operationType: operations.OperationNamespaceDisable, want: NamespaceBinding},
		{operationType: operations.OperationNamespaceVolumeBindingPut, want: NamespaceBinding},
		{operationType: operations.OperationRepoCreate, want: RepoCreate},
		{operationType: operations.OperationSavePointCreate, want: JVSSaveRestore},
		{operationType: operations.OperationRestorePreviewDiscard, want: JVSSaveRestore},
		{operationType: operations.OperationRestoreRun, want: JVSSaveRestore},
		{operationType: operations.OperationExportCreate, want: WebDAVExport},
		{operationType: operations.OperationExportRevoke, want: WebDAVExport},
		{operationType: operations.OperationRepoArchive, want: RepoLifecycleRetained},
		{operationType: operations.OperationRepoRestoreArchived, want: RepoLifecycleRetained},
		{operationType: operations.OperationRepoDelete, want: RepoLifecycleRetained},
		{operationType: operations.OperationRepoRestoreTombstoned, want: RepoLifecycleRetained},
		{operationType: operations.OperationMountBindingCreate, want: WorkloadMountBinding},
		{operationType: operations.OperationMountBindingStatusUpdate, want: WorkloadMountBinding},
		{operationType: operations.OperationMountBindingHeartbeat, want: WorkloadMountBinding},
		{operationType: operations.OperationMountBindingRelease, want: WorkloadMountBinding},
		{operationType: operations.OperationMountBindingRevoke, want: WorkloadMountBinding},
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
	for _, id := range []ID{
		Storage,
		JVS,
		NamespaceBinding,
		VolumePreflight,
		AdminBootstrap,
		CallerPolicyReadiness,
		PathRedaction,
		RepoCreate,
		RepoProjection,
		JVSSaveRestore,
		JVSProjection,
		WebDAVExport,
		WebDAVProjection,
		OperationRecovery,
		RepoLifecycleRetained,
	} {
		if !RequiredForDefaultGA(id) {
			t.Fatalf("%s RequiredForDefaultGA = false, want true", id)
		}
	}
	for _, id := range []ID{WorkloadMount, WorkloadMountBinding, WorkloadMountDiscovery, WorkloadTeardownPlan, RepoTemplate, RepoPurge} {
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
		wantTeardownOps   []operations.OperationType
		wantOptionalGated bool
	}{
		{
			capabilityID:     NamespaceBinding,
			wantDefaultGA:    true,
			wantAdmissionOps: []operations.OperationType{operations.OperationNamespaceUpsert, operations.OperationNamespaceDisable, operations.OperationNamespaceVolumeBindingPut},
		},
		{
			capabilityID:     RepoCreate,
			wantDefaultGA:    true,
			wantAdmissionOps: []operations.OperationType{operations.OperationRepoCreate},
		},
		{
			capabilityID:     JVSSaveRestore,
			wantDefaultGA:    true,
			wantAdmissionOps: []operations.OperationType{operations.OperationSavePointCreate, operations.OperationRestorePreviewDiscard, operations.OperationRestoreRun},
		},
		{
			capabilityID:     WebDAVExport,
			wantDefaultGA:    true,
			wantAdmissionOps: []operations.OperationType{operations.OperationExportCreate, operations.OperationExportRevoke},
		},
		{
			capabilityID:     RepoLifecycleRetained,
			wantDefaultGA:    true,
			wantAdmissionOps: []operations.OperationType{operations.OperationRepoArchive, operations.OperationRepoRestoreArchived, operations.OperationRepoDelete, operations.OperationRepoRestoreTombstoned},
		},
		{
			capabilityID:      WorkloadMountBinding,
			wantAdmissionOps:  []operations.OperationType{operations.OperationMountBindingCreate, operations.OperationMountBindingStatusUpdate, operations.OperationMountBindingHeartbeat, operations.OperationMountBindingRelease, operations.OperationMountBindingRevoke},
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
			gotTeardown := TeardownOperationTypesForCapability(tt.capabilityID)
			if !operationTypeSlicesEqual(gotTeardown, tt.wantTeardownOps) {
				t.Fatalf("TeardownOperationTypesForCapability(%s) = %#v, want %#v", tt.capabilityID, gotTeardown, tt.wantTeardownOps)
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

func operationTypeSliceContains(values []operations.OperationType, want operations.OperationType) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func capabilityMatrixV1RowsByID(t *testing.T) map[ID]Row {
	t.Helper()

	rows := CapabilityMatrixV1Rows()
	byID := make(map[ID]Row, len(rows))
	for _, row := range rows {
		if row.ID == "" {
			t.Fatal("CapabilityMatrixV1Rows contains an empty capability id")
		}
		if row.SurfaceType == "" {
			t.Fatalf("%s SurfaceType is empty", row.ID)
		}
		if row.DefaultGARequired && row.OptionalGated {
			t.Fatalf("%s cannot be both default GA required and optional gated", row.ID)
		}
		if _, exists := byID[row.ID]; exists {
			t.Fatalf("CapabilityMatrixV1Rows contains duplicate capability id %s", row.ID)
		}
		byID[row.ID] = row
	}
	return byID
}
