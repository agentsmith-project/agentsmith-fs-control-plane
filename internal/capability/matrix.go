package capability

type ID string

const (
	Storage       ID = "storage"
	JVS           ID = "jvs"
	WebDAVExport  ID = "webdav_export"
	WorkloadMount ID = "workload_mount"
)

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
