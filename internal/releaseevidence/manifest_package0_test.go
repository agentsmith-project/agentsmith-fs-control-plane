package releaseevidence

import (
	"path/filepath"
	"strings"
	"testing"
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
				return appendReleaseEvidenceItem(body, `"id":"optional_doc_trace_item","claim_id":"CLAIM_RELEASE_GATE_TRACEABLE","subclaim_id":"optional_doc_trace","acceptance_id":"P0_OPTIONAL_DOC_TRACE","risk_id":"","fixture_id":"","capability_id":"","evidence_profile":"default","default_mode":true,"fixture_enabled_mode":false,"negative_or_positive":"both","evidence_type":"doc-guard","required":false,"command":[],"anchors":["docs/GA_NEXT_PHASE_DEVELOPMENT_HANDOFF_PLAN.md"],"doc_only_allowed":true,"optional_gated":false,"default_ga_required":false,"pass_criteria":{}`)
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := releaseEvidenceFixtureRoot(t)
			path := filepath.Join(root, "manifest.json")
			writeReleaseEvidenceFile(t, path, tt.edit(validReleaseEvidenceManifest()))

			findings, err := VerifyFile(path, Options{RepoRoot: root, ExecuteRequired: false})
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
				return appendReleaseEvidenceItem(body, `"id":"workload_mount_fixture_positive_unit","claim_id":"CLAIM_OPTIONAL_FIXTURE_READY","subclaim_id":"workload_mount_fixture_smoke","acceptance_id":"P0_OPTIONAL_FIXTURE_POSITIVE","risk_id":"","fixture_id":"","capability_id":"workload_mount","evidence_profile":"repo-local-fixture-enabled","default_mode":false,"fixture_enabled_mode":true,"negative_or_positive":"positive","evidence_type":"unit","required":false,"command":["bash","scripts/pass.sh"],"anchors":["scripts/pass.sh"],"doc_only_allowed":false,"optional_gated":true,"default_ga_required":false,"pass_criteria":{"kind":"positive_path","assertions":["fixture-enabled workload mount smoke passes"]}`)
			},
			want: "fixture_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := releaseEvidenceFixtureRoot(t)
			path := filepath.Join(root, "manifest.json")
			writeReleaseEvidenceFile(t, path, tt.edit(validReleaseEvidenceManifest()))

			findings, err := VerifyFile(path, Options{RepoRoot: root, ExecuteRequired: false})
			if err != nil {
				t.Fatalf("VerifyFile returned unexpected error: %v", err)
			}
			assertReleaseEvidenceFindingContains(t, findings, tt.want)
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

	findings, err := VerifyFile(path, Options{RepoRoot: root, ExecuteRequired: false})
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

	findings, err := VerifyFile(path, Options{RepoRoot: root, ExecuteRequired: false})
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

			findings, err := VerifyFile(path, Options{RepoRoot: root, ExecuteRequired: false})
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

			findings, err := VerifyFile(path, Options{RepoRoot: root, ExecuteRequired: false})
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

			findings, err := VerifyFile(path, Options{RepoRoot: root, ExecuteRequired: false})
			if err != nil {
				t.Fatalf("VerifyFile returned unexpected error: %v", err)
			}
			assertReleaseEvidenceFindingContains(t, findings, tt.want)
		})
	}
}

func TestPackage0DefaultUserLoopCannotBeSatisfiedByNegativeEvidence(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	body := replacePackage0FieldForItem(validReleaseEvidenceManifest(), "webdav_export_disabled_admission_unit", `"claim_id":"CLAIM_DEFAULT_DENIAL_SAFE"`, `"claim_id":"CLAIM_DEFAULT_USER_LOOP"`)
	path := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, path, body)

	findings, err := VerifyFile(path, Options{RepoRoot: root, ExecuteRequired: false})
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
