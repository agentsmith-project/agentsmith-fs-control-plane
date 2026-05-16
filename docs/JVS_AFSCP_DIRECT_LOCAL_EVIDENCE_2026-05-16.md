# JVS AFSCP Direct Local Evidence 2026-05-16

Status: current pre-GA AFSCP JVS implementation pin evidence.

This artifact records the active local direct-capable JVS build used by AFSCP
until formal JVS release provenance exists. It identifies only the JVS binary
artifact; it does not attest to payload contents, GA release signoff, or
replace release provenance.

## Local Artifact

```text
version: pre-ga-local-afscp-direct-2026-05-16
artifact: afscp-jvs-direct-local-linux-amd64
binary evidence path: /tmp/afscp-jvs-direct-local
JVS binary artifact SHA-256: affa86a08dbb2195f594be0be01e9c3f128806f75d04826030afbe4ba283f2e2
source ref: jvs@main:eb026cc48efb57ef64c9f3e482f0011b9232701b
```

The source ref points at the local pre-GA JVS commit that provides the active
`jvs.afscp.direct.v1` implementation. AFSCP must replace this local pin with a
formal release artifact before GA.

## Build And Artifact Identity Evidence

Observed commands:

```bash
go build -o /tmp/afscp-jvs-direct-local ./cmd/jvs
sha256sum /tmp/afscp-jvs-direct-local
```

Observed JVS binary artifact SHA-256:

```text
affa86a08dbb2195f594be0be01e9c3f128806f75d04826030afbe4ba283f2e2  /tmp/afscp-jvs-direct-local
```

## Help Surface Evidence

Root help:

```bash
/tmp/afscp-jvs-direct-local afscp --help
```

Observed root usage includes:

```text
jvs afscp --control-root <control_root_path> --home <payload_home_path> <command> --json
```

Subcommand help was checked for:

```bash
/tmp/afscp-jvs-direct-local afscp save --help
/tmp/afscp-jvs-direct-local afscp list --help
/tmp/afscp-jvs-direct-local afscp restore --help
/tmp/afscp-jvs-direct-local afscp status --help
/tmp/afscp-jvs-direct-local afscp doctor --help
```

Observed required flags:

- `save`: `--message`, `--control-root`, `--home`, `--json`
- `list`: `--control-root`, `--home`, `--json`
- `restore`: `--save-point`, `--control-root`, `--home`, `--json`
- `status`: `--control-root`, `--home`, `--json`
- `doctor`: `--control-root`, `--home`, `--json`

## Active AFSCP Contract

AFSCP active direct commands are:

```bash
jvs afscp --control-root <control> --home <home> save --message <message> --json
jvs afscp --control-root <control> --home <home> list --json
jvs afscp --control-root <control> --home <home> restore --save-point <save_point_id> --json
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

- direct runner argv and JSON parsing for save/list/restore/status/doctor,
- fail-closed rejection of old public JVS envelopes and forbidden fields,
- save point executor using direct list/save only,
- restore executor using direct restore only,
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
