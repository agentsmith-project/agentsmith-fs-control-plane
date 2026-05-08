# Scripts

## Current GA Convergence Verification

`scripts/verify-ga-release.sh` is the single authoritative repo-local
convergence gate for the current seed/baseline evidence mode. It invokes the
release evidence manifest verifier with `-mode seed`; a zero exit code means the
current repo-local seed/baseline checks passed, not that final GA release
acceptance has passed.

```bash
bash scripts/verify-ga-release.sh
```

The convergence gate runs release-only governance checks first: whitespace
checks, shell syntax checks for the release and baseline scripts, the seed-mode
manifest check, and focused contract/governance tests. It then runs
`scripts/verify-ga-baseline.sh` for the full local baseline, including the
contract verifier CLI and the full Go test suite. The future final GA release
acceptance must use this same unique repo-local entrypoint and evaluate final
acceptance, for example by switching the manifest verifier to or including
`-mode final`. Any required/final claim, acceptance item, or evidence entry
with an open `seed_gap_*_open` marker or equivalent open seed gap must fail
final acceptance; final mode requires no open seed gaps, and a seed-mode pass
alone is not enough. It does not depend on sibling repositories, external
product acceptance, or manual sign-off.

Optional fixture positive evidence is final-blocking only when the manifest
entry explicitly declares fixture conformance with
`evidence_profile=repo-local-fixture-enabled`, `fixture_enabled_mode=true`,
`default_mode=false`, `optional_gated=true`, and `required=true`. A seed gap
marker by itself is not a default GA requirement.

## Local GA Baseline Verification

`scripts/verify-ga-baseline.sh` is the local GA implementation baseline
verification entrypoint for this repo. It runs the current local test and
contract gates:

```bash
bash scripts/verify-ga-baseline.sh
```

Passing this script means the local implementation baseline checks passed. The
baseline script is intentionally lower-level than the authoritative
`scripts/verify-ga-release.sh` convergence gate and does not replace the
release-only governance checks.

Future scripts may include local dev setup, smoke tests, contract generation,
and release checks.
