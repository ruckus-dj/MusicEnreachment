package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type coverPrepareHTTPFake struct {
	*libraryPublicationHTTPFake
	calls      int
	releaseID  uuid.UUID
	positionID uuid.UUID
	err        error
}

func (fake *coverPrepareHTTPFake) PrepareFirstCover(_ context.Context, releaseID, positionID uuid.UUID) error {
	fake.calls++
	fake.releaseID, fake.positionID = releaseID, positionID
	return fake.err
}

func TestLibraryReleaseCoverPrepareHTTP(t *testing.T) {
	store := apiSettingsStore{}
	registry := settings.New(store, nil)
	if err := registry.CompleteSetup(context.Background()); err != nil {
		t.Fatal(err)
	}
	setup := service.NewSetup(&store, registry, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, nil, nil)
	releaseID, positionID := uuid.New(), uuid.New()
	fake := &coverPrepareHTTPFake{libraryPublicationHTTPFake: &libraryPublicationHTTPFake{}}
	handler := HandlerWithDependencies(Dependencies{Setup: setup, LibraryPublications: fake})
	path := "/library/releases/" + releaseID.String() + "/cover/prepare"
	body := `{"position_id":"` + positionID.String() + `"}`

	response := libraryPublicationRequest(t, handler, http.MethodPost, path, body)
	if response.Code != http.StatusNoContent {
		t.Fatalf("prepare status = %d: %s", response.Code, response.Body)
	}
	if fake.calls != 1 || fake.releaseID != releaseID || fake.positionID != positionID {
		t.Fatalf("prepare call = %d, %s/%s", fake.calls, fake.releaseID, fake.positionID)
	}

	invalid := []struct {
		name   string
		path   string
		body   string
		status int
	}{
		{name: "invalid release", path: "/library/releases/not-a-uuid/cover/prepare", body: body, status: http.StatusBadRequest},
		{name: "missing position", path: path, body: `{}`, status: http.StatusUnprocessableEntity},
		{name: "zero position", path: path, body: `{"position_id":"00000000-0000-0000-0000-000000000000"}`, status: http.StatusBadRequest},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			before := fake.calls
			response := libraryPublicationRequest(t, handler, http.MethodPost, test.path, test.body)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.status, response.Body)
			}
			if fake.calls != before {
				t.Fatalf("invalid request reached the service")
			}
		})
	}

	failures := []struct {
		name   string
		err    error
		status int
	}{
		{name: "position not found", err: persistence.ErrPublicationPositionNotFound, status: http.StatusNotFound},
		{name: "admission conflict", err: persistence.ErrPublicationAdmissionConflict, status: http.StatusConflict},
		{name: "pending artwork", err: service.ErrPublicationArtwork, status: http.StatusServiceUnavailable},
		{name: "canceled", err: context.Canceled, status: http.StatusServiceUnavailable},
		{name: "internal", err: errors.New("postgres connection detail"), status: http.StatusInternalServerError},
	}
	for _, test := range failures {
		t.Run(test.name, func(t *testing.T) {
			fake.err = test.err
			response := libraryPublicationRequest(t, handler, http.MethodPost, path, body)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.status, response.Body)
			}
			if strings.Contains(response.Body.String(), "postgres connection detail") {
				t.Fatalf("response leaked infrastructure detail: %s", response.Body)
			}
		})
	}

	response = libraryPublicationRequest(t, HandlerWithDependencies(Dependencies{Setup: setup}), http.MethodPost, path, body)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing service status = %d: %s", response.Code, response.Body)
	}
	response = libraryPublicationRequest(t, HandlerWithDependencies(Dependencies{Setup: setup, LibraryPublications: &libraryPublicationHTTPFake{}}), http.MethodPost, path, body)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unsupported service status = %d: %s", response.Code, response.Body)
	}
}
