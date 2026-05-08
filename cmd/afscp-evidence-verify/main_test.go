package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCheckOnlyValidatesManifestWithoutExecutingCommands(t *testing.T) {
	root := t.TempDir()
	writeEvidenceCLIScripts(t, root)
	manifestPath := filepath.Join(root, "manifest.json")
	writeEvidenceCLIFile(t, manifestPath, evidenceCLIManifest(`["bash","scripts/fail.sh"]`, "scripts/fail.sh"))

	var stdout, stderr bytes.Buffer
	code := run([]string{"-manifest", manifestPath, "-repo-root", root, "-check-only"}, &stdout, &stderr)
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
	code := run([]string{"-manifest", manifestPath, "-repo-root", root}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "failed") {
		t.Fatalf("expected stdout to include command failure finding, got %q", stdout.String())
	}
}

func TestRunReturnsTwoWhenManifestFlagMissing(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(nil, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2, got %d; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "-manifest") {
		t.Fatalf("expected stderr to mention -manifest, got %q", stderr.String())
	}
}

func evidenceCLIManifest(command, anchor string) string {
	return `{
  "schema_version":"1",
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
      "capability_id":"workload_mount",
      "evidence_type":"unit",
      "required":true,
      "command":["bash","scripts/pass.sh"],
      "anchors":["` + anchor + `"],
      "doc_only_allowed":false,
      "optional_gated":true,
      "default_ga_required":false
    },
    {
      "id":"workload_mount_plan_store_freshness_unit",
      "capability_id":"workload_mount",
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
      "capability_id":"workload_mount",
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
      "capability_id":"workload_mount",
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
}`
}

func writeEvidenceCLIScripts(t *testing.T, root string) {
	t.Helper()

	writeEvidenceCLIFile(t, filepath.Join(root, "scripts", "pass.sh"), "#!/usr/bin/env bash\nexit 0\n")
	writeEvidenceCLIFile(t, filepath.Join(root, "scripts", "fail.sh"), "#!/usr/bin/env bash\nexit 1\n")
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
