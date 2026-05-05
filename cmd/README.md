# Commands

The repo now has neutral Go command entrypoints:

- `afscp-api`: validates config, can build or serve the neutral API shell, and
  exposes `/healthz`, `/readyz`, route metadata fallback, standard error
  envelopes, request logging, and capability-denied guardrails. It does not
  implement real storage-backed endpoint handlers yet.
- `afscp-worker`: bounded async worker entrypoint. `--run-once` defaults to
  fail-closed unless `AFSCP_WORKER_OPERATION_RECOVERY_ENABLED=true`; when
  enabled it wires PostgreSQL operation recovery for the minimal
  `namespace_upsert` executor only. A run-once pass emits its summary, then
  exits nonzero if operation recovery reports unsupported, manual, or failed
  records.
- `afscp-export-gateway`: versioned placeholder entrypoint for the WebDAV
  gateway. It has no WebDAV file access or export-session enforcement yet.
- `afscp-contract-verify`: verifies selected OpenAPI, schema, docs, and Go DTO
  contract guardrails.

Useful checks:

```bash
go test -count=1 ./...
go run ./cmd/afscp-contract-verify \
  -openapi api/openapi/internal-v1.openapi.yaml \
  -schema api/schemas/afscp-internal-v1.schema.json \
  -api-contract docs/contracts/afscp-internal-api-v1.md \
  -api-draft docs/API_CONTRACT_DRAFT.md
```
