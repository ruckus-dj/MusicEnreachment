package service

import (
	"context"
	"encoding/json"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// PublicationStageValidator validates a completed publication candidate and
// returns facts observed from that file, including stream topology and metadata.
type PublicationStageValidator interface {
	Validate(context.Context, string, persistence.PublicationInputs) (PublicationStageObservation, error)
}

type PublicationMetadataWriter interface {
	Write(context.Context, string, map[string][]string, *metadata.Cover) (metadata.WriteResult, error)
}

type publicationFlacArtworkMetadataWriter interface {
	WriteFlacTagsPreservingArtwork(context.Context, string, map[string][]string, metadata.FlacArtworkSnapshot) (metadata.WriteResult, error)
}

// PublicationPinnedTools resolves only executables admitted in frozen inputs.
type PublicationPinnedTools interface {
	Executable(context.Context, []persistence.SourceAnalysisToolSelection, string) (string, error)
}

type PublicationStageObservation struct {
	Ownership            persistence.ArtifactOwnership
	Audio                json.RawMessage
	Metadata             json.RawMessage
	SHA256               string
	Assets               json.RawMessage
	QualityPolicyVersion int
}
