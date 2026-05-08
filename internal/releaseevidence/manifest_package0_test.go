package releaseevidence

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/capability"
)

func TestPackage0RequiredFieldsAndPassCriteria(t *testing.T) {
	tests := []struct {
		name string
		edit func(string) string
		want string
	}{
		{
			name: "missing claim_id",
			edit: func(body string) string {
				return removePackage0FieldForItem(body, "webdav_export_disabled_admission_unit", `"claim_id":"CLAIM_DEFAULT_DENIAL_SAFE",`)
			},
			want: "claim_id",
		},
		{
			name: "missing expected_runtime",
			edit: func(body string) string {
				return removePackage0FieldForItem(body, "webdav_export_disabled_admission_unit", `
      "expected_runtime":"fast",`)
			},
			want: "expected_runtime",
		},
		{
			name: "missing scope",
			edit: func(body string) string {
				return removePackage0FieldForItem(body, "webdav_export_disabled_admission_unit", `
      "scope":"package",`)
			},
			want: "scope",
		},
		{
			name: "missing pass_criteria",
			edit: func(body string) string {
				return removePackage0FieldForItem(body, "webdav_export_disabled_admission_unit", `,
      "pass_criteria":{"kind":"denial_safety","assertions":["disabled admission rejects before metadata and audits without queuing"]}`)
			},
			want: "pass_criteria",
		},
		{
			name: "empty pass_criteria object",
			edit: func(body string) string {
				return replacePackage0FieldForItem(body, "webdav_export_disabled_admission_unit", `"pass_criteria":{"kind":"denial_safety","assertions":["disabled admission rejects before metadata and audits without queuing"]}`, `"pass_criteria":{}`)
			},
			want: "pass_criteria.kind",
		},
		{
			name: "empty pass_criteria object on non-required item",
			edit: func(body string) string {
				return appendReleaseEvidenceItem(body, `"id":"optional_doc_trace_item","claim_id":"CLAIM_RELEASE_GATE_TRACEABLE","subclaim_id":"optional_doc_trace","acceptance_id":"P0_OPTIONAL_DOC_TRACE","risk_id":"","fixture_id":"","capability_id":"","evidence_profile":"default","default_mode":true,"fixture_enabled_mode":false,"expected_runtime":"fast","scope":"doc-guard","negative_or_positive":"both","evidence_type":"doc-guard","required":false,"command":[],"anchors":["docs/GA_NEXT_PHASE_DEVELOPMENT_HANDOFF_PLAN.md"],"doc_only_allowed":true,"optional_gated":false,"default_ga_required":false,"pass_criteria":{}`)
			},
			want: "pass_criteria.kind",
		},
		{
			name: "empty pass_criteria assertions",
			edit: func(body string) string {
				return replacePackage0FieldForItem(body, "webdav_export_disabled_admission_unit", `"pass_criteria":{"kind":"denial_safety","assertions":["disabled admission rejects before metadata and audits without queuing"]}`, `"pass_criteria":{"kind":"denial_safety","assertions":[]}`)
			},
			want: "assertions",
		},
		{
			name: "missing default mode on required item",
			edit: func(body string) string {
				return removePackage0FieldForItem(body, "webdav_export_disabled_admission_unit", `
      "default_mode":true,`)
			},
			want: "default_mode",
		},
		{
			name: "missing fixture enabled mode on required item",
			edit: func(body string) string {
				return removePackage0FieldForItem(body, "webdav_export_disabled_admission_unit", `
      "fixture_enabled_mode":false,`)
			},
			want: "fixture_enabled_mode",
		},
		{
			name: "unknown evidence_profile",
			edit: func(body string) string {
				return replacePackage0FieldForItem(body, "webdav_export_disabled_admission_unit", `"evidence_profile":"default"`, `"evidence_profile":"default-ish"`)
			},
			want: "evidence_profile",
		},
		{
			name: "unknown negative_or_positive",
			edit: func(body string) string {
				return replacePackage0FieldForItem(body, "webdav_export_disabled_admission_unit", `"negative_or_positive":"negative"`, `"negative_or_positive":"optimistic"`)
			},
			want: "negative_or_positive",
		},
		{
			name: "unknown expected_runtime",
			edit: func(body string) string {
				return replacePackage0FieldForItem(body, "webdav_export_disabled_admission_unit", `"expected_runtime":"fast"`, `"expected_runtime":"slowish"`)
			},
			want: "expected_runtime",
		},
		{
			name: "unknown scope",
			edit: func(body string) string {
				return replacePackage0FieldForItem(body, "webdav_export_disabled_admission_unit", `"scope":"package"`, `"scope":"manual-lab"`)
			},
			want: "scope",
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

func TestPackage0ProfileInvariants(t *testing.T) {
	tests := []struct {
		name string
		edit func(string) string
		want string
	}{
		{
			name: "default and fixture modes cannot both be true",
			edit: func(body string) string {
				return replacePackage0FieldForItem(body, "webdav_export_disabled_admission_unit", `"fixture_enabled_mode":false`, `"fixture_enabled_mode":true`)
			},
			want: "default_mode",
		},
		{
			name: "deployment runtime support cannot be required local evidence",
			edit: func(body string) string {
				return replacePackage0FieldForItem(body, "webdav_export_disabled_admission_unit", `"evidence_profile":"default"`, `"evidence_profile":"deployment-runtime-support"`)
			},
			want: "deployment-runtime-support",
		},
		{
			name: "optional positive cannot be default profile",
			edit: func(body string) string {
				return replacePackage0FieldForItem(body, "workload_mount_disabled_admission_unit", `"negative_or_positive":"negative"`, `"negative_or_positive":"positive"`)
			},
			want: "optional positive",
		},
		{
			name: "optional both cannot be default profile",
			edit: func(body string) string {
				return replacePackage0FieldForItem(body, "workload_mount_disabled_admission_unit", `"negative_or_positive":"negative"`, `"negative_or_positive":"both"`)
			},
			want: "optional capability",
		},
		{
			name: "optional fixture positive requires fixture id",
			edit: func(body string) string {
				return appendReleaseEvidenceItem(body, `"id":"workload_mount_fixture_positive_unit","claim_id":"CLAIM_OPTIONAL_FIXTURE_CONFORMANT","subclaim_id":"workload_mount_fixture_smoke","acceptance_id":"P0_OPTIONAL_FIXTURE_POSITIVE","risk_id":"","fixture_id":"","capability_id":"workload_mount_binding","evidence_profile":"repo-local-fixture-enabled","default_mode":false,"fixture_enabled_mode":true,"expected_runtime":"fast","scope":"package","negative_or_positive":"positive","evidence_type":"unit","required":false,"command":["bash","scripts/pass.sh"],"anchors":["scripts/pass.sh"],"doc_only_allowed":false,"optional_gated":true,"default_ga_required":false,"pass_criteria":{"kind":"positive_path","assertions":["fixture-enabled workload mount smoke passes"]}`)
			},
			want: "fixture_id",
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

func TestPackage0RejectsUnknownClaimTaxonomy(t *testing.T) {
	tests := []struct {
		name    string
		claimID string
	}{
		{name: "unknown claim", claimID: "CLAIM_SOMETHING_ELSE_READY"},
		{name: "old optional fixture claim", claimID: "CLAIM_OPTIONAL_FIXTURE_READY"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := releaseEvidenceFixtureRoot(t)
			body := replacePackage0FieldForItem(validReleaseEvidenceManifest(), "webdav_export_disabled_admission_unit", `"claim_id":"CLAIM_DEFAULT_DENIAL_SAFE"`, `"claim_id":"`+tt.claimID+`"`)
			path := filepath.Join(root, "manifest.json")
			writeReleaseEvidenceFile(t, path, body)

			findings, err := VerifyFile(path, Options{Mode: ManifestModeSeed, RepoRoot: root, ExecuteRequired: false})
			if err != nil {
				t.Fatalf("VerifyFile returned unexpected error: %v", err)
			}
			assertReleaseEvidenceFindingContains(t, findings, tt.claimID)
			assertReleaseEvidenceFindingContains(t, findings, "claim_id")
		})
	}
}

func TestPackage0RiskBoundRequiredEvidenceCannotBeDocOnly(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	body := replacePackage0FieldForItem(validReleaseEvidenceManifest(), "webdav_export_disabled_admission_unit", `"anchors":["scripts/pass.sh"],
      "doc_only_allowed":false`, `"anchors":["docs/READINESS_EVIDENCE.md"],
      "doc_only_allowed":true`)
	path := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, path, body)

	findings, err := VerifyFile(path, Options{Mode: ManifestModeSeed, RepoRoot: root, ExecuteRequired: false})
	if err != nil {
		t.Fatalf("VerifyFile returned unexpected error: %v", err)
	}
	assertReleaseEvidenceFindingContains(t, findings, "risk")
	assertReleaseEvidenceFindingContains(t, findings, "doc-only")
}

func TestPackage0RiskBoundRequiredEvidenceTreatsRootMarkdownAsDocOnly(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	writeReleaseEvidenceFile(t, filepath.Join(root, "README.md"), "fixture\n")
	body := replacePackage0FieldForItem(validReleaseEvidenceManifest(), "webdav_export_disabled_admission_unit", `"anchors":["scripts/pass.sh"]`, `"anchors":["README.md"]`)
	path := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, path, body)

	findings, err := VerifyFile(path, Options{Mode: ManifestModeSeed, RepoRoot: root, ExecuteRequired: false})
	if err != nil {
		t.Fatalf("VerifyFile returned unexpected error: %v", err)
	}
	assertReleaseEvidenceFindingContains(t, findings, "doc-only")
}

func TestPackage0RequiredEvidenceLocksCanonicalMetadata(t *testing.T) {
	tests := []struct {
		name string
		edit func(string) string
	}{
		{
			name: "claim drift",
			edit: func(body string) string {
				return replacePackage0FieldForItem(body, "webdav_export_disabled_admission_unit", `"claim_id":"CLAIM_DEFAULT_DENIAL_SAFE"`, `"claim_id":"CLAIM_OPTIONAL_DENIED_SAFE"`)
			},
		},
		{
			name: "subclaim drift",
			edit: func(body string) string {
				return replacePackage0FieldForItem(body, "webdav_export_disabled_admission_unit", `"subclaim_id":"webdav_export_disabled_admission"`, `"subclaim_id":"workload_mount_disabled_admission"`)
			},
		},
		{
			name: "pass criteria kind drift",
			edit: func(body string) string {
				return replacePackage0FieldForItem(body, "webdav_export_disabled_admission_unit", `"kind":"denial_safety"`, `"kind":"coverage_guard"`)
			},
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
			assertReleaseEvidenceFindingContains(t, findings, "webdav_export_disabled_admission_unit")
			assertReleaseEvidenceFindingContains(t, findings, "metadata")
		})
	}
}

func TestPackage0RequiresSeedClaimSubclaimCoverage(t *testing.T) {
	tests := []struct {
		name string
		edit func(string) string
		want string
	}{
		{
			name: "missing required seed claim",
			edit: func(body string) string {
				return strings.ReplaceAll(body, `"claim_id":"CLAIM_RELEASE_GATE_TRACEABLE"`, `"claim_id":"CLAIM_CAPABILITY_MATRIX_CONSISTENT"`)
			},
			want: "CLAIM_RELEASE_GATE_TRACEABLE",
		},
		{
			name: "missing required seed subclaim",
			edit: func(body string) string {
				return strings.Replace(body, `"subclaim_id":"release_gate_invokes_manifest_verifier"`, `"subclaim_id":"release_gate_other"`, 1)
			},
			want: "release_gate_invokes_manifest_verifier",
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

func TestPackage0RequiresSeedGapMarkers(t *testing.T) {
	tests := []struct {
		name string
		edit func(string) string
		want string
	}{
		{
			name: "missing gap marker",
			edit: func(body string) string {
				return replacePackage0ItemID(body, "seed_gap_default_user_loop_open", "seed_gap_default_user_loop_typo")
			},
			want: "CLAIM_DEFAULT_USER_LOOP",
		},
		{
			name: "gap marker cannot become required",
			edit: func(body string) string {
				return replacePackage0FieldForItem(body, "seed_gap_default_user_loop_open", `"required":false`, `"required":true`)
			},
			want: "gap marker",
		},
		{
			name: "gap marker cannot carry executable evidence",
			edit: func(body string) string {
				return replacePackage0FieldForItem(body, "seed_gap_default_user_loop_open", `"command":[]`, `"command":["bash","scripts/pass.sh"]`)
			},
			want: "command",
		},
		{
			name: "gap marker cannot become executable required evidence",
			edit: func(body string) string {
				body = replacePackage0FieldForItem(body, "seed_gap_default_user_loop_open", `"evidence_type":"doc-guard"`, `"evidence_type":"unit"`)
				body = replacePackage0FieldForItem(body, "seed_gap_default_user_loop_open", `"required":false`, `"required":true`)
				body = replacePackage0FieldForItem(body, "seed_gap_default_user_loop_open", `"command":[]`, `"command":["bash","scripts/pass.sh"]`)
				return replacePackage0FieldForItem(body, "seed_gap_default_user_loop_open", `"doc_only_allowed":true`, `"doc_only_allowed":false`)
			},
			want: "gap marker",
		},
		{
			name: "gap marker must be open",
			edit: func(body string) string {
				return replacePackage0FieldForItem(body, "seed_gap_default_user_loop_open", `"kind":"seed_gap"`, `"kind":"coverage_guard"`)
			},
			want: "open",
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

func TestPackage0SeedModeAllowsClosedSeedGapReplacement(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	body := withoutPackage0SeedGapMarker(validReleaseEvidenceManifest(), "seed_gap_admin_bootstrap_ready_open")
	body = appendReleaseEvidenceItem(body, seedGapReplacementItem(seedGapSpecByID(t, "seed_gap_admin_bootstrap_ready_open"), `"bash","scripts/pass.sh"`, "scripts/pass.sh", "implemented"))
	path := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, path, body)

	findings, err := VerifyFile(path, Options{Mode: ManifestModeSeed, RepoRoot: root, ExecuteRequired: false})
	if err != nil {
		t.Fatalf("VerifyFile returned unexpected error: %v", err)
	}
	assertNoReleaseEvidenceFindingContains(t, findings, "seed_gap_admin_bootstrap_ready_open")
	assertNoReleaseEvidenceFindingContains(t, findings, "CLAIM_ADMIN_BOOTSTRAP_READY")
}

func TestPackage0SeedModeRejectsMissingSeedGapWithoutReplacement(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	body := withoutPackage0SeedGapMarker(validReleaseEvidenceManifest(), "seed_gap_admin_bootstrap_ready_open")
	path := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, path, body)

	findings, err := VerifyFile(path, Options{Mode: ManifestModeSeed, RepoRoot: root, ExecuteRequired: false})
	if err != nil {
		t.Fatalf("VerifyFile returned unexpected error: %v", err)
	}
	assertReleaseEvidenceFindingContains(t, findings, "manifest.seed_gap_state_missing")
	assertReleaseEvidenceFindingContains(t, findings, "CLAIM_ADMIN_BOOTSTRAP_READY")
}

func TestPackage0SeedModeRejectsOpenAndClosedSeedGapConflict(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	body := appendReleaseEvidenceItem(validReleaseEvidenceManifest(), seedGapReplacementItem(seedGapSpecByID(t, "seed_gap_admin_bootstrap_ready_open"), `"bash","scripts/pass.sh"`, "scripts/pass.sh", "implemented"))
	path := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, path, body)

	findings, err := VerifyFile(path, Options{Mode: ManifestModeSeed, RepoRoot: root, ExecuteRequired: false})
	if err != nil {
		t.Fatalf("VerifyFile returned unexpected error: %v", err)
	}
	assertReleaseEvidenceFindingContains(t, findings, "manifest.seed_gap_state_conflict")
	assertReleaseEvidenceFindingContains(t, findings, "seed_gap_admin_bootstrap_ready_open")
}

func TestPackage0SeedModeAllowsTargetCapabilityVocabulary(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	body := validReleaseEvidenceManifest()
	for _, capability := range package0CapabilityMatrixV1Targets() {
		body = appendReleaseEvidenceItem(body, package0CapabilityVocabularyItem(capability, capability.optionalGated, capability.defaultGARequired))
	}
	path := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, path, body)

	findings, err := VerifyFile(path, Options{Mode: ManifestModeSeed, RepoRoot: root, ExecuteRequired: false})
	if err != nil {
		t.Fatalf("VerifyFile returned unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("VerifyFile returned findings for seed capability vocabulary: %+v", findings)
	}
	assertNoReleaseEvidenceFindingContains(t, findings, "capability_id")
}

func TestPackage0CapabilityMatrixV1ClassificationRejectsDrift(t *testing.T) {
	for _, capability := range package0CapabilityMatrixV1Targets() {
		t.Run(capability.id+"_optional_gated_drift", func(t *testing.T) {
			root := releaseEvidenceFixtureRoot(t)
			body := validReleaseEvidenceManifest()
			body = appendReleaseEvidenceItem(body, package0CapabilityVocabularyItem(capability, !capability.optionalGated, capability.defaultGARequired))
			path := filepath.Join(root, "manifest.json")
			writeReleaseEvidenceFile(t, path, body)

			findings, err := VerifyFile(path, Options{Mode: ManifestModeSeed, RepoRoot: root, ExecuteRequired: false})
			if err != nil {
				t.Fatalf("VerifyFile returned unexpected error: %v", err)
			}
			assertReleaseEvidenceFindingContains(t, findings, "target_capability_"+capability.id)
			assertReleaseEvidenceFindingContains(t, findings, "optional_gated")
		})

		t.Run(capability.id+"_default_ga_required_drift", func(t *testing.T) {
			root := releaseEvidenceFixtureRoot(t)
			body := validReleaseEvidenceManifest()
			body = appendReleaseEvidenceItem(body, package0CapabilityVocabularyItem(capability, capability.optionalGated, !capability.defaultGARequired))
			path := filepath.Join(root, "manifest.json")
			writeReleaseEvidenceFile(t, path, body)

			findings, err := VerifyFile(path, Options{Mode: ManifestModeSeed, RepoRoot: root, ExecuteRequired: false})
			if err != nil {
				t.Fatalf("VerifyFile returned unexpected error: %v", err)
			}
			assertReleaseEvidenceFindingContains(t, findings, "target_capability_"+capability.id)
			assertReleaseEvidenceFindingContains(t, findings, "default_ga_required")
		})
	}
}

func TestPackage0FinalModeRejectsLegacyCompatibilityCapabilities(t *testing.T) {
	tests := []struct {
		name         string
		capabilityID string
	}{
		{name: "storage", capabilityID: "storage"},
		{name: "jvs", capabilityID: "jvs"},
		{name: "workload mount", capabilityID: "workload_mount"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := releaseEvidenceFixtureRoot(t)
			body := appendReleaseEvidenceItem(validReleaseEvidenceManifest(), `"id":"legacy_`+tt.capabilityID+`_compat","claim_id":"CLAIM_PROFILE_BOUNDARY","subclaim_id":"legacy_capability_compat","acceptance_id":"P0_LEGACY_CAPABILITY_COMPAT","risk_id":"","fixture_id":"","capability_id":"`+tt.capabilityID+`","evidence_profile":"default","default_mode":true,"fixture_enabled_mode":false,"expected_runtime":"fast","scope":"package","negative_or_positive":"both","evidence_type":"unit","required":false,"command":["bash","scripts/pass.sh"],"anchors":["scripts/pass.sh"],"doc_only_allowed":false,"optional_gated":false,"default_ga_required":true,"pass_criteria":{"kind":"coverage_guard","assertions":["legacy capability compatibility is seed-only"]}`)
			path := filepath.Join(root, "manifest.json")
			writeReleaseEvidenceFile(t, path, body)

			findings, err := VerifyFile(path, finalReleaseOptions(t, root, nil))
			if err != nil {
				t.Fatalf("VerifyFile returned unexpected error: %v", err)
			}
			assertReleaseEvidenceFindingContains(t, findings, "item.capability_id_legacy_final_invalid")
			assertReleaseEvidenceFindingContains(t, findings, tt.capabilityID)
		})
	}
}

type package0CapabilityMatrixV1Target struct {
	id                string
	optionalGated     bool
	defaultGARequired bool
}

func package0CapabilityMatrixV1Targets() []package0CapabilityMatrixV1Target {
	var targets []package0CapabilityMatrixV1Target
	for _, row := range capability.CapabilityMatrixV1Rows() {
		targets = append(targets, package0CapabilityMatrixV1Target{
			id:                string(row.ID),
			optionalGated:     row.OptionalGated,
			defaultGARequired: row.DefaultGARequired,
		})
	}
	return targets
}

func package0CapabilityVocabularyItem(capability package0CapabilityMatrixV1Target, optionalGated, defaultGARequired bool) string {
	return `"id":"target_capability_` + capability.id + `","claim_id":"CLAIM_PROFILE_BOUNDARY","subclaim_id":"target_capability_vocabulary","acceptance_id":"P0_TARGET_CAPABILITY_VOCABULARY","risk_id":"","fixture_id":"","capability_id":"` + capability.id + `","evidence_profile":"default","default_mode":true,"fixture_enabled_mode":false,"expected_runtime":"fast","scope":"doc-guard","negative_or_positive":"negative","evidence_type":"doc-guard","required":false,"command":[],"anchors":["docs/GA_NEXT_PHASE_DEVELOPMENT_HANDOFF_PLAN.md"],"doc_only_allowed":true,"optional_gated":` + package0BoolLiteral(optionalGated) + `,"default_ga_required":` + package0BoolLiteral(defaultGARequired) + `,"pass_criteria":{"kind":"coverage_guard","assertions":["target capability vocabulary remains accepted in seed mode"]}`
}

func package0BoolLiteral(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func TestPackage0FinalModeRequiresReplacementEvidenceForRequiredSeedGaps(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	body := withoutPackage0SeedGapMarkers(validReleaseEvidenceManifest())
	path := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, path, body)

	findings, err := VerifyFile(path, finalReleaseOptions(t, root, nil))
	if err != nil {
		t.Fatalf("VerifyFile returned unexpected error: %v", err)
	}
	assertReleaseEvidenceFindingContains(t, findings, "manifest.final_required_claim_missing")
	assertReleaseEvidenceFindingContains(t, findings, "CLAIM_ADMIN_BOOTSTRAP_READY")
	assertReleaseEvidenceFindingContains(t, findings, "CLAIM_DEFAULT_USER_LOOP")
	assertNoReleaseEvidenceFindingContains(t, findings, "manifest.final_seed_gap_open")
	assertNoReleaseEvidenceFindingContains(t, findings, "CLAIM_OPTIONAL_FIXTURE_CONFORMANT")
	assertNoReleaseEvidenceFindingContains(t, findings, "CLAIM_WORKLOAD_FIXTURE_READY")
}

func TestPackage0FinalModeRejectsFakeSameClaimReplacementForDeletedSeedGap(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	body := withoutPackage0SeedGapMarker(validReleaseEvidenceManifest(), "seed_gap_admin_bootstrap_ready_open")
	body = appendReleaseEvidenceItem(body, `"id":"admin_bootstrap_fake_same_claim_unit","claim_id":"CLAIM_ADMIN_BOOTSTRAP_READY","subclaim_id":"admin_bootstrap_fake_status","acceptance_id":"P0_ADMIN_BOOTSTRAP_FAKE_STATUS","risk_id":"F99","fixture_id":"","capability_id":"admin_bootstrap","evidence_profile":"default","default_mode":true,"fixture_enabled_mode":false,"expected_runtime":"fast","scope":"package","negative_or_positive":"positive","evidence_type":"unit","required":true,"command":["bash","scripts/pass.sh"],"anchors":["scripts/pass.sh"],"doc_only_allowed":false,"optional_gated":false,"default_ga_required":true,"pass_criteria":{"kind":"positive_path","assertions":["fake same-claim evidence must not close admin bootstrap seed gap"]}`)
	path := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, path, body)

	findings, err := VerifyFile(path, finalReleaseOptions(t, root, nil))
	if err != nil {
		t.Fatalf("VerifyFile returned unexpected error: %v", err)
	}
	assertReleaseEvidenceFindingContains(t, findings, "manifest.final_required_claim_missing")
	assertReleaseEvidenceFindingContains(t, findings, "seed_gap_admin_bootstrap_ready_open")
	assertReleaseEvidenceFindingContains(t, findings, "CLAIM_ADMIN_BOOTSTRAP_READY")
	assertReleaseEvidenceFindingContains(t, findings, "admin_bootstrap_ready")
}

func TestPackage0FinalModeRejectsExactReplacementShapeWithFakeAssertionForDeletedSeedGap(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	body := withoutPackage0SeedGapMarker(validReleaseEvidenceManifest(), "seed_gap_admin_bootstrap_ready_open")
	body = appendReleaseEvidenceItem(body, `"id":"admin_bootstrap_ready_unit","claim_id":"CLAIM_ADMIN_BOOTSTRAP_READY","subclaim_id":"admin_bootstrap_ready","acceptance_id":"P0_ADMIN_BOOTSTRAP_READY","risk_id":"F3","fixture_id":"","capability_id":"admin_bootstrap","evidence_profile":"default","default_mode":true,"fixture_enabled_mode":false,"expected_runtime":"fast","scope":"package","negative_or_positive":"positive","evidence_type":"unit","required":true,"command":["bash","scripts/pass.sh"],"anchors":["scripts/pass.sh"],"doc_only_allowed":false,"optional_gated":false,"default_ga_required":true,"pass_criteria":{"kind":"positive_path","assertions":["fake assertion text must not close admin bootstrap seed gap"]}`)
	path := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, path, body)

	findings, err := VerifyFile(path, finalReleaseOptions(t, root, nil))
	if err != nil {
		t.Fatalf("VerifyFile returned unexpected error: %v", err)
	}
	assertReleaseEvidenceFindingContains(t, findings, "manifest.final_required_claim_missing")
	assertReleaseEvidenceFindingContains(t, findings, "seed_gap_admin_bootstrap_ready_open")
	assertReleaseEvidenceFindingContains(t, findings, "CLAIM_ADMIN_BOOTSTRAP_READY")
	assertReleaseEvidenceFindingContains(t, findings, "admin_bootstrap_ready")
}

func TestPackage0FinalModeAcceptsExpectedAssertionForDeletedAdminSeedGap(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	body := withoutPackage0SeedGapMarker(validReleaseEvidenceManifest(), "seed_gap_admin_bootstrap_ready_open")
	body = appendReleaseEvidenceItem(body, `"id":"admin_bootstrap_ready_unit","claim_id":"CLAIM_ADMIN_BOOTSTRAP_READY","subclaim_id":"admin_bootstrap_ready","acceptance_id":"P0_ADMIN_BOOTSTRAP_READY","risk_id":"F3","fixture_id":"","capability_id":"admin_bootstrap","evidence_profile":"default","default_mode":true,"fixture_enabled_mode":false,"expected_runtime":"fast","scope":"package","negative_or_positive":"positive","evidence_type":"unit","required":true,"command":["bash","scripts/pass.sh"],"anchors":["scripts/pass.sh"],"doc_only_allowed":false,"optional_gated":false,"default_ga_required":true,"pass_criteria":{"kind":"positive_path","assertions":["admin bootstrap readiness passes in default mode"]}`)
	path := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, path, body)

	findings, err := VerifyFile(path, finalReleaseOptions(t, root, nil))
	if err != nil {
		t.Fatalf("VerifyFile returned unexpected error: %v", err)
	}
	assertNoReleaseEvidenceFindingContains(t, findings, "seed_gap_admin_bootstrap_ready_open")
	assertNoReleaseEvidenceFindingContains(t, findings, "CLAIM_ADMIN_BOOTSTRAP_READY")
	assertNoReleaseEvidenceFindingContains(t, findings, "admin_bootstrap_ready")
}

func TestPackage0FinalModeDoesNotRequireOrdinaryOptionalFixtureSeedGapReplacement(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	body := withoutPackage0SeedGapMarker(validReleaseEvidenceManifest(), "seed_gap_workload_fixture_ready_open")
	body = withoutPackage0SeedGapMarker(body, "seed_gap_optional_fixture_conformant_open")
	body = withoutPackage0SeedGapMarker(body, "seed_gap_purge_approval_safe_open")
	body = withoutPackage0SeedGapMarker(body, "seed_gap_template_quota_boundary_open")
	path := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, path, body)

	findings, err := VerifyFile(path, finalReleaseOptions(t, root, nil))
	if err != nil {
		t.Fatalf("VerifyFile returned unexpected error: %v", err)
	}
	assertNoReleaseEvidenceFindingContains(t, findings, "CLAIM_WORKLOAD_FIXTURE_READY")
	assertNoReleaseEvidenceFindingContains(t, findings, "CLAIM_OPTIONAL_FIXTURE_CONFORMANT")
	assertNoReleaseEvidenceFindingContains(t, findings, "CLAIM_PURGE_APPROVAL_SAFE")
	assertNoReleaseEvidenceFindingContains(t, findings, "CLAIM_TEMPLATE_QUOTA_BOUNDARY")
}

func TestPackage0FinalModeRequiresTargetedOptionalFixtureClaimsWhenExplicitlyRequired(t *testing.T) {
	tests := []struct {
		name       string
		seedGapID  string
		claimID    string
		subclaimID string
		capability string
		fixtureID  string
		acceptance string
		riskID     string
		assertion  string
	}{
		{
			name:       "purge approval",
			seedGapID:  "seed_gap_purge_approval_safe_open",
			claimID:    "CLAIM_PURGE_APPROVAL_SAFE",
			subclaimID: "purge_approval_safe",
			capability: "repo_purge",
			fixtureID:  "repo-purge-fixture",
			acceptance: "P0_PURGE_APPROVAL_FAKE_SMOKE",
			riskID:     "F13",
			assertion:  "fake purge fixture evidence must not close explicit purge approval conformance",
		},
		{
			name:       "template quota",
			seedGapID:  "seed_gap_template_quota_boundary_open",
			claimID:    "CLAIM_TEMPLATE_QUOTA_BOUNDARY",
			subclaimID: "template_quota_boundary",
			capability: "repo_template",
			fixtureID:  "repo-template-fixture",
			acceptance: "P0_TEMPLATE_QUOTA_FAKE_SMOKE",
			riskID:     "F16",
			assertion:  "fake template fixture evidence must not close explicit template quota conformance",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := releaseEvidenceFixtureRoot(t)
			body := withoutPackage0SeedGapMarker(validReleaseEvidenceManifest(), tt.seedGapID)
			body = appendReleaseEvidenceItem(body, `"id":"`+tt.seedGapID+`_fake_required_fixture","claim_id":"`+tt.claimID+`","subclaim_id":"fake_required_fixture_smoke","acceptance_id":"`+tt.acceptance+`","risk_id":"`+tt.riskID+`","fixture_id":"`+tt.fixtureID+`","capability_id":"`+tt.capability+`","evidence_profile":"repo-local-fixture-enabled","default_mode":false,"fixture_enabled_mode":true,"expected_runtime":"fast","scope":"package","negative_or_positive":"positive","evidence_type":"unit","required":true,"command":["bash","scripts/pass.sh"],"anchors":["scripts/pass.sh"],"doc_only_allowed":false,"optional_gated":true,"default_ga_required":false,"pass_criteria":{"kind":"positive_path","assertions":["`+tt.assertion+`"]}`)
			path := filepath.Join(root, "manifest.json")
			writeReleaseEvidenceFile(t, path, body)

			findings, err := VerifyFile(path, finalReleaseOptions(t, root, []string{tt.capability}))
			if err != nil {
				t.Fatalf("VerifyFile returned unexpected error: %v", err)
			}
			assertReleaseEvidenceFindingContains(t, findings, "manifest.final_required_claim_missing")
			assertReleaseEvidenceFindingContains(t, findings, tt.seedGapID)
			assertReleaseEvidenceFindingContains(t, findings, tt.claimID)
			assertReleaseEvidenceFindingContains(t, findings, tt.subclaimID)
		})
	}
}

func TestPackage0FinalModeRequiresTargetedOptionalFixtureConformanceWhenExplicitlyRequired(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	body := withoutPackage0SeedGapMarker(validReleaseEvidenceManifest(), "seed_gap_optional_fixture_conformant_open")
	body = appendReleaseEvidenceItem(body, `"id":"optional_fixture_fake_same_claim_unit","claim_id":"CLAIM_OPTIONAL_FIXTURE_CONFORMANT","subclaim_id":"optional_fixture_fake_smoke","acceptance_id":"P0_OPTIONAL_FIXTURE_FAKE_SMOKE","risk_id":"F99","fixture_id":"workload-fixture","capability_id":"workload_mount_binding","evidence_profile":"repo-local-fixture-enabled","default_mode":false,"fixture_enabled_mode":true,"expected_runtime":"fast","scope":"package","negative_or_positive":"positive","evidence_type":"unit","required":true,"command":["bash","scripts/pass.sh"],"anchors":["scripts/pass.sh"],"doc_only_allowed":false,"optional_gated":true,"default_ga_required":false,"pass_criteria":{"kind":"positive_path","assertions":["fake optional fixture evidence must not close explicit fixture conformance"]}`)
	path := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, path, body)

	findings, err := VerifyFile(path, finalReleaseOptions(t, root, []string{"workload_mount_binding"}))
	if err != nil {
		t.Fatalf("VerifyFile returned unexpected error: %v", err)
	}
	assertReleaseEvidenceFindingContains(t, findings, "manifest.final_required_claim_missing")
	assertReleaseEvidenceFindingContains(t, findings, "seed_gap_optional_fixture_conformant_open")
	assertReleaseEvidenceFindingContains(t, findings, "CLAIM_OPTIONAL_FIXTURE_CONFORMANT")
	assertReleaseEvidenceFindingContains(t, findings, "optional_fixture_conformant")
}

func TestPackage0FinalModeDoesNotTreatNegativeNoFixtureItemAsOptionalFixtureConformance(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	body := withoutPackage0SeedGapMarker(validReleaseEvidenceManifest(), "seed_gap_optional_fixture_conformant_open")
	body = appendReleaseEvidenceItem(body, `"id":"optional_fixture_negative_no_fixture_unit","claim_id":"CLAIM_OPTIONAL_FIXTURE_CONFORMANT","subclaim_id":"optional_fixture_negative_guard","acceptance_id":"P0_OPTIONAL_FIXTURE_NEGATIVE_GUARD","risk_id":"F9","fixture_id":"","capability_id":"workload_mount_binding","evidence_profile":"default","default_mode":true,"fixture_enabled_mode":false,"expected_runtime":"fast","scope":"package","negative_or_positive":"negative","evidence_type":"unit","required":true,"command":["bash","scripts/pass.sh"],"anchors":["scripts/pass.sh"],"doc_only_allowed":false,"optional_gated":true,"default_ga_required":false,"pass_criteria":{"kind":"denial_safety","assertions":["negative no-fixture evidence is not explicit optional fixture conformance"]}`)
	path := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, path, body)

	findings, err := VerifyFile(path, finalReleaseOptions(t, root, []string{"workload_mount_binding"}))
	if err != nil {
		t.Fatalf("VerifyFile returned unexpected error: %v", err)
	}
	assertReleaseEvidenceFindingContains(t, findings, "seed_gap_optional_fixture_conformant_open")
	assertReleaseEvidenceFindingContains(t, findings, "CLAIM_OPTIONAL_FIXTURE_CONFORMANT")
	assertReleaseEvidenceFindingContains(t, findings, "optional_fixture_conformant")
}

func TestPackage0DefaultUserLoopCannotBeSatisfiedByNegativeEvidence(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	body := replacePackage0FieldForItem(validReleaseEvidenceManifest(), "webdav_export_disabled_admission_unit", `"claim_id":"CLAIM_DEFAULT_DENIAL_SAFE"`, `"claim_id":"CLAIM_DEFAULT_USER_LOOP"`)
	path := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, path, body)

	findings, err := VerifyFile(path, Options{Mode: ManifestModeSeed, RepoRoot: root, ExecuteRequired: false})
	if err != nil {
		t.Fatalf("VerifyFile returned unexpected error: %v", err)
	}
	assertReleaseEvidenceFindingContains(t, findings, "CLAIM_DEFAULT_USER_LOOP")
	assertReleaseEvidenceFindingContains(t, findings, "positive")
}

func removePackage0FieldForItem(body, id, value string) string {
	return replacePackage0FieldForItem(body, id, value, "")
}

func replacePackage0FieldForItem(body, id, oldValue, newValue string) string {
	idIndex := strings.Index(body, `"id":"`+id+`"`)
	if idIndex < 0 {
		return body
	}
	fieldIndex := strings.Index(body[idIndex:], oldValue)
	if fieldIndex < 0 {
		return body
	}
	start := idIndex + fieldIndex
	return body[:start] + newValue + body[start+len(oldValue):]
}

func replacePackage0ItemID(body, oldID, newID string) string {
	return strings.Replace(body, `"id":"`+oldID+`"`, `"id":"`+newID+`"`, 1)
}

func withoutPackage0SeedGapMarkers(body string) string {
	start := strings.Index(body, ",\n    {\"id\":\"seed_gap_")
	if start < 0 {
		return body
	}
	end := strings.LastIndex(body, "\n  ]\n}")
	if end < start {
		return body
	}
	return body[:start] + body[end:]
}

func withoutPackage0SeedGapMarker(body, id string) string {
	lines := strings.Split(body, "\n")
	removed := -1
	for index, line := range lines {
		if strings.Contains(line, `"id":"`+id+`"`) {
			removed = index
			lines = append(lines[:index], lines[index+1:]...)
			break
		}
	}
	if removed < 0 {
		return body
	}
	for index := removed - 1; index >= 0; index-- {
		if strings.TrimSpace(lines[index]) == "" {
			continue
		}
		if removed < len(lines) && strings.TrimSpace(lines[removed]) == "]" {
			lines[index] = strings.TrimSuffix(lines[index], ",")
		}
		break
	}
	return strings.Join(lines, "\n")
}
