//go:build integration

package persistence_test

import (
	"context"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

// A pre-upgrade accepted release cache has raw media but no normalized-track
// recording object, and its recording projection is not marked as a direct
// lookup. A new apply may recover position metadata from the accepted media,
// but it must never publish that unmarked projection as shared local recording
// title or credits.
func TestApplyCurrentMatchingEvaluationKeepsLegacyRecordingScopePositionOwnedPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigratedWithRiver(t)
	ctx := context.Background()
	fixture := newIncomingMatchFixture(t, ctx, db)
	const releaseKey = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	if _, err := db.NewRaw(`UPDATE provider_source_entity SET fields = fields #- '{normalized_tracks,0,recording}'
		WHERE provider_source_id=? AND provider_id=? AND entity_kind='release' AND provider_key=?`,
		fixture.source, fixture.provider, releaseKey).Exec(ctx); err != nil {
		t.Fatalf("emulate pre-upgrade accepted release cache: %v", err)
	}
	// Before the recording-scope split the release-embedded recording was
	// persisted as a provider-wide projection without a direct-lookup marker.
	const recordingKey = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	if _, err := db.NewRaw(`INSERT INTO provider_source_entity
		(provider_source_id,provider_id,entity_kind,provider_key,name,fields,field_evidence,raw_source,authority,complete,fetch_order)
		VALUES (?,?,'recording',?,'Fixture Track',
		'{"title":"Fixture Track","artist-credit":[{"name":"Recording Credit","joinphrase":" feat.","artist":{"id":"ffffffff-ffff-4fff-8fff-ffffffffffff","name":"Canonical Recording Artist"}}]}'::jsonb,
		'{"title":{"authority":3,"fetch_order":1},"artist-credit":{"authority":3,"fetch_order":1},"name":{"authority":3,"fetch_order":1}}'::jsonb,
		'{}'::jsonb,3,false,1)`, fixture.source, fixture.provider, recordingKey).Exec(ctx); err != nil {
		t.Fatalf("emulate pre-upgrade shared recording projection: %v", err)
	}
	cache := persistence.NewProviderCacheRepository(db)
	queryKeys := []string{fixture.cacheKey, "release-search:b", "recording-search:a", "recording-search:b"}
	snapshot, err := cache.GetMusicBrainzMatchingSnapshot(ctx, "https://musicbrainz.example/ws/2", "incoming-match-integration",
		fixture.releaseKeys, queryKeys)
	if err != nil {
		t.Fatalf("read emulated legacy snapshot: %v", err)
	}
	var selected *persistence.MatchingRelease
	for index := range snapshot.Releases {
		if snapshot.Releases[index].ProviderKey == releaseKey {
			selected = &snapshot.Releases[index]
		}
	}
	if selected == nil || len(selected.Tracks) != 1 {
		t.Fatalf("emulated legacy snapshot lacks selected release: %+v", snapshot.Releases)
	}
	if selected.Tracks[0].RecordingTitle != "Fixture Track" {
		t.Fatalf("legacy fixture recording projection=%q, want unmarked source title", selected.Tracks[0].RecordingTitle)
	}
	fixture.acceptedProjectionRev = snapshot.AcceptedProjectionRevision

	repository := persistence.NewIncomingMatchCandidateRepository(db)
	evaluation := fixture.evaluation([]persistence.IncomingMatchCandidate{
		fixture.candidate(fixture.memberA, .95), fixture.candidate(fixture.memberB, .95),
	})
	evaluation.Groups[0].Evidence = matchingApplyEvidence(t, fixture, fixture.trackID)
	if err := repository.Replace(ctx, evaluation); err != nil {
		t.Fatalf("persist unique legacy matching evaluation: %v", err)
	}
	header, _ := mustIncomingMatchResult(t, ctx, repository, fixture.group.ID)
	if err := repository.ApplyCurrentMatchingEvaluation(ctx, fixture.group.ID, header.EvaluationRevision); err != nil {
		t.Fatalf("apply legacy matching evaluation: %v", err)
	}

	var recordingTitle, trackTitle string
	if err := db.NewRaw(`SELECT recording.title, local_track.title
		FROM provider_local_track mapping
		JOIN local_track ON local_track.id=mapping.local_track_id
		JOIN release_track ON release_track.track_id=local_track.id
		JOIN provider_local_release release_mapping ON release_mapping.local_release_id=release_track.release_id AND release_mapping.provider_id=mapping.provider_id
		JOIN local_recording recording ON recording.id=release_track.recording_id
		WHERE release_mapping.provider_release_id=? AND mapping.provider_id=? AND mapping.provider_track_id=?`,
		fixture.releaseID, fixture.provider, fixture.trackID).Scan(ctx, &recordingTitle, &trackTitle); err != nil {
		t.Fatalf("read materialized legacy titles: %v", err)
	}
	if recordingTitle != "" {
		t.Fatalf("unmarked recording projection became shared local title %q", recordingTitle)
	}
	if trackTitle != "Fixture Track" {
		t.Fatalf("position title=%q, want accepted media title", trackTitle)
	}
	var recordingCredits, trackCredits int
	if err := db.NewRaw(`SELECT count(*) FROM recording_artist credit
		JOIN provider_local_recording mapping ON mapping.local_recording_id=credit.recording_id
		WHERE mapping.provider_id=? AND mapping.provider_recording_id=(
			SELECT recording_id FROM provider_release_track WHERE provider_id=? AND id=?)`,
		fixture.provider, fixture.provider, fixture.trackID).Scan(ctx, &recordingCredits); err != nil {
		t.Fatalf("count shared legacy recording credits: %v", err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM track_artist credit
		JOIN provider_local_track mapping ON mapping.local_track_id=credit.track_id
		WHERE mapping.provider_id=? AND mapping.provider_track_id=?`,
		fixture.provider, fixture.trackID).Scan(ctx, &trackCredits); err != nil {
		t.Fatalf("count position legacy credits: %v", err)
	}
	if recordingCredits != 0 {
		t.Fatalf("unmarked recording projection wrote %d shared credits", recordingCredits)
	}
	if trackCredits != 1 {
		t.Fatalf("position credits=%d, want one accepted media credit", trackCredits)
	}
}
