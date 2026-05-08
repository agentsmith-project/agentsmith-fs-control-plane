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

type Row struct {
	ID                      ID
	SurfaceType             SurfaceType
	DefaultGARequired       bool
	OptionalGated           bool
	AdmissionOperationTypes []operations.OperationType
	TeardownOperationTypes  []operations.OperationType
}

var capabilityMatrixV1Rows = []Row{
	{
		ID:                      NamespaceBinding,
		SurfaceType:             SurfaceDurableOperation,
		DefaultGARequired:       true,
		AdmissionOperationTypes: []operations.OperationType{operations.OperationNamespaceUpsert, operations.OperationNamespaceDisable, operations.OperationNamespaceVolumeBindingPut},
	},
	{
		ID:                VolumePreflight,
		SurfaceType:       SurfacePreflight,
		DefaultGARequired: true,
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
		AdmissionOperationTypes: []operations.OperationType{operations.OperationSavePointCreate, operations.OperationRestorePreviewDiscard, operations.OperationRestoreRun},
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

func CapabilityMatrixV1Rows() []Row {
	rows := make([]Row, 0, len(capabilityMatrixV1Rows))
	for _, row := range capabilityMatrixV1Rows {
		rows = append(rows, row.copy())
	}
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
