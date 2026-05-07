package exportaccess

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/audit"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/operations"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/pathresolver"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/sessionstate"
)

const (
	ProtocolWebDAV Protocol = "webdav"

	MinTTLSeconds     = 60
	DefaultTTLSeconds = 3600

	passwordVerifierSHA256 = "sha256"
)

type Protocol string

type Actor struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type Session struct {
	ID                        string                    `json:"export_id"`
	NamespaceID               string                    `json:"namespace_id"`
	RepoID                    string                    `json:"repo_id"`
	Protocol                  Protocol                  `json:"protocol"`
	Mode                      sessionstate.AccessMode   `json:"mode"`
	Status                    sessionstate.ExportStatus `json:"status"`
	ExpiresAt                 time.Time                 `json:"expires_at"`
	CreatedByCallerService    string                    `json:"created_by_caller_service"`
	CreatedByActor            Actor                     `json:"created_by_actor"`
	RevokedAt                 *time.Time                `json:"revoked_at"`
	LastAccessedAt            *time.Time                `json:"last_accessed_at"`
	ActiveRequestCount        int                       `json:"active_request_count"`
	ActiveWriteCount          int                       `json:"active_write_count"`
	LastObservedAt            *time.Time                `json:"last_observed_at"`
	LastGatewayHeartbeatAt    *time.Time                `json:"last_gateway_heartbeat_at"`
	GatewayHeartbeatExpiresAt *time.Time                `json:"gateway_heartbeat_expires_at"`
	WriteDrainedAt            *time.Time                `json:"write_drained_at"`
	TerminalObservedAt        *time.Time                `json:"terminal_observed_at"`
	StatusReason              string                    `json:"status_reason"`
	CreatedAt                 time.Time                 `json:"created_at"`
	UpdatedAt                 time.Time                 `json:"updated_at"`
}

type BasicAuth struct {
	Type     string `json:"type"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type Access struct {
	URL       string                  `json:"url"`
	Auth      BasicAuth               `json:"auth"`
	Mode      sessionstate.AccessMode `json:"mode"`
	ExpiresAt time.Time               `json:"expires_at"`
}

type PasswordVerifier struct {
	Algorithm string `json:"algorithm"`
	Salt      string `json:"salt"`
	Hash      string `json:"hash"`
}

type GatewayCredential struct {
	Session             Session
	Verifier            PasswordVerifier
	VolumeID            string
	PayloadVolumeSubdir string
}

type CreateRequest struct {
	Session   Session
	Verifier  PasswordVerifier
	Operation operations.OperationRecord
	Audit     audit.Event
}

type CreateResult struct {
	Session   Session
	Operation operations.OperationRecord
	Reused    bool
}

type RevokeRequest struct {
	ExportID    string
	NamespaceID string
	Operation   operations.OperationRecord
	Audit       audit.Event
	Now         time.Time
}

type RevokeResult struct {
	Session   Session
	Operation operations.OperationRecord
	Reused    bool
}

type RuntimeObservation struct {
	ExportID                    string
	ObservedAt                  time.Time
	ActiveRequestDelta          int
	ActiveWriteDelta            int
	GatewayHeartbeatAt          *time.Time
	GatewayHeartbeatExpiresAt   *time.Time
	SuccessfulRequestAccessedAt *time.Time
}

type RuntimeRequestBegin struct {
	RequestID          string
	ExportID           string
	StartedAt          time.Time
	HeartbeatExpiresAt time.Time
	Write              bool
}

type RuntimeRequestHeartbeat struct {
	RequestID          string
	ExportID           string
	ObservedAt         time.Time
	HeartbeatExpiresAt time.Time
}

type RuntimeRequestEnd struct {
	RequestID                   string
	ExportID                    string
	EndedAt                     time.Time
	SuccessfulRequestAccessedAt *time.Time
}

type StaleRuntimeRequestRecovery struct {
	Now   time.Time
	Limit int
}

type StaleRuntimeRequestRecoveryResult struct {
	Recovered       int
	RecoveredWrites int
}

type ReconcileRequest struct {
	ExportID           string
	NamespaceID        string
	TargetStatus       sessionstate.ExportStatus
	ObservedAt         time.Time
	StatusReason       string
	ActiveRequestCount int
	ActiveWriteCount   int
	Operation          operations.OperationRecord
	Audit              audit.Event
}

type ReconcileResult struct {
	Session   Session
	Operation operations.OperationRecord
	Reused    bool
}

func (protocol Protocol) Valid() bool {
	return protocol == ProtocolWebDAV
}

func (session Session) Validate() error {
	if err := pathresolver.ValidateID(pathresolver.ExportID, strings.TrimSpace(session.ID)); err != nil {
		return err
	}
	if err := pathresolver.ValidateID(pathresolver.NamespaceID, strings.TrimSpace(session.NamespaceID)); err != nil {
		return err
	}
	if err := pathresolver.ValidateID(pathresolver.RepoID, strings.TrimSpace(session.RepoID)); err != nil {
		return err
	}
	if !session.Protocol.Valid() {
		return fmt.Errorf("invalid export protocol %q", session.Protocol)
	}
	switch session.Mode {
	case sessionstate.AccessModeReadOnly, sessionstate.AccessModeReadWrite:
	default:
		return fmt.Errorf("invalid export mode %q", session.Mode)
	}
	switch session.Status {
	case sessionstate.ExportStatusActive,
		sessionstate.ExportStatusRevoking,
		sessionstate.ExportStatusRevoked,
		sessionstate.ExportStatusExpired,
		sessionstate.ExportStatusFailed:
	default:
		return fmt.Errorf("invalid export status %q", session.Status)
	}
	if session.ExpiresAt.IsZero() || session.CreatedAt.IsZero() || session.UpdatedAt.IsZero() {
		return errors.New("export timestamps must be set")
	}
	if strings.TrimSpace(session.CreatedByCallerService) == "" || strings.TrimSpace(session.CreatedByActor.Type) == "" || strings.TrimSpace(session.CreatedByActor.ID) == "" {
		return errors.New("export creator context must be set")
	}
	if session.ActiveRequestCount < 0 || session.ActiveWriteCount < 0 {
		return errors.New("export runtime counts must be non-negative")
	}
	if session.ActiveWriteCount > session.ActiveRequestCount {
		return errors.New("export active_write_count cannot exceed active_request_count")
	}
	return nil
}

func (session Session) MarshalJSON() ([]byte, error) {
	type sessionJSON Session
	return json.Marshal(sessionJSON(session))
}

func (request RuntimeRequestBegin) Validate() error {
	if err := validateRuntimeRequestID(strings.TrimSpace(request.RequestID)); err != nil {
		return err
	}
	if err := pathresolver.ValidateID(pathresolver.ExportID, strings.TrimSpace(request.ExportID)); err != nil {
		return err
	}
	if request.StartedAt.IsZero() || request.HeartbeatExpiresAt.IsZero() {
		return errors.New("export runtime request begin timestamps must be set")
	}
	if !request.HeartbeatExpiresAt.After(request.StartedAt) {
		return errors.New("export runtime request heartbeat expiry must be after start")
	}
	return nil
}

func (request RuntimeRequestHeartbeat) Validate() error {
	if err := validateRuntimeRequestID(strings.TrimSpace(request.RequestID)); err != nil {
		return err
	}
	if err := pathresolver.ValidateID(pathresolver.ExportID, strings.TrimSpace(request.ExportID)); err != nil {
		return err
	}
	if request.ObservedAt.IsZero() || request.HeartbeatExpiresAt.IsZero() {
		return errors.New("export runtime request heartbeat timestamps must be set")
	}
	if !request.HeartbeatExpiresAt.After(request.ObservedAt) {
		return errors.New("export runtime request heartbeat expiry must be after observation")
	}
	return nil
}

func (request RuntimeRequestEnd) Validate() error {
	if err := validateRuntimeRequestID(strings.TrimSpace(request.RequestID)); err != nil {
		return err
	}
	if err := pathresolver.ValidateID(pathresolver.ExportID, strings.TrimSpace(request.ExportID)); err != nil {
		return err
	}
	if request.EndedAt.IsZero() {
		return errors.New("export runtime request end time must be set")
	}
	return nil
}

func (request StaleRuntimeRequestRecovery) Validate() error {
	if request.Now.IsZero() {
		return errors.New("export runtime stale recovery time must be set")
	}
	if request.Limit <= 0 {
		return errors.New("export runtime stale recovery limit must be positive")
	}
	return nil
}

func validateRuntimeRequestID(id string) error {
	const prefix = "errq_"
	if !strings.HasPrefix(id, prefix) {
		return fmt.Errorf("invalid export runtime request id")
	}
	suffix := id[len(prefix):]
	if len(suffix) < 2 || len(suffix) > 63 {
		return fmt.Errorf("invalid export runtime request id")
	}
	if !asciiAlnum(suffix[0]) {
		return fmt.Errorf("invalid export runtime request id")
	}
	for i := 1; i < len(suffix); i++ {
		if !asciiAlnum(suffix[i]) && suffix[i] != '_' && suffix[i] != '-' {
			return fmt.Errorf("invalid export runtime request id")
		}
	}
	return nil
}

func asciiAlnum(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9')
}

func ResolveTTLSeconds(requested, max int) (int, error) {
	if max < MinTTLSeconds {
		return 0, fmt.Errorf("export max ttl must be at least %d seconds", MinTTLSeconds)
	}
	if requested == 0 {
		if DefaultTTLSeconds > max {
			return max, nil
		}
		return DefaultTTLSeconds, nil
	}
	if requested < MinTTLSeconds {
		return 0, fmt.Errorf("export ttl must be at least %d seconds", MinTTLSeconds)
	}
	if requested > max {
		return 0, fmt.Errorf("export ttl must not exceed %d seconds", max)
	}
	return requested, nil
}

func GenerateExportID() string {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return "export_" + hex.EncodeToString(b[:])
}

func GeneratePassword() string {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func NewPasswordVerifier(password string, salt []byte) (PasswordVerifier, error) {
	password = strings.TrimSpace(password)
	if password == "" {
		return PasswordVerifier{}, errors.New("password is required")
	}
	if len(salt) < 16 {
		return PasswordVerifier{}, errors.New("password verifier salt must be at least 16 bytes")
	}
	sum := sha256.Sum256(append(append([]byte(nil), salt...), []byte(password)...))
	return PasswordVerifier{
		Algorithm: passwordVerifierSHA256,
		Salt:      base64.RawURLEncoding.EncodeToString(salt),
		Hash:      base64.RawURLEncoding.EncodeToString(sum[:]),
	}, nil
}

func NewRandomPasswordVerifier(password string) (PasswordVerifier, error) {
	var salt [32]byte
	if _, err := io.ReadFull(rand.Reader, salt[:]); err != nil {
		return PasswordVerifier{}, err
	}
	return NewPasswordVerifier(password, salt[:])
}

func (verifier PasswordVerifier) Validate() error {
	if verifier.Algorithm != passwordVerifierSHA256 {
		return fmt.Errorf("unsupported password verifier algorithm %q", verifier.Algorithm)
	}
	salt, err := base64.RawURLEncoding.DecodeString(verifier.Salt)
	if err != nil || len(salt) < 16 {
		return errors.New("invalid password verifier salt")
	}
	hash, err := base64.RawURLEncoding.DecodeString(verifier.Hash)
	if err != nil || len(hash) != sha256.Size {
		return errors.New("invalid password verifier hash")
	}
	return nil
}

func (verifier PasswordVerifier) Verify(password string) bool {
	if verifier.Validate() != nil {
		return false
	}
	salt, _ := base64.RawURLEncoding.DecodeString(verifier.Salt)
	want, _ := base64.RawURLEncoding.DecodeString(verifier.Hash)
	sum := sha256.Sum256(append(append([]byte(nil), salt...), []byte(strings.TrimSpace(password))...))
	return subtle.ConstantTimeCompare(sum[:], want) == 1
}
