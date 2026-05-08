package releaseevidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	ReleaseIntentConvergenceSeed = "convergence_seed"
	ReleaseIntentFinalCandidate  = "final_candidate"
	SeedGapPolicyRejectOpenGap   = "reject_open_seed_gap"
)

type ReleaseSelector struct {
	SchemaVersion                string
	ReleaseIntent                string
	ManifestPath                 string
	SeedGapPolicy                string
	ManifestDigest               string
	SelectorInputDigest          string
	SchemaMigrationSetDigest     string
	PolicyArtifactIdentityDigest string
	RollbackRollforwardPolicyRef string
	FinalAcceptanceSelector      []ReleaseSelectorAcceptanceRow
	ClaimedOptionalCapabilities  []string
}

type ReleaseSelectorAcceptanceRow struct {
	ClaimID      string
	SubclaimID   string
	AcceptanceID string
}

type rawReleaseSelector struct {
	SchemaVersion                *string                        `json:"schema_version"`
	ReleaseIntent                *string                        `json:"release_intent"`
	ManifestPath                 *string                        `json:"manifest_path"`
	SeedGapPolicy                *string                        `json:"seed_gap_policy"`
	ManifestDigest               *string                        `json:"manifest_digest"`
	SelectorInputDigest          *string                        `json:"selector_input_digest"`
	SchemaMigrationSetDigest     *string                        `json:"schema_migration_set_digest"`
	PolicyArtifactIdentityDigest *string                        `json:"policy_artifact_identity_digest"`
	RollbackRollforwardPolicyRef *string                        `json:"rollback_rollforward_policy_ref"`
	FinalAcceptanceSelector      []rawReleaseSelectorAcceptance `json:"final_acceptance_selector"`
	ClaimedOptionalCapabilities  []string                       `json:"claimed_optional_capabilities"`
}

type rawReleaseSelectorAcceptance struct {
	ClaimID      *string `json:"claim_id"`
	SubclaimID   *string `json:"subclaim_id"`
	AcceptanceID *string `json:"acceptance_id"`
}

func loadReleaseSelectorForMode(manifestPath string, options Options, mode, repoRoot string) (*ReleaseSelector, []Finding, error) {
	if mode == ManifestModeFinal && strings.TrimSpace(options.SelectorPath) == "" {
		return nil, []Finding{{Code: "selector.required_missing", Message: "final mode requires -selector docs/release-evidence/ga-release-selector.json"}}, nil
	}
	if strings.TrimSpace(options.SelectorPath) == "" {
		return nil, nil, nil
	}

	selectorPath, findings := validateReleaseSelectorPath(options.SelectorPath)
	if len(findings) > 0 {
		return nil, findings, nil
	}
	body, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(selectorPath)))
	if err != nil {
		return nil, nil, fmt.Errorf("read selector %s: %w", selectorPath, err)
	}

	selector, selectorFindings, err := decodeReleaseSelector(body)
	if err != nil {
		return nil, nil, err
	}
	findings = append(findings, selectorFindings...)
	findings = append(findings, validateReleaseSelector(selector, mode, repoLocalManifestPath(manifestPath, repoRoot), repoRoot, selectorPath, body)...)
	return &selector, findings, nil
}

func validateReleaseSelectorPath(path string) (string, []Finding) {
	if unsafeRepoLocalToken(path) {
		return "", []Finding{{Code: "selector.path_not_repo_local", Message: "selector path must be repo-local, non-absolute, and must not contain .."}}
	}
	clean := filepath.ToSlash(filepath.Clean(path))
	if clean != AuthoritativeReleaseSelectorPath {
		return "", []Finding{{Code: "selector.path_not_authoritative", Message: fmt.Sprintf("selector path must be %s", AuthoritativeReleaseSelectorPath)}}
	}
	return clean, nil
}

func decodeReleaseSelector(body []byte) (ReleaseSelector, []Finding, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()

	var raw rawReleaseSelector
	if err := decoder.Decode(&raw); err != nil {
		return ReleaseSelector{}, nil, fmt.Errorf("decode selector: %w", err)
	}

	var findings []Finding
	selector := ReleaseSelector{
		FinalAcceptanceSelector:     make([]ReleaseSelectorAcceptanceRow, 0, len(raw.FinalAcceptanceSelector)),
		ClaimedOptionalCapabilities: append([]string(nil), raw.ClaimedOptionalCapabilities...),
	}
	if raw.SchemaVersion == nil || strings.TrimSpace(*raw.SchemaVersion) == "" {
		findings = append(findings, Finding{Code: "selector.schema_version_missing", Message: "selector schema_version is required"})
	} else {
		selector.SchemaVersion = *raw.SchemaVersion
	}
	if raw.ReleaseIntent == nil || strings.TrimSpace(*raw.ReleaseIntent) == "" {
		findings = append(findings, Finding{Code: "selector.release_intent_missing", Message: "selector release_intent is required"})
	} else {
		selector.ReleaseIntent = *raw.ReleaseIntent
	}
	if raw.ManifestPath == nil || strings.TrimSpace(*raw.ManifestPath) == "" {
		findings = append(findings, Finding{Code: "selector.manifest_path_missing", Message: "selector manifest_path is required"})
	} else {
		selector.ManifestPath = *raw.ManifestPath
	}
	if raw.SeedGapPolicy == nil || strings.TrimSpace(*raw.SeedGapPolicy) == "" {
		findings = append(findings, Finding{Code: "selector.seed_gap_policy_missing", Message: "selector seed_gap_policy is required"})
	} else {
		selector.SeedGapPolicy = *raw.SeedGapPolicy
	}
	if raw.ManifestDigest != nil {
		selector.ManifestDigest = *raw.ManifestDigest
	}
	if raw.SelectorInputDigest != nil {
		selector.SelectorInputDigest = *raw.SelectorInputDigest
	}
	if raw.SchemaMigrationSetDigest != nil {
		selector.SchemaMigrationSetDigest = *raw.SchemaMigrationSetDigest
	}
	if raw.PolicyArtifactIdentityDigest != nil {
		selector.PolicyArtifactIdentityDigest = *raw.PolicyArtifactIdentityDigest
	}
	if raw.RollbackRollforwardPolicyRef != nil {
		selector.RollbackRollforwardPolicyRef = *raw.RollbackRollforwardPolicyRef
	}
	if raw.FinalAcceptanceSelector == nil {
		findings = append(findings, Finding{Code: "selector.final_acceptance_selector_missing", Message: "selector final_acceptance_selector is required; use [] when no rows are selected"})
	}
	if raw.ClaimedOptionalCapabilities == nil {
		findings = append(findings, Finding{Code: "selector.claimed_optional_capabilities_missing", Message: "selector claimed_optional_capabilities is required; use [] when none are claimed"})
	}
	for index, rawRow := range raw.FinalAcceptanceSelector {
		rowID := fmt.Sprintf("selector.final_acceptance_selector[%d]", index)
		row := ReleaseSelectorAcceptanceRow{}
		if rawRow.ClaimID == nil || strings.TrimSpace(*rawRow.ClaimID) == "" {
			findings = append(findings, Finding{ItemID: rowID, Code: "selector.claim_id_missing", Message: "claim_id is required"})
		} else {
			row.ClaimID = *rawRow.ClaimID
		}
		if rawRow.SubclaimID == nil || strings.TrimSpace(*rawRow.SubclaimID) == "" {
			findings = append(findings, Finding{ItemID: rowID, Code: "selector.subclaim_id_missing", Message: "subclaim_id is required"})
		} else {
			row.SubclaimID = *rawRow.SubclaimID
		}
		if rawRow.AcceptanceID == nil || strings.TrimSpace(*rawRow.AcceptanceID) == "" {
			findings = append(findings, Finding{ItemID: rowID, Code: "selector.acceptance_id_missing", Message: "acceptance_id is required"})
		} else {
			row.AcceptanceID = *rawRow.AcceptanceID
		}
		selector.FinalAcceptanceSelector = append(selector.FinalAcceptanceSelector, row)
	}
	return selector, findings, nil
}

func validateReleaseSelector(selector ReleaseSelector, mode, manifestPath, repoRoot, selectorPath string, selectorBody []byte) []Finding {
	var findings []Finding
	if selector.SchemaVersion != "" && selector.SchemaVersion != "1" {
		findings = append(findings, Finding{Code: "selector.schema_version_invalid", Message: `selector schema_version must be "1"`})
	}
	if selector.ReleaseIntent != "" && selector.ReleaseIntent != ReleaseIntentConvergenceSeed && selector.ReleaseIntent != ReleaseIntentFinalCandidate {
		findings = append(findings, Finding{Code: "selector.release_intent_invalid", Message: "selector release_intent must be convergence_seed or final_candidate"})
	}
	if mode == ManifestModeFinal && selector.ReleaseIntent != "" && selector.ReleaseIntent != ReleaseIntentFinalCandidate {
		findings = append(findings, Finding{Code: "selector.release_intent_not_final", Message: "final mode requires selector release_intent=final_candidate"})
	}
	if selector.ReleaseIntent == ReleaseIntentFinalCandidate {
		if selector.SeedGapPolicy != "" && selector.SeedGapPolicy != SeedGapPolicyRejectOpenGap {
			findings = append(findings, Finding{Code: "selector.seed_gap_policy_invalid", Message: fmt.Sprintf("final candidate selector seed_gap_policy must be %q", SeedGapPolicyRejectOpenGap)})
		}
		findings = append(findings, validateFinalSelectorIdentity(selector, repoRoot, manifestPath, selectorPath, selectorBody)...)
	}
	if selector.ManifestPath != "" {
		if unsafeRepoLocalToken(selector.ManifestPath) {
			findings = append(findings, Finding{Code: "selector.manifest_path_not_repo_local", Message: "selector manifest_path must be repo-local, non-absolute, and must not contain .."})
		} else if filepath.ToSlash(filepath.Clean(selector.ManifestPath)) != manifestPath {
			findings = append(findings, Finding{Code: "selector.manifest_path_mismatch", Message: fmt.Sprintf("selector manifest_path %q must point to current manifest %q", selector.ManifestPath, manifestPath)})
		}
	}
	for _, capabilityID := range selector.ClaimedOptionalCapabilities {
		if !optionalGatedCapabilities[capabilityID] {
			findings = append(findings, Finding{Code: "selector.claimed_optional_capability_invalid", Message: fmt.Sprintf("claimed optional capability %q is not optional-gated", capabilityID)})
		}
	}
	return findings
}

func validateFinalSelectorIdentity(selector ReleaseSelector, repoRoot, manifestPath, selectorPath string, selectorBody []byte) []Finding {
	required := []struct {
		name  string
		value string
	}{
		{name: "manifest_digest", value: selector.ManifestDigest},
		{name: "selector_input_digest", value: selector.SelectorInputDigest},
		{name: "schema_migration_set_digest", value: selector.SchemaMigrationSetDigest},
		{name: "policy_artifact_identity_digest", value: selector.PolicyArtifactIdentityDigest},
		{name: "rollback_rollforward_policy_ref", value: selector.RollbackRollforwardPolicyRef},
	}
	var findings []Finding
	for _, field := range required {
		if strings.TrimSpace(field.value) == "" {
			findings = append(findings, Finding{Code: "selector.identity_field_missing", Message: fmt.Sprintf("final candidate selector requires %s", field.name)})
			continue
		}
		if field.name != "rollback_rollforward_policy_ref" && !validSHA256Digest(field.value) {
			findings = append(findings, Finding{Code: "selector.identity_digest_invalid", Message: fmt.Sprintf("%s must use sha256:<hex>", field.name)})
		}
	}
	if strings.TrimSpace(selector.RollbackRollforwardPolicyRef) != "" && unsafeRepoLocalToken(selector.RollbackRollforwardPolicyRef) {
		findings = append(findings, Finding{Code: "selector.rollback_rollforward_policy_ref_invalid", Message: "rollback_rollforward_policy_ref must be a repo-local safe path"})
	}
	manifestDigest, manifestDigestErr := digestFile(filepath.Join(repoRoot, filepath.FromSlash(manifestPath)))
	findings = append(findings, compareSelectorDigest("manifest_digest", selector.ManifestDigest, manifestDigest, manifestDigestErr)...)
	selectorDigest, selectorDigestErr := digestSelectorInput(selectorBody)
	findings = append(findings, compareSelectorDigest("selector_input_digest", selector.SelectorInputDigest, selectorDigest, selectorDigestErr)...)
	schemaDigest, schemaDigestErr := digestSchemaMigrationSet(repoRoot)
	findings = append(findings, compareSelectorDigest("schema_migration_set_digest", selector.SchemaMigrationSetDigest, schemaDigest, schemaDigestErr)...)
	policyDigest, policyDigestErr := digestPolicyArtifactIdentitySet(repoRoot)
	findings = append(findings, compareSelectorDigest("policy_artifact_identity_digest", selector.PolicyArtifactIdentityDigest, policyDigest, policyDigestErr)...)
	return findings
}

func compareSelectorDigest(name, got string, want string, err error) []Finding {
	if strings.TrimSpace(got) == "" {
		return nil
	}
	if err != nil {
		return []Finding{{Code: "selector.identity_digest_error", Message: fmt.Sprintf("compute %s: %v", name, err)}}
	}
	if got != want {
		return []Finding{{Code: "selector.identity_digest_mismatch", Message: fmt.Sprintf("%s mismatch: got %s want %s", name, got, want)}}
	}
	return nil
}

func validSHA256Digest(value string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	hexPart := strings.TrimPrefix(value, prefix)
	if len(hexPart) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(hexPart)
	return err == nil
}

func digestFile(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return digestBytes(body), nil
}

func digestBytes(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func digestSelectorInput(body []byte) (string, error) {
	var raw map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return "", err
	}
	delete(raw, "selector_input_digest")
	canonical, err := json.Marshal(raw)
	if err != nil {
		return "", err
	}
	return digestBytes(canonical), nil
}

func digestSchemaMigrationSet(repoRoot string) (string, error) {
	paths := []string{
		"api/openapi/internal-v1.openapi.yaml",
		"api/schemas/afscp-internal-v1.schema.json",
	}
	migrations, err := filepath.Glob(filepath.Join(repoRoot, "migrations", "*.sql"))
	if err != nil {
		return "", err
	}
	if len(migrations) == 0 {
		return "", fmt.Errorf("migrations/*.sql must include at least one migration")
	}
	sort.Strings(migrations)
	for _, migration := range migrations {
		rel, err := filepath.Rel(repoRoot, migration)
		if err != nil {
			return "", err
		}
		paths = append(paths, filepath.ToSlash(rel))
	}
	return digestRepoLocalPathSet(repoRoot, paths)
}

func digestPolicyArtifactIdentitySet(repoRoot string) (string, error) {
	paths := []string{
		"scripts/verify-ga-release.sh",
		"scripts/verify-ga-baseline.sh",
		"docs/GA_RELEASE_GATES.md",
		"docs/GA_NEXT_PHASE_DEVELOPMENT_HANDOFF_PLAN.md",
	}
	workflow := ".github/workflows/ga-release.yml"
	if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(workflow))); err == nil {
		paths = append(paths, workflow)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return digestRepoLocalPathSet(repoRoot, paths)
}

func digestRepoLocalPathSet(repoRoot string, paths []string) (string, error) {
	sort.Strings(paths)
	hash := sha256.New()
	for _, path := range paths {
		if unsafeRepoLocalToken(path) {
			return "", fmt.Errorf("identity path %q must be repo-local", path)
		}
		body, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(path)))
		if err != nil {
			return "", fmt.Errorf("read identity path %s: %w", path, err)
		}
		hash.Write([]byte(path))
		hash.Write([]byte{0})
		hash.Write(body)
		hash.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func ReleaseSelectorIntentFile(path, repoRoot string) (string, error) {
	selectorPath, findings := validateReleaseSelectorPath(path)
	if len(findings) > 0 {
		return "", errors.New(findings[0].String())
	}
	body, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(selectorPath)))
	if err != nil {
		return "", fmt.Errorf("read selector %s: %w", selectorPath, err)
	}
	var raw struct {
		ReleaseIntent string `json:"release_intent"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return "", fmt.Errorf("decode selector intent: %w", err)
	}
	if strings.TrimSpace(raw.ReleaseIntent) == "" {
		return "", fmt.Errorf("selector release_intent is required")
	}
	if raw.ReleaseIntent != ReleaseIntentConvergenceSeed && raw.ReleaseIntent != ReleaseIntentFinalCandidate {
		return "", fmt.Errorf("selector release_intent must be convergence_seed or final_candidate")
	}
	return raw.ReleaseIntent, nil
}

func repoLocalManifestPath(path, repoRoot string) string {
	absRoot, rootErr := filepath.Abs(repoRoot)
	absPath, pathErr := filepath.Abs(path)
	if rootErr == nil && pathErr == nil {
		if rel, err := filepath.Rel(absRoot, absPath); err == nil && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
			return filepath.ToSlash(filepath.Clean(rel))
		}
	}
	return filepath.ToSlash(filepath.Clean(path))
}
