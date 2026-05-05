package observability

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestRedactFieldsRedactsSensitiveAFSCPKeys(t *testing.T) {
	fields := map[string]any{
		"metadata_url":          "redis://:secret@metadata:6379/1",
		"access_key":            "access-key",
		"secret_key":            "secret-key",
		"password":              "password",
		"token":                 "token",
		"kubernetes_secret_ref": "afscp/root-credentials",
		"webdav_password":       "webdav-password",
		"repo_id":               "repo_123",
	}

	redacted := RedactFields(fields)

	for _, key := range []string{
		"metadata_url",
		"access_key",
		"secret_key",
		"password",
		"token",
		"kubernetes_secret_ref",
		"webdav_password",
	} {
		if got := redacted[key]; got != Redacted {
			t.Fatalf("%s = %#v, want %q", key, got, Redacted)
		}
	}

	if got, want := redacted["repo_id"], "repo_123"; got != want {
		t.Fatalf("repo_id = %#v, want %#v", got, want)
	}
	if got, want := fields["access_key"], "access-key"; got != want {
		t.Fatalf("input access_key mutated to %#v, want %#v", got, want)
	}
}

func TestRedactFieldsRedactsCamelCaseHeadersAFSCPKeysAndBearerValues(t *testing.T) {
	fields := map[string]any{
		"metadataUrl":           "redis://metadata.camel/1",
		"storage_bucket_url":    "s3://bucket/root",
		"storageBucketURL":      "https://bucket.camel/root",
		"object_store_endpoint": "https://object-store.internal",
		"objectStoreEndpoint":   "https://object-store.camel",
		"accessKey":             "access-key",
		"secretAccessKey":       "secret-access-key",
		"apiKey":                "api-key",
		"privateKey":            "private-key",
		"Authorization":         "Bearer authorization-token",
		"Cookie":                "session=secret",
		"secret_ref":            "vault://secret/ref",
		"k8s secret":            "namespace/name",
		"webdav password":       "webdav-password",
		"message":               "Bearer message-token",
		"repo_id":               "repo_123",
		"headers": map[string]string{
			"Authorization": "Bearer nested-authorization-token",
			"Cookie":        "nested=session",
			"X-Trace":       "trace-ok",
			"Message":       "Bearer nested-message-token",
		},
	}

	redacted := RedactFields(fields)

	for _, key := range []string{
		"metadataUrl",
		"storage_bucket_url",
		"storageBucketURL",
		"object_store_endpoint",
		"objectStoreEndpoint",
		"accessKey",
		"secretAccessKey",
		"apiKey",
		"privateKey",
		"Authorization",
		"Cookie",
		"secret_ref",
		"k8s secret",
		"webdav password",
		"message",
	} {
		if got := redacted[key]; got != Redacted {
			t.Fatalf("%s = %#v, want %q", key, got, Redacted)
		}
	}

	headers, ok := redacted["headers"].(map[string]string)
	if !ok {
		t.Fatalf("headers redacted as %T, want map[string]string", redacted["headers"])
	}
	for _, key := range []string{"Authorization", "Cookie", "Message"} {
		if got := headers[key]; got != Redacted {
			t.Fatalf("headers[%s] = %#v, want %q", key, got, Redacted)
		}
	}
	if got, want := headers["X-Trace"], "trace-ok"; got != want {
		t.Fatalf("headers[X-Trace] = %#v, want %#v", got, want)
	}
	if got, want := redacted["repo_id"], "repo_123"; got != want {
		t.Fatalf("repo_id = %#v, want %#v", got, want)
	}
}

func TestRedactFieldsRedactsNestedSensitiveKeys(t *testing.T) {
	fields := map[string]any{
		"export": map[string]any{
			"auth": map[string]any{
				"password": "webdav-password",
			},
			"secret_ref": "afscp/export-webdav",
		},
		"volume": map[string]string{
			"metadata_url": "redis://metadata/1",
			"name":         "shared",
		},
	}

	redacted := RedactFields(fields)

	exportFields, ok := redacted["export"].(map[string]any)
	if !ok {
		t.Fatalf("export redacted as %T, want map[string]any", redacted["export"])
	}
	authFields, ok := exportFields["auth"].(map[string]any)
	if !ok {
		t.Fatalf("export.auth redacted as %T, want map[string]any", exportFields["auth"])
	}
	if got := authFields["password"]; got != Redacted {
		t.Fatalf("export.auth.password = %#v, want %q", got, Redacted)
	}
	if got := exportFields["secret_ref"]; got != Redacted {
		t.Fatalf("export.secret_ref = %#v, want %q", got, Redacted)
	}

	volumeFields, ok := redacted["volume"].(map[string]string)
	if !ok {
		t.Fatalf("volume redacted as %T, want map[string]string", redacted["volume"])
	}
	if got := volumeFields["metadata_url"]; got != Redacted {
		t.Fatalf("volume.metadata_url = %#v, want %q", got, Redacted)
	}
	if got, want := volumeFields["name"], "shared"; got != want {
		t.Fatalf("volume.name = %#v, want %#v", got, want)
	}
}

func TestNewEventRedactsFields(t *testing.T) {
	event := NewEvent("volume.check", map[string]any{
		"token":   "bearer-token",
		"volume":  "shared",
		"message": "volume capability check",
	})

	if got, want := event.Name, "volume.check"; got != want {
		t.Fatalf("Name = %q, want %q", got, want)
	}
	if got := event.Fields["token"]; got != Redacted {
		t.Fatalf("Fields[token] = %#v, want %q", got, Redacted)
	}
	if got, want := event.Fields["volume"], "shared"; got != want {
		t.Fatalf("Fields[volume] = %#v, want %#v", got, want)
	}
}

func TestRedactFieldsScrubsRawSecretStringForms(t *testing.T) {
	fields := map[string]any{
		"reason": `metadata postgres://user:metadata-secret@metadata.internal:5432/jfs token=assignment-token {"password":"json-password"} access_key: colon-key --password cli-password Authorization: Bearer bearer-token`,
		"path":   `/volumes/shared --token path-token redis://:path-metadata-secret@metadata:6379/1`,
	}

	redacted := RedactFields(fields)
	rendered := strings.ToLower(observabilityTestString(redacted))

	for _, forbidden := range []string{
		"metadata-secret",
		"assignment-token",
		"json-password",
		"colon-key",
		"cli-password",
		"bearer-token",
		"path-token",
		"path-metadata-secret",
		"postgres://",
		"redis://",
	} {
		if strings.Contains(rendered, strings.ToLower(forbidden)) {
			t.Fatalf("secret material %q leaked in %#v", forbidden, redacted)
		}
	}
	if !strings.Contains(rendered, strings.ToLower(Redacted)) {
		t.Fatalf("redacted marker missing from %#v", redacted)
	}
}

func observabilityTestString(value any) string {
	encoded, err := json.Marshal(value)
	if err == nil {
		return string(encoded)
	}
	return fmt.Sprint(value)
}
