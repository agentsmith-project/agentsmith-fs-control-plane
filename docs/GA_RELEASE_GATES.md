# GA Release Gates

Status: active selector-driven GA release gate definition.

AFSCP has one authoritative repo-local GA release entrypoint:

```bash
bash scripts/verify-ga-release.sh
```

The script is the only release gate entrypoint. It selects the manifest verifier
mode from `docs/release-evidence/ga-release-selector.json`. When that selector
is absent, or is present with `release_intent=convergence_seed`, the script runs
seed/convergence verification. When the selector exists with
`release_intent=final_candidate`, the same script must invoke the manifest
verifier in final mode with `-selector docs/release-evidence/ga-release-selector.json`.

Final mode rejects any required/default `seed_gap_*_open` marker without an
exact implemented or closed replacement, and a selected optional gap produces a
machine finding with a nonzero final-mode exit when the authoritative selector
declares the corresponding optional capability. The default GA selector in this
repo uses `claimed_optional_capabilities=[]`; the remaining workload, purge,
template, and optional fixture gaps are therefore unselected optional/future
gaps and do not block final. Once the selector claims an optional capability,
the matching selected optional gap hard-fails final mode with a machine finding
and nonzero exit. A direct `-mode final -check-only` run is only a structural
check and cannot declare final acceptance; final acceptance comes only from
`bash scripts/verify-ga-release.sh` running final mode without `-check-only`.

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

## Workflow Hardening

`scripts/verify-ga-release.sh` is the repo-local authoritative release entrypoint.
The workflow must call only this script for release acceptance and
must not bypass it by invoking the manifest verifier, baseline checks, final
mode, generated reports, or copied evidence directly. The release script must
run the manifest verifier and the baseline gate; workflow YAML is only the
repo-local trigger for that script.

Final intent is selected only by the authoritative selector
`docs/release-evidence/ga-release-selector.json` with
`release_intent=final_candidate`. Operators must not run final mode directly
from workflow YAML, and must not use `-check-only` as final acceptance. The
workflow must not use -check-only as final acceptance.
The generated report, digest, or copy artifacts are outputs and must not become
same-run authoritative input.

Do not add person-driven release conditions, role signoff states, hosted workflow
environment protections, deployment or runtime status checks, sibling-repository
status, generated-client review status, security-review status, or runbook
meeting outcomes as alternate GA release gates.

Owner roles in the active docs identify maintenance responsibility. They do not
add role-approval conditions to the GA release gate.

Runtime operator controls remain product behavior, not GA release workflow.
For example, `operator_intervention_required`, `operator_admin`, caller
approval references, purge approval references, and audited operator actions
are safety controls that the automated checks must preserve.
