package api

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/jvsrunner"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/pathresolver"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/resources"
)

var errSavePointHistoryUnavailable = errors.New("save point history unavailable")

const (
	savePointPurposeTemplateSource       = "template_source"
	savePointPurposeLegacyTemplateSource = "task_template_source"
)

type VolumeReader interface {
	GetVolume(ctx context.Context, volumeID string) (resources.Volume, error)
}

type JVSHistoryRunner interface {
	DirectList(ctx context.Context, target jvsrunner.DirectTarget) (jvsrunner.DirectListSummary, error)
}

type JVSBackedSavePointHistoryReaderConfig struct {
	RepoReader   RepoReader
	VolumeReader VolumeReader
	JVSRunner    JVSHistoryRunner
	VolumeRoots  map[string]string
}

type JVSBackedSavePointHistoryReader struct {
	repoReader   RepoReader
	volumeReader VolumeReader
	jvs          JVSHistoryRunner
	volumeRoots  map[string]string
}

func NewJVSBackedSavePointHistoryReader(config JVSBackedSavePointHistoryReaderConfig) (*JVSBackedSavePointHistoryReader, error) {
	if config.RepoReader == nil || config.VolumeReader == nil || config.JVSRunner == nil {
		return nil, errors.New("invalid save point history reader config")
	}
	roots := make(map[string]string, len(config.VolumeRoots))
	for volumeID, root := range config.VolumeRoots {
		if err := pathresolver.ValidateID(pathresolver.VolumeID, volumeID); err != nil {
			return nil, errors.New("invalid save point history reader config")
		}
		if !validHistoryVolumeRoot(root) {
			return nil, errors.New("invalid save point history reader config")
		}
		roots[volumeID] = root
	}
	if len(roots) == 0 {
		return nil, errors.New("invalid save point history reader config")
	}
	return &JVSBackedSavePointHistoryReader{repoReader: config.RepoReader, volumeReader: config.VolumeReader, jvs: config.JVSRunner, volumeRoots: roots}, nil
}

func (reader *JVSBackedSavePointHistoryReader) ListSavePoints(ctx context.Context, namespaceID, repoID string) (SavePointHistory, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if reader == nil || reader.repoReader == nil || reader.volumeReader == nil || reader.jvs == nil {
		return SavePointHistory{}, errSavePointHistoryUnavailable
	}
	if err := pathresolver.ValidateID(pathresolver.NamespaceID, strings.TrimSpace(namespaceID)); err != nil {
		return SavePointHistory{}, errSavePointHistoryUnavailable
	}
	if err := pathresolver.ValidateID(pathresolver.RepoID, strings.TrimSpace(repoID)); err != nil {
		return SavePointHistory{}, errSavePointHistoryUnavailable
	}

	repo, err := reader.repoReader.GetRepoInNamespace(ctx, namespaceID, repoID)
	if err != nil || repo.NamespaceID != namespaceID || repo.ID != repoID || repo.Kind != resources.RepoKindRepo {
		return SavePointHistory{}, errSavePointHistoryUnavailable
	}
	if err := repo.Validate(); err != nil {
		return SavePointHistory{}, errSavePointHistoryUnavailable
	}
	volume, err := reader.volumeReader.GetVolume(ctx, repo.VolumeID)
	if err != nil || volume.ID != repo.VolumeID || volume.Status != resources.VolumeStatusActive || volume.Capabilities["jvs_external_control_root"] != true {
		return SavePointHistory{}, errSavePointHistoryUnavailable
	}
	if err := volume.Validate(); err != nil {
		return SavePointHistory{}, errSavePointHistoryUnavailable
	}
	volumeRoot, ok := reader.volumeRoots[repo.VolumeID]
	if !ok {
		return SavePointHistory{}, errSavePointHistoryUnavailable
	}
	roots, err := pathresolver.ResolveRepoRootPaths(volumeRoot, namespaceID, repoID)
	if err != nil || roots.ControlVolumeSubdir != repo.ControlVolumeSubdir || roots.PayloadVolumeSubdir != repo.PayloadVolumeSubdir {
		return SavePointHistory{}, errSavePointHistoryUnavailable
	}
	history, err := reader.jvs.DirectList(ctx, jvsrunner.DirectTarget{ControlRoot: roots.ControlRootPath, Home: roots.PayloadRootPath})
	if err != nil {
		return SavePointHistory{}, errSavePointHistoryUnavailable
	}
	return savePointHistoryFromJVSSummary(repoID, historySummaryFromDirectList(history))
}

func historySummaryFromDirectList(history jvsrunner.DirectListSummary) jvsrunner.HistorySummary {
	savePoints := make([]jvsrunner.SavePointSummary, 0, len(history.SavePoints))
	for _, savePoint := range history.SavePoints {
		savePoints = append(savePoints, jvsrunner.SavePointSummary{
			SavePointID: savePoint.SavePointID,
			Message:     savePoint.Message,
			Purpose:     savePoint.Purpose,
			CreatedAt:   savePoint.CreatedAt,
		})
	}
	return jvsrunner.HistorySummary{NewestSavePointID: history.HistoryHeadID, SavePoints: savePoints}
}

func savePointHistoryFromJVSSummary(repoID string, history jvsrunner.HistorySummary) (SavePointHistory, error) {
	savePoints := make([]SavePointResponse, 0, len(history.SavePoints))
	for _, savePoint := range history.SavePoints {
		if isInternalTemplateSourceSavePoint(savePoint.Purpose) {
			continue
		}
		if strings.TrimSpace(savePoint.SavePointID) == "" || strings.TrimSpace(savePoint.CreatedAt) == "" {
			return SavePointHistory{}, errSavePointHistoryUnavailable
		}
		savePoints = append(savePoints, SavePointResponse{
			SavePointID: savePoint.SavePointID,
			Message:     savePoint.Message,
			CreatedAt:   savePoint.CreatedAt,
			RepoID:      repoID,
		})
	}
	return SavePointHistory{SavePoints: savePoints}, nil
}

func isInternalTemplateSourceSavePoint(purpose string) bool {
	switch strings.TrimSpace(purpose) {
	case savePointPurposeTemplateSource, savePointPurposeLegacyTemplateSource:
		return true
	default:
		return false
	}
}

func validHistoryVolumeRoot(root string) bool {
	root = strings.TrimSpace(root)
	return root != "" && filepath.IsAbs(root) && filepath.Clean(root) == root && root != string(filepath.Separator)
}
