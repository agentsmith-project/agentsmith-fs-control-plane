# OpenAPI

`internal-v1.openapi.yaml` is the first GA pre-dev internal OpenAPI draft. The
spec is private/internal, product-agnostic, and must not be exposed as a
user-facing API.

Service skeleton work may start from this file, but storage handlers still need
the relevant readiness gates in `docs/READINESS_EVIDENCE.md` to close.

The OpenAPI includes the standard operation envelope, standard error envelope,
stable error families, caller context headers, orchestrator-only response
boundaries, repo lifecycle endpoints, namespace lifecycle policy, purge
confirmation fields, and stable lifecycle error codes.
