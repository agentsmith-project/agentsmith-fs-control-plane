package inspection

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
)

func TestInspectOperationAllowsProductInspectionRoleAndRedactsRecord(t *testing.T) {
	reader := &fakeOperationReader{
		records: map[string]operations.OperationRecord{
			"op_123": namespacedRecord("op_123", "ns_123"),
		},
	}
	authorizer := storedNamespaceAuthorizer("ns_123", productInspectionCaller())

	record, err := InspectOperation(reader, authorizer, Request{
		OperationID:  "op_123",
		RouteClass:   auth.RouteClassOperationInspection,
		NamespaceID:  "ns_123",
		RequiredRole: auth.RoleOperationInspector,
		Caller:       productInspectionCaller(),
	})
	if err != nil {
		t.Fatalf("InspectOperation returned error: %v", err)
	}

	if reader.reads != 1 {
		t.Fatalf("reader reads = %d, want 1", reader.reads)
	}
	if reader.lastOperationID != "op_123" {
		t.Fatalf("reader operation id = %q, want op_123", reader.lastOperationID)
	}
	if record.ID != "op_123" {
		t.Fatalf("record ID = %q, want op_123", record.ID)
	}
	if authorizer.lastNamespaceID != "ns_123" {
		t.Fatalf("stored namespace authorization used %q, want ns_123", authorizer.lastNamespaceID)
	}
	rendered := strings.ToLower(toInspectionTestString(record))
	for _, forbidden := range []string{"plain-webdav-password", "metadata-secret", "jvs-secret"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("secret material %q leaked in %s", forbidden, rendered)
		}
	}
	if !record.Redaction.Redacted {
		t.Fatal("expected redaction report on returned record")
	}
}

func TestInspectOperationDeniesNamespaceRecordForEmptyCallerWithMatchingNamespace(t *testing.T) {
	reader := &fakeOperationReader{
		records: map[string]operations.OperationRecord{
			"op_123": namespacedRecord("op_123", "ns_123"),
		},
	}

	_, err := InspectOperation(reader, storedNamespaceAuthorizer("ns_123", productInspectionCaller()), Request{
		OperationID:  "op_123",
		RouteClass:   auth.RouteClassOperationInspection,
		NamespaceID:  "ns_123",
		RequiredRole: auth.RoleOperationInspector,
	})
	if !errors.Is(err, ErrInspectionDenied) {
		t.Fatalf("expected ErrInspectionDenied, got %v", err)
	}
}

func TestInspectOperationDeniesProductCallerWithoutRequiredInspectionRole(t *testing.T) {
	reader := &fakeOperationReader{
		records: map[string]operations.OperationRecord{
			"op_123": namespacedRecord("op_123", "ns_123"),
		},
	}

	_, err := InspectOperation(reader, storedNamespaceAuthorizer("ns_123", productCaller()), Request{
		OperationID:  "op_123",
		RouteClass:   auth.RouteClassOperationInspection,
		NamespaceID:  "ns_123",
		RequiredRole: auth.RoleOperationInspector,
		Caller:       productCaller(),
	})
	if !errors.Is(err, ErrInspectionDenied) {
		t.Fatalf("expected ErrInspectionDenied, got %v", err)
	}
}

func TestInspectOperationRequiresStoredNamespaceToMatchRequestNamespaceForProductCaller(t *testing.T) {
	reader := &fakeOperationReader{
		records: map[string]operations.OperationRecord{
			"op_123": namespacedRecord("op_123", "ns_123"),
		},
	}

	_, err := InspectOperation(reader, storedNamespaceAuthorizer("ns_123", productInspectionCaller()), Request{
		OperationID:  "op_123",
		RouteClass:   auth.RouteClassOperationInspection,
		NamespaceID:  "ns_456",
		RequiredRole: auth.RoleOperationInspector,
		Caller:       productInspectionCaller(),
	})
	if !errors.Is(err, ErrInspectionDenied) {
		t.Fatalf("expected ErrInspectionDenied, got %v", err)
	}
}

func TestInspectOperationAllowsNamespaceRecordWhenProductCallerHasNoRequestNamespaceButStoredNamespaceAllows(t *testing.T) {
	reader := &fakeOperationReader{
		records: map[string]operations.OperationRecord{
			"op_123": namespacedRecord("op_123", "ns_123"),
		},
	}
	authorizer := storedNamespaceAuthorizer("ns_123", productInspectionCaller())

	record, err := InspectOperation(reader, authorizer, Request{
		OperationID:  "op_123",
		RouteClass:   auth.RouteClassOperationInspection,
		RequiredRole: auth.RoleOperationInspector,
		Caller:       productInspectionCaller(),
	})
	if err != nil {
		t.Fatalf("InspectOperation returned error: %v", err)
	}
	if record.ID != "op_123" {
		t.Fatalf("record ID = %q, want op_123", record.ID)
	}
	if authorizer.lastNamespaceID != "ns_123" {
		t.Fatalf("stored namespace authorization used %q, want ns_123", authorizer.lastNamespaceID)
	}
}

func TestInspectOperationDeniesProductCallerWhenStoredNamespaceAuthorizationLacksInspector(t *testing.T) {
	reader := &fakeOperationReader{
		records: map[string]operations.OperationRecord{
			"op_123": namespacedRecord("op_123", "ns_123"),
		},
	}

	_, err := InspectOperation(reader, storedNamespaceAuthorizer("ns_123", productCaller()), Request{
		OperationID:  "op_123",
		RouteClass:   auth.RouteClassOperationInspection,
		RequiredRole: auth.RoleOperationInspector,
		Caller:       productInspectionCaller(),
	})
	if !errors.Is(err, ErrInspectionDenied) {
		t.Fatalf("expected ErrInspectionDenied, got %v", err)
	}
}

func TestInspectOperationDeniesProductCallerAuthorizedOnlyForDifferentStoredNamespace(t *testing.T) {
	reader := &fakeOperationReader{
		records: map[string]operations.OperationRecord{
			"op_456": namespacedRecord("op_456", "ns_456"),
		},
	}
	authorizer := storedNamespaceAuthorizer("ns_123", productInspectionCaller())

	_, err := InspectOperation(reader, authorizer, Request{
		OperationID:  "op_456",
		RouteClass:   auth.RouteClassOperationInspection,
		RequiredRole: auth.RoleOperationInspector,
		Caller:       productInspectionCaller(),
	})
	if !errors.Is(err, ErrInspectionDenied) {
		t.Fatalf("expected ErrInspectionDenied, got %v", err)
	}
	if authorizer.lastNamespaceID != "ns_456" {
		t.Fatalf("stored namespace authorization used %q, want ns_456", authorizer.lastNamespaceID)
	}
}

func TestInspectOperationDeniesProductCallerWithoutStoredNamespaceAuthorizationBoundary(t *testing.T) {
	reader := &fakeOperationReader{
		records: map[string]operations.OperationRecord{
			"op_123": namespacedRecord("op_123", "ns_123"),
		},
	}

	_, err := InspectOperation(reader, nil, Request{
		OperationID:  "op_123",
		RouteClass:   auth.RouteClassOperationInspection,
		RequiredRole: auth.RoleOperationInspector,
		Caller:       productInspectionCaller(),
	})
	if !errors.Is(err, ErrInspectionDenied) {
		t.Fatalf("expected ErrInspectionDenied, got %v", err)
	}
}

func TestInspectOperationDeniesNonProductCallerWithoutOperatorAdminEvenWhenStoredNamespaceAllowsInspector(t *testing.T) {
	reader := &fakeOperationReader{
		records: map[string]operations.OperationRecord{
			"op_123": namespacedRecord("op_123", "ns_123"),
		},
	}
	operatorInspector := auth.AllowedCaller{
		CallerService: "afscp-operator",
		Kind:          auth.CallerKindOperator,
		Roles:         []auth.Role{auth.RoleOperationInspector},
	}

	_, err := InspectOperation(reader, storedNamespaceAuthorizer("ns_123", operatorInspector), Request{
		OperationID:  "op_123",
		RouteClass:   auth.RouteClassOperationInspection,
		RequiredRole: auth.RoleOperationInspector,
		Caller:       operatorInspector,
	})
	if !errors.Is(err, ErrInspectionDenied) {
		t.Fatalf("expected ErrInspectionDenied, got %v", err)
	}
}

func TestInspectOperationDeniesNamespaceRecordFromVolumeGlobalProductRoute(t *testing.T) {
	reader := &fakeOperationReader{
		records: map[string]operations.OperationRecord{
			"op_123": namespacedRecord("op_123", "ns_123"),
		},
	}

	_, err := InspectOperation(reader, storedNamespaceAuthorizer("ns_123", productInspectionCaller()), Request{
		OperationID:  "op_123",
		RouteClass:   auth.RouteClassVolumeGlobal,
		NamespaceID:  "ns_123",
		RequiredRole: auth.RoleOperationInspector,
		Caller:       productInspectionCaller(),
	})
	if !errors.Is(err, ErrInspectionDenied) {
		t.Fatalf("expected ErrInspectionDenied, got %v", err)
	}
}

func TestInspectOperationAllowsAdminAndOperatorWithOperatorAdminForNamespacedAndGlobalRecords(t *testing.T) {
	tests := []struct {
		name             string
		record           operations.OperationRecord
		requestNamespace string
		caller           auth.AllowedCaller
	}{
		{
			name:             "operator namespaced",
			record:           namespacedRecord("op_namespaced", "ns_123"),
			requestNamespace: "ns_456",
			caller: auth.AllowedCaller{
				CallerService: "afscp-operator",
				Kind:          auth.CallerKindOperator,
				Roles:         []auth.Role{auth.RoleOperatorAdmin},
			},
		},
		{
			name:   "operator global",
			record: globalRecord("op_global"),
			caller: auth.AllowedCaller{
				CallerService: "afscp-operator",
				Kind:          auth.CallerKindOperator,
				Roles:         []auth.Role{auth.RoleOperatorAdmin},
			},
		},
		{
			name:             "admin namespaced",
			record:           namespacedRecord("op_namespaced", "ns_123"),
			requestNamespace: "ns_456",
			caller: auth.AllowedCaller{
				CallerService: "afscp-admin",
				Kind:          auth.CallerKindAdmin,
				Roles:         []auth.Role{auth.RoleOperatorAdmin},
			},
		},
		{
			name:   "admin global",
			record: globalRecord("op_global"),
			caller: auth.AllowedCaller{
				CallerService: "afscp-admin",
				Kind:          auth.CallerKindAdmin,
				Roles:         []auth.Role{auth.RoleOperatorAdmin},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &fakeOperationReader{
				records: map[string]operations.OperationRecord{
					tt.record.ID: tt.record,
				},
			}

			got, err := InspectOperation(reader, nil, Request{
				OperationID: tt.record.ID,
				RouteClass:  auth.RouteClassOperationInspection,
				NamespaceID: tt.requestNamespace,
				Caller:      tt.caller,
			})
			if err != nil {
				t.Fatalf("inspection returned error: %v", err)
			}
			if rendered := strings.ToLower(toInspectionTestString(got)); strings.Contains(rendered, "plain-webdav-password") || strings.Contains(rendered, "metadata-secret") {
				t.Fatalf("privileged inspection leaked secret material: %s", rendered)
			}
		})
	}
}

func TestInspectOperationDeniesAdminAndOperatorFromNonInspectionRouteClass(t *testing.T) {
	tests := []struct {
		name   string
		record operations.OperationRecord
		caller auth.AllowedCaller
	}{
		{
			name:   "operator namespaced",
			record: namespacedRecord("op_namespaced", "ns_123"),
			caller: auth.AllowedCaller{
				CallerService: "afscp-operator",
				Kind:          auth.CallerKindOperator,
				Roles:         []auth.Role{auth.RoleOperatorAdmin},
			},
		},
		{
			name:   "operator global",
			record: globalRecord("op_global"),
			caller: auth.AllowedCaller{
				CallerService: "afscp-operator",
				Kind:          auth.CallerKindOperator,
				Roles:         []auth.Role{auth.RoleOperatorAdmin},
			},
		},
		{
			name:   "admin namespaced",
			record: namespacedRecord("op_namespaced", "ns_123"),
			caller: auth.AllowedCaller{
				CallerService: "afscp-admin",
				Kind:          auth.CallerKindAdmin,
				Roles:         []auth.Role{auth.RoleOperatorAdmin},
			},
		},
		{
			name:   "admin global",
			record: globalRecord("op_global"),
			caller: auth.AllowedCaller{
				CallerService: "afscp-admin",
				Kind:          auth.CallerKindAdmin,
				Roles:         []auth.Role{auth.RoleOperatorAdmin},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &fakeOperationReader{
				records: map[string]operations.OperationRecord{
					tt.record.ID: tt.record,
				},
			}

			_, err := InspectOperation(reader, nil, Request{
				OperationID: tt.record.ID,
				RouteClass:  auth.RouteClassVolumeGlobal,
				Caller:      tt.caller,
			})
			if !errors.Is(err, ErrInspectionDenied) {
				t.Fatalf("expected ErrInspectionDenied, got %v", err)
			}
		})
	}
}

func TestInspectOperationDeniesGlobalRecordToProductCaller(t *testing.T) {
	reader := &fakeOperationReader{
		records: map[string]operations.OperationRecord{
			"op_global": globalRecord("op_global"),
		},
	}

	_, err := InspectOperation(reader, storedNamespaceAuthorizer("ns_123", productInspectionCaller()), Request{
		OperationID:  "op_global",
		RouteClass:   auth.RouteClassOperationInspection,
		NamespaceID:  "ns_123",
		RequiredRole: auth.RoleOperationInspector,
		Caller:       productInspectionCaller(),
	})
	if !errors.Is(err, ErrInspectionDenied) {
		t.Fatalf("expected ErrInspectionDenied, got %v", err)
	}
}

func TestInspectOperationDoesNotLetProductCallerUsePrivilegedConfiguredRole(t *testing.T) {
	reader := &fakeOperationReader{
		records: map[string]operations.OperationRecord{
			"op_global": globalRecord("op_global"),
		},
	}

	_, err := InspectOperation(reader, nil, Request{
		OperationID:  "op_global",
		RouteClass:   auth.RouteClassOperationInspection,
		RequiredRole: auth.RoleOperatorAdmin,
		Caller: auth.AllowedCaller{
			CallerService: "agentsmith-api",
			Kind:          auth.CallerKindProduct,
			Roles:         []auth.Role{auth.RoleOperatorAdmin},
		},
	})
	if !errors.Is(err, ErrInspectionDenied) {
		t.Fatalf("expected ErrInspectionDenied, got %v", err)
	}
}

func TestInspectOperationPropagatesReaderErrors(t *testing.T) {
	readerErr := errors.New("operation store unavailable")
	reader := &fakeOperationReader{err: readerErr}

	_, err := InspectOperation(reader, nil, Request{
		OperationID:  "op_123",
		RouteClass:   auth.RouteClassOperationInspection,
		NamespaceID:  "ns_123",
		RequiredRole: auth.RoleOperationInspector,
		Caller:       productInspectionCaller(),
	})
	if !errors.Is(err, readerErr) {
		t.Fatalf("expected reader error, got %v", err)
	}
}

func TestInspectOperationRequiresReaderAndOperationID(t *testing.T) {
	_, err := InspectOperation(nil, nil, Request{OperationID: "op_123"})
	if !errors.Is(err, ErrMissingOperationReader) {
		t.Fatalf("expected ErrMissingOperationReader, got %v", err)
	}

	reader := &fakeOperationReader{}
	_, err = InspectOperation(reader, nil, Request{OperationID: " \t"})
	if !errors.Is(err, ErrMissingOperationID) {
		t.Fatalf("expected ErrMissingOperationID, got %v", err)
	}
	if reader.reads != 0 {
		t.Fatalf("reader reads = %d, want 0 for missing operation id", reader.reads)
	}
}

func TestServiceInspectOperationUsesConfiguredReader(t *testing.T) {
	reader := &fakeOperationReader{
		records: map[string]operations.OperationRecord{
			"op_123": namespacedRecord("op_123", "ns_123"),
		},
	}
	service := Service{Reader: reader}
	service.StoredNamespaceAuthorizer = storedNamespaceAuthorizer("ns_123", productInspectionCaller())

	record, err := service.InspectOperation(Request{
		OperationID:  "op_123",
		RouteClass:   auth.RouteClassOperationInspection,
		NamespaceID:  "ns_123",
		RequiredRole: auth.RoleOperationInspector,
		Caller:       productInspectionCaller(),
	})
	if err != nil {
		t.Fatalf("service InspectOperation returned error: %v", err)
	}
	if record.ID != "op_123" {
		t.Fatalf("record ID = %q, want op_123", record.ID)
	}
}

type fakeStoredNamespaceAuthorizer struct {
	allowed           map[string][]auth.AllowedCaller
	calls             int
	lastNamespaceID   string
	lastCallerService string
}

func (authorizer *fakeStoredNamespaceAuthorizer) AllowsOperationInspection(namespaceID string, caller auth.AllowedCaller) bool {
	authorizer.calls++
	authorizer.lastNamespaceID = namespaceID
	authorizer.lastCallerService = caller.CallerService
	return !auth.CallerNotAllowed(caller.CallerService, auth.RoleOperationInspector, authorizer.allowed[namespaceID])
}

func storedNamespaceAuthorizer(namespaceID string, callers ...auth.AllowedCaller) *fakeStoredNamespaceAuthorizer {
	return &fakeStoredNamespaceAuthorizer{
		allowed: map[string][]auth.AllowedCaller{
			namespaceID: callers,
		},
	}
}

type fakeOperationReader struct {
	records         map[string]operations.OperationRecord
	err             error
	reads           int
	lastOperationID string
}

func (reader *fakeOperationReader) ReadOperation(operationID string) (operations.OperationRecord, error) {
	reader.reads++
	reader.lastOperationID = operationID
	if reader.err != nil {
		return operations.OperationRecord{}, reader.err
	}
	return reader.records[operationID], nil
}

func namespacedRecord(operationID, namespaceID string) operations.OperationRecord {
	record := globalRecord(operationID)
	record.NamespaceID = namespaceID
	record.RepoID = "repo_123"
	record.Resource = operations.ResourceRef{Type: "repo", ID: "repo_123"}
	return record
}

func globalRecord(operationID string) operations.OperationRecord {
	return operations.OperationRecord{
		ID:                  operationID,
		Type:                operations.OperationExportCreate,
		State:               operations.OperationStateSucceeded,
		Phase:               "finished",
		CallerService:       "agentsmith-api",
		Resource:            operations.ResourceRef{Type: "volume", ID: "vol_123"},
		ExternalResourceIDs: map[string]string{"jvs_repo_id": "jvs-secret"},
		InputSummary: map[string]any{
			"webdav_password": "plain-webdav-password",
			"metadata_url":    "redis://:metadata-secret@127.0.0.1:6379/1",
		},
	}
}

func productCaller() auth.AllowedCaller {
	return auth.AllowedCaller{
		CallerService: "agentsmith-api",
		Kind:          auth.CallerKindProduct,
		Roles:         []auth.Role{auth.RoleRepoAdmin},
	}
}

func productInspectionCaller() auth.AllowedCaller {
	return auth.AllowedCaller{
		CallerService: "agentsmith-api",
		Kind:          auth.CallerKindProduct,
		Roles:         []auth.Role{auth.RoleOperationInspector},
	}
}

func toInspectionTestString(value any) string {
	encoded, err := json.Marshal(value)
	if err == nil {
		return string(encoded)
	}
	return fmt.Sprint(value)
}
