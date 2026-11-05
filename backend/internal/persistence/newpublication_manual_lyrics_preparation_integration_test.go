//go:build integration

package persistence

import (
	"context"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/uptrace/bun"
)

func preparingManualLyricsFLACFixture(t *testing.T) (publicationExecutionFixture, *SetupManagerRepository, PublicationInputs) {
	t.Helper()
	ctx := context.Background()
	f, repo, input, _ := prepareManualLyricsCommitFixture(t)
	input.Version = PublicationInputsVersion
	input.QualityPolicyVersion = publication.QualityPolicyVersion
	input.Format = publication.AudioFormatSource
	input.ManualLyrics = &PublicationManualLyricsInput{
		ExpectedMetadataRevision:   7,
		ConfirmedCurrentGeneration: input.Rewrite.CurrentGeneration,
		State:                      "explicit_empty",
	}
	input.Rewrite.AudioFacts.Container = publication.AudioContainerFLAC
	input.Rewrite.AudioFacts.Codec = "flac"
	input.Rewrite.AudioFacts.SupportedSourceContainer = true
	if err := input.Validate(); err != nil {
		t.Fatalf("FLAC manual-lyrics input is invalid: %v", err)
	}
	currentAudio := mustJSON(PublicationCurrentAudio{Version: 1, Facts: input.Rewrite.AudioFacts})
	if err := f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE track_publication_attempt SET phase='preparing', frozen_inputs=?::jsonb WHERE operation_id=?`, string(mustJSON(input)), f.guard.OperationID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE operation SET stage='preparing' WHERE id=?`, f.guard.OperationID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE track_publication SET actual_audio=?::jsonb WHERE position_id=?`, string(currentAudio), f.guard.ExpectedPositionID); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return f, repo, input
}

// This exercises the private V3 capability through the PostgreSQL delivery
// guard, rather than treating a recognized input version as authorization.
