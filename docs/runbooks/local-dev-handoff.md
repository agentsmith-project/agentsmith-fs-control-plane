# Local Dev Handoff

This repository has no implementation yet.

Suggested local development assumptions once implementation begins:

- AFSCP API: `http://localhost:8090/internal/v1`
- AFSCP WebDAV dev base: `http://localhost:8091/exports/{exportId}/`
- optional orchestrator adapter local target: `http://localhost:8080`

## Local Paths

- Planning repo: `/home/percy/works/mbos-v1/improve-agentsmith-fs`
- First caller integration reference: `/home/percy/works/mbos-v1/agentsmith`
- Client connector reference: `/home/percy/works/mbos-v1/agentsmith-desktop`
- Orchestrator reference: `/home/percy/works/mbos-v1/mbos-sandbox-v1`
- JVS: `/home/percy/works/mbos-v1/jvs`

## Before Writing Code

1. Confirm runtime language in ADR.
2. Confirm internal auth model.
3. Confirm operation store database.
4. Confirm workload mount spec fields.
5. Confirm `ExportAccess` fields.
6. Confirm `.jvs` protection plan.
7. Confirm AFSCP core uses product-agnostic `volume`, `namespace`, `repo`, `template`, `export`, and `mount` terms.
8. Confirm that `agentsmith-oss` is not used for current-state analysis.
