package api

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type libraryReleaseCoverRepositoryFixture struct {
	releaseID uuid.UUID
	cover     persistence.ReleaseCover
	readErr   error
}

func (fixture *libraryReleaseCoverRepositoryFixture) ReadReleaseDetail(context.Context, uuid.UUID) (*persistence.LibraryReleaseDetail, error) {
	if fixture.readErr != nil {
		return nil, fixture.readErr
	}
	return &persistence.LibraryReleaseDetail{ID: fixture.releaseID}, nil
}

func TestLibraryReleaseCoverHTTPNotFoundAndStaleRevision(t *testing.T) {
	releaseID := uuid.New()
	repository := &libraryReleaseCoverRepositoryFixture{releaseID: releaseID, cover: persistence.ReleaseCover{ReleaseID: releaseID, Revision: 2, Mode: "manual"}}
	handler := HandlerWithDependencies(Dependencies{LibraryReleaseCovers: service.NewReleaseCovers(repository)})
	var imageData bytes.Buffer
	if err := png.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(libraryReleaseCoverSaveBody{ExpectedRevision: 1, Image: imageData.Bytes(), MIME: "image/png"})
	request := httptest.NewRequest(http.MethodPatch, "/library/releases/"+releaseID.String()+"/cover", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	stale := httptest.NewRecorder()
	handler.ServeHTTP(stale, request)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale PATCH status = %d, body %s", stale.Code, stale.Body.String())
	}
	repository.readErr = persistence.ErrLibraryReleaseNotFound
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/library/releases/"+releaseID.String()+"/cover", nil),
		httptest.NewRequest(http.MethodPatch, "/library/releases/"+releaseID.String()+"/cover", bytes.NewReader(body)),
		httptest.NewRequest(http.MethodGet, "/library/releases/"+releaseID.String()+"/cover/images/"+uuid.NewString(), nil),
	} {
		if request.Method == http.MethodPatch {
			request.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Errorf("%s %s status = %d, want 404: %s", request.Method, request.URL, response.Code, response.Body.String())
		}
	}
}

func (fixture *libraryReleaseCoverRepositoryFixture) ReadReleaseCover(context.Context, uuid.UUID) (persistence.ReleaseCover, error) {
	return fixture.cover, nil
}

func (fixture *libraryReleaseCoverRepositoryFixture) SetPendingReleaseCover(_ context.Context, releaseID uuid.UUID, expected int64, generation uuid.UUID, data []byte, mime string) (persistence.ReleaseCover, error) {
	if fixture.cover.Revision != expected {
		return persistence.ReleaseCover{}, persistence.ErrReleaseCoverConflict
	}
	fixture.cover.Revision++
	fixture.cover.ReleaseID = releaseID
	fixture.cover.Mode = "manual"
	fixture.cover.PendingGeneration = &generation
	fixture.cover.PendingBytes = append([]byte(nil), data...)
	fixture.cover.PendingMIME = &mime
	return fixture.cover, nil
}

func TestLibraryReleaseCoverHTTPReadsAndSavesPendingImage(t *testing.T) {
	releaseID := uuid.New()
	repository := &libraryReleaseCoverRepositoryFixture{
		releaseID: releaseID,
		cover:     persistence.ReleaseCover{ReleaseID: releaseID, Revision: 1, Mode: "automatic"},
	}
	handler := HandlerWithDependencies(Dependencies{LibraryReleaseCovers: service.NewReleaseCovers(repository)})
	read := httptest.NewRecorder()
	handler.ServeHTTP(read, httptest.NewRequest(http.MethodGet, "/library/releases/"+releaseID.String()+"/cover", nil))
	if read.Code != http.StatusOK {
		t.Fatalf("read status = %d, body %s", read.Code, read.Body.String())
	}

	imageData := image.NewRGBA(image.Rect(0, 0, 1, 1))
	imageData.Set(0, 0, color.White)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, imageData); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(libraryReleaseCoverSaveBody{ExpectedRevision: 1, Image: encoded.Bytes(), MIME: "image/png"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPatch, "/library/releases/"+releaseID.String()+"/cover", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	saved := httptest.NewRecorder()
	handler.ServeHTTP(saved, request)
	if saved.Code != http.StatusOK {
		t.Fatalf("save status = %d, body %s", saved.Code, saved.Body.String())
	}
	if repository.cover.PendingGeneration == nil {
		t.Fatal("pending generation was not saved")
	}
	oldGeneration := *repository.cover.PendingGeneration

	imageResponse := httptest.NewRecorder()
	imageURL := "/library/releases/" + releaseID.String() + "/cover/images/" + repository.cover.PendingGeneration.String()
	handler.ServeHTTP(imageResponse, httptest.NewRequest(http.MethodGet, imageURL, nil))
	if imageResponse.Code != http.StatusOK || imageResponse.Header().Get("Content-Type") != "image/png" || !bytes.Equal(imageResponse.Body.Bytes(), encoded.Bytes()) {
		t.Fatalf("image response status=%d type=%q body size=%d", imageResponse.Code, imageResponse.Header().Get("Content-Type"), imageResponse.Body.Len())
	}
	var jpegData bytes.Buffer
	if err := jpeg.Encode(&jpegData, image.NewRGBA(image.Rect(0, 0, 1, 1)), nil); err != nil {
		t.Fatal(err)
	}
	jpegBody, _ := json.Marshal(libraryReleaseCoverSaveBody{ExpectedRevision: repository.cover.Revision, Image: jpegData.Bytes(), MIME: "image/jpeg"})
	jpegRequest := httptest.NewRequest(http.MethodPatch, "/library/releases/"+releaseID.String()+"/cover", bytes.NewReader(jpegBody))
	jpegRequest.Header.Set("Content-Type", "application/json")
	jpegSaved := httptest.NewRecorder()
	handler.ServeHTTP(jpegSaved, jpegRequest)
	if jpegSaved.Code != http.StatusOK {
		t.Fatalf("JPEG save status = %d, body %s", jpegSaved.Code, jpegSaved.Body.String())
	}
	oldImage := httptest.NewRecorder()
	handler.ServeHTTP(oldImage, httptest.NewRequest(http.MethodGet, "/library/releases/"+releaseID.String()+"/cover/images/"+oldGeneration.String(), nil))
	if oldImage.Code != http.StatusNotFound {
		t.Fatalf("replaced pending generation status = %d, body %s", oldImage.Code, oldImage.Body.String())
	}
	stale := httptest.NewRecorder()
	handler.ServeHTTP(stale, httptest.NewRequest(http.MethodGet, "/library/releases/"+releaseID.String()+"/cover/images/"+uuid.NewString(), nil))
	if stale.Code != http.StatusNotFound {
		t.Fatalf("stale generation status = %d, body %s", stale.Code, stale.Body.String())
	}
}
