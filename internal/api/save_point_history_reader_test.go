package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/jvsrunner"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

func TestJVSBackedSavePointHistoryReaderResolvesRootAndReturnsSafeHistoryInJVSOrder(t *testing.T) {
	now := fixedNamespaceNow()
	repo := repoResourceFixture("ns_123", "repo_123", resources.RepoStatusActive)
	repo.CreatedAt = now.Add(-time.Hour)
	repo.UpdatedAt = now
	volume := savePointHistoryVolume(now)
	repoReader := &fakeRepoReader{repos: []resources.Repo{repo}}
	volumeReader := &fakeVolumeReader{volume: volume}
	jvs := &fakeHistoryJVSRunner{summary: jvsrunner.HistorySummary{Workspace: "main", NewestSavePointID: "sp_002", SavePoints: []jvsrunner.SavePointSummary{
		{SavePointID: "sp_002", Message: "second", CreatedAt: "2026-05-05T12:01:00Z"},
		{SavePointID: "sp_001", Message: "first", CreatedAt: "2026-05-05T12:00:00Z"},
	}}}
	reader, err := NewJVSBackedSavePointHistoryReader(JVSBackedSavePointHistoryReaderConfig{
		RepoReader:   repoReader,
		VolumeReader: volumeReader,
		JVSRunner:    jvs,
		VolumeRoots:  map[string]string{"vol_123": "/srv/afscp/volumes/vol_123"},
	})
	if err != nil {
		t.Fatalf("NewJVSBackedSavePointHistoryReader: %v", err)
	}

	history, err := reader.ListSavePoints(context.Background(), "ns_123", "repo_123")
	if err != nil {
		t.Fatalf("ListSavePoints: %v", err)
	}

	if repoReader.getInNamespaceCalls != 1 || volumeReader.calls != 1 || jvs.calls != 1 {
		t.Fatalf("calls repo/volume/jvs = %d/%d/%d, want 1/1/1", repoReader.getInNamespaceCalls, volumeReader.calls, jvs.calls)
	}
	if !strings.HasSuffix(jvs.controlRoot, "/afscp/namespaces/ns_123/repos/repo_123/control") {
		t.Fatalf("control root = %q, want resolved canonical control root", jvs.controlRoot)
	}
	if len(history.SavePoints) != 2 || history.SavePoints[0].SavePointID != "sp_002" || history.SavePoints[0].Message != "second" || history.SavePoints[1].SavePointID != "sp_001" {
		t.Fatalf("history = %#v, want JVS order preserved", history)
	}
	rendered := strings.ToLower(strings.Join([]string{history.SavePoints[0].RepoID, history.SavePoints[0].SavePointID, history.SavePoints[0].Message, history.SavePoints[0].CreatedAt}, " "))
	for _, forbidden := range []string{"/srv/afscp", "control", "payload"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("history response leaked %q: %#v", forbidden, history)
		}
	}
}

func TestJVSBackedSavePointHistoryReaderFailsClosedWithoutLeakingRawPaths(t *testing.T) {
	now := fixedNamespaceNow()
	repo := repoResourceFixture("ns_123", "repo_123", resources.RepoStatusActive)
	volume := savePointHistoryVolume(now)
	reader, err := NewJVSBackedSavePointHistoryReader(JVSBackedSavePointHistoryReaderConfig{
		RepoReader:   &fakeRepoReader{repos: []resources.Repo{repo}},
		VolumeReader: &fakeVolumeReader{volume: volume},
		JVSRunner:    &fakeHistoryJVSRunner{err: errors.New("jvs failed at /srv/afscp/volumes/vol_123/secret/control")},
		VolumeRoots:  map[string]string{"vol_123": "/srv/afscp/volumes/vol_123"},
	})
	if err != nil {
		t.Fatalf("NewJVSBackedSavePointHistoryReader: %v", err)
	}

	_, err = reader.ListSavePoints(context.Background(), "ns_123", "repo_123")
	if !errors.Is(err, errSavePointHistoryUnavailable) {
		t.Fatalf("ListSavePoints error = %v, want unavailable", err)
	}
	if strings.Contains(strings.ToLower(err.Error()), "/srv/afscp") || strings.Contains(strings.ToLower(err.Error()), "secret") {
		t.Fatalf("error leaked raw detail: %v", err)
	}
}

func TestInternalAPIShellCanInjectConcreteJVSBackedSavePointHistoryReader(t *testing.T) {
	now := fixedNamespaceNow()
	repo := repoResourceFixture("ns_123", "repo_123", resources.RepoStatusActive)
	handler := NewInternalAPIShell(InternalAPIShellConfig{
		PrincipalResolver:           namespaceBindingPrincipalResolver(),
		NamespaceReader:             &fakeNamespaceReader{namespace: resources.Namespace{ID: "ns_123", Status: resources.NamespaceStatusActive, CreatedAt: now.Add(-time.Hour), UpdatedAt: now}},
		NamespaceBindingReader:      &fakeNamespaceVolumeBindingReader{binding: namespacePolicyBindingFixture("ns_123", resources.AllowedCaller{CallerService: "agentsmith-api", Roles: []resources.CallerRole{resources.CallerRoleRepoAdmin}})},
		RepoReader:                  &fakeRepoReader{repos: []resources.Repo{repo}},
		VolumeReader:                &fakeVolumeReader{volume: savePointHistoryVolume(now)},
		RepoFenceReader:             &fakeRepoFenceReader{},
		SavePointMutationGate:       &fakeRepoJVSMutationGateReader{},
		SavePointHistoryJVSRunner:   &fakeHistoryJVSRunner{summary: jvsrunner.HistorySummary{Workspace: "main", NewestSavePointID: "sp_001", SavePoints: []jvsrunner.SavePointSummary{{SavePointID: "sp_001", Message: "first", CreatedAt: "2026-05-05T12:00:00Z"}}}},
		SavePointHistoryVolumeRoots: map[string]string{"vol_123": "/srv/afscp/volumes/vol_123"},
	})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, savePointRequest(http.MethodGet, "/internal/v1/repos/repo_123/save-points", "", "ns_123"))

	if rec.Code != 200 {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"save_point_id":"sp_001"`, `"message":"first"`, `"repo_id":"repo_123"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("response %s missing %s", body, want)
		}
	}
	if strings.Contains(body, "/srv/afscp") || strings.Contains(body, "control") || strings.Contains(body, "payload") {
		t.Fatalf("response leaked raw path detail: %s", body)
	}
}

func savePointHistoryVolume(now time.Time) resources.Volume {
	return resources.Volume{
		ID:             "vol_123",
		Backend:        resources.VolumeBackendJuiceFS,
		IsolationClass: resources.VolumeIsolationShared,
		Status:         resources.VolumeStatusActive,
		Capabilities:   map[string]any{"webdav_export": true, "workload_mount": true, "jvs_external_control_root": true, "directory_quota": false},
		CreatedAt:      now.Add(-time.Hour),
		UpdatedAt:      now,
	}
}

type fakeVolumeReader struct {
	calls  int
	volume resources.Volume
	err    error
}

func (reader *fakeVolumeReader) GetVolume(_ context.Context, _ string) (resources.Volume, error) {
	reader.calls++
	if reader.err != nil {
		return resources.Volume{}, reader.err
	}
	return reader.volume, nil
}

type fakeHistoryJVSRunner struct {
	calls       int
	controlRoot string
	summary     jvsrunner.HistorySummary
	err         error
}

func (runner *fakeHistoryJVSRunner) History(_ context.Context, controlRoot string) (jvsrunner.HistorySummary, error) {
	runner.calls++
	runner.controlRoot = controlRoot
	if runner.err != nil {
		return jvsrunner.HistorySummary{}, runner.err
	}
	return runner.summary, nil
}
