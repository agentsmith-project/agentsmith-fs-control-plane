# JVS AFSCP Direct Release Evidence 2026-05-18

Status: current AFSCP JVS release pin evidence.

This artifact records the published JVS release artifact consumed by AFSCP
release images. It identifies the JVS binary artifact and the source ref used
for the active direct AFSCP contract; it does not attest to caller payload
contents.

## Release Artifact

```text
version: v0.4.10
artifact: jvs-linux-amd64
release URL: https://github.com/agentsmith-project/jvs/releases/tag/v0.4.10
binary URL: https://github.com/agentsmith-project/jvs/releases/download/v0.4.10/jvs-linux-amd64
JVS binary artifact SHA-256: fa4ada8e3353f85679d13870ea53307caafbd8217b04ba576b185105d9178cef
source ref: jvs@v0.4.10:6a0f762bc436f0d3dc7c7c1d60847992c3a82718
GitHub Actions run: https://github.com/agentsmith-project/jvs/actions/runs/26012602687
```

## Artifact Identity Evidence

Observed commands:

```bash
gh release download v0.4.10 --repo agentsmith-project/jvs --pattern SHA256SUMS --pattern jvs-linux-amd64 --dir /tmp/jvs-v0.4.10
cd /tmp/jvs-v0.4.10
sha256sum --check --ignore-missing SHA256SUMS
sha256sum jvs-linux-amd64
```

Observed JVS binary artifact SHA-256:

```text
fa4ada8e3353f85679d13870ea53307caafbd8217b04ba576b185105d9178cef  jvs-linux-amd64
```

## Help Surface Evidence

Root help:

```bash
/tmp/jvs-v0.4.10/jvs-linux-amd64 afscp --help
```

Observed root usage includes:

```text
jvs afscp --control-root <control_root_path> --home <payload_home_path> <command> --json
```

Subcommand help was checked for:

```bash
/tmp/jvs-v0.4.10/jvs-linux-amd64 afscp save --help
/tmp/jvs-v0.4.10/jvs-linux-amd64 afscp list --help
/tmp/jvs-v0.4.10/jvs-linux-amd64 afscp restore --help
/tmp/jvs-v0.4.10/jvs-linux-amd64 afscp clone --help
/tmp/jvs-v0.4.10/jvs-linux-amd64 afscp status --help
/tmp/jvs-v0.4.10/jvs-linux-amd64 afscp doctor --help
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
- worker/API config accepting the current published JVS pin,
- runner/repoexec structural guards rejecting legacy public save/history,
  strict doctor, and restore plan fields from the active direct surface.

Focused verification commands:

```bash
go test -count=1 ./internal/jvsrunner ./internal/repoexec ./internal/api ./internal/workerapp ./internal/contractcheck
go run ./cmd/afscp-contract-verify -openapi api/openapi/internal-v1.openapi.yaml -schema api/schemas/afscp-internal-v1.schema.json -api-contract docs/contracts/afscp-internal-api-v1.md -api-draft docs/API_CONTRACT_DRAFT.md
go test -count=1 ./internal/releaseevidence ./cmd/afscp-evidence-verify
git diff --check
```
