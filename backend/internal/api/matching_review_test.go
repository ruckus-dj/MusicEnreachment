package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type matchingReviewHTTPReader struct {
	snapshot *persistence.IncomingMatchReviewSnapshot
	calls    int
}

func (reader *matchingReviewHTTPReader) GetReviewSnapshot(context.Context, uuid.UUID) (*persistence.IncomingMatchReviewSnapshot, error) {
	reader.calls++
	return reader.snapshot, nil
}

func TestMatchingReviewRegisteredRouteReadsSavedSnapshotAndValidatesGroupID(t *testing.T) {
	groupID, memberID, trackID := uuid.New(), uuid.New(), uuid.New()
	proposal, err := json.Marshal(map[string]any{
		"review_proposal": map[string]any{"version": 1, "group_id": groupID, "files": []any{map[string]any{
			"file_member_id": memberID, "raw_tags": map[string][]string{}, "diagnostics": []any{},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	reader := &matchingReviewHTTPReader{snapshot: &persistence.IncomingMatchReviewSnapshot{
		Header:  &persistence.IncomingMatchEvaluationHeader{EvaluationRevision: "saved-revision", EvaluationEvidence: proposal},
		Members: []uuid.UUID{memberID},
		Candidates: []persistence.IncomingMatchCandidate{{FileMemberID: memberID, ProviderID: uuid.New(), ProviderSourceID: uuid.New(), ReleaseID: uuid.New(), Score: .8,
			Evidence:  json.RawMessage(`{"edge_evidence":[{"schema_version":1,"medium":1,"position":1,"assignment_profile":{"profile":"recording_release","confidence":0.6}}]}`),
			Positions: []persistence.IncomingMatchPosition{{TrackID: trackID, Medium: 1, Position: 1}}}},
	}}
	handler := HandlerWithDependencies(Dependencies{MatchingReview: service.NewMatchingReviewService(reader)})

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/incoming-groups/"+groupID.String()+"/matching", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("GET matching status = %d, body: %s", response.Code, response.Body.String())
	}
	for _, expected := range []string{`"evaluation_revision":"saved-revision"`, `"release_confidence":0.8`, `"assignment_confidence":0.6`} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("response missing %s: %s", expected, response.Body.String())
		}
	}
	if reader.calls != 1 {
		t.Fatalf("saved snapshot reads = %d, want 1", reader.calls)
	}

	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, httptest.NewRequest(http.MethodGet, "/incoming-groups/not-a-uuid/matching", nil))
	if invalid.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid group ID status = %d, want Huma validation status 422; body: %s", invalid.Code, invalid.Body.String())
	}
	if reader.calls != 1 {
		t.Fatalf("invalid group ID reached repository: calls = %d", reader.calls)
	}
}
