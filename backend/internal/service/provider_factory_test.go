package service_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/acoustid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/musicbrainz"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type providerFactoryStore struct{ values map[string]string }

func (s *providerFactoryStore) Get(_ context.Context, key string) (string, bool, error) {
	value, ok := s.values[key]
	return value, ok, nil
}
func (s *providerFactoryStore) Set(_ context.Context, key, value string) error {
	s.values[key] = value
	return nil
}
func (s *providerFactoryStore) SetMany(_ context.Context, values map[string]string) error {
	for key, value := range values {
		s.values[key] = value
	}
	return nil
}
func (s *providerFactoryStore) SetIfAbsent(ctx context.Context, key, value string) (string, error) {
	if current, ok, _ := s.Get(ctx, key); ok {
		return current, nil
	}
	return value, s.Set(ctx, key, value)
}
func (s *providerFactoryStore) InitializePlatform(_ context.Context, goos, goarch string) (string, string, bool, error) {
	return goos, goarch, true, nil
}

type providerFactoryWaiter struct{ calls atomic.Int32 }

func (w *providerFactoryWaiter) Wait(context.Context) error {
	w.calls.Add(1)
	return nil
}

type providerFactoryRoundTripper struct{ calls atomic.Int32 }

func (rt *providerFactoryRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	rt.calls.Add(1)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       http.NoBody, Request: request,
	}, nil
}

func TestProviderFactoryReadsCurrentMusicBrainzConfiguration(t *testing.T) {
	ctx := context.Background()
	var firstCalls, secondCalls atomic.Int32
	newServer := func(calls *atomic.Int32) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"count":0,"offset":0,"created":"2026-01-01T00:00:00Z","releases":[]}`))
		}))
	}
	first := newServer(&firstCalls)
	defer first.Close()
	second := newServer(&secondCalls)
	defer second.Close()

	registry := settings.New(&providerFactoryStore{values: make(map[string]string)}, nil)
	if err := registry.SetMusicBrainzConfig(ctx, "self-hosted", first.URL); err != nil {
		t.Fatal(err)
	}
	factory := service.NewProviderFactory(registry, musicbrainz.NewPublicRateGate(), new(musicbrainz.SelfHostedRateGate), &http.Client{Timeout: 10 * time.Second}, nil)
	provider, err := factory.MusicBrainz(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.SearchReleases(ctx, "test", 0, 1); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetMusicBrainzConfig(ctx, "self-hosted", second.URL); err != nil {
		t.Fatal(err)
	}
	provider, err = factory.MusicBrainz(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.SearchReleases(ctx, "test", 0, 1); err != nil {
		t.Fatal(err)
	}
	if firstCalls.Load() != 1 || secondCalls.Load() != 1 {
		t.Fatalf("request counts = (%d, %d), want (1, 1)", firstCalls.Load(), secondCalls.Load())
	}
}

func TestProviderFactoryDoesNotLookupAcoustIDWithoutSavedKey(t *testing.T) {
	store := &providerFactoryStore{values: make(map[string]string)}
	registry := settings.New(store, nil)
	waiter := new(providerFactoryWaiter)
	transport := new(providerFactoryRoundTripper)
	client := acoustid.NewClient(&http.Client{Transport: transport, Timeout: acoustid.RequestTimeout}, waiter)
	factory := service.NewProviderFactory(registry, nil, nil, nil, client)
	result, err := factory.LookupAcoustID(context.Background(), "fingerprint", 123)
	if err != nil {
		t.Fatal(err)
	}
	if result.Available || waiter.calls.Load() != 0 || transport.calls.Load() != 0 {
		t.Fatalf("result=%#v waiter calls=%d requests=%d", result, waiter.calls.Load(), transport.calls.Load())
	}
}
