# GA Release Gates

Status: active seed/baseline evidence gate definition.

AFSCP currently has one repo-local seed/baseline convergence gate:

```bash
bash scripts/verify-ga-release.sh
```

The command runs the release evidence manifest verifier in `-mode seed` today.
A successful exit means the current repo-local seed/baseline convergence checks
passed; it does not mean final GA release acceptance has passed. Final GA
release acceptance must use this same unique repo-local entrypoint and evaluate
final acceptance, for example by switching the manifest verifier to or including
`-mode final`. Any required/final claim, acceptance item, or evidence entry that
still carries a required/default `seed_gap_*_open` marker without an exact
implemented/closed replacement, or an equivalent unresolved required/default
seed-gap marker, must fail final acceptance. Selected optional positive gaps
block final only when the authoritative final selector claims that optional
capability. Unselected optional positive gaps may remain visible as
future/fixture work and must not become default GA blockers. A seed-mode pass
alone is only current repo-local seed/baseline evidence; it is never sufficient
for final GA release. In this repo, final mode requires no unresolved
required/default seed-gap markers and no unresolved selected optional positive
seed-gap markers; unselected optional future/fixture gaps may remain visible.
The phrase "no open seed gaps" is a legacy shorthand for that final-mode rule.
Manual review, consumer adoption, sibling project status, generated-client
approval, security approval, owner approval, and runbook meetings are not
independent GA gate conditions.

The command may call lower-level checks such as `git diff --check`, `go test
-count=1 ./...`, contract verification, schema/OpenAPI drift checks,
documentation guards, and focused package tests. A check counts as current
repo-local evidence only when it is represented by a repo-local script, test,
contract, schema, OpenAPI artifact, runbook document, or documented fixture
covered by that command.

Optional capability positive evidence is final-blocking only when the manifest
entry explicitly declares it as required fixture conformance: the evidence must
use the `repo-local-fixture-enabled` profile and declare
`fixture_enabled_mode=true`, `default_mode=false`, `optional_gated=true`, and
`required=true`. A plain seed gap marker or a fixture test listed for future
closure does not make that optional capability a default GA requirement.

## Profile Boundary

Default evidence, `repo-local-fixture-enabled` evidence, and
`deployment-runtime-support` evidence are separate release profiles. Default
evidence must stay `evidence_profile=default`, `default_mode=true`,
`fixture_enabled_mode=false`, and `optional_gated=false`; it must not be closed
by fixture-enabled positive evidence or deployment runtime support evidence. A
selected optional positive becomes final-blocking only through the authoritative
selector `claimed_optional_capabilities` and must use the exact fixture-enabled
shape above. Deployment runtime support may provide repo-local schema, doc,
audit, or runbook guard evidence, but it must not close default evidence and
must not close selected optional fixture conformance.

Owner roles in the active docs identify maintenance responsibility. They do not
add role-approval conditions to the GA release gate.

Runtime operator controls remain product behavior, not GA release workflow.
For example, `operator_intervention_required`, `operator_admin`, caller
approval references, purge approval references, and audited operator actions
are safety controls that the automated checks must preserve.
