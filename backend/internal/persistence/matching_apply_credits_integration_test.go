//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/musicbrainz"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestApplyCurrentMatchingEvaluationMaterializesAcceptedArtistCreditsPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	f := newIncomingMatchFixture(t, ctx, db)
	cache := persistence.NewProviderCacheRepository(db)
	const releaseKey = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const recordingKey = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	const artistA = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	const artistB = "ffffffff-ffff-4fff-8fff-ffffffffffff"
	raw := json.RawMessage(`{"id":"` + releaseKey + `","title":"Fixture Album","artist-credit":[{"name":"A first","joinphrase":" feat. ","artist":{"id":"` + artistA + `","name":"Artist A"}},{"name":"A second","artist":{"id":"` + artistA + `","name":"Artist A"}}],"media":[{"position":1,"tracks":[{"id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","number":"1","recording":{"id":"` + recordingKey + `","title":"Fixture Track","artist-credit":[{"name":"Artist B","artist":{"id":"` + artistB + `","name":"Artist B"}}]}}]}]}`)
	graph, err := service.NormalizeMusicBrainzReleaseLookup(musicbrainz.LookupResult{Entity: musicbrainz.Entity{Raw: raw}})
	if err != nil {
		t.Fatalf("normalize accepted credits: %v", err)
	}
	ticket, err := cache.BeginRefresh(ctx, f.source, f.cacheKey, "incoming-match-integration")
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: json.RawMessage(`{"releases":[]}`), Graph: graph}); err != nil {
		t.Fatalf("persist accepted credits: %v", err)
	}
	snapshot, err := cache.GetMusicBrainzMatchingSnapshot(ctx, "https://musicbrainz.example/ws/2", "incoming-match-integration", f.releaseKeys, []string{f.cacheKey, "recording-search:a", "recording-search:b", "release-search:b"})
	if err != nil {
		t.Fatalf("refresh accepted projection fence: %v", err)
	}
	f.acceptedProjectionRev = snapshot.AcceptedProjectionRevision
	f.graph = graph
	repository := persistence.NewIncomingMatchCandidateRepository(db)
	evaluation := f.evaluation([]persistence.IncomingMatchCandidate{f.candidate(f.memberA, .95), f.candidate(f.memberB, .95)})
	evaluation.Groups[0].Evidence = matchingApplyEvidence(t, f, f.trackID)
	if err := repository.Replace(ctx, evaluation); err != nil {
		t.Fatalf("persist evaluation against latest projection: %v", err)
	}
	header, _, err := repository.Get(ctx, f.group.ID)
	if err != nil || header == nil {
		t.Fatalf("read evaluation header: %v", err)
	}
	if err := repository.ApplyCurrentMatchingEvaluation(ctx, f.group.ID, header.EvaluationRevision); err != nil {
		t.Fatalf("apply current evaluation: %v", err)
	}
	var releaseCredits []struct {
		Position         int
		ArtistID         uuid.UUID
		Name, JoinPhrase string
	}
	if err := db.NewRaw(`SELECT ra.position, ra.artist_id, ra.name, ra.name_join_phrase AS join_phrase FROM release_artist ra JOIN provider_local_release plr ON plr.local_release_id=ra.release_id WHERE plr.provider_id=? AND plr.provider_release_id=? ORDER BY ra.position`, f.provider, f.releaseID).Scan(ctx, &releaseCredits); err != nil {
		t.Fatal(err)
	}
	if len(releaseCredits) != 2 || releaseCredits[0].Position != 0 || releaseCredits[1].Position != 1 || releaseCredits[0].ArtistID != releaseCredits[1].ArtistID || releaseCredits[0].Name != "A first" || releaseCredits[0].JoinPhrase != " feat. " || releaseCredits[1].Name != "A second" || releaseCredits[1].JoinPhrase != "" {
		t.Fatalf("materialized release credits = %+v", releaseCredits)
	}
	var recordingCredits []struct {
		Position int
		Name     string
	}
	if err := db.NewRaw(`SELECT ra.position, ra.name FROM recording_artist ra JOIN provider_local_recording plr ON plr.local_recording_id=ra.recording_id JOIN provider_recording pr ON pr.id=plr.provider_recording_id AND pr.provider_id=plr.provider_id WHERE pr.provider_id=? AND pr.provider_key=? ORDER BY ra.position`, f.provider, recordingKey).Scan(ctx, &recordingCredits); err != nil {
		t.Fatal(err)
	}
	want := []struct {
		Position int
		Name     string
	}{{0, "Artist B"}}
	if !reflect.DeepEqual(recordingCredits, want) {
		t.Fatalf("recording credits = %+v, want %+v", recordingCredits, want)
	}
}
