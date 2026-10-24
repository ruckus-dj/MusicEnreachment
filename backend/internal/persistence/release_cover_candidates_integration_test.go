//go:build integration

package persistence_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestReadReleaseCoverCandidatesWithPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	releaseID, nonMusicBrainzReleaseID := uuid.New(), uuid.New()
	providerID, providerReleaseID := uuid.New(), uuid.New()
	otherProviderID, otherProviderReleaseID := uuid.New(), uuid.New()
	otherSecondReleaseID := uuid.New()
	trackA, trackB := uuid.New(), uuid.New()
	variantA, variantB, variantC := uuid.New(), uuid.New(), uuid.New()
	resultA := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	resultB := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	winningA, winningB := uuid.New(), uuid.New()

	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO local_release(id,title) VALUES (?,?),(?,?)`, []any{releaseID, "Release", nonMusicBrainzReleaseID, "Other provider release"}},
		{`INSERT INTO provider(id,code) VALUES (?, 'musicbrainz'), (?, 'other')`, []any{providerID, otherProviderID}},
		{`INSERT INTO provider_release(id,provider_id,provider_key,title) VALUES (?,?,?,'Release'),(?,?,?,'Release')`, []any{providerReleaseID, providerID, "musicbrainz-mbid", otherProviderReleaseID, otherProviderID, "not-a-mbid"}},
		{`INSERT INTO provider_release(id,provider_id,provider_key,title) VALUES (?,?,?,'Other release')`, []any{otherSecondReleaseID, otherProviderID, "other-not-a-mbid"}},
		{`INSERT INTO provider_local_release(provider_id,provider_release_id,local_release_id,method) VALUES (?,?,?,'manual'),(?,?,?,'manual')`, []any{providerID, providerReleaseID, releaseID, otherProviderID, otherProviderReleaseID, releaseID}},
		{`INSERT INTO provider_local_release(provider_id,provider_release_id,local_release_id,method) VALUES (?,?,?,'manual')`, []any{otherProviderID, otherSecondReleaseID, nonMusicBrainzReleaseID}},
		{`INSERT INTO local_track(id,title) VALUES (?, 'Track A'), (?, 'Track B')`, []any{trackA, trackB}},
		{`INSERT INTO release_track(id,release_id,track_id,medium,position,displayed_number) VALUES (?,?,?,0,0,'A1'),(?,?,?,0,1,'A2')`, []any{uuid.New(), releaseID, trackA, uuid.New(), releaseID, trackB}},
		{`INSERT INTO media_variant(id,size_bytes) VALUES (?,1),(?,1),(?,1)`, []any{variantA, variantB, variantC}},
		{`INSERT INTO media_metadata_result(id,observed_tags,provenance,observed_at,winning_result_id,applied_operation_id,cover_observed,cover_data,cover_mime,cover_sha256,cover_size_bytes)
			VALUES (?, '{}','{}',now(),?,?,true,?,'image/png',?,?),(?, '{}','{}',now(),?,?,true,?,'image/jpeg',?,?)`,
			[]any{resultA, winningA, uuid.New(), []byte("cover-a"), make([]byte, 32), int64(len("cover-a")), resultB, winningB, uuid.New(), []byte("cover-b"), make([]byte, 32), int64(len("cover-b"))}},
		{`INSERT INTO track_source(id,track_id,media_variant_id,selected,compatible,decision_method,policy_version,source_metadata_result_id)
			VALUES (?,?,?,true,true,'automatic','1',?),(?,?,?,false,true,'manual',NULL,?),(?,?,?,false,true,'manual',NULL,?)`,
			[]any{uuid.New(), trackA, variantA, resultA, uuid.New(), trackA, variantB, resultA, uuid.New(), trackB, variantC, resultB}},
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("insert release cover candidate fixture: %v", err)
		}
	}

	got, err := persistence.NewLibraryRepository(db).ReadReleaseCoverCandidates(ctx, releaseID)
	if err != nil {
		t.Fatalf("read release cover candidates: %v", err)
	}
	if got.MusicBrainzMBID == nil || *got.MusicBrainzMBID != "musicbrainz-mbid" {
		t.Fatalf("MusicBrainz identity = %v, want provider key", got.MusicBrainzMBID)
	}
	if len(got.Candidates) != 2 || got.Candidates[0].ID != resultA || got.Candidates[1].ID != resultB {
		t.Fatalf("distinct saved candidates = %+v", got.Candidates)
	}
	if got.Candidates[0].WinningResultID != winningA || string(got.Candidates[0].Data) != "cover-a" || got.Candidates[0].MIME != "image/png" || got.Candidates[0].SizeBytes != int64(len("cover-a")) {
		t.Fatalf("first saved cover metadata = %+v", got.Candidates[0])
	}
	if got.Candidates[1].WinningResultID != winningB || string(got.Candidates[1].Data) != "cover-b" {
		t.Fatalf("unselected-track cover candidate = %+v", got.Candidates[1])
	}
	if _, err := db.ExecContext(ctx, `UPDATE media_metadata_result SET cover_data=NULL,cover_mime=NULL,
		cover_sha256=NULL,cover_size_bytes=NULL,cover_error='artwork image dimensions exceed the maximum' WHERE id=?`, resultB); err != nil {
		t.Fatal(err)
	}
	warned, err := persistence.NewLibraryRepository(db).ReadReleaseCoverCandidates(ctx, releaseID)
	if err != nil || len(warned.Candidates) != 1 || len(warned.CoverErrors) != 1 || warned.CoverErrors[0] != "artwork image dimensions exceed the maximum" {
		t.Fatalf("oversized saved observation lost its optional error: %+v %v", warned, err)
	}
	other, err := persistence.NewLibraryRepository(db).ReadReleaseCoverCandidates(ctx, nonMusicBrainzReleaseID)
	if err != nil || other.MusicBrainzMBID != nil || len(other.Candidates) != 0 {
		t.Fatalf("non-MusicBrainz release result = %+v, %v", other, err)
	}
	if _, err := persistence.NewLibraryRepository(db).ReadReleaseCoverCandidates(ctx, uuid.New()); !errors.Is(err, persistence.ErrLibraryReleaseNotFound) {
		t.Fatalf("missing release error = %v, want not found", err)
	}
}
