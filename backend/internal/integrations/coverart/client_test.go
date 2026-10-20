package coverart

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testMBID = "12345678-1234-1234-1234-123456789abc"

var png = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("original"), 90)...)

func TestDownloadFrontStreamsOriginalAndReturnsDigest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/release/"+testMBID+"/front" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Accept") != "image/png, image/jpeg" {
			t.Errorf("Accept = %q", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	}))
	defer server.Close()
	client, _ := newClientWithEndpoint(server.Client(), server.URL)
	var got bytes.Buffer
	result, err := client.DownloadFront(context.Background(), testMBID, &got)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(png)
	if !result.Found || result.MIME != "image/png" || result.Size != int64(len(png)) || result.SHA256 != hex.EncodeToString(digest[:]) || !bytes.Equal(got.Bytes(), png) {
		t.Fatalf("result=%+v bytes match=%v", result, bytes.Equal(got.Bytes(), png))
	}
}

func TestDownloadFrontNotFound(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	client, _ := newClientWithEndpoint(server.Client(), server.URL)
	result, err := client.DownloadFront(context.Background(), testMBID, io.Discard)
	if err != nil || result.Found {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestDownloadFrontRedirectAllowAndReject(t *testing.T) {
	var start *httptest.Server
	start = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/image" {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(png)
			return
		}
		http.Redirect(w, r, start.URL+"/image", http.StatusFound)
	}))
	defer start.Close()
	client, _ := newClientWithEndpoint(start.Client(), start.URL)
	if result, err := client.DownloadFront(context.Background(), testMBID, io.Discard); err != nil || !result.Found {
		t.Fatalf("allowed redirect: result=%+v err=%v", result, err)
	}
	blocked := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://evil.example/image", http.StatusFound)
	}))
	defer blocked.Close()
	client, _ = newClientWithEndpoint(blocked.Client(), blocked.URL)
	if _, err := client.DownloadFront(context.Background(), testMBID, io.Discard); err == nil || !strings.Contains(err.Error(), "request failed") {
		t.Fatalf("rejected redirect error = %v", err)
	}
}

func TestDownloadFrontRejectsProtocolDowngrade(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("downgrade target was requested") }))
	defer plain.Close()
	start := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, plain.URL, http.StatusFound) }))
	defer start.Close()
	client, _ := newClientWithEndpoint(start.Client(), start.URL)
	if _, err := client.DownloadFront(context.Background(), testMBID, io.Discard); err == nil {
		t.Fatal("expected downgrade rejection")
	}
}

func TestDownloadFrontRejectsContentTypesAndBadImage(t *testing.T) {
	for _, test := range []struct {
		mime string
		data []byte
	}{{"text/html", png}, {"image/jpeg", png}, {"image/png", []byte("not an image")}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", test.mime)
			_, _ = w.Write(test.data)
		}))
		client, _ := newClientWithEndpoint(server.Client(), server.URL)
		if _, err := client.DownloadFront(context.Background(), testMBID, io.Discard); err == nil {
			t.Errorf("accepted %s data %q", test.mime, test.data)
		}
		server.Close()
	}
}

func TestDownloadFrontInvalidIDMakesNoRequest(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer server.Close()
	client, _ := newClientWithEndpoint(server.Client(), server.URL)
	if _, err := client.DownloadFront(context.Background(), "https://example.com/", io.Discard); err == nil || called {
		t.Fatalf("err=%v called=%v", err, called)
	}
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestDownloadFrontReturnsWriterAndCancellationErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	}))
	defer server.Close()
	client, _ := newClientWithEndpoint(server.Client(), server.URL)
	want := errors.New("disk full")
	if result, err := client.DownloadFront(context.Background(), testMBID, failingWriter{want}); !errors.Is(err, want) || result.Found {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.DownloadFront(ctx, testMBID, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error=%v", err)
	}
}

func TestAllowedRedirectRequiresHTTPSAndApprovedHosts(t *testing.T) {
	for _, raw := range []string{"https://coverartarchive.org/front", "https://images.archive.org/front", "https://archive.org/front"} {
		u, _ := http.NewRequest(http.MethodGet, raw, nil)
		if !allowedRedirect(u.URL, "") {
			t.Errorf("rejected %s", raw)
		}
	}
	for _, raw := range []string{"http://coverartarchive.org/front", "https://user@archive.org/", "https://archive.org.evil.test/", "https://evil.test/"} {
		u, _ := http.NewRequest(http.MethodGet, raw, nil)
		if allowedRedirect(u.URL, "") {
			t.Errorf("allowed %s", raw)
		}
	}
}

func TestDownloadFrontPreservesCallerRedirectRestrictions(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/image" {
			t.Error("caller-forbidden redirect target was requested")
			return
		}
		http.Redirect(w, r, server.URL+"/image", http.StatusFound)
	}))
	defer server.Close()
	caller := server.Client()
	checks := 0
	caller.CheckRedirect = func(*http.Request, []*http.Request) error {
		checks++
		return errors.New("caller disallows redirects")
	}
	client, err := newClientWithEndpoint(caller, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.DownloadFront(context.Background(), testMBID, io.Discard)
	if err == nil || result.Found || checks != 1 {
		t.Fatalf("caller redirect restriction ignored: %+v, %v, checks=%d", result, err, checks)
	}
	if err := caller.CheckRedirect(nil, nil); err == nil || checks != 2 {
		t.Fatal("shared caller redirect policy was mutated")
	}
}

func TestTestRedirectSeamAllowsOnlyExactTLSOrigin(t *testing.T) {
	for _, raw := range []string{"https://127.0.0.1:8443/image", "https://127.0.0.1:9443/image", "http://127.0.0.1:8443/image"} {
		request, err := http.NewRequest(http.MethodGet, raw, nil)
		if err != nil {
			t.Fatal(err)
		}
		want := raw == "https://127.0.0.1:8443/image"
		if got := allowedRedirect(request.URL, "127.0.0.1:8443"); got != want {
			t.Fatalf("test redirect %s = %v, want %v", raw, got, want)
		}
	}
}
