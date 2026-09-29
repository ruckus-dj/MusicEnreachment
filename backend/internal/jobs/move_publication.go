package jobs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type movePublicationFile struct {
	Target string `json:"target"`
	SHA256 string `json:"sha256"`
	Owned  bool   `json:"owned"`
}

type movePublication struct {
	OperationID uuid.UUID             `json:"operation_id"`
	NewRoot     string                `json:"new_root"`
	Files       []movePublicationFile `json:"files"`
}

func movePublicationPath(staging string) string {
	return filepath.Join(staging, "publication.json")
}

func loadMovePublication(staging string, operationID uuid.UUID, snapshot service.MoveSnapshot) (*movePublication, error) {
	path := movePublicationPath(staging)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect move publication: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, fmt.Errorf("move publication has an invalid file type or size")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read move publication: %w", err)
	}
	var publication movePublication
	if err := json.Unmarshal(raw, &publication); err != nil {
		return nil, fmt.Errorf("decode move publication: %w", err)
	}
	if publication.OperationID != operationID || publication.NewRoot != snapshot.NewRoot || len(publication.Files) != len(snapshot.Files) {
		return nil, fmt.Errorf("move publication identity changed")
	}
	for index, file := range snapshot.Files {
		record := publication.Files[index]
		if record.Target != filepath.Clean(file.TargetPath) || record.SHA256 != file.SHA256 {
			return nil, fmt.Errorf("move publication executable identity changed")
		}
	}
	return &publication, nil
}

func newMovePublication(staging string, operationID uuid.UUID, snapshot service.MoveSnapshot) (*movePublication, error) {
	publication := &movePublication{
		OperationID: operationID,
		NewRoot:     snapshot.NewRoot,
		Files:       make([]movePublicationFile, len(snapshot.Files)),
	}
	for index, file := range snapshot.Files {
		publication.Files[index] = movePublicationFile{
			Target: filepath.Clean(file.TargetPath),
			SHA256: file.SHA256,
		}
	}
	if err := saveMovePublication(staging, publication); err != nil {
		return nil, err
	}
	return publication, nil
}

func saveMovePublication(staging string, publication *movePublication) error {
	raw, err := json.Marshal(publication)
	if err != nil {
		return fmt.Errorf("encode move publication: %w", err)
	}
	file, err := os.CreateTemp(staging, ".publication-")
	if err != nil {
		return fmt.Errorf("create move publication: %w", err)
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if _, err := file.Write(raw); err != nil {
		_ = file.Close()
		return fmt.Errorf("write move publication: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync move publication: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close move publication: %w", err)
	}
	if err := os.Rename(file.Name(), movePublicationPath(staging)); err != nil {
		return fmt.Errorf("publish move journal: %w", err)
	}
	return nil
}

func moveFileWasPublished(file service.MoveFileIdentity, staging string, index int) (bool, error) {
	targetInfo, err := os.Lstat(file.TargetPath)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	payload := filepath.Join(staging, "payload", file.RelativePath, file.Executable)
	payloadInfo, err := os.Lstat(payload)
	if err != nil {
		return false, fmt.Errorf("inspect move publication witness %d: %w", index, err)
	}
	return targetInfo.Mode().IsRegular() && os.SameFile(targetInfo, payloadInfo), nil
}

func ownedMoveTargets(snapshot service.MoveSnapshot, staging string, publication *movePublication) ([]string, error) {
	owned := make([]string, 0, len(snapshot.Files))
	for index, file := range snapshot.Files {
		published, err := moveFileWasPublished(file, staging, index)
		if err != nil {
			return nil, err
		}
		if publication.Files[index].Owned || published {
			if published {
				owned = append(owned, filepath.Clean(file.TargetPath))
			}
		}
	}
	return owned, nil
}
