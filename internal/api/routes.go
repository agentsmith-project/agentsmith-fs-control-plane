package api

import (
	"net/http"
	"strings"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
)

type RouteMetadata struct {
	Method       string
	Path         string
	OperationID  string
	Class        auth.RouteClass
	Mutating     bool
	RequiredRole auth.Role
}

var internalV1RouteMetadata = []RouteMetadata{
	{Method: http.MethodPost, Path: "/internal/v1/volumes/{volumeId}:ensure", OperationID: "ensureVolume", Class: auth.RouteClassVolumeGlobal, Mutating: true, RequiredRole: auth.RoleVolumeAdmin},
	{Method: http.MethodGet, Path: "/internal/v1/volumes/{volumeId}/health", OperationID: "getVolumeHealth", Class: auth.RouteClassVolumeGlobal, Mutating: false, RequiredRole: auth.RoleVolumeAdmin},
	{Method: http.MethodPut, Path: "/internal/v1/namespaces/{namespaceId}", OperationID: "upsertNamespace", Class: auth.RouteClassNamespaceBound, Mutating: true, RequiredRole: auth.RoleNamespaceAdmin},
	{Method: http.MethodPost, Path: "/internal/v1/namespaces/{namespaceId}:disable", OperationID: "disableNamespace", Class: auth.RouteClassNamespaceBound, Mutating: true, RequiredRole: auth.RoleNamespaceAdmin},
	{Method: http.MethodPut, Path: "/internal/v1/namespaces/{namespaceId}/volume-binding", OperationID: "putNamespaceVolumeBinding", Class: auth.RouteClassNamespaceBound, Mutating: true, RequiredRole: auth.RoleNamespaceAdmin},
	{Method: http.MethodGet, Path: "/internal/v1/namespaces/{namespaceId}/volume-binding", OperationID: "getNamespaceVolumeBinding", Class: auth.RouteClassNamespaceBound, Mutating: false, RequiredRole: auth.RoleNamespaceAdmin},
	{Method: http.MethodPost, Path: "/internal/v1/repos", OperationID: "createRepo", Class: auth.RouteClassNamespaceBound, Mutating: true, RequiredRole: auth.RoleRepoAdmin},
	{Method: http.MethodGet, Path: "/internal/v1/repos", OperationID: "listRepos", Class: auth.RouteClassNamespaceBound, Mutating: false, RequiredRole: auth.RoleRepoAdmin},
	{Method: http.MethodGet, Path: "/internal/v1/repos/{repoId}", OperationID: "getRepo", Class: auth.RouteClassNamespaceBound, Mutating: false, RequiredRole: auth.RoleRepoAdmin},
	{Method: http.MethodPost, Path: "/internal/v1/repos/{repoId}:archive", OperationID: "archiveRepo", Class: auth.RouteClassNamespaceBound, Mutating: true, RequiredRole: auth.RoleRepoLifecycleAdmin},
	{Method: http.MethodPost, Path: "/internal/v1/repos/{repoId}:restore-archived", OperationID: "restoreArchivedRepo", Class: auth.RouteClassNamespaceBound, Mutating: true, RequiredRole: auth.RoleRepoLifecycleAdmin},
	{Method: http.MethodPost, Path: "/internal/v1/repos/{repoId}:delete", OperationID: "deleteRepo", Class: auth.RouteClassNamespaceBound, Mutating: true, RequiredRole: auth.RoleRepoLifecycleAdmin},
	{Method: http.MethodPost, Path: "/internal/v1/repos/{repoId}:restore-tombstoned", OperationID: "restoreTombstonedRepo", Class: auth.RouteClassNamespaceBound, Mutating: true, RequiredRole: auth.RoleRepoLifecycleAdmin},
	{Method: http.MethodPost, Path: "/internal/v1/repos/{repoId}:purge", OperationID: "purgeRepo", Class: auth.RouteClassNamespaceBound, Mutating: true, RequiredRole: auth.RoleRepoLifecycleAdmin},
	{Method: http.MethodPost, Path: "/internal/v1/repos/{repoId}/save-points", OperationID: "createSavePoint", Class: auth.RouteClassNamespaceBound, Mutating: true, RequiredRole: auth.RoleRepoAdmin},
	{Method: http.MethodGet, Path: "/internal/v1/repos/{repoId}/save-points", OperationID: "listSavePoints", Class: auth.RouteClassNamespaceBound, Mutating: false, RequiredRole: auth.RoleRepoAdmin},
	{Method: http.MethodPost, Path: "/internal/v1/repos/{repoId}/restore-preview", OperationID: "restorePreview", Class: auth.RouteClassNamespaceBound, Mutating: true, RequiredRole: auth.RoleRestoreAdmin},
	{Method: http.MethodPost, Path: "/internal/v1/repos/{repoId}/restore-preview:discard", OperationID: "restorePreviewDiscard", Class: auth.RouteClassNamespaceBound, Mutating: true, RequiredRole: auth.RoleRestoreAdmin},
	{Method: http.MethodPost, Path: "/internal/v1/repos/{repoId}/restore-run", OperationID: "restoreRun", Class: auth.RouteClassNamespaceBound, Mutating: true, RequiredRole: auth.RoleRestoreAdmin},
	{Method: http.MethodPost, Path: "/internal/v1/repo-templates", OperationID: "createRepoTemplate", Class: auth.RouteClassNamespaceBound, Mutating: true, RequiredRole: auth.RoleTemplateAdmin},
	{Method: http.MethodPost, Path: "/internal/v1/repo-templates/{templateId}:clone", OperationID: "cloneRepoTemplate", Class: auth.RouteClassNamespaceBound, Mutating: true, RequiredRole: auth.RoleTemplateAdmin},
	{Method: http.MethodPost, Path: "/internal/v1/repos/{repoId}/exports", OperationID: "createExport", Class: auth.RouteClassNamespaceBound, Mutating: true, RequiredRole: auth.RoleExportAdmin},
	{Method: http.MethodGet, Path: "/internal/v1/exports/{exportId}", OperationID: "getExport", Class: auth.RouteClassNamespaceBound, Mutating: false, RequiredRole: auth.RoleExportAdmin},
	{Method: http.MethodDelete, Path: "/internal/v1/exports/{exportId}", OperationID: "revokeExport", Class: auth.RouteClassNamespaceBound, Mutating: true, RequiredRole: auth.RoleExportAdmin},
	{Method: http.MethodPost, Path: "/internal/v1/repos/{repoId}/workload-mount-bindings", OperationID: "createWorkloadMountBinding", Class: auth.RouteClassNamespaceBound, Mutating: true, RequiredRole: auth.RoleMountAdmin},
	{Method: http.MethodGet, Path: "/internal/v1/workload-mount-bindings/{mountBindingId}", OperationID: "getWorkloadMountBinding", Class: auth.RouteClassNamespaceBound, Mutating: false, RequiredRole: auth.RoleMountAdmin},
	{Method: http.MethodPatch, Path: "/internal/v1/workload-mount-bindings/{mountBindingId}/status", OperationID: "updateWorkloadMountBindingStatus", Class: auth.RouteClassNamespaceBound, Mutating: true, RequiredRole: auth.RoleOrchestratorMount},
	{Method: http.MethodGet, Path: "/internal/v1/workload-mount-bindings/{mountBindingId}/orchestrator-plan", OperationID: "getOrchestratorMountPlan", Class: auth.RouteClassNamespaceBound, Mutating: false, RequiredRole: auth.RoleOrchestratorMount},
	{Method: http.MethodPost, Path: "/internal/v1/workload-mount-bindings/{mountBindingId}:heartbeat", OperationID: "heartbeatWorkloadMountBinding", Class: auth.RouteClassNamespaceBound, Mutating: true, RequiredRole: auth.RoleOrchestratorMount},
	{Method: http.MethodPost, Path: "/internal/v1/workload-mount-bindings/{mountBindingId}:release", OperationID: "releaseWorkloadMountBinding", Class: auth.RouteClassNamespaceBound, Mutating: true, RequiredRole: auth.RoleOrchestratorMount},
	{Method: http.MethodPost, Path: "/internal/v1/workload-mount-bindings/{mountBindingId}:revoke", OperationID: "revokeWorkloadMountBinding", Class: auth.RouteClassNamespaceBound, Mutating: true, RequiredRole: auth.RoleMountAdmin},
	{Method: http.MethodGet, Path: "/internal/v1/operations/{operationId}", OperationID: "getOperation", Class: auth.RouteClassOperationInspection, Mutating: false, RequiredRole: auth.RoleOperationInspector},
	{Method: http.MethodPost, Path: "/internal/v1/operations/{operationId}:repair", OperationID: "repairOperation", Class: auth.RouteClassOperationInspection, Mutating: true, RequiredRole: auth.RoleOperatorAdmin},
}

type staticRouteClassResolver struct{}

func InternalV1RouteClassResolver() RouteClassResolver {
	return staticRouteClassResolver{}
}

func InternalV1RouteMetadata() []RouteMetadata {
	routes := make([]RouteMetadata, len(internalV1RouteMetadata))
	copy(routes, internalV1RouteMetadata)
	return routes
}

func RouteMetadataByOperationID(operationID string) (RouteMetadata, bool) {
	for _, metadata := range internalV1RouteMetadata {
		if metadata.OperationID == operationID {
			return metadata, true
		}
	}
	return RouteMetadata{}, false
}

func RouteMetadataForRequest(r *http.Request) (RouteMetadata, bool) {
	if r == nil || r.URL == nil {
		return RouteMetadata{}, false
	}

	method := strings.ToUpper(strings.TrimSpace(r.Method))
	for _, metadata := range internalV1RouteMetadata {
		if method != metadata.Method {
			continue
		}
		if _, ok := RoutePathParams(metadata.Path, r.URL.Path); ok {
			return metadata, true
		}
	}
	return RouteMetadata{}, false
}

func (staticRouteClassResolver) ResolveRouteClass(r *http.Request) (RouteMetadata, bool) {
	return RouteMetadataForRequest(r)
}

func routePatternMatches(pattern, path string) bool {
	_, ok := RoutePathParams(pattern, path)
	return ok
}

func RoutePathParams(pattern, path string) (map[string]string, bool) {
	patternSegments := splitRoutePath(pattern)
	pathSegments := splitRoutePath(path)
	if len(patternSegments) != len(pathSegments) {
		return nil, false
	}

	params := make(map[string]string)
	for i := range patternSegments {
		name, value, ok := routeSegmentParam(patternSegments[i], pathSegments[i])
		if !ok {
			return nil, false
		}
		if name != "" {
			if _, exists := params[name]; exists {
				return nil, false
			}
			params[name] = value
		}
	}
	return params, true
}

func splitRoutePath(path string) []string {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
}

func routeSegmentMatches(pattern, path string) bool {
	_, _, ok := routeSegmentParam(pattern, path)
	return ok
}

func routeSegmentParam(pattern, path string) (string, string, bool) {
	if !strings.HasPrefix(pattern, "{") {
		return "", "", pattern == path
	}

	end := strings.Index(pattern, "}")
	if end <= 1 {
		return "", "", false
	}

	name := pattern[1:end]
	suffix := pattern[end+1:]
	if suffix != "" {
		if !strings.HasSuffix(path, suffix) {
			return "", "", false
		}
		path = strings.TrimSuffix(path, suffix)
	}
	if path == "" {
		return "", "", false
	}
	return name, path, true
}
