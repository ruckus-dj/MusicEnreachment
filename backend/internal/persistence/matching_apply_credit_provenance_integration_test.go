//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/musicbrainz"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestApplyCurrentMatchingEvaluationTrackCreditOverrideAndProvenancePostgreSQL(t *testing.T) {
	for _, test := range []struct {
		name, trackCredit  string
		wantTrackCredits   int
		wantRepeatedCredit bool
		recordingFallback  bool
	}{
		{name: "explicit empty is an override", trackCredit: `,"artist-credit":[]`, wantTrackCredits: 0},
		{name: "omitted inherits recording credit", wantTrackCredits: 1},
		{name: "ordered repeated override", trackCredit: `,"artist-credit":[{"name":"First Credit","joinphrase":" & ","artist":{"id":"ffffffff-ffff-4fff-8fff-ffffffffffff","name":"Canonical Artist Name"}},{"name":"Second Credit","artist":{"id":"ffffffff-ffff-4fff-8fff-ffffffffffff","name":"Canonical Artist Name"}}]`, wantTrackCredits: 2, wantRepeatedCredit: true},
		{name: "recording title fallback keeps recording provenance", recordingFallback: true, wantTrackCredits: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			db := testpostgres.OpenMigrated(t)
			ctx := context.Background()
			f := newIncomingMatchFixture(t, ctx, db)
			cache := persistence.NewProviderCacheRepository(db)
			const releaseKey = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
			const trackKey = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
			const recordingKey = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
			const artistKey = "ffffffff-ffff-4fff-8fff-ffffffffffff"
			trackTitle := `,"title":"Accepted Track Title"`
			recordingTitle := "Recording Title"
			if test.recordingFallback {
				trackTitle = ""
				recordingTitle = "Fallback Recording"
			}
			raw := json.RawMessage(`{"id":"` + releaseKey + `","title":"Fixture Album","artist-credit":[],"media":[{"tracks":[{"id":"` + trackKey + `","number":"1"` + trackTitle + test.trackCredit + `,"recording":{"id":"` + recordingKey + `","title":"` + recordingTitle + `","artist-credit":[{"name":"Credited Name","artist":{"id":"` + artistKey + `","name":"Canonical Artist Name"}}]}}]}]}`)
			graph, err := service.NormalizeMusicBrainzReleaseLookup(musicbrainz.LookupResult{Entity: musicbrainz.Entity{Raw: raw}})
			if err != nil {
				t.Fatalf("normalize accepted projection: %v", err)
			}
			ticket, err := cache.BeginRefresh(ctx, f.source, f.cacheKey, "incoming-match-integration")
			if err != nil {
				t.Fatal(err)
			}
			if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: json.RawMessage(`{"releases":[]}`), Graph: graph}); err != nil {
				t.Fatalf("persist accepted projection: %v", err)
			}
			newerTrackTitle := `,"title":"Accepted Track Title"`
			newerRecordingTitle := `,"title":"New Recording Title"`
			if test.recordingFallback {
				newerTrackTitle = ""
				newerRecordingTitle = `,"title":"Fallback Recording"`
			}
			newerRaw := json.RawMessage(`{"id":"` + releaseKey + `","title":"Fixture Album","artist-credit":[],"media":[{"tracks":[{"id":"` + trackKey + `","number":"1"` + newerTrackTitle + test.trackCredit + `,"recording":{"id":"` + recordingKey + `"` + newerRecordingTitle + `}}]}]}`)
			newerGraph, err := service.NormalizeMusicBrainzReleaseLookup(musicbrainz.LookupResult{Entity: musicbrainz.Entity{Raw: newerRaw}})
			if err != nil {
				t.Fatalf("normalize newer recording fields: %v", err)
			}
			newerTicket, err := cache.BeginRefresh(ctx, f.source, f.cacheKey, "incoming-match-integration")
			if err != nil {
				t.Fatal(err)
			}
			if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: newerTicket, FetchedAt: time.Now().UTC(), Payload: json.RawMessage(`{"releases":[]}`), Graph: newerGraph}); err != nil {
				t.Fatalf("persist newer recording fields: %v", err)
			}
			snapshot, err := cache.GetMusicBrainzMatchingSnapshot(ctx, "https://musicbrainz.example/ws/2", "incoming-match-integration", f.releaseKeys, []string{f.cacheKey, "recording-search:a", "recording-search:b", "release-search:b"})
			if err != nil {
				t.Fatalf("refresh accepted projection fence: %v", err)
			}
			f.acceptedProjectionRev, f.graph = snapshot.AcceptedProjectionRevision, graph
			repository := persistence.NewIncomingMatchCandidateRepository(db)
			evaluation := f.evaluation([]persistence.IncomingMatchCandidate{f.candidate(f.memberA, .95), f.candidate(f.memberB, .95)})
			evaluation.Groups[0].Evidence = matchingApplyEvidence(t, f, f.trackID)
			if err := repository.Replace(ctx, evaluation); err != nil {
				t.Fatalf("persist evaluation: %v", err)
			}
			header, _, err := repository.Get(ctx, f.group.ID)
			if err != nil || header == nil {
				t.Fatalf("read evaluation: header=%v err=%v", header, err)
			}
			if err := repository.ApplyCurrentMatchingEvaluation(ctx, f.group.ID, header.EvaluationRevision); err != nil {
				t.Fatalf("apply current evaluation: %v", err)
			}
			var count int
			if err := db.NewRaw(`SELECT count(*) FROM track_artist WHERE track_id=(SELECT local_track_id FROM provider_local_track WHERE provider_id=? AND provider_track_id=?)`, f.provider, f.trackID).Scan(ctx, &count); err != nil {
				t.Fatal(err)
			}
			if count != test.wantTrackCredits {
				t.Fatalf("track credits=%d, want %d", count, test.wantTrackCredits)
			}
			if test.wantRepeatedCredit {
				var trackCredits []struct {
					Position   int
					Name       string
					JoinPhrase string `bun:"name_join_phrase"`
				}
				if err := db.NewRaw(`SELECT position, name, name_join_phrase FROM track_artist WHERE track_id=(SELECT local_track_id FROM provider_local_track WHERE provider_id=? AND provider_track_id=?) ORDER BY position`, f.provider, f.trackID).Scan(ctx, &trackCredits); err != nil {
					t.Fatal(err)
				}
				if len(trackCredits) != 2 || trackCredits[0].Position != 0 || trackCredits[0].Name != "First Credit" || trackCredits[0].JoinPhrase != " & " || trackCredits[1].Position != 1 || trackCredits[1].Name != "Second Credit" || trackCredits[1].JoinPhrase != "" {
					t.Fatalf("ordered repeated track credits=%+v", trackCredits)
				}
			}
			if err := db.NewRaw(`SELECT count(*) FROM recording_artist WHERE recording_id=(SELECT local_recording_id FROM provider_local_recording WHERE provider_id=? AND provider_recording_id=(SELECT id FROM provider_recording WHERE provider_id=? AND provider_key=?))`, f.provider, f.provider, recordingKey).Scan(ctx, &count); err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Fatalf("recording credits=%d, want 1", count)
			}
			var persistedRecordingTitle string
			if err := db.NewRaw(`SELECT title FROM local_recording WHERE id=(SELECT local_recording_id FROM provider_local_recording WHERE provider_id=? AND provider_recording_id=(SELECT id FROM provider_recording WHERE provider_id=? AND provider_key=?))`, f.provider, f.provider, recordingKey).Scan(ctx, &persistedRecordingTitle); err != nil {
				t.Fatal(err)
			}
			wantRecordingTitle := "New Recording Title"
			if test.recordingFallback {
				wantRecordingTitle = "Fallback Recording"
			}
			if persistedRecordingTitle != wantRecordingTitle {
				t.Fatalf("recording title=%q", persistedRecordingTitle)
			}
			var acceptedFetchOrders struct{ Credit, Title int64 }
			if err := db.NewRaw(`SELECT (field_evidence->'artist-credit'->>'fetch_order')::bigint, (field_evidence->'title'->>'fetch_order')::bigint FROM provider_source_entity WHERE provider_source_id=? AND entity_kind='recording' AND provider_key=?`, f.source, recordingKey).Scan(ctx, &acceptedFetchOrders.Credit, &acceptedFetchOrders.Title); err != nil {
				t.Fatal(err)
			}
			if acceptedFetchOrders.Credit == 0 || acceptedFetchOrders.Title <= acceptedFetchOrders.Credit {
				t.Fatalf("recording evidence fetch orders=%+v, want older artist-credit and newer title", acceptedFetchOrders)
			}
			var title string
			if err := db.NewRaw(`SELECT title FROM local_track WHERE id=(SELECT local_track_id FROM provider_local_track WHERE provider_id=? AND provider_track_id=?)`, f.provider, f.trackID).Scan(ctx, &title); err != nil {
				t.Fatal(err)
			}
			wantTitle := "Accepted Track Title"
			if test.recordingFallback {
				wantTitle = "Fallback Recording"
			}
			if title != wantTitle {
				t.Fatalf("track title=%q", title)
			}
			if test.recordingFallback {
				var metadata string
				if err := db.NewRaw(`SELECT metadata FROM local_track WHERE id=(SELECT local_track_id FROM provider_local_track WHERE provider_id=? AND provider_track_id=?)`, f.provider, f.trackID).Scan(ctx, &metadata); err != nil {
					t.Fatal(err)
				}
				var trackMetadata map[string]map[string]json.RawMessage
				if err := json.Unmarshal([]byte(metadata), &trackMetadata); err != nil {
					t.Fatal(err)
				}
				var reference struct {
					EntityKind string   `json:"entity_kind"`
					Key        string   `json:"provider_key"`
					Fields     []string `json:"fields"`
				}
				if err := json.Unmarshal(trackMetadata["automatic_matching"]["provider_fields_reference"], &reference); err != nil {
					t.Fatal(err)
				}
				if reference.EntityKind != "recording" || reference.Key != recordingKey || len(reference.Fields) != 1 || reference.Fields[0] != "title" {
					t.Fatalf("provider fields reference=%+v, want recording %s title", reference, recordingKey)
				}
				var titleFetchOrder int64
				if err := db.NewRaw(`SELECT (field_evidence->'title'->>'fetch_order')::bigint FROM provider_source_entity WHERE provider_source_id=? AND entity_kind='recording' AND provider_key=?`, f.source, recordingKey).Scan(ctx, &titleFetchOrder); err != nil {
					t.Fatal(err)
				}
				if titleFetchOrder != acceptedFetchOrders.Title {
					t.Fatalf("recording title fetch order=%d, want accepted recording fetch order %d", titleFetchOrder, acceptedFetchOrders.Title)
				}
				var storedEvidence map[string]persistence.ProviderFieldEvidence
				if err := json.Unmarshal(trackMetadata["automatic_matching"]["field_evidence"], &storedEvidence); err != nil {
					t.Fatal(err)
				}
				var expectedEvidenceJSON string
				if err := db.NewRaw(`SELECT field_evidence->'title' FROM provider_source_entity WHERE provider_source_id=? AND entity_kind='recording' AND provider_key=?`, f.source, recordingKey).Scan(ctx, &expectedEvidenceJSON); err != nil {
					t.Fatal(err)
				}
				var expectedEvidence persistence.ProviderFieldEvidence
				if err := json.Unmarshal([]byte(expectedEvidenceJSON), &expectedEvidence); err != nil {
					t.Fatal(err)
				}
				if storedEvidence["title"] != expectedEvidence {
					t.Fatalf("stored title provenance=%+v, want recording field evidence %+v", storedEvidence["title"], expectedEvidence)
				}
			}
			var artistName string
			if err := db.NewRaw(`SELECT name FROM local_artist WHERE id=(SELECT artist_id FROM recording_artist WHERE recording_id=(SELECT local_recording_id FROM provider_local_recording WHERE provider_id=? AND provider_recording_id=(SELECT id FROM provider_recording WHERE provider_id=? AND provider_key=?)) ORDER BY position LIMIT 1)`, f.provider, f.provider, recordingKey).Scan(ctx, &artistName); err != nil {
				t.Fatal(err)
			}
			if artistName != "Canonical Artist Name" {
				t.Fatalf("local artist name=%q, want source-owned name", artistName)
			}
			var artistMetadata string
			if err := db.NewRaw(`SELECT metadata FROM local_artist WHERE name=?`, artistName).Scan(ctx, &artistMetadata); err != nil {
				t.Fatal(err)
			}
			var artistProvenance map[string]map[string]json.RawMessage
			if err := json.Unmarshal([]byte(artistMetadata), &artistProvenance); err != nil {
				t.Fatal(err)
			}
			var nameSource string
			if err := json.Unmarshal(artistProvenance["automatic_matching"]["name_source"], &nameSource); err != nil || nameSource != "name" {
				t.Fatalf("artist name source=%q err=%v", nameSource, err)
			}
			var metadata string
			if err := db.NewRaw(`SELECT metadata FROM local_track WHERE id=(SELECT local_track_id FROM provider_local_track WHERE provider_id=? AND provider_track_id=?)`, f.provider, f.trackID).Scan(ctx, &metadata); err != nil {
				t.Fatal(err)
			}
			var provenance map[string]map[string]json.RawMessage
			if err := json.Unmarshal([]byte(metadata), &provenance); err != nil {
				t.Fatal(err)
			}
			var revision string
			if err := json.Unmarshal(provenance["automatic_matching"]["projection_revision"], &revision); err != nil || revision != f.acceptedProjectionRev {
				t.Fatalf("track provenance revision=%q err=%v", revision, err)
			}
		})
	}
}
