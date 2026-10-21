package musicbrainz

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/time/rate"
)

func TestPublicGateConcurrentWaitersRespectExtendedCooldownAndRate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		limiterInterval := 10 * time.Millisecond
		waiter := rateWaiter{limiter: rate.NewLimiter(rate.Every(limiterInterval), 1)}
		gate := NewPublicRateGateWithWaiter(waiter, time.Now)
		gate.mu.Lock()
		gate.cooldownUntil = time.Now().Add(40 * time.Millisecond)
		gate.mu.Unlock()

		type arrival struct {
			at  time.Time
			err error
		}
		results := make(chan arrival, 3)
		for range 3 {
			go func() { err := gate.wait(context.Background()); results <- arrival{at: time.Now(), err: err} }()
		}
		synctest.Wait()
		time.Sleep(10 * time.Millisecond)
		gate.mu.Lock()
		extendedDeadline := time.Now().Add(80 * time.Millisecond)
		gate.cooldownUntil = extendedDeadline
		gate.mu.Unlock()
		arrivals := make([]time.Time, 0, 3)
		for range 3 {
			result := <-results
			if result.err != nil {
				t.Fatal(result.err)
			}
			arrivals = append(arrivals, result.at)
		}
		for i, at := range arrivals {
			if at.Before(extendedDeadline) {
				t.Fatalf("gate returned before extended cooldown: arrival %d at %s, deadline %s", i, at, extendedDeadline)
			}
			if i > 0 && at.Sub(arrivals[i-1]) < limiterInterval {
				t.Fatalf("concurrent waiters burst after cooldown: %v", arrivals)
			}
		}
	})
}

func TestRateWaiterCancellationDoesNotLeaveFutureReservation(t *testing.T) {
	limiter := rate.NewLimiter(rate.Every(time.Second), 1)
	waiter := rateWaiter{limiter: limiter}
	if err := waiter.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if err := waiter.Wait(ctx); err == nil {
		t.Fatal("expected canceled wait")
	}
	if tokens := limiter.Tokens(); tokens <= 0 {
		t.Fatalf("canceled future reservation was not restored: %v tokens remain", tokens)
	}
}

type countingRequestWaiter struct{ calls atomic.Int32 }

// Limit keeps the gate's completion-spacing floor fast in tests while still
// exercising the limitProvider path.
func (waiter *countingRequestWaiter) Limit() rate.Limit { return rate.Every(time.Millisecond) }

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

func TestPublicDispatchSerializationSpacing(t *testing.T) {
	gate := NewPublicRateGateWithWaiter(rate.NewLimiter(100, 1), time.Now)
	interval := gate.interval
	if interval <= 0 {
		t.Fatalf("gate interval = %s, want positive spacing", interval)
	}

	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	var calls atomic.Int32
	var mu sync.Mutex
	var firstCompleted time.Time
	var secondEntered time.Time

	transport := &governedTransport{
		gate: gate,
		base: musicBrainzTestRoundTripper(func(request *http.Request) (*http.Response, error) {
			if calls.Add(1) == 1 {
				close(firstEntered)
				<-releaseFirst
				mu.Lock()
				firstCompleted = time.Now()
				mu.Unlock()
			} else {
				mu.Lock()
				secondEntered = time.Now()
				mu.Unlock()
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader("{}")),
				Request:    request,
			}, nil
		}),
	}
	client := &http.Client{Transport: transport, Timeout: RequestTimeout}

	dispatch := func(path string) chan error {
		done := make(chan error, 1)
		go func() {
			request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://musicbrainz.org/ws/2/"+path, nil)
			if err != nil {
				done <- err
				return
			}
			response, err := client.Do(request)
			if err == nil {
				_ = response.Body.Close()
			}
			done <- err
		}()
		return done
	}

	first := dispatch("artist/first")
	<-firstEntered
	second := dispatch("artist/second")
	// Let the second caller reach the dispatch permit while the first is still
	// in flight. It must not enter the base transport before the first finishes.
	time.Sleep(10 * time.Millisecond)
	mu.Lock()
	queuedEntered := !secondEntered.IsZero()
	mu.Unlock()
	if queuedEntered {
		t.Fatal("second dispatch entered while the first was still in flight")
	}

	close(releaseFirst)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	completed := firstCompleted
	entered := secondEntered
	mu.Unlock()
	if spacing := entered.Sub(completed); spacing < interval {
		t.Fatalf("second dispatch spacing = %s, want >= %s from first completion", spacing, interval)
	}
}

func TestCanceledQueuedDispatchReleasesPermitWithoutHTTP(t *testing.T) {
	gate := NewPublicRateGateWithWaiter(rate.NewLimiter(1000, 1), time.Now)

	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	var calls atomic.Int32
	transport := &governedTransport{
		gate: gate,
		base: musicBrainzTestRoundTripper(func(request *http.Request) (*http.Response, error) {
			if calls.Add(1) == 1 {
				close(firstEntered)
				<-releaseFirst
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader("{}")),
				Request:    request,
			}, nil
		}),
	}
	client := &http.Client{Transport: transport, Timeout: RequestTimeout}

	do := func(ctx context.Context, path string) chan error {
		done := make(chan error, 1)
		go func() {
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://musicbrainz.org/ws/2/"+path, nil)
			if err != nil {
				done <- err
				return
			}
			response, err := client.Do(request)
			if err == nil {
				_ = response.Body.Close()
			}
			done <- err
		}()
		return done
	}

	first := do(context.Background(), "artist/first")
	<-firstEntered

	ctx, cancel := context.WithCancel(context.Background())
	second := do(ctx, "artist/second")
	// Let the canceled caller queue behind the in-flight dispatch permit.
	time.Sleep(10 * time.Millisecond)
	cancel()
	if err := <-second; err == nil {
		t.Fatal("expected canceled queued dispatch to fail")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("canceled dispatch reached the base transport: calls = %d", got)
	}

	close(releaseFirst)
	if err := <-first; err != nil {
		t.Fatal(err)
	}

	third := do(context.Background(), "artist/third")
	if err := <-third; err != nil {
		t.Fatalf("dispatch permit was not released after cancellation: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("base transport calls = %d, want 2", got)
	}
}

func TestObservedRetryAfterCooldownBlocksNextDispatch(t *testing.T) {
	gate := NewPublicRateGateWithWaiter(rate.NewLimiter(1000, 1), time.Now)
	var calls atomic.Int32
	transport := &governedTransport{
		gate: gate,
		base: musicBrainzTestRoundTripper(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Header:     http.Header{"Content-Type": []string{"application/json"}, "Retry-After": []string{"1"}},
				Body:       io.NopCloser(strings.NewReader("{}")),
				Request:    request,
			}, nil
		}),
	}
	client := &http.Client{Transport: transport, Timeout: RequestTimeout}

	first, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://musicbrainz.org/ws/2/artist/first", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(first)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()

	// observeRetryAfter runs inside the dispatch permit, so the cooldown must
	// already be recorded once the first round trip has completed.
	gate.mu.Lock()
	cooldown := gate.cooldownUntil
	gate.mu.Unlock()
	if !cooldown.After(time.Now()) {
		t.Fatal("Retry-After was not observed before the dispatch permit was released")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	second, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://musicbrainz.org/ws/2/artist/second", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Do(second); err == nil {
		t.Fatal("expected server-directed cooldown to block the next dispatch")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("cooldown did not block the next dispatch: base calls = %d", got)
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
	synctest.Test(t, func(t *testing.T) {
		const interval = 100 * time.Millisecond
		gate := new(SelfHostedRateGate)
		gate.Configure(interval.Seconds())
		if err := gate.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
		gate.mu.Lock()
		lastDispatch := gate.lastDispatch
		gate.mu.Unlock()

		for range 3 {
			ctx, cancel := context.WithCancel(context.Background())
			result := make(chan error, 1)
			go func() { result <- gate.Wait(ctx) }()
			synctest.Wait() // The waiter is durably blocked on the gate timer.
			cancel()
			if err := <-result; err != context.Canceled {
				t.Fatalf("canceled wait error = %v, want %v", err, context.Canceled)
			}
			gate.mu.Lock()
			got := gate.lastDispatch
			gate.mu.Unlock()
			if !got.Equal(lastDispatch) {
				t.Fatalf("canceled wait reserved a slot: last dispatch changed from %s to %s", lastDispatch, got)
			}
		}

		if err := gate.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
		gate.mu.Lock()
		got := gate.lastDispatch
		gate.mu.Unlock()
		if want := lastDispatch.Add(interval); !got.Equal(want) {
			t.Fatalf("successful wait reserved at %s, want exactly one interval later at %s", got, want)
		}
	})
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
