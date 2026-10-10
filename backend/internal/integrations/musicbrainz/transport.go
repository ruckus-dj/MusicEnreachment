package musicbrainz

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// ResponseBodyLimit bounds the decompressed response kept for the SDK and raw
// provider projection.
const ResponseBodyLimit = 8 << 20

type requestCollectorKey struct{}

type responseCollector struct {
	mu           sync.Mutex
	responses    [][]byte
	contentTypes []string
}

func (c *responseCollector) add(body []byte, contentType string) {
	c.mu.Lock()
	c.responses = append(c.responses, append([]byte(nil), body...))
	c.contentTypes = append(c.contentTypes, contentType)
	c.mu.Unlock()
}

func (c *responseCollector) latestContentType() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.contentTypes) == 0 {
		return ""
	}
	return c.contentTypes[len(c.contentTypes)-1]
}

func (c *responseCollector) last() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.responses) == 0 {
		return nil
	}
	return append([]byte(nil), c.responses[len(c.responses)-1]...)
}

// RequestWaiter is injectable to make limiter behavior deterministic in tests.
type RequestWaiter interface {
	Wait(context.Context) error
}

type rateWaiter struct{ limiter *rate.Limiter }

// Limit exposes the underlying limiter rate so the gate can derive its
// completion spacing from the same budget the waiter enforces.
func (w rateWaiter) Limit() rate.Limit { return w.limiter.Limit() }

func (w rateWaiter) Wait(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		now := time.Now()
		reservation := w.limiter.ReserveN(now, 1)
		if !reservation.OK() {
			return errors.New("musicbrainz rate limit reservation failed")
		}
		delay := reservation.DelayFrom(now)
		if delay <= 0 {
			if err := ctx.Err(); err != nil {
				reservation.CancelAt(time.Now())
				return err
			}
			return nil
		}
		// Do not leave a future reservation behind while sleeping. If the
		// timer wakes late, reserve again at the current time so the next
		// admission is still spaced from this one rather than from a stale slot.
		reservation.CancelAt(now)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// PublicRateGate is intended to be shared by every MusicBrainz client. Both
// the SDK and connectivity checks must use this same gate instance.
type PublicRateGate struct {
	waiter        RequestWaiter
	now           func() time.Time
	interval      time.Duration
	mu            sync.Mutex
	cooldownUntil time.Time
	lastCompleted time.Time
	serial        chan struct{}
	dispatch      chan struct{}
}

// limitProvider is implemented by waiters backed by a rate.Limiter so the gate
// can derive the completion spacing floor from the configured limit.
type limitProvider interface{ Limit() rate.Limit }

// gateInterval converts a waiter's rate limit into the minimum spacing between
// one dispatch completing and the next being admitted. Waiters that do not
// expose a limit fall back to the public one-request-per-second default.
func gateInterval(waiter RequestWaiter) time.Duration {
	if provider, ok := waiter.(limitProvider); ok {
		if limit := provider.Limit(); limit > 0 {
			return time.Duration(float64(time.Second) / float64(limit))
		}
	}
	return time.Second
}

func NewPublicRateGate() *PublicRateGate {
	return NewPublicRateGateWithWaiter(rateWaiter{rate.NewLimiter(rate.Every(time.Second), 1)}, time.Now)
}

func NewPublicRateGateWithWaiter(waiter RequestWaiter, now func() time.Time) *PublicRateGate {
	if now == nil {
		now = time.Now
	}
	return &PublicRateGate{waiter: waiter, now: now, interval: gateInterval(waiter)}
}

func (g *PublicRateGate) wait(ctx context.Context) error {
	if g == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	g.mu.Lock()
	if g.serial == nil {
		g.serial = make(chan struct{}, 1)
		g.serial <- struct{}{}
	}
	serial := g.serial
	g.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-serial:
	}
	defer func() { serial <- struct{}{} }()
	for {
		g.mu.Lock()
		now := g.now()
		delay := g.cooldownUntil.Sub(now)
		// Space admission from the previous dispatch's completion as well as
		// from the protocol cooldown. This is a time floor only: it does not
		// consume an extra token, so the waiter budget is not double charged.
		if pace := g.lastCompleted.Add(g.interval).Sub(now); pace > delay {
			delay = pace
		}
		g.mu.Unlock()
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			continue
		}
		// Wait only after protocol cooldown so the token bucket is not spent
		// while cooling down. Recheck afterward because another response may
		// extend the server-directed cooldown while this call is queued.
		if g.waiter != nil {
			if err := g.waiter.Wait(ctx); err != nil {
				return err
			}
		}
		g.mu.Lock()
		stillCooling := g.cooldownUntil.After(g.now())
		g.mu.Unlock()
		if !stillCooling {
			return nil
		}
	}
}

// dispatchRoundTrip serializes the entire public round trip behind the gate's
// dispatch permit. Holding the permit across the underlying RoundTrip (not only
// across admission) prevents a descheduled caller from being overtaken by a
// later request, which would otherwise dispatch out of order. The permit is
// released only after fn returns and the completion time is recorded, so the
// next dispatch is spaced from this completion by at least g.interval. The
// dispatch permit is a separate channel from wait's admission permit, so
// waiting here never reenters wait and cannot deadlock.
func (g *PublicRateGate) dispatchRoundTrip(ctx context.Context, fn func() (*http.Response, error)) (*http.Response, error) {
	if g == nil {
		return fn()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g.mu.Lock()
	if g.dispatch == nil {
		g.dispatch = make(chan struct{}, 1)
		g.dispatch <- struct{}{}
	}
	dispatch := g.dispatch
	g.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-dispatch:
	}
	defer func() { dispatch <- struct{}{} }()
	if err := g.wait(ctx); err != nil {
		return nil, err
	}
	resp, err := fn()
	g.mu.Lock()
	g.lastCompleted = g.now()
	g.mu.Unlock()
	return resp, err
}

func (g *PublicRateGate) observeRetryAfter(value string, now time.Time) {
	if g == nil {
		return
	}
	value = strings.TrimSpace(value)
	var until time.Time
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds > 0 {
		const maxSeconds = int64((1<<63 - 1) / int64(time.Second))
		if seconds > maxSeconds {
			until = now.Add(time.Duration(1<<63 - 1))
		} else {
			until = now.Add(time.Duration(seconds) * time.Second)
		}
	} else if date, err := http.ParseTime(value); err == nil && date.After(now) {
		until = date
	}
	if until.IsZero() {
		return
	}
	g.mu.Lock()
	if until.After(g.cooldownUntil) {
		g.cooldownUntil = until
	}
	g.mu.Unlock()
}

type governedTransport struct {
	base             http.RoundTripper
	gate             *PublicRateGate
	selfHosted       *selfHostedGate
	sharedSelfHosted *SelfHostedRateGate
}

func (t *governedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	public := isPublicMusicBrainzHost(req.URL.Hostname())
	if !public && t.selfHosted != nil {
		if err := t.selfHosted.wait(req.Context()); err != nil {
			return nil, err
		}
	}
	if !public && t.sharedSelfHosted != nil {
		if err := t.sharedSelfHosted.Wait(req.Context()); err != nil {
			return nil, err
		}
	}
	roundTrip := func() (*http.Response, error) {
		response, roundTripErr := base.RoundTrip(req)
		if roundTripErr != nil {
			return nil, roundTripErr
		}
		if public && t.gate != nil && (response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusServiceUnavailable) {
			// Observe the server-directed cooldown before the dispatch permit is
			// released so the next dispatch is admitted against it.
			t.gate.observeRetryAfter(response.Header.Get("Retry-After"), t.gate.now())
		}
		return response, nil
	}
	var resp *http.Response
	var err error
	if public && t.gate != nil {
		resp, err = t.gate.dispatchRoundTrip(req.Context(), roundTrip)
	} else {
		resp, err = roundTrip()
	}
	if err != nil {
		return nil, err
	}
	if collector, ok := req.Context().Value(requestCollectorKey{}).(*responseCollector); ok && resp.Body != nil {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, ResponseBodyLimit+1))
		closeErr := resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read musicbrainz response: %w", readErr)
		}
		collector.add(body, resp.Header.Get("Content-Type"))
		resp.Body = io.NopCloser(bytes.NewReader(body))
		if closeErr != nil {
			return nil, fmt.Errorf("close musicbrainz response: %w", closeErr)
		}
	}
	return resp, nil
}

func isPublicMusicBrainzHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return host == "musicbrainz.org" || strings.HasSuffix(host, ".musicbrainz.org")
}

type selfHostedGate struct {
	limiter *rate.Limiter
}

// SelfHostedRateGate retains one request budget while its configured interval
// changes. Applications share it across providers built from runtime settings.
type SelfHostedRateGate struct {
	mu           sync.Mutex
	interval     time.Duration
	lastDispatch time.Time
	serial       chan struct{}
}

func (g *SelfHostedRateGate) Configure(delaySeconds float64) {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.interval = time.Duration(delaySeconds * float64(time.Second))
	g.mu.Unlock()
}

func (g *SelfHostedRateGate) Wait(ctx context.Context) error {
	if g == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	g.mu.Lock()
	if g.serial == nil {
		g.serial = make(chan struct{}, 1)
		g.serial <- struct{}{}
	}
	serial := g.serial
	g.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-serial:
	}
	defer func() { serial <- struct{}{} }()
	g.mu.Lock()
	interval := g.interval
	lastDispatch := g.lastDispatch
	g.mu.Unlock()
	if interval <= 0 {
		return ctx.Err()
	}
	delay := time.Until(lastDispatch.Add(interval))
	if delay <= 0 {
		g.mu.Lock()
		g.lastDispatch = time.Now()
		g.mu.Unlock()
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		if err := ctx.Err(); err != nil {
			return err
		}
		g.mu.Lock()
		g.lastDispatch = time.Now()
		g.mu.Unlock()
		return nil
	}
}

func (g *selfHostedGate) wait(ctx context.Context) error {
	if g == nil || g.limiter == nil {
		return nil
	}
	return g.limiter.Wait(ctx)
}

// NewGovernedHTTPClient constructs the shared HTTP transport used by the
// existing connectivity checker and all SDK adapters.
func NewGovernedHTTPClient(gate *PublicRateGate) *http.Client {
	return &http.Client{Transport: &governedTransport{base: http.DefaultTransport, gate: gate}, Timeout: RequestTimeout}
}
