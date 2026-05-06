package operations

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestIdempotencyScopeAndRequestHashAreStable(t *testing.T) {
	scope := NewIdempotencyScope("afscp-api", "namespace-1", OperationRepoCreate, "client-key-1")

	if got, want := scope.String(), "afscp-api:namespace-1:repo_create:client-key-1"; got != want {
		t.Fatalf("scope string = %q, want %q", got, want)
	}

	first, err := HashRequest(map[string]any{
		"repo_id": "repo-1",
		"labels":  []string{"blue", "prod"},
	})
	if err != nil {
		t.Fatalf("hash first request: %v", err)
	}

	second, err := HashRequest(map[string]any{
		"labels":  []string{"blue", "prod"},
		"repo_id": "repo-1",
	})
	if err != nil {
		t.Fatalf("hash second request: %v", err)
	}

	if first != second {
		t.Fatalf("same JSON request hashed differently:\nfirst:  %s\nsecond: %s", first, second)
	}
	if first == "" {
		t.Fatalf("unexpected empty request hash")
	}
}

func TestValidateSavePointIDMatchesSafeOpaqueSchema(t *testing.T) {
	for _, id := range []string{"sp_001", "1.a:b-c_2", strings.Repeat("a", 128)} {
		if err := ValidateSavePointID(id); err != nil {
			t.Fatalf("ValidateSavePointID(%q): %v", id, err)
		}
	}
	for _, id := range []string{"", "_sp", " sp_001", "sp/001", strings.Repeat("a", 129)} {
		if err := ValidateSavePointID(id); err == nil {
			t.Fatalf("ValidateSavePointID(%q) succeeded, want error", id)
		}
	}
}

func TestCompareIdempotencyReturnsExistingOperationForSameScopeKeyAndHash(t *testing.T) {
	scope := NewIdempotencyScope("afscp-api", "namespace-1", OperationRepoCreate, "client-key-1")
	requestHash := RequestHash("sha256:same")
	existing := OperationRecord{
		ID:               "op-1",
		Type:             OperationRepoCreate,
		State:            OperationStateSucceeded,
		IdempotencyScope: scope.String(),
		IdempotencyKey:   "client-key-1",
		RequestHash:      requestHash,
	}

	resolution, err := CompareIdempotency([]OperationRecord{existing}, scope, requestHash)
	if err != nil {
		t.Fatalf("compare idempotency: %v", err)
	}
	if !resolution.Existing || !resolution.Reused {
		t.Fatalf("expected existing operation to be reused")
	}
	if resolution.Operation.ID != existing.ID {
		t.Fatalf("operation ID = %q, want %q", resolution.Operation.ID, existing.ID)
	}
}

func TestCompareIdempotencyConflictsForSameScopeKeyAndDifferentHash(t *testing.T) {
	scope := NewIdempotencyScope("afscp-api", "namespace-1", OperationRepoCreate, "client-key-1")
	existing := OperationRecord{
		ID:               "op-1",
		Type:             OperationRepoCreate,
		State:            OperationStateQueued,
		IdempotencyScope: scope.String(),
		IdempotencyKey:   "client-key-1",
		RequestHash:      RequestHash("sha256:original"),
	}

	_, err := CompareIdempotency([]OperationRecord{existing}, scope, RequestHash("sha256:changed"))
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("err = %v, want ErrIdempotencyConflict", err)
	}
}

func TestCompareIdempotencyReturnsNoExistingWhenScopeIsAbsent(t *testing.T) {
	scope := NewIdempotencyScope("afscp-api", "namespace-1", OperationRepoCreate, "client-key-1")
	resolution, err := CompareIdempotency(nil, scope, RequestHash("sha256:new"))
	if err != nil {
		t.Fatalf("compare idempotency: %v", err)
	}
	if resolution.Existing || resolution.Reused {
		t.Fatalf("expected missing scope to be explicit no-existing result: %#v", resolution)
	}
	if resolution.Operation.ID != "" {
		t.Fatalf("no-existing resolution should not synthesize operations, got %q", resolution.Operation.ID)
	}
	if resolution.Operation.State != "" {
		t.Fatalf("no-existing resolution should not synthesize queued state, got %q", resolution.Operation.State)
	}
}

func TestNewQueuedOperationRecordAllowsEmptyNamespaceForGlobalScope(t *testing.T) {
	scope := NewIdempotencyScope("afscp-worker", "", OperationMountBindingStatusUpdate, "volume-shared")
	createdAt := time.Date(2026, 5, 4, 12, 35, 0, 0, time.UTC)

	record, err := NewQueuedOperationRecord(QueuedOperationSpec{
		OperationID:     "op_global",
		Scope:           scope,
		RequestHash:     RequestHash("sha256:global"),
		Phase:           "queued",
		CorrelationID:   "corr-global",
		CallerService:   "afscp-worker",
		AuthorizedActor: Actor{Type: "operator", ID: "operator_123"},
		Resource:        ResourceRef{Type: "volume", ID: "shared"},
		CreatedAt:       createdAt,
	})
	if err != nil {
		t.Fatalf("new global queued operation record: %v", err)
	}

	if got, want := scope.String(), "afscp-worker::mount_binding_status_update:volume-shared"; got != want {
		t.Fatalf("scope string = %q, want explicit empty namespace component %q", got, want)
	}
	if record.NamespaceID != "" {
		t.Fatalf("namespace_id = %q, want empty for global operation", record.NamespaceID)
	}
	if record.IdempotencyScope != scope.String() {
		t.Fatalf("idempotency_scope = %q, want %q", record.IdempotencyScope, scope.String())
	}
	if got := scope.ConstraintKey(); got.NamespaceID != "" {
		t.Fatalf("constraint namespace = %q, want empty namespace component", got.NamespaceID)
	}
}

func TestIdempotencyConstraintKeyDocumentsAtomicStoreBoundary(t *testing.T) {
	scope := NewIdempotencyScope("afscp-api", "ns_alpha", OperationRepoCreate, "client-key-1")

	got := scope.ConstraintKey()
	want := IdempotencyConstraintKey{
		CallerService:  "afscp-api",
		NamespaceID:    "ns_alpha",
		OperationType:  OperationRepoCreate,
		IdempotencyKey: "client-key-1",
	}
	if got != want {
		t.Fatalf("constraint key = %#v, want %#v", got, want)
	}

	columns := got.Columns()
	wantColumns := []string{"caller_service", "namespace_id", "operation_type", "idempotency_key"}
	if len(columns) != len(wantColumns) {
		t.Fatalf("columns = %#v, want %#v", columns, wantColumns)
	}
	for i := range columns {
		if columns[i] != wantColumns[i] {
			t.Fatalf("columns = %#v, want %#v", columns, wantColumns)
		}
	}
}

func TestNewQueuedOperationRecordRequiresDurableBoundaryInputs(t *testing.T) {
	scope := NewIdempotencyScope("afscp-api", "ns_alpha", OperationRepoCreate, "client-key-1")
	_, err := NewQueuedOperationRecord(QueuedOperationSpec{
		Scope:       scope,
		RequestHash: RequestHash("sha256:new"),
		Phase:       "allocate_repo_path",
		CreatedAt:   time.Date(2026, 5, 4, 12, 30, 0, 0, time.UTC),
	})
	if !errors.Is(err, ErrMissingOperationBoundary) {
		t.Fatalf("err = %v, want ErrMissingOperationBoundary", err)
	}
}

func TestNewQueuedOperationRecordUsesCallerSuppliedDurableBoundaryInputs(t *testing.T) {
	scope := NewIdempotencyScope("afscp-api", "ns_alpha", OperationRepoCreate, "client-key-1")
	createdAt := time.Date(2026, 5, 4, 12, 30, 0, 0, time.UTC)

	record, err := NewQueuedOperationRecord(QueuedOperationSpec{
		OperationID:         "op_alpha",
		Scope:               scope,
		RequestHash:         RequestHash("sha256:new"),
		Phase:               "allocate_repo_path",
		CorrelationID:       "corr-1",
		CallerService:       "afscp-api",
		AuthorizedActor:     Actor{Type: "system", ID: "svc-1"},
		Resource:            ResourceRef{Type: "repo", ID: "repo_project"},
		NamespaceID:         "ns_alpha",
		RepoID:              "repo_project",
		ExternalResourceIDs: map[string]string{"jvs_repo_id": "jvs-secret"},
		InputSummary:        map[string]any{"repo_id": "repo_project"},
		CreatedAt:           createdAt,
	})
	if err != nil {
		t.Fatalf("new queued operation record: %v", err)
	}
	if record.ID != "op_alpha" || record.State != OperationStateQueued || record.Phase != "allocate_repo_path" {
		t.Fatalf("unexpected queued record boundary fields: %#v", record)
	}
	if record.IdempotencyScope != scope.String() || record.RequestHash != RequestHash("sha256:new") {
		t.Fatalf("idempotency fields not copied: %#v", record)
	}
	if record.CallerService != "afscp-api" || record.Resource.ID != "repo_project" {
		t.Fatalf("caller/resource boundary not copied: %#v", record)
	}
	if !record.CreatedAt.Equal(createdAt) {
		t.Fatalf("created_at = %s, want %s", record.CreatedAt, createdAt)
	}
}
