package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/policy"
)

const matchingCatalogPageSize = 100

var ErrInvalidMatchingAcquisitionResponse = errors.New("invalid accepted matching acquisition response")

// ErrMatchingAcquisitionProviderFailed fences the parent matching run when a
// provider child registered to it settled with a terminal failure. The parent
// must not read a failed dependency as a successful acquisition step.
var ErrMatchingAcquisitionProviderFailed = errors.New("matching acquisition provider dependency failed")

type matchingAcquisitionRepository interface {
	GetMatchingAcquisition(context.Context, uuid.UUID) (persistence.MatchingAcquisitionSnapshot, bool, error)
	AdvanceMatchingAcquisitionForDelivery(context.Context, persistence.RiverInserter, uuid.UUID, int, int64, persistence.MatchingDeliveryClaim, int64, persistence.MatchingAcquisitionPhase, []persistence.MatchingProviderRequest) (bool, error)
	ParkMatchingAcquisitionForDelivery(context.Context, uuid.UUID, int, int64, persistence.MatchingDeliveryClaim, int64, persistence.MatchingAcquisitionPhase) (bool, error)
}

type matchingAcquisitionDeliveryCurrentChecker interface {
	IsMatchingDeliveryCurrent(context.Context, uuid.UUID, int, int64, persistence.MatchingDeliveryClaim) (bool, error)
}

type matchingReleaseHydrationStarter interface {
	StartMatchingReleaseHydration(context.Context, persistence.RiverInserter, uuid.UUID, int64, persistence.MatchingAcquisitionPhase, []persistence.MatchingProviderRequest) (bool, error)
}

type matchingAcquisitionProvider interface {
	CachedResponse(context.Context, string) (*persistence.ProviderCachedResponse, error)
	PrepareOperation(context.Context, string, string, int, int, bool) (*persistence.Operation, error)
}

type matchingAcquisitionEvaluationSealReader interface {
	GetMatchingAcquisitionEvaluationSeal(context.Context, uuid.UUID) (persistence.MatchingAcquisitionEvaluationInput, bool, error)
}

type matchingAcquisitionSealWriter interface {
	SealMatchingAcquisitionEvaluation(context.Context, persistence.RiverInserter, uuid.UUID, persistence.MatchingAcquisitionSealRequest) (int64, bool, error)
}

type matchingAcquisitionCatalogKeyReader interface {
	GetMatchingAcquisitionCatalogCacheKeys(context.Context, uuid.UUID) ([]string, error)
}

type matchingAcquisitionCaptureReader interface {
	GetMatchingAcquisitionMetadataCapture(context.Context, uuid.UUID, string) ([]persistence.MatchingAcquisitionCapturedMember, string, bool, error)
}

// Catalog phase provenance is needed here because a provider cache scope may be
// replaced after admission. Ranking must consume the bytes accepted by each
// durable phase, not a newer response found under the same key.
type matchingAcquisitionCatalogPhaseReader interface {
	GetMatchingAcquisitionCatalogPhases(context.Context, uuid.UUID) ([]persistence.MatchingAcquisitionPhase, error)
}

type matchingFallbackSearchReceiptReader interface {
	GetMatchingFallbackSearchReceipt(context.Context, uuid.UUID, uuid.UUID) (*persistence.MatchingFallbackSearchReceipt, error)
}

type matchingFallbackSearchAdvancer interface {
	AdvanceFallbackSearchForDelivery(context.Context, persistence.RiverInserter, uuid.UUID, int, int64, persistence.MatchingDeliveryClaim, int64, persistence.MatchingFallbackSearchProof, []persistence.MatchingProviderRequest, json.RawMessage) (bool, error)
}

type matchingFallbackCalculationReceiptReader interface {
	GetMatchingFallbackCalculationReceipt(context.Context, uuid.UUID, int64) (*persistence.MatchingFallbackCalculationReceipt, error)
}

type matchingFallbackCalculationPreparer interface {
	LoadMatchingFallbackCalculationContext(context.Context, uuid.UUID, int64) (persistence.MatchingFallbackCalculationContext, bool, error)
	RecordMatchingFallbackCalculationForDelivery(context.Context, uuid.UUID, int, int64, persistence.MatchingDeliveryClaim, int64, persistence.MatchingFallbackCalculationReceipt) (bool, error)
}

type matchingFallbackApplicabilityAdvancer interface {
	AdvanceFallbackApplicabilityAndContinueForDelivery(context.Context, persistence.RiverInserter, uuid.UUID, int, int64, persistence.MatchingDeliveryClaim, int64, *persistence.MatchingProviderRequest) (bool, error)
}

type matchingFallbackHydrationCompleter interface {
	CompleteFallbackHydrationForDelivery(context.Context, persistence.RiverInserter, uuid.UUID, int, int64, persistence.MatchingDeliveryClaim, int64) (bool, error)
}

type matchingEmptyFallbackPlanCompleter interface {
	CompleteEmptyFallbackPlanAndContinueForDelivery(context.Context, persistence.RiverInserter, uuid.UUID, int, int64, persistence.MatchingDeliveryClaim, int64) (bool, error)
}

type matchingDeliveryReleaseHydrationStarter interface {
	StartMatchingReleaseHydrationForDelivery(context.Context, persistence.RiverInserter, uuid.UUID, int, int64, persistence.MatchingDeliveryClaim, int64, persistence.MatchingAcquisitionPhase, []persistence.MatchingProviderRequest) (bool, error)
}

// MatchingAcquisitionService advances only from accepted cached provider
// responses. It never performs network I/O inside a matching delivery.
type MatchingAcquisitionService struct {
	repository matchingAcquisitionRepository
	providers  matchingAcquisitionProvider
	river      persistence.RiverInserter
}

func NewMatchingAcquisitionService(repository matchingAcquisitionRepository, providers matchingAcquisitionProvider, river persistence.RiverInserter) *MatchingAcquisitionService {
	return &MatchingAcquisitionService{repository: repository, providers: providers, river: river}
}

// HydrateShortlist accepts an explicit caller-selected shortlist of catalog
// release group identities. It intentionally does not rank or choose releases.
func (acquisition *MatchingAcquisitionService) HydrateShortlist(ctx context.Context, runID uuid.UUID, expectedRevision int64, releaseIDs []string) (bool, error) {
	if acquisition == nil || acquisition.repository == nil || acquisition.providers == nil || acquisition.river == nil {
		return false, fmt.Errorf("matching acquisition service is unavailable")
	}
	if runID == uuid.Nil || len(releaseIDs) == 0 || len(releaseIDs) > 10 {
		return false, fmt.Errorf("matching release shortlist must contain between one and ten releases")
	}
	snapshot, exists, err := acquisition.repository.GetMatchingAcquisition(ctx, runID)
	if err != nil {
		return false, err
	}
	if !exists || snapshot.Phase != "catalog_ready" || snapshot.Revision != expectedRevision {
		return false, fmt.Errorf("matching acquisition catalog phase changed")
	}
	canonical := make([]string, len(releaseIDs))
	seen := make(map[string]bool, len(releaseIDs))
	for index, value := range releaseIDs {
		id, err := canonicalMBID(value)
		if err != nil || seen[id] {
			return false, fmt.Errorf("matching release shortlist identities must be canonical and unique")
		}
		canonical[index] = id
		seen[id] = true
	}
	sort.Strings(canonical)
	children := make([]persistence.MatchingProviderRequest, 0, len(canonical))
	lookups := make([]map[string]any, 0, len(canonical))
	for _, id := range canonical {
		child, err := acquisition.providers.PrepareOperation(ctx, "release_lookup", id, 0, 0, false)
		if err != nil {
			return false, err
		}
		if child.ProviderCacheKey == nil {
			return false, fmt.Errorf("prepared release lookup has no cache scope")
		}
		children = append(children, persistence.MatchingProviderRequest{Operation: child, Args: ProviderFetchOperationArgs{OperationID: child.ID}})
		lookups = append(lookups, map[string]any{"release_id": id, "request_id": child.ID, "cache_key": *child.ProviderCacheKey})
	}
	input, _ := json.Marshal(map[string]any{"release_ids": canonical})
	output, _ := json.Marshal(map[string]any{"lookups": lookups})
	phase := persistence.MatchingAcquisitionPhase{
		Revision: expectedRevision + 1, Phase: persistence.MatchingAcquisitionReleaseHydration,
		Input: input, Output: output, ResponseProvenance: json.RawMessage("[]"),
	}
	starter, ok := acquisition.repository.(matchingReleaseHydrationStarter)
	if !ok {
		return false, fmt.Errorf("matching release hydration persistence is unavailable")
	}
	return starter.StartMatchingReleaseHydration(ctx, acquisition.river, runID, expectedRevision, phase, children)
}

// EvaluationSeal returns only the persisted write-once handoff. It is separate
// from Advance so a sealed continuation cannot be mistaken for an acquisition
// page or decoded as the legacy caller-authored evaluation plan.
func (acquisition *MatchingAcquisitionService) EvaluationSeal(ctx context.Context, runID uuid.UUID) (persistence.MatchingAcquisitionEvaluationInput, bool, error) {
	if acquisition == nil || acquisition.repository == nil {
		return persistence.MatchingAcquisitionEvaluationInput{}, false, fmt.Errorf("matching acquisition service is unavailable")
	}
	snapshot, exists, err := acquisition.repository.GetMatchingAcquisition(ctx, runID)
	if err != nil || !exists {
		return persistence.MatchingAcquisitionEvaluationInput{}, false, err
	}
	if snapshot.Phase != "evaluation_sealed" {
		return persistence.MatchingAcquisitionEvaluationInput{}, false, nil
	}
	reader, ok := acquisition.repository.(matchingAcquisitionEvaluationSealReader)
	if !ok {
		return persistence.MatchingAcquisitionEvaluationInput{}, false, fmt.Errorf("matching acquisition seal reader is unavailable")
	}
	input, exists, err := reader.GetMatchingAcquisitionEvaluationSeal(ctx, runID)
	if err != nil {
		return persistence.MatchingAcquisitionEvaluationInput{}, false, err
	}
	if !exists {
		return persistence.MatchingAcquisitionEvaluationInput{}, false, fmt.Errorf("matching acquisition evaluation seal is missing")
	}
	return input, true, nil
}

func (acquisition *MatchingAcquisitionService) Advance(ctx context.Context, runID uuid.UUID, attempt int, riverJobID int64, claim persistence.MatchingDeliveryClaim) (bool, bool, error) {
	if acquisition == nil || acquisition.repository == nil || acquisition.providers == nil || acquisition.river == nil {
		return false, false, fmt.Errorf("matching acquisition service is unavailable")
	}
	snapshot, exists, err := acquisition.repository.GetMatchingAcquisition(ctx, runID)
	if err != nil || !exists {
		return false, exists, err
	}
	if checker, ok := acquisition.repository.(matchingAcquisitionDeliveryCurrentChecker); ok {
		current, err := checker.IsMatchingDeliveryCurrent(ctx, runID, attempt, riverJobID, claim)
		if err != nil {
			return false, true, err
		}
		if !current {
			return false, true, nil
		}
	}
	if snapshot.FailedProviderDependency {
		return false, true, ErrMatchingAcquisitionProviderFailed
	}
	if snapshot.Intent.SchemaVersion == 3 && snapshot.Phase == "waiting_policy" {
		var output struct {
			State  string `json:"state"`
			Reason string `json:"reason"`
		}
		if json.Unmarshal(snapshot.Output, &output) != nil || output.State != "waiting_policy" || output.Reason != "fallback_plan_empty" || len(snapshot.Intent.FallbackQueryPlan) == 0 || len(snapshot.Intent.InitialProviderInputs) != 0 {
			return false, true, fmt.Errorf("saved empty fallback plan disposition is invalid")
		}
		completer, ok := acquisition.repository.(matchingEmptyFallbackPlanCompleter)
		if !ok {
			return false, true, fmt.Errorf("empty fallback plan persistence is unavailable")
		}
		changed, err := completer.CompleteEmptyFallbackPlanAndContinueForDelivery(ctx, acquisition.river, runID, attempt, riverJobID, claim, snapshot.Revision)
		return changed, true, err
	}
	if snapshot.Intent.SchemaVersion == 3 && snapshot.Phase == "fallback_search" {
		return acquisition.advanceFallbackSearch(ctx, snapshot, runID, attempt, riverJobID, claim)
	}
	if snapshot.Intent.SchemaVersion == 3 && snapshot.Phase == persistence.MatchingAcquisitionFallbackHydration {
		completer, ok := acquisition.repository.(matchingFallbackHydrationCompleter)
		if !ok {
			return false, true, fmt.Errorf("fallback hydration persistence is unavailable")
		}
		changed, err := completer.CompleteFallbackHydrationForDelivery(ctx, acquisition.river, runID, attempt, riverJobID, claim, snapshot.Revision)
		return changed, true, err
	}
	if snapshot.Phase == "catalog_ready" {
		return acquisition.rankAcceptedCatalog(ctx, snapshot, runID, attempt, riverJobID, claim)
	}
	if snapshot.Phase == persistence.MatchingAcquisitionReleaseHydration {
		return acquisition.sealReleaseHydration(ctx, snapshot, runID, attempt, riverJobID, claim)
	}
	if snapshot.Phase == persistence.MatchingAcquisitionFallbackApplicability {
		return acquisition.advanceFallbackApplicability(ctx, snapshot, runID, attempt, riverJobID, claim)
	}
	if snapshot.Phase == "admitted" {
		return acquisition.startArtistBrowse(ctx, snapshot, runID, attempt, riverJobID, claim)
	}
	if snapshot.Phase != persistence.MatchingAcquisitionArtistCatalogPage {
		return false, true, fmt.Errorf("matching acquisition phase is unsupported")
	}
	return acquisition.advanceCatalogPage(ctx, snapshot, runID, attempt, riverJobID, claim)
}

// advanceFallbackSearch derives only release lookup requests from the immutable
// receipt for stage zero. Ranking and applicability remain later engine stages.
func (acquisition *MatchingAcquisitionService) advanceFallbackSearch(ctx context.Context, snapshot persistence.MatchingAcquisitionSnapshot, runID uuid.UUID, attempt int, riverJobID int64, claim persistence.MatchingDeliveryClaim) (bool, bool, error) {
	var phaseInput, phaseOutput struct {
		StageIndex int       `json:"stage_index"`
		RequestID  uuid.UUID `json:"request_id"`
	}
	if len(snapshot.Input) > 0 && json.Unmarshal(snapshot.Input, &phaseInput) != nil ||
		json.Unmarshal(snapshot.Output, &phaseOutput) != nil {
		return false, true, fmt.Errorf("saved fallback search stage is invalid")
	}
	stageIndex := phaseInput.StageIndex
	if stageIndex == 0 {
		stageIndex = phaseOutput.StageIndex
	}
	if stageIndex < 0 {
		return false, true, fmt.Errorf("saved fallback search stage is invalid")
	}
	var searchID uuid.UUID
	if stageIndex == 0 {
		if len(snapshot.Intent.InitialProviderInputs) != 1 {
			return false, true, fmt.Errorf("saved fallback search intent is invalid")
		}
		searchID = snapshot.Intent.InitialProviderInputs[0].OperationID
	} else {
		if phaseOutput.RequestID == uuid.Nil {
			return false, true, fmt.Errorf("saved fallback stage request is invalid")
		}
		searchID = phaseOutput.RequestID
	}
	reader, ok := acquisition.repository.(matchingFallbackSearchReceiptReader)
	if !ok {
		return false, true, fmt.Errorf("fallback search receipt reader is unavailable")
	}
	receipt, err := reader.GetMatchingFallbackSearchReceipt(ctx, runID, searchID)
	if err != nil {
		return false, true, err
	}
	if receipt == nil || receipt.StageIndex != stageIndex || receipt.RunID != runID || receipt.RequestID != searchID {
		return false, true, fmt.Errorf("immutable fallback search receipt is unavailable or mismatched")
	}
	var plan policy.TypedFallbackQueryPlan
	if json.Unmarshal(snapshot.Intent.FallbackQueryPlan, &plan) != nil || stageIndex >= len(plan.Stages) {
		return false, true, fmt.Errorf("saved fallback query plan stage is invalid")
	}
	kind, _, valid := policy.ProviderKindForFallbackStage(plan.Stages[stageIndex].Kind)
	if !valid {
		return false, true, fmt.Errorf("saved fallback query plan kind is invalid")
	}
	ids, manifest, err := fallbackSearchManifest(kind, receipt.Payload)
	if err != nil {
		return false, true, fmt.Errorf("accepted fallback search response is invalid: %w: %v", ErrInvalidMatchingAcquisitionResponse, err)
	}
	children := make([]persistence.MatchingProviderRequest, 0, len(ids))
	for _, id := range ids {
		child, err := acquisition.providers.PrepareOperation(ctx, "release_lookup", id, 0, 0, false)
		if err != nil {
			return false, true, err
		}
		if child.ProviderCacheKey == nil {
			return false, true, fmt.Errorf("prepared release lookup has no cache scope")
		}
		children = append(children, persistence.MatchingProviderRequest{Operation: child, Args: ProviderFetchOperationArgs{OperationID: child.ID}})
	}
	advancer, ok := acquisition.repository.(matchingFallbackSearchAdvancer)
	if !ok {
		return false, true, fmt.Errorf("fallback search delivery persistence is unavailable")
	}
	changed, err := advancer.AdvanceFallbackSearchForDelivery(ctx, acquisition.river, runID, attempt, riverJobID, claim, snapshot.Revision,
		persistence.MatchingFallbackSearchProof{StageIndex: stageIndex, RequestID: receipt.RequestID, PayloadSHA256: receipt.PayloadSHA256}, children, manifest)
	return changed, true, err
}

func (acquisition *MatchingAcquisitionService) advanceFallbackApplicability(ctx context.Context, snapshot persistence.MatchingAcquisitionSnapshot, runID uuid.UUID, attempt int, riverJobID int64, claim persistence.MatchingDeliveryClaim) (bool, bool, error) {
	receiptReader, ok := acquisition.repository.(matchingFallbackCalculationReceiptReader)
	if !ok {
		return false, true, fmt.Errorf("fallback calculation receipt reader is unavailable")
	}
	receipt, err := receiptReader.GetMatchingFallbackCalculationReceipt(ctx, runID, snapshot.Revision)
	if err != nil {
		return false, true, err
	}
	if receipt == nil {
		preparer, ok := acquisition.repository.(matchingFallbackCalculationPreparer)
		if !ok {
			return false, true, fmt.Errorf("fallback calculation persistence is unavailable")
		}
		_, recorded, err := PrepareFallbackApplicabilityReceipt(ctx, preparer, preparer, runID, attempt, riverJobID, claim, snapshot.Revision)
		if err != nil || !recorded {
			return false, true, err
		}
		receipt, err = receiptReader.GetMatchingFallbackCalculationReceipt(ctx, runID, snapshot.Revision)
		if err != nil {
			return false, true, err
		}
	}
	if receipt == nil || receipt.RunID != runID || receipt.AcquisitionRevision != snapshot.Revision {
		return false, true, fmt.Errorf("saved fallback calculation receipt is unavailable or mismatched")
	}
	var edges []struct {
		Confidence float64 `json:"confidence"`
	}
	if err := json.Unmarshal(receipt.Edges, &edges); err != nil {
		return false, true, fmt.Errorf("saved fallback calculation edges are invalid: %w", err)
	}
	qualified := false
	for _, edge := range edges {
		if edge.Confidence >= 0.70 {
			qualified = true
			break
		}
	}
	var prepared *persistence.MatchingProviderRequest
	if !qualified {
		var plan struct {
			Stages []struct {
				Kind, Query   string
				Offset, Limit int
			} `json:"stages"`
		}
		if err := json.Unmarshal(snapshot.Intent.FallbackQueryPlan, &plan); err != nil || receipt.StageIndex < 0 || receipt.StageIndex >= len(plan.Stages) {
			return false, true, fmt.Errorf("saved fallback plan stage is invalid")
		}
		if receipt.StageIndex+1 < len(plan.Stages) {
			stage := plan.Stages[receipt.StageIndex+1]
			kind, _, valid := fallbackProviderKind(stage.Kind)
			if !valid || stage.Offset != 0 || stage.Limit != 10 {
				return false, true, fmt.Errorf("saved next fallback stage is invalid")
			}
			operation, err := acquisition.providers.PrepareOperation(ctx, kind, stage.Query, 0, 10, false)
			if err != nil {
				return false, true, err
			}
			if operation.ProviderCacheKey == nil {
				return false, true, fmt.Errorf("prepared fallback search has no cache scope")
			}
			request := persistence.MatchingProviderRequest{Operation: operation, Args: ProviderFetchOperationArgs{OperationID: operation.ID}}
			prepared = &request
		}
	}
	advancer, ok := acquisition.repository.(matchingFallbackApplicabilityAdvancer)
	if !ok {
		return false, true, fmt.Errorf("fallback applicability delivery persistence is unavailable")
	}
	changed, err := advancer.AdvanceFallbackApplicabilityAndContinueForDelivery(ctx, acquisition.river, runID, attempt, riverJobID, claim, snapshot.Revision, prepared)
	return changed, true, err
}

func fallbackProviderKind(stage string) (string, string, bool) {
	switch stage {
	case "album", "album_track":
		return "release_search", stage, true
	case "track":
		return "recording_search", stage, true
	default:
		return "", "", false
	}
}

// fallbackSearchReleaseIDs validates the MusicBrainz search envelope and returns
// every distinct release identity, not merely the number of recording hits.
func fallbackSearchManifest(kind string, payload json.RawMessage) ([]string, json.RawMessage, error) {
	if len(payload) == 0 {
		return nil, nil, fmt.Errorf("payload is missing")
	}
	var envelope struct {
		Count      *int `json:"count"`
		Offset     *int `json:"offset"`
		Recordings *[]struct {
			ID       string `json:"id"`
			Releases *[]struct {
				ID string `json:"id"`
			} `json:"releases"`
		} `json:"recordings"`
		Releases *[]struct {
			ID string `json:"id"`
		} `json:"releases"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil || envelope.Count == nil || envelope.Offset == nil || *envelope.Count < 0 || *envelope.Offset != 0 {
		return nil, nil, fmt.Errorf("search envelope is malformed")
	}
	ids := make(map[string]struct{})
	add := func(value string) error {
		id, err := canonicalMBID(value)
		if err != nil {
			return fmt.Errorf("search result contains an invalid MBID")
		}
		ids[id] = struct{}{}
		return nil
	}
	switch kind {
	case "provider_release_search":
		if envelope.Releases == nil || len(*envelope.Releases) > 10 || *envelope.Count < len(*envelope.Releases) {
			return nil, nil, fmt.Errorf("release search result list is invalid")
		}
		for _, hit := range *envelope.Releases {
			if err := add(hit.ID); err != nil {
				return nil, nil, err
			}
		}
	case "provider_recording_search":
		if envelope.Recordings == nil || len(*envelope.Recordings) > 10 || *envelope.Count < len(*envelope.Recordings) {
			return nil, nil, fmt.Errorf("recording search result list is invalid")
		}
		for _, hit := range *envelope.Recordings {
			if _, err := canonicalMBID(hit.ID); err != nil {
				return nil, nil, fmt.Errorf("recording result identity is invalid")
			}
			if hit.Releases == nil {
				return nil, nil, fmt.Errorf("recording release list is missing")
			}
			for _, release := range *hit.Releases {
				if err := add(release.ID); err != nil {
					return nil, nil, err
				}
			}
		}
	default:
		return nil, nil, fmt.Errorf("unsupported search operation")
	}
	result := make([]string, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	sort.Strings(result)
	manifest, err := json.Marshal(map[string]any{"kind": kind, "accepted_hits": json.RawMessage(payload), "release_ids": result})
	if err != nil {
		return nil, nil, err
	}
	return result, manifest, nil
}

func (acquisition *MatchingAcquisitionService) sealReleaseHydration(ctx context.Context, snapshot persistence.MatchingAcquisitionSnapshot, runID uuid.UUID, attempt int, riverJobID int64, claim persistence.MatchingDeliveryClaim) (bool, bool, error) {
	var phaseInput struct {
		ReleaseIDs []string `json:"release_ids"`
	}
	var phaseOutput struct {
		Lookups []struct {
			ReleaseID string    `json:"release_id"`
			RequestID uuid.UUID `json:"request_id"`
			CacheKey  string    `json:"cache_key"`
		} `json:"lookups"`
	}
	if json.Unmarshal(snapshot.Input, &phaseInput) != nil || json.Unmarshal(snapshot.Output, &phaseOutput) != nil || len(phaseInput.ReleaseIDs) == 0 || len(phaseInput.ReleaseIDs) > 10 || len(phaseInput.ReleaseIDs) != len(phaseOutput.Lookups) {
		return false, true, fmt.Errorf("saved matching release hydration manifest is invalid")
	}
	lookupKeys := make([]string, 0, len(phaseOutput.Lookups))
	lookupDigests := make(map[string]string, len(phaseOutput.Lookups))
	for index, lookup := range phaseOutput.Lookups {
		id, err := canonicalMBID(lookup.ReleaseID)
		if err != nil || id != lookup.ReleaseID || id != phaseInput.ReleaseIDs[index] || lookup.RequestID == uuid.Nil || lookup.CacheKey != "release-lookup:"+id {
			return false, true, fmt.Errorf("saved matching release lookup descriptor is invalid")
		}
		response, err := acquisition.providers.CachedResponse(ctx, lookup.CacheKey)
		if err != nil {
			return false, true, err
		}
		if response == nil || len(response.Payload) == 0 {
			return false, true, fmt.Errorf("accepted selected release response is unavailable: %w", ErrInvalidMatchingAcquisitionResponse)
		}
		var release struct {
			ID    string `json:"id"`
			Media *[]struct {
				Tracks *[]json.RawMessage `json:"tracks"`
			} `json:"media"`
		}
		if json.Unmarshal(response.Payload, &release) != nil || release.ID != id || release.Media == nil {
			return false, true, fmt.Errorf("accepted selected release response is malformed: %w", ErrInvalidMatchingAcquisitionResponse)
		}
		for _, medium := range *release.Media {
			if medium.Tracks == nil {
				return false, true, fmt.Errorf("accepted selected release track list is unavailable: %w", ErrInvalidMatchingAcquisitionResponse)
			}
		}
		lookupKeys = append(lookupKeys, lookup.CacheKey)
		payloadDigest := sha256.Sum256(response.Payload)
		lookupDigests[lookup.CacheKey] = fmt.Sprintf("%x", payloadDigest[:])
	}
	groupIDs := make([]uuid.UUID, 0, len(snapshot.Intent.Targets))
	for _, target := range snapshot.Intent.Targets {
		groupIDs = append(groupIDs, target.GroupID)
	}
	if len(groupIDs) == 0 {
		return false, true, fmt.Errorf("saved matching acquisition has no target groups")
	}
	keyReader, ok := acquisition.repository.(matchingAcquisitionCatalogKeyReader)
	if !ok {
		return false, true, fmt.Errorf("matching acquisition catalog key reader is unavailable")
	}
	catalogKeys, err := keyReader.GetMatchingAcquisitionCatalogCacheKeys(ctx, runID)
	if err != nil {
		return false, true, err
	}
	input := persistence.MatchingAcquisitionEvaluationInput{
		SchemaVersion: 2, GroupIDs: groupIDs, ReleaseIDs: append([]string(nil), phaseInput.ReleaseIDs...), CatalogCacheKeys: catalogKeys,
		ReleaseLookupCacheKeys: lookupKeys, ReleaseLookupPayloadSHA256: lookupDigests,
	}
	writer, ok := acquisition.repository.(matchingAcquisitionSealWriter)
	if !ok {
		return false, true, fmt.Errorf("matching acquisition seal writer is unavailable")
	}
	_, changed, err := writer.SealMatchingAcquisitionEvaluation(ctx, acquisition.river, runID, persistence.MatchingAcquisitionSealRequest{
		ExpectedRevision: snapshot.Revision, Input: input, Delivery: &claim, RiverJobID: riverJobID, Attempt: attempt,
	})
	return changed, true, err
}

func (acquisition *MatchingAcquisitionService) startArtistBrowse(ctx context.Context, snapshot persistence.MatchingAcquisitionSnapshot, runID uuid.UUID, attempt int, riverJobID int64, claim persistence.MatchingDeliveryClaim) (bool, bool, error) {
	if len(snapshot.Intent.InitialProviderInputs) != 1 || snapshot.Intent.InitialProviderInputs[0].Kind != "provider_artist_search" {
		return false, true, fmt.Errorf("matching acquisition initial artist search is invalid")
	}
	initial := snapshot.Intent.InitialProviderInputs[0]
	response, err := acquisition.providers.CachedResponse(ctx, initial.CacheKey)
	if err != nil {
		return false, true, err
	}
	if response == nil || len(response.Payload) == 0 {
		return false, true, fmt.Errorf("accepted artist search response is unavailable")
	}
	var page struct {
		Artists *[]struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"artists"`
	}
	if err := json.Unmarshal(response.Payload, &page); err != nil {
		return false, true, fmt.Errorf("accepted artist search payload is invalid: %w: %v", ErrInvalidMatchingAcquisitionResponse, err)
	}
	if page.Artists == nil {
		return false, true, fmt.Errorf("accepted artist search payload is invalid: %w", ErrInvalidMatchingAcquisitionResponse)
	}
	if len(*page.Artists) == 0 {
		input := json.RawMessage(initial.Input)
		output, _ := json.Marshal(map[string]string{"state": "waiting_policy", "reason": "artist_search_no_results"})
		changed, err := acquisition.repository.ParkMatchingAcquisitionForDelivery(ctx, runID, attempt, riverJobID, claim, snapshot.Revision,
			acquisitionPhase(snapshot.Revision+1, "waiting_policy", input, output, initial.OperationID, initial.CacheKey, response.Payload))
		return changed, true, err
	}
	if strings.TrimSpace((*page.Artists)[0].Name) == "" {
		return false, true, fmt.Errorf("accepted artist search top result is invalid: %w", ErrInvalidMatchingAcquisitionResponse)
	}
	artistID, err := canonicalMBID((*page.Artists)[0].ID)
	if err != nil {
		return false, true, fmt.Errorf("accepted artist search top result is invalid: %w: %v", ErrInvalidMatchingAcquisitionResponse, err)
	}
	capture, err := acquisition.validatedCapture(ctx, runID, "admitted")
	if err != nil {
		return false, true, err
	}
	fit, available, err := MatchingArtistFit(capture, (*page.Artists)[0].Name)
	if err != nil {
		return false, true, err
	}
	if !available || fit < 0.70 {
		input := json.RawMessage(initial.Input)
		output, _ := json.Marshal(map[string]string{"state": "waiting_policy", "reason": "artist_not_fit"})
		changed, err := acquisition.repository.ParkMatchingAcquisitionForDelivery(ctx, runID, attempt, riverJobID, claim, snapshot.Revision,
			acquisitionPhase(snapshot.Revision+1, "waiting_policy", input, output, initial.OperationID, initial.CacheKey, response.Payload))
		return changed, true, err
	}
	input, _ := json.Marshal(map[string]any{"artist_mbid": artistID, "offset": 0, "limit": matchingCatalogPageSize})
	child, err := acquisition.providers.PrepareOperation(ctx, "artist_release_browse", artistID, 0, matchingCatalogPageSize, false)
	if err != nil {
		return false, true, err
	}
	output, _ := json.Marshal(map[string]any{"artist_mbid": artistID, "next_offset": 0, "complete": false, "next_request_id": child.ID, "next_cache_key": *child.ProviderCacheKey})
	changed, err := acquisition.repository.AdvanceMatchingAcquisitionForDelivery(ctx, acquisition.river, runID, attempt, riverJobID, claim, snapshot.Revision,
		acquisitionPhase(snapshot.Revision+1, persistence.MatchingAcquisitionArtistCatalogPage, input, output, initial.OperationID, initial.CacheKey, response.Payload),
		[]persistence.MatchingProviderRequest{{Operation: child, Args: ProviderFetchOperationArgs{OperationID: child.ID}}})
	return changed, true, err
}

func (acquisition *MatchingAcquisitionService) validatedCapture(ctx context.Context, runID uuid.UUID, phase string) ([]persistence.MatchingAcquisitionCapturedMember, error) {
	reader, ok := acquisition.repository.(matchingAcquisitionCaptureReader)
	if !ok {
		return nil, fmt.Errorf("matching acquisition metadata capture reader is unavailable")
	}
	captured, digest, exists, err := reader.GetMatchingAcquisitionMetadataCapture(ctx, runID, phase)
	if err != nil {
		return nil, err
	}
	if !exists || digest == "" || len(captured) == 0 {
		return nil, fmt.Errorf("validated matching acquisition metadata capture is unavailable")
	}
	return captured, nil
}

func (acquisition *MatchingAcquisitionService) rankAcceptedCatalog(ctx context.Context, snapshot persistence.MatchingAcquisitionSnapshot, runID uuid.UUID, attempt int, riverJobID int64, claim persistence.MatchingDeliveryClaim) (bool, bool, error) {
	if len(snapshot.Intent.Targets) != 1 {
		return false, true, fmt.Errorf("automatic matching acquisition currently requires exactly one target group")
	}
	var manifest struct {
		ArtistMBID string   `json:"artist_mbid"`
		Complete   bool     `json:"complete"`
		Count      *int     `json:"catalog_count"`
		Covered    *int     `json:"covered_releases"`
		ReleaseIDs []string `json:"release_ids"`
	}
	if json.Unmarshal(snapshot.Output, &manifest) != nil || canonicalArtistCursor(manifest.ArtistMBID, 0) != nil || !manifest.Complete || manifest.Count == nil || manifest.Covered == nil || *manifest.Count < 0 || *manifest.Count != *manifest.Covered {
		return false, true, fmt.Errorf("saved matching catalog manifest is invalid")
	}
	keysReader, ok := acquisition.repository.(matchingAcquisitionCatalogKeyReader)
	if !ok {
		return false, true, fmt.Errorf("matching acquisition catalog key reader is unavailable")
	}
	keys, err := keysReader.GetMatchingAcquisitionCatalogCacheKeys(ctx, runID)
	if err != nil {
		return false, true, err
	}
	phaseReader, ok := acquisition.repository.(matchingAcquisitionCatalogPhaseReader)
	if !ok {
		return false, true, fmt.Errorf("matching acquisition catalog phase provenance reader is unavailable")
	}
	phases, err := phaseReader.GetMatchingAcquisitionCatalogPhases(ctx, runID)
	if err != nil {
		return false, true, err
	}
	// The first artist_catalog_page transition records the accepted artist-search
	// response that admitted the first browse request. It is provenance for the
	// transition, not a catalog page. Exclude only that exact request captured in
	// the immutable admission intent; every browse response remains mandatory.
	if len(snapshot.Intent.InitialProviderInputs) != 1 || snapshot.Intent.InitialProviderInputs[0].Kind != "provider_artist_search" ||
		snapshot.Intent.InitialProviderInputs[0].OperationID == uuid.Nil || snapshot.Intent.InitialProviderInputs[0].CacheKey == "" {
		return false, true, fmt.Errorf("matching acquisition initial artist search intent is invalid")
	}
	initialSearchKey := snapshot.Intent.InitialProviderInputs[0].CacheKey
	searchProvenanceCount := 0
	filteredPhases := make([]persistence.MatchingAcquisitionPhase, 0, len(phases))
	for _, phase := range phases {
		var provenance []struct {
			CacheKey string `json:"cache_key"`
		}
		if json.Unmarshal(phase.ResponseProvenance, &provenance) != nil || len(provenance) != 1 {
			return false, true, fmt.Errorf("accepted matching catalog page provenance is invalid")
		}
		if phase.Revision == 1 && phase.Phase == persistence.MatchingAcquisitionArtistCatalogPage && provenance[0].CacheKey == initialSearchKey {
			searchProvenanceCount++
			continue
		}
		filteredPhases = append(filteredPhases, phase)
	}
	if searchProvenanceCount != 1 {
		return false, true, fmt.Errorf("accepted initial artist search provenance is missing or duplicated")
	}
	phases = filteredPhases
	filteredKeys := make([]string, 0, len(keys))
	initialSearchKeyCount := 0
	for _, key := range keys {
		if key == initialSearchKey {
			initialSearchKeyCount++
			continue
		}
		filteredKeys = append(filteredKeys, key)
	}
	if initialSearchKeyCount != 1 {
		return false, true, fmt.Errorf("accepted initial artist search cache scope is missing or duplicated")
	}
	keys = filteredKeys
	if len(keys) == 0 || len(keys) != len(phases) {
		return false, true, fmt.Errorf("accepted matching catalog pages do not match their phase ledger")
	}
	phasesByKey := make(map[string]persistence.MatchingAcquisitionPhase, len(phases))
	readyPhases := 0
	for _, phase := range phases {
		if phase.Phase == "catalog_ready" {
			readyPhases++
			if phase.Revision != snapshot.Revision || !bytes.Equal(phase.Output, snapshot.Output) {
				return false, true, fmt.Errorf("accepted matching catalog manifest differs from its completion phase")
			}
		}
		var provenance []struct {
			CacheKey string `json:"cache_key"`
		}
		if json.Unmarshal(phase.ResponseProvenance, &provenance) != nil || len(provenance) != 1 || provenance[0].CacheKey == "" {
			return false, true, fmt.Errorf("accepted matching catalog page provenance is invalid")
		}
		if _, exists := phasesByKey[provenance[0].CacheKey]; exists {
			return false, true, fmt.Errorf("accepted matching catalog page provenance is duplicated")
		}
		phasesByKey[provenance[0].CacheKey] = phase
	}
	if readyPhases != 1 {
		return false, true, fmt.Errorf("accepted matching catalog must have one completion phase")
	}
	type catalogPage struct {
		Count    *int `json:"release-count"`
		Offset   *int `json:"release-offset"`
		Releases *[]struct {
			ID             string `json:"id"`
			Title          string `json:"title"`
			Disambiguation string `json:"disambiguation"`
			Date           string `json:"date"`
			ArtistCredit   []struct {
				Name       string `json:"name"`
				JoinPhrase string `json:"joinphrase"`
			} `json:"artist-credit"`
			LabelInfo []struct {
				CatalogNumber string `json:"catalog-number"`
			} `json:"label-info"`
		} `json:"releases"`
	}
	type verifiedCatalogPage struct {
		offset  int
		count   int
		length  int
		payload []byte
	}
	ids := make([]string, 0, len(manifest.ReleaseIDs))
	candidatesByID := make(map[string]MatchingRetrievalCandidate, len(manifest.ReleaseIDs))
	verifiedPages := make(map[string]verifiedCatalogPage, len(keys))
	for _, id := range manifest.ReleaseIDs {
		canonical, err := canonicalMBID(id)
		if err != nil || canonical != id {
			return false, true, fmt.Errorf("saved matching catalog manifest contains a noncanonical release id")
		}
		ids = append(ids, id)
	}
	if len(ids) != *manifest.Count {
		return false, true, fmt.Errorf("saved matching catalog manifest count is inconsistent")
	}
	seenKeys := make(map[string]bool, len(keys))
	for _, key := range keys {
		phase, exists := phasesByKey[key]
		if seenKeys[key] || !exists || phase.Phase != persistence.MatchingAcquisitionArtistCatalogPage && phase.Phase != "catalog_ready" {
			return false, true, fmt.Errorf("accepted matching catalog page ledger is invalid")
		}
		seenKeys[key] = true
		var provenance []struct {
			CacheKey   string `json:"cache_key"`
			PayloadSHA string `json:"payload_sha256"`
		}
		if json.Unmarshal(phase.ResponseProvenance, &provenance) != nil || len(provenance) != 1 || provenance[0].CacheKey != key || provenance[0].PayloadSHA == "" {
			return false, true, fmt.Errorf("accepted matching catalog page provenance is invalid")
		}
		response, err := acquisition.providers.CachedResponse(ctx, key)
		if err != nil {
			return false, true, err
		}
		if response == nil || len(response.Payload) == 0 {
			return false, true, fmt.Errorf("accepted matching catalog page is unavailable")
		}
		digest := sha256.Sum256(response.Payload)
		if fmt.Sprintf("%x", digest[:]) != provenance[0].PayloadSHA {
			return false, true, fmt.Errorf("accepted matching catalog page payload changed")
		}
		var page catalogPage
		if err := json.Unmarshal(response.Payload, &page); err != nil {
			return false, true, fmt.Errorf("accepted matching catalog page is malformed: %w: %v", ErrInvalidMatchingAcquisitionResponse, err)
		}
		if page.Count == nil || page.Offset == nil || page.Releases == nil || *page.Count != *manifest.Count || *page.Offset < 0 || len(*page.Releases) == 0 && *page.Offset < *page.Count || *page.Offset+len(*page.Releases) > *page.Count {
			return false, true, fmt.Errorf("accepted matching catalog page is malformed (count=%v offset=%v releases=%v manifest_count=%d): %w", page.Count, page.Offset, page.Releases != nil, *manifest.Count, ErrInvalidMatchingAcquisitionResponse)
		}
		verifiedPages[key] = verifiedCatalogPage{offset: *page.Offset, count: *page.Count, length: len(*page.Releases), payload: append([]byte(nil), response.Payload...)}
		for _, release := range *page.Releases {
			id, err := canonicalMBID(release.ID)
			if err != nil || id != release.ID {
				return false, true, fmt.Errorf("accepted matching catalog release id is invalid")
			}
			if _, duplicate := candidatesByID[id]; duplicate {
				return false, true, fmt.Errorf("accepted matching catalog contains duplicate release ids")
			}
			var credit strings.Builder
			for _, item := range release.ArtistCredit {
				credit.WriteString(item.Name)
				credit.WriteString(item.JoinPhrase)
			}
			catalogNumbers := make([]string, 0, len(release.LabelInfo))
			for _, label := range release.LabelInfo {
				catalogNumbers = append(catalogNumbers, label.CatalogNumber)
			}
			candidatesByID[id] = MatchingRetrievalCandidate{ID: id, Title: release.Title, Disambiguation: release.Disambiguation, Date: release.Date, ArtistCredit: credit.String(), CatalogNumbers: catalogNumbers}
		}
	}
	// The cache-key reader is sorted; the payload pages still must cover every
	// offset once. Manifest ID equality alone would not detect holes/overlap.
	type keyedOffset struct {
		key    string
		offset int
	}
	pageOffsets := make([]keyedOffset, len(keys))
	for index, key := range keys {
		var artist string
		var offset, limit int
		if _, err := fmt.Sscanf(key, "artist-release-browse:%36s:%d:%d", &artist, &offset, &limit); err != nil || limit != matchingCatalogPageSize || canonicalArtistCursor(artist, offset) != nil {
			return false, true, fmt.Errorf("accepted matching catalog key is invalid")
		}
		pageOffsets[index] = keyedOffset{key: key, offset: offset}
	}
	sort.Slice(pageOffsets, func(i, j int) bool { return pageOffsets[i].offset < pageOffsets[j].offset })
	covered := 0
	for _, item := range pageOffsets {
		if item.offset != covered {
			return false, true, fmt.Errorf("accepted matching catalog pages have a gap or overlap")
		}
		var input struct {
			ArtistMBID string `json:"artist_mbid"`
			Offset     int    `json:"offset"`
			Limit      int    `json:"limit"`
		}
		pagePhase := phasesByKey[item.key]
		if json.Unmarshal(pagePhase.Input, &input) != nil || input.ArtistMBID != manifest.ArtistMBID || input.Offset != item.offset || input.Limit != matchingCatalogPageSize {
			return false, true, fmt.Errorf("accepted matching catalog page input does not match its cache scope")
		}
		page, exists := verifiedPages[item.key]
		if !exists || page.offset != item.offset || page.count != *manifest.Count {
			return false, true, fmt.Errorf("accepted matching catalog page snapshot is inconsistent")
		}
		covered += page.length
	}
	if covered != *manifest.Count || len(candidatesByID) != len(ids) {
		return false, true, fmt.Errorf("accepted matching catalog pages do not cover the manifest")
	}
	for _, id := range ids {
		if _, exists := candidatesByID[id]; !exists {
			return false, true, fmt.Errorf("accepted matching catalog manifest differs from catalog pages")
		}
	}
	if len(candidatesByID) == 0 {
		var finalPage *persistence.MatchingAcquisitionPhase
		var finalPayload []byte
		for index := range phases {
			phase := &phases[index]
			var provenance []struct {
				RequestID uuid.UUID `json:"request_id"`
				CacheKey  string    `json:"cache_key"`
			}
			if json.Unmarshal(phase.ResponseProvenance, &provenance) == nil && len(provenance) == 1 {
				if page, ok := verifiedPages[provenance[0].CacheKey]; ok && page.offset+page.length == *manifest.Count {
					finalPage = phase
					finalPayload = page.payload
				}
			}
		}
		if finalPage == nil {
			return false, true, fmt.Errorf("accepted empty catalog completion page is unavailable")
		}
		var provenance []struct {
			RequestID uuid.UUID `json:"request_id"`
			CacheKey  string    `json:"cache_key"`
		}
		if json.Unmarshal(finalPage.ResponseProvenance, &provenance) != nil || len(provenance) != 1 {
			return false, true, fmt.Errorf("accepted empty catalog completion provenance is invalid")
		}
		if len(finalPayload) == 0 {
			return false, true, fmt.Errorf("accepted empty catalog completion page is unavailable")
		}
		input := append(json.RawMessage(nil), snapshot.Output...)
		output, _ := json.Marshal(map[string]string{"state": "waiting_policy", "reason": "catalog_no_results"})
		changed, err := acquisition.repository.ParkMatchingAcquisitionForDelivery(ctx, runID, attempt, riverJobID, claim, snapshot.Revision,
			acquisitionPhase(snapshot.Revision+1, "waiting_policy", input, output, provenance[0].RequestID, provenance[0].CacheKey, finalPayload))
		return changed, true, err
	}
	captured, err := acquisition.validatedCapture(ctx, runID, "catalog_ready")
	if err != nil {
		return false, true, err
	}
	candidates := make([]MatchingRetrievalCandidate, 0, len(candidatesByID))
	for _, candidate := range candidatesByID {
		candidates = append(candidates, candidate)
	}
	shortlist, err := RankMatchingCatalog(captured, candidates)
	if err != nil {
		return false, true, err
	}
	if len(shortlist) > 10 {
		shortlist = shortlist[:10]
	}
	children := make([]persistence.MatchingProviderRequest, 0, len(shortlist))
	lookups := make([]map[string]any, 0, len(shortlist))
	for _, id := range shortlist {
		child, err := acquisition.providers.PrepareOperation(ctx, "release_lookup", id, 0, 0, false)
		if err != nil {
			return false, true, err
		}
		if child.ProviderCacheKey == nil || child.ID == uuid.Nil || *child.ProviderCacheKey != "release-lookup:"+id {
			return false, true, fmt.Errorf("prepared release lookup has an invalid cache scope")
		}
		children = append(children, persistence.MatchingProviderRequest{Operation: child, Args: ProviderFetchOperationArgs{OperationID: child.ID}})
		lookups = append(lookups, map[string]any{"release_id": id, "request_id": child.ID, "cache_key": *child.ProviderCacheKey})
	}
	input, _ := json.Marshal(map[string]any{"release_ids": shortlist})
	output, _ := json.Marshal(map[string]any{"lookups": lookups})
	phase := persistence.MatchingAcquisitionPhase{Revision: snapshot.Revision + 1, Phase: persistence.MatchingAcquisitionReleaseHydration, Input: input, Output: output, ResponseProvenance: json.RawMessage("[]")}
	starter, ok := acquisition.repository.(matchingDeliveryReleaseHydrationStarter)
	if !ok {
		return false, true, fmt.Errorf("claim-aware matching release hydration persistence is unavailable")
	}
	changed, err := starter.StartMatchingReleaseHydrationForDelivery(ctx, acquisition.river, runID, attempt, riverJobID, claim, snapshot.Revision, phase, children)
	return changed, true, err
}

func (acquisition *MatchingAcquisitionService) advanceCatalogPage(ctx context.Context, snapshot persistence.MatchingAcquisitionSnapshot, runID uuid.UUID, attempt int, riverJobID int64, claim persistence.MatchingDeliveryClaim) (bool, bool, error) {
	var prior struct {
		ArtistMBID   string    `json:"artist_mbid"`
		NextOffset   int       `json:"next_offset"`
		CatalogCount *int      `json:"catalog_count"`
		ReleaseIDs   []string  `json:"release_ids"`
		RequestID    uuid.UUID `json:"next_request_id"`
		CacheKey     string    `json:"next_cache_key"`
	}
	if err := json.Unmarshal(snapshot.Output, &prior); err != nil || canonicalArtistCursor(prior.ArtistMBID, prior.NextOffset) != nil {
		return false, true, fmt.Errorf("saved matching catalog cursor is invalid")
	}
	responseRequestID := prior.RequestID
	if responseRequestID == uuid.Nil || prior.CacheKey == "" {
		return false, true, fmt.Errorf("saved matching catalog child descriptor is invalid")
	}
	cacheKey := fmt.Sprintf("artist-release-browse:%s:%d:%d", strings.ToLower(prior.ArtistMBID), prior.NextOffset, matchingCatalogPageSize)
	if cacheKey != prior.CacheKey {
		return false, true, fmt.Errorf("saved matching catalog child cache key does not match its cursor")
	}
	response, err := acquisition.providers.CachedResponse(ctx, cacheKey)
	if err != nil {
		return false, true, err
	}
	if response == nil || len(response.Payload) == 0 {
		return false, true, fmt.Errorf("accepted artist catalog page is unavailable")
	}
	// The cached payload is the raw MusicBrainz artist release browse envelope,
	// which reports hyphenated release-count/release-offset keys. Parse them
	// strictly: absent or null values must not be silently read as zero, while a
	// genuine zero count with zero offset and no releases is a valid empty catalog.
	var page struct {
		Count    *int `json:"release-count"`
		Offset   *int `json:"release-offset"`
		Releases *[]struct {
			ID string `json:"id"`
		} `json:"releases"`
	}
	if err := json.Unmarshal(response.Payload, &page); err != nil || page.Count == nil || page.Offset == nil || page.Releases == nil ||
		*page.Count < 0 || *page.Offset < 0 || *page.Offset != prior.NextOffset ||
		len(*page.Releases) > matchingCatalogPageSize || len(*page.Releases) == 0 && *page.Offset < *page.Count ||
		*page.Offset+len(*page.Releases) > *page.Count {
		return false, true, fmt.Errorf("accepted artist catalog page is invalid or made no progress: %w", ErrInvalidMatchingAcquisitionResponse)
	}
	if prior.CatalogCount != nil && *prior.CatalogCount != *page.Count {
		return false, true, fmt.Errorf("accepted artist catalog count changed during pagination: %w", ErrInvalidMatchingAcquisitionResponse)
	}
	seen := make(map[string]struct{}, len(prior.ReleaseIDs)+len(*page.Releases))
	releaseIDs := append([]string(nil), prior.ReleaseIDs...)
	for _, id := range releaseIDs {
		seen[strings.ToLower(id)] = struct{}{}
	}
	for _, release := range *page.Releases {
		id, err := canonicalMBID(release.ID)
		if err != nil {
			return false, true, fmt.Errorf("accepted artist catalog contains an invalid release id: %w", ErrInvalidMatchingAcquisitionResponse)
		}
		if _, duplicate := seen[strings.ToLower(id)]; duplicate {
			return false, true, fmt.Errorf("accepted artist catalog contains duplicate release ids: %w", ErrInvalidMatchingAcquisitionResponse)
		}
		seen[strings.ToLower(id)] = struct{}{}
		releaseIDs = append(releaseIDs, id)
	}
	nextOffset := *page.Offset + len(*page.Releases)
	complete := nextOffset >= *page.Count
	input, _ := json.Marshal(map[string]any{"artist_mbid": prior.ArtistMBID, "offset": *page.Offset, "limit": matchingCatalogPageSize})
	phaseName := persistence.MatchingAcquisitionArtistCatalogPage
	var children []persistence.MatchingProviderRequest
	if complete {
		phaseName = "catalog_ready"
	} else {
		child, err := acquisition.providers.PrepareOperation(ctx, "artist_release_browse", prior.ArtistMBID, nextOffset, matchingCatalogPageSize, false)
		if err != nil {
			return false, true, err
		}
		children = []persistence.MatchingProviderRequest{{Operation: child, Args: ProviderFetchOperationArgs{OperationID: child.ID}}}
		prior.RequestID = child.ID
		prior.CacheKey = *child.ProviderCacheKey
	}
	output, _ := json.Marshal(map[string]any{"artist_mbid": prior.ArtistMBID, "next_offset": nextOffset, "complete": complete, "catalog_count": *page.Count, "covered_releases": nextOffset, "release_ids": releaseIDs, "next_request_id": prior.RequestID, "next_cache_key": prior.CacheKey})
	changed, err := acquisition.repository.AdvanceMatchingAcquisitionForDelivery(ctx, acquisition.river, runID, attempt, riverJobID, claim, snapshot.Revision,
		acquisitionPhase(snapshot.Revision+1, phaseName, input, output, responseRequestID, cacheKey, response.Payload), children)
	return changed, true, err
}

func acquisitionPhase(revision int64, phase string, input, output []byte, requestID uuid.UUID, cacheKey string, payload []byte) persistence.MatchingAcquisitionPhase {
	digest := sha256.Sum256(payload)
	provenance, _ := json.Marshal([]map[string]any{{"request_id": requestID, "cache_key": cacheKey, "payload_sha256": fmt.Sprintf("%x", digest[:])}})
	return persistence.MatchingAcquisitionPhase{Revision: revision, Phase: phase, Input: input, Output: output, ResponseProvenance: provenance}
}

func canonicalMBID(value string) (string, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", err
	}
	return strings.ToLower(parsed.String()), nil
}

func canonicalArtistCursor(artist string, offset int) error {
	if _, err := canonicalMBID(artist); err != nil || offset < 0 {
		return fmt.Errorf("invalid artist cursor")
	}
	return nil
}
