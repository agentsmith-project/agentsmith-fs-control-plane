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

manifest_path="docs/release-evidence/ga-manifest.json"
selector_path="docs/release-evidence/ga-release-selector.json"
mode="seed"
selector_args=()
if [[ -f "$selector_path" ]]; then
  selector_intent="$(go run ./cmd/afscp-evidence-verify -selector-intent "$selector_path")"
  if [[ "$selector_intent" == "final_candidate" ]]; then
    mode="final"
    selector_args=(-selector "$selector_path")
  elif [[ "${AFSCP_RELEASE_INTENT:-}" == "final_candidate" ]]; then
    printf 'AFSCP_RELEASE_INTENT=final_candidate requires %s with release_intent=final_candidate\n' "$selector_path" >&2
    exit 2
  fi
elif [[ "${AFSCP_RELEASE_INTENT:-}" == "final_candidate" ]]; then
  printf 'AFSCP_RELEASE_INTENT=final_candidate requires %s\n' "$selector_path" >&2
  exit 2
fi

run go run ./cmd/afscp-evidence-verify -mode "$mode" -manifest "$manifest_path" "${selector_args[@]}"
run go test -count=1 ./internal/contractcheck -run 'Test(CurrentRepoContractsPass|CurrentRepoActiveDocsHaveCurrentImplementationStatus|CurrentRepoReadinessEvidenceHasCurrentImplementationStatus|CurrentRepoPullRequestTemplateHasGovernanceEvidenceChecklist|CurrentRepoGAVerificationScriptsAreAuthoritative|CurrentRepoGAReleaseWorkflowRunsAuthoritativeScript)$'
run bash scripts/verify-ga-baseline.sh
