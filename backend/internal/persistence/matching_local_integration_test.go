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

func TestMatchingLocalConfirmIsAdditiveAndReplaysWithoutProviderUsePostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	fixture := newIncomingMatchFixture(t, ctx, db)
	// Reuse the analysis fixture's existing media identities; no provider
	// evaluation, response, or provider-to-local mapping is required.
	for _, workID := range []uuid.UUID{fixture.workA, fixture.workB} {
		var mediaID uuid.UUID
		if err := db.NewRaw(`SELECT media_variant_id FROM source_work_variant_identity WHERE work_id=?`, workID).Scan(ctx, &mediaID); err != nil {
			t.Fatal(err)
		}
	}
	intent := persistence.MatchingLocalIntent{ExpectedGroupRevision: fixture.group.Revision, ExpectedAuthorityRevision: 0,
		ReleaseTitle: "Operator release", ReleaseArtists: []persistence.MatchingLocalArtist{{Name: "Operator artist"}}, Tracks: []persistence.MatchingLocalTrack{
			{Medium: 1, Position: 1, DisplayedNumber: "A1", Title: "First", FileMemberIDs: []uuid.UUID{fixture.memberA, fixture.memberB}},
			{Medium: 1, Position: 2, DisplayedNumber: "A2", Title: "Second", FileMemberIDs: []uuid.UUID{fixture.memberA}},
		}}
	repository := persistence.NewIncomingMatchCandidateRepository(db)
	if _, err := repository.PreviewMatchingLocal(ctx, fixture.group.ID, persistence.MatchingLocalIntent{
		ExpectedGroupRevision: "stale-group-revision", ExpectedAuthorityRevision: 0, ReleaseTitle: "Stale", Tracks: intent.Tracks,
	}); !errors.Is(err, persistence.ErrMatchingLocalConflict) {
		t.Fatalf("stale group revision error = %v", err)
	}
	partial := intent
	partial.Tracks = append([]persistence.MatchingLocalTrack(nil), intent.Tracks...)
	partial.Tracks[0].FileMemberIDs = []uuid.UUID{fixture.memberA}
	partial.Tracks = partial.Tracks[:1]
	partialPreview, err := repository.PreviewMatchingLocal(ctx, fixture.group.ID, partial)
	if err != nil || len(partialPreview.OmittedFileMemberIDs) != 1 || partialPreview.OmittedFileMemberIDs[0] != fixture.memberB {
		t.Fatalf("partial preview omitted members = %v, error = %v", partialPreview.OmittedFileMemberIDs, err)
	}
	// A changed saved location fence invalidates the source snapshot without
	// requiring filesystem access; restoring it lets preview continue unchanged.
	if _, err := db.NewRaw(`UPDATE incoming_group_member_location SET size_bytes=size_bytes+1 WHERE group_id=?`, fixture.group.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.PreviewMatchingLocal(ctx, fixture.group.ID, intent); !errors.Is(err, persistence.ErrMatchingLocalConflict) {
		t.Fatalf("stale source capture error = %v", err)
	}
	if _, err := db.NewRaw(`UPDATE incoming_group_member_location SET size_bytes=size_bytes-1 WHERE group_id=?`, fixture.group.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	preview, err := repository.PreviewMatchingLocal(ctx, fixture.group.ID, intent)
	if err != nil {
		t.Fatalf("preview local release without provider evaluation: %v", err)
	}
	if len(preview.Sources) != 2 || len(preview.OmittedFileMemberIDs) != 0 || preview.PreviewDigest == "" {
		t.Fatalf("preview omitted current sources or intent: %+v", preview)
	}
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION test_matching_local_fail_track() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced local track failure'; RETURN NEW; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER test_matching_local_fail_track BEFORE INSERT ON local_track FOR EACH ROW EXECUTE FUNCTION test_matching_local_fail_track()`); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ConfirmMatchingLocal(ctx, fixture.group.ID, intent, preview.PreviewDigest, uuid.New()); err == nil {
		t.Fatal("injected local track failure unexpectedly committed")
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER test_matching_local_fail_track ON local_track`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DROP FUNCTION test_matching_local_fail_track()`); err != nil {
		t.Fatal(err)
	}
	var afterRollback int
	if err := db.NewRaw(`SELECT count(*) FROM local_release`).Scan(ctx, &afterRollback); err != nil || afterRollback != 0 {
		t.Fatalf("local releases after rollback = %d, %v", afterRollback, err)
	}
	requestID := uuid.New()
	confirmed, err := repository.ConfirmMatchingLocal(ctx, fixture.group.ID, intent, preview.PreviewDigest, requestID)
	if err != nil {
		t.Fatalf("confirm local release: %v", err)
	}
	replayed, err := repository.ConfirmMatchingLocal(ctx, fixture.group.ID, intent, preview.PreviewDigest, requestID)
	if err != nil || replayed.LocalReleaseID == nil || confirmed.LocalReleaseID == nil || *replayed.LocalReleaseID != *confirmed.LocalReleaseID {
		t.Fatalf("same-intent replay = %+v, %v", replayed, err)
	}
	changedIntent := intent
	changedIntent.ReleaseTitle = "Different intent"
	if _, err := repository.ConfirmMatchingLocal(ctx, fixture.group.ID, changedIntent, preview.PreviewDigest, requestID); !errors.Is(err, persistence.ErrMatchingLocalConflict) {
		t.Fatalf("changed request replay error = %v, want conflict", err)
	}
	if _, err := repository.ConfirmMatchingLocal(ctx, fixture.group.ID, intent, preview.PreviewDigest, uuid.New()); !errors.Is(err, persistence.ErrMatchingLocalConflict) {
		t.Fatalf("stale authority confirmation error = %v, want conflict", err)
	}
	var releaseCount, trackCount, sourceCount, providerReleaseCount int
	if err := db.NewRaw(`SELECT count(*) FROM local_release WHERE id=?`, *confirmed.LocalReleaseID).Scan(ctx, &releaseCount); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM release_track WHERE release_id=?`, *confirmed.LocalReleaseID).Scan(ctx, &trackCount); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM track_source WHERE track_id IN (SELECT track_id FROM release_track WHERE release_id=?)`, *confirmed.LocalReleaseID).Scan(ctx, &sourceCount); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM provider_local_release WHERE local_release_id=?`, *confirmed.LocalReleaseID).Scan(ctx, &providerReleaseCount); err != nil {
		t.Fatal(err)
	}
	if releaseCount != 1 || trackCount != 2 || sourceCount != 3 || providerReleaseCount != 0 {
		t.Fatalf("materialized release=%d tracks=%d sources=%d provider links=%d", releaseCount, trackCount, sourceCount, providerReleaseCount)
	}
}

func TestMatchingLocalCanSelectOnlyPureLocalReleaseAndAddSources(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	fixture := newIncomingMatchFixture(t, ctx, db)
	media := make(map[uuid.UUID]uuid.UUID)
	for _, workID := range []uuid.UUID{fixture.workA, fixture.workB} {
		var mediaID uuid.UUID
		if err := db.NewRaw(`SELECT media_variant_id FROM source_work_variant_identity WHERE work_id=?`, workID).Scan(ctx, &mediaID); err != nil {
			t.Fatal(err)
		}
		media[workID] = mediaID
	}
	releaseID, trackID, recordingID := uuid.New(), uuid.New(), uuid.New()
	extraTrackID, extraRecordingID := uuid.New(), uuid.New()
	otherMediumTrackID, otherMediumRecordingID := uuid.New(), uuid.New()
	artistID := uuid.New()
	if _, err := db.NewRaw(`INSERT INTO local_release(id,title) VALUES (?,?)`, releaseID, "Seed release").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO local_artist(id,name) VALUES (?,?)`, artistID, "Seed artist").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO release_artist(release_id,position,artist_id,name,name_join_phrase) VALUES (?,0,?,?,'')`, releaseID, artistID, "Seed artist").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO local_recording(id,title) VALUES (?,?)`, recordingID, "Recording identity").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO local_track(id,title) VALUES (?,?)`, trackID, "Seed track").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO release_track(release_id,position,track_id,recording_id,medium,displayed_number) VALUES (?,0,?,?,0,'1')`, releaseID, trackID, recordingID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO track_source(id,track_id,media_variant_id,decision_method) VALUES (?, ?, ?, 'manual')`, uuid.New(), trackID, media[fixture.workA]).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO local_track(id,title) VALUES (?,?)`, extraTrackID, "Untouched position").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO local_recording(id,title) VALUES (?,?)`, extraRecordingID, "Untouched recording").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO release_track(release_id,position,track_id,recording_id,medium,displayed_number) VALUES (?,1,?,?,0,'2')`, releaseID, extraTrackID, extraRecordingID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO track_source(id,track_id,media_variant_id,decision_method) VALUES (?, ?, ?, 'manual')`, uuid.New(), extraTrackID, media[fixture.workA]).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO local_track(id,title) VALUES (?,?)`, otherMediumTrackID, "Other medium track").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO local_recording(id,title) VALUES (?,?)`, otherMediumRecordingID, "Other medium recording").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO release_track(release_id,position,track_id,recording_id,medium,displayed_number) VALUES (?,0,?,?,1,'disc-two-original')`, releaseID, otherMediumTrackID, otherMediumRecordingID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO track_source(id,track_id,media_variant_id,decision_method) VALUES (?, ?, ?, 'manual')`, uuid.New(), otherMediumTrackID, media[fixture.workA]).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	foreignReleaseID, foreignTrackID := uuid.New(), uuid.New()
	if _, err := db.NewRaw(`INSERT INTO local_release(id,title) VALUES (?,?)`, foreignReleaseID, "Other release").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO local_track(id,title) VALUES (?,?)`, foreignTrackID, "Foreign track").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO release_track(release_id,position,track_id,medium,displayed_number) VALUES (?,0,?,0,'1')`, foreignReleaseID, foreignTrackID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	providerID, providerReleaseID, linkedLocalID := uuid.New(), uuid.New(), uuid.New()
	if _, err := db.NewRaw(`INSERT INTO provider(id,code) VALUES (?,'local-catalog-test')`, providerID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO provider_release(id,provider_id,provider_key,title) VALUES (?,?,?,'Provider')`, providerReleaseID, providerID, "provider-release").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO local_release(id,title) VALUES (?,?)`, linkedLocalID, "Linked release").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO provider_local_release(provider_id,provider_release_id,local_release_id,method) VALUES (?,?,?,'manual')`, providerID, providerReleaseID, linkedLocalID).Exec(ctx); err != nil {
		t.Fatal(err)
	}

	repository := persistence.NewIncomingMatchCandidateRepository(db)
	catalog, err := repository.ListLocalReleases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, release := range catalog.Releases {
		if release.ID == releaseID {
			found = true
			if release.Title != "Seed release" || len(release.Artists) != 1 || release.Artists[0].Name != "Seed artist" || len(release.Tracks) != 3 || release.Tracks[0].LocalTrackID == nil || *release.Tracks[0].LocalTrackID != trackID || len(release.Tracks[0].SourceMediaIDs) != 1 {
				t.Fatalf("catalog read model lost local metadata/identity: %+v", release)
			}
		}
		if release.ID == linkedLocalID {
			t.Fatal("provider-linked release leaked into pure-local catalog")
		}
	}
	if !found {
		t.Fatal("pure local release missing from catalog")
	}
	intent := persistence.MatchingLocalIntent{LocalReleaseID: &releaseID, ExpectedGroupRevision: fixture.group.Revision, ExpectedAuthorityRevision: 0,
		ReleaseTitle: "", ReleaseArtists: []persistence.MatchingLocalArtist{}, Tracks: []persistence.MatchingLocalTrack{{LocalTrackID: &trackID, Medium: 1, Position: 1, DisplayedNumber: "", Title: "", Artists: []persistence.MatchingLocalArtist{{Name: ""}}, FileMemberIDs: []uuid.UUID{fixture.memberB}}}}
	preview, err := repository.PreviewMatchingLocal(ctx, fixture.group.ID, intent)
	if err != nil {
		t.Fatalf("preview existing pure local release: %v", err)
	}
	requestID := uuid.New()
	confirmed, err := repository.ConfirmMatchingLocal(ctx, fixture.group.ID, intent, preview.PreviewDigest, requestID)
	if err != nil {
		t.Fatalf("confirm existing release addition: %v", err)
	}
	replayed, err := repository.ConfirmMatchingLocal(ctx, fixture.group.ID, intent, preview.PreviewDigest, requestID)
	if err != nil || replayed.LocalReleaseID == nil || *replayed.LocalReleaseID != releaseID {
		t.Fatalf("existing release replay = %+v, %v", replayed, err)
	}
	duplicateSourceIntent := intent
	duplicateSourceIntent.ExpectedAuthorityRevision = confirmed.AuthorityRevision
	duplicateSourcePreview, err := repository.PreviewMatchingLocal(ctx, fixture.group.ID, duplicateSourceIntent)
	if err != nil {
		t.Fatalf("preview duplicate source addition: %v", err)
	}
	confirmed, err = repository.ConfirmMatchingLocal(ctx, fixture.group.ID, duplicateSourceIntent, duplicateSourcePreview.PreviewDigest, uuid.New())
	if err != nil {
		t.Fatalf("confirm duplicate source addition: %v", err)
	}
	var title, recordingAfter string
	var positions, sources, untouchedSources, otherMediumSources, releaseArtists, trackArtists int
	if err := db.NewRaw(`SELECT title FROM local_release WHERE id=?`, releaseID).Scan(ctx, &title); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM release_track WHERE release_id=?`, releaseID).Scan(ctx, &positions); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM track_source WHERE track_id=?`, trackID).Scan(ctx, &sources); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM track_source WHERE track_id=?`, extraTrackID).Scan(ctx, &untouchedSources); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM track_source WHERE track_id=?`, otherMediumTrackID).Scan(ctx, &otherMediumSources); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM release_artist WHERE release_id=?`, releaseID).Scan(ctx, &releaseArtists); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM track_artist WHERE track_id=?`, trackID).Scan(ctx, &trackArtists); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT recording_id::text FROM release_track WHERE release_id=? AND track_id=?`, releaseID, trackID).Scan(ctx, &recordingAfter); err != nil {
		t.Fatal(err)
	}
	var otherMediumNumber, otherMediumTitle, otherMediumRecordingAfter string
	if err := db.NewRaw(`SELECT rt.displayed_number, lt.title, rt.recording_id::text FROM release_track rt JOIN local_track lt ON lt.id=rt.track_id WHERE rt.release_id=? AND rt.track_id=?`, releaseID, otherMediumTrackID).Scan(ctx, &otherMediumNumber, &otherMediumTitle, &otherMediumRecordingAfter); err != nil {
		t.Fatal(err)
	}
	if title != "" || positions != 3 || sources != 2 || untouchedSources != 1 || otherMediumSources != 1 || otherMediumNumber != "disc-two-original" || otherMediumTitle != "Other medium track" || otherMediumRecordingAfter != otherMediumRecordingID.String() || releaseArtists != 0 || trackArtists != 1 || recordingAfter != recordingID.String() {
		t.Fatalf("existing release update title=%q positions=%d sources=%d untouchedSources=%d otherMediumSources=%d otherMediumNumber=%q otherMediumTitle=%q otherMediumRecording=%s releaseArtists=%d trackArtists=%d recording=%s", title, positions, sources, untouchedSources, otherMediumSources, otherMediumNumber, otherMediumTitle, otherMediumRecordingAfter, releaseArtists, trackArtists, recordingAfter)
	}
	var emptyCredit string
	if err := db.NewRaw(`SELECT name FROM track_artist WHERE track_id=? ORDER BY position LIMIT 1`, trackID).Scan(ctx, &emptyCredit); err != nil || emptyCredit != "" {
		t.Fatalf("empty credit = %q, %v", emptyCredit, err)
	}
	for _, rejectedID := range []uuid.UUID{linkedLocalID, foreignReleaseID} {
		invalid := intent
		invalid.LocalReleaseID = &rejectedID
		if _, err := repository.PreviewMatchingLocal(ctx, fixture.group.ID, invalid); !errors.Is(err, persistence.ErrMatchingLocalConflict) {
			t.Fatalf("non-pure release %s error = %v", rejectedID, err)
		}
	}
	foreignTrackIntent := intent
	foreignTrackIntent.Tracks = append([]persistence.MatchingLocalTrack(nil), intent.Tracks...)
	foreignTrackIntent.Tracks[0].LocalTrackID = &foreignTrackID
	if _, err := repository.PreviewMatchingLocal(ctx, fixture.group.ID, foreignTrackIntent); !errors.Is(err, persistence.ErrMatchingLocalConflict) {
		t.Fatalf("foreign track error = %v", err)
	}
	currentIntent := intent
	currentIntent.ExpectedAuthorityRevision = confirmed.AuthorityRevision
	currentPreview, err := repository.PreviewMatchingLocal(ctx, fixture.group.ID, currentIntent)
	if err != nil {
		t.Fatalf("preview for local metadata fence: %v", err)
	}
	if _, err := db.NewRaw(`UPDATE local_release SET title='Concurrent edit' WHERE id=?`, releaseID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ConfirmMatchingLocal(ctx, fixture.group.ID, currentIntent, currentPreview.PreviewDigest, uuid.New()); !errors.Is(err, persistence.ErrMatchingLocalConflict) {
		t.Fatalf("concurrent local metadata change error = %v", err)
	}
}
