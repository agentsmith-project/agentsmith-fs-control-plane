# Contributing

This repository is currently a GA implementation baseline for the AFSCP storage control plane implementation.

Before continuing implementation:

1. Read [docs/HANDOFF.md](docs/HANDOFF.md).
2. Read [docs/GA_PRE_DEV_READINESS.md](docs/GA_PRE_DEV_READINESS.md).
3. Read [docs/DEVELOPMENT_GOVERNANCE.md](docs/DEVELOPMENT_GOVERNANCE.md).
4. Read [docs/RISK_REGISTER.md](docs/RISK_REGISTER.md).
5. Confirm the security boundaries in [docs/SECURITY_AND_TENANCY.md](docs/SECURITY_AND_TENANCY.md).
6. Confirm the draft API shape in [docs/API_CONTRACT_DRAFT.md](docs/API_CONTRACT_DRAFT.md).

Development rules:

- Do not expose JuiceFS metadata URLs or object store credentials to ordinary clients or workloads.
- Do not make AFSCP the product authorization authority.
- Do not add caller-specific workflow concepts to AFSCP core.
- Do not implement ordinary cross-namespace template sharing or clone for GA.
- Do not add merge semantics or ordinary single-writer enforcement for GA.
- Only implement or modify endpoint handlers or storage mutations through accepted schemas, OpenAPI, auth, JVS, operation, audit, export, mount, writer-session contracts, and focused tests.
- Tie implementation PRs to accepted ADRs, contracts, and GA admission evidence.

The runtime language and initial service shape are selected in [ADR 0005](docs/adr/0005-runtime-and-service-shape.md). New implementation PRs should follow [docs/DEVELOPER_HANDOFF.md](docs/DEVELOPER_HANDOFF.md).
