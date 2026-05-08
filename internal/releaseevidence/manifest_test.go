package releaseevidence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyFileFailsForMissingAndMalformedManifest(t *testing.T) {
	root := t.TempDir()

	if findings, err := VerifyFile(filepath.Join(root, "missing.json"), Options{RepoRoot: root}); err == nil && len(findings) == 0 {
		t.Fatal("VerifyFile accepted a missing manifest")
	}

	path := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, path, `{`)
	if findings, err := VerifyFile(path, Options{RepoRoot: root}); err == nil && len(findings) == 0 {
		t.Fatal("VerifyFile accepted malformed JSON")
	}
}

func TestValidateManifestRequiresTopLevelAndItemFields(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "missing schema version",
			body: `{"release_gate":"scripts/verify-ga-release.sh","items":[]}`,
			want: "schema_version",
		},
		{
			name: "missing release gate",
			body: `{"schema_version":"1","items":[]}`,
			want: "release_gate",
		},
		{
			name: "missing item id",
			body: `{"schema_version":"1","release_gate":"scripts/verify-ga-release.sh","items":[{"capability_id":"storage","evidence_type":"unit","required":true,"command":["go","test","./internal/capability"],"anchors":["go.mod"],"doc_only_allowed":false,"optional_gated":false,"default_ga_required":true}]}`,
			want: "id",
		},
		{
			name: "missing command",
			body: `{"schema_version":"1","release_gate":"scripts/verify-ga-release.sh","items":[{"id":"storage_unit","capability_id":"storage","evidence_type":"unit","required":true,"anchors":["go.mod"],"doc_only_allowed":false,"optional_gated":false,"default_ga_required":true}]}`,
			want: "command",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := releaseEvidenceFixtureRoot(t)
			path := filepath.Join(root, "manifest.json")
			writeReleaseEvidenceFile(t, path, tt.body)

			findings, err := VerifyFile(path, Options{RepoRoot: root})
			if err != nil {
				t.Fatalf("VerifyFile returned unexpected error: %v", err)
			}
			assertReleaseEvidenceFindingContains(t, findings, tt.want)
		})
	}
}

func TestValidateManifestRejectsUnknownEvidenceTypesAndCapabilities(t *testing.T) {
	tests := []struct {
		name string
		edit string
		want string
	}{
		{name: "bad evidence type", edit: `"evidence_type":"manual"`, want: "evidence_type"},
		{name: "bad capability", edit: `"capability_id":"sibling_project"`, want: "capability_id"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := releaseEvidenceFixtureRoot(t)
			body := strings.Replace(validReleaseEvidenceManifest(), `"evidence_type":"unit"`, tt.edit, 1)
			if tt.name == "bad capability" {
				body = strings.Replace(validReleaseEvidenceManifest(), `"capability_id":"repo_template"`, tt.edit, 1)
			}
			path := filepath.Join(root, "manifest.json")
			writeReleaseEvidenceFile(t, path, body)

			findings, err := VerifyFile(path, Options{RepoRoot: root})
			if err != nil {
				t.Fatalf("VerifyFile returned unexpected error: %v", err)
			}
			assertReleaseEvidenceFindingContains(t, findings, tt.want)
		})
	}
}

func TestValidateManifestAllowsOnlyStableEvidenceTypeSet(t *testing.T) {
	for _, evidenceType := range []string{
		"unit",
		"contract",
		"schema",
		"openapi",
		"generated-client",
		"integration",
		"e2e",
		"provenance",
		"race",
		"doc-guard",
	} {
		t.Run(evidenceType, func(t *testing.T) {
			root := releaseEvidenceFixtureRoot(t)
			body := strings.Replace(validReleaseEvidenceManifest(), `"evidence_type":"unit"`, `"evidence_type":"`+evidenceType+`"`, 1)
			if evidenceType == "doc-guard" {
				body = strings.Replace(body, `"doc_only_allowed":false`, `"doc_only_allowed":true`, 1)
			}
			path := filepath.Join(root, "manifest.json")
			writeReleaseEvidenceFile(t, path, body)

			findings, err := VerifyFile(path, Options{RepoRoot: root})
			if err != nil {
				t.Fatalf("VerifyFile returned unexpected error: %v", err)
			}
			assertNoReleaseEvidenceFindingContains(t, findings, "evidence_type")
		})
	}
}

func TestValidateManifestRejectsUnsafeRequiredCommands(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    string
	}{
		{name: "empty command", command: `"command":[]`, want: "command"},
		{name: "parent traversal", command: `"command":["bash","../outside.sh"]`, want: "../"},
		{name: "sibling repo token", command: `"command":["bash","mbos-sandbox/scripts/check.sh"]`, want: "sibling"},
		{name: "manual approval", command: `"command":["bash","scripts/manual-approval.sh"]`, want: "manual"},
		{name: "owner signoff", command: `"command":["bash","scripts/owner-signoff.sh"]`, want: "signoff"},
		{name: "unknown executable", command: `"command":["curl","https://example.invalid"]`, want: "executable"},
		{name: "bash inline shell", command: `"command":["bash","-c","cd .."]`, want: "inline"},
		{name: "bash login inline shell cluster", command: `"command":["bash","-lc","cd .."]`, want: "inline"},
		{name: "bash errexit inline shell cluster", command: `"command":["bash","-ec","cd .."]`, want: "inline"},
		{name: "bash long command inline shell", command: `"command":["bash","--command","cd .."]`, want: "inline"},
		{name: "bash missing script target", command: `"command":["bash","foo"]`, want: "script"},
		{name: "git remote command", command: `"command":["git","ls-remote","https://example.invalid"]`, want: "git"},
		{name: "go run command", command: `"command":["go","run","./cmd/afscp-evidence-verify"]`, want: "go test"},
		{name: "go env command", command: `"command":["go","env"]`, want: "go test"},
		{name: "go install command", command: `"command":["go","install","./cmd/afscp-evidence-verify"]`, want: "go test"},
		{name: "go test missing package target", command: `"command":["go","test","-run","TestExistingEvidenceSelector$"]`, want: "package"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := releaseEvidenceFixtureRoot(t)
			body := strings.Replace(validReleaseEvidenceManifest(), `"command":["bash","scripts/pass.sh"]`, tt.command, 1)
			path := filepath.Join(root, "manifest.json")
			writeReleaseEvidenceFile(t, path, body)

			findings, err := VerifyFile(path, Options{RepoRoot: root})
			if err != nil {
				t.Fatalf("VerifyFile returned unexpected error: %v", err)
			}
			assertReleaseEvidenceFindingContains(t, findings, tt.want)
		})
	}
}

func TestValidateManifestRejectsUnsafeCommandsOnNonRequiredItems(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	body := strings.Replace(validReleaseEvidenceManifest(), `"required":true`, `"required":false`, 1)
	body = strings.Replace(body, `"command":["bash","scripts/pass.sh"]`, `"command":["bash","scripts/manual-approval.sh"]`, 1)
	path := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, path, body)

	findings, err := VerifyFile(path, Options{RepoRoot: root, ExecuteRequired: false})
	if err != nil {
		t.Fatalf("VerifyFile returned unexpected error: %v", err)
	}
	assertReleaseEvidenceFindingContains(t, findings, "manual")
}

func TestValidateManifestRejectsMissingRepoLocalCommandTargetsInCheckOnly(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    string
	}{
		{
			name:    "missing go test package",
			command: `"command":["go","test","./internal/missingpkg"]`,
			want:    "package",
		},
		{
			name:    "missing bash script",
			command: `"command":["bash","scripts/missing.sh"]`,
			want:    "script",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := releaseEvidenceFixtureRoot(t)
			path := filepath.Join(root, "manifest.json")
			body := strings.Replace(validReleaseEvidenceManifest(), `"command":["bash","scripts/pass.sh"]`, tt.command, 1)
			writeReleaseEvidenceFile(t, path, body)

			findings, err := VerifyFile(path, Options{RepoRoot: root, ExecuteRequired: false})
			if err != nil {
				t.Fatalf("VerifyFile returned unexpected error: %v", err)
			}
			assertReleaseEvidenceFindingContains(t, findings, "missing")
			assertReleaseEvidenceFindingContains(t, findings, tt.want)
		})
	}
}

func TestValidateManifestRejectsGoTestRunSelectorThatMatchesNoTestsInCheckOnly(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	body := strings.Replace(validReleaseEvidenceManifest(), `"command":["bash","scripts/pass.sh"]`, `"command":["go","test","./internal/evidencetest","-run","TestDefinitelyDoesNotExistForEvidenceReview$"]`, 1)
	path := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, path, body)

	findings, err := VerifyFile(path, Options{RepoRoot: root, ExecuteRequired: false})
	if err != nil {
		t.Fatalf("VerifyFile returned unexpected error: %v", err)
	}
	assertReleaseEvidenceFindingContains(t, findings, "run")
	assertReleaseEvidenceFindingContains(t, findings, "match")
}

func TestValidateManifestRejectsGoTestAllPackagesRunSelectorThatMatchesNoTestsInCheckOnly(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	body := strings.Replace(validReleaseEvidenceManifest(), `"command":["bash","scripts/pass.sh"]`, `"command":["go","test","./...","-run","TestDefinitelyDoesNotExistForEvidenceReview$"]`, 1)
	path := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, path, body)

	findings, err := VerifyFile(path, Options{RepoRoot: root, ExecuteRequired: false})
	if err != nil {
		t.Fatalf("VerifyFile returned unexpected error: %v", err)
	}
	assertReleaseEvidenceFindingContains(t, findings, "run")
	assertReleaseEvidenceFindingContains(t, findings, "match")
}

func TestValidateManifestRejectsGoTestRunSelectorThatOnlyMatchesBenchmark(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	body := strings.Replace(validReleaseEvidenceManifest(), `"command":["bash","scripts/pass.sh"]`, `"command":["go","test","./internal/benchonly","-run","BenchmarkEvidenceOnly$"]`, 1)
	path := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, path, body)

	findings, err := VerifyFile(path, Options{RepoRoot: root, ExecuteRequired: false})
	if err != nil {
		t.Fatalf("VerifyFile returned unexpected error: %v", err)
	}
	assertReleaseEvidenceFindingContains(t, findings, "Benchmark")
	assertReleaseEvidenceFindingContains(t, findings, "go test -run")
}

func TestValidateManifestRejectsDocOnlyEvidenceForNonDocItems(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "all anchors are docs",
			body: strings.Replace(validReleaseEvidenceManifest(), `"anchors":["scripts/pass.sh"]`, `"anchors":["docs/READINESS_EVIDENCE.md"]`, 1),
			want: "docs/",
		},
		{
			name: "doc guard type",
			body: strings.Replace(validReleaseEvidenceManifest(), `"evidence_type":"unit"`, `"evidence_type":"doc-guard"`, 1),
			want: "doc-guard",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := releaseEvidenceFixtureRoot(t)
			path := filepath.Join(root, "manifest.json")
			writeReleaseEvidenceFile(t, path, tt.body)

			findings, err := VerifyFile(path, Options{RepoRoot: root})
			if err != nil {
				t.Fatalf("VerifyFile returned unexpected error: %v", err)
			}
			assertReleaseEvidenceFindingContains(t, findings, tt.want)
		})
	}
}

func TestValidateManifestChecksCapabilityClassification(t *testing.T) {
	tests := []struct {
		name string
		edit func(string) string
		want string
	}{
		{
			name: "default ga capability cannot be optional gated",
			edit: func(body string) string {
				return appendReleaseEvidenceItem(body, `"id":"bad_storage_optional","capability_id":"storage","evidence_type":"unit","required":true,"command":["bash","scripts/pass.sh"],"anchors":["scripts/pass.sh"],"doc_only_allowed":false,"optional_gated":true,"default_ga_required":true`)
			},
			want: "optional_gated",
		},
		{
			name: "default ga capability must be default required",
			edit: func(body string) string {
				return appendReleaseEvidenceItem(body, `"id":"bad_jvs_not_default","capability_id":"jvs","evidence_type":"unit","required":true,"command":["bash","scripts/pass.sh"],"anchors":["scripts/pass.sh"],"doc_only_allowed":false,"optional_gated":false,"default_ga_required":false`)
			},
			want: "default_ga_required",
		},
		{
			name: "optional capability cannot be default required",
			edit: func(body string) string {
				return appendReleaseEvidenceItem(body, `"id":"bad_workload_default","capability_id":"workload_mount","evidence_type":"unit","required":true,"command":["bash","scripts/pass.sh"],"anchors":["scripts/pass.sh"],"doc_only_allowed":false,"optional_gated":true,"default_ga_required":true`)
			},
			want: "default_ga_required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := releaseEvidenceFixtureRoot(t)
			path := filepath.Join(root, "manifest.json")
			writeReleaseEvidenceFile(t, path, tt.edit(validReleaseEvidenceManifest()))

			findings, err := VerifyFile(path, Options{RepoRoot: root})
			if err != nil {
				t.Fatalf("VerifyFile returned unexpected error: %v", err)
			}
			assertReleaseEvidenceFindingContains(t, findings, tt.want)
		})
	}
}

func TestVerifyFileExecutesRequiredCommandsAndPropagatesFailure(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	path := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, path, validReleaseEvidenceManifest())

	findings, err := VerifyFile(path, Options{RepoRoot: root, ExecuteRequired: true})
	if err != nil {
		t.Fatalf("VerifyFile returned unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("VerifyFile returned findings for passing command: %+v", findings)
	}

	writeReleaseEvidenceFile(t, path, strings.Replace(validReleaseEvidenceManifest(), `"command":["bash","scripts/pass.sh"]`, `"command":["bash","scripts/fail.sh"]`, 1))
	findings, err = VerifyFile(path, Options{RepoRoot: root, ExecuteRequired: true})
	if err != nil {
		t.Fatalf("VerifyFile returned unexpected error: %v", err)
	}
	assertReleaseEvidenceFindingContains(t, findings, "failed")
}

func TestCurrentRepoManifestContainsOptionalCapabilityDisabledAdmissionEvidence(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	manifestPath := filepath.Join(repoRoot, "docs", "release-evidence", "ga-manifest.json")

	manifest, findings, err := LoadAndValidateFile(manifestPath, Options{RepoRoot: repoRoot})
	if err != nil {
		t.Fatalf("LoadAndValidateFile returned error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("current manifest findings: %+v", findings)
	}

	for _, capabilityID := range []string{"workload_mount", "repo_template", "repo_purge"} {
		item, ok := manifest.OptionalDisabledAdmissionEvidence(capabilityID)
		if !ok {
			t.Fatalf("manifest missing optional-gated disabled admission evidence for %s", capabilityID)
		}
		if !item.Required || item.EvidenceType != "unit" || item.DocOnlyAllowed {
			t.Fatalf("%s evidence = %+v, want required unit evidence with doc_only_allowed=false", capabilityID, item)
		}
	}
	for _, capabilityID := range []string{"repo_template", "repo_purge"} {
		item, ok := manifest.DisabledWorkerRecoveryEvidence(capabilityID)
		if !ok {
			t.Fatalf("manifest missing disabled worker recovery evidence for %s", capabilityID)
		}
		if !item.Required || item.EvidenceType != "unit" || item.DocOnlyAllowed {
			t.Fatalf("%s worker recovery evidence = %+v, want required unit evidence with doc_only_allowed=false", capabilityID, item)
		}
	}
}

func TestValidateManifestRequiresExactReleaseEvidenceItems(t *testing.T) {
	tests := []struct {
		name string
		edit func(string) string
		want string
	}{
		{
			name: "missing exact webdav admission id",
			edit: func(body string) string {
				return strings.Replace(body, `"id":"webdav_export_disabled_admission_unit"`, `"id":"webdav_export_disabled_admission_typo"`, 1)
			},
			want: "webdav_export_disabled_admission_unit",
		},
		{
			name: "missing exact template create recovery id",
			edit: func(body string) string {
				return strings.Replace(body, `"id":"repo_template_create_disabled_worker_recovery_unit"`, `"id":"repo_template_disabled_worker_recovery_unit"`, 1)
			},
			want: "repo_template_create_disabled_worker_recovery_unit",
		},
		{
			name: "wrong default ga metadata",
			edit: func(body string) string {
				return strings.Replace(body, `"default_ga_required":true`, `"default_ga_required":false`, 1)
			},
			want: "webdav_export_disabled_admission_unit",
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

func releaseEvidenceFixtureRoot(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	writeReleaseEvidenceFile(t, filepath.Join(root, "go.mod"), "module example.com/releaseevidencefixture\n\ngo 1.22\n")
	writeReleaseEvidenceFile(t, filepath.Join(root, "docs", "READINESS_EVIDENCE.md"), "fixture\n")
	writeReleaseEvidenceFile(t, filepath.Join(root, "scripts", "pass.sh"), "#!/usr/bin/env bash\nexit 0\n")
	writeReleaseEvidenceFile(t, filepath.Join(root, "scripts", "fail.sh"), "#!/usr/bin/env bash\nexit 1\n")
	writeReleaseEvidenceFile(t, filepath.Join(root, "internal", "evidencetest", "evidence_test.go"), `package evidencetest

import "testing"

func TestExistingEvidenceSelector(t *testing.T) {}
`)
	writeReleaseEvidenceFile(t, filepath.Join(root, "internal", "benchonly", "benchonly_test.go"), `package benchonly

import "testing"

func BenchmarkEvidenceOnly(b *testing.B) {}
`)
	return root
}

func validReleaseEvidenceManifest() string {
	return `{
  "schema_version":"1",
  "release_gate":"scripts/verify-ga-release.sh",
  "items":[
    {
      "id":"webdav_export_disabled_admission_unit",
      "capability_id":"webdav_export",
      "evidence_type":"unit",
      "required":true,
      "command":["bash","scripts/pass.sh"],
      "anchors":["scripts/pass.sh"],
      "doc_only_allowed":false,
      "optional_gated":false,
      "default_ga_required":true
    },
    {
      "id":"workload_mount_disabled_admission_unit",
      "capability_id":"workload_mount",
      "evidence_type":"unit",
      "required":true,
      "command":["bash","scripts/pass.sh"],
      "anchors":["scripts/pass.sh"],
      "doc_only_allowed":false,
      "optional_gated":true,
      "default_ga_required":false
    },
    {
      "id":"workload_mount_plan_store_freshness_unit",
      "capability_id":"workload_mount",
      "evidence_type":"unit",
      "required":true,
      "command":["bash","scripts/pass.sh"],
      "anchors":["scripts/pass.sh"],
      "doc_only_allowed":false,
      "optional_gated":true,
      "default_ga_required":false
    },
    {
      "id":"workload_mount_runtime_secretref_config_unit",
      "capability_id":"workload_mount",
      "evidence_type":"unit",
      "required":true,
      "command":["bash","scripts/pass.sh"],
      "anchors":["scripts/pass.sh"],
      "doc_only_allowed":false,
      "optional_gated":true,
      "default_ga_required":false
    },
    {
      "id":"workload_mount_secretref_redaction_unit",
      "capability_id":"workload_mount",
      "evidence_type":"unit",
      "required":true,
      "command":["bash","scripts/pass.sh"],
      "anchors":["scripts/pass.sh"],
      "doc_only_allowed":false,
      "optional_gated":true,
      "default_ga_required":false
    },
    {
      "id":"repo_template_disabled_admission_unit",
      "capability_id":"repo_template",
      "evidence_type":"unit",
      "required":true,
      "command":["bash","scripts/pass.sh"],
      "anchors":["scripts/pass.sh"],
      "doc_only_allowed":false,
      "optional_gated":true,
      "default_ga_required":false
    },
    {
      "id":"repo_template_create_disabled_worker_recovery_unit",
      "capability_id":"repo_template",
      "evidence_type":"unit",
      "required":true,
      "command":["bash","scripts/pass.sh"],
      "anchors":["scripts/pass.sh"],
      "doc_only_allowed":false,
      "optional_gated":true,
      "default_ga_required":false
    },
    {
      "id":"repo_template_clone_disabled_worker_recovery_unit",
      "capability_id":"repo_template",
      "evidence_type":"unit",
      "required":true,
      "command":["bash","scripts/pass.sh"],
      "anchors":["scripts/pass.sh"],
      "doc_only_allowed":false,
      "optional_gated":true,
      "default_ga_required":false
    },
    {
      "id":"repo_purge_disabled_admission_unit",
      "capability_id":"repo_purge",
      "evidence_type":"unit",
      "required":true,
      "command":["bash","scripts/pass.sh"],
      "anchors":["scripts/pass.sh"],
      "doc_only_allowed":false,
      "optional_gated":true,
      "default_ga_required":false
    },
    {
      "id":"repo_purge_disabled_worker_recovery_unit",
      "capability_id":"repo_purge",
      "evidence_type":"unit",
      "required":true,
      "command":["bash","scripts/pass.sh"],
      "anchors":["scripts/pass.sh"],
      "doc_only_allowed":false,
      "optional_gated":true,
      "default_ga_required":false
    },
    {
      "id":"default_ga_capability_classification_unit",
      "capability_id":"",
      "evidence_type":"unit",
      "required":true,
      "command":["bash","scripts/pass.sh"],
      "anchors":["scripts/pass.sh"],
      "doc_only_allowed":false,
      "optional_gated":false,
      "default_ga_required":false
    },
    {
      "id":"capability_admission_operation_coverage_unit",
      "capability_id":"",
      "evidence_type":"unit",
      "required":true,
      "command":["bash","scripts/pass.sh"],
      "anchors":["scripts/pass.sh"],
      "doc_only_allowed":false,
      "optional_gated":false,
      "default_ga_required":false
    },
    {
      "id":"release_script_evidence_manifest_guard",
      "capability_id":"",
      "evidence_type":"contract",
      "required":true,
      "command":["bash","scripts/pass.sh"],
      "anchors":["scripts/pass.sh"],
      "doc_only_allowed":false,
      "optional_gated":false,
      "default_ga_required":false
    }
  ]
}`
}

func writeReleaseEvidenceFile(t *testing.T, path, body string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func appendReleaseEvidenceItem(body, item string) string {
	return strings.Replace(body, "\n  ]\n}", ",\n    {"+item+"}\n  ]\n}", 1)
}

func assertReleaseEvidenceFindingContains(t *testing.T, findings []Finding, needle string) {
	t.Helper()

	for _, finding := range findings {
		if strings.Contains(finding.String(), needle) {
			return
		}
	}
	t.Fatalf("expected finding containing %q in %+v", needle, findings)
}

func assertNoReleaseEvidenceFindingContains(t *testing.T, findings []Finding, needle string) {
	t.Helper()

	for _, finding := range findings {
		if strings.Contains(finding.String(), needle) {
			t.Fatalf("did not expect finding containing %q in %+v", needle, findings)
		}
	}
}
