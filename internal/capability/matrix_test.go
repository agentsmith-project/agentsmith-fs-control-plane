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
			if row.SurfaceType != SurfaceDurableOperation && row.ID != VolumePreflight && len(row.AdmissionOperationTypes) != 0 {
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
		operations.OperationRestore,
		operations.OperationRestorePreview,
		operations.OperationRestorePreviewDiscard,
		operations.OperationRestoreRun,
	}) {
		t.Fatalf("%s AdmissionOperationTypes = %#v, want save point create, direct restore, restore preview, restore preview discard, and restore run", JVSSaveRestore, jvsDurable.AdmissionOperationTypes)
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

func TestCapabilityMatrixV1DecisionRowsCoverP2aSurfaceContract(t *testing.T) {
	rows := CapabilityMatrixV1DecisionRows()
	if len(rows) == 0 {
		t.Fatal("CapabilityMatrixV1DecisionRows is empty")
	}

	seenSurface := map[DecisionSurfaceType]bool{}
	seenOperation := map[operations.OperationType]bool{}
	for _, row := range rows {
		if row.SurfaceType == "" {
			t.Fatalf("decision row missing surface type: %#v", row)
		}
		if row.CapabilityID == "" {
			t.Fatalf("decision row missing capability id: %#v", row)
		}
		if row.ResourceScope == "" {
			t.Fatalf("%s/%s missing resource scope", row.SurfaceType, row.OperationType)
		}
		if row.RunbookRef == "" || row.EvidenceRef == "" {
			t.Fatalf("%s/%s missing runbook/evidence refs: %#v", row.SurfaceType, row.OperationType, row)
		}
		if row.RequiredForDefaultGA && row.OptionalGated {
			t.Fatalf("%s/%s cannot be both default-required and optional-gated", row.SurfaceType, row.OperationType)
		}
		if !row.Supported && row.RequiredForDefaultGA {
			t.Fatalf("%s/%s unsupported row cannot be default-required", row.SurfaceType, row.OperationType)
		}
		seenSurface[row.SurfaceType] = true
		if row.OperationType != "" {
			seenOperation[row.OperationType] = true
		}
	}

	for _, surface := range []DecisionSurfaceType{
		SurfaceAPIAdmission,
		SurfaceWorkerExecution,
		SurfaceWorkerRecovery,
		SurfaceReadyz,
		SurfaceCallerDiscovery,
		SurfaceOrchestratorDiscovery,
		SurfaceOperatorInspection,
		SurfaceEvidence,
	} {
		if !seenSurface[surface] {
			t.Fatalf("CapabilityMatrixV1DecisionRows missing surface %s", surface)
		}
	}
	for _, operationType := range operations.OperationTypes() {
		if !seenOperation[operationType] {
			t.Fatalf("CapabilityMatrixV1DecisionRows missing operation inventory row for %s", operationType)
		}
	}
}

func TestCapabilityMatrixV1DecisionRowsExposeHandoffMinimumFields(t *testing.T) {
	for _, row := range CapabilityMatrixV1DecisionRows() {
		if row.Configured == "" {
			t.Fatalf("%s/%s missing configured contract field", row.SurfaceType, row.OperationType)
		}
		if row.Ready == "" {
			t.Fatalf("%s/%s missing ready contract field", row.SurfaceType, row.OperationType)
		}
		if row.NamespacePolicy == "" {
			t.Fatalf("%s/%s missing namespace_policy contract field", row.SurfaceType, row.OperationType)
		}
		if row.VolumeRuntimeCapability == "" {
			t.Fatalf("%s/%s missing volume_runtime_capability contract field", row.SurfaceType, row.OperationType)
		}
	}
}

func TestCapabilityMatrixV1DecisionRowsCoverRequiredSurfacesByOperationClass(t *testing.T) {
	rows := CapabilityMatrixV1DecisionRows()
	for routeOperationID, operationType := range operations.RouteOperationTypes() {
		assertDecisionRow(t, rows, operationType, SurfaceAPIAdmission, "", true)
		assertDecisionRow(t, rows, operationType, SurfaceEvidence, "", true)
		if capabilityMatrixV1RuntimeWorkerOperation(operationType) {
			assertDecisionRow(t, rows, operationType, SurfaceWorkerExecution, "", true)
			assertDecisionRow(t, rows, operationType, SurfaceWorkerRecovery, OperationRecovery, true)
			continue
		}
		if got := DecisionRowsForOperationSurface(operationType, SurfaceWorkerExecution); len(got) != 0 {
			t.Fatalf("%s/%s worker-execution rows = %#v, want none for API/store boundary operation", routeOperationID, operationType, got)
		}
		if got := DecisionRowsForOperationSurface(operationType, SurfaceWorkerRecovery); len(got) != 0 {
			t.Fatalf("%s/%s worker-recovery rows = %#v, want none for API/store boundary operation", routeOperationID, operationType, got)
		}
	}

	for _, operationType := range []operations.OperationType{
		operations.OperationRepoPurge,
		operations.OperationTemplateCreate,
		operations.OperationTemplateClone,
		operations.OperationMountBindingCreate,
		operations.OperationMountBindingStatusUpdate,
		operations.OperationMountBindingHeartbeat,
		operations.OperationMountBindingRelease,
		operations.OperationMountBindingRevoke,
	} {
		row := assertDecisionRow(t, rows, operationType, SurfaceAPIAdmission, "", true)
		assertDecisionRow(t, rows, operationType, SurfaceWorkerExecution, "", true)
		assertDecisionRow(t, rows, operationType, SurfaceWorkerRecovery, OperationRecovery, true)
		if !row.OptionalGated || row.RequiredForDefaultGA {
			t.Fatalf("%s api-admission row = %#v, want default-negative optional-gated", operationType, row)
		}
	}

	for _, operationType := range []operations.OperationType{
		operations.OperationExportSessionReconcile,
		operations.OperationMigrationCutover,
	} {
		assertDecisionRow(t, rows, operationType, SurfaceWorkerExecution, "", operationType == operations.OperationExportSessionReconcile)
		assertDecisionRow(t, rows, operationType, SurfaceWorkerRecovery, OperationRecovery, operationType == operations.OperationExportSessionReconcile)
		assertDecisionRow(t, rows, operationType, SurfaceEvidence, "", operationType == operations.OperationExportSessionReconcile)
		if operationType == operations.OperationMigrationCutover {
			row := assertDecisionRow(t, rows, operationType, SurfaceWorkerExecution, OperationRecovery, false)
			if row.Configured != "conditional" || row.Ready != "unsupported" {
				t.Fatalf("%s worker-execution row = %#v, want conditional unsupported", operationType, row)
			}
		}
	}

	for _, tt := range []struct {
		surface      DecisionSurfaceType
		capabilityID ID
	}{
		{surface: SurfaceReadyz, capabilityID: AdminBootstrap},
		{surface: SurfaceReadyz, capabilityID: VolumePreflight},
		{surface: SurfaceCallerDiscovery, capabilityID: CallerPolicyReadiness},
		{surface: SurfaceOrchestratorDiscovery, capabilityID: WorkloadTeardownPlan},
		{surface: SurfaceOperatorInspection, capabilityID: OperationRecovery},
	} {
		assertDecisionRow(t, rows, "", tt.surface, tt.capabilityID, true)
	}
}

func TestCapabilityMatrixV1DecisionRowsAlignWithRecoveryOnlyContractOperations(t *testing.T) {
	rows := CapabilityMatrixV1DecisionRows()
	reconcileExecution := assertDecisionRow(t, rows, operations.OperationExportSessionReconcile, SurfaceWorkerExecution, OperationRecovery, true)
	if reconcileExecution.Configured != "runtime-derived" || reconcileExecution.Ready != "runtime-derived" {
		t.Fatalf("export_session_reconcile worker-execution row = %#v, want runtime-derived state", reconcileExecution)
	}
	migrationExecution := assertDecisionRow(t, rows, operations.OperationMigrationCutover, SurfaceWorkerExecution, OperationRecovery, false)
	if migrationExecution.Configured != "conditional" || migrationExecution.Ready != "unsupported" {
		t.Fatalf("migration_cutover worker-execution row = %#v, want conditional unsupported", migrationExecution)
	}
	migrationRecovery := assertDecisionRow(t, rows, operations.OperationMigrationCutover, SurfaceWorkerRecovery, OperationRecovery, false)
	if migrationRecovery.Configured != "conditional" || migrationRecovery.Ready != "recovery-only" {
		t.Fatalf("migration_cutover worker-recovery row = %#v, want conditional recovery-only", migrationRecovery)
	}
}

func TestCapabilityMatrixV1DecisionRowsKeepWebDAVExportAPIOperationsOutOfWorkerRecovery(t *testing.T) {
	for _, operationType := range []operations.OperationType{operations.OperationExportCreate, operations.OperationExportRevoke} {
		if rows := DecisionRowsForOperationSurface(operationType, SurfaceAPIAdmission); len(rows) != 1 {
			t.Fatalf("%s api-admission rows = %#v, want exactly one API decision", operationType, rows)
		}
		if rows := DecisionRowsForOperationSurface(operationType, SurfaceEvidence); len(rows) != 1 {
			t.Fatalf("%s evidence rows = %#v, want exactly one evidence decision", operationType, rows)
		}
		if rows := DecisionRowsForOperationSurface(operationType, SurfaceWorkerExecution); len(rows) != 0 {
			t.Fatalf("%s worker-execution rows = %#v, want none; WebDAV create/revoke commit at API/store boundary", operationType, rows)
		}
		if rows := DecisionRowsForOperationSurface(operationType, SurfaceWorkerRecovery); len(rows) != 0 {
			t.Fatalf("%s worker-recovery rows = %#v, want none; export_session_reconcile owns export worker recovery surface", operationType, rows)
		}
	}

	if rows := DecisionRowsForOperationSurface(operations.OperationExportSessionReconcile, SurfaceWorkerRecovery); len(rows) != 1 {
		t.Fatalf("export_session_reconcile worker-recovery rows = %#v, want explicit recovery surface", rows)
	}
	if rows := DecisionRowsForOperationSurface(operations.OperationExportSessionReconcile, SurfaceAPIAdmission); len(rows) != 0 {
		t.Fatalf("export_session_reconcile api-admission rows = %#v, want none for internal recovery operation", rows)
	}
}

func TestCapabilityMatrixV1DecisionRowsMapRouteAndWorkerOperationSurfaces(t *testing.T) {
	for routeOperationID, operationType := range operations.RouteOperationTypes() {
		admissionRows := DecisionRowsForOperationSurface(operationType, SurfaceAPIAdmission)
		if len(admissionRows) != 1 {
			t.Fatalf("%s/%s api-admission rows = %#v, want exactly one matrix decision", routeOperationID, operationType, admissionRows)
		}
		evidenceRows := DecisionRowsForOperationSurface(operationType, SurfaceEvidence)
		if len(evidenceRows) != 1 {
			t.Fatalf("%s/%s evidence rows = %#v, want exactly one matrix decision", routeOperationID, operationType, evidenceRows)
		}
		workerRows := DecisionRowsForOperationSurface(operationType, SurfaceWorkerExecution)
		recoveryRows := DecisionRowsForOperationSurface(operationType, SurfaceWorkerRecovery)
		if capabilityMatrixV1RuntimeWorkerOperation(operationType) {
			if len(workerRows) != 1 {
				t.Fatalf("%s/%s worker-execution rows = %#v, want exactly one worker decision", routeOperationID, operationType, workerRows)
			}
			if len(recoveryRows) != 1 {
				t.Fatalf("%s/%s worker-recovery rows = %#v, want exactly one worker recovery decision", routeOperationID, operationType, recoveryRows)
			}
			if admissionRows[0].CapabilityID != workerRows[0].CapabilityID || admissionRows[0].CapabilityID != evidenceRows[0].CapabilityID {
				t.Fatalf("%s matrix capability mismatch admission/worker/evidence = %s/%s/%s", operationType, admissionRows[0].CapabilityID, workerRows[0].CapabilityID, evidenceRows[0].CapabilityID)
			}
			if recoveryRows[0].CapabilityID != OperationRecovery {
				t.Fatalf("%s worker-recovery capability = %s, want %s", operationType, recoveryRows[0].CapabilityID, OperationRecovery)
			}
			continue
		}
		if len(workerRows) != 0 || len(recoveryRows) != 0 {
			t.Fatalf("%s/%s worker rows = execution %#v recovery %#v, want none for API/store boundary operation", routeOperationID, operationType, workerRows, recoveryRows)
		}
	}

	migrationRecovery := DecisionRowsForOperationSurface(operations.OperationMigrationCutover, SurfaceWorkerRecovery)
	if len(migrationRecovery) != 1 || migrationRecovery[0].Supported || migrationRecovery[0].Ready != "recovery-only" {
		t.Fatalf("migration_cutover recovery row = %#v, want single conditional unsupported recovery-only decision", migrationRecovery)
	}
}

func capabilityMatrixV1RuntimeWorkerOperation(operationType operations.OperationType) bool {
	switch operationType {
	case operations.OperationExportCreate, operations.OperationExportRevoke:
		return false
	default:
		return true
	}
}

func TestCapabilityMatrixV1DecisionRowsEvidenceRefsMapRuntimeSurfaces(t *testing.T) {
	for _, evidenceRef := range []string{"capability_runtime_parity_unit", "operation_runtime_terminalization_unit"} {
		rows := DecisionRowsForEvidenceRef(evidenceRef)
		if len(rows) == 0 {
			t.Fatalf("DecisionRowsForEvidenceRef(%q) returned no rows", evidenceRef)
		}
		for _, row := range rows {
			switch row.SurfaceType {
			case SurfaceAPIAdmission, SurfaceWorkerExecution, SurfaceWorkerRecovery, SurfaceEvidence:
			default:
				t.Fatalf("%s row for %s = %#v, want runtime parity surface", row.OperationType, evidenceRef, row)
			}
		}
	}
}

func TestCapabilityMatrixV1CoversEveryRouteMutationOperation(t *testing.T) {
	for routeOperationID, operationType := range operations.RouteOperationTypes() {
		got, ok := AdmissionCapabilityForOperationType(operationType)
		if !ok {
			t.Fatalf("route operation %s (%s) missing API admission capability", routeOperationID, operationType)
		}
		if got == "" {
			t.Fatalf("route operation %s (%s) mapped to empty capability", routeOperationID, operationType)
		}
	}
}

func TestCapabilityMatrixV1IncludesDirectRestoreAndPreviewAsDurableJVSMutations(t *testing.T) {
	row, ok := CapabilityMatrixV1Row(JVSSaveRestore)
	if !ok {
		t.Fatalf("CapabilityMatrixV1Rows missing %s", JVSSaveRestore)
	}
	if !operationTypeSliceContains(row.AdmissionOperationTypes, operations.OperationRestore) {
		t.Fatalf("%s AdmissionOperationTypes = %#v, want restore direct mutation", JVSSaveRestore, row.AdmissionOperationTypes)
	}
	if !operationTypeSliceContains(row.AdmissionOperationTypes, operations.OperationRestorePreview) {
		t.Fatalf("%s AdmissionOperationTypes = %#v, want restore_preview durable plan mutation", JVSSaveRestore, row.AdmissionOperationTypes)
	}
	directGot, ok := AdmissionCapabilityForOperationType(operations.OperationRestore)
	if !ok {
		t.Fatalf("AdmissionCapabilityForOperationType(%s) missing", operations.OperationRestore)
	}
	if directGot != JVSSaveRestore {
		t.Fatalf("AdmissionCapabilityForOperationType(%s) = %s, want %s", operations.OperationRestore, directGot, JVSSaveRestore)
	}
	got, ok := AdmissionCapabilityForOperationType(operations.OperationRestorePreview)
	if !ok {
		t.Fatalf("AdmissionCapabilityForOperationType(%s) missing", operations.OperationRestorePreview)
	}
	if got != JVSSaveRestore {
		t.Fatalf("AdmissionCapabilityForOperationType(%s) = %s, want %s", operations.OperationRestorePreview, got, JVSSaveRestore)
	}
}

func TestCapabilityMatrixV1ClassifiesVolumeEnsureAdmission(t *testing.T) {
	got, ok := AdmissionCapabilityForOperationType(operations.OperationVolumeEnsure)
	if !ok {
		t.Fatalf("AdmissionCapabilityForOperationType(%s) missing", operations.OperationVolumeEnsure)
	}
	if got != VolumePreflight {
		t.Fatalf("AdmissionCapabilityForOperationType(%s) = %s, want %s", operations.OperationVolumeEnsure, got, VolumePreflight)
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
		{operationType: operations.OperationVolumeEnsure, want: VolumePreflight},
		{operationType: operations.OperationRepoCreate, want: RepoCreate},
		{operationType: operations.OperationSavePointCreate, want: JVSSaveRestore},
		{operationType: operations.OperationRestore, want: JVSSaveRestore},
		{operationType: operations.OperationRestorePreview, want: JVSSaveRestore},
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
			capabilityID:     VolumePreflight,
			wantDefaultGA:    true,
			wantAdmissionOps: []operations.OperationType{operations.OperationVolumeEnsure},
		},
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
			wantAdmissionOps: []operations.OperationType{operations.OperationSavePointCreate, operations.OperationRestore, operations.OperationRestorePreview, operations.OperationRestorePreviewDiscard, operations.OperationRestoreRun},
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

func assertDecisionRow(t *testing.T, rows []DecisionRow, operationType operations.OperationType, surface DecisionSurfaceType, capabilityID ID, supported bool) DecisionRow {
	t.Helper()
	for _, row := range rows {
		if row.OperationType != operationType || row.SurfaceType != surface {
			continue
		}
		if capabilityID != "" && row.CapabilityID != capabilityID {
			continue
		}
		if row.Supported != supported {
			t.Fatalf("%s/%s supported = %v, want %v in row %#v", surface, operationType, row.Supported, supported, row)
		}
		return row
	}
	t.Fatalf("missing decision row operation=%s surface=%s capability=%s supported=%v", operationType, surface, capabilityID, supported)
	return DecisionRow{}
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
