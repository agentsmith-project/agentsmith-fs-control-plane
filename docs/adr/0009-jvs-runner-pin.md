# ADR 0009: Pin Released JVS Runner Binary Before Storage Mutations

Status: accepted for development handoff; G-005 closed by v0.4.8 smoke evidence

## Context

AFSCP executes JVS for repo init, save, history, restore preview/run, clone, and
doctor. Storage mutation handlers must not depend on an unpinned JVS binary or
undocumented JSON shape.

JVS is published as GitHub release binaries. AFSCP should consume a verified
release asset; it should not require rebuilding JVS locally during GA handoff.

Pinned release for the AFSCP runner contract:

```text
release: https://github.com/agentsmith-project/jvs/releases/tag/v0.4.8
version: v0.4.8
asset: jvs-linux-amd64
sha256: f011699fa92abae59e70153d32f3b9a10de1159fc23a390b22208db23f965521
signature bundle: jvs-linux-amd64.bundle
```

## Decision

Pin the AFSCP JVS runner contract to the GitHub release asset above for
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
- restore discard
- recovery status for pending preview and post-discard/post-run idle states
- `repo clone --target-control-root --save-points main`
- `doctor --strict`
- clean controlled CWD behavior for post-init commands with explicit
  `--control-root <control> --workspace main --json`
- redacted JSON capture

Accepted evidence:

Pre-dev smoke with the pinned `v0.4.8` release binary passed the required
external control-root, save/history, restore preview/run, post-restore recovery
status, doctor, and clone-after-restore checks. After restore-run, recovery
status returned `ok=true` with no plans, and `doctor --strict` returned healthy.
Follow-up clean-CWD lifecycle smoke with the same official binary and SHA-256
verified post-init commands from a clean controlled CWD, restore discard,
pending preview recovery status with `restore_state` blocking and empty
`plans`, and idle recovery status after discard and restore-run.

Evidence:

- accepted: `docs/JVS_SMOKE_EVIDENCE_2026-05-05-v0.4.8.md`
- historical blocker: `docs/JVS_SMOKE_EVIDENCE_2026-05-05.md`

## Consequences

Positive:

- Prevents implementing restore-run or clone against ambiguous JVS recovery
  state.
- Records that the v0.4.7 restore-run recovery plan residual blocker is resolved
  by v0.4.8 evidence.
- Keeps AFSCP storage mutation handlers behind evidence instead of hope.

Tradeoffs:

- G-005 is closed. This only closes the JVS gate.
- Real repo/JVS/storage handlers may proceed only through accepted contracts,
  fences, session drain, operation leases, audit behavior, and focused tests.
