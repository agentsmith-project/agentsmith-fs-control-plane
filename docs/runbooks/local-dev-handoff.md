# Local Dev Handoff

This runbook records product-neutral local development assumptions for AFSCP.
Consumer-specific checkouts, adapters, and compatibility work are maintained
outside this repository.

Suggested local targets:

- AFSCP API: `http://localhost:8090/internal/v1`
- AFSCP WebDAV dev base: `http://localhost:8091/exports/{exportId}/`
- optional workload orchestrator adapter target: `http://localhost:8080`

## Before Writing Code

1. Confirm runtime language in ADR.
2. Confirm internal auth model.
3. Confirm operation store database.
4. Confirm workload mount binding and orchestrator plan fields.
5. Confirm mount binding lease/status lifecycle.
6. Confirm export session and access credential fields.
7. Confirm writer-session fence behavior.
8. Confirm JVS external-control/payload-only mount plan.
9. Confirm AFSCP core uses product-agnostic `volume`, `namespace`, `repo`,
   `template`, `export`, and `mount` terms.
10. Confirm consumer-specific business objects and local sibling paths stay out
    of AFSCP docs, contracts, tests, and implementation.
