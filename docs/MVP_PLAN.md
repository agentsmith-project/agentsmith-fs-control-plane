# GA Delivery Readiness Plan

Status: historical filename retained; active plan targets GA directly.

This repository previously used MVP/P0 language. The active target is one GA
readiness line defined by `docs/GA_PRE_DEV_READINESS.md`. This document is a
compact delivery readiness view, not a staged rollout plan.

## GA Scope

GA must prove the storage control plane boundary and the end-to-end functional
primitives:

- independent AFSCP service/container
- durable operation store and recovery
- volume registry and health checks
- namespace-to-volume binding
- namespace caller-service authorization
- shared JuiceFS-backed volume support for new repos
- repo path allocation under AFSCP-controlled namespace roots
- repo archive, restore-archived, delete, restore-tombstoned, and purge lifecycle operations
- JVS init/save/history/restore execution through a pinned runner contract
- WebDAV export without JuiceFS credentials
- workload mount binding generation and orchestrator-only mount plans after the orchestrator contract is accepted
- mount binding status, heartbeat, release, revoke, expiry, and stale-lease reconciliation
- repo clone into namespace-scoped immutable template repo
- same-namespace template clone into an independent repo
- cross-namespace template clone rejection by default
- JVS control metadata protection gate for WebDAV and workload mounts
- restore-run writer-session fencing that blocks new read-write sessions and rejects active or uncertain read-write sessions
- low-level audit event emission and operator inspection

## GA Non-Scope

- Product-specific UI or workflows.
- Product authorization.
- Caller-specific job/task semantics.
- Caller-specific catalog semantics.
- Migrating all legacy repos.
- Multi-region or multi-cloud storage policy.
- Global template marketplace.
- Cross-namespace sharing.
- Product display-name rename or catalog detach APIs.
- Namespace delete APIs.
- Creating templates from older historical save points.
- SMB/NFS export.
- Per-file ACL UI.
- Billing and quota enforcement UI.
- Version merge/conflict resolution.
- Runtime language optimization work.

## Readiness Workstreams

These workstreams can proceed in parallel as long as implementation remains
bound to accepted contracts:

- runtime ADR and neutral service skeleton
- schemas, OpenAPI, standard envelopes, and stable error families
- service auth and namespace caller authorization
- operation store, audit, recovery, and writer-session fence contract
- repo lifecycle fence, drain, tombstone, restore, purge, and recovery contract
- JVS binary pin and runner smoke tests
- path resolver and control/payload storage layout tests
- WebDAV export contract and policy gateway tests
- workload mount orchestrator contract and Secret boundary review
- GA operational runbooks, observability, backup/restore, and risk closure

## Definition Of Done

- All GA admission criteria in `docs/PRODUCT_REQUIREMENTS.md` pass.
- No ordinary API response contains JuiceFS root credential material.
- Workload mount bindings, orchestrator plans, and workload environments contain no JuiceFS root credentials.
- Ordinary product callers cannot see JuiceFS Secret references.
- WebDAV exposes only payload roots and cannot access JVS control metadata.
- Workload mounts expose only payload roots and never include `.jvs` for AFSCP-managed repos.
- JVS `doctor --strict` passes after repo create, save, restore, and clone.
- Calling products can map their own business objects to AFSCP primitives without AFSCP knowing those business object types.
- Operators can inspect and recover GA operation failures using documented runbooks.
- Repo lifecycle operations support AgentSmith file library archive/delete/restore/purge without exposing raw storage paths or credentials.
- GA-blocking risks in `docs/RISK_REGISTER.md` are closed or have approved residual-risk acceptance under `docs/DEVELOPMENT_GOVERNANCE.md`.
