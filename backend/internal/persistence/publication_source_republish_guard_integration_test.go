//go:build integration

package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	var rawMetadata string
	if err := f.db.NewRaw(`SELECT release_id,metadata_revision,metadata::text FROM release_track WHERE id=?`, f.guard.ExpectedPositionID).Scan(ctx, &releaseID, &metadataRevision, &rawMetadata); err != nil {
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
	var metadataRoot map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rawMetadata), &metadataRoot); err != nil {
		t.Fatalf("decode fixture raw lyrics metadata: %v", err)
	}
	lyricsEnvelope, err := librarymetadata.DecodeEnvelope(metadataRoot["local_metadata"])
	if err != nil {
		t.Fatalf("decode fixture raw lyrics authority: %v", err)
	}
	manualField, exists := lyricsEnvelope.Desired["synced_lyrics"]
	if !exists || manualField.Authority != librarymetadata.AuthorityManual || manualField.State != librarymetadata.StateValue {
		t.Fatalf("fixture does not contain the expected saved manual lyrics value: %+v", manualField)
	}
	manualLyrics, ok := manualField.Value.(string)
	if !ok {
		t.Fatalf("fixture manual lyrics value has type %T", manualField.Value)
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
		Lyrics:          PublicationRepublishLyrics{State: "value", Origin: "manual", Text: manualLyrics, SHA256: testAssetDigest(manualLyrics)},
		LyricsAuthority: PublicationInitialLyricsInput{ExpectedMetadataRevision: metadataRevision, Desired: &manualField},
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

	changedLyrics := map[string]any{"local_metadata": map[string]any{
		"version": 1, "scope": "position", "desired": map[string]any{
			"synced_lyrics": librarymetadata.FieldValue{
				State: librarymetadata.StateExplicitEmpty, Authority: librarymetadata.AuthorityManual,
				Provenance: librarymetadata.Provenance{Kind: librarymetadata.ProvenanceManual},
			},
		},
	}}
	if _, err := f.db.ExecContext(ctx, `UPDATE release_track SET metadata=?::jsonb WHERE id=?`, string(mustJSON(changedLyrics)), f.guard.ExpectedPositionID); err != nil {
		t.Fatalf("change raw lyrics authority without changing its revision: %v", err)
	}
	_, valid, err = repository.PublicationPreparationInputs(ctx, f.guard)
	if err != nil || valid {
		t.Fatalf("preparation accepted changed raw lyrics authority: valid=%v err=%v", valid, err)
	}
	currentResult, err = repository.CheckPublicationResultCurrent(ctx, f.guard)
	if err != nil || currentResult {
		t.Fatalf("result freshness accepted changed raw lyrics authority: current=%v err=%v", currentResult, err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE release_track SET metadata=?::jsonb WHERE id=?`, rawMetadata, f.guard.ExpectedPositionID); err != nil {
		t.Fatalf("restore raw lyrics authority: %v", err)
	}
	prepared, valid, err = repository.PublicationPreparationInputs(ctx, f.guard)
	if err != nil || !valid {
		t.Fatalf("preparation rejected restored lyrics authority: valid=%v err=%v", valid, err)
	}
	currentResult, err = repository.CheckPublicationResultCurrent(ctx, f.guard)
	if err != nil || !currentResult {
		t.Fatalf("result freshness rejected restored lyrics authority: current=%v err=%v", currentResult, err)
	}

	if _, err := f.db.ExecContext(ctx, `UPDATE release_publication_cover SET revision=revision+1 WHERE release_id=?`, releaseID); err != nil {
		t.Fatalf("change selected cover authority revision: %v", err)
	}
	_, valid, err = repository.PublicationPreparationInputs(ctx, f.guard)
	if err != nil || valid {
		t.Fatalf("preparation accepted changed cover authority: valid=%v err=%v", valid, err)
	}
	currentResult, err = repository.CheckPublicationResultCurrent(ctx, f.guard)
	if err != nil || currentResult {
		t.Fatalf("result freshness accepted changed cover authority: current=%v err=%v", currentResult, err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE release_publication_cover SET revision=? WHERE release_id=?`, coverRevision, releaseID); err != nil {
		t.Fatalf("restore frozen cover authority revision: %v", err)
	}
	prepared, valid, err = repository.PublicationPreparationInputs(ctx, f.guard)
	if err != nil || !valid {
		t.Fatalf("preparation rejected restored cover authority: valid=%v err=%v", valid, err)
	}
	currentResult, err = repository.CheckPublicationResultCurrent(ctx, f.guard)
	if err != nil || !currentResult {
		t.Fatalf("result freshness rejected restored cover authority: current=%v err=%v", currentResult, err)
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

	// A source republish may move the publication while proving the old output
	// absent. The proof retires only the exact old file claim/reservation.
	absent, absentRepository, absentInput, absentObservation, _ := prepareSourceRepublishResultFixture(t, db, "source-republish-absent-"+uuid.NewString())
	var oldClaimID uuid.UUID
	if err := db.NewRaw(`SELECT id FROM publication_namespace_claim WHERE epoch=? AND owner_position_id=? AND relative_path=? AND node_kind='file'`, absent.guard.Epoch, absent.guard.ExpectedPositionID, absentInput.SourceRepublish.Current.RelativePath).Scan(ctx, &oldClaimID); err != nil {
		t.Fatalf("read old output claim: %v", err)
	}
	newPath := absentObservation.RelativePath + ".republished"
	absentObservation.RelativePath = newPath
	var attempt TrackPublicationAttempt
	if err := db.NewSelect().Model(&attempt).Where("operation_id=?", absent.guard.OperationID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Version int                        `json:"version"`
		Nodes   []PublicationNamespaceNode `json:"nodes"`
	}
	if err := json.Unmarshal(attempt.NamespaceManifest, &manifest); err != nil {
		t.Fatalf("decode attempt namespace manifest: %v", err)
	}
	for i := range manifest.Nodes {
		if manifest.Nodes[i].NodeKind == "file" {
			manifest.Nodes[i].CanonicalKey, manifest.Nodes[i].RelativePath = newPath, newPath
		}
	}
	if _, err := db.ExecContext(ctx, `UPDATE track_publication_attempt SET namespace_manifest=?::jsonb WHERE operation_id=?`, string(mustJSON(manifest)), absent.guard.OperationID); err != nil {
		t.Fatalf("freeze distinct candidate path: %v", err)
	}
	// The candidate generation must reserve the manifest's destination file, not
	// the old path, or the absence proof would still see a live consumer.
	newClaimID := uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO publication_namespace_claim(id,epoch,key_digest,canonical_key,relative_path,node_kind,owner_position_id) VALUES (?,?,?,?,?,'file',?)`,
		newClaimID, absent.guard.Epoch, publicationNamespaceKeyDigest(newPath), newPath, newPath, absent.guard.ExpectedPositionID); err != nil {
		t.Fatalf("seed candidate destination file claim: %v", err)
	}
	released, err := db.ExecContext(ctx, `DELETE FROM publication_namespace_reservation WHERE namespace_claim_id=? AND position_id=? AND generation=?`,
		oldClaimID, absent.guard.ExpectedPositionID, absent.guard.CandidateGeneration)
	if err != nil {
		t.Fatalf("release old file claim candidate reservation: %v", err)
	}
	if rows, err := released.RowsAffected(); err != nil || rows != 1 {
		t.Fatalf("release old file claim candidate reservation rows=%d err=%v", rows, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO publication_namespace_reservation(namespace_claim_id,position_id,generation) VALUES (?,?,?)`,
		newClaimID, absent.guard.ExpectedPositionID, absent.guard.CandidateGeneration); err != nil {
		t.Fatalf("reserve candidate destination file claim: %v", err)
	}
	absentResult := sourceRepublishResult(absentInput, absentObservation)
	absentResult.ObsoleteOutputAbsent = &PublicationObsoleteOutputAbsent{
		Epoch: absent.guard.Epoch, PositionID: absent.guard.ExpectedPositionID,
		Generation: absentInput.SourceRepublish.Current.Generation, NamespaceClaimID: oldClaimID,
		RelativePath: absentInput.SourceRepublish.Current.RelativePath,
	}
	wrongClaim := absentResult
	wrongProof := *absentResult.ObsoleteOutputAbsent
	wrongProof.NamespaceClaimID = uuid.New()
	wrongClaim.ObsoleteOutputAbsent = &wrongProof
	if committed, err := absentRepository.CommitPublicationResult(ctx, absent.guard, wrongClaim); err == nil || committed {
		t.Fatalf("wrong absence claim result = %v, %v; want rejected proof", committed, err)
	}
	var stillLive int
	if err := db.NewRaw(`SELECT count(*) FROM track_publication_attempt WHERE operation_id=?`, absent.guard.OperationID).Scan(ctx, &stillLive); err != nil || stillLive != 1 {
		t.Fatalf("wrong proof consumed attempt: count=%d err=%v", stillLive, err)
	}
	committed, err = absentRepository.CommitPublicationResult(ctx, absent.guard, absentResult)
	if err != nil || !committed {
		t.Fatalf("commit source-republish with absent old output: %v, %v", committed, err)
	}
	var oldReservations, oldClaims, cleanupJobs int
	for query, target := range map[string]*int{
		`SELECT count(*) FROM publication_namespace_reservation WHERE namespace_claim_id=?`: &oldReservations,
		`SELECT count(*) FROM publication_namespace_claim WHERE id=?`:                       &oldClaims,
		`SELECT count(*) FROM river_job WHERE kind=? AND args->>'operation_id'=?`:           &cleanupJobs,
	} {
		args := []any{oldClaimID}
		if strings.Contains(query, "river_job") {
			args = []any{ObsoletePublicationOutputCleanupJobKind, absent.guard.OperationID.String()}
		}
		if err := db.NewRaw(query, args...).Scan(ctx, target); err != nil {
			t.Fatal(err)
		}
	}
	if oldReservations != 0 || oldClaims != 0 || cleanupJobs != 0 {
		t.Fatalf("absent output cleanup state: reservations=%d claims=%d cleanup jobs=%d", oldReservations, oldClaims, cleanupJobs)
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
		SourceRepublishOptionalResult: &PublicationSourceRepublishOptionalResult{
			LyricsOutcome: PublicationSourceRepublishLyricsNotRequested,
			CoverOutcome:  PublicationSourceRepublishCoverNotRequested,
		},
	}
}

func TestCommitSourceRepublishOptionalResultBridgesPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.OpenMigratedWithRiver(t)
	initializeRiver(t, ctx, db)

	// A selected manual value whose read-back falls back remains pending while
	// CURRENT receives a truthful warning summary and no applied receipt.
	fallback, repository, input, observation, _ := prepareSourceRepublishResultFixture(t, db, "source-republish-fallback-"+uuid.NewString())
	const manualText = "[00:01.00]saved manual lyric"
	updateSourceRepublishLyricsAuthority(t, db, fallback, &input, librarymetadata.StateValue, manualText)
	result := sourceRepublishResult(input, observation)
	result.SourceRepublishOptionalResult.LyricsOutcome = PublicationSourceRepublishLyricsRebuiltWithoutLyrics
	committed, err := repository.CommitPublicationResult(ctx, fallback.guard, result)
	if err != nil || !committed {
		t.Fatalf("commit fallback lyrics result = %v, %v", committed, err)
	}
	var current TrackPublication
	if err := db.NewSelect().Model(&current).Where("position_id=?", fallback.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	var lyricsProjection PublicationInitialLyricsProjection
	if err := json.Unmarshal(current.InitialLyrics, &lyricsProjection); err != nil {
		t.Fatalf("decode fallback lyrics summary: %v", err)
	}
	if current.ActualAssets == nil || lyricsProjection.Outcome != PublicationOptionalLyricsRebuiltWithoutLyrics ||
		lyricsProjection.OutcomeWarningCode != "readback_mismatch" || lyricsProjection.Source != "manual" ||
		lyricsProjection.SHA256 != testAssetDigest(manualText) {
		t.Fatalf("fallback did not persist truthful pending summary: projection=%+v assets=%s", lyricsProjection, current.ActualAssets)
	}
	assertManualLyricsReceiptCount(t, ctx, db, fallback.guard.ExpectedPositionID, 0)

	// Saved manual absence is a no-op, not an explicit-empty applied receipt.
	absent, absentRepository, absentInput, absentObservation, _ := prepareSourceRepublishResultFixture(t, db, "source-republish-saved-absent-"+uuid.NewString())
	updateSourceRepublishLyricsAuthority(t, db, absent, &absentInput, librarymetadata.StateAbsent, "")
	committed, err = absentRepository.CommitPublicationResult(ctx, absent.guard, sourceRepublishResult(absentInput, absentObservation))
	if err != nil || !committed {
		t.Fatalf("commit saved-absent lyrics result = %v, %v", committed, err)
	}
	var absentCurrent TrackPublication
	if err := db.NewSelect().Model(&absentCurrent).Where("position_id=?", absent.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if len(absentCurrent.InitialLyrics) != 0 {
		t.Fatalf("saved-absent manual lyrics fabricated a summary: %s", absentCurrent.InitialLyrics)
	}
	assertManualLyricsReceiptCount(t, ctx, db, absent.guard.ExpectedPositionID, 0)

	// A failed optional cover derivation still proves the frozen selected
	// authority and stores the version-2 preparation-failed summary.
	coverFailure, coverRepository, coverInput, coverObservation, _ := prepareSourceRepublishResultFixture(t, db, "source-republish-cover-failure-"+uuid.NewString())
	configureSourceRepublishCoverPreparationFailure(t, db, coverFailure, &coverInput)
	coverResult := sourceRepublishResult(coverInput, coverObservation)
	coverResult.SourceRepublishOptionalResult.CoverOutcome = PublicationSourceRepublishCoverPreparationFailed
	committed, err = coverRepository.CommitPublicationResult(ctx, coverFailure.guard, coverResult)
	if err != nil || !committed {
		t.Fatalf("commit cover-preparation-failed result = %v, %v", committed, err)
	}
	var coverCurrent TrackPublication
	if err := db.NewSelect().Model(&coverCurrent).Where("position_id=?", coverFailure.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	coverProjection, err := decodePublicationCoverEmbeddingProjection(coverCurrent.CoverEmbedding)
	if err != nil {
		t.Fatalf("decode preparation-failed cover summary: %v", err)
	}
	if coverProjection.Version != PublicationCoverEmbeddingDerivativeProjectionVersion || coverProjection.Outcome != string(PublicationSourceRepublishCoverPreparationFailed) ||
		coverProjection.WarningCode != "decode_failed" || coverProjection.RequestedGeneration != coverInput.SourceRepublish.Cover.SelectedGeneration {
		t.Fatalf("cover preparation failure summary does not retain selected authority and safe code: %+v", coverProjection)
	}
	coverAssets, err := DecodePublicationAssetOutcomes(coverCurrent.ActualAssets)
	if err != nil || coverAssets.Cover.State != PublicationAssetAbsent {
		t.Fatalf("cover failure CURRENT asset = %+v, %v", coverAssets.Cover, err)
	}

	// A receipt that contradicts frozen absent lyrics must fail before state
	// changes; the ordinary absent/not-requested receipt is the valid baseline.
	contradiction, contradictionRepository, contradictionInput, contradictionObservation, contradictionAuthority :=
		prepareSourceRepublishResultFixture(t, db, "source-republish-contradiction-"+uuid.NewString())
	contradictory := sourceRepublishResult(contradictionInput, contradictionObservation)
	contradictory.SourceRepublishOptionalResult.LyricsOutcome = PublicationSourceRepublishLyricsExactRoundTrip
	committed, err = contradictionRepository.CommitPublicationResult(ctx, contradiction.guard, contradictory)
	if err == nil || committed || !strings.Contains(err.Error(), "absent lyrics require an absent, not-requested result") {
		t.Fatalf("contradictory optional receipt commit = %v, %v", committed, err)
	}
	var unchanged TrackPublication
	if err := db.NewSelect().Model(&unchanged).Where("position_id=?", contradiction.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	var unchangedAuthority PositionSourceAuthority
	if err := db.NewSelect().Model(&unchangedAuthority).Where("position_id=?", contradiction.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if unchanged.Generation != contradictionInput.SourceRepublish.Current.Generation || unchangedAuthority != contradictionAuthority {
		t.Fatalf("contradictory receipt changed CURRENT or authority: current=%+v authority=%+v", unchanged, unchangedAuthority)
	}
	assertManualLyricsReceiptCount(t, ctx, db, contradiction.guard.ExpectedPositionID, 0)

	// Interruption receipts are accepted only with the persisted recovery mode.
	noRecovery, noRecoveryRepository, noRecoveryInput, noRecoveryObservation, _ :=
		prepareSourceRepublishResultFixture(t, db, "source-republish-observed-without-mode-"+uuid.NewString())
	updateSourceRepublishLyricsAuthority(t, db, noRecovery, &noRecoveryInput, librarymetadata.StateValue, manualText)
	noRecoveryResult := sourceRepublishResult(noRecoveryInput, noRecoveryObservation)
	noRecoveryResult.SourceRepublishOptionalResult.LyricsOutcome = PublicationSourceRepublishLyricsObservedAfterInterruption
	if committed, err := noRecoveryRepository.CommitPublicationResult(ctx, noRecovery.guard, noRecoveryResult); err == nil || committed {
		t.Fatalf("observed lyrics without recovery mode committed = %v, %v", committed, err)
	}

	// Recovery exactness is derived from actual tags/assets and records the
	// applied manual value without claiming where the lyrics were acquired.
	recoveredExact, recoveredExactRepository, recoveredExactInput, recoveredExactObservation, _ :=
		prepareSourceRepublishResultFixture(t, db, "source-republish-observed-exact-"+uuid.NewString())
	updateSourceRepublishLyricsAuthority(t, db, recoveredExact, &recoveredExactInput, librarymetadata.StateValue, manualText)
	setSourceRepublishResumeMode(t, db, recoveredExact, PublicationResumeModeInspectSourceRepublishTarget)
	exactTags := cloneProviderTagMap(recoveredExactInput.DesiredTags)
	exactTags["LYRICS"] = []string{manualText}
	recoveredExactObservation.ActualMetadata = mustJSON(PublicationCommitMetadata{
		Version: 1, Tags: exactTags,
	})
	recoveredExactObservation.ActualAssets = mustJSON(PublicationAssetOutcomes{
		Version: PublicationAssetOutcomesVersion, Cover: PublicationCoverOutcome{State: PublicationAssetAbsent},
		Lyrics: PublicationLyricsOutcome{State: PublicationAssetEmbedded, SHA256: recoveredExactInput.SourceRepublish.Lyrics.SHA256, Format: "lrc"},
	})
	recoveredExactResult := sourceRepublishResult(recoveredExactInput, recoveredExactObservation)
	recoveredExactResult.SourceRepublishOptionalResult.LyricsOutcome = PublicationSourceRepublishLyricsObservedAfterInterruption
	if committed, err := recoveredExactRepository.CommitPublicationResult(ctx, recoveredExact.guard, recoveredExactResult); err != nil || !committed {
		t.Fatalf("commit observed exact lyrics after interruption = %v, %v", committed, err)
	}
	var recoveredExactCurrent TrackPublication
	if err := db.NewSelect().Model(&recoveredExactCurrent).Where("position_id=?", recoveredExact.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	var recoveredExactProjection PublicationInitialLyricsProjection
	if err := json.Unmarshal(recoveredExactCurrent.InitialLyrics, &recoveredExactProjection); err != nil {
		t.Fatalf("decode observed exact lyrics projection: %v", err)
	}
	if recoveredExactProjection.Source != "unknown" || recoveredExactProjection.Outcome != PublicationInitialLyricsObservedAfterInterruption ||
		recoveredExactProjection.SHA256 != recoveredExactInput.SourceRepublish.Lyrics.SHA256 {
		t.Fatalf("recovered exact projection claimed acquisition provenance or lost actual digest: %+v", recoveredExactProjection)
	}
	assertManualLyricsReceiptCount(t, ctx, db, recoveredExact.guard.ExpectedPositionID, 1)

	// Observed absence does not carry the selected manual value's digest or
	// settle its receipt; explicit-empty remains an applicable manual choice.
	recoveredAbsent, recoveredAbsentRepository, recoveredAbsentInput, recoveredAbsentObservation, _ :=
		prepareSourceRepublishResultFixture(t, db, "source-republish-observed-absent-"+uuid.NewString())
	updateSourceRepublishLyricsAuthority(t, db, recoveredAbsent, &recoveredAbsentInput, librarymetadata.StateValue, manualText)
	setSourceRepublishResumeMode(t, db, recoveredAbsent, PublicationResumeModeInspectSourceRepublishTarget)
	configureSourceRepublishCoverPreparationFailure(t, db, recoveredAbsent, &recoveredAbsentInput)
	originalCover := releaseCoverPNG(t)
	originalCoverDigest := sha256.Sum256(originalCover)
	originalCoverSize := int64(len(originalCover))
	recoveredAbsentInput.SourceRepublish.Cover = PublicationRepublishCover{
		ReleaseID:          recoveredAbsentInput.SourceRepublish.Cover.ReleaseID,
		AuthorityRevision:  recoveredAbsentInput.SourceRepublish.Cover.AuthorityRevision,
		AuthorityMode:      recoveredAbsentInput.SourceRepublish.Cover.AuthorityMode,
		SelectedGeneration: recoveredAbsentInput.SourceRepublish.Cover.SelectedGeneration,
		OriginalSHA256:     hex.EncodeToString(originalCoverDigest[:]), OriginalMIME: "image/png",
		OriginalSizeBytes: &originalCoverSize, Derivation: "ready", PNG: originalCover,
		SHA256: hex.EncodeToString(originalCoverDigest[:]), MIME: "image/png", SizeBytes: originalCoverSize,
	}
	if err := recoveredAbsentInput.Validate(); err != nil {
		t.Fatalf("validate recovered ready cover authority: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE track_publication_attempt SET frozen_inputs=?::jsonb WHERE operation_id=?`, string(mustJSON(recoveredAbsentInput)), recoveredAbsent.guard.OperationID); err != nil {
		t.Fatalf("freeze recovered ready cover authority: %v", err)
	}
	recoveredAbsentResult := sourceRepublishResult(recoveredAbsentInput, recoveredAbsentObservation)
	recoveredAbsentResult.SourceRepublishOptionalResult.LyricsOutcome = PublicationSourceRepublishLyricsObservedAfterInterruption
	recoveredAbsentResult.SourceRepublishOptionalResult.CoverOutcome = PublicationSourceRepublishCoverAbsentAfterInterruption
	if committed, err := recoveredAbsentRepository.CommitPublicationResult(ctx, recoveredAbsent.guard, recoveredAbsentResult); err != nil || !committed {
		t.Fatalf("commit observed absent lyrics after interruption = %v, %v", committed, err)
	}
	var recoveredAbsentCurrent TrackPublication
	if err := db.NewSelect().Model(&recoveredAbsentCurrent).Where("position_id=?", recoveredAbsent.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	var recoveredAbsentProjection PublicationInitialLyricsProjection
	if err := json.Unmarshal(recoveredAbsentCurrent.InitialLyrics, &recoveredAbsentProjection); err != nil {
		t.Fatalf("decode observed absent lyrics projection: %v", err)
	}
	if recoveredAbsentProjection.Source != "unknown" || recoveredAbsentProjection.Outcome != PublicationInitialLyricsObservedAfterInterruption || recoveredAbsentProjection.SHA256 != "" {
		t.Fatalf("recovered absence fabricated selected provenance or digest: %+v", recoveredAbsentProjection)
	}
	coverProjection, err = decodePublicationCoverEmbeddingProjection(recoveredAbsentCurrent.CoverEmbedding)
	if err != nil || coverProjection.Outcome != string(PublicationSourceRepublishCoverAbsentAfterInterruption) || coverProjection.WarningCode != "cover_absent_after_interruption" {
		t.Fatalf("recovered cover absence projection = %+v, %v", coverProjection, err)
	}
	assertManualLyricsReceiptCount(t, ctx, db, recoveredAbsent.guard.ExpectedPositionID, 0)

	recoveredEmpty, recoveredEmptyRepository, recoveredEmptyInput, recoveredEmptyObservation, _ :=
		prepareSourceRepublishResultFixture(t, db, "source-republish-observed-explicit-empty-"+uuid.NewString())
	updateSourceRepublishLyricsAuthority(t, db, recoveredEmpty, &recoveredEmptyInput, librarymetadata.StateExplicitEmpty, "")
	setSourceRepublishResumeMode(t, db, recoveredEmpty, PublicationResumeModeInspectSourceRepublishTarget)
	recoveredEmptyResult := sourceRepublishResult(recoveredEmptyInput, recoveredEmptyObservation)
	recoveredEmptyResult.SourceRepublishOptionalResult.LyricsOutcome = PublicationSourceRepublishLyricsObservedAfterInterruption
	if committed, err := recoveredEmptyRepository.CommitPublicationResult(ctx, recoveredEmpty.guard, recoveredEmptyResult); err != nil || !committed {
		t.Fatalf("commit observed explicit-empty lyrics after interruption = %v, %v", committed, err)
	}
	assertManualLyricsReceiptCount(t, ctx, db, recoveredEmpty.guard.ExpectedPositionID, 1)
}

func setSourceRepublishResumeMode(t *testing.T, db *bun.DB, fixture publicationExecutionFixture, mode string) {
	t.Helper()
	result, err := db.ExecContext(context.Background(), `UPDATE track_publication_attempt SET resume_mode=? WHERE operation_id=?`, mode, fixture.guard.OperationID)
	if err != nil {
		t.Fatalf("set source-republish resume mode: %v", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		t.Fatalf("set source-republish resume mode rows=%d err=%v", affected, err)
	}
}

func updateSourceRepublishLyricsAuthority(
	t *testing.T,
	db *bun.DB,
	f publicationExecutionFixture,
	input *PublicationInputs,
	state librarymetadata.ValueState,
	text string,
) {
	t.Helper()
	field := librarymetadata.FieldValue{State: state, Authority: librarymetadata.AuthorityManual, Provenance: librarymetadata.Provenance{Kind: librarymetadata.ProvenanceManual}}
	if state == librarymetadata.StateValue {
		field.Value = text
		input.SourceRepublish.Lyrics = PublicationRepublishLyrics{State: "value", Origin: "manual", Text: text, SHA256: testAssetDigest(text)}
	} else {
		input.SourceRepublish.Lyrics = PublicationRepublishLyrics{State: "absent", Origin: "none"}
	}
	input.SourceRepublish.LyricsAuthority.Desired = &field
	if err := input.Validate(); err != nil {
		t.Fatalf("validate updated source-republish lyrics authority: %v", err)
	}
	metadata := map[string]any{"local_metadata": map[string]any{
		"version": 1, "scope": "position", "desired": map[string]any{"synced_lyrics": field},
	}}
	if err := db.RunInTx(context.Background(), nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE release_track SET metadata=?::jsonb WHERE id=?`, string(mustJSON(metadata)), f.guard.ExpectedPositionID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE track_publication_attempt SET frozen_inputs=?::jsonb WHERE operation_id=?`, string(mustJSON(*input)), f.guard.OperationID)
		return err
	}); err != nil {
		t.Fatalf("freeze changed source-republish lyrics authority: %v", err)
	}
}

func configureSourceRepublishCoverPreparationFailure(t *testing.T, db *bun.DB, f publicationExecutionFixture, input *PublicationInputs) {
	t.Helper()
	original := releaseCoverPNG(t)
	digest := sha256.Sum256(original)
	size := int64(len(original))
	selectedGeneration := uuid.New()
	input.SourceRepublish.Cover = PublicationRepublishCover{
		ReleaseID: input.SourceRepublish.Cover.ReleaseID, AuthorityRevision: input.SourceRepublish.Cover.AuthorityRevision,
		AuthorityMode: input.SourceRepublish.Cover.AuthorityMode, SelectedGeneration: selectedGeneration,
		OriginalSHA256: hex.EncodeToString(digest[:]), OriginalMIME: "image/png", OriginalSizeBytes: &size,
		Derivation: "optional_failure", Failure: "decode_failed",
	}
	if err := input.Validate(); err != nil {
		t.Fatalf("validate frozen source cover preparation failure: %v", err)
	}
	if err := db.RunInTx(context.Background(), nil, func(ctx context.Context, tx bun.Tx) error {
		updated, err := tx.ExecContext(ctx, `UPDATE release_publication_cover SET selected_generation=?,selected_bytes=?,selected_mime='image/png',selected_sha256=?,selected_size=? WHERE release_id=?`,
			selectedGeneration, original, digest[:], size, input.SourceRepublish.Cover.ReleaseID)
		if err != nil {
			return err
		}
		if rows, err := updated.RowsAffected(); err != nil || rows != 1 {
			return fmt.Errorf("selected source cover authority rows=%d err=%v", rows, err)
		}
		_, err = tx.ExecContext(ctx, `UPDATE track_publication_attempt SET frozen_inputs=?::jsonb WHERE operation_id=?`, string(mustJSON(*input)), f.guard.OperationID)
		return err
	}); err != nil {
		t.Fatalf("freeze selected source cover authority: %v", err)
	}
}

func assertManualLyricsReceiptCount(t *testing.T, ctx context.Context, db *bun.DB, positionID uuid.UUID, want int) {
	t.Helper()
	var count int
	if err := db.NewRaw(`SELECT count(*) FROM publication_manual_lyrics_receipt WHERE position_id=?`, positionID).Scan(ctx, &count); err != nil || count != want {
		t.Fatalf("manual lyrics receipt count=%d, want %d; err=%v", count, want, err)
	}
}
