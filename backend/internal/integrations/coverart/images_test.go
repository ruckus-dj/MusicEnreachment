package coverart

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testImageID = "456"

func TestListImagesReturnsOnlyApprovedValidDeduplicatedEntries(t *testing.T) {
	client, server := coverArtTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/release/"+testMBID {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q", got)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = io.WriteString(w, `{"images":[
			{"id":"100","approved":true,"front":true,"back":false,"types":["Front"],"comment":"front",
			 "image":"https://evil.example/100.jpg",
			 "thumbnails":{"250":"https://evil.example/100-250.jpg"}},
			{"id":"200","front":false,"back":true,"types":["Back"],"comment":"omitted approval"},
			{"id":"300","approved":false,"front":true,"types":["Front"],"comment":"unapproved"},
			{"id":"abc","approved":false,"comment":"unapproved malformed ID"},
			{"id":"100","approved":true,"front":false,"types":["Other"],"comment":"duplicate"},
			{"id":"400","approved":true,"types":["Booklet"],"comment":""}
		]}`)
	})

	images, err := client.ListImages(context.Background(), testMBID)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 3 {
		t.Fatalf("images = %+v", images)
	}
	if images[0].ID != "100" || !images[0].Front || images[0].Back || len(images[0].Types) != 1 || images[0].Types[0] != "Front" || images[0].Comment != "front" {
		t.Fatalf("first image = %+v", images[0])
	}
	if want := server.URL + "/release/" + testMBID + "/100-250.jpg"; images[0].ThumbnailURL != want {
		t.Fatalf("first thumbnail = %q, want %q", images[0].ThumbnailURL, want)
	}
	if images[1].ID != "200" || images[1].Front || !images[1].Back || images[1].Types[0] != "Back" || images[1].Comment != "omitted approval" {
		t.Fatalf("second image = %+v", images[1])
	}
	if images[2].ID != "400" || images[2].Front || images[2].Back || images[2].Types[0] != "Booklet" {
		t.Fatalf("third image = %+v", images[2])
	}
	for _, image := range images {
		if strings.Contains(image.ThumbnailURL, "evil.example") {
			t.Fatalf("upstream thumbnail URL was accepted: %+v", image)
		}
	}
}

func TestListImagesAcceptsNumericImageIDs(t *testing.T) {
	client, server := coverArtTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"images":[
			{"id":26189693047,"approved":true,"front":true,"types":["Front"]},
			{"id":18446744073709551615,"approved":true},
			{"id":26189693047,"approved":true,"comment":"duplicate number"},
			{"id":"26189693047","approved":true,"comment":"duplicate string form"},
			{"id":0,"approved":false,"comment":"unapproved numeric zero"},
			{"id":"456","approved":true,"comment":"legacy string"}
		]}`)
	})
	images, err := client.ListImages(context.Background(), testMBID)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 3 {
		t.Fatalf("images = %+v", images)
	}
	if images[0].ID != "26189693047" || !images[0].Front {
		t.Fatalf("first image = %+v", images[0])
	}
	if images[1].ID != "18446744073709551615" {
		t.Fatalf("second image = %+v", images[1])
	}
	if images[2].ID != "456" {
		t.Fatalf("third image = %+v", images[2])
	}
	if want := server.URL + "/release/" + testMBID + "/26189693047-250.jpg"; images[0].ThumbnailURL != want {
		t.Fatalf("first thumbnail = %q, want %q", images[0].ThumbnailURL, want)
	}
}

func TestListImagesRejectsMalformedApprovedImageID(t *testing.T) {
	for _, entry := range []string{
		`{"id":""}`,
		`{"id":"0"}`,
		`{"id":"-1"}`,
		`{"id":"abc"}`,
		`{"id":"00101"}`,
		`{"id":"1/2"}`,
		`{"id":"18446744073709551616"}`,
		`{"id":"","approved":true}`,
		`{"id":0}`,
		`{"id":-1}`,
		`{"id":1.5}`,
		`{"id":1e3}`,
		`{"id":18446744073709551616}`,
		`{"id":true}`,
		`{"id":null}`,
		`{"id":"100"},{"id":"bad"}`,
		`{"id":100},{"id":-2}`,
	} {
		client, _ := coverArtTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"images":[`+entry+`]}`)
		})
		if images, err := client.ListImages(context.Background(), testMBID); err == nil {
			t.Fatalf("accepted %s: images=%+v", entry, images)
		}
	}
}

func TestListImagesNotFoundIsNormalAbsence(t *testing.T) {
	client, _ := coverArtTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	images, err := client.ListImages(context.Background(), testMBID)
	if err != nil || images == nil || len(images) != 0 {
		t.Fatalf("images=%+v err=%v", images, err)
	}
}

func TestListImagesExplicitEmptyArrayIsDefinitiveAbsence(t *testing.T) {
	client, _ := coverArtTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"images":[]}`)
	})
	images, err := client.ListImages(context.Background(), testMBID)
	if err != nil || images == nil || len(images) != 0 {
		t.Fatalf("images=%+v err=%v", images, err)
	}
}

func TestListImagesRejectsMissingOrNullImages(t *testing.T) {
	for _, body := range []string{`{}`, `{"images":null}`, `null`} {
		client, _ := coverArtTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, body)
		})
		if images, err := client.ListImages(context.Background(), testMBID); err == nil {
			t.Fatalf("accepted %s: images=%+v", body, images)
		}
	}
}

func TestListImagesNotFoundAfterRedirectIsTemporary(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/release/"+testMBID+"/index.json" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, server.URL+"/release/"+testMBID+"/index.json", http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	client, err := newClientWithEndpoint(server.Client(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if images, err := client.ListImages(context.Background(), testMBID); err == nil {
		t.Fatalf("redirected 404 treated as absence: images=%+v", images)
	}
}

func TestListImagesInvalidMBIDMakesNoRequest(t *testing.T) {
	called := false
	client, _ := coverArtTestClient(t, func(http.ResponseWriter, *http.Request) { called = true })
	if _, err := client.ListImages(context.Background(), "https://evil.example/release"); err == nil || called {
		t.Fatalf("err=%v called=%v", err, called)
	}
}

func TestListImagesRejectsInvalidResponses(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		status      int
		body        func(io.Writer)
	}{
		{"status", "application/json", http.StatusInternalServerError, func(w io.Writer) { _, _ = io.WriteString(w, `{}`) }},
		{"content type", "text/html", http.StatusOK, func(w io.Writer) { _, _ = io.WriteString(w, `{}`) }},
		{"malformed", "application/json", http.StatusOK, func(w io.Writer) { _, _ = io.WriteString(w, `{`) }},
		{"trailing data", "application/json", http.StatusOK, func(w io.Writer) {
			_, _ = io.WriteString(w, `{"images":[]}{}`)
		}},
		{"oversized", "application/json", http.StatusOK, func(w io.Writer) {
			_, _ = io.WriteString(w, `{"images":[]}`)
			_, _ = io.WriteString(w, strings.Repeat(" ", maxReleaseListingBytes))
		}},
		{"too many images", "application/json", http.StatusOK, func(w io.Writer) {
			_, _ = io.WriteString(w, `{"images":[`)
			for index := 0; index <= maxReleaseImages; index++ {
				if index > 0 {
					_, _ = io.WriteString(w, ",")
				}
				_, _ = fmt.Fprintf(w, `{"id":"%d"}`, index+1)
			}
			_, _ = io.WriteString(w, `]}`)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, _ := coverArtTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", test.contentType)
				w.WriteHeader(test.status)
				test.body(w)
			})
			if images, err := client.ListImages(context.Background(), testMBID); err == nil {
				t.Fatalf("accepted images=%+v", images)
			}
		})
	}
}

func TestListImagesReturnsCancellationError(t *testing.T) {
	client, _ := coverArtTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"images":[]}`)
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.ListImages(ctx, testMBID); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func TestListImagesFollowsGovernedRedirect(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/release/"+testMBID+"/index.json" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"images":[{"id":"100","approved":true}]}`)
			return
		}
		http.Redirect(w, r, server.URL+"/release/"+testMBID+"/index.json", http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	client, err := newClientWithEndpoint(server.Client(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	images, err := client.ListImages(context.Background(), testMBID)
	if err != nil || len(images) != 1 || images[0].ID != "100" {
		t.Fatalf("images=%+v err=%v", images, err)
	}
}

func TestPositiveImageID(t *testing.T) {
	for _, value := range []string{"1", "456", "18446744073709551615"} {
		if got, ok := positiveImageID(value); !ok || got != value {
			t.Errorf("positiveImageID(%q) = %q, %v", value, got, ok)
		}
	}
	for _, value := range []string{"", "0", "00", "01", "-1", "+1", "1.0", "1e3", " 1", "1 ", "abc", "1/2", "18446744073709551616", "https://evil.example/1"} {
		if got, ok := positiveImageID(value); ok || got != "" {
			t.Errorf("positiveImageID(%q) = %q, %v", value, got, ok)
		}
	}
}

func TestDownloadImageUsesFixedEndpointAndSafeguards(t *testing.T) {
	client, _ := coverArtTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/release/"+testMBID+"/"+testImageID {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Accept"); got != "image/png, image/jpeg" {
			t.Errorf("Accept = %q", got)
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	})
	var got bytes.Buffer
	result, err := client.DownloadImage(context.Background(), testMBID, testImageID, &got)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(png)
	if !result.Found || result.MIME != "image/png" || result.Size != int64(len(png)) || result.SHA256 != hex.EncodeToString(digest[:]) || !bytes.Equal(got.Bytes(), png) {
		t.Fatalf("result=%+v bytes match=%v", result, bytes.Equal(got.Bytes(), png))
	}
}

func TestDownloadImageRejectsInvalidIDsWithoutRequest(t *testing.T) {
	called := false
	client, _ := coverArtTestClient(t, func(http.ResponseWriter, *http.Request) { called = true })
	if _, err := client.DownloadImage(context.Background(), "https://evil.example", testImageID, io.Discard); err == nil || called {
		t.Fatalf("invalid release: err=%v called=%v", err, called)
	}
	for _, imageID := range []string{"", "0", "-1", "abc", "456/../../front", "https://evil.example/1", "01"} {
		if _, err := client.DownloadImage(context.Background(), testMBID, imageID, io.Discard); err == nil || called {
			t.Fatalf("invalid image %q: err=%v called=%v", imageID, err, called)
		}
	}
	if _, err := client.DownloadImage(context.Background(), testMBID, testImageID, nil); err == nil || called {
		t.Fatalf("nil destination: err=%v called=%v", err, called)
	}
}

func TestDownloadImageNotFoundAndUnsupportedContent(t *testing.T) {
	client, _ := coverArtTestClient(t, http.NotFound)
	result, err := client.DownloadImage(context.Background(), testMBID, testImageID, io.Discard)
	if err != nil || result.Found {
		t.Fatalf("not found: result=%+v err=%v", result, err)
	}

	client, _ = coverArtTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<html></html>")
	})
	if _, err := client.DownloadImage(context.Background(), testMBID, testImageID, io.Discard); err == nil {
		t.Fatal("accepted unsupported content")
	}

	client, _ = coverArtTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = io.WriteString(w, "not an image")
	})
	if _, err := client.DownloadImage(context.Background(), testMBID, testImageID, io.Discard); err == nil {
		t.Fatal("accepted invalid image bytes")
	}
}

func coverArtTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := newClientWithEndpoint(server.Client(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return client, server
}
