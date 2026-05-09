package releaseevidence

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFinalModeRequiresAuthoritativeFinalSelector(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	manifestPath := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, manifestPath, validReleaseEvidenceManifest())

	_, findings, err := LoadAndValidateFile(manifestPath, Options{Mode: ManifestModeFinal, RepoRoot: root, ExecuteRequired: false})
	if err != nil {
		t.Fatalf("LoadAndValidateFile returned unexpected error: %v", err)
	}
	assertReleaseEvidenceFindingContains(t, findings, "selector")

	selectorPath := writeReleaseSelector(t, root, "manifest.json", "convergence_seed", nil)
	_, findings, err = LoadAndValidateFile(manifestPath, Options{Mode: ManifestModeFinal, RepoRoot: root, SelectorPath: selectorPath, ExecuteRequired: false})
	if err != nil {
		t.Fatalf("LoadAndValidateFile returned unexpected error: %v", err)
	}
	assertReleaseEvidenceFindingContains(t, findings, "final_candidate")

	writeReleaseEvidenceFile(t, filepath.Join(root, selectorPath), releaseSelectorBody("other-manifest.json", "final_candidate", nil))
	_, findings, err = LoadAndValidateFile(manifestPath, Options{Mode: ManifestModeFinal, RepoRoot: root, SelectorPath: selectorPath, ExecuteRequired: false})
	if err != nil {
		t.Fatalf("LoadAndValidateFile returned unexpected error: %v", err)
	}
	assertReleaseEvidenceFindingContains(t, findings, "manifest_path")
}

func TestSelectorRejectsUnsafePathAndGeneratedReportDigest(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	manifestPath := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, manifestPath, validReleaseEvidenceManifest())

	_, findings, err := LoadAndValidateFile(manifestPath, Options{Mode: ManifestModeFinal, RepoRoot: root, SelectorPath: "../ga-release-selector.json", ExecuteRequired: false})
	if err != nil {
		t.Fatalf("LoadAndValidateFile returned unexpected error: %v", err)
	}
	assertReleaseEvidenceFindingContains(t, findings, "repo-local")

	selectorPath := writeReleaseSelector(t, root, "manifest.json", "final_candidate", nil)
	writeReleaseEvidenceFile(t, filepath.Join(root, selectorPath), strings.Replace(releaseSelectorBody("manifest.json", "final_candidate", nil), `"claimed_optional_capabilities":[]`, `"generated_report_digest":"sha256:abc","claimed_optional_capabilities":[]`, 1))
	_, _, err = LoadAndValidateFile(manifestPath, Options{Mode: ManifestModeFinal, RepoRoot: root, SelectorPath: selectorPath, ExecuteRequired: false})
	if err == nil || !strings.Contains(err.Error(), "generated_report_digest") {
		t.Fatalf("expected generated_report_digest decode error, got %v", err)
	}
}

func TestFinalSelectorRequiresRejectOpenSeedGapPolicy(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	manifestPath := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, manifestPath, validReleaseEvidenceManifest())
	selectorPath := writeReleaseSelector(t, root, "manifest.json", "final_candidate", nil)
	writeReleaseEvidenceFile(t, filepath.Join(root, selectorPath), strings.Replace(releaseSelectorBody("manifest.json", "final_candidate", nil), `"seed_gap_policy":"reject_open_seed_gap"`, `"seed_gap_policy":"selected_final_replacements"`, 1))

	_, findings, err := LoadAndValidateFile(manifestPath, Options{Mode: ManifestModeFinal, RepoRoot: root, SelectorPath: selectorPath, ExecuteRequired: false})
	if err != nil {
		t.Fatalf("LoadAndValidateFile returned unexpected error: %v", err)
	}
	assertReleaseEvidenceFindingContains(t, findings, "seed_gap_policy")
	assertReleaseEvidenceFindingContains(t, findings, "reject_open_seed_gap")
}

func TestFinalSelectorRequiresIdentityFields(t *testing.T) {
	tests := []struct {
		name  string
		field string
		want  string
	}{
		{name: "manifest digest", field: `"manifest_digest":"sha256:manifest",`, want: "manifest_digest"},
		{name: "selector input digest", field: `"selector_input_digest":"sha256:selector",`, want: "selector_input_digest"},
		{name: "schema migration set digest", field: `"schema_migration_set_digest":"sha256:schema",`, want: "schema_migration_set_digest"},
		{name: "policy artifact identity digest", field: `"policy_artifact_identity_digest":"sha256:policy",`, want: "policy_artifact_identity_digest"},
		{name: "rollback rollforward policy ref", field: `"rollback_rollforward_policy_ref":"docs/release-evidence/rollback-rollforward.md",`, want: "rollback_rollforward_policy_ref"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := releaseEvidenceFixtureRoot(t)
			manifestPath := filepath.Join(root, "manifest.json")
			writeReleaseEvidenceFile(t, manifestPath, validReleaseEvidenceManifest())
			selectorPath := writeReleaseSelector(t, root, "manifest.json", "final_candidate", nil)
			writeReleaseEvidenceFile(t, filepath.Join(root, selectorPath), strings.Replace(releaseSelectorBody("manifest.json", "final_candidate", nil), "\n  "+tt.field, "", 1))

			_, findings, err := LoadAndValidateFile(manifestPath, Options{Mode: ManifestModeFinal, RepoRoot: root, SelectorPath: selectorPath, ExecuteRequired: false})
			if err != nil {
				t.Fatalf("LoadAndValidateFile returned unexpected error: %v", err)
			}
			assertReleaseEvidenceFindingContains(t, findings, tt.want)
		})
	}
}

func TestFinalSelectorRejectsDigestMismatch(t *testing.T) {
	tests := []struct {
		name  string
		field string
		want  string
	}{
		{name: "manifest digest", field: "manifest_digest", want: "manifest_digest"},
		{name: "selector input digest", field: "selector_input_digest", want: "selector_input_digest"},
		{name: "schema migration set digest", field: "schema_migration_set_digest", want: "schema_migration_set_digest"},
		{name: "policy artifact identity digest", field: "policy_artifact_identity_digest", want: "policy_artifact_identity_digest"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := releaseEvidenceFixtureRoot(t)
			manifestPath := filepath.Join(root, "manifest.json")
			writeReleaseEvidenceFile(t, manifestPath, validReleaseEvidenceManifest())
			selectorPath := writeReleaseSelector(t, root, "manifest.json", "final_candidate", nil)
			body, err := os.ReadFile(filepath.Join(root, selectorPath))
			if err != nil {
				t.Fatalf("read selector: %v", err)
			}
			writeReleaseEvidenceFile(t, filepath.Join(root, selectorPath), replaceSelectorDigestField(t, string(body), tt.field, "sha256:"+strings.Repeat("0", 64)))

			_, findings, err := LoadAndValidateFile(manifestPath, Options{Mode: ManifestModeFinal, RepoRoot: root, SelectorPath: selectorPath, ExecuteRequired: false})
			if err != nil {
				t.Fatalf("LoadAndValidateFile returned unexpected error: %v", err)
			}
			assertReleaseEvidenceFindingContains(t, findings, tt.want)
			assertReleaseEvidenceFindingContains(t, findings, "mismatch")
		})
	}
}

func TestEvidenceStatusContract(t *testing.T) {
	tests := []struct {
		name string
		edit func(string) string
		want string
	}{
		{
			name: "unknown evidence status",
			edit: func(body string) string {
				return replacePackage0FieldForItem(body, "webdav_export_disabled_admission_unit", `"evidence_status":"implemented"`, `"evidence_status":"running"`)
			},
			want: "evidence_status",
		},
		{
			name: "required placeholder fails",
			edit: func(body string) string {
				return replacePackage0FieldForItem(body, "webdav_export_disabled_admission_unit", `"evidence_status":"implemented"`, `"evidence_status":"placeholder"`)
			},
			want: "placeholder",
		},
		{
			name: "seed gap must remain placeholder",
			edit: func(body string) string {
				return replacePackage0FieldForItem(body, "seed_gap_workload_fixture_ready_open", `"evidence_status":"placeholder"`, `"evidence_status":"implemented"`)
			},
			want: "placeholder",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := releaseEvidenceFixtureRoot(t)
			path := filepath.Join(root, "manifest.json")
			writeReleaseEvidenceFile(t, path, tt.edit(validReleaseEvidenceManifest()))

			findings, err := VerifyFile(path, Options{Mode: ManifestModeSeed, RepoRoot: root, ExecuteRequired: false})
			if err != nil {
				t.Fatalf("VerifyFile returned unexpected error: %v", err)
			}
			assertReleaseEvidenceFindingContains(t, findings, tt.want)
		})
	}
}

func TestOptionalAcceptanceRowCannotSelectFixtureWithoutClaimedCapability(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	manifestPath := filepath.Join(root, "manifest.json")
	body := withoutPackage0SeedGapMarkers(validReleaseEvidenceManifest())
	writeReleaseEvidenceFile(t, manifestPath, body)
	selectorPath := writeReleaseSelector(t, root, "manifest.json", "final_candidate", nil)
	writeReleaseEvidenceFile(t, filepath.Join(root, selectorPath), releaseSelectorBodyWithAcceptanceRows("manifest.json", "final_candidate", nil, []ReleaseSelectorAcceptanceRow{{
		ClaimID:      "CLAIM_PURGE_APPROVAL_SAFE",
		SubclaimID:   "purge_approval_safe",
		AcceptanceID: "P0_PURGE_APPROVAL_SAFE",
	}}))

	_, findings, err := LoadAndValidateFile(manifestPath, Options{Mode: ManifestModeFinal, RepoRoot: root, SelectorPath: selectorPath, ExecuteRequired: false})
	if err != nil {
		t.Fatalf("LoadAndValidateFile returned unexpected error: %v", err)
	}
	assertNoReleaseEvidenceFindingContains(t, findings, "CLAIM_PURGE_APPROVAL_SAFE")
}

func TestFinalOptionalPositiveSelectionComesFromSelector(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	manifestPath := filepath.Join(root, "manifest.json")
	body := withoutPackage0SeedGapMarkers(validReleaseEvidenceManifest())
	writeReleaseEvidenceFile(t, manifestPath, body)
	selectorPath := writeReleaseSelector(t, root, "manifest.json", "final_candidate", nil)

	_, findings, err := LoadAndValidateFile(manifestPath, Options{Mode: ManifestModeFinal, RepoRoot: root, SelectorPath: selectorPath, ExecuteRequired: false})
	if err != nil {
		t.Fatalf("LoadAndValidateFile returned unexpected error: %v", err)
	}
	assertNoReleaseEvidenceFindingContains(t, findings, "CLAIM_PURGE_APPROVAL_SAFE")
	assertNoReleaseEvidenceFindingContains(t, findings, "CLAIM_TEMPLATE_QUOTA_BOUNDARY")

	writeReleaseEvidenceFile(t, filepath.Join(root, selectorPath), releaseSelectorBody("manifest.json", "final_candidate", []string{"repo_purge"}))
	_, findings, err = LoadAndValidateFile(manifestPath, Options{Mode: ManifestModeFinal, RepoRoot: root, SelectorPath: selectorPath, ExecuteRequired: false})
	if err != nil {
		t.Fatalf("LoadAndValidateFile returned unexpected error: %v", err)
	}
	assertReleaseEvidenceFindingContains(t, findings, "CLAIM_PURGE_APPROVAL_SAFE")
	assertNoReleaseEvidenceFindingContains(t, findings, "CLAIM_TEMPLATE_QUOTA_BOUNDARY")
}

func TestUnselectedOptionalRequiredEvidenceDoesNotExecute(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	body := finalManifestWithRequiredDefaultSeedGapReplacements(validReleaseEvidenceManifest())
	body = appendReleaseEvidenceItem(body, seedGapReplacementItem(seedGapSpecByID(t, "seed_gap_purge_approval_safe_open"), `"bash","scripts/fail.sh"`, "scripts/fail.sh", "implemented"))
	manifestPath := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, manifestPath, body)
	selectorPath := writeReleaseSelector(t, root, "manifest.json", "final_candidate", nil)

	findings, err := VerifyFile(manifestPath, Options{Mode: ManifestModeFinal, RepoRoot: root, SelectorPath: selectorPath, ExecuteRequired: true})
	if err != nil {
		t.Fatalf("VerifyFile returned unexpected error: %v", err)
	}
	assertNoReleaseEvidenceFindingContains(t, findings, "seed_gap_purge_approval_safe_open")
	assertNoReleaseEvidenceFindingContains(t, findings, "item.command_failed")
}

func TestFinalReplacementRequiresImplementedOrClosedStatus(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	body := withoutPackage0SeedGapMarker(validReleaseEvidenceManifest(), "seed_gap_admin_bootstrap_ready_open")
	body = appendReleaseEvidenceItem(body, seedGapReplacementItem(seedGapSpecByID(t, "seed_gap_admin_bootstrap_ready_open"), `"bash","scripts/pass.sh"`, "scripts/pass.sh", "placeholder"))
	manifestPath := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, manifestPath, body)
	selectorPath := writeReleaseSelector(t, root, "manifest.json", "final_candidate", nil)

	_, findings, err := LoadAndValidateFile(manifestPath, Options{Mode: ManifestModeFinal, RepoRoot: root, SelectorPath: selectorPath, ExecuteRequired: false})
	if err != nil {
		t.Fatalf("LoadAndValidateFile returned unexpected error: %v", err)
	}
	assertReleaseEvidenceFindingContains(t, findings, "manifest.final_replacement_status_invalid")
}

func writeReleaseSelector(t *testing.T, root, manifestPath, intent string, capabilities []string) string {
	t.Helper()

	writeReleaseSelectorIdentityFixtureFiles(t, root)
	path := filepath.Join(root, AuthoritativeReleaseSelectorPath)
	writeReleaseEvidenceFile(t, path, releaseSelectorBody(manifestPath, intent, capabilities))
	if intent == "final_candidate" {
		body := releaseSelectorBodyWithDigests(t, root, manifestPath, intent, capabilities, nil)
		writeReleaseEvidenceFile(t, path, body)
	}
	return AuthoritativeReleaseSelectorPath
}

func finalReleaseOptions(t *testing.T, root string, capabilities []string) Options {
	t.Helper()

	selectorPath := writeReleaseSelector(t, root, "manifest.json", "final_candidate", capabilities)
	return Options{Mode: ManifestModeFinal, RepoRoot: root, SelectorPath: selectorPath, ExecuteRequired: false}
}

func releaseSelectorBody(manifestPath, intent string, capabilities []string) string {
	return releaseSelectorBodyWithAcceptanceRows(manifestPath, intent, capabilities, nil)
}

func releaseSelectorBodyWithAcceptanceRows(manifestPath, intent string, capabilities []string, rows []ReleaseSelectorAcceptanceRow) string {
	capabilityJSON := "[]"
	if len(capabilities) > 0 {
		quoted := make([]string, 0, len(capabilities))
		for _, capability := range capabilities {
			quoted = append(quoted, `"`+capability+`"`)
		}
		capabilityJSON = "[" + strings.Join(quoted, ",") + "]"
	}
	rowJSON := "[]"
	if len(rows) > 0 {
		encodedRows := make([]string, 0, len(rows))
		for _, row := range rows {
			encodedRows = append(encodedRows, `{"claim_id":"`+row.ClaimID+`","subclaim_id":"`+row.SubclaimID+`","acceptance_id":"`+row.AcceptanceID+`"}`)
		}
		rowJSON = "[" + strings.Join(encodedRows, ",") + "]"
	}
	return `{
  "schema_version":"1",
  "release_intent":"` + intent + `",
  "manifest_path":"` + manifestPath + `",
  "seed_gap_policy":"reject_open_seed_gap",
  "manifest_digest":"sha256:manifest",
  "selector_input_digest":"sha256:selector",
  "schema_migration_set_digest":"sha256:schema",
  "policy_artifact_identity_digest":"sha256:policy",
  "rollback_rollforward_policy_ref":"docs/release-evidence/rollback-rollforward.md",
  "final_acceptance_selector":` + rowJSON + `,
  "claimed_optional_capabilities":` + capabilityJSON + `
}`
}

func releaseSelectorBodyWithDigests(t *testing.T, root, manifestPath, intent string, capabilities []string, rows []ReleaseSelectorAcceptanceRow) string {
	t.Helper()

	body := releaseSelectorBodyWithAcceptanceRows(manifestPath, intent, capabilities, rows)
	body = replaceSelectorDigestField(t, body, "manifest_digest", sha256FileDigestForTest(t, filepath.Join(root, filepath.FromSlash(manifestPath))))
	schemaDigest, schemaDigestErr := digestSchemaMigrationSet(root)
	body = replaceSelectorDigestField(t, body, "schema_migration_set_digest", mustDigestForTest(t, schemaDigest, schemaDigestErr))
	policyDigest, policyDigestErr := digestPolicyArtifactIdentitySet(root)
	body = replaceSelectorDigestField(t, body, "policy_artifact_identity_digest", mustDigestForTest(t, policyDigest, policyDigestErr))
	selectorDigest, selectorDigestErr := digestSelectorInput([]byte(body))
	body = replaceSelectorDigestField(t, body, "selector_input_digest", mustDigestForTest(t, selectorDigest, selectorDigestErr))
	return body
}

func replaceSelectorDigestField(t *testing.T, body, field, value string) string {
	t.Helper()

	prefix := `"` + field + `":"`
	start := strings.Index(body, prefix)
	if start < 0 {
		t.Fatalf("selector body missing %s", field)
	}
	valueStart := start + len(prefix)
	valueEnd := strings.Index(body[valueStart:], `"`)
	if valueEnd < 0 {
		t.Fatalf("selector body has malformed %s", field)
	}
	valueEnd += valueStart
	return body[:valueStart] + value + body[valueEnd:]
}

func mustDigestForTest(t *testing.T, digest string, err error) string {
	t.Helper()
	if err != nil {
		t.Fatalf("compute digest: %v", err)
	}
	return digest
}

func sha256FileDigestForTest(t *testing.T, path string) string {
	t.Helper()

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func writeReleaseSelectorIdentityFixtureFiles(t *testing.T, root string) {
	t.Helper()

	for _, file := range []string{
		"api/openapi/internal-v1.openapi.yaml",
		"api/schemas/afscp-internal-v1.schema.json",
		"migrations/0001_test.sql",
		"scripts/verify-ga-release.sh",
		"scripts/verify-ga-baseline.sh",
		".github/workflows/ga-release.yml",
		"docs/GA_RELEASE_GATES.md",
		"docs/GA_NEXT_PHASE_DEVELOPMENT_HANDOFF_PLAN.md",
	} {
		writeReleaseEvidenceFile(t, filepath.Join(root, filepath.FromSlash(file)), fmt.Sprintf("fixture %s\n", file))
	}
}

func finalManifestWithRequiredDefaultSeedGapReplacements(body string) string {
	body = withoutPackage0SeedGapMarkers(body)
	for _, spec := range seedGapSpecs {
		if optionalFixtureFinalShape(spec) {
			continue
		}
		if strings.Contains(body, `"id":"`+finalReplacementIDForSeedGap(spec)+`"`) {
			continue
		}
		body = appendReleaseEvidenceItem(body, seedGapReplacementItem(spec, `"bash","scripts/pass.sh"`, "scripts/pass.sh", "implemented"))
	}
	return body
}

func seedGapSpecByID(t *testing.T, id string) seedGapSpec {
	t.Helper()

	for _, spec := range seedGapSpecs {
		if spec.ID == id {
			return spec
		}
	}
	t.Fatalf("missing seed gap spec %s", id)
	return seedGapSpec{}
}

func seedGapReplacementItem(spec seedGapSpec, command, anchor, evidenceStatus string) string {
	return `"id":"` + finalReplacementIDForSeedGap(spec) + `","evidence_status":"` + evidenceStatus + `","claim_id":"` + spec.ClaimID + `","subclaim_id":"` + spec.FinalSubclaimID + `","acceptance_id":"` + spec.FinalAcceptanceID + `","risk_id":"` + spec.RiskID + `","fixture_id":"fixture-` + spec.FinalCapabilityID + `","capability_id":"` + spec.FinalCapabilityID + `","evidence_profile":"` + spec.FinalEvidenceProfile + `","default_mode":` + boolLiteral(spec.FinalDefaultMode) + `,"fixture_enabled_mode":` + boolLiteral(spec.FinalFixtureEnabledMode) + `,"expected_runtime":"` + spec.FinalExpectedRuntime + `","scope":"` + spec.FinalScope + `","negative_or_positive":"` + spec.FinalNegativeOrPositive + `","evidence_type":"unit","required":true,"command":[` + command + `],"anchors":["` + anchor + `"],"doc_only_allowed":false,"optional_gated":` + boolLiteral(spec.FinalOptionalGated) + `,"default_ga_required":` + boolLiteral(spec.FinalDefaultGARequired) + `,"pass_criteria":{"kind":"` + spec.FinalPassCriteriaKind + `","assertions":["` + spec.FinalPassCriteriaAssert + `"]}`
}

func boolLiteral(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
