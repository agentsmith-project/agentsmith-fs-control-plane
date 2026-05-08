package releaseevidence

import (
	"os"
	"os/exec"
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

func TestCurrentRepoManifestFinalModeRejectsOpenSeedGaps(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	manifestPath := filepath.Join(repoRoot, "docs", "release-evidence", "ga-manifest.json")

	_, findings, err := LoadAndValidateFile(manifestPath, Options{Mode: ManifestModeFinal, RepoRoot: repoRoot})
	if err != nil {
		t.Fatalf("LoadAndValidateFile returned error: %v", err)
	}
	assertReleaseEvidenceFindingContains(t, findings, "manifest.final_seed_gap_open")
	assertReleaseEvidenceFindingContains(t, findings, "seed_gap_admin_bootstrap_ready_open")
	assertReleaseEvidenceFindingContains(t, findings, "CLAIM_ADMIN_BOOTSTRAP_READY")
	assertReleaseEvidenceFindingContains(t, findings, "seed_gap_optional_fixture_conformant_open")
	assertNoReleaseEvidenceFindingContains(t, findings, "repo_create_jvs_runtime_unavailable_recovery_unit: item.capability_id_legacy_final_invalid")
}

func goTestPackageForTestName(testName string) string {
	if strings.HasPrefix(testName, "TestCapability") {
		return "./internal/capability"
	}
	if strings.HasPrefix(testName, "TestInternalRuntime") {
		return "./internal/apiapp"
	}
	return "./internal/api"
}

func assertGoTestListIncludesTest(t *testing.T, repoRoot, itemID, selector, pkg, testName string) {
	t.Helper()

	command := exec.Command("go", "test", "-list", selector, pkg)
	command.Dir = repoRoot
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s go test -list %q %s failed: %v: %s", itemID, selector, pkg, err, compactCommandOutput(output))
	}
	for _, line := range strings.Split(string(output), "\n") {
		if strings.TrimSpace(line) == testName {
			return
		}
	}
	t.Fatalf("%s go test -list %q %s output missing %s: %s", itemID, selector, pkg, testName, compactCommandOutput(output))
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
		body = appendReleaseEvidenceItem(body, `"id":"`+gap.id+`","claim_id":"`+gap.claimID+`","subclaim_id":"seed_gap_open","acceptance_id":"P0_SEED_GAP_OPEN","risk_id":"`+gap.riskID+`","fixture_id":"","capability_id":"","evidence_profile":"default","default_mode":true,"fixture_enabled_mode":false,"expected_runtime":"fast","scope":"doc-guard","negative_or_positive":"both","evidence_type":"doc-guard","required":false,"command":[],"anchors":["docs/GA_NEXT_PHASE_DEVELOPMENT_HANDOFF_PLAN.md"],"doc_only_allowed":true,"optional_gated":false,"default_ga_required":false,"pass_criteria":{"kind":"seed_gap","assertions":["open"]}`)
	}
	return body
}

var package0SeedGapFixtureMetadata = []struct {
	id      string
	claimID string
	riskID  string
}{
	{"seed_gap_admin_bootstrap_ready_open", "CLAIM_ADMIN_BOOTSTRAP_READY", "F3"},
	{"seed_gap_default_user_loop_open", "CLAIM_DEFAULT_USER_LOOP", "F2"},
	{"seed_gap_workload_fixture_ready_open", "CLAIM_WORKLOAD_FIXTURE_READY", "F9"},
	{"seed_gap_operator_repair_safe_open", "CLAIM_OPERATOR_REPAIR_SAFE", "F11"},
	{"seed_gap_purge_approval_safe_open", "CLAIM_PURGE_APPROVAL_SAFE", "F13"},
	{"seed_gap_restore_reconciliation_open", "CLAIM_RESTORE_RECONCILIATION", "F14"},
	{"seed_gap_residual_risk_catalog_open", "CLAIM_RESIDUAL_RISK_CATALOG", "F12"},
	{"seed_gap_deployment_risk_envelope_open", "CLAIM_DEPLOYMENT_RISK_ENVELOPE", "F17"},
	{"seed_gap_profile_boundary_open", "CLAIM_PROFILE_BOUNDARY", "F1"},
	{"seed_gap_discovery_surfaces_open", "CLAIM_DISCOVERY_SURFACES", "F7"},
	{"seed_gap_webdav_default_access_open", "CLAIM_WEBDAV_DEFAULT_ACCESS", "F8"},
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
	{"repo_create_jvs_runtime_unavailable_recovery_unit", "CLAIM_OPERATION_TERMINALIZATION", "repo_create_jvs_runtime_unavailable_recovery", "P1_OPERATION_TERMINALIZATION_REPO_CREATE_JVS_RUNTIME_UNAVAILABLE_RECOVERY", "F6", "", "default", "true", "false", "fast", "package", "negative", "true", "denial_safety", "repo_create enabled recovery terminalizes when production JVS runtime is unavailable and fail-fast boundaries hold"},
	{"default_ga_capability_classification_unit", "CLAIM_CAPABILITY_MATRIX_CONSISTENT", "default_ga_capability_classification", "P0_CAPABILITY_MATRIX_DEFAULT_CLASSIFICATION", "F4", "", "default", "true", "false", "fast", "package", "both", "false", "coverage_guard", "capability matrix classifies default and optional capabilities consistently"},
	{"capability_admission_operation_coverage_unit", "CLAIM_CAPABILITY_MATRIX_CONSISTENT", "capability_admission_operation_coverage", "P0_CAPABILITY_MATRIX_OPERATION_COVERAGE", "F4", "", "default", "true", "false", "fast", "package", "both", "false", "coverage_guard", "capability admission operation coverage stays consistent"},
	{"capability_matrix_v1_contract_unit", "CLAIM_CAPABILITY_MATRIX_CONSISTENT", "capability_matrix_v1_contract", "P1_CAPABILITY_MATRIX_V1_CONTRACT", "F4", "", "default", "true", "false", "fast", "package", "both", "false", "coverage_guard", "capability matrix v1 contract covers readyz workload split vocabulary"},
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
	return strings.Replace(body, "\n  ]\n}", ",\n    {"+item+"}\n  ]\n}", 1)
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
