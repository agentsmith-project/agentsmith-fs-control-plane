package sessionstate

import (
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/pathresolver"
)

type AccessMode string

const (
	AccessModeReadOnly  AccessMode = "read_only"
	AccessModeReadWrite AccessMode = "read_write"
)

type ExportStatus string

const (
	ExportStatusActive   ExportStatus = "active"
	ExportStatusRevoking ExportStatus = "revoking"
	ExportStatusRevoked  ExportStatus = "revoked"
	ExportStatusExpired  ExportStatus = "expired"
	ExportStatusFailed   ExportStatus = "failed"
)

type MountStatus string

const (
	MountStatusIssued    MountStatus = "issued"
	MountStatusPending   MountStatus = "pending"
	MountStatusActive    MountStatus = "active"
	MountStatusReleasing MountStatus = "releasing"
	MountStatusReleased  MountStatus = "released"
	MountStatusRevoked   MountStatus = "revoked"
	MountStatusExpired   MountStatus = "expired"
	MountStatusFailed    MountStatus = "failed"
)

type ErrorFamily string

const (
	ErrorFamilyInternalError                ErrorFamily = "INTERNAL_ERROR"
	ErrorFamilyActiveWriterSessions         ErrorFamily = "ACTIVE_WRITER_SESSIONS"
	ErrorFamilyStaleWriterSessionUncertain  ErrorFamily = "STALE_WRITER_SESSION_UNCERTAIN"
	ErrorFamilyActiveSessionsBlockLifecycle ErrorFamily = "ACTIVE_SESSIONS_BLOCK_LIFECYCLE"
	ErrorFamilyStaleSessionsBlockLifecycle  ErrorFamily = "STALE_SESSION_BLOCKS_LIFECYCLE"
)

func (family ErrorFamily) String() string {
	return string(family)
}

type Action string

const (
	ActionAllow Action = "allow"
	ActionDeny  Action = "deny"
)

type ExportSession struct {
	ID                        string
	NamespaceID               string
	RepoID                    string
	Mode                      AccessMode
	Status                    ExportStatus
	ExpiresAt                 time.Time
	ActiveRequestCount        int
	ActiveWriteCount          int
	LastObservedAt            *time.Time
	LastGatewayHeartbeatAt    *time.Time
	GatewayHeartbeatExpiresAt *time.Time
	WriteDrainedAt            *time.Time
	TerminalObservedAt        *time.Time
	StatusReason              string
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
}

type WorkloadMountBinding struct {
	ID             string
	NamespaceID    string
	RepoID         string
	ReadOnly       bool
	Status         MountStatus
	LeaseExpiresAt time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type GateRequest struct {
	NamespaceID    string
	RepoID         string
	Now            time.Time
	ExportSessions []ExportSession
	Mounts         []WorkloadMountBinding
}

type Decision struct {
	Allowed      bool
	Action       Action
	ErrorFamily  ErrorFamily
	Reason       string
	BlockingKind string
}

func RestoreRunWriterGate(request GateRequest) Decision {
	return evaluate(request, gateRestoreRunWriter)
}

func LifecycleDrainGate(request GateRequest) Decision {
	return evaluate(request, gateLifecycleDrain)
}

type gateKind string

const (
	gateRestoreRunWriter gateKind = "restore_run_writer"
	gateLifecycleDrain   gateKind = "lifecycle_drain"
)

type blockerClass int

const (
	blockerNone blockerClass = iota
	blockerActive
	blockerStale
)

func evaluate(request GateRequest, kind gateKind) Decision {
	if err := validateTarget(request); err != nil {
		return deny(ErrorFamilyInternalError, "invalid session gate request", "")
	}

	active := false
	stale := false
	activeKind := ""
	staleKind := ""

	for _, session := range request.ExportSessions {
		if session.RepoID != request.RepoID {
			continue
		}
		if err := validateExport(session); err != nil {
			return deny(ErrorFamilyInternalError, "invalid stored session state", "")
		}
		if session.NamespaceID != request.NamespaceID {
			return deny(ErrorFamilyInternalError, "invalid stored session state", "")
		}
		blocker := exportBlocker(kind, session, request.Now)
		switch blocker {
		case blockerActive:
			active = true
			activeKind = "export"
		case blockerStale:
			stale = true
			staleKind = "export"
		}
	}

	for _, mount := range request.Mounts {
		if mount.RepoID != request.RepoID {
			continue
		}
		if err := validateMount(mount); err != nil {
			return deny(ErrorFamilyInternalError, "invalid stored session state", "")
		}
		if mount.NamespaceID != request.NamespaceID {
			return deny(ErrorFamilyInternalError, "invalid stored session state", "")
		}
		blocker := mountBlocker(kind, mount, request.Now)
		switch blocker {
		case blockerActive:
			active = true
			activeKind = "workload_mount"
		case blockerStale:
			stale = true
			staleKind = "workload_mount"
		}
	}

	if active {
		if kind == gateLifecycleDrain {
			return deny(ErrorFamilyActiveSessionsBlockLifecycle, "active session blocks lifecycle drain", activeKind)
		}
		return deny(ErrorFamilyActiveWriterSessions, "active writer session blocks restore-run", activeKind)
	}
	if stale {
		if kind == gateLifecycleDrain {
			return deny(ErrorFamilyStaleSessionsBlockLifecycle, "stale session blocks lifecycle drain", staleKind)
		}
		return deny(ErrorFamilyStaleWriterSessionUncertain, "stale writer session blocks restore-run", staleKind)
	}

	return allow()
}

func validateTarget(request GateRequest) error {
	if err := pathresolver.ValidateID(pathresolver.NamespaceID, request.NamespaceID); err != nil {
		return err
	}
	if err := pathresolver.ValidateID(pathresolver.RepoID, request.RepoID); err != nil {
		return err
	}
	if request.Now.IsZero() {
		return errInvalidTime
	}
	return nil
}

func validateExport(session ExportSession) error {
	if err := pathresolver.ValidateID(pathresolver.ExportID, session.ID); err != nil {
		return err
	}
	if err := pathresolver.ValidateID(pathresolver.NamespaceID, session.NamespaceID); err != nil {
		return err
	}
	if err := pathresolver.ValidateID(pathresolver.RepoID, session.RepoID); err != nil {
		return err
	}
	if !session.Mode.valid() || !session.Status.valid() || session.ExpiresAt.IsZero() {
		return errInvalidSession
	}
	if session.ActiveRequestCount < 0 || session.ActiveWriteCount < 0 || session.ActiveWriteCount > session.ActiveRequestCount {
		return errInvalidSession
	}
	return nil
}

func validateMount(mount WorkloadMountBinding) error {
	if err := pathresolver.ValidateID(pathresolver.WorkloadMountBindingID, mount.ID); err != nil {
		return err
	}
	if err := pathresolver.ValidateID(pathresolver.NamespaceID, mount.NamespaceID); err != nil {
		return err
	}
	if err := pathresolver.ValidateID(pathresolver.RepoID, mount.RepoID); err != nil {
		return err
	}
	if !mount.Status.valid() || mount.LeaseExpiresAt.IsZero() {
		return errInvalidSession
	}
	return nil
}

var (
	errInvalidTime    = sessionError("invalid time")
	errInvalidSession = sessionError("invalid session")
)

type sessionError string

func (err sessionError) Error() string {
	return string(err)
}

func exportBlocker(kind gateKind, session ExportSession, now time.Time) blockerClass {
	if exportTerminal(session.Status) {
		return blockerNone
	}
	if kind == gateRestoreRunWriter && session.Mode != AccessModeReadWrite {
		return blockerNone
	}
	if exportObservationStale(session, now) {
		return blockerStale
	}
	if kind == gateLifecycleDrain {
		return blockerActive
	}
	if kind == gateRestoreRunWriter && exportWriterDrained(session, now) {
		return blockerNone
	}
	if session.ExpiresAt.After(now) {
		return blockerActive
	}
	return blockerStale
}

func exportObservationStale(session ExportSession, now time.Time) bool {
	if session.LastObservedAt == nil || session.GatewayHeartbeatExpiresAt == nil {
		return true
	}
	return !session.GatewayHeartbeatExpiresAt.After(now)
}

func exportWriterDrained(session ExportSession, now time.Time) bool {
	if session.Mode != AccessModeReadWrite || session.ActiveWriteCount != 0 || session.WriteDrainedAt == nil {
		return false
	}
	if session.ActiveRequestCount < 0 || session.ActiveWriteCount < 0 {
		return false
	}
	if session.Status == ExportStatusRevoking || !session.ExpiresAt.After(now) {
		return true
	}
	return false
}

func mountBlocker(kind gateKind, mount WorkloadMountBinding, now time.Time) blockerClass {
	if mountTerminal(mount.Status) {
		return blockerNone
	}
	if kind == gateRestoreRunWriter && mount.ReadOnly {
		return blockerNone
	}
	if mount.LeaseExpiresAt.After(now) {
		return blockerActive
	}
	return blockerStale
}

func exportTerminal(status ExportStatus) bool {
	switch status {
	case ExportStatusRevoked, ExportStatusExpired, ExportStatusFailed:
		return true
	default:
		return false
	}
}

func mountTerminal(status MountStatus) bool {
	switch status {
	case MountStatusReleased, MountStatusRevoked, MountStatusExpired, MountStatusFailed:
		return true
	default:
		return false
	}
}

func (mode AccessMode) valid() bool {
	switch mode {
	case AccessModeReadOnly, AccessModeReadWrite:
		return true
	default:
		return false
	}
}

func (status ExportStatus) valid() bool {
	switch status {
	case ExportStatusActive, ExportStatusRevoking, ExportStatusRevoked, ExportStatusExpired, ExportStatusFailed:
		return true
	default:
		return false
	}
}

func (status MountStatus) valid() bool {
	switch status {
	case MountStatusIssued,
		MountStatusPending,
		MountStatusActive,
		MountStatusReleasing,
		MountStatusReleased,
		MountStatusRevoked,
		MountStatusExpired,
		MountStatusFailed:
		return true
	default:
		return false
	}
}

func allow() Decision {
	return Decision{Allowed: true, Action: ActionAllow}
}

func deny(family ErrorFamily, reason string, blockingKind string) Decision {
	return Decision{
		Allowed:      false,
		Action:       ActionDeny,
		ErrorFamily:  family,
		Reason:       reason,
		BlockingKind: blockingKind,
	}
}
