package api

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/fences"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

type fakeRestoreHTTPStore struct {
	repo               resources.Repo
	namespace          resources.Namespace
	binding            resources.NamespaceVolumeBinding
	fences             []fences.Fence
	existing           operations.OperationRecord
	spec               operations.QueuedOperationSpec
	repoErr            error
	namespaceErr       error
	bindingErr         error
	fenceErr           error
	jvsMutation        bool
	jvsMutationErr     error
	lookupErr          error
	createErr          error
	createCalls        int
	genericCreateCalls int
	restoreIntakeCalls int
	previewIntakeCalls int
	runIntakeCalls     int
	jvsMutationCalls   int
}

func newFakeRestoreHTTPStore(time.Time) *fakeRestoreHTTPStore {
	return &fakeRestoreHTTPStore{
		repo:      repoResourceFixture("ns_alpha01", "repo_alpha01", resources.RepoStatusActive),
		namespace: activeNamespaceFixture("ns_alpha01"),
		binding: namespacePolicyBindingFixture("ns_alpha01", resources.AllowedCaller{
			CallerService: "product-caller",
			Roles:         []resources.CallerRole{resources.CallerRoleRepoAdmin, resources.CallerRoleRestoreAdmin},
		}),
	}
}

func (store *fakeRestoreHTTPStore) GetRepoInNamespace(_ context.Context, namespaceID, repoID string) (resources.Repo, error) {
	if store.repoErr != nil {
		return resources.Repo{}, store.repoErr
	}
	if store.repo.ID == repoID && store.repo.NamespaceID == namespaceID {
		return store.repo, nil
	}
	return resources.Repo{}, sql.ErrNoRows
}

func (store *fakeRestoreHTTPStore) GetRepo(_ context.Context, repoID string) (resources.Repo, error) {
	if store.repoErr != nil {
		return resources.Repo{}, store.repoErr
	}
	if store.repo.ID == repoID {
		return store.repo, nil
	}
	return resources.Repo{}, sql.ErrNoRows
}

func (store *fakeRestoreHTTPStore) ListReposByNamespace(_ context.Context, namespaceID string) ([]resources.Repo, error) {
	if store.repoErr != nil {
		return nil, store.repoErr
	}
	if store.repo.NamespaceID == namespaceID {
		return []resources.Repo{store.repo}, nil
	}
	return nil, nil
}

func (store *fakeRestoreHTTPStore) GetNamespace(context.Context, string) (resources.Namespace, error) {
	if store.namespaceErr != nil {
		return resources.Namespace{}, store.namespaceErr
	}
	return store.namespace, nil
}

func (store *fakeRestoreHTTPStore) GetNamespaceVolumeBinding(context.Context, string) (resources.NamespaceVolumeBinding, error) {
	if store.bindingErr != nil {
		return resources.NamespaceVolumeBinding{}, store.bindingErr
	}
	return store.binding, nil
}

func (store *fakeRestoreHTTPStore) ListHeldRepoFences(context.Context, string) ([]fences.Fence, error) {
	if store.fenceErr != nil {
		return nil, store.fenceErr
	}
	return append([]fences.Fence(nil), store.fences...), nil
}

func (store *fakeRestoreHTTPStore) RepoHasNonTerminalJVSMutation(context.Context, string) (bool, error) {
	store.jvsMutationCalls++
	if store.jvsMutationErr != nil {
		return false, store.jvsMutationErr
	}
	return store.jvsMutation, nil
}

func (store *fakeRestoreHTTPStore) GetOperationByIdempotencyScope(context.Context, operations.IdempotencyScope) (operations.OperationRecord, error) {
	if store.lookupErr != nil {
		return operations.OperationRecord{}, store.lookupErr
	}
	if store.existing.ID == "" {
		return operations.OperationRecord{}, sql.ErrNoRows
	}
	return store.existing, nil
}

func (store *fakeRestoreHTTPStore) CreateOrReuseOperation(_ context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	store.genericCreateCalls++
	return store.createOrReuseOperation(spec)
}

func (store *fakeRestoreHTTPStore) CreateOrReuseRestoreOperation(_ context.Context, spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	store.restoreIntakeCalls++
	return store.createOrReuseOperation(spec)
}

func (store *fakeRestoreHTTPStore) createOrReuseOperation(spec operations.QueuedOperationSpec) (operations.IdempotencyResolution, error) {
	store.createCalls++
	store.spec = spec
	if store.createErr != nil {
		return operations.IdempotencyResolution{}, store.createErr
	}
	record, err := operations.NewQueuedOperationRecord(spec)
	if err != nil {
		return operations.IdempotencyResolution{}, err
	}
	return operations.IdempotencyResolution{Operation: record.Sanitized()}, nil
}

func assertRestoreHTTPNoRawCommand(t *testing.T, value any) {
	t.Helper()
	rendered := strings.ToLower(fmt.Sprint(value))
	for _, forbidden := range []string{"run_command", "recommended_next_command", "restore_command", "command"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("restore HTTP intake leaked raw command marker %q in %#v", forbidden, value)
		}
	}
}
