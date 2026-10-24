//go:build integration

package persistence

import (
	"context"
	"crypto/sha256"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestOutputResetClearsOnlyPublicationNamespaceStatePostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	initializeRiver(t, ctx, db)
	oldRoot, newRoot := nativeTestPath(t, "/music/old"), nativeTestPath(t, "/music/new")
	settings := NewSettingsRepository(db)
	if err := settings.Set(ctx, "output_directory", oldRoot); err != nil {
		t.Fatal(err)
	}
	repository := NewSetupManagerRepository(db)

	var epoch uuid.UUID
	if err := db.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &epoch); err != nil {
		t.Fatal(err)
	}
	releaseID, trackID, position, generation, claimID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO local_release(id,title) VALUES (?, 'release')`, releaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?, 'track')`, trackID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO release_track(id,release_id,position,track_id) VALUES (?,?,0,?)`, position, releaseID, trackID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at) VALUES (?, ?, ?, 'track.flac', '{}', '{}', '{}', '{}', 1, now())`, position, epoch, generation); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO position_source_authority(position_id,mode,revision,publication_epoch,publication_generation,source_location_id,media_variant_id) VALUES (?,'manual',?,?,?,?,?)`, position, uuid.New(), epoch, generation, uuid.New(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO publication_namespace_claim(id,epoch,key_digest,canonical_key,relative_path,node_kind,owner_position_id) VALUES (?, ?, decode(repeat('ab',32),'hex'), 'track.flac', 'track.flac', 'file', ?)`, claimID, epoch, position); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO publication_namespace_reservation(namespace_claim_id,position_id,generation) VALUES (?, ?, ?)`, claimID, position, generation); err != nil {
		t.Fatal(err)
	}
	obsoleteClaim, obsoleteGeneration := uuid.New(), uuid.New()
	if _, err := db.NewRaw(`INSERT INTO publication_namespace_claim(id,epoch,key_digest,canonical_key,relative_path,node_kind,owner_position_id)
		VALUES (?,?,decode(repeat('cd',32),'hex'),'obsolete.flac','obsolete.flac','file',?)`, obsoleteClaim, epoch, position).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewInsert().Model(&PublicationNamespaceReservation{NamespaceClaimID: obsoleteClaim, PositionID: position, Generation: obsoleteGeneration}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("old output"))
	var cleanupOperation *Operation
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var err error
		cleanupOperation, err = repository.AdmitObsoletePublicationOutputCleanupTx(ctx, tx, ObsoletePublicationOutputCleanupAdmission{
			Epoch: epoch, PositionID: position, Generation: obsoleteGeneration, RelativePath: "obsolete.flac", NativePath: filepath.Join(oldRoot, "obsolete.flac"), NamespaceClaimID: obsoleteClaim,
			OutputRootIdentity: PublicationDirectoryIdentity{Platform: "unix", Volume: "volume", FileID: "root"}, ParentIdentity: PublicationDirectoryIdentity{Platform: "unix", Volume: "volume", FileID: "parent"},
			FileSize: 10, ModifiedAt: time.Now().UTC(), SHA256: digest[:],
		}, obsoleteCleanupTestInserter{}, func(id uuid.UUID) river.JobArgs { return ObsoletePublicationOutputCleanupJobArgs{OperationID: id} }, nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	token, err := repository.RunOutputResetRequest(ctx, OutputResetRequest{
		ExpectedOldRoot: oldRoot, NewRoot: newRoot,
		Prepare: func(context.Context, func(OutputResetDirectory) error) error { return nil },
		Finish:  func(context.Context, OutputResetJournal) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	var got uuid.UUID
	if err := db.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &got); err != nil {
		t.Fatal(err)
	}
	if got == epoch || got != token {
		t.Fatalf("output epoch = %s, want reset token %s distinct from %s", got, token, epoch)
	}
	var publications, claims, reservations, authorities int
	for query, target := range map[string]*int{"SELECT count(*) FROM track_publication": &publications, "SELECT count(*) FROM publication_namespace_claim": &claims, "SELECT count(*) FROM publication_namespace_reservation": &reservations, "SELECT count(*) FROM position_source_authority": &authorities} {
		if err := db.NewRaw(query).Scan(ctx, target); err != nil {
			t.Fatal(err)
		}
	}
	if publications != 0 || claims != 0 || reservations != 0 || authorities != 0 {
		t.Fatalf("publication state after reset: publications=%d claims=%d reservations=%d authority=%d", publications, claims, reservations, authorities)
	}
	if row, err := repository.ReadObsoletePublicationOutputCleanup(ctx, cleanupOperation.ID); err != nil || row != nil {
		t.Fatalf("reset retained obsolete cleanup authority: %+v %v", row, err)
	}
	var jobState string
	if err := db.NewRaw(`SELECT state FROM river_job WHERE id=?`, *cleanupOperation.RiverJobID).Scan(ctx, &jobState); err != nil || jobState != "cancelled" {
		t.Fatalf("obsolete cleanup job after reset=%q %v", jobState, err)
	}
	settled, err := repository.GetOperation(ctx, cleanupOperation.ID)
	if err != nil || settled.State != "failed" || settled.SafeError == nil || settled.FinishedAt == nil {
		t.Fatalf("obsolete cleanup operation after reset=%+v %v", settled, err)
	}
	calls := 0
	if acquired, err := repository.ExecuteObsoletePublicationOutputCleanup(ctx, cleanupOperation.ID, cleanupOperation.Attempt, *cleanupOperation.RiverJobID, func(context.Context, ObsoletePublicationOutputCleanup) error { calls++; return nil }); err != nil || acquired || calls != 0 {
		t.Fatalf("reset delivery invoked filesystem: acquired=%t calls=%d err=%v", acquired, calls, err)
	}
	var retainedPositions int
	if err := db.NewRaw(`SELECT count(*) FROM release_track WHERE id=? AND release_id=? AND track_id=?`, position, releaseID, trackID).Scan(ctx, &retainedPositions); err != nil || retainedPositions != 1 {
		t.Fatalf("retained local position = %d, %v", retainedPositions, err)
	}
	newGeneration := uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at) VALUES (?, ?, ?, 'track.flac', '{}', '{}', '{}', '{}', 1, now())`, position, got, newGeneration); err != nil {
		t.Fatalf("republish after reset: %v", err)
	}
	var created bool
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var err error
		created, err = compareAndSetPositionSourceAuthorityTx(ctx, tx, nil, PositionSourceAuthority{PositionID: position, Mode: "automatic", Revision: uuid.New(), PublicationEpoch: got, PublicationGeneration: newGeneration, SourceLocationID: uuid.New(), MediaVariantID: uuid.New()})
		return err
	}); err != nil || !created {
		t.Fatalf("establish authority after reset: created=%t err=%v", created, err)
	}
}
