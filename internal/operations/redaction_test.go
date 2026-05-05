package operations

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestSanitizeRecordRedactsSummariesErrorsAndDetails(t *testing.T) {
	record := OperationRecord{
		ID:    "op-1",
		Type:  OperationExportCreate,
		State: OperationStateFailed,
		InputSummary: map[string]any{
			"webdav_password": "plain-webdav-password",
			"metadata_url":    "redis://:metadata-secret@127.0.0.1:6379/1",
			"safe":            "kept",
			"nested": map[string]any{
				"secret_value": "nested-secret",
			},
		},
		Error: &OperationError{
			Code:          "OPERATION_RECOVERY_REQUIRED",
			Message:       "failed with bearer token abc.def.ghi and AWS_SECRET_ACCESS_KEY=super-secret",
			Retryable:     true,
			CorrelationID: "corr-1",
			Details: map[string]any{
				"access_key": "AKIAIOSFODNN7EXAMPLE",
				"command":    "mount -o password=webdav-command-secret https://example.invalid",
			},
		},
	}

	sanitized := record.Sanitized()

	if sanitized.InputSummary["safe"] != "kept" {
		t.Fatalf("safe input field was not preserved: %#v", sanitized.InputSummary)
	}
	assertNoSecretMaterial(t, sanitized.InputSummary)
	if sanitized.Error == nil {
		t.Fatal("expected sanitized error")
	}
	assertNoSecretMaterial(t, sanitized.Error.Message)
	assertNoSecretMaterial(t, sanitized.Error.Details)
	if !sanitized.Redaction.Redacted {
		t.Fatalf("expected redaction marker")
	}
}

func TestSanitizeRecordRedactsExternalResourceIDs(t *testing.T) {
	record := OperationRecord{
		ID:                  "op-1",
		Type:                OperationRepoCreate,
		State:               OperationStateSucceeded,
		ExternalResourceIDs: map[string]string{"jvs_repo_id": "jvs-secret", "volume": "vol-secret"},
	}

	sanitized := record.Sanitized()
	if got := sanitized.ExternalResourceIDs["jvs_repo_id"]; got != redactedValue {
		t.Fatalf("jvs_repo_id = %q, want redacted value", got)
	}
	if got := sanitized.ExternalResourceIDs["volume"]; got != redactedValue {
		t.Fatalf("volume = %q, want redacted value", got)
	}
	assertNoSecretMaterial(t, sanitized.ExternalResourceIDs)
	if !sanitized.Redaction.Redacted {
		t.Fatalf("expected redaction marker")
	}
}

func TestOperationRecordMarshalRedactsBeforePersistence(t *testing.T) {
	record := OperationRecord{
		ID:                  "op-1",
		Type:                OperationExportCreate,
		State:               OperationStateQueued,
		ExternalResourceIDs: map[string]string{"jvs_repo_id": "jvs-secret"},
		InputSummary: map[string]any{
			"command": "export --token persist-token-secret",
		},
	}

	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal operation record: %v", err)
	}

	rendered := strings.ToLower(string(encoded))
	for _, forbidden := range []string{"jvs-secret", "persist-token-secret"} {
		if strings.Contains(rendered, strings.ToLower(forbidden)) {
			t.Fatalf("secret material %q leaked in %s", forbidden, rendered)
		}
	}
}

func TestRedactValueHandlesRawSecretStringForms(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		forbidden []string
	}{
		{
			name:      "key value assignment",
			input:     "token=plain-token",
			forbidden: []string{"plain-token"},
		},
		{
			name:      "json quoted colon",
			input:     `"password":"json-secret"`,
			forbidden: []string{"json-secret"},
		},
		{
			name:      "json quoted colon with whitespace",
			input:     `"access_key": "json-access-secret"`,
			forbidden: []string{"json-access-secret"},
		},
		{
			name:      "plain colon",
			input:     "secret_key: colon-secret",
			forbidden: []string{"colon-secret"},
		},
		{
			name:      "cli token",
			input:     "--token cli-token-secret",
			forbidden: []string{"cli-token-secret"},
		},
		{
			name:      "cli password",
			input:     "--password cli-password-secret",
			forbidden: []string{"cli-password-secret"},
		},
		{
			name:      "authorization bearer",
			input:     "Authorization: Bearer bearer-secret",
			forbidden: []string{"bearer-secret"},
		},
		{
			name:      "bearer token variant",
			input:     "bearer token bearer-token-secret",
			forbidden: []string{"bearer-token-secret"},
		},
		{
			name:      "metadata URL",
			input:     "metadata at postgres://user:metadata-secret@metadata.internal:5432/jfs",
			forbidden: []string{"metadata-secret", "postgres://"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redacted, report := RedactValue(tt.input)
			rendered := strings.ToLower(toTestString(redacted))
			for _, forbidden := range tt.forbidden {
				if strings.Contains(rendered, strings.ToLower(forbidden)) {
					t.Fatalf("secret material %q leaked in %s", forbidden, rendered)
				}
			}
			if !report.Redacted {
				t.Fatalf("expected redaction report for %q", tt.input)
			}
		})
	}
}

func TestOperationRecordEnvelopeIsAlwaysSanitized(t *testing.T) {
	envelope := NewOperationRecordEnvelope(OperationRecord{
		ID:    "op-1",
		Type:  OperationExportCreate,
		State: OperationStateFailed,
		InputSummary: map[string]any{
			"webdav_password": "plain-webdav-password",
		},
		Error: &OperationError{
			Code:          "OPERATION_RECOVERY_REQUIRED",
			Message:       "metadata at mysql://user:metadata-secret@metadata.internal:3306/jfs",
			Retryable:     false,
			CorrelationID: "corr-1",
			Details: map[string]any{
				"Secret": map[string]any{
					"value": "kubernetes-secret-value",
				},
			},
		},
	})

	assertNoSecretMaterial(t, envelope.Operation.InputSummary)
	if envelope.Error == nil {
		t.Fatal("expected envelope error")
	}
	assertNoSecretMaterial(t, envelope.Error.Message)
	assertNoSecretMaterial(t, envelope.Error.Details)
	if !envelope.Redaction.Redacted {
		t.Fatalf("expected redaction marker on envelope")
	}
}

func TestSanitizedForPersistenceRedactsQueuedRecordBeforeStoreWrite(t *testing.T) {
	record := OperationRecord{
		ID:                  "op-persist",
		Type:                OperationExportCreate,
		State:               OperationStateQueued,
		ExternalResourceIDs: map[string]string{"jvs_repo_id": "jvs-persistence-secret"},
		InputSummary: map[string]any{
			"command":  "export --token persistence-token",
			"metadata": "redis://:persistence-metadata-secret@metadata:6379/1",
			"safe":     "kept",
		},
	}

	sanitized := record.SanitizedForPersistence().Record()

	if record.ExternalResourceIDs["jvs_repo_id"] != "jvs-persistence-secret" {
		t.Fatalf("raw record was mutated: %#v", record.ExternalResourceIDs)
	}
	if got := sanitized.InputSummary["safe"]; got != "kept" {
		t.Fatalf("safe input field was not preserved: %#v", sanitized.InputSummary)
	}
	if got := sanitized.ExternalResourceIDs["jvs_repo_id"]; got != redactedValue {
		t.Fatalf("jvs_repo_id = %q, want redacted value", got)
	}
	assertNoSecretMaterial(t, sanitized.InputSummary)
	assertNoSecretMaterial(t, sanitized.ExternalResourceIDs)
	if !sanitized.Redaction.Redacted {
		t.Fatalf("expected persistence record to carry redaction report")
	}
}

func assertNoSecretMaterial(t *testing.T, value any) {
	t.Helper()

	rendered := strings.ToLower(toTestString(value))
	for _, forbidden := range []string{
		"plain-webdav-password",
		"metadata-secret",
		"nested-secret",
		"super-secret",
		"akiaiosfodnn7example",
		"webdav-command-secret",
		"kubernetes-secret-value",
		"abc.def.ghi",
		"jvs-persistence-secret",
		"persistence-token",
		"persistence-metadata-secret",
		"redis://",
		"mysql://",
	} {
		if strings.Contains(rendered, strings.ToLower(forbidden)) {
			t.Fatalf("secret material %q leaked in %s", forbidden, rendered)
		}
	}
}

func toTestString(value any) string {
	encoded, err := json.Marshal(value)
	if err == nil {
		return string(encoded)
	}
	return fmt.Sprint(value)
}
