package sessionstate

import (
	"strings"
	"testing"
	"time"
)

func TestRestoreRunWriterGateExportSemantics(t *testing.T) {
	now := testNow()
	tests := []struct {
		name        string
		session     ExportSession
		wantAllowed bool
		wantFamily  ErrorFamily
	}{
		{name: "read write active live", session: exportFixture(AccessModeReadWrite, ExportStatusActive, now.Add(time.Hour)), wantFamily: ErrorFamilyActiveWriterSessions},
		{name: "read write revoking live", session: exportFixture(AccessModeReadWrite, ExportStatusRevoking, now.Add(time.Hour)), wantFamily: ErrorFamilyActiveWriterSessions},
		{name: "read write active expired", session: exportFixture(AccessModeReadWrite, ExportStatusActive, now.Add(-time.Minute)), wantFamily: ErrorFamilyStaleWriterSessionUncertain},
		{name: "read only active ignored", session: exportFixture(AccessModeReadOnly, ExportStatusActive, now.Add(time.Hour)), wantAllowed: true},
		{name: "read write revoking drained allows restore", session: exportDrainedFixture(now, ExportStatusRevoking), wantAllowed: true},
		{name: "read write expired drained allows restore", session: exportDrainedFixture(now, ExportStatusActive), wantAllowed: true},
		{name: "terminal revoked observed", session: exportTerminalFixture(now, ExportStatusRevoked), wantAllowed: true},
		{name: "terminal expired observed", session: exportTerminalFixture(now, ExportStatusExpired), wantAllowed: true},
		{name: "terminal failed observed", session: exportTerminalFixture(now, ExportStatusFailed), wantAllowed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := RestoreRunWriterGate(GateRequest{
				NamespaceID:    "ns_123",
				RepoID:         "repo_123",
				Now:            now,
				ExportSessions: []ExportSession{tt.session},
			})
			assertDecision(t, decision, tt.wantAllowed, tt.wantFamily)
		})
	}
}

func TestRestoreRunWriterGateRequiresTerminalExportEvidence(t *testing.T) {
	now := testNow()
	for _, status := range []ExportStatus{ExportStatusRevoked, ExportStatusExpired, ExportStatusFailed} {
		t.Run(string(status)+" without terminal observed", func(t *testing.T) {
			session := exportDrainedFixture(now, status)
			session.StatusReason = "terminal_reconciled"
			decision := RestoreRunWriterGate(GateRequest{NamespaceID: "ns_123", RepoID: "repo_123", Now: now, ExportSessions: []ExportSession{session}})
			assertDecision(t, decision, false, ErrorFamilyStaleWriterSessionUncertain)
		})
		t.Run(string(status)+" terminal observed", func(t *testing.T) {
			decision := RestoreRunWriterGate(GateRequest{NamespaceID: "ns_123", RepoID: "repo_123", Now: now, ExportSessions: []ExportSession{exportTerminalFixture(now, status)}})
			assertDecision(t, decision, true, "")
		})
	}

	failedWithoutReason := exportTerminalFixture(now, ExportStatusFailed)
	failedWithoutReason.StatusReason = ""
	decision := RestoreRunWriterGate(GateRequest{NamespaceID: "ns_123", RepoID: "repo_123", Now: now, ExportSessions: []ExportSession{failedWithoutReason}})
	assertDecision(t, decision, false, ErrorFamilyStaleWriterSessionUncertain)
}

func TestRestoreRunWriterGateStaleExportObservationFailsClosed(t *testing.T) {
	now := testNow()
	staleHeartbeat := exportFixture(AccessModeReadWrite, ExportStatusActive, now.Add(time.Hour))
	staleHeartbeat.GatewayHeartbeatExpiresAt = timePtr(now.Add(-time.Second))
	staleObservation := exportFixture(AccessModeReadWrite, ExportStatusActive, now.Add(time.Hour))
	staleObservation.LastObservedAt = nil
	notDrained := exportDrainedFixture(now, ExportStatusRevoking)
	notDrained.WriteDrainedAt = nil
	notDrained.GatewayHeartbeatExpiresAt = timePtr(now.Add(-time.Second))

	for _, session := range []ExportSession{staleHeartbeat, staleObservation, notDrained} {
		decision := RestoreRunWriterGate(GateRequest{
			NamespaceID:    "ns_123",
			RepoID:         "repo_123",
			Now:            now,
			ExportSessions: []ExportSession{session},
		})
		assertDecision(t, decision, false, ErrorFamilyStaleWriterSessionUncertain)
	}
}

func TestRestoreRunWriterGateAllowsExportDrainedWithExpiredHeartbeat(t *testing.T) {
	now := testNow()
	tests := []struct {
		name    string
		session ExportSession
	}{
		{
			name: "revoking drained heartbeat expired",
			session: func() ExportSession {
				session := exportDrainedFixture(now, ExportStatusRevoking)
				session.ExpiresAt = now.Add(time.Hour)
				session.GatewayHeartbeatExpiresAt = timePtr(now.Add(-time.Second))
				return session
			}(),
		},
		{
			name: "active expired drained heartbeat expired",
			session: func() ExportSession {
				session := exportDrainedFixture(now, ExportStatusActive)
				session.GatewayHeartbeatExpiresAt = timePtr(now.Add(-time.Second))
				return session
			}(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			restore := RestoreRunWriterGate(GateRequest{
				NamespaceID:    "ns_123",
				RepoID:         "repo_123",
				Now:            now,
				ExportSessions: []ExportSession{tt.session},
			})
			assertDecision(t, restore, true, "")
		})
	}
}

func TestRestoreRunWriterGateRejectsWriteDrainedAtZeroWithExpiredHeartbeat(t *testing.T) {
	now := testNow()
	zero := time.Time{}
	tests := []struct {
		name    string
		session ExportSession
	}{
		{
			name: "revoking",
			session: func() ExportSession {
				session := exportDrainedFixture(now, ExportStatusRevoking)
				session.ExpiresAt = now.Add(time.Hour)
				session.GatewayHeartbeatExpiresAt = timePtr(now.Add(-time.Second))
				session.WriteDrainedAt = &zero
				return session
			}(),
		},
		{
			name: "active expired",
			session: func() ExportSession {
				session := exportDrainedFixture(now, ExportStatusActive)
				session.GatewayHeartbeatExpiresAt = timePtr(now.Add(-time.Second))
				session.WriteDrainedAt = &zero
				return session
			}(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := RestoreRunWriterGate(GateRequest{
				NamespaceID:    "ns_123",
				RepoID:         "repo_123",
				Now:            now,
				ExportSessions: []ExportSession{tt.session},
			})
			assertDecision(t, decision, false, ErrorFamilyStaleWriterSessionUncertain)
		})
	}
}

func TestLifecycleDrainGateBlocksDrainedNonTerminalExportWithExpiredHeartbeat(t *testing.T) {
	now := testNow()
	session := exportDrainedFixture(now, ExportStatusRevoking)
	session.ExpiresAt = now.Add(time.Hour)
	session.GatewayHeartbeatExpiresAt = timePtr(now.Add(-time.Second))

	decision := LifecycleDrainGate(GateRequest{
		NamespaceID:    "ns_123",
		RepoID:         "repo_123",
		Now:            now,
		ExportSessions: []ExportSession{session},
	})
	assertDecision(t, decision, false, ErrorFamilyStaleSessionsBlockLifecycle)
}

func TestRestoreRunWriterGateMountSemantics(t *testing.T) {
	now := testNow()
	tests := []struct {
		name        string
		mount       WorkloadMountBinding
		wantAllowed bool
		wantFamily  ErrorFamily
	}{
		{name: "read write issued live", mount: mountFixture(false, MountStatusIssued, now.Add(time.Hour)), wantFamily: ErrorFamilyActiveWriterSessions},
		{name: "read write active live", mount: mountFixture(false, MountStatusActive, now.Add(time.Hour)), wantFamily: ErrorFamilyActiveWriterSessions},
		{name: "read write releasing expired", mount: mountFixture(false, MountStatusReleasing, now.Add(-time.Minute)), wantFamily: ErrorFamilyStaleWriterSessionUncertain},
		{name: "read only active ignored", mount: mountFixture(true, MountStatusActive, now.Add(time.Hour)), wantAllowed: true},
		{name: "terminal released without evidence stale", mount: mountFixture(false, MountStatusReleased, now.Add(-time.Hour)), wantFamily: ErrorFamilyStaleWriterSessionUncertain},
		{name: "terminal revoked without evidence stale", mount: mountFixture(false, MountStatusRevoked, now.Add(-time.Hour)), wantFamily: ErrorFamilyStaleWriterSessionUncertain},
		{name: "terminal expired without evidence stale", mount: mountFixture(false, MountStatusExpired, now.Add(-time.Hour)), wantFamily: ErrorFamilyStaleWriterSessionUncertain},
		{name: "terminal failed without evidence stale", mount: mountFixture(false, MountStatusFailed, now.Add(time.Hour)), wantFamily: ErrorFamilyStaleWriterSessionUncertain},
		{name: "terminal observed only does not prove writer drained", mount: mountWithEvidence(mountFixture(false, MountStatusFailed, now.Add(-time.Hour)), nil, nil, timePtr(now.Add(-time.Minute))), wantFamily: ErrorFamilyStaleWriterSessionUncertain},
		{name: "terminal confirmed unmounted proves writer drained", mount: mountWithEvidence(mountFixture(false, MountStatusReleased, now.Add(-time.Hour)), timePtr(now.Add(-time.Minute)), nil, nil), wantAllowed: true},
		{name: "released unable to write proves writer drained", mount: mountWithEvidence(mountFixture(false, MountStatusReleased, now.Add(-time.Hour)), nil, timePtr(now.Add(-time.Minute)), nil), wantAllowed: true},
		{name: "failed unable to write only remains stale", mount: mountWithEvidence(mountFixture(false, MountStatusFailed, now.Add(-time.Hour)), nil, timePtr(now.Add(-time.Minute)), nil), wantFamily: ErrorFamilyStaleWriterSessionUncertain},
		{name: "read only terminal without evidence ignored", mount: mountFixture(true, MountStatusFailed, now.Add(time.Hour)), wantAllowed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := RestoreRunWriterGate(GateRequest{
				NamespaceID: "ns_123",
				RepoID:      "repo_123",
				Now:         now,
				Mounts:      []WorkloadMountBinding{tt.mount},
			})
			assertDecision(t, decision, tt.wantAllowed, tt.wantFamily)
		})
	}
}

func TestRestoreRunWriterGateTerminalMountEvidenceMatrix(t *testing.T) {
	now := testNow()
	for _, status := range []MountStatus{MountStatusReleased, MountStatusRevoked, MountStatusExpired, MountStatusFailed} {
		t.Run(string(status)+" without evidence", func(t *testing.T) {
			decision := RestoreRunWriterGate(GateRequest{NamespaceID: "ns_123", RepoID: "repo_123", Now: now, Mounts: []WorkloadMountBinding{mountFixture(false, status, now.Add(-time.Hour))}})
			assertDecision(t, decision, false, ErrorFamilyStaleWriterSessionUncertain)
		})
		t.Run(string(status)+" confirmed unmounted", func(t *testing.T) {
			mount := mountWithEvidence(mountFixture(false, status, now.Add(-time.Hour)), timePtr(now.Add(-time.Minute)), nil, nil)
			decision := RestoreRunWriterGate(GateRequest{NamespaceID: "ns_123", RepoID: "repo_123", Now: now, Mounts: []WorkloadMountBinding{mount}})
			assertDecision(t, decision, true, "")
		})
		t.Run(string(status)+" unable to write", func(t *testing.T) {
			mount := mountWithEvidence(mountFixture(false, status, now.Add(-time.Hour)), nil, timePtr(now.Add(-time.Minute)), nil)
			decision := RestoreRunWriterGate(GateRequest{NamespaceID: "ns_123", RepoID: "repo_123", Now: now, Mounts: []WorkloadMountBinding{mount}})
			switch status {
			case MountStatusReleased, MountStatusRevoked:
				assertDecision(t, decision, true, "")
			case MountStatusExpired, MountStatusFailed:
				assertDecision(t, decision, false, ErrorFamilyStaleWriterSessionUncertain)
			default:
				t.Fatalf("unhandled terminal mount status %s", status)
			}
		})
		t.Run(string(status)+" terminal observed only", func(t *testing.T) {
			mount := mountWithEvidence(mountFixture(false, status, now.Add(-time.Hour)), nil, nil, timePtr(now.Add(-time.Minute)))
			decision := RestoreRunWriterGate(GateRequest{NamespaceID: "ns_123", RepoID: "repo_123", Now: now, Mounts: []WorkloadMountBinding{mount}})
			assertDecision(t, decision, false, ErrorFamilyStaleWriterSessionUncertain)
		})
	}
}

func TestLifecycleDrainGateBlocksAnyNonTerminalAccess(t *testing.T) {
	now := testNow()
	tests := []struct {
		name       string
		exports    []ExportSession
		mounts     []WorkloadMountBinding
		wantFamily ErrorFamily
	}{
		{name: "read only export live", exports: []ExportSession{exportFixture(AccessModeReadOnly, ExportStatusActive, now.Add(time.Hour))}, wantFamily: ErrorFamilyActiveSessionsBlockLifecycle},
		{name: "read write export expired but freshly observed", exports: []ExportSession{exportFixture(AccessModeReadWrite, ExportStatusRevoking, now.Add(-time.Minute))}, wantFamily: ErrorFamilyActiveSessionsBlockLifecycle},
		{name: "read write export drained still blocks lifecycle", exports: []ExportSession{exportDrainedFixture(now, ExportStatusRevoking)}, wantFamily: ErrorFamilyActiveSessionsBlockLifecycle},
		{name: "read only export stale observation", exports: []ExportSession{exportStaleObservationFixture(now, AccessModeReadOnly)}, wantFamily: ErrorFamilyStaleSessionsBlockLifecycle},
		{name: "read only mount live", mounts: []WorkloadMountBinding{mountFixture(true, MountStatusPending, now.Add(time.Hour))}, wantFamily: ErrorFamilyActiveSessionsBlockLifecycle},
		{name: "read write mount stale", mounts: []WorkloadMountBinding{mountFixture(false, MountStatusReleasing, now.Add(-time.Minute))}, wantFamily: ErrorFamilyStaleSessionsBlockLifecycle},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := LifecycleDrainGate(GateRequest{
				NamespaceID:    "ns_123",
				RepoID:         "repo_123",
				Now:            now,
				ExportSessions: tt.exports,
				Mounts:         tt.mounts,
			})
			assertDecision(t, decision, false, tt.wantFamily)
		})
	}
}

func TestLifecycleDrainGateRequiresTerminalMountNonAccessingEvidence(t *testing.T) {
	now := testNow()
	for _, status := range []MountStatus{MountStatusReleased, MountStatusRevoked, MountStatusExpired, MountStatusFailed} {
		t.Run(string(status)+" without evidence", func(t *testing.T) {
			decision := LifecycleDrainGate(GateRequest{NamespaceID: "ns_123", RepoID: "repo_123", Now: now, Mounts: []WorkloadMountBinding{mountFixture(false, status, now.Add(-time.Hour))}})
			assertDecision(t, decision, false, ErrorFamilyStaleSessionsBlockLifecycle)
		})
		t.Run(string(status)+" terminal observed only", func(t *testing.T) {
			mount := mountWithEvidence(mountFixture(false, status, now.Add(-time.Hour)), nil, nil, timePtr(now.Add(-time.Minute)))
			decision := LifecycleDrainGate(GateRequest{NamespaceID: "ns_123", RepoID: "repo_123", Now: now, Mounts: []WorkloadMountBinding{mount}})
			assertDecision(t, decision, false, ErrorFamilyStaleSessionsBlockLifecycle)
		})
		t.Run(string(status)+" unable to write only", func(t *testing.T) {
			mount := mountWithEvidence(mountFixture(false, status, now.Add(-time.Hour)), nil, timePtr(now.Add(-time.Minute)), nil)
			decision := LifecycleDrainGate(GateRequest{NamespaceID: "ns_123", RepoID: "repo_123", Now: now, Mounts: []WorkloadMountBinding{mount}})
			assertDecision(t, decision, false, ErrorFamilyStaleSessionsBlockLifecycle)
		})
		t.Run(string(status)+" confirmed unmounted", func(t *testing.T) {
			mount := mountWithEvidence(mountFixture(false, status, now.Add(-time.Hour)), timePtr(now.Add(-time.Minute)), nil, nil)
			decision := LifecycleDrainGate(GateRequest{NamespaceID: "ns_123", RepoID: "repo_123", Now: now, Mounts: []WorkloadMountBinding{mount}})
			assertDecision(t, decision, true, "")
		})
	}

	readOnlyConfirmed := mountWithEvidence(mountFixture(true, MountStatusReleased, now.Add(-time.Hour)), timePtr(now.Add(-time.Minute)), nil, nil)
	decision := LifecycleDrainGate(GateRequest{
		NamespaceID:    "ns_123",
		RepoID:         "repo_123",
		Now:            now,
		ExportSessions: []ExportSession{exportTerminalFixture(now, ExportStatusRevoked)},
		Mounts:         []WorkloadMountBinding{readOnlyConfirmed},
	})
	assertDecision(t, decision, true, "")
}

func TestLifecycleDrainGateRequiresTerminalExportNonAccessingEvidence(t *testing.T) {
	now := testNow()
	for _, status := range []ExportStatus{ExportStatusRevoked, ExportStatusExpired, ExportStatusFailed} {
		t.Run(string(status)+" without terminal evidence", func(t *testing.T) {
			session := exportDrainedFixture(now, status)
			session.StatusReason = "terminal_reconciled"
			decision := LifecycleDrainGate(GateRequest{NamespaceID: "ns_123", RepoID: "repo_123", Now: now, ExportSessions: []ExportSession{session}})
			assertDecision(t, decision, false, ErrorFamilyStaleSessionsBlockLifecycle)
		})
		t.Run(string(status)+" terminal observed", func(t *testing.T) {
			decision := LifecycleDrainGate(GateRequest{NamespaceID: "ns_123", RepoID: "repo_123", Now: now, ExportSessions: []ExportSession{exportTerminalFixture(now, status)}})
			assertDecision(t, decision, true, "")
		})
	}

	failedWithoutReason := exportTerminalFixture(now, ExportStatusFailed)
	failedWithoutReason.StatusReason = ""
	decision := LifecycleDrainGate(GateRequest{NamespaceID: "ns_123", RepoID: "repo_123", Now: now, ExportSessions: []ExportSession{failedWithoutReason}})
	assertDecision(t, decision, false, ErrorFamilyStaleSessionsBlockLifecycle)
}

func TestGateAggregatesActiveBeforeStale(t *testing.T) {
	now := testNow()
	restore := RestoreRunWriterGate(GateRequest{
		NamespaceID: "ns_123",
		RepoID:      "repo_123",
		Now:         now,
		ExportSessions: []ExportSession{
			exportFixture(AccessModeReadWrite, ExportStatusActive, now.Add(-time.Minute)),
			exportFixture(AccessModeReadWrite, ExportStatusActive, now.Add(time.Hour)),
		},
	})
	assertDecision(t, restore, false, ErrorFamilyActiveWriterSessions)

	lifecycle := LifecycleDrainGate(GateRequest{
		NamespaceID: "ns_123",
		RepoID:      "repo_123",
		Now:         now,
		Mounts: []WorkloadMountBinding{
			mountFixture(false, MountStatusActive, now.Add(-time.Minute)),
			mountFixture(false, MountStatusActive, now.Add(time.Hour)),
		},
	})
	assertDecision(t, lifecycle, false, ErrorFamilyActiveSessionsBlockLifecycle)
}

func TestGateIgnoresOtherRepoSessions(t *testing.T) {
	now := testNow()
	otherRepo := exportFixture(AccessModeReadWrite, ExportStatusActive, now.Add(time.Hour))
	otherRepo.RepoID = "repo_other"
	otherRepoMount := mountFixture(false, MountStatusActive, now.Add(time.Hour))
	otherRepoMount.RepoID = "repo_other"
	otherRepoMount.NamespaceID = "ns_other"

	restore := RestoreRunWriterGate(GateRequest{
		NamespaceID:    "ns_123",
		RepoID:         "repo_123",
		Now:            now,
		ExportSessions: []ExportSession{otherRepo},
		Mounts:         []WorkloadMountBinding{otherRepoMount},
	})
	assertDecision(t, restore, true, "")

	lifecycle := LifecycleDrainGate(GateRequest{
		NamespaceID:    "ns_123",
		RepoID:         "repo_123",
		Now:            now,
		ExportSessions: []ExportSession{otherRepo},
		Mounts:         []WorkloadMountBinding{otherRepoMount},
	})
	assertDecision(t, lifecycle, true, "")
}

func TestGateFailsClosedForSameRepoNamespaceMismatch(t *testing.T) {
	now := testNow()
	badExport := exportFixture(AccessModeReadWrite, ExportStatusActive, now.Add(time.Hour))
	badExport.NamespaceID = "ns_other"
	badMount := mountFixture(false, MountStatusActive, now.Add(time.Hour))
	badMount.NamespaceID = "ns_other"

	for _, decision := range []Decision{
		RestoreRunWriterGate(GateRequest{NamespaceID: "ns_123", RepoID: "repo_123", Now: now, ExportSessions: []ExportSession{badExport}}),
		LifecycleDrainGate(GateRequest{NamespaceID: "ns_123", RepoID: "repo_123", Now: now, Mounts: []WorkloadMountBinding{badMount}}),
	} {
		assertDecision(t, decision, false, ErrorFamilyInternalError)
		rendered := strings.ToLower(decision.ErrorFamily.String() + " " + decision.Reason + " " + decision.BlockingKind)
		for _, leaked := range []string{"ns_other", "repo_123", "active", "/srv", "secret"} {
			if strings.Contains(rendered, leaked) {
				t.Fatalf("decision leaked %q: %#v", leaked, decision)
			}
		}
	}
}

func TestGateInvalidSameRepoSessionFailsClosedWithoutSecretLeak(t *testing.T) {
	now := testNow()
	badExport := exportFixture(AccessModeReadWrite, ExportStatus("active/secret"), now.Add(time.Hour))
	badMount := mountFixture(false, MountStatusActive, time.Time{})

	for _, decision := range []Decision{
		RestoreRunWriterGate(GateRequest{NamespaceID: "ns_123", RepoID: "repo_123", Now: now, ExportSessions: []ExportSession{badExport}}),
		LifecycleDrainGate(GateRequest{NamespaceID: "ns_123", RepoID: "repo_123", Now: now, Mounts: []WorkloadMountBinding{badMount}}),
	} {
		assertDecision(t, decision, false, ErrorFamilyInternalError)
		rendered := strings.ToLower(decision.ErrorFamily.String() + " " + decision.Reason + " " + decision.BlockingKind)
		for _, leaked := range []string{"secret", "active/secret", "/srv", "token"} {
			if strings.Contains(rendered, leaked) {
				t.Fatalf("decision leaked %q: %#v", leaked, decision)
			}
		}
	}
}

func TestGateInvalidTerminalExportActiveCountsFailsClosed(t *testing.T) {
	now := testNow()
	badExport := exportTerminalFixture(now, ExportStatusRevoked)
	badExport.ActiveRequestCount = 1

	for _, decision := range []Decision{
		RestoreRunWriterGate(GateRequest{NamespaceID: "ns_123", RepoID: "repo_123", Now: now, ExportSessions: []ExportSession{badExport}}),
		LifecycleDrainGate(GateRequest{NamespaceID: "ns_123", RepoID: "repo_123", Now: now, ExportSessions: []ExportSession{badExport}}),
	} {
		assertDecision(t, decision, false, ErrorFamilyInternalError)
		if decision.Reason != "invalid stored session state" {
			t.Fatalf("reason = %q, want invalid stored session state", decision.Reason)
		}
	}
}

func TestGateInvalidTargetFailsClosed(t *testing.T) {
	decision := RestoreRunWriterGate(GateRequest{NamespaceID: "namespace", RepoID: "repo_123", Now: testNow()})
	assertDecision(t, decision, false, ErrorFamilyInternalError)

	decision = LifecycleDrainGate(GateRequest{NamespaceID: "ns_123", RepoID: "repo_123"})
	assertDecision(t, decision, false, ErrorFamilyInternalError)
}

func assertDecision(t *testing.T, decision Decision, wantAllowed bool, wantFamily ErrorFamily) {
	t.Helper()
	if decision.Allowed != wantAllowed || decision.ErrorFamily != wantFamily {
		t.Fatalf("decision = %#v, want allowed=%v family=%s", decision, wantAllowed, wantFamily)
	}
	if wantAllowed && decision.Action != ActionAllow {
		t.Fatalf("action = %s, want allow", decision.Action)
	}
	if !wantAllowed && decision.Action != ActionDeny {
		t.Fatalf("action = %s, want deny", decision.Action)
	}
}

func exportFixture(mode AccessMode, status ExportStatus, expiresAt time.Time) ExportSession {
	now := testNow()
	return ExportSession{
		ID:                        "export_123",
		NamespaceID:               "ns_123",
		RepoID:                    "repo_123",
		Mode:                      mode,
		Status:                    status,
		ExpiresAt:                 expiresAt,
		ActiveRequestCount:        1,
		ActiveWriteCount:          1,
		LastObservedAt:            timePtr(now.Add(-time.Second)),
		LastGatewayHeartbeatAt:    timePtr(now.Add(-time.Second)),
		GatewayHeartbeatExpiresAt: timePtr(now.Add(time.Minute)),
	}
}

func exportDrainedFixture(now time.Time, status ExportStatus) ExportSession {
	session := exportFixture(AccessModeReadWrite, status, now.Add(-time.Minute))
	session.ActiveRequestCount = 0
	session.ActiveWriteCount = 0
	session.WriteDrainedAt = timePtr(now.Add(-time.Second))
	return session
}

func exportTerminalFixture(now time.Time, status ExportStatus) ExportSession {
	session := exportDrainedFixture(now, status)
	session.TerminalObservedAt = timePtr(now.Add(-time.Second))
	if status == ExportStatusFailed {
		session.StatusReason = "terminal_reconciled"
	}
	return session
}

func exportStaleObservationFixture(now time.Time, mode AccessMode) ExportSession {
	session := exportFixture(mode, ExportStatusActive, now.Add(time.Hour))
	session.LastObservedAt = timePtr(now.Add(-10 * time.Minute))
	session.GatewayHeartbeatExpiresAt = timePtr(now.Add(-time.Minute))
	return session
}

func mountFixture(readOnly bool, status MountStatus, leaseExpiresAt time.Time) WorkloadMountBinding {
	return WorkloadMountBinding{
		ID:             "wmb_123",
		NamespaceID:    "ns_123",
		RepoID:         "repo_123",
		ReadOnly:       readOnly,
		Status:         status,
		LeaseExpiresAt: leaseExpiresAt,
	}
}

func mountWithEvidence(mount WorkloadMountBinding, confirmedUnmountedAt, unableToWriteAt, terminalObservedAt *time.Time) WorkloadMountBinding {
	mount.ConfirmedUnmountedAt = confirmedUnmountedAt
	mount.UnableToWriteAt = unableToWriteAt
	mount.TerminalObservedAt = terminalObservedAt
	return mount
}

func testNow() time.Time {
	return time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
}

func timePtr(t time.Time) *time.Time {
	return &t
}
