package inspection

import (
	"context"
	"strings"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/namespaceauth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/pathresolver"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

type NamespaceVolumeBindingReader interface {
	GetNamespaceVolumeBinding(ctx context.Context, namespaceID string) (resources.NamespaceVolumeBinding, error)
}

type NamespaceVolumeBindingAuthorizer struct {
	Reader NamespaceVolumeBindingReader
}

func (authorizer NamespaceVolumeBindingAuthorizer) AllowsOperationInspection(ctx context.Context, namespaceID string, caller auth.AllowedCaller) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	if authorizer.Reader == nil {
		return false
	}
	if caller.Kind != auth.CallerKindProduct {
		return false
	}

	namespaceID = strings.TrimSpace(namespaceID)
	if err := pathresolver.ValidateID(pathresolver.NamespaceID, namespaceID); err != nil {
		return false
	}

	binding, err := authorizer.Reader.GetNamespaceVolumeBinding(ctx, namespaceID)
	if err != nil {
		return false
	}
	if strings.TrimSpace(binding.NamespaceID) != namespaceID {
		return false
	}
	if binding.Status != resources.NamespaceStatusActive {
		return false
	}

	for _, storedCaller := range binding.AllowedCallers {
		mapped, ok := namespaceauth.MapAllowedCaller(storedCaller)
		if !ok {
			continue
		}
		if !auth.CallerNotAllowed(caller.CallerService, auth.RoleOperationInspector, []auth.AllowedCaller{mapped}) {
			return true
		}
	}

	return false
}
