package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// SourceAnalysisRepository is the source-root read contract used by the
// normalized analysis worker.
type SourceAnalysisRepository interface {
	GetSourceRoot(context.Context, uuid.UUID) (*persistence.SourceRoot, error)
}
