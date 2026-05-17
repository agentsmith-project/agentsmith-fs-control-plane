package repoexec

import (
	"strings"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/jvsrunner"
)

func directDoctorAllowsMutation(doctor jvsrunner.DirectDoctorSummary) bool {
	if directDoctorHasNonBlockingCleanupEvidence(doctor) {
		return true
	}
	return doctor.Healthy &&
		directDoctorMetadataReady(doctor.MetadataState) &&
		strings.TrimSpace(doctor.Journal) == "clean" &&
		directDoctorRecoveryNone(doctor.Recovery)
}

func directDoctorHasNonBlockingCleanupEvidence(doctor jvsrunner.DirectDoctorSummary) bool {
	if doctor.Healthy ||
		doctor.FindingCount != 1 ||
		len(doctor.Findings) != 1 ||
		strings.TrimSpace(doctor.Recovery) != "cleanup_pending" ||
		!directDoctorMetadataReady(doctor.MetadataState) ||
		strings.TrimSpace(doctor.Journal) != "clean" {
		return false
	}
	finding := doctor.Findings[0]
	return strings.TrimSpace(finding.Code) == "" &&
		strings.TrimSpace(finding.Severity) == "warning" &&
		strings.TrimSpace(finding.Message) == "direct restore cleanup pending" &&
		!finding.Retryable
}

func directDoctorMetadataReady(metadataState string) bool {
	return strings.TrimSpace(metadataState) == "ready"
}

func directDoctorRecoveryNone(recovery string) bool {
	switch strings.TrimSpace(recovery) {
	case "", "none":
		return true
	default:
		return false
	}
}
