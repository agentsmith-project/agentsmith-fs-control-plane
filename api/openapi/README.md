# OpenAPI

`internal-v1.openapi.yaml` is the GA implementation-baseline internal OpenAPI
artifact. The spec is private/internal, product-agnostic, and must not be
exposed as a user-facing API.

Endpoint handler and storage behavior changes may continue from this artifact
only while the relevant contracts, generated clients, and readiness evidence
remain aligned. FINAL GA acceptance remains governed by
`docs/READINESS_EVIDENCE.md`; owner, security, generated-client, operations,
runbook drill, and human sign-off entries must be complete before the
applicable readiness gate is closed.

The OpenAPI includes the standard operation envelope, standard error envelope,
stable error families, caller context headers, orchestrator-only response
boundaries, repo lifecycle endpoints, namespace lifecycle policy, purge
confirmation fields, and stable lifecycle error codes.
