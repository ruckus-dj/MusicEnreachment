//go:build integration

package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	targetPosition, foreignPosition := uuid.New(), uuid.New()
	targetTrack, foreignTrack := uuid.New(), uuid.New()
	artistID, collectionID := uuid.New(), uuid.New()
	targetGeneration, foreignGeneration := uuid.New(), uuid.New()
	sharedDirectoryClaim, targetFileClaim, foreignFileClaim := uuid.New(), uuid.New(), uuid.New()
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO local_release(id,title) VALUES (?,?)`, []any{targetRelease, "deletion target"}},
		{`INSERT INTO local_release(id,title) VALUES (?,?)`, []any{foreignRelease, "foreign release"}},
		{`INSERT INTO local_track(id,title) VALUES (?,?)`, []any{targetTrack, "target track"}},
		{`INSERT INTO local_track(id,title) VALUES (?,?)`, []any{foreignTrack, "foreign track"}},
		{`INSERT INTO release_track(id,release_id,position,track_id) VALUES (?,?,0,?)`, []any{targetPosition, targetRelease, targetTrack}},
		{`INSERT INTO release_track(id,release_id,position,track_id) VALUES (?,?,0,?)`, []any{foreignPosition, foreignRelease, foreignTrack}},
		{`INSERT INTO local_artist(id,name) VALUES (?, 'retained artist')`, []any{artistID}},
		{`INSERT INTO track_artist(track_id,position,artist_id,name) VALUES (?,0,?,'retained artist')`, []any{foreignTrack, artistID}},
		{`INSERT INTO release_artist(release_id,position,artist_id,name) VALUES (?,0,?,'retained artist')`, []any{foreignRelease, artistID}},
		{`INSERT INTO collection(id,title) VALUES (?, 'retained collection')`, []any{collectionID}},
		{`INSERT INTO collection_release(collection_id,position,release_id) VALUES (?,0,?)`, []any{collectionID, foreignRelease}},
		{`INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at) VALUES (?,?,?,'root/target.flac','{"size":11}','{}','{}','{}',1,now())`, []any{targetPosition, epoch, targetGeneration}},
		{`INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at) VALUES (?,?,?,'root/foreign.flac','{"size":12}','{}','{}','{}',1,now())`, []any{foreignPosition, epoch, foreignGeneration}},
	} {
		if _, err := db.ExecContext(ctx, query.sql, query.args...); err != nil {
			t.Fatalf("seed deletion fixture: %v", err)
		}
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
		publications := make([]TrackPublication, 0, 1)
		if err := db.NewSelect().Model(&publications).Where("position_id IN (?)", bun.In(preview.Scope.PublicationIDs)).Order("position_id").Scan(ctx); err != nil {
			t.Fatalf("read deletion witness source: %v", err)
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
			if _, err := db.ExecContext(ctx, `INSERT INTO collection_release(collection_id,position,release_id) VALUES (?,1,?)`, collectionID, releaseID); err != nil {
				t.Fatalf("change release scope after admission: %v", err)
			}
			changedPreview, err := repository.PreviewLibraryDeletion(ctx, kind, releaseID, positionID)
			if err != nil {
				t.Fatalf("preview changed release scope: %v", err)
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
	var sharedClaims, foreignReservations, targetPositions int
	if err := db.NewRaw(`SELECT count(*) FROM publication_namespace_claim WHERE id=?`, sharedDirectoryClaim).Scan(ctx, &sharedClaims); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM publication_namespace_reservation WHERE namespace_claim_id=? AND position_id=? AND generation=?`, sharedDirectoryClaim, foreignPosition, foreignGeneration).Scan(ctx, &foreignReservations); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM release_track WHERE id=?`, targetPosition).Scan(ctx, &targetPositions); err != nil {
		t.Fatal(err)
	}
	if sharedClaims != 1 || foreignReservations != 1 || targetPositions != 0 {
		t.Fatalf("release deletion: shared claims=%d foreign reservations=%d target positions=%d", sharedClaims, foreignReservations, targetPositions)
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
}
