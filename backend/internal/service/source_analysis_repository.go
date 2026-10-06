package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// SourceAnalysisRepository is the read-only persistence contract of one source
// analysis. It only reads: the analysis never writes the database, so a caller
// applies the prepared result later, in the transactional apply of step 2.
type SourceAnalysisRepository interface {
	GetSourceRoot(context.Context, uuid.UUID) (*persistence.SourceRoot, error)
	GetSourceLocation(context.Context, uuid.UUID, uuid.UUID) (*persistence.SourceLocation, error)
	GetInstallation(context.Context, uuid.UUID) (*persistence.ToolInstallation, error)
}
