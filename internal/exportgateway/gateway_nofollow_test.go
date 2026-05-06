package exportgateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/exportaccess"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
)

func TestGatewayBackendRejectsSymlinkSwappedAfterValidation(t *testing.T) {
	env := newGatewayTestEnv(t, sessionstate.AccessModeReadOnly, sessionstate.ExportStatusActive)
	env.writePayload(t, "swap.txt", "inside")
	outside := t.TempDir()
	outsideSecret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(outsideSecret, []byte("outside-secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := &swapAfterValidationStore{
		credential: env.store.credential,
		onFirstObservation: func() {
			target := filepath.Join(env.payloadRoot, "swap.txt")
			if err := os.Remove(target); err != nil {
				t.Errorf("remove swap target: %v", err)
				return
			}
			if err := os.Symlink(outsideSecret, target); err != nil {
				t.Errorf("symlink swap target: %v", err)
			}
		},
	}
	handler, err := NewHandler(Config{
		Store:       store,
		VolumeRoots: map[string]string{testVolumeID: env.volumeRoot},
		Prefix:      "/e/",
		Now:         fixedGatewayNow,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://files.example.test/e/"+testExportID+"/swap.txt", nil)
	req.SetBasicAuth(testExportID, testPassword)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code < 400 {
		t.Fatalf("status = %d, want backend no-follow rejection", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "outside-secret") {
		t.Fatalf("response exposed swapped symlink target: %q", rec.Body.String())
	}
	if len(store.observations) != 2 {
		t.Fatalf("runtime observations = %d, want 2", len(store.observations))
	}
	if store.observations[1].SuccessfulRequestAccessedAt != nil {
		t.Fatal("failed backend response was recorded as successful access")
	}
}

type swapAfterValidationStore struct {
	credential         exportaccess.GatewayCredential
	onFirstObservation func()
	observations       []exportaccess.RuntimeObservation
}

func (store *swapAfterValidationStore) GetExportGatewayCredential(ctx context.Context, exportID string) (exportaccess.GatewayCredential, error) {
	if exportID != store.credential.Session.ID {
		return exportaccess.GatewayCredential{}, errors.New("not found")
	}
	return store.credential, nil
}

func (store *swapAfterValidationStore) RecordExportRuntimeObservation(ctx context.Context, observation exportaccess.RuntimeObservation) (exportaccess.Session, error) {
	store.observations = append(store.observations, observation)
	if len(store.observations) == 1 && store.onFirstObservation != nil {
		store.onFirstObservation()
	}
	session := store.credential.Session
	session.ActiveRequestCount += observation.ActiveRequestDelta
	session.ActiveWriteCount += observation.ActiveWriteDelta
	session.LastObservedAt = ptrTime(observation.ObservedAt)
	session.LastGatewayHeartbeatAt = cloneTimePtr(observation.GatewayHeartbeatAt)
	session.GatewayHeartbeatExpiresAt = cloneTimePtr(observation.GatewayHeartbeatExpiresAt)
	if observation.SuccessfulRequestAccessedAt != nil {
		session.LastAccessedAt = cloneTimePtr(observation.SuccessfulRequestAccessedAt)
	}
	store.credential.Session = session
	return session, nil
}
