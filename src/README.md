# Source

Application source code has not been added yet.

The first implementation PR should follow [docs/DEVELOPER_HANDOFF.md](../docs/DEVELOPER_HANDOFF.md) and include:

- Go module bootstrap from ADR 0005.
- Service skeleton.
- Health endpoint.
- Config loading.
- Structured logging.
- Operation store migration or schema.
- No storage mutation until security and API contracts are reviewed.
- No caller-specific business concepts in core packages.
