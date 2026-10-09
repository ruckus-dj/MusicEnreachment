package service

import (
	"context"
	"encoding/json"
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

// StartProviderOperation records a bounded lookup or search request atomically
// with its River delivery. A missing successful cache entry is fetched on the
// worker; setting explicitRefresh requests an intentional refresh.
func (service *ProviderFetchService) StartProviderOperation(ctx context.Context, kind, queryOrMBID string, offset, limit int, explicitRefresh bool) (*persistence.Operation, error) {
	if service.operations == nil || service.river == nil {
		return nil, fmt.Errorf("durable provider operations are unavailable")
	}
	config, err := service.registry.GetMusicBrainzConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("read MusicBrainz provider configuration: %w", err)
	}
	if config.Identity == "" {
		return nil, fmt.Errorf("MusicBrainz provider configuration is not saved")
	}
	source, err := service.cache.ResolveMusicBrainzSource(ctx, musicBrainzEndpoint(config), config.Identity)
	if err != nil {
		return nil, err
	}
	intent := providerFetchIntent{SchemaVersion: 1}
	var key string
	switch kind {
	case "release_lookup":
		intent.MBID = strings.ToLower(queryOrMBID)
		key = "release-lookup:" + intent.MBID
	case "recording_lookup":
		intent.MBID = strings.ToLower(queryOrMBID)
		key = "recording-lookup:" + intent.MBID
	case "release_search":
		intent.Query, intent.Offset, intent.Limit = queryOrMBID, offset, limit
		key = fmt.Sprintf("release-search:%d:%d:%s", offset, limit, queryOrMBID)
	case "recording_search":
		intent.Query, intent.Offset, intent.Limit = queryOrMBID, offset, limit
		key = fmt.Sprintf("recording-search:%d:%d:%s", offset, limit, queryOrMBID)
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
		ProviderCacheKey: &cacheKey, ProviderExplicitRefresh: explicitRefresh,
	}
	if err := service.operations.CreateOperationAndEnqueue(ctx, operation, service.river, ProviderFetchOperationArgs{OperationID: operation.ID}, nil); err != nil {
		return nil, err
	}
	return operation, nil
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
	response, err := service.cache.GetCurrentCachedResponse(ctx, *operation.ProviderSourceID, *operation.ProviderCacheKey, *operation.ProviderConfigurationIdentity)
	if err != nil {
		return service.failProviderOperation(ctx, operations, operation, epoch, err)
	}
	if response != nil && len(response.Payload) != 0 && !operation.ProviderExplicitRefresh {
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
