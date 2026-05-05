package api

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/namespaceauth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operationinspect"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/pathresolver"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

type operationInspectionNamespaceBindingAuthorizer struct {
	Reader NamespaceVolumeBindingReader
}

func (authorizer operationInspectionNamespaceBindingAuthorizer) AllowsOperationInspection(ctx context.Context, namespaceID string, caller auth.AllowedCaller) bool {
	allowed, err := authorizer.AllowsOperationInspectionWithError(ctx, namespaceID, caller)
	return err == nil && allowed
}

func (authorizer operationInspectionNamespaceBindingAuthorizer) AllowsOperationInspectionWithError(ctx context.Context, namespaceID string, caller auth.AllowedCaller) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if authorizer.Reader == nil || caller.Kind != auth.CallerKindProduct {
		return false, nil
	}
	namespaceID = strings.TrimSpace(namespaceID)
	if err := pathresolver.ValidateID(pathresolver.NamespaceID, namespaceID); err != nil {
		return false, operationinspect.ErrInvalidStoredNamespaceAuthorizationState
	}
	binding, err := authorizer.Reader.GetNamespaceVolumeBinding(ctx, namespaceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, operationinspect.ErrStoredNamespaceAuthorizationUnavailable
	}
	if strings.TrimSpace(binding.NamespaceID) != namespaceID {
		return false, operationinspect.ErrInvalidStoredNamespaceAuthorizationState
	}
	if err := binding.Validate(); err != nil {
		return false, operationinspect.ErrInvalidStoredNamespaceAuthorizationState
	}
	if binding.Status != resources.NamespaceStatusActive {
		return false, nil
	}
	for _, storedCaller := range binding.AllowedCallers {
		mapped, ok := namespaceauth.MapAllowedCaller(storedCaller)
		if !ok {
			return false, operationinspect.ErrInvalidStoredNamespaceAuthorizationState
		}
		if !auth.CallerNotAllowed(caller.CallerService, auth.RoleOperationInspector, []auth.AllowedCaller{mapped}) {
			return true, nil
		}
	}
	return false, nil
}
