# JVS Smoke Evidence 2026-05-05 v0.4.8

Status: accepted evidence for JVS runner gate G-005.

Release: https://github.com/agentsmith-project/jvs/releases/tag/v0.4.8

API release metadata:

- tag: `v0.4.8`
- draft: `false`
- prerelease: `false`
- target_commitish: `main`

Pinned asset:

- asset: `jvs-linux-amd64`
- size: `8586255`
- URL: https://github.com/agentsmith-project/jvs/releases/download/v0.4.8/jvs-linux-amd64
- SHA-256 from `SHA256SUMS`: `f011699fa92abae59e70153d32f3b9a10de1159fc23a390b22208db23f965521`

Verification:

- `sha256sum -c --ignore-missing SHA256SUMS` passed with `jvs-linux-amd64: OK`.
- Matching `jvs-linux-amd64.bundle` and `SHA256SUMS.bundle` assets exist.
- Local `cosign` was not installed, so bundle presence was recorded but not
  locally verified.
- The CLI has no `--version`; this smoke does not require one.

Smoke root: `/tmp/afscp-jvs-smoke-v0.4.8.H6DaIB`

CLI contract observation:

- `--control-root` cannot be combined with `--repo`.
- After `init`, run commands from the payload root CWD with
  `--control-root <control> --workspace main --json`.

Smoke results:

- External control-root `init` passed; payload `.jvs` was absent.
- `doctor --help` confirmed `doctor --repair-runtime` exists; smoke did not
  need repair because restore cleanup was already correct.
- Baseline and second `save` passed; history count was `2`.
- Restore preview to baseline passed with plan ID
  `f0cc8c6e-6fcd-4b9d-b441-c83118bcc11d`.
- `restore --run` passed and file content returned to baseline.
- After restore-run, `jvs recovery status` returned `ok=true` with `plans: []`.
- After restore-run, `jvs doctor --strict` returned `ok=true`, `healthy=true`,
  and no findings.
- `jvs repo clone <target> --target-control-root <target-control> --save-points
  main` returned `ok=true`, `runtime_state_copied=false`, and
  `save_points_copied_count=2`; clone payload `.jvs` was absent.
- Clone `doctor --strict` returned `ok=true`, `healthy=true`, and no findings.
- Clone history showed both save points, and viewing the second save point
  content worked.

Preview/discard cleanup smoke:

- Smoke root: `/tmp/afscp-jvs-smoke-v0.4.8-preview-discard.Nhx6nK`.
- Restore preview passed with plan ID
  `b644aec4-bcb6-4480-b5fa-a283927dd3cd`.
- During the pending preview, `jvs recovery status` returned `ok=true`;
  `restore_state.state=pending_restore_preview`,
  `restore_state.blocking=true`, and a recommended `restore --run` command.
  The `plans` array was empty, so consumers must inspect `restore_state`.
- During the pending preview, `jvs doctor --strict` exited `1` with JSON
  `ok=true`, `healthy=false`; check `recovery_state` failed with
  `E_RECOVERY_BLOCKING` and a recommended next command.
- During the pending preview, `jvs repo clone` exited `1` with JSON `ok=false`
  and error code `E_RECOVERY_BLOCKING`; no clone succeeded.
- `restore discard b644aec4-bcb6-4480-b5fa-a283927dd3cd` returned `ok=true`,
  `plan_discarded=true`, `files_changed=false`, and
  `history_changed=false`.
- After discard, `jvs recovery status` returned no `restore_state`, and
  `jvs doctor --strict` returned `healthy=true`.

Decision:

JVS v0.4.8 resolves the v0.4.7 restore-run recovery plan residual blocker for
AFSCP pre-dev admission. G-005 is closed on this evidence.

This only closes the JVS runner gate. Real AFSCP storage mutation still requires
accepted contracts, fences, session drain, operation leases, audit behavior, and
focused tests before implementation or GA claims. AFSCP should use public JVS
commands such as `restore discard` for preview cleanup and must not delete
private JVS files directly.
