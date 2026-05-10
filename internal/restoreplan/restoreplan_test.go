package restoreplan

import (
	"strings"
	"testing"
	"time"
)

func TestPlanValidateRequiresDurableIdentityStatusAndTimestamps(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	valid := Plan{
		ID:                 "b644aec4-bcb6-4480-b5fa-a283927dd3cd",
		NamespaceID:        "ns_alpha01",
		RepoID:             "repo_alpha01",
		PreviewOperationID: "op_preview01",
		SourceSavePointID:  "sp_001",
		BaseRevision:       "sp_002",
		HeadRevision:       "sp_002",
		Generation:         "sha256:preview-base",
		FenceMarker:        "preview_fence_op_preview01",
		Summary: Summary{
			Changed: ChangeSummary{Count: 1, Samples: []string{"docs/readme.md"}},
			Removed: ChangeSummary{Count: 1, Samples: []string{"tmp/cache.txt"}},
			Added:   ChangeSummary{Count: 1, Samples: []string{"src/new.ts"}},
		},
		Blockers: []Blocker{},
		Stale:    false,
		Status:             StatusPending,
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate valid plan: %v", err)
	}

	tests := []struct {
		name string
		edit func(*Plan)
	}{
		{name: "bad restore plan id", edit: func(plan *Plan) { plan.ID = "plan/unsafe" }},
		{name: "bad namespace id", edit: func(plan *Plan) { plan.NamespaceID = "repo_alpha01" }},
		{name: "bad repo id", edit: func(plan *Plan) { plan.RepoID = "repo/unsafe" }},
		{name: "bad preview operation id", edit: func(plan *Plan) { plan.PreviewOperationID = "preview01" }},
		{name: "bad source save point id", edit: func(plan *Plan) { plan.SourceSavePointID = "sp/001" }},
		{name: "missing base revision", edit: func(plan *Plan) { plan.BaseRevision = "" }},
		{name: "missing head revision", edit: func(plan *Plan) { plan.HeadRevision = "" }},
		{name: "missing generation", edit: func(plan *Plan) { plan.Generation = "" }},
		{name: "missing fence marker", edit: func(plan *Plan) { plan.FenceMarker = "" }},
		{name: "unknown status", edit: func(plan *Plan) { plan.Status = Status("blocked") }},
		{name: "missing created at", edit: func(plan *Plan) { plan.CreatedAt = time.Time{} }},
		{name: "updated before created", edit: func(plan *Plan) { plan.UpdatedAt = plan.CreatedAt.Add(-time.Second) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := valid
			tt.edit(&plan)
			if err := plan.Validate(); err == nil {
				t.Fatal("Validate succeeded, want error")
			}
		})
	}
}

func TestValidateIDAcceptsSafeOpaqueJVSPlanIDs(t *testing.T) {
	valid := []string{
		"b644aec4-bcb6-4480-b5fa-a283927dd3cd",
		"plan_001",
		"rp_123",
		"a.b:c-1_2",
	}
	for _, id := range valid {
		t.Run(id, func(t *testing.T) {
			if err := ValidateID(id); err != nil {
				t.Fatalf("ValidateID(%q): %v", id, err)
			}
		})
	}
}

func TestValidateIDRejectsUnsafeRestorePlanIDs(t *testing.T) {
	unsafe := []string{
		"",
		" ",
		" plan_001",
		"plan_001 ",
		"plan/001",
		"../plan_001",
		"plan;rm",
		"plan$(rm)",
		"plan*001",
		"plan\n001",
		strings.Repeat("a", 129),
	}
	for _, id := range unsafe {
		t.Run("unsafe", func(t *testing.T) {
			if err := ValidateID(id); err == nil {
				t.Fatalf("ValidateID(%q) succeeded, want error", id)
			}
		})
	}
}

func TestActiveStatusesMatchRestorePlanLifecycleContract(t *testing.T) {
	active := []Status{
		StatusPending,
		StatusConsuming,
		StatusDiscarding,
		StatusOperatorInterventionRequired,
	}
	for _, status := range active {
		if !status.Active() {
			t.Fatalf("%s Active() = false, want true", status)
		}
		plan := Plan{Status: status}
		if !plan.Active() {
			t.Fatalf("plan status %s Active() = false, want true", status)
		}
		if !Active(status) {
			t.Fatalf("Active(%s) = false, want true", status)
		}
	}

	terminal := []Status{StatusConsumed, StatusDiscarded}
	for _, status := range terminal {
		if status.Active() {
			t.Fatalf("%s Active() = true, want false", status)
		}
	}
}

func TestValidTransitionAllowsOnlyMinimumLifecycleEdges(t *testing.T) {
	allowed := [][2]Status{
		{StatusPending, StatusConsuming},
		{StatusConsuming, StatusConsumed},
		{StatusPending, StatusDiscarding},
		{StatusDiscarding, StatusDiscarded},
		{StatusPending, StatusOperatorInterventionRequired},
		{StatusConsuming, StatusOperatorInterventionRequired},
		{StatusDiscarding, StatusOperatorInterventionRequired},
	}
	for _, edge := range allowed {
		if !ValidTransition(edge[0], edge[1]) {
			t.Fatalf("ValidTransition(%s, %s) = false, want true", edge[0], edge[1])
		}
	}

	rejected := [][2]Status{
		{StatusPending, StatusConsumed},
		{StatusPending, StatusDiscarded},
		{StatusConsumed, StatusPending},
		{StatusConsumed, StatusOperatorInterventionRequired},
		{StatusDiscarded, StatusPending},
		{StatusOperatorInterventionRequired, StatusDiscarding},
		{StatusPending, StatusPending},
		{Status("unknown"), StatusPending},
		{StatusPending, Status("unknown")},
	}
	for _, edge := range rejected {
		if ValidTransition(edge[0], edge[1]) {
			t.Fatalf("ValidTransition(%s, %s) = true, want false", edge[0], edge[1])
		}
	}
}
