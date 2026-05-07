# GA Review Checklist

Use this checklist for final GA acceptance review after the implementation
baseline. New or changed endpoint handlers and storage mutation logic must also
be reviewed against this checklist before acceptance.

AFSCP GA is reviewed as an independent shared filesystem control-plane release.
Reference consumers can provide adoption feedback, but their acceptance is not
the final AFSCP GA gate.

Each checked item needs an owner role, reviewer role, evidence link or file
path, and blocking/non-blocking status recorded in `docs/READINESS_EVIDENCE.md`.
The evidence entry should name the related gate or risk ID where one exists.
Waivers and residual-risk acceptance follow `docs/DEVELOPMENT_GOVERNANCE.md`;
credential exposure, tenant isolation failure, user data loss, irrecoverable
operation ambiguity, and caller-visible contract break risks are non-waivable
for GA.

## Product Boundary

- [ ] AFSCP core docs use generic storage concepts: volume, namespace, repo, save point, restore, template, export, mount, operation.
- [ ] This repo contains only generic adoption guidance; caller-specific adoption and handoff material lives outside this repo in consumer-owned repositories.
- [ ] Direct and indirect caller model is accepted.
- [ ] Cross-namespace template clone remains rejected by default.
- [ ] Ordinary concurrent read/write behavior is accepted without merge semantics.
- [ ] Restore-run is treated as a version mutation, blocks new read-write sessions, and rejects active or uncertain read-write sessions.
- [ ] Ordinary client access uses controlled exports rather than raw JuiceFS.
- [ ] Product deletion/archive/restore/purge behavior maps to AFSCP repo lifecycle APIs when storage state changes.
- [ ] Calling product catalog keeps repo ID mappings while tombstoned storage remains restorable.
- [ ] Product display-name rename and catalog detach remain outside AFSCP core.
- [ ] Quota behavior is documented as policy hook or explicit enforcement capability.

## Architecture

- [ ] AFSCP is deployed independently from calling products.
- [ ] AFSCP has its own ServiceAccount and Secret access.
- [ ] Calling products remain product authorization authorities.
- [ ] AFSCP owns storage execution and operation records.
- [ ] External orchestrators remain runtime mount execution layers and do not decide product authorization.
- [ ] No direct dependency on a caller product package exists.
- [ ] Namespace volume binding does not contain an authoritative raw filesystem path supplied by a caller.
- [ ] Namespace binding includes caller-service authorization policy.
- [ ] Namespace disable semantics for new and existing sessions are accepted.
- [ ] Repo lifecycle fence, session drain, tombstone, restore, purge, and retention semantics are accepted.

## Security

- [ ] No ordinary client response contains JuiceFS metadata URL or object store credentials.
- [ ] Workload containers receive no JuiceFS root credentials.
- [ ] Ordinary product callers never receive JuiceFS Secret references.
- [ ] Orchestrator-only mount plans are protected by a dedicated caller role.
- [ ] WebDAV is served by an AFSCP-controlled policy gateway or equivalent wrapper; stock `juicefs webdav` alone is not accepted.
- [ ] WebDAV exposes only payload roots, never JVS control roots, and rejects root-level `.jvs` access/creation attempts.
- [ ] Workload mounts expose only `payload_volume_subdir`; embedded-control repos are rejected until migrated or protected by a verified filtered view.
- [ ] Mount binding lease/status lifecycle defines revoke-request versus confirmed-unmounted terminal states and is integrated with restore-run.
- [ ] Path resolver rejects traversal and namespace mismatch.
- [ ] Template clone is checked by namespace, resource, and volume rules in AFSCP.
- [ ] Mutating JVS operations use durable operation records, phases, and resource locks.
- [ ] Mutating AFSCP requests include the authorized end actor, separate from the calling service identity.
- [ ] Denied authorization, path, namespace, and capability checks emit audit events.
- [ ] Break-glass direct mount is disabled by default or covered by a reviewed contract.
- [ ] Repo purge is irreversible, explicitly authorized, audited, and blocked by active or uncertain sessions.
- [ ] Namespace lifecycle policy defines tombstone retention, purge eligibility, and break-glass purge behavior.

## Contract Readiness

- [ ] Runtime language decision is captured in a new ADR.
- [ ] Internal service auth and caller identity model are accepted.
- [ ] JSON schemas and internal OpenAPI exist and match narrative contracts.
- [ ] Standard operation envelope and standard error envelope are accepted.
- [ ] Stable error families are accepted by AFSCP product and generated-client compatibility reviewers.
- [ ] JVS release binary/version/checksum is pinned, required command smoke tests pass, and runner CWD cannot affect repo resolution.
- [ ] JVS argv, JSON success/error, exit-code mapping, dirty-state behavior, and recovery mapping are frozen.
- [ ] Workload mount binding/orchestrator plan split is accepted by platform/runtime contract reviewers.
- [ ] Writer-session fence is accepted by operations, platform/runtime contract, and generated-client compatibility reviewers.
- [ ] Repo lifecycle contract is accepted by AFSCP product, operations, and security owners.
- [ ] Export session/access credential contract is accepted by generated-client compatibility reviewers.
- [ ] Operation recovery matrix and audit delivery semantics are accepted.

## GA Operations

- [ ] Required runbooks in `docs/runbooks/README.md` exist and have drill evidence.
- [ ] Observability covers volume health, JVS failures, operation failures, stale leases, held fences, export denials, and audit outbox lag.
- [ ] Backup and restore behavior is documented for operation store and metadata records.
- [ ] Secret rotation runbook exists.
- [ ] Operator intervention semantics and escalation are documented.
- [ ] All GA-blocking risks in `docs/RISK_REGISTER.md` are closed or have approved residual-risk acceptance under `docs/DEVELOPMENT_GOVERNANCE.md`.
- [ ] Operation records, logs, audit payloads, generated schemas, OpenAPI examples, and error payloads pass secret redaction review.
