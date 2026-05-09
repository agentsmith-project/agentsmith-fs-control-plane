# AFSCP Rollback/Roll-Forward Policy Reference

This document is a release recovery policy reference for the AFSCP default GA
final selector.

It is not production deployment proof, not a manual approval, and not evidence
that a production rollback or roll-forward has already been performed.

## Scope

- Applies to AFSCP release evidence and verifier recovery decisions only.
- Uses repo-local artifacts, release scripts, schema/API contracts, and
  manifest evidence as the authoritative inputs.
- Does not depend on external repositories, business workflow gates, meetings,
  or deployment environment state.

## Recovery Decision

When a final-candidate verification failure is found, the release owner chooses
one of two machine-verifiable recovery paths:

- Roll forward by adding or correcting repo-local evidence, schema/API contract
  changes, verifier logic, or tests, then rerun `scripts/verify-ga-release.sh`.
- Roll back by removing or reverting the release-candidate artifact change that
  introduced the failing evidence state, then rerun `scripts/verify-ga-release.sh`.

The recovery path is acceptable only after the authoritative release gate passes
without ignored findings or alternate approval gates.
