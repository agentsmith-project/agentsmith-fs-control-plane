package capability

import "github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"

type ID string

const (
	Storage       ID = "storage"
	JVS           ID = "jvs"
	WebDAVExport  ID = "webdav_export"
	WorkloadMount ID = "workload_mount"
	RepoTemplate  ID = "repo_template"
	RepoPurge     ID = "repo_purge"
)

var admissionCapabilitiesByOperationType = map[operations.OperationType]ID{
	operations.OperationExportCreate:             WebDAVExport,
	operations.OperationMountBindingCreate:       WorkloadMount,
	operations.OperationMountBindingStatusUpdate: WorkloadMount,
	operations.OperationMountBindingHeartbeat:    WorkloadMount,
	operations.OperationTemplateCreate:           RepoTemplate,
	operations.OperationTemplateClone:            RepoTemplate,
	operations.OperationRepoPurge:                RepoPurge,
}

var teardownOperationsByCapability = map[ID][]operations.OperationType{
	WorkloadMount: {
		operations.OperationMountBindingRelease,
		operations.OperationMountBindingRevoke,
	},
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

func RequiredForDefaultGA(id ID) bool {
	switch id {
	case Storage, JVS, WebDAVExport:
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
