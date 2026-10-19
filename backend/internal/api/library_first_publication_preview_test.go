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

func TestLibraryFirstPublicationPreviewHTTP(t *testing.T) {
	releaseID, positionID, epoch := uuid.New(), uuid.New(), uuid.New()
	store := apiSettingsStore{}
	registry := settings.New(store, nil)
	if err := registry.CompleteSetup(context.Background()); err != nil {
		t.Fatal(err)
	}
	setup := service.NewSetup(&store, registry, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, nil, nil)
	fake := &libraryPublicationHTTPFake{preview: service.FirstPublicationPreview{
		ReleaseID: releaseID, PositionID: positionID, Epoch: epoch, ContextDigest: "digest",
		SelectedSource: &service.FirstPublicationPreviewSource{
			RootID: uuid.New(), LocationID: uuid.New(), WorkID: uuid.New(), MediaVariantID: uuid.New(),
			StreamIndex: 2, Codec: "flac",
		},
		PathComponents: []string{"Музыка", "Björk"}, MetadataRevisions: map[string]int64{"release": 4},
		Warnings: []string{"native verification required"}, Blockers: []string{"first-publication blocker"},
		NativeStatus: "native verification required",
	}}
	handler := HandlerWithDependencies(Dependencies{Setup: setup, LibraryPublications: fake})
	path := "/library/releases/" + releaseID.String() + "/positions/" + positionID.String() + "/publication/preview"
	response := libraryPublicationRequest(t, handler, http.MethodGet, path, "")
	if response.Code != http.StatusOK {
		t.Fatalf("GET status = %d: %s", response.Code, response.Body)
	}
	body := response.Body.String()
	for _, required := range []string{"Музыка", "Björk", "first-publication blocker", "context_digest", "stream_index"} {
		if !strings.Contains(body, required) {
			t.Errorf("preview response missing %q: %s", required, body)
		}
	}
	for _, forbidden := range []string{"work_id", "quality", "frozen", "installation", "tools_directory", "output_directory", "source_path"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("preview response leaked %q: %s", forbidden, body)
		}
	}
	if fake.previewCalls != 1 {
		t.Fatalf("PreviewFirst calls = %d", fake.previewCalls)
	}

	fake.previewErr = persistence.ErrPublicationPositionNotFound
	response = libraryPublicationRequest(t, handler, http.MethodGet, path, "")
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing position status = %d: %s", response.Code, response.Body)
	}
	fake.previewErr = errors.New("database unavailable")
	response = libraryPublicationRequest(t, handler, http.MethodGet, path, "")
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "database unavailable") {
		t.Fatalf("infra failure status/body = %d: %s", response.Code, response.Body)
	}
}
