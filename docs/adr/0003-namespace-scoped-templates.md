# ADR 0003: Keep Repo Templates Namespace-Scoped For GA

Status: accepted for handoff

## Context

Calling products need a way to create reusable repo templates and clone them into independent repos. Cross-tenant or cross-product sharing carries data leakage and policy risks.

## Decision

Repo templates are scoped to one AFSCP namespace for GA.

Rules:

- `template.namespace_id` is required.
- AFSCP rejects clone requests where template namespace differs from target namespace.
- GA must not cross volumes. If a namespace binding changed after template creation and the template volume no longer matches the namespace default volume, clone rejects with `VOLUME_MISMATCH_REQUIRES_IMPORT`.
- Template creation must create a fresh source save point in the operation. Creating a template from an older save point is future work because it needs a staging restore/import flow.
- Template clone history mode is pinned to JVS capability; GA can start with `main`, and `all` requires durable imported-save-point protection.
- Cloning creates an independent repo with a new JVS repo identity.
- Calling products may add their own product-level visibility metadata, but that metadata does not change AFSCP namespace boundaries.

## Consequences

Positive:

- Clear tenancy boundary.
- Simpler authorization contract.
- No global template marketplace needed in GA.
- No implicit cross-volume clone behavior in GA.

Tradeoffs:

- Callers cannot share templates across namespaces through ordinary clone APIs.
- Future cross-namespace sharing, if ever required, must be designed as a separate admin/import product with explicit controls.
