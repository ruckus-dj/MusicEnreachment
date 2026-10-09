package service

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/acoustid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/musicbrainz"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

// ProviderFactory builds request-scoped immutable adapters from saved runtime
// settings while retaining application-wide request budgets.
type ProviderFactory struct {
	registry       *settings.Registry
	publicGate     *musicbrainz.PublicRateGate
	selfHostedGate *musicbrainz.SelfHostedRateGate
	httpClient     *http.Client
	acoustID       *acoustid.Client
	musicBrainzMu  sync.Mutex
}

func NewProviderFactory(registry *settings.Registry, publicGate *musicbrainz.PublicRateGate, selfHostedGate *musicbrainz.SelfHostedRateGate, httpClient *http.Client, acoustIDClient *acoustid.Client) *ProviderFactory {
	return &ProviderFactory{
		registry: registry, publicGate: publicGate, selfHostedGate: selfHostedGate,
		httpClient: httpClient, acoustID: acoustIDClient,
	}
}

func (f *ProviderFactory) MusicBrainz(ctx context.Context) (*musicbrainz.Provider, error) {
	// Serialize configuration snapshots through shared-gate configuration so an
	// older snapshot can never finish construction after a newer one and reset it.
	f.musicBrainzMu.Lock()
	defer f.musicBrainzMu.Unlock()
	config, err := f.registry.GetMusicBrainzConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("read MusicBrainz provider configuration: %w", err)
	}
	throttle, delay, err := f.registry.GetMusicBrainzProviderSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("read MusicBrainz provider settings: %w", err)
	}
	baseURL := config.BaseURL
	if config.Mode == "self-hosted" && baseURL == "" {
		return nil, fmt.Errorf("self-hosted MusicBrainz base URL is not configured")
	}
	if config.Mode != "self-hosted" {
		baseURL = musicbrainz.OfficialEndpoint
	}
	if config.Mode == "self-hosted" && throttle && f.selfHostedGate != nil {
		f.selfHostedGate.Configure(delay)
	}
	return musicbrainz.NewProvider(musicbrainz.ProviderOptions{
		BaseURL: baseURL, PublicGate: f.publicGate, HTTPClient: f.httpClient,
		SelfHostedThrottle:     config.Mode == "self-hosted" && throttle,
		SelfHostedDelaySeconds: delay, SelfHostedRateGate: f.selfHostedGate,
	})
}

// LookupAcoustID never sends a request until the application key is saved.
func (f *ProviderFactory) LookupAcoustID(ctx context.Context, fingerprint string, durationSeconds int) (acoustid.LookupResult, error) {
	key, exists, err := f.registry.GetAcoustIDApplicationKey(ctx)
	if err != nil {
		return acoustid.LookupResult{}, fmt.Errorf("read AcoustID application key: %w", err)
	}
	if !exists || key == "" || f.acoustID == nil {
		return acoustid.LookupResult{Available: false}, nil
	}
	return f.acoustID.Lookup(ctx, key, fingerprint, durationSeconds)
}
