//go:build linux

package exportgateway

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/net/webdav"
)

func TestNoFollowFileSystemConstructorRejectsUnsafeRoot(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	fs, err := newNoFollowFileSystem(root)
	if err != nil {
		t.Fatalf("newNoFollowFileSystem(valid root): %v", err)
	}
	defer fs.Close()
	var _ webdav.FileSystem = fs
	if _, err := fs.Stat(ctx, "/"); err != nil {
		t.Fatalf("Stat root: %v", err)
	}

	linkRoot := filepath.Join(t.TempDir(), "payload")
	if err := os.Symlink(root, linkRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := newNoFollowFileSystem(linkRoot); err == nil {
		t.Fatal("newNoFollowFileSystem(symlink root) succeeded, want error")
	}

	fileRoot := filepath.Join(t.TempDir(), "payload")
	if err := os.WriteFile(fileRoot, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := newNoFollowFileSystem(fileRoot); err == nil {
		t.Fatal("newNoFollowFileSystem(non-dir root) succeeded, want error")
	}
}

func TestNoFollowFileSystemRejectsIntermediateAndFinalSymlink(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "links"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "links", "out")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "final-link.txt")); err != nil {
		t.Fatal(err)
	}

	fs := mustNoFollowFS(t, root)

	for _, name := range []string{"/links/out/secret.txt", "/final-link.txt"} {
		t.Run("Stat "+name, func(t *testing.T) {
			if _, err := fs.Stat(ctx, name); err == nil {
				t.Fatalf("Stat(%q) succeeded, want error", name)
			}
		})
		t.Run("OpenFile "+name, func(t *testing.T) {
			file, err := fs.OpenFile(ctx, name, os.O_RDONLY, 0)
			if err == nil {
				file.Close()
				t.Fatalf("OpenFile(%q) succeeded, want error", name)
			}
		})
	}
}

func TestNoFollowFileSystemWriteRejectsExistingHardlinkBeforeTruncate(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("do not truncate"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(outside, filepath.Join(root, "hardlinked.txt")); err != nil {
		t.Fatal(err)
	}

	fs := mustNoFollowFS(t, root)
	file, err := fs.OpenFile(ctx, "/hardlinked.txt", os.O_WRONLY|os.O_TRUNC, 0o644)
	if err == nil {
		file.Close()
		t.Fatal("OpenFile hardlink for truncate succeeded, want error")
	}
	got, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "do not truncate" {
		t.Fatalf("outside hardlink content = %q, want unchanged", got)
	}
}

func TestNoFollowFileSystemRemoveAllRefusesSymlinkAndPreservesOutsideTarget(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	outside := t.TempDir()
	outsideTarget := filepath.Join(outside, "target.txt")
	if err := os.WriteFile(outsideTarget, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dir", "owned.txt"), []byte("owned"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideTarget, filepath.Join(root, "dir", "link.txt")); err != nil {
		t.Fatal(err)
	}

	fs := mustNoFollowFS(t, root)
	if err := fs.RemoveAll(ctx, "/dir"); err == nil {
		t.Fatal("RemoveAll directory containing symlink succeeded, want error")
	}
	if got, err := os.ReadFile(outsideTarget); err != nil || string(got) != "keep" {
		t.Fatalf("outside target after RemoveAll = %q, %v; want keep", got, err)
	}
	if _, err := os.Lstat(filepath.Join(root, "dir")); err != nil {
		t.Fatalf("payload directory was removed after refused RemoveAll: %v", err)
	}
}

func TestNoFollowFileSystemReaddirRejectsUnsafeEntries(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}

	fs := mustNoFollowFS(t, root)
	dir, err := fs.OpenFile(ctx, "/", os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	if _, err := dir.Readdir(-1); err == nil {
		t.Fatal("Readdir with symlink child succeeded, want error")
	}
}

func TestNoFollowFileSystemRenameRejectsSymlinksAndAllowsSameRootRename(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "real", "file.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "linkdir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real", "file.txt"), filepath.Join(root, "linkfile.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "real", "dest.txt"), []byte("dest"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real", "dest.txt"), filepath.Join(root, "destlink.txt")); err != nil {
		t.Fatal(err)
	}

	fs := mustNoFollowFS(t, root)

	for _, tt := range []struct {
		name    string
		oldName string
		newName string
	}{
		{name: "source intermediate symlink", oldName: "/linkdir/file.txt", newName: "/moved.txt"},
		{name: "source final symlink", oldName: "/linkfile.txt", newName: "/moved.txt"},
		{name: "destination intermediate symlink", oldName: "/real/file.txt", newName: "/linkdir/new.txt"},
		{name: "destination final symlink", oldName: "/real/file.txt", newName: "/destlink.txt"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := fs.Rename(ctx, tt.oldName, tt.newName); err == nil {
				t.Fatalf("Rename(%q, %q) succeeded, want error", tt.oldName, tt.newName)
			}
		})
	}

	if err := fs.Rename(ctx, "/real/file.txt", "/real/moved.txt"); err != nil {
		t.Fatalf("legal same-root Rename: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "real", "moved.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("renamed file content = %q, want hello", got)
	}
}

func TestNoFollowFileSystemRenameRefusesUnsafeSourceDirectoryTree(t *testing.T) {
	ctx := context.Background()

	for _, tt := range []struct {
		name  string
		setup func(t *testing.T, root, sourceDir, outsideTarget string)
	}{
		{
			name: "source directory contains symlink",
			setup: func(t *testing.T, root, sourceDir, outsideTarget string) {
				t.Helper()
				if err := os.Symlink(outsideTarget, filepath.Join(sourceDir, "link.txt")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "source directory contains hardlink",
			setup: func(t *testing.T, root, sourceDir, outsideTarget string) {
				t.Helper()
				if err := os.Link(outsideTarget, filepath.Join(sourceDir, "hardlinked.txt")); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			outsideTarget := filepath.Join(t.TempDir(), "outside.txt")
			if err := os.WriteFile(outsideTarget, []byte("keep"), 0o644); err != nil {
				t.Fatal(err)
			}
			sourceDir := filepath.Join(root, "source")
			if err := os.MkdirAll(filepath.Join(sourceDir, "nested"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(sourceDir, "nested", "owned.txt"), []byte("owned"), 0o644); err != nil {
				t.Fatal(err)
			}
			tt.setup(t, root, sourceDir, outsideTarget)

			fs := mustNoFollowFS(t, root)
			if err := fs.Rename(ctx, "/source", "/moved"); err == nil {
				t.Fatal("Rename source directory containing unsafe entry succeeded, want error")
			}
			if _, err := os.Lstat(sourceDir); err != nil {
				t.Fatalf("source directory after refused Rename: %v", err)
			}
			if _, err := os.Lstat(filepath.Join(root, "moved")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("destination after refused Rename error = %v, want not exist", err)
			}
			if got, err := os.ReadFile(outsideTarget); err != nil || string(got) != "keep" {
				t.Fatalf("outside target after refused Rename = %q, %v; want keep", got, err)
			}
		})
	}
}

func TestNoFollowFileUsesAlreadyOpenedDescriptor(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "file.txt")
	if err := os.WriteFile(path, []byte("safe"), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := mustNoFollowFS(t, root)

	file, err := fs.OpenFile(ctx, "/file.txt", os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "outside.txt"), path); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "safe" {
		t.Fatalf("open file read = %q, want original fd content", data)
	}
	if _, err := file.Stat(); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Stat on already opened fd returned unexpected error: %v", err)
	}
}

func mustNoFollowFS(t *testing.T, root string) *noFollowFileSystem {
	t.Helper()
	fs, err := newNoFollowFileSystem(root)
	if err != nil {
		t.Fatalf("newNoFollowFileSystem: %v", err)
	}
	t.Cleanup(func() {
		if err := fs.Close(); err != nil {
			t.Errorf("Close noFollowFileSystem: %v", err)
		}
	})
	return fs
}
