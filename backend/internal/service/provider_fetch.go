package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/musicbrainz"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

var ErrMusicBrainzConfigurationMissing = errors.New("MusicBrainz provider configuration is not saved")

// ProviderFetchService performs only caller-requested MusicBrainz network
// operations. Cache reads are separate and never trigger a refresh.
type ProviderFetchService struct {
	registry   *settings.Registry
	factory    *ProviderFactory
	cache      *persistence.ProviderCacheRepository
	now        func() time.Time
	operations durableProviderOperationRepository
	river      persistence.RiverInserter
}

type durableProviderOperationRepository interface {
	CreateOperationAndEnqueue(context.Context, *persistence.Operation, persistence.RiverInserter, river.JobArgs, *river.InsertOpts) error
	CreateAcoustIDLookupOperationAndEnqueue(context.Context, uuid.UUID, bool, persistence.RiverInserter, river.JobArgs, *river.InsertOpts, func(persistence.AcoustIDLookupCapture) (*persistence.Operation, error)) error
	GetOperation(context.Context, uuid.UUID) (*persistence.Operation, error)
	ClaimProviderOperationDelivery(context.Context, uuid.UUID, int, int64) (int64, bool, error)
}

// ProviderFetchOperationArgs intentionally contains no request data or credentials.
// Workers reload the typed intent and captured source configuration from operation.
type ProviderFetchOperationArgs struct {
	OperationID uuid.UUID `json:"operation_id"`
}

func (ProviderFetchOperationArgs) Kind() string { return "provider_fetch_v1" }

type providerFetchIntent struct {
	SchemaVersion int    `json:"schema_version"`
	Query         string `json:"query,omitempty"`
	MBID          string `json:"mbid,omitempty"`
	Offset        int    `json:"offset,omitempty"`
	Limit         int    `json:"limit,omitempty"`
}

func NewProviderFetchService(registry *settings.Registry, factory *ProviderFactory, cache *persistence.ProviderCacheRepository) *ProviderFetchService {
	return &ProviderFetchService{registry: registry, factory: factory, cache: cache, now: time.Now}
}

// WithDurableOperations enables explicit provider requests to be admitted as
// durable operations. The cache-only CachedResponse API remains independent.
func (service *ProviderFetchService) WithDurableOperations(repository durableProviderOperationRepository, client persistence.RiverInserter) *ProviderFetchService {
	service.operations, service.river = repository, client
	return service
}

// PrepareOperation validates and captures an explicit provider request without
// enqueueing it. Coordinators use this to create the provider operation and
// their own durable plan in one transaction.
func (service *ProviderFetchService) PrepareOperation(ctx context.Context, kind, queryOrMBID string, offset, limit int, explicitRefresh bool) (*persistence.Operation, error) {
	config, err := service.registry.GetMusicBrainzConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("read MusicBrainz provider configuration: %w", err)
	}
	if config.Identity == "" {
		return nil, ErrMusicBrainzConfigurationMissing
	}
	source, err := service.cache.ResolveMusicBrainzSource(ctx, musicBrainzEndpoint(config), config.Identity)
	if err != nil {
		return nil, err
	}
	intent := providerFetchIntent{SchemaVersion: 1}
	var key string
	switch kind {
	case "release_lookup":
		intent.MBID = strings.ToLower(strings.TrimSpace(queryOrMBID))
		if !validProviderMBID(intent.MBID) {
			return nil, fmt.Errorf("invalid MusicBrainz release id")
		}
		key = "release-lookup:" + intent.MBID
	case "recording_lookup":
		intent.MBID = strings.ToLower(strings.TrimSpace(queryOrMBID))
		if !validProviderMBID(intent.MBID) {
			return nil, fmt.Errorf("invalid MusicBrainz recording id")
		}
		key = "recording-lookup:" + intent.MBID
	case "release_search":
		if strings.TrimSpace(queryOrMBID) == "" || offset < 0 || limit < 1 || limit > 100 {
			return nil, fmt.Errorf("invalid MusicBrainz search parameters")
		}
		intent.Query, intent.Offset, intent.Limit = queryOrMBID, offset, limit
		key = fmt.Sprintf("release-search:%d:%d:%s", offset, limit, queryOrMBID)
	case "recording_search":
		if strings.TrimSpace(queryOrMBID) == "" || offset < 0 || limit < 1 || limit > 100 {
			return nil, fmt.Errorf("invalid MusicBrainz search parameters")
		}
		intent.Query, intent.Offset, intent.Limit = queryOrMBID, offset, limit
		key = fmt.Sprintf("recording-search:%d:%d:%s", offset, limit, queryOrMBID)
	case "artist_search":
		query := strings.TrimSpace(queryOrMBID)
		if query == "" || offset < 0 || limit < 1 || limit > 100 {
			return nil, fmt.Errorf("invalid MusicBrainz search parameters")
		}
		intent.Query, intent.Offset, intent.Limit = query, offset, limit
		key = fmt.Sprintf("artist-search:%d:%d:%s", offset, limit, query)
	case "artist_release_browse":
		intent.MBID = strings.ToLower(strings.TrimSpace(queryOrMBID))
		if !validProviderMBID(intent.MBID) || offset < 0 || limit < 1 || limit > 100 {
			return nil, fmt.Errorf("invalid MusicBrainz artist browse parameters")
		}
		intent.Offset, intent.Limit = offset, limit
		key = fmt.Sprintf("artist-release-browse:%s:%d:%d", intent.MBID, offset, limit)
	default:
		return nil, fmt.Errorf("unsupported provider operation")
	}
	snapshot, err := json.Marshal(intent)
	if err != nil {
		return nil, fmt.Errorf("encode provider operation intent: %w", err)
	}
	operationKind := "provider_" + kind
	identity, cacheKey, sourceID := config.Identity, key, source.ID
	operation := &persistence.Operation{
		ID: uuid.New(), Kind: operationKind, State: "queued", Stage: "queued", InputSnapshot: snapshot,
		ProviderSourceID: &sourceID, ProviderConfigurationIdentity: &identity,
		ProviderID: &source.ProviderID, ProviderEndpoint: source.Endpoint,
		ProviderCacheKey: &cacheKey, ProviderExplicitRefresh: explicitRefresh,
	}
	return operation, nil
}

// ResolveCurrentMusicBrainzSource captures the saved source identity without
// preparing a provider request or performing network I/O.
func (service *ProviderFetchService) ResolveCurrentMusicBrainzSource(ctx context.Context) (persistence.AutomaticFallbackProviderSource, error) {
	config, err := service.registry.GetMusicBrainzConfig(ctx)
	if err != nil {
		return persistence.AutomaticFallbackProviderSource{}, fmt.Errorf("read MusicBrainz provider configuration: %w", err)
	}
	if config.Identity == "" {
		return persistence.AutomaticFallbackProviderSource{}, ErrMusicBrainzConfigurationMissing
	}
	source, err := service.cache.ResolveMusicBrainzSource(ctx, musicBrainzEndpoint(config), config.Identity)
	if err != nil {
		return persistence.AutomaticFallbackProviderSource{}, err
	}
	return persistence.AutomaticFallbackProviderSource{ID: source.ID, ProviderID: source.ProviderID, Endpoint: source.Endpoint, ConfigurationIdentity: source.ConfigurationIdentity}, nil
}

// StartProviderOperation records a bounded lookup or search request atomically
// with its River delivery. A missing successful cache entry is fetched on the
// worker; setting explicitRefresh requests an intentional refresh.
func (service *ProviderFetchService) StartProviderOperation(ctx context.Context, kind, queryOrMBID string, offset, limit int, explicitRefresh bool) (*persistence.Operation, error) {
	if service.operations == nil || service.river == nil {
		return nil, fmt.Errorf("durable provider operations are unavailable")
	}
	operation, err := service.PrepareOperation(ctx, kind, queryOrMBID, offset, limit, explicitRefresh)
	if err != nil {
		return nil, err
	}
	if err := service.operations.CreateOperationAndEnqueue(ctx, operation, service.river, ProviderFetchOperationArgs{OperationID: operation.ID}, nil); err != nil {
		return nil, err
	}
	return operation, nil
}

func validProviderMBID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && strings.EqualFold(value, parsed.String())
}

// ExecuteDurableOperation reloads immutable intent from persistence and only
// applies provider data while the saved configuration/source fence still matches.
func (service *ProviderFetchService) ExecuteDurableOperation(ctx context.Context, operations *Operations, id uuid.UUID, jobID int64) error {
	operation, err := service.operations.GetOperation(ctx, id)
	if err != nil {
		return err
	}
	if operation.RiverJobID == nil || *operation.RiverJobID != jobID || operation.State == "succeeded" || operation.State == "failed" {
		return nil
	}
	epoch, claimed, err := service.operations.ClaimProviderOperationDelivery(ctx, id, operation.Attempt, jobID)
	if err != nil {
		return err
	}
	if !claimed {
		return fmt.Errorf("provider operation delivery is already claimed")
	}
	if operation.Kind == "provider_acoustid_lookup" {
		return service.executeAcoustIDOperation(ctx, operations, operation, epoch, jobID)
	}
	if operation.ProviderSourceID == nil || operation.ProviderConfigurationIdentity == nil || operation.ProviderCacheKey == nil {
		return service.failProviderOperation(ctx, operations, operation, epoch, fmt.Errorf("provider operation intent is incomplete"))
	}
	var intent providerFetchIntent
	decoder := json.NewDecoder(strings.NewReader(string(operation.InputSnapshot)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&intent); err != nil || intent.SchemaVersion != 1 {
		return service.failProviderOperation(ctx, operations, operation, epoch, fmt.Errorf("provider operation intent is invalid"))
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return service.failProviderOperation(ctx, operations, operation, epoch, fmt.Errorf("provider operation intent has trailing data"))
	}
	if err := operations.RunningForProviderDelivery(ctx, operation, epoch, "fetching_provider"); err != nil {
		return err
	}
	if operation.Kind == "provider_release_search" || operation.Kind == "provider_recording_search" {
		receipt, err := service.cache.ReplayAutomaticFallbackSearchReceipt(ctx, persistence.ProviderDeliveryFence{
			OperationID: operation.ID, Attempt: operation.Attempt, RiverJobID: jobID, ExecutionEpoch: epoch,
		})
		if err != nil {
			return service.failProviderOperation(ctx, operations, operation, epoch, err)
		}
		if receipt != nil {
			return operations.SucceedForProviderDelivery(ctx, operation, epoch, "provider_fallback_result_reused")
		}
	}
	if operation.Kind == "provider_artist_search" || operation.Kind == "provider_release_search" || operation.Kind == "provider_artist_release_browse" {
		captured, err := service.cache.HasCapturedManualPickerSearchResult(ctx, persistence.ProviderDeliveryFence{
			OperationID: operation.ID, Attempt: operation.Attempt, RiverJobID: jobID, ExecutionEpoch: epoch,
		})
		if err != nil {
			return service.failProviderOperation(ctx, operations, operation, epoch, err)
		}
		if captured {
			return operations.SucceedForProviderDelivery(ctx, operation, epoch, "provider_result_reused")
		}
	}
	response, err := service.cache.GetCurrentCachedResponse(ctx, *operation.ProviderSourceID, *operation.ProviderCacheKey, *operation.ProviderConfigurationIdentity)
	if err != nil {
		return service.failProviderOperation(ctx, operations, operation, epoch, err)
	}
	if response != nil && len(response.Payload) != 0 && !operation.ProviderExplicitRefresh {
		if operation.Kind == "provider_artist_search" || operation.Kind == "provider_release_search" || operation.Kind == "provider_recording_search" || operation.Kind == "provider_artist_release_browse" {
			if err := service.cache.CaptureManualPickerSearchReuse(ctx, persistence.ProviderDeliveryFence{
				OperationID: operation.ID, Attempt: operation.Attempt, RiverJobID: jobID, ExecutionEpoch: epoch,
			}, response); err != nil {
				return service.failProviderOperation(ctx, operations, operation, epoch, err)
			}
		}
		_, err := service.cache.CaptureSuccessfulManualLookupReuse(ctx, persistence.ProviderDeliveryFence{
			OperationID: operation.ID, Attempt: operation.Attempt, RiverJobID: jobID, ExecutionEpoch: epoch,
		}, response)
		if err != nil {
			return service.failProviderOperation(ctx, operations, operation, epoch, err)
		}
		return operations.SucceedForProviderDelivery(ctx, operation, epoch, "provider_cache_reused")
	}
	config, err := service.registry.GetMusicBrainzConfig(ctx)
	if err != nil {
		return service.failProviderOperation(ctx, operations, operation, epoch, err)
	}
	if config.Identity != *operation.ProviderConfigurationIdentity {
		return service.failProviderOperation(ctx, operations, operation, epoch, fmt.Errorf("MusicBrainz configuration changed"))
	}
	source, err := service.cache.ResolveMusicBrainzSource(ctx, musicBrainzEndpoint(config), config.Identity)
	if err != nil {
		return service.failProviderOperation(ctx, operations, operation, epoch, err)
	}
	if source.ID != *operation.ProviderSourceID {
		return service.failProviderOperation(ctx, operations, operation, epoch, fmt.Errorf("provider source changed"))
	}
	ticket, err := service.cache.BeginRefresh(ctx, source.ID, *operation.ProviderCacheKey, config.Identity)
	if err != nil {
		return service.failProviderOperation(ctx, operations, operation, epoch, err)
	}
	provider, err := service.factory.MusicBrainzForConfig(ctx, config)
	if err != nil {
		return service.failProviderOperation(ctx, operations, operation, epoch, err)
	}
	var payload []byte
	var revision string
	var graph persistence.ProviderGraph
	switch operation.Kind {
	case "provider_release_lookup":
		result, fetchErr := provider.LookupRelease(ctx, intent.MBID)
		err = fetchErr
		if err == nil {
			payload = result.Raw
			graph, err = NormalizeMusicBrainzReleaseLookup(result)
		}
	case "provider_recording_lookup":
		result, fetchErr := provider.LookupRecording(ctx, intent.MBID)
		err = fetchErr
		if err == nil {
			payload = result.Raw
			graph, err = NormalizeMusicBrainzRecordingLookup(result)
		}
	case "provider_release_search":
		result, fetchErr := provider.SearchReleases(ctx, intent.Query, intent.Offset, intent.Limit)
		err = fetchErr
		if err == nil {
			payload, revision = result.Raw, result.Created.UTC().Format(time.RFC3339Nano)
			graph, err = NormalizeMusicBrainzReleaseSearch(result)
		}
	case "provider_recording_search":
		result, fetchErr := provider.SearchRecordings(ctx, intent.Query, intent.Offset, intent.Limit)
		err = fetchErr
		if err == nil {
			payload, revision = result.Raw, result.Created.UTC().Format(time.RFC3339Nano)
			graph, err = NormalizeMusicBrainzRecordingSearch(result)
		}
	case "provider_artist_search":
		result, fetchErr := provider.SearchArtists(ctx, intent.Query, intent.Offset, intent.Limit)
		err = fetchErr
		if err == nil {
			payload, revision = result.Raw, result.Created.UTC().Format(time.RFC3339Nano)
			graph, err = NormalizeMusicBrainzArtistSearch(result)
		}
	case "provider_artist_release_browse":
		result, fetchErr := provider.BrowseReleasesByArtist(ctx, intent.MBID, intent.Offset, intent.Limit)
		err = fetchErr
		if err == nil {
			payload = result.Raw
			graph, err = NormalizeMusicBrainzArtistReleaseBrowse(result)
		}
	default:
		err = fmt.Errorf("unsupported provider operation kind")
	}
	if err == nil && len(payload) == 0 {
		err = fmt.Errorf("MusicBrainz response body is missing")
	}
	if err == nil {
		err = service.cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{
			Ticket: ticket, FetchedAt: service.now().UTC(), Revision: revision, Payload: payload, Graph: graph,
			Delivery: &persistence.ProviderDeliveryFence{OperationID: operation.ID, Attempt: operation.Attempt, RiverJobID: jobID, ExecutionEpoch: epoch},
		})
	}
	if err != nil {
		return service.failProviderOperation(ctx, operations, operation, epoch, err)
	}
	return operations.SucceedForProviderDelivery(ctx, operation, epoch, "provider_response_saved")
}

func (service *ProviderFetchService) failProviderOperation(ctx context.Context, operations *Operations, operation *persistence.Operation, epoch int64, _ error) error {
	if err := operations.FailForProviderDelivery(ctx, operation, epoch, "provider_fetch_failed", "MusicBrainz request failed; cached data was retained."); err != nil {
		return err
	}
	// The safe user-facing failure is persisted; do not make River retry a settled operation.
	return nil
}

func (service *ProviderFetchService) SearchReleases(ctx context.Context, query string, offset, limit int) (musicbrainz.SearchResult, error) {
	return fetchProviderResponse(service, ctx, fmt.Sprintf("release-search:%d:%d:%s", offset, limit, query), func(provider *musicbrainz.Provider) (musicbrainz.SearchResult, persistence.ProviderGraph, []byte, string, error) {
		result, err := provider.SearchReleases(ctx, query, offset, limit)
		if err != nil {
			return result, persistence.ProviderGraph{}, nil, "", err
		}
		graph, err := NormalizeMusicBrainzReleaseSearch(result)
		return result, graph, result.Raw, result.Created.UTC().Format(time.RFC3339Nano), err
	})
}

func (service *ProviderFetchService) SearchRecordings(ctx context.Context, query string, offset, limit int) (musicbrainz.SearchResult, error) {
	return fetchProviderResponse(service, ctx, fmt.Sprintf("recording-search:%d:%d:%s", offset, limit, query), func(provider *musicbrainz.Provider) (musicbrainz.SearchResult, persistence.ProviderGraph, []byte, string, error) {
		result, err := provider.SearchRecordings(ctx, query, offset, limit)
		if err != nil {
			return result, persistence.ProviderGraph{}, nil, "", err
		}
		graph, err := NormalizeMusicBrainzRecordingSearch(result)
		return result, graph, result.Raw, result.Created.UTC().Format(time.RFC3339Nano), err
	})
}

func (service *ProviderFetchService) SearchArtists(ctx context.Context, query string, offset, limit int) (musicbrainz.SearchResult, error) {
	canonicalQuery := strings.TrimSpace(query)
	return fetchProviderResponse(service, ctx, fmt.Sprintf("artist-search:%d:%d:%s", offset, limit, canonicalQuery), func(provider *musicbrainz.Provider) (musicbrainz.SearchResult, persistence.ProviderGraph, []byte, string, error) {
		result, err := provider.SearchArtists(ctx, canonicalQuery, offset, limit)
		if err != nil {
			return result, persistence.ProviderGraph{}, nil, "", err
		}
		graph, err := NormalizeMusicBrainzArtistSearch(result)
		return result, graph, result.Raw, result.Created.UTC().Format(time.RFC3339Nano), err
	})
}

func (service *ProviderFetchService) BrowseReleasesByArtist(ctx context.Context, artistID string, offset, limit int) (musicbrainz.SearchResult, error) {
	canonicalID := strings.ToLower(strings.TrimSpace(artistID))
	return fetchProviderResponse(service, ctx, fmt.Sprintf("artist-release-browse:%s:%d:%d", canonicalID, offset, limit), func(provider *musicbrainz.Provider) (musicbrainz.SearchResult, persistence.ProviderGraph, []byte, string, error) {
		result, err := provider.BrowseReleasesByArtist(ctx, canonicalID, offset, limit)
		if err != nil {
			return result, persistence.ProviderGraph{}, nil, "", err
		}
		graph, err := NormalizeMusicBrainzArtistReleaseBrowse(result)
		return result, graph, result.Raw, "", err
	})
}

func (service *ProviderFetchService) LookupRelease(ctx context.Context, mbid string) (musicbrainz.LookupResult, error) {
	return fetchProviderResponse(service, ctx, "release-lookup:"+strings.ToLower(mbid), func(provider *musicbrainz.Provider) (musicbrainz.LookupResult, persistence.ProviderGraph, []byte, string, error) {
		result, err := provider.LookupRelease(ctx, mbid)
		if err != nil {
			return result, persistence.ProviderGraph{}, nil, "", err
		}
		graph, err := NormalizeMusicBrainzReleaseLookup(result)
		return result, graph, result.Raw, "", err
	})
}

func (service *ProviderFetchService) LookupRecording(ctx context.Context, mbid string) (musicbrainz.LookupResult, error) {
	return fetchProviderResponse(service, ctx, "recording-lookup:"+strings.ToLower(mbid), func(provider *musicbrainz.Provider) (musicbrainz.LookupResult, persistence.ProviderGraph, []byte, string, error) {
		result, err := provider.LookupRecording(ctx, mbid)
		if err != nil {
			return result, persistence.ProviderGraph{}, nil, "", err
		}
		graph, err := NormalizeMusicBrainzRecordingLookup(result)
		return result, graph, result.Raw, "", err
	})
}

// CachedResponse is a pure cache read. It never starts provider I/O.
func (service *ProviderFetchService) CachedResponse(ctx context.Context, cacheKey string) (*persistence.ProviderCachedResponse, error) {
	config, err := service.registry.GetMusicBrainzConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("read MusicBrainz provider configuration: %w", err)
	}
	source, err := service.cache.ResolveMusicBrainzSource(ctx, musicBrainzEndpoint(config), config.Identity)
	if err != nil {
		return nil, err
	}
	return service.cache.GetCurrentCachedResponse(ctx, source.ID, cacheKey, config.Identity)
}

func fetchProviderResponse[T any](service *ProviderFetchService, ctx context.Context, cacheKey string,
	request func(*musicbrainz.Provider) (T, persistence.ProviderGraph, []byte, string, error),
) (T, error) {
	var zero T
	config, err := service.registry.GetMusicBrainzConfig(ctx)
	if err != nil {
		return zero, fmt.Errorf("read MusicBrainz provider configuration: %w", err)
	}
	if config.Identity == "" {
		return zero, fmt.Errorf("MusicBrainz provider configuration is not saved")
	}
	source, err := service.cache.ResolveMusicBrainzSource(ctx, musicBrainzEndpoint(config), config.Identity)
	if err != nil {
		return zero, err
	}
	ticket, err := service.cache.BeginRefresh(ctx, source.ID, cacheKey, config.Identity)
	if err != nil {
		return zero, err
	}
	provider, err := service.factory.MusicBrainzForConfig(ctx, config)
	if err != nil {
		return zero, err
	}
	result, graph, payload, revision, err := request(provider)
	if err != nil {
		return zero, err
	}
	if len(payload) == 0 {
		return zero, fmt.Errorf("MusicBrainz response body is missing")
	}
	if err := service.cache.ApplySuccessfulResponse(ctx, persistence.ProviderSuccessfulResponse{
		Ticket: ticket, FetchedAt: service.now().UTC(), Revision: revision, Payload: payload, Graph: graph,
	}); err != nil {
		return zero, fmt.Errorf("apply MusicBrainz response: %w", err)
	}
	return result, nil
}

func musicBrainzEndpoint(config settings.MusicBrainzConfig) string {
	if config.Mode == "self-hosted" {
		return persistence.NormalizeMusicBrainzEndpoint(config.BaseURL)
	}
	return musicbrainz.OfficialEndpoint
}
