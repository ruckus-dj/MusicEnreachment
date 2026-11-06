package musicbrainz_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/musicbrainz"
)

func TestGenrePageRequestsAndDecodesRequestedPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/genre/all" || r.URL.Query().Get("fmt") != "json" || r.URL.Query().Get("offset") != "100" || r.URL.Query().Get("limit") != "2" {
			t.Errorf("request = %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		if r.Header.Get("Accept") != "application/json" || r.Header.Get("User-Agent") == "" {
			t.Errorf("headers = %#v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"genre-count":102,"genre-offset":100,"genres":[{"id":"11111111-1111-4111-8111-111111111111","name":"first"},{"id":"22222222-2222-4222-8222-222222222222","name":"second"}]}`))
	}))
	defer server.Close()
	p, err := musicbrainz.NewProvider(musicbrainz.ProviderOptions{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	page, err := p.GenrePage(context.Background(), 100, 2)
	if err != nil {
		t.Fatal(err)
	}
	if page.Count != 102 || page.Offset != 100 || len(page.Genres) != 2 || page.Genres[1].Name != "second" {
		t.Fatalf("page = %+v", page)
	}
}

func TestGenrePageRejectsInvalidArgumentsAndResponses(t *testing.T) {
	for _, tc := range []struct {
		name          string
		offset, limit int
		body          string
		status        int
	}{
		{name: "negative offset", offset: -1, limit: 1},
		{name: "zero limit", offset: 0, limit: 0},
		{name: "limit over maximum", offset: 0, limit: 101},
		{name: "missing count", offset: 0, limit: 1, body: `{"genre-offset":0,"genres":[]}`},
		{name: "missing offset", offset: 0, limit: 1, body: `{"genre-count":0,"genres":[]}`},
		{name: "wrong offset", offset: 1, limit: 1, body: `{"genre-count":1,"genre-offset":0,"genres":[{"id":"11111111-1111-4111-8111-111111111111","name":"x"}]}`},
		{name: "negative count", offset: 0, limit: 1, body: `{"genre-count":-1,"genre-offset":0,"genres":[]}`},
		{name: "null genres", offset: 0, limit: 1, body: `{"genre-count":0,"genre-offset":0,"genres":null}`},
		{name: "missing genres", offset: 0, limit: 1, body: `{"genre-count":0,"genre-offset":0}`},
		{name: "invalid JSON", offset: 0, limit: 1, body: `{`},
		{name: "server error", offset: 0, limit: 1, status: http.StatusBadGateway, body: `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			p, err := musicbrainz.NewProvider(musicbrainz.ProviderOptions{BaseURL: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			_, err = p.GenrePage(context.Background(), tc.offset, tc.limit)
			if err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestGenrePageRejectsCountTypeAndMalformedContentType(t *testing.T) {
	for _, tc := range []struct{ name, body, contentType string }{
		{"null count", `{"genre-count":null,"genre-offset":0,"genres":[]}`, "application/json"},
		{"string count", `{"genre-count":"0","genre-offset":0,"genres":[]}`, "application/json"},
		{"null offset", `{"genre-count":0,"genre-offset":null,"genres":[]}`, "application/json"},
		{"null genres", `{"genre-count":0,"genre-offset":0,"genres":null}`, "application/json"},
		{"wrong content type", `{"genre-count":0,"genre-offset":0,"genres":[]}`, "text/plain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			p, err := musicbrainz.NewProvider(musicbrainz.ProviderOptions{BaseURL: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := p.GenrePage(context.Background(), 0, 1); err == nil || strings.TrimSpace(err.Error()) == "" {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
