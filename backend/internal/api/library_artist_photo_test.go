package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/musicbrainz"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type libraryArtistPhotoRepositoryFixture struct {
	binding persistence.LibraryArtistPhotoBinding
	err     error
	records map[string]persistence.LibraryArtistPhotoRecord
}

func (repository *libraryArtistPhotoRepositoryFixture) ReadLibraryArtistPhotoBinding(context.Context, uuid.UUID) (persistence.LibraryArtistPhotoBinding, error) {
	return repository.binding, repository.err
}

func (repository *libraryArtistPhotoRepositoryFixture) ReadLibraryArtistPhoto(_ context.Context, identity, mbid string) (persistence.LibraryArtistPhotoRecord, error) {
	record, ok := repository.records[identity+"\x00"+mbid]
	if !ok {
		return persistence.LibraryArtistPhotoRecord{}, persistence.ErrLibraryArtistPhotoNotFound
	}
	return record, nil
}

func (repository *libraryArtistPhotoRepositoryFixture) SaveLibraryArtistPhoto(context.Context, persistence.LibraryArtistPhotoRecord) error {
	return nil
}

type libraryArtistPhotoSettingsFixture struct {
	config settings.MusicBrainzConfig
	err    error
}

func (fixture *libraryArtistPhotoSettingsFixture) GetMusicBrainzConfig(context.Context) (settings.MusicBrainzConfig, error) {
	return fixture.config, fixture.err
}

type libraryArtistPhotoProviderForConfigFixture struct {
	provider *musicbrainz.Provider
	err      error
}

func (fixture *libraryArtistPhotoProviderForConfigFixture) MusicBrainzForConfig(context.Context, settings.MusicBrainzConfig) (*musicbrainz.Provider, error) {
	return fixture.provider, fixture.err
}

type libraryArtistPhotoResolverFixture struct {
	photo *service.LibraryArtistPhoto
	err   error
	calls int
}

func (fixture *libraryArtistPhotoResolverFixture) Resolve(context.Context, *musicbrainz.Provider, string) (*service.LibraryArtistPhoto, error) {
	fixture.calls++
	return fixture.photo, fixture.err
}

func libraryArtistPhotoHandler(t *testing.T, photos *service.LibraryArtistPhotos) http.Handler {
	t.Helper()
	router := chi.NewRouter()
	api := New(router)
	registerLibraryArtistPhoto(api, photos)
	return router
}

func libraryArtistPhotoService(t *testing.T, binding persistence.LibraryArtistPhotoBinding, resolver *libraryArtistPhotoResolverFixture) *service.LibraryArtistPhotos {
	t.Helper()
	return libraryArtistPhotoServiceWithRepository(t, &libraryArtistPhotoRepositoryFixture{binding: binding}, resolver)
}

func libraryArtistPhotoServiceWithRepository(t *testing.T, repository *libraryArtistPhotoRepositoryFixture, resolver *libraryArtistPhotoResolverFixture) *service.LibraryArtistPhotos {
	t.Helper()
	provider, err := musicbrainz.NewProvider(musicbrainz.ProviderOptions{BaseURL: musicbrainz.OfficialEndpoint})
	if err != nil {
		t.Fatalf("create MusicBrainz provider: %v", err)
	}
	return service.NewLibraryArtistPhotos(
		repository,
		&libraryArtistPhotoSettingsFixture{config: settings.MusicBrainzConfig{Identity: "config-1"}},
		&libraryArtistPhotoProviderForConfigFixture{provider: provider},
		resolver,
	)
}

func TestLibraryArtistPhotoAPIEmitsSnakeCasePhoto(t *testing.T) {
	licenseURL := "https://creativecommons.org/licenses/by-sa/4.0/"
	resolver := &libraryArtistPhotoResolverFixture{photo: &service.LibraryArtistPhoto{
		ImageURL: "https://upload.wikimedia.org/photo.jpg", SourceURL: "https://commons.wikimedia.org/wiki/File:Photo.jpg",
		Author: "Photographer", License: "CC BY-SA 4.0", LicenseURL: &licenseURL,
	}}
	artistID := uuid.New()
	handler := libraryArtistPhotoHandler(t, libraryArtistPhotoService(t, persistence.LibraryArtistPhotoBinding{
		ArtistExists: true, ProviderKeys: []string{uuid.NewString()},
	}, resolver))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/library/artists/"+artistID.String()+"/photo", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Photo map[string]any `json:"photo"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	wantImageURL := "/api/library/artists/" + artistID.String() + "/photo/image"
	if body.Photo["image_url"] != wantImageURL ||
		body.Photo["source_url"] != "https://commons.wikimedia.org/wiki/File:Photo.jpg" ||
		body.Photo["author"] != "Photographer" ||
		body.Photo["license"] != "CC BY-SA 4.0" ||
		body.Photo["license_url"] != licenseURL {
		t.Fatalf("photo body = %#v", body.Photo)
	}
	if _, exists := body.Photo["ImageURL"]; exists {
		t.Fatalf("Go field name leaked: %#v", body.Photo)
	}
}

func TestLibraryArtistPhotoAPIReturnsEmptyBodyWithoutValidatedMBID(t *testing.T) {
	resolver := &libraryArtistPhotoResolverFixture{}
	handler := libraryArtistPhotoHandler(t, libraryArtistPhotoService(t, persistence.LibraryArtistPhotoBinding{
		ArtistExists: true, ProviderKeys: []string{"invalid"},
	}, resolver))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/library/artists/"+uuid.NewString()+"/photo", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	delete(body, "$schema")
	if len(body) != 0 {
		t.Fatalf("body = %#v, want empty object", body)
	}
	if resolver.calls != 0 {
		t.Fatalf("resolver calls = %d, want none", resolver.calls)
	}
}

func TestLibraryArtistPhotoAPIMapsFailures(t *testing.T) {
	tests := []struct {
		name       string
		artistURL  string
		service    *service.LibraryArtistPhotos
		wantStatus int
	}{
		{
			name: "invalid id", artistURL: "/library/artists/not-a-uuid/photo",
			service:    libraryArtistPhotoService(t, persistence.LibraryArtistPhotoBinding{ArtistExists: true}, &libraryArtistPhotoResolverFixture{}),
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "not found", artistURL: "/library/artists/" + uuid.NewString() + "/photo",
			service:    libraryArtistPhotoService(t, persistence.LibraryArtistPhotoBinding{}, &libraryArtistPhotoResolverFixture{}),
			wantStatus: http.StatusNotFound,
		},
		{
			name: "transient failure", artistURL: "/library/artists/" + uuid.NewString() + "/photo",
			service: libraryArtistPhotoService(t, persistence.LibraryArtistPhotoBinding{ArtistExists: true, ProviderKeys: []string{uuid.NewString()}},
				&libraryArtistPhotoResolverFixture{err: errors.New("provider outage")}),
			wantStatus: http.StatusServiceUnavailable,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			libraryArtistPhotoHandler(t, test.service).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.artistURL, nil))
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
		})
	}
}

func TestLibraryArtistPhotoAPIReturnsServiceUnavailableWithoutService(t *testing.T) {
	recorder := httptest.NewRecorder()
	libraryArtistPhotoHandler(t, nil).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/library/artists/"+uuid.NewString()+"/photo", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body = %s", recorder.Code, recorder.Body.String())
	}
}

func libraryArtistPhotoPNG(t *testing.T) []byte {
	t.Helper()
	value := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := range 8 {
		for x := range 8 {
			value.Set(x, y, color.RGBA{R: 20, G: 40, B: 60, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, value); err != nil {
		t.Fatalf("encode artist photo: %v", err)
	}
	return encoded.Bytes()
}

func libraryArtistPhotoStoredRecord(mbid string, data []byte) persistence.LibraryArtistPhotoRecord {
	digest := sha256.Sum256(data)
	licenseURL := "https://creativecommons.org/licenses/by-sa/4.0/"
	return persistence.LibraryArtistPhotoRecord{
		ConfigIdentity: "config-1", ArtistMBID: uuid.MustParse(mbid),
		ImageURL:  "https://upload.wikimedia.org/wikipedia/commons/thumb/a/ab/Photo.png/330px-Photo.png",
		SourceURL: "https://commons.wikimedia.org/wiki/File:Photo.png", Author: "Photographer",
		License: "CC BY-SA 4.0", LicenseURL: &licenseURL,
		Data: data, MIME: "image/png", SHA256: digest[:], SizeBytes: int64(len(data)),
		Width: 8, Height: 8, FetchedAt: time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC),
	}
}

func TestLibraryArtistPhotoAPIServesDurableImageWithoutResolving(t *testing.T) {
	artistID, mbid := uuid.New(), uuid.NewString()
	data := libraryArtistPhotoPNG(t)
	repository := &libraryArtistPhotoRepositoryFixture{
		binding: persistence.LibraryArtistPhotoBinding{ArtistExists: true, ProviderKeys: []string{mbid}},
		records: map[string]persistence.LibraryArtistPhotoRecord{
			"config-1\x00" + mbid: libraryArtistPhotoStoredRecord(mbid, data),
		},
	}
	resolver := &libraryArtistPhotoResolverFixture{}
	handler := libraryArtistPhotoHandler(t, libraryArtistPhotoServiceWithRepository(t, repository, resolver))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/library/artists/"+artistID.String()+"/photo/image", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("Content-Type = %q, want image/png", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "private, max-age=300" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := recorder.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
	if !bytes.Equal(recorder.Body.Bytes(), data) {
		t.Fatalf("image body = %d bytes, want %d", recorder.Body.Len(), len(data))
	}
	if resolver.calls != 0 {
		t.Fatalf("resolver calls = %d, want none", resolver.calls)
	}
}

func TestLibraryArtistPhotoAPIImageErrors(t *testing.T) {
	tests := []struct {
		name       string
		artistURL  string
		service    *service.LibraryArtistPhotos
		wantStatus int
	}{
		{
			name: "invalid id", artistURL: "/library/artists/not-a-uuid/photo/image",
			service:    libraryArtistPhotoService(t, persistence.LibraryArtistPhotoBinding{ArtistExists: true}, &libraryArtistPhotoResolverFixture{}),
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "missing artist", artistURL: "/library/artists/" + uuid.NewString() + "/photo/image",
			service:    libraryArtistPhotoService(t, persistence.LibraryArtistPhotoBinding{}, &libraryArtistPhotoResolverFixture{}),
			wantStatus: http.StatusNotFound,
		},
		{
			name: "no saved image", artistURL: "/library/artists/" + uuid.NewString() + "/photo/image",
			service: libraryArtistPhotoService(t, persistence.LibraryArtistPhotoBinding{
				ArtistExists: true, ProviderKeys: []string{uuid.NewString()},
			}, &libraryArtistPhotoResolverFixture{}),
			wantStatus: http.StatusNotFound,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			libraryArtistPhotoHandler(t, test.service).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.artistURL, nil))
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", recorder.Code, test.wantStatus, recorder.Body.String())
			}
		})
	}
	recorder := httptest.NewRecorder()
	libraryArtistPhotoHandler(t, nil).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/library/artists/"+uuid.NewString()+"/photo/image", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil service status = %d, want 503", recorder.Code)
	}
}
