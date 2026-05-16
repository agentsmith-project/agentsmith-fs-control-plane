# JVS Integration

Status: current implementation baseline for pre-GA direct AFSCP integration
guidance.

Final GA is governed by `docs/GA_RELEASE_GATES.md`,
`docs/READINESS_EVIDENCE.md`, and `scripts/verify-ga-release.sh`.

AFSCP is the only ordinary JVS executor in the storage-control path.

## Integration Mode

AFSCP integrates through the JVS CLI with JSON output. The active
save/list/restore/status/doctor surface is the internal direct contract:

```bash
jvs afscp --control-root <control> --home <home> save --message <message> --json
jvs afscp --control-root <control> --home <home> list --json
jvs afscp --control-root <control> --home <home> restore --save-point <save_point_id> --json
jvs afscp --control-root <control> --home <home> status --json
jvs afscp --control-root <control> --home <home> doctor --json
```

The current active pin is the pre-GA local direct-capable artifact recorded in
`docs/JVS_AFSCP_DIRECT_LOCAL_EVIDENCE_2026-05-16.md`. The old `v0.4.9` release
evidence is historical only and must not be treated as active direct restore
support.

Do not reimplement JVS save, version restore, or clone semantics inside AFSCP.
Repo availability, archive, tombstone, and purge semantics are owned by the
AFSCP repo lifecycle contract.

## Required Commands

The active direct command expectations include:

- direct save point creation through `jvs afscp ... save --json`
- direct save point list/history through `jvs afscp ... list --json`
- direct version restore through `jvs afscp ... restore --save-point ... --json`
- explicit metadata-only status through `jvs afscp ... status --json`
- explicit metadata-only doctor through `jvs afscp ... doctor --json`

The active contract excludes restore preview, restore-run, restore discard,
`--direct --discard-unsaved`, automatic safety save points, and default
doctor/status calls in save or restore hot paths.

Repo lifecycle operations are GA storage-control operations implemented through
AFSCP durable lifecycle state, session drain, and the accepted tombstone/purge
storage contract. JVS lifecycle commands are optional implementation helpers
only after their external-control-root behavior is pinned; they are not the GA
source of lifecycle authority.

See:

- [contracts/jvs-runner-contract-v1.md](contracts/jvs-runner-contract-v1.md)
  for the self-contained AFSCP command matrix and fail-closed behavior.
- [JVS_AFSCP_DIRECT_LOCAL_EVIDENCE_2026-05-16.md](JVS_AFSCP_DIRECT_LOCAL_EVIDENCE_2026-05-16.md)
  for the current local pin evidence.

## External Control Root Mode

AFSCP-managed repos must use JVS external control roots.

Repo create command shape:

```bash
jvs init <payload_root_path> --control-root <control_root_path> --workspace main --json
```

Non-direct repo/template helper command shape:

```bash
jvs --control-root <control_root_path> --workspace main <command> --json
```

External control root rules for AFSCP:

- `payload_root_path` is the JVS `main` workspace folder and contains user files only.
- `control_root_path` contains JVS control metadata and is not mounted/exported.
- A bare payload folder cannot auto-discover the control root; AFSCP runner must pass explicit selectors.
- `--repo` is not the selector for external control root repos.
- Direct save/list/restore/status/doctor target selection is authoritative only
  through `--control-root <control_root_path>` plus
  `--home <payload_home_path>`.
- Non-direct `--workspace main` usage is limited to repo init and repo clone
  until those surfaces have direct AFSCP equivalents. Template source save,
  save point list/create, restore, status, and doctor use `jvs afscp`.

## Operation Rules

- Every mutating JVS action must have an AFSCP operation record.
- Mutating JVS actions must use resource locks.
- JVS JSON output stored with the operation record must be reduced to a safe
  summary and must not include absolute roots, raw stdout/stderr, commands, or
  secrets.
- AFSCP maps JVS errors into stable caller-visible error codes.
- Direct list must fail closed on malformed or old envelope output rather than
  returning partial or legacy history output.
- `jvs afscp status` and `jvs afscp doctor` are explicit metadata-only
  diagnostics, recovery aids, or smoke checks. They are not called by default
  after save or restore.
- The packaged JVS binary must match the active JVS binary artifact SHA-256 and
  source ref. Until a formal JVS release exists, Docker builds must inject the
  verified local direct-capable binary instead of downloading the old release
  asset.
- When `AFSCP_JVS_READY=true`, the API image needs the same direct-capable JVS
  binary config as workers because save-point list is JVS-backed in the
  internal API runtime.
