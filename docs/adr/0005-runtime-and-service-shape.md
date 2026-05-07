# ADR 0005: Use Go For The AFSCP Runtime

Status: accepted for development handoff

## Context

AFSCP is a privileged storage control plane. It needs an internal HTTP API,
durable background operation execution, filesystem/path safety, process
execution for JVS, WebDAV gateway behavior, Kubernetes-friendly deployment, and
strong testability without product-specific application dependencies.

The local development environment has Go available:

```text
go version go1.26.1-X:nodwarf5 linux/amd64
```

## Decision

Implement AFSCP in Go.

Initial service shape:

- `afscp-api`: internal HTTP API and health/readiness endpoints.
- `afscp-worker`: durable operation worker. It may run in the same binary as the
  API for local development, but the operation lease model must allow separate
  deployment later.
- `afscp-export-gateway`: WebDAV policy gateway. It may start as a package or
  sidecar-compatible binary, but must share the canonical path resolver and
  export session store.

Use the Go standard library first for HTTP, context, JSON, filesystem traversal,
process execution, and structured tests. Add third-party dependencies only when
they remove real risk. OpenAPI/server code generation is allowed after the
OpenAPI contract is accepted.

Packaging:

- one container image may contain all GA binaries
- build command: `go test ./...`
- lint command may be added after module bootstrap
- generated OpenAPI/schema artifacts are contract inputs, not runtime source of truth

## Consequences

Positive:

- Good fit for storage, filesystem, process, and HTTP gateway work.
- Easy static binaries for containers and operational tools.
- Strong compatibility with Kubernetes, CSI/orchestrator integration, and JVS
  runner process management.
- Lower runtime complexity than mixing API and storage workers across languages.

Tradeoffs:

- Caller integration code may still live in the calling product's language and
  repo.
- Product teams need generated clients instead of importing AFSCP packages.
- WebDAV gateway behavior must be carefully tested because Go libraries alone do
  not provide the AFSCP policy boundary.
