# ADR 0009: Pin Direct-Capable JVS Before Storage Mutations

Status: accepted for pre-GA convergence; active pin is a local
direct-capable JVS build

## Context

AFSCP executes JVS for repo init, save point operations, direct restore, clone,
and metadata diagnostics. The active save/list/restore/clone/status/doctor contract
has been reset to direct `jvs afscp ...` commands. The old public save/history,
strict doctor, restore preview/run/discard lifecycle, and
`restore --direct --discard-unsaved` adapter are not active contract.

The previously recorded `v0.4.9` release does not contain the direct
`jvs.afscp.direct.v1` surface. There is not yet a formal release artifact for
the direct-capable JVS build, so pre-GA AFSCP pins a local binary plus source
ref as explicit evidence instead of continuing to claim `v0.4.9` as active.

Pinned pre-GA local artifact:

```text
version: pre-ga-local-afscp-direct-2026-05-16-r3
artifact: afscp-jvs-direct-local-linux-amd64
JVS binary artifact SHA-256: f6028582acdf9257f83636bcb70dc63a809887689bb3bc52c47336360f6b3d1c
source ref: jvs@main:edd317474db5fd6f9e3e98015438a47d02ad73c6
binary evidence path: /tmp/afscp-jvs-direct-local
```

## Decision

Pin AFSCP's active direct JVS runtime to the local artifact above for pre-GA
convergence. The AFSCP Dockerfile expects build pipelines to provide this
verified direct-capable binary in the build context; it no longer downloads the
old `v0.4.9` release asset.

AFSCP direct commands must use:

```text
jvs afscp --control-root <control> --home <home> save --message <message> --json
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

- Record source ref, JVS binary artifact SHA-256, and help-surface evidence in
  `docs/JVS_AFSCP_DIRECT_LOCAL_EVIDENCE_2026-05-16.md`.
- Keep `docs/contracts/jvs-runner-contract-v1.md` aligned with direct argv and
  JSON fail-closed parsing.
- Replace this local pin with a formal JVS release URL, JVS binary artifact
  SHA-256, and signature bundle before GA/release packaging.

## Consequences

Positive:

- Prevents AFSCP from advertising old `v0.4.9` or restore-preview behavior as
  active.
- Makes the pre-GA local artifact explicit and auditable.
- Keeps active save/list/restore/clone/status/doctor paths on the fast direct
  contract.

Tradeoffs:

- The pin is local and dirty-source pre-GA evidence, not a production release
  supply-chain artifact.
- Docker builds must inject the verified local direct JVS binary until a formal
  JVS release exists.

Evidence:

- active local pin:
  `docs/JVS_AFSCP_DIRECT_LOCAL_EVIDENCE_2026-05-16.md`
- historical release context:
  `docs/JVS_PIN_EVIDENCE_2026-05-12-v0.4.9.md`
