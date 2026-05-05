# ADR 0009: Pin Released JVS Runner Binary Before Storage Mutations

Status: accepted with upstream blocker for development handoff

## Context

AFSCP executes JVS for repo init, save, history, restore preview/run, clone, and
doctor. Storage mutation handlers must not depend on an unpinned JVS binary or
undocumented JSON shape.

JVS is published as GitHub release binaries. AFSCP should consume a verified
release asset; it should not require rebuilding JVS locally during GA handoff.

Pinned release for the first AFSCP runner contract:

```text
release: https://github.com/agentsmith-project/jvs/releases/tag/v0.4.7
version: v0.4.7
asset: jvs-linux-amd64
sha256: 4ec030d62b24d980192af550d04cb4b7455299b285b68c91843386cfa5157e6d
signature bundle: jvs-linux-amd64.bundle
```

## Decision

Pin the first AFSCP JVS runner contract to the GitHub release asset above for
development handoff. Before storage mutation implementation, the development
team must:

- package or download the verified release binary for each target platform,
- verify `SHA256SUMS` and the matching cosign bundle where the deployment
  pipeline supports Sigstore verification,
- record the asset name, version, checksum, and smoke evidence in this ADR or
  the runner contract, and
- update this ADR and `docs/contracts/jvs-runner-contract-v1.md` before moving
  to any newer JVS release.

The release page's source revision is informational for traceability. It is not
the AFSCP supply-chain pin, and developers should not compile JVS from source as
the GA handoff path.

Required smoke surface:

- external control root `init`
- `save`
- `history`
- restore preview
- restore-run
- `repo clone --target-control-root --save-points main`
- `doctor --strict`
- clean CWD behavior
- redacted JSON capture

Current blocker:

During pre-dev smoke with the pinned `v0.4.7` release binary, `restore --run
<plan>` returned `ok=true`, but a completed restore plan remained under the
control root. A following `doctor --strict` returned non-zero and reported
`healthy=false` with `E_RECOVERY_BLOCKING`. Earlier smoke also showed `repo
clone` can be blocked by the same stale restore plan. This blocks closing G-005
and storage mutation handlers until JVS behavior is fixed or the runner
contract defines a reviewed safe cleanup/resolution command.

Evidence: `docs/JVS_SMOKE_EVIDENCE_2026-05-05.md`.

## Consequences

Positive:

- Prevents implementing restore-run or clone against ambiguous JVS recovery
  state.
- Gives the JVS owner a precise blocker to resolve.
- Keeps AFSCP storage mutation handlers behind evidence instead of hope.

Tradeoffs:

- Neutral service skeleton can start, but storage mutations remain blocked until
  G-005 closes.
- The development team must coordinate with the JVS owner before enabling
  restore-run, clone-after-restore, or lifecycle reactivation flows.
