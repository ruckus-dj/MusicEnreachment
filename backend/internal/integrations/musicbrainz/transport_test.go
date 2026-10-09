package musicbrainz

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestPublicGateConcurrentWaitersRespectExtendedCooldownAndRate(t *testing.T) {
	waiter := rateWaiter{limiter: rate.NewLimiter(rate.Every(10*time.Millisecond), 1)}
	gate := NewPublicRateGateWithWaiter(waiter, time.Now)
	gate.mu.Lock()
	gate.cooldownUntil = time.Now().Add(40 * time.Millisecond)
	gate.mu.Unlock()

	started := time.Now()
	type arrival struct {
		at  time.Time
		err error
	}
	results := make(chan arrival, 3)
	for range 3 {
		go func() { err := gate.wait(context.Background()); results <- arrival{at: time.Now(), err: err} }()
	}
	time.Sleep(10 * time.Millisecond)
	gate.mu.Lock()
	gate.cooldownUntil = time.Now().Add(80 * time.Millisecond)
	gate.mu.Unlock()
	arrivals := make([]time.Time, 0, 3)
	for range 3 {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		arrivals = append(arrivals, result.at)
	}
	if elapsed := arrivals[0].Sub(started); elapsed < 70*time.Millisecond {
		t.Fatalf("gate returned before extended cooldown: %s", elapsed)
	}
	for i := 1; i < len(arrivals); i++ {
		if arrivals[i].Sub(arrivals[i-1]) < 8*time.Millisecond {
			t.Fatalf("concurrent waiters burst after cooldown: %v", arrivals)
		}
	}
}

type countingRequestWaiter struct{ calls atomic.Int32 }

func (waiter *countingRequestWaiter) Wait(context.Context) error {
	waiter.calls.Add(1)
	return nil
}

type musicBrainzTestRoundTripper func(*http.Request) (*http.Response, error)

func (roundTripper musicBrainzTestRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTripper(request)
}

func TestConnectivityAndSDKProviderSharePublicRequestGate(t *testing.T) {
	waiter := new(countingRequestWaiter)
	gate := NewPublicRateGateWithWaiter(waiter, time.Now)
	var requestCount atomic.Int32
	transport := &governedTransport{
		gate: gate,
		base: musicBrainzTestRoundTripper(func(request *http.Request) (*http.Response, error) {
			requestCount.Add(1)
			body := `{"id":"5b11f4ce-a62d-471e-81fc-a69a8278c7da"}`
			if strings.Contains(request.URL.Path, "/release/") {
				body = `{"count":0,"offset":0,"created":"2026-01-01T00:00:00Z","releases":[]}`
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(body)), Request: request,
			}, nil
		}),
	}
	sharedClient := &http.Client{Transport: transport, Timeout: RequestTimeout}
	checker := NewClientWithHTTPClient(sharedClient)
	if result := checker.CheckConnectivity(context.Background(), "public", ""); !result.Success {
		t.Fatalf("connectivity check failed: %s", result.Error)
	}
	provider, err := NewProvider(ProviderOptions{
		BaseURL: OfficialEndpoint, PublicGate: gate, HTTPClient: sharedClient,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.SearchReleases(context.Background(), "test", 0, 1); err != nil {
		t.Fatal(err)
	}
	if got := requestCount.Load(); got != 2 {
		t.Fatalf("HTTP requests = %d, want 2", got)
	}
	if got := waiter.calls.Load(); got != 2 {
		t.Fatalf("shared gate waits = %d, want one per checker/provider request", got)
	}
}

func TestPublicHostClassificationIgnoresCaseAndPort(t *testing.T) {
	for host, want := range map[string]bool{
		"MusicBrainz.org":               true,
		"MUSICBRAINZ.ORG:443":           true,
		"api.musicbrainz.org:443":       true,
		"notmusicbrainz.org":            false,
		"musicbrainz.org.attacker.test": false,
		"selfhost.test":                 false,
	} {
		request, err := http.NewRequest(http.MethodGet, "https://"+host+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := isPublicMusicBrainzHost(request.URL.Hostname()); got != want {
			t.Errorf("host %q classified as %t, want %t", host, got, want)
		}
	}
}

func TestSelfHostedRateWaitCancellationDoesNotHoldGate(t *testing.T) {
	gate := &selfHostedGate{limiter: rate.NewLimiter(rate.Every(40*time.Millisecond), 1)}
	if err := gate.wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := gate.wait(ctx); err == nil {
		t.Fatal("expected canceled wait")
	}
	if err := gate.wait(context.Background()); err != nil {
		t.Fatalf("later call blocked after cancellation: %v", err)
	}
}

func TestSharedSelfHostedRateGateDoesNotDelayFirstRequestAndKeepsBudgetOnConfigure(t *testing.T) {
	gate := new(SelfHostedRateGate)
	gate.Configure(0.04)
	started := time.Now()
	if err := gate.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 20*time.Millisecond {
		t.Fatalf("first request was delayed: %s", elapsed)
	}
	// Runtime changes update the delay but must not reset the already reserved
	// next request slot.
	gate.Configure(0.02)
	started = time.Now()
	if err := gate.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < 20*time.Millisecond {
		t.Fatalf("shared budget was reset after configure: %s", elapsed)
	}

	gate.Configure(0)
	started = time.Now()
	if err := gate.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 20*time.Millisecond {
		t.Fatalf("zero delay blocked: %s", elapsed)
	}
}

func TestSharedSelfHostedRateGateWaitIsCancelable(t *testing.T) {
	gate := new(SelfHostedRateGate)
	gate.Configure(0.2)
	if err := gate.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err := gate.Wait(ctx); err == nil {
		t.Fatal("expected canceled wait")
	}
	if time.Since(started) > 100*time.Millisecond {
		t.Fatal("canceled wait did not return promptly")
	}
}

func TestSharedSelfHostedRateGateCanceledWaitDoesNotReserveSlot(t *testing.T) {
	gate := new(SelfHostedRateGate)
	gate.Configure(0.1)
	if err := gate.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
		err := gate.Wait(ctx)
		cancel()
		if err == nil {
			t.Fatal("expected canceled wait")
		}
	}
	started := time.Now()
	if err := gate.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 150*time.Millisecond {
		t.Fatalf("successful request waited behind canceled reservations: %s", elapsed)
	}
}

func TestProviderConstructionDoesNotReconfigureSharedSelfHostedGate(t *testing.T) {
	gate := new(SelfHostedRateGate)
	gate.Configure(0.02)
	if _, err := NewProvider(ProviderOptions{
		BaseURL: "https://musicbrainz.example", SelfHostedThrottle: true,
		SelfHostedDelaySeconds: 0.01, SelfHostedRateGate: gate,
	}); err != nil {
		t.Fatal(err)
	}
	gate.mu.Lock()
	interval := gate.interval
	gate.mu.Unlock()
	if interval != 20*time.Millisecond {
		t.Fatalf("provider construction changed shared interval to %s", interval)
	}
}
