//go:build integration

package persistence

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

func TestSourceRepublishGuardedReadersRequireCurrentAndAuthorityPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repository, _, _ := prepareManualLyricsCommitFixture(t)

	var current TrackPublication
	if err := f.db.NewSelect().Model(&current).Where("position_id=?", f.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	var ownership PublicationCurrentOwnership
	if err := decodePublicationCurrentContract(current.Ownership, &ownership); err != nil {
		t.Fatalf("decode fixture CURRENT ownership: %v", err)
	}
	audioFacts, ok := publicationAudioFactsFromObservation(current.ActualAudio)
	if !ok {
		t.Fatal("decode fixture CURRENT audio facts")
	}
	assets, err := DecodePublicationAssetOutcomes(current.ActualAssets)
	if err != nil {
		t.Fatalf("decode fixture CURRENT assets: %v", err)
	}
	var metadata PublicationCommitMetadata
	if err := strictMetadata(current.ActualMetadata, &metadata); err != nil {
		t.Fatalf("decode fixture CURRENT metadata: %v", err)
	}
	witness := PublicationCurrentWitness{
		Generation: current.Generation, RelativePath: current.RelativePath, Ownership: ownership.Identity,
		AudioFacts: audioFacts, Assets: assets, Tags: metadata.Tags,
		ArtworkInventory: []PublicationArtworkWitness{},
	}
	var authority PositionSourceAuthority
	if err := f.db.NewSelect().Model(&authority).Where("position_id=?", f.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatalf("read fixture source authority: %v", err)
	}
	var releaseID uuid.UUID
	var metadataRevision int64
	if err := f.db.NewRaw(`SELECT release_id,metadata_revision FROM release_track WHERE id=?`, f.guard.ExpectedPositionID).Scan(ctx, &releaseID, &metadataRevision); err != nil {
		t.Fatalf("read fixture lyrics and cover authority owners: %v", err)
	}
	if _, err := f.db.ExecContext(ctx, `INSERT INTO release_publication_cover(release_id,revision,mode) VALUES (?,1,'manual')`, releaseID); err != nil {
		t.Fatalf("seed explicit absent cover authority: %v", err)
	}
	var coverRevision int64
	var coverMode string
	if err := f.db.NewRaw(`SELECT revision,mode FROM release_publication_cover WHERE release_id=?`, releaseID).Scan(ctx, &coverRevision, &coverMode); err != nil {
		t.Fatalf("read seeded cover authority: %v", err)
	}

	input := publicationInputsFixture(t, "audio")
	input.Version = PublicationSourceRepublishInputsVersion
	input.SourceAuthorityMode = ""
	input.InitialLyrics = nil
	input.AudioUpgrade = nil
	input.ProviderTags = nil
	input.DesiredTags = metadata.Tags
	input.SourceRepublish = &PublicationSourceRepublishInput{
		Current: witness, Mode: "ordinary",
		SourceAuthority: PublicationSourceAuthority{
			Present: true, PositionID: authority.PositionID, Mode: authority.Mode, Revision: authority.Revision,
			PublicationEpoch: authority.PublicationEpoch, PublicationGeneration: authority.PublicationGeneration,
			SourceLocationID: authority.SourceLocationID, MediaVariantID: authority.MediaVariantID,
		},
		Association:     PublicationRepublishAssociation{ID: uuid.New(), LocationID: input.Audio.LocationID, MediaVariantID: input.Audio.MediaVariantID},
		Lyrics:          PublicationRepublishLyrics{State: "absent", Origin: "none"},
		LyricsAuthority: PublicationInitialLyricsInput{ExpectedMetadataRevision: metadataRevision},
		Cover:           PublicationRepublishCover{ReleaseID: releaseID, AuthorityRevision: coverRevision, AuthorityMode: coverMode, Derivation: "absent"},
	}
	if err := input.Validate(); err != nil {
		t.Fatalf("valid v4 frozen inputs: %v", err)
	}
	if err := f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE operation SET input_snapshot=jsonb_set(input_snapshot,'{intent_kind}',?::jsonb,true) WHERE id=?`, string(mustJSON("audio")), f.guard.OperationID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE track_publication_attempt SET expected_generation=?,intent_kind='audio',frozen_inputs=?::jsonb WHERE operation_id=?`, current.Generation, string(mustJSON(input)), f.guard.OperationID)
		return err
	}); err != nil {
		t.Fatalf("freeze v4 operation and attempt inputs: %v", err)
	}
	prepared, valid, err := repository.PublicationPreparationInputs(ctx, f.guard)
	if err != nil || !valid || prepared.Version != PublicationSourceRepublishInputsVersion {
		t.Fatalf("valid guarded preparation = valid:%v version:%d err:%v", valid, prepared.Version, err)
	}
	currentResult, err := repository.CheckPublicationResultCurrent(ctx, f.guard)
	if err != nil || !currentResult {
		t.Fatalf("valid guarded result freshness = %v, %v", currentResult, err)
	}

	if _, err := f.db.ExecContext(ctx, `UPDATE position_source_authority SET revision=? WHERE position_id=?`, uuid.New(), f.guard.ExpectedPositionID); err != nil {
		t.Fatalf("change baseline source authority revision: %v", err)
	}
	_, valid, err = repository.PublicationPreparationInputs(ctx, f.guard)
	if err != nil || valid {
		t.Fatalf("preparation accepted changed source authority: valid=%v err=%v", valid, err)
	}
	currentResult, err = repository.CheckPublicationResultCurrent(ctx, f.guard)
	if err != nil || currentResult {
		t.Fatalf("result freshness accepted changed source authority: current=%v err=%v", currentResult, err)
	}
}
