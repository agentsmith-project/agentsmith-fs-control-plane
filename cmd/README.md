# Commands

The repo now has neutral Go command entrypoints:

- `afscp-api`: validates config, can build or serve the neutral API shell, and
  exposes `/healthz`, `/readyz`, route metadata fallback, standard error
  envelopes, request logging, and capability-denied guardrails. It does not
  implement real storage-backed endpoint handlers yet.
- `afscp-worker`: versioned placeholder entrypoint for the async operation
  runner. It has no durable queue or mutation execution loop yet.
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
