# ADR 0003: Keep Templates Workspace-Scoped

Status: accepted for handoff

## Context

Users want to save a notebook task as a reusable template and let other users start from it. The product also requires storage profiles to be owned by AgentSmith workspaces, and explicitly disallows cross-workspace template sharing.

## Decision

Templates are scoped to one AgentSmith workspace.

Rules:

- `template.tenant_workspace_id` is required.
- AgentSmith API rejects clone requests where template workspace differs from request workspace.
- AFSCP also rejects clone requests when source and target paths do not resolve under the same workspace prefix.
- Cloning creates an independent repo with a new JVS repo identity.

## Consequences

Positive:

- Clear tenant boundary.
- Simpler authorization.
- No global template marketplace needed in MVP.
- No cross-storage-pool clone policy needed in MVP.

Tradeoffs:

- Users cannot share templates across workspaces.
- Future cross-workspace sharing, if ever required, must be designed as a separate product with explicit admin and compliance controls.
