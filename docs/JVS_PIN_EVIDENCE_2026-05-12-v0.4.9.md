# JVS Pin Evidence 2026-05-12 v0.4.9

Status: current AFSCP JVS implementation pin evidence.

This artifact records the active JVS release pin consumed by AFSCP config,
runner contracts, and readiness references. It is release-pin evidence, not a
full GA release signoff by itself.

## Release

- Release: https://github.com/agentsmith-project/jvs/releases/tag/v0.4.9
- Asset: `jvs-linux-amd64`
- SHA-256 from `SHA256SUMS`:
  `0a1c6896cecf85ec2ac4e15e1c29f6e3f8cf09b9a4db48a516559604f0e7e944`

## Verification

The official checksum was checked from the release `SHA256SUMS`:

```bash
curl -fsSL https://github.com/agentsmith-project/jvs/releases/download/v0.4.9/SHA256SUMS | rg 'jvs-linux-amd64'
```

Observed:

```text
0a1c6896cecf85ec2ac4e15e1c29f6e3f8cf09b9a4db48a516559604f0e7e944  jvs-linux-amd64
```

## Runner Contract Delta

JVS v0.4.9 is the active pin for external-control-root runner behavior,
including:

- ordinary strict validation:
  `jvs --control-root <control_root_path> --workspace main doctor --strict --json`
- stale repository mutation lock cleanup after save `E_REPO_BUSY` only:
  `jvs --control-root <control_root_path> --workspace main doctor --strict --repair-runtime --json`

AFSCP must parse the runtime repair result, require a successful `clean_locks`
repair, and retry save only after the repair command succeeds. If repair cannot
prove safe cleanup, the save point operation remains terminal `failed` and
retryable. AFSCP must not delete JVS lock files directly.

## AFSCP Repair Path Closure

Focused fake-runner coverage closes the AFSCP side of the v0.4.9 repair
contract without requiring a sibling checkout:

- `TestDoctorRepairRuntimeUsesExternalControlCommandAndParsesRepairs` verifies
  the exact `doctor --strict --repair-runtime --json` argv and successful
  `clean_locks` parsing.
- `TestSavePointExecutorRetriesTransientRepoBusySave` verifies save
  `E_REPO_BUSY` -> repair-runtime `clean_locks` -> retry save -> succeeded.
- `TestSavePointExecutorRepoBusyFailsTerminallyWithoutManualIntervention`
  verifies repair failure -> terminal `failed` save point operation with
  retryable `JVS_COMMAND_FAILED` details.

Focused verification commands for this AFSCP closure:

```bash
go test -count=1 ./internal/repoexec ./internal/jvsrunner ./internal/store ./internal/workerapp ./internal/contractcheck ./cmd/afscp-contract-verify
go run ./cmd/afscp-contract-verify -openapi api/openapi/internal-v1.openapi.yaml -schema api/schemas/afscp-internal-v1.schema.json -api-contract docs/contracts/afscp-internal-api-v1.md -api-draft docs/API_CONTRACT_DRAFT.md
```

Historical v0.4.8 smoke evidence remains useful only as historical context and
is not the active pin.
