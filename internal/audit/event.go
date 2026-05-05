package audit

import (
	"context"
	"encoding/json"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/observability"
)

type EventType string

const (
	EventTypeAuthzDenied      EventType = "authz_denied"
	EventTypePathDenied       EventType = "path_denied"
	EventTypeCapabilityDenied EventType = "capability_denied"
	EventTypeExportCreate     EventType = "export_create"
	EventTypeRepoPurge        EventType = "repo_purge"
)

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
