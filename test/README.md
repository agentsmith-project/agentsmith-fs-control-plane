# Test Suites

This top-level test suites directory currently documents the future dedicated
conformance, integration, and smoke layout. Primary automated coverage now
lives in Go package tests, including package-level tests and
`internal/api/caller_runtime_boundary_e2e_test.go`.

Dedicated suites remain a GA should-have.

Suggested future layout:

- `conformance`: contract and security conformance tests.
- `integration`: JuiceFS/JVS/orchestrator integration tests.
- `smoke`: end-to-end P0 smoke tests.
