# Operational Readiness Contract

Status: active operational readiness contract for final GA.

This document defines repo-local operational evidence for
`scripts/verify-ga-release.sh`. It does not create a separate role-approval
gate.

This document turns the runbook checklist into operator-ready criteria. It does
not replace the detailed runbooks; it defines what each runbook artifact and
automated check must cover.

GA candidate deployments must explicitly set `AFSCP_READINESS_PROFILE=ga` so
`/readyz` requires the full GA capability set; passing `/readyz` is still not a
substitute for the repo-local GA release gate.

Internal API runtime config must include
`AFSCP_API_WEBDAV_EXPORT_PUBLIC_BASE_URL` only when WebDAV export capability is
enabled and ready for export issuance. It is AFSCP control-plane configuration,
not caller/product-specific configuration. The value must be an `http`/`https`
absolute URL with no userinfo, query, or fragment, and serves as the base for
WebDAV export `access.url`. When WebDAV export is disabled or not ready, the
runtime may start without this value, but create export remains fail-closed
until the capability is available with a valid public base URL.

`/readyz` reports storage ready only when static storage capability config is
available and the runtime store health check passes.

## Alert Classes

| Class | Examples | Page |
| --- | --- | --- |
| critical data risk | purge failure ambiguity, direct restore to a save point ambiguous state, writer-session fence stuck during restore, lifecycle fence stuck during purge, credential leak | yes |
| security boundary risk | Secret reference exposed, raw JuiceFS credential in response/log, path traversal accepted | yes |
| availability risk | volume unhealthy, operation worker stopped, audit outbox stuck, stale mount lease backlog | yes when sustained |
| product degradation | export create failing, lifecycle drain waiting, template clone denied by capability | ticket/triage |

## Required Thresholds Before GA

- audit outbox lag alert threshold and replay procedure
- operation stuck threshold per operation type
- stale workload mount lease threshold
- held writer-session fence threshold
- held repo lifecycle fence threshold
- WebDAV denied-path anomaly threshold
- export credential misuse threshold
- JVS doctor failure threshold
- volume health degradation threshold
- backup age threshold for operation store and metadata

The development team may choose concrete numeric thresholds after deployment
SLOs are known, but each threshold class must have an owner, alert severity,
runbook, and repo-local evidence before GA release.

## Backup And Restore

GA backup scope:

- PostgreSQL operation/resource metadata
- namespace volume bindings
- repo lifecycle state and tombstone records
- export and mount session records
- audit outbox records until delivered and retained
- deployment config excluding raw Secret values

GA restore evidence must prove:

- operation store restore does not reissue credentials blindly
- lifecycle fences are recovered from durable state
- tombstoned repos remain restorable within retention
- purged repos are not resurrected
- audit event replay is idempotent

GA audit delivery evidence covers the configured HTTP JSON sink path. Non-HTTP
audit sink integrations are future extensions and are not required to close
operational readiness; deployments still must preserve external sink
idempotency, outbox retention, and replay behavior.

## Runbook Evidence

Each runbook artifact should record:

- environment
- induced failure or scenario
- detection signal
- operator action
- expected terminal state
- audit events checked
- rollback or recovery outcome
- gaps and follow-up owner
