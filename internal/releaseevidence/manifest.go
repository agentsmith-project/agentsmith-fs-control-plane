package releaseevidence

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	SchemaVersion = "1"
	ReleaseGate   = "scripts/verify-ga-release.sh"
)

type Manifest struct {
	SchemaVersion string `json:"schema_version"`
	ReleaseGate   string `json:"release_gate"`
	Items         []Item `json:"items"`
}

type Item struct {
	ID                string   `json:"id"`
	CapabilityID      string   `json:"capability_id"`
	EvidenceType      string   `json:"evidence_type"`
	Required          bool     `json:"required"`
	Command           []string `json:"command"`
	Anchors           []string `json:"anchors"`
	DocOnlyAllowed    bool     `json:"doc_only_allowed"`
	OptionalGated     bool     `json:"optional_gated"`
	DefaultGARequired bool     `json:"default_ga_required"`
}

type Finding struct {
	ItemID  string
	Code    string
	Message string
}

func (finding Finding) String() string {
	if finding.ItemID == "" {
		return fmt.Sprintf("%s: %s", finding.Code, finding.Message)
	}
	return fmt.Sprintf("%s: %s: %s", finding.ItemID, finding.Code, finding.Message)
}

type Options struct {
	RepoRoot        string
	ExecuteRequired bool
	Stdout          io.Writer
	Stderr          io.Writer
}

func VerifyFile(path string, options Options) ([]Finding, error) {
	_, findings, err := LoadAndValidateFile(path, options)
	if err != nil || len(findings) > 0 || !options.ExecuteRequired {
		return findings, err
	}
	return executeRequiredCommands(path, options)
}

func LoadAndValidateFile(path string, options Options) (Manifest, []Finding, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, nil, fmt.Errorf("read manifest %s: %w", path, err)
	}

	manifest, findings, err := decodeManifest(body)
	if err != nil {
		return Manifest{}, nil, err
	}
	findings = append(findings, validateManifest(manifest, repoRootOrDefault(options.RepoRoot))...)
	return manifest, findings, nil
}

func (manifest Manifest) OptionalDisabledAdmissionEvidence(capabilityID string) (Item, bool) {
	for _, item := range manifest.Items {
		if item.CapabilityID == capabilityID &&
			item.Required &&
			item.EvidenceType == "unit" &&
			!item.DocOnlyAllowed &&
			item.OptionalGated &&
			strings.Contains(item.ID, "disabled_admission") {
			return item, true
		}
	}
	return Item{}, false
}

func (manifest Manifest) DisabledWorkerRecoveryEvidence(capabilityID string) (Item, bool) {
	for _, item := range manifest.Items {
		if item.CapabilityID == capabilityID &&
			item.Required &&
			item.EvidenceType == "unit" &&
			!item.DocOnlyAllowed &&
			item.OptionalGated &&
			strings.Contains(item.ID, "disabled_worker_recovery") {
			return item, true
		}
	}
	return Item{}, false
}

type rawManifest struct {
	SchemaVersion *string   `json:"schema_version"`
	ReleaseGate   *string   `json:"release_gate"`
	Items         []rawItem `json:"items"`
}

type rawItem struct {
	ID                *string  `json:"id"`
	CapabilityID      *string  `json:"capability_id"`
	EvidenceType      *string  `json:"evidence_type"`
	Required          *bool    `json:"required"`
	Command           []string `json:"command"`
	Anchors           []string `json:"anchors"`
	DocOnlyAllowed    *bool    `json:"doc_only_allowed"`
	OptionalGated     *bool    `json:"optional_gated"`
	DefaultGARequired *bool    `json:"default_ga_required"`
}

func decodeManifest(body []byte) (Manifest, []Finding, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()

	var raw rawManifest
	if err := decoder.Decode(&raw); err != nil {
		return Manifest{}, nil, fmt.Errorf("decode manifest: %w", err)
	}

	var findings []Finding
	manifest := Manifest{Items: make([]Item, 0, len(raw.Items))}
	if raw.SchemaVersion == nil {
		findings = append(findings, Finding{Code: "manifest.schema_version_missing", Message: "schema_version is required"})
	} else {
		manifest.SchemaVersion = *raw.SchemaVersion
	}
	if raw.ReleaseGate == nil {
		findings = append(findings, Finding{Code: "manifest.release_gate_missing", Message: "release_gate is required"})
	} else {
		manifest.ReleaseGate = *raw.ReleaseGate
	}

	for i, rawItem := range raw.Items {
		item, itemFindings := decodeItem(i, rawItem)
		findings = append(findings, itemFindings...)
		manifest.Items = append(manifest.Items, item)
	}
	return manifest, findings, nil
}

func decodeItem(index int, raw rawItem) (Item, []Finding) {
	var findings []Finding
	item := Item{Command: append([]string(nil), raw.Command...), Anchors: append([]string(nil), raw.Anchors...)}
	itemID := fmt.Sprintf("items[%d]", index)

	if raw.ID == nil || strings.TrimSpace(*raw.ID) == "" {
		findings = append(findings, Finding{ItemID: itemID, Code: "item.id_missing", Message: "id is required"})
	} else {
		item.ID = *raw.ID
		itemID = item.ID
	}
	if raw.CapabilityID == nil {
		findings = append(findings, Finding{ItemID: itemID, Code: "item.capability_id_missing", Message: "capability_id is required; use an empty string for workflow/schema evidence"})
	} else {
		item.CapabilityID = *raw.CapabilityID
	}
	if raw.EvidenceType == nil || strings.TrimSpace(*raw.EvidenceType) == "" {
		findings = append(findings, Finding{ItemID: itemID, Code: "item.evidence_type_missing", Message: "evidence_type is required"})
	} else {
		item.EvidenceType = *raw.EvidenceType
	}
	if raw.Required == nil {
		findings = append(findings, Finding{ItemID: itemID, Code: "item.required_missing", Message: "required is required"})
	} else {
		item.Required = *raw.Required
	}
	if raw.DocOnlyAllowed == nil {
		findings = append(findings, Finding{ItemID: itemID, Code: "item.doc_only_allowed_missing", Message: "doc_only_allowed is required"})
	} else {
		item.DocOnlyAllowed = *raw.DocOnlyAllowed
	}
	if raw.OptionalGated == nil {
		findings = append(findings, Finding{ItemID: itemID, Code: "item.optional_gated_missing", Message: "optional_gated is required"})
	} else {
		item.OptionalGated = *raw.OptionalGated
	}
	if raw.DefaultGARequired == nil {
		findings = append(findings, Finding{ItemID: itemID, Code: "item.default_ga_required_missing", Message: "default_ga_required is required"})
	} else {
		item.DefaultGARequired = *raw.DefaultGARequired
	}
	if raw.Command == nil {
		findings = append(findings, Finding{ItemID: itemID, Code: "item.command_missing", Message: "command is required"})
	}
	if raw.Anchors == nil {
		findings = append(findings, Finding{ItemID: itemID, Code: "item.anchors_missing", Message: "anchors is required"})
	}
	return item, findings
}

func validateManifest(manifest Manifest, repoRoot string) []Finding {
	var findings []Finding
	if manifest.SchemaVersion != "" && manifest.SchemaVersion != SchemaVersion {
		findings = append(findings, Finding{Code: "manifest.schema_version_invalid", Message: fmt.Sprintf("schema_version must be %q", SchemaVersion)})
	}
	if manifest.ReleaseGate != "" && manifest.ReleaseGate != ReleaseGate {
		findings = append(findings, Finding{Code: "manifest.release_gate_invalid", Message: fmt.Sprintf("release_gate must be %q", ReleaseGate)})
	}
	if len(manifest.Items) == 0 {
		findings = append(findings, Finding{Code: "manifest.items_missing", Message: "items must not be empty"})
	}

	for _, item := range manifest.Items {
		findings = append(findings, validateItem(item, repoRoot)...)
	}
	findings = append(findings, validateRequiredEvidenceItems(manifest)...)
	return findings
}

type requiredEvidenceSpec struct {
	ID                string
	CapabilityID      string
	EvidenceType      string
	Required          bool
	DocOnlyAllowed    bool
	OptionalGated     bool
	DefaultGARequired bool
}

func validateRequiredEvidenceItems(manifest Manifest) []Finding {
	itemsByID := make(map[string]Item, len(manifest.Items))
	for _, item := range manifest.Items {
		itemsByID[item.ID] = item
	}

	var findings []Finding
	for _, spec := range requiredEvidenceSpecs {
		item, ok := itemsByID[spec.ID]
		if !ok {
			findings = append(findings, Finding{Code: "manifest.required_evidence_missing", Message: fmt.Sprintf("missing exact required evidence item %s", spec.ID)})
			continue
		}
		if item.CapabilityID != spec.CapabilityID ||
			item.EvidenceType != spec.EvidenceType ||
			item.Required != spec.Required ||
			item.DocOnlyAllowed != spec.DocOnlyAllowed ||
			item.OptionalGated != spec.OptionalGated ||
			item.DefaultGARequired != spec.DefaultGARequired {
			findings = append(findings, Finding{ItemID: item.ID, Code: "manifest.required_evidence_metadata_invalid", Message: fmt.Sprintf("required evidence item %s metadata does not match release contract", spec.ID)})
		}
	}
	return findings
}

var requiredEvidenceSpecs = []requiredEvidenceSpec{
	{ID: "webdav_export_disabled_admission_unit", CapabilityID: "webdav_export", EvidenceType: "unit", Required: true, DocOnlyAllowed: false, OptionalGated: false, DefaultGARequired: true},
	{ID: "repo_lifecycle_retained_positive_unit", CapabilityID: "repo_lifecycle_retained", EvidenceType: "unit", Required: true, DocOnlyAllowed: false, OptionalGated: false, DefaultGARequired: true},
	{ID: "workload_mount_disabled_admission_unit", CapabilityID: "workload_mount", EvidenceType: "unit", Required: true, DocOnlyAllowed: false, OptionalGated: true, DefaultGARequired: false},
	{ID: "workload_mount_plan_store_freshness_unit", CapabilityID: "workload_mount", EvidenceType: "unit", Required: true, DocOnlyAllowed: false, OptionalGated: true, DefaultGARequired: false},
	{ID: "workload_mount_runtime_secretref_config_unit", CapabilityID: "workload_mount", EvidenceType: "unit", Required: true, DocOnlyAllowed: false, OptionalGated: true, DefaultGARequired: false},
	{ID: "workload_mount_secretref_redaction_unit", CapabilityID: "workload_mount", EvidenceType: "unit", Required: true, DocOnlyAllowed: false, OptionalGated: true, DefaultGARequired: false},
	{ID: "repo_template_disabled_admission_unit", CapabilityID: "repo_template", EvidenceType: "unit", Required: true, DocOnlyAllowed: false, OptionalGated: true, DefaultGARequired: false},
	{ID: "repo_purge_disabled_admission_unit", CapabilityID: "repo_purge", EvidenceType: "unit", Required: true, DocOnlyAllowed: false, OptionalGated: true, DefaultGARequired: false},
	{ID: "repo_template_create_disabled_worker_recovery_unit", CapabilityID: "repo_template", EvidenceType: "unit", Required: true, DocOnlyAllowed: false, OptionalGated: true, DefaultGARequired: false},
	{ID: "repo_template_clone_disabled_worker_recovery_unit", CapabilityID: "repo_template", EvidenceType: "unit", Required: true, DocOnlyAllowed: false, OptionalGated: true, DefaultGARequired: false},
	{ID: "repo_purge_disabled_worker_recovery_unit", CapabilityID: "repo_purge", EvidenceType: "unit", Required: true, DocOnlyAllowed: false, OptionalGated: true, DefaultGARequired: false},
	{ID: "default_ga_capability_classification_unit", CapabilityID: "", EvidenceType: "unit", Required: true, DocOnlyAllowed: false, OptionalGated: false, DefaultGARequired: false},
	{ID: "capability_admission_operation_coverage_unit", CapabilityID: "", EvidenceType: "unit", Required: true, DocOnlyAllowed: false, OptionalGated: false, DefaultGARequired: false},
	{ID: "release_script_evidence_manifest_guard", CapabilityID: "", EvidenceType: "contract", Required: true, DocOnlyAllowed: false, OptionalGated: false, DefaultGARequired: false},
}

func validateItem(item Item, repoRoot string) []Finding {
	var findings []Finding
	if !validEvidenceTypes[item.EvidenceType] && item.EvidenceType != "" {
		findings = append(findings, Finding{ItemID: item.ID, Code: "item.evidence_type_invalid", Message: fmt.Sprintf("unsupported evidence_type %q", item.EvidenceType)})
	}
	if !validCapabilities[item.CapabilityID] {
		findings = append(findings, Finding{ItemID: item.ID, Code: "item.capability_id_invalid", Message: fmt.Sprintf("unsupported capability_id %q", item.CapabilityID)})
	}
	findings = append(findings, validateCapabilityClassification(item)...)
	findings = append(findings, validateAnchors(item, repoRoot)...)
	if item.Required || len(item.Command) > 0 {
		findings = append(findings, validateCommand(item, repoRoot)...)
	}
	if !item.DocOnlyAllowed {
		if item.EvidenceType == "doc-guard" {
			findings = append(findings, Finding{ItemID: item.ID, Code: "item.doc_only_invalid", Message: "doc_only_allowed=false cannot use evidence_type doc-guard only"})
		}
		if len(item.Anchors) > 0 && allDocAnchors(item.Anchors) {
			findings = append(findings, Finding{ItemID: item.ID, Code: "item.doc_only_invalid", Message: "doc_only_allowed=false requires at least one non-docs/ anchor"})
		}
	}
	return findings
}

func validateCapabilityClassification(item Item) []Finding {
	switch item.CapabilityID {
	case "storage", "jvs", "webdav_export", "repo_lifecycle_retained":
		if item.OptionalGated {
			return []Finding{{ItemID: item.ID, Code: "item.optional_gated_invalid", Message: fmt.Sprintf("%s is default GA required and cannot be optional_gated=true", item.CapabilityID)}}
		}
		if !item.DefaultGARequired {
			return []Finding{{ItemID: item.ID, Code: "item.default_ga_required_invalid", Message: fmt.Sprintf("%s must have default_ga_required=true", item.CapabilityID)}}
		}
	case "workload_mount", "repo_template", "repo_purge":
		if item.DefaultGARequired {
			return []Finding{{ItemID: item.ID, Code: "item.default_ga_required_invalid", Message: fmt.Sprintf("%s is optional-gated and cannot have default_ga_required=true", item.CapabilityID)}}
		}
	}
	return nil
}

func validateAnchors(item Item, repoRoot string) []Finding {
	if len(item.Anchors) == 0 {
		return []Finding{{ItemID: item.ID, Code: "item.anchors_empty", Message: "anchors must not be empty"}}
	}

	var findings []Finding
	for _, anchor := range item.Anchors {
		if unsafeRepoLocalToken(anchor) {
			findings = append(findings, Finding{ItemID: item.ID, Code: "item.anchor_not_repo_local", Message: fmt.Sprintf("anchor %q must be repo-local", anchor)})
			continue
		}
		path := strings.Split(anchor, "#")[0]
		if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(path))); err != nil {
			findings = append(findings, Finding{ItemID: item.ID, Code: "item.anchor_missing", Message: fmt.Sprintf("anchor %q must exist in the repo: %v", anchor, err)})
		}
	}
	return findings
}

func validateCommand(item Item, repoRoot string) []Finding {
	if len(item.Command) == 0 {
		return []Finding{{ItemID: item.ID, Code: "item.command_empty", Message: "item command must not be empty"}}
	}
	if item.Command[0] == "git" {
		return []Finding{{ItemID: item.ID, Code: "item.command_git_forbidden", Message: "git manifest commands are forbidden"}}
	}
	if !allowedExecutables[item.Command[0]] {
		return []Finding{{ItemID: item.ID, Code: "item.command_executable_invalid", Message: fmt.Sprintf("executable %q is not allowed", item.Command[0])}}
	}
	if _, err := exec.LookPath(item.Command[0]); err != nil {
		return []Finding{{ItemID: item.ID, Code: "item.command_executable_missing", Message: fmt.Sprintf("executable %q is not available: %v", item.Command[0], err)}}
	}
	for _, arg := range item.Command {
		if unsafeRepoLocalToken(arg) {
			return []Finding{{ItemID: item.ID, Code: "item.command_not_repo_local", Message: fmt.Sprintf("command argument %q is not repo-local and must not reference sibling repositories", arg)}}
		}
		lower := strings.ToLower(arg)
		switch {
		case strings.Contains(lower, "manual"):
			return []Finding{{ItemID: item.ID, Code: "item.command_manual_gate", Message: "commands must not depend on manual gates"}}
		case strings.Contains(lower, "signoff") || strings.Contains(lower, "sign-off"):
			return []Finding{{ItemID: item.ID, Code: "item.command_signoff_gate", Message: "commands must not depend on signoff gates"}}
		case strings.Contains(lower, "approval") && !strings.Contains(lower, "approval_reference"):
			return []Finding{{ItemID: item.ID, Code: "item.command_approval_gate", Message: "commands must not depend on approval gates"}}
		}
	}
	if finding, ok := validateRetainedLifecycleEvidenceScope(item); ok {
		return []Finding{finding}
	}
	return validateCommandTarget(item, repoRoot)
}

func validateRetainedLifecycleEvidenceScope(item Item) (Finding, bool) {
	if item.ID != "repo_lifecycle_retained_positive_unit" {
		return Finding{}, false
	}
	command := strings.ToLower(strings.Join(item.Command, " "))
	if strings.Contains(command, "purge") || strings.Contains(command, "repo_purge") {
		return Finding{ItemID: item.ID, Code: "item.command_retained_lifecycle_scope_invalid", Message: "retained lifecycle positive evidence command must not include purge or repo_purge selectors"}, true
	}
	return Finding{}, false
}

func validateCommandTarget(item Item, repoRoot string) []Finding {
	switch item.Command[0] {
	case "go":
		if len(item.Command) < 2 || item.Command[1] != "test" {
			return []Finding{{ItemID: item.ID, Code: "item.command_go_subcommand_invalid", Message: "go manifest commands must be repo-local go test evidence"}}
		}
		return validateGoTestTargets(item, repoRoot)
	case "bash":
		return validateBashScriptTarget(item, repoRoot)
	default:
		return nil
	}
}

func validateGoTestTargets(item Item, repoRoot string) []Finding {
	if len(item.Command) < 2 || item.Command[1] != "test" {
		return nil
	}

	var findings []Finding
	packages := goTestPackageArgs(item.Command)
	if len(packages) == 0 {
		return []Finding{{ItemID: item.ID, Code: "item.command_package_missing", Message: "go test evidence must include at least one explicit repo-local package target"}}
	}
	for _, arg := range packages {
		if arg == "./..." {
			continue
		}
		path := strings.TrimPrefix(arg, "./")
		path = strings.TrimSuffix(path, "/...")
		if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(path))); err != nil {
			findings = append(findings, Finding{ItemID: item.ID, Code: "item.command_package_missing", Message: fmt.Sprintf("go test package target %q is missing: %v", arg, err)})
		}
	}
	if len(findings) == 0 {
		findings = append(findings, validateGoTestRunSelector(item, repoRoot, packages)...)
	}
	return findings
}

func validateGoTestRunSelector(item Item, repoRoot string, packages []string) []Finding {
	runSelector, ok := goTestRunSelector(item.Command)
	if !ok || runSelector == "" || len(packages) == 0 {
		return nil
	}

	for _, pkg := range packages {
		listCommand := exec.Command("go", "test", "-list", runSelector, pkg)
		listCommand.Dir = repoRoot
		output, err := listCommand.CombinedOutput()
		if err != nil {
			return []Finding{{ItemID: item.ID, Code: "item.command_run_selector_invalid", Message: fmt.Sprintf("go test -list failed for package %q and -run selector %q: %v: %s", pkg, runSelector, err, compactCommandOutput(output))}}
		}
		if !goTestListOutputHasTest(output) {
			if goTestListOutputHasBenchmark(output) {
				return []Finding{{ItemID: item.ID, Code: "item.command_run_selector_benchmark_only", Message: fmt.Sprintf("go test -run selector %q only matches Benchmark targets in package %q; benchmark evidence is not supported", runSelector, pkg)}}
			}
			return []Finding{{ItemID: item.ID, Code: "item.command_run_selector_empty", Message: fmt.Sprintf("go test -run selector %q matches no tests in package %q", runSelector, pkg)}}
		}
	}
	return nil
}

func goTestPackageArgs(command []string) []string {
	var packages []string
	for index := 2; index < len(command); index++ {
		arg := command[index]
		if skipNextGoTestFlagValue(arg) && index+1 < len(command) {
			index++
			continue
		}
		if strings.HasPrefix(arg, "-") || !isRepoLocalGoPackageArg(arg) {
			continue
		}
		packages = append(packages, arg)
	}
	return packages
}

func goTestRunSelector(command []string) (string, bool) {
	for index := 2; index < len(command); index++ {
		arg := command[index]
		if arg == "-run" && index+1 < len(command) {
			return command[index+1], true
		}
		if strings.HasPrefix(arg, "-run=") {
			return strings.TrimPrefix(arg, "-run="), true
		}
	}
	return "", false
}

func skipNextGoTestFlagValue(arg string) bool {
	switch arg {
	case "-run", "-count", "-timeout", "-list":
		return true
	default:
		return false
	}
}

func goTestListOutputHasTest(output []byte) bool {
	for _, line := range strings.Split(string(output), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Test") || strings.HasPrefix(trimmed, "Example") {
			return true
		}
	}
	return false
}

func goTestListOutputHasBenchmark(output []byte) bool {
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Benchmark") {
			return true
		}
	}
	return false
}

func compactCommandOutput(output []byte) string {
	return strings.TrimSpace(strings.Join(strings.Fields(string(output)), " "))
}

func validateBashScriptTarget(item Item, repoRoot string) []Finding {
	for _, arg := range item.Command[1:] {
		if bashInlineCommandArg(arg) {
			return []Finding{{ItemID: item.ID, Code: "item.command_bash_inline_forbidden", Message: "bash inline command forms are forbidden"}}
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if !strings.HasSuffix(arg, ".sh") && !strings.HasPrefix(arg, "scripts/") {
			return []Finding{{ItemID: item.ID, Code: "item.command_script_missing", Message: fmt.Sprintf("bash evidence must target a repo-local script, got %q", arg)}}
		}
		if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(arg))); err != nil {
			return []Finding{{ItemID: item.ID, Code: "item.command_script_missing", Message: fmt.Sprintf("bash script target %q is missing: %v", arg, err)}}
		}
		return nil
	}
	return []Finding{{ItemID: item.ID, Code: "item.command_script_missing", Message: "bash evidence must include an explicit repo-local script target"}}
}

func bashInlineCommandArg(arg string) bool {
	if arg == "--command" || strings.HasPrefix(arg, "--command=") {
		return true
	}
	if !strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "--") {
		return false
	}
	return strings.Contains(strings.TrimPrefix(arg, "-"), "c")
}

func isRepoLocalGoPackageArg(arg string) bool {
	return arg == "./..." ||
		strings.HasPrefix(arg, "./internal/") ||
		strings.HasPrefix(arg, "./cmd/")
}

func executeRequiredCommands(path string, options Options) ([]Finding, error) {
	manifest, findings, err := LoadAndValidateFile(path, options)
	if err != nil || len(findings) > 0 {
		return findings, err
	}

	repoRoot := repoRootOrDefault(options.RepoRoot)
	for _, item := range manifest.Items {
		if !item.Required {
			continue
		}
		if options.Stdout != nil {
			fmt.Fprintf(options.Stdout, "+ %s\n", strings.Join(item.Command, " "))
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		cmd := exec.CommandContext(ctx, item.Command[0], item.Command[1:]...)
		cmd.Dir = repoRoot
		cmd.Stdout = options.Stdout
		cmd.Stderr = options.Stderr
		err := cmd.Run()
		cancel()
		if ctx.Err() == context.DeadlineExceeded {
			findings = append(findings, Finding{ItemID: item.ID, Code: "item.command_failed", Message: "command timed out"})
			continue
		}
		if err != nil {
			findings = append(findings, Finding{ItemID: item.ID, Code: "item.command_failed", Message: fmt.Sprintf("command failed: %v", err)})
		}
	}
	return findings, nil
}

func repoRootOrDefault(repoRoot string) string {
	if repoRoot != "" {
		return repoRoot
	}
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

func allDocAnchors(anchors []string) bool {
	for _, anchor := range anchors {
		if !strings.HasPrefix(anchor, "docs/") {
			return false
		}
	}
	return true
}

func unsafeRepoLocalToken(value string) bool {
	if value == "" {
		return true
	}
	if filepath.IsAbs(value) || strings.Contains(value, "../") || strings.Contains(value, `..\`) || strings.HasPrefix(value, "..") {
		return true
	}
	lower := strings.ToLower(value)
	return strings.Contains(lower, "mbos-sandbox") || strings.Contains(lower, "improve-agentsmith")
}

var validEvidenceTypes = map[string]bool{
	"unit":             true,
	"contract":         true,
	"schema":           true,
	"openapi":          true,
	"generated-client": true,
	"integration":      true,
	"e2e":              true,
	"provenance":       true,
	"race":             true,
	"doc-guard":        true,
}

var validCapabilities = map[string]bool{
	"":                        true,
	"storage":                 true,
	"jvs":                     true,
	"webdav_export":           true,
	"repo_lifecycle_retained": true,
	"workload_mount":          true,
	"repo_template":           true,
	"repo_purge":              true,
}

var allowedExecutables = map[string]bool{
	"go":   true,
	"bash": true,
}
