package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type matchingLocalHTTPRepository struct {
	intent    persistence.MatchingLocalIntent
	digest    string
	requestID uuid.UUID
	calls     int
}

func (r *matchingLocalHTTPRepository) PreviewMatchingLocal(_ context.Context, group uuid.UUID, intent persistence.MatchingLocalIntent) (persistence.MatchingLocalPreview, error) {
	r.calls++
	r.intent = intent
	return persistence.MatchingLocalPreview{GroupID: group, GroupRevision: intent.ExpectedGroupRevision, PreviewDigest: "digest"}, nil
}
func (r *matchingLocalHTTPRepository) ConfirmMatchingLocal(_ context.Context, group uuid.UUID, intent persistence.MatchingLocalIntent, digest string, requestID uuid.UUID) (persistence.MatchingLocalPreview, error) {
	r.calls++
	r.intent, r.digest, r.requestID = intent, digest, requestID
	return persistence.MatchingLocalPreview{GroupID: group, GroupRevision: intent.ExpectedGroupRevision, PreviewDigest: digest}, nil
}
func (*matchingLocalHTTPRepository) ListLocalReleases(context.Context) (persistence.MatchingLocalReleaseCatalog, error) {
	return persistence.MatchingLocalReleaseCatalog{Releases: []persistence.MatchingLocalRelease{{ID: uuid.MustParse("00000000-0000-0000-0000-000000000001"), Title: "Local"}}}, nil
}

func TestMatchingLocalRoutesAcceptExplicitLocalReleaseAndReceiptIdentity(t *testing.T) {
	repository := &matchingLocalHTTPRepository{}
	service := service.NewMatchingLocalService(repository)
	router := chi.NewRouter()
	api := New(router)
	registerMatchingLocal(api, service)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/local-releases", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "00000000-0000-0000-0000-000000000001") {
		t.Fatalf("local release catalog response %d: %s", response.Code, response.Body.String())
	}
	group, member, request, releaseID, trackID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	payload := map[string]any{"expected_group_revision": "r1", "expected_authority_revision": 0, "local_release_id": releaseID.String(), "release_title": "Operator title",
		"release_artists": []any{}, "tracks": []any{map[string]any{"local_track_id": trackID.String(), "medium": 1, "position": 1, "displayed_number": "A1", "title": "Track", "artists": []any{}, "file_member_ids": []string{member.String()}}}}
	encoded, _ := json.Marshal(payload)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/incoming-groups/"+group.String()+"/matching/local/preview", strings.NewReader(string(encoded))))
	if response.Code != http.StatusOK {
		t.Fatalf("preview status %d: %s", response.Code, response.Body.String())
	}
	if repository.calls != 1 || repository.intent.LocalReleaseID == nil || *repository.intent.LocalReleaseID != releaseID || repository.intent.ReleaseTitle != "Operator title" ||
		repository.intent.Tracks[0].LocalTrackID == nil || *repository.intent.Tracks[0].LocalTrackID != trackID || repository.intent.Tracks[0].FileMemberIDs[0] != member {
		t.Fatalf("preview intent not forwarded: %+v", repository)
	}
	payload["preview_digest"], payload["request_id"] = "digest", request.String()
	encoded, _ = json.Marshal(payload)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/incoming-groups/"+group.String()+"/matching/local/confirm", strings.NewReader(string(encoded))))
	if response.Code != http.StatusOK {
		t.Fatalf("confirm status %d: %s", response.Code, response.Body.String())
	}
	if repository.calls != 2 || repository.digest != "digest" || repository.requestID != request {
		t.Fatalf("confirm identity not forwarded: %+v", repository)
	}
}
