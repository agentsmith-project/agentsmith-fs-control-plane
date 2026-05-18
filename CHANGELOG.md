# Changelog

## v1.0.6 - 2026-05-18

- Packages the published direct-capable JVS `v0.4.10` release artifact in the AFSCP container image.
- Keeps namespace, volume, binding, and mount recovery available when JVS-only recovery executors are unavailable, while keeping JVS mutation failures fail-closed.
- Hardens the release workflow so a clean tag build depends on GitHub-published JVS artifacts rather than sibling source checkout builds.
- Records the active JVS release pin evidence in `docs/JVS_AFSCP_DIRECT_RELEASE_EVIDENCE_2026-05-18.md`.

## v1.0.5 - 2026-05-14

- Last published AFSCP image before the direct-capable JVS pin refresh.
- Release container image: `ghcr.io/agentsmith-project/agentsmith-fs-control-plane:v1.0.5`.
