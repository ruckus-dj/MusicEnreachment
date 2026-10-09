package musicbrainz

import (
	"bytes"
	"context"
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

func (w rateWaiter) Wait(ctx context.Context) error { return w.limiter.Wait(ctx) }

// PublicRateGate is intended to be shared by every MusicBrainz client. Both
// the SDK and connectivity checks must use this same gate instance.
type PublicRateGate struct {
	waiter        RequestWaiter
	now           func() time.Time
	mu            sync.Mutex
	cooldownUntil time.Time
}

func NewPublicRateGate() *PublicRateGate {
	return NewPublicRateGateWithWaiter(rateWaiter{rate.NewLimiter(rate.Every(time.Second), 1)}, time.Now)
}

func NewPublicRateGateWithWaiter(waiter RequestWaiter, now func() time.Time) *PublicRateGate {
	if now == nil {
		now = time.Now
	}
	return &PublicRateGate{waiter: waiter, now: now}
}

func (g *PublicRateGate) wait(ctx context.Context) error {
	if g == nil {
		return nil
	}
	for {
		g.mu.Lock()
		delay := g.cooldownUntil.Sub(g.now())
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
	base       http.RoundTripper
	gate       *PublicRateGate
	selfHosted *selfHostedGate
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
	if public && t.gate != nil {
		if err := t.gate.wait(req.Context()); err != nil {
			return nil, err
		}
	}
	resp, err := base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if public && t.gate != nil && (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable) {
		t.gate.observeRetryAfter(resp.Header.Get("Retry-After"), t.gate.now())
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
