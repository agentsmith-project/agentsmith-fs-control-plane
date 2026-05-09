package releaseevidence

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestVerifyFileFailsForMissingAndMalformedManifest(t *testing.T) {
	root := t.TempDir()

	if findings, err := VerifyFile(filepath.Join(root, "missing.json"), Options{Mode: ManifestModeSeed, RepoRoot: root}); err == nil && len(findings) == 0 {
		t.Fatal("VerifyFile accepted a missing manifest")
	}

	path := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, path, `{`)
	if findings, err := VerifyFile(path, Options{Mode: ManifestModeSeed, RepoRoot: root}); err == nil && len(findings) == 0 {
		t.Fatal("VerifyFile accepted malformed JSON")
	}
}

func TestLoadAndValidateFileRequiresExplicitLibraryMode(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	path := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, path, validReleaseEvidenceManifest())

	_, findings, err := LoadAndValidateFile(path, Options{RepoRoot: root})
	if err != nil {
		t.Fatalf("LoadAndValidateFile returned unexpected error: %v", err)
	}
	assertReleaseEvidenceFindingContains(t, findings, "manifest.mode_missing")
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
			body: `{"schema_version":"2","items":[]}`,
			want: "release_gate",
		},
		{
			name: "missing item id",
			body: `{"schema_version":"2","release_gate":"scripts/verify-ga-release.sh","items":[{"capability_id":"storage","evidence_type":"unit","required":true,"command":["go","test","./internal/capability"],"anchors":["go.mod"],"doc_only_allowed":false,"optional_gated":false,"default_ga_required":true}]}`,
			want: "id",
		},
		{
			name: "missing command",
			body: `{"schema_version":"2","release_gate":"scripts/verify-ga-release.sh","items":[{"id":"storage_unit","capability_id":"storage","evidence_type":"unit","required":true,"anchors":["go.mod"],"doc_only_allowed":false,"optional_gated":false,"default_ga_required":true}]}`,
			want: "command",
		},
		{
			name: "missing evidence status",
			body: `{"schema_version":"2","release_gate":"scripts/verify-ga-release.sh","items":[{"id":"storage_unit","claim_id":"CLAIM_DEFAULT_DENIAL_SAFE","subclaim_id":"webdav_export_disabled_admission","acceptance_id":"P0_DEFAULT_DENIAL_WEBDAV_DISABLED_ADMISSION","capability_id":"webdav_export","evidence_profile":"default","default_mode":true,"fixture_enabled_mode":false,"expected_runtime":"fast","scope":"package","negative_or_positive":"negative","evidence_type":"unit","required":true,"command":["go","test","./internal/capability"],"anchors":["go.mod"],"doc_only_allowed":false,"optional_gated":false,"default_ga_required":true,"pass_criteria":{"kind":"denial_safety","assertions":["disabled admission rejects before metadata and audits without queuing"]}}]}`,
			want: "evidence_status",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := releaseEvidenceFixtureRoot(t)
			path := filepath.Join(root, "manifest.json")
			writeReleaseEvidenceFile(t, path, tt.body)

			findings, err := VerifyFile(path, Options{Mode: ManifestModeSeed, RepoRoot: root})
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

			findings, err := VerifyFile(path, Options{Mode: ManifestModeSeed, RepoRoot: root})
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

			findings, err := VerifyFile(path, Options{Mode: ManifestModeSeed, RepoRoot: root})
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

			findings, err := VerifyFile(path, Options{Mode: ManifestModeSeed, RepoRoot: root})
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

	findings, err := VerifyFile(path, Options{Mode: ManifestModeSeed, RepoRoot: root, ExecuteRequired: false})
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

			findings, err := VerifyFile(path, Options{Mode: ManifestModeSeed, RepoRoot: root, ExecuteRequired: false})
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

	findings, err := VerifyFile(path, Options{Mode: ManifestModeSeed, RepoRoot: root, ExecuteRequired: false})
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

	findings, err := VerifyFile(path, Options{Mode: ManifestModeSeed, RepoRoot: root, ExecuteRequired: false})
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

	findings, err := VerifyFile(path, Options{Mode: ManifestModeSeed, RepoRoot: root, ExecuteRequired: false})
	if err != nil {
		t.Fatalf("VerifyFile returned unexpected error: %v", err)
	}
	assertReleaseEvidenceFindingContains(t, findings, "Benchmark")
	assertReleaseEvidenceFindingContains(t, findings, "go test -run")
}

func TestValidateManifestRejectsRetainedLifecyclePositivePurgeSelectors(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	body := replacePackage0FieldForItem(
		validReleaseEvidenceManifest(),
		"repo_lifecycle_retained_positive_unit",
		`"command":["bash","scripts/pass.sh"]`,
		`"command":["go","test","./internal/evidencetest","-run","^TestRepoLifecycleHandlerCreatesDeleteAndPurgeOperations$"]`,
	)
	path := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, path, body)

	findings, err := VerifyFile(path, Options{Mode: ManifestModeSeed, RepoRoot: root, ExecuteRequired: false})
	if err != nil {
		t.Fatalf("VerifyFile returned unexpected error: %v", err)
	}
	assertReleaseEvidenceFindingContains(t, findings, "retained lifecycle")
	assertReleaseEvidenceFindingContains(t, findings, "purge")
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

			findings, err := VerifyFile(path, Options{Mode: ManifestModeSeed, RepoRoot: root})
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
				return appendReleaseEvidenceItem(body, `"id":"bad_workload_default","capability_id":"workload_mount_binding","evidence_type":"unit","required":true,"command":["bash","scripts/pass.sh"],"anchors":["scripts/pass.sh"],"doc_only_allowed":false,"optional_gated":true,"default_ga_required":true`)
			},
			want: "default_ga_required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := releaseEvidenceFixtureRoot(t)
			path := filepath.Join(root, "manifest.json")
			writeReleaseEvidenceFile(t, path, tt.edit(validReleaseEvidenceManifest()))

			findings, err := VerifyFile(path, Options{Mode: ManifestModeSeed, RepoRoot: root})
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

	findings, err := VerifyFile(path, Options{Mode: ManifestModeSeed, RepoRoot: root, ExecuteRequired: true})
	if err != nil {
		t.Fatalf("VerifyFile returned unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("VerifyFile returned findings for passing command: %+v", findings)
	}

	writeReleaseEvidenceFile(t, path, strings.Replace(validReleaseEvidenceManifest(), `"command":["bash","scripts/pass.sh"]`, `"command":["bash","scripts/fail.sh"]`, 1))
	findings, err = VerifyFile(path, Options{Mode: ManifestModeSeed, RepoRoot: root, ExecuteRequired: true})
	if err != nil {
		t.Fatalf("VerifyFile returned unexpected error: %v", err)
	}
	assertReleaseEvidenceFindingContains(t, findings, "failed")
}

func TestCurrentRepoManifestContainsOptionalCapabilityDisabledAdmissionEvidence(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	manifestPath := filepath.Join(repoRoot, "docs", "release-evidence", "ga-manifest.json")

	manifest, findings, err := LoadAndValidateFile(manifestPath, Options{Mode: ManifestModeSeed, RepoRoot: repoRoot})
	if err != nil {
		t.Fatalf("LoadAndValidateFile returned error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("current manifest findings: %+v", findings)
	}

	for _, capabilityID := range []string{"workload_mount_binding", "repo_template", "repo_purge"} {
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

func TestCurrentRepoManifestRepoCreateJVSEvidenceRunSelectorCoversRecoveryTests(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	manifestPath := filepath.Join(repoRoot, "docs", "release-evidence", "ga-manifest.json")

	manifest, findings, err := LoadAndValidateFile(manifestPath, Options{Mode: ManifestModeSeed, RepoRoot: repoRoot})
	if err != nil {
		t.Fatalf("LoadAndValidateFile returned error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("current manifest findings: %+v", findings)
	}

	var item Item
	found := false
	for _, candidate := range manifest.Items {
		if candidate.ID == "repo_create_jvs_runtime_unavailable_recovery_unit" {
			item = candidate
			found = true
			break
		}
	}
	if !found {
		t.Fatal("manifest missing repo_create_jvs_runtime_unavailable_recovery_unit")
	}

	selector, ok := goTestRunSelector(item.Command)
	if !ok {
		t.Fatalf("%s command has no go test -run selector: %#v", item.ID, item.Command)
	}
	if packages := goTestPackageArgs(item.Command); len(packages) != 1 || packages[0] != "./internal/workerapp" {
		t.Fatalf("%s command packages = %#v, want only ./internal/workerapp", item.ID, packages)
	}

	compiled, err := regexp.Compile(selector)
	if err != nil {
		t.Fatalf("%s has invalid -run selector %q: %v", item.ID, selector, err)
	}
	requiredTests := []string{
		"TestRunOnceRepoCreateEnabledButJVSUnavailableScansAndPersistsUnsupportedIntervention",
		"TestRunOnceRepoCreateEnabledProductionJVSUnavailableScansAndPersistsUnsupportedIntervention",
		"TestRunOnceRepoCreateEnabledChecksumMismatchFailsFast",
		"TestRunOnceRepoCreateEnabledRepoExecutorConstructorErrorFailsFast",
		"TestNewJVSRunnerFromConfigRedactsBinaryReadErrors",
	}
	coreMatches := 0
	for _, testName := range requiredTests {
		if !compiled.MatchString(testName) {
			t.Fatalf("%s -run selector %q does not match required test %s", item.ID, selector, testName)
		}
		if strings.HasPrefix(testName, "TestRunOnceRepoCreateEnabled") {
			coreMatches++
		}
	}
	if coreMatches == 0 {
		t.Fatalf("%s -run selector %q only covers the redaction test", item.ID, selector)
	}
}

func TestCurrentRepoManifestWorkloadMountDisabledAdmissionSelectorCoversCoreTests(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	manifestPath := filepath.Join(repoRoot, "docs", "release-evidence", "ga-manifest.json")

	manifest, findings, err := LoadAndValidateFile(manifestPath, Options{Mode: ManifestModeSeed, RepoRoot: repoRoot})
	if err != nil {
		t.Fatalf("LoadAndValidateFile returned error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("current manifest findings: %+v", findings)
	}

	var item Item
	found := false
	for _, candidate := range manifest.Items {
		if candidate.ID == "workload_mount_disabled_admission_unit" {
			item = candidate
			found = true
			break
		}
	}
	if !found {
		t.Fatal("manifest missing workload_mount_disabled_admission_unit")
	}

	selector, ok := goTestRunSelector(item.Command)
	if !ok {
		t.Fatalf("%s command has no go test -run selector: %#v", item.ID, item.Command)
	}
	if packages := goTestPackageArgs(item.Command); !stringSlicesEqual(packages, []string{"./internal/capability", "./internal/api"}) {
		t.Fatalf("%s command packages = %#v, want capability and api packages", item.ID, packages)
	}

	compiled, err := regexp.Compile(selector)
	if err != nil {
		t.Fatalf("%s has invalid -run selector %q: %v", item.ID, selector, err)
	}
	for _, testName := range []string{
		"TestCapabilityAdmissionOperationCoverageContract",
		"TestInternalAPIShellWorkloadMountPlanAdmissionDisabled",
		"TestCreateWorkloadMountBindingAdmissionDisabledReplaysExistingBeforeMetadata",
		"TestCreateWorkloadMountBindingAdmissionDisabledRejectsNewBeforeMetadataAndAudits",
		"TestCreateWorkloadMountBindingAdmissionDisabledHashConflictBeforeCapabilityDenied",
		"TestWorkloadMountAdmissionDisabledMutations",
		"TestWorkloadMountAdmissionDisabledPlan",
	} {
		if !compiled.MatchString(testName) {
			t.Fatalf("%s -run selector %q does not match required test %s", item.ID, selector, testName)
		}
		assertGoTestListIncludesTest(t, repoRoot, item.ID, selector, goTestPackageForTestName(testName), testName)
	}
	passCriteria := strings.Join(item.PassCriteria.Assertions, "\n")
	for _, required := range []string{"workload mount binding", "status update", "heartbeat", "ordinary orchestrator plan", "workload teardown plan"} {
		if !strings.Contains(passCriteria, required) {
			t.Fatalf("%s pass criteria %q does not mention %q", item.ID, passCriteria, required)
		}
	}
	if strings.Contains(passCriteria, "teardown exceptions") {
		t.Fatalf("%s pass criteria %q must not describe release/revoke as teardown exceptions", item.ID, passCriteria)
	}
}

func TestCurrentRepoManifestCapabilityMatrixSelectorCoversReadyzWorkloadSplitTests(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	manifestPath := filepath.Join(repoRoot, "docs", "release-evidence", "ga-manifest.json")

	manifest, findings, err := LoadAndValidateFile(manifestPath, Options{Mode: ManifestModeSeed, RepoRoot: repoRoot})
	if err != nil {
		t.Fatalf("LoadAndValidateFile returned error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("current manifest findings: %+v", findings)
	}

	var item Item
	found := false
	for _, candidate := range manifest.Items {
		if candidate.ID == "capability_matrix_v1_contract_unit" {
			item = candidate
			found = true
			break
		}
	}
	if !found {
		t.Fatal("manifest missing capability_matrix_v1_contract_unit")
	}

	selector, ok := goTestRunSelector(item.Command)
	if !ok {
		t.Fatalf("%s command has no go test -run selector: %#v", item.ID, item.Command)
	}
	if packages := goTestPackageArgs(item.Command); !stringSlicesEqual(packages, []string{"./internal/capability", "./internal/api", "./internal/apiapp"}) {
		t.Fatalf("%s command packages = %#v, want capability, api, and apiapp packages", item.ID, packages)
	}

	compiled, err := regexp.Compile(selector)
	if err != nil {
		t.Fatalf("%s has invalid -run selector %q: %v", item.ID, selector, err)
	}
	for _, testName := range []string{
		"TestNeutralReadinessHandlerReportsNotReadyAndDisabledGates",
		"TestReadinessFromCapabilityMatrixSerializesWorkloadMountSplitCapabilities",
		"TestNeutralShellFallbackReportsSplitWorkloadCapabilities",
		"TestInternalRuntimeReadinessIsNotNeutralAndDoesNotAdvertiseUnimplementedHandlersReady",
		"TestInternalRuntimeReadinessGAProfileTreatsWorkloadMountAsOptionalGated",
		"TestInternalRuntimeReadinessRuntimeProfileRequiresOptedInWorkloadMountFacets",
	} {
		if !compiled.MatchString(testName) {
			t.Fatalf("%s -run selector %q does not match required test %s", item.ID, selector, testName)
		}
		assertGoTestListIncludesTest(t, repoRoot, item.ID, selector, goTestPackageForTestName(testName), testName)
	}
	for _, anchor := range []string{
		"internal/api/health.go",
		"internal/api/health_test.go",
		"internal/api/shell.go",
		"internal/api/shell_test.go",
		"internal/apiapp/runtime.go",
		"internal/apiapp/runtime_test.go",
	} {
		if !containsString(item.Anchors, anchor) {
			t.Fatalf("%s anchors = %#v, missing %s", item.ID, item.Anchors, anchor)
		}
	}
	passCriteria := strings.Join(item.PassCriteria.Assertions, "\n")
	for _, required := range []string{"readyz", "workload binding", "discovery", "teardown plan"} {
		if !strings.Contains(passCriteria, required) {
			t.Fatalf("%s pass criteria %q does not mention %q", item.ID, passCriteria, required)
		}
	}
}

func TestCurrentRepoManifestReplacesAdminBootstrapSeedGap(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	manifestPath := filepath.Join(repoRoot, "docs", "release-evidence", "ga-manifest.json")

	manifest, findings, err := LoadAndValidateFile(manifestPath, Options{Mode: ManifestModeSeed, RepoRoot: repoRoot})
	if err != nil {
		t.Fatalf("LoadAndValidateFile returned error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("current manifest findings: %+v", findings)
	}

	for _, item := range manifest.Items {
		if item.ID == "seed_gap_admin_bootstrap_ready_open" {
			t.Fatal("current manifest must close seed_gap_admin_bootstrap_ready_open with implemented evidence")
		}
	}

	var item Item
	found := false
	for _, candidate := range manifest.Items {
		if candidate.ID == "admin_bootstrap_ready_unit" {
			item = candidate
			found = true
			break
		}
	}
	if !found {
		t.Fatal("manifest missing admin_bootstrap_ready_unit")
	}
	if item.EvidenceStatus != "implemented" ||
		item.ClaimID != "CLAIM_ADMIN_BOOTSTRAP_READY" ||
		item.SubclaimID != "admin_bootstrap_ready" ||
		item.AcceptanceID != "P0_ADMIN_BOOTSTRAP_READY" ||
		item.RiskID != "F3" ||
		item.CapabilityID != "admin_bootstrap" ||
		item.EvidenceProfile != "default" ||
		!item.DefaultMode ||
		item.FixtureEnabledMode ||
		!item.Required ||
		item.DocOnlyAllowed ||
		item.OptionalGated ||
		!item.DefaultGARequired {
		t.Fatalf("admin_bootstrap_ready_unit shape = %+v, want exact default positive replacement", item)
	}
	if item.PassCriteria.Kind != "positive_path" || !containsString(item.PassCriteria.Assertions, "admin bootstrap readiness passes in default mode") {
		t.Fatalf("admin_bootstrap_ready_unit pass criteria = %+v", item.PassCriteria)
	}
	if packages := goTestPackageArgs(item.Command); !stringSlicesEqual(packages, []string{"./internal/api", "./internal/apiapp", "./internal/contractcheck"}) {
		t.Fatalf("%s command packages = %#v, want api, apiapp, and contractcheck", item.ID, packages)
	}
	selector, ok := goTestRunSelector(item.Command)
	if !ok {
		t.Fatalf("%s command has no go test -run selector: %#v", item.ID, item.Command)
	}
	compiled, err := regexp.Compile(selector)
	if err != nil {
		t.Fatalf("%s has invalid -run selector %q: %v", item.ID, selector, err)
	}
	for _, testName := range []string{
		"TestReadinessFromCapabilityMatrixSerializesAdminBootstrapFacets",
		"TestReadinessHandlerRedactsAdminBootstrapReasons",
		"TestInternalRuntimeReadinessIncludesAdminBootstrapFacets",
		"TestInternalRuntimeReadinessGAProfileRequiresAdminBootstrap",
		"TestInternalRuntimeReadinessAdminBootstrapGatesOnStoragePingWithoutLeakingErrors",
		"TestInternalRuntimeReadinessAdminBootstrapRequiresUsableCallerPolicyRoles",
		"TestInternalRuntimeReadinessAdminBootstrapRequiresPolicyCallersToBeAuthenticatable",
		"TestInternalRuntimeReadinessAdminBootstrapDoesNotRequireDefaultUserLoop",
		"TestCurrentRepoReadinessEvidenceHasCurrentImplementationStatus",
	} {
		if !compiled.MatchString(testName) {
			t.Fatalf("%s -run selector %q does not match required test %s", item.ID, selector, testName)
		}
		assertGoTestListIncludesTest(t, repoRoot, item.ID, selector, goTestPackageForTestName(testName), testName)
	}
}

func TestCurrentRepoManifestContainsP2aOperationTerminalizationContractEvidence(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	manifestPath := filepath.Join(repoRoot, "docs", "release-evidence", "ga-manifest.json")

	manifest, findings, err := LoadAndValidateFile(manifestPath, Options{Mode: ManifestModeSeed, RepoRoot: repoRoot})
	if err != nil {
		t.Fatalf("LoadAndValidateFile returned error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("current manifest findings: %+v", findings)
	}

	var item Item
	found := false
	for _, candidate := range manifest.Items {
		if candidate.ID == "operation_terminalization_contract_unit" {
			item = candidate
			found = true
			break
		}
	}
	if !found {
		t.Fatal("manifest missing operation_terminalization_contract_unit")
	}
	if item.EvidenceStatus != "implemented" ||
		item.ClaimID != "CLAIM_OPERATION_TERMINALIZATION" ||
		item.SubclaimID != "operation_terminalization_contract" ||
		item.AcceptanceID != "P2A_OPERATION_TERMINALIZATION_CONTRACT" ||
		item.CapabilityID != "operation_recovery" ||
		!item.Required ||
		item.DocOnlyAllowed ||
		item.OptionalGated ||
		!item.DefaultGARequired {
		t.Fatalf("operation_terminalization_contract_unit shape = %+v, want default required contract evidence", item)
	}
	if packages := goTestPackageArgs(item.Command); !stringSlicesEqual(packages, []string{"./internal/capability", "./internal/contractcheck"}) {
		t.Fatalf("%s command packages = %#v, want capability and contractcheck", item.ID, packages)
	}
	selector, ok := goTestRunSelector(item.Command)
	if !ok {
		t.Fatalf("%s command has no go test -run selector: %#v", item.ID, item.Command)
	}
	compiled, err := regexp.Compile(selector)
	if err != nil {
		t.Fatalf("%s has invalid -run selector %q: %v", item.ID, selector, err)
	}
	for _, testName := range []string{
		"TestCapabilityMatrixV1DecisionRowsCoverP2aSurfaceContract",
		"TestCapabilityMatrixV1CoversEveryRouteMutationOperation",
		"TestCapabilityMatrixV1IncludesRestorePreviewAsDurableJVSMutation",
		"TestCapabilityMatrixV1ClassifiesVolumeEnsureAdmission",
		"TestOperationStateMachineContractCoversEveryOperationType",
		"TestOperationTerminalizationContractRequiresSideEffectReplayAndTerminalDecision",
	} {
		if !compiled.MatchString(testName) {
			t.Fatalf("%s -run selector %q does not match required test %s", item.ID, selector, testName)
		}
		assertGoTestListIncludesTest(t, repoRoot, item.ID, selector, goTestPackageForTestName(testName), testName)
	}
}

func TestPartialDefaultUserLoopEvidenceWithOpenSeedGapRemainsValidSeedMode(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	body := strings.Replace(validReleaseEvidenceManifest(), `"id":"default_user_loop_positive_unit"`, `"id":"default_user_loop_positive_missing"`, 1)
	body = appendReleaseEvidenceItem(body, `"id":"seed_gap_default_user_loop_open","evidence_status":"placeholder","claim_id":"CLAIM_DEFAULT_USER_LOOP","subclaim_id":"seed_gap_open","acceptance_id":"P0_SEED_GAP_OPEN","risk_id":"F2","fixture_id":"","capability_id":"","evidence_profile":"default","default_mode":true,"fixture_enabled_mode":false,"expected_runtime":"fast","scope":"doc-guard","negative_or_positive":"both","evidence_type":"doc-guard","required":false,"command":[],"anchors":["docs/GA_NEXT_PHASE_DEVELOPMENT_HANDOFF_PLAN.md"],"doc_only_allowed":true,"optional_gated":false,"default_ga_required":false,"pass_criteria":{"kind":"seed_gap","assertions":["open"]}`)
	path := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, path, body)

	findings, err := VerifyFile(path, Options{Mode: ManifestModeSeed, RepoRoot: root, ExecuteRequired: false})
	if err != nil {
		t.Fatalf("VerifyFile returned unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("partial default user loop with open seed gap should remain valid seed mode, got findings: %+v", findings)
	}
}

func TestCurrentRepoManifestContainsP2bRuntimeParityEvidence(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	manifestPath := filepath.Join(repoRoot, "docs", "release-evidence", "ga-manifest.json")

	manifest, findings, err := LoadAndValidateFile(manifestPath, Options{Mode: ManifestModeSeed, RepoRoot: repoRoot})
	if err != nil {
		t.Fatalf("LoadAndValidateFile returned error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("current manifest findings: %+v", findings)
	}

	tests := []struct {
		id           string
		claimID      string
		subclaimID   string
		acceptanceID string
		riskID       string
		capabilityID string
		wantPackages []string
		wantTests    []string
	}{
		{
			id:           "capability_runtime_parity_unit",
			claimID:      "CLAIM_CAPABILITY_MATRIX_CONSISTENT",
			subclaimID:   "capability_runtime_parity",
			acceptanceID: "P2B_CAPABILITY_RUNTIME_PARITY",
			riskID:       "F4",
			wantPackages: []string{"./internal/capability", "./internal/api", "./internal/apiapp"},
			wantTests: []string{
				"TestCapabilityMatrixV1DecisionRowsMapRouteAndWorkerOperationSurfaces",
				"TestCapabilityMatrixV1DecisionRowsEvidenceRefsMapRuntimeSurfaces",
				"TestCapabilityMatrixAdmissionDisabledDeniesMatrixOptionalMutationsBeforeQueue",
				"TestCapabilityMatrixAdmissionDisabledReplaysExistingOperationBeforeDenial",
				"TestInternalAPIShellCreateExportCapabilityDeniedWhenWebDAVAdmissionDisabled",
				"TestInternalAPIShellCreateExportAdmissionDisabledReplaysExistingOperation",
				"TestInternalAPIShellCreateWorkloadMountAdmissionDisabledReplaysExistingOperation",
				"TestInternalAPIShellCreateWorkloadMountAdmissionDisabledRejectsBrandNewBeforeMetadata",
				"TestRepoTemplateAdmissionDisabledReplaysExistingOperationsBeforeMetadata",
				"TestRepoTemplateAdmissionDisabledRejectsNewOperationsBeforeMetadataAndAudits",
				"TestRepoTemplateAdmissionDisabledReturnsIdempotencyConflictBeforeCapabilityDenied",
				"TestRepoLifecyclePurgeAdmissionDisabledRejectsNewBeforeMetadataAndAudits",
				"TestInternalRuntimeAdmissionDisabledFlagsMatchCapabilityMatrix",
			},
		},
		{
			id:           "operation_runtime_terminalization_unit",
			claimID:      "CLAIM_OPERATION_TERMINALIZATION",
			subclaimID:   "operation_runtime_terminalization",
			acceptanceID: "P2B_OPERATION_RUNTIME_TERMINALIZATION",
			riskID:       "F6",
			capabilityID: "operation_recovery",
			wantPackages: []string{"./internal/workerapp"},
			wantTests: []string{
				"TestWorkerCapabilityMatrixExecutorRegistryMatchesDecisionRows",
				"TestWorkerCapabilityMatrixRecoveryRegistryMatchesDecisionRows",
				"TestWorkerCapabilityMatrixUnsupportedTerminalizationRegistryIncludesDisabledOperations",
				"TestRunOnceExportSessionReconcileOnlyRunsWhenExplicitlyEnabled",
				"TestRunOnceClaimsQueuedVolumeEnsureThroughDefaultRunner",
				"TestRunOnceClaimsQueuedNamespaceUpsertThroughDefaultRunner",
				"TestRunOnceClaimsQueuedNamespaceDisableThroughDefaultRunner",
				"TestRunOnceClaimsQueuedNamespaceUpsertAndBindingPutThroughDefaultRunner",
				"TestRunOnceRepoCreateDisabledScansAndPersistsUnsupportedIntervention",
				"TestRunOnceRepoCreateEnabledButJVSUnavailableScansAndPersistsUnsupportedIntervention",
				"TestRunOnceWorkloadMountBindingCreateClaimsThroughMountBindingExecutor",
				"TestRunOnceWorkloadMountBindingStatusUpdateClaimsThroughMountBindingExecutor",
				"TestRunOnceWorkloadMountBindingHeartbeatClaimsThroughMountBindingExecutor",
				"TestRunOnceWorkloadMountBindingReleaseClaimsThroughMountBindingExecutor",
				"TestRunOnceWorkloadMountBindingRevokeClaimsThroughMountBindingExecutor",
				"TestRunOnceRepoLifecycleDisabledScansAndPersistsUnsupportedInterventions",
				"TestRunOnceRepoPurgeDisabledScansAndPersistsUnsupportedIntervention",
				"TestRunOnceSavePointCreateDisabledScansAndPersistsUnsupportedIntervention",
				"TestRunOnceTemplateCreateDisabledScansAndPersistsUnsupportedIntervention",
				"TestRunOnceTemplateCloneDisabledScansAndPersistsUnsupportedIntervention",
				"TestRunOnceRestorePreviewDisabledScansAndPersistsUnsupportedIntervention",
				"TestRunOnceRestorePreviewDiscardDisabledScansAndPersistsUnsupportedIntervention",
				"TestRunOnceRestoreRunDisabledScansAndPersistsUnsupportedIntervention",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			item, ok := manifestItemByID(manifest, tt.id)
			if !ok {
				t.Fatalf("manifest missing %s", tt.id)
			}
			if item.EvidenceStatus != "implemented" ||
				item.ClaimID != tt.claimID ||
				item.SubclaimID != tt.subclaimID ||
				item.AcceptanceID != tt.acceptanceID ||
				item.RiskID != tt.riskID ||
				item.CapabilityID != tt.capabilityID ||
				item.EvidenceProfile != "default" ||
				!item.DefaultMode ||
				item.FixtureEnabledMode ||
				!item.Required ||
				item.DocOnlyAllowed ||
				item.OptionalGated {
				t.Fatalf("%s shape = %+v, want default required P2b runtime parity evidence", tt.id, item)
			}
			if packages := goTestPackageArgs(item.Command); !stringSlicesEqual(packages, tt.wantPackages) {
				t.Fatalf("%s command packages = %#v, want %#v", item.ID, packages, tt.wantPackages)
			}
			selector, ok := goTestRunSelector(item.Command)
			if !ok {
				t.Fatalf("%s command has no go test -run selector: %#v", item.ID, item.Command)
			}
			compiled, err := regexp.Compile(selector)
			if err != nil {
				t.Fatalf("%s has invalid -run selector %q: %v", item.ID, selector, err)
			}
			for _, testName := range tt.wantTests {
				if !compiled.MatchString(testName) {
					t.Fatalf("%s -run selector %q does not match required test %s", item.ID, selector, testName)
				}
				assertGoTestListIncludesTest(t, repoRoot, item.ID, selector, goTestPackageForTestName(testName), testName)
			}
		})
	}
}

func TestCurrentRepoManifestContainsP1bRepoProjectionEvidence(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	manifestPath := filepath.Join(repoRoot, "docs", "release-evidence", "ga-manifest.json")

	manifest, findings, err := LoadAndValidateFile(manifestPath, Options{Mode: ManifestModeSeed, RepoRoot: repoRoot})
	if err != nil {
		t.Fatalf("LoadAndValidateFile returned error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("current manifest findings: %+v", findings)
	}

	item, ok := manifestItemByID(manifest, "default_user_loop_repo_projection_unit")
	if !ok {
		t.Fatal("manifest missing default_user_loop_repo_projection_unit")
	}
	if item.EvidenceStatus != "implemented" ||
		item.ClaimID != "CLAIM_DEFAULT_USER_LOOP" ||
		item.SubclaimID != "default_user_loop_repo_projection" ||
		item.AcceptanceID != "P1B_DEFAULT_USER_LOOP_REPO_PROJECTION" ||
		item.RiskID != "F2" ||
		item.CapabilityID != "repo_projection" ||
		item.EvidenceProfile != "default" ||
		!item.DefaultMode ||
		item.FixtureEnabledMode ||
		item.NegativeOrPositive != "positive" ||
		item.EvidenceType != "unit" ||
		!item.Required ||
		item.DocOnlyAllowed ||
		item.OptionalGated ||
		!item.DefaultGARequired ||
		item.PassCriteria.Kind != "positive_path" {
		t.Fatalf("%s shape = %+v, want default required P1b repo projection positive evidence", item.ID, item)
	}
	if packages := goTestPackageArgs(item.Command); !stringSlicesEqual(packages, []string{"./internal/api", "./internal/store/postgres", "./internal/workerapp"}) {
		t.Fatalf("%s command packages = %#v, want api, postgres store, and workerapp", item.ID, packages)
	}
	selector, ok := goTestRunSelector(item.Command)
	if !ok {
		t.Fatalf("%s command has no go test -run selector: %#v", item.ID, item.Command)
	}
	compiled, err := regexp.Compile(selector)
	if err != nil {
		t.Fatalf("%s has invalid -run selector %q: %v", item.ID, selector, err)
	}
	for _, testName := range []string{
		"TestCreateRepoHandlerCreatesOperationIntake",
		"TestCreateRepoHandlerReusesExistingOperation",
		"TestCreateRepoHandlerValidationDeniesBeforeIntake",
		"TestCreateRepoHandlerReturnsRepoAlreadyExistsFromDedicatedIntake",
		"TestCreateRepoHandlerMapsIntakeErrorsWithoutLeakingDetails",
		"TestGetRepoHandlerReturnsNamespaceBoundProjection",
		"TestGetRepoHandlerUsesNamespaceScopedReadBoundary",
		"TestListReposHandlerReturnsNamespaceBoundProjectionAndLifecycleFilter",
		"TestRepoReadHandlerValidationDeniesBeforeStore",
		"TestRepoReadHandlerRejectsUnsafeStoredJVSRepoIDWithoutLeaking",
		"TestRepoReadHandlerNotFoundNamespaceMismatchAndStoreErrors",
		"TestCreateGetAndListReposPersistImmutableIdentityAndLifecycleMetadata",
		"TestCreateOrReuseRepoCreateOperationReusesExistingBeforeRepoExists",
		"TestCreateOrReuseRepoCreateOperationNewRequestExistingRepoReturnsRepoAlreadyExists",
		"TestCreateOrReuseRepoCreateOperationDifferentHashReturnsIdempotencyConflictBeforeRepoExists",
		"TestCommitRepoCreateSucceededWithLeaseAtomicBoundary",
		"TestRunOnceRepoCreateEnabledClaimsThroughRepoExecutor",
	} {
		if !compiled.MatchString(testName) {
			t.Fatalf("%s -run selector %q does not match required test %s", item.ID, selector, testName)
		}
		assertGoTestListIncludesTest(t, repoRoot, item.ID, selector, goTestPackageForTestName(testName), testName)
	}
}

func TestCurrentRepoManifestContainsP1cJVSSaveRestoreEvidence(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	manifestPath := filepath.Join(repoRoot, "docs", "release-evidence", "ga-manifest.json")

	manifest, findings, err := LoadAndValidateFile(manifestPath, Options{Mode: ManifestModeSeed, RepoRoot: repoRoot})
	if err != nil {
		t.Fatalf("LoadAndValidateFile returned error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("current manifest findings: %+v", findings)
	}

	item, ok := manifestItemByID(manifest, "default_user_loop_jvs_save_restore_unit")
	if !ok {
		t.Fatal("manifest missing default_user_loop_jvs_save_restore_unit")
	}
	if item.EvidenceStatus != "implemented" ||
		item.ClaimID != "CLAIM_DEFAULT_USER_LOOP" ||
		item.SubclaimID != "default_user_loop_jvs_save_restore" ||
		item.AcceptanceID != "P1C_DEFAULT_USER_LOOP_JVS_SAVE_RESTORE" ||
		item.RiskID != "F2" ||
		item.CapabilityID != "jvs_save_restore" ||
		item.EvidenceProfile != "default" ||
		!item.DefaultMode ||
		item.FixtureEnabledMode ||
		item.NegativeOrPositive != "positive" ||
		item.EvidenceType != "unit" ||
		!item.Required ||
		item.DocOnlyAllowed ||
		item.OptionalGated ||
		!item.DefaultGARequired ||
		item.PassCriteria.Kind != "positive_path" {
		t.Fatalf("%s shape = %+v, want default required P1c JVS save/restore positive evidence", item.ID, item)
	}
	if packages := goTestPackageArgs(item.Command); !stringSlicesEqual(packages, []string{"./internal/api", "./internal/repoexec", "./internal/workerapp", "./internal/store/postgres"}) {
		t.Fatalf("%s command packages = %#v, want api, repoexec, workerapp, and postgres store", item.ID, packages)
	}
	selector, ok := goTestRunSelector(item.Command)
	if !ok {
		t.Fatalf("%s command has no go test -run selector: %#v", item.ID, item.Command)
	}
	compiled, err := regexp.Compile(selector)
	if err != nil {
		t.Fatalf("%s has invalid -run selector %q: %v", item.ID, selector, err)
	}
	for _, testName := range []string{
		"TestSavePointCreateValidatesMessageAndCreatesQueuedOperation",
		"TestSavePointCreateIdempotentReuseBeforeRepoStateChecks",
		"TestSavePointCreateRejectsSecretShapedMessage",
		"TestSavePointCreateRejectsDisabledNamespaceBeforeIntakeAndAudits",
		"TestSavePointListReturnsHistoryAndFailsClosed",
		"TestSavePointListGateConflictDoesNotReadHistory",
		"TestSavePointListDeniesArchivedAndLifecycleFenceBeforeHistory",
		"TestJVSBackedSavePointHistoryReaderResolvesRootAndReturnsSafeHistoryInJVSOrder",
		"TestJVSBackedSavePointHistoryReaderFailsClosedWithoutLeakingRawPaths",
		"TestRestorePreviewHandlerCreatesQueuedPreviewForSavePoint",
		"TestRestorePreviewHandlerFailsClosedForActivePlanOrJVSMutation",
		"TestRestorePreviewHandlerReusesExistingIdempotentOperationBeforePlanState",
		"TestRestorePreviewHandlerRejectsDisabledNamespacePolicy",
		"TestRestoreRunHandlerCreatesQueuedRunForPendingPlan",
		"TestRestoreRunHandlerRejectsDisabledNamespaceBeforeIntake",
		"TestRestoreRunHandlerRejectsLifecycleFenceBeforeIntake",
		"TestRestoreRunHandlerRejectsPreviewMetadataMismatch",
		"TestRestoreRunHandlerReusesExistingIdempotentOperationBeforePlanStateAndRunGate",
		"TestRestorePreviewDiscardHandlerCreatesQueuedDiscardForPendingPlan",
		"TestRestorePreviewDiscardHandlerRejectsCleanupAdmissionRisksBeforePreviewPlanAndIntake",
		"TestRestorePreviewDiscardHandlerReusesExistingIdempotentOperationBeforePlanState",
		"TestRestorePreviewDiscardHandlerAllowsDisabledNamespaceCleanupForPendingPlan",
		"TestSavePointExecutorPersistsPreSaveMarkerThenSavesAndCommits",
		"TestSavePointExecutorAdoptsCrashAfterSaveWithoutCallingSaveAgain",
		"TestSavePointExecutorRejectsSecretShapedMessageBeforeJVS",
		"TestRestorePreviewExecutorPersistsIdleMarkerBeforePreviewAndCommitsPlan",
		"TestRestorePreviewExecutorNonIdleRecoveryStatusRequiresOperatorIntervention",
		"TestRestorePreviewDiscardExecutorMarksPlanDiscardingBeforeJVSAndCommitsDiscarded",
		"TestRestorePreviewDiscardExecutorAllowsDisabledNamespaceCleanupAndCommitsDiscarded",
		"TestRestoreRunExecutorFencesWriterRunsDoctorChecksIdleAndCommitsConsumed",
		"TestRestoreRunExecutorPreJVSWriterSessionDenialReleasesFenceAndKeepsPlanPending",
		"TestRunOnceSavePointCreateEnabledClaimsThroughSavePointExecutor",
		"TestRunOnceRestorePreviewEnabledClaimsThroughRestorePreviewExecutor",
		"TestRunOnceRestorePreviewDiscardEnabledClaimsThroughDiscardExecutor",
		"TestRunOnceRestoreRunEnabledClaimsThroughRestoreRunExecutor",
		"TestRunOnceRestorePreviewEnabledRejectsUnpinnedJVSChecksum",
		"TestNewJVSRunnerFromConfigVerifiesFileAgainstAcceptedPin",
		"TestAcquireSavePointCreateOperationLeaseSerializesEarlierLifecycleAndJVSMutations",
		"TestCommitSavePointCreateSucceededRequiresPreparedStoredMarkerBoundary",
		"TestCreateOrReuseRestorePreviewOperationUsesAtomicGateAfterIdempotency",
		"TestCreateOrReuseRestoreRunOperationUsesAtomicPlanAndDuplicateGatesAfterIdempotency",
		"TestCreateOrReuseRestorePreviewDiscardOperationUsesAtomicPlanGateAfterIdempotency",
		"TestCommitRestorePreviewSucceededWithLeaseInsertsPlanAuditAndOperationAtomically",
		"TestCommitRestorePreviewDiscardSucceededWithLeaseDiscardsPlanAuditAndOperationAtomically",
		"TestCommitRestoreRunSucceededWithLeaseConsumesPlanAuditAndReleasesFenceAtomically",
	} {
		if !compiled.MatchString(testName) {
			t.Fatalf("%s -run selector %q does not match required test %s", item.ID, selector, testName)
		}
		assertGoTestListIncludesTest(t, repoRoot, item.ID, selector, goTestPackageForTestName(testName), testName)
	}
}

func TestCurrentRepoManifestContainsP1dWebDAVDefaultAccessEvidence(t *testing.T) {
	assertCurrentRepoManifestContainsP1dWebDAVEvidence(t, p1dWebDAVEvidenceWant{
		id:           "webdav_default_access_unit",
		claimID:      "CLAIM_WEBDAV_DEFAULT_ACCESS",
		subclaimID:   "webdav_default_access",
		acceptanceID: "P0_WEBDAV_DEFAULT_ACCESS",
		riskID:       "F8",
	})
}

func TestCurrentRepoManifestContainsP1dDefaultUserLoopWebDAVPartialEvidence(t *testing.T) {
	assertCurrentRepoManifestContainsP1dWebDAVEvidence(t, p1dWebDAVEvidenceWant{
		id:           "default_user_loop_webdav_access_unit",
		claimID:      "CLAIM_DEFAULT_USER_LOOP",
		subclaimID:   "default_user_loop_webdav_access",
		acceptanceID: "P1D_DEFAULT_USER_LOOP_WEBDAV_ACCESS",
		riskID:       "F2",
	})
}

type p1dWebDAVEvidenceWant struct {
	id           string
	claimID      string
	subclaimID   string
	acceptanceID string
	riskID       string
}

func assertCurrentRepoManifestContainsP1dWebDAVEvidence(t *testing.T, want p1dWebDAVEvidenceWant) {
	t.Helper()

	repoRoot := filepath.Join("..", "..")
	manifestPath := filepath.Join(repoRoot, "docs", "release-evidence", "ga-manifest.json")

	manifest, findings, err := LoadAndValidateFile(manifestPath, Options{Mode: ManifestModeSeed, RepoRoot: repoRoot})
	if err != nil {
		t.Fatalf("LoadAndValidateFile returned error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("current manifest findings: %+v", findings)
	}

	item, ok := manifestItemByID(manifest, want.id)
	if !ok {
		t.Fatalf("manifest missing %s", want.id)
	}
	if item.EvidenceStatus != "implemented" ||
		item.ClaimID != want.claimID ||
		item.SubclaimID != want.subclaimID ||
		item.AcceptanceID != want.acceptanceID ||
		item.RiskID != want.riskID ||
		item.CapabilityID != "webdav_export" ||
		item.EvidenceProfile != "default" ||
		!item.DefaultMode ||
		item.FixtureEnabledMode ||
		item.NegativeOrPositive != "positive" ||
		item.EvidenceType != "integration" ||
		!item.Required ||
		item.DocOnlyAllowed ||
		item.OptionalGated ||
		!item.DefaultGARequired ||
		item.PassCriteria.Kind != "positive_path" {
		t.Fatalf("%s shape = %+v, want default required P1d WebDAV positive evidence", item.ID, item)
	}
	if packages := goTestPackageArgs(item.Command); !stringSlicesEqual(packages, []string{"./internal/api", "./internal/exportaccess", "./internal/exportgateway", "./internal/exportreconcile", "./internal/store/postgres"}) {
		t.Fatalf("%s command packages = %#v, want api, exportaccess, exportgateway, exportreconcile, and postgres store", item.ID, packages)
	}
	selector, ok := goTestRunSelector(item.Command)
	if !ok {
		t.Fatalf("%s command has no go test -run selector: %#v", item.ID, item.Command)
	}
	compiled, err := regexp.Compile(selector)
	if err != nil {
		t.Fatalf("%s has invalid -run selector %q: %v", item.ID, selector, err)
	}
	for _, testName := range p1dWebDAVRequiredTestNames() {
		if !compiled.MatchString(testName) {
			t.Fatalf("%s -run selector %q does not match required test %s", item.ID, selector, testName)
		}
		assertGoTestListIncludesTest(t, repoRoot, item.ID, selector, goTestPackageForTestName(testName), testName)
	}
}

func p1dWebDAVRequiredTestNames() []string {
	return []string{
		"TestCreateExportReturnsOneTimePasswordAndPersistsOnlyVerifier",
		"TestCreateExportIdempotentReplayReturnsRedactedSessionWithoutPassword",
		"TestCreateExportDefaultsTTLAndClampsDefaultToPolicyMax",
		"TestGetExportReturnsRedactedSessionOnly",
		"TestGetExportRejectsNamespaceMismatch",
		"TestRevokeExportIsIdempotentAndLeavesSessionRevoking",
		"TestRevokeExportRemainsAvailableForRevokingSessionAfterNamespaceDisable",
		"TestPasswordVerifierAcceptsOnlyOriginalSecret",
		"TestResolveTTLSecondsAppliesDefaultMinAndPolicyMax",
		"TestSessionValidationKeepsCredentialFieldsOutOfAPIModel",
		"TestReadOnlyMethodPolicy",
		"TestReadWritePutGetAndCopyMoveDestinationPolicy",
		"TestSuccessfulGETRecordsRuntimeLedger",
		"TestSuccessfulGETUsesSingleDurableRuntimeRequestID",
		"TestReadWritePUTUsesDurableWriteRuntimeRequest",
		"TestReadWritePUTRecordsActiveWriteRuntimeLedger",
		"TestInactiveAndExpiredSessionsDenyClosed",
		"TestInactiveExpiredAndRevokingSessionsEmitRedactedAuditWithoutRuntimeObservation",
		"TestBasicAuthFailureDoesNotLeakCredentialOrPaths",
		"TestGatewayStoreFailClosedDeniesDisabledNamespaceCredential",
		"TestDeniedRequestsEmitAuditWithoutRuntimeObservation",
		"TestDeniedAuditPayloadDoesNotContainSensitiveWebDAVMaterial",
		"TestBeginRuntimeRequestAdmissionDeniedFailsClosedBeforeBackend",
		"TestRunOnceReconcilesZeroCountRevokingAndExpiredSessions",
		"TestRunOnceRecoversStaleRuntimeRequestsBeforeTerminalList",
		"TestRunOnceTreatsNoRowsAsRaceLost",
		"TestCreateOrReuseExportSQLCommitsSessionOperationAndAuditInOneBoundary",
		"TestCreateOrReuseExportSQLPredicatesMatchOperationAndSessionArgs",
		"TestCreateOrReuseExportSQLOnlyCreatesSessionAndAuditForNewOperation",
		"TestCreateOrReuseExportClassifiesReplayAndRejectsHashConflict",
		"TestCreateOrReuseExportFallsBackToCommittedReplayWhenInsertRaceReturnsNoRows",
		"TestGetExportSessionSelectsOnlyRedactedColumns",
		"TestRevokeExportSQLUsesRevokingDrainStateNotTerminalRevoked",
		"TestRevokeExportClassifiesReplayConflictAndReturnsRevokingSession",
		"TestGatewayCredentialReadsVerifierAndPayloadSubdirWithoutRawRoot",
		"TestGatewayCredentialSQLFailsClosedOnInactiveNamespaceBindingOrSession",
		"TestBeginExportRuntimeRequestReplayDoesNotIncrementAndConflictsFailClosed",
		"TestBeginExportRuntimeRequestUsesLedgerAndPositiveAdmissionAtomically",
		"TestHeartbeatAndEndExportRuntimeRequestUseSameLedgerRequestID",
		"TestEndExportRuntimeRequestReplayDoesNotMutateSession",
		"TestRecoverStaleExportRuntimeRequestsClosesOpenLedgerAndAdjustsCounts",
		"TestReconcileExportSessionTerminalSQLCommitsOperationSessionAndAudit",
		"TestListExportSessionsForTerminalReconcileFindsZeroCountRevokingAndExpiredWithoutHeartbeat",
		"TestReconcileExportSessionTerminalRejectsActiveCountsBeforeSQL",
		"TestReconcileExportSessionTerminalReturnsOperationAuditBoundary",
	}
}

func TestCurrentRepoManifestContainsDefaultUserLoopTraceEvidence(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	manifestPath := filepath.Join(repoRoot, "docs", "release-evidence", "ga-manifest.json")

	manifest, findings, err := LoadAndValidateFile(manifestPath, Options{Mode: ManifestModeSeed, RepoRoot: repoRoot})
	if err != nil {
		t.Fatalf("LoadAndValidateFile returned error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("current manifest findings: %+v", findings)
	}

	item, ok := manifestItemByID(manifest, "default_user_loop_trace_unit")
	if !ok {
		t.Fatal("manifest missing default_user_loop_trace_unit")
	}
	if item.EvidenceStatus != "implemented" ||
		item.ClaimID != "CLAIM_DEFAULT_USER_LOOP" ||
		item.SubclaimID != "default_user_loop_trace" ||
		item.AcceptanceID != "P1E_DEFAULT_USER_LOOP_TRACE" ||
		item.RiskID != "F2" ||
		item.CapabilityID != "caller_policy_readiness" ||
		item.EvidenceProfile != "default" ||
		!item.DefaultMode ||
		item.FixtureEnabledMode ||
		item.NegativeOrPositive != "both" ||
		item.EvidenceType != "unit" ||
		!item.Required ||
		item.DocOnlyAllowed ||
		item.OptionalGated ||
		!item.DefaultGARequired ||
		item.PassCriteria.Kind != "coverage_guard" {
		t.Fatalf("%s shape = %+v, want default required default-loop trace evidence", item.ID, item)
	}
	if packages := goTestPackageArgs(item.Command); !stringSlicesEqual(packages, []string{"./internal/api", "./internal/inspection", "./internal/audit", "./internal/recovery", "./internal/workerapp", "./internal/store/postgres"}) {
		t.Fatalf("%s command packages = %#v, want api, inspection, audit, recovery, workerapp, and postgres store", item.ID, packages)
	}
	selector, ok := goTestRunSelector(item.Command)
	if !ok {
		t.Fatalf("%s command has no go test -run selector: %#v", item.ID, item.Command)
	}
	compiled, err := regexp.Compile(selector)
	if err != nil {
		t.Fatalf("%s has invalid -run selector %q: %v", item.ID, selector, err)
	}
	for _, testName := range defaultUserLoopTraceRequiredTestNames() {
		if !compiled.MatchString(testName) {
			t.Fatalf("%s -run selector %q does not match required test %s", item.ID, selector, testName)
		}
		assertGoTestListIncludesTest(t, repoRoot, item.ID, selector, goTestPackageForTestName(testName), testName)
	}
}

func TestCurrentRepoManifestContainsDefaultUserLoopAggregationEvidence(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	manifestPath := filepath.Join(repoRoot, "docs", "release-evidence", "ga-manifest.json")

	manifest, findings, err := LoadAndValidateFile(manifestPath, Options{Mode: ManifestModeSeed, RepoRoot: repoRoot})
	if err != nil {
		t.Fatalf("LoadAndValidateFile returned error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("current manifest findings: %+v", findings)
	}

	item, ok := manifestItemByID(manifest, "default_user_loop_positive_unit")
	if !ok {
		t.Fatal("manifest missing default_user_loop_positive_unit")
	}
	if item.EvidenceStatus != "implemented" ||
		item.ClaimID != "CLAIM_DEFAULT_USER_LOOP" ||
		item.SubclaimID != "default_user_loop_positive" ||
		item.AcceptanceID != "P0_DEFAULT_USER_LOOP_POSITIVE" ||
		item.RiskID != "F2" ||
		item.CapabilityID != "caller_policy_readiness" ||
		item.EvidenceProfile != "default" ||
		!item.DefaultMode ||
		item.FixtureEnabledMode ||
		item.NegativeOrPositive != "positive" ||
		item.EvidenceType != "unit" ||
		!item.Required ||
		item.DocOnlyAllowed ||
		item.OptionalGated ||
		!item.DefaultGARequired ||
		item.PassCriteria.Kind != "positive_path" ||
		!containsString(item.PassCriteria.Assertions, "default user loop passes in default mode") {
		t.Fatalf("%s shape = %+v, want default required default-loop aggregation evidence", item.ID, item)
	}
	if _, ok := manifestItemByID(manifest, "seed_gap_default_user_loop_open"); ok {
		t.Fatal("default user loop aggregation must close seed_gap_default_user_loop_open")
	}
	if packages := goTestPackageArgs(item.Command); !stringSlicesEqual(packages, []string{"./internal/releaseevidence", "./cmd/afscp-evidence-verify"}) {
		t.Fatalf("%s command packages = %#v, want releaseevidence and CLI verifier", item.ID, packages)
	}
	selector, ok := goTestRunSelector(item.Command)
	if !ok {
		t.Fatalf("%s command has no go test -run selector: %#v", item.ID, item.Command)
	}
	compiled, err := regexp.Compile(selector)
	if err != nil {
		t.Fatalf("%s has invalid -run selector %q: %v", item.ID, selector, err)
	}
	for _, testName := range defaultUserLoopAggregationRequiredTestNames {
		if !compiled.MatchString(testName) {
			t.Fatalf("%s -run selector %q does not match required test %s", item.ID, selector, testName)
		}
		assertGoTestListIncludesTest(t, repoRoot, item.ID, selector, goTestPackageForTestName(testName), testName)
	}
}

func TestDefaultUserLoopAggregationRejectsMissingPrereq(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	body := strings.Replace(validReleaseEvidenceManifest(), `"id":"default_user_loop_trace_unit"`, `"id":"default_user_loop_trace_missing"`, 1)
	path := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, path, body)

	findings, err := VerifyFile(path, Options{Mode: ManifestModeSeed, RepoRoot: root, ExecuteRequired: false})
	if err != nil {
		t.Fatalf("VerifyFile returned unexpected error: %v", err)
	}
	assertReleaseEvidenceFindingContains(t, findings, "default_user_loop_trace_unit")
}

func TestDefaultUserLoopAggregationRejectsPlaceholderPrereq(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	body := strings.Replace(validReleaseEvidenceManifest(), `"id":"default_user_loop_trace_unit",
      "evidence_status":"implemented"`, `"id":"default_user_loop_trace_unit",
      "evidence_status":"placeholder"`, 1)
	path := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, path, body)

	findings, err := VerifyFile(path, Options{Mode: ManifestModeSeed, RepoRoot: root, ExecuteRequired: false})
	if err != nil {
		t.Fatalf("VerifyFile returned unexpected error: %v", err)
	}
	assertReleaseEvidenceFindingContains(t, findings, "default_user_loop_trace_unit")
	assertReleaseEvidenceFindingContains(t, findings, "placeholder")
}

func TestDefaultUserLoopAggregationRejectsWrongProfileDefaultModePolarityRequiredOrDocOnlyPrereq(t *testing.T) {
	tests := []struct {
		name string
		edit func(string) string
	}{
		{
			name: "wrong profile",
			edit: func(body string) string {
				return replaceItemField(t, body, "default_user_loop_trace_unit", `"evidence_profile":"default"`, `"evidence_profile":"repo-local-fixture-enabled"`)
			},
		},
		{
			name: "wrong default mode",
			edit: func(body string) string {
				return replaceItemField(t, body, "default_user_loop_trace_unit", `"default_mode":true`, `"default_mode":false`)
			},
		},
		{
			name: "wrong polarity",
			edit: func(body string) string {
				return replaceItemField(t, body, "default_user_loop_trace_unit", `"negative_or_positive":"both"`, `"negative_or_positive":"negative"`)
			},
		},
		{
			name: "not required",
			edit: func(body string) string {
				return replaceItemField(t, body, "default_user_loop_trace_unit", `"required":true`, `"required":false`)
			},
		},
		{
			name: "doc only",
			edit: func(body string) string {
				return replaceItemField(t, body, "default_user_loop_trace_unit", `"doc_only_allowed":false`, `"doc_only_allowed":true`)
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
			assertReleaseEvidenceFindingContains(t, findings, "default_user_loop_trace_unit")
		})
	}
}

func TestDefaultUserLoopAggregationRejectsPartialOnlyManifest(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	body := strings.Replace(validReleaseEvidenceManifest(), `"id":"default_user_loop_positive_unit"`, `"id":"default_user_loop_positive_missing"`, 1)
	path := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, path, body)

	findings, err := VerifyFile(path, Options{Mode: ManifestModeSeed, RepoRoot: root, ExecuteRequired: false})
	if err != nil {
		t.Fatalf("VerifyFile returned unexpected error: %v", err)
	}
	assertReleaseEvidenceFindingContains(t, findings, "default_user_loop_positive_unit")
}

func TestDefaultUserLoopOpenSeedGapIsAcceptedInSeedModeWithoutAggregation(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	body := strings.Replace(validReleaseEvidenceManifest(), `"id":"default_user_loop_positive_unit"`, `"id":"default_user_loop_positive_missing"`, 1)
	body = strings.Replace(body, `"id":"default_user_loop_trace_unit"`, `"id":"default_user_loop_trace_missing"`, 1)
	body = appendReleaseEvidenceItem(body, `"id":"seed_gap_default_user_loop_open","evidence_status":"placeholder","claim_id":"CLAIM_DEFAULT_USER_LOOP","subclaim_id":"seed_gap_open","acceptance_id":"P0_SEED_GAP_OPEN","risk_id":"F2","fixture_id":"","capability_id":"","evidence_profile":"default","default_mode":true,"fixture_enabled_mode":false,"expected_runtime":"fast","scope":"doc-guard","negative_or_positive":"both","evidence_type":"doc-guard","required":false,"command":[],"anchors":["docs/GA_NEXT_PHASE_DEVELOPMENT_HANDOFF_PLAN.md"],"doc_only_allowed":true,"optional_gated":false,"default_ga_required":false,"pass_criteria":{"kind":"seed_gap","assertions":["open"]}`)
	path := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, path, body)

	findings, err := VerifyFile(path, Options{Mode: ManifestModeSeed, RepoRoot: root, ExecuteRequired: false})
	if err != nil {
		t.Fatalf("VerifyFile returned unexpected error: %v", err)
	}
	assertNoReleaseEvidenceFindingContains(t, findings, "default_user_loop_positive_unit")
	assertNoReleaseEvidenceFindingContains(t, findings, "default_user_loop_trace_unit")
	assertNoReleaseEvidenceFindingContains(t, findings, "manifest.default_user_loop_aggregation_missing")
}

func TestDefaultUserLoopOpenSeedGapIsRejectedInFinalMode(t *testing.T) {
	root := releaseEvidenceFixtureRoot(t)
	body := strings.Replace(validReleaseEvidenceManifest(), `"id":"default_user_loop_positive_unit"`, `"id":"default_user_loop_positive_missing"`, 1)
	body = appendReleaseEvidenceItem(body, `"id":"seed_gap_default_user_loop_open","evidence_status":"placeholder","claim_id":"CLAIM_DEFAULT_USER_LOOP","subclaim_id":"seed_gap_open","acceptance_id":"P0_SEED_GAP_OPEN","risk_id":"F2","fixture_id":"","capability_id":"","evidence_profile":"default","default_mode":true,"fixture_enabled_mode":false,"expected_runtime":"fast","scope":"doc-guard","negative_or_positive":"both","evidence_type":"doc-guard","required":false,"command":[],"anchors":["docs/GA_NEXT_PHASE_DEVELOPMENT_HANDOFF_PLAN.md"],"doc_only_allowed":true,"optional_gated":false,"default_ga_required":false,"pass_criteria":{"kind":"seed_gap","assertions":["open"]}`)
	path := filepath.Join(root, "manifest.json")
	writeReleaseEvidenceFile(t, path, body)

	findings, err := VerifyFile(path, finalReleaseOptions(t, root, nil))
	if err != nil {
		t.Fatalf("VerifyFile returned unexpected error: %v", err)
	}
	assertReleaseEvidenceFindingContains(t, findings, "seed_gap_default_user_loop_open")
	assertReleaseEvidenceFindingContains(t, findings, "default_user_loop_positive_unit")
}

func TestCurrentRepoManifestContainsP3OperatorRepairSafeEvidence(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	manifestPath := filepath.Join(repoRoot, "docs", "release-evidence", "ga-manifest.json")

	manifest, findings, err := LoadAndValidateFile(manifestPath, Options{Mode: ManifestModeSeed, RepoRoot: repoRoot})
	if err != nil {
		t.Fatalf("LoadAndValidateFile returned error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("current manifest findings: %+v", findings)
	}

	item, ok := manifestItemByID(manifest, "operator_repair_safe_unit")
	if !ok {
		t.Fatal("manifest missing operator_repair_safe_unit")
	}
	if item.EvidenceStatus != "implemented" ||
		item.ClaimID != "CLAIM_OPERATOR_REPAIR_SAFE" ||
		item.SubclaimID != "operator_repair_safe" ||
		item.AcceptanceID != "P0_OPERATOR_REPAIR_SAFE" ||
		item.RiskID != "F11" ||
		item.CapabilityID != "operation_recovery" ||
		item.EvidenceProfile != "default" ||
		!item.DefaultMode ||
		item.FixtureEnabledMode ||
		item.NegativeOrPositive != "both" ||
		item.EvidenceType != "unit" ||
		!item.Required ||
		item.DocOnlyAllowed ||
		item.OptionalGated ||
		!item.DefaultGARequired ||
		item.PassCriteria.Kind != "coverage_guard" ||
		!containsString(item.PassCriteria.Assertions, "operator repair safety passes in default mode") {
		t.Fatalf("%s shape = %+v, want default required operator repair evidence", item.ID, item)
	}
	if _, ok := manifestItemByID(manifest, "seed_gap_operator_repair_safe_open"); ok {
		t.Fatal("operator repair evidence must close seed_gap_operator_repair_safe_open")
	}
	if packages := goTestPackageArgs(item.Command); !stringSlicesEqual(packages, []string{"./internal/operatorrepair", "./internal/store/postgres", "./internal/api", "./internal/contractcheck", "./internal/releaseevidence", "./cmd/afscp-evidence-verify"}) {
		t.Fatalf("%s command packages = %#v, want operatorrepair, postgres, api, contractcheck, releaseevidence, and CLI verifier", item.ID, packages)
	}
	selector, ok := goTestRunSelector(item.Command)
	if !ok {
		t.Fatalf("%s command has no go test -run selector: %#v", item.ID, item.Command)
	}
	compiled, err := regexp.Compile(selector)
	if err != nil {
		t.Fatalf("%s has invalid -run selector %q: %v", item.ID, selector, err)
	}
	for _, testName := range operatorRepairSafeRequiredTestNames {
		if !compiled.MatchString(testName) {
			t.Fatalf("%s -run selector %q does not match required test %s", item.ID, selector, testName)
		}
		assertGoTestListIncludesTest(t, repoRoot, item.ID, selector, goTestPackageForTestName(testName), testName)
	}
}

func TestOperatorRepairSafeReplacementRejectsWrongShapeOrBroadSelector(t *testing.T) {
	tests := []struct {
		name string
		edit func(string) string
		want string
	}{
		{
			name: "placeholder",
			edit: func(body string) string {
				return replaceItemField(t, body, "operator_repair_safe_unit", `"evidence_status":"implemented"`, `"evidence_status":"placeholder"`)
			},
			want: "operator_repair_safe_unit",
		},
		{
			name: "doc only",
			edit: func(body string) string {
				return replaceItemField(t, body, "operator_repair_safe_unit", `"doc_only_allowed":false`, `"doc_only_allowed":true`)
			},
			want: "operator_repair_safe_unit",
		},
		{
			name: "broad selector",
			edit: func(body string) string {
				return replaceItemCommand(t, body, "operator_repair_safe_unit", `"command":["go","test","./internal/api","-run","Test.*Repair"]`)
			},
			want: "selector",
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

func TestDefaultUserLoopAggregationRejectsBroadOrHelperOnlyCommand(t *testing.T) {
	tests := []struct {
		name        string
		replacement string
	}{
		{
			name:        "broad selector",
			replacement: `"command":["go","test","-count=1","./internal/releaseevidence","./cmd/afscp-evidence-verify","-run","Test.*"]`,
		},
		{
			name:        "helper only package",
			replacement: `"command":["go","test","-count=1","./internal/evidencetest","-run","^TestExistingEvidenceSelector$"]`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := releaseEvidenceFixtureRoot(t)
			body := strings.Replace(validReleaseEvidenceManifest(), `"command":["go","test","-count=1","./internal/releaseevidence","./cmd/afscp-evidence-verify","-run","^Test(DefaultUserLoopAggregationRejectsMissingPrereq|DefaultUserLoopAggregationRejectsPlaceholderPrereq|DefaultUserLoopAggregationRejectsWrongProfileDefaultModePolarityRequiredOrDocOnlyPrereq|DefaultUserLoopAggregationRejectsPartialOnlyManifest|DefaultUserLoopAggregationRejectsBroadOrHelperOnlyCommand|DefaultUserLoopAggregationRejectsBroadOrHelperOnlyPrereqCommand|RunCheckOnlyAcceptsDefaultUserLoopAggregationManifest)$"]`, tt.replacement, 1)
			path := filepath.Join(root, "manifest.json")
			writeReleaseEvidenceFile(t, path, body)

			findings, err := VerifyFile(path, Options{Mode: ManifestModeSeed, RepoRoot: root, ExecuteRequired: false})
			if err != nil {
				t.Fatalf("VerifyFile returned unexpected error: %v", err)
			}
			assertReleaseEvidenceFindingContains(t, findings, "default_user_loop_positive_unit")
		})
	}
}

func TestDefaultUserLoopAggregationRejectsBroadOrHelperOnlyPrereqCommand(t *testing.T) {
	tests := []struct {
		name        string
		id          string
		replacement string
	}{
		{
			name:        "repo projection broad selector",
			id:          "default_user_loop_repo_projection_unit",
			replacement: `"command":["go","test","-count=1","./internal/api","-run","Test.*"]`,
		},
		{
			name:        "trace helper only selector",
			id:          "default_user_loop_trace_unit",
			replacement: `"command":["go","test","-count=1","./internal/evidencetest","-run","^TestExistingEvidenceSelector$"]`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := releaseEvidenceFixtureRoot(t)
			body := replaceItemCommand(t, validReleaseEvidenceManifest(), tt.id, tt.replacement)
			path := filepath.Join(root, "manifest.json")
			writeReleaseEvidenceFile(t, path, body)

			findings, err := VerifyFile(path, Options{Mode: ManifestModeSeed, RepoRoot: root, ExecuteRequired: false})
			if err != nil {
				t.Fatalf("VerifyFile returned unexpected error: %v", err)
			}
			assertReleaseEvidenceFindingContains(t, findings, tt.id)
			assertReleaseEvidenceFindingContains(t, findings, "command")
		})
	}
}

func defaultUserLoopTraceRequiredTestNames() []string {
	return []string{
		"TestOperationInspectionHandlerReturnsRedactedRecordWithoutNamespaceHeader",
		"TestOperationInspectionHandlerHidesProductNamespaceMismatchAsNotFoundAndAudits",
		"TestOperationInspectionHandlerHidesProductGlobalDeniedAsNotFoundAndAudits",
		"TestOperationInspectionHandlerRequiresStoredBindingForProductCaller",
		"TestOperationEnvelopeJSONIsFlatAndSchemaShaped",
		"TestOperationEnvelopeCarriesResultAndTerminalError",
		"TestProductCallerOperationResponsesDoNotLeakStorageInternals",
		"TestAuthGateWithAuditSinkEmitsDeniedEventsWithoutSensitiveRequestData",
		"TestInternalAPIShellServesOperationInspectionThroughInjectedReader",
		"TestInternalAPIShellOperationInspectionProductStillRequiresStoredBinding",
		"TestInspectOperationAllowsProductInspectionRoleAndRedactsRecord",
		"TestInspectOperationRequiresStoredNamespaceToMatchRequestNamespaceForProductCaller",
		"TestInspectOperationDeniesGlobalRecordToProductCaller",
		"TestInspectOperationDeniesProductCallerAuthorizedOnlyForDifferentStoredNamespace",
		"TestNamespaceVolumeBindingAuthorizerWiresIntoInspectOperationAndKeepsRedaction",
		"TestOperationTypesMapToStableAuditEventTypes",
		"TestDeniedEventsAllowEmptyOperationID",
		"TestEventJSONContainsStableAuditFields",
		"TestOperationCoordinatorCommitsUnsupportedClaimRetryAndReclaimWithAuditWithoutExecute",
		"TestRunOnceOperationAndAuditCanRunTogether",
		"TestRunOnceAuditOnlyRunsStaleRecoveryBeforeDelivery",
		"TestRunOnceExportSessionReconcileRunsBeforeOperationRecovery",
		"TestRunOnceAuditDeliveryFailureRecordsRetryWithoutLeakingSecret",
		"TestCreateOperationBuildsFullInsertWithSanitizedJSON",
		"TestGetOperationScansFullRecord",
		"TestCommitOperationWithLeaseAtomicallyUpdatesOperationAndAppendsAudit",
		"TestAppendAuditEventInsertsPendingOutboxRecord",
		"TestRecoverStaleAuditOutboxRecordsAtomicallyUpdatesRetryWaitWithoutTerminalFailure",
		"TestMarkAuditOutboxDeliveryFailedChoosesRetryWaitOrFailedAndRedactsError",
	}
}

func operatorRepairSafeRequiredTestNamesForTest() []string {
	return []string{
		"TestOperatorRepairRejectsUnknownAction",
		"TestOperatorRepairRequiresReasonEvidenceAndAffectedIDs",
		"TestOperatorRepairRejectsSecretShapedReasonOrEvidenceRef",
		"TestOperatorRepairRejectsAmbiguousOrFencedIntervention",
		"TestOperatorRepairBuildsFailedRecordWithRedactedBeforeAfter",
		"TestStoreImplementsOperatorRepairStore",
		"TestCommitOperatorRepairFailedUsesAtomicCASAndAuditOutbox",
		"TestCommitOperatorRepairFailedRequiresSafeInterventionShapeBeforeSQL",
		"TestCommitOperatorRepairFailedCASRejectsConcurrentAmbiguousPhase",
		"TestCommitOperatorRepairFailedNoRowsFailsClosed",
		"TestOperatorRepairHandlerOperatorAdminTerminalizesUnsupportedInterventionWithAudit",
		"TestOperatorRepairHandlerRejectsProductOperationInspectorBeforeStore",
		"TestOperatorRepairHandlerRejectsInvalidBodyBeforeStore",
		"TestOperatorRepairHandlerIdempotentReplayStableWithoutDuplicateAudit",
		"TestInternalAPIShellServesOperatorRepairThroughInjectedStore",
		"TestOperatorRepairContractDefinesAllowlistPreconditionsAuditAndForbiddenSQL",
		"TestOperatorRepairContractIsLinkedFromContractsReadme",
		"TestCurrentRepoManifestContainsP3OperatorRepairSafeEvidence",
		"TestOperatorRepairSafeReplacementRejectsWrongShapeOrBroadSelector",
		"TestRunCheckOnlyAcceptsOperatorRepairSafeManifest",
	}
}

func TestCurrentRepoManifestKeepsOtherSeedGapsOpenAfterPartialP1b(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	manifestPath := filepath.Join(repoRoot, "docs", "release-evidence", "ga-manifest.json")

	manifest, findings, err := LoadAndValidateFile(manifestPath, Options{Mode: ManifestModeSeed, RepoRoot: repoRoot})
	if err != nil {
		t.Fatalf("LoadAndValidateFile returned error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("current manifest findings: %+v", findings)
	}

	for _, gapID := range []string{
		"seed_gap_discovery_surfaces_open",
		"seed_gap_secret_path_redaction_open",
	} {
		item, ok := manifestItemByID(manifest, gapID)
		if !ok {
			t.Fatalf("manifest must keep %s open for partial P1b", gapID)
		}
		if item.EvidenceStatus != "placeholder" || item.PassCriteria.Kind != "seed_gap" || !containsString(item.PassCriteria.Assertions, "open") {
			t.Fatalf("%s = %+v, want open placeholder seed gap", gapID, item)
		}
	}
	for _, item := range manifest.Items {
		if item.ID == "default_user_loop_repo_projection_unit" && item.AcceptanceID != "P1B_DEFAULT_USER_LOOP_REPO_PROJECTION" {
			t.Fatalf("P1b repo projection must remain partial evidence with P1B acceptance: %+v", item)
		}
	}
}

func TestCurrentRepoManifestKeepsOtherSeedGapsOpenAfterPartialP1c(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	manifestPath := filepath.Join(repoRoot, "docs", "release-evidence", "ga-manifest.json")

	manifest, findings, err := LoadAndValidateFile(manifestPath, Options{Mode: ManifestModeSeed, RepoRoot: repoRoot})
	if err != nil {
		t.Fatalf("LoadAndValidateFile returned error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("current manifest findings: %+v", findings)
	}

	for _, gapID := range []string{
		"seed_gap_restore_reconciliation_open",
		"seed_gap_secret_path_redaction_open",
		"seed_gap_discovery_surfaces_open",
	} {
		item, ok := manifestItemByID(manifest, gapID)
		if !ok {
			t.Fatalf("manifest must keep %s open for partial P1c", gapID)
		}
		if item.EvidenceStatus != "placeholder" || item.PassCriteria.Kind != "seed_gap" || !containsString(item.PassCriteria.Assertions, "open") {
			t.Fatalf("%s = %+v, want open placeholder seed gap", gapID, item)
		}
	}
	for _, item := range manifest.Items {
		if item.ID == "default_user_loop_jvs_save_restore_unit" && item.AcceptanceID != "P1C_DEFAULT_USER_LOOP_JVS_SAVE_RESTORE" {
			t.Fatalf("P1c JVS save/restore must remain partial evidence with P1C acceptance: %+v", item)
		}
	}
}

func TestCurrentRepoManifestKeepsWebDAVSeedGapClosedAfterPartialP1d(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	manifestPath := filepath.Join(repoRoot, "docs", "release-evidence", "ga-manifest.json")

	manifest, findings, err := LoadAndValidateFile(manifestPath, Options{Mode: ManifestModeSeed, RepoRoot: repoRoot})
	if err != nil {
		t.Fatalf("LoadAndValidateFile returned error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("current manifest findings: %+v", findings)
	}

	if _, ok := manifestItemByID(manifest, "seed_gap_webdav_default_access_open"); ok {
		t.Fatal("P1d must close seed_gap_webdav_default_access_open with exact WebDAV replacement evidence")
	}
	for _, item := range manifest.Items {
		if item.ID == "default_user_loop_webdav_access_unit" && item.AcceptanceID != "P1D_DEFAULT_USER_LOOP_WEBDAV_ACCESS" {
			t.Fatalf("P1d WebDAV access must remain partial evidence with P1D acceptance: %+v", item)
		}
	}
}

func TestCurrentRepoManifestClosesDefaultUserLoopOnlyWithAggregation(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	manifestPath := filepath.Join(repoRoot, "docs", "release-evidence", "ga-manifest.json")

	manifest, findings, err := LoadAndValidateFile(manifestPath, Options{Mode: ManifestModeSeed, RepoRoot: repoRoot})
	if err != nil {
		t.Fatalf("LoadAndValidateFile returned error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("current manifest findings: %+v", findings)
	}
	if _, ok := manifestItemByID(manifest, "seed_gap_default_user_loop_open"); ok {
		t.Fatal("manifest must close seed_gap_default_user_loop_open only after aggregation evidence exists")
	}
	item, ok := manifestItemByID(manifest, "default_user_loop_positive_unit")
	if !ok {
		t.Fatal("manifest must include default_user_loop_positive_unit aggregation evidence")
	}
	if item.AcceptanceID != "P0_DEFAULT_USER_LOOP_POSITIVE" || item.EvidenceStatus != "implemented" {
		t.Fatalf("default loop closure must use implemented P0 aggregation evidence: %+v", item)
	}
}

func TestCurrentRepoManifestKeepsDefaultUserLoopAndDiscoverySeedGapsOpenForP2b(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	manifestPath := filepath.Join(repoRoot, "docs", "release-evidence", "ga-manifest.json")

	manifest, findings, err := LoadAndValidateFile(manifestPath, Options{Mode: ManifestModeSeed, RepoRoot: repoRoot})
	if err != nil {
		t.Fatalf("LoadAndValidateFile returned error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("current manifest findings: %+v", findings)
	}
	for _, gapID := range []string{"seed_gap_discovery_surfaces_open"} {
		item, ok := manifestItemByID(manifest, gapID)
		if !ok {
			t.Fatalf("manifest must keep %s open for P2b", gapID)
		}
		if item.EvidenceStatus != "placeholder" || item.PassCriteria.Kind != "seed_gap" || !containsString(item.PassCriteria.Assertions, "open") {
			t.Fatalf("%s = %+v, want open placeholder seed gap", gapID, item)
		}
	}
	if _, ok := manifestItemByID(manifest, "default_user_loop_positive_unit"); !ok {
		t.Fatal("manifest should close default user loop through aggregation after P2b and P1 partial evidence")
	}
}

func TestCurrentRepoManifestSeedModeAllowsOpenSeedGaps(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	manifestPath := filepath.Join(repoRoot, "docs", "release-evidence", "ga-manifest.json")

	_, findings, err := LoadAndValidateFile(manifestPath, Options{Mode: ManifestModeSeed, RepoRoot: repoRoot})
	if err != nil {
		t.Fatalf("LoadAndValidateFile returned error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("seed mode should allow the current repo manifest, got findings: %+v", findings)
	}
}

func TestCurrentRepoManifestFinalModeRequiresSelector(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	manifestPath := filepath.Join(repoRoot, "docs", "release-evidence", "ga-manifest.json")

	_, findings, err := LoadAndValidateFile(manifestPath, Options{Mode: ManifestModeFinal, RepoRoot: repoRoot})
	if err != nil {
		t.Fatalf("LoadAndValidateFile returned error: %v", err)
	}
	assertReleaseEvidenceFindingContains(t, findings, "selector")
	assertNoReleaseEvidenceFindingContains(t, findings, "repo_create_jvs_runtime_unavailable_recovery_unit: item.capability_id_legacy_final_invalid")
}

func goTestPackageForTestName(testName string) string {
	if strings.HasPrefix(testName, "TestOperatorRepairRejects") ||
		strings.HasPrefix(testName, "TestOperatorRepairRequires") ||
		strings.HasPrefix(testName, "TestOperatorRepairBuilds") {
		return "./internal/operatorrepair"
	}
	if strings.HasPrefix(testName, "TestOperatorRepairHandler") {
		return "./internal/api"
	}
	if strings.HasPrefix(testName, "TestOperatorRepairContract") {
		return "./internal/contractcheck"
	}
	if strings.HasPrefix(testName, "TestCurrentRepoManifestContainsP3OperatorRepairSafe") ||
		strings.HasPrefix(testName, "TestOperatorRepairSafeReplacement") {
		return "./internal/releaseevidence"
	}
	if strings.HasPrefix(testName, "TestRunCheckOnlyAcceptsOperatorRepairSafe") {
		return "./cmd/afscp-evidence-verify"
	}
	if strings.HasPrefix(testName, "TestStoreImplementsOperatorRepairStore") ||
		strings.HasPrefix(testName, "TestCommitOperatorRepairFailed") {
		return "./internal/store/postgres"
	}
	if strings.HasPrefix(testName, "TestCapabilityMatrixAdmissionDisabled") {
		return "./internal/api"
	}
	if strings.HasPrefix(testName, "TestCapability") {
		return "./internal/capability"
	}
	if strings.HasPrefix(testName, "TestInternalRuntime") {
		return "./internal/apiapp"
	}
	if strings.HasPrefix(testName, "TestOperationInspectionHandler") ||
		strings.HasPrefix(testName, "TestOperationEnvelope") ||
		strings.HasPrefix(testName, "TestProductCallerOperationResponses") ||
		strings.HasPrefix(testName, "TestAuthGateWithAuditSink") ||
		strings.HasPrefix(testName, "TestInternalAPIShell") {
		return "./internal/api"
	}
	if strings.HasPrefix(testName, "TestInspectOperation") ||
		strings.HasPrefix(testName, "TestNamespaceVolumeBindingAuthorizer") {
		return "./internal/inspection"
	}
	if strings.HasPrefix(testName, "TestOperationTypesMap") ||
		strings.HasPrefix(testName, "TestDeniedEvents") ||
		strings.HasPrefix(testName, "TestEventJSON") {
		return "./internal/audit"
	}
	if strings.HasPrefix(testName, "TestOperationCoordinator") {
		return "./internal/recovery"
	}
	if strings.HasPrefix(testName, "TestRunOnceReconciles") ||
		strings.HasPrefix(testName, "TestRunOnceRecovers") ||
		strings.HasPrefix(testName, "TestRunOnceTreats") {
		return "./internal/exportreconcile"
	}
	if strings.HasPrefix(testName, "TestRunOnce") {
		return "./internal/workerapp"
	}
	if strings.HasPrefix(testName, "TestWorker") {
		return "./internal/workerapp"
	}
	if strings.HasPrefix(testName, "TestNewJVSRunner") {
		return "./internal/workerapp"
	}
	if strings.HasPrefix(testName, "TestSavePointExecutor") ||
		strings.HasPrefix(testName, "TestRestorePreviewExecutor") ||
		strings.HasPrefix(testName, "TestRestorePreviewDiscardExecutor") ||
		strings.HasPrefix(testName, "TestRestoreRunExecutor") {
		return "./internal/repoexec"
	}
	if strings.HasPrefix(testName, "TestCreateGetAndListRepos") ||
		strings.HasPrefix(testName, "TestCreateOperationBuilds") ||
		strings.HasPrefix(testName, "TestCreateOrReuseRepoCreateOperation") ||
		strings.HasPrefix(testName, "TestCreateOrReuseExport") ||
		strings.HasPrefix(testName, "TestCommitRepoCreate") ||
		strings.HasPrefix(testName, "TestCommitOperationWithLease") ||
		strings.HasPrefix(testName, "TestAppendAuditEvent") ||
		strings.HasPrefix(testName, "TestRecoverStaleAudit") ||
		strings.HasPrefix(testName, "TestMarkAuditOutbox") ||
		strings.HasPrefix(testName, "TestGetOperationScans") ||
		strings.HasPrefix(testName, "TestAcquireSavePointCreateOperationLease") ||
		strings.HasPrefix(testName, "TestCommitSavePointCreate") ||
		strings.HasPrefix(testName, "TestCreateOrReuseRestore") ||
		strings.HasPrefix(testName, "TestCommitRestore") ||
		strings.HasPrefix(testName, "TestGetExportSession") ||
		strings.HasPrefix(testName, "TestRevokeExportSQL") ||
		strings.HasPrefix(testName, "TestRevokeExportClassifies") ||
		strings.HasPrefix(testName, "TestGatewayCredential") ||
		strings.HasPrefix(testName, "TestBeginExportRuntime") ||
		strings.HasPrefix(testName, "TestHeartbeatAndEndExportRuntime") ||
		strings.HasPrefix(testName, "TestEndExportRuntime") ||
		strings.HasPrefix(testName, "TestRecoverStaleExportRuntimeRequests") ||
		strings.HasPrefix(testName, "TestReconcileExportSession") ||
		strings.HasPrefix(testName, "TestListExportSessionsForTerminalReconcile") {
		return "./internal/store/postgres"
	}
	if strings.HasPrefix(testName, "TestPasswordVerifier") ||
		strings.HasPrefix(testName, "TestResolveTTL") ||
		strings.HasPrefix(testName, "TestSessionValidation") {
		return "./internal/exportaccess"
	}
	if strings.HasPrefix(testName, "TestReadOnly") ||
		strings.HasPrefix(testName, "TestReadWrite") ||
		strings.HasPrefix(testName, "TestSuccessfulGET") ||
		strings.HasPrefix(testName, "TestInactive") ||
		strings.HasPrefix(testName, "TestBasicAuth") ||
		strings.HasPrefix(testName, "TestGatewayStore") ||
		strings.HasPrefix(testName, "TestDenied") ||
		strings.HasPrefix(testName, "TestBeginRuntimeRequest") {
		return "./internal/exportgateway"
	}
	if strings.HasPrefix(testName, "TestCurrentRepoReadiness") {
		return "./internal/contractcheck"
	}
	if strings.HasPrefix(testName, "TestDefaultUserLoopAggregation") {
		return "./internal/releaseevidence"
	}
	if strings.HasPrefix(testName, "TestRunCheckOnlyAcceptsDefaultUserLoopAggregationManifest") {
		return "./cmd/afscp-evidence-verify"
	}
	if strings.HasPrefix(testName, "TestOperation") {
		return "./internal/contractcheck"
	}
	return "./internal/api"
}

func manifestItemByID(manifest Manifest, id string) (Item, bool) {
	for _, item := range manifest.Items {
		if item.ID == id {
			return item, true
		}
	}
	return Item{}, false
}

func assertGoTestListIncludesTest(t *testing.T, repoRoot, itemID, selector, pkg, testName string) {
	t.Helper()

	compiled, err := regexp.Compile(selector)
	if err != nil {
		t.Fatalf("%s selector %q failed to compile: %v", itemID, selector, err)
	}
	if !compiled.MatchString(testName) {
		t.Fatalf("%s selector %q does not match %s before package lookup", itemID, selector, testName)
	}
	result := goTestListPackage(repoRoot, pkg)
	if result.err != "" {
		t.Fatalf("%s go test -list %s failed: %s: %s", itemID, pkg, result.err, result.output)
	}
	for _, name := range result.tests {
		if name == testName {
			return
		}
	}
	t.Fatalf("%s go test -list %s output missing %s: %s", itemID, pkg, testName, result.output)
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

			findings, err := VerifyFile(path, Options{Mode: ManifestModeSeed, RepoRoot: root, ExecuteRequired: false})
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
	writeReleaseEvidenceFile(t, filepath.Join(root, "docs", "GA_NEXT_PHASE_DEVELOPMENT_HANDOFF_PLAN.md"), "fixture\n")
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
	writeReleaseEvidenceFile(t, filepath.Join(root, "internal", "releaseevidence", "aggregation_test.go"), `package releaseevidence

import "testing"

func TestDefaultUserLoopAggregationRejectsMissingPrereq(t *testing.T) {}
func TestDefaultUserLoopAggregationRejectsPlaceholderPrereq(t *testing.T) {}
func TestDefaultUserLoopAggregationRejectsWrongProfileDefaultModePolarityRequiredOrDocOnlyPrereq(t *testing.T) {}
func TestDefaultUserLoopAggregationRejectsPartialOnlyManifest(t *testing.T) {}
func TestDefaultUserLoopAggregationRejectsBroadOrHelperOnlyCommand(t *testing.T) {}
func TestDefaultUserLoopAggregationRejectsBroadOrHelperOnlyPrereqCommand(t *testing.T) {}
func TestCurrentRepoManifestContainsP3OperatorRepairSafeEvidence(t *testing.T) {}
func TestOperatorRepairSafeReplacementRejectsWrongShapeOrBroadSelector(t *testing.T) {}
`)
	writeReleaseEvidenceFile(t, filepath.Join(root, "internal", "operatorrepair", "repair_test.go"), `package operatorrepair

import "testing"

func TestOperatorRepairRejectsUnknownAction(t *testing.T) {}
func TestOperatorRepairRequiresReasonEvidenceAndAffectedIDs(t *testing.T) {}
func TestOperatorRepairRejectsSecretShapedReasonOrEvidenceRef(t *testing.T) {}
func TestOperatorRepairRejectsAmbiguousOrFencedIntervention(t *testing.T) {}
func TestOperatorRepairBuildsFailedRecordWithRedactedBeforeAfter(t *testing.T) {}
`)
	writeReleaseEvidenceFile(t, filepath.Join(root, "internal", "store", "postgres", "operator_repair_test.go"), `package postgres

import "testing"

func TestStoreImplementsOperatorRepairStore(t *testing.T) {}
func TestCommitOperatorRepairFailedUsesAtomicCASAndAuditOutbox(t *testing.T) {}
func TestCommitOperatorRepairFailedRequiresSafeInterventionShapeBeforeSQL(t *testing.T) {}
func TestCommitOperatorRepairFailedCASRejectsConcurrentAmbiguousPhase(t *testing.T) {}
func TestCommitOperatorRepairFailedNoRowsFailsClosed(t *testing.T) {}
`)
	writeReleaseEvidenceFile(t, filepath.Join(root, "internal", "api", "operator_repair_handler_test.go"), `package api

import "testing"

func TestOperatorRepairHandlerOperatorAdminTerminalizesUnsupportedInterventionWithAudit(t *testing.T) {}
func TestOperatorRepairHandlerRejectsProductOperationInspectorBeforeStore(t *testing.T) {}
func TestOperatorRepairHandlerRejectsInvalidBodyBeforeStore(t *testing.T) {}
func TestOperatorRepairHandlerIdempotentReplayStableWithoutDuplicateAudit(t *testing.T) {}
func TestInternalAPIShellServesOperatorRepairThroughInjectedStore(t *testing.T) {}
`)
	writeReleaseEvidenceFile(t, filepath.Join(root, "internal", "contractcheck", "operator_repair_contract_test.go"), `package contractcheck

import "testing"

func TestOperatorRepairContractDefinesAllowlistPreconditionsAuditAndForbiddenSQL(t *testing.T) {}
func TestOperatorRepairContractIsLinkedFromContractsReadme(t *testing.T) {}
`)
	writeReleaseEvidenceFile(t, filepath.Join(root, "cmd", "afscp-evidence-verify", "main_test.go"), `package main

import "testing"

func TestRunCheckOnlyAcceptsDefaultUserLoopAggregationManifest(t *testing.T) {}
func TestRunCheckOnlyAcceptsOperatorRepairSafeManifest(t *testing.T) {}
`)
	return root
}

func validReleaseEvidenceManifest() string {
	return withPackage0SeedGapMarkers(withPackage0Metadata(`{
  "schema_version":"2",
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
      "capability_id":"workload_mount_binding",
      "evidence_type":"unit",
      "required":true,
      "command":["bash","scripts/pass.sh"],
      "anchors":["scripts/pass.sh"],
      "doc_only_allowed":false,
      "optional_gated":true,
      "default_ga_required":false
    },
    {
      "id":"repo_lifecycle_retained_positive_unit",
      "capability_id":"repo_lifecycle_retained",
      "evidence_type":"unit",
      "required":true,
      "command":["bash","scripts/pass.sh"],
      "anchors":["scripts/pass.sh"],
      "doc_only_allowed":false,
      "optional_gated":false,
      "default_ga_required":true
    },
    {
      "id":"workload_mount_plan_store_freshness_unit",
      "capability_id":"workload_teardown_plan",
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
      "capability_id":"workload_teardown_plan",
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
      "capability_id":"workload_mount_discovery",
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
      "id":"default_user_loop_repo_projection_unit",
      "capability_id":"repo_projection",
      "evidence_type":"unit",
      "required":true,
      "command":["bash","scripts/pass.sh"],
      "anchors":["scripts/pass.sh"],
      "doc_only_allowed":false,
      "optional_gated":false,
      "default_ga_required":true
    },
    {
      "id":"default_user_loop_jvs_save_restore_unit",
      "capability_id":"jvs_save_restore",
      "evidence_type":"unit",
      "required":true,
      "command":["bash","scripts/pass.sh"],
      "anchors":["scripts/pass.sh"],
      "doc_only_allowed":false,
      "optional_gated":false,
      "default_ga_required":true
    },
    {
      "id":"webdav_default_access_unit",
      "capability_id":"webdav_export",
      "evidence_type":"integration",
      "required":true,
      "command":["bash","scripts/pass.sh"],
      "anchors":["scripts/pass.sh"],
      "doc_only_allowed":false,
      "optional_gated":false,
      "default_ga_required":true
    },
    {
      "id":"default_user_loop_webdav_access_unit",
      "capability_id":"webdav_export",
      "evidence_type":"integration",
      "required":true,
      "command":["bash","scripts/pass.sh"],
      "anchors":["scripts/pass.sh"],
      "doc_only_allowed":false,
      "optional_gated":false,
      "default_ga_required":true
    },
    {
      "id":"default_user_loop_trace_unit",
      "capability_id":"caller_policy_readiness",
      "evidence_type":"unit",
      "required":true,
      "command":["bash","scripts/pass.sh"],
      "anchors":["scripts/pass.sh"],
      "doc_only_allowed":false,
      "optional_gated":false,
      "default_ga_required":true
    },
    {
      "id":"default_user_loop_positive_unit",
      "capability_id":"caller_policy_readiness",
      "evidence_type":"unit",
      "required":true,
      "command":["go","test","-count=1","./internal/releaseevidence","./cmd/afscp-evidence-verify","-run","^Test(DefaultUserLoopAggregationRejectsMissingPrereq|DefaultUserLoopAggregationRejectsPlaceholderPrereq|DefaultUserLoopAggregationRejectsWrongProfileDefaultModePolarityRequiredOrDocOnlyPrereq|DefaultUserLoopAggregationRejectsPartialOnlyManifest|DefaultUserLoopAggregationRejectsBroadOrHelperOnlyCommand|DefaultUserLoopAggregationRejectsBroadOrHelperOnlyPrereqCommand|RunCheckOnlyAcceptsDefaultUserLoopAggregationManifest)$"],
      "anchors":["scripts/pass.sh"],
      "doc_only_allowed":false,
      "optional_gated":false,
      "default_ga_required":true
    },
    {
      "id":"operator_repair_safe_unit",
      "capability_id":"operation_recovery",
      "evidence_type":"unit",
      "required":true,
      "command":["go","test","-count=1","./internal/operatorrepair","./internal/store/postgres","./internal/api","./internal/contractcheck","./internal/releaseevidence","./cmd/afscp-evidence-verify","-run","^Test(OperatorRepairRejectsUnknownAction|OperatorRepairRequiresReasonEvidenceAndAffectedIDs|OperatorRepairRejectsSecretShapedReasonOrEvidenceRef|OperatorRepairRejectsAmbiguousOrFencedIntervention|OperatorRepairBuildsFailedRecordWithRedactedBeforeAfter|StoreImplementsOperatorRepairStore|CommitOperatorRepairFailedUsesAtomicCASAndAuditOutbox|CommitOperatorRepairFailedRequiresSafeInterventionShapeBeforeSQL|CommitOperatorRepairFailedCASRejectsConcurrentAmbiguousPhase|CommitOperatorRepairFailedNoRowsFailsClosed|OperatorRepairHandlerOperatorAdminTerminalizesUnsupportedInterventionWithAudit|OperatorRepairHandlerRejectsProductOperationInspectorBeforeStore|OperatorRepairHandlerRejectsInvalidBodyBeforeStore|OperatorRepairHandlerIdempotentReplayStableWithoutDuplicateAudit|InternalAPIShellServesOperatorRepairThroughInjectedStore|OperatorRepairContractDefinesAllowlistPreconditionsAuditAndForbiddenSQL|OperatorRepairContractIsLinkedFromContractsReadme|CurrentRepoManifestContainsP3OperatorRepairSafeEvidence|OperatorRepairSafeReplacementRejectsWrongShapeOrBroadSelector|RunCheckOnlyAcceptsOperatorRepairSafeManifest)$"],
      "anchors":["scripts/pass.sh"],
      "doc_only_allowed":false,
      "optional_gated":false,
      "default_ga_required":true
    },
    {
      "id":"repo_create_jvs_runtime_unavailable_recovery_unit",
      "capability_id":"repo_create",
      "evidence_type":"unit",
      "required":true,
      "command":["bash","scripts/pass.sh"],
      "anchors":["scripts/pass.sh"],
      "doc_only_allowed":false,
      "optional_gated":false,
      "default_ga_required":true
    },
    {
      "id":"operation_terminalization_contract_unit",
      "capability_id":"operation_recovery",
      "evidence_type":"contract",
      "required":true,
      "command":["bash","scripts/pass.sh"],
      "anchors":["scripts/pass.sh"],
      "doc_only_allowed":false,
      "optional_gated":false,
      "default_ga_required":true
    },
    {
      "id":"operation_runtime_terminalization_unit",
      "capability_id":"operation_recovery",
      "evidence_type":"unit",
      "required":true,
      "command":["bash","scripts/pass.sh"],
      "anchors":["scripts/pass.sh"],
      "doc_only_allowed":false,
      "optional_gated":false,
      "default_ga_required":true
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
      "id":"capability_matrix_v1_contract_unit",
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
      "id":"capability_runtime_parity_unit",
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
}`))
}

func withPackage0Metadata(body string) string {
	for _, metadata := range package0FixtureMetadata {
		body = strings.Replace(body, `"id":"`+metadata.id+`",`, `"id":"`+metadata.id+`",
      "evidence_status":"implemented",
      "claim_id":"`+metadata.claimID+`",
      "subclaim_id":"`+metadata.subclaimID+`",
      "acceptance_id":"`+metadata.acceptanceID+`",
      "risk_id":"`+metadata.riskID+`",
      "fixture_id":"`+metadata.fixtureID+`",
      "evidence_profile":"`+metadata.evidenceProfile+`",
      "default_mode":`+metadata.defaultMode+`,
      "fixture_enabled_mode":`+metadata.fixtureEnabledMode+`,
      "expected_runtime":"`+metadata.expectedRuntime+`",
      "scope":"`+metadata.scope+`",
      "negative_or_positive":"`+metadata.negativeOrPositive+`",`, 1)
		body = insertPackage0PassCriteria(body, metadata.id, metadata.defaultGARequired, metadata.passCriteriaKind, metadata.passCriteriaAssertion)
	}
	return body
}

func insertPackage0PassCriteria(body, id, defaultGARequired, kind, assertion string) string {
	idIndex := strings.Index(body, `"id":"`+id+`"`)
	if idIndex < 0 {
		return body
	}
	field := `"default_ga_required":` + defaultGARequired
	fieldIndex := strings.Index(body[idIndex:], field)
	if fieldIndex < 0 {
		return body
	}
	insertAt := idIndex + fieldIndex + len(field)
	return body[:insertAt] + `,
      "pass_criteria":{"kind":"` + kind + `","assertions":["` + assertion + `"]}` + body[insertAt:]
}

func withPackage0SeedGapMarkers(body string) string {
	for _, gap := range package0SeedGapFixtureMetadata {
		body = appendReleaseEvidenceItem(body, `"id":"`+gap.id+`","evidence_status":"placeholder","claim_id":"`+gap.claimID+`","subclaim_id":"seed_gap_open","acceptance_id":"P0_SEED_GAP_OPEN","risk_id":"`+gap.riskID+`","fixture_id":"","capability_id":"","evidence_profile":"default","default_mode":true,"fixture_enabled_mode":false,"expected_runtime":"fast","scope":"doc-guard","negative_or_positive":"both","evidence_type":"doc-guard","required":false,"command":[],"anchors":["docs/GA_NEXT_PHASE_DEVELOPMENT_HANDOFF_PLAN.md"],"doc_only_allowed":true,"optional_gated":false,"default_ga_required":false,"pass_criteria":{"kind":"seed_gap","assertions":["open"]}`)
	}
	return body
}

var package0SeedGapFixtureMetadata = []struct {
	id      string
	claimID string
	riskID  string
}{
	{"seed_gap_admin_bootstrap_ready_open", "CLAIM_ADMIN_BOOTSTRAP_READY", "F3"},
	{"seed_gap_workload_fixture_ready_open", "CLAIM_WORKLOAD_FIXTURE_READY", "F9"},
	{"seed_gap_purge_approval_safe_open", "CLAIM_PURGE_APPROVAL_SAFE", "F13"},
	{"seed_gap_restore_reconciliation_open", "CLAIM_RESTORE_RECONCILIATION", "F14"},
	{"seed_gap_residual_risk_catalog_open", "CLAIM_RESIDUAL_RISK_CATALOG", "F12"},
	{"seed_gap_deployment_risk_envelope_open", "CLAIM_DEPLOYMENT_RISK_ENVELOPE", "F17"},
	{"seed_gap_profile_boundary_open", "CLAIM_PROFILE_BOUNDARY", "F1"},
	{"seed_gap_discovery_surfaces_open", "CLAIM_DISCOVERY_SURFACES", "F7"},
	{"seed_gap_secret_path_redaction_open", "CLAIM_SECRET_PATH_REDACTION", "F10"},
	{"seed_gap_optional_fixture_conformant_open", "CLAIM_OPTIONAL_FIXTURE_CONFORMANT", "F9"},
	{"seed_gap_template_quota_boundary_open", "CLAIM_TEMPLATE_QUOTA_BOUNDARY", "F16"},
	{"seed_gap_workflow_hardening_guard_open", "CLAIM_WORKFLOW_HARDENING_GUARD", "F18"},
}

var package0FixtureMetadata = []struct {
	id                    string
	claimID               string
	subclaimID            string
	acceptanceID          string
	riskID                string
	fixtureID             string
	evidenceProfile       string
	defaultMode           string
	fixtureEnabledMode    string
	expectedRuntime       string
	scope                 string
	negativeOrPositive    string
	defaultGARequired     string
	passCriteriaKind      string
	passCriteriaAssertion string
}{
	{"webdav_export_disabled_admission_unit", "CLAIM_DEFAULT_DENIAL_SAFE", "webdav_export_disabled_admission", "P0_DEFAULT_DENIAL_WEBDAV_DISABLED_ADMISSION", "F5", "", "default", "true", "false", "fast", "package", "negative", "true", "denial_safety", "disabled admission rejects before metadata and audits without queuing"},
	{"workload_mount_disabled_admission_unit", "CLAIM_OPTIONAL_DENIED_SAFE", "workload_mount_disabled_admission", "P0_OPTIONAL_DENIED_WORKLOAD_ADMISSION", "F5", "", "default", "true", "false", "fast", "package", "negative", "false", "denial_safety", "optional disabled workload mount binding admission rejects create, status update, heartbeat, release, and revoke before metadata/runtime continuation while preserving idempotency replay/conflict precedence"},
	{"repo_lifecycle_retained_positive_unit", "CLAIM_RETAINED_LIFECYCLE_DEFAULT", "retained_lifecycle_positive", "P0_RETAINED_LIFECYCLE_DEFAULT_POSITIVE", "F15", "", "default", "true", "false", "fast", "package", "positive", "true", "positive_path", "retained lifecycle archive restore delete and tombstone flows pass without purge selectors"},
	{"workload_mount_plan_store_freshness_unit", "CLAIM_OPTIONAL_DENIED_SAFE", "workload_mount_plan_store_freshness", "P0_OPTIONAL_DENIED_WORKLOAD_PLAN_STORE", "F9", "", "default", "true", "false", "fast", "package", "negative", "false", "denial_safety", "workload mount plan store fails closed on stale or unsupported default state"},
	{"workload_mount_runtime_secretref_config_unit", "CLAIM_OPTIONAL_DENIED_SAFE", "workload_mount_runtime_secretref_config", "P0_OPTIONAL_DENIED_WORKLOAD_RUNTIME_SECRETREF", "F10", "", "default", "true", "false", "fast", "package", "negative", "false", "denial_safety", "runtime secretref configuration fails closed without leaking values"},
	{"workload_mount_secretref_redaction_unit", "CLAIM_OPTIONAL_DENIED_SAFE", "workload_mount_secretref_redaction", "P0_OPTIONAL_DENIED_WORKLOAD_SECRETREF_REDACTION", "F10", "", "default", "true", "false", "fast", "package", "negative", "false", "denial_safety", "workload mount responses and audits redact secret references and raw paths"},
	{"repo_template_disabled_admission_unit", "CLAIM_OPTIONAL_DENIED_SAFE", "repo_template_disabled_admission", "P0_OPTIONAL_DENIED_TEMPLATE_ADMISSION", "F16", "", "default", "true", "false", "fast", "package", "negative", "false", "denial_safety", "repo template disabled admission rejects before metadata and audits without queuing"},
	{"repo_template_create_disabled_worker_recovery_unit", "CLAIM_OPTIONAL_DENIED_SAFE", "repo_template_create_disabled_worker_recovery", "P0_OPTIONAL_DENIED_TEMPLATE_CREATE_RECOVERY", "F6", "", "default", "true", "false", "fast", "package", "negative", "false", "denial_safety", "disabled template create recovery terminalizes unsupported historical operations"},
	{"repo_template_clone_disabled_worker_recovery_unit", "CLAIM_OPTIONAL_DENIED_SAFE", "repo_template_clone_disabled_worker_recovery", "P0_OPTIONAL_DENIED_TEMPLATE_CLONE_RECOVERY", "F6", "", "default", "true", "false", "fast", "package", "negative", "false", "denial_safety", "disabled template clone recovery terminalizes unsupported historical operations"},
	{"repo_purge_disabled_admission_unit", "CLAIM_OPTIONAL_DENIED_SAFE", "repo_purge_disabled_admission", "P0_OPTIONAL_DENIED_PURGE_ADMISSION", "F13", "", "default", "true", "false", "fast", "package", "negative", "false", "denial_safety", "repo purge disabled admission rejects before metadata and audits without queuing"},
	{"repo_purge_disabled_worker_recovery_unit", "CLAIM_OPTIONAL_DENIED_SAFE", "repo_purge_disabled_worker_recovery", "P0_OPTIONAL_DENIED_PURGE_RECOVERY", "F13", "", "default", "true", "false", "fast", "package", "negative", "false", "denial_safety", "disabled repo purge recovery terminalizes unsupported historical operations"},
	{"default_user_loop_repo_projection_unit", "CLAIM_DEFAULT_USER_LOOP", "default_user_loop_repo_projection", "P1B_DEFAULT_USER_LOOP_REPO_PROJECTION", "F2", "", "default", "true", "false", "fast", "package", "positive", "true", "positive_path", "repo create get list projection and repo-create worker positive path pass without closing the full default user loop"},
	{"default_user_loop_jvs_save_restore_unit", "CLAIM_DEFAULT_USER_LOOP", "default_user_loop_jvs_save_restore", "P1C_DEFAULT_USER_LOOP_JVS_SAVE_RESTORE", "F2", "", "default", "true", "false", "fast", "package", "positive", "true", "positive_path", "JVS save history restore-preview restore-run and discard paths pass without closing the full default user loop"},
	{"webdav_default_access_unit", "CLAIM_WEBDAV_DEFAULT_ACCESS", "webdav_default_access", "P0_WEBDAV_DEFAULT_ACCESS", "F8", "", "default", "true", "false", "fast", "package", "positive", "true", "positive_path", "webdav default access passes in default mode"},
	{"default_user_loop_webdav_access_unit", "CLAIM_DEFAULT_USER_LOOP", "default_user_loop_webdav_access", "P1D_DEFAULT_USER_LOOP_WEBDAV_ACCESS", "F2", "", "default", "true", "false", "fast", "package", "positive", "true", "positive_path", "WebDAV access contributes only partial default user loop evidence"},
	{"default_user_loop_trace_unit", "CLAIM_DEFAULT_USER_LOOP", "default_user_loop_trace", "P1E_DEFAULT_USER_LOOP_TRACE", "F2", "", "default", "true", "false", "fast", "package", "both", "true", "coverage_guard", "caller-scoped operation audit and recovery trace stays redacted and terminally visible"},
	{"default_user_loop_positive_unit", "CLAIM_DEFAULT_USER_LOOP", "default_user_loop_positive", "P0_DEFAULT_USER_LOOP_POSITIVE", "F2", "", "default", "true", "false", "fast", "package", "positive", "true", "positive_path", "default user loop passes in default mode"},
	{"operator_repair_safe_unit", "CLAIM_OPERATOR_REPAIR_SAFE", "operator_repair_safe", "P0_OPERATOR_REPAIR_SAFE", "F11", "", "default", "true", "false", "fast", "package", "both", "true", "coverage_guard", "operator repair safety passes in default mode"},
	{"repo_create_jvs_runtime_unavailable_recovery_unit", "CLAIM_OPERATION_TERMINALIZATION", "repo_create_jvs_runtime_unavailable_recovery", "P1_OPERATION_TERMINALIZATION_REPO_CREATE_JVS_RUNTIME_UNAVAILABLE_RECOVERY", "F6", "", "default", "true", "false", "fast", "package", "negative", "true", "denial_safety", "repo_create enabled recovery terminalizes when production JVS runtime is unavailable and fail-fast boundaries hold"},
	{"operation_terminalization_contract_unit", "CLAIM_OPERATION_TERMINALIZATION", "operation_terminalization_contract", "P2A_OPERATION_TERMINALIZATION_CONTRACT", "F6", "", "default", "true", "false", "fast", "package", "both", "true", "coverage_guard", "operation terminalization contract covers inventory side-effect replay and terminal decisions"},
	{"operation_runtime_terminalization_unit", "CLAIM_OPERATION_TERMINALIZATION", "operation_runtime_terminalization", "P2B_OPERATION_RUNTIME_TERMINALIZATION", "F6", "", "default", "true", "false", "fast", "package", "both", "true", "coverage_guard", "real RunOnce tests cover supported worker rows and registry coverage is auxiliary"},
	{"default_ga_capability_classification_unit", "CLAIM_CAPABILITY_MATRIX_CONSISTENT", "default_ga_capability_classification", "P0_CAPABILITY_MATRIX_DEFAULT_CLASSIFICATION", "F4", "", "default", "true", "false", "fast", "package", "both", "false", "coverage_guard", "capability matrix classifies default and optional capabilities consistently"},
	{"capability_admission_operation_coverage_unit", "CLAIM_CAPABILITY_MATRIX_CONSISTENT", "capability_admission_operation_coverage", "P0_CAPABILITY_MATRIX_OPERATION_COVERAGE", "F4", "", "default", "true", "false", "fast", "package", "both", "false", "coverage_guard", "capability admission operation coverage stays consistent"},
	{"capability_matrix_v1_contract_unit", "CLAIM_CAPABILITY_MATRIX_CONSISTENT", "capability_matrix_v1_contract", "P1_CAPABILITY_MATRIX_V1_CONTRACT", "F4", "", "default", "true", "false", "fast", "package", "both", "false", "coverage_guard", "capability matrix v1 contract covers readyz workload split vocabulary"},
	{"capability_runtime_parity_unit", "CLAIM_CAPABILITY_MATRIX_CONSISTENT", "capability_runtime_parity", "P2B_CAPABILITY_RUNTIME_PARITY", "F4", "", "default", "true", "false", "fast", "package", "both", "false", "coverage_guard", "actual API request-path tests prove matrix disabled admission behavior while worker runtime is carried by operation evidence"},
	{"release_script_evidence_manifest_guard", "CLAIM_RELEASE_GATE_TRACEABLE", "release_gate_invokes_manifest_verifier", "P0_RELEASE_GATE_TRACEABLE_MANIFEST_VERIFIER", "F18", "", "default", "true", "false", "fast", "workflow-guard", "both", "false", "coverage_guard", "release gate invokes the manifest verifier and keeps evidence traceable"},
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
	if !strings.Contains(item, `"evidence_status"`) {
		item = `"evidence_status":"implemented",` + item
	}
	return strings.Replace(body, "\n  ]\n}", ",\n    {"+item+"}\n  ]\n}", 1)
}

func replaceItemField(t *testing.T, body, id, old, replacement string) string {
	t.Helper()

	idNeedle := `"id":"` + id + `"`
	start := strings.Index(body, idNeedle)
	if start < 0 {
		t.Fatalf("missing item %s", id)
	}
	relative := strings.Index(body[start:], old)
	if relative < 0 {
		t.Fatalf("missing field %q after item %s", old, id)
	}
	offset := start + relative
	return body[:offset] + replacement + body[offset+len(old):]
}

func replaceItemCommand(t *testing.T, body, id, replacement string) string {
	t.Helper()

	idNeedle := `"id":"` + id + `"`
	start := strings.Index(body, idNeedle)
	if start < 0 {
		t.Fatalf("missing item %s", id)
	}
	commandStartRelative := strings.Index(body[start:], `"command":[`)
	if commandStartRelative < 0 {
		t.Fatalf("missing command after item %s", id)
	}
	commandStart := start + commandStartRelative
	commandEndRelative := strings.Index(body[commandStart:], `],
      "anchors"`)
	if commandEndRelative < 0 {
		t.Fatalf("missing command terminator after item %s", id)
	}
	commandEnd := commandStart + commandEndRelative + len(`]`)
	return body[:commandStart] + replacement + body[commandEnd:]
}

func stringSlicesEqual(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
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
