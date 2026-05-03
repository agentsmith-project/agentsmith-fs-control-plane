# Contract: AFSCP Internal API V1

Status: draft

AFSCP internal APIs are called by trusted product control planes, privileged admin jobs, migration jobs, and operator tools.

## Required Headers

- `Authorization`: service credential, exact scheme TBD.
- `Idempotency-Key`: required for mutating requests.
- `X-Correlation-Id`: required.
- `X-AFSCP-Namespace-Id`: required where applicable.
- `X-AFSCP-Actor-Type`: required for mutating requests; examples: `user`, `system`, `admin_job`.
- `X-AFSCP-Actor-Id`: required for mutating requests; the authorized end actor, not the caller service identity.
- `X-AFSCP-Caller-Service`: required; the trusted service invoking AFSCP.

## Endpoint Groups

- volume ensure/health
- namespace volume binding get/update
- repo create/get/archive
- save point create/list
- restore preview/run
- template create/clone
- export create/revoke
- workload mount spec
- operation get/list

See [../API_CONTRACT_DRAFT.md](../API_CONTRACT_DRAFT.md) for the current draft payloads.

## Required Invariants

- Every request includes namespace context where resource access is namespace-bound.
- Every mutating request includes the authorized end actor for audit.
- AFSCP validates namespace/repo/template consistency.
- Mutations create operation records before executing.
- Response payloads never include JuiceFS root credentials.
- Errors are stable enough for callers to render product-facing messages.
