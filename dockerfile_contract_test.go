package afscp_test

import (
	"os"
	"strings"
	"testing"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/config"
)

const (
	directRestoreEvidencePath = "docs/JVS_AFSCP_DIRECT_LOCAL_EVIDENCE_2026-05-16.md"
)

func TestDockerfilePackagesPinnedJVSLinuxAMD64Binary(t *testing.T) {
	data, err := os.ReadFile("Dockerfile")
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	dockerfile := string(data)

	for _, want := range []string{
		"ARG JVS_VERSION=" + config.JVSAcceptedReleaseVersion,
		"ARG JVS_ASSET=" + config.JVSAcceptedLinuxAMD64AssetName,
		"ARG JVS_SHA256=" + config.JVSAcceptedLinuxAMD64SHA256,
		"ARG JVS_SOURCE_REF=" + config.JVSAcceptedSourceRef,
		"ARG JVS_LOCAL_BINARY=dist/jvs-linux-amd64",
		"COPY --chmod=0755 ${JVS_LOCAL_BINARY} /jvs",
		"AFSCP_JVS_BINARY_SHA256=\"${JVS_SHA256}\"",
		"AFSCP_JVS_DIRECT_RESTORE_SOURCE_REF=\"${JVS_SOURCE_REF}\"",
		"COPY --from=jvs --chmod=0755 /jvs /usr/local/bin/jvs",
	} {
		if !strings.Contains(dockerfile, want) {
			t.Fatalf("Dockerfile missing %q", want)
		}
	}

	finalStageStart := strings.LastIndex(dockerfile, "\nFROM ")
	if finalStageStart == -1 {
		t.Fatal("Dockerfile has no final FROM stage")
	}
	finalStage := dockerfile[finalStageStart:]
	if !strings.Contains(finalStage, "COPY --from=jvs --chmod=0755 /jvs /usr/local/bin/jvs") {
		t.Fatalf("final image stage does not package pinned JVS binary: %s", finalStage)
	}
}

func TestDockerfileFinalImageSupportsPinnedDynamicJVSBinary(t *testing.T) {
	data, err := os.ReadFile("Dockerfile")
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	dockerfile := string(data)

	finalFrom := finalFromInstruction(t, dockerfile)
	if strings.Contains(finalFrom, "static-debian12") {
		t.Fatalf("final image uses %q; pinned JVS is dynamically linked and requires a glibc runtime loader", finalFrom)
	}

	knownDynamicLoaderBases := map[string]bool{
		"gcr.io/distroless/base-debian12:nonroot": true,
		"gcr.io/distroless/cc-debian12:nonroot":   true,
	}
	if !knownDynamicLoaderBases[finalFrom] {
		t.Fatalf("final image base = %q, want a known nonroot distroless base with glibc dynamic loader support", finalFrom)
	}
}

func TestCurrentJVSDirectLocalEvidenceMatchesPinnedBinary(t *testing.T) {
	data, err := os.ReadFile(directRestoreEvidencePath)
	if err != nil {
		t.Fatalf("read %s: %v", directRestoreEvidencePath, err)
	}
	doc := string(data)
	for _, want := range []string{
		"Status: current pre-GA AFSCP JVS implementation pin evidence.",
		"version: " + config.JVSAcceptedReleaseVersion,
		"artifact: " + config.JVSAcceptedLinuxAMD64AssetName,
		"JVS binary artifact SHA-256: " + config.JVSAcceptedLinuxAMD64SHA256,
		"source ref: " + config.JVSAcceptedSourceRef,
		"jvs afscp --control-root <control> --home <home> restore --save-point <save_point_id> --json",
		"restore preview",
		"restore run",
		"`--direct --discard-unsaved`",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("%s missing direct restore pin evidence marker %q", directRestoreEvidencePath, want)
		}
	}
	if strings.Contains(doc, "dirty") {
		t.Fatalf("%s still describes the active source ref as dirty", directRestoreEvidencePath)
	}
}

func finalFromInstruction(t *testing.T, dockerfile string) string {
	t.Helper()

	lines := strings.Split(dockerfile, "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if !strings.HasPrefix(line, "FROM ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			t.Fatalf("malformed final FROM instruction %q", line)
		}
		return fields[1]
	}

	t.Fatal("Dockerfile has no final FROM stage")
	return ""
}
