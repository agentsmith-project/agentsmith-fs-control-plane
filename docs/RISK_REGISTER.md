# Risk Register

Status: active GA implementation-baseline risk register.

GA risk closure is objective and repo-local. A GA-blocking risk is closed only
when its mitigation is represented by scripts, tests, contracts, schemas,
OpenAPI artifacts, runbooks, or docs covered by:

```bash
bash scripts/verify-ga-release.sh
```

Severity uses `critical`, `high`, `medium`, and `low`. `GA blocker` means the
risk fails the GA release gate when the automated evidence is missing or
failing. Risks that can cause credential exposure, tenant isolation failure,
user data loss, irrecoverable operation ambiguity, or caller-visible contract
break are non-waivable for GA.

| ID | Risk | Severity | GA Blocker | Owner Role | Status | Decision | Automated Evidence/Check | Mitigation |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| R-001 | Narrative API contracts are implemented before schemas, OpenAPI, and stable errors are frozen | high | yes | AFSCP maintainer | auto_verified | closed by schema/OpenAPI/contract parity checks | `scripts/verify-ga-release.sh`, `api/schemas/afscp-internal-v1.schema.json`, `api/openapi/internal-v1.openapi.yaml`, `cmd/afscp-contract-verify`, API tests | Keep schema/OpenAPI parity with narrative contracts and standard envelopes before handlers |
| R-002 | JVS runner behavior differs from AFSCP assumptions | high | yes | AFSCP maintainer | auto_verified | closed by v0.4.9 repo-local pin and runner contract evidence | `scripts/verify-ga-release.sh`, `docs/JVS_PIN_EVIDENCE_2026-05-12-v0.4.9.md`, `docs/contracts/jvs-runner-contract-v1.md`, JVS runner tests | JVS v0.4.9 release binary/checksum pinned; runner contract covers CWD/argv rules and strict runtime repair for stale repository mutation locks |
| R-003 | WebDAV gateway misses path, method, destination, symlink, credential revocation, or stale runtime request recovery edge cases | critical | yes | AFSCP maintainer, security owner | auto_verified | closed by gateway/session/reconcile tests and contract guards | `scripts/verify-ga-release.sh`, `docs/adr/0010-webdav-export-gateway.md`, `docs/contracts/export-access-webdav-v1.md`, WebDAV gateway, resolver, session, reconcile, and audit redaction tests | AFSCP-controlled gateway contract, durable runtime request ledger, stale non-terminal request recovery before terminal reconcile, shared resolver corpus, method-level tests, credential lifecycle guards |
| R-004 | Workload orchestrator still consumes caller-provided JuiceFS metadata or exposes Secret refs outside orchestrator role | critical | yes | AFSCP maintainer | auto_verified | closed by mount-plan contract and secrecy tests | `scripts/verify-ga-release.sh`, `docs/adr/0011-workload-orchestrator-contract.md`, `docs/contracts/workload-mount-binding-v1.md`, workload mount/orchestrator tests | Adopt orchestrator-only mount plan with payload-only subdir, heartbeat/release/revoke, and Secret RBAC boundaries |
| R-005 | Restore-run races active or stale read-write sessions | critical | yes | AFSCP maintainer, operations owner | auto_verified | closed by writer-session fence and drain tests | `scripts/verify-ga-release.sh`, `docs/adr/0012-path-resolver-and-fences.md`, `docs/contracts/operation-state-machine-v1.md`, fence/session/recovery/restore rejection tests | Writer-session fence contract, active session accounting, stale lease reconciliation, fail-closed restore |
| R-006 | Operation recovery after crash is ambiguous | high | yes | AFSCP maintainer, operations owner | auto_verified | closed by recovery model, worker, and runbook artifacts | `scripts/verify-ga-release.sh`, `docs/adr/0007-operation-store-and-audit-outbox.md`, `docs/contracts/operation-state-machine-v1.md`, operation/recovery/runbook tests and docs | Phase-specific recovery matrix, external IDs, doctor verification, lifecycle fence recovery, operator intervention runbooks |
| R-007 | Audit events are incomplete, lossy, or leak secrets | high | yes | Security owner, operations owner | auto_verified | closed by audit taxonomy, outbox, and redaction tests | `scripts/verify-ga-release.sh`, `docs/OPERATIONS_AND_AUDIT.md`, `internal/audit/event_test.go`, audit outbox tests | Append-only/outbox audit semantics, retention, replay, and redaction guards |
| R-008 | Namespace disable and policy changes do not define active session behavior | high | yes | AFSCP maintainer | auto_verified | closed by namespace policy contracts and tests | `scripts/verify-ga-release.sh`, `docs/contracts/namespace-volume-binding-v1.md`, namespace/resource/session tests | Freeze disable semantics for new operations, existing exports, mounts, and operator actions |
| R-009 | Break-glass direct mount becomes an ordinary access path | critical | yes | Security owner, operations owner | auto_verified | closed by product-boundary docs and contract guards | `scripts/verify-ga-release.sh`, `docs/SECURITY_AND_TENANCY.md`, `docs/DEVELOPMENT_GOVERNANCE.md`, contract guard tests | Default disabled; separate contract required if enabled; ticket/reason/TTL/audit required as runtime controls |
| R-010 | Quota fields imply enforcement that GA does not actually provide | medium | yes | AFSCP maintainer | auto_verified | closed by quota semantics guard | `scripts/verify-ga-release.sh`, `docs/GA_PRE_DEV_READINESS.md`, `docs/contracts/namespace-volume-binding-v1.md`, schema/contract guard tests for quota semantics | `quota_bytes_default` is a policy record and enforcement hook only; it is not enforced unless selected volume capability `directory_quota` and corresponding volume integration explicitly enables directory quota enforcement |
| R-011 | Repo lifecycle operations are missing or unsafe for generic archive/delete/restore/purge storage lifecycle | high | yes | AFSCP maintainer | auto_verified | closed by lifecycle contracts, workers, and recovery tests | `scripts/verify-ga-release.sh`, `docs/adr/0008-repo-lifecycle-policy.md`, `docs/contracts/repo-lifecycle-v1.md`, repo lifecycle/session drain/purge/audit tests | Provide archive, restore-archived, delete, restore-tombstoned, and purge contracts with transition rules, session drain, retention, recovery, and audit |
| R-012 | Product-specific concepts leak into core AFSCP contracts | medium | yes | AFSCP maintainer | auto_verified | closed by repo-local product-boundary guard | `scripts/verify-ga-release.sh`, `docs/PRODUCT_BOUNDARY.md`, `docs/DEVELOPER_HANDOFF.md`, `docs/DEVELOPMENT_GOVERNANCE.md`, `internal/contractcheck/contractcheck_test.go` | Keep this repo limited to generic adoption guidance; caller-specific adoption and handoff material lives outside this repo |
| R-013 | Legacy embedded-control repos are workload-mounted without verified protection | critical | yes | AFSCP maintainer, operations owner | auto_verified | closed by mount contract and migration guidance guards | `scripts/verify-ga-release.sh`, `docs/STORAGE_LAYOUT.md`, `docs/contracts/workload-mount-binding-v1.md`, mount/session tests | Reject workload mount unless external control root or verified filtered view is present |
| R-014 | Client connector flow continues to receive raw JuiceFS credentials for AFSCP-backed repos | critical | yes | AFSCP maintainer | auto_verified | closed by WebDAV export boundary and forbidden-credential guards | `scripts/verify-ga-release.sh`, `docs/EXPORT_WEBDAV.md`, `docs/contracts/export-access-webdav-v1.md`, `internal/contractcheck/contractcheck_test.go`, export credential tests | Move ordinary access to WebDAV export credentials; disable raw mount for AFSCP-backed repos |
| R-015 | Repo purge permanently removes data before retention, session drain, or caller approval reference | critical | yes | AFSCP maintainer, operations owner | auto_verified | closed by purge authorization, retention, drain, and audit tests | `scripts/verify-ga-release.sh`, `docs/contracts/repo-lifecycle-v1.md`, `api/schemas/afscp-internal-v1.schema.json`, purge/runbook/audit tests | Require explicit purge operation, caller approval reference, retention-policy check, lifecycle fence, no active/uncertain sessions, audit, and recovery evidence |

## Current Summary

Implementation-baseline maturity is high for product direction and major
security decisions. GA risk closure is now expressed as automated evidence
covered by `scripts/verify-ga-release.sh`, not as role approval state. R-010 is
closed by the repo-local quota semantics guard: quota fields are policy records
and enforcement hooks unless a selected volume capability and corresponding volume integration explicitly enables directory quota enforcement.
