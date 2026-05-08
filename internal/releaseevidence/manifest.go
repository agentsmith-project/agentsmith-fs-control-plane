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

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/capability"
)

const (
	SchemaVersion                    = "2"
	ReleaseGate                      = "scripts/verify-ga-release.sh"
	AuthoritativeReleaseSelectorPath = "docs/release-evidence/ga-release-selector.json"

	ManifestModeSeed  = "seed"
	ManifestModeFinal = "final"
)

type Manifest struct {
	SchemaVersion string `json:"schema_version"`
	ReleaseGate   string `json:"release_gate"`
	Items         []Item `json:"items"`
}

type Item struct {
	ID                 string       `json:"id"`
	ClaimID            string       `json:"claim_id"`
	SubclaimID         string       `json:"subclaim_id"`
	AcceptanceID       string       `json:"acceptance_id"`
	RiskID             string       `json:"risk_id"`
	FixtureID          string       `json:"fixture_id"`
	CapabilityID       string       `json:"capability_id"`
	EvidenceStatus     string       `json:"evidence_status"`
	EvidenceProfile    string       `json:"evidence_profile"`
	DefaultMode        bool         `json:"default_mode"`
	FixtureEnabledMode bool         `json:"fixture_enabled_mode"`
	ExpectedRuntime    string       `json:"expected_runtime"`
	Scope              string       `json:"scope"`
	NegativeOrPositive string       `json:"negative_or_positive"`
	EvidenceType       string       `json:"evidence_type"`
	Required           bool         `json:"required"`
	Command            []string     `json:"command"`
	Anchors            []string     `json:"anchors"`
	DocOnlyAllowed     bool         `json:"doc_only_allowed"`
	OptionalGated      bool         `json:"optional_gated"`
	DefaultGARequired  bool         `json:"default_ga_required"`
	PassCriteria       PassCriteria `json:"pass_criteria"`
}

type PassCriteria struct {
	Kind       string   `json:"kind"`
	Assertions []string `json:"assertions"`
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
	Mode            string
	SelectorPath    string
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
	mode, modeFindings := manifestMode(options.Mode)
	findings = append(findings, modeFindings...)
	if len(modeFindings) > 0 {
		return manifest, findings, nil
	}
	repoRoot := repoRootOrDefault(options.RepoRoot)
	selector, selectorFindings, err := loadReleaseSelectorForMode(path, options, mode, repoRoot)
	if err != nil {
		return manifest, findings, err
	}
	findings = append(findings, selectorFindings...)
	findings = append(findings, validateManifest(manifest, repoRoot, mode, selector)...)
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
	ID                 *string       `json:"id"`
	ClaimID            *string       `json:"claim_id"`
	SubclaimID         *string       `json:"subclaim_id"`
	AcceptanceID       *string       `json:"acceptance_id"`
	RiskID             *string       `json:"risk_id"`
	FixtureID          *string       `json:"fixture_id"`
	CapabilityID       *string       `json:"capability_id"`
	EvidenceStatus     *string       `json:"evidence_status"`
	EvidenceProfile    *string       `json:"evidence_profile"`
	DefaultMode        *bool         `json:"default_mode"`
	FixtureEnabledMode *bool         `json:"fixture_enabled_mode"`
	ExpectedRuntime    *string       `json:"expected_runtime"`
	Scope              *string       `json:"scope"`
	NegativeOrPositive *string       `json:"negative_or_positive"`
	EvidenceType       *string       `json:"evidence_type"`
	Required           *bool         `json:"required"`
	Command            []string      `json:"command"`
	Anchors            []string      `json:"anchors"`
	DocOnlyAllowed     *bool         `json:"doc_only_allowed"`
	OptionalGated      *bool         `json:"optional_gated"`
	DefaultGARequired  *bool         `json:"default_ga_required"`
	PassCriteria       *PassCriteria `json:"pass_criteria"`
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
	if raw.ClaimID == nil || strings.TrimSpace(*raw.ClaimID) == "" {
		findings = append(findings, Finding{ItemID: itemID, Code: "item.claim_id_missing", Message: "claim_id is required"})
	} else {
		item.ClaimID = *raw.ClaimID
	}
	if raw.SubclaimID == nil || strings.TrimSpace(*raw.SubclaimID) == "" {
		findings = append(findings, Finding{ItemID: itemID, Code: "item.subclaim_id_missing", Message: "subclaim_id is required"})
	} else {
		item.SubclaimID = *raw.SubclaimID
	}
	if raw.AcceptanceID == nil || strings.TrimSpace(*raw.AcceptanceID) == "" {
		findings = append(findings, Finding{ItemID: itemID, Code: "item.acceptance_id_missing", Message: "acceptance_id is required"})
	} else {
		item.AcceptanceID = *raw.AcceptanceID
	}
	if raw.RiskID != nil {
		item.RiskID = *raw.RiskID
	}
	if raw.FixtureID != nil {
		item.FixtureID = *raw.FixtureID
	}
	if raw.CapabilityID == nil {
		findings = append(findings, Finding{ItemID: itemID, Code: "item.capability_id_missing", Message: "capability_id is required; use an empty string for workflow/schema evidence"})
	} else {
		item.CapabilityID = *raw.CapabilityID
	}
	if raw.EvidenceStatus == nil || strings.TrimSpace(*raw.EvidenceStatus) == "" {
		findings = append(findings, Finding{ItemID: itemID, Code: "item.evidence_status_missing", Message: "evidence_status is required"})
	} else {
		item.EvidenceStatus = *raw.EvidenceStatus
	}
	if raw.EvidenceProfile == nil || strings.TrimSpace(*raw.EvidenceProfile) == "" {
		findings = append(findings, Finding{ItemID: itemID, Code: "item.evidence_profile_missing", Message: "evidence_profile is required"})
	} else {
		item.EvidenceProfile = *raw.EvidenceProfile
	}
	if raw.DefaultMode != nil {
		item.DefaultMode = *raw.DefaultMode
	}
	if raw.FixtureEnabledMode != nil {
		item.FixtureEnabledMode = *raw.FixtureEnabledMode
	}
	if raw.ExpectedRuntime == nil || strings.TrimSpace(*raw.ExpectedRuntime) == "" {
		findings = append(findings, Finding{ItemID: itemID, Code: "item.expected_runtime_missing", Message: "expected_runtime is required"})
	} else {
		item.ExpectedRuntime = *raw.ExpectedRuntime
	}
	if raw.Scope == nil || strings.TrimSpace(*raw.Scope) == "" {
		findings = append(findings, Finding{ItemID: itemID, Code: "item.scope_missing", Message: "scope is required"})
	} else {
		item.Scope = *raw.Scope
	}
	if raw.NegativeOrPositive == nil || strings.TrimSpace(*raw.NegativeOrPositive) == "" {
		findings = append(findings, Finding{ItemID: itemID, Code: "item.negative_or_positive_missing", Message: "negative_or_positive is required"})
	} else {
		item.NegativeOrPositive = *raw.NegativeOrPositive
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
	if raw.Required != nil && *raw.Required && raw.DefaultMode == nil {
		findings = append(findings, Finding{ItemID: itemID, Code: "item.default_mode_missing", Message: "default_mode must be explicit for required evidence"})
	}
	if raw.Required != nil && *raw.Required && raw.FixtureEnabledMode == nil {
		findings = append(findings, Finding{ItemID: itemID, Code: "item.fixture_enabled_mode_missing", Message: "fixture_enabled_mode must be explicit for required evidence"})
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
	if raw.PassCriteria == nil {
		findings = append(findings, Finding{ItemID: itemID, Code: "item.pass_criteria_missing", Message: "pass_criteria is required"})
	} else {
		item.PassCriteria = PassCriteria{
			Kind:       raw.PassCriteria.Kind,
			Assertions: append([]string(nil), raw.PassCriteria.Assertions...),
		}
	}
	return item, findings
}

func validateManifest(manifest Manifest, repoRoot, mode string, selector *ReleaseSelector) []Finding {
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
		findings = append(findings, validateItem(item, repoRoot, mode)...)
	}
	findings = append(findings, validateRequiredEvidenceItems(manifest, mode, selector)...)
	return findings
}

type requiredEvidenceSpec struct {
	ID                 string
	ClaimID            string
	SubclaimID         string
	AcceptanceID       string
	RiskID             string
	EvidenceProfile    string
	DefaultMode        bool
	FixtureEnabledMode bool
	ExpectedRuntime    string
	Scope              string
	NegativeOrPositive string
	PassCriteriaKind   string
	CapabilityID       string
	EvidenceType       string
	Required           bool
	DocOnlyAllowed     bool
	OptionalGated      bool
	DefaultGARequired  bool
}

func validateRequiredEvidenceItems(manifest Manifest, mode string, selector *ReleaseSelector) []Finding {
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
			item.ClaimID != spec.ClaimID ||
			item.SubclaimID != spec.SubclaimID ||
			item.AcceptanceID != spec.AcceptanceID ||
			item.RiskID != spec.RiskID ||
			item.EvidenceProfile != spec.EvidenceProfile ||
			item.DefaultMode != spec.DefaultMode ||
			item.FixtureEnabledMode != spec.FixtureEnabledMode ||
			item.ExpectedRuntime != spec.ExpectedRuntime ||
			item.Scope != spec.Scope ||
			item.NegativeOrPositive != spec.NegativeOrPositive ||
			item.PassCriteria.Kind != spec.PassCriteriaKind ||
			item.EvidenceType != spec.EvidenceType ||
			item.Required != spec.Required ||
			item.DocOnlyAllowed != spec.DocOnlyAllowed ||
			item.OptionalGated != spec.OptionalGated ||
			item.DefaultGARequired != spec.DefaultGARequired {
			findings = append(findings, Finding{ItemID: item.ID, Code: "manifest.required_evidence_metadata_invalid", Message: fmt.Sprintf("required evidence item %s metadata does not match release contract", spec.ID)})
		}
	}
	findings = append(findings, validateRequiredClaimSubclaimCoverage(manifest)...)
	if mode == ManifestModeFinal {
		findings = append(findings, validateFinalSeedGaps(manifest, selector)...)
		findings = append(findings, validateFinalRequiredClaimReplacements(manifest, selector)...)
	} else {
		findings = append(findings, validateSeedGapMarkers(itemsByID)...)
	}
	return findings
}

type requiredClaimSubclaimSpec struct {
	ClaimID    string
	SubclaimID string
}

func validateRequiredClaimSubclaimCoverage(manifest Manifest) []Finding {
	requiredCoverage := make(map[requiredClaimSubclaimSpec]bool, len(manifest.Items))
	for _, item := range manifest.Items {
		if !item.Required {
			continue
		}
		requiredCoverage[requiredClaimSubclaimSpec{ClaimID: item.ClaimID, SubclaimID: item.SubclaimID}] = true
	}

	var findings []Finding
	for _, spec := range requiredClaimSubclaimSpecs {
		if !requiredCoverage[spec] {
			findings = append(findings, Finding{
				Code:    "manifest.required_claim_subclaim_missing",
				Message: fmt.Sprintf("missing required seed claim/subclaim coverage %s/%s", spec.ClaimID, spec.SubclaimID),
			})
		}
	}
	return findings
}

type seedGapSpec struct {
	ID                      string
	ClaimID                 string
	RiskID                  string
	FinalSubclaimID         string
	FinalAcceptanceID       string
	FinalCapabilityID       string
	FinalEvidenceProfile    string
	FinalDefaultMode        bool
	FinalFixtureEnabledMode bool
	FinalExpectedRuntime    string
	FinalScope              string
	FinalNegativeOrPositive string
	FinalPassCriteriaKind   string
	FinalPassCriteriaAssert string
	FinalOptionalGated      bool
	FinalDefaultGARequired  bool
}

func validateSeedGapMarkers(itemsByID map[string]Item) []Finding {
	var findings []Finding
	for _, spec := range seedGapSpecs {
		item, ok := itemsByID[spec.ID]
		hasClosedReplacement := false
		if replacement, replacementOK := itemsByID[finalReplacementIDForSeedGap(spec)]; replacementOK {
			hasClosedReplacement = finalClaimReplacementEvidenceMatchesSeedGap(replacement, spec)
		}
		switch {
		case ok && hasClosedReplacement:
			findings = append(findings, Finding{ItemID: spec.ID, Code: "manifest.seed_gap_state_conflict", Message: fmt.Sprintf("seed gap %s for claim %s cannot be both open marker and closed replacement", spec.ID, spec.ClaimID)})
		case !ok && !hasClosedReplacement:
			findings = append(findings, Finding{Code: "manifest.seed_gap_state_missing", Message: fmt.Sprintf("seed gap %s for claim %s must have an open marker or exact implemented/closed replacement evidence", spec.ID, spec.ClaimID)})
		case !ok && hasClosedReplacement:
			continue
		}
		if !ok {
			continue
		}
		if item.ClaimID != spec.ClaimID ||
			item.SubclaimID != "seed_gap_open" ||
			item.AcceptanceID != "P0_SEED_GAP_OPEN" ||
			item.RiskID != spec.RiskID ||
			item.EvidenceProfile != "default" ||
			!item.DefaultMode ||
			item.FixtureEnabledMode ||
			item.ExpectedRuntime != "fast" ||
			item.Scope != "doc-guard" ||
			item.NegativeOrPositive != "both" ||
			item.CapabilityID != "" ||
			item.EvidenceType != "doc-guard" ||
			item.Required ||
			len(item.Command) != 0 ||
			!item.DocOnlyAllowed ||
			item.OptionalGated ||
			item.DefaultGARequired ||
			item.PassCriteria.Kind != "seed_gap" ||
			item.EvidenceStatus != "placeholder" ||
			!containsString(item.PassCriteria.Assertions, "open") {
			findings = append(findings, Finding{ItemID: item.ID, Code: "manifest.seed_gap_marker_invalid", Message: fmt.Sprintf("seed gap marker for %s must remain non-required, command-empty, doc-only, placeholder, and marked open", spec.ClaimID)})
		}
	}
	return findings
}

func validateFinalSeedGaps(manifest Manifest, selector *ReleaseSelector) []Finding {
	finalRequiredSeedGapIDs := make(map[string]bool)
	for _, spec := range finalRequiredSeedGapSpecs(selector) {
		finalRequiredSeedGapIDs[spec.ID] = true
	}

	var findings []Finding
	for _, item := range manifest.Items {
		if !isOpenSeedGap(item) || !finalRequiredSeedGapIDs[item.ID] {
			continue
		}
		findings = append(findings, Finding{
			ItemID:  item.ID,
			Code:    "manifest.final_seed_gap_open",
			Message: fmt.Sprintf("final manifest cannot keep open seed gap %s for claim %s", item.ID, item.ClaimID),
		})
	}
	return findings
}

func validateFinalRequiredClaimReplacements(manifest Manifest, selector *ReleaseSelector) []Finding {
	var findings []Finding
	for _, spec := range finalRequiredSeedGapSpecs(selector) {
		findings = append(findings, validateFinalReplacementStatus(manifest, spec)...)
		if manifestHasFinalReplacementForSeedGap(manifest, spec) {
			continue
		}
		findings = append(findings, Finding{
			Code:    "manifest.final_required_claim_missing",
			Message: fmt.Sprintf("final manifest requires replacement evidence for seed gap %s expected item %s claim %s subclaim %s acceptance %s risk %s", spec.ID, finalReplacementIDForSeedGap(spec), spec.ClaimID, spec.FinalSubclaimID, spec.FinalAcceptanceID, spec.RiskID),
		})
	}
	return findings
}

func validateFinalReplacementStatus(manifest Manifest, spec seedGapSpec) []Finding {
	var findings []Finding
	for _, item := range manifest.Items {
		if item.ID != finalReplacementIDForSeedGap(spec) {
			continue
		}
		if item.EvidenceStatus != "implemented" && item.EvidenceStatus != "closed" {
			findings = append(findings, Finding{ItemID: item.ID, Code: "manifest.final_replacement_status_invalid", Message: "final replacement evidence_status must be implemented or closed"})
		}
	}
	return findings
}

func finalRequiredSeedGapSpecs(selector *ReleaseSelector) []seedGapSpec {
	specs := make([]seedGapSpec, 0, len(seedGapSpecs))
	seen := make(map[string]bool)
	for _, spec := range seedGapSpecs {
		if seen[spec.ClaimID] {
			continue
		}
		if optionalFixtureFinalShape(spec) && !selectorSelectsFinalSeedGap(selector, spec) {
			continue
		}
		specs = append(specs, spec)
		seen[spec.ClaimID] = true
	}
	return specs
}

func selectorSelectsFinalSeedGap(selector *ReleaseSelector, spec seedGapSpec) bool {
	if selector == nil {
		return false
	}
	for _, capabilityID := range selector.ClaimedOptionalCapabilities {
		if capabilityID == spec.FinalCapabilityID {
			return true
		}
	}
	return false
}

func optionalFixtureFinalShape(spec seedGapSpec) bool {
	return spec.FinalEvidenceProfile == "repo-local-fixture-enabled" &&
		!spec.FinalDefaultMode &&
		spec.FinalFixtureEnabledMode &&
		spec.FinalOptionalGated &&
		!spec.FinalDefaultGARequired
}

func manifestHasFinalReplacementForSeedGap(manifest Manifest, spec seedGapSpec) bool {
	for _, item := range manifest.Items {
		if finalClaimReplacementEvidenceMatchesSeedGap(item, spec) {
			return true
		}
	}
	return false
}

func finalClaimReplacementEvidenceMatchesSeedGap(item Item, spec seedGapSpec) bool {
	return finalClaimReplacementEvidenceHasRepoLocalCommandAndAnchor(item) &&
		(item.EvidenceStatus == "implemented" || item.EvidenceStatus == "closed") &&
		item.ID == finalReplacementIDForSeedGap(spec) &&
		item.ClaimID == spec.ClaimID &&
		item.SubclaimID == spec.FinalSubclaimID &&
		item.AcceptanceID == spec.FinalAcceptanceID &&
		item.RiskID == spec.RiskID &&
		item.CapabilityID == spec.FinalCapabilityID &&
		item.EvidenceProfile == spec.FinalEvidenceProfile &&
		item.DefaultMode == spec.FinalDefaultMode &&
		item.FixtureEnabledMode == spec.FinalFixtureEnabledMode &&
		item.ExpectedRuntime == spec.FinalExpectedRuntime &&
		item.Scope == spec.FinalScope &&
		item.NegativeOrPositive == spec.FinalNegativeOrPositive &&
		item.PassCriteria.Kind == spec.FinalPassCriteriaKind &&
		containsString(item.PassCriteria.Assertions, spec.FinalPassCriteriaAssert) &&
		item.OptionalGated == spec.FinalOptionalGated &&
		item.DefaultGARequired == spec.FinalDefaultGARequired &&
		(!spec.FinalFixtureEnabledMode || strings.TrimSpace(item.FixtureID) != "")
}

func finalClaimReplacementEvidence(item Item) bool {
	return item.Required &&
		len(item.Command) > 0 &&
		!item.DocOnlyAllowed &&
		!isSeedGap(item)
}

func finalClaimReplacementEvidenceHasRepoLocalCommandAndAnchor(item Item) bool {
	return finalClaimReplacementEvidence(item) &&
		item.EvidenceType != "doc-guard" &&
		commandLooksRepoLocalEvidence(item.Command) &&
		anchorsIncludeRepoLocalRuntimeEvidence(item.Anchors)
}

func commandLooksRepoLocalEvidence(command []string) bool {
	if len(command) == 0 || !allowedExecutables[command[0]] {
		return false
	}
	for _, arg := range command {
		if unsafeRepoLocalToken(arg) {
			return false
		}
	}
	switch command[0] {
	case "go":
		return len(command) >= 2 && command[1] == "test" && len(goTestPackageArgs(command)) > 0
	case "bash":
		for _, arg := range command[1:] {
			if strings.HasPrefix(arg, "-") {
				continue
			}
			return (strings.HasPrefix(arg, "scripts/") || strings.HasSuffix(arg, ".sh")) && !docOnlyAnchor(arg)
		}
	}
	return false
}

func anchorsIncludeRepoLocalRuntimeEvidence(anchors []string) bool {
	for _, anchor := range anchors {
		if unsafeRepoLocalToken(anchor) {
			return false
		}
		if !docOnlyAnchor(anchor) {
			return true
		}
	}
	return false
}

func finalReplacementIDForSeedGap(spec seedGapSpec) string {
	return spec.FinalSubclaimID + "_unit"
}

func optionalPositiveFixtureEvidence(item Item) bool {
	return item.OptionalGated &&
		item.NegativeOrPositive == "positive" &&
		item.EvidenceProfile == "repo-local-fixture-enabled" &&
		item.FixtureEnabledMode &&
		!item.DefaultMode
}

func isOpenSeedGap(item Item) bool {
	if item.SubclaimID == "seed_gap_open" {
		return true
	}
	return item.PassCriteria.Kind == "seed_gap" && containsString(item.PassCriteria.Assertions, "open")
}

func isSeedGap(item Item) bool {
	return item.SubclaimID == "seed_gap_open" || item.PassCriteria.Kind == "seed_gap"
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

var requiredEvidenceSpecs = []requiredEvidenceSpec{
	{ID: "webdav_export_disabled_admission_unit", ClaimID: "CLAIM_DEFAULT_DENIAL_SAFE", SubclaimID: "webdav_export_disabled_admission", AcceptanceID: "P0_DEFAULT_DENIAL_WEBDAV_DISABLED_ADMISSION", RiskID: "F5", EvidenceProfile: "default", DefaultMode: true, FixtureEnabledMode: false, ExpectedRuntime: "fast", Scope: "package", NegativeOrPositive: "negative", PassCriteriaKind: "denial_safety", CapabilityID: "webdav_export", EvidenceType: "unit", Required: true, DocOnlyAllowed: false, OptionalGated: false, DefaultGARequired: true},
	{ID: "repo_lifecycle_retained_positive_unit", ClaimID: "CLAIM_RETAINED_LIFECYCLE_DEFAULT", SubclaimID: "retained_lifecycle_positive", AcceptanceID: "P0_RETAINED_LIFECYCLE_DEFAULT_POSITIVE", RiskID: "F15", EvidenceProfile: "default", DefaultMode: true, FixtureEnabledMode: false, ExpectedRuntime: "fast", Scope: "package", NegativeOrPositive: "positive", PassCriteriaKind: "positive_path", CapabilityID: "repo_lifecycle_retained", EvidenceType: "unit", Required: true, DocOnlyAllowed: false, OptionalGated: false, DefaultGARequired: true},
	{ID: "workload_mount_disabled_admission_unit", ClaimID: "CLAIM_OPTIONAL_DENIED_SAFE", SubclaimID: "workload_mount_disabled_admission", AcceptanceID: "P0_OPTIONAL_DENIED_WORKLOAD_ADMISSION", RiskID: "F5", EvidenceProfile: "default", DefaultMode: true, FixtureEnabledMode: false, ExpectedRuntime: "fast", Scope: "package", NegativeOrPositive: "negative", PassCriteriaKind: "denial_safety", CapabilityID: "workload_mount_binding", EvidenceType: "unit", Required: true, DocOnlyAllowed: false, OptionalGated: true, DefaultGARequired: false},
	{ID: "workload_mount_plan_store_freshness_unit", ClaimID: "CLAIM_OPTIONAL_DENIED_SAFE", SubclaimID: "workload_mount_plan_store_freshness", AcceptanceID: "P0_OPTIONAL_DENIED_WORKLOAD_PLAN_STORE", RiskID: "F9", EvidenceProfile: "default", DefaultMode: true, FixtureEnabledMode: false, ExpectedRuntime: "fast", Scope: "package", NegativeOrPositive: "negative", PassCriteriaKind: "denial_safety", CapabilityID: "workload_teardown_plan", EvidenceType: "unit", Required: true, DocOnlyAllowed: false, OptionalGated: true, DefaultGARequired: false},
	{ID: "workload_mount_runtime_secretref_config_unit", ClaimID: "CLAIM_OPTIONAL_DENIED_SAFE", SubclaimID: "workload_mount_runtime_secretref_config", AcceptanceID: "P0_OPTIONAL_DENIED_WORKLOAD_RUNTIME_SECRETREF", RiskID: "F10", EvidenceProfile: "default", DefaultMode: true, FixtureEnabledMode: false, ExpectedRuntime: "fast", Scope: "package", NegativeOrPositive: "negative", PassCriteriaKind: "denial_safety", CapabilityID: "workload_teardown_plan", EvidenceType: "unit", Required: true, DocOnlyAllowed: false, OptionalGated: true, DefaultGARequired: false},
	{ID: "workload_mount_secretref_redaction_unit", ClaimID: "CLAIM_OPTIONAL_DENIED_SAFE", SubclaimID: "workload_mount_secretref_redaction", AcceptanceID: "P0_OPTIONAL_DENIED_WORKLOAD_SECRETREF_REDACTION", RiskID: "F10", EvidenceProfile: "default", DefaultMode: true, FixtureEnabledMode: false, ExpectedRuntime: "fast", Scope: "package", NegativeOrPositive: "negative", PassCriteriaKind: "denial_safety", CapabilityID: "workload_mount_discovery", EvidenceType: "unit", Required: true, DocOnlyAllowed: false, OptionalGated: true, DefaultGARequired: false},
	{ID: "repo_template_disabled_admission_unit", ClaimID: "CLAIM_OPTIONAL_DENIED_SAFE", SubclaimID: "repo_template_disabled_admission", AcceptanceID: "P0_OPTIONAL_DENIED_TEMPLATE_ADMISSION", RiskID: "F16", EvidenceProfile: "default", DefaultMode: true, FixtureEnabledMode: false, ExpectedRuntime: "fast", Scope: "package", NegativeOrPositive: "negative", PassCriteriaKind: "denial_safety", CapabilityID: "repo_template", EvidenceType: "unit", Required: true, DocOnlyAllowed: false, OptionalGated: true, DefaultGARequired: false},
	{ID: "repo_purge_disabled_admission_unit", ClaimID: "CLAIM_OPTIONAL_DENIED_SAFE", SubclaimID: "repo_purge_disabled_admission", AcceptanceID: "P0_OPTIONAL_DENIED_PURGE_ADMISSION", RiskID: "F13", EvidenceProfile: "default", DefaultMode: true, FixtureEnabledMode: false, ExpectedRuntime: "fast", Scope: "package", NegativeOrPositive: "negative", PassCriteriaKind: "denial_safety", CapabilityID: "repo_purge", EvidenceType: "unit", Required: true, DocOnlyAllowed: false, OptionalGated: true, DefaultGARequired: false},
	{ID: "repo_template_create_disabled_worker_recovery_unit", ClaimID: "CLAIM_OPTIONAL_DENIED_SAFE", SubclaimID: "repo_template_create_disabled_worker_recovery", AcceptanceID: "P0_OPTIONAL_DENIED_TEMPLATE_CREATE_RECOVERY", RiskID: "F6", EvidenceProfile: "default", DefaultMode: true, FixtureEnabledMode: false, ExpectedRuntime: "fast", Scope: "package", NegativeOrPositive: "negative", PassCriteriaKind: "denial_safety", CapabilityID: "repo_template", EvidenceType: "unit", Required: true, DocOnlyAllowed: false, OptionalGated: true, DefaultGARequired: false},
	{ID: "repo_template_clone_disabled_worker_recovery_unit", ClaimID: "CLAIM_OPTIONAL_DENIED_SAFE", SubclaimID: "repo_template_clone_disabled_worker_recovery", AcceptanceID: "P0_OPTIONAL_DENIED_TEMPLATE_CLONE_RECOVERY", RiskID: "F6", EvidenceProfile: "default", DefaultMode: true, FixtureEnabledMode: false, ExpectedRuntime: "fast", Scope: "package", NegativeOrPositive: "negative", PassCriteriaKind: "denial_safety", CapabilityID: "repo_template", EvidenceType: "unit", Required: true, DocOnlyAllowed: false, OptionalGated: true, DefaultGARequired: false},
	{ID: "repo_purge_disabled_worker_recovery_unit", ClaimID: "CLAIM_OPTIONAL_DENIED_SAFE", SubclaimID: "repo_purge_disabled_worker_recovery", AcceptanceID: "P0_OPTIONAL_DENIED_PURGE_RECOVERY", RiskID: "F13", EvidenceProfile: "default", DefaultMode: true, FixtureEnabledMode: false, ExpectedRuntime: "fast", Scope: "package", NegativeOrPositive: "negative", PassCriteriaKind: "denial_safety", CapabilityID: "repo_purge", EvidenceType: "unit", Required: true, DocOnlyAllowed: false, OptionalGated: true, DefaultGARequired: false},
	{ID: "default_user_loop_repo_projection_unit", ClaimID: "CLAIM_DEFAULT_USER_LOOP", SubclaimID: "default_user_loop_repo_projection", AcceptanceID: "P1B_DEFAULT_USER_LOOP_REPO_PROJECTION", RiskID: "F2", EvidenceProfile: "default", DefaultMode: true, FixtureEnabledMode: false, ExpectedRuntime: "fast", Scope: "package", NegativeOrPositive: "positive", PassCriteriaKind: "positive_path", CapabilityID: "repo_projection", EvidenceType: "unit", Required: true, DocOnlyAllowed: false, OptionalGated: false, DefaultGARequired: true},
	{ID: "repo_create_jvs_runtime_unavailable_recovery_unit", ClaimID: "CLAIM_OPERATION_TERMINALIZATION", SubclaimID: "repo_create_jvs_runtime_unavailable_recovery", AcceptanceID: "P1_OPERATION_TERMINALIZATION_REPO_CREATE_JVS_RUNTIME_UNAVAILABLE_RECOVERY", RiskID: "F6", EvidenceProfile: "default", DefaultMode: true, FixtureEnabledMode: false, ExpectedRuntime: "fast", Scope: "package", NegativeOrPositive: "negative", PassCriteriaKind: "denial_safety", CapabilityID: "repo_create", EvidenceType: "unit", Required: true, DocOnlyAllowed: false, OptionalGated: false, DefaultGARequired: true},
	{ID: "operation_terminalization_contract_unit", ClaimID: "CLAIM_OPERATION_TERMINALIZATION", SubclaimID: "operation_terminalization_contract", AcceptanceID: "P2A_OPERATION_TERMINALIZATION_CONTRACT", RiskID: "F6", EvidenceProfile: "default", DefaultMode: true, FixtureEnabledMode: false, ExpectedRuntime: "fast", Scope: "package", NegativeOrPositive: "both", PassCriteriaKind: "coverage_guard", CapabilityID: "operation_recovery", EvidenceType: "contract", Required: true, DocOnlyAllowed: false, OptionalGated: false, DefaultGARequired: true},
	{ID: "operation_runtime_terminalization_unit", ClaimID: "CLAIM_OPERATION_TERMINALIZATION", SubclaimID: "operation_runtime_terminalization", AcceptanceID: "P2B_OPERATION_RUNTIME_TERMINALIZATION", RiskID: "F6", EvidenceProfile: "default", DefaultMode: true, FixtureEnabledMode: false, ExpectedRuntime: "fast", Scope: "package", NegativeOrPositive: "both", PassCriteriaKind: "coverage_guard", CapabilityID: "operation_recovery", EvidenceType: "unit", Required: true, DocOnlyAllowed: false, OptionalGated: false, DefaultGARequired: true},
	{ID: "default_ga_capability_classification_unit", ClaimID: "CLAIM_CAPABILITY_MATRIX_CONSISTENT", SubclaimID: "default_ga_capability_classification", AcceptanceID: "P0_CAPABILITY_MATRIX_DEFAULT_CLASSIFICATION", RiskID: "F4", EvidenceProfile: "default", DefaultMode: true, FixtureEnabledMode: false, ExpectedRuntime: "fast", Scope: "package", NegativeOrPositive: "both", PassCriteriaKind: "coverage_guard", CapabilityID: "", EvidenceType: "unit", Required: true, DocOnlyAllowed: false, OptionalGated: false, DefaultGARequired: false},
	{ID: "capability_admission_operation_coverage_unit", ClaimID: "CLAIM_CAPABILITY_MATRIX_CONSISTENT", SubclaimID: "capability_admission_operation_coverage", AcceptanceID: "P0_CAPABILITY_MATRIX_OPERATION_COVERAGE", RiskID: "F4", EvidenceProfile: "default", DefaultMode: true, FixtureEnabledMode: false, ExpectedRuntime: "fast", Scope: "package", NegativeOrPositive: "both", PassCriteriaKind: "coverage_guard", CapabilityID: "", EvidenceType: "unit", Required: true, DocOnlyAllowed: false, OptionalGated: false, DefaultGARequired: false},
	{ID: "capability_matrix_v1_contract_unit", ClaimID: "CLAIM_CAPABILITY_MATRIX_CONSISTENT", SubclaimID: "capability_matrix_v1_contract", AcceptanceID: "P1_CAPABILITY_MATRIX_V1_CONTRACT", RiskID: "F4", EvidenceProfile: "default", DefaultMode: true, FixtureEnabledMode: false, ExpectedRuntime: "fast", Scope: "package", NegativeOrPositive: "both", PassCriteriaKind: "coverage_guard", CapabilityID: "", EvidenceType: "unit", Required: true, DocOnlyAllowed: false, OptionalGated: false, DefaultGARequired: false},
	{ID: "capability_runtime_parity_unit", ClaimID: "CLAIM_CAPABILITY_MATRIX_CONSISTENT", SubclaimID: "capability_runtime_parity", AcceptanceID: "P2B_CAPABILITY_RUNTIME_PARITY", RiskID: "F4", EvidenceProfile: "default", DefaultMode: true, FixtureEnabledMode: false, ExpectedRuntime: "fast", Scope: "package", NegativeOrPositive: "both", PassCriteriaKind: "coverage_guard", CapabilityID: "", EvidenceType: "unit", Required: true, DocOnlyAllowed: false, OptionalGated: false, DefaultGARequired: false},
	{ID: "release_script_evidence_manifest_guard", ClaimID: "CLAIM_RELEASE_GATE_TRACEABLE", SubclaimID: "release_gate_invokes_manifest_verifier", AcceptanceID: "P0_RELEASE_GATE_TRACEABLE_MANIFEST_VERIFIER", RiskID: "F18", EvidenceProfile: "default", DefaultMode: true, FixtureEnabledMode: false, ExpectedRuntime: "fast", Scope: "workflow-guard", NegativeOrPositive: "both", PassCriteriaKind: "coverage_guard", CapabilityID: "", EvidenceType: "contract", Required: true, DocOnlyAllowed: false, OptionalGated: false, DefaultGARequired: false},
}

var requiredClaimSubclaimSpecs = []requiredClaimSubclaimSpec{
	{ClaimID: "CLAIM_DEFAULT_DENIAL_SAFE", SubclaimID: "webdav_export_disabled_admission"},
	{ClaimID: "CLAIM_OPTIONAL_DENIED_SAFE", SubclaimID: "workload_mount_disabled_admission"},
	{ClaimID: "CLAIM_OPTIONAL_DENIED_SAFE", SubclaimID: "workload_mount_plan_store_freshness"},
	{ClaimID: "CLAIM_OPTIONAL_DENIED_SAFE", SubclaimID: "workload_mount_runtime_secretref_config"},
	{ClaimID: "CLAIM_OPTIONAL_DENIED_SAFE", SubclaimID: "workload_mount_secretref_redaction"},
	{ClaimID: "CLAIM_OPTIONAL_DENIED_SAFE", SubclaimID: "repo_template_disabled_admission"},
	{ClaimID: "CLAIM_OPTIONAL_DENIED_SAFE", SubclaimID: "repo_template_create_disabled_worker_recovery"},
	{ClaimID: "CLAIM_OPTIONAL_DENIED_SAFE", SubclaimID: "repo_template_clone_disabled_worker_recovery"},
	{ClaimID: "CLAIM_OPTIONAL_DENIED_SAFE", SubclaimID: "repo_purge_disabled_admission"},
	{ClaimID: "CLAIM_OPTIONAL_DENIED_SAFE", SubclaimID: "repo_purge_disabled_worker_recovery"},
	{ClaimID: "CLAIM_DEFAULT_USER_LOOP", SubclaimID: "default_user_loop_repo_projection"},
	{ClaimID: "CLAIM_OPERATION_TERMINALIZATION", SubclaimID: "repo_create_jvs_runtime_unavailable_recovery"},
	{ClaimID: "CLAIM_OPERATION_TERMINALIZATION", SubclaimID: "operation_terminalization_contract"},
	{ClaimID: "CLAIM_OPERATION_TERMINALIZATION", SubclaimID: "operation_runtime_terminalization"},
	{ClaimID: "CLAIM_RETAINED_LIFECYCLE_DEFAULT", SubclaimID: "retained_lifecycle_positive"},
	{ClaimID: "CLAIM_CAPABILITY_MATRIX_CONSISTENT", SubclaimID: "default_ga_capability_classification"},
	{ClaimID: "CLAIM_CAPABILITY_MATRIX_CONSISTENT", SubclaimID: "capability_admission_operation_coverage"},
	{ClaimID: "CLAIM_CAPABILITY_MATRIX_CONSISTENT", SubclaimID: "capability_matrix_v1_contract"},
	{ClaimID: "CLAIM_CAPABILITY_MATRIX_CONSISTENT", SubclaimID: "capability_runtime_parity"},
	{ClaimID: "CLAIM_RELEASE_GATE_TRACEABLE", SubclaimID: "release_gate_invokes_manifest_verifier"},
}

var seedGapSpecs = []seedGapSpec{
	{ID: "seed_gap_admin_bootstrap_ready_open", ClaimID: "CLAIM_ADMIN_BOOTSTRAP_READY", RiskID: "F3", FinalSubclaimID: "admin_bootstrap_ready", FinalAcceptanceID: "P0_ADMIN_BOOTSTRAP_READY", FinalCapabilityID: "admin_bootstrap", FinalEvidenceProfile: "default", FinalDefaultMode: true, FinalFixtureEnabledMode: false, FinalExpectedRuntime: "fast", FinalScope: "package", FinalNegativeOrPositive: "positive", FinalPassCriteriaKind: "positive_path", FinalPassCriteriaAssert: "admin bootstrap readiness passes in default mode", FinalOptionalGated: false, FinalDefaultGARequired: true},
	{ID: "seed_gap_default_user_loop_open", ClaimID: "CLAIM_DEFAULT_USER_LOOP", RiskID: "F2", FinalSubclaimID: "default_user_loop_positive", FinalAcceptanceID: "P0_DEFAULT_USER_LOOP_POSITIVE", FinalCapabilityID: "caller_policy_readiness", FinalEvidenceProfile: "default", FinalDefaultMode: true, FinalFixtureEnabledMode: false, FinalExpectedRuntime: "fast", FinalScope: "package", FinalNegativeOrPositive: "positive", FinalPassCriteriaKind: "positive_path", FinalPassCriteriaAssert: "default user loop passes in default mode", FinalOptionalGated: false, FinalDefaultGARequired: true},
	{ID: "seed_gap_workload_fixture_ready_open", ClaimID: "CLAIM_WORKLOAD_FIXTURE_READY", RiskID: "F9", FinalSubclaimID: "workload_fixture_ready", FinalAcceptanceID: "P0_WORKLOAD_FIXTURE_READY", FinalCapabilityID: "workload_mount_binding", FinalEvidenceProfile: "repo-local-fixture-enabled", FinalDefaultMode: false, FinalFixtureEnabledMode: true, FinalExpectedRuntime: "integration", FinalScope: "repo-local-e2e", FinalNegativeOrPositive: "positive", FinalPassCriteriaKind: "positive_path", FinalPassCriteriaAssert: "workload fixture readiness passes with repo-local fixture", FinalOptionalGated: true, FinalDefaultGARequired: false},
	{ID: "seed_gap_operator_repair_safe_open", ClaimID: "CLAIM_OPERATOR_REPAIR_SAFE", RiskID: "F11", FinalSubclaimID: "operator_repair_safe", FinalAcceptanceID: "P0_OPERATOR_REPAIR_SAFE", FinalCapabilityID: "operation_recovery", FinalEvidenceProfile: "default", FinalDefaultMode: true, FinalFixtureEnabledMode: false, FinalExpectedRuntime: "fast", FinalScope: "package", FinalNegativeOrPositive: "positive", FinalPassCriteriaKind: "positive_path", FinalPassCriteriaAssert: "operator repair safety passes in default mode", FinalOptionalGated: false, FinalDefaultGARequired: true},
	{ID: "seed_gap_purge_approval_safe_open", ClaimID: "CLAIM_PURGE_APPROVAL_SAFE", RiskID: "F13", FinalSubclaimID: "purge_approval_safe", FinalAcceptanceID: "P0_PURGE_APPROVAL_SAFE", FinalCapabilityID: "repo_purge", FinalEvidenceProfile: "repo-local-fixture-enabled", FinalDefaultMode: false, FinalFixtureEnabledMode: true, FinalExpectedRuntime: "integration", FinalScope: "repo-local-e2e", FinalNegativeOrPositive: "positive", FinalPassCriteriaKind: "positive_path", FinalPassCriteriaAssert: "purge approval safety passes with repo-local fixture", FinalOptionalGated: true, FinalDefaultGARequired: false},
	{ID: "seed_gap_restore_reconciliation_open", ClaimID: "CLAIM_RESTORE_RECONCILIATION", RiskID: "F14", FinalSubclaimID: "restore_reconciliation_safe", FinalAcceptanceID: "P0_RESTORE_RECONCILIATION_SAFE", FinalCapabilityID: "jvs_save_restore", FinalEvidenceProfile: "default", FinalDefaultMode: true, FinalFixtureEnabledMode: false, FinalExpectedRuntime: "fast", FinalScope: "package", FinalNegativeOrPositive: "positive", FinalPassCriteriaKind: "positive_path", FinalPassCriteriaAssert: "restore reconciliation safety passes in default mode", FinalOptionalGated: false, FinalDefaultGARequired: true},
	{ID: "seed_gap_residual_risk_catalog_open", ClaimID: "CLAIM_RESIDUAL_RISK_CATALOG", RiskID: "F12", FinalSubclaimID: "residual_risk_catalog_guard", FinalAcceptanceID: "P0_RESIDUAL_RISK_CATALOG_GUARD", FinalCapabilityID: "", FinalEvidenceProfile: "default", FinalDefaultMode: true, FinalFixtureEnabledMode: false, FinalExpectedRuntime: "fast", FinalScope: "package", FinalNegativeOrPositive: "both", FinalPassCriteriaKind: "coverage_guard", FinalPassCriteriaAssert: "residual risk catalog guard covers final release evidence", FinalOptionalGated: false, FinalDefaultGARequired: false},
	{ID: "seed_gap_deployment_risk_envelope_open", ClaimID: "CLAIM_DEPLOYMENT_RISK_ENVELOPE", RiskID: "F17", FinalSubclaimID: "deployment_risk_envelope_guard", FinalAcceptanceID: "P0_DEPLOYMENT_RISK_ENVELOPE_GUARD", FinalCapabilityID: "", FinalEvidenceProfile: "default", FinalDefaultMode: true, FinalFixtureEnabledMode: false, FinalExpectedRuntime: "fast", FinalScope: "workflow-guard", FinalNegativeOrPositive: "both", FinalPassCriteriaKind: "coverage_guard", FinalPassCriteriaAssert: "deployment risk envelope guard covers final release evidence", FinalOptionalGated: false, FinalDefaultGARequired: false},
	{ID: "seed_gap_profile_boundary_open", ClaimID: "CLAIM_PROFILE_BOUNDARY", RiskID: "F1", FinalSubclaimID: "profile_boundary_consistent", FinalAcceptanceID: "P0_PROFILE_BOUNDARY_CONSISTENT", FinalCapabilityID: "", FinalEvidenceProfile: "default", FinalDefaultMode: true, FinalFixtureEnabledMode: false, FinalExpectedRuntime: "fast", FinalScope: "package", FinalNegativeOrPositive: "both", FinalPassCriteriaKind: "coverage_guard", FinalPassCriteriaAssert: "profile boundary consistency covers final release evidence", FinalOptionalGated: false, FinalDefaultGARequired: false},
	{ID: "seed_gap_discovery_surfaces_open", ClaimID: "CLAIM_DISCOVERY_SURFACES", RiskID: "F7", FinalSubclaimID: "discovery_surfaces_layered", FinalAcceptanceID: "P0_DISCOVERY_SURFACES_LAYERED", FinalCapabilityID: "repo_projection", FinalEvidenceProfile: "default", FinalDefaultMode: true, FinalFixtureEnabledMode: false, FinalExpectedRuntime: "fast", FinalScope: "package", FinalNegativeOrPositive: "positive", FinalPassCriteriaKind: "positive_path", FinalPassCriteriaAssert: "discovery surfaces pass layered default checks", FinalOptionalGated: false, FinalDefaultGARequired: true},
	{ID: "seed_gap_webdav_default_access_open", ClaimID: "CLAIM_WEBDAV_DEFAULT_ACCESS", RiskID: "F8", FinalSubclaimID: "webdav_default_access", FinalAcceptanceID: "P0_WEBDAV_DEFAULT_ACCESS", FinalCapabilityID: "webdav_export", FinalEvidenceProfile: "default", FinalDefaultMode: true, FinalFixtureEnabledMode: false, FinalExpectedRuntime: "fast", FinalScope: "package", FinalNegativeOrPositive: "positive", FinalPassCriteriaKind: "positive_path", FinalPassCriteriaAssert: "webdav default access passes in default mode", FinalOptionalGated: false, FinalDefaultGARequired: true},
	{ID: "seed_gap_secret_path_redaction_open", ClaimID: "CLAIM_SECRET_PATH_REDACTION", RiskID: "F10", FinalSubclaimID: "secret_path_redaction", FinalAcceptanceID: "P0_SECRET_PATH_REDACTION", FinalCapabilityID: "path_redaction", FinalEvidenceProfile: "default", FinalDefaultMode: true, FinalFixtureEnabledMode: false, FinalExpectedRuntime: "fast", FinalScope: "package", FinalNegativeOrPositive: "negative", FinalPassCriteriaKind: "denial_safety", FinalPassCriteriaAssert: "secret path redaction denies secret path disclosure", FinalOptionalGated: false, FinalDefaultGARequired: true},
	{ID: "seed_gap_optional_fixture_conformant_open", ClaimID: "CLAIM_OPTIONAL_FIXTURE_CONFORMANT", RiskID: "F9", FinalSubclaimID: "optional_fixture_conformant", FinalAcceptanceID: "P0_OPTIONAL_FIXTURE_CONFORMANT", FinalCapabilityID: "workload_mount_binding", FinalEvidenceProfile: "repo-local-fixture-enabled", FinalDefaultMode: false, FinalFixtureEnabledMode: true, FinalExpectedRuntime: "integration", FinalScope: "repo-local-e2e", FinalNegativeOrPositive: "positive", FinalPassCriteriaKind: "positive_path", FinalPassCriteriaAssert: "optional fixture conformance passes with repo-local fixture", FinalOptionalGated: true, FinalDefaultGARequired: false},
	{ID: "seed_gap_template_quota_boundary_open", ClaimID: "CLAIM_TEMPLATE_QUOTA_BOUNDARY", RiskID: "F16", FinalSubclaimID: "template_quota_boundary", FinalAcceptanceID: "P0_TEMPLATE_QUOTA_BOUNDARY", FinalCapabilityID: "repo_template", FinalEvidenceProfile: "repo-local-fixture-enabled", FinalDefaultMode: false, FinalFixtureEnabledMode: true, FinalExpectedRuntime: "integration", FinalScope: "repo-local-e2e", FinalNegativeOrPositive: "positive", FinalPassCriteriaKind: "positive_path", FinalPassCriteriaAssert: "template quota boundary passes with repo-local fixture", FinalOptionalGated: true, FinalDefaultGARequired: false},
	{ID: "seed_gap_workflow_hardening_guard_open", ClaimID: "CLAIM_WORKFLOW_HARDENING_GUARD", RiskID: "F18", FinalSubclaimID: "workflow_hardening_guard", FinalAcceptanceID: "P0_WORKFLOW_HARDENING_GUARD", FinalCapabilityID: "", FinalEvidenceProfile: "default", FinalDefaultMode: true, FinalFixtureEnabledMode: false, FinalExpectedRuntime: "fast", FinalScope: "workflow-guard", FinalNegativeOrPositive: "both", FinalPassCriteriaKind: "coverage_guard", FinalPassCriteriaAssert: "workflow hardening guard covers final release evidence", FinalOptionalGated: false, FinalDefaultGARequired: false},
}

func validateItem(item Item, repoRoot, mode string) []Finding {
	var findings []Finding
	findings = append(findings, validatePackage0Metadata(item)...)
	if !validEvidenceTypes[item.EvidenceType] && item.EvidenceType != "" {
		findings = append(findings, Finding{ItemID: item.ID, Code: "item.evidence_type_invalid", Message: fmt.Sprintf("unsupported evidence_type %q", item.EvidenceType)})
	}
	if !validCapabilities[item.CapabilityID] {
		findings = append(findings, Finding{ItemID: item.ID, Code: "item.capability_id_invalid", Message: fmt.Sprintf("unsupported capability_id %q", item.CapabilityID)})
	}
	if !validEvidenceStatuses[item.EvidenceStatus] && item.EvidenceStatus != "" {
		findings = append(findings, Finding{ItemID: item.ID, Code: "item.evidence_status_invalid", Message: fmt.Sprintf("unsupported evidence_status %q; valid values are placeholder, implemented, or closed (static manifest state, not this command run result)", item.EvidenceStatus)})
	}
	if item.Required && item.EvidenceStatus == "placeholder" && !optionalPositiveFixtureEvidence(item) {
		findings = append(findings, Finding{ItemID: item.ID, Code: "item.evidence_status_placeholder_required", Message: "required evidence cannot have evidence_status=placeholder"})
	}
	if mode == ManifestModeFinal && legacyFinalInvalidCapabilities[item.CapabilityID] {
		findings = append(findings, Finding{ItemID: item.ID, Code: "item.capability_id_legacy_final_invalid", Message: fmt.Sprintf("legacy compatibility capability_id %q is not valid in final mode", item.CapabilityID)})
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

func validatePackage0Metadata(item Item) []Finding {
	var findings []Finding
	if item.ClaimID != "" && !validClaimIDs[item.ClaimID] {
		findings = append(findings, Finding{ItemID: item.ID, Code: "item.claim_id_invalid", Message: fmt.Sprintf("unsupported claim_id %q", item.ClaimID)})
	}
	if item.EvidenceProfile != "" && !validEvidenceProfiles[item.EvidenceProfile] {
		findings = append(findings, Finding{ItemID: item.ID, Code: "item.evidence_profile_invalid", Message: fmt.Sprintf("unsupported evidence_profile %q", item.EvidenceProfile)})
	}
	if item.ExpectedRuntime != "" && !validExpectedRuntimes[item.ExpectedRuntime] {
		findings = append(findings, Finding{ItemID: item.ID, Code: "item.expected_runtime_invalid", Message: fmt.Sprintf("unsupported expected_runtime %q", item.ExpectedRuntime)})
	}
	if item.Scope != "" && !validScopes[item.Scope] {
		findings = append(findings, Finding{ItemID: item.ID, Code: "item.scope_invalid", Message: fmt.Sprintf("unsupported scope %q", item.Scope)})
	}
	if item.NegativeOrPositive != "" && !validNegativeOrPositive[item.NegativeOrPositive] {
		findings = append(findings, Finding{ItemID: item.ID, Code: "item.negative_or_positive_invalid", Message: fmt.Sprintf("unsupported negative_or_positive %q", item.NegativeOrPositive)})
	}
	if item.PassCriteria.Kind != "" && !validPassCriteriaKinds[item.PassCriteria.Kind] {
		findings = append(findings, Finding{ItemID: item.ID, Code: "item.pass_criteria_kind_invalid", Message: fmt.Sprintf("unsupported pass_criteria.kind %q", item.PassCriteria.Kind)})
	}
	if item.PassCriteria.Kind == "" {
		findings = append(findings, Finding{ItemID: item.ID, Code: "item.pass_criteria_kind_missing", Message: "pass_criteria.kind is required"})
	}
	if len(nonEmptyStrings(item.PassCriteria.Assertions)) == 0 {
		findings = append(findings, Finding{ItemID: item.ID, Code: "item.pass_criteria_assertions_missing", Message: "pass_criteria.assertions must contain at least one non-empty assertion"})
	}
	if item.DefaultMode && item.FixtureEnabledMode {
		findings = append(findings, Finding{ItemID: item.ID, Code: "item.evidence_mode_invalid", Message: "default_mode=true and fixture_enabled_mode=true cannot both be set"})
	}
	if item.Required && item.EvidenceProfile == "deployment-runtime-support" {
		findings = append(findings, Finding{ItemID: item.ID, Code: "item.evidence_profile_required_invalid", Message: "deployment-runtime-support cannot be required local GA evidence"})
	}
	if item.OptionalGated && item.NegativeOrPositive == "positive" {
		if item.EvidenceProfile != "repo-local-fixture-enabled" || !item.FixtureEnabledMode || item.DefaultMode {
			findings = append(findings, Finding{ItemID: item.ID, Code: "item.optional_positive_profile_invalid", Message: "optional positive evidence must use repo-local-fixture-enabled profile with fixture_enabled_mode=true and default_mode=false"})
		}
		if strings.TrimSpace(item.FixtureID) == "" {
			findings = append(findings, Finding{ItemID: item.ID, Code: "item.fixture_id_missing", Message: "optional fixture positive evidence requires fixture_id"})
		}
	}
	if optionalCapability(item.CapabilityID) && item.EvidenceProfile == "default" && item.NegativeOrPositive != "" && item.NegativeOrPositive != "negative" {
		findings = append(findings, Finding{ItemID: item.ID, Code: "item.optional_default_polarity_invalid", Message: fmt.Sprintf("optional capability %s default profile evidence must be negative", item.CapabilityID)})
	}
	if item.Required && item.ClaimID == "CLAIM_DEFAULT_USER_LOOP" && (!item.DefaultMode || item.NegativeOrPositive == "negative") {
		findings = append(findings, Finding{ItemID: item.ID, Code: "item.default_user_loop_evidence_invalid", Message: "CLAIM_DEFAULT_USER_LOOP required evidence must be default_mode=true and positive or both"})
	}
	if item.Required && (strings.TrimSpace(item.RiskID) != "" || highRiskClaims[item.ClaimID]) {
		if item.DocOnlyAllowed || item.EvidenceType == "doc-guard" || len(item.Anchors) == 0 || allDocAnchors(item.Anchors) {
			findings = append(findings, Finding{ItemID: item.ID, Code: "item.risk_bound_doc_only_invalid", Message: "risk-bound required evidence cannot be doc-only"})
		}
	}
	return findings
}

func nonEmptyStrings(values []string) []string {
	var nonEmpty []string
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			nonEmpty = append(nonEmpty, value)
		}
	}
	return nonEmpty
}

func optionalCapability(capabilityID string) bool {
	return optionalGatedCapabilities[capabilityID]
}

func validateCapabilityClassification(item Item) []Finding {
	if defaultGARequiredCapabilities[item.CapabilityID] {
		if item.OptionalGated {
			return []Finding{{ItemID: item.ID, Code: "item.optional_gated_invalid", Message: fmt.Sprintf("%s is default GA required and cannot be optional_gated=true", item.CapabilityID)}}
		}
		if !item.DefaultGARequired {
			return []Finding{{ItemID: item.ID, Code: "item.default_ga_required_invalid", Message: fmt.Sprintf("%s must have default_ga_required=true", item.CapabilityID)}}
		}
		return nil
	}
	if optionalGatedCapabilities[item.CapabilityID] {
		if !item.OptionalGated {
			return []Finding{{ItemID: item.ID, Code: "item.optional_gated_invalid", Message: fmt.Sprintf("%s is optional-gated and must have optional_gated=true", item.CapabilityID)}}
		}
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
	selector, _, err := loadReleaseSelectorForMode(path, options, options.Mode, repoRootOrDefault(options.RepoRoot))
	if err != nil {
		return findings, err
	}

	repoRoot := repoRootOrDefault(options.RepoRoot)
	for _, item := range manifest.Items {
		if !itemRequiredForExecution(item, selector) {
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

func itemRequiredForExecution(item Item, selector *ReleaseSelector) bool {
	if !item.Required {
		return false
	}
	if !optionalPositiveFixtureEvidence(item) {
		return true
	}
	if selector == nil {
		return false
	}
	for _, capabilityID := range selector.ClaimedOptionalCapabilities {
		if capabilityID == item.CapabilityID {
			return true
		}
	}
	return false
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

func manifestMode(mode string) (string, []Finding) {
	switch mode {
	case "":
		return "", []Finding{{Code: "manifest.mode_missing", Message: "mode must be explicitly set to seed or final"}}
	case ManifestModeSeed:
		return ManifestModeSeed, nil
	case ManifestModeFinal:
		return ManifestModeFinal, nil
	default:
		return "", []Finding{{Code: "manifest.mode_invalid", Message: "mode must be seed or final"}}
	}
}

func allDocAnchors(anchors []string) bool {
	for _, anchor := range anchors {
		if !docOnlyAnchor(anchor) {
			return false
		}
	}
	return true
}

func docOnlyAnchor(anchor string) bool {
	path := strings.Split(anchor, "#")[0]
	return strings.HasPrefix(path, "docs/") || strings.HasSuffix(path, ".md")
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

var validEvidenceStatuses = map[string]bool{
	"placeholder": true,
	"implemented": true,
	"closed":      true,
}

var validEvidenceProfiles = map[string]bool{
	"default":                    true,
	"repo-local-fixture-enabled": true,
	"deployment-runtime-support": true,
}

var validExpectedRuntimes = map[string]bool{
	"fast":        true,
	"integration": true,
	"e2e":         true,
	"race":        true,
}

var validScopes = map[string]bool{
	"unit":           true,
	"package":        true,
	"service":        true,
	"repo-local-e2e": true,
	"doc-guard":      true,
	"workflow-guard": true,
}

var validNegativeOrPositive = map[string]bool{
	"negative": true,
	"positive": true,
	"both":     true,
}

var validPassCriteriaKinds = map[string]bool{
	"command_exit_zero": true,
	"denial_safety":     true,
	"positive_path":     true,
	"coverage_guard":    true,
	"seed_gap":          true,
}

var highRiskClaims = map[string]bool{
	"CLAIM_DEFAULT_DENIAL_SAFE":          true,
	"CLAIM_OPTIONAL_DENIED_SAFE":         true,
	"CLAIM_OPERATION_TERMINALIZATION":    true,
	"CLAIM_CAPABILITY_MATRIX_CONSISTENT": true,
	"CLAIM_RELEASE_GATE_TRACEABLE":       true,
}

var validClaimIDs = map[string]bool{
	"CLAIM_PROFILE_BOUNDARY":             true,
	"CLAIM_ADMIN_BOOTSTRAP_READY":        true,
	"CLAIM_DEFAULT_USER_LOOP":            true,
	"CLAIM_DEFAULT_DENIAL_SAFE":          true,
	"CLAIM_OPTIONAL_DENIED_SAFE":         true,
	"CLAIM_CAPABILITY_MATRIX_CONSISTENT": true,
	"CLAIM_OPERATION_TERMINALIZATION":    true,
	"CLAIM_DISCOVERY_SURFACES":           true,
	"CLAIM_WEBDAV_DEFAULT_ACCESS":        true,
	"CLAIM_SECRET_PATH_REDACTION":        true,
	"CLAIM_WORKLOAD_FIXTURE_READY":       true,
	"CLAIM_OPTIONAL_FIXTURE_CONFORMANT":  true,
	"CLAIM_OPERATOR_REPAIR_SAFE":         true,
	"CLAIM_RESIDUAL_RISK_CATALOG":        true,
	"CLAIM_PURGE_APPROVAL_SAFE":          true,
	"CLAIM_RESTORE_RECONCILIATION":       true,
	"CLAIM_RETAINED_LIFECYCLE_DEFAULT":   true,
	"CLAIM_TEMPLATE_QUOTA_BOUNDARY":      true,
	"CLAIM_DEPLOYMENT_RISK_ENVELOPE":     true,
	"CLAIM_WORKFLOW_HARDENING_GUARD":     true,
	"CLAIM_RELEASE_GATE_TRACEABLE":       true,
}

var validCapabilities = releaseEvidenceValidCapabilities()

var defaultGARequiredCapabilities = releaseEvidenceDefaultGARequiredCapabilities()

var optionalGatedCapabilities = releaseEvidenceOptionalGatedCapabilities()

var legacyFinalInvalidCapabilities = map[string]bool{
	"storage":        true,
	"jvs":            true,
	"workload_mount": true,
}

func releaseEvidenceValidCapabilities() map[string]bool {
	ids := map[string]bool{
		"":                               true,
		string(capability.Storage):       true,
		string(capability.JVS):           true,
		string(capability.WorkloadMount): true,
	}
	for _, row := range capability.CapabilityMatrixV1Rows() {
		ids[string(row.ID)] = true
	}
	return ids
}

func releaseEvidenceDefaultGARequiredCapabilities() map[string]bool {
	ids := map[string]bool{
		string(capability.Storage): true,
		string(capability.JVS):     true,
	}
	for _, row := range capability.CapabilityMatrixV1Rows() {
		if row.DefaultGARequired {
			ids[string(row.ID)] = true
		}
	}
	return ids
}

func releaseEvidenceOptionalGatedCapabilities() map[string]bool {
	ids := map[string]bool{
		string(capability.WorkloadMount): true,
	}
	for _, row := range capability.CapabilityMatrixV1Rows() {
		if row.OptionalGated {
			ids[string(row.ID)] = true
		}
	}
	return ids
}

var allowedExecutables = map[string]bool{
	"go":   true,
	"bash": true,
}
