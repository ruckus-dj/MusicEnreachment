package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
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

type matchingAcquisitionProvider interface {
	CachedResponse(context.Context, string) (*persistence.ProviderCachedResponse, error)
	PrepareOperation(context.Context, string, string, int, int, bool) (*persistence.Operation, error)
}

type matchingAcquisitionEvaluationSealReader interface {
	GetMatchingAcquisitionEvaluationSeal(context.Context, uuid.UUID) (persistence.MatchingAcquisitionEvaluationInput, bool, error)
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
	if snapshot.FailedProviderDependency {
		return false, true, ErrMatchingAcquisitionProviderFailed
	}
	if snapshot.Phase == "catalog_ready" {
		return false, true, nil
	}
	if snapshot.Phase == "admitted" {
		return acquisition.startArtistBrowse(ctx, snapshot, runID, attempt, riverJobID, claim)
	}
	if snapshot.Phase != persistence.MatchingAcquisitionArtistCatalogPage {
		return false, true, fmt.Errorf("matching acquisition phase is unsupported")
	}
	return acquisition.advanceCatalogPage(ctx, snapshot, runID, attempt, riverJobID, claim)
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
			ID string `json:"id"`
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
	artistID, err := canonicalMBID((*page.Artists)[0].ID)
	if err != nil {
		return false, true, fmt.Errorf("accepted artist search top result is invalid: %w: %v", ErrInvalidMatchingAcquisitionResponse, err)
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
