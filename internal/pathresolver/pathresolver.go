package pathresolver

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	ErrInvalidID   = errors.New("invalid id")
	ErrInvalidPath = errors.New("invalid caller path")
	ErrPathEscape  = errors.New("path escape risk")
)

type IDKind string

const (
	NamespaceID            IDKind = "namespace_id"
	RepoID                 IDKind = "repo_id"
	TemplateID             IDKind = "template_id"
	VolumeID               IDKind = "volume_id"
	ExportID               IDKind = "export_id"
	WorkloadMountBindingID IDKind = "mount_binding_id"
	MountID                IDKind = WorkloadMountBindingID
	OperationID            IDKind = "operation_id"
)

type idSpec struct {
	prefix string
}

var idSpecs = map[IDKind]idSpec{
	NamespaceID:            {prefix: "ns_"},
	RepoID:                 {prefix: "repo_"},
	TemplateID:             {prefix: "tmpl_"},
	VolumeID:               {prefix: "vol_"},
	ExportID:               {prefix: "export_"},
	WorkloadMountBindingID: {prefix: "wmb_"},
	OperationID:            {prefix: "op_"},
}

type RepoPaths struct {
	ContainerVolumeSubdir string
	ControlVolumeSubdir   string
	PayloadVolumeSubdir   string
}

type TemplatePaths struct {
	ContainerVolumeSubdir string
	ControlVolumeSubdir   string
	PayloadVolumeSubdir   string
}

type CallerPath struct {
	// Deprecated: use ResolvePayloadTraversal when a caller path will be resolved under a storage root.
	Clean    string
	Segments []string
}

type TraversalPlan struct {
	RootSubdir      string
	Segments        []string
	NoFollow        bool
	RejectHardlinks bool
}

type EntryType string

const (
	EntryUnknown   EntryType = ""
	EntryDirectory EntryType = "directory"
	EntryFile      EntryType = "file"
	EntryOther     EntryType = "other"
)

type TraversalEntry struct {
	Exists    bool
	Type      EntryType
	Symlink   bool
	LinkCount uint64
}

type TraversalInspector interface {
	InspectTraversalEntry(segments []string) (TraversalEntry, error)
}

func ValidateID(kind IDKind, id string) error {
	spec, ok := idSpecs[kind]
	if !ok {
		return fmt.Errorf("%w: unknown kind %q", ErrInvalidID, kind)
	}
	if !strings.HasPrefix(id, spec.prefix) {
		return fmt.Errorf("%w: %s must start with %q", ErrInvalidID, kind, spec.prefix)
	}

	suffix := id[len(spec.prefix):]
	if len(suffix) < 2 || len(suffix) > 63 {
		return fmt.Errorf("%w: %s suffix length must be between 2 and 63", ErrInvalidID, kind)
	}
	if !isASCIIAlnum(suffix[0]) {
		return fmt.Errorf("%w: %s suffix must start with an ASCII letter or digit", ErrInvalidID, kind)
	}
	for i := 1; i < len(suffix); i++ {
		if !isIDChar(suffix[i]) {
			return fmt.Errorf("%w: %s contains an unsafe character", ErrInvalidID, kind)
		}
	}

	return nil
}

func ResolveRepoPaths(namespaceID, repoID string) (RepoPaths, error) {
	if err := ValidateID(NamespaceID, namespaceID); err != nil {
		return RepoPaths{}, err
	}
	if err := ValidateID(RepoID, repoID); err != nil {
		return RepoPaths{}, err
	}

	container := "afscp/namespaces/" + namespaceID + "/repos/" + repoID
	return RepoPaths{
		ContainerVolumeSubdir: container,
		ControlVolumeSubdir:   container + "/control",
		PayloadVolumeSubdir:   container + "/payload",
	}, nil
}

func ResolveTemplatePaths(namespaceID, templateID string) (TemplatePaths, error) {
	if err := ValidateID(NamespaceID, namespaceID); err != nil {
		return TemplatePaths{}, err
	}
	if err := ValidateID(TemplateID, templateID); err != nil {
		return TemplatePaths{}, err
	}

	container := "afscp/namespaces/" + namespaceID + "/templates/" + templateID
	return TemplatePaths{
		ContainerVolumeSubdir: container,
		ControlVolumeSubdir:   container + "/control",
		PayloadVolumeSubdir:   container + "/payload",
	}, nil
}

func ResolveCallerPath(rawPath string) (CallerPath, error) {
	if rawPath == "" {
		return CallerPath{}, fmt.Errorf("%w: empty path", ErrInvalidPath)
	}
	if !utf8.ValidString(rawPath) {
		return CallerPath{}, fmt.Errorf("%w: path is not valid UTF-8", ErrInvalidPath)
	}
	if isAbsoluteLike(rawPath) {
		return CallerPath{}, fmt.Errorf("%w: absolute path", ErrInvalidPath)
	}
	if hasSlashLikeOrControl(rawPath) {
		return CallerPath{}, fmt.Errorf("%w: slash-like or control character", ErrInvalidPath)
	}
	if containsEncodedSeparator(rawPath) {
		return CallerPath{}, fmt.Errorf("%w: encoded separator", ErrInvalidPath)
	}

	decoded, err := url.PathUnescape(rawPath)
	if err != nil {
		return CallerPath{}, fmt.Errorf("%w: malformed percent escape", ErrInvalidPath)
	}
	if !utf8.ValidString(decoded) {
		return CallerPath{}, fmt.Errorf("%w: decoded path is not valid UTF-8", ErrInvalidPath)
	}
	if containsEncodedPathMeta(decoded) {
		return CallerPath{}, fmt.Errorf("%w: double-encoded path metacharacter", ErrInvalidPath)
	}
	if isAbsoluteLike(decoded) {
		return CallerPath{}, fmt.Errorf("%w: decoded absolute path", ErrInvalidPath)
	}
	if hasSlashLikeOrControl(decoded) {
		return CallerPath{}, fmt.Errorf("%w: decoded slash-like or control character", ErrInvalidPath)
	}

	segments := strings.Split(decoded, "/")
	for _, segment := range segments {
		switch {
		case segment == "":
			return CallerPath{}, fmt.Errorf("%w: empty segment", ErrInvalidPath)
		case segment == ".":
			return CallerPath{}, fmt.Errorf("%w: dot segment", ErrInvalidPath)
		case segment == "..":
			return CallerPath{}, fmt.Errorf("%w: parent traversal", ErrInvalidPath)
		case strings.EqualFold(segment, ".jvs"):
			return CallerPath{}, fmt.Errorf("%w: .jvs segment", ErrInvalidPath)
		}
	}

	clean := strings.Join(segments, "/")
	return CallerPath{
		Clean:    clean,
		Segments: append([]string(nil), segments...),
	}, nil
}

func ResolvePayloadTraversal(rootSubdir, rawPath string) (TraversalPlan, error) {
	if err := validateManagedSubdir(rootSubdir); err != nil {
		return TraversalPlan{}, err
	}

	callerPath, err := ResolveCallerPath(rawPath)
	if err != nil {
		return TraversalPlan{}, err
	}

	return TraversalPlan{
		RootSubdir:      rootSubdir,
		Segments:        append([]string(nil), callerPath.Segments...),
		NoFollow:        true,
		RejectHardlinks: true,
	}, nil
}

func ValidateTraversalPlan(plan TraversalPlan, inspector TraversalInspector) error {
	if err := validateManagedSubdir(plan.RootSubdir); err != nil {
		return err
	}
	if !plan.NoFollow {
		return fmt.Errorf("%w: traversal plan must not follow symlinks", ErrPathEscape)
	}
	if !plan.RejectHardlinks {
		return fmt.Errorf("%w: traversal plan must reject hardlinks", ErrPathEscape)
	}
	if inspector == nil {
		return fmt.Errorf("%w: nil traversal inspector", ErrPathEscape)
	}
	if err := validateCallerSegments(plan.Segments); err != nil {
		return err
	}

	for i := range plan.Segments {
		prefix := append([]string(nil), plan.Segments[:i+1]...)
		entry, err := inspector.InspectTraversalEntry(prefix)
		if err != nil {
			return err
		}
		if !entry.Exists {
			continue
		}
		if plan.NoFollow && entry.Symlink {
			return fmt.Errorf("%w: symlink component %q", ErrPathEscape, strings.Join(prefix, "/"))
		}
		if plan.RejectHardlinks && entry.Type == EntryFile && entry.LinkCount > 1 {
			return fmt.Errorf("%w: hardlinked file %q", ErrPathEscape, strings.Join(prefix, "/"))
		}
	}

	return nil
}

func validateManagedSubdir(subdir string) error {
	if subdir == "" {
		return fmt.Errorf("%w: empty managed subdir", ErrInvalidPath)
	}
	if !utf8.ValidString(subdir) {
		return fmt.Errorf("%w: managed subdir is not valid UTF-8", ErrInvalidPath)
	}
	if isAbsoluteLike(subdir) {
		return fmt.Errorf("%w: absolute managed subdir", ErrInvalidPath)
	}
	if hasSlashLikeOrControl(subdir) {
		return fmt.Errorf("%w: slash-like or control character in managed subdir", ErrInvalidPath)
	}
	if strings.Contains(subdir, "%") {
		return fmt.Errorf("%w: encoded managed subdir", ErrInvalidPath)
	}

	segments := strings.Split(subdir, "/")
	if err := validateCallerSegments(segments); err != nil {
		return err
	}
	if len(segments) != 6 || segments[0] != "afscp" || segments[1] != "namespaces" || segments[5] != "payload" {
		return fmt.Errorf("%w: managed subdir must be a canonical payload root", ErrInvalidPath)
	}
	if err := ValidateID(NamespaceID, segments[2]); err != nil {
		return err
	}

	switch segments[3] {
	case "repos":
		if err := ValidateID(RepoID, segments[4]); err != nil {
			return err
		}
	case "templates":
		if err := ValidateID(TemplateID, segments[4]); err != nil {
			return err
		}
	default:
		return fmt.Errorf("%w: managed subdir must target repos or templates", ErrInvalidPath)
	}

	return nil
}

func validateCallerSegments(segments []string) error {
	if len(segments) == 0 {
		return fmt.Errorf("%w: empty path", ErrInvalidPath)
	}
	for _, segment := range segments {
		switch {
		case segment == "":
			return fmt.Errorf("%w: empty segment", ErrInvalidPath)
		case segment == ".":
			return fmt.Errorf("%w: dot segment", ErrInvalidPath)
		case segment == "..":
			return fmt.Errorf("%w: parent traversal", ErrInvalidPath)
		case strings.EqualFold(segment, ".jvs"):
			return fmt.Errorf("%w: .jvs segment", ErrInvalidPath)
		case hasSlashLikeOrControl(segment):
			return fmt.Errorf("%w: slash-like or control character", ErrInvalidPath)
		}
	}
	return nil
}

func isIDChar(b byte) bool {
	return isASCIIAlnum(b) || b == '_' || b == '-'
}

func isASCIIAlnum(b byte) bool {
	return ('A' <= b && b <= 'Z') || ('a' <= b && b <= 'z') || ('0' <= b && b <= '9')
}

func isAbsoluteLike(path string) bool {
	if strings.HasPrefix(path, "/") || strings.HasPrefix(path, "\\") {
		return true
	}
	if len(path) >= 2 && isASCIILetter(path[0]) && path[1] == ':' {
		return true
	}
	return false
}

func isASCIILetter(b byte) bool {
	return ('A' <= b && b <= 'Z') || ('a' <= b && b <= 'z')
}

func hasSlashLikeOrControl(path string) bool {
	for _, r := range path {
		if unicode.IsControl(r) || isSlashLikeRune(r) {
			return true
		}
	}
	return false
}

func isSlashLikeRune(r rune) bool {
	switch r {
	case '\\',
		'\u2044', // fraction slash
		'\u2215', // division slash
		'\u2216', // set minus
		'\u27cd', // mathematical falling diagonal
		'\u29f8', // big solidus
		'\u29f9', // big reverse solidus
		'\u2571', // box drawings light diagonal upper right to lower left
		'\u2572', // box drawings light diagonal upper left to lower right
		'\ufe68', // small reverse solidus
		'\uff0f', // fullwidth solidus
		'\uff3c': // fullwidth reverse solidus
		return true
	default:
		return false
	}
}

func containsEncodedSeparator(path string) bool {
	for i := 0; i+2 < len(path); i++ {
		if path[i] != '%' {
			continue
		}
		value, ok := hexByte(path[i+1], path[i+2])
		if !ok {
			continue
		}
		if value == '/' || value == '\\' {
			return true
		}
		i += 2
	}
	return false
}

func containsEncodedPathMeta(path string) bool {
	for i := 0; i+2 < len(path); i++ {
		if path[i] != '%' {
			continue
		}
		value, ok := hexByte(path[i+1], path[i+2])
		if !ok {
			continue
		}
		if value == '.' || value == '/' || value == '\\' {
			return true
		}
		i += 2
	}
	return false
}

func hexByte(hi, lo byte) (byte, bool) {
	high, ok := hexValue(hi)
	if !ok {
		return 0, false
	}
	low, ok := hexValue(lo)
	if !ok {
		return 0, false
	}
	return high<<4 | low, true
}

func hexValue(b byte) (byte, bool) {
	switch {
	case '0' <= b && b <= '9':
		return b - '0', true
	case 'a' <= b && b <= 'f':
		return b - 'a' + 10, true
	case 'A' <= b && b <= 'F':
		return b - 'A' + 10, true
	default:
		return 0, false
	}
}
