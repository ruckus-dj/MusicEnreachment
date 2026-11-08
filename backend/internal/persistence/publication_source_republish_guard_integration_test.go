//go:build integration

package persistence

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
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

func TestCommitSourceRepublishResultSuccessAndLateRollbackPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.OpenMigratedWithRiver(t)
	initializeRiver(t, ctx, db)

	// Prove the valid result baseline before exercising SQL rollback.
	f, repository, input, observation, baselineAuthority := prepareSourceRepublishResultFixture(t, db, "source-republish-success-"+uuid.NewString())
	result := sourceRepublishResult(input, observation)
	committed, err := repository.CommitPublicationResult(ctx, f.guard, result)
	if err != nil || !committed {
		t.Fatalf("commit valid source-republish result = %v, %v", committed, err)
	}
	var current TrackPublication
	if err := db.NewSelect().Model(&current).Where("position_id=?", f.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if current.Generation != f.guard.CandidateGeneration || !sameJSONValue(current.ActualMetadata, observation.ActualMetadata) || !sameJSONValue(current.ActualAssets, observation.ActualAssets) {
		t.Fatalf("CURRENT does not match source-republish result: %+v", current)
	}
	if len(current.InitialLyrics) != 0 || len(current.CoverEmbedding) != 0 {
		t.Fatalf("source-republish inherited stale optional summaries: lyrics=%s cover=%s", current.InitialLyrics, current.CoverEmbedding)
	}
	var authority PositionSourceAuthority
	if err := db.NewSelect().Model(&authority).Where("position_id=?", f.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if authority.Revision == baselineAuthority.Revision || authority.Mode != baselineAuthority.Mode || authority.PublicationEpoch != f.guard.Epoch ||
		authority.PublicationGeneration != f.guard.CandidateGeneration || authority.SourceLocationID != input.Audio.LocationID || authority.MediaVariantID != input.Audio.MediaVariantID {
		t.Fatalf("source authority was not rebound to selected source B: %+v", authority)
	}
	var receiptEpoch, confirmedGeneration, publishedGeneration uuid.UUID
	var receiptRevision int64
	var receiptState string
	var receiptClean bool
	if err := db.NewRaw(`SELECT epoch,desired_revision,confirmed_generation,published_generation,desired_state,warning_code IS NULL AND recorded_at IS NOT NULL
		FROM publication_manual_lyrics_receipt WHERE position_id=?`, f.guard.ExpectedPositionID).Scan(ctx,
		&receiptEpoch, &receiptRevision, &confirmedGeneration, &publishedGeneration, &receiptState, &receiptClean); err != nil {
		t.Fatalf("read fresh manual lyrics receipt: %v", err)
	}
	if receiptEpoch != f.guard.Epoch || receiptRevision != input.SourceRepublish.LyricsAuthority.ExpectedMetadataRevision ||
		confirmedGeneration != input.SourceRepublish.Current.Generation || publishedGeneration != f.guard.CandidateGeneration || receiptState != string(librarymetadata.StateExplicitEmpty) || !receiptClean {
		t.Fatalf("manual lyrics receipt is not fresh: epoch=%s revision=%d confirmed=%s published=%s state=%s clean=%v",
			receiptEpoch, receiptRevision, confirmedGeneration, publishedGeneration, receiptState, receiptClean)
	}
	var consumed int
	for _, query := range []string{
		`SELECT count(*) FROM track_publication_attempt WHERE operation_id=?`,
		`SELECT count(*) FROM publication_execution_claim WHERE operation_id=?`,
	} {
		if err := db.NewRaw(query, f.guard.OperationID).Scan(ctx, &consumed); err != nil || consumed != 0 {
			t.Fatalf("successful result retained delivery authority: count=%d err=%v", consumed, err)
		}
	}

	rollback, rollbackRepository, rollbackInput, rollbackObservation, rollbackAuthority := prepareSourceRepublishResultFixture(t, db, "source-republish-rollback-"+uuid.NewString())
	var before TrackPublication
	if err := db.NewSelect().Model(&before).Where("position_id=?", rollback.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION fail_source_republish_result_attempt_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected source-republish settlement failure'; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER fail_source_republish_result_attempt_delete AFTER DELETE ON track_publication_attempt FOR EACH ROW EXECUTE FUNCTION fail_source_republish_result_attempt_delete()`); err != nil {
		t.Fatal(err)
	}
	_, err = rollbackRepository.CommitPublicationResult(ctx, rollback.guard, sourceRepublishResult(rollbackInput, rollbackObservation))
	if err == nil || !strings.Contains(err.Error(), "injected source-republish settlement failure") {
		t.Fatalf("late source-republish settlement error = %v, want injected trigger diagnostic", err)
	}
	var after TrackPublication
	if err := db.NewSelect().Model(&after).Where("position_id=?", rollback.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if after.Generation != before.Generation || !sameJSONValue(after.Ownership, before.Ownership) || !sameJSONValue(after.ActualAudio, before.ActualAudio) ||
		!sameJSONValue(after.ActualMetadata, before.ActualMetadata) || !sameJSONValue(after.ActualAssets, before.ActualAssets) {
		t.Fatalf("late failure changed CURRENT despite transaction rollback: before=%+v after=%+v", before, after)
	}
	var afterAuthority PositionSourceAuthority
	if err := db.NewSelect().Model(&afterAuthority).Where("position_id=?", rollback.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if afterAuthority != rollbackAuthority {
		t.Fatalf("late failure changed source authority: before=%+v after=%+v", rollbackAuthority, afterAuthority)
	}
	for _, check := range []struct {
		query string
		id    uuid.UUID
		want  int
	}{
		{query: `SELECT count(*) FROM publication_manual_lyrics_receipt WHERE position_id=?`, id: rollback.guard.ExpectedPositionID, want: 0},
		{query: `SELECT count(*) FROM track_publication_attempt WHERE operation_id=?`, id: rollback.guard.OperationID, want: 1},
		{query: `SELECT count(*) FROM publication_execution_claim WHERE operation_id=?`, id: rollback.guard.OperationID, want: 1},
	} {
		var count int
		if err := db.NewRaw(check.query, check.id).Scan(ctx, &count); err != nil || count != check.want {
			t.Fatalf("late failure count for %q = %d, want %d; err=%v", check.query, count, check.want, err)
		}
	}
}

func prepareSourceRepublishResultFixture(t *testing.T, db *bun.DB, scope string) (publicationExecutionFixture, *SetupManagerRepository, PublicationInputs, PublicationStageObservation, PositionSourceAuthority) {
	t.Helper()
	ctx := context.Background()
	f, repository, _, observation := prepareManualLyricsCommitFixtureWithDatabase(t, db, scope)
	var current TrackPublication
	if err := db.NewSelect().Model(&current).Where("position_id=?", f.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	var ownership PublicationCurrentOwnership
	if err := decodePublicationCurrentContract(current.Ownership, &ownership); err != nil {
		t.Fatal(err)
	}
	facts, ok := publicationAudioFactsFromObservation(current.ActualAudio)
	if !ok {
		t.Fatal("decode baseline CURRENT audio facts")
	}
	assets, err := DecodePublicationAssetOutcomes(current.ActualAssets)
	if err != nil {
		t.Fatal(err)
	}
	var metadata PublicationCommitMetadata
	if err := strictMetadata(current.ActualMetadata, &metadata); err != nil {
		t.Fatal(err)
	}
	var authority PositionSourceAuthority
	if err := db.NewSelect().Model(&authority).Where("position_id=?", f.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	var releaseID uuid.UUID
	var metadataRevision int64
	if err := db.NewRaw(`SELECT release_id,metadata_revision FROM release_track WHERE id=?`, f.guard.ExpectedPositionID).Scan(ctx, &releaseID, &metadataRevision); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO release_publication_cover(release_id,revision,mode) VALUES (?,1,'manual')`, releaseID); err != nil {
		t.Fatal(err)
	}
	var coverRevision int64
	var coverMode string
	if err := db.NewRaw(`SELECT revision,mode FROM release_publication_cover WHERE release_id=?`, releaseID).Scan(ctx, &coverRevision, &coverMode); err != nil {
		t.Fatal(err)
	}
	input := publicationInputsFixture(t, "audio")
	input.Version = PublicationSourceRepublishInputsVersion
	input.SourceAuthorityMode = ""
	input.InitialLyrics = nil
	input.AudioUpgrade = nil
	input.ProviderTags = nil
	input.DesiredTags = metadata.Tags
	for input.Audio.LocationID == authority.SourceLocationID {
		input.Audio.LocationID = uuid.New()
	}
	for input.Audio.MediaVariantID == authority.MediaVariantID {
		input.Audio.MediaVariantID = uuid.New()
	}
	manualEmpty := librarymetadata.FieldValue{
		State: librarymetadata.StateExplicitEmpty, Authority: librarymetadata.AuthorityManual,
		Provenance: librarymetadata.Provenance{Kind: librarymetadata.ProvenanceManual},
	}
	input.SourceRepublish = &PublicationSourceRepublishInput{
		Current: PublicationCurrentWitness{
			Generation: current.Generation, RelativePath: current.RelativePath, Ownership: ownership.Identity,
			AudioFacts: facts, Assets: assets, Tags: metadata.Tags, ArtworkInventory: []PublicationArtworkWitness{},
		},
		Mode: "ordinary",
		SourceAuthority: PublicationSourceAuthority{
			Present: true, PositionID: authority.PositionID, Mode: authority.Mode, Revision: authority.Revision,
			PublicationEpoch: authority.PublicationEpoch, PublicationGeneration: authority.PublicationGeneration,
			SourceLocationID: authority.SourceLocationID, MediaVariantID: authority.MediaVariantID,
		},
		Association:     PublicationRepublishAssociation{ID: uuid.New(), LocationID: input.Audio.LocationID, MediaVariantID: input.Audio.MediaVariantID},
		Lyrics:          PublicationRepublishLyrics{State: "absent", Origin: "none"},
		LyricsAuthority: PublicationInitialLyricsInput{ExpectedMetadataRevision: metadataRevision, Desired: &manualEmpty},
		Cover:           PublicationRepublishCover{ReleaseID: releaseID, AuthorityRevision: coverRevision, AuthorityMode: coverMode, Derivation: "absent"},
	}
	if err := input.Validate(); err != nil {
		t.Fatalf("valid v4 result inputs: %v", err)
	}
	metadataEnvelope := map[string]any{"local_metadata": map[string]any{
		"version": 1, "scope": "position", "desired": map[string]any{"synced_lyrics": manualEmpty},
	}}
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE release_track SET metadata=?::jsonb WHERE id=?`, string(mustJSON(metadataEnvelope)), f.guard.ExpectedPositionID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE operation SET input_snapshot=jsonb_set(input_snapshot,'{intent_kind}',?::jsonb,true) WHERE id=?`, string(mustJSON("audio")), f.guard.OperationID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE track_publication_attempt SET expected_generation=?,intent_kind='audio',frozen_inputs=?::jsonb WHERE operation_id=?`, current.Generation, string(mustJSON(input)), f.guard.OperationID)
		return err
	}); err != nil {
		t.Fatalf("freeze source-republish input and matching authority: %v", err)
	}
	observation.ActualMetadata = mustJSON(PublicationCommitMetadata{Version: 1, Tags: input.DesiredTags})
	observation.ActualAssets = mustJSON(PublicationAssetOutcomes{
		Version: PublicationAssetOutcomesVersion, Cover: PublicationCoverOutcome{State: PublicationAssetAbsent},
		Lyrics: PublicationLyricsOutcome{State: PublicationAssetAbsent},
	})
	plan, err := publication.PlanAudioTransform(input.Format, input.Audio.AudioFacts.AudioSourceFacts())
	if err != nil {
		t.Fatalf("plan frozen source audio: %v", err)
	}
	resultFacts := input.Audio.AudioFacts
	resultFacts.Container = plan.OutputContainer
	if resultFacts.Lossless != nil {
		lossless := *resultFacts.Lossless
		resultFacts.Lossless = &lossless
	}
	switch plan.Action {
	case publication.AudioCodecALAC:
		resultFacts.Codec = "alac"
	case publication.AudioCodecAAC:
		resultFacts.Codec = "aac"
		if resultFacts.Lossless != nil {
			lossless := false
			resultFacts.Lossless = &lossless
		}
	}
	observation.ActualAudio = mustJSON(PublicationCurrentAudio{Version: 1, Facts: resultFacts})
	return f, repository, input, observation, authority
}

func sourceRepublishResult(input PublicationInputs, observation PublicationStageObservation) PublicationResult {
	return PublicationResult{
		Observation: observation,
		InstalledOwnership: PublicationCurrentOwnership{Version: PublicationCurrentOwnershipVersion, Identity: PublicationFileIdentity{
			SizeBytes: observation.Ownership.ExpectedSize, MTime: observation.Ownership.ExpectedMTime, SHA256: observation.PayloadSHA256,
		}},
	}
}
