//go:build integration

package persistence_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/api"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/musicbrainz"
	"github.com/ruckus/MusicEnreachment/backend/internal/jobs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/uptrace/bun"
)

func TestFrozenManualPartialConfirmationOverHTTPPostgreSQL(t *testing.T) {
	testAutomaticFallbackFiniteFlowWithPublicationCallback(t, false, false, false, false, false, func(fixture uniquePublicationApplyFixture) {
		ctx, database, groupID := fixture.Context, fixture.Database, fixture.InputSnapshot.Authority.GroupID
		candidateRepository := persistence.NewIncomingMatchCandidateRepository(database)
		groupingRepository := persistence.NewIncomingGroupingRepository(database)
		groups := service.NewIncomingGroups(groupingRepository)
		dependencies := api.Dependencies{
			IncomingGroups: groups,
			MatchingReview: service.NewMatchingReviewService(candidateRepository),
			MatchingManual: service.NewMatchingManualService(candidateRepository),
		}
		handler := api.HandlerWithDependencies(dependencies)

		// Accept a second provider projection under the unchanged configuration.
		// Review and manual operations must continue to use accepted frozen A.
		cache := persistence.NewProviderCacheRepository(database)
		payload := json.RawMessage(fmt.Sprintf(`{"id":"90000000-0000-4000-8000-0000000000f1","title":"Projection B Album","artist-credit":[{"name":"B Release Artist","artist":{"id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","name":"B Release Artist"}}],"media":[{"position":1,"tracks":[{"id":"90000000-0000-4000-8000-0000000000fa","number":"1","title":"Projection B Track Alpha","length":180000,"recording":{"id":"90000000-0000-4000-8000-0000000000fb","title":"Projection B Recording Alpha","artist-credit":[{"name":"B Recording Artist","artist":{"id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","name":"B Recording Artist"}}]}},{"id":"90000000-0000-4000-8000-0000000000fc","number":"2","title":"Projection B Track Beta","length":180000,"recording":{"id":"90000000-0000-4000-8000-0000000000fd","title":"Projection B Recording Beta","artist-credit":[{"name":"B Recording Artist","artist":{"id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","name":"B Recording Artist"}}]}}]}]}`))
		graph, err := service.NormalizeMusicBrainzReleaseLookup(musicbrainz.LookupResult{Entity: musicbrainz.Entity{Raw: payload}})
		if err != nil {
			t.Fatalf("normalize refreshed provider projection B: %v", err)
		}
		ticket, err := cache.BeginRefresh(ctx, fixture.InputSnapshot.Source.ID, fixture.InputSnapshot.Selected.Lookup.CacheKey, fixture.InputSnapshot.Source.ConfigurationIdentity)
		if err != nil {
			t.Fatalf("reserve same-configuration provider projection B: %v", err)
		}
		if err := cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{Ticket: ticket, FetchedAt: time.Now().UTC(), Payload: payload, Graph: graph}); err != nil {
			t.Fatalf("accept same-configuration provider projection B: %v", err)
		}
		// Refresh the live grouping projection as well; neither current projection
		// is permitted to rewrite the frozen provider projection, header, or seal.
		if err := groups.Refresh(ctx); err != nil {
			t.Fatalf("refresh live incoming projection B: %v", err)
		}
		beforeHeader, beforeSeal := frozenManualHTTPAuthority(t, ctx, database, groupID, fixture.InputSnapshot.Authority.ParentRunID, fixture.InputSnapshot.Authority.WaitRevision)
		getReview := func() service.MatchingReview {
			t.Helper()
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/incoming-groups/"+groupID.String()+"/matching", nil))
			if response.Code != http.StatusOK {
				t.Fatalf("GET matching review status=%d body=%s", response.Code, response.Body.String())
			}
			var review service.MatchingReview
			if err := json.Unmarshal(response.Body.Bytes(), &review); err != nil {
				t.Fatalf("decode actual matching review response: %v; body=%s", err, response.Body.String())
			}
			return review
		}
		review := getReview()
		if !review.Evaluated || !review.ProposalAvailable || review.EvaluationRevision == "" || len(review.Releases) != 1 || len(review.Members) != 2 {
			t.Fatalf("GET review did not expose the persisted frozen decision: %+v", review)
		}
		layout := review.Releases[0]
		if len(layout.Positions) != 2 || layout.Title != "Captured Album" {
			t.Fatalf("frozen public release layout positions=%d, want two: %+v", len(layout.Positions), layout)
		}
		member := review.Members[0]
		omitted := review.Members[1]
		assignment := service.MatchingManualAssignment{
			FileMemberID: member, ProviderTrackID: layout.Positions[0].TrackID,
			// The official review DTO exposes zero-based layout coordinates;
			// manual assignment inputs are one-based by contract.
			Medium: layout.Positions[0].Medium + 1, Position: layout.Positions[0].Position + 1,
		}
		bodyFor := func(assignments []service.MatchingManualAssignment) map[string]any {
			return map[string]any{
				"expected_evaluation_revision": review.EvaluationRevision,
				"provider_release_id":          layout.ProviderReleaseID,
				"assignments":                  assignments,
			}
		}
		call := func(method, path string, payload any) *httptest.ResponseRecorder {
			t.Helper()
			encoded, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("encode HTTP payload: %v", err)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(method, path, bytes.NewReader(encoded)))
			return response
		}
		path := "/incoming-groups/" + groupID.String() + "/matching/preview"
		firstPreviewResponse := call(http.MethodPost, path, bodyFor([]service.MatchingManualAssignment{assignment}))
		var firstPreview service.MatchingManualPreview
		if firstPreviewResponse.Code != http.StatusOK || json.Unmarshal(firstPreviewResponse.Body.Bytes(), &firstPreview) != nil {
			t.Fatalf("first actual HTTP preview status=%d body=%s", firstPreviewResponse.Code, firstPreviewResponse.Body.String())
		}
		if firstPreview.ProviderReleaseID != layout.ProviderReleaseID || len(firstPreview.Members) != 2 || len(firstPreview.OmittedMemberIDs) != 1 || firstPreview.OmittedMemberIDs[0] != omitted {
			t.Fatalf("first preview is not a whole-group partial preview: %+v", firstPreview)
		}
		var omittedTrackSourcesBefore int
		if err := database.NewRaw(`SELECT count(*) FROM track_source source JOIN source_work_variant_identity identity ON identity.media_variant_id=source.media_variant_id
			JOIN incoming_match_evaluation_member member ON member.work_id=identity.work_id WHERE member.group_id=? AND member.file_member_id=?`, groupID, omitted).Scan(ctx, &omittedTrackSourcesBefore); err != nil {
			t.Fatalf("read omitted member track links before confirmation: %v", err)
		}

		confirmBody := bodyFor([]service.MatchingManualAssignment{assignment})
		confirmBody["preview_digest"] = firstPreview.PreviewDigest
		confirmBody["request_id"] = uuid.New()
		confirmed := call(http.MethodPost, "/incoming-groups/"+groupID.String()+"/matching/confirm", confirmBody)
		var confirmation service.MatchingManualPreview
		if confirmed.Code != http.StatusOK || json.Unmarshal(confirmed.Body.Bytes(), &confirmation) != nil {
			t.Fatalf("actual HTTP confirm status=%d body=%s", confirmed.Code, confirmed.Body.String())
		}
		if confirmation.LocalReleaseID == uuid.Nil || len(confirmation.OmittedMemberIDs) != 1 || confirmation.OmittedMemberIDs[0] != omitted {
			t.Fatalf("confirmation did not preserve partial scope: %+v", confirmation)
		}
		var omittedTrackSourcesAfter int
		if err := database.NewRaw(`SELECT count(*) FROM track_source source JOIN source_work_variant_identity identity ON identity.media_variant_id=source.media_variant_id
			JOIN incoming_match_evaluation_member member ON member.work_id=identity.work_id WHERE member.group_id=? AND member.file_member_id=?`, groupID, omitted).Scan(ctx, &omittedTrackSourcesAfter); err != nil || omittedTrackSourcesAfter != omittedTrackSourcesBefore {
			t.Fatalf("omitted member links changed: before=%d after=%d err=%v", omittedTrackSourcesBefore, omittedTrackSourcesAfter, err)
		}
		assertFrozenManualHTTPMaterialization(t, ctx, database, fixture, assignment, confirmation.LocalReleaseID)
		afterHeader, afterSeal := frozenManualHTTPAuthority(t, ctx, database, groupID, fixture.InputSnapshot.Authority.ParentRunID, fixture.InputSnapshot.Authority.WaitRevision)
		if !bytes.Equal(beforeHeader, afterHeader) || !bytes.Equal(beforeSeal, afterSeal) {
			t.Fatalf("manual confirmation mutated live evaluation header or frozen seal")
		}

		// A stale preview digest must conflict with a fresh request identity, while
		// another subset can be previewed against the promoted manual state.
		staleBody := bodyFor([]service.MatchingManualAssignment{assignment})
		staleBody["preview_digest"] = firstPreview.PreviewDigest
		staleBody["request_id"] = uuid.New()
		stale := call(http.MethodPost, "/incoming-groups/"+groupID.String()+"/matching/confirm", staleBody)
		assertFrozenManualHTTPConflictWithoutDigest(t, "stale preview confirm", stale)
		secondMember := omitted
		secondAssignment := service.MatchingManualAssignment{
			FileMemberID: secondMember, ProviderTrackID: layout.Positions[1].TrackID,
			Medium: layout.Positions[1].Medium + 1, Position: layout.Positions[1].Position + 1,
		}
		secondPreview := call(http.MethodPost, path, bodyFor([]service.MatchingManualAssignment{secondAssignment}))
		if secondPreview.Code != http.StatusOK {
			t.Fatalf("fresh second-subset preview status=%d body=%s", secondPreview.Code, secondPreview.Body.String())
		}

		// The original registered child remains a real durable delivery. Its
		// execution must settle review_required after manual promotion, not restore
		// automatic links over the explicit manual decision.
		child := fixture.ChildOperation
		if child.RiverJobID == nil {
			t.Fatal("frozen automatic child has no registered River job")
		}
		if _, err := database.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, *child.RiverJobID).Exec(ctx); err != nil {
			t.Fatalf("make automatic child delivery runnable: %v", err)
		}
		worker := jobs.NewMatchingApplyWorker(fixture.Repository, service.NewOperations(fixture.Repository))
		if err := worker.Work(ctx, &river.Job[persistence.MatchingApplyOperationArgs]{
			JobRow: &rivertype.JobRow{ID: *child.RiverJobID, Attempt: 1}, Args: persistence.MatchingApplyOperationArgs{OperationID: child.ID},
		}); err != nil {
			t.Fatalf("execute actual frozen automatic child after manual promotion: %v", err)
		}
		settled, err := fixture.Repository.GetOperation(ctx, child.ID)
		if err != nil || settled.State != "succeeded" || settled.Stage != "review_required" {
			t.Fatalf("automatic child state=%+v err=%v, want succeeded/review_required", settled, err)
		}
		assertFrozenManualHTTPMaterialization(t, ctx, database, fixture, assignment, confirmation.LocalReleaseID)

		// Configuration identity rotation and source-capture changes must reject
		// fresh preview requests rather than minting a digest from stale authority.
		var receiptCountBefore, mappingCountBefore int
		if err := database.NewRaw(`SELECT count(*) FROM manual_matching_receipt WHERE group_id=?`, groupID).Scan(ctx, &receiptCountBefore); err != nil {
			t.Fatalf("count manual receipts before stale-source checks: %v", err)
		}
		if err := database.NewRaw(`SELECT count(*) FROM provider_local_track WHERE provider_id=?`, fixture.InputSnapshot.Source.ProviderID).Scan(ctx, &mappingCountBefore); err != nil {
			t.Fatalf("count local track mappings before stale-source checks: %v", err)
		}
		var originalIdentity string
		if err := database.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &originalIdentity); err != nil {
			t.Fatalf("read original provider configuration identity: %v", err)
		}
		rotatedIdentity := uuid.NewString()
		if _, err := database.NewRaw(`UPDATE app_setting SET setting_value=? WHERE setting_name='musicbrainz_config_identity'`, rotatedIdentity).Exec(ctx); err != nil {
			t.Fatalf("rotate provider configuration identity: %v", err)
		}
		staleConfiguration := call(http.MethodPost, path, bodyFor([]service.MatchingManualAssignment{secondAssignment}))
		assertFrozenManualHTTPConflictWithoutDigest(t, "rotated provider config", staleConfiguration)
		if _, err := database.NewRaw(`UPDATE app_setting SET setting_value=? WHERE setting_name='musicbrainz_config_identity'`, originalIdentity).Exec(ctx); err != nil {
			t.Fatalf("restore provider configuration identity: %v", err)
		}

		var originalCaptures []struct {
			ID   uuid.UUID `bun:"id"`
			Tags []byte    `bun:"observed_tags"`
		}
		if err := database.NewRaw(`SELECT id,observed_tags FROM media_metadata_result ORDER BY created_at,id`).Scan(ctx, &originalCaptures); err != nil || len(originalCaptures) == 0 {
			t.Fatalf("read original source capture metadata: %v", err)
		}
		if _, err := database.NewRaw(`UPDATE media_metadata_result SET observed_tags=jsonb_set(observed_tags,'{TITLE}', '["Changed after frozen capture"]'::jsonb)`).Exec(ctx); err != nil {
			t.Fatalf("change live source capture metadata: %v", err)
		}
		if err := groups.Refresh(ctx); err != nil {
			t.Fatalf("refresh changed live source projection: %v", err)
		}
		staleCapture := call(http.MethodPost, path, bodyFor([]service.MatchingManualAssignment{secondAssignment}))
		assertFrozenManualHTTPConflictWithoutDigest(t, "source capture change", staleCapture)
		for _, capture := range originalCaptures {
			if _, err := database.NewRaw(`UPDATE media_metadata_result SET observed_tags=?::jsonb WHERE id=?`, string(capture.Tags), capture.ID).Exec(ctx); err != nil {
				t.Fatalf("restore original source capture metadata: %v", err)
			}
		}
		if err := groups.Refresh(ctx); err != nil {
			t.Fatalf("restore live source projection: %v", err)
		}
		var originalLocations []struct {
			ID           uuid.UUID `bun:"id"`
			RelativePath string    `bun:"relative_path"`
			SizeBytes    int64     `bun:"size_bytes"`
			Mtime        time.Time `bun:"mtime"`
		}
		if err := database.NewRaw(`SELECT id,relative_path,size_bytes,mtime FROM source_location ORDER BY id`).Scan(ctx, &originalLocations); err != nil || len(originalLocations) == 0 {
			t.Fatalf("read source locations before location-fence check: %v", err)
		}
		if _, err := database.NewRaw(`UPDATE source_location SET relative_path=relative_path||'.changed',size_bytes=size_bytes+1,mtime=mtime+interval '1 second'`).Exec(ctx); err != nil {
			t.Fatalf("change live source location metadata: %v", err)
		}
		if err := groups.Refresh(ctx); err != nil {
			t.Fatalf("refresh changed source location projection: %v", err)
		}
		staleLocation := call(http.MethodPost, path, bodyFor([]service.MatchingManualAssignment{secondAssignment}))
		assertFrozenManualHTTPConflictWithoutDigest(t, "source location/size/mtime change", staleLocation)
		for _, location := range originalLocations {
			if _, err := database.NewRaw(`UPDATE source_location SET relative_path=?,size_bytes=?,mtime=? WHERE id=?`, location.RelativePath, location.SizeBytes, location.Mtime, location.ID).Exec(ctx); err != nil {
				t.Fatalf("restore source location metadata: %v", err)
			}
		}
		if err := groups.Refresh(ctx); err != nil {
			t.Fatalf("restore source location projection: %v", err)
		}
		var receiptCountAfter, mappingCountAfter int
		if err := database.NewRaw(`SELECT count(*) FROM manual_matching_receipt WHERE group_id=?`, groupID).Scan(ctx, &receiptCountAfter); err != nil {
			t.Fatalf("count manual receipts after stale-source checks: %v", err)
		}
		if err := database.NewRaw(`SELECT count(*) FROM provider_local_track WHERE provider_id=?`, fixture.InputSnapshot.Source.ProviderID).Scan(ctx, &mappingCountAfter); err != nil {
			t.Fatalf("count local track mappings after stale-source checks: %v", err)
		}
		if receiptCountBefore != receiptCountAfter || mappingCountBefore != mappingCountAfter {
			t.Fatalf("stale previews materialized or receipted work: receipts=%d/%d mappings=%d/%d", receiptCountBefore, receiptCountAfter, mappingCountBefore, mappingCountAfter)
		}
	}, true)
}

func assertFrozenManualHTTPConflictWithoutDigest(t *testing.T, label string, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Code != http.StatusConflict || bytes.Contains(response.Body.Bytes(), []byte(`"preview_digest"`)) {
		t.Fatalf("%s preview status=%d body=%s; want 409 without a replacement preview digest", label, response.Code, response.Body.String())
	}
}

func frozenManualHTTPAuthority(t *testing.T, ctx context.Context, database *bun.DB, groupID, runID uuid.UUID, waitRevision int64) ([]byte, []byte) {
	t.Helper()
	var header []byte
	if err := database.NewRaw(`SELECT jsonb_build_object('evaluation_revision',evaluation_revision,'evaluation_evidence',evaluation_evidence,
		'fence_version',fence_version,'target_run_id',target_run_id,'target_capture',target_capture,'target_capture_digest',encode(target_capture_digest,'hex'),
		'frozen_authority_version',frozen_authority_version,'frozen_run_id',frozen_run_id,'frozen_wait_revision',frozen_wait_revision,'frozen_seal_digest',encode(frozen_seal_digest,'hex'))
		FROM incoming_match_evaluation WHERE group_id=?`, groupID).Scan(ctx, &header); err != nil {
		t.Fatalf("read frozen evaluation header: %v", err)
	}
	var seal []byte
	if err := database.NewRaw(`SELECT jsonb_build_object('digest',encode(authority_digest,'hex'),'authority',authority)
		FROM matching_fallback_authority_seal WHERE run_id=? AND wait_revision=?`, runID, waitRevision).Scan(ctx, &seal); err != nil {
		t.Fatalf("read frozen authority seal: %v", err)
	}
	return header, seal
}

func assertFrozenManualHTTPMaterialization(t *testing.T, ctx context.Context, database *bun.DB, fixture uniquePublicationApplyFixture, assignment service.MatchingManualAssignment, localReleaseID uuid.UUID) {
	t.Helper()
	var frozenTrack *persistence.FrozenRecordingTrack
	for index := range fixture.InputSnapshot.Selected.Projection.Tracks {
		track := &fixture.InputSnapshot.Selected.Projection.Tracks[index]
		if track.ProviderTrackID == assignment.ProviderTrackID {
			frozenTrack = track
			break
		}
	}
	if frozenTrack == nil || frozenTrack.RecordingTitle == frozenTrack.Title || len(frozenTrack.RecordingCredits) == 0 || len(frozenTrack.Credits) == 0 ||
		frozenTrack.RecordingCredits[0].ArtistName == frozenTrack.Credits[0].ArtistName {
		t.Fatalf("fixture must distinguish original recording fields from track overrides: %+v", frozenTrack)
	}
	var method string
	if err := database.NewRaw(`SELECT method FROM provider_local_release WHERE provider_id=? AND provider_release_id=?`, fixture.InputSnapshot.Source.ProviderID, fixture.InputSnapshot.Selected.Projection.Release.ID).Scan(ctx, &method); err != nil || method != "manual" {
		t.Fatalf("manual release mapping method=%q err=%v", method, err)
	}
	var trackID, recordingID uuid.UUID
	var trackTitle, recordingTitle string
	if err := database.NewRaw(`SELECT mapping.local_track_id,track.recording_id,local_track.title,local_recording.title FROM provider_local_track mapping
		JOIN release_track track ON track.track_id=mapping.local_track_id JOIN provider_local_release release ON release.local_release_id=track.release_id
		JOIN local_track ON local_track.id=track.track_id JOIN local_recording ON local_recording.id=track.recording_id
		WHERE mapping.provider_id=? AND mapping.provider_track_id=? AND release.local_release_id=?`, fixture.InputSnapshot.Source.ProviderID, assignment.ProviderTrackID, localReleaseID).
		Scan(ctx, &trackID, &recordingID, &trackTitle, &recordingTitle); err != nil {
		t.Fatalf("read manually materialized track and recording: %v", err)
	}
	if trackTitle != frozenTrack.Title || recordingTitle != frozenTrack.RecordingTitle {
		t.Fatalf("manual materialization conflated track override and original recording: track=%q want %q recording=%q want %q", trackTitle, frozenTrack.Title, recordingTitle, frozenTrack.RecordingTitle)
	}
	var recordingMethod string
	var recordingPolicy *string
	if err := database.NewRaw(`SELECT method,policy_version FROM provider_local_recording WHERE provider_id=? AND local_recording_id=?`, fixture.InputSnapshot.Source.ProviderID, recordingID).Scan(ctx, &recordingMethod, &recordingPolicy); err != nil || recordingMethod != "manual" || recordingPolicy != nil {
		t.Fatalf("manual recording mapping method=%q policy=%v err=%v", recordingMethod, recordingPolicy, err)
	}
	var trackMethod string
	var trackPolicy *string
	if err := database.NewRaw(`SELECT method,policy_version FROM provider_local_track WHERE provider_id=? AND provider_track_id=?`, fixture.InputSnapshot.Source.ProviderID, assignment.ProviderTrackID).Scan(ctx, &trackMethod, &trackPolicy); err != nil || trackMethod != "manual" || trackPolicy != nil {
		t.Fatalf("manual track mapping method=%q policy=%v err=%v", trackMethod, trackPolicy, err)
	}
	var trackSources int
	if err := database.NewRaw(`SELECT count(*) FROM track_source source JOIN source_work_variant_identity identity ON identity.media_variant_id=source.media_variant_id
		JOIN incoming_match_evaluation_member member ON member.work_id=identity.work_id WHERE source.track_id=? AND member.group_id=? AND member.file_member_id=?
		AND source.decision_method='manual' AND source.policy_version IS NULL`, trackID, fixture.InputSnapshot.Authority.GroupID, assignment.FileMemberID).Scan(ctx, &trackSources); err != nil || trackSources != 1 {
		t.Fatalf("manual track source rows=%d err=%v", trackSources, err)
	}
	var recordingCredits, trackCredits int
	if err := database.NewRaw(`SELECT count(*) FROM recording_artist WHERE recording_id=?`, recordingID).Scan(ctx, &recordingCredits); err != nil {
		t.Fatalf("read recording credits: %v", err)
	}
	if err := database.NewRaw(`SELECT count(*) FROM track_artist WHERE track_id=?`, trackID).Scan(ctx, &trackCredits); err != nil {
		t.Fatalf("read track credits: %v", err)
	}
	if recordingCredits == 0 || trackCredits == 0 {
		t.Fatalf("frozen manual artist credits missing: recording=%d track=%d", recordingCredits, trackCredits)
	}
	var recordingArtist, trackArtist string
	if err := database.NewRaw(`SELECT artist.name FROM recording_artist credit JOIN local_artist artist ON artist.id=credit.artist_id WHERE credit.recording_id=? ORDER BY credit.position LIMIT 1`, recordingID).Scan(ctx, &recordingArtist); err != nil {
		t.Fatalf("read materialized recording artist: %v", err)
	}
	if err := database.NewRaw(`SELECT artist.name FROM track_artist credit JOIN local_artist artist ON artist.id=credit.artist_id WHERE credit.track_id=? ORDER BY credit.position LIMIT 1`, trackID).Scan(ctx, &trackArtist); err != nil {
		t.Fatalf("read materialized track artist: %v", err)
	}
	if recordingArtist != frozenTrack.RecordingCredits[0].ArtistName || trackArtist != frozenTrack.Credits[0].ArtistName {
		t.Fatalf("manual artist credits conflated original recording and track override: recording=%q want %q track=%q want %q", recordingArtist, frozenTrack.RecordingCredits[0].ArtistName, trackArtist, frozenTrack.Credits[0].ArtistName)
	}
	var manualArtists, policyBearingArtists int
	if err := database.NewRaw(`SELECT count(*) FILTER (WHERE mapping.method='manual'),count(*) FILTER (WHERE mapping.policy_version IS NOT NULL)
		FROM provider_local_artist mapping WHERE mapping.provider_id=? AND mapping.local_artist_id IN (
			SELECT artist_id FROM recording_artist WHERE recording_id=? UNION SELECT artist_id FROM track_artist WHERE track_id=?)`,
		fixture.InputSnapshot.Source.ProviderID, recordingID, trackID).Scan(ctx, &manualArtists, &policyBearingArtists); err != nil || manualArtists == 0 || policyBearingArtists != 0 {
		t.Fatalf("manual artist mapping policy/method: manual=%d policy-bearing=%d err=%v", manualArtists, policyBearingArtists, err)
	}
}
