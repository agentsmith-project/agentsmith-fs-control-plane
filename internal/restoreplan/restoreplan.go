package restoreplan

import (
	"fmt"
	"strings"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/pathresolver"
)

type Status string

const (
	StatusPending                      Status = "pending"
	StatusConsuming                    Status = "consuming"
	StatusConsumed                     Status = "consumed"
	StatusDiscarding                   Status = "discarding"
	StatusDiscarded                    Status = "discarded"
	StatusOperatorInterventionRequired Status = "operator_intervention_required"
)

type Plan struct {
	ID                 string
	NamespaceID        string
	RepoID             string
	PreviewOperationID string
	SourceSavePointID  string
	BaseRevision       string
	HeadRevision       string
	Generation         string
	FenceMarker        string
	Summary            Summary
	Blockers           []Blocker
	Stale              bool
	Status             Status
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type ChangeSummary struct {
	Count   int
	Samples []string
}

type Summary struct {
	Added       ChangeSummary
	Changed     ChangeSummary
	Removed     ChangeSummary
	Destructive bool
}

type Blocker struct {
	Code    string
	Message string
}

func (status Status) String() string {
	return string(status)
}

func (status Status) Valid() bool {
	switch status {
	case StatusPending,
		StatusConsuming,
		StatusConsumed,
		StatusDiscarding,
		StatusDiscarded,
		StatusOperatorInterventionRequired:
		return true
	default:
		return false
	}
}

func (status Status) Active() bool {
	switch status {
	case StatusPending, StatusConsuming, StatusDiscarding, StatusOperatorInterventionRequired:
		return true
	default:
		return false
	}
}

func Active(status Status) bool {
	return status.Active()
}

func (plan Plan) Active() bool {
	return plan.Status.Active()
}

func (plan Plan) Validate() error {
	if err := ValidateID(plan.ID); err != nil {
		return err
	}
	if err := pathresolver.ValidateID(pathresolver.NamespaceID, plan.NamespaceID); err != nil {
		return err
	}
	if err := pathresolver.ValidateID(pathresolver.RepoID, plan.RepoID); err != nil {
		return err
	}
	if err := pathresolver.ValidateID(pathresolver.OperationID, plan.PreviewOperationID); err != nil {
		return err
	}
	if !safeOpaqueID(plan.SourceSavePointID) {
		return fmt.Errorf("invalid source_save_point_id %q", plan.SourceSavePointID)
	}
	if !safeOpaqueID(plan.BaseRevision) {
		return fmt.Errorf("invalid base_revision %q", plan.BaseRevision)
	}
	if !safeOpaqueID(plan.HeadRevision) {
		return fmt.Errorf("invalid head_revision %q", plan.HeadRevision)
	}
	if !safeOpaqueID(plan.Generation) {
		return fmt.Errorf("invalid generation %q", plan.Generation)
	}
	if !safeOpaqueID(plan.FenceMarker) {
		return fmt.Errorf("invalid fence_marker %q", plan.FenceMarker)
	}
	if err := plan.Summary.Validate(); err != nil {
		return err
	}
	if err := ValidateBlockers(plan.Blockers); err != nil {
		return err
	}
	if !plan.Status.Valid() {
		return fmt.Errorf("unknown restore plan status %q", plan.Status)
	}
	if plan.CreatedAt.IsZero() {
		return fmt.Errorf("restore plan missing created_at")
	}
	if plan.UpdatedAt.IsZero() {
		return fmt.Errorf("restore plan missing updated_at")
	}
	if plan.UpdatedAt.Before(plan.CreatedAt) {
		return fmt.Errorf("restore plan updated_at before created_at")
	}
	return nil
}

func (summary Summary) Validate() error {
	if err := summary.Added.Validate("added"); err != nil {
		return err
	}
	if err := summary.Changed.Validate("changed"); err != nil {
		return err
	}
	if err := summary.Removed.Validate("removed"); err != nil {
		return err
	}
	return nil
}

func (summary ChangeSummary) Validate(name string) error {
	if summary.Count < 0 {
		return fmt.Errorf("invalid %s count", name)
	}
	if len(summary.Samples) > 10 {
		return fmt.Errorf("too many %s samples", name)
	}
	for _, sample := range summary.Samples {
		if !safeDisplayPath(sample) {
			return fmt.Errorf("invalid %s sample %q", name, sample)
		}
	}
	return nil
}

func ValidateBlockers(blockers []Blocker) error {
	if len(blockers) > 16 {
		return fmt.Errorf("too many restore blockers")
	}
	for _, blocker := range blockers {
		if !safeOpaqueID(blocker.Code) {
			return fmt.Errorf("invalid blocker code %q", blocker.Code)
		}
		if blocker.Message != "" && !safeBlockerMessage(blocker.Message) {
			return fmt.Errorf("invalid blocker message")
		}
	}
	return nil
}

func ValidTransition(from, to Status) bool {
	if !from.Valid() || !to.Valid() {
		return false
	}
	switch from {
	case StatusPending:
		return to == StatusConsuming || to == StatusDiscarding || to == StatusOperatorInterventionRequired
	case StatusConsuming:
		return to == StatusConsumed || to == StatusOperatorInterventionRequired
	case StatusDiscarding:
		return to == StatusDiscarded || to == StatusOperatorInterventionRequired
	default:
		return false
	}
}

func SummaryMap(summary Summary) map[string]any {
	return map[string]any{
		"added":       ChangeSummaryMap(summary.Added),
		"changed":     ChangeSummaryMap(summary.Changed),
		"removed":     ChangeSummaryMap(summary.Removed),
		"destructive": summary.Destructive,
	}
}

func ChangeSummaryMap(summary ChangeSummary) map[string]any {
	samples := append([]string(nil), summary.Samples...)
	if samples == nil {
		samples = []string{}
	}
	return map[string]any{"count": summary.Count, "samples": samples}
}

func BlockersList(blockers []Blocker) []map[string]any {
	out := make([]map[string]any, 0, len(blockers))
	for _, blocker := range blockers {
		item := map[string]any{"code": blocker.Code}
		if blocker.Message != "" {
			item["message"] = blocker.Message
		}
		out = append(out, item)
	}
	return out
}

func SummaryFromMap(raw map[string]any) (Summary, error) {
	added, err := changeSummaryFromMap(raw, "added")
	if err != nil {
		return Summary{}, err
	}
	changed, err := changeSummaryFromMap(raw, "changed")
	if err != nil {
		return Summary{}, err
	}
	removed, err := changeSummaryFromMap(raw, "removed")
	if err != nil {
		return Summary{}, err
	}
	destructive, _ := raw["destructive"].(bool)
	summary := Summary{Added: added, Changed: changed, Removed: removed, Destructive: destructive}
	if err := summary.Validate(); err != nil {
		return Summary{}, err
	}
	return summary, nil
}

func BlockersFromList(raw []any) ([]Blocker, error) {
	blockers := make([]Blocker, 0, len(raw))
	for _, item := range raw {
		mapped, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid blocker")
		}
		code, _ := mapped["code"].(string)
		message, _ := mapped["message"].(string)
		blockers = append(blockers, Blocker{Code: code, Message: message})
	}
	if err := ValidateBlockers(blockers); err != nil {
		return nil, err
	}
	return blockers, nil
}

func changeSummaryFromMap(raw map[string]any, key string) (ChangeSummary, error) {
	value, ok := raw[key].(map[string]any)
	if !ok {
		return ChangeSummary{}, fmt.Errorf("missing %s summary", key)
	}
	count, ok := intValue(value["count"])
	if !ok {
		return ChangeSummary{}, fmt.Errorf("invalid %s count", key)
	}
	samples := []string{}
	if rawSamples, exists := value["samples"]; exists {
		list, ok := rawSamples.([]any)
		if !ok {
			return ChangeSummary{}, fmt.Errorf("invalid %s samples", key)
		}
		for _, item := range list {
			text, ok := item.(string)
			if !ok {
				return ChangeSummary{}, fmt.Errorf("invalid %s sample", key)
			}
			samples = append(samples, text)
		}
	}
	summary := ChangeSummary{Count: count, Samples: samples}
	if err := summary.Validate(key); err != nil {
		return ChangeSummary{}, err
	}
	return summary, nil
}

func intValue(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case float64:
		if typed != float64(int(typed)) {
			return 0, false
		}
		return int(typed), true
	default:
		return 0, false
	}
}

func ValidateID(id string) error {
	if !safeOpaqueID(id) {
		return fmt.Errorf("invalid restore_plan_id %q", id)
	}
	return nil
}

func safeDisplayPath(value string) bool {
	if len(value) == 0 || len(value) > 256 || strings.TrimSpace(value) != value {
		return false
	}
	if strings.HasPrefix(value, "/") || strings.Contains(value, "\\") || strings.Contains(value, "\x00") {
		return false
	}
	parts := strings.Split(value, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	for i := 0; i < len(value); i++ {
		b := value[i]
		if b < 0x20 || b == 0x7f {
			return false
		}
	}
	return true
}

func safeBlockerMessage(value string) bool {
	if len(value) > 512 || strings.TrimSpace(value) != value {
		return false
	}
	for i := 0; i < len(value); i++ {
		b := value[i]
		if b < 0x20 || b == 0x7f {
			return false
		}
	}
	return true
}

func safeOpaqueID(id string) bool {
	if len(id) == 0 || len(id) > 128 || strings.TrimSpace(id) != id {
		return false
	}
	for i := 0; i < len(id); i++ {
		b := id[i]
		if i == 0 {
			if !asciiAlphaNum(b) {
				return false
			}
			continue
		}
		if !asciiAlphaNum(b) && b != '_' && b != '-' && b != '.' && b != ':' {
			return false
		}
	}
	return true
}

func asciiAlphaNum(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}
