package inspection

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

func TestNamespaceVolumeBindingAuthorizerAllowsActiveStoredOperationInspector(t *testing.T) {
	reader := &fakeNamespaceVolumeBindingReader{
		bindings: map[string]resources.NamespaceVolumeBinding{
			"ns_123": namespaceBindingFixture("ns_123", resources.NamespaceStatusActive, resources.AllowedCaller{
				CallerService: "product-caller",
				Roles:         []resources.CallerRole{resources.CallerRoleOperationInspector},
			}),
		},
	}
	authorizer := NamespaceVolumeBindingAuthorizer{Reader: reader}
	ctx := context.WithValue(context.Background(), inspectionTestContextKey("namespace-binding-reader"), "ctx-sentinel")

	if !authorizer.AllowsOperationInspection(ctx, " ns_123 ", productInspectionCaller()) {
		t.Fatal("expected active binding operation_inspector to allow operation inspection")
	}
	if reader.calls != 1 || reader.lastNamespaceID != "ns_123" {
		t.Fatalf("reader calls = %d namespace = %q, want one read of ns_123", reader.calls, reader.lastNamespaceID)
	}
	if reader.lastContext != ctx {
		t.Fatal("namespace binding reader did not receive request context")
	}
}

func TestNamespaceVolumeBindingAuthorizerDeniesWhenStoredBindingLacksOperationInspector(t *testing.T) {
	reader := &fakeNamespaceVolumeBindingReader{
		bindings: map[string]resources.NamespaceVolumeBinding{
			"ns_123": namespaceBindingFixture("ns_123", resources.NamespaceStatusActive, resources.AllowedCaller{
				CallerService: "product-caller",
				Roles:         []resources.CallerRole{resources.CallerRoleRepoAdmin},
			}),
		},
	}
	authorizer := NamespaceVolumeBindingAuthorizer{Reader: reader}

	if authorizer.AllowsOperationInspection(context.Background(), "ns_123", productInspectionCaller()) {
		t.Fatal("expected stored binding without operation_inspector to deny")
	}
}

func TestNamespaceVolumeBindingAuthorizerDeniesCallerServiceMismatch(t *testing.T) {
	reader := &fakeNamespaceVolumeBindingReader{
		bindings: map[string]resources.NamespaceVolumeBinding{
			"ns_123": namespaceBindingFixture("ns_123", resources.NamespaceStatusActive, resources.AllowedCaller{
				CallerService: "other-service",
				Roles:         []resources.CallerRole{resources.CallerRoleOperationInspector},
			}),
		},
	}
	authorizer := NamespaceVolumeBindingAuthorizer{Reader: reader}

	if authorizer.AllowsOperationInspection(context.Background(), "ns_123", productInspectionCaller()) {
		t.Fatal("expected caller service mismatch to deny")
	}
}

func TestNamespaceVolumeBindingAuthorizerDeniesDisabledBinding(t *testing.T) {
	reader := &fakeNamespaceVolumeBindingReader{
		bindings: map[string]resources.NamespaceVolumeBinding{
			"ns_123": namespaceBindingFixture("ns_123", resources.NamespaceStatusDisabled, resources.AllowedCaller{
				CallerService: "product-caller",
				Roles:         []resources.CallerRole{resources.CallerRoleOperationInspector},
			}),
		},
	}
	authorizer := NamespaceVolumeBindingAuthorizer{Reader: reader}

	if authorizer.AllowsOperationInspection(context.Background(), "ns_123", productInspectionCaller()) {
		t.Fatal("expected disabled binding to deny")
	}
}

func TestNamespaceVolumeBindingAuthorizerDeniesReturnedBindingNamespaceMismatch(t *testing.T) {
	reader := &fakeNamespaceVolumeBindingReader{
		bindings: map[string]resources.NamespaceVolumeBinding{
			"ns_123": namespaceBindingFixture("ns_456", resources.NamespaceStatusActive, resources.AllowedCaller{
				CallerService: "product-caller",
				Roles:         []resources.CallerRole{resources.CallerRoleOperationInspector},
			}),
		},
	}
	authorizer := NamespaceVolumeBindingAuthorizer{Reader: reader}

	if authorizer.AllowsOperationInspection(context.Background(), "ns_123", productInspectionCaller()) {
		t.Fatal("expected returned binding namespace mismatch to deny")
	}
}

func TestNamespaceVolumeBindingAuthorizerDeniesInvalidNamespaceWithoutStoreRead(t *testing.T) {
	reader := &fakeNamespaceVolumeBindingReader{}
	authorizer := NamespaceVolumeBindingAuthorizer{Reader: reader}

	for _, namespaceID := range []string{"", " ", "namespace-123", "ns_x"} {
		if authorizer.AllowsOperationInspection(context.Background(), namespaceID, productInspectionCaller()) {
			t.Fatalf("expected invalid namespace %q to deny", namespaceID)
		}
	}
	if reader.calls != 0 {
		t.Fatalf("reader calls = %d, want no reads for invalid namespace IDs", reader.calls)
	}
}

func TestNamespaceVolumeBindingAuthorizerDeniesNilStore(t *testing.T) {
	authorizer := NamespaceVolumeBindingAuthorizer{}

	if authorizer.AllowsOperationInspection(context.Background(), "ns_123", productInspectionCaller()) {
		t.Fatal("expected nil store to deny")
	}
}

func TestNamespaceVolumeBindingAuthorizerDeniesStoreErrorAndNotFound(t *testing.T) {
	for _, err := range []error{errors.New("store unavailable"), sql.ErrNoRows} {
		reader := &fakeNamespaceVolumeBindingReader{err: err}
		authorizer := NamespaceVolumeBindingAuthorizer{Reader: reader}

		if authorizer.AllowsOperationInspection(context.Background(), "ns_123", productInspectionCaller()) {
			t.Fatalf("expected store error %v to deny", err)
		}
	}
}

func TestNamespaceVolumeBindingAuthorizerDeniesDedicatedOrchestratorCaller(t *testing.T) {
	reader := &fakeNamespaceVolumeBindingReader{
		bindings: map[string]resources.NamespaceVolumeBinding{
			"ns_123": namespaceBindingFixture("ns_123", resources.NamespaceStatusActive, resources.AllowedCaller{
				CallerService: "runtime-orchestrator",
				Roles:         []resources.CallerRole{resources.CallerRoleOrchestratorMount},
			}),
		},
	}
	authorizer := NamespaceVolumeBindingAuthorizer{Reader: reader}
	caller := auth.AllowedCaller{
		CallerService: "runtime-orchestrator",
		Kind:          auth.CallerKindOrchestrator,
		Roles:         []auth.Role{auth.RoleOrchestratorMount},
	}

	if authorizer.AllowsOperationInspection(context.Background(), "ns_123", caller) {
		t.Fatal("expected dedicated orchestrator caller to deny operation inspection")
	}
}

func TestNamespaceVolumeBindingAuthorizerDeniesDedicatedOrchestratorBindingForProductInspection(t *testing.T) {
	reader := &fakeNamespaceVolumeBindingReader{
		bindings: map[string]resources.NamespaceVolumeBinding{
			"ns_123": namespaceBindingFixture("ns_123", resources.NamespaceStatusActive, resources.AllowedCaller{
				CallerService: "product-caller",
				Roles:         []resources.CallerRole{resources.CallerRoleOrchestratorMount},
			}),
		},
	}
	authorizer := NamespaceVolumeBindingAuthorizer{Reader: reader}

	if authorizer.AllowsOperationInspection(context.Background(), "ns_123", productInspectionCaller()) {
		t.Fatal("expected orchestrator-only stored binding to deny product operation inspection")
	}
}

func TestNamespaceVolumeBindingAuthorizerDeniesUnsupportedRole(t *testing.T) {
	reader := &fakeNamespaceVolumeBindingReader{
		bindings: map[string]resources.NamespaceVolumeBinding{
			"ns_123": namespaceBindingFixture("ns_123", resources.NamespaceStatusActive, resources.AllowedCaller{
				CallerService: "product-caller",
				Roles:         []resources.CallerRole{resources.CallerRole("unknown_role")},
			}),
		},
	}
	authorizer := NamespaceVolumeBindingAuthorizer{Reader: reader}

	if authorizer.AllowsOperationInspection(context.Background(), "ns_123", productInspectionCaller()) {
		t.Fatal("expected unsupported stored role to deny")
	}
}

func TestNamespaceVolumeBindingAuthorizerWithErrorRejectsInvalidStoredBindingInvariant(t *testing.T) {
	tests := []struct {
		name    string
		binding resources.NamespaceVolumeBinding
	}{
		{
			name:    "invalid binding policy",
			binding: resources.NamespaceVolumeBinding{NamespaceID: "ns_123", DefaultVolumeID: "vol_123", Status: resources.NamespaceStatusActive},
		},
		{
			name: "stored caller cannot map",
			binding: namespaceBindingFixture("ns_123", resources.NamespaceStatusActive, resources.AllowedCaller{
				CallerService: "product-caller",
				Roles:         []resources.CallerRole{resources.CallerRoleVolumeAdmin},
			}),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &fakeNamespaceVolumeBindingReader{
				bindings: map[string]resources.NamespaceVolumeBinding{
					"ns_123": tt.binding,
				},
			}
			authorizer := NamespaceVolumeBindingAuthorizer{Reader: reader}

			allowed, err := authorizer.AllowsOperationInspectionWithError(context.Background(), "ns_123", productInspectionCaller())
			if allowed {
				t.Fatal("allowed = true, want false")
			}
			if !errors.Is(err, ErrInvalidStoredNamespaceAuthorizationState) {
				t.Fatalf("error = %v, want ErrInvalidStoredNamespaceAuthorizationState", err)
			}
			if authorizer.AllowsOperationInspection(context.Background(), "ns_123", productInspectionCaller()) {
				t.Fatal("old bool API allowed invalid stored binding")
			}
		})
	}
}

func TestNamespaceVolumeBindingAuthorizerWiresIntoInspectOperationAndKeepsRedaction(t *testing.T) {
	operationReader := &fakeOperationReader{
		records: map[string]operations.OperationRecord{
			"op_123": namespacedRecord("op_123", "ns_123"),
		},
	}
	bindingReader := &fakeNamespaceVolumeBindingReader{
		bindings: map[string]resources.NamespaceVolumeBinding{
			"ns_123": namespaceBindingFixture("ns_123", resources.NamespaceStatusActive, resources.AllowedCaller{
				CallerService: "product-caller",
				Roles:         []resources.CallerRole{resources.CallerRoleOperationInspector},
			}),
		},
	}

	record, err := InspectOperation(context.Background(), operationReader, NamespaceVolumeBindingAuthorizer{Reader: bindingReader}, Request{
		OperationID:  "op_123",
		RouteClass:   auth.RouteClassOperationInspection,
		RequiredRole: auth.RoleOperationInspector,
		Caller:       productInspectionCaller(),
	})
	if err != nil {
		t.Fatalf("InspectOperation returned error: %v", err)
	}
	rendered := strings.ToLower(toInspectionTestString(record))
	for _, forbidden := range []string{"plain-webdav-password", "metadata-secret", "jvs-secret"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("secret material %q leaked in %s", forbidden, rendered)
		}
	}
}

type fakeNamespaceVolumeBindingReader struct {
	bindings        map[string]resources.NamespaceVolumeBinding
	err             error
	calls           int
	lastContext     context.Context
	lastNamespaceID string
}

func (reader *fakeNamespaceVolumeBindingReader) GetNamespaceVolumeBinding(ctx context.Context, namespaceID string) (resources.NamespaceVolumeBinding, error) {
	reader.calls++
	reader.lastContext = ctx
	reader.lastNamespaceID = namespaceID
	if reader.err != nil {
		return resources.NamespaceVolumeBinding{}, reader.err
	}
	binding, ok := reader.bindings[namespaceID]
	if !ok {
		return resources.NamespaceVolumeBinding{}, sql.ErrNoRows
	}
	return binding, nil
}

func namespaceBindingFixture(namespaceID string, status resources.NamespaceStatus, callers ...resources.AllowedCaller) resources.NamespaceVolumeBinding {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	return resources.NamespaceVolumeBinding{
		NamespaceID:       namespaceID,
		DefaultVolumeID:   "vol_shared01",
		AllowedCallers:    callers,
		QuotaBytesDefault: 0,
		ExportPolicy:      map[string]any{"webdav_enabled": true, "max_session_seconds": float64(3600)},
		LifecyclePolicy:   map[string]any{"tombstone_retention_seconds": float64(604800), "purge_requires_lifecycle_admin": true, "break_glass_purge_enabled": false},
		MountPolicy:       map[string]any{"workload_mount_enabled": true, "workload_mount_requires_external_control_root": true, "allow_privileged_workload": false},
		TemplatePolicy:    map[string]any{"namespace_templates_enabled": true, "cross_namespace_clone_enabled": false},
		Status:            status,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}
