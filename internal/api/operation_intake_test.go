package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
)

func TestCreateOrReuseOperationIntakeCreatesQueuedOperationEnvelope(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	store := &fakeOperationIntakeStore{}
	route, _ := RouteMetadataByOperationID("putNamespaceVolumeBinding")
	canonical := map[string]any{"namespace_id": "ns_123", "default_volume_id": "vol_123"}
	inputSummary := map[string]any{"default_volume_id": "vol_123"}
	externalIDs := map[string]string{"volume_id": "vol_123"}

	envelope, err := CreateOrReuseOperationIntake(context.Background(), OperationIntakeConfig{Store: store}, OperationIntakeRequest{
		RequestContext: auth.RequestContext{
			IdempotencyKey: "idem_123",
			CorrelationID:  "corr_123",
			NamespaceID:    "ns_123",
			Actor:          auth.Actor{Type: "user", ID: "user_123"},
			CallerService:  "agentsmith-api",
		},
		Route:               route,
		NamespaceID:         "ns_123",
		Resource:            operations.ResourceRef{Type: "namespace_volume_binding", ID: "ns_123"},
		ExternalResourceIDs: externalIDs,
		CanonicalRequest:    canonical,
		InputSummary:        inputSummary,
		Phase:               "validate_binding",
		GenerateOperationID: func() string { return "op_123" },
		Now:                 func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("CreateOrReuseOperationIntake returned error: %v", err)
	}

	if store.calls != 1 {
		t.Fatalf("store calls = %d, want 1", store.calls)
	}
	spec := store.spec
	wantType := operations.OperationNamespaceVolumeBindingPut
	wantScope := operations.NewIdempotencyScope("agentsmith-api", "ns_123", wantType, "idem_123")
	if spec.OperationID != "op_123" || spec.Scope != wantScope || spec.Scope.ConstraintKey() != wantScope.ConstraintKey() {
		t.Fatalf("spec operation/scope = %q/%#v, want op_123/%#v", spec.OperationID, spec.Scope, wantScope)
	}
	if spec.RequestHash == "" {
		t.Fatal("spec RequestHash is empty")
	}
	wantHash, err := operations.HashRequest(canonical)
	if err != nil {
		t.Fatalf("hash canonical request: %v", err)
	}
	if spec.RequestHash != wantHash {
		t.Fatalf("request hash = %q, want canonical request hash %q", spec.RequestHash, wantHash)
	}
	if spec.Phase != "validate_binding" || spec.CorrelationID != "corr_123" || spec.CallerService != "agentsmith-api" {
		t.Fatalf("spec phase/correlation/caller = %q/%q/%q", spec.Phase, spec.CorrelationID, spec.CallerService)
	}
	if spec.NamespaceID != "ns_123" || spec.Resource.Type != "namespace_volume_binding" || spec.Resource.ID != "ns_123" {
		t.Fatalf("spec namespace/resource = %q/%#v", spec.NamespaceID, spec.Resource)
	}
	if spec.AuthorizedActor.Type != "user" || spec.AuthorizedActor.ID != "user_123" {
		t.Fatalf("spec actor = %#v", spec.AuthorizedActor)
	}
	if spec.ExternalResourceIDs["volume_id"] != "vol_123" || spec.InputSummary["default_volume_id"] != "vol_123" {
		t.Fatalf("spec external/input summary = %#v/%#v", spec.ExternalResourceIDs, spec.InputSummary)
	}
	if !spec.CreatedAt.Equal(now) {
		t.Fatalf("CreatedAt = %v, want %v", spec.CreatedAt, now)
	}

	if envelope.OperationID != "op_123" || envelope.OperationState != OperationStateQueued {
		t.Fatalf("envelope id/state = %q/%q, want op_123/queued", envelope.OperationID, envelope.OperationState)
	}
	if envelope.Resource.Type != "namespace_volume_binding" || envelope.Resource.ID != "ns_123" {
		t.Fatalf("envelope resource = %#v", envelope.Resource)
	}
	assertOperationEnvelopeDoesNotLeakInternalFields(t, envelope)
}

func TestCreateOrReuseOperationIntakeReusesExistingOperation(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	store := &fakeOperationIntakeStore{existingOperationID: "op_existing", reused: true}
	route, _ := RouteMetadataByOperationID("createRepo")

	envelope, err := CreateOrReuseOperationIntake(context.Background(), OperationIntakeConfig{Store: store}, OperationIntakeRequest{
		RequestContext: auth.RequestContext{
			IdempotencyKey: "idem_123",
			CorrelationID:  "corr_123",
			NamespaceID:    "ns_123",
			Actor:          auth.Actor{Type: "user", ID: "user_123"},
			CallerService:  "agentsmith-api",
		},
		Route:               route,
		NamespaceID:         "ns_123",
		RepoID:              "repo_123",
		Resource:            operations.ResourceRef{Type: "repo", ID: "repo_123"},
		CanonicalRequest:    map[string]any{"repo_id": "repo_123"},
		InputSummary:        map[string]any{"repo_id": "repo_123"},
		Phase:               "allocate_repo_path",
		GenerateOperationID: func() string { return "op_new" },
		Now:                 func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("CreateOrReuseOperationIntake returned error: %v", err)
	}
	if envelope.OperationID != "op_existing" || envelope.OperationState != OperationStateQueued {
		t.Fatalf("envelope = %#v, want existing queued operation", envelope)
	}
	assertOperationEnvelopeDoesNotLeakInternalFields(t, envelope)
}

func TestCreateOrReuseOperationIntakeAllowsVolumeGlobalWithoutNamespace(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	store := &fakeOperationIntakeStore{}
	route, _ := RouteMetadataByOperationID("ensureVolume")

	envelope, err := CreateOrReuseOperationIntake(context.Background(), OperationIntakeConfig{Store: store}, OperationIntakeRequest{
		RequestContext: auth.RequestContext{
			IdempotencyKey: "idem_123",
			CorrelationID:  "corr_123",
			Actor:          auth.Actor{Type: "admin_job", ID: "job_123"},
			CallerService:  "afscp-admin",
		},
		Route:               route,
		Resource:            operations.ResourceRef{Type: "volume", ID: "vol_123"},
		CanonicalRequest:    map[string]any{"volume_id": "vol_123"},
		InputSummary:        map[string]any{"volume_id": "vol_123"},
		Phase:               operations.OperationPhaseVolumeEnsureValidate,
		GenerateOperationID: func() string { return "op_volume" },
		Now:                 func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("CreateOrReuseOperationIntake returned error: %v", err)
	}
	if store.calls != 1 {
		t.Fatalf("store calls = %d, want 1", store.calls)
	}
	if store.spec.NamespaceID != "" || store.spec.Scope.NamespaceID != "" {
		t.Fatalf("namespace/spec scope = %q/%q, want empty volume-global namespace", store.spec.NamespaceID, store.spec.Scope.NamespaceID)
	}
	if envelope.OperationID != "op_volume" || envelope.OperationState != OperationStateQueued {
		t.Fatalf("envelope = %#v, want queued op_volume", envelope)
	}
}

func TestCreateOrReuseOperationIntakeReusesFailedOperationWithFlatError(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	store := &fakeOperationIntakeStore{
		reusedRecord: &operations.OperationRecord{
			ID:                  "op_failed",
			Type:                operations.OperationRepoCreate,
			State:               operations.OperationStateFailed,
			Phase:               "allocate_repo_path",
			IdempotencyScope:    "agentsmith-api:ns_123:repo_create:idem_123",
			IdempotencyKey:      "idem_123",
			RequestHash:         operations.RequestHash("sha256:secret"),
			CorrelationID:       "corr_123",
			CallerService:       "agentsmith-api",
			AuthorizedActor:     operations.Actor{Type: "user", ID: "user_123"},
			Resource:            operations.ResourceRef{Type: "repo", ID: "repo_123"},
			NamespaceID:         "ns_123",
			RepoID:              "repo_123",
			ExternalResourceIDs: map[string]string{"jvs_repo_id": "secret-jvs-id"},
			InputSummary:        map[string]any{"safe": "summary"},
			Error: &operations.OperationError{
				Code:          "REPO_TOMBSTONED",
				Message:       "repo is tombstoned token=message-secret",
				Retryable:     false,
				CorrelationID: "corr_123",
				OperationID:   "op_failed",
				Details:       map[string]any{"reason": "tombstoned", "command": "mount --password=detail-secret"},
			},
			CreatedAt: now,
		},
	}
	route, _ := RouteMetadataByOperationID("createRepo")

	envelope, err := CreateOrReuseOperationIntake(context.Background(), OperationIntakeConfig{Store: store}, OperationIntakeRequest{
		RequestContext: auth.RequestContext{
			IdempotencyKey: "idem_123",
			CorrelationID:  "corr_123",
			NamespaceID:    "ns_123",
			Actor:          auth.Actor{Type: "user", ID: "user_123"},
			CallerService:  "agentsmith-api",
		},
		Route:               route,
		NamespaceID:         "ns_123",
		RepoID:              "repo_123",
		Resource:            operations.ResourceRef{Type: "repo", ID: "repo_123"},
		CanonicalRequest:    map[string]any{"repo_id": "repo_123"},
		InputSummary:        map[string]any{"repo_id": "repo_123"},
		Phase:               "allocate_repo_path",
		GenerateOperationID: func() string { return "op_new" },
		Now:                 func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("CreateOrReuseOperationIntake returned error: %v", err)
	}
	if envelope.OperationID != "op_failed" || envelope.OperationState != OperationStateFailed {
		t.Fatalf("envelope id/state = %q/%q, want op_failed/failed", envelope.OperationID, envelope.OperationState)
	}
	if envelope.Error == nil {
		t.Fatal("envelope Error = nil, want failed operation error")
	}
	if envelope.Error.Code != CodeRepoTombstoned || envelope.Error.Retryable {
		t.Fatalf("envelope error = %#v, want repo tombstoned non-retryable", envelope.Error)
	}
	if strings.Contains(envelope.Error.Message, "message-secret") || strings.Contains(fmt.Sprint(envelope.Error.Details), "detail-secret") {
		t.Fatalf("envelope error leaked secret message/details: %#v", envelope.Error)
	}
	if envelope.Error.CorrelationID != "corr_123" {
		t.Fatalf("error correlation = %q, want corr_123", envelope.Error.CorrelationID)
	}
	if envelope.Error.OperationID == nil || *envelope.Error.OperationID != "op_failed" {
		t.Fatalf("error operation id = %#v, want op_failed", envelope.Error.OperationID)
	}
	assertOperationEnvelopeDoesNotLeakInternalFields(t, envelope)
}

func TestRouteOperationTypesCoverMutatingInternalV1Routes(t *testing.T) {
	for _, route := range InternalV1RouteMetadata() {
		got, ok := operations.OperationTypeForRouteOperationID(route.OperationID)
		if route.Mutating && !ok {
			t.Fatalf("missing operation type mapping for mutating route operationId %q", route.OperationID)
		}
		if !route.Mutating && ok {
			t.Fatalf("read route operationId %q unexpectedly maps to operation type %q", route.OperationID, got)
		}
	}
}

func TestCreateOrReuseOperationIntakeMapsErrors(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	route, _ := RouteMetadataByOperationID("createRepo")
	validRequest := func() OperationIntakeRequest {
		return OperationIntakeRequest{
			RequestContext: auth.RequestContext{
				IdempotencyKey: "idem_123",
				CorrelationID:  "corr_123",
				NamespaceID:    "ns_123",
				Actor:          auth.Actor{Type: "user", ID: "user_123"},
				CallerService:  "agentsmith-api",
			},
			Route:               route,
			NamespaceID:         "ns_123",
			RepoID:              "repo_123",
			Resource:            operations.ResourceRef{Type: "repo", ID: "repo_123"},
			CanonicalRequest:    map[string]any{"repo_id": "repo_123"},
			InputSummary:        map[string]any{"repo_id": "repo_123"},
			Phase:               "allocate_repo_path",
			GenerateOperationID: func() string { return "op_123" },
			Now:                 func() time.Time { return now },
		}
	}

	tests := []struct {
		name      string
		store     *fakeOperationIntakeStore
		edit      func(*OperationIntakeRequest)
		wantCode  ErrorCode
		wantHTTP  int
		retryable bool
		wantCalls int
	}{
		{name: "idempotency conflict", store: &fakeOperationIntakeStore{err: operations.ErrIdempotencyConflict}, wantCode: CodeIdempotencyConflict, wantHTTP: http.StatusConflict, wantCalls: 1},
		{name: "store outage", store: &fakeOperationIntakeStore{err: errors.New("postgres dsn password=secret failed")}, wantCode: CodeStorageUnavailable, wantHTTP: http.StatusServiceUnavailable, retryable: true, wantCalls: 1},
		{name: "missing boundary from store", store: &fakeOperationIntakeStore{err: operations.ErrMissingOperationBoundary}, wantCode: CodeInternalError, wantHTTP: http.StatusInternalServerError, wantCalls: 1},
		{name: "nil store", wantCode: CodeInternalError, wantHTTP: http.StatusInternalServerError},
		{name: "unknown route operation id", store: &fakeOperationIntakeStore{}, edit: func(req *OperationIntakeRequest) { req.Route.OperationID = "unknownRoute" }, wantCode: CodeInternalError, wantHTTP: http.StatusInternalServerError},
		{name: "nil generator", store: &fakeOperationIntakeStore{}, edit: func(req *OperationIntakeRequest) { req.GenerateOperationID = nil }, wantCode: CodeInternalError, wantHTTP: http.StatusInternalServerError},
		{name: "empty generated operation id", store: &fakeOperationIntakeStore{}, edit: func(req *OperationIntakeRequest) { req.GenerateOperationID = func() string { return "" } }, wantCode: CodeInternalError, wantHTTP: http.StatusInternalServerError},
		{name: "nil canonical request", store: &fakeOperationIntakeStore{}, edit: func(req *OperationIntakeRequest) { req.CanonicalRequest = nil }, wantCode: CodeInternalError, wantHTTP: http.StatusInternalServerError},
		{name: "typed nil map canonical request", store: &fakeOperationIntakeStore{}, edit: func(req *OperationIntakeRequest) { var value map[string]any; req.CanonicalRequest = value }, wantCode: CodeInternalError, wantHTTP: http.StatusInternalServerError},
		{name: "typed nil slice canonical request", store: &fakeOperationIntakeStore{}, edit: func(req *OperationIntakeRequest) { var value []string; req.CanonicalRequest = value }, wantCode: CodeInternalError, wantHTTP: http.StatusInternalServerError},
		{name: "typed nil pointer canonical request", store: &fakeOperationIntakeStore{}, edit: func(req *OperationIntakeRequest) { var value *struct{ RepoID string }; req.CanonicalRequest = value }, wantCode: CodeInternalError, wantHTTP: http.StatusInternalServerError},
		{name: "namespace-bound request missing request namespace", store: &fakeOperationIntakeStore{}, edit: func(req *OperationIntakeRequest) { req.NamespaceID = "" }, wantCode: CodeInternalError, wantHTTP: http.StatusInternalServerError},
		{name: "namespace-bound context missing namespace", store: &fakeOperationIntakeStore{}, edit: func(req *OperationIntakeRequest) { req.RequestContext.NamespaceID = "" }, wantCode: CodeInternalError, wantHTTP: http.StatusInternalServerError},
		{name: "namespace-bound namespace mismatch", store: &fakeOperationIntakeStore{}, edit: func(req *OperationIntakeRequest) { req.RequestContext.NamespaceID = "ns_other" }, wantCode: CodeInternalError, wantHTTP: http.StatusInternalServerError},
		{name: "namespace-bound guard uses canonical route metadata", store: &fakeOperationIntakeStore{}, edit: func(req *OperationIntakeRequest) {
			req.Route = RouteMetadata{OperationID: "createRepo"}
			req.RequestContext.NamespaceID = "ns_other"
		}, wantCode: CodeInternalError, wantHTTP: http.StatusInternalServerError},
		{name: "hash marshal error", store: &fakeOperationIntakeStore{}, edit: func(req *OperationIntakeRequest) { req.CanonicalRequest = map[string]any{"bad": func() {}} }, wantCode: CodeInternalError, wantHTTP: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validRequest()
			if tt.edit != nil {
				tt.edit(&req)
			}
			_, err := CreateOrReuseOperationIntake(context.Background(), OperationIntakeConfig{Store: tt.store}, req)
			if err == nil {
				t.Fatal("CreateOrReuseOperationIntake succeeded, want error")
			}
			intakeErr := operationIntakeError(t, err)
			if intakeErr.Code != tt.wantCode || intakeErr.Status != tt.wantHTTP || intakeErr.Retryable != tt.retryable {
				t.Fatalf("intake error = %#v, want code/status/retryable %s/%d/%v", intakeErr, tt.wantCode, tt.wantHTTP, tt.retryable)
			}
			if tt.store != nil && tt.store.calls != tt.wantCalls {
				t.Fatalf("store calls = %d, want %d", tt.store.calls, tt.wantCalls)
			}
			if strings.Contains(intakeErr.Error(), "secret") || strings.Contains(intakeErr.Error(), "postgres") {
				t.Fatalf("intake error leaked raw detail: %v", intakeErr)
			}
		})
	}
}

func TestOperationIntakeHelperDoesNotImportResourceMutationDependencies(t *testing.T) {
	source, err := os.ReadFile("operation_intake.go")
	if err != nil {
		t.Fatalf("read operation_intake.go: %v", err)
	}
	for _, forbidden := range []string{
		"internal/resources",
		"internal/jvs",
		"internal/worker",
		"internal/store/postgres",
	} {
		if strings.Contains(string(source), forbidden) {
			t.Fatalf("operation intake helper imported forbidden dependency %q", forbidden)
		}
	}
}

type fakeOperationIntakeStore struct {
	calls               int
	lookupCalls         int
	spec                operations.QueuedOperationSpec
	err                 error
	lookupErr           error
	lookupRecord        *operations.OperationRecord
	reused              bool
	existingOperationID string
	reusedRecord        *operations.OperationRecord
	repoAlreadyExists   bool
	jvsMutation         bool
	jvsMutationErr      error
	jvsMutationCalls    int
}

func (store *fakeOperationIntakeStore) GetOperationByIdempotencyScope(_ context.Context, _ operations.IdempotencyScope) (operations.OperationRecord, error) {
	store.lookupCalls++
	if store.lookupErr != nil {
		return operations.OperationRecord{}, store.lookupErr
	}
	if store.lookupRecord != nil {
		return store.lookupRecord.Sanitized(), nil
	}
	return operations.OperationRecord{}, sql.ErrNoRows
}

func (store *fakeOperationIntakeStore) CreateOrReuseOperation(_ context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	store.calls++
	store.spec = spec
	if store.err != nil {
		return operations.IdempotencyResolution{}, store.err
	}
	record, err := operations.NewQueuedOperationRecord(spec)
	if err != nil {
		return operations.IdempotencyResolution{}, err
	}
	if store.reusedRecord != nil {
		return operations.IdempotencyResolution{Operation: store.reusedRecord.Sanitized(), Existing: true, Reused: true}, nil
	}
	if store.reused {
		record.ID = store.existingOperationID
		return operations.IdempotencyResolution{Operation: record.Sanitized(), Existing: true, Reused: true}, nil
	}
	return operations.IdempotencyResolution{Operation: record.Sanitized()}, nil
}

func (store *fakeOperationIntakeStore) CreateOrReuseRepoCreateOperation(ctx context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	if store.reused || store.reusedRecord != nil {
		return store.CreateOrReuseOperation(ctx, spec)
	}
	if store.repoAlreadyExists {
		store.calls++
		store.spec = spec
		return operations.IdempotencyResolution{}, operations.ErrRepoAlreadyExists
	}
	return store.CreateOrReuseOperation(ctx, spec)
}

func (store *fakeOperationIntakeStore) CreateOrReuseTemplateCreateOperation(ctx context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	return store.CreateOrReuseOperation(ctx, spec)
}

func (store *fakeOperationIntakeStore) CreateOrReuseTemplateCloneOperation(ctx context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	if store.repoAlreadyExists && !store.reused && store.reusedRecord == nil {
		store.calls++
		store.spec = spec
		return operations.IdempotencyResolution{}, operations.ErrRepoAlreadyExists
	}
	return store.CreateOrReuseOperation(ctx, spec)
}

func (store *fakeOperationIntakeStore) RepoHasNonTerminalJVSMutation(context.Context, string) (bool, error) {
	store.jvsMutationCalls++
	if store.jvsMutationErr != nil {
		return false, store.jvsMutationErr
	}
	return store.jvsMutation, nil
}

func operationIntakeError(t *testing.T, err error) *OperationIntakeError {
	t.Helper()
	var intakeErr *OperationIntakeError
	if !errors.As(err, &intakeErr) {
		t.Fatalf("error = %T %v, want OperationIntakeError", err, err)
	}
	return intakeErr
}

func assertOperationEnvelopeDoesNotLeakInternalFields(t *testing.T, envelope OperationEnvelope) {
	t.Helper()
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	for _, forbidden := range []string{"request_hash", "idempotency_scope", "phase", "input_summary", "external_resource_ids", "lease_owner", "lease_expires_at"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("operation envelope leaked %q: %s", forbidden, string(encoded))
		}
	}
}
