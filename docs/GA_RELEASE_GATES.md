# GA Release Gates

Status: active GA gate definition.

AFSCP has one GA release gate:

```bash
bash scripts/verify-ga-release.sh
```

GA is releasable only when this repo-local command exits successfully from a
clean checkout. The command is the objective gate boundary; manual review,
consumer adoption, sibling project status, generated-client approval, security
approval, owner approval, and runbook meetings are not independent GA gate
conditions.

The command may call lower-level checks such as `git diff --check`, `go test
-count=1 ./...`, contract verification, schema/OpenAPI drift checks,
documentation guards, and focused package tests. A check counts as GA evidence
only when it is represented by a repo-local script, test, contract, schema,
OpenAPI artifact, runbook document, or documented fixture covered by that
command.

Owner roles in the active docs identify maintenance responsibility. They do not
add role-approval conditions to the GA release gate.

Runtime operator controls remain product behavior, not GA release workflow.
For example, `operator_intervention_required`, `operator_admin`, caller
approval references, purge approval references, and audited operator actions
are safety controls that the automated checks must preserve.
