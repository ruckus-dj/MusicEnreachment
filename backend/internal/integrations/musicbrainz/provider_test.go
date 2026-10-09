package musicbrainz_test

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/musicbrainz"
)

func TestSearchReleasesPreservesRawUnknownFieldsAndScorePresence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":2,"offset":0,"created":"2020-01-01T00:00:00Z","releases":[{"id":"11111111-1111-4111-8111-111111111111","title":"Zero","score":0,"media":[{"tracks":[{"title":"track"}]}],"future-field":{"keep":true}},{"id":"22222222-2222-4222-8222-222222222222","title":"Missing"}]}`))
	}))
	defer server.Close()
	provider, err := musicbrainz.NewProvider(musicbrainz.ProviderOptions{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.SearchReleases(context.Background(), "artist:test", 0, 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 2 || !result.Items[0].ScoreSet || result.Items[0].Score != 0 || result.Items[1].ScoreSet {
		t.Fatalf("score projection = %#v", result.Items)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(result.Items[0].Raw, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["future-field"] == nil || raw["media"] == nil {
		t.Fatalf("raw fields were lost: %s", result.Items[0].Raw)
	}
}

func TestSearchRejectsWrongContentTypeAndOversizedResponse(t *testing.T) {
	for _, tc := range []struct {
		name, contentType string
		body              func(http.ResponseWriter)
	}{
		{name: "content type", contentType: "text/plain", body: func(w http.ResponseWriter) { _, _ = w.Write([]byte(`{"count":0,"releases":[]}`)) }},
		{name: "oversized", contentType: "application/json", body: func(w http.ResponseWriter) {
			_, _ = w.Write([]byte(`{"count":0,"releases":[],"padding":"` + strings.Repeat("x", musicbrainz.ResponseBodyLimit) + `"}`))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				tc.body(w)
			}))
			defer server.Close()
			provider, err := musicbrainz.NewProvider(musicbrainz.ProviderOptions{BaseURL: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := provider.SearchReleases(context.Background(), "query", 0, 25); err == nil {
				t.Fatal("expected response validation error")
			}
		})
	}
}

func TestSearchRejectsMalformedEnvelopeAndEntityFields(t *testing.T) {
	for _, body := range []string{
		`{"offset":0,"created":"2020-01-01T00:00:00Z","releases":[]}`,
		`{"count":0,"offset":0,"releases":[]}`,
		`{"count":1,"offset":0,"created":"2020-01-01T00:00:00Z","releases":null}`,
		`{"count":1,"offset":0,"created":"2020-01-01T00:00:00Z","releases":[{}]}`,
		`{"count":1,"offset":0,"created":"2020-01-01T00:00:00Z","releases":[{"id":"11111111-1111-4111-8111-111111111111","title":"x","score":null}]}`,
		`{"count":1,"offset":0,"created":"2020-01-01T00:00:00Z","releases":[{"id":"11111111-1111-4111-8111-111111111111","title":"x","score":101}]}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		}))
		provider, err := musicbrainz.NewProvider(musicbrainz.ProviderOptions{BaseURL: server.URL})
		if err != nil {
			server.Close()
			t.Fatal(err)
		}
		if _, err := provider.SearchReleases(context.Background(), "query", 0, 25); err == nil {
			t.Errorf("accepted malformed response: %s", body)
		}
		server.Close()
	}
}

func TestLookupRejectsResponseForDifferentMBID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"22222222-2222-4222-8222-222222222222","title":"wrong release"}`))
	}))
	defer server.Close()
	provider, err := musicbrainz.NewProvider(musicbrainz.ProviderOptions{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.LookupRelease(context.Background(), "11111111-1111-4111-8111-111111111111")
	if err == nil {
		t.Fatal("accepted lookup response with mismatched MBID")
	}
}

func TestSearchConcurrentCallsKeepRawBodiesIsolated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		query := r.URL.Query().Get("query")
		id := "11111111-1111-4111-8111-111111111111"
		if query == "second" {
			id = "22222222-2222-4222-8222-222222222222"
		}
		_, _ = w.Write([]byte(`{"count":1,"offset":0,"created":"2020-01-01T00:00:00Z","recordings":[{"id":"` + id + `","title":"` + query + `","future":"` + query + `"}]}`))
	}))
	defer server.Close()
	provider, err := musicbrainz.NewProvider(musicbrainz.ProviderOptions{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, query := range []string{"first", "second"} {
		wg.Add(1)
		go func(query string) {
			defer wg.Done()
			result, callErr := provider.SearchRecordings(context.Background(), query, 0, 25)
			if callErr != nil {
				t.Error(callErr)
				return
			}
			expectedID := "11111111-1111-4111-8111-111111111111"
			if query == "second" {
				expectedID = "22222222-2222-4222-8222-222222222222"
			}
			if len(result.Items) != 1 || result.Items[0].ID != expectedID {
				t.Errorf("%s got %#v", query, result.Items)
			}
		}(query)
	}
	wg.Wait()
}

func TestSearchContextCancellationIsBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	provider, err := musicbrainz.NewProvider(musicbrainz.ProviderOptions{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = provider.SearchReleases(ctx, "query", 0, 25)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error = %v", err)
	}
}

func TestProviderRejectsUnsafeEndpointAndDelay(t *testing.T) {
	for _, options := range []musicbrainz.ProviderOptions{
		{BaseURL: "https://user:pass@example.test/ws/2"},
		{BaseURL: "https://example.test/ws/2?token=x"},
		{BaseURL: "https://example.test/ws/2", SelfHostedDelaySeconds: 60.1},
	} {
		if _, err := musicbrainz.NewProvider(options); err == nil {
			t.Fatalf("accepted unsafe options: %#v", options)
		}
	}
}

func TestGovernedHTTPClientHasSharedRateGate(t *testing.T) {
	gate := musicbrainz.NewPublicRateGate()
	first := musicbrainz.NewClientWithGate(gate)
	second, err := musicbrainz.NewProvider(musicbrainz.ProviderOptions{PublicGate: gate})
	if err != nil {
		t.Fatal(err)
	}
	if first == nil || second == nil {
		t.Fatal("clients were not created")
	}
}

func TestGzipResponseIsDecodedBeforeRawProjection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		writer := gzip.NewWriter(w)
		_, _ = writer.Write([]byte(`{"count":0,"offset":0,"created":"2020-01-01T00:00:00Z","releases":[]}`))
		_ = writer.Close()
	}))
	defer server.Close()
	provider, err := musicbrainz.NewProvider(musicbrainz.ProviderOptions{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.SearchReleases(context.Background(), "query", 0, 25); err != nil {
		t.Fatal(err)
	}
}
