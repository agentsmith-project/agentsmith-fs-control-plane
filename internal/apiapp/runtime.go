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
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/config"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/jvsrunner"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/store/postgres"

	_ "github.com/lib/pq"
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

	storeFactory := options.StoreFactory
	if storeFactory == nil {
		storeFactory = OpenPostgresStore
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
		ReadinessProvider:              internalReadinessProvider(cfg, handle.Ping),
		WorkloadMountAdmissionDisabled: !cfg.Capabilities.Mount.Available(),
	})

	return &Runtime{Handler: handler, close: handle.Close}, nil
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

func OpenPostgresStore(ctx context.Context, dsn string) (StoreHandle, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return StoreHandle{}, err
	}
	if err := db.PingContext(ctx); err != nil {
		closeErr := db.Close()
		return StoreHandle{}, errors.Join(err, closeErr)
	}
	st := postgres.New(db)
	return StoreHandle{Store: st, Close: db.Close, Ping: db.PingContext}, nil
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
	return api.ReadinessResponse{
		Status:               "not_ready",
		Ready:                false,
		RequiredCapabilities: internalRequiredReadinessCapabilities(cfg),
		Capabilities: map[string]api.CapabilityGate{
			api.CapabilityStorage: {
				Enabled: true,
				Ready:   true,
				Gated:   false,
				Reason:  "",
			},
			api.CapabilityJVS:          capabilityReadiness(cfg.Capabilities.JVS, "jvs_not_configured", "jvs_not_ready"),
			api.CapabilityWebDAVExport: capabilityReadiness(cfg.Capabilities.WebDAV, "webdav_not_configured", "webdav_not_ready"),
			api.CapabilityWorkloadMount: {
				Enabled: cfg.Capabilities.Mount.Enabled,
				Ready:   cfg.Capabilities.Mount.Ready,
				Gated:   !cfg.Capabilities.Mount.Available(),
				Reason:  mountReadinessReason(cfg.Capabilities.Mount),
			},
		},
	}
}

func internalReadinessProvider(cfg config.Config, ping func(context.Context) error) func(context.Context) api.ReadinessResponse {
	return func(ctx context.Context) api.ReadinessResponse {
		readiness := internalReadiness(cfg)
		if ping == nil {
			readiness.Capabilities[api.CapabilityStorage] = api.CapabilityGate{
				Enabled: true,
				Ready:   false,
				Gated:   true,
				Reason:  "storage_health_check_missing",
			}
			return readiness
		}
		if err := ping(ctx); err != nil {
			readiness.Capabilities[api.CapabilityStorage] = api.CapabilityGate{
				Enabled: true,
				Ready:   false,
				Gated:   true,
				Reason:  "storage_not_ready",
			}
		}
		return readiness
	}
}

func internalRequiredReadinessCapabilities(cfg config.Config) []string {
	if cfg.ReadinessProfile == config.ReadinessProfileGA {
		return []string{
			api.CapabilityStorage,
			api.CapabilityJVS,
			api.CapabilityWebDAVExport,
			api.CapabilityWorkloadMount,
		}
	}
	required := []string{api.CapabilityStorage}
	if cfg.Capabilities.JVS.Enabled {
		required = append(required, api.CapabilityJVS)
	}
	if cfg.Capabilities.Mount.Enabled {
		required = append(required, api.CapabilityWorkloadMount)
	}
	if cfg.Capabilities.WebDAV.Enabled {
		required = append(required, api.CapabilityWebDAVExport)
	}
	return required
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

func capabilityReadiness(capability config.Capability, disabledReason string, unreadyReason string) api.CapabilityGate {
	gate := api.CapabilityGate{
		Enabled: capability.Enabled,
		Ready:   capability.Ready,
		Gated:   false,
		Reason:  "",
	}
	if !capability.Enabled {
		gate.Gated = true
		gate.Reason = disabledReason
		return gate
	}
	if !capability.Ready {
		gate.Gated = true
		gate.Reason = unreadyReason
	}
	return gate
}

func NewOperationID() string {
	counter := atomic.AddUint64(&operationCounter, 1)
	return fmt.Sprintf("op_api_%d_%d", time.Now().UTC().UnixNano(), counter)
}
