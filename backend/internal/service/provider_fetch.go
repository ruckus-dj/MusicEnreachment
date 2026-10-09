package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/musicbrainz"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

// ProviderFetchService performs only caller-requested MusicBrainz network
// operations. Cache reads are separate and never trigger a refresh.
type ProviderFetchService struct {
	registry *settings.Registry
	factory  *ProviderFactory
	cache    *persistence.ProviderCacheRepository
	now      func() time.Time
}

func NewProviderFetchService(registry *settings.Registry, factory *ProviderFactory, cache *persistence.ProviderCacheRepository) *ProviderFetchService {
	return &ProviderFetchService{registry: registry, factory: factory, cache: cache, now: time.Now}
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
