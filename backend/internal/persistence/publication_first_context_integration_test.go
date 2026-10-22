//go:build integration

package persistence

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestReadLibraryFirstPublicationContextPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newPublicationExecutionFixture(t)
	repo := NewSetupManagerRepository(f.db)
	var releaseID, trackID uuid.UUID
	if err := f.db.NewRaw(`SELECT rt.release_id,rt.track_id FROM release_track rt WHERE rt.id=?`, f.guard.ExpectedPositionID).Scan(ctx, &releaseID, &trackID); err != nil {
		t.Fatal(err)
	}
	const releaseMetadata = `{"local_metadata":{"version":1,"scope":"release","desired":{"genre":{"state":"value","value":["album genre"],"authority":"manual","provenance":{"kind":"manual"}},"title":{"state":"value","value":"Desired Album","authority":"manual","provenance":{"kind":"manual"}}}}}`
	const trackMetadata = `{"local_metadata":{"version":1,"scope":"track","desired":{"genre":{"state":"value","value":["track genre"],"authority":"manual","provenance":{"kind":"manual"}}}}}`
	const positionMetadata = `{"local_metadata":{"version":1,"scope":"position","desired":{"genre":{"state":"value","value":["position genre"],"authority":"manual","provenance":{"kind":"manual"}}}}}`
	for _, update := range []struct {
		query string
		args  []any
	}{
		{`UPDATE local_release SET metadata=?::jsonb WHERE id=?`, []any{releaseMetadata, releaseID}},
		{`UPDATE local_track SET metadata=?::jsonb WHERE id=?`, []any{trackMetadata, trackID}},
		{`UPDATE release_track SET metadata=?::jsonb WHERE id=?`, []any{positionMetadata, f.guard.ExpectedPositionID}},
	} {
		if _, err := f.db.ExecContext(ctx, update.query, update.args...); err != nil {
			t.Fatal(err)
		}
	}
	got, err := repo.ReadLibraryFirstPublicationContext(ctx, releaseID, f.guard.ExpectedPositionID)
	if err != nil {
		t.Fatalf("read first publication context: %v", err)
	}
	if got.Metadata.Release.Envelope.Scope != "release" || got.Metadata.Track.Envelope.Scope != "track" || got.Metadata.Position.Envelope.Scope != "position" {
		t.Fatalf("metadata scopes = %q/%q/%q", got.Metadata.Release.Envelope.Scope, got.Metadata.Track.Envelope.Scope, got.Metadata.Position.Envelope.Scope)
	}
	if got.Tags.Release.Title != "Desired Album" {
		t.Fatalf("release desired title = %q", got.Tags.Release.Title)
	}
	if len(got.Tags.Position.Desired.Desired["genre"].Value.([]any)) != 1 || got.Tags.Position.Desired.Desired["genre"].Value.([]any)[0] != "position genre" {
		t.Fatalf("position genre projection = %#v", got.Tags.Position.Desired.Desired["genre"])
	}
	if got.TemplateTokens["ALBUM_NAME"] != "Desired Album" {
		t.Fatalf("album token = %q", got.TemplateTokens["ALBUM_NAME"])
	}
	if len(got.DefaultSourceBlockers) != 1 || got.DefaultSourceBlockers[0] != "no selected source has a current inventory identity and successful probe" || len(got.Sources) != 0 {
		t.Fatalf("unexpected source state: sources=%+v default blockers=%v", got.Sources, got.DefaultSourceBlockers)
	}
	if _, err := repo.ReadLibraryFirstPublicationContext(ctx, uuid.New(), f.guard.ExpectedPositionID); err == nil {
		t.Fatal("wrong release membership accepted")
	}
}
