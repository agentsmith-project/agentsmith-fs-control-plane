package audit

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/observability"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
)

func TestNewEventRedactsDetails(t *testing.T) {
	details := map[string]any{
		"metadata_url":    "redis://:metadata-secret@metadata:6379/1",
		"accessKey":       "access-key",
		"secret":          "plain-secret",
		"token":           "token-value",
		"password":        "password-value",
		"Secret ref":      "namespace/name",
		"WebDAV password": "webdav-password",
		"repo_id":         "repo_123",
		"nested": map[string]any{
			"accessKey": "nested-access-key",
			"headers": map[string]string{
				"Authorization": "Bearer nested-token",
				"X-Trace":       "trace-ok",
			},
		},
	}

	event := NewEvent(Event{
		EventID:         "evt_123",
		Type:            EventTypeExportCreate,
		Time:            time.Date(2026, 5, 4, 22, 0, 0, 0, time.UTC),
		CallerService:   "product-caller",
		AuthorizedActor: Actor{Type: "user", ID: "user_123"},
		CorrelationID:   "corr_123",
		OperationID:     "op_123",
		Resource:        Resource{Type: "export", ID: "export_123"},
		Outcome:         OutcomeSucceeded,
		Reason:          "export created",
		Details:         details,
	})

	for _, key := range []string{
		"metadata_url",
		"accessKey",
		"secret",
		"token",
		"password",
		"Secret ref",
		"WebDAV password",
	} {
		if got := event.Details[key]; got != observability.Redacted {
			t.Fatalf("Details[%s] = %#v, want %q", key, got, observability.Redacted)
		}
	}

	if got, want := event.Details["repo_id"], "repo_123"; got != want {
		t.Fatalf("Details[repo_id] = %#v, want %#v", got, want)
	}

	nested, ok := event.Details["nested"].(map[string]any)
	if !ok {
		t.Fatalf("Details[nested] redacted as %T, want map[string]any", event.Details["nested"])
	}
	if got := nested["accessKey"]; got != observability.Redacted {
		t.Fatalf("Details[nested][accessKey] = %#v, want %q", got, observability.Redacted)
	}
	headers, ok := nested["headers"].(map[string]string)
	if !ok {
		t.Fatalf("Details[nested][headers] redacted as %T, want map[string]string", nested["headers"])
	}
	if got := headers["Authorization"]; got != observability.Redacted {
		t.Fatalf("Details[nested][headers][Authorization] = %#v, want %q", got, observability.Redacted)
	}
	if got, want := headers["X-Trace"], "trace-ok"; got != want {
		t.Fatalf("Details[nested][headers][X-Trace] = %#v, want %#v", got, want)
	}

	if got, want := details["accessKey"], "access-key"; got != want {
		t.Fatalf("input details mutated: accessKey = %#v, want %#v", got, want)
	}
}

func TestNewEventRedactsReasonResourcePathAndRawDetailStrings(t *testing.T) {
	event := NewEvent(Event{
		EventID:         "evt_raw_strings",
		Type:            EventTypePathDenied,
		Time:            time.Date(2026, 5, 4, 22, 2, 0, 0, time.UTC),
		CallerService:   "product-caller",
		AuthorizedActor: Actor{Type: "user", ID: "user_123"},
		CorrelationID:   "corr_raw_strings",
		Resource: Resource{
			Type: "repo",
			ID:   "repo_123",
			Path: `/payload --token path-token redis://:path-metadata-secret@metadata:6379/1`,
		},
		Outcome: OutcomeDenied,
		Reason:  `metadata postgres://user:metadata-secret@metadata.internal:5432/jfs token=reason-token {"password":"json-password"} access_key: colon-key --password cli-password Authorization: Bearer bearer-token`,
		Details: map[string]any{
			"message": `command --token detail-token password=detail-password Authorization: Bearer detail-bearer`,
		},
	})

	renderedDirect := auditEventTestString(t, map[string]any{
		"reason":  event.Reason,
		"path":    event.Resource.Path,
		"details": event.Details,
	})
	renderedJSON := auditEventTestString(t, event)
	rendered := strings.ToLower(renderedDirect + " " + renderedJSON)

	for _, forbidden := range []string{
		"metadata-secret",
		"reason-token",
		"json-password",
		"colon-key",
		"cli-password",
		"bearer-token",
		"path-token",
		"path-metadata-secret",
		"detail-token",
		"detail-password",
		"detail-bearer",
		"postgres://",
		"redis://",
	} {
		if strings.Contains(rendered, strings.ToLower(forbidden)) {
			t.Fatalf("secret material %q leaked in direct=%s json=%s", forbidden, renderedDirect, renderedJSON)
		}
	}
	if !strings.Contains(rendered, strings.ToLower(observability.Redacted)) {
		t.Fatalf("redacted marker missing from direct=%s json=%s", renderedDirect, renderedJSON)
	}
}

func TestEventMarshalJSONRedactsDetails(t *testing.T) {
	event := Event{
		EventID:       "evt_marshal",
		Type:          EventTypeCapabilityDenied,
		Time:          time.Date(2026, 5, 4, 22, 5, 0, 0, time.UTC),
		CallerService: "product-caller",
		CorrelationID: "corr_marshal",
		Resource:      Resource{Type: "repo", ID: "repo_123"},
		Outcome:       OutcomeDenied,
		Reason:        "capability denied",
		Details: map[string]any{
			"password": "password-value",
			"safe":     "safe-value",
		},
	}

	body, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("MarshalJSON returned error: %v", err)
	}

	rendered := string(body)
	if strings.Contains(rendered, "password-value") {
		t.Fatalf("marshaled event leaked unredacted password: %s", rendered)
	}
	if !strings.Contains(rendered, observability.Redacted) {
		t.Fatalf("marshaled event did not include redacted marker: %s", rendered)
	}
	if !strings.Contains(rendered, "safe-value") {
		t.Fatalf("marshaled event dropped safe detail: %s", rendered)
	}
}

func TestNewEventWithRedactorUsesProvidedRedactor(t *testing.T) {
	redactor := RedactorFunc(func(details map[string]any) map[string]any {
		redacted := make(map[string]any, len(details))
		for key := range details {
			redacted[key] = "custom-redacted"
		}
		return redacted
	})

	event := NewEventWithRedactor(Event{
		EventID: "evt_custom_redactor",
		Details: map[string]any{
			"token": "token-value",
		},
	}, redactor)

	if got, want := event.Details["token"], "custom-redacted"; got != want {
		t.Fatalf("Details[token] = %#v, want %#v", got, want)
	}
}

func TestStableEventTypesUseCommonRedactionRules(t *testing.T) {
	for _, eventType := range EventTypes() {
		t.Run(string(eventType), func(t *testing.T) {
			event := NewEvent(Event{
				EventID:       "evt_stable_redaction",
				Type:          eventType,
				Time:          time.Date(2026, 5, 4, 22, 7, 0, 0, time.UTC),
				CallerService: "product-caller",
				CorrelationID: "corr_stable_redaction",
				OperationID:   "op_stable_redaction",
				Resource: Resource{
					Type:        "repo",
					ID:          "repo_123",
					NamespaceID: "ns_123",
					Path:        `/payload --token path-token redis://:path-metadata-secret@metadata:6379/1`,
				},
				Outcome: OutcomeFailed,
				Reason:  `metadata postgres://user:metadata-secret@metadata.internal:5432/jfs token=reason-token password=reason-password Secret ref: ns/raw-secret`,
				Details: auditSecretDetailsForTest(),
			})

			body, err := json.Marshal(event)
			if err != nil {
				t.Fatalf("MarshalJSON returned error: %v", err)
			}

			rendered := strings.ToLower(auditEventTestString(t, event) + " " + string(body))
			for _, forbidden := range []string{
				"detail-token",
				"detail-password",
				"detail-metadata-secret",
				"detail-access-key",
				"detail-webdav-password",
				"nested-token",
				"nested-password",
				"nested-metadata-secret",
				"path-token",
				"path-metadata-secret",
				"reason-token",
				"reason-password",
				"metadata-secret",
				"raw-secret",
				"postgres://",
				"redis://",
			} {
				if strings.Contains(rendered, strings.ToLower(forbidden)) {
					t.Fatalf("%s leaked secret material %q in %s", eventType, forbidden, rendered)
				}
			}
			if !strings.Contains(rendered, strings.ToLower(observability.Redacted)) {
				t.Fatalf("%s missing redacted marker in %s", eventType, rendered)
			}
		})
	}
}

func TestSecretPathRedactionAuditOutboxAndStableEventsUseCommonRedactor(t *testing.T) {
	event := NewEvent(Event{
		EventID:       "evt_secret_path_redaction",
		Type:          EventTypeExportCreate,
		Time:          time.Date(2026, 5, 4, 22, 9, 0, 0, time.UTC),
		CallerService: "product-caller",
		CorrelationID: "corr_secret_path_redaction",
		OperationID:   "op_secret_path_redaction",
		Resource: Resource{
			Type:        "repo",
			ID:          "repo_123",
			NamespaceID: "ns_123",
			Path:        `/srv/afscp/namespaces/ns_123/repos/repo_123/payload/.jvs token=path-token`,
		},
		Outcome: OutcomeDenied,
		Reason:  `failed jvs restore --run plan-secret; next jvs --control-root /srv/afscp/namespaces/ns_123/repos/repo_123/control --workspace main recovery status --json SecretRef: runtime/ns postgres://api:metadata-secret@db/afscp credential=runtime-credential`,
		Details: map[string]any{
			"control_volume_subdir": "afscp/namespaces/ns_123/repos/repo_123/control",
			"payload_volume_subdir": "afscp/namespaces/ns_123/repos/repo_123/payload",
			"raw_path":              "/srv/afscp/namespaces/ns_123/repos/repo_123/control/.jvs",
			"command":               "jvs doctor /srv/afscp/namespaces/ns_123/repos/repo_123/control",
		},
	})
	record, err := NewOutboxRecord(event, time.Date(2026, 5, 4, 22, 10, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("NewOutboxRecord returned error: %v", err)
	}

	rendered := strings.ToLower(auditEventTestString(t, event) + " " + string(record.PayloadJSON))
	for _, forbidden := range []string{
		"/srv/afscp",
		".jvs",
		"afscp/namespaces/ns_123/repos/repo_123/control",
		"afscp/namespaces/ns_123/repos/repo_123/payload",
		"jvs restore --run",
		"recovery status",
		"jvs doctor",
		"plan-secret",
		"runtime/ns",
		"metadata-secret",
		"runtime-credential",
		"path-token",
		"postgres://",
	} {
		if strings.Contains(rendered, strings.ToLower(forbidden)) {
			t.Fatalf("audit/outbox leaked secret/path material %q in %s", forbidden, rendered)
		}
	}
	if !strings.Contains(rendered, strings.ToLower(observability.Redacted)) {
		t.Fatalf("redacted marker missing in %s", rendered)
	}
}

func TestOperationTypesMapToStableAuditEventTypes(t *testing.T) {
	eventTypes := EventTypes()
	knownEventTypes := make(map[EventType]bool, len(eventTypes))
	for _, eventType := range eventTypes {
		knownEventTypes[eventType] = true
	}

	seenMappedTypes := make(map[EventType]operations.OperationType)
	for _, operationType := range operations.OperationTypes() {
		eventType, ok := EventTypeForOperationType(operationType.String())
		if !ok {
			t.Fatalf("EventTypeForOperationType(%q) ok = false, want true", operationType)
		}
		if eventType == "" {
			t.Fatalf("EventTypeForOperationType(%q) = empty", operationType)
		}
		if !knownEventTypes[eventType] {
			t.Fatalf("EventTypeForOperationType(%q) = %q, not in EventTypes()", operationType, eventType)
		}
		if previous, ok := seenMappedTypes[eventType]; ok {
			t.Fatalf("duplicate operation event type %q mapped from %q and %q", eventType, previous, operationType)
		}
		seenMappedTypes[eventType] = operationType
	}
}

func TestOperationEventTypesContainOnlyKnownUniqueEventTypes(t *testing.T) {
	eventTypes := EventTypes()
	knownEventTypes := make(map[EventType]bool, len(eventTypes))
	for _, eventType := range eventTypes {
		knownEventTypes[eventType] = true
	}

	mapped := OperationEventTypes()
	seen := make(map[EventType]string, len(mapped))
	for operationType, eventType := range mapped {
		if strings.TrimSpace(operationType) == "" {
			t.Fatalf("OperationEventTypes contains empty operation type")
		}
		if eventType == "" {
			t.Fatalf("OperationEventTypes[%q] = empty", operationType)
		}
		if !knownEventTypes[eventType] {
			t.Fatalf("OperationEventTypes[%q] = %q, not in EventTypes()", operationType, eventType)
		}
		if previous, ok := seen[eventType]; ok {
			t.Fatalf("duplicate operation event type %q mapped from %q and %q", eventType, previous, operationType)
		}
		seen[eventType] = operationType
	}
}

func TestEventTypeAccessorsReturnDefensiveCopies(t *testing.T) {
	eventTypes := EventTypes()
	if len(eventTypes) == 0 {
		t.Fatalf("EventTypes() returned empty list")
	}
	eventTypes[0] = EventType("mutated")
	if got := EventTypes()[0]; got == EventType("mutated") {
		t.Fatalf("EventTypes() returned mutable backing storage")
	}

	operationEventTypes := OperationEventTypes()
	if len(operationEventTypes) == 0 {
		t.Fatalf("OperationEventTypes() returned empty map")
	}
	for operationType := range operationEventTypes {
		operationEventTypes[operationType] = EventType("mutated")
		break
	}
	for operationType, eventType := range OperationEventTypes() {
		if eventType == EventType("mutated") {
			t.Fatalf("OperationEventTypes() returned mutable backing storage at %q", operationType)
		}
	}
}

func TestDeniedEventsAllowEmptyOperationID(t *testing.T) {
	for _, eventType := range []EventType{
		EventTypeAuthzDenied,
		EventTypePathDenied,
		EventTypeCapabilityDenied,
		EventTypeResourceNamespaceMismatchDenied,
	} {
		event := NewEvent(Event{
			EventID:         "evt_denied",
			Type:            eventType,
			Time:            time.Date(2026, 5, 4, 22, 10, 0, 0, time.UTC),
			CallerService:   "product-caller",
			AuthorizedActor: Actor{Type: "user", ID: "user_123"},
			CorrelationID:   "corr_denied",
			Resource:        Resource{Type: "repo", ID: "repo_123", Path: "/payload/readme.md"},
			Outcome:         OutcomeDenied,
			Reason:          "request denied before operation creation",
			Details: map[string]any{
				"method": "PUT",
			},
		})

		if event.OperationID != "" {
			t.Fatalf("%s OperationID = %q, want empty", eventType, event.OperationID)
		}

		var decoded map[string]any
		body, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("%s MarshalJSON returned error: %v", eventType, err)
		}
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Fatalf("%s json.Unmarshal returned error: %v", eventType, err)
		}
		if _, ok := decoded["operation_id"]; !ok {
			t.Fatalf("%s marshaled event missing operation_id field: %s", eventType, string(body))
		}
		if got := decoded["operation_id"]; got != "" {
			t.Fatalf("%s operation_id = %#v, want empty string", eventType, got)
		}
		if got := decoded["outcome"]; got != string(OutcomeDenied) {
			t.Fatalf("%s outcome = %#v, want %q", eventType, got, OutcomeDenied)
		}
	}
}

func TestEventJSONContainsStableAuditFields(t *testing.T) {
	event := NewEvent(Event{
		EventID:         "evt_fields",
		Type:            EventTypeRepoPurge,
		Time:            time.Date(2026, 5, 4, 22, 15, 0, 0, time.UTC),
		CallerService:   "product-caller",
		AuthorizedActor: Actor{Type: "operator", ID: "operator_123"},
		CorrelationID:   "corr_fields",
		OperationID:     "op_fields",
		Resource:        Resource{Type: "repo", ID: "repo_123", NamespaceID: "ns_123"},
		Outcome:         OutcomeFailed,
		Reason:          "retention policy blocked purge",
		Details: map[string]any{
			"policy": "retain_30d",
		},
	})

	body, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("MarshalJSON returned error: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}

	for _, key := range []string{
		"event_id",
		"type",
		"time",
		"caller_service",
		"authorized_actor",
		"correlation_id",
		"operation_id",
		"resource",
		"outcome",
		"reason",
		"details",
	} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("marshaled event missing %q: %s", key, string(body))
		}
	}
}

type recordingSink struct {
	event Event
}

func (sink *recordingSink) Emit(_ context.Context, event Event) error {
	sink.event = event
	return nil
}

var _ Sink = (*recordingSink)(nil)

func TestSinkInterfaceAcceptsEvents(t *testing.T) {
	sink := &recordingSink{}
	event := NewEvent(Event{
		EventID:       "evt_sink",
		Type:          EventTypeAuthzDenied,
		Time:          time.Date(2026, 5, 4, 22, 20, 0, 0, time.UTC),
		CallerService: "product-caller",
		CorrelationID: "corr_sink",
		Resource:      Resource{Type: "route", ID: "POST /internal/v1/repos"},
		Outcome:       OutcomeDenied,
		Reason:        "caller missing role",
		Details: map[string]any{
			"token": "token-value",
		},
	})

	if err := sink.Emit(context.Background(), event); err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	if got := sink.event.Details["token"]; got != observability.Redacted {
		t.Fatalf("sink event Details[token] = %#v, want %q", got, observability.Redacted)
	}
}

func auditEventTestString(t *testing.T, value any) string {
	t.Helper()

	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal audit test value: %v", err)
	}
	return string(encoded)
}

func auditSecretDetailsForTest() map[string]any {
	return map[string]any{
		"metadata_url":    "redis://:detail-metadata-secret@metadata:6379/1",
		"accessKey":       "detail-access-key",
		"token":           "detail-token",
		"password":        "detail-password",
		"Secret ref":      "ns/detail-secret-ref",
		"WebDAV password": "detail-webdav-password",
		"repo_id":         "repo_123",
		"nested": map[string]any{
			"token":        "nested-token",
			"password":     "nested-password",
			"metadata_url": "redis://:nested-metadata-secret@metadata:6379/1",
		},
	}
}
