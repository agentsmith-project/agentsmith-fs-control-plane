package repoexec

import "github.com/agentsmith-project/agentsmith-fs-control-plane/internal/jvsrunner"

const cloneEvidenceOutputField = "clone_evidence"

func withCloneEvidenceProjection(values map[string]any, evidence []jvsrunner.CloneEvidence) map[string]any {
	projected := cloneEvidenceProjection(evidence)
	if len(projected) == 0 {
		return values
	}
	if values == nil {
		values = map[string]any{}
	}
	values[cloneEvidenceOutputField] = projected
	return values
}

func cloneEvidenceProjection(evidence []jvsrunner.CloneEvidence) []map[string]any {
	if len(evidence) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(evidence))
	for _, item := range evidence {
		out = append(out, map[string]any{
			"operation":   item.Operation,
			"phase":       item.Phase,
			"engine":      item.Engine,
			"status":      item.Status,
			"started_at":  item.StartedAt,
			"finished_at": item.FinishedAt,
			"duration_ms": item.DurationMs,
		})
	}
	return out
}

func mergeCloneEvidence(groups ...[]jvsrunner.CloneEvidence) []jvsrunner.CloneEvidence {
	var total int
	for _, group := range groups {
		total += len(group)
	}
	if total == 0 {
		return nil
	}
	out := make([]jvsrunner.CloneEvidence, 0, total)
	for _, group := range groups {
		out = append(out, group...)
	}
	return out
}
