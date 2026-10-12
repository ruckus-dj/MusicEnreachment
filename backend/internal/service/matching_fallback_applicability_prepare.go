package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type matchingFallbackCalculationContextReader interface {
	LoadMatchingFallbackCalculationContext(context.Context, uuid.UUID, int64) (persistence.MatchingFallbackCalculationContext, bool, error)
}

type matchingFallbackCalculationReceiptWriter interface {
	RecordMatchingFallbackCalculationForDelivery(context.Context, uuid.UUID, int, int64, persistence.MatchingDeliveryClaim, int64, persistence.MatchingFallbackCalculationReceipt) (bool, error)
}

// PrepareFallbackApplicability loads one immutable persistence snapshot and
// passes an explicit service-owned conversion to the pure calculator.
func PrepareFallbackApplicability(
	ctx context.Context,
	reader matchingFallbackCalculationContextReader,
	runID uuid.UUID,
	expectedRevision int64,
) (MatchingFallbackApplicabilityResult, bool, error) {
	if reader == nil {
		return MatchingFallbackApplicabilityResult{}, false, fmt.Errorf("fallback applicability: context reader is unavailable")
	}
	snapshot, exists, err := reader.LoadMatchingFallbackCalculationContext(ctx, runID, expectedRevision)
	if err != nil || !exists {
		return MatchingFallbackApplicabilityResult{}, exists, err
	}
	input := fallbackApplicabilityInputFromContext(snapshot)
	result, err := CalculateFallbackApplicability(input)
	return result, true, err
}

// PrepareFallbackApplicabilityReceipt calculates and records stage-scoped evidence
// from the same validated persisted context. It intentionally leaves delivery
// consumption and acquisition advancement to their existing owners.
func PrepareFallbackApplicabilityReceipt(
	ctx context.Context,
	reader matchingFallbackCalculationContextReader,
	writer matchingFallbackCalculationReceiptWriter,
	runID uuid.UUID,
	attempt int,
	riverJobID int64,
	claim persistence.MatchingDeliveryClaim,
	expectedRevision int64,
) (MatchingFallbackApplicabilityResult, bool, error) {
	if reader == nil || writer == nil {
		return MatchingFallbackApplicabilityResult{}, false, fmt.Errorf("fallback applicability: receipt persistence is unavailable")
	}
	snapshot, exists, err := reader.LoadMatchingFallbackCalculationContext(ctx, runID, expectedRevision)
	if err != nil || !exists {
		return MatchingFallbackApplicabilityResult{}, exists, err
	}
	result, err := CalculateFallbackApplicability(fallbackApplicabilityInputFromContext(snapshot))
	if err != nil {
		return MatchingFallbackApplicabilityResult{}, true, err
	}
	digest, err := persistence.MatchingFallbackCalculationInputDigest(snapshot)
	if err != nil {
		return MatchingFallbackApplicabilityResult{}, true, err
	}
	contextJSON, err := json.Marshal(snapshot)
	if err != nil {
		return MatchingFallbackApplicabilityResult{}, true, fmt.Errorf("fallback applicability: serialize receipt context: %w", err)
	}
	edges := make([]matchingFallbackReceiptEdge, len(result.AllCandidates))
	for index, candidate := range result.AllCandidates {
		requestID := uuid.Nil
		for _, lookup := range snapshot.Lookups {
			if lookup.ReleaseID == candidate.ReleaseID {
				requestID = lookup.RequestID
				break
			}
		}
		if requestID == uuid.Nil {
			return MatchingFallbackApplicabilityResult{}, true, fmt.Errorf("fallback applicability: candidate lacks registered lookup authority")
		}
		edges[index] = matchingFallbackReceiptEdge{MemberID: candidate.MemberID, WorkID: candidate.FrozenWorkID,
			MetadataSHA: candidate.MetadataInputSHA, ReleaseID: candidate.ReleaseID, RecordingID: candidate.RecordingID,
			ProviderTrackID: candidate.ProviderTrackUUID, Medium: candidate.Medium, Position: candidate.Position,
			Profile: string(candidate.Score.Profile), Confidence: candidate.Score.Confidence, LookupRequestID: requestID,
			LookupPayloadSHA: candidate.LookupPayloadSHA, LookupProjectionSHA: candidate.LookupProjectionSHA}
	}
	edgesJSON, err := json.Marshal(edges)
	if err != nil {
		return MatchingFallbackApplicabilityResult{}, true, fmt.Errorf("fallback applicability: serialize receipt edges: %w", err)
	}
	recorded, err := writer.RecordMatchingFallbackCalculationForDelivery(ctx, runID, attempt, riverJobID, claim, expectedRevision,
		persistence.MatchingFallbackCalculationReceipt{RunID: runID, AcquisitionRevision: expectedRevision,
			StageIndex: snapshot.StageIndex, InputDigest: digest, Context: contextJSON, Edges: edgesJSON})
	return result, recorded, err
}

type matchingFallbackReceiptEdge struct {
	MemberID            uuid.UUID `json:"member_id"`
	WorkID              uuid.UUID `json:"work_id"`
	MetadataSHA         string    `json:"metadata_sha256"`
	ReleaseID           string    `json:"release_id"`
	RecordingID         string    `json:"recording_id"`
	ProviderTrackID     uuid.UUID `json:"provider_track_id"`
	Medium              int       `json:"medium"`
	Position            int       `json:"position"`
	Profile             string    `json:"profile"`
	Confidence          float64   `json:"confidence"`
	LookupRequestID     uuid.UUID `json:"lookup_request_id"`
	LookupPayloadSHA    string    `json:"lookup_payload_sha256"`
	LookupProjectionSHA string    `json:"lookup_projection_sha256"`
}

func fallbackApplicabilityInputFromContext(snapshot persistence.MatchingFallbackCalculationContext) MatchingFallbackApplicabilityInput {
	lookups := make([]MatchingFallbackLookupAuthority, len(snapshot.Lookups))
	for index, lookup := range snapshot.Lookups {
		lookups[index] = MatchingFallbackLookupAuthority{
			RequestID: lookup.RequestID, ReleaseID: lookup.ReleaseID, Kind: lookup.Kind,
			ProviderSourceID: lookup.ProviderSourceID, ProviderID: lookup.ProviderID,
			Endpoint: lookup.Endpoint, Configuration: lookup.Configuration,
			CacheKey: lookup.CacheKey, Input: append([]byte(nil), lookup.Input...),
		}
	}
	return MatchingFallbackApplicabilityInput{
		RunID: snapshot.RunID, AcquisitionRevision: snapshot.AcquisitionRevision, StageIndex: snapshot.StageIndex,
		Intent: snapshot.Intent, Target: snapshot.Target,
		PhaseInput: append([]byte(nil), snapshot.PhaseInput...), PhaseOutput: append([]byte(nil), snapshot.PhaseOutput...),
		PhaseProvenance: append([]byte(nil), snapshot.PhaseProvenance...), SearchReceipt: snapshot.SearchReceipt,
		LookupReceipts: snapshot.LookupReceipts,
		Authority: MatchingFallbackAuthority{
			RunSourceID: snapshot.RunSourceID, RunProviderID: snapshot.RunProviderID,
			RunEndpoint: snapshot.RunEndpoint, RunConfiguration: snapshot.RunConfiguration,
			SearchKind: snapshot.SearchKind, SearchCacheKey: snapshot.SearchCacheKey,
			SearchInput: append([]byte(nil), snapshot.SearchInput...), Lookups: lookups,
		},
	}
}
