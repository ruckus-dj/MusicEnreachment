package persistence

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

// CompleteFallbackHydrationForDelivery consumes one fallback lookup wake. It
// derives the expected lookup set from the registered phase manifest; callers
// cannot supply candidates or receipt evidence.
func (repository *SetupManagerRepository) CompleteFallbackHydrationForDelivery(
	ctx context.Context, client RiverInserter, runID uuid.UUID, attempt int, riverJobID int64,
	claim MatchingDeliveryClaim, expectedRevision int64,
) (bool, error) {
	snapshot, exists, err := repository.GetMatchingAcquisition(ctx, runID)
	if err != nil || !exists {
		return false, err
	}
	if snapshot.Intent.SchemaVersion != matchingAutomaticFallbackAcquisitionIntentVersion || snapshot.Phase != MatchingAcquisitionFallbackHydration || snapshot.Revision != expectedRevision {
		return false, fmt.Errorf("complete fallback hydration: registered phase changed")
	}
	var input struct {
		StageIndex              int             `json:"stage_index"`
		SearchRequestID         uuid.UUID       `json:"search_request_id"`
		OriginHydrationRevision int64           `json:"origin_hydration_revision"`
		ReleaseIDs              []string        `json:"release_ids"`
		SearchManifest          json.RawMessage `json:"search_manifest"`
	}
	var output struct {
		Lookups []struct {
			ReleaseID string    `json:"release_id"`
			RequestID uuid.UUID `json:"request_id"`
			CacheKey  string    `json:"cache_key"`
		} `json:"lookups"`
	}
	var searchProvenanceBytes []byte
	if err := repository.db.NewRaw(`SELECT response_provenance FROM matching_acquisition_phase WHERE run_id=? AND revision=?`, runID, expectedRevision).Scan(ctx, &searchProvenanceBytes); err != nil {
		return false, fmt.Errorf("complete fallback hydration: read search receipt reference: %w", err)
	}
	if json.Unmarshal(snapshot.Input, &input) != nil || json.Unmarshal(snapshot.Output, &output) != nil || input.StageIndex < 0 ||
		input.OriginHydrationRevision != expectedRevision || input.SearchRequestID == uuid.Nil ||
		len(input.ReleaseIDs) == 0 || len(input.ReleaseIDs) != len(output.Lookups) {
		return false, fmt.Errorf("complete fallback hydration: registered lookup manifest is invalid")
	}
	var searchProvenance []matchingAcquisitionResponseProvenance
	if json.Unmarshal(searchProvenanceBytes, &searchProvenance) != nil || len(searchProvenance) != 1 || searchProvenance[0].RequestID != input.SearchRequestID {
		return false, fmt.Errorf("complete fallback hydration: search receipt reference is invalid")
	}
	registration, err := loadFallbackRequestRegistration(ctx, repository.db, runID, input.StageIndex, "search", input.SearchRequestID)
	if err != nil || registration.RequestID != input.SearchRequestID {
		return false, fmt.Errorf("complete fallback hydration: registered search request is unavailable")
	}
	receipts := make([]map[string]any, 0, len(output.Lookups))
	for index, lookup := range output.Lookups {
		id, parseErr := uuid.Parse(lookup.ReleaseID)
		if parseErr != nil || id.String() != lookup.ReleaseID || lookup.ReleaseID != input.ReleaseIDs[index] || lookup.RequestID == uuid.Nil || lookup.CacheKey != "release-lookup:"+lookup.ReleaseID {
			return false, fmt.Errorf("complete fallback hydration: lookup descriptor is invalid")
		}
		receipt, readErr := getMatchingFallbackLookupReceipt(ctx, repository.db, runID, lookup.RequestID)
		if readErr != nil {
			return false, fmt.Errorf("complete fallback hydration: read immutable lookup receipt: %w", readErr)
		}
		if receipt == nil {
			// Earlier child-settlement wakes may arrive while later lookups are
			// pending. The fenced transaction consumes those wakes without treating
			// absence of an unfinished child's receipt as evidence.
			continue
		}
		if receipt.StageIndex != input.StageIndex || receipt.CacheKey != lookup.CacheKey {
			return false, fmt.Errorf("complete fallback hydration: immutable lookup receipt is mismatched")
		}
		receipts = append(receipts, map[string]any{"release_id": lookup.ReleaseID, "request_id": lookup.RequestID,
			"cache_key": lookup.CacheKey, "payload_sha256": receipt.PayloadSHA256, "projection_sha256": receipt.ProjectionSHA256})
	}
	phaseOutput, _ := json.Marshal(map[string]any{"stage_index": input.StageIndex, "search_request_id": input.SearchRequestID,
		"origin_hydration_revision": input.OriginHydrationRevision, "search_receipt": searchProvenance[0], "search_manifest": input.SearchManifest, "lookup_receipts": receipts})
	phase := MatchingAcquisitionPhase{Revision: expectedRevision + 1, Phase: MatchingAcquisitionFallbackApplicability,
		Input: snapshot.Input, Output: phaseOutput, ResponseProvenance: searchProvenanceBytes}
	return repository.advanceMatchingAcquisitionForDelivery(ctx, client, runID, attempt, riverJobID, claim, expectedRevision, phase, nil, nil)
}
