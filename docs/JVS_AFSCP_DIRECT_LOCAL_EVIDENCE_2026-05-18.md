# JVS AFSCP Direct Local Evidence 2026-05-18

Status: historical pre-GA AFSCP JVS implementation pin evidence. The current
active release pin is `docs/JVS_AFSCP_DIRECT_RELEASE_EVIDENCE_2026-05-18.md`.

This artifact records the former local direct-capable JVS build used during
pre-GA development before JVS v0.4.10 was published. It is retained only as
historical evidence and must not be used as a release dependency.

## Local Artifact

```text
version: pre-ga-local-afscp-direct-2026-05-18-r1
artifact: afscp-jvs-direct-local-linux-amd64
binary evidence path: dist/jvs-linux-amd64
JVS binary artifact SHA-256: 8bc40b092355e29f8a8a852255b306d4d660c66f7dbd8581a402caa07cd64471
source ref: jvs@main:e0d6539e81c2da1e896ad3c5925f4e896840d281
```

The source ref points at the local pre-GA JVS commit that first provided the
`jvs.afscp.direct.v1` implementation. The active AFSCP release dependency is
now the published JVS v0.4.10 artifact recorded in
`docs/JVS_AFSCP_DIRECT_RELEASE_EVIDENCE_2026-05-18.md`.

## Build And Artifact Identity Evidence

Observed commands:

```bash
make release-build
cp <jvs_checkout>/bin/jvs-linux-amd64 dist/jvs-linux-amd64
sha256sum dist/jvs-linux-amd64
```

Observed JVS binary artifact SHA-256:

```text
8bc40b092355e29f8a8a852255b306d4d660c66f7dbd8581a402caa07cd64471  dist/jvs-linux-amd64
```

## Help Surface Evidence

Root help:

```bash
dist/jvs-linux-amd64 afscp --help
```

Observed root usage includes:

```text
jvs afscp --control-root <control_root_path> --home <payload_home_path> <command> --json
```

Subcommand help was checked for:

```bash
dist/jvs-linux-amd64 afscp save --help
dist/jvs-linux-amd64 afscp list --help
dist/jvs-linux-amd64 afscp restore --help
dist/jvs-linux-amd64 afscp clone --help
dist/jvs-linux-amd64 afscp status --help
dist/jvs-linux-amd64 afscp doctor --help
```

Observed required flags:

- `save`: `--message`, `--purpose`, `--control-root`, `--home`, `--json`
- `list`: `--control-root`, `--home`, `--json`
- `restore`: `--save-point`, `--control-root`, `--home`, `--json`
- `clone`: `--target-control-root`, `--target-home`, `--control-root`, `--home`, `--json`
- `status`: `--control-root`, `--home`, `--json`
- `doctor`: `--control-root`, `--home`, `--json`

## Active AFSCP Contract

AFSCP active direct commands are:

```bash
jvs afscp --control-root <control> --home <home> save --message <message> [--purpose <purpose>] --json
jvs afscp --control-root <control> --home <home> list --json
jvs afscp --control-root <control> --home <home> restore --save-point <save_point_id> --json
jvs afscp --control-root <source_control> --home <source_home> clone --target-control-root <target_control> --target-home <target_home> --json [--save-point <save_point_id>]
jvs afscp --control-root <control> --home <home> status --json
jvs afscp --control-root <control> --home <home> doctor --json
```

Removed from the active contract:

- restore preview,
- restore run,
- restore discard,
- `--direct --discard-unsaved`,
- default post-restore doctor/status calls.

## AFSCP Closure Evidence

Focused AFSCP tests cover:

- direct runner argv and JSON parsing for save/list/restore/clone/status/doctor,
- fail-closed rejection of old public JVS envelopes and forbidden fields,
- save point executor using direct list/save only,
- restore executor using direct restore only,
- template create/clone executors using direct clone only,
- worker/API config accepting the current direct-capable local pin,
- runner/repoexec structural guards rejecting legacy public save/history,
  strict doctor, and restore plan fields from the active direct surface.

Focused verification commands:

```bash
go test -count=1 ./internal/jvsrunner ./internal/repoexec ./internal/api ./internal/workerapp ./internal/contractcheck
go run ./cmd/afscp-contract-verify -openapi api/openapi/internal-v1.openapi.yaml -schema api/schemas/afscp-internal-v1.schema.json -api-contract docs/contracts/afscp-internal-api-v1.md -api-draft docs/API_CONTRACT_DRAFT.md
go test -count=1 ./internal/releaseevidence ./cmd/afscp-evidence-verify
git diff --check
```
