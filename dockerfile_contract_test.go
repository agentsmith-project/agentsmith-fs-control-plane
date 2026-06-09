package afscp_test

import (
	"os"
	"strings"
	"testing"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/config"
)

const (
	directRestoreEvidencePath = "docs/JVS_AFSCP_DIRECT_RELEASE_EVIDENCE_2026-05-18.md"
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
		"ADD --checksum=sha256:" + config.JVSAcceptedLinuxAMD64SHA256,
		"https://github.com/agentsmith-project/jvs/releases/download/" + config.JVSAcceptedReleaseVersion + "/" + config.JVSAcceptedLinuxAMD64AssetName,
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

func TestDockerfilePackagesJuiceFSCloneRuntimeForDirectSavePoints(t *testing.T) {
	data, err := os.ReadFile("Dockerfile")
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	dockerfile := string(data)

	for _, want := range []string{
		"FROM juicedata/mount:ce-v1.3.1 AS juicefs",
		"LD_LIBRARY_PATH=\"/usr/local/juicefs-lib\"",
		"COPY --from=juicefs --chmod=0755 /usr/local/bin/juicefs /usr/local/bin/juicefs",
		"COPY --from=juicefs --chmod=0755 /usr/lib/libfdb_c.so /usr/local/juicefs-lib/",
		"COPY --from=juicefs --chmod=0755 /usr/lib/ceph/libceph-common.so* /usr/local/juicefs-lib/",
		"COPY --from=juicefs --chmod=0755 /usr/lib/librados.so* /usr/local/juicefs-lib/",
		"COPY --from=juicefs --chmod=0755 /usr/lib/librados_tp.so* /usr/local/juicefs-lib/",
		"COPY --from=juicefs --chmod=0755 /usr/lib/x86_64-linux-gnu/libcrypto.so* /usr/local/juicefs-lib/",
		"COPY --from=juicefs --chmod=0755 /usr/lib/x86_64-linux-gnu/libstdc++.so* /usr/local/juicefs-lib/",
		"COPY --from=juicefs --chmod=0755 /usr/lib/x86_64-linux-gnu/libtcmalloc_minimal.so* /usr/local/juicefs-lib/",
		"COPY --from=juicefs --chmod=0755 /usr/lib/x86_64-linux-gnu/libgssapi_krb5.so* /usr/local/juicefs-lib/",
		"COPY --from=juicefs --chmod=0755 /usr/lib/x86_64-linux-gnu/libgfapi.so* /usr/local/juicefs-lib/",
		"COPY --from=juicefs --chmod=0755 /usr/lib/x86_64-linux-gnu/libglusterfs.so* /usr/local/juicefs-lib/",
		"COPY --from=juicefs --chmod=0755 /lib/x86_64-linux-gnu/libtirpc.so* /usr/local/juicefs-lib/",
		"COPY --from=juicefs --chmod=0755 /lib/x86_64-linux-gnu/libnl-3.so* /usr/local/juicefs-lib/",
	} {
		if !strings.Contains(dockerfile, want) {
			t.Fatalf("Dockerfile missing JuiceFS runtime marker %q", want)
		}
	}

	finalStageStart := strings.LastIndex(dockerfile, "\nFROM ")
	if finalStageStart == -1 {
		t.Fatal("Dockerfile has no final FROM stage")
	}
	finalStage := dockerfile[finalStageStart:]
	if !strings.Contains(finalStage, "COPY --from=juicefs --chmod=0755 /usr/local/bin/juicefs /usr/local/bin/juicefs") {
		t.Fatalf("final image stage does not package juicefs CLI for direct save/restore clone operations: %s", finalStage)
	}
	if !strings.Contains(finalStage, "LD_LIBRARY_PATH=\"/usr/local/juicefs-lib\"") {
		t.Fatalf("final image stage does not expose the JuiceFS runtime library path: %s", finalStage)
	}
	if !strings.Contains(finalStage, "COPY --from=juicefs --chmod=0755 /usr/lib/libfdb_c.so /usr/local/juicefs-lib/") {
		t.Fatalf("final image stage does not package the JuiceFS FoundationDB runtime library: %s", finalStage)
	}
	if !strings.Contains(finalStage, "COPY --from=juicefs --chmod=0755 /usr/lib/ceph/libceph-common.so* /usr/local/juicefs-lib/") {
		t.Fatalf("final image stage does not package the JuiceFS Ceph runtime library: %s", finalStage)
	}
	if !strings.Contains(finalStage, "COPY --from=juicefs --chmod=0755 /usr/lib/librados.so* /usr/local/juicefs-lib/") {
		t.Fatalf("final image stage does not package the JuiceFS RADOS runtime library: %s", finalStage)
	}
	if !strings.Contains(finalStage, "COPY --from=juicefs --chmod=0755 /usr/lib/librados_tp.so* /usr/local/juicefs-lib/") {
		t.Fatalf("final image stage does not package the JuiceFS RADOS thread-pool runtime library: %s", finalStage)
	}
	if !strings.Contains(finalStage, "COPY --from=juicefs --chmod=0755 /usr/lib/x86_64-linux-gnu/libgfapi.so* /usr/local/juicefs-lib/") {
		t.Fatalf("final image stage does not package the JuiceFS Gluster runtime libraries: %s", finalStage)
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

func TestDockerfileRunsStorageReadersAsRootAndNonStorageCommandsDropPrivileges(t *testing.T) {
	data, err := os.ReadFile("Dockerfile")
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	dockerfile := string(data)
	finalStageStart := strings.LastIndex(dockerfile, "\nFROM ")
	if finalStageStart == -1 {
		t.Fatal("Dockerfile has no final FROM stage")
	}
	finalStage := dockerfile[finalStageStart:]
	if !strings.Contains(finalStage, "\nUSER 0:0\n") {
		t.Fatalf("final image stage must run as storage reader root for gateway and worker: %s", finalStage)
	}

	for _, path := range []string{
		"cmd/afscp-api/main.go",
		"cmd/afscp-migrate/main.go",
		"cmd/afscp-volume-bootstrap/main.go",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		source := string(data)
		if !strings.Contains(source, "runtimeidentity.DropToContainerNonrootIfRoot()") {
			t.Fatalf("%s must drop root image privileges at process start", path)
		}
	}

	for _, path := range []string{
		"cmd/afscp-export-gateway/main.go",
		"cmd/afscp-worker/main.go",
	} {
		data, err = os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strings.Contains(string(data), "DropToContainerNonrootIfRoot") {
			t.Fatalf("%s must retain storage-reader identity instead of dropping to API/migration nonroot", path)
		}
	}
}

func TestCurrentJVSDirectReleaseEvidenceMatchesPinnedBinary(t *testing.T) {
	data, err := os.ReadFile(directRestoreEvidencePath)
	if err != nil {
		t.Fatalf("read %s: %v", directRestoreEvidencePath, err)
	}
	doc := string(data)
	for _, want := range []string{
		"Status: current AFSCP JVS release pin evidence.",
		"version: " + config.JVSAcceptedReleaseVersion,
		"artifact: " + config.JVSAcceptedLinuxAMD64AssetName,
		"JVS binary artifact SHA-256: " + config.JVSAcceptedLinuxAMD64SHA256,
		"source ref: " + config.JVSAcceptedSourceRef,
		"https://github.com/agentsmith-project/jvs/releases/tag/" + config.JVSAcceptedReleaseVersion,
		"`save`: `--message`, `--purpose`, `--control-root`, `--home`, `--json`",
		"jvs afscp --control-root <control> --home <home> restore --save-point <save_point_id> --json",
		"restore preview",
		"restore run",
		"`--direct --discard-unsaved`",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("%s missing direct release pin evidence marker %q", directRestoreEvidencePath, want)
		}
	}
	if strings.Contains(doc, "dirty") {
		t.Fatalf("%s still describes the active source ref as dirty", directRestoreEvidencePath)
	}
}

func TestReleaseWorkflowUsesPublishedJVSArtifactInsteadOfSiblingSourceBuild(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/release.yml")
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	workflow := string(data)

	for _, forbidden := range []string{
		"repository: agentsmith-project/jvs",
		"path: _release-jvs",
		"make release-build",
		"dist/jvs-linux-amd64",
	} {
		if strings.Contains(workflow, forbidden) {
			t.Fatalf("release workflow must not build or inject JVS from sibling source; found %q", forbidden)
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
