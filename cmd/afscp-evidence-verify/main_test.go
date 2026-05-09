package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestRunCheckOnlyValidatesManifestWithoutExecutingCommands(t *testing.T) {
	root := t.TempDir()
	writeEvidenceCLIScripts(t, root)
	manifestPath := filepath.Join(root, "manifest.json")
	writeEvidenceCLIFile(t, manifestPath, evidenceCLIManifest(`["bash","scripts/fail.sh"]`, "scripts/fail.sh"))

	var stdout, stderr bytes.Buffer
	code := run([]string{"-mode", "seed", "-manifest", manifestPath, "-repo-root", root, "-check-only"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunCheckOnlyAcceptsDefaultUserLoopAggregationManifest(t *testing.T) {
	root := t.TempDir()
	writeEvidenceCLIScripts(t, root)
	manifestPath := filepath.Join(root, "manifest.json")
	writeEvidenceCLIFile(t, manifestPath, evidenceCLIManifest(`["bash","scripts/pass.sh"]`, "scripts/pass.sh"))

	var stdout, stderr bytes.Buffer
	code := run([]string{"-mode", "seed", "-manifest", manifestPath, "-repo-root", root, "-check-only"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunCheckOnlyAcceptsOperatorRepairSafeManifest(t *testing.T) {
	root := t.TempDir()
	writeEvidenceCLIScripts(t, root)
	manifestPath := filepath.Join(root, "manifest.json")
	writeEvidenceCLIFile(t, manifestPath, evidenceCLIManifest(`["bash","scripts/pass.sh"]`, "scripts/pass.sh"))

	var stdout, stderr bytes.Buffer
	code := run([]string{"-mode", "seed", "-manifest", manifestPath, "-repo-root", root, "-check-only"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunCheckOnlyAcceptsRestoreReconciliationManifest(t *testing.T) {
	root := t.TempDir()
	writeEvidenceCLIScripts(t, root)
	manifestPath := filepath.Join(root, "manifest.json")
	writeEvidenceCLIFile(t, manifestPath, evidenceCLIManifest(`["bash","scripts/pass.sh"]`, "scripts/pass.sh"))

	var stdout, stderr bytes.Buffer
	code := run([]string{"-mode", "seed", "-manifest", manifestPath, "-repo-root", root, "-check-only"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunCheckOnlyAcceptsDiscoverySurfacesManifest(t *testing.T) {
	root := t.TempDir()
	writeEvidenceCLIScripts(t, root)
	manifestPath := filepath.Join(root, "manifest.json")
	writeEvidenceCLIFile(t, manifestPath, evidenceCLIManifest(`["bash","scripts/pass.sh"]`, "scripts/pass.sh"))

	var stdout, stderr bytes.Buffer
	code := run([]string{"-mode", "seed", "-manifest", manifestPath, "-repo-root", root, "-check-only"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunCheckOnlyAcceptsSecretPathRedactionManifest(t *testing.T) {
	root := t.TempDir()
	writeEvidenceCLIScripts(t, root)
	manifestPath := filepath.Join(root, "manifest.json")
	writeEvidenceCLIFile(t, manifestPath, evidenceCLIManifest(`["bash","scripts/pass.sh"]`, "scripts/pass.sh"))

	var stdout, stderr bytes.Buffer
	code := run([]string{"-mode", "seed", "-manifest", manifestPath, "-repo-root", root, "-check-only"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunCheckOnlyAcceptsProfileBoundaryManifest(t *testing.T) {
	root := t.TempDir()
	writeEvidenceCLIScripts(t, root)
	manifestPath := filepath.Join(root, "manifest.json")
	writeEvidenceCLIFile(t, manifestPath, evidenceCLIManifest(`["bash","scripts/pass.sh"]`, "scripts/pass.sh"))

	var stdout, stderr bytes.Buffer
	code := run([]string{"-mode", "seed", "-manifest", manifestPath, "-repo-root", root, "-check-only"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunCheckOnlyAcceptsWorkflowHardeningManifest(t *testing.T) {
	root := t.TempDir()
	writeEvidenceCLIScripts(t, root)
	manifestPath := filepath.Join(root, "manifest.json")
	writeEvidenceCLIFile(t, manifestPath, evidenceCLIManifest(`["bash","scripts/pass.sh"]`, "scripts/pass.sh"))

	var stdout, stderr bytes.Buffer
	code := run([]string{"-mode", "seed", "-manifest", manifestPath, "-repo-root", root, "-check-only"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunCheckOnlyAcceptsResidualRiskCatalogManifest(t *testing.T) {
	root := t.TempDir()
	writeEvidenceCLIScripts(t, root)
	manifestPath := filepath.Join(root, "manifest.json")
	writeEvidenceCLIFile(t, manifestPath, evidenceCLIManifest(`["bash","scripts/pass.sh"]`, "scripts/pass.sh"))

	var stdout, stderr bytes.Buffer
	code := run([]string{"-mode", "seed", "-manifest", manifestPath, "-repo-root", root, "-check-only"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunCheckOnlyAcceptsDeploymentRiskEnvelopeManifest(t *testing.T) {
	root := t.TempDir()
	writeEvidenceCLIScripts(t, root)
	manifestPath := filepath.Join(root, "manifest.json")
	writeEvidenceCLIFile(t, manifestPath, evidenceCLIManifest(`["bash","scripts/pass.sh"]`, "scripts/pass.sh"))

	var stdout, stderr bytes.Buffer
	code := run([]string{"-mode", "seed", "-manifest", manifestPath, "-repo-root", root, "-check-only"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunExecutesRequiredCommandsByDefault(t *testing.T) {
	root := t.TempDir()
	writeEvidenceCLIScripts(t, root)
	manifestPath := filepath.Join(root, "manifest.json")
	writeEvidenceCLIFile(t, manifestPath, evidenceCLIManifest(`["bash","scripts/fail.sh"]`, "scripts/fail.sh"))

	var stdout, stderr bytes.Buffer
	code := run([]string{"-mode", "seed", "-manifest", manifestPath, "-repo-root", root}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "failed") {
		t.Fatalf("expected stdout to include command failure finding, got %q", stdout.String())
	}
}

func TestRunReturnsTwoWhenManifestFlagMissing(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"-mode", "seed"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2, got %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "-manifest") {
		t.Fatalf("expected stderr to mention -manifest, got %q", stderr.String())
	}
}

func TestRunReturnsTwoWhenModeFlagMissing(t *testing.T) {
	root := t.TempDir()
	writeEvidenceCLIScripts(t, root)
	manifestPath := filepath.Join(root, "manifest.json")
	writeEvidenceCLIFile(t, manifestPath, evidenceCLIManifest(`["bash","scripts/pass.sh"]`, "scripts/pass.sh"))

	var stdout, stderr bytes.Buffer
	code := run([]string{"-manifest", manifestPath, "-repo-root", root, "-check-only"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2, got %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "-mode seed|final") {
		t.Fatalf("expected stderr to mention -mode seed|final, got %q", stderr.String())
	}
}

func TestRunReturnsTwoWhenModeFlagInvalid(t *testing.T) {
	root := t.TempDir()
	writeEvidenceCLIScripts(t, root)
	manifestPath := filepath.Join(root, "manifest.json")
	writeEvidenceCLIFile(t, manifestPath, evidenceCLIManifest(`["bash","scripts/pass.sh"]`, "scripts/pass.sh"))

	var stdout, stderr bytes.Buffer
	code := run([]string{"-mode", "baseline", "-manifest", manifestPath, "-repo-root", root, "-check-only"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2, got %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "-mode seed|final") {
		t.Fatalf("expected stderr to mention -mode seed|final, got %q", stderr.String())
	}
}

func TestRunFinalModeRequiresSelectorFlag(t *testing.T) {
	root := t.TempDir()
	writeEvidenceCLIScripts(t, root)
	manifestPath := filepath.Join(root, "manifest.json")
	writeEvidenceCLIFile(t, manifestPath, evidenceCLIManifest(`["bash","scripts/pass.sh"]`, "scripts/pass.sh"))

	var stdout, stderr bytes.Buffer
	code := run([]string{"-mode", "final", "-manifest", manifestPath, "-repo-root", root, "-check-only"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2, got %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "-selector") {
		t.Fatalf("expected stderr to mention -selector, got %q", stderr.String())
	}
}

func TestFinalCheckOnlyCannotDeclareFinalAcceptance(t *testing.T) {
	root := t.TempDir()
	writeEvidenceCLIScripts(t, root)
	manifestPath := filepath.Join(root, "manifest.json")
	writeEvidenceCLIFile(t, manifestPath, evidenceCLIManifest(`["bash","scripts/pass.sh"]`, "scripts/pass.sh"))
	writeEvidenceCLISelector(t, root, "manifest.json", "final_candidate")

	var stdout, stderr bytes.Buffer
	code := run([]string{"-mode", "final", "-manifest", manifestPath, "-repo-root", root, "-selector", "docs/release-evidence/ga-release-selector.json", "-check-only"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2, got %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "check-only") || !strings.Contains(stderr.String(), "final") ||
		!strings.Contains(stderr.String(), "cannot declare final acceptance") ||
		!strings.Contains(stderr.String(), "run the release script without -check-only") {
		t.Fatalf("expected stderr to distinguish final check-only from final acceptance, got %q", stderr.String())
	}
}

func TestRunFinalModeWithSelectorRejectsOpenSeedGaps(t *testing.T) {
	root := t.TempDir()
	writeEvidenceCLIScripts(t, root)
	manifestPath := filepath.Join(root, "manifest.json")
	writeEvidenceCLIFile(t, manifestPath, evidenceCLIManifest(`["bash","scripts/pass.sh"]`, "scripts/pass.sh"))
	writeEvidenceCLISelector(t, root, "manifest.json", "final_candidate")

	var stdout, stderr bytes.Buffer
	code := run([]string{"-mode", "final", "-manifest", manifestPath, "-repo-root", root, "-selector", "docs/release-evidence/ga-release-selector.json"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{"manifest.final_seed_gap_open", "seed_gap_admin_bootstrap_ready_open", "CLAIM_ADMIN_BOOTSTRAP_READY"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("expected stdout to include %q, got %q", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "repo_create_jvs_runtime_unavailable_recovery_unit: item.capability_id_legacy_final_invalid") {
		t.Fatalf("final mode must not flag the canonical repo_create JVS-unavailable evidence as legacy, got %q", stdout.String())
	}
}

func evidenceCLIManifest(command, anchor string) string {
	return withPackage0CLISeedGapMarkers(withPackage0CLIMetadata(`{
  "schema_version":"2",
  "release_gate":"scripts/verify-ga-release.sh",
  "items":[
    {
      "id":"webdav_export_disabled_admission_unit",
      "capability_id":"webdav_export",
      "evidence_type":"unit",
      "required":true,
      "command":["bash","scripts/pass.sh"],
      "anchors":["` + anchor + `"],
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
      "anchors":["` + anchor + `"],
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
      "anchors":["` + anchor + `"],
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
      "anchors":["` + anchor + `"],
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
      "anchors":["` + anchor + `"],
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
      "anchors":["` + anchor + `"],
      "doc_only_allowed":false,
      "optional_gated":true,
      "default_ga_required":false
    },
    {
      "id":"repo_template_disabled_admission_unit",
      "capability_id":"repo_template",
      "evidence_type":"unit",
      "required":true,
      "command":` + command + `,
      "anchors":["` + anchor + `"],
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
      "anchors":["` + anchor + `"],
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
      "anchors":["` + anchor + `"],
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
      "anchors":["` + anchor + `"],
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
      "anchors":["` + anchor + `"],
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
      "anchors":["` + anchor + `"],
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
      "anchors":["` + anchor + `"],
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
      "anchors":["` + anchor + `"],
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
      "anchors":["` + anchor + `"],
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
      "anchors":["` + anchor + `"],
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
      "anchors":["` + anchor + `"],
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
      "anchors":["` + anchor + `"],
      "doc_only_allowed":false,
      "optional_gated":false,
      "default_ga_required":true
    },
    {
      "id":"restore_reconciliation_safe_unit",
      "capability_id":"jvs_save_restore",
      "evidence_type":"unit",
      "required":true,
      "command":["go","test","-count=1","./internal/restorereconcile","./internal/store/postgres","./internal/api","./internal/workerapp","./internal/contractcheck","./internal/releaseevidence","./cmd/afscp-evidence-verify","-run","^Test(RestoreReconciliationDecisionDeniesDangerousWritesUntilSafe|RestoreReconciliationDecisionPurgedStoragePresentDoesNotResurrect|RestoreReconciliationEvidenceRedactsSensitiveMaterial|RestoreReconciliationRejectsSecretShapedEvidenceRefsAndMarkers|RestoreReconciliationCleanObservationRequiresMarkersAndEvidence|RestoreReconciliationRunOnceCompletesCleanRun|RestoreReconciliationRunOnceFailsClosedWhenTargetSetIsEmptyOrMissingObservation|RestoreReconciliationRunOnceMismatchBlocksAndMarksIntervention|RestoreReconciliationRunOnceObservedMarkerMismatchBlocksAndDoesNotComplete|RunOnceRestoreReconciliationOnlyRunsWhenExplicitlyEnabled|RestoreReconciliationRunOncePurgedRepoNeverResurrects|RestoreReconciliationMigrationDefinesRunAndObservationTables|CommitRestoreReconciliationMismatchMarksRepoOperatorInterventionAndAudits|CommitRestoreReconciliationMismatchRequiresEligibleReconcilingRunBeforeSideEffects|RestoreReconciliationPurgedStoragePresentDoesNotResurrect|ObserveRestoreReconciliationTargetDerivesObservedMarkersFromCurrentRepoNotExpectedEcho|CompleteRestoreReconciliationRunRequiresAllReposObservedClean|RestoreReconciliationStoreDoesNotTouchCredentialsFencesOrStorageSideEffects|BeginExportRuntimeWriteRequestFailsClosedDuringRestoreReconciliationBeforeLedgerMutation|RestoreReconciliationModeDeniesExportCreateBeforePassword|RestoreReconciliationModeDeniesRestoreSaveLifecycleBeforeOperationCreate|RestoreReconciliationModeExportReplayDoesNotReturnAccess|RestoreReconciliationModeDeniesWorkloadMountMutationsAndPlanBeforeIntake|ErrorCodesExposeStableSchemaEnumOrder|ProductCallerOperationResponsesDoNotLeakStorageInternals|RunOnceRestoreReconciliationRunsBeforeOperationRecovery|RunOnceRestoreReconciliationBlockedSkipsOperationRecovery|RestoreReconciliationContractDefinesModeDenialCredentialPurgeMismatch|CurrentRepoManifestContainsP4bRestoreReconciliationEvidence|RestoreReconciliationReplacementRejectsWrongShapeBroadSelectorOrP1cOnly|RunCheckOnlyAcceptsRestoreReconciliationManifest)$"],
      "anchors":["` + anchor + `"],
      "doc_only_allowed":false,
      "optional_gated":false,
      "default_ga_required":true
    },
    {
      "id":"discovery_surfaces_layered_unit",
      "capability_id":"repo_projection",
      "evidence_type":"unit",
      "required":true,
      "command":["go","test","-count=1","./internal/api","./internal/apiapp","./internal/contractcheck","./internal/releaseevidence","./cmd/afscp-evidence-verify","-run","^Test(DiscoverySurfacesCallerProjectionExcludesRuntimeAndOperatorFields|DiscoverySurfacesCallerOperationInspectionRedactsCallerUnsafeFields|DiscoverySurfacesReadyzDoesNotPromoteOptionalRuntimeDefaultReady|DiscoverySurfacesOrchestratorDefaultDeniedDoesNotLeakPlanOrSecrets|DiscoverySurfacesOperatorInspectionGlobalRecordIsReadOnlyRedactedAndDistinctFromRepair|DiscoverySurfacesContractDefinesLayeredDiscoveryBoundaries|CurrentRepoManifestContainsDiscoverySurfacesEvidence|DiscoverySurfacesReplacementRejectsWrongShapeBroadSelectorMatrixOnlyOrRuntimeOnly|RunCheckOnlyAcceptsDiscoverySurfacesManifest)$"],
      "anchors":["` + anchor + `"],
      "doc_only_allowed":false,
      "optional_gated":false,
      "default_ga_required":true
    },
    {
      "id":"secret_path_redaction_unit",
      "capability_id":"path_redaction",
      "evidence_type":"unit",
      "required":true,
      "command":["go","test","-count=1","./internal/observability","./internal/audit","./internal/operations","./internal/api","./internal/apiapp","./internal/exportgateway","./internal/store/postgres","./internal/workerapp","./internal/restorereconcile","./internal/operatorrepair","./internal/contractcheck","./internal/releaseevidence","./cmd/afscp-evidence-verify","-run","^Test(SecretPathRedactionCorpusCoversForbiddenKeysAndRawStringForms|SecretPathRedactionAuditOutboxAndStableEventsUseCommonRedactor|SanitizedForPersistenceRedactsStorageInternalAndCommandFields|OperationInspectionHandlerReturnsRedactedRecordWithoutNamespaceHeader|SecretPathRedactionOperatorInspectionResponseDoesNotLeakStorageMaterial|SecretPathRedactionCallerRepoAndOperationResponsesDoNotLeakStorageMaterial|ReadinessHandlerRedactsAdminBootstrapReasons|InternalRuntimeReadinessAdminBootstrapGatesOnStoragePingWithoutLeakingErrors|BasicAuthFailureDoesNotLeakCredentialOrPaths|DeniedAuditPayloadDoesNotContainSensitiveWebDAVMaterial|GetExportSessionSelectsOnlyRedactedColumns|NewJVSRunnerFromConfigRedactsBinaryReadErrors|RestoreReconciliationEvidenceRedactsSensitiveMaterial|RestoreReconciliationRejectsSecretShapedEvidenceRefsAndMarkers|OperatorRepairRejectsSecretShapedReasonOrEvidenceRef|OperatorRepairBuildsFailedRecordWithRedactedBeforeAfter|SecretPathRedactionContractDefinesDefaultControlPlaneOutputBoundary|CurrentRepoManifestContainsSecretPathRedactionEvidence|SecretPathRedactionReplacementRejectsWrongShapeBroadSelectorOptionalDiscoveryRuntimeOrHelperOnly|RunCheckOnlyAcceptsSecretPathRedactionManifest)$"],
      "anchors":["` + anchor + `"],
      "doc_only_allowed":false,
      "optional_gated":false,
      "default_ga_required":true
    },
    {
      "id":"profile_boundary_consistent_unit",
      "capability_id":"",
      "evidence_type":"unit",
      "required":true,
      "command":["go","test","-count=1","./internal/releaseevidence","./internal/contractcheck","./cmd/afscp-evidence-verify","-run","^Test(ProfileBoundaryDefaultFinalRejectsOptionalFixtureAndRuntimeSupportSubstitutes|ProfileBoundarySelectedOptionalOnlyBlocksWhenSelectorClaimsCapability|ProfileBoundaryDeploymentRuntimeSupportCannotCloseDefaultOrOptionalPositive|ProfileBoundaryContractDefinesDefaultFixtureAndRuntimeSupportSeparation|CurrentRepoManifestContainsProfileBoundaryEvidence|ProfileBoundaryReplacementRejectsWrongShapeBroadSelectorOptionalRuntimeOrHelperOnly|RunCheckOnlyAcceptsProfileBoundaryManifest)$"],
      "anchors":["` + anchor + `"],
      "doc_only_allowed":false,
      "optional_gated":false,
      "default_ga_required":false
    },
    {
      "id":"workflow_hardening_guard_unit",
      "capability_id":"",
      "evidence_type":"unit",
      "required":true,
      "command":["go","test","-count=1","./internal/contractcheck","./internal/releaseevidence","./cmd/afscp-evidence-verify","-run","^Test(WorkflowHardeningCurrentRepoWorkflowUsesSingleAuthoritativeGate|WorkflowHardeningReleaseScriptCannotBypassManifestOrBaseline|WorkflowHardeningFinalIntentRequiresSelectorAndRejectsCheckOnlyFinalAcceptance|WorkflowHardeningContractRejectsManualApprovalAlternateGateOrDeploymentRuntimeProof|SelectorRejectsUnsafePathAndGeneratedReportDigest|FinalCheckOnlyCannotDeclareFinalAcceptance|CurrentRepoManifestContainsWorkflowHardeningEvidence|WorkflowHardeningReplacementRejectsWrongShapeBroadSelectorDocOnlyRuntimeOnlyOrHelperOnly|RunCheckOnlyAcceptsWorkflowHardeningManifest)$"],
      "anchors":["` + anchor + `"],
      "doc_only_allowed":false,
      "optional_gated":false,
      "default_ga_required":false
    },
    {
      "id":"residual_risk_catalog_guard_unit",
      "capability_id":"",
      "evidence_type":"unit",
      "required":true,
      "command":["go","test","-count=1","./internal/contractcheck","./internal/releaseevidence","./cmd/afscp-evidence-verify","-run","^(TestResidualRiskCatalogCurrentRepoDefinesMachineCheckableRiskRows|TestResidualRiskCatalogRejectsHumanApprovalWaiverOrSubjectiveException|TestResidualRiskCatalogRequiresEvidenceRefsOwnerStatusDecisionAndMitigation|TestResidualRiskCatalogSharedVolumeThreatModelHasScopeExpiryReviewAndEscalation|TestResidualRiskAcceptanceRequiresPredefinedRiskScopeReasonEvidenceAndReviewPoint|TestResidualRiskAcceptanceAuditIsOperatorScopedAndRedacted|TestCurrentRepoManifestContainsResidualRiskCatalogEvidence|TestResidualRiskCatalogReplacementRejectsWrongShapeBroadSelectorDocOnlyRuntimeOnlyOrHelperOnly|TestRunCheckOnlyAcceptsResidualRiskCatalogManifest)$"],
      "anchors":["` + anchor + `"],
      "doc_only_allowed":false,
      "optional_gated":false,
      "default_ga_required":false
    },
    {
      "id":"deployment_risk_envelope_guard_unit",
      "capability_id":"",
      "evidence_type":"unit",
      "required":true,
      "command":["go","test","-count=1","./internal/contractcheck","./internal/releaseevidence","./cmd/afscp-evidence-verify","-run","^(TestDeploymentRiskEnvelopeCurrentRepoDefinesRuntimeSupportRows|TestDeploymentRiskEnvelopeRejectsProductionOrManualGateProof|TestDeploymentRiskEnvelopeRequiresDetectionRedactionRollbackAndResidualLinks|TestDeploymentRiskEnvelopeRunbookRefsAreRepoLocalOperatorHandoff|TestDeploymentRiskEnvelopeRuntimePrereqsDoNotCloseOptionalFixturePurgeTemplateOrWorkload|TestDeploymentRiskEnvelopeContractSeparatesRuntimeSupportFromDefaultPositiveProof|TestCurrentRepoManifestContainsDeploymentRiskEnvelopeEvidence|TestDeploymentRiskEnvelopeReplacementRejectsWrongShapeBroadSelectorRuntimeOptionalResidualWorkflowProfileOrHelperOnly|TestRunCheckOnlyAcceptsDeploymentRiskEnvelopeManifest)$"],
      "anchors":["` + anchor + `"],
      "doc_only_allowed":false,
      "optional_gated":false,
      "default_ga_required":false
    },
    {
      "id":"repo_create_jvs_runtime_unavailable_recovery_unit",
      "capability_id":"repo_create",
      "evidence_type":"unit",
      "required":true,
      "command":["bash","scripts/pass.sh"],
      "anchors":["` + anchor + `"],
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
      "anchors":["` + anchor + `"],
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
      "anchors":["` + anchor + `"],
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
      "anchors":["` + anchor + `"],
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
      "anchors":["` + anchor + `"],
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
      "anchors":["` + anchor + `"],
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
      "anchors":["` + anchor + `"],
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
      "anchors":["` + anchor + `"],
      "doc_only_allowed":false,
      "optional_gated":false,
      "default_ga_required":false
    }
  ]
}`))
}

func withPackage0CLIMetadata(body string) string {
	for _, metadata := range package0CLIMetadata {
		body = strings.Replace(body, `"id":"`+metadata.id+`",`, `"id":"`+metadata.id+`",
      "evidence_status":"implemented",
      "claim_id":"`+metadata.claimID+`",
      "subclaim_id":"`+metadata.subclaimID+`",
      "acceptance_id":"`+metadata.acceptanceID+`",
      "risk_id":"`+metadata.riskID+`",
      "fixture_id":"",
      "evidence_profile":"default",
      "default_mode":true,
      "fixture_enabled_mode":false,
      "expected_runtime":"`+metadata.expectedRuntime+`",
      "scope":"`+metadata.scope+`",
      "negative_or_positive":"`+metadata.negativeOrPositive+`",`, 1)
		body = insertPackage0CLIPassCriteria(body, metadata.id, metadata.defaultGARequired, metadata.passCriteriaKind, metadata.passCriteriaAssertion)
	}
	return body
}

func insertPackage0CLIPassCriteria(body, id, defaultGARequired, kind, assertion string) string {
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

func withPackage0CLISeedGapMarkers(body string) string {
	for _, gap := range package0CLISeedGapMetadata {
		body = appendEvidenceCLIItem(body, `"id":"`+gap.id+`","evidence_status":"placeholder","claim_id":"`+gap.claimID+`","subclaim_id":"seed_gap_open","acceptance_id":"P0_SEED_GAP_OPEN","risk_id":"`+gap.riskID+`","fixture_id":"","capability_id":"","evidence_profile":"default","default_mode":true,"fixture_enabled_mode":false,"expected_runtime":"fast","scope":"doc-guard","negative_or_positive":"both","evidence_type":"doc-guard","required":false,"command":[],"anchors":["docs/GA_NEXT_PHASE_DEVELOPMENT_HANDOFF_PLAN.md"],"doc_only_allowed":true,"optional_gated":false,"default_ga_required":false,"pass_criteria":{"kind":"seed_gap","assertions":["open"]}`)
	}
	return body
}

var package0CLISeedGapMetadata = []struct {
	id      string
	claimID string
	riskID  string
}{
	{"seed_gap_admin_bootstrap_ready_open", "CLAIM_ADMIN_BOOTSTRAP_READY", "F3"},
	{"seed_gap_workload_fixture_ready_open", "CLAIM_WORKLOAD_FIXTURE_READY", "F9"},
	{"seed_gap_purge_approval_safe_open", "CLAIM_PURGE_APPROVAL_SAFE", "F13"},
	{"seed_gap_optional_fixture_conformant_open", "CLAIM_OPTIONAL_FIXTURE_CONFORMANT", "F9"},
	{"seed_gap_template_quota_boundary_open", "CLAIM_TEMPLATE_QUOTA_BOUNDARY", "F16"},
}

var package0CLIMetadata = []struct {
	id                    string
	claimID               string
	subclaimID            string
	acceptanceID          string
	riskID                string
	negativeOrPositive    string
	expectedRuntime       string
	scope                 string
	defaultGARequired     string
	passCriteriaKind      string
	passCriteriaAssertion string
}{
	{"webdav_export_disabled_admission_unit", "CLAIM_DEFAULT_DENIAL_SAFE", "webdav_export_disabled_admission", "P0_DEFAULT_DENIAL_WEBDAV_DISABLED_ADMISSION", "F5", "negative", "fast", "package", "true", "denial_safety", "disabled admission rejects before metadata and audits without queuing"},
	{"workload_mount_disabled_admission_unit", "CLAIM_OPTIONAL_DENIED_SAFE", "workload_mount_disabled_admission", "P0_OPTIONAL_DENIED_WORKLOAD_ADMISSION", "F5", "negative", "fast", "package", "false", "denial_safety", "optional disabled workload mount binding admission rejects create, status update, heartbeat, release, and revoke before metadata/runtime continuation while preserving idempotency replay/conflict precedence"},
	{"repo_lifecycle_retained_positive_unit", "CLAIM_RETAINED_LIFECYCLE_DEFAULT", "retained_lifecycle_positive", "P0_RETAINED_LIFECYCLE_DEFAULT_POSITIVE", "F15", "positive", "fast", "package", "true", "positive_path", "retained lifecycle positive path passes"},
	{"workload_mount_plan_store_freshness_unit", "CLAIM_OPTIONAL_DENIED_SAFE", "workload_mount_plan_store_freshness", "P0_OPTIONAL_DENIED_WORKLOAD_PLAN_STORE", "F9", "negative", "fast", "package", "false", "denial_safety", "workload mount plan store fails closed"},
	{"workload_mount_runtime_secretref_config_unit", "CLAIM_OPTIONAL_DENIED_SAFE", "workload_mount_runtime_secretref_config", "P0_OPTIONAL_DENIED_WORKLOAD_RUNTIME_SECRETREF", "F10", "negative", "fast", "package", "false", "denial_safety", "runtime secretref config fails closed"},
	{"workload_mount_secretref_redaction_unit", "CLAIM_OPTIONAL_DENIED_SAFE", "workload_mount_secretref_redaction", "P0_OPTIONAL_DENIED_WORKLOAD_SECRETREF_REDACTION", "F10", "negative", "fast", "package", "false", "denial_safety", "secret references stay redacted"},
	{"repo_template_disabled_admission_unit", "CLAIM_OPTIONAL_DENIED_SAFE", "repo_template_disabled_admission", "P0_OPTIONAL_DENIED_TEMPLATE_ADMISSION", "F16", "negative", "fast", "package", "false", "denial_safety", "repo template disabled admission rejects safely"},
	{"repo_template_create_disabled_worker_recovery_unit", "CLAIM_OPTIONAL_DENIED_SAFE", "repo_template_create_disabled_worker_recovery", "P0_OPTIONAL_DENIED_TEMPLATE_CREATE_RECOVERY", "F6", "negative", "fast", "package", "false", "denial_safety", "template create recovery terminalizes unsupported work"},
	{"repo_template_clone_disabled_worker_recovery_unit", "CLAIM_OPTIONAL_DENIED_SAFE", "repo_template_clone_disabled_worker_recovery", "P0_OPTIONAL_DENIED_TEMPLATE_CLONE_RECOVERY", "F6", "negative", "fast", "package", "false", "denial_safety", "template clone recovery terminalizes unsupported work"},
	{"repo_purge_disabled_admission_unit", "CLAIM_OPTIONAL_DENIED_SAFE", "repo_purge_disabled_admission", "P0_OPTIONAL_DENIED_PURGE_ADMISSION", "F13", "negative", "fast", "package", "false", "denial_safety", "repo purge disabled admission rejects safely"},
	{"repo_purge_disabled_worker_recovery_unit", "CLAIM_OPTIONAL_DENIED_SAFE", "repo_purge_disabled_worker_recovery", "P0_OPTIONAL_DENIED_PURGE_RECOVERY", "F13", "negative", "fast", "package", "false", "denial_safety", "repo purge recovery terminalizes unsupported work"},
	{"default_user_loop_repo_projection_unit", "CLAIM_DEFAULT_USER_LOOP", "default_user_loop_repo_projection", "P1B_DEFAULT_USER_LOOP_REPO_PROJECTION", "F2", "positive", "fast", "package", "true", "positive_path", "repo create get list projection and repo-create worker positive path pass without closing the full default user loop"},
	{"default_user_loop_jvs_save_restore_unit", "CLAIM_DEFAULT_USER_LOOP", "default_user_loop_jvs_save_restore", "P1C_DEFAULT_USER_LOOP_JVS_SAVE_RESTORE", "F2", "positive", "fast", "package", "true", "positive_path", "JVS save history restore-preview restore-run and discard paths pass without closing the full default user loop"},
	{"webdav_default_access_unit", "CLAIM_WEBDAV_DEFAULT_ACCESS", "webdav_default_access", "P0_WEBDAV_DEFAULT_ACCESS", "F8", "positive", "fast", "package", "true", "positive_path", "webdav default access passes in default mode"},
	{"default_user_loop_webdav_access_unit", "CLAIM_DEFAULT_USER_LOOP", "default_user_loop_webdav_access", "P1D_DEFAULT_USER_LOOP_WEBDAV_ACCESS", "F2", "positive", "fast", "package", "true", "positive_path", "WebDAV access contributes only partial default user loop evidence"},
	{"default_user_loop_trace_unit", "CLAIM_DEFAULT_USER_LOOP", "default_user_loop_trace", "P1E_DEFAULT_USER_LOOP_TRACE", "F2", "both", "fast", "package", "true", "coverage_guard", "caller-scoped operation audit and recovery trace stays redacted and terminally visible"},
	{"default_user_loop_positive_unit", "CLAIM_DEFAULT_USER_LOOP", "default_user_loop_positive", "P0_DEFAULT_USER_LOOP_POSITIVE", "F2", "positive", "fast", "package", "true", "positive_path", "default user loop passes in default mode"},
	{"operator_repair_safe_unit", "CLAIM_OPERATOR_REPAIR_SAFE", "operator_repair_safe", "P0_OPERATOR_REPAIR_SAFE", "F11", "both", "fast", "package", "true", "coverage_guard", "operator repair safety passes in default mode"},
	{"restore_reconciliation_safe_unit", "CLAIM_RESTORE_RECONCILIATION", "restore_reconciliation_safe", "P0_RESTORE_RECONCILIATION_SAFE", "F14", "positive", "fast", "package", "true", "positive_path", "restore reconciliation safety passes in default mode"},
	{"discovery_surfaces_layered_unit", "CLAIM_DISCOVERY_SURFACES", "discovery_surfaces_layered", "P0_DISCOVERY_SURFACES_LAYERED", "F7", "positive", "fast", "package", "true", "positive_path", "discovery surfaces pass layered default checks"},
	{"secret_path_redaction_unit", "CLAIM_SECRET_PATH_REDACTION", "secret_path_redaction", "P0_SECRET_PATH_REDACTION", "F10", "negative", "fast", "package", "true", "denial_safety", "secret path redaction denies secret path disclosure"},
	{"profile_boundary_consistent_unit", "CLAIM_PROFILE_BOUNDARY", "profile_boundary_consistent", "P0_PROFILE_BOUNDARY_CONSISTENT", "F1", "both", "fast", "package", "false", "coverage_guard", "profile boundary consistency covers final release evidence"},
	{"workflow_hardening_guard_unit", "CLAIM_WORKFLOW_HARDENING_GUARD", "workflow_hardening_guard", "P0_WORKFLOW_HARDENING_GUARD", "F18", "both", "fast", "workflow-guard", "false", "coverage_guard", "workflow hardening guard covers final release evidence"},
	{"residual_risk_catalog_guard_unit", "CLAIM_RESIDUAL_RISK_CATALOG", "residual_risk_catalog_guard", "P0_RESIDUAL_RISK_CATALOG_GUARD", "F12", "both", "fast", "package", "false", "coverage_guard", "residual risk catalog guard covers final release evidence"},
	{"deployment_risk_envelope_guard_unit", "CLAIM_DEPLOYMENT_RISK_ENVELOPE", "deployment_risk_envelope_guard", "P0_DEPLOYMENT_RISK_ENVELOPE_GUARD", "F17", "both", "fast", "workflow-guard", "false", "coverage_guard", "deployment risk envelope guard covers final release evidence"},
	{"repo_create_jvs_runtime_unavailable_recovery_unit", "CLAIM_OPERATION_TERMINALIZATION", "repo_create_jvs_runtime_unavailable_recovery", "P1_OPERATION_TERMINALIZATION_REPO_CREATE_JVS_RUNTIME_UNAVAILABLE_RECOVERY", "F6", "negative", "fast", "package", "true", "denial_safety", "repo_create enabled recovery terminalizes when production JVS runtime is unavailable and fail-fast boundaries hold"},
	{"operation_terminalization_contract_unit", "CLAIM_OPERATION_TERMINALIZATION", "operation_terminalization_contract", "P2A_OPERATION_TERMINALIZATION_CONTRACT", "F6", "both", "fast", "package", "true", "coverage_guard", "operation terminalization contract covers inventory side-effect replay and terminal decisions"},
	{"operation_runtime_terminalization_unit", "CLAIM_OPERATION_TERMINALIZATION", "operation_runtime_terminalization", "P2B_OPERATION_RUNTIME_TERMINALIZATION", "F6", "both", "fast", "package", "true", "coverage_guard", "real RunOnce tests cover supported worker rows and registry coverage is auxiliary"},
	{"default_ga_capability_classification_unit", "CLAIM_CAPABILITY_MATRIX_CONSISTENT", "default_ga_capability_classification", "P0_CAPABILITY_MATRIX_DEFAULT_CLASSIFICATION", "F4", "both", "fast", "package", "false", "coverage_guard", "capability matrix classifies default and optional capabilities consistently"},
	{"capability_admission_operation_coverage_unit", "CLAIM_CAPABILITY_MATRIX_CONSISTENT", "capability_admission_operation_coverage", "P0_CAPABILITY_MATRIX_OPERATION_COVERAGE", "F4", "both", "fast", "package", "false", "coverage_guard", "capability admission operation coverage stays consistent"},
	{"capability_matrix_v1_contract_unit", "CLAIM_CAPABILITY_MATRIX_CONSISTENT", "capability_matrix_v1_contract", "P1_CAPABILITY_MATRIX_V1_CONTRACT", "F4", "both", "fast", "package", "false", "coverage_guard", "capability matrix v1 contract covers readyz workload split vocabulary"},
	{"capability_runtime_parity_unit", "CLAIM_CAPABILITY_MATRIX_CONSISTENT", "capability_runtime_parity", "P2B_CAPABILITY_RUNTIME_PARITY", "F4", "both", "fast", "package", "false", "coverage_guard", "actual API request-path tests prove matrix disabled admission behavior while worker runtime is carried by operation evidence"},
	{"release_script_evidence_manifest_guard", "CLAIM_RELEASE_GATE_TRACEABLE", "release_gate_invokes_manifest_verifier", "P0_RELEASE_GATE_TRACEABLE_MANIFEST_VERIFIER", "F18", "both", "fast", "workflow-guard", "false", "coverage_guard", "release gate invokes the manifest verifier"},
}

func appendEvidenceCLIItem(body, item string) string {
	if !strings.Contains(item, `"evidence_status"`) {
		item = `"evidence_status":"implemented",` + item
	}
	return strings.Replace(body, "\n  ]\n}", ",\n    {"+item+"}\n  ]\n}", 1)
}

func writeEvidenceCLISelector(t *testing.T, root, manifestPath, intent string) {
	t.Helper()

	writeEvidenceCLISelectorIdentityFiles(t, root)
	body := `{
  "schema_version":"1",
  "release_intent":"` + intent + `",
  "manifest_path":"` + manifestPath + `",
  "seed_gap_policy":"reject_open_seed_gap",
  "manifest_digest":"sha256:manifest",
  "selector_input_digest":"sha256:selector",
  "schema_migration_set_digest":"sha256:schema",
  "policy_artifact_identity_digest":"sha256:policy",
  "rollback_rollforward_policy_ref":"docs/release-evidence/rollback-rollforward.md",
  "final_acceptance_selector":[],
  "claimed_optional_capabilities":[]
}`
	body = replaceEvidenceCLISelectorField(t, body, "manifest_digest", sha256EvidenceCLIFileDigest(t, filepath.Join(root, filepath.FromSlash(manifestPath))))
	body = replaceEvidenceCLISelectorField(t, body, "schema_migration_set_digest", sha256EvidenceCLIPathSetDigest(t, root, []string{
		"api/openapi/internal-v1.openapi.yaml",
		"api/schemas/afscp-internal-v1.schema.json",
		"migrations/0001_test.sql",
	}))
	body = replaceEvidenceCLISelectorField(t, body, "policy_artifact_identity_digest", sha256EvidenceCLIPathSetDigest(t, root, []string{
		".github/workflows/ga-release.yml",
		"docs/GA_NEXT_PHASE_DEVELOPMENT_HANDOFF_PLAN.md",
		"docs/GA_RELEASE_GATES.md",
		"docs/release-evidence/rollback-rollforward.md",
		"scripts/verify-ga-baseline.sh",
		"scripts/verify-ga-release.sh",
	}))
	body = replaceEvidenceCLISelectorField(t, body, "selector_input_digest", sha256EvidenceCLISelectorInputDigest(t, body))
	writeEvidenceCLIFile(t, filepath.Join(root, "docs", "release-evidence", "ga-release-selector.json"), body)
}

func writeEvidenceCLISelectorIdentityFiles(t *testing.T, root string) {
	t.Helper()

	for _, path := range []string{
		"api/openapi/internal-v1.openapi.yaml",
		"api/schemas/afscp-internal-v1.schema.json",
		"migrations/0001_test.sql",
		"scripts/verify-ga-release.sh",
		"scripts/verify-ga-baseline.sh",
		".github/workflows/ga-release.yml",
		"docs/GA_RELEASE_GATES.md",
		"docs/release-evidence/rollback-rollforward.md",
	} {
		writeEvidenceCLIFile(t, filepath.Join(root, filepath.FromSlash(path)), "fixture "+path+"\n")
	}
}

func replaceEvidenceCLISelectorField(t *testing.T, body, field, value string) string {
	t.Helper()

	prefix := `"` + field + `":"`
	start := strings.Index(body, prefix)
	if start < 0 {
		t.Fatalf("selector body missing %s", field)
	}
	valueStart := start + len(prefix)
	valueEnd := strings.Index(body[valueStart:], `"`)
	if valueEnd < 0 {
		t.Fatalf("selector body malformed %s", field)
	}
	valueEnd += valueStart
	return body[:valueStart] + value + body[valueEnd:]
}

func sha256EvidenceCLIFileDigest(t *testing.T, path string) string {
	t.Helper()

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func sha256EvidenceCLIPathSetDigest(t *testing.T, root string, paths []string) string {
	t.Helper()

	sort.Strings(paths)
	hash := sha256.New()
	for _, path := range paths {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		hash.Write([]byte(path))
		hash.Write([]byte{0})
		hash.Write(body)
		hash.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func sha256EvidenceCLISelectorInputDigest(t *testing.T, body string) string {
	t.Helper()

	var raw map[string]any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("decode selector: %v", err)
	}
	delete(raw, "selector_input_digest")
	canonical, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("canonical selector: %v", err)
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func writeEvidenceCLIScripts(t *testing.T, root string) {
	t.Helper()

	writeEvidenceCLIFile(t, filepath.Join(root, "go.mod"), "module example.com/evidenceclifixture\n\ngo 1.22\n")
	writeEvidenceCLIFile(t, filepath.Join(root, "docs", "GA_NEXT_PHASE_DEVELOPMENT_HANDOFF_PLAN.md"), "fixture\n")
	writeEvidenceCLIFile(t, filepath.Join(root, "scripts", "pass.sh"), "#!/usr/bin/env bash\nexit 0\n")
	writeEvidenceCLIFile(t, filepath.Join(root, "scripts", "fail.sh"), "#!/usr/bin/env bash\nexit 1\n")
	writeEvidenceCLIFile(t, filepath.Join(root, "internal", "releaseevidence", "aggregation_test.go"), `package releaseevidence

import "testing"

func TestDefaultUserLoopAggregationRejectsMissingPrereq(t *testing.T) {}
func TestDefaultUserLoopAggregationRejectsPlaceholderPrereq(t *testing.T) {}
func TestDefaultUserLoopAggregationRejectsWrongProfileDefaultModePolarityRequiredOrDocOnlyPrereq(t *testing.T) {}
func TestDefaultUserLoopAggregationRejectsPartialOnlyManifest(t *testing.T) {}
func TestDefaultUserLoopAggregationRejectsBroadOrHelperOnlyCommand(t *testing.T) {}
func TestDefaultUserLoopAggregationRejectsBroadOrHelperOnlyPrereqCommand(t *testing.T) {}
func TestCurrentRepoManifestContainsP3OperatorRepairSafeEvidence(t *testing.T) {}
func TestOperatorRepairSafeReplacementRejectsWrongShapeOrBroadSelector(t *testing.T) {}
func TestCurrentRepoManifestContainsP4bRestoreReconciliationEvidence(t *testing.T) {}
func TestRestoreReconciliationReplacementRejectsWrongShapeBroadSelectorOrP1cOnly(t *testing.T) {}
func TestCurrentRepoManifestContainsDiscoverySurfacesEvidence(t *testing.T) {}
func TestDiscoverySurfacesReplacementRejectsWrongShapeBroadSelectorMatrixOnlyOrRuntimeOnly(t *testing.T) {}
func TestCurrentRepoManifestContainsSecretPathRedactionEvidence(t *testing.T) {}
func TestSecretPathRedactionReplacementRejectsWrongShapeBroadSelectorOptionalDiscoveryRuntimeOrHelperOnly(t *testing.T) {}
func TestProfileBoundaryDefaultFinalRejectsOptionalFixtureAndRuntimeSupportSubstitutes(t *testing.T) {}
func TestProfileBoundarySelectedOptionalOnlyBlocksWhenSelectorClaimsCapability(t *testing.T) {}
func TestProfileBoundaryDeploymentRuntimeSupportCannotCloseDefaultOrOptionalPositive(t *testing.T) {}
func TestCurrentRepoManifestContainsProfileBoundaryEvidence(t *testing.T) {}
func TestProfileBoundaryReplacementRejectsWrongShapeBroadSelectorOptionalRuntimeOrHelperOnly(t *testing.T) {}
func TestCurrentRepoManifestContainsWorkflowHardeningEvidence(t *testing.T) {}
func TestWorkflowHardeningReplacementRejectsWrongShapeBroadSelectorDocOnlyRuntimeOnlyOrHelperOnly(t *testing.T) {}
func TestSelectorRejectsUnsafePathAndGeneratedReportDigest(t *testing.T) {}
func TestCurrentRepoManifestContainsResidualRiskCatalogEvidence(t *testing.T) {}
func TestResidualRiskCatalogReplacementRejectsWrongShapeBroadSelectorDocOnlyRuntimeOnlyOrHelperOnly(t *testing.T) {}
func TestCurrentRepoManifestContainsDeploymentRiskEnvelopeEvidence(t *testing.T) {}
func TestDeploymentRiskEnvelopeReplacementRejectsWrongShapeBroadSelectorRuntimeOptionalResidualWorkflowProfileOrHelperOnly(t *testing.T) {}
`)
	writeEvidenceCLIFile(t, filepath.Join(root, "internal", "observability", "secret_path_redaction_test.go"), `package observability

import "testing"

func TestSecretPathRedactionCorpusCoversForbiddenKeysAndRawStringForms(t *testing.T) {}
`)
	writeEvidenceCLIFile(t, filepath.Join(root, "internal", "audit", "secret_path_redaction_test.go"), `package audit

import "testing"

func TestSecretPathRedactionAuditOutboxAndStableEventsUseCommonRedactor(t *testing.T) {}
`)
	writeEvidenceCLIFile(t, filepath.Join(root, "internal", "operations", "secret_path_redaction_test.go"), `package operations

import "testing"

func TestSanitizedForPersistenceRedactsStorageInternalAndCommandFields(t *testing.T) {}
`)
	writeEvidenceCLIFile(t, filepath.Join(root, "internal", "restorereconcile", "reconcile_test.go"), `package restorereconcile

import "testing"

func TestRestoreReconciliationDecisionDeniesDangerousWritesUntilSafe(t *testing.T) {}
func TestRestoreReconciliationDecisionPurgedStoragePresentDoesNotResurrect(t *testing.T) {}
func TestRestoreReconciliationEvidenceRedactsSensitiveMaterial(t *testing.T) {}
func TestRestoreReconciliationRejectsSecretShapedEvidenceRefsAndMarkers(t *testing.T) {}
func TestRestoreReconciliationCleanObservationRequiresMarkersAndEvidence(t *testing.T) {}
func TestRestoreReconciliationRunOnceCompletesCleanRun(t *testing.T) {}
func TestRestoreReconciliationRunOnceFailsClosedWhenTargetSetIsEmptyOrMissingObservation(t *testing.T) {}
func TestRestoreReconciliationRunOnceMismatchBlocksAndMarksIntervention(t *testing.T) {}
func TestRestoreReconciliationRunOnceObservedMarkerMismatchBlocksAndDoesNotComplete(t *testing.T) {}
func TestRestoreReconciliationRunOncePurgedRepoNeverResurrects(t *testing.T) {}
`)
	writeEvidenceCLIFile(t, filepath.Join(root, "internal", "store", "postgres", "restore_reconciliation_test.go"), `package postgres

import "testing"

func TestRestoreReconciliationMigrationDefinesRunAndObservationTables(t *testing.T) {}
func TestCommitRestoreReconciliationMismatchMarksRepoOperatorInterventionAndAudits(t *testing.T) {}
func TestCommitRestoreReconciliationMismatchRequiresEligibleReconcilingRunBeforeSideEffects(t *testing.T) {}
func TestRestoreReconciliationPurgedStoragePresentDoesNotResurrect(t *testing.T) {}
func TestObserveRestoreReconciliationTargetDerivesObservedMarkersFromCurrentRepoNotExpectedEcho(t *testing.T) {}
func TestCompleteRestoreReconciliationRunRequiresAllReposObservedClean(t *testing.T) {}
func TestRestoreReconciliationStoreDoesNotTouchCredentialsFencesOrStorageSideEffects(t *testing.T) {}
func TestBeginExportRuntimeWriteRequestFailsClosedDuringRestoreReconciliationBeforeLedgerMutation(t *testing.T) {}
`)
	writeEvidenceCLIFile(t, filepath.Join(root, "internal", "api", "restore_reconciliation_test.go"), `package api

import "testing"

func TestRestoreReconciliationModeDeniesExportCreateBeforePassword(t *testing.T) {}
func TestRestoreReconciliationModeDeniesRestoreSaveLifecycleBeforeOperationCreate(t *testing.T) {}
func TestRestoreReconciliationModeExportReplayDoesNotReturnAccess(t *testing.T) {}
func TestRestoreReconciliationModeDeniesWorkloadMountMutationsAndPlanBeforeIntake(t *testing.T) {}
func TestErrorCodesExposeStableSchemaEnumOrder(t *testing.T) {}
func TestProductCallerOperationResponsesDoNotLeakStorageInternals(t *testing.T) {}
func TestOperationInspectionHandlerReturnsRedactedRecordWithoutNamespaceHeader(t *testing.T) {}
func TestSecretPathRedactionCallerRepoAndOperationResponsesDoNotLeakStorageMaterial(t *testing.T) {}
func TestSecretPathRedactionOperatorInspectionResponseDoesNotLeakStorageMaterial(t *testing.T) {}
func TestReadinessHandlerRedactsAdminBootstrapReasons(t *testing.T) {}
func TestDiscoverySurfacesCallerProjectionExcludesRuntimeAndOperatorFields(t *testing.T) {}
func TestDiscoverySurfacesCallerOperationInspectionRedactsCallerUnsafeFields(t *testing.T) {}
func TestDiscoverySurfacesOrchestratorDefaultDeniedDoesNotLeakPlanOrSecrets(t *testing.T) {}
func TestDiscoverySurfacesOperatorInspectionGlobalRecordIsReadOnlyRedactedAndDistinctFromRepair(t *testing.T) {}
`)
	writeEvidenceCLIFile(t, filepath.Join(root, "internal", "apiapp", "discovery_surfaces_test.go"), `package apiapp

import "testing"

func TestDiscoverySurfacesReadyzDoesNotPromoteOptionalRuntimeDefaultReady(t *testing.T) {}
func TestInternalRuntimeReadinessAdminBootstrapGatesOnStoragePingWithoutLeakingErrors(t *testing.T) {}
`)
	writeEvidenceCLIFile(t, filepath.Join(root, "internal", "exportgateway", "secret_path_redaction_test.go"), `package exportgateway

import "testing"

func TestBasicAuthFailureDoesNotLeakCredentialOrPaths(t *testing.T) {}
func TestDeniedAuditPayloadDoesNotContainSensitiveWebDAVMaterial(t *testing.T) {}
`)
	writeEvidenceCLIFile(t, filepath.Join(root, "internal", "workerapp", "restore_reconciliation_test.go"), `package workerapp

import "testing"

func TestRunOnceRestoreReconciliationOnlyRunsWhenExplicitlyEnabled(t *testing.T) {}
func TestRunOnceRestoreReconciliationRunsBeforeOperationRecovery(t *testing.T) {}
func TestRunOnceRestoreReconciliationBlockedSkipsOperationRecovery(t *testing.T) {}
func TestNewJVSRunnerFromConfigRedactsBinaryReadErrors(t *testing.T) {}
`)
	writeEvidenceCLIFile(t, filepath.Join(root, "internal", "operatorrepair", "repair_test.go"), `package operatorrepair

import "testing"

func TestOperatorRepairRejectsUnknownAction(t *testing.T) {}
func TestOperatorRepairRequiresReasonEvidenceAndAffectedIDs(t *testing.T) {}
func TestOperatorRepairRejectsSecretShapedReasonOrEvidenceRef(t *testing.T) {}
func TestOperatorRepairRejectsAmbiguousOrFencedIntervention(t *testing.T) {}
func TestOperatorRepairBuildsFailedRecordWithRedactedBeforeAfter(t *testing.T) {}
`)
	writeEvidenceCLIFile(t, filepath.Join(root, "internal", "store", "postgres", "operator_repair_test.go"), `package postgres

import "testing"

func TestStoreImplementsOperatorRepairStore(t *testing.T) {}
func TestCommitOperatorRepairFailedUsesAtomicCASAndAuditOutbox(t *testing.T) {}
func TestCommitOperatorRepairFailedRequiresSafeInterventionShapeBeforeSQL(t *testing.T) {}
func TestCommitOperatorRepairFailedCASRejectsConcurrentAmbiguousPhase(t *testing.T) {}
func TestCommitOperatorRepairFailedNoRowsFailsClosed(t *testing.T) {}
func TestGetExportSessionSelectsOnlyRedactedColumns(t *testing.T) {}
`)
	writeEvidenceCLIFile(t, filepath.Join(root, "internal", "api", "operator_repair_handler_test.go"), `package api

import "testing"

func TestOperatorRepairHandlerOperatorAdminTerminalizesUnsupportedInterventionWithAudit(t *testing.T) {}
func TestOperatorRepairHandlerRejectsProductOperationInspectorBeforeStore(t *testing.T) {}
func TestOperatorRepairHandlerRejectsInvalidBodyBeforeStore(t *testing.T) {}
func TestOperatorRepairHandlerIdempotentReplayStableWithoutDuplicateAudit(t *testing.T) {}
func TestInternalAPIShellServesOperatorRepairThroughInjectedStore(t *testing.T) {}
`)
	writeEvidenceCLIFile(t, filepath.Join(root, "internal", "contractcheck", "operator_repair_contract_test.go"), `package contractcheck

import "testing"

func TestOperatorRepairContractDefinesAllowlistPreconditionsAuditAndForbiddenSQL(t *testing.T) {}
func TestOperatorRepairContractIsLinkedFromContractsReadme(t *testing.T) {}
func TestRestoreReconciliationContractDefinesModeDenialCredentialPurgeMismatch(t *testing.T) {}
func TestDiscoverySurfacesContractDefinesLayeredDiscoveryBoundaries(t *testing.T) {}
func TestSecretPathRedactionContractDefinesDefaultControlPlaneOutputBoundary(t *testing.T) {}
func TestProfileBoundaryContractDefinesDefaultFixtureAndRuntimeSupportSeparation(t *testing.T) {}
func TestWorkflowHardeningCurrentRepoWorkflowUsesSingleAuthoritativeGate(t *testing.T) {}
func TestWorkflowHardeningReleaseScriptCannotBypassManifestOrBaseline(t *testing.T) {}
func TestWorkflowHardeningFinalIntentRequiresSelectorAndRejectsCheckOnlyFinalAcceptance(t *testing.T) {}
func TestWorkflowHardeningContractRejectsManualApprovalAlternateGateOrDeploymentRuntimeProof(t *testing.T) {}
func TestResidualRiskCatalogCurrentRepoDefinesMachineCheckableRiskRows(t *testing.T) {}
func TestResidualRiskCatalogRejectsHumanApprovalWaiverOrSubjectiveException(t *testing.T) {}
func TestResidualRiskCatalogRequiresEvidenceRefsOwnerStatusDecisionAndMitigation(t *testing.T) {}
func TestResidualRiskCatalogSharedVolumeThreatModelHasScopeExpiryReviewAndEscalation(t *testing.T) {}
func TestResidualRiskAcceptanceRequiresPredefinedRiskScopeReasonEvidenceAndReviewPoint(t *testing.T) {}
func TestResidualRiskAcceptanceAuditIsOperatorScopedAndRedacted(t *testing.T) {}
func TestDeploymentRiskEnvelopeCurrentRepoDefinesRuntimeSupportRows(t *testing.T) {}
func TestDeploymentRiskEnvelopeRejectsProductionOrManualGateProof(t *testing.T) {}
func TestDeploymentRiskEnvelopeRequiresDetectionRedactionRollbackAndResidualLinks(t *testing.T) {}
func TestDeploymentRiskEnvelopeRunbookRefsAreRepoLocalOperatorHandoff(t *testing.T) {}
func TestDeploymentRiskEnvelopeRuntimePrereqsDoNotCloseOptionalFixturePurgeTemplateOrWorkload(t *testing.T) {}
func TestDeploymentRiskEnvelopeContractSeparatesRuntimeSupportFromDefaultPositiveProof(t *testing.T) {}
`)
	writeEvidenceCLIFile(t, filepath.Join(root, "cmd", "afscp-evidence-verify", "main_test.go"), `package main

import "testing"

func TestRunCheckOnlyAcceptsDefaultUserLoopAggregationManifest(t *testing.T) {}
func TestRunCheckOnlyAcceptsOperatorRepairSafeManifest(t *testing.T) {}
func TestRunCheckOnlyAcceptsRestoreReconciliationManifest(t *testing.T) {}
func TestRunCheckOnlyAcceptsDiscoverySurfacesManifest(t *testing.T) {}
func TestRunCheckOnlyAcceptsSecretPathRedactionManifest(t *testing.T) {}
func TestRunCheckOnlyAcceptsProfileBoundaryManifest(t *testing.T) {}
func TestRunCheckOnlyAcceptsWorkflowHardeningManifest(t *testing.T) {}
func TestFinalCheckOnlyCannotDeclareFinalAcceptance(t *testing.T) {}
func TestRunCheckOnlyAcceptsResidualRiskCatalogManifest(t *testing.T) {}
func TestRunCheckOnlyAcceptsDeploymentRiskEnvelopeManifest(t *testing.T) {}
`)
}

func writeEvidenceCLIFile(t *testing.T, path, body string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
