# ADR 0003: Keep Repo Templates Namespace-Scoped In P0

Status: accepted for handoff

## Context

Calling products need a way to create reusable repo templates and clone them into independent repos. Cross-tenant or cross-product sharing carries data leakage and policy risks.

## Decision

Repo templates are scoped to one AFSCP namespace in P0.

Rules:

- `template.namespace_id` is required.
- AFSCP rejects clone requests where template namespace differs from target namespace.
- Cloning creates an independent repo with a new JVS repo identity.
- Calling products may add their own product-level visibility metadata, but that metadata does not change AFSCP namespace boundaries.

## Consequences

Positive:

- Clear tenancy boundary.
- Simpler authorization contract.
- No global template marketplace needed in MVP.
- No cross-volume clone policy needed in MVP.

Tradeoffs:

- Callers cannot share templates across namespaces through ordinary clone APIs.
- Future cross-namespace sharing, if ever required, must be designed as a separate admin/import product with explicit controls.
