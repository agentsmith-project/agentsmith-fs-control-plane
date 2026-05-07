#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

run() {
  printf '\n+ %s\n' "$*"
  "$@"
}

run git diff --check
run bash -n scripts/verify-ga-release.sh
run bash -n scripts/verify-ga-baseline.sh
run go test -count=1 ./internal/contractcheck -run 'Test(CurrentRepoContractsPass|CurrentRepoActiveDocsHaveCurrentImplementationStatus|CurrentRepoReadinessEvidenceHasCurrentImplementationStatus|CurrentRepoPullRequestTemplateHasGovernanceEvidenceChecklist|CurrentRepoGAVerificationScriptsAreAuthoritative|CurrentRepoGAReleaseWorkflowRunsAuthoritativeScript)$'
run bash scripts/verify-ga-baseline.sh
