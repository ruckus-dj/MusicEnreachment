package musicbrainz

import (
	"context"
	"net/http"
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
