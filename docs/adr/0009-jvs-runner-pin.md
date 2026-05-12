# ADR 0009: Pin Released JVS Runner Binary Before Storage Mutations

Status: accepted for development handoff; active pin is JVS v0.4.9

## Context

AFSCP executes JVS for repo init, save, history, restore preview/run, clone, and
doctor. Storage mutation handlers must not depend on an unpinned JVS binary or
undocumented JSON shape.

JVS is published as GitHub release binaries. AFSCP should consume a verified
release asset; it should not require rebuilding JVS locally during GA handoff.

Pinned release for the AFSCP runner contract:

```text
release: https://github.com/agentsmith-project/jvs/releases/tag/v0.4.9
version: v0.4.9
asset: jvs-linux-amd64
sha256: 0a1c6896cecf85ec2ac4e15e1c29f6e3f8cf09b9a4db48a516559604f0e7e944
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
- `doctor --strict --repair-runtime` for stale repository mutation lock cleanup
  after save `E_REPO_BUSY`
- clean controlled CWD behavior for post-init commands with explicit
  `--control-root <control> --workspace main --json`
- redacted JSON capture

Accepted evidence:

JVS v0.4.9 release pin evidence records the active release URL, asset, and
SHA-256 from the official `SHA256SUMS`. AFSCP runner tests pin the argv shape,
JSON parsing, and fail-closed behavior for the external-control
`doctor --strict --repair-runtime --json` stale repository mutation lock cleanup
contract.

Earlier pre-dev smoke with the pinned `v0.4.8` release binary passed the
required external control-root, save/history, restore preview/run, post-restore
recovery status, doctor, and clone-after-restore checks. That artifact remains
historical context and is not the active pin.

Evidence:

- active pin: `docs/JVS_PIN_EVIDENCE_2026-05-12-v0.4.9.md`
- historical smoke: `docs/JVS_SMOKE_EVIDENCE_2026-05-05-v0.4.8.md`
- historical blocker: `docs/JVS_SMOKE_EVIDENCE_2026-05-05.md`

## Consequences

Positive:

- Prevents implementing restore-run or clone against ambiguous JVS recovery
  state.
- Records the current v0.4.9 runner pin and stale repository mutation lock
  repair contract.
- Keeps AFSCP storage mutation handlers behind evidence instead of hope.

Tradeoffs:

- G-005 is closed. This only closes the JVS gate.
- Real repo/JVS/storage handlers may proceed only through accepted contracts,
  fences, session drain, operation leases, audit behavior, and focused tests.
