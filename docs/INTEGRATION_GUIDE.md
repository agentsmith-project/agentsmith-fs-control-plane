# Integration Guide

Status: generic consumer adoption guidance; not an AFSCP release gate.

AFSCP integrates with caller applications through neutral storage-control
contracts. A caller application may use this guide to map its own product
objects to AFSCP resources, but those objects, methods, and workflows remain
outside this repository.

Consumer adoption can provide functional requirements and compatibility
feedback. It cannot define AFSCP gate closure, release readiness, or business
logic.

## Concept Mapping

| Consumer responsibility | AFSCP primitive |
| --- | --- |
| product tenant or isolation boundary | `namespace` |
| storage profile choice | namespace volume binding |
| durable filesystem resource | `repo` |
| reusable source content | `repo_template` |
| client access session | `export` |
| runtime payload access | `workload_mount_binding` plus orchestrator-only mount plan |
| storage lifecycle decision | repo lifecycle operation |
| product audit projection | projection of AFSCP operation and audit events |

AFSCP stores only the right-hand concepts. Caller applications store their own
catalog, authorization, display, workflow, and user-facing state.

## Caller Application Responsibilities

Caller applications should:

- authenticate users and authorize product actions before calling AFSCP
- map product resources to AFSCP namespaces, repos, templates, exports, and
  mount bindings
- call AFSCP with authenticated service identity, authorized actor, namespace,
  idempotency, and correlation context
- store opaque AFSCP IDs instead of raw filesystem paths, volume credentials, or
  backend connection details
- request repo lifecycle operations only when storage availability or retention
  state changes
- project AFSCP operation and audit events into caller-owned product audit or UI
  state when needed

Caller applications should not:

- run `juicefs` or `jvs` as part of ordinary product flows
- hold JuiceFS root credentials
- pass raw filesystem paths, metadata URLs, bucket credentials, Secret values, or
  source subdirectories to ordinary clients or workloads
- require AFSCP to understand caller business object IDs
- use AFSCP repo lifecycle APIs for display-only or catalog-only changes

## Workload Orchestrator Responsibilities

The workload orchestrator consumes privileged AFSCP mount plans through an
orchestrator-scoped service identity. It executes runtime mount setup, reports
heartbeat/release/revoke/confirmed-unmounted state, and keeps Secret-bearing
details away from ordinary callers and workloads.

AFSCP may only treat a write-capable binding as terminal when the orchestrator
confirms that the runtime mount is unmounted or otherwise non-accessing.

## Client Connector Responsibilities

Client connectors consume caller-issued export access, initially WebDAV. They do
not call AFSCP directly and do not receive raw JuiceFS credentials for ordinary
flows.

## Adoption Sequence

1. Register caller service identities and namespace roles.
2. Map caller-side isolation boundaries to AFSCP namespaces.
3. Bind namespaces to managed volumes.
4. Map caller storage resources to AFSCP repos.
5. Route storage lifecycle changes through AFSCP repo lifecycle APIs.
6. Route client access through AFSCP exports.
7. Route runtime access through workload mount bindings and orchestrator-only
   mount plans.
8. Route save, history, restore, and clone through AFSCP.
9. Keep legacy migration, feature flags, and caller UX changes in
   consumer-owned repositories.

This sequence is consumer adoption guidance. It is not an AFSCP GA closure
checklist.
