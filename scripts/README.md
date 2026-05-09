# Scripts

## GA Release Verification

`scripts/verify-ga-release.sh` is the single authoritative repo-local
release gate entrypoint.

```bash
bash scripts/verify-ga-release.sh
```

The release gate runs release-only governance checks first: whitespace checks,
shell syntax checks for the release and baseline scripts, selector-driven
manifest verification, and focused contract/governance tests. It then runs
`scripts/verify-ga-baseline.sh` for the full local baseline, including the
contract verifier CLI and the full Go test suite.

The script selects verifier mode from
`docs/release-evidence/ga-release-selector.json`. With no authoritative final
selector, or with `release_intent=convergence_seed`, it runs seed/convergence
verification. When the selector exists with `release_intent=final_candidate`,
the same script must run final mode with `-selector docs/release-evidence/ga-release-selector.json`.
Direct `-mode final -check-only` validates structure only and cannot declare
final acceptance.

Final mode hard fails required/default `seed_gap_*_open` markers without exact
implemented or closed replacements. The default GA selector has
`claimed_optional_capabilities=[]`, so remaining workload, purge, template, and
optional fixture gaps are unselected optional/future gaps and do not block final.
If the selector declares an optional capability, the matching selected optional
gap hard-fails final mode with a machine finding and nonzero exit. The gate does
not depend on sibling repositories, external product acceptance, or manual
sign-off.

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
`scripts/verify-ga-release.sh` release gate and does not replace the
release-only governance checks.

Future scripts may include local dev setup, smoke tests, contract generation,
and release checks.
