# Contract: JVS Runner V1

Status: current implementation baseline for the pre-GA direct AFSCP runner
contract. FINAL GA is governed by `docs/GA_RELEASE_GATES.md`,
`docs/READINESS_EVIDENCE.md`, and `scripts/verify-ga-release.sh`.

AFSCP integrates with JVS through CLI JSON output. For save point list/create,
direct restore, direct template/repo clone, explicit status, and explicit
doctor, the active contract is `jvs.afscp.direct.v1`.

## Version Pin

The active AFSCP pin is a pre-GA local direct-capable JVS build, not the old
`v0.4.9` release asset.

```text
version: pre-ga-local-afscp-direct-2026-05-18-r1
artifact: afscp-jvs-direct-local-linux-amd64
jvs binary artifact SHA-256: 8bc40b092355e29f8a8a852255b306d4d660c66f7dbd8581a402caa07cd64471
source ref: jvs@main:e0d6539e81c2da1e896ad3c5925f4e896840d281
```

This local source ref identifies the committed pre-GA JVS direct-command source
used by AFSCP. It is acceptable only for pre-GA convergence. Before GA/release
packaging, replace it with a formal JVS release URL, JVS binary artifact
SHA-256, and signature evidence.

## Active Direct Commands

AFSCP must use the paired selector `--control-root <control>` and
`--home <home>` for every direct command. `workspace main` is internal to the
direct v1 binding and is not an argv parameter.

| Capability | Active Argv Template | JSON Fields AFSCP Must Parse | Fail-Closed Behavior |
| --- | --- | --- | --- |
| direct save | `jvs afscp --control-root <control_root_path> --home <payload_home_path> save --message <message> [--purpose <purpose>] --json` | `contract:"jvs.afscp.direct.v1"`, `command:"save"`, `status`, `data.save_point_id`, `data.history_head`, `data.created_at`, `data.message`, optional `data.purpose` | Reject missing save point id, missing/mismatched history head, malformed JSON, old public-JVS envelope shapes, forbidden internal fields, raw paths, credentials, or command material. |
| direct list | `jvs afscp --control-root <control_root_path> --home <payload_home_path> list --json` | `command:"list"`, `status`, `data.history_head`, `data.save_points[].save_point_id`, optional message/timestamp/head marker | Fail closed on malformed JSON, forbidden internal fields, raw paths, credentials, or legacy `history --limit 0` envelope shapes. |
| direct restore | `jvs afscp --control-root <control_root_path> --home <payload_home_path> restore --save-point <save_point_id> --json` | `command:"restore"`, `status`, `data.restored_save_point_id`, optional `data.previous_head`, `data.new_head` | Restore the requested save point directly. Fail closed on missing restored id, missing/mismatched new head, malformed JSON, legacy preview/run/discard fields, `plan_id`, `restore_plan_id`, `run_command`, raw paths, credentials, or command material. |
| direct clone | `jvs afscp --control-root <source_control_root_path> --home <source_home_path> clone --target-control-root <target_control_root_path> --target-home <target_home_path> --json [--save-point <save_point_id>]` | `command:"clone"`, `status`, `data.source_repo_id`, `data.target_repo_id`, `data.save_point_id`, `data.save_points_copied_count` | Materialize template/repo targets from direct save point metadata. Fail closed on malformed JSON, missing ids, raw paths, credentials, command material, or any ordinary workspace dirty-check envelope. |
| direct status | `jvs afscp --control-root <control_root_path> --home <payload_home_path> status --json` | `command:"status"`, `status`, `data.history_head`, metadata state, active operation, recovery summary | Explicit metadata-only visibility/diagnostic command. It must not be called by default in save/restore hot paths. |
| direct doctor | `jvs afscp --control-root <control_root_path> --home <payload_home_path> doctor --json` | `command:"doctor"`, `status`, `data.repo_id`, `data.healthy`, findings, metadata state, journal, recovery summary | Explicit metadata-only diagnostic command. It must not be called by default in save/restore hot paths. |

AFSCP runner preflight verifies `jvs afscp --help` plus
`jvs afscp <save|list|restore|clone|status|doctor> --help` so root help that
uses a generic `<command>` placeholder does not hide missing subcommands.

## Non-Direct Legacy Boundary

The old public JVS restore lifecycle is not part of the active AFSCP contract:

- no restore preview,
- no restore run,
- no restore discard,
- no `--direct --discard-unsaved` compatibility adapter,
- no restore safety save point,
- no default post-restore `doctor` or `status` call.

`jvs --control-root <control_root_path> --workspace main ... --json` is not
allowed for save/list/restore/clone/status/doctor/template materialization. Repo
initialization remains the only non-direct JVS surface in AFSCP. Template
create/clone must use direct clone and must not reintroduce public `save`,
`history`, strict doctor, preview, run, discard, compatibility restore, or
ordinary `repo clone` behavior.

## Required Non-Direct Commands

| Capability | Argv Template | Boundary |
| --- | --- | --- |
| repo init | `jvs init <payload_root_path> --control-root <control_root_path> --workspace main --json` | Creates/adopts the external-control-root repo during repo create. |

## General Rules

- AFSCP invokes JVS with canonical internal repo paths and never relies on CWD
  discovery.
- Direct JSON must not echo HOME, control root, raw argv, JuiceFS internal
  paths, or any internal path.
- JVS output is captured and stored with operation records only after reduction
  to safe metadata. Raw stdout/stderr, absolute paths, credentials,
  `run_command`, and `recommended_next_command` must not be stored or returned
  verbatim.
- Mutating JVS commands run under the resource locks defined in
  `docs/contracts/operation-state-machine-v1.md`.
- Malformed JSON or old envelope shapes fail closed.
- Save/restore hot paths must not perform default doctor/status, payload tree
  scan, content digest, capacity pre-scan, compression, payload sync, or copy
  fallback.

## Direct Restore Recovery Contract

Before invoking direct restore, AFSCP owns a durable `restore` operation lease
and writer-session fence. On worker restart, recovery resumes only from durable
operation phase, lease, fence, and safe direct JVS evidence. AFSCP must not
attempt to adopt, consume, discard, or persist preview plans as part of current
direct restore recovery.

## Evidence

- Active local pin evidence:
  `docs/JVS_AFSCP_DIRECT_LOCAL_EVIDENCE_2026-05-18.md`
- Historical release evidence retained for old external-control-root context:
  `docs/JVS_PIN_EVIDENCE_2026-05-12-v0.4.9.md`
