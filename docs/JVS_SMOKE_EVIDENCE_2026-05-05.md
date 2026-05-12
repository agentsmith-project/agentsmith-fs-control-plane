# JVS Smoke Evidence 2026-05-05

Status: historical blocker evidence for JVS runner gate G-005.

Superseded by historical v0.4.8 evidence in
`docs/JVS_SMOKE_EVIDENCE_2026-05-05-v0.4.8.md`. The active AFSCP JVS pin is
v0.4.9 in `docs/JVS_PIN_EVIDENCE_2026-05-12-v0.4.9.md`.

## Environment

```text
AFSCP repo: agentsmith-fs-control-plane
JVS release: https://github.com/agentsmith-project/jvs/releases/tag/v0.4.7
JVS version: v0.4.7
JVS asset: jvs-linux-amd64
JVS asset sha256: 4ec030d62b24d980192af550d04cb4b7455299b285b68c91843386cfa5157e6d
JVS signature bundle: jvs-linux-amd64.bundle
Smoke host: linux/amd64
Runner CWD: /tmp, outside any JVS repo
```

The release page publishes binaries, matching `.bundle` files, `SHA256SUMS`,
and `SHA256SUMS.bundle`. The smoke test downloaded the release asset directly
from GitHub and verified the `jvs-linux-amd64` checksum above before execution.

Local source trees and local binaries are not the AFSCP handoff pin. They may
be useful for reading JVS docs, but release binary evidence is authoritative
for GA handoff.

## Passing Observations

The release binary exposes the required command surface:

- `init`
- `save`
- `history`
- `restore`
- `recovery`
- `repo clone`
- `doctor`
- global `--control-root`
- global `--workspace`
- global `--json`

External control-root smoke with a temporary repo proved:

- `jvs init <payload> --control-root <control> --workspace main --json` works.
- `jvs save -m <message> --json` works.
- `jvs history` can emit JSON; the current AFSCP runner contract uses
  `jvs history --limit 0 --json` for complete history.
- `jvs repo clone <target_payload> --target-control-root <target_control> --save-points main --json` works before restore-run.
- `jvs doctor --strict --json` reports clone health before restore-run.
- `payload/.jvs` is absent for external-control-root repos.

## Blocking Observation

After restore preview and restore-run:

```text
jvs --control-root <control> --workspace main restore <save_point> --json
jvs --control-root <control> --workspace main restore --run <plan_id> --json
```

`restore --run` returned `ok=true`, but a restore plan remained in the control
root. Subsequent `doctor --strict` returned non-zero and JSON with command
`ok=true` but resource health false:

```json
{
  "name": "recovery_state",
  "status": "failed",
  "error_code": "E_RECOVERY_BLOCKING",
  "message": "Restore plan <plan_id> is pending."
}
```

Release-binary smoke sample:

```text
sha256=4ec030d62b24d980192af550d04cb4b7455299b285b68c91843386cfa5157e6d
save1=1777950873714-70bc440e
save2=1777950873731-ec7bebc1
plan=6c7a72d4-33cb-4d6b-b193-93d10e90f0f0
doctor_status=1
doctor error_code=E_RECOVERY_BLOCKING
doctor healthy=false
```

Earlier smoke also showed a subsequent `repo clone` can fail with:

```json
{
  "code": "E_RECOVERY_BLOCKING",
  "message": "Cannot clone: source restore plan <plan_id>.json is pending."
}
```

`jvs recovery status --json` did not report an unresolved plan, even though the
restore plan file still blocked doctor and clone.

## Decision

G-005 cannot be closed from this evidence. Before implementing AFSCP storage
mutation handlers, the JVS owner and AFSCP maintainer must either:

- fix JVS restore-run so completed restore plans do not block doctor/clone, or
- document and test a safe JVS command-level cleanup/resolution path that AFSCP
  may call without inspecting private JVS files.

AFSCP must not delete JVS private restore-plan files directly in GA.
