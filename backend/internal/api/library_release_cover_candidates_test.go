package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/coverart"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type libraryReleaseCoverCandidateRepositoryFixture struct {
	mbid   string
	linked bool
	err    error
}

func (fixture *libraryReleaseCoverCandidateRepositoryFixture) ReadReleaseMusicBrainzReleaseID(context.Context, uuid.UUID) (string, bool, error) {
	return fixture.mbid, fixture.linked, fixture.err
}

type libraryReleaseCoverCandidateProviderFixture struct {
	images        []coverart.Image
	downloadData  []byte
	downloadMIME  string
	listErr       error
	downloadErr   error
	downloadCalls []string
}

func (fixture *libraryReleaseCoverCandidateProviderFixture) ListImages(context.Context, string) ([]coverart.Image, error) {
	return fixture.images, fixture.listErr
}

func (fixture *libraryReleaseCoverCandidateProviderFixture) DownloadImage(_ context.Context, _ string, imageID string, destination io.Writer) (coverart.Result, error) {
	fixture.downloadCalls = append(fixture.downloadCalls, imageID)
	if fixture.downloadErr != nil {
		return coverart.Result{}, fixture.downloadErr
	}
	if _, err := destination.Write(fixture.downloadData); err != nil {
		return coverart.Result{}, err
	}
	return coverart.Result{Found: true, MIME: fixture.downloadMIME, Size: int64(len(fixture.downloadData))}, nil
}

type libraryReleaseCoverCandidateSaverFixture struct {
	calls    int
	expected int64
	data     []byte
	mime     string
	summary  service.ReleaseCoverSummary
	err      error
}

func (fixture *libraryReleaseCoverCandidateSaverFixture) Save(_ context.Context, _ uuid.UUID, expectedRevision int64, data []byte, mime string) (service.ReleaseCoverSummary, error) {
	fixture.calls++
	fixture.expected, fixture.data, fixture.mime = expectedRevision, append([]byte(nil), data...), mime
	return fixture.summary, fixture.err
}

func libraryReleaseCoverCandidatePNG(t *testing.T, width int, fill color.Color) []byte {
	t.Helper()
	imageData := image.NewRGBA(image.Rect(0, 0, width, 1))
	imageData.Set(0, 0, fill)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, imageData); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func libraryReleaseCoverCandidateHandler(t *testing.T, repository *libraryReleaseCoverCandidateRepositoryFixture, provider *libraryReleaseCoverCandidateProviderFixture, saver *libraryReleaseCoverCandidateSaverFixture) http.Handler {
	t.Helper()
	return HandlerWithDependencies(Dependencies{
		LibraryReleaseCoverCandidates: service.NewReleaseCoverCandidates(repository, provider, saver),
	})
}

func TestLibraryReleaseCoverCandidatesHTTPListsAndSelects(t *testing.T) {
	releaseID, expectedRevision := uuid.New(), int64(4)
	data := libraryReleaseCoverCandidatePNG(t, 1, color.White)
	provider := &libraryReleaseCoverCandidateProviderFixture{
		images:       []coverart.Image{{ID: "123", Front: true, Types: []string{"Front"}, Comment: "cover", ThumbnailURL: "https://example.test/123"}},
		downloadData: data, downloadMIME: "image/png",
	}
	saver := &libraryReleaseCoverCandidateSaverFixture{summary: service.ReleaseCoverSummary{ReleaseID: releaseID, Revision: expectedRevision + 1, Mode: "manual"}}
	handler := libraryReleaseCoverCandidateHandler(t, &libraryReleaseCoverCandidateRepositoryFixture{mbid: "mbid", linked: true}, provider, saver)

	list := httptest.NewRecorder()
	handler.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/library/releases/"+releaseID.String()+"/cover/candidates", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, body %s", list.Code, list.Body.String())
	}
	var listed struct {
		Items []struct {
			ID           string   `json:"id"`
			Front        bool     `json:"front"`
			Back         bool     `json:"back"`
			Types        []string `json:"types"`
			Comment      string   `json:"comment"`
			ThumbnailURL string   `json:"thumbnail_url"`
		} `json:"items"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Items) != 1 || listed.Items[0].ID != "123" || !listed.Items[0].Front || listed.Items[0].Comment != "cover" ||
		listed.Items[0].ThumbnailURL != "https://example.test/123" || len(listed.Items[0].Types) != 1 {
		t.Fatalf("listed JSON = %s", list.Body.String())
	}

	selectBody, _ := json.Marshal(map[string]int64{"expected_revision": expectedRevision})
	selected := httptest.NewRecorder()
	handler.ServeHTTP(selected, httptest.NewRequest(http.MethodPost, "/library/releases/"+releaseID.String()+"/cover/candidates/123/select", bytes.NewReader(selectBody)))
	if selected.Code != http.StatusOK {
		t.Fatalf("select status = %d, body %s", selected.Code, selected.Body.String())
	}
	if saver.calls != 1 || saver.expected != expectedRevision || saver.mime != "image/png" || !bytes.Equal(saver.data, data) {
		t.Fatalf("select save = %+v", saver)
	}
	if len(provider.downloadCalls) != 1 || provider.downloadCalls[0] != "123" {
		t.Fatalf("select downloads = %v", provider.downloadCalls)
	}
}

func TestLibraryReleaseCoverCandidatesHTTPEmptyWithoutExactLink(t *testing.T) {
	handler := libraryReleaseCoverCandidateHandler(t,
		&libraryReleaseCoverCandidateRepositoryFixture{},
		&libraryReleaseCoverCandidateProviderFixture{listErr: errors.New("must not be called")},
		&libraryReleaseCoverCandidateSaverFixture{},
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/library/releases/"+uuid.NewString()+"/cover/candidates", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("empty list status = %d, body %s", response.Code, response.Body.String())
	}
	var body struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Items == nil || len(body.Items) != 0 {
		t.Fatalf("empty list = %d %s", response.Code, response.Body.String())
	}
}

func TestLibraryReleaseCoverCandidatesHTTPErrors(t *testing.T) {
	releaseID := uuid.New()
	missing := libraryReleaseCoverCandidateHandler(t, &libraryReleaseCoverCandidateRepositoryFixture{err: persistence.ErrLibraryReleaseNotFound}, &libraryReleaseCoverCandidateProviderFixture{}, &libraryReleaseCoverCandidateSaverFixture{})
	response := httptest.NewRecorder()
	missing.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/library/releases/"+releaseID.String()+"/cover/candidates", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing release list status = %d, body %s", response.Code, response.Body.String())
	}

	provider := &libraryReleaseCoverCandidateProviderFixture{images: []coverart.Image{{ID: "123"}}, downloadData: libraryReleaseCoverCandidatePNG(t, 1, color.White), downloadMIME: "image/png"}
	saver := &libraryReleaseCoverCandidateSaverFixture{err: service.ErrReleaseCoverConflict}
	conflict := libraryReleaseCoverCandidateHandler(t, &libraryReleaseCoverCandidateRepositoryFixture{mbid: "mbid", linked: true}, provider, saver)
	response = httptest.NewRecorder()
	conflict.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/library/releases/"+releaseID.String()+"/cover/candidates/123/select", bytes.NewReader([]byte(`{"expected_revision":1}`))))
	if response.Code != http.StatusConflict {
		t.Fatalf("conflict select status = %d, body %s", response.Code, response.Body.String())
	}

	ineligible := &libraryReleaseCoverCandidateProviderFixture{images: []coverart.Image{{ID: "123"}}}
	saver = &libraryReleaseCoverCandidateSaverFixture{}
	notFound := libraryReleaseCoverCandidateHandler(t, &libraryReleaseCoverCandidateRepositoryFixture{mbid: "mbid", linked: true}, ineligible, saver)
	response = httptest.NewRecorder()
	notFound.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/library/releases/"+releaseID.String()+"/cover/candidates/999/select", bytes.NewReader([]byte(`{"expected_revision":1}`))))
	if response.Code != http.StatusNotFound || saver.calls != 0 || len(ineligible.downloadCalls) != 0 {
		t.Fatalf("unlisted candidate select = %d %s", response.Code, response.Body.String())
	}

	oversizeProvider := &libraryReleaseCoverCandidateProviderFixture{
		images: []coverart.Image{{ID: "123"}}, downloadData: libraryReleaseCoverCandidatePNG(t, 10001, color.White), downloadMIME: "image/png",
	}
	saver = &libraryReleaseCoverCandidateSaverFixture{}
	oversize := libraryReleaseCoverCandidateHandler(t, &libraryReleaseCoverCandidateRepositoryFixture{mbid: "mbid", linked: true}, oversizeProvider, saver)
	response = httptest.NewRecorder()
	oversize.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/library/releases/"+releaseID.String()+"/cover/candidates/123/select", bytes.NewReader([]byte(`{"expected_revision":1}`))))
	if response.Code != http.StatusUnprocessableEntity || saver.calls != 0 {
		t.Fatalf("oversized candidate select = %d %s", response.Code, response.Body.String())
	}

	provider = &libraryReleaseCoverCandidateProviderFixture{images: []coverart.Image{{ID: "123"}}, downloadData: libraryReleaseCoverCandidatePNG(t, 1, color.White), downloadMIME: "image/png"}
	valid := libraryReleaseCoverCandidateHandler(t, &libraryReleaseCoverCandidateRepositoryFixture{mbid: "mbid", linked: true}, provider, &libraryReleaseCoverCandidateSaverFixture{})
	for name, request := range map[string]*http.Request{
		"invalid release": httptest.NewRequest(http.MethodGet, "/library/releases/not-a-uuid/cover/candidates", nil),
		"zero revision":   httptest.NewRequest(http.MethodPost, "/library/releases/"+releaseID.String()+"/cover/candidates/123/select", bytes.NewReader([]byte(`{"expected_revision":0}`))),
		"padded image":    httptest.NewRequest(http.MethodPost, "/library/releases/"+releaseID.String()+"/cover/candidates/%20123%20/select", bytes.NewReader([]byte(`{"expected_revision":1}`))),
	} {
		response = httptest.NewRecorder()
		valid.ServeHTTP(response, request)
		switch name {
		case "invalid release":
			if response.Code != http.StatusBadRequest {
				t.Errorf("%s status = %d, body %s", name, response.Code, response.Body.String())
			}
		default:
			if response.Code != http.StatusUnprocessableEntity {
				t.Errorf("%s status = %d, body %s", name, response.Code, response.Body.String())
			}
		}
	}
}
