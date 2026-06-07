package api

import (
	"errors"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/pathresolver"
)

var errRepoPayloadNotExportVisible = errors.New("repo payload root is not export-visible")

func repoPayloadExportVisible(volumeRoots map[string]string, volumeID, namespaceID, repoID string) error {
	if len(volumeRoots) == 0 {
		return errRepoPayloadNotExportVisible
	}
	volumeRoot, ok := volumeRoots[volumeID]
	if !ok {
		return errRepoPayloadNotExportVisible
	}
	paths, err := pathresolver.ResolveRepoRootPaths(volumeRoot, namespaceID, repoID)
	if err != nil {
		return errRepoPayloadNotExportVisible
	}
	return repoPayloadRootOpenable(paths.PayloadRootPath)
}

func cloneVolumeRoots(roots map[string]string) map[string]string {
	if roots == nil {
		return nil
	}
	cloned := make(map[string]string, len(roots))
	for volumeID, root := range roots {
		cloned[volumeID] = root
	}
	return cloned
}
