package audit

import (
	"context"
	"encoding/json"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/observability"
)

type EventType string

const (
	EventTypeAuthzDenied                     EventType = "authz_denied"
	EventTypePathDenied                      EventType = "path_denied"
	EventTypeCapabilityDenied                EventType = "capability_denied"
	EventTypeResourceNamespaceMismatchDenied EventType = "resource_namespace_mismatch_denied"
	EventTypeVolumeEnsure                    EventType = "volume_ensure"
	EventTypeNamespaceUpsert                 EventType = "namespace_upsert"
	EventTypeNamespaceDisable                EventType = "namespace_disable"
	EventTypeNamespaceVolumeBindingPut       EventType = "namespace_volume_binding_put"
	EventTypeRepoCreate                      EventType = "repo_create"
	EventTypeRepoArchive                     EventType = "repo_archive"
	EventTypeRepoRestoreArchived             EventType = "repo_restore_archived"
	EventTypeRepoDelete                      EventType = "repo_delete"
	EventTypeRepoRestoreTombstoned           EventType = "repo_restore_tombstoned"
	EventTypeRepoPurge                       EventType = "repo_purge"
	EventTypeSavePointCreate                 EventType = "save_point_create"
	EventTypeRestorePreview                  EventType = "restore_preview"
	EventTypeRestoreRun                      EventType = "restore_run"
	EventTypeTemplateCreate                  EventType = "template_create"
	EventTypeTemplateClone                   EventType = "template_clone"
	EventTypeExportCreate                    EventType = "export_create"
	EventTypeExportRevoke                    EventType = "export_revoke"
	EventTypeExportSessionReconcile          EventType = "export_session_reconcile"
	EventTypeMountBindingCreate              EventType = "mount_binding_create"
	EventTypeMountBindingStatusUpdate        EventType = "mount_binding_status_update"
	EventTypeMountBindingHeartbeat           EventType = "mount_binding_heartbeat"
	EventTypeMountBindingRelease             EventType = "mount_binding_release"
	EventTypeMountBindingRevoke              EventType = "mount_binding_revoke"
	EventTypeMigrationCutover                EventType = "migration_cutover"
)

var eventTypes = []EventType{
	EventTypeAuthzDenied,
	EventTypePathDenied,
	EventTypeCapabilityDenied,
	EventTypeResourceNamespaceMismatchDenied,
	EventTypeVolumeEnsure,
	EventTypeNamespaceUpsert,
	EventTypeNamespaceDisable,
	EventTypeNamespaceVolumeBindingPut,
	EventTypeRepoCreate,
	EventTypeRepoArchive,
	EventTypeRepoRestoreArchived,
	EventTypeRepoDelete,
	EventTypeRepoRestoreTombstoned,
	EventTypeRepoPurge,
	EventTypeSavePointCreate,
	EventTypeRestorePreview,
	EventTypeRestoreRun,
	EventTypeTemplateCreate,
	EventTypeTemplateClone,
	EventTypeExportCreate,
	EventTypeExportRevoke,
	EventTypeExportSessionReconcile,
	EventTypeMountBindingCreate,
	EventTypeMountBindingStatusUpdate,
	EventTypeMountBindingHeartbeat,
	EventTypeMountBindingRelease,
	EventTypeMountBindingRevoke,
	EventTypeMigrationCutover,
}

var operationEventTypes = map[string]EventType{
	"volume_ensure":                EventTypeVolumeEnsure,
	"namespace_upsert":             EventTypeNamespaceUpsert,
	"namespace_disable":            EventTypeNamespaceDisable,
	"namespace_volume_binding_put": EventTypeNamespaceVolumeBindingPut,
	"repo_create":                  EventTypeRepoCreate,
	"repo_archive":                 EventTypeRepoArchive,
	"repo_restore_archived":        EventTypeRepoRestoreArchived,
	"repo_delete":                  EventTypeRepoDelete,
	"repo_restore_tombstoned":      EventTypeRepoRestoreTombstoned,
	"repo_purge":                   EventTypeRepoPurge,
	"save_point_create":            EventTypeSavePointCreate,
	"restore_preview":              EventTypeRestorePreview,
	"restore_run":                  EventTypeRestoreRun,
	"template_create":              EventTypeTemplateCreate,
	"template_clone":               EventTypeTemplateClone,
	"export_create":                EventTypeExportCreate,
	"export_revoke":                EventTypeExportRevoke,
	"export_session_reconcile":     EventTypeExportSessionReconcile,
	"mount_binding_create":         EventTypeMountBindingCreate,
	"mount_binding_status_update":  EventTypeMountBindingStatusUpdate,
	"mount_binding_heartbeat":      EventTypeMountBindingHeartbeat,
	"mount_binding_release":        EventTypeMountBindingRelease,
	"mount_binding_revoke":         EventTypeMountBindingRevoke,
	"migration_cutover":            EventTypeMigrationCutover,
}

func EventTypes() []EventType {
	types := make([]EventType, len(eventTypes))
	copy(types, eventTypes)
	return types
}

func OperationEventTypes() map[string]EventType {
	mapped := make(map[string]EventType, len(operationEventTypes))
	for operationType, eventType := range operationEventTypes {
		mapped[operationType] = eventType
	}
	return mapped
}

func EventTypeForOperationType(operationType string) (EventType, bool) {
	eventType, ok := operationEventTypes[operationType]
	return eventType, ok
}

type Outcome string

const (
	OutcomeSucceeded Outcome = "succeeded"
	OutcomeFailed    Outcome = "failed"
	OutcomeDenied    Outcome = "denied"
)

type Actor struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type Resource struct {
	Type        string `json:"type"`
	ID          string `json:"id,omitempty"`
	NamespaceID string `json:"namespace_id,omitempty"`
	Path        string `json:"path,omitempty"`
}

type Event struct {
	EventID         string         `json:"event_id"`
	Type            EventType      `json:"type"`
	Time            time.Time      `json:"time"`
	CallerService   string         `json:"caller_service"`
	AuthorizedActor Actor          `json:"authorized_actor"`
	CorrelationID   string         `json:"correlation_id"`
	OperationID     string         `json:"operation_id"`
	Resource        Resource       `json:"resource"`
	Outcome         Outcome        `json:"outcome"`
	Reason          string         `json:"reason"`
	Details         map[string]any `json:"details"`
}

type Redactor interface {
	RedactDetails(map[string]any) map[string]any
}

type RedactorFunc func(map[string]any) map[string]any

func (fn RedactorFunc) RedactDetails(details map[string]any) map[string]any {
	if fn == nil {
		return RedactDetails(details)
	}
	return fn(details)
}

var defaultRedactor Redactor = RedactorFunc(RedactDetails)

func NewEvent(event Event) Event {
	return NewEventWithRedactor(event, defaultRedactor)
}

func NewEventWithRedactor(event Event, redactor Redactor) Event {
	if redactor == nil {
		redactor = defaultRedactor
	}
	event.Reason = RedactString(event.Reason)
	event.Resource.Path = RedactString(event.Resource.Path)
	event.Details = redactor.RedactDetails(event.Details)
	return event
}

func RedactDetails(details map[string]any) map[string]any {
	return observability.RedactFields(details)
}

func RedactString(value string) string {
	redacted, _ := observability.RedactString(value)
	return redacted
}

func (event Event) Sanitized() Event {
	event.Reason = RedactString(event.Reason)
	event.Resource.Path = RedactString(event.Resource.Path)
	event.Details = RedactDetails(event.Details)
	return event
}

func (event Event) MarshalJSON() ([]byte, error) {
	type eventJSON Event
	sanitized := event.Sanitized()
	return json.Marshal(eventJSON(sanitized))
}

type Sink interface {
	Emit(context.Context, Event) error
}
