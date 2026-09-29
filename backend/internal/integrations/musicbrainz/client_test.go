package musicbrainz_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/musicbrainz"
)

func TestCheckConnectivity_SelfHosted_InvalidURL(t *testing.T) {
	client := musicbrainz.NewClient()
	result := client.CheckConnectivity(context.Background(), "self-hosted", "not-a-url")
	if result.Success {
		t.Error("expected failure for invalid URL")
	}
	if result.Error == "" {
		t.Error("expected error message")
	}
}

func TestCheckConnectivity_SelfHosted_EmptyURL(t *testing.T) {
	client := musicbrainz.NewClient()
	result := client.CheckConnectivity(context.Background(), "self-hosted", "")
	if result.Success {
		t.Error("expected failure when base URL is empty in self-hosted mode")
	}
	if result.Error != "self-hosted mode requires base URL" {
		t.Errorf("unexpected error: %s", result.Error)
	}
}

func TestCheckConnectivity_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	client := musicbrainz.NewClient()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	result := client.CheckConnectivity(ctx, "self-hosted", server.URL)
	if result.Success {
		t.Error("expected timeout failure")
	}
	if result.Error != "request timeout" {
		t.Errorf("unexpected error: %s", result.Error)
	}
}

func TestCheckConnectivity_NonHTTPS_SelfHosted(t *testing.T) {
	client := musicbrainz.NewClient()
	result := client.CheckConnectivity(context.Background(), "self-hosted", "ftp://example.com")
	if result.Success {
		t.Error("expected failure for non-HTTP(S) URL")
	}
}

func TestCheckConnectivity_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not json"))
	}))
	defer server.Close()

	client := musicbrainz.NewClient()
	result := client.CheckConnectivity(context.Background(), "self-hosted", server.URL)
	if result.Success {
		t.Error("expected failure for invalid JSON")
	}
	if result.Error != "invalid response format" {
		t.Errorf("unexpected error: %s", result.Error)
	}
}

func TestCheckConnectivity_MissingID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"name": "Nirvana"})
	}))
	defer server.Close()

	client := musicbrainz.NewClient()
	result := client.CheckConnectivity(context.Background(), "self-hosted", server.URL)
	if result.Success {
		t.Error("expected failure when response is missing ID field")
	}
	if result.Error != "response missing expected fields" {
		t.Errorf("unexpected error: %s", result.Error)
	}
}

func TestCheckConnectivity_RejectsUnrelatedArtist(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "00000000-0000-0000-0000-000000000000"})
	}))
	defer server.Close()

	result := musicbrainz.NewClient().CheckConnectivity(context.Background(), "self-hosted", server.URL)
	if result.Success {
		t.Fatal("unrelated MusicBrainz artist was accepted")
	}
}

func TestCheckConnectivity_OversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"5b11f4ce-a62d-471e-81fc-a69a8278c7da","padding":"`))
		_, _ = w.Write(make([]byte, musicbrainz.MaxResponseSize))
		_, _ = w.Write([]byte(`"}`))
	}))
	defer server.Close()

	result := musicbrainz.NewClient().CheckConnectivity(context.Background(), "self-hosted", server.URL)
	if result.Success || result.Error != "response exceeds size limit" {
		t.Fatalf("oversized response result = %#v", result)
	}
}

func TestCheckConnectivity_Non200Status(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := musicbrainz.NewClient()
	result := client.CheckConnectivity(context.Background(), "self-hosted", server.URL)
	if result.Success {
		t.Error("expected failure for non-200 status")
	}
}
