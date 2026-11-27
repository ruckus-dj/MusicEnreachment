//go:build integration

package persistence_test

import (
	"bytes"
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestCoverArtArchiveCacheWithPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	repository := persistence.NewLibraryRepository(db)
	ctx := context.Background()
	mbid := uuid.NewString()

	state, err := repository.ReadCoverArtArchiveRelease(ctx, mbid)
	if err != nil {
		t.Fatal(err)
	}
	if state.ReleaseMBID != mbid || state.FrontStatus != persistence.CoverArtArchiveUnchecked ||
		state.GalleryStatus != persistence.CoverArtArchiveUnchecked || len(state.Images) != 0 {
		t.Fatalf("initial cache state = %#v", state)
	}
	front, err := repository.ReadCoverArtArchiveFront(ctx, mbid)
	if err != nil || front.Status != persistence.CoverArtArchiveUnchecked || front.Data != nil {
		t.Fatalf("initial front = %#v, %v", front, err)
	}

	images := []persistence.CoverArtArchiveImage{
		{ID: "2", Front: true, Types: []string{"Front"}, Comment: "front", ThumbnailURL: "https://example.test/2-250.jpg"},
		{ID: "10", Back: true, Types: []string{"Back"}, Comment: "back", ThumbnailURL: "https://example.test/10-250.jpg"},
	}
	if err := repository.SaveCoverArtArchiveGalleryCandidates(ctx, mbid, images); err != nil {
		t.Fatal(err)
	}
	state, err = repository.ReadCoverArtArchiveRelease(ctx, mbid)
	if err != nil {
		t.Fatal(err)
	}
	if state.GalleryStatus != persistence.CoverArtArchiveUnchecked || state.GalleryCheckedAt != nil ||
		len(state.Images) != 2 || state.Images[0].ID != "2" || state.Images[1].ID != "10" ||
		!state.Images[0].Front || state.Images[0].Cached || state.Images[0].Types[0] != "Front" {
		t.Fatalf("gallery candidates = %#v", state)
	}
	if state.FrontStatus != persistence.CoverArtArchiveUnchecked {
		t.Fatalf("gallery candidate write changed front status: %#v", state)
	}

	imageData := []byte("cached-image-bytes")
	if err := repository.UpsertCoverArtArchiveImage(ctx, mbid, "2", imageData, "image/png"); err != nil {
		t.Fatal(err)
	}
	if err := repository.UpsertCoverArtArchiveImage(ctx, mbid, "10", []byte("back-image-bytes"), "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	cached, err := repository.ReadCoverArtArchiveImage(ctx, mbid, "2")
	if err != nil {
		t.Fatal(err)
	}
	if !cached.Cached || !bytes.Equal(cached.Data, imageData) || cached.MIME == nil || *cached.MIME != "image/png" || cached.SizeBytes == nil || *cached.SizeBytes != int64(len(imageData)) {
		t.Fatalf("cached image = %#v", cached)
	}
	if err := repository.CompleteCoverArtArchiveGallery(ctx, mbid); err != nil {
		t.Fatal(err)
	}
	state, err = repository.ReadCoverArtArchiveRelease(ctx, mbid)
	if err != nil {
		t.Fatal(err)
	}
	if state.GalleryStatus != persistence.CoverArtArchiveGalleryComplete || state.GalleryCheckedAt == nil {
		t.Fatalf("complete gallery status = %#v", state)
	}

	// Repeating the same listing is idempotent and must not erase cached bytes.
	if err := repository.SaveCoverArtArchiveGalleryCandidates(ctx, mbid, images); err != nil {
		t.Fatal(err)
	}
	again, err := repository.ReadCoverArtArchiveImage(ctx, mbid, "2")
	if err != nil || !bytes.Equal(again.Data, imageData) {
		t.Fatalf("idempotent gallery write lost image bytes: %#v, %v", again, err)
	}
	relisted, err := repository.ReadCoverArtArchiveRelease(ctx, mbid)
	if err != nil || relisted.GalleryStatus != persistence.CoverArtArchiveUnchecked || len(relisted.Images) != 2 {
		t.Fatalf("refreshed candidates status = %#v, %v", relisted, err)
	}
	if err := repository.CompleteCoverArtArchiveGallery(ctx, mbid); err != nil {
		t.Fatal(err)
	}

	if err := repository.RecordCoverArtArchiveGalleryError(ctx, mbid, "provider timeout"); err != nil {
		t.Fatal(err)
	}
	failed, err := repository.ReadCoverArtArchiveRelease(ctx, mbid)
	if err != nil || failed.GalleryStatus != persistence.CoverArtArchiveGalleryComplete ||
		failed.GalleryError == nil || *failed.GalleryError != "provider timeout" || len(failed.Images) != 2 {
		t.Fatalf("gallery error changed usable state: %#v, %v", failed, err)
	}

	frontData := []byte("cached-front-bytes")
	if err := repository.SaveCoverArtArchiveFront(ctx, mbid, "2", frontData, "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	front, err = repository.ReadCoverArtArchiveFront(ctx, mbid)
	if err != nil || front.Status != persistence.CoverArtArchiveFrontPresent || !bytes.Equal(front.Data, frontData) || front.ImageID == nil || *front.ImageID != "2" {
		t.Fatalf("cached front = %#v, %v", front, err)
	}
	if err := repository.RecordCoverArtArchiveFrontError(ctx, mbid, "temporary failure"); err != nil {
		t.Fatal(err)
	}
	front, err = repository.ReadCoverArtArchiveFront(ctx, mbid)
	if err != nil || front.Status != persistence.CoverArtArchiveFrontPresent || !bytes.Equal(front.Data, frontData) || front.LastError == nil {
		t.Fatalf("front error erased usable bytes: %#v, %v", front, err)
	}
	if err := repository.MarkCoverArtArchiveFrontAbsent(ctx, mbid); err != nil {
		t.Fatal(err)
	}
	front, err = repository.ReadCoverArtArchiveFront(ctx, mbid)
	if err != nil || front.Status != persistence.CoverArtArchiveFrontAbsent || front.Data != nil || front.CheckedAt == nil {
		t.Fatalf("absent front = %#v, %v", front, err)
	}
	if err := repository.RecordCoverArtArchiveFrontError(ctx, mbid, "later failure"); err != nil {
		t.Fatal(err)
	}
	front, err = repository.ReadCoverArtArchiveFront(ctx, mbid)
	if err != nil || front.Status != persistence.CoverArtArchiveFrontAbsent || front.LastError == nil {
		t.Fatalf("front error changed definitive absence: %#v, %v", front, err)
	}

	if err := repository.RecordCoverArtArchiveImageError(ctx, mbid, "2", "download failed"); err != nil {
		t.Fatal(err)
	}
	cached, err = repository.ReadCoverArtArchiveImage(ctx, mbid, "2")
	if err != nil || !bytes.Equal(cached.Data, imageData) || cached.DownloadError == nil {
		t.Fatalf("image error erased cached bytes: %#v, %v", cached, err)
	}

	// A definitive empty listing hides previous entries but keeps their bytes.
	if err := repository.SaveCoverArtArchiveGallery(ctx, mbid, nil); err != nil {
		t.Fatal(err)
	}
	empty, err := repository.ReadCoverArtArchiveRelease(ctx, mbid)
	if err != nil || empty.GalleryStatus != persistence.CoverArtArchiveGalleryComplete || len(empty.Images) != 0 || empty.GalleryError != nil {
		t.Fatalf("empty complete gallery = %#v, %v", empty, err)
	}
	unlisted, err := repository.ReadCoverArtArchiveImage(ctx, mbid, "2")
	if err != nil || !bytes.Equal(unlisted.Data, imageData) || unlisted.Listed {
		t.Fatalf("unlisted image bytes = %#v, %v", unlisted, err)
	}
}

func TestCoverArtArchiveValidationWithPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	repository := persistence.NewLibraryRepository(db)
	ctx := context.Background()
	mbid := uuid.NewString()

	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "invalid mbid", err: repository.SaveCoverArtArchiveGallery(ctx, "not-a-mbid", nil)},
		{name: "invalid image id", err: repository.SaveCoverArtArchiveGallery(ctx, mbid, []persistence.CoverArtArchiveImage{{ID: "0"}})},
		{name: "duplicate image id", err: repository.SaveCoverArtArchiveGallery(ctx, mbid, []persistence.CoverArtArchiveImage{{ID: "1"}, {ID: "1"}})},
		{name: "invalid front mime", err: repository.SaveCoverArtArchiveFront(ctx, mbid, "", []byte("image"), "image/gif")},
		{name: "empty gallery error", err: repository.RecordCoverArtArchiveGalleryError(ctx, mbid, "  ")},
		{name: "complete without candidates", err: repository.CompleteCoverArtArchiveGallery(ctx, mbid)},
	} {
		if test.err == nil {
			t.Errorf("%s was accepted", test.name)
		}
	}
	if err := repository.SaveCoverArtArchiveGalleryCandidates(ctx, mbid, []persistence.CoverArtArchiveImage{{ID: "1"}}); err != nil {
		t.Fatal(err)
	}
	if err := repository.CompleteCoverArtArchiveGallery(ctx, mbid); err == nil {
		t.Fatal("gallery completed with an undownloaded listed image")
	}
	state, err := repository.ReadCoverArtArchiveRelease(ctx, mbid)
	if err != nil || state.GalleryStatus != persistence.CoverArtArchiveUnchecked || state.GalleryCheckedAt != nil {
		t.Fatalf("failed gallery completion changed status: %#v, %v", state, err)
	}
	if err := repository.UpsertCoverArtArchiveImage(ctx, mbid, "1", []byte("image"), "image/png"); err != nil {
		t.Fatal(err)
	}
	if err := repository.CompleteCoverArtArchiveGallery(ctx, mbid); err != nil {
		t.Fatal(err)
	}
}

func TestReadLibraryArtistCoverArtReleaseMBIDsWithPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	repository := persistence.NewLibraryRepository(db)
	ctx := context.Background()

	configIdentity := "musicbrainz-config-1"
	artistMBID := uuid.NewString()
	providerID := uuid.New()
	sourceID := uuid.New()
	providerArtistID := uuid.New()
	localArtistID := uuid.New()
	otherLocalArtistID := uuid.New()
	providerDiscographyRelease := uuid.NewString()
	providerOtherArtistRelease := uuid.NewString()
	providerInvalidRelease := "not-a-release-mbid"
	localReleaseMBID := uuid.NewString()
	localNoSourceMBID := uuid.NewString()
	localOtherArtistMBID := uuid.NewString()

	if _, err := db.NewRaw(`INSERT INTO app_setting(setting_name,setting_value) VALUES ('musicbrainz_config_identity', ?)`, configIdentity).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO provider(id,code) VALUES (?, 'musicbrainz')`, providerID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO provider_source(id,provider_id,namespace,endpoint,configuration_identity) VALUES (?,?,'musicbrainz','https://musicbrainz.test',?)`,
		sourceID, providerID, configIdentity).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO provider_artist(id,provider_id,provider_key,name) VALUES (?,?,?,'Discography Artist')`,
		providerArtistID, providerID, artistMBID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO local_artist(id,name) VALUES (?, 'Discography Artist'), (?, 'Other Artist')`,
		localArtistID, otherLocalArtistID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO provider_local_artist(provider_id,provider_artist_id,local_artist_id,method) VALUES (?,?,?,'manual')`,
		providerID, providerArtistID, localArtistID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO provider_artist_discography(provider_source_id,provider_id,configuration_identity,artist_mbid,release_count,completed_at)
		VALUES (?,?,?,?,2,now())`, sourceID, providerID, configIdentity, artistMBID).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	credit := `{"artist-credit":[{"artist":{"id":"` + artistMBID + `"}}]}`
	evidence := `{"artist-credit":{"authority":1,"fetch_order":1}}`
	otherCredit := `{"artist-credit":[{"artist":{"id":"` + uuid.NewString() + `"}}]}`
	for _, entity := range []struct {
		key      string
		fields   string
		evidence string
	}{
		{providerDiscographyRelease, credit, evidence},
		{providerOtherArtistRelease, otherCredit, evidence},
		{providerInvalidRelease, credit, evidence},
	} {
		if _, err := db.NewRaw(`INSERT INTO provider_source_entity(provider_source_id,provider_id,entity_kind,provider_key,fields,field_evidence,raw_source,authority,fetch_order)
			VALUES (?,?,'release',?,?::jsonb,?::jsonb,'{}'::jsonb,1,1)`,
			sourceID, providerID, entity.key, entity.fields, entity.evidence).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}

	insertLocalRelease := func(localReleaseID uuid.UUID, releaseMBID string, artistID uuid.UUID, withSource bool) {
		t.Helper()
		err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			providerReleaseID := uuid.New()
			if _, err := tx.NewRaw(`INSERT INTO local_release(id,title) VALUES (?, 'Release')`, localReleaseID).Exec(ctx); err != nil {
				return err
			}
			if _, err := tx.NewRaw(`INSERT INTO release_artist(release_id,position,artist_id) VALUES (?,0,?)`, localReleaseID, artistID).Exec(ctx); err != nil {
				return err
			}
			if _, err := tx.NewRaw(`INSERT INTO provider_release(id,provider_id,provider_key,title) VALUES (?,?,?,'Release')`,
				providerReleaseID, providerID, releaseMBID).Exec(ctx); err != nil {
				return err
			}
			if _, err := tx.NewRaw(`INSERT INTO provider_local_release(provider_id,provider_release_id,local_release_id,method) VALUES (?,?,?,'manual')`,
				providerID, providerReleaseID, localReleaseID).Exec(ctx); err != nil {
				return err
			}
			if !withSource {
				return nil
			}
			trackID, variantID := uuid.New(), uuid.New()
			if _, err := tx.NewRaw(`INSERT INTO local_track(id,title) VALUES (?, 'Track')`, trackID).Exec(ctx); err != nil {
				return err
			}
			if _, err := tx.NewRaw(`INSERT INTO release_track(id,release_id,track_id,medium,position,displayed_number) VALUES (?,?,?,0,0,'A1')`,
				uuid.New(), localReleaseID, trackID).Exec(ctx); err != nil {
				return err
			}
			if _, err := tx.NewRaw(`INSERT INTO media_variant(id,size_bytes) VALUES (?,1)`, variantID).Exec(ctx); err != nil {
				return err
			}
			if _, err := tx.NewRaw(`INSERT INTO track_source(id,track_id,media_variant_id,selected,compatible,decision_method) VALUES (?,?,?,false,true,'manual')`,
				uuid.New(), trackID, variantID).Exec(ctx); err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	insertLocalRelease(uuid.MustParse(localReleaseMBID), localReleaseMBID, localArtistID, true)
	noSourceID := uuid.MustParse(localNoSourceMBID)
	insertLocalRelease(noSourceID, localNoSourceMBID, localArtistID, false)
	otherArtistID := uuid.MustParse(localOtherArtistMBID)
	insertLocalRelease(otherArtistID, localOtherArtistMBID, otherLocalArtistID, true)

	got, err := repository.ReadLibraryArtistCoverArtReleaseMBIDs(ctx, localArtistID)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{providerDiscographyRelease, localReleaseMBID}
	slices.Sort(want)
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("cover art projection = %#v, want %#v", got, want)
	}
	for mbid, expected := range map[string]bool{
		localReleaseMBID:           true,
		localNoSourceMBID:          false,
		localOtherArtistMBID:       true,
		providerDiscographyRelease: false,
	} {
		eligible, err := repository.CoverArtArchiveGalleryEligible(ctx, mbid)
		if err != nil || eligible != expected {
			t.Fatalf("gallery eligibility for %s = %t, %v; want %t", mbid, eligible, err, expected)
		}
	}

	// A stale/incomplete discography marker does not authorize provider
	// releases, but a source-linked local release remains eligible.
	if _, err := db.NewRaw(`DELETE FROM provider_artist_discography WHERE provider_source_id=?`, sourceID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	got, err = repository.ReadLibraryArtistCoverArtReleaseMBIDs(ctx, localArtistID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != localReleaseMBID {
		t.Fatalf("projection without complete discography = %#v", got)
	}

	// A configuration change invalidates the cached discography scope.
	if _, err := db.NewRaw(`INSERT INTO provider_artist_discography(provider_source_id,provider_id,configuration_identity,artist_mbid,release_count,completed_at)
		VALUES (?,?,?,?,1,now())`, sourceID, providerID, configIdentity, artistMBID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`UPDATE app_setting SET setting_value='musicbrainz-config-2' WHERE setting_name='musicbrainz_config_identity'`).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	got, err = repository.ReadLibraryArtistCoverArtReleaseMBIDs(ctx, localArtistID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != localReleaseMBID {
		t.Fatalf("projection with changed configuration = %#v", got)
	}
}
