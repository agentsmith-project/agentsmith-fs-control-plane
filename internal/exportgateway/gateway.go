package exportgateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/exportaccess"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/pathresolver"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
	"golang.org/x/net/webdav"
)

const (
	defaultPrefix       = "/e/"
	defaultHeartbeatTTL = 15 * time.Second
)

type Store interface {
	GetExportGatewayCredential(ctx context.Context, exportID string) (exportaccess.GatewayCredential, error)
	RecordExportRuntimeObservation(ctx context.Context, observation exportaccess.RuntimeObservation) (exportaccess.Session, error)
}

type Config struct {
	Store        Store
	VolumeRoots  map[string]string
	Prefix       string
	Now          func() time.Time
	HeartbeatTTL time.Duration
}

type ServerConfig struct {
	ListenAddr  string
	PostgresDSN string
	VolumeRoots map[string]string
	Prefix      string
}

type Handler struct {
	store        Store
	volumeRoots  map[string]string
	prefix       string
	now          func() time.Time
	heartbeatTTL time.Duration
	lockSystem   webdav.LockSystem
}

type gatewayPath struct {
	ExportID string
	Root     bool
	RawChild string
	Segments []string
}

func NewHandler(cfg Config) (http.Handler, error) {
	if cfg.Store == nil {
		return nil, errors.New("export gateway store is required")
	}
	prefix := cfg.Prefix
	if prefix == "" {
		prefix = defaultPrefix
	}
	if !validPrefix(prefix) {
		return nil, errors.New("export gateway prefix must start and end with /")
	}
	if len(cfg.VolumeRoots) == 0 {
		return nil, errors.New("export gateway volume roots are required")
	}
	roots := make(map[string]string, len(cfg.VolumeRoots))
	for volumeID, root := range cfg.VolumeRoots {
		if err := pathresolver.ValidateID(pathresolver.VolumeID, volumeID); err != nil {
			return nil, fmt.Errorf("invalid export gateway volume roots")
		}
		if err := validateVolumeRoot(root); err != nil {
			return nil, fmt.Errorf("invalid export gateway volume roots")
		}
		roots[volumeID] = root
	}
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	heartbeatTTL := cfg.HeartbeatTTL
	if heartbeatTTL <= 0 {
		heartbeatTTL = defaultHeartbeatTTL
	}
	return &Handler{
		store:        cfg.Store,
		volumeRoots:  roots,
		prefix:       prefix,
		now:          now,
		heartbeatTTL: heartbeatTTL,
		lockSystem:   webdav.NewMemLS(),
	}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	source, ok := h.parseSourcePath(rawRequestPath(r))
	if !ok {
		http.NotFound(w, r)
		return
	}

	username, password, ok := r.BasicAuth()
	if !ok {
		w.Header().Set("WWW-Authenticate", `Basic realm="afscp-export"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if username != source.ExportID {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	credential, err := h.store.GetExportGatewayCredential(r.Context(), source.ExportID)
	if err != nil || !credential.Verifier.Verify(password) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !credentialUsable(credential, h.now()) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if credential.Session.ID != source.ExportID {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !methodAllowed(r.Method, credential.Session.Mode) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	payloadRoot, err := h.payloadRoot(credential)
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := h.validatePath(payloadRoot, credential.PayloadVolumeSubdir, source); err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	dest := gatewayPath{Root: true}
	if methodNeedsDestination(r.Method) {
		parsedDest, ok := h.parseDestination(r, source.ExportID)
		if !ok {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		dest = parsedDest
		if err := h.validatePath(payloadRoot, credential.PayloadVolumeSubdir, dest); err != nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}

	fs, err := newNoFollowFileSystem(payloadRoot)
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	mutating := methodMutates(r.Method)
	if err := h.startRequestObservation(r.Context(), source.ExportID, mutating); err != nil {
		_ = fs.Close()
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}
	defer fs.Close()
	stopHeartbeat := h.startHeartbeat(r.Context(), source.ExportID)
	defer stopHeartbeat()

	backendReq := cloneForBackend(r, source, dest)
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	defer func() {
		h.endRequestObservation(r.Context(), source.ExportID, mutating, rec.status)
	}()
	backend := &webdav.Handler{
		Prefix:     "/",
		FileSystem: fs,
		LockSystem: h.lockSystem,
	}
	backend.ServeHTTP(rec, backendReq)
}

func (h *Handler) startRequestObservation(ctx context.Context, exportID string, mutating bool) error {
	delta := activeDelta{requests: 1}
	if mutating {
		delta.writes = 1
	}
	if _, err := h.recordRuntimeObservation(ctx, exportID, delta, nil); err != nil {
		return err
	}
	return nil
}

func (h *Handler) endRequestObservation(ctx context.Context, exportID string, mutating bool, status int) {
	delta := activeDelta{requests: -1}
	if mutating {
		delta.writes = -1
	}
	var accessedAt *time.Time
	if status < 400 {
		now := h.now()
		accessedAt = &now
	}
	_, _ = h.recordRuntimeObservation(ctx, exportID, delta, accessedAt)
}

type activeDelta struct {
	requests int
	writes   int
}

func (h *Handler) recordRuntimeObservation(ctx context.Context, exportID string, delta activeDelta, successfulAccessedAt *time.Time) (exportaccess.Session, error) {
	observedAt := h.now()
	heartbeatAt := observedAt
	heartbeatExpiresAt := observedAt.Add(h.heartbeatTTL)
	return h.store.RecordExportRuntimeObservation(ctx, exportaccess.RuntimeObservation{
		ExportID:                    exportID,
		ObservedAt:                  observedAt,
		ActiveRequestDelta:          delta.requests,
		ActiveWriteDelta:            delta.writes,
		GatewayHeartbeatAt:          &heartbeatAt,
		GatewayHeartbeatExpiresAt:   &heartbeatExpiresAt,
		SuccessfulRequestAccessedAt: successfulAccessedAt,
	})
}

func (h *Handler) startHeartbeat(ctx context.Context, exportID string) func() {
	interval := h.heartbeatTTL / 2
	if interval <= 0 {
		interval = h.heartbeatTTL
	}
	stop := make(chan struct{})
	go func() {
		timer := time.NewTimer(interval)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-timer.C:
				_, _ = h.recordRuntimeObservation(ctx, exportID, activeDelta{}, nil)
				timer.Reset(interval)
			}
		}
	}()
	return func() {
		close(stop)
	}
}

func (h *Handler) parseSourcePath(rawPath string) (gatewayPath, bool) {
	return parseGatewayPath(rawPath, h.prefix, "")
}

func (h *Handler) parseDestination(r *http.Request, exportID string) (gatewayPath, bool) {
	raw := strings.TrimSpace(r.Header.Get("Destination"))
	if raw == "" {
		return gatewayPath{}, false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || !sameHost(parsed.Host, r.Host) {
		return gatewayPath{}, false
	}
	return parseGatewayPath(parsed.EscapedPath(), h.prefix, exportID)
}

func parseGatewayPath(rawPath, prefix, requiredExportID string) (gatewayPath, bool) {
	if !strings.HasPrefix(rawPath, prefix) {
		return gatewayPath{}, false
	}
	rest := strings.TrimPrefix(rawPath, prefix)
	if rest == "" {
		return gatewayPath{}, false
	}
	exportID, child, hasSlash := strings.Cut(rest, "/")
	if !hasSlash {
		return gatewayPath{}, false
	}
	if err := pathresolver.ValidateID(pathresolver.ExportID, exportID); err != nil {
		return gatewayPath{}, false
	}
	if requiredExportID != "" && exportID != requiredExportID {
		return gatewayPath{}, false
	}
	if child == "" {
		return gatewayPath{ExportID: exportID, Root: true}, true
	}
	if strings.HasSuffix(child, "/") {
		child = strings.TrimSuffix(child, "/")
		if child == "" || strings.HasSuffix(child, "/") {
			return gatewayPath{}, false
		}
	}
	plan, err := pathresolver.ResolvePayloadTraversal("afscp/namespaces/ns_probe/repos/repo_probe/payload", child)
	if err != nil {
		return gatewayPath{}, false
	}
	return gatewayPath{
		ExportID: exportID,
		RawChild: child,
		Segments: plan.Segments,
	}, true
}

func (h *Handler) payloadRoot(credential exportaccess.GatewayCredential) (string, error) {
	root, ok := h.volumeRoots[credential.VolumeID]
	if !ok {
		return "", errors.New("unknown volume")
	}
	if err := validateVolumeRoot(root); err != nil {
		return "", err
	}
	if _, err := pathresolver.ResolvePayloadTraversal(credential.PayloadVolumeSubdir, "__afscp_probe__"); err != nil {
		return "", err
	}
	payloadRoot := filepath.Join(root, filepath.FromSlash(credential.PayloadVolumeSubdir))
	clean := filepath.Clean(payloadRoot)
	if clean != payloadRoot || !pathWithin(root, clean) {
		return "", pathresolver.ErrPathEscape
	}
	return clean, nil
}

func (h *Handler) validatePath(root, payloadSubdir string, parsed gatewayPath) error {
	if parsed.Root {
		return nil
	}
	plan, err := pathresolver.ResolvePayloadTraversal(payloadSubdir, parsed.RawChild)
	if err != nil {
		return err
	}
	return pathresolver.ValidateTraversalPlan(plan, lstatInspector{root: root})
}

type lstatInspector struct {
	root string
}

func (inspector lstatInspector) InspectTraversalEntry(segments []string) (pathresolver.TraversalEntry, error) {
	localPath := filepath.Join(append([]string{inspector.root}, segments...)...)
	if !pathWithin(inspector.root, localPath) {
		return pathresolver.TraversalEntry{}, pathresolver.ErrPathEscape
	}
	info, err := os.Lstat(localPath)
	if errors.Is(err, os.ErrNotExist) {
		return pathresolver.TraversalEntry{Exists: false}, nil
	}
	if err != nil {
		return pathresolver.TraversalEntry{}, err
	}
	entry := pathresolver.TraversalEntry{Exists: true, LinkCount: linkCount(info)}
	if info.Mode()&os.ModeSymlink != 0 {
		entry.Symlink = true
		return entry, nil
	}
	switch {
	case info.IsDir():
		entry.Type = pathresolver.EntryDirectory
	case info.Mode().IsRegular():
		entry.Type = pathresolver.EntryFile
	default:
		entry.Type = pathresolver.EntryOther
	}
	return entry, nil
}

func credentialUsable(credential exportaccess.GatewayCredential, now time.Time) bool {
	if credential.Session.Status != sessionstate.ExportStatusActive {
		return false
	}
	if credential.Session.ExpiresAt.IsZero() || !credential.Session.ExpiresAt.After(now) {
		return false
	}
	return credential.Session.Protocol == exportaccess.ProtocolWebDAV
}

func methodAllowed(method string, mode sessionstate.AccessMode) bool {
	switch method {
	case http.MethodOptions, http.MethodHead, http.MethodGet, "PROPFIND":
		return true
	case http.MethodPut, http.MethodDelete, "MKCOL", "MOVE", "COPY", "PROPPATCH", "LOCK", "UNLOCK":
		return mode == sessionstate.AccessModeReadWrite
	default:
		return false
	}
}

func methodMutates(method string) bool {
	switch method {
	case http.MethodPut, http.MethodDelete, "MKCOL", "MOVE", "COPY", "PROPPATCH", "LOCK", "UNLOCK":
		return true
	default:
		return false
	}
}

func methodNeedsDestination(method string) bool {
	return method == "MOVE" || method == "COPY"
}

func cloneForBackend(r *http.Request, source gatewayPath, dest gatewayPath) *http.Request {
	clone := r.Clone(r.Context())
	clone.URL.Path = backendPath(source)
	clone.URL.RawPath = ""
	clone.RequestURI = ""
	if methodNeedsDestination(r.Method) {
		clone.Header = clone.Header.Clone()
		clone.Header.Set("Destination", backendDestination(r, dest))
	}
	return clone
}

func backendDestination(r *http.Request, dest gatewayPath) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host + backendPath(dest)
}

func backendPath(parsed gatewayPath) string {
	if parsed.Root {
		return "/"
	}
	return "/" + path.Join(parsed.Segments...)
}

func rawRequestPath(r *http.Request) string {
	if r.RequestURI != "" && strings.HasPrefix(r.RequestURI, "/") {
		raw, _, _ := strings.Cut(r.RequestURI, "?")
		return raw
	}
	return r.URL.EscapedPath()
}

func validateVolumeRoot(root string) error {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root || root == string(filepath.Separator) {
		return pathresolver.ErrInvalidPath
	}
	return nil
}

func pathWithin(root, child string) bool {
	rel, err := filepath.Rel(root, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func validPrefix(prefix string) bool {
	return strings.HasPrefix(prefix, "/") && strings.HasSuffix(prefix, "/") &&
		!strings.Contains(prefix, "%") && !strings.Contains(prefix, "\\") &&
		!strings.Contains(prefix, "//") && prefix != "/"
}

func sameHost(left, right string) bool {
	return strings.EqualFold(left, right)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (rec *statusRecorder) WriteHeader(status int) {
	rec.status = status
	rec.ResponseWriter.WriteHeader(status)
}
