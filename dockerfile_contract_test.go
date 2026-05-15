package afscp_test

import (
	"os"
	"strings"
	"testing"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/config"
)

const (
	directRestoreLocalJVSBinarySHA256 = "c88553bb18bdd70e1399bf562fcb853bd200798498bd24bc25458196fb568902"
	directRestoreJVSSourceRef         = "jvs@c65b418f58d6e39e91199c1d55783e2ec91be9a1"
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
		"ADD --checksum=sha256:" + config.JVSAcceptedLinuxAMD64SHA256,
		"https://github.com/agentsmith-project/jvs/releases/download/" + config.JVSAcceptedReleaseVersion + "/" + config.JVSAcceptedLinuxAMD64AssetName,
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

func TestJVSPinEvidenceDeclaresDirectRestoreGapAndOverride(t *testing.T) {
	path := "docs/JVS_PIN_EVIDENCE_2026-05-12-v0.4.9.md"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	doc := string(data)
	for _, want := range []string{
		"restore --direct",
		"does not declare",
		"AFSCP_JVS_BINARY_PATH=<absolute path to direct-capable local JVS artifact>",
		"AFSCP_JVS_BINARY_SHA256=" + directRestoreLocalJVSBinarySHA256,
		"AFSCP_JVS_DIRECT_RESTORE_BINARY_SHA256=" + directRestoreLocalJVSBinarySHA256,
		"AFSCP_JVS_DIRECT_RESTORE_SOURCE_REF=" + directRestoreJVSSourceRef,
		"AFSCP_JVS_DIRECT_RESTORE_BINARY_SHA256",
		"AFSCP_JVS_DIRECT_RESTORE_SOURCE_REF",
		"vcs.revision=c65b418f58d6e39e91199c1d55783e2ec91be9a1",
		"vcs.modified=false",
		"--discard-unsaved",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("%s missing direct restore pin evidence marker %q", path, want)
		}
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
