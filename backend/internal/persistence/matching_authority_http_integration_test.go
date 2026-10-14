//go:build integration

package persistence_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/api"
	"github.com/ruckus/MusicEnreachment/backend/internal/jobs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/uptrace/bun"
)

func TestMatchingAuthorityUnlinkAndReturnAutomaticOverHTTPPostgreSQL(t *testing.T) {
	testAutomaticFallbackFiniteFlowWithPublicationCallback(t, false, false, false, false, false, func(fixture uniquePublicationApplyFixture) {
		ctx, db, groupID := fixture.Context, fixture.Database, fixture.InputSnapshot.Authority.GroupID
		repository := persistence.NewIncomingMatchCandidateRepository(db)
		groups := service.NewIncomingGroups(persistence.NewIncomingGroupingRepository(db))
		handler := api.HandlerWithDependencies(api.Dependencies{
			IncomingGroups:    groups,
			MatchingReview:    service.NewMatchingReviewService(repository),
			MatchingManual:    service.NewMatchingManualService(repository),
			MatchingAuthority: service.NewMatchingAuthorityService(repository),
		})
		call := func(method, path string, payload any) *httptest.ResponseRecorder {
			t.Helper()
			var body bytes.Buffer
			if payload != nil {
				if err := json.NewEncoder(&body).Encode(payload); err != nil {
					t.Fatal(err)
				}
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(method, path, &body))
			return response
		}
		matchingPath := "/incoming-groups/" + groupID.String() + "/matching"
		var review service.MatchingReview
		response := call(http.MethodGet, matchingPath, nil)
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &review) != nil {
			t.Fatalf("initial matching review status=%d body=%s", response.Code, response.Body.String())
		}
		if len(review.Members) < 2 || len(review.Releases) == 0 {
			t.Fatalf("fixture review incomplete: %+v", review)
		}
		assignment := service.MatchingManualAssignment{FileMemberID: review.Members[0], ProviderTrackID: review.Releases[0].Positions[0].TrackID, Medium: review.Releases[0].Positions[0].Medium + 1, Position: review.Releases[0].Positions[0].Position + 1}
		manualBody := map[string]any{"expected_evaluation_revision": review.EvaluationRevision, "provider_release_id": review.Releases[0].ProviderReleaseID, "assignments": []service.MatchingManualAssignment{assignment}}
		previewResponse := call(http.MethodPost, "/incoming-groups/"+groupID.String()+"/matching/preview", manualBody)
		var preview service.MatchingManualPreview
		if previewResponse.Code != http.StatusOK || json.Unmarshal(previewResponse.Body.Bytes(), &preview) != nil {
			t.Fatalf("manual preview status=%d body=%s", previewResponse.Code, previewResponse.Body.String())
		}
		manualBody["preview_digest"], manualBody["request_id"] = preview.PreviewDigest, uuid.New()
		confirmed := call(http.MethodPost, "/incoming-groups/"+groupID.String()+"/matching/confirm", manualBody)
		if confirmed.Code != http.StatusOK {
			t.Fatalf("manual confirm status=%d body=%s", confirmed.Code, confirmed.Body.String())
		}
		var beforeHeader []byte
		if err := db.NewRaw(`SELECT jsonb_build_object('evaluation_revision',evaluation_revision,'evaluation_evidence',evaluation_evidence,'fence_version',fence_version,'target_run_id',target_run_id,'target_capture',target_capture,'target_capture_digest',encode(target_capture_digest,'hex')) FROM incoming_match_evaluation WHERE group_id=?`, groupID).Scan(ctx, &beforeHeader); err != nil {
			t.Fatal(err)
		}
		response = call(http.MethodGet, matchingPath, nil)
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &review) != nil || review.AuthorityMode != "manual" || len(review.CurrentLinkedManualMediaSourceIDs) != 1 {
			t.Fatalf("manual authority review status=%d review=%+v body=%s", response.Code, review, response.Body.String())
		}
		unlinkBody := map[string]any{"expected_group_revision": reviewGroupRevision(t, ctx, db, groupID), "expected_authority_revision": review.AuthorityRevision, "source_ids": review.CurrentLinkedManualMediaSourceIDs, "request_id": uuid.New()}
		unlinked := call(http.MethodPost, "/incoming-groups/"+groupID.String()+"/matching/unlink", unlinkBody)
		if unlinked.Code != http.StatusOK {
			t.Fatalf("unlink status=%d body=%s", unlinked.Code, unlinked.Body.String())
		}
		response = call(http.MethodGet, matchingPath, nil)
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &review) != nil || review.AuthorityMode != "manual" || len(review.CurrentLinkedManualMediaSourceIDs) != 0 {
			t.Fatalf("after unlink review status=%d review=%+v body=%s", response.Code, review, response.Body.String())
		}
		var afterHeader []byte
		if err := db.NewRaw(`SELECT jsonb_build_object('evaluation_revision',evaluation_revision,'evaluation_evidence',evaluation_evidence,'fence_version',fence_version,'target_run_id',target_run_id,'target_capture',target_capture,'target_capture_digest',encode(target_capture_digest,'hex')) FROM incoming_match_evaluation WHERE group_id=?`, groupID).Scan(ctx, &afterHeader); err != nil || !bytes.Equal(beforeHeader, afterHeader) {
			t.Fatalf("unlink changed frozen evaluation header: err=%v", err)
		}
		returnBody := map[string]any{"expected_group_revision": reviewGroupRevision(t, ctx, db, groupID), "expected_authority_revision": review.AuthorityRevision, "request_id": uuid.New()}
		returned := call(http.MethodPost, "/incoming-groups/"+groupID.String()+"/matching/return-automatic", returnBody)
		if returned.Code != http.StatusOK {
			t.Fatalf("return automatic status=%d body=%s", returned.Code, returned.Body.String())
		}
		var receipt service.MatchingReturnAutomaticResult
		if err := json.Unmarshal(returned.Body.Bytes(), &receipt); err != nil || receipt.RequestedGeneration < 1 {
			t.Fatalf("return receipt=%+v err=%v", receipt, err)
		}
		originalGroupRevision := reviewGroupRevision(t, ctx, db, groupID)
		if originalGroupRevision != receipt.GroupRevision {
			t.Fatalf("return receipt group revision=%q current=%q", receipt.GroupRevision, originalGroupRevision)
		}
		if err := groups.Refresh(ctx); err != nil {
			t.Fatalf("refresh incoming grouping after return: %v", err)
		}
		if refreshedGroupRevision := reviewGroupRevision(t, ctx, db, groupID); refreshedGroupRevision != originalGroupRevision {
			t.Fatalf("refresh changed group revision: before=%q after=%q", originalGroupRevision, refreshedGroupRevision)
		}
		var receiptsAfterRefresh int
		if err := db.NewRaw(`SELECT count(*) FROM incoming_group_return_automatic_receipt WHERE request_id=? AND group_id=?`, returnBody["request_id"], groupID).Scan(ctx, &receiptsAfterRefresh); err != nil || receiptsAfterRefresh != 1 {
			t.Fatalf("refresh did not preserve return receipt: count=%d err=%v", receiptsAfterRefresh, err)
		}
		var generationBeforeReplay int64
		if err := db.NewRaw(`SELECT requested_generation FROM incoming_matching_generation WHERE group_id=?`, groupID).Scan(ctx, &generationBeforeReplay); err != nil {
			t.Fatal(err)
		}
		replay := call(http.MethodPost, "/incoming-groups/"+groupID.String()+"/matching/return-automatic", returnBody)
		var replayReceipt service.MatchingReturnAutomaticResult
		var receiptFields, replayFields map[string]json.RawMessage
		if replay.Code != http.StatusOK || json.Unmarshal(replay.Body.Bytes(), &replayReceipt) != nil || json.Unmarshal(returned.Body.Bytes(), &receiptFields) != nil || json.Unmarshal(replay.Body.Bytes(), &replayFields) != nil || replayReceipt != receipt || !equalJSONFields(receiptFields, replayFields) {
			t.Fatalf("return replay status=%d result=%+v original=%+v body=%s", replay.Code, replayReceipt, receipt, replay.Body.String())
		}
		response = call(http.MethodGet, matchingPath, nil)
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &review) != nil || review.AuthorityMode != "automatic" || review.AuthorityRevision != receipt.AuthorityRevision {
			t.Fatalf("automatic authority review status=%d review=%+v", response.Code, review)
		}
		var requested, reconciled int64
		if err := db.NewRaw(`SELECT requested_generation,reconciled_generation FROM incoming_matching_generation WHERE group_id=?`, groupID).Scan(ctx, &requested, &reconciled); err != nil || requested != receipt.RequestedGeneration || requested != generationBeforeReplay {
			t.Fatalf("return replay changed request generation: requested=%d receipt=%d err=%v", requested, receipt.RequestedGeneration, err)
		}
		if fixture.ChildOperation.RiverJobID != nil {
			if _, err := db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, *fixture.ChildOperation.RiverJobID).Exec(ctx); err != nil {
				t.Fatalf("make original automatic child runnable: %v", err)
			}
			worker := jobs.NewMatchingApplyWorker(fixture.Repository, service.NewOperations(fixture.Repository))
			if err := worker.Work(ctx, &river.Job[persistence.MatchingApplyOperationArgs]{JobRow: &rivertype.JobRow{ID: *fixture.ChildOperation.RiverJobID, Attempt: 1}, Args: persistence.MatchingApplyOperationArgs{OperationID: fixture.ChildOperation.ID}}); err != nil {
				t.Fatalf("execute original frozen automatic child: %v", err)
			}
		}
		var manualLinks int
		if err := db.NewRaw(`SELECT count(*) FROM track_source source JOIN source_work_variant_identity identity ON identity.media_variant_id=source.media_variant_id JOIN incoming_group_member_location member ON member.work_id=identity.work_id WHERE member.group_id=? AND source.decision_method='manual'`, groupID).Scan(ctx, &manualLinks); err != nil || manualLinks != 0 {
			t.Fatalf("return restored old manual link count=%d err=%v", manualLinks, err)
		}
	}, true)
}

func equalJSONFields(left, right map[string]json.RawMessage) bool {
	if len(left) != len(right) {
		return false
	}
	for key, leftValue := range left {
		var leftDecoded, rightDecoded any
		if _, exists := right[key]; !exists || json.Unmarshal(leftValue, &leftDecoded) != nil || json.Unmarshal(right[key], &rightDecoded) != nil {
			return false
		}
		leftCanonical, leftErr := json.Marshal(leftDecoded)
		rightCanonical, rightErr := json.Marshal(rightDecoded)
		if leftErr != nil || rightErr != nil || !bytes.Equal(leftCanonical, rightCanonical) {
			return false
		}
	}
	return true
}

func reviewGroupRevision(t *testing.T, ctx context.Context, db *bun.DB, groupID uuid.UUID) string {
	t.Helper()
	var revision string
	if err := db.NewRaw(`SELECT revision FROM incoming_group WHERE id=?`, groupID).Scan(ctx, &revision); err != nil {
		t.Fatal(err)
	}
	return revision
}
