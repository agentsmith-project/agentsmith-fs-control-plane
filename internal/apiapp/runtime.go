package apiapp

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/api"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/auth"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/capability"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/config"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/jvsrunner"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/pathresolver"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store/postgres"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/workloadmount"

	_ "github.com/lib/pq"
	"golang.org/x/sys/unix"
)

type InternalStore interface {
	api.NamespaceVolumeBindingReader
	api.NamespaceReader
	api.RepoReader
	api.VolumeReader
	api.RepoFenceReader
	api.RepoJVSMutationGateReader
	api.OperationInspectionStoreReader
	api.OperationIntakeStore
	api.RepoCreateOperationIntakeStore
	api.TemplateOperationIntakeStore
	api.RestorePreviewOperationIntakeStore
	api.RestorePreviewDiscardOperationIntakeStore
	api.RestoreRunOperationIntakeStore
	api.OperationIdempotencyLookupStore
	api.RestorePreviewPlanGateReader
	api.RestorePreviewDiscardMetadataReader
	api.RestoreRunMetadataReader
	api.RestoreRunIntakeGateReader
	api.WorkloadMountBindingReader
	api.WorkloadMountPlanReader
	api.ExportStore
	auditAppendStore
}

type auditAppendStore interface {
	AppendAuditEvent(context.Context, audit.Event) error
}

type StoreFactory func(context.Context, string) (StoreHandle, error)

type StoreHandle struct {
	Store InternalStore
	Close func() error
	Ping  func(context.Context) error
}

type Options struct {
	Source       config.Source
	StoreFactory StoreFactory
	Logger       *slog.Logger
	OperationID  api.OperationIDGenerator
	Clock        func() time.Time
}

type Runtime struct {
	Handler http.Handler
	close   func() error
}

var operationCounter uint64

func NewRuntime(options Options) (*Runtime, error) {
	source := options.Source
	if source == nil {
		source = config.EnvSource{}
	}
	cfg, err := config.Load(source)
	if err != nil {
		return nil, err
	}
	return NewRuntimeFromConfig(cfg, options)
}

func NewRuntimeFromConfig(cfg config.Config, options Options) (*Runtime, error) {
	if strings.TrimSpace(cfg.API.Mode) != "internal" {
		return nil, errors.New("api internal runtime requires AFSCP_API_MODE=internal")
	}
	dsn := strings.TrimSpace(cfg.API.PostgresDSN)
	if dsn == "" {
		return nil, errors.New("AFSCP_API_POSTGRES_DSN is required when AFSCP_API_MODE is internal")
	}
	webDAVExportPublicBaseURL := ""
	if cfg.Capabilities.WebDAV.Available() {
		normalized, err := config.NormalizeWebDAVExportPublicBaseURL(cfg.API.WebDAVExportPublicBaseURL)
		if err != nil {
			return nil, err
		}
		webDAVExportPublicBaseURL = normalized
	}
	resolver, err := NewServiceTokenPrincipalResolver(cfg.API.ServiceTokens)
	if err != nil {
		return nil, err
	}
	globalCallers, err := parseAllowedCallerConfig("AFSCP_API_DEPLOYMENT_GLOBAL_ALLOWED_CALLERS", cfg.API.DeploymentGlobalAllowedCallers)
	if err != nil {
		return nil, err
	}
	namespaceCallers, err := parseAllowedCallerConfig("AFSCP_API_DEPLOYMENT_NAMESPACE_ALLOWED_CALLERS", cfg.API.DeploymentNamespaceAllowedCallers)
	if err != nil {
		return nil, err
	}
	workloadMountRuntimeSecretRefs, err := workloadMountRuntimeSecretRefsFromConfig(cfg.API.WorkloadMountRuntimeSecretRefs)
	if err != nil {
		return nil, err
	}
	if cfg.Capabilities.Mount.Available() && len(workloadMountRuntimeSecretRefs) == 0 {
		return nil, errors.New("AFSCP_API_WORKLOAD_MOUNT_SECRET_REFS is required when workload mount capability is available")
	}

	storeFactory := options.StoreFactory
	if storeFactory == nil {
		storeFactory = func(ctx context.Context, dsn string) (StoreHandle, error) {
			return OpenPostgresStore(ctx, dsn, postgres.WithWorkloadMountRuntimeSecretRefs(workloadMountRuntimeSecretRefs))
		}
	}
	openCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	handle, err := storeFactory(openCtx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open api store: %w", err)
	}
	if handle.Store == nil {
		err := errors.New("api store is required")
		if handle.Close != nil {
			err = errors.Join(err, handle.Close())
		}
		return nil, err
	}

	operationID := options.OperationID
	if operationID == nil {
		operationID = NewOperationID
	}
	now := options.Clock
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	var savePointHistoryJVSRunner api.JVSHistoryRunner
	if cfg.API.SavePointHistory.Enabled {
		if err := verifyFileSHA256(cfg.API.SavePointHistory.JVSBinaryPath, config.JVSAcceptedLinuxAMD64SHA256); err != nil {
			if handle.Close != nil {
				err = errors.Join(err, handle.Close())
			}
			return nil, err
		}
		runner, err := jvsrunner.New(jvsrunner.Config{BinaryPath: cfg.API.SavePointHistory.JVSBinaryPath, CWD: cfg.API.SavePointHistory.JVSCWD})
		if err != nil {
			if handle.Close != nil {
				err = errors.Join(err, handle.Close())
			}
			return nil, err
		}
		savePointHistoryJVSRunner = runner
	}

	handler := api.NewInternalAPIShell(api.InternalAPIShellConfig{
		Logger:                         options.Logger,
		AuditSink:                      auditOutboxSink{store: handle.Store},
		PrincipalResolver:              resolver,
		NamespaceBindingReader:         handle.Store,
		NamespaceReader:                handle.Store,
		RepoReader:                     handle.Store,
		VolumeReader:                   handle.Store,
		VolumeBackendHealthProbe:       newVolumeRootBackendHealthProbe(cfg.API.VolumeRoots),
		WorkloadMountBindingReader:     handle.Store,
		WorkloadMountPlanReader:        handle.Store,
		ExportStore:                    handle.Store,
		RepoFenceReader:                handle.Store,
		SavePointMutationGate:          handle.Store,
		SavePointHistoryJVSRunner:      savePointHistoryJVSRunner,
		SavePointHistoryVolumeRoots:    cfg.API.SavePointHistory.VolumeRoots,
		OperationInspectionReader:      handle.Store,
		RepoCreateIntakeStore:          handle.Store,
		DeploymentGlobalCallers:        globalCallers,
		DeploymentNamespaceCallers:     namespaceCallers,
		OperationIntakeStore:           handle.Store,
		GenerateOperationID:            operationID,
		Now:                            func() time.Time { return now().UTC() },
		WebDAVExportPublicBaseURL:      webDAVExportPublicBaseURL,
		ReadinessProvider:              internalReadinessProvider(cfg, handle.Ping),
		WebDAVExportAdmissionDisabled:  !cfg.Capabilities.WebDAV.Available(),
		WorkloadMountAdmissionDisabled: !cfg.Capabilities.Mount.Available(),
		RepoTemplateAdmissionDisabled:  !cfg.Capabilities.RepoTemplate.Available(),
		RepoPurgeAdmissionDisabled:     !cfg.Capabilities.RepoPurge.Available(),
	})

	return &Runtime{Handler: handler, close: handle.Close}, nil
}

type volumeRootBackendHealthProbe struct {
	roots map[string]string
}

func newVolumeRootBackendHealthProbe(roots map[string]string) api.VolumeBackendHealthProbe {
	if len(roots) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(roots))
	for volumeID, root := range roots {
		cloned[volumeID] = root
	}
	return volumeRootBackendHealthProbe{roots: cloned}
}

func (probe volumeRootBackendHealthProbe) CheckVolumeBackendHealth(ctx context.Context, volume resources.Volume) (api.VolumeBackendHealthResult, error) {
	select {
	case <-ctx.Done():
		return api.VolumeBackendHealthResult{}, ctx.Err()
	default:
	}

	root, ok := probe.roots[volume.ID]
	if !ok {
		return api.VolumeBackendHealthResult{Healthy: false}, nil
	}
	if !volumeRootUsableForChildCreation(root) {
		return api.VolumeBackendHealthResult{Healthy: false}, nil
	}
	return api.VolumeBackendHealthResult{Healthy: true}, nil
}

func volumeRootUsableForChildCreation(root string) bool {
	info, err := os.Lstat(root)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false
	}
	mode := info.Mode().Perm()
	if mode&0o222 == 0 || mode&0o111 == 0 {
		return false
	}
	if err := unix.Access(root, unix.W_OK|unix.X_OK); err != nil {
		return false
	}
	return true
}

func verifyFileSHA256(path, want string) error {
	file, err := os.Open(path)
	if err != nil {
		return errors.New("jvs binary verification failed")
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return errors.New("jvs binary verification failed")
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != want {
		return errors.New("jvs binary checksum mismatch")
	}
	return nil
}

func (runtime *Runtime) Close() error {
	if runtime == nil || runtime.close == nil {
		return nil
	}
	return runtime.close()
}

func OpenPostgresStore(ctx context.Context, dsn string, opts ...postgres.Option) (StoreHandle, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return StoreHandle{}, err
	}
	if err := db.PingContext(ctx); err != nil {
		closeErr := db.Close()
		return StoreHandle{}, errors.Join(err, closeErr)
	}
	st := postgres.New(db, opts...)
	return StoreHandle{Store: st, Close: db.Close, Ping: db.PingContext}, nil
}

func workloadMountRuntimeSecretRefsFromConfig(refs map[string]config.SecretRef) (map[string]workloadmount.SecretRef, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	out := make(map[string]workloadmount.SecretRef, len(refs))
	for volumeID, ref := range refs {
		if err := pathresolver.ValidateID(pathresolver.VolumeID, volumeID); err != nil {
			return nil, errors.New("AFSCP_API_WORKLOAD_MOUNT_SECRET_REFS must contain valid volume ids")
		}
		secretRef := workloadmount.SecretRef{Namespace: ref.Namespace, Name: ref.Name}
		if err := workloadmount.ValidateSecretRef(secretRef); err != nil {
			return nil, errors.New("AFSCP_API_WORKLOAD_MOUNT_SECRET_REFS must contain valid secret refs")
		}
		out[volumeID] = secretRef
	}
	return out, nil
}

type auditOutboxSink struct {
	store auditAppendStore
}

func (sink auditOutboxSink) Emit(ctx context.Context, event audit.Event) error {
	if sink.store == nil {
		return nil
	}
	return sink.store.AppendAuditEvent(ctx, event)
}

type ServiceTokenPrincipalResolver struct {
	tokenToCaller map[string]string
}

func NewServiceTokenPrincipalResolver(raw string) (*ServiceTokenPrincipalResolver, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("AFSCP_API_SERVICE_TOKENS is required when AFSCP_API_MODE is internal")
	}
	tokenToCaller := map[string]string{}
	callerSeen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		pair := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(pair) != 2 {
			return nil, errors.New("AFSCP_API_SERVICE_TOKENS must contain caller_service=token pairs")
		}
		caller := strings.TrimSpace(pair[0])
		token := strings.TrimSpace(pair[1])
		if caller == "" || token == "" {
			return nil, errors.New("AFSCP_API_SERVICE_TOKENS must contain non-empty caller_service=token pairs")
		}
		if callerSeen[caller] {
			return nil, errors.New("AFSCP_API_SERVICE_TOKENS must not contain duplicate caller services")
		}
		if _, exists := tokenToCaller[token]; exists {
			return nil, errors.New("AFSCP_API_SERVICE_TOKENS must not contain duplicate tokens")
		}
		callerSeen[caller] = true
		tokenToCaller[token] = caller
	}
	return &ServiceTokenPrincipalResolver{tokenToCaller: tokenToCaller}, nil
}

func (resolver *ServiceTokenPrincipalResolver) ResolvePrincipal(r *http.Request) (auth.AuthenticatedPrincipal, error) {
	if resolver == nil || len(resolver.tokenToCaller) == 0 || r == nil {
		return auth.AuthenticatedPrincipal{}, auth.ErrMissingAuthenticatedPrincipal
	}
	scheme, token, ok := bearerToken(r.Header.Get(auth.HeaderAuthorization))
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return auth.AuthenticatedPrincipal{}, auth.ErrMissingAuthenticatedPrincipal
	}
	for configuredToken, caller := range resolver.tokenToCaller {
		if subtle.ConstantTimeCompare([]byte(token), []byte(configuredToken)) == 1 {
			return auth.AuthenticatedPrincipal{
				Subject:                "service_token:" + caller,
				CanonicalCallerService: caller,
			}, nil
		}
	}
	return auth.AuthenticatedPrincipal{}, auth.ErrMissingAuthenticatedPrincipal
}

func bearerToken(header string) (string, string, bool) {
	fields := strings.Fields(header)
	if len(fields) != 2 {
		return "", "", false
	}
	return fields[0], fields[1], true
}

func parseAllowedCallerConfig(key string, raw string) ([]auth.AllowedCaller, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("%s is required when AFSCP_API_MODE is internal", key)
	}
	var callers []auth.AllowedCaller
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		fields := strings.Split(strings.TrimSpace(part), ":")
		if len(fields) != 3 {
			return nil, fmt.Errorf("%s must contain caller_service:kind:role|role entries", key)
		}
		callerService := strings.TrimSpace(fields[0])
		kind, ok := parseCallerKind(strings.TrimSpace(fields[1]))
		roles, rolesOK := parseRoles(strings.TrimSpace(fields[2]))
		if callerService == "" || !ok || !rolesOK {
			return nil, fmt.Errorf("%s must contain valid caller_service:kind:role|role entries", key)
		}
		if seen[callerService] {
			return nil, fmt.Errorf("%s must not contain duplicate caller services", key)
		}
		seen[callerService] = true
		callers = append(callers, auth.AllowedCaller{
			CallerService: callerService,
			Kind:          kind,
			Roles:         roles,
		})
	}
	return callers, nil
}

func parseCallerKind(raw string) (auth.CallerKind, bool) {
	switch auth.CallerKind(raw) {
	case auth.CallerKindProduct,
		auth.CallerKindAdmin,
		auth.CallerKindOperator,
		auth.CallerKindMigration,
		auth.CallerKindOrchestrator:
		return auth.CallerKind(raw), true
	default:
		return "", false
	}
}

func parseRoles(raw string) ([]auth.Role, bool) {
	if raw == "" {
		return nil, false
	}
	valid := map[auth.Role]bool{}
	for _, role := range auth.CallerRoles() {
		valid[role] = true
	}
	var roles []auth.Role
	seen := map[auth.Role]bool{}
	for _, part := range strings.Split(raw, "|") {
		role := auth.Role(strings.TrimSpace(part))
		if !valid[role] || seen[role] {
			return nil, false
		}
		seen[role] = true
		roles = append(roles, role)
	}
	return roles, len(roles) > 0
}

func internalReadiness(cfg config.Config) api.ReadinessResponse {
	return api.ReadinessFromCapabilityMatrix(internalCapabilityMatrix(cfg))
}

func internalCapabilityMatrix(cfg config.Config) capability.Matrix {
	storageStatus := capabilityStatus(cfg.Capabilities.Storage, "storage_not_configured", "storage_not_ready")
	jvsStatus := capabilityStatus(cfg.Capabilities.JVS, "jvs_not_configured", "jvs_not_ready")
	webDAVStatus := capabilityStatus(cfg.Capabilities.WebDAV, "webdav_not_configured", "webdav_not_ready")
	mountStatus := capability.Status{
		Enabled: cfg.Capabilities.Mount.Enabled,
		Ready:   cfg.Capabilities.Mount.Ready,
		Gated:   !cfg.Capabilities.Mount.Available(),
		Reason:  mountReadinessReason(cfg.Capabilities.Mount),
	}
	templateStatus := capabilityStatus(cfg.Capabilities.RepoTemplate, "repo_template_not_configured", "repo_template_not_ready")
	purgeStatus := capabilityStatus(cfg.Capabilities.RepoPurge, "repo_purge_not_configured", "repo_purge_not_ready")

	return capability.NewMatrix(
		capability.Entry{
			ID:          capability.Storage,
			Status:      storageStatus,
			Requirement: internalCapabilityRequirement(cfg, capability.Storage, storageStatus),
		},
		capability.Entry{
			ID:          capability.JVS,
			Status:      jvsStatus,
			Requirement: internalCapabilityRequirement(cfg, capability.JVS, jvsStatus),
		},
		capability.Entry{
			ID:          capability.WebDAVExport,
			Status:      webDAVStatus,
			Requirement: internalCapabilityRequirement(cfg, capability.WebDAVExport, webDAVStatus),
		},
		capability.Entry{
			ID:          capability.WorkloadMount,
			Status:      mountStatus,
			Requirement: internalCapabilityRequirement(cfg, capability.WorkloadMount, mountStatus),
		},
		capability.Entry{
			ID:          capability.RepoTemplate,
			Status:      templateStatus,
			Requirement: internalCapabilityRequirement(cfg, capability.RepoTemplate, templateStatus),
		},
		capability.Entry{
			ID:          capability.RepoPurge,
			Status:      purgeStatus,
			Requirement: internalCapabilityRequirement(cfg, capability.RepoPurge, purgeStatus),
		},
	)
}

func internalReadinessProvider(cfg config.Config, ping func(context.Context) error) func(context.Context) api.ReadinessResponse {
	return func(ctx context.Context) api.ReadinessResponse {
		readiness := internalReadiness(cfg)
		if !cfg.Capabilities.Storage.Available() {
			return readiness
		}
		if ping == nil {
			readiness.Capabilities[api.CapabilityStorage] = storageReadinessOverride(readiness, "storage_health_check_missing")
			return readiness
		}
		if err := ping(ctx); err != nil {
			readiness.Capabilities[api.CapabilityStorage] = storageReadinessOverride(readiness, "storage_not_ready")
		}
		return readiness
	}
}

func internalRequiredReadinessCapabilities(cfg config.Config) []string {
	ids := []capability.ID{
		capability.Storage,
		capability.JVS,
		capability.WebDAVExport,
		capability.WorkloadMount,
		capability.RepoTemplate,
		capability.RepoPurge,
	}
	required := make([]string, 0, len(ids))
	for _, id := range ids {
		if internalRequiredForServiceReady(cfg, id) {
			required = append(required, string(id))
		}
	}
	return required
}

func storageReadinessOverride(readiness api.ReadinessResponse, reason string) api.CapabilityGate {
	gate := readiness.Capabilities[api.CapabilityStorage]
	gate.Enabled = true
	gate.Ready = false
	gate.Gated = true
	gate.Reason = reason
	return gate
}

func internalCapabilityRequirement(cfg config.Config, id capability.ID, status capability.Status) capability.Requirement {
	requiredForServiceReady := internalRequiredForServiceReady(cfg, id)
	return capability.Requirement{
		RequiredForServiceReady: requiredForServiceReady,
		RequiredForDefaultGA:    capability.RequiredForDefaultGA(id),
		OptionalGated:           !requiredForServiceReady && status.Gated,
	}
}

func internalRequiredForServiceReady(cfg config.Config, id capability.ID) bool {
	if cfg.ReadinessProfile == config.ReadinessProfileGA {
		switch id {
		case capability.Storage, capability.JVS, capability.WebDAVExport:
			return true
		default:
			return false
		}
	}

	switch id {
	case capability.Storage:
		return true
	case capability.JVS:
		return cfg.Capabilities.JVS.Enabled
	case capability.WebDAVExport:
		return cfg.Capabilities.WebDAV.Enabled
	case capability.WorkloadMount:
		return cfg.Capabilities.Mount.Enabled
	case capability.RepoTemplate:
		return cfg.Capabilities.RepoTemplate.Enabled
	case capability.RepoPurge:
		return cfg.Capabilities.RepoPurge.Enabled
	default:
		return false
	}
}

func mountReadinessReason(capability config.Capability) string {
	if !capability.Enabled {
		return "mount_not_configured"
	}
	if !capability.Ready {
		return "mount_not_ready"
	}
	return ""
}

func capabilityStatus(cap config.Capability, disabledReason string, unreadyReason string) capability.Status {
	status := capability.Status{
		Enabled: cap.Enabled,
		Ready:   cap.Ready,
		Gated:   false,
		Reason:  "",
	}
	if !cap.Enabled {
		status.Gated = true
		status.Reason = disabledReason
		return status
	}
	if !cap.Ready {
		status.Gated = true
		status.Reason = unreadyReason
	}
	return status
}

func NewOperationID() string {
	counter := atomic.AddUint64(&operationCounter, 1)
	return fmt.Sprintf("op_api_%d_%d", time.Now().UTC().UnixNano(), counter)
}
