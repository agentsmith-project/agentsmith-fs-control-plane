# Operational Readiness Contract

Status: active operational acceptance contract for final GA.

Drills and threshold owners are still pending where tracked; this document does
not mark operational readiness complete.

This document turns the runbook checklist into operator-ready acceptance
criteria. It does not replace the detailed runbooks; it defines what each
runbook and drill must prove.

GA candidate deployments must explicitly set `AFSCP_READINESS_PROFILE=ga` so
`/readyz` requires the full GA capability set; passing `/readyz` is still not a
substitute for final evidence review and human GA approval.

Internal API runtime config must include
`AFSCP_API_WEBDAV_EXPORT_PUBLIC_BASE_URL`. It is AFSCP control-plane
configuration, not caller/product-specific configuration. The value must be an
`http`/`https` absolute URL with no userinfo, query, or fragment, and serves as
the base for WebDAV export `access.url`; deployments missing a valid value fail
closed for export access URL issuance.

`/readyz` reports storage ready only when static storage capability config is
available and the runtime store health check passes.

## Alert Classes

| Class | Examples | Page |
| --- | --- | --- |
| critical data risk | purge failure ambiguity, restore-run ambiguous state, lifecycle fence stuck during purge, credential leak | yes |
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
SLOs are known, but each threshold must have an owner, alert severity, runbook,
and drill record before GA.

## Backup And Restore

GA backup scope:

- PostgreSQL operation/resource metadata
- namespace volume bindings
- repo lifecycle state and tombstone records
- export and mount session records
- audit outbox records until delivered and retained
- deployment config excluding raw Secret values

GA restore drills must prove:

- operation store restore does not reissue credentials blindly
- lifecycle fences are recovered from durable state
- tombstoned repos remain restorable within retention
- purged repos are not resurrected
- audit event replay is idempotent

## Runbook Drill Evidence

Each runbook drill must record:

- drill date
- environment
- induced failure or scenario
- detection signal
- operator action
- expected terminal state
- audit events checked
- rollback or recovery outcome
- gaps and follow-up owner
