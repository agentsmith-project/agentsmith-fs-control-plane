# Contract: AgentSmith AFSCP Internal API V1

Status: draft

AFSCP internal APIs are called by AgentSmith API and privileged admin jobs only.

## Required Headers

- `Authorization`: service credential, exact scheme TBD.
- `Idempotency-Key`: required for mutating requests.
- `X-Correlation-Id`: required.
- `X-AgentSmith-Workspace-Id`: required where applicable.
- `X-AgentSmith-Actor-Type`: required for mutating requests; examples: `user`, `system`, `admin_job`.
- `X-AgentSmith-Actor-Id`: required for mutating requests; the authorized end actor, not the AgentSmith API service identity.

## Endpoint Groups

- storage pool ensure/health
- repo create/get/archive
- save point create/list
- restore preview/run
- template clone
- export create/revoke
- sandbox mount spec
- operation get/list

See [../API_CONTRACT_DRAFT.md](../API_CONTRACT_DRAFT.md) for the current draft payloads.

## Required Invariants

- Every request includes tenant workspace context.
- Every mutating request includes the authorized end actor for audit.
- AFSCP validates workspace/repo consistency.
- Mutations create operation records before executing.
- Response payloads never include JuiceFS root credentials.
- Errors are stable enough for AgentSmith to render user-facing messages.
