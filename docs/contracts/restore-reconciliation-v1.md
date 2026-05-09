# Restore Reconciliation Contract v1

Status: GA implementation-baseline contract. FINAL GA is governed by
`docs/GA_RELEASE_GATES.md`, `docs/READINESS_EVIDENCE.md`, and
`scripts/verify-ga-release.sh`.

This contract defines the AFSCP after-restore safety mode. It is not JVS restore-run, not a backup scheduler, not object-store restore orchestration, and not a production DR certification gate.

## Modes

`restore_reconciliation_runs.mode` has exactly these durable states:

| Mode | Meaning |
| --- | --- |
| `reconciling` | Explicit reconciliation mode is active. Dangerous writes must fail closed until observations prove safety. |
| `blocked_operator_intervention` | A metadata/storage mismatch was found and the run is blocked with operator-visible observation and audit evidence. Non-purged repo mismatches may move the repo to `operator_intervention_required`; purged repo mismatches keep the repo purged. |
| `completed` | All target repos have clean observations and dangerous writes may resume. |

## Required Safety

- Dangerous writes are denied while a run is `reconciling` or `blocked_operator_intervention`.
- Completion requires a durable target set. every target repo in that set must have a clean observation; zero targets or a missing target observation fails closed.
- A clean observation must carry explicit snapshot, generation, tombstone marker, and purge marker evidence. Expected and observed markers must match before completion.
- no WebDAV credential reissue occurs while reconciliation blocks export create. Existing idempotent replay may return a redacted session without access credentials.
- A purged repo is absorbing. If storage is observed for a purged repo, the repo must not resurrect and its repo status/lifecycle must remain purged.
- A non-purged metadata/storage mismatch must atomically record an observation, mark the repo `operator_intervention_required`, and append audit.
- A purged metadata/storage mismatch must atomically record an observation, block the run, and append audit/operator evidence without resurrecting or moving the repo out of purged state.
- Evidence references are hashes, IDs, policy refs, or redacted paths. They must not contain raw storage roots, `.jvs`, credentials, SecretRef, or metadata URLs.

## Non-Goals

The contract does not define backup creation, snapshot scheduling, object-store restore, JVS restore-run behavior, production deployment state, manual release approval, purge positive behavior, or external project gates.

## Gate

Repository evidence for this contract is verified by `scripts/verify-ga-release.sh`. Final acceptance requires machine-verifiable manifest evidence for restore reconciliation and must not depend on deployment runtime state.
