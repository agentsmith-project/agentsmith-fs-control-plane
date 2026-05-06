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
	Status             Status
	CreatedAt          time.Time
	UpdatedAt          time.Time
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

func ValidateID(id string) error {
	if !safeOpaqueID(id) {
		return fmt.Errorf("invalid restore_plan_id %q", id)
	}
	return nil
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
