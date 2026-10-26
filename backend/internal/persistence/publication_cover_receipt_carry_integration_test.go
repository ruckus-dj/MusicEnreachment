//go:build integration

package persistence

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/uptrace/bun"
)

func TestCoverRewriteCarriesOnlyCurrentManualLyricsReceiptPostgreSQL(t *testing.T) {
	for _, newerDesired := range []bool{false, true} {
		t.Run(fmt.Sprintf("newer_desired_%t", newerDesired), func(t *testing.T) {
			f, repository, input, observation := prepareManualLyricsCommitFixture(t)
			ctx := context.Background()
			seedManualPublicationResultChoice(t, ctx, f)
			var previous TrackPublication
			if err := f.db.NewSelect().Model(&previous).Where("position_id=?", f.guard.ExpectedPositionID).Scan(ctx); err != nil {
				t.Fatal(err)
			}
			previous.ActualMetadata, previous.ActualAssets = observation.ActualMetadata, observation.ActualAssets
			if _, err := f.db.ExecContext(ctx, `UPDATE track_publication SET actual_metadata=?::jsonb,actual_assets=?::jsonb WHERE position_id=?`, string(previous.ActualMetadata), string(previous.ActualAssets), previous.PositionID); err != nil {
				t.Fatal(err)
			}
			digest := fmt.Sprintf("%x", sha256.Sum256([]byte(input.ManualLyrics.Value)))
			recorded := time.Now().UTC().Truncate(time.Microsecond)
			if _, err := f.db.ExecContext(ctx, `INSERT INTO publication_manual_lyrics_receipt(position_id,epoch,desired_revision,confirmed_generation,published_generation,desired_state,desired_sha256,recorded_at) VALUES (?,?,?,?,?,'value',?,?)`, previous.PositionID, previous.Epoch, input.ManualLyrics.ExpectedMetadataRevision, previous.Generation, previous.Generation, digest, recorded); err != nil {
				t.Fatal(err)
			}
			if newerDesired {
				if _, err := f.db.ExecContext(ctx, `UPDATE release_track SET metadata_revision=metadata_revision+1 WHERE id=?`, previous.PositionID); err != nil {
					t.Fatal(err)
				}
			}
			attempt, err := repository.ReadPublicationAttempt(ctx, f.guard.OperationID)
			if err != nil {
				t.Fatal(err)
			}
			candidate := previous
			candidate.Generation = f.guard.CandidateGeneration
			assets, err := DecodePublicationAssetOutcomes(candidate.ActualAssets)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				return carryManualLyricsReceiptForCoverRewriteTx(ctx, tx, attempt, previous, candidate, assets)
			}); err != nil {
				t.Fatal(err)
			}
			var receipt PublicationManualLyricsReceipt
			if err := f.db.NewSelect().Model(&receipt).Where("position_id=?", previous.PositionID).Scan(ctx); err != nil {
				t.Fatal(err)
			}
			want := candidate.Generation
			if newerDesired {
				want = previous.Generation
			}
			if receipt.PublishedGeneration != want || receipt.ConfirmedGeneration != previous.Generation || receipt.DesiredRevision != input.ManualLyrics.ExpectedMetadataRevision || !receipt.RecordedAt.Equal(recorded) {
				t.Fatalf("cover rewrite changed receipt authority: %+v", receipt)
			}
		})
	}
}
