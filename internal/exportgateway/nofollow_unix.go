//go:build unix

package exportgateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/webdav"
	"golang.org/x/sys/unix"
)

var errNoFollowUnsafe = errors.New("unsafe no-follow filesystem path")

type noFollowFileSystem struct {
	root   string
	rootFD int
}

func newNoFollowFileSystem(root string) (*noFollowFileSystem, error) {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root || root == string(filepath.Separator) {
		return nil, fmt.Errorf("%w: invalid root", errNoFollowUnsafe)
	}
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("%w: root must be a non-symlink directory", errNoFollowUnsafe)
	}
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	if err := ensureDirFD(fd); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	return &noFollowFileSystem{root: root, rootFD: fd}, nil
}

func (fs *noFollowFileSystem) Close() error {
	if fs.rootFD < 0 {
		return nil
	}
	err := unix.Close(fs.rootFD)
	fs.rootFD = -1
	return err
}

func (fs *noFollowFileSystem) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	segments, err := davSegments(name)
	if err != nil {
		return err
	}
	if len(segments) == 0 {
		return fmt.Errorf("%w: refusing to create payload root", errNoFollowUnsafe)
	}
	parentFD, leaf, err := fs.openParent(segments)
	if err != nil {
		return err
	}
	defer unix.Close(parentFD)
	return unix.Mkdirat(parentFD, leaf, uint32(perm.Perm()))
}

func (fs *noFollowFileSystem) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	segments, err := davSegments(name)
	if err != nil {
		return nil, err
	}
	fd, err := fs.openFileFD(segments, flag, perm)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), fs.displayName(segments))
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("%w: invalid file descriptor", errNoFollowUnsafe)
	}
	return &noFollowFile{file: file}, nil
}

func (fs *noFollowFileSystem) RemoveAll(ctx context.Context, name string) error {
	segments, err := davSegments(name)
	if err != nil {
		return err
	}
	if len(segments) == 0 {
		return fmt.Errorf("%w: refusing to remove payload root", errNoFollowUnsafe)
	}
	parentFD, leaf, err := fs.openParent(segments)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, unix.ENOENT) {
			return nil
		}
		return err
	}
	defer unix.Close(parentFD)

	if err := validateTreeAt(parentFD, leaf); err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, unix.ENOENT) {
			return nil
		}
		return err
	}
	if err := deleteTreeAt(parentFD, leaf); err != nil && (errors.Is(err, os.ErrNotExist) || errors.Is(err, unix.ENOENT)) {
		return nil
	} else {
		return err
	}
}

func (fs *noFollowFileSystem) Rename(ctx context.Context, oldName, newName string) error {
	oldSegments, err := davSegments(oldName)
	if err != nil {
		return err
	}
	newSegments, err := davSegments(newName)
	if err != nil {
		return err
	}
	if len(oldSegments) == 0 || len(newSegments) == 0 {
		return fmt.Errorf("%w: refusing to rename payload root", errNoFollowUnsafe)
	}
	oldParentFD, oldLeaf, err := fs.openParent(oldSegments)
	if err != nil {
		return err
	}
	defer unix.Close(oldParentFD)
	newParentFD, newLeaf, err := fs.openParent(newSegments)
	if err != nil {
		return err
	}
	defer unix.Close(newParentFD)

	if err := validateRenameEndpoint(oldParentFD, oldLeaf, true); err != nil {
		return err
	}
	if err := validateRenameEndpoint(newParentFD, newLeaf, false); err != nil {
		return err
	}
	return unix.Renameat(oldParentFD, oldLeaf, newParentFD, newLeaf)
}

func (fs *noFollowFileSystem) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	segments, err := davSegments(name)
	if err != nil {
		return nil, err
	}
	fd, err := fs.openStatFD(segments)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), fs.displayName(segments))
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("%w: invalid file descriptor", errNoFollowUnsafe)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if err := rejectUnsafeFileInfo(info); err != nil {
		return nil, err
	}
	return info, nil
}

func (fs *noFollowFileSystem) openFileFD(segments []string, flag int, perm os.FileMode) (int, error) {
	if len(segments) == 0 {
		if writeRequested(flag) {
			return -1, unix.EISDIR
		}
		return fs.dupRoot()
	}
	parentFD, leaf, err := fs.openParent(segments)
	if err != nil {
		return -1, err
	}
	defer unix.Close(parentFD)

	openFlag := flag | unix.O_NOFOLLOW | unix.O_CLOEXEC
	needsTruncate := openFlag&os.O_TRUNC != 0
	openFlag &^= os.O_TRUNC
	fd, err := unix.Openat(parentFD, leaf, openFlag, uint32(perm.Perm()))
	if err != nil {
		return -1, err
	}
	if err := validateOpenedFileFD(fd); err != nil {
		_ = unix.Close(fd)
		return -1, err
	}
	if needsTruncate {
		if err := truncateOpenedRegularFD(fd); err != nil {
			_ = unix.Close(fd)
			return -1, err
		}
	}
	return fd, nil
}

func (fs *noFollowFileSystem) openStatFD(segments []string) (int, error) {
	if len(segments) == 0 {
		return fs.dupRoot()
	}
	parentFD, leaf, err := fs.openParent(segments)
	if err != nil {
		return -1, err
	}
	defer unix.Close(parentFD)
	fd, err := unix.Openat(parentFD, leaf, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	if err := validateOpenedStatFD(fd); err != nil {
		_ = unix.Close(fd)
		return -1, err
	}
	return fd, nil
}

func (fs *noFollowFileSystem) openParent(segments []string) (int, string, error) {
	if len(segments) == 0 {
		return -1, "", fmt.Errorf("%w: payload root has no parent", errNoFollowUnsafe)
	}
	fd, err := fs.dupRoot()
	if err != nil {
		return -1, "", err
	}
	for _, segment := range segments[:len(segments)-1] {
		nextFD, err := unix.Openat(fd, segment, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		_ = unix.Close(fd)
		if err != nil {
			return -1, "", err
		}
		if err := ensureDirFD(nextFD); err != nil {
			_ = unix.Close(nextFD)
			return -1, "", err
		}
		fd = nextFD
	}
	return fd, segments[len(segments)-1], nil
}

func (fs *noFollowFileSystem) dupRoot() (int, error) {
	if fs.rootFD < 0 {
		return -1, os.ErrClosed
	}
	fd, err := unix.Dup(fs.rootFD)
	if err != nil {
		return -1, err
	}
	_, _ = unix.FcntlInt(uintptr(fd), unix.F_SETFD, unix.FD_CLOEXEC)
	return fd, nil
}

func (fs *noFollowFileSystem) displayName(segments []string) string {
	if len(segments) == 0 {
		return fs.root
	}
	return filepath.Join(append([]string{fs.root}, segments...)...)
}

type noFollowFile struct {
	file *os.File
}

func (file *noFollowFile) Close() error {
	return file.file.Close()
}

func (file *noFollowFile) Read(p []byte) (int, error) {
	return file.file.Read(p)
}

func (file *noFollowFile) Readdir(count int) ([]os.FileInfo, error) {
	infos, err := file.file.Readdir(count)
	if err != nil {
		return nil, err
	}
	for _, info := range infos {
		if err := rejectUnsafeFileInfo(info); err != nil {
			return nil, err
		}
	}
	return infos, nil
}

func (file *noFollowFile) Seek(offset int64, whence int) (int64, error) {
	return file.file.Seek(offset, whence)
}

func (file *noFollowFile) Stat() (os.FileInfo, error) {
	info, err := file.file.Stat()
	if err != nil {
		return nil, err
	}
	if err := rejectUnsafeFileInfo(info); err != nil {
		return nil, err
	}
	return info, nil
}

func (file *noFollowFile) Write(p []byte) (int, error) {
	return file.file.Write(p)
}

func davSegments(name string) ([]string, error) {
	if !utf8.ValidString(name) {
		return nil, fmt.Errorf("%w: invalid UTF-8 path", errNoFollowUnsafe)
	}
	if name == "" || name == "/" {
		return nil, nil
	}
	raw := strings.Trim(name, "/")
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.Contains(part, "\\") {
			return nil, fmt.Errorf("%w: invalid path segment", errNoFollowUnsafe)
		}
	}
	return parts, nil
}

func validateTreeAt(parentFD int, name string) error {
	st, err := fstatatNoFollow(parentFD, name)
	if err != nil {
		return err
	}
	if err := rejectUnsafeStat(st); err != nil {
		return err
	}
	if fileType(st) != unix.S_IFDIR {
		return nil
	}
	dirFD, err := unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(dirFD), name)
	if file == nil {
		_ = unix.Close(dirFD)
		return fmt.Errorf("%w: invalid directory descriptor", errNoFollowUnsafe)
	}
	defer file.Close()
	opened, err := fstatFD(int(file.Fd()))
	if err != nil {
		return err
	}
	if !sameFile(st, opened) || fileType(opened) != unix.S_IFDIR {
		return fmt.Errorf("%w: directory changed during validation", errNoFollowUnsafe)
	}
	names, err := file.Readdirnames(-1)
	if err != nil {
		return err
	}
	for _, child := range names {
		if child == "." || child == ".." {
			continue
		}
		if err := validateTreeAt(int(file.Fd()), child); err != nil {
			return err
		}
	}
	return nil
}

func deleteTreeAt(parentFD int, name string) error {
	st, err := fstatatNoFollow(parentFD, name)
	if err != nil {
		return err
	}
	if err := rejectUnsafeStat(st); err != nil {
		return err
	}
	switch fileType(st) {
	case unix.S_IFDIR:
		dirFD, err := unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return err
		}
		file := os.NewFile(uintptr(dirFD), name)
		if file == nil {
			_ = unix.Close(dirFD)
			return fmt.Errorf("%w: invalid directory descriptor", errNoFollowUnsafe)
		}
		opened, err := fstatFD(int(file.Fd()))
		if err != nil {
			_ = file.Close()
			return err
		}
		if !sameFile(st, opened) {
			_ = file.Close()
			return fmt.Errorf("%w: directory changed during delete", errNoFollowUnsafe)
		}
		names, err := file.Readdirnames(-1)
		if err != nil {
			_ = file.Close()
			return err
		}
		for _, child := range names {
			if child == "." || child == ".." {
				continue
			}
			if err := deleteTreeAt(int(file.Fd()), child); err != nil {
				_ = file.Close()
				return err
			}
		}
		if err := file.Close(); err != nil {
			return err
		}
		return unix.Unlinkat(parentFD, name, unix.AT_REMOVEDIR)
	case unix.S_IFREG:
		return unlinkRegularAt(parentFD, name, st)
	default:
		return fmt.Errorf("%w: unsupported file type", errNoFollowUnsafe)
	}
}

func unlinkRegularAt(parentFD int, name string, expected unix.Stat_t) error {
	fd, err := unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	opened, err := fstatFD(fd)
	if err != nil {
		return err
	}
	if !sameFile(expected, opened) {
		return fmt.Errorf("%w: file changed during delete", errNoFollowUnsafe)
	}
	if err := rejectUnsafeStat(opened); err != nil {
		return err
	}
	return unix.Unlinkat(parentFD, name, 0)
}

func validateRenameEndpoint(parentFD int, leaf string, mustExist bool) error {
	st, err := fstatatNoFollow(parentFD, leaf)
	if err != nil {
		if !mustExist && (errors.Is(err, os.ErrNotExist) || errors.Is(err, unix.ENOENT)) {
			return nil
		}
		return err
	}
	if err := rejectUnsafeStat(st); err != nil {
		return err
	}
	if fileType(st) == unix.S_IFDIR {
		return validateTreeAt(parentFD, leaf)
	}
	return nil
}

func validateOpenedFileFD(fd int) error {
	st, err := fstatFD(fd)
	if err != nil {
		return err
	}
	switch fileType(st) {
	case unix.S_IFREG:
		if st.Nlink > 1 {
			return fmt.Errorf("%w: hardlinked regular file", errNoFollowUnsafe)
		}
		return nil
	case unix.S_IFDIR:
		return nil
	default:
		return fmt.Errorf("%w: unsupported file type", errNoFollowUnsafe)
	}
}

func validateOpenedStatFD(fd int) error {
	st, err := fstatFD(fd)
	if err != nil {
		return err
	}
	if err := rejectUnsafeStat(st); err != nil {
		return err
	}
	switch fileType(st) {
	case unix.S_IFREG, unix.S_IFDIR:
		return nil
	default:
		return fmt.Errorf("%w: unsupported file type", errNoFollowUnsafe)
	}
}

func truncateOpenedRegularFD(fd int) error {
	st, err := fstatFD(fd)
	if err != nil {
		return err
	}
	if fileType(st) != unix.S_IFREG {
		return fmt.Errorf("%w: truncate target is not a regular file", errNoFollowUnsafe)
	}
	if st.Nlink > 1 {
		return fmt.Errorf("%w: hardlinked regular file", errNoFollowUnsafe)
	}
	return unix.Ftruncate(fd, 0)
}

func ensureDirFD(fd int) error {
	st, err := fstatFD(fd)
	if err != nil {
		return err
	}
	if fileType(st) != unix.S_IFDIR {
		return fmt.Errorf("%w: not a directory", errNoFollowUnsafe)
	}
	return nil
}

func rejectUnsafeFileInfo(info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: symlink", errNoFollowUnsafe)
	}
	if info.Mode().IsRegular() && linkCount(info) > 1 {
		return fmt.Errorf("%w: hardlinked regular file", errNoFollowUnsafe)
	}
	if !info.Mode().IsRegular() && !info.IsDir() {
		return fmt.Errorf("%w: unsupported file type", errNoFollowUnsafe)
	}
	return nil
}

func rejectUnsafeStat(st unix.Stat_t) error {
	switch fileType(st) {
	case unix.S_IFLNK:
		return fmt.Errorf("%w: symlink", errNoFollowUnsafe)
	case unix.S_IFREG:
		if st.Nlink > 1 {
			return fmt.Errorf("%w: hardlinked regular file", errNoFollowUnsafe)
		}
		return nil
	case unix.S_IFDIR:
		return nil
	default:
		return fmt.Errorf("%w: unsupported file type", errNoFollowUnsafe)
	}
}

func fstatFD(fd int) (unix.Stat_t, error) {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return unix.Stat_t{}, err
	}
	return st, nil
}

func fstatatNoFollow(parentFD int, name string) (unix.Stat_t, error) {
	var st unix.Stat_t
	if err := unix.Fstatat(parentFD, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return unix.Stat_t{}, err
	}
	return st, nil
}

func fileType(st unix.Stat_t) uint32 {
	return st.Mode & unix.S_IFMT
}

func sameFile(left, right unix.Stat_t) bool {
	return left.Dev == right.Dev && left.Ino == right.Ino
}

func writeRequested(flag int) bool {
	access := flag & unix.O_ACCMODE
	return access == os.O_WRONLY || access == os.O_RDWR || flag&os.O_TRUNC != 0 || flag&os.O_CREATE != 0
}

var _ webdav.FileSystem = (*noFollowFileSystem)(nil)
var _ webdav.File = (*noFollowFile)(nil)
var _ http.File = (*noFollowFile)(nil)
var _ io.Writer = (*noFollowFile)(nil)
