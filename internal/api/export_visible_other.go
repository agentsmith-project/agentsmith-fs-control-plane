//go:build !unix

package api

import "os"

func repoPayloadRootOpenable(root string) error {
	info, err := os.Lstat(root)
	if err != nil {
		return errRepoPayloadNotExportVisible
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errRepoPayloadNotExportVisible
	}
	return nil
}
