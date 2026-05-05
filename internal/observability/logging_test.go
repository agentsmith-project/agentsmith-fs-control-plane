package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

func TestLogEventEmitsRedactedStructuredJSON(t *testing.T) {
	var logs bytes.Buffer
	logger := NewJSONLogger(&logs, nil)

	LogEvent(context.Background(), logger, slog.LevelInfo, "afscp.request.denied", "capability denied", map[string]any{
		"correlation_id": "corr_123",
		"operation_id":   "createRepo",
		"route":          "/internal/v1/repos",
		"method":         http.MethodPost,
		"path":           "/internal/v1/repos",
		"metadata_url":   "redis://:metadata-secret@metadata:6379/1",
		"Authorization":  "Bearer authorization-token",
		"headers": map[string]string{
			"Authorization": "Bearer nested-authorization-token",
			"X-Request":     "request-id",
		},
	})

	entry := decodeSingleLogEntry(t, logs.Bytes())

	for _, key := range []string{"time", "level", "message", "event"} {
		if _, ok := entry[key]; !ok {
			t.Fatalf("log entry missing %q: %#v", key, entry)
		}
	}
	if got, want := entry["level"], "INFO"; got != want {
		t.Fatalf("level = %#v, want %#v", got, want)
	}
	if got, want := entry["message"], "capability denied"; got != want {
		t.Fatalf("message = %#v, want %#v", got, want)
	}
	if got, want := entry["event"], "afscp.request.denied"; got != want {
		t.Fatalf("event = %#v, want %#v", got, want)
	}
	if got, want := entry["correlation_id"], "corr_123"; got != want {
		t.Fatalf("correlation_id = %#v, want %#v", got, want)
	}
	if got, want := entry["operation_id"], "createRepo"; got != want {
		t.Fatalf("operation_id = %#v, want %#v", got, want)
	}
	if got, want := entry["route"], "/internal/v1/repos"; got != want {
		t.Fatalf("route = %#v, want %#v", got, want)
	}
	if got, want := entry["method"], http.MethodPost; got != want {
		t.Fatalf("method = %#v, want %#v", got, want)
	}
	if got, want := entry["path"], "/internal/v1/repos"; got != want {
		t.Fatalf("path = %#v, want %#v", got, want)
	}
	if got := entry["metadata_url"]; got != Redacted {
		t.Fatalf("metadata_url = %#v, want %q", got, Redacted)
	}
	if got := entry["Authorization"]; got != Redacted {
		t.Fatalf("Authorization = %#v, want %q", got, Redacted)
	}

	headers, ok := entry["headers"].(map[string]any)
	if !ok {
		t.Fatalf("headers = %T, want map[string]any", entry["headers"])
	}
	if got := headers["Authorization"]; got != Redacted {
		t.Fatalf("headers.Authorization = %#v, want %q", got, Redacted)
	}
	if got, want := headers["X-Request"], "request-id"; got != want {
		t.Fatalf("headers.X-Request = %#v, want %#v", got, want)
	}

	rendered := logs.String()
	for _, leaked := range []string{
		"metadata-secret",
		"authorization-token",
		"nested-authorization-token",
		"redis://",
	} {
		if strings.Contains(rendered, leaked) {
			t.Fatalf("structured log leaked sensitive material %q in %s", leaked, rendered)
		}
	}
}

func TestNewJSONLoggerRedactsDirectSlogAttributesAndMessage(t *testing.T) {
	var logs bytes.Buffer
	logger := NewJSONLogger(&logs, nil)

	logger.WarnContext(
		context.Background(),
		`metadata postgres://user:metadata-secret@metadata.internal:5432/jfs Authorization: Bearer message-token`,
		slog.String("path", `/payload --token path-token redis://:path-metadata-secret@metadata:6379/1`),
		slog.Group("headers",
			slog.String("Authorization", "Bearer nested-authorization-token"),
			slog.String("X-Request", "request-id"),
		),
	)

	entry := decodeSingleLogEntry(t, logs.Bytes())
	if got, want := entry["level"], "WARN"; got != want {
		t.Fatalf("level = %#v, want %#v", got, want)
	}

	rendered := logs.String()
	for _, leaked := range []string{
		"metadata-secret",
		"message-token",
		"path-token",
		"path-metadata-secret",
		"nested-authorization-token",
		"postgres://",
		"redis://",
	} {
		if strings.Contains(rendered, leaked) {
			t.Fatalf("structured log leaked sensitive material %q in %s", leaked, rendered)
		}
	}
	if !strings.Contains(rendered, Redacted) {
		t.Fatalf("structured log did not contain redaction marker: %s", rendered)
	}
	if !strings.Contains(rendered, `"message"`) {
		t.Fatalf("structured log must expose the slog message as message: %s", rendered)
	}
}

func TestLogEventNilLoggerIsNoop(t *testing.T) {
	LogEvent(context.Background(), nil, slog.LevelInfo, "afscp.noop", "noop", map[string]any{
		"Authorization": "Bearer no-op-token",
	})
}

func decodeSingleLogEntry(t *testing.T, body []byte) map[string]any {
	t.Helper()

	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		t.Fatal("expected one structured log entry, got empty output")
	}
	if bytes.Count(trimmed, []byte("\n")) != 0 {
		t.Fatalf("expected one structured log entry, got %q", string(body))
	}

	var entry map[string]any
	if err := json.Unmarshal(trimmed, &entry); err != nil {
		t.Fatalf("structured log did not decode as JSON: %v: %s", err, string(body))
	}
	return entry
}
