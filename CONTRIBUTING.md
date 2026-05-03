# Contributing

This repository is currently a handoff scaffold for the AgentSmith FS Control Plane implementation.

Before starting implementation:

1. Read [docs/HANDOFF.md](docs/HANDOFF.md).
2. Confirm the MVP in [docs/MVP_PLAN.md](docs/MVP_PLAN.md).
3. Confirm the security boundaries in [docs/SECURITY_AND_TENANCY.md](docs/SECURITY_AND_TENANCY.md).
4. Confirm the draft API shape in [docs/API_CONTRACT_DRAFT.md](docs/API_CONTRACT_DRAFT.md).

Development rules:

- Do not expose JuiceFS metadata URLs or object store credentials to ordinary users, Desktop, or sandbox workloads.
- Do not make AFSCP the user permission authority. AgentSmith API owns product authorization.
- Do not implement cross-workspace template sharing or clone.
- Do not add merge semantics or ordinary single-writer enforcement in MVP.
- Keep implementation milestones small and tied to the P0 acceptance criteria.

The runtime language, framework, and build system have not been selected yet. Make that decision explicitly in a future ADR before adding application code.
