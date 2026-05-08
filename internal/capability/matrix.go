package capability

import "github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"

type ID string

const (
	Storage                ID = "storage"
	JVS                    ID = "jvs"
	NamespaceBinding       ID = "namespace_binding"
	VolumePreflight        ID = "volume_preflight"
	AdminBootstrap         ID = "admin_bootstrap"
	CallerPolicyReadiness  ID = "caller_policy_readiness"
	PathRedaction          ID = "path_redaction"
	RepoCreate             ID = "repo_create"
	RepoProjection         ID = "repo_projection"
	JVSSaveRestore         ID = "jvs_save_restore"
	JVSProjection          ID = "jvs_projection"
	WebDAVExport           ID = "webdav_export"
	WebDAVProjection       ID = "webdav_projection"
	OperationRecovery      ID = "operation_recovery"
	RepoLifecycleRetained  ID = "repo_lifecycle_retained"
	RepoPurge              ID = "repo_purge"
	RepoTemplate           ID = "repo_template"
	WorkloadMount          ID = "workload_mount"
	WorkloadMountBinding   ID = "workload_mount_binding"
	WorkloadMountDiscovery ID = "workload_mount_discovery"
	WorkloadTeardownPlan   ID = "workload_teardown_plan"
)

type SurfaceType string

const (
	SurfaceDurableOperation SurfaceType = "durable_operation"
	SurfaceReadProjection   SurfaceType = "read_projection"
	SurfacePreflight        SurfaceType = "preflight"
	SurfaceDiscovery        SurfaceType = "discovery"
	SurfaceRedaction        SurfaceType = "redaction"
	SurfaceRuntimeSupport   SurfaceType = "runtime_support"
)

type DecisionSurfaceType string

const (
	SurfaceAPIAdmission          DecisionSurfaceType = "api-admission"
	SurfaceWorkerExecution       DecisionSurfaceType = "worker-execution"
	SurfaceWorkerRecovery        DecisionSurfaceType = "worker-recovery"
	SurfaceReadyz                DecisionSurfaceType = "readyz"
	SurfaceCallerDiscovery       DecisionSurfaceType = "caller-discovery"
	SurfaceOrchestratorDiscovery DecisionSurfaceType = "orchestrator-discovery"
	SurfaceOperatorInspection    DecisionSurfaceType = "operator-inspection"
	SurfaceEvidence              DecisionSurfaceType = "evidence"
)

type Row struct {
	ID                      ID
	SurfaceType             SurfaceType
	DefaultGARequired       bool
	OptionalGated           bool
	AdmissionOperationTypes []operations.OperationType
	TeardownOperationTypes  []operations.OperationType
}

type DecisionRow struct {
	SurfaceType             DecisionSurfaceType
	OperationType           operations.OperationType
	CapabilityID            ID
	ResourceScope           string
	Supported               bool
	Configured              string
	Ready                   string
	RequiredForDefaultGA    bool
	RequiredForServiceReady bool
	OptionalGated           bool
	NamespacePolicy         string
	VolumeRuntimeCapability string
	DenialCode              string
	RunbookRef              string
	EvidenceRef             string
}

var capabilityMatrixV1Rows = []Row{
	{
		ID:                      NamespaceBinding,
		SurfaceType:             SurfaceDurableOperation,
		DefaultGARequired:       true,
		AdmissionOperationTypes: []operations.OperationType{operations.OperationNamespaceUpsert, operations.OperationNamespaceDisable, operations.OperationNamespaceVolumeBindingPut},
	},
	{
		ID:                      VolumePreflight,
		SurfaceType:             SurfacePreflight,
		DefaultGARequired:       true,
		AdmissionOperationTypes: []operations.OperationType{operations.OperationVolumeEnsure},
	},
	{
		ID:                AdminBootstrap,
		SurfaceType:       SurfacePreflight,
		DefaultGARequired: true,
	},
	{
		ID:                CallerPolicyReadiness,
		SurfaceType:       SurfaceDiscovery,
		DefaultGARequired: true,
	},
	{
		ID:                PathRedaction,
		SurfaceType:       SurfaceRedaction,
		DefaultGARequired: true,
	},
	{
		ID:                      RepoCreate,
		SurfaceType:             SurfaceDurableOperation,
		DefaultGARequired:       true,
		AdmissionOperationTypes: []operations.OperationType{operations.OperationRepoCreate},
	},
	{
		ID:                RepoProjection,
		SurfaceType:       SurfaceReadProjection,
		DefaultGARequired: true,
	},
	{
		ID:                      JVSSaveRestore,
		SurfaceType:             SurfaceDurableOperation,
		DefaultGARequired:       true,
		AdmissionOperationTypes: []operations.OperationType{operations.OperationSavePointCreate, operations.OperationRestorePreview, operations.OperationRestorePreviewDiscard, operations.OperationRestoreRun},
	},
	{
		ID:                JVSProjection,
		SurfaceType:       SurfaceReadProjection,
		DefaultGARequired: true,
	},
	{
		ID:                      WebDAVExport,
		SurfaceType:             SurfaceDurableOperation,
		DefaultGARequired:       true,
		AdmissionOperationTypes: []operations.OperationType{operations.OperationExportCreate, operations.OperationExportRevoke},
	},
	{
		ID:                WebDAVProjection,
		SurfaceType:       SurfaceReadProjection,
		DefaultGARequired: true,
	},
	{
		ID:                OperationRecovery,
		SurfaceType:       SurfaceRuntimeSupport,
		DefaultGARequired: true,
	},
	{
		ID:                      RepoLifecycleRetained,
		SurfaceType:             SurfaceDurableOperation,
		DefaultGARequired:       true,
		AdmissionOperationTypes: []operations.OperationType{operations.OperationRepoArchive, operations.OperationRepoRestoreArchived, operations.OperationRepoDelete, operations.OperationRepoRestoreTombstoned},
	},
	{
		ID:                      RepoPurge,
		SurfaceType:             SurfaceDurableOperation,
		OptionalGated:           true,
		AdmissionOperationTypes: []operations.OperationType{operations.OperationRepoPurge},
	},
	{
		ID:                      RepoTemplate,
		SurfaceType:             SurfaceDurableOperation,
		OptionalGated:           true,
		AdmissionOperationTypes: []operations.OperationType{operations.OperationTemplateCreate, operations.OperationTemplateClone},
	},
	{
		ID:                      WorkloadMountBinding,
		SurfaceType:             SurfaceDurableOperation,
		OptionalGated:           true,
		AdmissionOperationTypes: []operations.OperationType{operations.OperationMountBindingCreate, operations.OperationMountBindingStatusUpdate, operations.OperationMountBindingHeartbeat, operations.OperationMountBindingRelease, operations.OperationMountBindingRevoke},
	},
	{
		ID:            WorkloadMountDiscovery,
		SurfaceType:   SurfaceDiscovery,
		OptionalGated: true,
	},
	{
		ID:            WorkloadTeardownPlan,
		SurfaceType:   SurfaceDiscovery,
		OptionalGated: true,
	},
}

var admissionCapabilitiesByOperationType = admissionCapabilitiesFromRows(capabilityMatrixV1Rows)
var teardownOperationsByCapability = teardownOperationsFromRows(capabilityMatrixV1Rows)
var capabilityMatrixV1DecisionRows = buildCapabilityMatrixV1DecisionRows()

func CapabilityMatrixV1Rows() []Row {
	rows := make([]Row, 0, len(capabilityMatrixV1Rows))
	for _, row := range capabilityMatrixV1Rows {
		rows = append(rows, row.copy())
	}
	return rows
}

func CapabilityMatrixV1DecisionRows() []DecisionRow {
	rows := make([]DecisionRow, len(capabilityMatrixV1DecisionRows))
	copy(rows, capabilityMatrixV1DecisionRows)
	return rows
}

func CapabilityMatrixV1Row(id ID) (Row, bool) {
	for _, row := range capabilityMatrixV1Rows {
		if row.ID == id {
			return row.copy(), true
		}
	}
	return Row{}, false
}

func buildCapabilityMatrixV1DecisionRows() []DecisionRow {
	rows := []DecisionRow{
		decisionRow(SurfaceReadyz, "", AdminBootstrap, "service", true, true, true, false, "static", "runtime-derived", "deployment", "not-applicable", "CAPABILITY_DENIED", "docs/READINESS_EVIDENCE.md", "admin_bootstrap_ready_unit"),
		decisionRow(SurfaceReadyz, "", VolumePreflight, "volume", true, true, true, false, "runtime-derived", "runtime-derived", "deployment", "required", "STORAGE_UNAVAILABLE", "docs/READINESS_EVIDENCE.md", "admin_bootstrap_ready_unit"),
		decisionRow(SurfaceCallerDiscovery, "", CallerPolicyReadiness, "deployment", true, true, true, false, "runtime-derived", "runtime-derived", "deployment", "not-applicable", "ROLE_NOT_ALLOWED", "docs/contracts/afscp-internal-api-v1.md", "admin_bootstrap_ready_unit"),
		decisionRow(SurfaceOrchestratorDiscovery, "", WorkloadTeardownPlan, "workload_mount_binding", true, false, false, true, "disabled-by-default", "disabled-by-default", "namespace-binding", "not-applicable", "CAPABILITY_DENIED", "docs/contracts/workload-mount-binding-v1.md", "workload_mount_disabled_admission_unit"),
		decisionRow(SurfaceOperatorInspection, "", OperationRecovery, "operation", true, true, true, false, "static", "runtime-derived", "operator", "not-applicable", "OPERATION_NOT_FOUND", "docs/contracts/operation-state-machine-v1.md", "operation_terminalization_contract_unit"),
		decisionRow(SurfaceEvidence, "", OperationRecovery, "release", true, true, true, false, "static", "static", "not-applicable", "not-applicable", "", "docs/GA_RELEASE_GATES.md", "operation_terminalization_contract_unit"),
	}

	for _, row := range CapabilityMatrixV1Rows() {
		for _, operationType := range row.AdmissionOperationTypes {
			rows = append(rows, decisionRow(
				SurfaceAPIAdmission,
				operationType,
				row.ID,
				resourceScopeForOperation(operationType),
				true,
				row.DefaultGARequired,
				row.DefaultGARequired,
				row.OptionalGated,
				configuredForCapability(row.ID),
				readyForCapability(row.ID),
				namespacePolicyForOperation(operationType),
				volumeRuntimeCapabilityForOperation(operationType),
				denialCodeForCapability(row.ID),
				"docs/contracts/operation-state-machine-v1.md",
				"capability_matrix_v1_contract_unit",
			))
			rows = append(rows, decisionRow(
				SurfaceWorkerExecution,
				operationType,
				row.ID,
				resourceScopeForOperation(operationType),
				true,
				row.DefaultGARequired,
				false,
				row.OptionalGated,
				configuredForCapability(row.ID),
				readyForCapability(row.ID),
				namespacePolicyForOperation(operationType),
				volumeRuntimeCapabilityForOperation(operationType),
				"",
				"docs/contracts/operation-state-machine-v1.md",
				"operation_terminalization_contract_unit",
			))
			rows = append(rows, decisionRow(
				SurfaceWorkerRecovery,
				operationType,
				OperationRecovery,
				resourceScopeForOperation(operationType),
				true,
				true,
				false,
				false,
				"runtime-derived",
				"runtime-derived",
				namespacePolicyForOperation(operationType),
				volumeRuntimeCapabilityForOperation(operationType),
				"OPERATION_RECOVERY_REQUIRED",
				"docs/contracts/operation-state-machine-v1.md",
				"operation_terminalization_contract_unit",
			))
			rows = append(rows, decisionRow(
				SurfaceEvidence,
				operationType,
				row.ID,
				resourceScopeForOperation(operationType),
				true,
				row.DefaultGARequired,
				false,
				row.OptionalGated,
				"static",
				"static",
				namespacePolicyForOperation(operationType),
				volumeRuntimeCapabilityForOperation(operationType),
				"",
				"docs/GA_RELEASE_GATES.md",
				"capability_matrix_v1_contract_unit",
			))
		}
	}

	rows = append(rows,
		decisionRow(SurfaceWorkerExecution, operations.OperationExportSessionReconcile, OperationRecovery, "export_session", true, true, false, false, "runtime-derived", "runtime-derived", "not-applicable", "not-applicable", "", "docs/contracts/export-access-webdav-v1.md", "operation_terminalization_contract_unit"),
		decisionRow(SurfaceWorkerRecovery, operations.OperationExportSessionReconcile, OperationRecovery, "export_session", true, true, false, false, "runtime-derived", "runtime-derived", "not-applicable", "not-applicable", "OPERATION_RECOVERY_REQUIRED", "docs/contracts/export-access-webdav-v1.md", "operation_terminalization_contract_unit"),
		decisionRow(SurfaceEvidence, operations.OperationExportSessionReconcile, OperationRecovery, "export_session", true, true, false, false, "static", "static", "not-applicable", "not-applicable", "", "docs/contracts/export-access-webdav-v1.md", "operation_terminalization_contract_unit"),
		decisionRow(SurfaceWorkerExecution, operations.OperationMigrationCutover, OperationRecovery, "migration", false, false, false, false, "conditional", "unsupported", "operator", "not-applicable", "CAPABILITY_DENIED", "docs/contracts/operation-state-machine-v1.md", "operation_terminalization_contract_unit"),
		decisionRow(SurfaceWorkerRecovery, operations.OperationMigrationCutover, OperationRecovery, "migration", false, false, false, false, "conditional", "recovery-only", "operator", "not-applicable", "OPERATION_RECOVERY_REQUIRED", "docs/contracts/operation-state-machine-v1.md", "operation_terminalization_contract_unit"),
		decisionRow(SurfaceEvidence, operations.OperationMigrationCutover, OperationRecovery, "migration", false, false, false, false, "conditional", "recovery-only", "operator", "not-applicable", "", "docs/contracts/operation-state-machine-v1.md", "operation_terminalization_contract_unit"),
	)

	return rows
}

func decisionRow(surface DecisionSurfaceType, operationType operations.OperationType, capabilityID ID, resourceScope string, supported, requiredDefault, requiredService, optionalGated bool, configured, ready, namespacePolicy, volumeRuntimeCapability, denialCode, runbookRef, evidenceRef string) DecisionRow {
	return DecisionRow{
		SurfaceType:             surface,
		OperationType:           operationType,
		CapabilityID:            capabilityID,
		ResourceScope:           resourceScope,
		Supported:               supported,
		Configured:              configured,
		Ready:                   ready,
		RequiredForDefaultGA:    requiredDefault,
		RequiredForServiceReady: requiredService,
		OptionalGated:           optionalGated,
		NamespacePolicy:         namespacePolicy,
		VolumeRuntimeCapability: volumeRuntimeCapability,
		DenialCode:              denialCode,
		RunbookRef:              runbookRef,
		EvidenceRef:             evidenceRef,
	}
}

func configuredForCapability(id ID) string {
	if RequiredForDefaultGA(id) {
		return "runtime-derived"
	}
	return "disabled-by-default"
}

func readyForCapability(id ID) string {
	if RequiredForDefaultGA(id) {
		return "runtime-derived"
	}
	return "disabled-by-default"
}

func namespacePolicyForOperation(operationType operations.OperationType) string {
	switch operationType {
	case operations.OperationVolumeEnsure, operations.OperationExportSessionReconcile, operations.OperationMigrationCutover:
		return "not-applicable"
	default:
		return "namespace-binding"
	}
}

func volumeRuntimeCapabilityForOperation(operationType operations.OperationType) string {
	switch operationType {
	case operations.OperationVolumeEnsure,
		operations.OperationRepoCreate,
		operations.OperationRepoArchive,
		operations.OperationRepoRestoreArchived,
		operations.OperationRepoDelete,
		operations.OperationRepoRestoreTombstoned,
		operations.OperationRepoPurge,
		operations.OperationSavePointCreate,
		operations.OperationRestorePreview,
		operations.OperationRestorePreviewDiscard,
		operations.OperationRestoreRun,
		operations.OperationTemplateCreate,
		operations.OperationTemplateClone,
		operations.OperationExportCreate,
		operations.OperationExportRevoke:
		return "required"
	default:
		return "not-applicable"
	}
}

func resourceScopeForOperation(operationType operations.OperationType) string {
	switch operationType {
	case operations.OperationVolumeEnsure:
		return "volume"
	case operations.OperationNamespaceUpsert, operations.OperationNamespaceDisable, operations.OperationNamespaceVolumeBindingPut:
		return "namespace"
	case operations.OperationExportCreate, operations.OperationExportRevoke, operations.OperationExportSessionReconcile:
		return "export_session"
	case operations.OperationMountBindingCreate,
		operations.OperationMountBindingStatusUpdate,
		operations.OperationMountBindingHeartbeat,
		operations.OperationMountBindingRelease,
		operations.OperationMountBindingRevoke:
		return "workload_mount_binding"
	case operations.OperationMigrationCutover:
		return "migration"
	default:
		return "repo"
	}
}

func denialCodeForCapability(id ID) string {
	switch id {
	case VolumePreflight:
		return "STORAGE_UNAVAILABLE"
	default:
		return "CAPABILITY_DENIED"
	}
}

func (row Row) copy() Row {
	row.AdmissionOperationTypes = append([]operations.OperationType(nil), row.AdmissionOperationTypes...)
	row.TeardownOperationTypes = append([]operations.OperationType(nil), row.TeardownOperationTypes...)
	return row
}

func AdmissionCapabilityForOperationType(operationType operations.OperationType) (ID, bool) {
	id, ok := admissionCapabilitiesByOperationType[operationType]
	return id, ok
}

func AdmissionOperationTypesForCapability(id ID) []operations.OperationType {
	var operationTypes []operations.OperationType
	for _, operationType := range operations.OperationTypes() {
		if capabilityID, ok := admissionCapabilitiesByOperationType[operationType]; ok && capabilityID == id {
			operationTypes = append(operationTypes, operationType)
		}
	}
	return operationTypes
}

func TeardownOperationTypesForCapability(id ID) []operations.OperationType {
	return append([]operations.OperationType(nil), teardownOperationsByCapability[id]...)
}

func admissionCapabilitiesFromRows(rows []Row) map[operations.OperationType]ID {
	byOperationType := make(map[operations.OperationType]ID)
	for _, row := range rows {
		for _, operationType := range row.AdmissionOperationTypes {
			if existingID, exists := byOperationType[operationType]; exists {
				panic("capability admission operation " + operationType.String() + " is mapped to both " + string(existingID) + " and " + string(row.ID))
			}
			byOperationType[operationType] = row.ID
		}
	}
	return byOperationType
}

func teardownOperationsFromRows(rows []Row) map[ID][]operations.OperationType {
	byCapability := make(map[ID][]operations.OperationType)
	for _, row := range rows {
		if len(row.TeardownOperationTypes) == 0 {
			continue
		}
		byCapability[row.ID] = append([]operations.OperationType(nil), row.TeardownOperationTypes...)
	}
	return byCapability
}

func RequiredForDefaultGA(id ID) bool {
	if row, ok := CapabilityMatrixV1Row(id); ok {
		return row.DefaultGARequired
	}
	switch id {
	case Storage, JVS:
		return true
	default:
		return false
	}
}

type Status struct {
	Enabled bool
	Ready   bool
	Gated   bool
	Reason  string
}

func (status Status) EffectiveReady() bool {
	return status.Enabled && status.Ready && !status.Gated
}

type Requirement struct {
	RequiredForServiceReady bool
	RequiredForDefaultGA    bool
	OptionalGated           bool
}

type Entry struct {
	ID          ID
	Status      Status
	Requirement Requirement
}

type Matrix struct {
	entries []Entry
}

func NewMatrix(entries ...Entry) Matrix {
	copied := append([]Entry(nil), entries...)
	return Matrix{entries: copied}
}

func (matrix Matrix) Entries() []Entry {
	return append([]Entry(nil), matrix.entries...)
}

func (matrix Matrix) RequiredForServiceReady() []ID {
	required := make([]ID, 0, len(matrix.entries))
	for _, entry := range matrix.entries {
		if entry.Requirement.RequiredForServiceReady {
			required = append(required, entry.ID)
		}
	}
	return required
}

func (matrix Matrix) ServiceReady() bool {
	for _, entry := range matrix.entries {
		if entry.Requirement.RequiredForServiceReady && !entry.Status.EffectiveReady() {
			return false
		}
	}
	return true
}
