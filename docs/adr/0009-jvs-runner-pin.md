# ADR 0009: Pin Direct-Capable JVS Before Storage Mutations

Status: accepted; active pin is a published direct-capable JVS release artifact

## Context

AFSCP executes JVS for repo init, save point operations, direct restore, clone,
and metadata diagnostics. The active save/list/restore/clone/status/doctor contract
has been reset to direct `jvs afscp ...` commands. The old public save/history,
strict doctor, restore preview/run/discard lifecycle, and
`restore --direct --discard-unsaved` adapter are not active contract.

The previously recorded `v0.4.9` release does not contain the direct
`jvs.afscp.direct.v1` surface. JVS `v0.4.10` now publishes the direct-capable
artifact AFSCP needs, so AFSCP pins that GitHub release artifact instead of a
local sibling checkout or ad hoc binary.

Pinned release artifact:

```text
version: v0.4.10
artifact: jvs-linux-amd64
release URL: https://github.com/agentsmith-project/jvs/releases/tag/v0.4.10
JVS binary artifact SHA-256: fa4ada8e3353f85679d13870ea53307caafbd8217b04ba576b185105d9178cef
source ref: jvs@v0.4.10:6a0f762bc436f0d3dc7c7c1d60847992c3a82718
```

## Decision

Pin AFSCP's active direct JVS runtime to the published artifact above. The AFSCP
Dockerfile downloads the GitHub-published `v0.4.10` `jvs-linux-amd64` artifact
with a fixed SHA-256 checksum; release workflows must not rebuild JVS from a
sibling source checkout.

AFSCP direct commands must use:

```text
jvs afscp --control-root <control> --home <home> save --message <message> [--purpose <purpose>] --json
jvs afscp --control-root <control> --home <home> list --json
jvs afscp --control-root <source_control> --home <source_home> clone --target-control-root <target_control> --target-home <target_home> --json [--save-point <save_point_id>]
jvs afscp --control-root <control> --home <home> restore --save-point <save_point_id> --json
jvs afscp --control-root <control> --home <home> status --json
jvs afscp --control-root <control> --home <home> doctor --json
```

Save and restore hot paths do not call doctor/status by default. Status and
doctor are explicit metadata-only diagnostics, recovery aids, or smoke checks.

Non-direct JVS commands remain only for explicitly scoped repo init. They are
not compatibility adapters for active direct save, clone, or restore, and they
must not reintroduce public save/history, ordinary repo clone, or strict doctor
validation.

## Required Evidence

- Record release URL, source ref, JVS binary artifact SHA-256, and help-surface
  evidence in `docs/JVS_AFSCP_DIRECT_RELEASE_EVIDENCE_2026-05-18.md`.
- Keep `docs/contracts/jvs-runner-contract-v1.md` aligned with direct argv and
  JSON fail-closed parsing.
- Keep release packaging on GitHub-published JVS artifacts.

## Consequences

Positive:

- Prevents AFSCP from advertising old `v0.4.9` or restore-preview behavior as
  active.
- Makes the release artifact explicit and auditable.
- Keeps active save/list/restore/clone/status/doctor paths on the fast direct
  contract.

Tradeoffs:

- Updating JVS now requires publishing a new JVS release first, then updating
  AFSCP's explicit release pin.
- Docker builds require network access to GitHub release artifacts unless the
  artifact is pre-cached by the builder.

Evidence:

- active release pin:
  `docs/JVS_AFSCP_DIRECT_RELEASE_EVIDENCE_2026-05-18.md`
- historical release context:
  `docs/JVS_PIN_EVIDENCE_2026-05-12-v0.4.9.md`
