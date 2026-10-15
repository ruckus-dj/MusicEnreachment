package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type manualMatchingHTTPRepository struct {
	preview      persistence.MatchingManualPreview
	err          error
	previewCalls int
	confirmCalls int
	groupID      uuid.UUID
	revision     string
	releaseID    uuid.UUID
	assignments  []persistence.MatchingManualAssignment
	digest       string
	requestID    uuid.UUID
}

func (repository *manualMatchingHTTPRepository) PreviewMatchingManual(_ context.Context, groupID uuid.UUID, revision string, releaseID uuid.UUID, assignments []persistence.MatchingManualAssignment) (persistence.MatchingManualPreview, error) {
	repository.previewCalls++
	repository.record(groupID, revision, releaseID, assignments)
	return repository.preview, repository.err
}

func (repository *manualMatchingHTTPRepository) ConfirmMatchingManual(_ context.Context, groupID uuid.UUID, revision string, releaseID uuid.UUID, assignments []persistence.MatchingManualAssignment, digest string, requestID uuid.UUID) (persistence.MatchingManualPreview, error) {
	repository.confirmCalls++
	repository.record(groupID, revision, releaseID, assignments)
	repository.digest, repository.requestID = digest, requestID
	return repository.preview, repository.err
}

func (repository *manualMatchingHTTPRepository) record(groupID uuid.UUID, revision string, releaseID uuid.UUID, assignments []persistence.MatchingManualAssignment) {
	repository.groupID, repository.revision, repository.releaseID = groupID, revision, releaseID
	repository.assignments = append([]persistence.MatchingManualAssignment(nil), assignments...)
}

func TestMatchingManualRegisteredRoutesPreviewWholeGroupAndConfirmIntent(t *testing.T) {
	groupID, member1, member2 := uuid.New(), uuid.New(), uuid.New()
	releaseID, trackID, requestID := uuid.New(), uuid.New(), uuid.New()
	repository := &manualMatchingHTTPRepository{preview: persistence.MatchingManualPreview{
		GroupID: groupID, EvaluationRevision: "evaluation-r1", ProviderReleaseID: releaseID,
		SelectedRelease: persistence.MatchingManualReleaseLayout{ProviderReleaseID: releaseID, ReleaseID: "provider-release", Title: "Selected release", TracklistComplete: true,
			Positions: []persistence.MatchingManualReleaseLayoutPosition{{TrackID: trackID, Medium: 0, Position: 0, RecordingID: "recording", Title: "Track title"}}},
		LocalReleaseID: uuid.New(), PreviewDigest: "digest-r1",
		Members: []persistence.MatchingManualMember{
			{FileMemberID: member1, Locations: []string{"/music/one.flac"}, Assignment: &persistence.MatchingManualAssignment{FileMemberID: member1, ProviderTrackID: trackID, Medium: 1, Position: 1}},
			{FileMemberID: member2, Locations: []string{"/music/two.flac"}},
		}, OmittedMemberIDs: []uuid.UUID{member2},
	}}
	handler := HandlerWithDependencies(Dependencies{MatchingManual: service.NewMatchingManualService(repository)})
	payload := map[string]any{"expected_evaluation_revision": "evaluation-r1", "provider_release_id": releaseID,
		"assignments": []any{map[string]any{"file_member_id": member1, "provider_track_id": trackID, "medium": 1, "position": 1}}}
	body, _ := json.Marshal(payload)
	preview := httptest.NewRecorder()
	handler.ServeHTTP(preview, httptest.NewRequest(http.MethodPost, "/incoming-groups/"+groupID.String()+"/matching/preview", strings.NewReader(string(body))))
	if preview.Code != http.StatusOK {
		t.Fatalf("preview status = %d, body: %s", preview.Code, preview.Body.String())
	}
	for _, expected := range []string{`"local_release_id"`, `"selected_release":{"provider_release_id":"` + releaseID.String(), `"title":"Selected release"`, `"recording_id":"recording"`, `"omitted_member_ids":["` + member2.String(), `"file_member_id":"` + member2.String() + `"`, `"preview_digest":"digest-r1"`} {
		if !strings.Contains(preview.Body.String(), expected) {
			t.Errorf("preview response missing %s: %s", expected, preview.Body.String())
		}
	}
	if repository.previewCalls != 1 || repository.confirmCalls != 0 || repository.groupID != groupID || repository.revision != "evaluation-r1" || repository.releaseID != releaseID || len(repository.assignments) != 1 {
		t.Fatalf("preview intent not passed exactly: %#v", repository)
	}

	confirmPayload := map[string]any{"expected_evaluation_revision": "evaluation-r1", "provider_release_id": releaseID,
		"assignments": payload["assignments"], "preview_digest": "digest-r1", "request_id": requestID}
	confirmBody, _ := json.Marshal(confirmPayload)
	confirmed := httptest.NewRecorder()
	handler.ServeHTTP(confirmed, httptest.NewRequest(http.MethodPost, "/incoming-groups/"+groupID.String()+"/matching/confirm", strings.NewReader(string(confirmBody))))
	if confirmed.Code != http.StatusOK || repository.confirmCalls != 1 || repository.digest != "digest-r1" || repository.requestID != requestID {
		t.Fatalf("confirm status = %d, body=%s intent=%#v", confirmed.Code, confirmed.Body.String(), repository)
	}
	if !strings.Contains(confirmed.Body.String(), `"omitted_member_ids"`) {
		t.Fatalf("confirm hid unassigned members: %s", confirmed.Body.String())
	}
	replayed := httptest.NewRecorder()
	handler.ServeHTTP(replayed, httptest.NewRequest(http.MethodPost, "/incoming-groups/"+groupID.String()+"/matching/confirm", strings.NewReader(string(confirmBody))))
	if replayed.Code != http.StatusOK || !bytes.Equal(replayed.Body.Bytes(), confirmed.Body.Bytes()) || repository.confirmCalls != 2 {
		t.Fatalf("exact confirmation replay changed response: status=%d calls=%d body=%s", replayed.Code, repository.confirmCalls, replayed.Body.String())
	}
}

func TestMatchingManualAssignmentsPreserveExplicitEmptyOverrides(t *testing.T) {
	var payload []matchingManualAssignmentBody
	if err := json.Unmarshal([]byte(`[{"file_member_id":"`+uuid.NewString()+`","provider_track_id":"`+uuid.NewString()+`","medium":1,"position":1,"title_override":"","artist_overrides":[]}]`), &payload); err != nil {
		t.Fatal(err)
	}
	assignments := matchingManualAssignments(payload)
	if len(assignments) != 1 || assignments[0].TitleOverride == nil || *assignments[0].TitleOverride != "" || assignments[0].ArtistOverrides == nil || len(*assignments[0].ArtistOverrides) != 0 {
		t.Fatalf("explicit empty overrides were lost: %#v", assignments)
	}
}

func TestMatchingManualErrorsKeepValidationConflictAndInfrastructureDistinct(t *testing.T) {
	groupID, releaseID := uuid.New(), uuid.New()
	for _, test := range []struct {
		name string
		err  error
		body string
		want int
	}{
		{name: "stale conflict", err: persistence.ErrIncomingMatchConflict, body: `{"expected_evaluation_revision":"r1","provider_release_id":"` + releaseID.String() + `","assignments":[]}`, want: http.StatusConflict},
		{name: "invalid", body: `{"expected_evaluation_revision":"","provider_release_id":"` + releaseID.String() + `","assignments":[]}`, want: http.StatusUnprocessableEntity},
		{name: "infrastructure", err: errors.New("database unavailable"), body: `{"expected_evaluation_revision":"r1","provider_release_id":"` + releaseID.String() + `","assignments":[]}`, want: http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &manualMatchingHTTPRepository{err: test.err}
			handler := HandlerWithDependencies(Dependencies{MatchingManual: service.NewMatchingManualService(repository)})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/incoming-groups/"+groupID.String()+"/matching/preview", strings.NewReader(test.body)))
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d; body: %s", response.Code, test.want, response.Body.String())
			}
			if test.want == http.StatusUnprocessableEntity && repository.previewCalls != 0 {
				t.Fatalf("invalid request reached repository: %d calls", repository.previewCalls)
			}
		})
	}

	repository := &manualMatchingHTTPRepository{}
	handler := HandlerWithDependencies(Dependencies{MatchingManual: service.NewMatchingManualService(repository)})
	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, httptest.NewRequest(http.MethodPost, "/incoming-groups/not-a-uuid/matching/preview", strings.NewReader(`{}`)))
	if invalid.Code != http.StatusUnprocessableEntity || repository.previewCalls != 0 {
		t.Fatalf("invalid path UUID status/calls = %d/%d; body: %s", invalid.Code, repository.previewCalls, invalid.Body.String())
	}
}
