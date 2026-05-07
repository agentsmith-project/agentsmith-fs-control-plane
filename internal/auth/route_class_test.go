package auth

import (
	"errors"
	"net/http"
	"testing"
)

func TestValidateRequestContextForRouteRequiresNamespaceOnlyForNamespaceBound(t *testing.T) {
	base := RequestContext{
		Authorization: "Bearer service-token",
		CorrelationID: "corr-123",
		CallerService: "product-caller",
	}

	tests := []struct {
		name    string
		class   RouteClass
		wantErr error
	}{
		{name: "namespace bound", class: RouteClassNamespaceBound, wantErr: ErrMissingNamespaceID},
		{name: "volume global", class: RouteClassVolumeGlobal},
		{name: "operation inspection", class: RouteClassOperationInspection},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRequestContextForRoute(base, RouteValidation{
				Class:    tt.class,
				Mutating: false,
			})

			if tt.wantErr == nil && err != nil {
				t.Fatalf("validation failed: %v", err)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestValidateRequestContextForRouteUsesRouteMutatingMetadata(t *testing.T) {
	ctx := RequestContext{
		Authorization: "Bearer service-token",
		CorrelationID: "corr-123",
		CallerService: "product-caller",
		NamespaceID:   "ns-123",
	}

	err := ValidateRequestContextForRoute(ctx, RouteValidation{
		Class:    RouteClassNamespaceBound,
		Mutating: true,
	})

	if !errors.Is(err, ErrMissingIdempotencyKey) {
		t.Fatalf("expected ErrMissingIdempotencyKey, got %v", err)
	}
	if !errors.Is(err, ErrMissingActor) {
		t.Fatalf("expected ErrMissingActor, got %v", err)
	}
}

func TestValidateAuthenticatedRequestForRouteBindsPrincipalAndClass(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/internal/v1/operations/op_123", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(HeaderAuthorization, "Bearer service-token")
	req.Header.Set(HeaderCorrelationID, "corr-123")

	ctx, err := ValidateAuthenticatedRequestForRoute(req, AuthenticatedPrincipal{
		Subject:                "service-account:product-caller",
		CanonicalCallerService: "product-caller",
	}, RouteValidation{
		Class:    RouteClassOperationInspection,
		Mutating: false,
	})
	if err != nil {
		t.Fatalf("operation inspection should not require request namespace: %v", err)
	}
	if ctx.CallerService != "product-caller" {
		t.Fatalf("CallerService = %q", ctx.CallerService)
	}
}
