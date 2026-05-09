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

func TestCurrentRepoFinalSelectorArtifactHasDefaultGAIntentAndNoOptionalClaims(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	selectorPath := filepath.Join(repoRoot, filepath.FromSlash(AuthoritativeReleaseSelectorPath))

	body, err := os.ReadFile(selectorPath)
	if err != nil {
		t.Fatalf("authoritative final selector must exist at %s: %v", selectorPath, err)
	}
	selector, findings, err := decodeReleaseSelector(body)
	if err != nil {
		t.Fatalf("decode selector: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("selector should be structurally complete, got findings: %+v", findings)
	}
	if selector.ReleaseIntent != ReleaseIntentFinalCandidate {
		t.Fatalf("selector release_intent = %q, want %q", selector.ReleaseIntent, ReleaseIntentFinalCandidate)
	}
	if selector.SeedGapPolicy != SeedGapPolicyRejectOpenGap {
		t.Fatalf("selector seed_gap_policy = %q, want %q", selector.SeedGapPolicy, SeedGapPolicyRejectOpenGap)
	}
	if selector.ManifestPath != "docs/release-evidence/ga-manifest.json" {
		t.Fatalf("selector manifest_path = %q, want docs/release-evidence/ga-manifest.json", selector.ManifestPath)
	}
	if selector.RollbackRollforwardPolicyRef != AuthoritativeRollbackRollforwardPolicyPath {
		t.Fatalf("selector rollback_rollforward_policy_ref = %q, want %q", selector.RollbackRollforwardPolicyRef, AuthoritativeRollbackRollforwardPolicyPath)
	}
	if len(selector.FinalAcceptanceSelector) != 0 {
		t.Fatalf("default GA selector must not select final acceptance rows, got %+v", selector.FinalAcceptanceSelector)
	}
	if len(selector.ClaimedOptionalCapabilities) != 0 {
		t.Fatalf("default GA selector must not claim optional capabilities, got %+v", selector.ClaimedOptionalCapabilities)
	}

	policyPath := filepath.Join(repoRoot, filepath.FromSlash(selector.RollbackRollforwardPolicyRef))
	policyBody, err := os.ReadFile(policyPath)
	if err != nil {
		t.Fatalf("rollback/roll-forward policy ref must exist at %s: %v", policyPath, err)
	}
	policyText := strings.ToLower(string(policyBody))
	for _, want := range []string{
		"release recovery policy reference",
		"not production deployment proof",
		"not a manual approval",
	} {
		if !strings.Contains(policyText, want) {
			t.Fatalf("%s must state %q", policyPath, want)
		}
	}
	for _, forbidden := range []string{"sandbox", "sibling repo"} {
		if strings.Contains(policyText, forbidden) {
			t.Fatalf("%s must remain AFSCP-local and must not mention %q", policyPath, forbidden)
		}
	}
}

func TestCurrentRepoFinalSelectorDigestsMatchManifestSchemaPolicyAndSelectorInput(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	selectorPath := filepath.Join(repoRoot, filepath.FromSlash(AuthoritativeReleaseSelectorPath))
	body, err := os.ReadFile(selectorPath)
	if err != nil {
		t.Fatalf("read selector: %v", err)
	}
	selector, findings, err := decodeReleaseSelector(body)
	if err != nil {
		t.Fatalf("decode selector: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("selector should be structurally complete, got findings: %+v", findings)
	}

	wantManifestDigest, err := digestFile(filepath.Join(repoRoot, filepath.FromSlash(selector.ManifestPath)))
	if err != nil {
		t.Fatalf("compute manifest digest: %v", err)
	}
	wantSelectorDigest, err := digestSelectorInput(body)
	if err != nil {
		t.Fatalf("compute selector input digest: %v", err)
	}
	wantSchemaDigest, err := digestSchemaMigrationSet(repoRoot)
	if err != nil {
		t.Fatalf("compute schema/migration digest: %v", err)
	}
	wantPolicyDigest, err := digestPolicyArtifactIdentitySet(repoRoot)
	if err != nil {
		t.Fatalf("compute policy identity digest: %v", err)
	}
	for _, check := range []struct {
		name string
		got  string
		want string
	}{
		{name: "manifest_digest", got: selector.ManifestDigest, want: wantManifestDigest},
		{name: "selector_input_digest", got: selector.SelectorInputDigest, want: wantSelectorDigest},
		{name: "schema_migration_set_digest", got: selector.SchemaMigrationSetDigest, want: wantSchemaDigest},
		{name: "policy_artifact_identity_digest", got: selector.PolicyArtifactIdentityDigest, want: wantPolicyDigest},
	} {
		if check.got != check.want {
			t.Fatalf("%s = %q, want %q", check.name, check.got, check.want)
		}
	}
}

func TestCurrentRepoFinalModeAcceptsUnselectedOptionalSeedGaps(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	manifestPath := filepath.Join(repoRoot, "docs", "release-evidence", "ga-manifest.json")
	manifest, findings, err := LoadAndValidateFile(manifestPath, Options{
		Mode:            ManifestModeFinal,
		RepoRoot:        repoRoot,
		SelectorPath:    AuthoritativeReleaseSelectorPath,
		ExecuteRequired: false,
	})
	if err != nil {
		t.Fatalf("LoadAndValidateFile returned error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("current repo final selector should accept unselected optional seed gaps, got findings: %+v", findings)
	}

	openGaps := openSeedGapIDs(manifest)
	for _, gapID := range []string{
		"seed_gap_workload_fixture_ready_open",
		"seed_gap_purge_approval_safe_open",
		"seed_gap_optional_fixture_conformant_open",
		"seed_gap_template_quota_boundary_open",
	} {
		if !openGaps[gapID] {
			t.Fatalf("current repo manifest should keep unselected optional seed gap %s open", gapID)
		}
	}
}

func TestCurrentRepoFinalSelectorRejectsSelectedOptionalOpenGaps(t *testing.T) {
	tests := []struct {
		name       string
		capability string
		wantGaps   []string
		wantClaims []string
	}{
		{
			name:       "workload mount binding",
			capability: "workload_mount_binding",
			wantGaps:   []string{"seed_gap_workload_fixture_ready_open", "seed_gap_optional_fixture_conformant_open"},
			wantClaims: []string{"CLAIM_WORKLOAD_FIXTURE_READY", "CLAIM_OPTIONAL_FIXTURE_CONFORMANT"},
		},
		{
			name:       "repo purge",
			capability: "repo_purge",
			wantGaps:   []string{"seed_gap_purge_approval_safe_open"},
			wantClaims: []string{"CLAIM_PURGE_APPROVAL_SAFE"},
		},
		{
			name:       "repo template",
			capability: "repo_template",
			wantGaps:   []string{"seed_gap_template_quota_boundary_open"},
			wantClaims: []string{"CLAIM_TEMPLATE_QUOTA_BOUNDARY"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := currentRepoFinalSelectorFixtureRoot(t, []string{tt.capability})
			manifestPath := filepath.Join(root, "docs", "release-evidence", "ga-manifest.json")

			_, findings, err := LoadAndValidateFile(manifestPath, Options{
				Mode:            ManifestModeFinal,
				RepoRoot:        root,
				SelectorPath:    AuthoritativeReleaseSelectorPath,
				ExecuteRequired: false,
			})
			if err != nil {
				t.Fatalf("LoadAndValidateFile returned error: %v", err)
			}
			assertReleaseEvidenceFindingContains(t, findings, "manifest.final_seed_gap_open")
			for _, gapID := range tt.wantGaps {
				assertReleaseEvidenceFindingContains(t, findings, gapID)
			}
			for _, claimID := range tt.wantClaims {
				assertReleaseEvidenceFindingContains(t, findings, claimID)
			}
		})
	}
}

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

func TestFinalSelectorRequiresAuthoritativeRollbackRollforwardPolicyRef(t *testing.T) {
	tests := []struct {
		name       string
		ref        string
		setup      func(t *testing.T, root string)
		want       string
		wantDetail string
	}{
		{
			name: "other existing repo local file",
			ref:  "docs/release-evidence/other-rollback-rollforward.md",
			setup: func(t *testing.T, root string) {
				t.Helper()
				writeReleaseEvidenceFile(t, filepath.Join(root, "docs", "release-evidence", "other-rollback-rollforward.md"), "fixture alternate policy\n")
			},
			want:       "selector.rollback_rollforward_policy_ref_not_authoritative",
			wantDetail: "docs/release-evidence/other-rollback-rollforward.md",
		},
		{
			name:       "missing repo local file",
			ref:        "docs/release-evidence/missing-rollback-rollforward.md",
			want:       "selector.rollback_rollforward_policy_ref_missing",
			wantDetail: "docs/release-evidence/missing-rollback-rollforward.md",
		},
		{
			name:       "unsafe parent path",
			ref:        "../rollback.md",
			want:       "selector.rollback_rollforward_policy_ref_invalid",
			wantDetail: "repo-local safe path",
		},
		{
			name: "authoritative path is directory",
			ref:  AuthoritativeRollbackRollforwardPolicyPath,
			setup: func(t *testing.T, root string) {
				t.Helper()
				path := filepath.Join(root, filepath.FromSlash(AuthoritativeRollbackRollforwardPolicyPath))
				if err := os.Remove(path); err != nil {
					t.Fatalf("remove policy file: %v", err)
				}
				if err := os.MkdirAll(path, 0o755); err != nil {
					t.Fatalf("mkdir policy ref: %v", err)
				}
			},
			want:       "selector.rollback_rollforward_policy_ref_invalid",
			wantDetail: "must be a file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := releaseEvidenceFixtureRoot(t)
			manifestPath := filepath.Join(root, "manifest.json")
			writeReleaseEvidenceFile(t, manifestPath, validReleaseEvidenceManifest())
			selectorPath := writeReleaseSelector(t, root, "manifest.json", "final_candidate", nil)
			if tt.setup != nil {
				tt.setup(t, root)
			}

			body, err := os.ReadFile(filepath.Join(root, selectorPath))
			if err != nil {
				t.Fatalf("read selector: %v", err)
			}
			bodyText := strings.Replace(string(body), `"rollback_rollforward_policy_ref":"`+AuthoritativeRollbackRollforwardPolicyPath+`"`, `"rollback_rollforward_policy_ref":"`+tt.ref+`"`, 1)
			selectorDigest, selectorDigestErr := digestSelectorInput([]byte(bodyText))
			bodyText = replaceSelectorDigestField(t, bodyText, "selector_input_digest", mustDigestForTest(t, selectorDigest, selectorDigestErr))
			writeReleaseEvidenceFile(t, filepath.Join(root, selectorPath), bodyText)

			_, findings, err := LoadAndValidateFile(manifestPath, Options{Mode: ManifestModeFinal, RepoRoot: root, SelectorPath: selectorPath, ExecuteRequired: false})
			if err != nil {
				t.Fatalf("LoadAndValidateFile returned unexpected error: %v", err)
			}
			assertReleaseEvidenceFindingContains(t, findings, tt.want)
			if tt.wantDetail != "" {
				assertReleaseEvidenceFindingContains(t, findings, tt.wantDetail)
			}
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

func TestSelectedOptionalRequiredEvidenceExecutesWhenCapabilityClaimed(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	body := finalManifestWithRequiredDefaultSeedGapReplacements(validReleaseEvidenceManifest())
	body = appendReleaseEvidenceItem(body, seedGapReplacementItem(seedGapSpecByID(t, "seed_gap_purge_approval_safe_open"), `"bash","scripts/fail.sh"`, "scripts/fail.sh", "implemented"))
	manifestPath := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, manifestPath, body)
	selectorPath := writeReleaseSelector(t, root, "manifest.json", "final_candidate", []string{"repo_purge"})

	findings, err := VerifyFile(manifestPath, Options{Mode: ManifestModeFinal, RepoRoot: root, SelectorPath: selectorPath, ExecuteRequired: true})
	if err != nil {
		t.Fatalf("VerifyFile returned unexpected error: %v", err)
	}
	assertNoReleaseEvidenceFindingContains(t, findings, "seed_gap_purge_approval_safe_open")
	assertReleaseEvidenceFindingContains(t, findings, "item.command_failed")
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
  "rollback_rollforward_policy_ref":"` + AuthoritativeRollbackRollforwardPolicyPath + `",
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
		AuthoritativeRollbackRollforwardPolicyPath,
	} {
		writeReleaseEvidenceFile(t, filepath.Join(root, filepath.FromSlash(file)), fmt.Sprintf("fixture %s\n", file))
	}
}

func currentRepoFinalSelectorFixtureRoot(t *testing.T, capabilities []string) string {
	t.Helper()

	currentRoot := filepath.Join("..", "..")
	root := t.TempDir()
	for _, file := range []string{
		"docs/release-evidence/ga-manifest.json",
		AuthoritativeRollbackRollforwardPolicyPath,
		"api/openapi/internal-v1.openapi.yaml",
		"api/schemas/afscp-internal-v1.schema.json",
		"scripts/verify-ga-release.sh",
		"scripts/verify-ga-baseline.sh",
		"docs/GA_RELEASE_GATES.md",
		"docs/GA_NEXT_PHASE_DEVELOPMENT_HANDOFF_PLAN.md",
		".github/workflows/ga-release.yml",
	} {
		copyRepoLocalFileForSelectorTest(t, currentRoot, root, file)
	}
	migrationPaths, err := filepath.Glob(filepath.Join(currentRoot, "migrations", "*.sql"))
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	if len(migrationPaths) == 0 {
		t.Fatal("current repo must include migrations/*.sql")
	}
	for _, path := range migrationPaths {
		rel, err := filepath.Rel(currentRoot, path)
		if err != nil {
			t.Fatalf("rel migration path: %v", err)
		}
		copyRepoLocalFileForSelectorTest(t, currentRoot, root, filepath.ToSlash(rel))
	}
	selectorBody := releaseSelectorBodyWithDigests(t, root, "docs/release-evidence/ga-manifest.json", ReleaseIntentFinalCandidate, capabilities, nil)
	writeReleaseEvidenceFile(t, filepath.Join(root, filepath.FromSlash(AuthoritativeReleaseSelectorPath)), selectorBody)
	return root
}

func copyRepoLocalFileForSelectorTest(t *testing.T, srcRoot, dstRoot, rel string) {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(srcRoot, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read current repo file %s: %v", rel, err)
	}
	writeReleaseEvidenceFile(t, filepath.Join(dstRoot, filepath.FromSlash(rel)), string(body))
}

func openSeedGapIDs(manifest Manifest) map[string]bool {
	ids := make(map[string]bool)
	for _, item := range manifest.Items {
		if isOpenSeedGap(item) {
			ids[item.ID] = true
		}
	}
	return ids
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
