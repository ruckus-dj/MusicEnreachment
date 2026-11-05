//go:build integration

package service_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

const (
	acoustIDRecordingA = "00112233-4455-4677-8899-aabbccddeeff"
	acoustIDRecordingB = "10112233-4455-4677-8899-aabbccddeeff"
	acoustIDRecordingC = "20112233-4455-4677-8899-aabbccddeeff"
)

func acoustIDSuccessResponse(secret string) string {
	return `{"status":"ok","request_echo":{"client_secret":"` + secret + `"},"results":[` +
		`{"score":0.8,"recordings":[{"id":"` + acoustIDRecordingA + `","title":"ordered-first ` + secret + `"}],"client-` + secret + `-echo":"` + secret + `"},` +
		`{"score":0.93,"recordings":[{"id":"` + acoustIDRecordingA + `","title":"ordered-second"}]},` +
		`{"score":0,"recordings":[{"id":"` + acoustIDRecordingB + `"}]},` +
		`{"recordings":[{"id":"` + acoustIDRecordingC + `"}]}]}`
}

func startAcoustIDOperation(t *testing.T, ctx context.Context, fetch *service.ProviderFetchService, repository *persistence.SetupManagerRepository, workID uuid.UUID, refresh bool) *persistence.Operation {
	t.Helper()
	operation, err := fetch.StartAcoustIDLookup(ctx, workID, refresh)
	if err != nil {
		t.Fatalf("start durable AcoustID lookup: %v", err)
	}
	return awaitAcoustIDOperation(t, ctx, repository, operation.ID)
}

func awaitAcoustIDOperation(t *testing.T, ctx context.Context, repository *persistence.SetupManagerRepository, id uuid.UUID) *persistence.Operation {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		operation, err := repository.GetOperation(ctx, id)
		if err != nil {
			t.Fatalf("read durable AcoustID operation: %v", err)
		}
		if operation.State == "succeeded" || operation.State == "failed" {
			return operation
		}
		select {
		case <-ctx.Done():
			t.Fatalf("waiting for AcoustID operation %s: %v", id, ctx.Err())
		case <-ticker.C:
		}
	}
}

func assertAcoustIDResponseRetained(t *testing.T, ctx context.Context, cache *persistence.ProviderCacheRepository, operation *persistence.Operation, original *persistence.ProviderCachedResponse) {
	t.Helper()
	retained, err := cache.GetCachedResponse(ctx, *operation.ProviderSourceID, *operation.ProviderCacheKey)
	if err != nil || retained == nil || string(retained.Payload) != string(original.Payload) || !sameAcoustIDTime(retained.FetchedAt, original.FetchedAt) || !sameAcoustIDString(retained.Revision, original.Revision) {
		t.Fatalf("accepted AcoustID response changed: retained=%#v original=%#v err=%v", retained, original, err)
	}
}

func sameAcoustIDTime(left, right *time.Time) bool {
	return left == nil && right == nil || left != nil && right != nil && left.Equal(*right)
}

func sameAcoustIDString(left, right *string) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func awaitSignal(t *testing.T, ctx context.Context, signal <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-ctx.Done():
		t.Fatalf("waiting for %s: %v", label, ctx.Err())
	case <-signal:
	}
}

type acoustIDFixtureTransport struct {
	mu       sync.Mutex
	base     http.RoundTripper
	target   *url.URL
	status   int
	response string
	entered  chan struct{}
	release  chan struct{}
	forms    []url.Values
}

func (transport *acoustIDFixtureTransport) setTarget(target string) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	parsed, err := url.Parse(target)
	if err != nil {
		panic(err)
	}
	transport.target = parsed
	transport.status, transport.response = http.StatusOK, `{"status":"ok","results":[]}`
}

func (transport *acoustIDFixtureTransport) setResponse(status int, response string, entered, release chan struct{}) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	transport.status, transport.response, transport.entered, transport.release = status, response, entered, release
}

func (transport *acoustIDFixtureTransport) snapshotResponse() (int, string, chan struct{}, chan struct{}) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return transport.status, transport.response, transport.entered, transport.release
}

func (transport *acoustIDFixtureTransport) recordForm(form url.Values) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	transport.forms = append(transport.forms, form)
}

func (transport *acoustIDFixtureTransport) lastForm() url.Values {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if len(transport.forms) == 0 {
		return url.Values{}
	}
	return transport.forms[len(transport.forms)-1]
}

func (transport *acoustIDFixtureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.mu.Lock()
	target := transport.target
	transport.mu.Unlock()
	if request.URL.Host != "api.acoustid.org" || request.URL.Path != "/v2/lookup" {
		return nil, &url.Error{Op: "roundtrip", URL: request.URL.String(), Err: io.ErrUnexpectedEOF}
	}
	copyRequest := request.Clone(request.Context())
	copyURL := *request.URL
	copyURL.Scheme, copyURL.Host = target.Scheme, target.Host
	copyRequest.URL, copyRequest.Host = &copyURL, target.Host
	base := transport.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(copyRequest)
}
