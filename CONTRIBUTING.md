# Contributing

This repository is currently a handoff scaffold for the AFSCP storage control plane implementation.

Before starting implementation:

1. Read [docs/HANDOFF.md](docs/HANDOFF.md).
2. Read [docs/DECOUPLING_REVIEW.md](docs/DECOUPLING_REVIEW.md).
3. Confirm the MVP in [docs/MVP_PLAN.md](docs/MVP_PLAN.md).
4. Confirm the security boundaries in [docs/SECURITY_AND_TENANCY.md](docs/SECURITY_AND_TENANCY.md).
5. Confirm the draft API shape in [docs/API_CONTRACT_DRAFT.md](docs/API_CONTRACT_DRAFT.md).

Development rules:

- Do not expose JuiceFS metadata URLs or object store credentials to ordinary clients or workloads.
- Do not make AFSCP the product authorization authority.
- Do not add caller-specific workflow concepts to AFSCP core.
- Do not implement cross-namespace template sharing or clone in P0.
- Do not add merge semantics or ordinary single-writer enforcement in MVP.
- Keep implementation milestones small and tied to the P0 acceptance criteria.

The runtime language, framework, and build system have not been selected yet. Make that decision explicitly in a future ADR before adding application code.
