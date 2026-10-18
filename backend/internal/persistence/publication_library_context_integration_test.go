//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func TestReadLibraryPublicationContextUsesPositionMetadataAndCurrentNamespacePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, input, _, _ := prepareManualLyricsCommitFixture(t)

	const positionEnvelope = `{"version":1,"scope":"position","desired":{"synced_lyrics":{"state":"value","value":"[00:01.00]position lyrics\n","authority":"manual","provenance":{"kind":"manual"}}}}`
	// The local track carries its own valid track-scope metadata. synced_lyrics is
	// position-only, so the track decoy uses comment to prove the position read
	// ignores the track owner rather than to mirror the lyrics envelope.
	const trackMetadata = `{"local_metadata":{"version":1,"scope":"track","desired":{"comment":{"state":"value","value":"track-only comment","authority":"manual","provenance":{"kind":"manual"}}}}}`
	if _, err := f.db.ExecContext(ctx, `UPDATE release_track SET metadata=?::jsonb, metadata_revision=7 WHERE id=?`, `{"local_metadata":`+positionEnvelope+`}`, f.guard.ExpectedPositionID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE local_track SET metadata=?::jsonb WHERE id=(SELECT track_id FROM release_track WHERE id=?)`, trackMetadata, f.guard.ExpectedPositionID); err != nil {
		t.Fatal(err)
	}

	// A namespace reservation for another generation in the active epoch must
	// not leak into the manifest for the currently published generation.
	foreignGeneration := uuid.New()
	currentClaim := uuid.New()
	if _, err := f.db.ExecContext(ctx, `INSERT INTO publication_namespace_claim
		(id,epoch,key_digest,canonical_key,relative_path,node_kind,owner_position_id)
		VALUES (?,?,decode(repeat('cd',32),'hex'),?,?, 'file',?)`,
		currentClaim, f.guard.Epoch, input.Rewrite.RelativePath, input.Rewrite.RelativePath, f.guard.ExpectedPositionID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `INSERT INTO publication_namespace_reservation(namespace_claim_id,position_id,generation) VALUES (?,?,?)`,
		currentClaim, f.guard.ExpectedPositionID, input.Rewrite.CurrentGeneration); err != nil {
		t.Fatal(err)
	}
	foreignClaim := uuid.New()
	foreignKey := "foreign-generation-only"
	if _, err := f.db.ExecContext(ctx, `INSERT INTO publication_namespace_claim
		(id,epoch,key_digest,canonical_key,relative_path,node_kind,owner_position_id)
		VALUES (?,?,decode(repeat('ab',32),'hex'),?,'foreign/file.flac','file',?)`,
		foreignClaim, f.guard.Epoch, foreignKey, f.guard.ExpectedPositionID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `INSERT INTO publication_namespace_reservation(namespace_claim_id,position_id,generation) VALUES (?,?,?)`,
		foreignClaim, f.guard.ExpectedPositionID, foreignGeneration); err != nil {
		t.Fatal(err)
	}

	var releaseID uuid.UUID
	if err := f.db.NewRaw(`SELECT release_id FROM release_track WHERE id=?`, f.guard.ExpectedPositionID).Scan(ctx, &releaseID); err != nil {
		t.Fatal(err)
	}
	// The fixture seeds the replacement-generation row being read, so capture its
	// committed actual_metadata before reading the context. The pending manual
	// lyrics are only desired metadata for the candidate generation and must not
	// have reached this row yet.
	var seededMetadataJSON string
	if err := f.db.NewRaw(`SELECT actual_metadata FROM track_publication WHERE position_id=?`, f.guard.ExpectedPositionID).Scan(ctx, &seededMetadataJSON); err != nil {
		t.Fatal(err)
	}
	var seededMetadata PublicationCommitMetadata
	if err := strictMetadata(json.RawMessage(seededMetadataJSON), &seededMetadata); err != nil {
		t.Fatalf("decode seeded current metadata: %v", err)
	}
	got, err := repo.ReadLibraryPublicationContext(ctx, releaseID, f.guard.ExpectedPositionID)
	if err != nil {
		t.Fatalf("read publication context: %v", err)
	}
	if got.Metadata.Revision != 7 || got.Metadata.Envelope.Scope != "position" {
		t.Fatalf("position metadata owner/revision = %q/%d", got.Metadata.Envelope.Scope, got.Metadata.Revision)
	}
	lyrics := got.Metadata.Envelope.Desired["synced_lyrics"]
	if lyrics.Value != "[00:01.00]position lyrics\n" {
		t.Fatalf("position lyrics = %#v", lyrics.Value)
	}
	if got.Current == nil || got.Current.Record.Generation != input.Rewrite.CurrentGeneration {
		t.Fatalf("current publication = %#v", got.Current)
	}
	if !reflect.DeepEqual(got.Current.Metadata, seededMetadata) {
		t.Fatalf("current publication metadata = %#v, want seeded %#v", got.Current.Metadata, seededMetadata)
	}
	// The candidate's desired manual lyrics are still pending and must not be
	// observable as committed current metadata before the commit.
	for _, value := range got.Current.Metadata.Tags["LYRICS"] {
		if value == input.ManualLyrics.Value {
			t.Fatalf("uncommitted desired lyrics leaked into current publication metadata: %#v", got.Current.Metadata)
		}
	}
	if len(got.NamespaceManifest) == 0 {
		t.Fatal("current-generation namespace manifest is empty")
	}
	for _, node := range got.NamespaceManifest {
		if node.PositionID == nil || *node.PositionID != f.guard.ExpectedPositionID || node.Generation == nil || *node.Generation != got.Current.Record.Generation {
			t.Fatalf("namespace manifest contains foreign owner: %+v", node)
		}
		if node.Claim.CanonicalKey == foreignKey {
			t.Fatalf("namespace manifest contains foreign generation claim: %+v", node)
		}
	}
}

func TestReadLibraryPublicationContextRejectsWrongReleaseMembershipPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, _, _, _ := prepareManualLyricsCommitFixture(t)
	if _, err := repo.ReadLibraryPublicationContext(ctx, uuid.New(), f.guard.ExpectedPositionID); !errors.Is(err, ErrPublicationPositionNotFound) {
		t.Fatalf("wrong release membership error = %v, want %v", err, ErrPublicationPositionNotFound)
	}

	// A position in another release is also not addressable through this context.
	if _, err := repo.ReadLibraryPublicationContext(ctx, uuid.New(), uuid.New()); !errors.Is(err, ErrPublicationPositionNotFound) {
		t.Fatalf("missing position error = %v, want %v", err, ErrPublicationPositionNotFound)
	}
}

func TestReadLibraryPublicationContextWithoutCurrentPublicationPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newPublicationExecutionFixture(t)
	// The fixture seeds an active publication operation. Terminate it in the same
	// transaction that removes its attempt so the deferred publication-owner
	// constraint never observes an active operation without a matching attempt.
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE operation SET state='succeeded',stage='finished',finished_at=now() WHERE id=?`, f.guard.OperationID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM track_publication_attempt WHERE operation_id=?`, f.guard.OperationID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	repo := NewSetupManagerRepository(f.db)
	var releaseID uuid.UUID
	if err := f.db.NewRaw(`SELECT release_id FROM release_track WHERE id=?`, f.guard.ExpectedPositionID).Scan(ctx, &releaseID); err != nil {
		t.Fatal(err)
	}
	got, err := repo.ReadLibraryPublicationContext(ctx, releaseID, f.guard.ExpectedPositionID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Current != nil || got.ActiveAttempt != nil || len(got.NamespaceManifest) != 0 {
		t.Fatalf("unexpected current publication state: current=%#v attempt=%#v manifest=%#v", got.Current, got.ActiveAttempt, got.NamespaceManifest)
	}
}
