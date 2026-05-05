package pathresolver

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestValidateIDAcceptsStrictGrammar(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		kind IDKind
		id   string
	}{
		{name: "namespace", kind: NamespaceID, id: "ns_ab"},
		{name: "repo", kind: RepoID, id: "repo_A1-b_2"},
		{name: "template", kind: TemplateID, id: "tmpl_09"},
		{name: "volume", kind: VolumeID, id: "vol_Z9"},
		{name: "export", kind: ExportID, id: "export_a-b_c9"},
		{name: "mount binding", kind: WorkloadMountBindingID, id: "wmb_m0"},
		{name: "mount alias", kind: MountID, id: "wmb_m0"},
		{name: "operation", kind: OperationID, id: "op_xY"},
		{name: "max suffix", kind: RepoID, id: "repo_A" + repeatForTest("b", 62)},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := ValidateID(tt.kind, tt.id); err != nil {
				t.Fatalf("ValidateID(%q, %q) returned error: %v", tt.kind, tt.id, err)
			}
		})
	}
}

func TestValidateIDRejectsUnsafeInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		kind IDKind
		id   string
	}{
		{name: "empty", kind: NamespaceID, id: ""},
		{name: "wrong prefix", kind: RepoID, id: "ns_ab"},
		{name: "too short suffix", kind: NamespaceID, id: "ns_a"},
		{name: "too long suffix", kind: NamespaceID, id: "ns_A" + repeatForTest("b", 63)},
		{name: "first suffix underscore", kind: VolumeID, id: "vol__a"},
		{name: "first suffix hyphen", kind: VolumeID, id: "vol_-a"},
		{name: "first suffix dot", kind: RepoID, id: "repo_.hidden"},
		{name: "slash", kind: RepoID, id: "repo_ab/cd"},
		{name: "backslash", kind: RepoID, id: "repo_ab\\cd"},
		{name: "dot", kind: TemplateID, id: "tmpl_ab.cd"},
		{name: "space", kind: ExportID, id: "export_ab cd"},
		{name: "display name", kind: RepoID, id: "repo_My Project"},
		{name: "display name punctuation", kind: TemplateID, id: "tmpl_Base:v1"},
		{name: "percent encoded slash", kind: RepoID, id: "repo_ab%2fcd"},
		{name: "double encoded slash", kind: RepoID, id: "repo_ab%252fcd"},
		{name: "unicode division slash", kind: RepoID, id: "repo_ab\u2215cd"},
		{name: "unicode fullwidth reverse solidus", kind: RepoID, id: "repo_ab\uff3ccd"},
		{name: "unicode", kind: OperationID, id: "op_ab\u00e9"},
		{name: "control", kind: WorkloadMountBindingID, id: "wmb_ab\ncd"},
		{name: "unknown kind", kind: IDKind("other"), id: "ns_ab"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := ValidateID(tt.kind, tt.id); err == nil {
				t.Fatalf("ValidateID(%q, %q) succeeded, want error", tt.kind, tt.id)
			}
		})
	}
}

func TestRepoPathContractStrictIDCorpus(t *testing.T) {
	t.Parallel()

	accepted := []struct {
		name string
		kind IDKind
		id   string
	}{
		{name: "namespace lowercase", kind: NamespaceID, id: "ns_ab"},
		{name: "namespace mixed", kind: NamespaceID, id: "ns_A9-_b"},
		{name: "repo lowercase", kind: RepoID, id: "repo_ab"},
		{name: "repo max suffix", kind: RepoID, id: "repo_A" + repeatForTest("z", 62)},
		{name: "template", kind: TemplateID, id: "tmpl_T9"},
		{name: "volume", kind: VolumeID, id: "vol_v1"},
	}
	for _, tt := range accepted {
		tt := tt
		t.Run("accept/"+tt.name, func(t *testing.T) {
			t.Parallel()

			if err := ValidateID(tt.kind, tt.id); err != nil {
				t.Fatalf("ValidateID(%q, %q) returned error: %v", tt.kind, tt.id, err)
			}
		})
	}

	rejected := []struct {
		name string
		kind IDKind
		id   string
	}{
		{name: "empty", kind: NamespaceID, id: ""},
		{name: "encoded separator slash", kind: RepoID, id: "repo_ab%2Fcd"},
		{name: "encoded separator backslash", kind: RepoID, id: "repo_ab%5ccd"},
		{name: "double encoded separator", kind: RepoID, id: "repo_ab%252fcd"},
		{name: "slash-like fraction slash", kind: RepoID, id: "repo_ab\u2044cd"},
		{name: "slash-like division slash", kind: RepoID, id: "repo_ab\u2215cd"},
		{name: "slash-like fullwidth solidus", kind: RepoID, id: "repo_ab\uff0fcd"},
		{name: "control newline", kind: RepoID, id: "repo_ab\ncd"},
		{name: "control nul", kind: RepoID, id: "repo_ab\x00cd"},
		{name: "leading dot display segment", kind: RepoID, id: "repo_.hidden"},
		{name: "display name spaces", kind: RepoID, id: "repo_Project Alpha"},
		{name: "display name punctuation", kind: RepoID, id: "repo_Project.Alpha"},
		{name: "template id passed as repo", kind: RepoID, id: "tmpl_alpha"},
		{name: "repo id passed as template", kind: TemplateID, id: "repo_alpha"},
	}
	for _, tt := range rejected {
		tt := tt
		t.Run("reject/"+tt.name, func(t *testing.T) {
			t.Parallel()

			if err := ValidateID(tt.kind, tt.id); err == nil {
				t.Fatalf("ValidateID(%q, %q) succeeded, want error", tt.kind, tt.id)
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
		{name: "bad namespace", namespaceID: "ns_alpha/01", repoID: "repo_ok"},
		{name: "bad repo", namespaceID: "ns_ok", repoID: "repo_.."},
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

func TestResolveTemplatePathsRejectsInvalidIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		namespaceID string
		templateID  string
	}{
		{name: "bad namespace", namespaceID: "ns_alpha/01", templateID: "tmpl_ok"},
		{name: "bad template", namespaceID: "ns_ok", templateID: "repo_wrong"},
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
				paths, err := ResolveTemplatePaths("ns_alpha", "repo_project")
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
				paths, err := ResolveRepoPaths("repo_alpha", "repo_project")
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

func TestResolveCallerPathAcceptsSafeRelativePaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		rawPath  string
		clean    string
		segments []string
	}{
		{name: "single segment", rawPath: "file.txt", clean: "file.txt", segments: []string{"file.txt"}},
		{name: "nested", rawPath: "dir/subdir/file.txt", clean: "dir/subdir/file.txt", segments: []string{"dir", "subdir", "file.txt"}},
		{name: "url decoded space", rawPath: "dir/a%20b.txt", clean: "dir/a b.txt", segments: []string{"dir", "a b.txt"}},
		{name: "unicode filename", rawPath: "\u5ba2\u6237/\u62a5\u544a.txt", clean: "\u5ba2\u6237/\u62a5\u544a.txt", segments: []string{"\u5ba2\u6237", "\u62a5\u544a.txt"}},
		{name: "literal percent", rawPath: "dir/100%25.txt", clean: "dir/100%.txt", segments: []string{"dir", "100%.txt"}},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ResolveCallerPath(tt.rawPath)
			if err != nil {
				t.Fatalf("ResolveCallerPath(%q) returned error: %v", tt.rawPath, err)
			}
			if got.Clean != tt.clean {
				t.Fatalf("Clean mismatch: got %q, want %q", got.Clean, tt.clean)
			}
			if !reflect.DeepEqual(got.Segments, tt.segments) {
				t.Fatalf("Segments mismatch: got %#v, want %#v", got.Segments, tt.segments)
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

func TestValidateTraversalPlanRejectsSymlinkComponents(t *testing.T) {
	t.Parallel()

	plan := TraversalPlan{
		RootSubdir:      "afscp/namespaces/ns_alpha/repos/repo_project/payload",
		Segments:        []string{"dir", "file.txt"},
		NoFollow:        true,
		RejectHardlinks: true,
	}
	inspector := fakeTraversalInspector{
		"dir": {Exists: true, Type: EntryDirectory, Symlink: true},
	}

	err := ValidateTraversalPlan(plan, inspector)
	if !errors.Is(err, ErrPathEscape) {
		t.Fatalf("err = %v, want ErrPathEscape", err)
	}
}

func TestValidateTraversalPlanRejectsHardlinkedFiles(t *testing.T) {
	t.Parallel()

	plan := TraversalPlan{
		RootSubdir:      "afscp/namespaces/ns_alpha/repos/repo_project/payload",
		Segments:        []string{"dir", "file.txt"},
		NoFollow:        true,
		RejectHardlinks: true,
	}
	inspector := fakeTraversalInspector{
		"dir":          {Exists: true, Type: EntryDirectory, LinkCount: 1},
		"dir/file.txt": {Exists: true, Type: EntryFile, LinkCount: 2},
	}

	err := ValidateTraversalPlan(plan, inspector)
	if !errors.Is(err, ErrPathEscape) {
		t.Fatalf("err = %v, want ErrPathEscape", err)
	}
}

func TestValidateTraversalPlanAcceptsPlainComponents(t *testing.T) {
	t.Parallel()

	plan := TraversalPlan{
		RootSubdir:      "afscp/namespaces/ns_alpha/repos/repo_project/payload",
		Segments:        []string{"dir", "file.txt"},
		NoFollow:        true,
		RejectHardlinks: true,
	}
	inspector := fakeTraversalInspector{
		"dir":          {Exists: true, Type: EntryDirectory, LinkCount: 1},
		"dir/file.txt": {Exists: true, Type: EntryFile, LinkCount: 1},
	}

	if err := ValidateTraversalPlan(plan, inspector); err != nil {
		t.Fatalf("ValidateTraversalPlan returned error: %v", err)
	}
}

func TestValidateTraversalPlanRequiresEscapeDefenseFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		plan TraversalPlan
	}{
		{
			name: "missing nofollow",
			plan: TraversalPlan{
				RootSubdir:      "afscp/namespaces/ns_alpha/repos/repo_project/payload",
				Segments:        []string{"dir", "file.txt"},
				NoFollow:        false,
				RejectHardlinks: true,
			},
		},
		{
			name: "missing hardlink rejection",
			plan: TraversalPlan{
				RootSubdir:      "afscp/namespaces/ns_alpha/repos/repo_project/payload",
				Segments:        []string{"dir", "file.txt"},
				NoFollow:        true,
				RejectHardlinks: false,
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateTraversalPlan(tt.plan, fakeTraversalInspector{})
			if !errors.Is(err, ErrPathEscape) {
				t.Fatalf("err = %v, want ErrPathEscape", err)
			}
		})
	}
}

func TestResolveCallerPathRejectsUnsafePaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		rawPath string
	}{
		{name: "empty", rawPath: ""},
		{name: "dot", rawPath: "."},
		{name: "dot segment", rawPath: "dir/./file"},
		{name: "absolute", rawPath: "/dir/file"},
		{name: "windows absolute", rawPath: `C:\dir\file`},
		{name: "parent", rawPath: "../file"},
		{name: "nested parent", rawPath: "dir/../file"},
		{name: "empty middle segment", rawPath: "dir//file"},
		{name: "trailing slash", rawPath: "dir/"},
		{name: "backslash", rawPath: `dir\file`},
		{name: "encoded parent", rawPath: "dir/%2e%2e/file"},
		{name: "encoded slash", rawPath: "dir%2ffile"},
		{name: "encoded backslash", rawPath: "dir%5cfile"},
		{name: "double encoded traversal", rawPath: "dir/%252e%252e%252fsecret"},
		{name: "malformed escape", rawPath: "dir/%zz/file"},
		{name: "unicode division slash", rawPath: "dir\u2215file"},
		{name: "unicode fullwidth solidus", rawPath: "dir\uff0ffile"},
		{name: "control char", rawPath: "dir/\n/file"},
		{name: "root jvs", rawPath: ".jvs/config"},
		{name: "nested jvs", rawPath: "dir/.jvs/config"},
		{name: "encoded jvs", rawPath: "%2ejvs/config"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := ResolveCallerPath(tt.rawPath); err == nil {
				t.Fatalf("ResolveCallerPath(%q) succeeded, want error", tt.rawPath)
			}
		})
	}
}

func TestRepoPathContractCallerPathCorpus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		rawPath string
	}{
		{name: "absolute unix", rawPath: "/payload/file.txt"},
		{name: "absolute windows drive", rawPath: `C:\payload\file.txt`},
		{name: "absolute windows root", rawPath: `\payload\file.txt`},
		{name: "dot segment", rawPath: "dir/./file.txt"},
		{name: "parent traversal", rawPath: "../file.txt"},
		{name: "nested parent traversal", rawPath: "dir/../file.txt"},
		{name: "encoded parent traversal", rawPath: "dir/%2e%2e/file.txt"},
		{name: "encoded separator slash lower", rawPath: "dir%2ffile.txt"},
		{name: "encoded separator slash upper", rawPath: "dir%2Ffile.txt"},
		{name: "encoded separator backslash lower", rawPath: "dir%5cfile.txt"},
		{name: "encoded separator backslash upper", rawPath: "dir%5Cfile.txt"},
		{name: "double encoded parent", rawPath: "dir/%252e%252e/file.txt"},
		{name: "double encoded slash", rawPath: "dir%252ffile.txt"},
		{name: "double encoded backslash", rawPath: "dir%255cfile.txt"},
		{name: "slash-like fraction slash", rawPath: "dir\u2044file.txt"},
		{name: "slash-like division slash", rawPath: "dir\u2215file.txt"},
		{name: "slash-like fullwidth solidus", rawPath: "dir\uff0ffile.txt"},
		{name: "slash-like fullwidth reverse solidus", rawPath: "dir\uff3cfile.txt"},
		{name: "control newline", rawPath: "dir/\n/file.txt"},
		{name: "control nul", rawPath: "dir/\x00/file.txt"},
		{name: "root jvs directory", rawPath: ".jvs"},
		{name: "root jvs access", rawPath: ".jvs/config.json"},
		{name: "root jvs create case variant", rawPath: ".JVS/config.json"},
		{name: "nested jvs access", rawPath: "dir/.jvs/config.json"},
		{name: "encoded jvs dot", rawPath: "%2ejvs/config.json"},
		{name: "encoded jvs mixed case", rawPath: "%2EJVS/config.json"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := ResolveCallerPath(tt.rawPath); err == nil {
				t.Fatalf("ResolveCallerPath(%q) succeeded, want error", tt.rawPath)
			}
		})
	}
}

func TestRepoPathContractPayloadTraversalCorpus(t *testing.T) {
	t.Parallel()

	paths, err := ResolveRepoPaths("ns_alpha", "repo_project")
	if err != nil {
		t.Fatalf("ResolveRepoPaths returned error: %v", err)
	}

	accepted, err := ResolvePayloadTraversal(paths.PayloadVolumeSubdir, "dir/file.txt")
	if err != nil {
		t.Fatalf("ResolvePayloadTraversal returned error: %v", err)
	}
	if !accepted.NoFollow {
		t.Fatalf("NoFollow = false, want true in traversal plan: %#v", accepted)
	}
	if !accepted.RejectHardlinks {
		t.Fatalf("RejectHardlinks = false, want true in traversal plan: %#v", accepted)
	}

	templatePaths, err := ResolveTemplatePaths("ns_alpha", "tmpl_base")
	if err != nil {
		t.Fatalf("ResolveTemplatePaths returned error: %v", err)
	}
	templateAccepted, err := ResolvePayloadTraversal(templatePaths.PayloadVolumeSubdir, "seed/file.txt")
	if err != nil {
		t.Fatalf("ResolvePayloadTraversal for template returned error: %v", err)
	}
	if templateAccepted.RootSubdir != templatePaths.PayloadVolumeSubdir {
		t.Fatalf("template RootSubdir = %q, want %q", templateAccepted.RootSubdir, templatePaths.PayloadVolumeSubdir)
	}

	rejected := []struct {
		name       string
		rootSubdir string
		rawPath    string
	}{
		{name: "control root is not payload root", rootSubdir: paths.ControlVolumeSubdir, rawPath: "dir/file.txt"},
		{name: "repo root with template id", rootSubdir: "afscp/namespaces/ns_alpha/repos/tmpl_base/payload", rawPath: "dir/file.txt"},
		{name: "template root with repo id", rootSubdir: "afscp/namespaces/ns_alpha/templates/repo_project/payload", rawPath: "dir/file.txt"},
		{name: "root contains jvs", rootSubdir: "afscp/namespaces/ns_alpha/repos/repo_project/.jvs/payload", rawPath: "dir/file.txt"},
		{name: "payload path attempts jvs", rootSubdir: paths.PayloadVolumeSubdir, rawPath: ".jvs/config.json"},
		{name: "payload path encoded separator", rootSubdir: paths.PayloadVolumeSubdir, rawPath: "dir%2ffile.txt"},
		{name: "payload path double encoded traversal", rootSubdir: paths.PayloadVolumeSubdir, rawPath: "dir/%252e%252e/file.txt"},
	}
	for _, tt := range rejected {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := ResolvePayloadTraversal(tt.rootSubdir, tt.rawPath); err == nil {
				t.Fatalf("ResolvePayloadTraversal(%q, %q) succeeded, want error", tt.rootSubdir, tt.rawPath)
			}
		})
	}
}

type fakeTraversalInspector map[string]TraversalEntry

func (inspector fakeTraversalInspector) InspectTraversalEntry(segments []string) (TraversalEntry, error) {
	return inspector[strings.Join(segments, "/")], nil
}

func repeatForTest(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
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
