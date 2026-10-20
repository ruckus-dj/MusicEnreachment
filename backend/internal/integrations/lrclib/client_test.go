package lrclib

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLookupRejectsNonFiniteDurationWithoutRequest(t *testing.T) {
	client, _ := testClient(t, func(http.ResponseWriter, *http.Request) {
		t.Error("non-finite duration reached provider")
	})
	for _, duration := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := client.Lookup(context.Background(), Metadata{Duration: duration}); err == nil {
			t.Fatalf("accepted non-finite duration %v", duration)
		}
	}
}

func testClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	client, err := NewClientWithEndpoint(server.Client(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)
	return client, server
}

func TestLookupUsesExactMetadataAndPreservesSyncedLyrics(t *testing.T) {
	want := "[00:01.23]line one\n[00:02.34]line two\n"
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/get" {
			t.Errorf("path = %q", r.URL.Path)
		}
		wantQuery := url.Values{"artist_name": {"Artist & Co"}, "track_name": {"Title"}, "album_name": {"Album"}, "duration": {"123"}}
		if r.URL.Query().Encode() != wantQuery.Encode() {
			t.Errorf("query = %v", r.URL.Query())
		}
		if got := r.UserAgent(); got != UserAgent {
			t.Errorf("user-agent = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":42,"trackName":"Title","artistName":"Artist & Co","albumName":"Album","duration":123,"syncedLyrics":"[00:01.23]line one\n[00:02.34]line two\n","plainLyrics":"must not replace synced"}`)
	})
	result, err := client.Lookup(context.Background(), Metadata{Artist: "Artist & Co", Title: "Title", Album: "Album", Duration: 123})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Found || result.Lyrics != want {
		t.Fatalf("result = %#v", result)
	}
}

func TestLookupSendsExactFractionalDuration(t *testing.T) {
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("duration"); got != "123.456" {
			t.Errorf("duration = %q, want exact seconds 123.456", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"syncedLyrics":"[00:01.00]line"}`)
	})
	result, err := client.Lookup(context.Background(), Metadata{Artist: "Artist", Title: "Title", Album: "Album", Duration: 123.456})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Found || result.Lyrics != "[00:01.00]line" {
		t.Fatalf("result = %#v", result)
	}
}

func TestLookupAbsenceCases(t *testing.T) {
	for name, testCase := range map[string]struct {
		status int
		body   string
	}{
		"plain only":   {http.StatusOK, `{"plainLyrics":"plain text"}`},
		"instrumental": {http.StatusOK, `{"instrumental":true,"syncedLyrics":"[00:01.00]"}`},
		"not found":    {http.StatusNotFound, `not json`},
	} {
		t.Run(name, func(t *testing.T) {
			client, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(testCase.status)
				_, _ = io.WriteString(w, testCase.body)
			})
			result, err := client.Lookup(context.Background(), Metadata{})
			if err != nil || result.Found || result.Lyrics != "" {
				t.Fatalf("result=%#v err=%v", result, err)
			}
		})
	}
}

func TestRetryAfterStatusErrors(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			client, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", "17")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, "secret lyric text")
			})
			_, err := client.Lookup(context.Background(), Metadata{})
			requestErr, ok := err.(*RequestError)
			if !ok || requestErr.StatusCode != status || requestErr.RetryAfter == nil {
				t.Fatalf("error = %#v", err)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("provider body leaked: %v", err)
			}
		})
	}
}

func TestParseRetryAfterDateAndMissing(t *testing.T) {
	want := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	if got := parseRetryAfter(want.Format(http.TimeFormat), time.Now()); got == nil || !got.Equal(want) {
		t.Fatalf("date = %v", got)
	}
	if got := parseRetryAfter("invalid", time.Now()); got != nil {
		t.Fatalf("invalid value = %v", got)
	}
}

func TestParseRetryAfterSecondsIsDeterministicAndGuardsOverflow(t *testing.T) {
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	if got := parseRetryAfter("17", now); got == nil || !got.Equal(now.Add(17*time.Second)) {
		t.Fatalf("seconds = %v", got)
	}
	if got := parseRetryAfter("0", now); got == nil || !got.Equal(now) {
		t.Fatalf("zero = %v", got)
	}
	if got := parseRetryAfter("-5", now); got != nil {
		t.Fatalf("negative = %v", got)
	}
	if got := parseRetryAfter("", now); got != nil {
		t.Fatalf("absent = %v", got)
	}
	// A value that parses as int64 but overflows time.Duration multiplication,
	// and one beyond int64, are both invalid rather than wrapping.
	if got := parseRetryAfter("9223372037", now); got != nil {
		t.Fatalf("overflow = %v", got)
	}
	if got := parseRetryAfter("9223372036854775807", now); got != nil {
		t.Fatalf("max = %v", got)
	}
	if got := parseRetryAfter("9223372036854775808", now); got != nil {
		t.Fatalf("beyond int64 = %v", got)
	}
}

func TestLookupAcceptsMetadataHourTimestampsAndMultipleTags(t *testing.T) {
	want := "[ar:Artist]\n[ti:Title]\n[offset:0]\n[00:12.00]line one\n[01:02:03.45][01:02:05.00]shared line\n"
	client, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"syncedLyrics":`+strconv.Quote(want)+`}`)
	})
	result, err := client.Lookup(context.Background(), Metadata{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Found || result.Lyrics != want {
		t.Fatalf("result = %#v", result)
	}
}

func TestLookupAllowsLiteralBracketLyricAfterTimestamp(t *testing.T) {
	want := "[00:12.00][Chorus]sing along\n[00:15.00]plain line\n"
	client, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"syncedLyrics":`+strconv.Quote(want)+`}`)
	})
	result, err := client.Lookup(context.Background(), Metadata{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Found || result.Lyrics != want {
		t.Fatalf("result = %#v", result)
	}
}

func TestMalformedResponsesDoNotExposeBody(t *testing.T) {
	for _, body := range []string{"{", "provider secret plain error"} {
		client, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) })
		_, err := client.Lookup(context.Background(), Metadata{})
		if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "provider") {
			t.Fatalf("error = %v", err)
		}
	}
}

func TestSearchReturnsCandidatesWithoutSelectingAndIsExplicit(t *testing.T) {
	requests := 0
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/api/search" || r.URL.Query().Get("artist_name") != "Artist" || r.URL.Query().Get("track_name") != "Track" {
			t.Errorf("request = %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `[{"id":1,"trackName":"first"},{"id":2,"trackName":"second"}]`)
	})
	tracks, err := client.Search(context.Background(), SearchQuery{Artist: "Artist", Title: "Track"})
	if err != nil || len(tracks) != 2 || tracks[0].ID != 1 || tracks[1].ID != 2 {
		t.Fatalf("tracks=%#v err=%v", tracks, err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d", requests)
	}
}

func TestLookupDoesNotAcceptMalformedSyncedLyrics(t *testing.T) {
	for name, lyrics := range map[string]string{
		"no timestamp":             "no timestamp",
		"metadata only":            "[ar:Artist]",
		"negative seconds":         "[00:-1]line",
		"seconds out of range":     "[00:60.00]line",
		"too many fraction digits": "[00:01.2345]line",
		"missing colon":            "[00.00]line",
		"leading bracket text":     "[Chorus]sing",
	} {
		t.Run(name, func(t *testing.T) {
			client, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, `{"syncedLyrics":`+strconv.Quote(lyrics)+`}`)
			})
			_, err := client.Lookup(context.Background(), Metadata{})
			if err == nil || err.Error() != "invalid lrclib synced lyrics" {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestCanceledAndExpiredContextsPreserveCause(t *testing.T) {
	if _, err := NewClientWithEndpoint(nil, "file:///tmp/lrclib"); err == nil {
		t.Fatal("expected endpoint error")
	}
	client, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Lookup(ctx, Metadata{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error = %v", err)
	}

	expired, expire := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer expire()
	if _, err := client.Lookup(expired, Metadata{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline error = %v", err)
	}
}

func TestLookupBodyReadFailureAfterCancelPreservesCause(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client, err := NewClientWithEndpoint(nil, "https://lrclib.test")
	if err != nil {
		t.Fatal(err)
	}
	client.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: cancelOnReadBody{cancel: cancel}, Request: req}, nil
	})}
	if _, err := client.Lookup(ctx, Metadata{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

type cancelOnReadBody struct{ cancel context.CancelFunc }

// Read cancels the request context and then fails, modeling a body read that is
// interrupted after the response headers were already received.
func (b cancelOnReadBody) Read([]byte) (int, error) {
	b.cancel()
	return 0, errors.New("read failed")
}

func (b cancelOnReadBody) Close() error { return nil }

func TestResponsesAreClosed(t *testing.T) {
	closed := false
	client, server := testClient(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{}`) })
	server.Client().Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: closeRecorder{Reader: strings.NewReader(`{}`), closed: &closed}, Request: req}, nil
	})
	client.httpClient = server.Client()
	if _, err := client.Lookup(context.Background(), Metadata{}); err != nil {
		t.Fatal(err)
	}
	if !closed {
		t.Fatal("response body was not closed")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type closeRecorder struct {
	io.Reader
	closed *bool
}

func (r closeRecorder) Close() error { *r.closed = true; return nil }
