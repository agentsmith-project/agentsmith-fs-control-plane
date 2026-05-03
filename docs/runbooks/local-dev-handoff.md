# Local Dev Handoff

This repository has no implementation yet.

Suggested local development assumptions once implementation begins:

- AFSCP API: `http://localhost:8090/internal/v1`
- AFSCP WebDAV dev base: `http://localhost:8091/exports/{exportId}/`
- sandbox-manager local port-forward: `http://localhost:8080`

## Local Paths

- Planning repo: `/home/percy/works/mbos-v1/improve-agentsmith-fs`
- AgentSmith API: `/home/percy/works/mbos-v1/agentsmith`
- Desktop: `/home/percy/works/mbos-v1/agentsmith-desktop`
- Sandbox: `/home/percy/works/mbos-v1/mbos-sandbox-v1`
- JVS: `/home/percy/works/mbos-v1/jvs`

## Before Writing Code

1. Confirm runtime language in ADR.
2. Confirm internal auth model.
3. Confirm operation store database.
4. Confirm sandbox binding v2 fields.
5. Confirm Desktop `ExportAccess` fields.
6. Confirm `.jvs` protection plan.
7. Confirm that `agentsmith-oss` is not used for current-state analysis.
