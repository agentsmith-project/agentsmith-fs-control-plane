package testcorpus

const (
	KindNamespace            = "namespace_id"
	KindRepo                 = "repo_id"
	KindTemplate             = "template_id"
	KindVolume               = "volume_id"
	KindExport               = "export_id"
	KindWorkloadMountBinding = "mount_binding_id"
	KindOperation            = "operation_id"

	TagRepoManagedRoot          = "repo_managed_root"
	TagTemplateManagedRoot      = "template_managed_root"
	TagJVS                      = "jvs"
	TagEncodedSeparator         = "encoded_separator"
	TagDoubleEncodedSeparator   = "double_encoded_separator"
	TagUnicodeSlashLike         = "unicode_slash_like"
	TagControlChar              = "control_char"
	TagSymlink                  = "symlink"
	TagHardlink                 = "hardlink"
	TagTraversalDefenseFlag     = "traversal_defense_flag"
	TagNilInspector             = "nil_inspector"
	TagManagedRootKindMismatch  = "managed_root_kind_mismatch"
	TagManagedRootControlRoot   = "managed_root_control_root"
	TagManagedRootPayloadEscape = "managed_root_payload_escape"
)

type IDCase struct {
	Name string
	Kind string
	ID   string
	Tags []string
}

type CallerPathCase struct {
	Name     string
	RawPath  string
	Clean    string
	Segments []string
	Tags     []string
}

type ManagedRootCase struct {
	Name       string
	RootSubdir string
	Tags       []string
}

type PayloadTraversalCase struct {
	Name       string
	RootSubdir string
	RawPath    string
	Tags       []string
}

type TraversalPlanCase struct {
	Name            string
	RootSubdir      string
	Segments        []string
	NoFollow        bool
	RejectHardlinks bool
	InspectorNil    bool
	Entries         []TraversalEntry
	WantEscape      bool
	Tags            []string
}

type TraversalEntry struct {
	Path      string
	Exists    bool
	Type      string
	Symlink   bool
	LinkCount uint64
}

func AcceptedIDCases() []IDCase {
	return cloneIDCases(acceptedIDCases)
}

func RejectedIDCases() []IDCase {
	return cloneIDCases(rejectedIDCases)
}

func AcceptedCallerPathCases() []CallerPathCase {
	return cloneCallerPathCases(acceptedCallerPathCases)
}

func RejectedCallerPathCases() []CallerPathCase {
	return cloneCallerPathCases(rejectedCallerPathCases)
}

func AcceptedManagedRootCases() []ManagedRootCase {
	return cloneManagedRootCases(acceptedManagedRootCases)
}

func RejectedManagedRootCases() []ManagedRootCase {
	return cloneManagedRootCases(rejectedManagedRootCases)
}

func RejectedPayloadTraversalCases() []PayloadTraversalCase {
	return clonePayloadTraversalCases(rejectedPayloadTraversalCases)
}

func TraversalPlanCases() []TraversalPlanCase {
	return cloneTraversalPlanCases(traversalPlanCases)
}

func AllTags() []string {
	var tags []string
	for _, tt := range acceptedIDCases {
		tags = append(tags, tt.Tags...)
	}
	for _, tt := range rejectedIDCases {
		tags = append(tags, tt.Tags...)
	}
	for _, tt := range acceptedCallerPathCases {
		tags = append(tags, tt.Tags...)
	}
	for _, tt := range rejectedCallerPathCases {
		tags = append(tags, tt.Tags...)
	}
	for _, tt := range acceptedManagedRootCases {
		tags = append(tags, tt.Tags...)
	}
	for _, tt := range rejectedManagedRootCases {
		tags = append(tags, tt.Tags...)
	}
	for _, tt := range rejectedPayloadTraversalCases {
		tags = append(tags, tt.Tags...)
	}
	for _, tt := range traversalPlanCases {
		tags = append(tags, tt.Tags...)
	}
	return append([]string(nil), tags...)
}

var acceptedIDCases = []IDCase{
	{Name: "namespace lowercase", Kind: KindNamespace, ID: "ns_ab"},
	{Name: "namespace mixed", Kind: KindNamespace, ID: "ns_A9-_b"},
	{Name: "repo lowercase", Kind: KindRepo, ID: "repo_ab"},
	{Name: "repo mixed", Kind: KindRepo, ID: "repo_A1-b_2"},
	{Name: "repo max suffix", Kind: KindRepo, ID: "repo_A" + repeat("b", 62)},
	{Name: "template", Kind: KindTemplate, ID: "tmpl_T9"},
	{Name: "volume", Kind: KindVolume, ID: "vol_Z9"},
	{Name: "export", Kind: KindExport, ID: "export_a-b_c9"},
	{Name: "mount binding", Kind: KindWorkloadMountBinding, ID: "wmb_m0"},
	{Name: "operation", Kind: KindOperation, ID: "op_xY"},
}

var rejectedIDCases = []IDCase{
	{Name: "empty", Kind: KindNamespace, ID: ""},
	{Name: "wrong prefix", Kind: KindRepo, ID: "ns_ab"},
	{Name: "too short suffix", Kind: KindNamespace, ID: "ns_a"},
	{Name: "too long suffix", Kind: KindNamespace, ID: "ns_A" + repeat("b", 63)},
	{Name: "first suffix underscore", Kind: KindVolume, ID: "vol__a"},
	{Name: "first suffix hyphen", Kind: KindVolume, ID: "vol_-a"},
	{Name: "first suffix dot", Kind: KindRepo, ID: "repo_.hidden"},
	{Name: "slash", Kind: KindRepo, ID: "repo_ab/cd"},
	{Name: "backslash", Kind: KindRepo, ID: "repo_ab\\cd"},
	{Name: "dot", Kind: KindTemplate, ID: "tmpl_ab.cd"},
	{Name: "space", Kind: KindExport, ID: "export_ab cd"},
	{Name: "display name", Kind: KindRepo, ID: "repo_My Project"},
	{Name: "display name punctuation", Kind: KindTemplate, ID: "tmpl_Base:v1"},
	{Name: "encoded separator slash lower", Kind: KindRepo, ID: "repo_ab%2fcd", Tags: []string{TagEncodedSeparator}},
	{Name: "encoded separator slash upper", Kind: KindRepo, ID: "repo_ab%2Fcd", Tags: []string{TagEncodedSeparator}},
	{Name: "encoded separator backslash", Kind: KindRepo, ID: "repo_ab%5ccd", Tags: []string{TagEncodedSeparator}},
	{Name: "double encoded separator", Kind: KindRepo, ID: "repo_ab%252fcd", Tags: []string{TagDoubleEncodedSeparator}},
	{Name: "unicode fraction slash", Kind: KindRepo, ID: "repo_ab\u2044cd", Tags: []string{TagUnicodeSlashLike}},
	{Name: "unicode division slash", Kind: KindRepo, ID: "repo_ab\u2215cd", Tags: []string{TagUnicodeSlashLike}},
	{Name: "unicode fullwidth solidus", Kind: KindRepo, ID: "repo_ab\uff0fcd", Tags: []string{TagUnicodeSlashLike}},
	{Name: "unicode fullwidth reverse solidus", Kind: KindRepo, ID: "repo_ab\uff3ccd", Tags: []string{TagUnicodeSlashLike}},
	{Name: "unicode letter", Kind: KindOperation, ID: "op_ab\u00e9"},
	{Name: "control newline", Kind: KindWorkloadMountBinding, ID: "wmb_ab\ncd", Tags: []string{TagControlChar}},
	{Name: "control nul", Kind: KindRepo, ID: "repo_ab\x00cd", Tags: []string{TagControlChar}},
	{Name: "template id passed as repo", Kind: KindRepo, ID: "tmpl_alpha"},
	{Name: "repo id passed as template", Kind: KindTemplate, ID: "repo_alpha"},
	{Name: "unknown kind", Kind: "other", ID: "ns_ab"},
}

var acceptedCallerPathCases = []CallerPathCase{
	{Name: "single segment", RawPath: "file.txt", Clean: "file.txt", Segments: []string{"file.txt"}},
	{Name: "nested", RawPath: "dir/subdir/file.txt", Clean: "dir/subdir/file.txt", Segments: []string{"dir", "subdir", "file.txt"}},
	{Name: "url decoded space", RawPath: "dir/a%20b.txt", Clean: "dir/a b.txt", Segments: []string{"dir", "a b.txt"}},
	{Name: "unicode filename", RawPath: "\u5ba2\u6237/\u62a5\u544a.txt", Clean: "\u5ba2\u6237/\u62a5\u544a.txt", Segments: []string{"\u5ba2\u6237", "\u62a5\u544a.txt"}},
	{Name: "literal percent", RawPath: "dir/100%25.txt", Clean: "dir/100%.txt", Segments: []string{"dir", "100%.txt"}},
}

var rejectedCallerPathCases = []CallerPathCase{
	{Name: "empty", RawPath: ""},
	{Name: "dot", RawPath: "."},
	{Name: "dot segment", RawPath: "dir/./file"},
	{Name: "absolute unix", RawPath: "/payload/file.txt"},
	{Name: "absolute windows drive", RawPath: `C:\payload\file.txt`},
	{Name: "absolute windows root", RawPath: `\payload\file.txt`},
	{Name: "parent traversal", RawPath: "../file.txt"},
	{Name: "nested parent traversal", RawPath: "dir/../file.txt"},
	{Name: "empty middle segment", RawPath: "dir//file"},
	{Name: "trailing slash", RawPath: "dir/"},
	{Name: "backslash", RawPath: `dir\file`},
	{Name: "encoded parent traversal", RawPath: "dir/%2e%2e/file.txt"},
	{Name: "encoded separator slash lower", RawPath: "dir%2ffile.txt", Tags: []string{TagEncodedSeparator}},
	{Name: "encoded separator slash upper", RawPath: "dir%2Ffile.txt", Tags: []string{TagEncodedSeparator}},
	{Name: "encoded separator backslash lower", RawPath: "dir%5cfile.txt", Tags: []string{TagEncodedSeparator}},
	{Name: "encoded separator backslash upper", RawPath: "dir%5Cfile.txt", Tags: []string{TagEncodedSeparator}},
	{Name: "double encoded parent", RawPath: "dir/%252e%252e/file.txt"},
	{Name: "double encoded slash", RawPath: "dir%252ffile.txt", Tags: []string{TagDoubleEncodedSeparator}},
	{Name: "double encoded backslash", RawPath: "dir%255cfile.txt", Tags: []string{TagDoubleEncodedSeparator}},
	{Name: "malformed escape", RawPath: "dir/%zz/file"},
	{Name: "unicode fraction slash", RawPath: "dir\u2044file.txt", Tags: []string{TagUnicodeSlashLike}},
	{Name: "unicode division slash", RawPath: "dir\u2215file.txt", Tags: []string{TagUnicodeSlashLike}},
	{Name: "unicode fullwidth solidus", RawPath: "dir\uff0ffile.txt", Tags: []string{TagUnicodeSlashLike}},
	{Name: "unicode fullwidth reverse solidus", RawPath: "dir\uff3cfile.txt", Tags: []string{TagUnicodeSlashLike}},
	{Name: "control newline", RawPath: "dir/\n/file.txt", Tags: []string{TagControlChar}},
	{Name: "control nul", RawPath: "dir/\x00/file.txt", Tags: []string{TagControlChar}},
	{Name: "root jvs directory", RawPath: ".jvs", Tags: []string{TagJVS}},
	{Name: "root jvs access", RawPath: ".jvs/config.json", Tags: []string{TagJVS}},
	{Name: "root jvs create case variant", RawPath: ".JVS/config.json", Tags: []string{TagJVS}},
	{Name: "nested jvs access", RawPath: "dir/.jvs/config.json", Tags: []string{TagJVS}},
	{Name: "encoded jvs dot", RawPath: "%2ejvs/config.json", Tags: []string{TagJVS}},
	{Name: "encoded jvs mixed case", RawPath: "%2EJVS/config.json", Tags: []string{TagJVS}},
}

var acceptedManagedRootCases = []ManagedRootCase{
	{Name: "repo payload root", RootSubdir: "afscp/namespaces/ns_alpha/repos/repo_project/payload", Tags: []string{TagRepoManagedRoot}},
	{Name: "template payload root", RootSubdir: "afscp/namespaces/ns_alpha/templates/tmpl_base/payload", Tags: []string{TagTemplateManagedRoot}},
}

var rejectedManagedRootCases = []ManagedRootCase{
	{Name: "empty", RootSubdir: ""},
	{Name: "absolute", RootSubdir: "/afscp/namespaces/ns_alpha/repos/repo_project/payload"},
	{Name: "control root is not payload root", RootSubdir: "afscp/namespaces/ns_alpha/repos/repo_project/control", Tags: []string{TagManagedRootControlRoot}},
	{Name: "repo root with template id", RootSubdir: "afscp/namespaces/ns_alpha/repos/tmpl_base/payload", Tags: []string{TagManagedRootKindMismatch}},
	{Name: "template root with repo id", RootSubdir: "afscp/namespaces/ns_alpha/templates/repo_project/payload", Tags: []string{TagManagedRootKindMismatch}},
	{Name: "root contains jvs", RootSubdir: "afscp/namespaces/ns_alpha/repos/repo_project/.jvs/payload", Tags: []string{TagJVS, TagManagedRootPayloadEscape}},
	{Name: "encoded separator", RootSubdir: "afscp/namespaces/ns_alpha/repos/repo_project/payload%2fsecret", Tags: []string{TagEncodedSeparator}},
	{Name: "double encoded separator", RootSubdir: "afscp/namespaces/ns_alpha/repos/repo_project/payload%252fsecret", Tags: []string{TagDoubleEncodedSeparator}},
	{Name: "unicode slash-like", RootSubdir: "afscp/namespaces/ns_alpha/repos/repo_project\u2215payload", Tags: []string{TagUnicodeSlashLike}},
	{Name: "control char", RootSubdir: "afscp/namespaces/ns_alpha/repos/repo_project/payload\n", Tags: []string{TagControlChar}},
}

var rejectedPayloadTraversalCases = []PayloadTraversalCase{
	{Name: "payload path attempts jvs", RootSubdir: "afscp/namespaces/ns_alpha/repos/repo_project/payload", RawPath: ".jvs/config.json", Tags: []string{TagJVS}},
	{Name: "payload path encoded separator", RootSubdir: "afscp/namespaces/ns_alpha/repos/repo_project/payload", RawPath: "dir%2ffile.txt", Tags: []string{TagEncodedSeparator}},
	{Name: "payload path double encoded traversal", RootSubdir: "afscp/namespaces/ns_alpha/repos/repo_project/payload", RawPath: "dir/%252e%252e/file.txt"},
}

var traversalPlanCases = []TraversalPlanCase{
	{
		Name:            "plain components",
		RootSubdir:      "afscp/namespaces/ns_alpha/repos/repo_project/payload",
		Segments:        []string{"dir", "file.txt"},
		NoFollow:        true,
		RejectHardlinks: true,
		Entries: []TraversalEntry{
			{Path: "dir", Exists: true, Type: "directory", LinkCount: 1},
			{Path: "dir/file.txt", Exists: true, Type: "file", LinkCount: 1},
		},
	},
	{
		Name:            "symlink component",
		RootSubdir:      "afscp/namespaces/ns_alpha/repos/repo_project/payload",
		Segments:        []string{"dir", "file.txt"},
		NoFollow:        true,
		RejectHardlinks: true,
		Entries: []TraversalEntry{
			{Path: "dir", Exists: true, Type: "directory", Symlink: true},
		},
		WantEscape: true,
		Tags:       []string{TagSymlink},
	},
	{
		Name:            "hardlinked file",
		RootSubdir:      "afscp/namespaces/ns_alpha/repos/repo_project/payload",
		Segments:        []string{"dir", "file.txt"},
		NoFollow:        true,
		RejectHardlinks: true,
		Entries: []TraversalEntry{
			{Path: "dir", Exists: true, Type: "directory", LinkCount: 1},
			{Path: "dir/file.txt", Exists: true, Type: "file", LinkCount: 2},
		},
		WantEscape: true,
		Tags:       []string{TagHardlink},
	},
	{
		Name:            "missing nofollow",
		RootSubdir:      "afscp/namespaces/ns_alpha/repos/repo_project/payload",
		Segments:        []string{"dir", "file.txt"},
		NoFollow:        false,
		RejectHardlinks: true,
		WantEscape:      true,
		Tags:            []string{TagTraversalDefenseFlag},
	},
	{
		Name:            "missing hardlink rejection",
		RootSubdir:      "afscp/namespaces/ns_alpha/repos/repo_project/payload",
		Segments:        []string{"dir", "file.txt"},
		NoFollow:        true,
		RejectHardlinks: false,
		WantEscape:      true,
		Tags:            []string{TagTraversalDefenseFlag},
	},
	{
		Name:            "nil inspector",
		RootSubdir:      "afscp/namespaces/ns_alpha/repos/repo_project/payload",
		Segments:        []string{"dir", "file.txt"},
		NoFollow:        true,
		RejectHardlinks: true,
		InspectorNil:    true,
		WantEscape:      true,
		Tags:            []string{TagNilInspector},
	},
}

func cloneIDCases(in []IDCase) []IDCase {
	out := append([]IDCase(nil), in...)
	for i := range out {
		out[i].Tags = append([]string(nil), out[i].Tags...)
	}
	return out
}

func cloneCallerPathCases(in []CallerPathCase) []CallerPathCase {
	out := append([]CallerPathCase(nil), in...)
	for i := range out {
		out[i].Segments = append([]string(nil), out[i].Segments...)
		out[i].Tags = append([]string(nil), out[i].Tags...)
	}
	return out
}

func cloneManagedRootCases(in []ManagedRootCase) []ManagedRootCase {
	out := append([]ManagedRootCase(nil), in...)
	for i := range out {
		out[i].Tags = append([]string(nil), out[i].Tags...)
	}
	return out
}

func clonePayloadTraversalCases(in []PayloadTraversalCase) []PayloadTraversalCase {
	out := append([]PayloadTraversalCase(nil), in...)
	for i := range out {
		out[i].Tags = append([]string(nil), out[i].Tags...)
	}
	return out
}

func cloneTraversalPlanCases(in []TraversalPlanCase) []TraversalPlanCase {
	out := append([]TraversalPlanCase(nil), in...)
	for i := range out {
		out[i].Segments = append([]string(nil), out[i].Segments...)
		out[i].Entries = append([]TraversalEntry(nil), out[i].Entries...)
		out[i].Tags = append([]string(nil), out[i].Tags...)
	}
	return out
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}
