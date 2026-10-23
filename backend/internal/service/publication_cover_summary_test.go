package service

import (
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestCoverSummaryDoesNotRequireInitialLyrics(t *testing.T) {
	generation := uuid.New()
	projection := publicationTestContext(uuid.New(), uuid.New(), uuid.New(), generation, "value", "saved")
	projection.Current.InitialLyrics = nil
	projection.Current.CoverEmbedding = &persistence.PublicationCoverEmbeddingProjection{
		Generation: generation, Outcome: "mismatch", WarningCode: "cover_mismatch",
	}
	view, _, _ := libraryPublicationView(projection)
	if view.CurrentCoverEmbedding == nil || view.CurrentCoverEmbedding.WarningCode != "cover_mismatch" {
		t.Fatalf("missing independent current cover summary: %+v", view.CurrentCoverEmbedding)
	}
	projection.Current.CoverEmbedding.Generation = uuid.New()
	view, _, _ = libraryPublicationView(projection)
	if view.CurrentCoverEmbedding != nil {
		t.Fatalf("stale cover summary exposed: %+v", view.CurrentCoverEmbedding)
	}
}
