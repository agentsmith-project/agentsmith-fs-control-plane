# Scripts

## Authoritative GA Release Verification

`scripts/verify-ga-release.sh` is the authoritative repo-local GA release gate.
Its exit code is the objective GA decision for this repository: zero means the
release gate passed, and non-zero means it did not pass.

```bash
bash scripts/verify-ga-release.sh
```

The release gate runs release-only governance checks first: whitespace checks,
shell syntax checks for the release and baseline scripts, and focused
contract/governance tests. It then runs `scripts/verify-ga-baseline.sh` for the
full local baseline, including the contract verifier CLI and the full Go test
suite. It does not depend on sibling repositories, external product acceptance,
or manual sign-off.

## Local GA Baseline Verification

`scripts/verify-ga-baseline.sh` is the local GA implementation baseline
verification entrypoint for this repo. It runs the current local test and
contract gates:

```bash
bash scripts/verify-ga-baseline.sh
```

Passing this script means the local implementation baseline checks passed. The
baseline script is intentionally lower-level than the authoritative
`scripts/verify-ga-release.sh` gate and does not replace the release-only
governance checks.

Future scripts may include local dev setup, smoke tests, contract generation,
and release checks.
