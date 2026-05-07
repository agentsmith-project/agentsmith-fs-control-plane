# Schemas

`afscp-internal-v1.schema.json` is the GA implementation-baseline JSON Schema
bundle for the internal API. It is intentionally product-agnostic and must stay
aligned with `api/openapi/internal-v1.openapi.yaml` and
`docs/API_CONTRACT_DRAFT.md`.

FINAL GA is governed by `docs/GA_RELEASE_GATES.md`,
`docs/READINESS_EVIDENCE.md`, and `scripts/verify-ga-release.sh`.

Schema coverage:

- operation envelope
- standard error envelope and stable error families
- canonical request context headers
- JVS JSON envelope
- export session and access credential
- workload mount binding
- orchestrator mount plan
- writer-session fence projection
- namespace volume binding projection
- namespace lifecycle policy projection
- repo and repo template request/response
- namespace-bound repo list projection
- repo lifecycle request/response and status projection
- repo purge confirmation request fields
- save point and restore request/response
- export session status

New or changed endpoint handlers that depend on these schemas must keep schema
names, stable error enums, secret-bearing fields, generated client behavior,
and readiness evidence aligned.
