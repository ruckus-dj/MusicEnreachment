package acoustid

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

const (
	applicationKey = "secret-application-key"
	fingerprint    = "AQAA..."
	recordingID    = "00112233-4455-6677-8899-aabbccddeeff"
	releaseID      = "10112233-4455-6677-8899-aabbccddeeff"
)

type testWaiter struct {
	calls int
	err   error
}

func (w *testWaiter) Wait(context.Context) error {
	w.calls++
	return w.err
}

// blockingWaiter blocks until the context is done, modeling a limiter that
// cannot make progress, so tests can verify the derived deadline bounds it.
type blockingWaiter struct{}

func (blockingWaiter) Wait(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestLookupPostsGzipRequestAndPreservesDistinctIDs(t *testing.T) {
	responseBody := `{"status":"ok","results":[{"score":0,"recordings":[{"id":"` + recordingID + `","title":"Song","releases":[{"id":"` + releaseID + `"},{"id":"` + strings.ToUpper(releaseID) + `"}],"releasegroups":[{"id":"20112233-4455-6677-8899-aabbccddeeff"}],"tracks":[{"id":"30112233-4455-6677-8899-aabbccddeeff"}]},{"id":"` + strings.ToUpper(recordingID) + `","releases":[{"id":"40112233-4455-6677-8899-aabbccddeeff"}]}]},{"recordings":[{"id":"50112233-4455-6677-8899-aabbccddeeff"}]}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.RawQuery != "" {
			t.Errorf("request method/query = %s %q", r.Method, r.URL.RawQuery)
		}
		if strings.Contains(r.URL.String(), applicationKey) {
			t.Error("application key appeared in URL")
		}
		if got := r.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type = %q", got)
		}
		if got := r.Header.Get("Accept-Encoding"); got != "gzip" {
			t.Errorf("Accept-Encoding = %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		form, err := url.ParseQuery(string(body))
		if err != nil {
			t.Errorf("parse body: %v", err)
		}
		if form.Get("client") != applicationKey || form.Get("fingerprint") != fingerprint || form.Get("duration") != "0" {
			t.Errorf("unexpected form values: %#v", form)
		}
		// Assert the wire form with an independent literal rather than
		// requestMeta: Encode must emit the four meta tokens joined by "+" so the
		// server decodes them to spaces and splits on whitespace.
		if !strings.Contains(string(body), "meta=recordings+releases+releasegroups+tracks") {
			t.Errorf("meta wire form = %q", string(body))
		}
		metaFields := strings.Fields(form.Get("meta"))
		if len(metaFields) != 4 || metaFields[0] != "recordings" || metaFields[1] != "releases" || metaFields[2] != "releasegroups" || metaFields[3] != "tracks" {
			t.Errorf("meta = %q", form.Get("meta"))
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Encoding", "gzip")
		writer := gzip.NewWriter(w)
		_, _ = io.WriteString(writer, responseBody)
		_ = writer.Close()
	}))
	defer server.Close()
	waiter := &testWaiter{}
	client := NewClientWithEndpoint(server.Client(), waiter, server.URL)
	result, err := client.Lookup(context.Background(), applicationKey, fingerprint, 0)
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if !result.Available || len(result.Matches) != 2 {
		t.Fatalf("result = %#v", result)
	}
	first := result.Matches[0]
	if first.Score == nil || *first.Score != 0 {
		t.Errorf("zero score must be present, got %#v", first.Score)
	}
	if len(first.RecordingIDs) != 1 || first.RecordingIDs[0] != recordingID {
		t.Errorf("recording IDs = %#v", first.RecordingIDs)
	}
	if len(first.ReleaseIDs) != 2 || first.ReleaseIDs[0] != releaseID || first.ReleaseIDs[1] != "40112233-4455-6677-8899-aabbccddeeff" || len(first.ReleaseGroupIDs) != 1 || len(first.TrackIDs) != 1 {
		t.Errorf("related IDs not preserved: %#v", first)
	}
	if len(first.Raw) == 0 || !strings.Contains(string(first.Raw), `"title":"Song"`) || result.Matches[1].Score != nil {
		t.Errorf("raw/omitted score not preserved: %#v", result.Matches)
	}
	if waiter.calls != 1 {
		t.Errorf("limiter calls = %d", waiter.calls)
	}
}

func TestMissingKeyDoesNotWaitOrRequest(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer server.Close()
	waiter := &testWaiter{}
	result, err := NewClientWithEndpoint(server.Client(), waiter, server.URL).Lookup(context.Background(), " \t", fingerprint, 1)
	if err != nil || result.Available || len(result.Matches) != 0 {
		t.Fatalf("Lookup() = %#v, %v", result, err)
	}
	if waiter.calls != 0 || calls != 0 {
		t.Errorf("waiter/request calls = %d/%d", waiter.calls, calls)
	}
}

func TestLookupRejectsInvalidInputWithoutRequest(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer server.Close()
	client := NewClientWithEndpoint(server.Client(), nil, server.URL)
	if _, err := client.Lookup(context.Background(), applicationKey, fingerprint, -1); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("negative duration error = %v", err)
	}
	if calls != 0 {
		t.Errorf("request calls = %d", calls)
	}
}

func TestLookupErrorsAreSafeAndRejectMalformedResponses(t *testing.T) {
	for _, test := range []struct {
		name        string
		contentType string
		status      int
		body        string
		oversized   bool
	}{
		{name: "malformed", contentType: "application/json", status: http.StatusOK, body: `{"status":"ok",`},
		{name: "wrong content type", contentType: "text/plain", status: http.StatusOK, body: applicationKey},
		{name: "bad score", contentType: "application/json", status: http.StatusOK, body: `{"status":"ok","results":[{"score":1.1}]}`},
		{name: "remote error", contentType: "application/json", status: http.StatusBadGateway, body: applicationKey},
		{name: "oversized", contentType: "application/json", status: http.StatusOK, oversized: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", test.contentType)
				w.WriteHeader(test.status)
				body := test.body
				if test.oversized {
					body = strings.Repeat(" ", MaxResponseSize+1)
				}
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			_, err := NewClientWithEndpoint(server.Client(), nil, server.URL).Lookup(context.Background(), applicationKey, fingerprint, 1)
			if err == nil || strings.Contains(err.Error(), applicationKey) || test.body != "" && strings.Contains(err.Error(), test.body) {
				t.Errorf("unsafe/unexpected error = %v", err)
			}
		})
	}
}

func TestLookupHonorsCancellationAndLimiterFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewClientWithEndpoint(server.Client(), nil, server.URL).Lookup(ctx, applicationKey, fingerprint, 1)
	if !errors.Is(err, ErrRequest) {
		t.Errorf("cancellation error = %v", err)
	}
	failedWaiter := &testWaiter{err: context.Canceled}
	_, err = NewClientWithEndpoint(server.Client(), failedWaiter, server.URL).Lookup(context.Background(), applicationKey, fingerprint, 1)
	if !errors.Is(err, ErrRequest) || failedWaiter.calls != 1 {
		t.Errorf("limiter error/calls = %v/%d", err, failedWaiter.calls)
	}
}

func TestLookupBoundsBlockingWaiter(t *testing.T) {
	for _, test := range []struct {
		name          string
		clientTimeout time.Duration
		callerTimeout time.Duration
	}{
		{name: "client timeout", clientTimeout: 50 * time.Millisecond},
		{name: "caller deadline earlier than client timeout", clientTimeout: time.Hour, callerTimeout: 50 * time.Millisecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls int
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
			defer server.Close()
			client := NewClientWithEndpoint(&http.Client{Timeout: test.clientTimeout}, blockingWaiter{}, server.URL)
			ctx := context.Background()
			if test.callerTimeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, test.callerTimeout)
				defer cancel()
			}
			start := time.Now()
			_, err := client.Lookup(ctx, applicationKey, fingerprint, 1)
			if !errors.Is(err, ErrRequest) {
				t.Fatalf("blocking waiter error = %v, want ErrRequest", err)
			}
			if calls != 0 {
				t.Errorf("HTTP calls = %d, want 0", calls)
			}
			if elapsed := time.Since(start); elapsed > time.Second {
				t.Errorf("lookup exceeded derived deadline, elapsed = %v", elapsed)
			}
		})
	}
}

func TestValidMBID(t *testing.T) {
	for _, test := range []struct {
		name string
		id   string
		want bool
	}{
		{name: "canonical lowercase", id: "00112233-4455-6677-8899-aabbccddeeff", want: true},
		{name: "canonical uppercase", id: "00112233-4455-6677-8899-AABBCCDDEEFF", want: true},
		{name: "nil uuid", id: "00000000-0000-0000-0000-000000000000", want: false},
		{name: "hyphenless", id: "00112233445566778899aabbccddeeff", want: false},
		{name: "urn prefixed", id: "urn:uuid:00112233-4455-6677-8899-aabbccddeeff", want: false},
		{name: "short", id: "00112233-4455-6677-8899-aabbccddeef", want: false},
		{name: "non hex", id: "00112233-4455-6677-8899-aabbccddeefg", want: false},
		{name: "empty", id: "", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := validMBID(test.id); got != test.want {
				t.Errorf("validMBID(%q) = %v, want %v", test.id, got, test.want)
			}
		})
	}
}

func TestCollectNestedIDsIsDeterministic(t *testing.T) {
	recording := json.RawMessage(`{
		"id": "00112233-4455-6677-8899-aabbccddeeff",
		"releases": [{"id": "10112233-4455-6677-8899-aabbccddeeff"}, {"id": "11112233-4455-6677-8899-aabbccddeeff"}],
		"zeta": {"releases": [{"id": "20112233-4455-6677-8899-aabbccddeeff"}]},
		"alpha": {"releases": [{"id": "30112233-4455-6677-8899-aabbccddeeff"}, {"id": "31112233-4455-6677-8899-aabbccddeeff"}]}
	}`)
	// Sorted-key depth-first traversal, preserving array order: alpha, releases, zeta.
	want := []string{
		"30112233-4455-6677-8899-aabbccddeeff",
		"31112233-4455-6677-8899-aabbccddeeff",
		"10112233-4455-6677-8899-aabbccddeeff",
		"11112233-4455-6677-8899-aabbccddeeff",
		"20112233-4455-6677-8899-aabbccddeeff",
	}
	for i := 0; i < 100; i++ {
		var ids []string
		if err := collectNestedIDs(recording, "releases", &ids); err != nil {
			t.Fatalf("collectNestedIDs() error = %v", err)
		}
		if !reflect.DeepEqual(ids, want) {
			t.Fatalf("iteration %d: ids = %#v, want %#v", i, ids, want)
		}
	}
}
