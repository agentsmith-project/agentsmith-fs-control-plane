package pathresolver

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/pathresolver/testcorpus"
)

func TestSharedCorpusContainsContractGuardCategories(t *testing.T) {
	t.Parallel()

	tags := map[string]bool{}
	for _, tag := range testcorpus.AllTags() {
		tags[tag] = true
	}

	required := []string{
		testcorpus.TagRepoManagedRoot,
		testcorpus.TagTemplateManagedRoot,
		testcorpus.TagJVS,
		testcorpus.TagEncodedSeparator,
		testcorpus.TagDoubleEncodedSeparator,
		testcorpus.TagUnicodeSlashLike,
		testcorpus.TagControlChar,
		testcorpus.TagSymlink,
		testcorpus.TagHardlink,
		testcorpus.TagTraversalDefenseFlag,
		testcorpus.TagNilInspector,
	}
	for _, tag := range required {
		tag := tag
		t.Run(tag, func(t *testing.T) {
			t.Parallel()

			if !tags[tag] {
				t.Fatalf("shared resolver corpus is missing required category tag %q", tag)
			}
		})
	}
}

func TestValidateIDAcceptsSharedCorpus(t *testing.T) {
	t.Parallel()

	tests := testcorpus.AcceptedIDCases()
	for _, tt := range tests {
		tt := tt
		t.Run(tt.Name, func(t *testing.T) {
			t.Parallel()

			if err := ValidateID(IDKind(tt.Kind), tt.ID); err != nil {
				t.Fatalf("ValidateID(%q, %q) returned error: %v", tt.Kind, tt.ID, err)
			}
		})
	}

	t.Run("mount alias", func(t *testing.T) {
		t.Parallel()

		if err := ValidateID(MountID, "wmb_m0"); err != nil {
			t.Fatalf("ValidateID(%q, %q) returned error: %v", MountID, "wmb_m0", err)
		}
	})
}

func TestValidateIDRejectsSharedCorpus(t *testing.T) {
	t.Parallel()

	for _, tt := range testcorpus.RejectedIDCases() {
		tt := tt
		t.Run(tt.Name, func(t *testing.T) {
			t.Parallel()

			if err := ValidateID(IDKind(tt.Kind), tt.ID); err == nil {
				t.Fatalf("ValidateID(%q, %q) succeeded, want error", tt.Kind, tt.ID)
			}
		})
	}
}

func TestResolveRepoPathsReturnsCanonicalRelativeSubdirs(t *testing.T) {
	t.Parallel()

	got, err := ResolveRepoPaths("ns_alpha-01", "repo_Project_02")
	if err != nil {
		t.Fatalf("ResolveRepoPaths returned error: %v", err)
	}

	want := RepoPaths{
		ContainerVolumeSubdir: "afscp/namespaces/ns_alpha-01/repos/repo_Project_02",
		ControlVolumeSubdir:   "afscp/namespaces/ns_alpha-01/repos/repo_Project_02/control",
		PayloadVolumeSubdir:   "afscp/namespaces/ns_alpha-01/repos/repo_Project_02/payload",
	}
	if got != want {
		t.Fatalf("ResolveRepoPaths mismatch:\n got: %#v\nwant: %#v", got, want)
	}
	for _, path := range []string{got.ContainerVolumeSubdir, got.ControlVolumeSubdir, got.PayloadVolumeSubdir} {
		assertRelativeForTest(t, path)
	}
}

func TestResolveTemplatePathsReturnsCanonicalRelativeSubdirs(t *testing.T) {
	t.Parallel()

	got, err := ResolveTemplatePaths("ns_alpha-01", "tmpl_Base_02")
	if err != nil {
		t.Fatalf("ResolveTemplatePaths returned error: %v", err)
	}

	want := TemplatePaths{
		ContainerVolumeSubdir: "afscp/namespaces/ns_alpha-01/templates/tmpl_Base_02",
		ControlVolumeSubdir:   "afscp/namespaces/ns_alpha-01/templates/tmpl_Base_02/control",
		PayloadVolumeSubdir:   "afscp/namespaces/ns_alpha-01/templates/tmpl_Base_02/payload",
	}
	if got != want {
		t.Fatalf("ResolveTemplatePaths mismatch:\n got: %#v\nwant: %#v", got, want)
	}
	for _, path := range []string{got.ContainerVolumeSubdir, got.ControlVolumeSubdir, got.PayloadVolumeSubdir} {
		assertRelativeForTest(t, path)
	}
}

func TestResolveRepoPathsRejectsInvalidIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		namespaceID string
		repoID      string
	}{
		{name: "bad namespace", namespaceID: findRejectedIDForTest(t, "too short suffix").ID, repoID: "repo_ok"},
		{name: "bad repo", namespaceID: "ns_ok", repoID: findRejectedIDForTest(t, "first suffix dot").ID},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := ResolveRepoPaths(tt.namespaceID, tt.repoID); err == nil {
				t.Fatalf("ResolveRepoPaths(%q, %q) succeeded, want error", tt.namespaceID, tt.repoID)
			}
		})
	}
}

func TestResolveRepoRootPathsReturnsCanonicalInternalRoots(t *testing.T) {
	t.Parallel()

	got, err := ResolveRepoRootPaths("/srv/afscp/volumes/vol_default", "ns_alpha-01", "repo_Project_02")
	if err != nil {
		t.Fatalf("ResolveRepoRootPaths returned error: %v", err)
	}

	wantRepoPaths := RepoPaths{
		ContainerVolumeSubdir: "afscp/namespaces/ns_alpha-01/repos/repo_Project_02",
		ControlVolumeSubdir:   "afscp/namespaces/ns_alpha-01/repos/repo_Project_02/control",
		PayloadVolumeSubdir:   "afscp/namespaces/ns_alpha-01/repos/repo_Project_02/payload",
	}
	if got.RepoPaths != wantRepoPaths {
		t.Fatalf("RepoPaths mismatch:\n got: %#v\nwant: %#v", got.RepoPaths, wantRepoPaths)
	}

	wantControl := filepath.Join("/srv/afscp/volumes/vol_default", "afscp", "namespaces", "ns_alpha-01", "repos", "repo_Project_02", "control")
	wantPayload := filepath.Join("/srv/afscp/volumes/vol_default", "afscp", "namespaces", "ns_alpha-01", "repos", "repo_Project_02", "payload")
	if got.ControlRootPath != wantControl {
		t.Fatalf("ControlRootPath = %q, want %q", got.ControlRootPath, wantControl)
	}
	if got.PayloadRootPath != wantPayload {
		t.Fatalf("PayloadRootPath = %q, want %q", got.PayloadRootPath, wantPayload)
	}
	assertAbsoluteCleanForTest(t, got.ControlRootPath)
	assertAbsoluteCleanForTest(t, got.PayloadRootPath)
	if got.ControlRootPath == got.PayloadRootPath {
		t.Fatal("control and payload roots must be different")
	}
	if pathContainsForTest(got.ControlRootPath, got.PayloadRootPath) || pathContainsForTest(got.PayloadRootPath, got.ControlRootPath) {
		t.Fatalf("control and payload roots must be siblings, got control=%q payload=%q", got.ControlRootPath, got.PayloadRootPath)
	}
}

func TestResolveRepoRootPathsRejectsUntrustedVolumeRoot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		root string
	}{
		{name: "empty", root: ""},
		{name: "relative", root: "srv/afscp"},
		{name: "root filesystem", root: string(filepath.Separator)},
		{name: "not clean", root: "/srv/../srv/afscp"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := ResolveRepoRootPaths(tt.root, "ns_alpha", "repo_unit"); err == nil {
				t.Fatalf("ResolveRepoRootPaths(%q, ...) succeeded, want error", tt.root)
			}
		})
	}
}

func TestResolveRepoRootPathsRejectsInvalidIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		namespaceID string
		repoID      string
	}{
		{name: "bad namespace", namespaceID: "repo_alpha", repoID: "repo_unit"},
		{name: "bad repo", namespaceID: "ns_alpha", repoID: "ns_project"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := ResolveRepoRootPaths("/srv/afscp/volumes/vol_default", tt.namespaceID, tt.repoID); err == nil {
				t.Fatalf("ResolveRepoRootPaths(..., %q, %q) succeeded, want error", tt.namespaceID, tt.repoID)
			}
		})
	}
}

func TestResolveTemplatePathsRejectsInvalidIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		namespaceID string
		templateID  string
	}{
		{name: "bad namespace", namespaceID: findRejectedIDForTest(t, "too short suffix").ID, templateID: "tmpl_ok"},
		{name: "bad template", namespaceID: "ns_ok", templateID: findRejectedIDForTest(t, "repo id passed as template").ID},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := ResolveTemplatePaths(tt.namespaceID, tt.templateID); err == nil {
				t.Fatalf("ResolveTemplatePaths(%q, %q) succeeded, want error", tt.namespaceID, tt.templateID)
			}
		})
	}
}

func TestRepoPathContractRejectsKindMismatchesBeforePathCompute(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		resolve     func() (string, error)
		wantZeroOut string
	}{
		{
			name: "repo resolver rejects template id",
			resolve: func() (string, error) {
				paths, err := ResolveRepoPaths("ns_alpha", "tmpl_base")
				return paths.PayloadVolumeSubdir, err
			},
		},
		{
			name: "template resolver rejects repo id",
			resolve: func() (string, error) {
				paths, err := ResolveTemplatePaths("ns_alpha", "repo_unit")
				return paths.PayloadVolumeSubdir, err
			},
		},
		{
			name: "repo resolver rejects namespace-shaped repo id",
			resolve: func() (string, error) {
				paths, err := ResolveRepoPaths("ns_alpha", "ns_project")
				return paths.PayloadVolumeSubdir, err
			},
		},
		{
			name: "repo resolver rejects repo-shaped namespace id",
			resolve: func() (string, error) {
				paths, err := ResolveRepoPaths("repo_alpha", "repo_unit")
				return paths.PayloadVolumeSubdir, err
			},
		},
		{
			name: "template resolver rejects namespace-shaped template id",
			resolve: func() (string, error) {
				paths, err := ResolveTemplatePaths("ns_alpha", "ns_base")
				return paths.PayloadVolumeSubdir, err
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tt.resolve()
			if err == nil {
				t.Fatal("resolver succeeded, want error")
			}
			if got != tt.wantZeroOut {
				t.Fatalf("resolver returned computed path %q, want zero value", got)
			}
		})
	}
}

func TestResolveCallerPathAcceptsSharedCorpus(t *testing.T) {
	t.Parallel()

	for _, tt := range testcorpus.AcceptedCallerPathCases() {
		tt := tt
		t.Run(tt.Name, func(t *testing.T) {
			t.Parallel()

			got, err := ResolveCallerPath(tt.RawPath)
			if err != nil {
				t.Fatalf("ResolveCallerPath(%q) returned error: %v", tt.RawPath, err)
			}
			if got.Clean != tt.Clean {
				t.Fatalf("Clean mismatch: got %q, want %q", got.Clean, tt.Clean)
			}
			if !reflect.DeepEqual(got.Segments, tt.Segments) {
				t.Fatalf("Segments mismatch: got %#v, want %#v", got.Segments, tt.Segments)
			}
		})
	}
}

func TestResolveCallerPathRejectsSharedCorpus(t *testing.T) {
	t.Parallel()

	for _, tt := range testcorpus.RejectedCallerPathCases() {
		tt := tt
		t.Run(tt.Name, func(t *testing.T) {
			t.Parallel()

			if _, err := ResolveCallerPath(tt.RawPath); err == nil {
				t.Fatalf("ResolveCallerPath(%q) succeeded, want error", tt.RawPath)
			}
		})
	}
}

func TestResolvePayloadTraversalPlanCarriesEscapeDefenseRequirements(t *testing.T) {
	t.Parallel()

	paths, err := ResolveRepoPaths("ns_alpha-01", "repo_Project_02")
	if err != nil {
		t.Fatalf("ResolveRepoPaths returned error: %v", err)
	}

	got, err := ResolvePayloadTraversal(paths.PayloadVolumeSubdir, "dir/file.txt")
	if err != nil {
		t.Fatalf("ResolvePayloadTraversal returned error: %v", err)
	}
	if got.RootSubdir != paths.PayloadVolumeSubdir {
		t.Fatalf("RootSubdir = %q, want %q", got.RootSubdir, paths.PayloadVolumeSubdir)
	}
	if !reflect.DeepEqual(got.Segments, []string{"dir", "file.txt"}) {
		t.Fatalf("Segments = %#v, want dir/file.txt segments", got.Segments)
	}
	if !got.NoFollow || !got.RejectHardlinks {
		t.Fatalf("plan does not carry escape defense requirements: %#v", got)
	}
}

func TestResolvePayloadTraversalAcceptsManagedRootCorpus(t *testing.T) {
	t.Parallel()

	for _, tt := range testcorpus.AcceptedManagedRootCases() {
		tt := tt
		t.Run(tt.Name, func(t *testing.T) {
			t.Parallel()

			got, err := ResolvePayloadTraversal(tt.RootSubdir, "dir/file.txt")
			if err != nil {
				t.Fatalf("ResolvePayloadTraversal(%q, %q) returned error: %v", tt.RootSubdir, "dir/file.txt", err)
			}
			if got.RootSubdir != tt.RootSubdir {
				t.Fatalf("RootSubdir = %q, want %q", got.RootSubdir, tt.RootSubdir)
			}
		})
	}
}

func TestResolvePayloadTraversalRejectsManagedRootCorpus(t *testing.T) {
	t.Parallel()

	for _, tt := range testcorpus.RejectedManagedRootCases() {
		tt := tt
		t.Run(tt.Name, func(t *testing.T) {
			t.Parallel()

			if _, err := ResolvePayloadTraversal(tt.RootSubdir, "dir/file.txt"); err == nil {
				t.Fatalf("ResolvePayloadTraversal(%q, %q) succeeded, want error", tt.RootSubdir, "dir/file.txt")
			}
		})
	}
}

func TestResolvePayloadTraversalRejectsPayloadPathCorpus(t *testing.T) {
	t.Parallel()

	for _, tt := range testcorpus.RejectedPayloadTraversalCases() {
		tt := tt
		t.Run(tt.Name, func(t *testing.T) {
			t.Parallel()

			if _, err := ResolvePayloadTraversal(tt.RootSubdir, tt.RawPath); err == nil {
				t.Fatalf("ResolvePayloadTraversal(%q, %q) succeeded, want error", tt.RootSubdir, tt.RawPath)
			}
		})
	}
}

func TestValidateTraversalPlanUsesSharedEscapeCorpus(t *testing.T) {
	t.Parallel()

	for _, tt := range testcorpus.TraversalPlanCases() {
		tt := tt
		t.Run(tt.Name, func(t *testing.T) {
			t.Parallel()

			err := ValidateTraversalPlan(traversalPlanFromCorpus(tt), inspectorFromCorpus(tt))
			if tt.WantEscape {
				if !errors.Is(err, ErrPathEscape) {
					t.Fatalf("err = %v, want ErrPathEscape", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateTraversalPlan returned error: %v", err)
			}
		})
	}
}

type fakeTraversalInspector map[string]TraversalEntry

func (inspector fakeTraversalInspector) InspectTraversalEntry(segments []string) (TraversalEntry, error) {
	return inspector[strings.Join(segments, "/")], nil
}

func traversalPlanFromCorpus(tt testcorpus.TraversalPlanCase) TraversalPlan {
	return TraversalPlan{
		RootSubdir:      tt.RootSubdir,
		Segments:        append([]string(nil), tt.Segments...),
		NoFollow:        tt.NoFollow,
		RejectHardlinks: tt.RejectHardlinks,
	}
}

func inspectorFromCorpus(tt testcorpus.TraversalPlanCase) TraversalInspector {
	if tt.InspectorNil {
		return nil
	}

	inspector := fakeTraversalInspector{}
	for _, entry := range tt.Entries {
		inspector[entry.Path] = TraversalEntry{
			Exists:    entry.Exists,
			Type:      entryTypeFromCorpus(entry.Type),
			Symlink:   entry.Symlink,
			LinkCount: entry.LinkCount,
		}
	}
	return inspector
}

func entryTypeFromCorpus(entryType string) EntryType {
	switch entryType {
	case "directory":
		return EntryDirectory
	case "file":
		return EntryFile
	case "other":
		return EntryOther
	default:
		return EntryUnknown
	}
}

func findRejectedIDForTest(t *testing.T, name string) testcorpus.IDCase {
	t.Helper()

	for _, tt := range testcorpus.RejectedIDCases() {
		if tt.Name == name {
			return tt
		}
	}
	t.Fatalf("missing rejected ID corpus case %q", name)
	return testcorpus.IDCase{}
}

func assertRelativeForTest(t *testing.T, path string) {
	t.Helper()

	if path == "" {
		t.Fatal("path is empty")
	}
	if path[0] == '/' {
		t.Fatalf("path %q is absolute", path)
	}
	for i := 0; i < len(path); i++ {
		if path[i] == '\\' {
			t.Fatalf("path %q contains backslash", path)
		}
	}
}

func assertAbsoluteCleanForTest(t *testing.T, path string) {
	t.Helper()

	if !filepath.IsAbs(path) {
		t.Fatalf("path %q is not absolute", path)
	}
	if filepath.Clean(path) != path {
		t.Fatalf("path %q is not clean", path)
	}
}

func pathContainsForTest(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
