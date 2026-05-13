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
