package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// observedRequest takes a read-only presence snapshot before SQL filtering and
// pagination. It does not change source inventory or CURRENT publication state.
func (catalog *LibraryCatalog) observedRequest(ctx context.Context, filter LibraryCatalogFilter) (persistence.LibraryCatalogRequest, error) {
	request := catalogRequest(filter)
	candidates, err := catalog.repository.ReadLibraryAudioCandidates(ctx, request)
	if err != nil {
		return request, fmt.Errorf("read library audio candidates: %w", err)
	}
	ids, err := observeCatalogAudio(ctx, sourcefs.NewOpener(), candidates)
	if err != nil {
		return request, err
	}
	request.AvailablePositionIDs = ids
	return request, nil
}

func observeCatalogAudio(
	ctx context.Context,
	opener sourcefs.Opener,
	candidates []persistence.LibraryCatalogAudioCandidate,
) ([]uuid.UUID, error) {
	roots := make(map[string]sourcefs.Directory)
	defer func() {
		for _, root := range roots {
			if root != nil {
				_ = root.Close()
			}
		}
	}()
	type observationKey struct {
		root, relative, kind string
		size, mtime          int64
	}
	observed := make(map[observationKey]bool)
	available := make(map[uuid.UUID]bool)
	ids := make([]uuid.UUID, 0)
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if available[candidate.PositionID] {
			continue
		}
		key := observationKey{
			root: candidate.RootPath, relative: candidate.RelativePath, kind: candidate.Kind,
			size: candidate.SizeBytes, mtime: candidate.MTimeNS,
		}
		present, known := observed[key]
		if !known {
			root, opened := roots[candidate.RootPath]
			if !opened {
				var err error
				root, err = opener.OpenRoot(ctx, candidate.RootPath)
				if err != nil {
					root = nil
				}
				roots[candidate.RootPath] = root
			}
			if root != nil {
				present = catalogAudioPresent(ctx, root, candidate)
			}
			observed[key] = present
		}
		if present {
			available[candidate.PositionID] = true
			ids = append(ids, candidate.PositionID)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

func catalogAudioPresent(
	ctx context.Context,
	root sourcefs.Directory,
	candidate persistence.LibraryCatalogAudioCandidate,
) bool {
	if candidate.Kind != "source" && candidate.Kind != "publication" {
		return false
	}
	file, err := sourcefs.OpenRegularAt(ctx, root, candidate.RelativePath)
	if err != nil {
		return false
	}
	info, statErr := file.Stat(ctx)
	closeErr := file.Close()
	if statErr != nil || closeErr != nil || !info.Mode().IsRegular() {
		return false
	}
	if candidate.Kind == "publication" {
		return true
	}
	return info.Size() == candidate.SizeBytes && sourceScanMtime(info.ModTime()).UnixNano() == candidate.MTimeNS
}
