package afscp_test

import (
	"os"
	"strings"
	"testing"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/config"
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
