package exportaccess

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
)

func TestPasswordVerifierAcceptsOnlyOriginalSecret(t *testing.T) {
	verifier, err := NewPasswordVerifier("secret-once", []byte("fixed-test-salt-32-bytes-long!!"))
	if err != nil {
		t.Fatalf("NewPasswordVerifier: %v", err)
	}

	if !verifier.Verify("secret-once") {
		t.Fatal("verifier rejected original password")
	}
	if verifier.Verify("other-secret") {
		t.Fatal("verifier accepted wrong password")
	}
	if strings.Contains(verifier.Hash, "secret-once") || strings.Contains(verifier.Salt, "secret-once") {
		t.Fatalf("verifier stored raw password: %#v", verifier)
	}
	if err := verifier.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestResolveTTLSecondsAppliesDefaultMinAndPolicyMax(t *testing.T) {
	tests := []struct {
		name      string
		requested int
		max       int
		want      int
		wantErr   bool
	}{
		{name: "default under policy max", max: 7200, want: DefaultTTLSeconds},
		{name: "default clamped to policy max", max: 300, want: 300},
		{name: "minimum", requested: MinTTLSeconds, max: 3600, want: MinTTLSeconds},
		{name: "below minimum", requested: MinTTLSeconds - 1, max: 3600, wantErr: true},
		{name: "above max", requested: 3601, max: 3600, wantErr: true},
		{name: "invalid policy max", requested: MinTTLSeconds, max: MinTTLSeconds - 1, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveTTLSeconds(tt.requested, tt.max)
			if tt.wantErr {
				if err == nil {
					t.Fatal("ResolveTTLSeconds succeeded, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveTTLSeconds: %v", err)
			}
			if got != tt.want {
				t.Fatalf("ttl = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSessionValidationKeepsCredentialFieldsOutOfAPIModel(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	session := Session{
		ID:                     "export_123",
		NamespaceID:            "ns_123",
		RepoID:                 "repo_123",
		Protocol:               ProtocolWebDAV,
		Mode:                   sessionstate.AccessModeReadWrite,
		Status:                 sessionstate.ExportStatusRevoking,
		ExpiresAt:              now.Add(time.Hour),
		CreatedByCallerService: "agentsmith-api",
		CreatedByActor:         Actor{Type: "user", ID: "user_123"},
		ActiveRequestCount:     2,
		ActiveWriteCount:       1,
		LastObservedAt:         timePtr(now.Add(time.Minute)),
		LastGatewayHeartbeatAt: timePtr(now.Add(time.Minute)),
		GatewayHeartbeatExpiresAt: timePtr(now.Add(2 *
			time.Minute)),
		StatusReason: "gateway observed active writers",
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := session.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	rendered := string(mustMarshalJSONForTest(t, session))
	for _, forbidden := range []string{"password", "secret", "credential_hash", "credential_salt", "raw_path", "storage"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("session JSON leaked %q: %s", forbidden, rendered)
		}
	}
	for _, want := range []string{"active_request_count", "active_write_count", "last_observed_at", "last_gateway_heartbeat_at", "gateway_heartbeat_expires_at", "status_reason"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("session JSON missing runtime field %q: %s", want, rendered)
		}
	}
}

func TestSessionValidationRejectsInvalidRuntimeCounts(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	session := Session{
		ID:                     "export_123",
		NamespaceID:            "ns_123",
		RepoID:                 "repo_123",
		Protocol:               ProtocolWebDAV,
		Mode:                   sessionstate.AccessModeReadWrite,
		Status:                 sessionstate.ExportStatusActive,
		ExpiresAt:              now.Add(time.Hour),
		CreatedByCallerService: "agentsmith-api",
		CreatedByActor:         Actor{Type: "user", ID: "user_123"},
		ActiveRequestCount:     1,
		ActiveWriteCount:       2,
		CreatedAt:              now,
		UpdatedAt:              now,
	}

	if err := session.Validate(); err == nil {
		t.Fatal("Validate accepted active_write_count greater than active_request_count")
	}
	session.ActiveRequestCount = -1
	session.ActiveWriteCount = 0
	if err := session.Validate(); err == nil {
		t.Fatal("Validate accepted negative active_request_count")
	}
}

func mustMarshalJSONForTest(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return data
}

func timePtr(t time.Time) *time.Time {
	return &t
}
