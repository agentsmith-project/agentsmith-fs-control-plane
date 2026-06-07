//go:build unix

package api

import (
	"os"

	"golang.org/x/sys/unix"
)

func repoPayloadRootOpenable(root string) error {
	info, err := os.Lstat(root)
	if err != nil {
		return errRepoPayloadNotExportVisible
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errRepoPayloadNotExportVisible
	}
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return errRepoPayloadNotExportVisible
	}
	if err := unix.Close(fd); err != nil {
		return errRepoPayloadNotExportVisible
	}
	return nil
}
