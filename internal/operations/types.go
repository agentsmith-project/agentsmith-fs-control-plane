package operations

import (
	"encoding/json"
	"time"
)

type OperationType string

const (
	OperationVolumeEnsure              OperationType = "volume_ensure"
	OperationNamespaceUpsert           OperationType = "namespace_upsert"
	OperationNamespaceDisable          OperationType = "namespace_disable"
	OperationNamespaceVolumeBindingPut OperationType = "namespace_volume_binding_put"
	OperationRepoCreate                OperationType = "repo_create"
	OperationRepoArchive               OperationType = "repo_archive"
	OperationRepoRestoreArchived       OperationType = "repo_restore_archived"
	OperationRepoDelete                OperationType = "repo_delete"
	OperationRepoRestoreTombstoned     OperationType = "repo_restore_tombstoned"
	OperationRepoPurge                 OperationType = "repo_purge"
	OperationSavePointCreate           OperationType = "save_point_create"
	OperationRestorePreview            OperationType = "restore_preview"
	OperationRestoreRun                OperationType = "restore_run"
	OperationTemplateCreate            OperationType = "template_create"
	OperationTemplateClone             OperationType = "template_clone"
	OperationExportCreate              OperationType = "export_create"
	OperationExportRevoke              OperationType = "export_revoke"
	OperationExportSessionReconcile    OperationType = "export_session_reconcile"
	OperationMountBindingCreate        OperationType = "mount_binding_create"
	OperationMountBindingStatusUpdate  OperationType = "mount_binding_status_update"
	OperationMountBindingHeartbeat     OperationType = "mount_binding_heartbeat"
	OperationMountBindingRelease       OperationType = "mount_binding_release"
	OperationMountBindingRevoke        OperationType = "mount_binding_revoke"
	OperationMigrationCutover          OperationType = "migration_cutover"
)

var operationTypes = []OperationType{
	OperationVolumeEnsure,
	OperationNamespaceUpsert,
	OperationNamespaceDisable,
	OperationNamespaceVolumeBindingPut,
	OperationRepoCreate,
	OperationRepoArchive,
	OperationRepoRestoreArchived,
	OperationRepoDelete,
	OperationRepoRestoreTombstoned,
	OperationRepoPurge,
	OperationSavePointCreate,
	OperationRestorePreview,
	OperationRestoreRun,
	OperationTemplateCreate,
	OperationTemplateClone,
	OperationExportCreate,
	OperationExportRevoke,
	OperationExportSessionReconcile,
	OperationMountBindingCreate,
	OperationMountBindingStatusUpdate,
	OperationMountBindingHeartbeat,
	OperationMountBindingRelease,
	OperationMountBindingRevoke,
	OperationMigrationCutover,
}

var routeOperationTypes = map[string]OperationType{
	"ensureVolume":                     OperationVolumeEnsure,
	"upsertNamespace":                  OperationNamespaceUpsert,
	"disableNamespace":                 OperationNamespaceDisable,
	"putNamespaceVolumeBinding":        OperationNamespaceVolumeBindingPut,
	"createRepo":                       OperationRepoCreate,
	"archiveRepo":                      OperationRepoArchive,
	"restoreArchivedRepo":              OperationRepoRestoreArchived,
	"deleteRepo":                       OperationRepoDelete,
	"restoreTombstonedRepo":            OperationRepoRestoreTombstoned,
	"purgeRepo":                        OperationRepoPurge,
	"createSavePoint":                  OperationSavePointCreate,
	"restorePreview":                   OperationRestorePreview,
	"restoreRun":                       OperationRestoreRun,
	"createRepoTemplate":               OperationTemplateCreate,
	"cloneRepoTemplate":                OperationTemplateClone,
	"createExport":                     OperationExportCreate,
	"revokeExport":                     OperationExportRevoke,
	"createWorkloadMountBinding":       OperationMountBindingCreate,
	"updateWorkloadMountBindingStatus": OperationMountBindingStatusUpdate,
	"heartbeatWorkloadMountBinding":    OperationMountBindingHeartbeat,
	"releaseWorkloadMountBinding":      OperationMountBindingRelease,
	"revokeWorkloadMountBinding":       OperationMountBindingRevoke,
}

func (typ OperationType) String() string {
	return string(typ)
}

func OperationTypes() []OperationType {
	types := make([]OperationType, len(operationTypes))
	copy(types, operationTypes)
	return types
}

func RouteOperationTypes() map[string]OperationType {
	mapped := make(map[string]OperationType, len(routeOperationTypes))
	for operationID, typ := range routeOperationTypes {
		mapped[operationID] = typ
	}
	return mapped
}

func OperationTypeForRouteOperationID(operationID string) (OperationType, bool) {
	typ, ok := routeOperationTypes[operationID]
	return typ, ok
}

type CallerContext struct {
	Service             string `json:"caller_service"`
	AuthorizedActorType string `json:"authorized_actor_type"`
	AuthorizedActorID   string `json:"authorized_actor_id"`
}

type Actor struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type ResourceRef struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type ResourceRefs struct {
	NamespaceID         string                `json:"namespace_id,omitempty"`
	RepoID              string                `json:"repo_id,omitempty"`
	TemplateID          string                `json:"template_id,omitempty"`
	ExportID            string                `json:"export_id,omitempty"`
	MountBindingID      string                `json:"mount_binding_id,omitempty"`
	SessionFenceID      string                `json:"session_fence_id,omitempty"`
	ExternalResourceIDs []ExternalResourceRef `json:"external_resource_ids,omitempty"`
}

type ExternalResourceRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type OperationRecord struct {
	ID                  string            `json:"operation_id"`
	Type                OperationType     `json:"operation_type"`
	State               OperationState    `json:"operation_state"`
	Phase               string            `json:"phase"`
	Attempt             int               `json:"attempt"`
	LeaseOwner          string            `json:"lease_owner,omitempty"`
	LeaseExpiresAt      *time.Time        `json:"lease_expires_at,omitempty"`
	IdempotencyScope    string            `json:"idempotency_scope"`
	IdempotencyKey      string            `json:"idempotency_key"`
	RequestHash         RequestHash       `json:"request_hash"`
	CorrelationID       string            `json:"correlation_id"`
	CallerService       string            `json:"caller_service"`
	AuthorizedActor     Actor             `json:"authorized_actor"`
	Resource            ResourceRef       `json:"resource"`
	NamespaceID         string            `json:"namespace_id,omitempty"`
	RepoID              string            `json:"repo_id,omitempty"`
	TemplateID          string            `json:"template_id,omitempty"`
	ExportID            string            `json:"export_id,omitempty"`
	MountBindingID      string            `json:"mount_binding_id,omitempty"`
	SessionFenceID      string            `json:"session_fence_id,omitempty"`
	ExternalResourceIDs map[string]string `json:"external_resource_ids"`
	InputSummary        map[string]any    `json:"input_summary"`
	JVSJSONOutput       any               `json:"jvs_json_output,omitempty"`
	VerificationResult  any               `json:"verification_result,omitempty"`
	CompensationStatus  string            `json:"compensation_status,omitempty"`
	Error               *OperationError   `json:"error"`
	Redaction           RedactionReport   `json:"-"`
	CreatedAt           time.Time         `json:"created_at"`
	StartedAt           *time.Time        `json:"started_at,omitempty"`
	FinishedAt          *time.Time        `json:"finished_at,omitempty"`
}

type OperationError struct {
	Code          string         `json:"code"`
	Message       string         `json:"message"`
	Retryable     bool           `json:"retryable"`
	CorrelationID string         `json:"correlation_id"`
	OperationID   string         `json:"operation_id,omitempty"`
	Details       map[string]any `json:"details,omitempty"`
}

type OperationRecordEnvelope struct {
	Operation OperationRecord `json:"operation"`
	Error     *OperationError `json:"error,omitempty"`
	Redaction RedactionReport `json:"redaction,omitempty"`
}

type RedactionReport struct {
	Redacted bool     `json:"redacted"`
	Fields   []string `json:"fields,omitempty"`
}

// SanitizedOperationRecord is the only operation shape that durable writers
// should accept. Construct it with OperationRecord.SanitizedForPersistence.
type SanitizedOperationRecord struct {
	record OperationRecord
}

// SanitizedForPersistence returns a typed wrapper around a sanitized copy of
// the record so store writers cannot accidentally accept raw operation fields.
func (record OperationRecord) SanitizedForPersistence() SanitizedOperationRecord {
	return SanitizedOperationRecord{record: record.Sanitized()}
}

func (record SanitizedOperationRecord) Record() OperationRecord {
	return record.record.Sanitized()
}

func (record SanitizedOperationRecord) MarshalJSON() ([]byte, error) {
	return json.Marshal(record.Record())
}

func (record OperationRecord) Sanitized() OperationRecord {
	sanitized := record

	externalResourceIDs, externalResourceReport := RedactExternalResourceIDs(record.ExternalResourceIDs)
	sanitized.ExternalResourceIDs = externalResourceIDs

	inputSummary, inputReport := RedactValue(record.InputSummary)
	if inputSummary == nil {
		sanitized.InputSummary = nil
	} else {
		sanitized.InputSummary, _ = inputSummary.(map[string]any)
	}

	jvsJSONOutput, jvsReport := RedactValue(record.JVSJSONOutput)
	sanitized.JVSJSONOutput = jvsJSONOutput

	verificationResult, verificationReport := RedactValue(record.VerificationResult)
	sanitized.VerificationResult = verificationResult

	var errReport RedactionReport
	if record.Error != nil {
		err, report := record.Error.Sanitized()
		sanitized.Error = &err
		errReport = report
	}

	sanitized.Redaction = MergeRedactionReports(record.Redaction, externalResourceReport, inputReport, jvsReport, verificationReport, errReport)
	return sanitized
}

func (err OperationError) Sanitized() (OperationError, RedactionReport) {
	sanitized := err
	message, messageReport := RedactValue(err.Message)
	if message != nil {
		sanitized.Message, _ = message.(string)
	}

	details, detailsReport := RedactValue(err.Details)
	if details == nil {
		sanitized.Details = nil
	} else {
		sanitized.Details, _ = details.(map[string]any)
	}

	return sanitized, MergeRedactionReports(messageReport, detailsReport)
}

func NewOperationRecordEnvelope(record OperationRecord) OperationRecordEnvelope {
	sanitized := record.Sanitized()

	return OperationRecordEnvelope{
		Operation: sanitized,
		Error:     sanitized.Error,
		Redaction: sanitized.Redaction,
	}
}

func (record OperationRecord) MarshalJSON() ([]byte, error) {
	type operationRecordJSON struct {
		ID                  string            `json:"operation_id"`
		Type                OperationType     `json:"operation_type"`
		State               OperationState    `json:"operation_state"`
		Phase               string            `json:"phase"`
		Attempt             int               `json:"attempt"`
		LeaseOwner          *string           `json:"lease_owner"`
		LeaseExpiresAt      *time.Time        `json:"lease_expires_at"`
		IdempotencyScope    string            `json:"idempotency_scope"`
		IdempotencyKey      string            `json:"idempotency_key"`
		RequestHash         RequestHash       `json:"request_hash"`
		CorrelationID       string            `json:"correlation_id"`
		CallerService       string            `json:"caller_service"`
		AuthorizedActor     Actor             `json:"authorized_actor"`
		Resource            ResourceRef       `json:"resource"`
		NamespaceID         *string           `json:"namespace_id"`
		RepoID              *string           `json:"repo_id"`
		TemplateID          *string           `json:"template_id"`
		ExportID            *string           `json:"export_id"`
		MountBindingID      *string           `json:"mount_binding_id"`
		SessionFenceID      *string           `json:"session_fence_id"`
		ExternalResourceIDs map[string]string `json:"external_resource_ids"`
		InputSummary        map[string]any    `json:"input_summary"`
		JVSJSONOutput       any               `json:"jvs_json_output"`
		VerificationResult  any               `json:"verification_result"`
		CompensationStatus  *string           `json:"compensation_status"`
		Error               *OperationError   `json:"error"`
		CreatedAt           time.Time         `json:"created_at"`
		StartedAt           *time.Time        `json:"started_at"`
		FinishedAt          *time.Time        `json:"finished_at"`
	}

	sanitized := record.Sanitized()
	normalized := operationRecordJSON{
		ID:                  sanitized.ID,
		Type:                sanitized.Type,
		State:               sanitized.State,
		Phase:               sanitized.Phase,
		Attempt:             sanitized.Attempt,
		LeaseOwner:          nullableString(sanitized.LeaseOwner),
		LeaseExpiresAt:      sanitized.LeaseExpiresAt,
		IdempotencyScope:    sanitized.IdempotencyScope,
		IdempotencyKey:      sanitized.IdempotencyKey,
		RequestHash:         sanitized.RequestHash,
		CorrelationID:       sanitized.CorrelationID,
		CallerService:       sanitized.CallerService,
		AuthorizedActor:     sanitized.AuthorizedActor,
		Resource:            sanitized.Resource,
		NamespaceID:         nullableString(sanitized.NamespaceID),
		RepoID:              nullableString(sanitized.RepoID),
		TemplateID:          nullableString(sanitized.TemplateID),
		ExportID:            nullableString(sanitized.ExportID),
		MountBindingID:      nullableString(sanitized.MountBindingID),
		SessionFenceID:      nullableString(sanitized.SessionFenceID),
		ExternalResourceIDs: sanitized.ExternalResourceIDs,
		InputSummary:        sanitized.InputSummary,
		JVSJSONOutput:       sanitized.JVSJSONOutput,
		VerificationResult:  sanitized.VerificationResult,
		CompensationStatus:  nullableString(sanitized.CompensationStatus),
		Error:               sanitized.Error,
		CreatedAt:           sanitized.CreatedAt,
		StartedAt:           sanitized.StartedAt,
		FinishedAt:          sanitized.FinishedAt,
	}
	if normalized.ExternalResourceIDs == nil {
		normalized.ExternalResourceIDs = map[string]string{}
	}
	if normalized.InputSummary == nil {
		normalized.InputSummary = map[string]any{}
	}

	return json.Marshal(normalized)
}

func nullableString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
