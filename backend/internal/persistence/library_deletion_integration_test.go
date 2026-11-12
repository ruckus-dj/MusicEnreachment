//go:build integration

package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

type libraryDeletionInsertThenFail struct {
	RiverInserter
	err error
}

func (inserter libraryDeletionInsertThenFail) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, options *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	if _, err := inserter.RiverInserter.InsertTx(ctx, tx, args, options); err != nil {
		return nil, err
	}
	return nil, inserter.err
}

func TestLibraryDeletionPreservesSharedNamespaceAndPublicationScopePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.OpenMigratedWithRiver(t)
	repository := NewSetupManagerRepository(db)
	initializeRiver(t, ctx, db)
	client, err := river.NewClient(riverdatabasesql.New(db.DB), &river.Config{})
	if err != nil {
		t.Fatalf("create real River client: %v", err)
	}

	var epoch uuid.UUID
	if err := db.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &epoch); err != nil {
		t.Fatal(err)
	}
	targetRelease, foreignRelease := uuid.New(), uuid.New()
	targetPosition, targetSharedPosition, foreignPosition := uuid.New(), uuid.New(), uuid.New()
	survivorPosition := uuid.New()
	targetTrack, foreignTrack := uuid.New(), uuid.New()
	targetRecording, sharedRecording := uuid.New(), uuid.New()
	artistID, foreignPrimaryArtistID, collectionID := uuid.New(), uuid.New(), uuid.New()
	providerID, providerReleaseID, providerRecordingID, providerTrackID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	targetVariant, sharedVariant := uuid.New(), uuid.New()
	targetSourceID, sharedSourceID := uuid.New(), uuid.New()
	targetGeneration, foreignGeneration := uuid.New(), uuid.New()
	sharedDirectoryClaim, targetFileClaim, foreignFileClaim := uuid.New(), uuid.New(), uuid.New()
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for _, query := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO local_release(id,title) VALUES (?,?)`, []any{targetRelease, "deletion target"}},
			{`INSERT INTO local_release(id,title) VALUES (?,?)`, []any{foreignRelease, "foreign release"}},
			{`INSERT INTO local_track(id,title) VALUES (?,?)`, []any{targetTrack, "target track"}},
			{`INSERT INTO local_track(id,title) VALUES (?,?)`, []any{foreignTrack, "foreign track"}},
			{`INSERT INTO local_recording(id,title) VALUES (?,?)`, []any{targetRecording, "target recording"}},
			{`INSERT INTO local_recording(id,title) VALUES (?,?)`, []any{sharedRecording, "shared recording"}},
			{`INSERT INTO release_track(id,release_id,position,track_id,recording_id) VALUES (?,?,0,?,?)`, []any{targetPosition, targetRelease, targetTrack, targetRecording}},
			{`INSERT INTO release_track(id,release_id,position,track_id,recording_id) VALUES (?,?,1,?,?)`, []any{targetSharedPosition, targetRelease, foreignTrack, sharedRecording}},
			{`INSERT INTO release_track(id,release_id,position,track_id,recording_id) VALUES (?,?,0,?,?)`, []any{foreignPosition, foreignRelease, foreignTrack, sharedRecording}},
			{`INSERT INTO local_artist(id,name) VALUES (?, 'retained artist')`, []any{artistID}},
			{`INSERT INTO local_artist(id,name) VALUES (?, 'foreign primary artist')`, []any{foreignPrimaryArtistID}},
			{`UPDATE local_release SET primary_artist_id=? WHERE id=?`, []any{foreignPrimaryArtistID, foreignRelease}},
			{`INSERT INTO track_artist(track_id,position,artist_id,name) VALUES (?,0,?,'retained artist')`, []any{foreignTrack, artistID}},
			{`INSERT INTO release_artist(release_id,position,artist_id,name) VALUES (?,0,?,'retained artist')`, []any{targetRelease, artistID}},
			{`INSERT INTO release_artist(release_id,position,artist_id,name) VALUES (?,0,?,'retained artist')`, []any{foreignRelease, artistID}},
			{`INSERT INTO release_artist(release_id,position,artist_id,name) VALUES (?,1,?,'foreign primary artist')`, []any{foreignRelease, foreignPrimaryArtistID}},
			{`INSERT INTO provider(id,code) VALUES (?, 'deletion-test')`, []any{providerID}},
			{`INSERT INTO provider_release(id,provider_id,provider_key,title) VALUES (?,?, 'release-key','provider release')`, []any{providerReleaseID, providerID}},
			{`INSERT INTO provider_recording(id,provider_id,provider_key,title) VALUES (?,?, 'recording-key','provider recording')`, []any{providerRecordingID, providerID}},
			{`INSERT INTO provider_release_track(id,provider_id,release_id,recording_id,medium,position,displayed_number) VALUES (?,?,?,?,0,0,'1')`, []any{providerTrackID, providerID, providerReleaseID, providerRecordingID}},
			{`INSERT INTO provider_local_track(provider_id,provider_track_id,local_track_id,method) VALUES (?,?,?,'manual')`, []any{providerID, providerTrackID, targetTrack}},
			{`INSERT INTO provider_local_recording(provider_id,provider_recording_id,local_recording_id,method) VALUES (?,?,?,'manual')`, []any{providerID, providerRecordingID, targetRecording}},
			{`INSERT INTO media_variant(id,size_bytes) VALUES (?,1)`, []any{targetVariant}},
			{`INSERT INTO media_variant(id,size_bytes) VALUES (?,1)`, []any{sharedVariant}},
			{`INSERT INTO track_source(id,track_id,media_variant_id,selected,compatible,decision_method,metadata) VALUES (?,?,?,false,true,'manual','{}'::jsonb)`, []any{targetSourceID, targetTrack, targetVariant}},
			{`INSERT INTO track_source(id,track_id,media_variant_id,selected,compatible,decision_method,metadata) VALUES (?,?,?,false,true,'manual','{}'::jsonb)`, []any{sharedSourceID, foreignTrack, sharedVariant}},
			{`INSERT INTO collection(id,title) VALUES (?, 'retained collection')`, []any{collectionID}},
			{`INSERT INTO collection_release(collection_id,position,release_id) VALUES (?,0,?)`, []any{collectionID, foreignRelease}},
			{`INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at) VALUES (?,?,?,'root/target.flac','{"size":11}','{}','{}','{}',1,now())`, []any{targetPosition, epoch, targetGeneration}},
			{`INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at) VALUES (?,?,?,'root/foreign.flac','{"size":12}','{}','{}','{}',1,now())`, []any{foreignPosition, epoch, foreignGeneration}},
		} {
			if _, err := tx.ExecContext(ctx, query.sql, query.args...); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed deletion fixture: %v", err)
	}
	insertNamespaceClaim := func(id uuid.UUID, key, relative, kind string, owner *uuid.UUID, scope *string) {
		t.Helper()
		var ownerValue, scopeValue any
		if owner != nil {
			ownerValue = *owner
		}
		if scope != nil {
			scopeValue = *scope
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO publication_namespace_claim(id,epoch,key_digest,canonical_key,relative_path,node_kind,owner_position_id,directory_scope_key)
			VALUES (?,?,?::bytea,?,?,?, ?,?)`, id, epoch, publicationNamespaceKeyDigest(key), key, relative, kind, ownerValue, scopeValue); err != nil {
			t.Fatalf("seed namespace claim %q: %v", key, err)
		}
	}
	insertReservation := func(claimID, positionID, generation uuid.UUID) {
		t.Helper()
		if _, err := db.ExecContext(ctx, `INSERT INTO publication_namespace_reservation(namespace_claim_id,position_id,generation) VALUES (?,?,?)`, claimID, positionID, generation); err != nil {
			t.Fatalf("seed namespace reservation: %v", err)
		}
	}
	directoryScope := "root"
	insertNamespaceClaim(sharedDirectoryClaim, "root", "root", "dir", nil, &directoryScope)
	insertReservation(sharedDirectoryClaim, targetPosition, targetGeneration)
	insertReservation(sharedDirectoryClaim, foreignPosition, foreignGeneration)
	insertNamespaceClaim(targetFileClaim, "root/target.flac", "root/target.flac", "file", &targetPosition, nil)
	insertReservation(targetFileClaim, targetPosition, targetGeneration)
	insertNamespaceClaim(foreignFileClaim, "root/foreign.flac", "root/foreign.flac", "file", &foreignPosition, nil)
	insertReservation(foreignFileClaim, foreignPosition, foreignGeneration)

	admitAndClaim := func(kind string, releaseID, positionID uuid.UUID, rollbackInsert bool) LibraryDeletionDeliveryGuard {
		t.Helper()
		preview, err := repository.PreviewLibraryDeletion(ctx, kind, releaseID, positionID)
		if err != nil {
			t.Fatalf("preview %s deletion: %v", kind, err)
		}
		if kind == "release" && (preview.RecordingCount != 1 || len(preview.RecordingIDs) != 1 || preview.RecordingIDs[0] != targetRecording) {
			t.Fatalf("release preview orphan recordings = %v (%d), want only %s", preview.RecordingIDs, preview.RecordingCount, targetRecording)
		}
		publications := make([]TrackPublication, 0, 1)
		if len(preview.Scope.PublicationIDs) > 0 {
			if err := db.NewSelect().Model(&publications).Where("position_id IN (?)", bun.In(preview.Scope.PublicationIDs)).Order("position_id").Scan(ctx); err != nil {
				t.Fatalf("read deletion witness source: %v", err)
			}
		}
		witnesses := make([]json.RawMessage, 0, len(publications))
		for _, publication := range publications {
			raw, err := json.Marshal(LibraryDeletionPublicationWitness{
				PositionID: publication.PositionID, Epoch: publication.Epoch, Generation: publication.Generation,
				RelativePath: publication.RelativePath, Ownership: publication.Ownership,
			})
			if err != nil {
				t.Fatal(err)
			}
			witnesses = append(witnesses, raw)
		}
		nonce := uuid.NewString()
		digestBytes := sha256.Sum256([]byte(preview.ScopeDigest + nonce))
		confirmationDigest := hex.EncodeToString(digestBytes[:])
		operationID, err := LibraryDeletionOperationID(confirmationDigest)
		if err != nil {
			t.Fatal(err)
		}
		expiresAt := time.Now().Add(time.Minute)
		if rollbackInsert {
			insertFailure := errors.New("injected failure after real River insert")
			if _, err := repository.AdmitLibraryDeletionOperation(ctx, operationID, confirmationDigest, expiresAt, preview, witnesses,
				libraryDeletionInsertThenFail{RiverInserter: client, err: insertFailure}, nil); !errors.Is(err, insertFailure) {
				t.Fatalf("admission with post-insert failure returned %v", err)
			}
			var operationRows, jobRows int
			if err := db.NewRaw(`SELECT count(*) FROM operation WHERE id=?`, operationID).Scan(ctx, &operationRows); err != nil {
				t.Fatal(err)
			}
			if err := db.NewRaw(`SELECT count(*) FROM river_job WHERE kind=? AND args->>'operation_id'=?`, LibraryDeletionOperationKind, operationID.String()).Scan(ctx, &jobRows); err != nil {
				t.Fatal(err)
			}
			if operationRows != 0 || jobRows != 0 {
				t.Fatalf("failed admission left operation/job rows: %d/%d", operationRows, jobRows)
			}
		}
		operation, err := repository.AdmitLibraryDeletionOperation(ctx, operationID, confirmationDigest, expiresAt, preview, witnesses, client, nil)
		if err != nil {
			t.Fatalf("admit %s deletion: %v", kind, err)
		}
		jobID := operation.RiverJobID
		if kind == "release" {
			if _, err := db.ExecContext(ctx, `UPDATE operation SET input_snapshot=jsonb_set(input_snapshot,'{dependencies}','{}'::jsonb) WHERE id=?`, operation.ID); err == nil || !strings.Contains(err.Error(), "library deletion confirmation and residual scope are immutable") {
				t.Fatalf("frozen dependency mutation: expected immutable confirmation rejection, got %v", err)
			}
			if _, err := db.ExecContext(ctx, `INSERT INTO collection_release(collection_id,position,release_id) VALUES (?,1,?)`, collectionID, releaseID); err != nil {
				t.Fatalf("change release scope after admission: %v", err)
			}
			if _, err := db.ExecContext(ctx, `INSERT INTO release_track(id,release_id,position,track_id,recording_id) VALUES (?,?,1,?,?)`, survivorPosition, foreignRelease, targetTrack, targetRecording); err != nil {
				t.Fatalf("add a survivor reference after admission: %v", err)
			}
			changedPreview, err := repository.PreviewLibraryDeletion(ctx, kind, releaseID, positionID)
			if err != nil {
				t.Fatalf("preview changed release scope: %v", err)
			}
			if changedPreview.ScopeDigest == preview.ScopeDigest {
				t.Fatal("survivor-set change did not change the deletion scope digest")
			}
			repeated, err := repository.AdmitLibraryDeletionOperation(ctx, operationID, confirmationDigest, expiresAt, changedPreview, witnesses, client, nil)
			if err != nil || repeated == nil || repeated.ID != operation.ID || repeated.RiverJobID == nil || jobID == nil || *repeated.RiverJobID != *jobID {
				t.Fatalf("resolve repeated admission after scope change: operation=%v err=%v", repeated, err)
			}
			result, err := db.ExecContext(ctx, `DELETE FROM collection_release WHERE collection_id=? AND position=1 AND release_id=?`, collectionID, releaseID)
			if err != nil {
				t.Fatalf("restore admitted release scope: %v", err)
			}
			if rows, err := result.RowsAffected(); err != nil || rows != 1 {
				t.Fatalf("restore admitted release scope rows=%d err=%v, want one", rows, err)
			}
			if _, err := db.ExecContext(ctx, `DELETE FROM release_track WHERE id=?`, survivorPosition); err != nil {
				t.Fatalf("restore survivor reference: %v", err)
			}
		}
		repeated, err := repository.AdmitLibraryDeletionOperation(ctx, operationID, confirmationDigest, expiresAt, preview, witnesses, client, nil)
		if err != nil || repeated.ID != operation.ID || repeated.RiverJobID == nil || jobID == nil || *repeated.RiverJobID != *jobID {
			t.Fatalf("repeat %s admission: operation=%v err=%v", kind, repeated, err)
		}
		var jobsForOperation int
		if err := db.NewRaw(`SELECT count(*) FROM river_job WHERE kind=? AND args->>'operation_id'=?`, LibraryDeletionOperationKind, operationID.String()).Scan(ctx, &jobsForOperation); err != nil {
			t.Fatal(err)
		}
		if jobsForOperation != 1 {
			t.Fatalf("repeat admission created %d River jobs, want one", jobsForOperation)
		}
		resolved, err := repository.FindLibraryDeletionConfirmation(ctx, operationID, confirmationDigest)
		if err != nil || resolved == nil || resolved.ID != operationID || resolved.RiverJobID == nil || *resolved.RiverJobID != *jobID {
			t.Fatalf("resolve repeated admission: operation=%v err=%v", resolved, err)
		}
		if operation.RiverJobID == nil {
			t.Fatal("admission did not retain the inserted River job identity")
		}
		var rawArgs []byte
		if err := db.NewRaw(`SELECT args FROM river_job WHERE id=?`, *operation.RiverJobID).Scan(ctx, &rawArgs); err != nil {
			t.Fatalf("read inserted River args: %v", err)
		}
		var jobArgs map[string]json.RawMessage
		if err := json.Unmarshal(rawArgs, &jobArgs); err != nil {
			t.Fatalf("decode inserted River args: %v", err)
		}
		if len(jobArgs) != 1 || json.Unmarshal(jobArgs["operation_id"], new(uuid.UUID)) != nil {
			t.Fatalf("River args are not the ID-only deletion payload: %s", rawArgs)
		}
		var argOperationID uuid.UUID
		if err := json.Unmarshal(jobArgs["operation_id"], &argOperationID); err != nil || argOperationID != operation.ID {
			t.Fatalf("River args operation ID = %s, want %s (err %v)", argOperationID, operation.ID, err)
		}
		if _, err := db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, *operation.RiverJobID).Exec(ctx); err != nil {
			t.Fatalf("mark inserted River delivery running: %v", err)
		}
		guard := LibraryDeletionDeliveryGuard{
			OperationID: operation.ID, ApplicationAttempt: operation.Attempt, RiverJobID: *operation.RiverJobID,
			RiverAttempt: 1, ClaimUUID: uuid.New(), BootUUID: uuid.New(), DeliveryToken: uuid.NewString(),
		}
		outcome, err := repository.ClaimLibraryDeletionDelivery(ctx, guard)
		if err != nil || outcome != LibraryDeletionDeliveryAcquired {
			t.Fatalf("claim %s deletion: outcome=%q err=%v", kind, outcome, err)
		}
		var claim PublicationExecutionClaim
		if err := db.NewSelect().Model(&claim).Where("operation_id=? AND application_attempt=?", operation.ID, operation.Attempt).Scan(ctx); err != nil {
			t.Fatalf("read inserted deletion claim: %v", err)
		}
		if !libraryDeletionClaimMatches(claim, guard) || claim.RiverJobID != *operation.RiverJobID {
			t.Fatalf("delivery claim does not bind inserted job and operation: %+v", claim)
		}
		return guard
	}

	releaseGuard := admitAndClaim("release", targetRelease, uuid.Nil, true)
	if settled, err := repository.SettleLibraryDeletionPublication(ctx, releaseGuard, targetPosition, func(context.Context, json.RawMessage) error { return nil }); err != nil || !settled {
		t.Fatalf("settle release publication: settled=%t err=%v", settled, err)
	}
	if finalized, err := repository.FinalizeLibraryDeletion(ctx, releaseGuard); err != nil || !finalized {
		t.Fatalf("finalize release deletion: finalized=%t err=%v", finalized, err)
	}
	var sharedClaims, foreignReservations, targetPositions, sharedTargetPositions, targetTracks, targetRecordings, sharedTracks, sharedRecordings int
	if err := db.NewRaw(`SELECT count(*) FROM publication_namespace_claim WHERE id=?`, sharedDirectoryClaim).Scan(ctx, &sharedClaims); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM publication_namespace_reservation WHERE namespace_claim_id=? AND position_id=? AND generation=?`, sharedDirectoryClaim, foreignPosition, foreignGeneration).Scan(ctx, &foreignReservations); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM release_track WHERE id=?`, targetPosition).Scan(ctx, &targetPositions); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM release_track WHERE id=?`, targetSharedPosition).Scan(ctx, &sharedTargetPositions); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM local_track WHERE id=?`, targetTrack).Scan(ctx, &targetTracks); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM local_recording WHERE id=?`, targetRecording).Scan(ctx, &targetRecordings); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM local_track WHERE id=?`, foreignTrack).Scan(ctx, &sharedTracks); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM local_recording WHERE id=?`, sharedRecording).Scan(ctx, &sharedRecordings); err != nil {
		t.Fatal(err)
	}
	if sharedClaims != 1 || foreignReservations != 1 || targetPositions != 0 || sharedTargetPositions != 0 || targetTracks != 0 || targetRecordings != 0 || sharedTracks != 1 || sharedRecordings != 1 {
		t.Fatalf("release deletion: shared claims=%d foreign reservations=%d target positions=%d/%d target track/recording=%d/%d shared track/recording=%d/%d", sharedClaims, foreignReservations, targetPositions, sharedTargetPositions, targetTracks, targetRecordings, sharedTracks, sharedRecordings)
	}
	var orphanTrackMappings, orphanRecordingMappings, orphanSources, retainedSource, retainedMediaVariant, retainedProviderRows, retainedProviderRecording int
	for _, check := range []struct {
		query       string
		args        []any
		destination *int
	}{
		{`SELECT count(*) FROM provider_local_track WHERE local_track_id=?`, []any{targetTrack}, &orphanTrackMappings},
		{`SELECT count(*) FROM provider_local_recording WHERE local_recording_id=?`, []any{targetRecording}, &orphanRecordingMappings},
		{`SELECT count(*) FROM track_source WHERE id=?`, []any{targetSourceID}, &orphanSources},
		{`SELECT count(*) FROM track_source WHERE id=?`, []any{sharedSourceID}, &retainedSource},
		{`SELECT count(*) FROM media_variant WHERE id IN (?,?)`, []any{targetVariant, sharedVariant}, &retainedMediaVariant},
		{`SELECT count(*) FROM provider_release_track WHERE id=?`, []any{providerTrackID}, &retainedProviderRows},
		{`SELECT count(*) FROM provider_recording WHERE id=?`, []any{providerRecordingID}, &retainedProviderRecording},
	} {
		if err := db.NewRaw(check.query, check.args...).Scan(ctx, check.destination); err != nil {
			t.Fatal(err)
		}
	}
	if orphanTrackMappings != 0 || orphanRecordingMappings != 0 || orphanSources != 0 || retainedSource != 1 || retainedMediaVariant != 2 || retainedProviderRows != 1 || retainedProviderRecording != 1 {
		t.Fatalf("orphan cleanup/preservation: track mappings=%d recording mappings=%d orphan sources=%d retained source=%d media variants=%d provider track/recording entities=%d/%d", orphanTrackMappings, orphanRecordingMappings, orphanSources, retainedSource, retainedMediaVariant, retainedProviderRows, retainedProviderRecording)
	}

	publicationGuard := admitAndClaim("publication", foreignRelease, foreignPosition, false)
	if settled, err := repository.SettleLibraryDeletionPublication(ctx, publicationGuard, foreignPosition, func(context.Context, json.RawMessage) error { return nil }); err != nil || !settled {
		t.Fatalf("settle publication-only deletion: settled=%t err=%v", settled, err)
	}
	if finalized, err := repository.FinalizeLibraryDeletion(ctx, publicationGuard); err != nil || !finalized {
		t.Fatalf("finalize publication-only deletion: finalized=%t err=%v", finalized, err)
	}
	var retainedPositions, retainedTrackArtists, retainedReleaseArtists, retainedCollectionLinks int
	for query, destination := range map[string]*int{
		`SELECT count(*) FROM release_track WHERE id=? AND release_id=? AND track_id=?`:  &retainedPositions,
		`SELECT count(*) FROM track_artist WHERE track_id=? AND artist_id=?`:             &retainedTrackArtists,
		`SELECT count(*) FROM release_artist WHERE release_id=? AND artist_id=?`:         &retainedReleaseArtists,
		`SELECT count(*) FROM collection_release WHERE collection_id=? AND release_id=?`: &retainedCollectionLinks,
	} {
		var args []any
		switch query {
		case `SELECT count(*) FROM release_track WHERE id=? AND release_id=? AND track_id=?`:
			args = []any{foreignPosition, foreignRelease, foreignTrack}
		case `SELECT count(*) FROM track_artist WHERE track_id=? AND artist_id=?`:
			args = []any{foreignTrack, artistID}
		case `SELECT count(*) FROM release_artist WHERE release_id=? AND artist_id=?`:
			args = []any{foreignRelease, artistID}
		case `SELECT count(*) FROM collection_release WHERE collection_id=? AND release_id=?`:
			args = []any{collectionID, foreignRelease}
		}
		if err := db.NewRaw(query, args...).Scan(ctx, destination); err != nil {
			t.Fatal(err)
		}
	}
	if retainedPositions != 1 || retainedTrackArtists != 1 || retainedReleaseArtists != 1 || retainedCollectionLinks != 1 {
		t.Fatalf("publication-only deletion removed library associations: position=%d track_artist=%d release_artist=%d collection_release=%d",
			retainedPositions, retainedTrackArtists, retainedReleaseArtists, retainedCollectionLinks)
	}

	artistTargetRelease, artistTargetPosition, artistTargetPosition2 := uuid.New(), uuid.New(), uuid.New()
	artistTargetGeneration, artistTargetGeneration2 := uuid.New(), uuid.New()
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for _, query := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO local_release(id,title,primary_artist_id) VALUES (?,'artist deletion target',?)`, []any{artistTargetRelease, artistID}},
			{`INSERT INTO release_track(id,release_id,position,track_id,recording_id) VALUES (?,?,0,?,?)`, []any{artistTargetPosition, artistTargetRelease, foreignTrack, sharedRecording}},
			{`INSERT INTO release_track(id,release_id,position,track_id,recording_id) VALUES (?,?,1,?,?)`, []any{artistTargetPosition2, artistTargetRelease, foreignTrack, sharedRecording}},
			{`INSERT INTO release_artist(release_id,position,artist_id,name) VALUES (?,0,?,'retained artist')`, []any{artistTargetRelease, artistID}},
			{`INSERT INTO release_artist(release_id,position,artist_id,name) VALUES (?,1,?,'coartist')`, []any{artistTargetRelease, foreignPrimaryArtistID}},
		} {
			if _, err := tx.ExecContext(ctx, query.sql, query.args...); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed artist deletion target: %v", err)
	}
	artistPreview, err := repository.PreviewLibraryDeletion(ctx, "artist", artistID, uuid.Nil)
	if err != nil {
		t.Fatalf("preview artist deletion: %v", err)
	}
	if !slices.Equal(artistPreview.Scope.ArtistReleaseIDs, []uuid.UUID{artistTargetRelease}) ||
		!slices.Equal(artistPreview.Scope.WarningReleaseIDs, []uuid.UUID{artistTargetRelease}) ||
		len(artistPreview.Scope.PublicationIDs) != 2 || !slices.Contains(artistPreview.Scope.PublicationIDs, artistTargetPosition) ||
		!slices.Contains(artistPreview.Scope.PublicationIDs, artistTargetPosition2) ||
		len(artistPreview.RecordingIDs) != 0 {
		t.Fatalf("artist preview target/warning/position/recordings = %v/%v/%v/%v", artistPreview.Scope.ArtistReleaseIDs,
			artistPreview.Scope.WarningReleaseIDs, artistPreview.Scope.PublicationIDs, artistPreview.RecordingIDs)
	}

	staleRelease, stalePosition := uuid.New(), uuid.New()
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw(`INSERT INTO local_release(id,title,primary_artist_id) VALUES (?,'scope expansion',?)`, staleRelease, artistID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO release_track(id,release_id,position,track_id,recording_id) VALUES (?,?,0,?,?)`, stalePosition, staleRelease, foreignTrack, sharedRecording).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewRaw(`INSERT INTO release_artist(release_id,position,artist_id,name) VALUES (?,0,?,'retained artist')`, staleRelease, artistID).Exec(ctx)
		return err
	}); err != nil {
		t.Fatalf("seed post-preview artist release: %v", err)
	}
	staleHash := sha256.Sum256([]byte(artistPreview.ScopeDigest + uuid.NewString()))
	staleConfirmation := hex.EncodeToString(staleHash[:])
	staleOperationID, err := LibraryDeletionOperationID(staleConfirmation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.AdmitLibraryDeletionOperation(ctx, staleOperationID, staleConfirmation, time.Now().Add(time.Minute), artistPreview, nil, client, nil); !errors.Is(err, ErrLibraryDeletionConflict) {
		t.Fatalf("admit stale artist scope = %v, want scope conflict", err)
	}
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw(`DELETE FROM release_artist WHERE release_id=?`, staleRelease).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`DELETE FROM release_track WHERE id=?`, stalePosition).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`DELETE FROM release_medium WHERE release_id=?`, staleRelease).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewRaw(`DELETE FROM local_release WHERE id=?`, staleRelease).Exec(ctx)
		return err
	}); err != nil {
		t.Fatalf("remove post-preview artist release: %v", err)
	}
	artistRootClaim, artistFileClaim, artistFileClaim2 := uuid.New(), uuid.New(), uuid.New()
	artistRoot := "artist-root"
	insertNamespaceClaim(artistRootClaim, artistRoot, artistRoot, "dir", nil, &artistRoot)
	insertReservation(artistRootClaim, artistTargetPosition, artistTargetGeneration)
	insertReservation(artistRootClaim, artistTargetPosition2, artistTargetGeneration2)
	insertNamespaceClaim(artistFileClaim, "artist-root/one.flac", "artist-root/one.flac", "file", &artistTargetPosition, nil)
	insertReservation(artistFileClaim, artistTargetPosition, artistTargetGeneration)
	insertNamespaceClaim(artistFileClaim2, "artist-root/two.flac", "artist-root/two.flac", "file", &artistTargetPosition2, nil)
	insertReservation(artistFileClaim2, artistTargetPosition2, artistTargetGeneration2)
	for _, publication := range []struct {
		positionID uuid.UUID
		generation uuid.UUID
		path       string
		bytes      int
	}{
		{artistTargetPosition, artistTargetGeneration, "artist-root/one.flac", 13},
		{artistTargetPosition2, artistTargetGeneration2, "artist-root/two.flac", 14},
	} {
		if _, err := db.NewRaw(`INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at)
			VALUES (?,?,?,?::text,jsonb_build_object('size',?), '{}','{}','{}',1,now())`, publication.positionID, epoch,
			publication.generation, publication.path, publication.bytes).Exec(ctx); err != nil {
			t.Fatalf("seed artist CURRENT publication %s: %v", publication.positionID, err)
		}
	}
	artistGuard := admitAndClaim("artist", artistID, uuid.Nil, false)
	if settled, err := repository.SettleLibraryDeletionPublication(ctx, artistGuard, artistTargetPosition, func(context.Context, json.RawMessage) error { return nil }); err != nil || !settled {
		t.Fatalf("settle first artist CURRENT publication: settled=%t err=%v", settled, err)
	}
	failed, err := repository.FailLibraryDeletionDelivery(ctx, artistGuard, "injected artist deletion interruption")
	if err != nil || !failed {
		t.Fatalf("fail partially settled artist deletion: failed=%t err=%v", failed, err)
	}
	failedOperation, err := repository.GetOperation(ctx, artistGuard.OperationID)
	if err != nil {
		t.Fatalf("read failed artist deletion: %v", err)
	}
	var failedSnapshot LibraryDeletionOperationSnapshot
	if err := decodeStrictJSON(failedOperation.InputSnapshot, &failedSnapshot); err != nil || !slices.Equal(failedSnapshot.ResidualPublicationIDs, []uuid.UUID{artistTargetPosition2}) {
		t.Fatalf("failed artist deletion residual = %v (decode error %v), want second position only", failedSnapshot.ResidualPublicationIDs, err)
	}
	if _, err := db.NewRaw(`UPDATE river_job SET state='completed',finalized_at=now() WHERE id=? AND state='running'`, artistGuard.RiverJobID).Exec(ctx); err != nil {
		t.Fatalf("finalize failed artist River delivery: %v", err)
	}
	retriedArtistOperation, err := repository.RetryLibraryDeletionOperationAndEnqueue(ctx, artistGuard.OperationID, client)
	if err != nil {
		t.Fatalf("retry partially settled artist deletion: %v", err)
	}
	var retriedSnapshot LibraryDeletionOperationSnapshot
	if err := decodeStrictJSON(retriedArtistOperation.InputSnapshot, &retriedSnapshot); err != nil || !slices.Equal(retriedSnapshot.ResidualPublicationIDs, []uuid.UUID{artistTargetPosition2}) {
		t.Fatalf("retried artist deletion residual = %v (decode error %v), want second position only", retriedSnapshot.ResidualPublicationIDs, err)
	}
	if _, err := db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, *retriedArtistOperation.RiverJobID).Exec(ctx); err != nil {
		t.Fatalf("start retried artist River delivery: %v", err)
	}
	retriedArtistGuard := LibraryDeletionDeliveryGuard{
		OperationID: retriedArtistOperation.ID, ApplicationAttempt: retriedArtistOperation.Attempt, RiverJobID: *retriedArtistOperation.RiverJobID,
		RiverAttempt: 1, ClaimUUID: uuid.New(), BootUUID: uuid.New(), DeliveryToken: uuid.NewString(),
	}
	if outcome, err := repository.ClaimLibraryDeletionDelivery(ctx, retriedArtistGuard); err != nil || outcome != LibraryDeletionDeliveryAcquired {
		t.Fatalf("claim retried artist deletion: outcome=%q err=%v", outcome, err)
	}
	if settled, err := repository.SettleLibraryDeletionPublication(ctx, retriedArtistGuard, artistTargetPosition2, func(context.Context, json.RawMessage) error { return nil }); err != nil || !settled {
		t.Fatalf("settle remaining artist CURRENT publication: settled=%t err=%v", settled, err)
	}
	if finalized, err := repository.FinalizeLibraryDeletion(ctx, retriedArtistGuard); err != nil || !finalized {
		t.Fatalf("finalize artist deletion: finalized=%t err=%v", finalized, err)
	}
	var artistVisible, artistExists bool
	var artistTargetReleases, retainedForeignReleases, retainedForeignPositions int
	if err := db.NewRaw(`SELECT catalog_visible FROM local_artist WHERE id=?`, artistID).Scan(ctx, &artistVisible); err != nil {
		t.Fatalf("read retained technical artist: %v", err)
	}
	if err := db.NewRaw(`SELECT EXISTS(SELECT 1 FROM local_artist WHERE id=?)`, artistID).Scan(ctx, &artistExists); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM local_release WHERE id=?`, artistTargetRelease).Scan(ctx, &artistTargetReleases); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM local_release WHERE id=? AND primary_artist_id=?`, foreignRelease, foreignPrimaryArtistID).Scan(ctx, &retainedForeignReleases); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM release_track WHERE id=? AND release_id=? AND recording_id=?`, foreignPosition, foreignRelease, sharedRecording).Scan(ctx, &retainedForeignPositions); err != nil {
		t.Fatal(err)
	}
	if !artistExists || artistVisible || artistTargetReleases != 0 || retainedForeignReleases != 1 || retainedForeignPositions != 1 {
		t.Fatalf("artist deletion result: exists=%t visible=%t target releases=%d retained foreign release/position=%d/%d",
			artistExists, artistVisible, artistTargetReleases, retainedForeignReleases, retainedForeignPositions)
	}

	emptyArtist := uuid.New()
	if _, err := db.NewRaw(`INSERT INTO local_artist(id,name) VALUES (?,'unreferenced artist')`, emptyArtist).Exec(ctx); err != nil {
		t.Fatalf("seed unreferenced artist: %v", err)
	}
	emptyPreview, err := repository.PreviewLibraryDeletion(ctx, "artist", emptyArtist, uuid.Nil)
	if err != nil {
		t.Fatalf("preview unreferenced artist deletion: %v", err)
	}
	if emptyPreview.Scope.ArtistReleaseIDs == nil || emptyPreview.Scope.ArtistReferenceReleaseIDs == nil || emptyPreview.Scope.WarningReleaseIDs == nil ||
		len(emptyPreview.Scope.ArtistReleaseIDs) != 0 || len(emptyPreview.Scope.ArtistReferenceReleaseIDs) != 0 || len(emptyPreview.Scope.WarningReleaseIDs) != 0 {
		t.Fatalf("unreferenced artist scope arrays = targets:%v references:%v warnings:%v, want present empty arrays",
			emptyPreview.Scope.ArtistReleaseIDs, emptyPreview.Scope.ArtistReferenceReleaseIDs, emptyPreview.Scope.WarningReleaseIDs)
	}
	emptyArtistGuard := admitAndClaim("artist", emptyArtist, uuid.Nil, false)
	if finalized, err := repository.FinalizeLibraryDeletion(ctx, emptyArtistGuard); err != nil || !finalized {
		t.Fatalf("finalize unreferenced artist deletion: finalized=%t err=%v", finalized, err)
	}
}
