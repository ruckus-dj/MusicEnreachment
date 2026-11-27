package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type selectionPreviewHTTPFake struct {
	*libraryPublicationHTTPFake
	preview service.LibraryPublicationSelectionPreview
	err     error
	calls   int
	request service.LibraryPublicationSelectionPreviewRequest
}

func (fake *selectionPreviewHTTPFake) PreviewPublicationSelection(_ context.Context, request service.LibraryPublicationSelectionPreviewRequest) (service.LibraryPublicationSelectionPreview, error) {
	fake.calls++
	fake.request = request
	return fake.preview, fake.err
}

func TestLibraryPublicationSelectionPreviewHTTP(t *testing.T) {
	store := apiSettingsStore{}
	registry := settings.New(store, nil)
	if err := registry.CompleteSetup(context.Background()); err != nil {
		t.Fatal(err)
	}
	setup := service.NewSetup(&store, registry, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, nil, nil)
	first := service.LibraryPublicationSelectionPosition{ReleaseID: uuid.New(), PositionID: uuid.New()}
	second := service.LibraryPublicationSelectionPosition{ReleaseID: uuid.New(), PositionID: uuid.New()}
	fake := &selectionPreviewHTTPFake{libraryPublicationHTTPFake: &libraryPublicationHTTPFake{}}
	fake.preview = service.LibraryPublicationSelectionPreview{
		Mode: "ordinary",
		Items: []service.LibraryPublicationSelectionPreviewItem{
			{ReleaseID: first.ReleaseID, PositionID: first.PositionID, Disposition: "create", ResolvedPath: "Artist/Album/Track.flac", PathClash: true,
				PathClashPositions: []service.LibraryPublicationSelectionPreviewReference{{ReleaseID: second.ReleaseID, PositionID: second.PositionID}}},
			{ReleaseID: second.ReleaseID, PositionID: second.PositionID, Disposition: "blocked", Reason: "source_selection_required"},
		},
	}
	handler := HandlerWithDependencies(Dependencies{Setup: setup, LibraryPublications: fake})
	body, err := json.Marshal(map[string]any{
		"prepare_covers": true,
		"positions": []map[string]any{
			{"release_id": first.ReleaseID, "position_id": first.PositionID},
			{"release_id": second.ReleaseID, "position_id": second.PositionID},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	response := libraryPublicationRequest(t, handler, http.MethodPost, "/library/publication/preview", string(body))
	if response.Code != http.StatusOK {
		t.Fatalf("preview status = %d: %s", response.Code, response.Body)
	}
	if fake.calls != 1 || fake.request.Mode != "ordinary" || !fake.request.PrepareCovers || len(fake.request.Positions) != 2 ||
		fake.request.Positions[0] != first || fake.request.Positions[1] != second {
		t.Fatalf("preview request = %+v, calls = %d", fake.request, fake.calls)
	}
	decoded := struct {
		Mode  string `json:"mode"`
		Items []struct {
			ReleaseID    uuid.UUID `json:"release_id"`
			PositionID   uuid.UUID `json:"position_id"`
			Disposition  string    `json:"disposition"`
			Reason       string    `json:"reason"`
			ResolvedPath string    `json:"resolved_path"`
			PathClash    bool      `json:"path_clash"`
		} `json:"items"`
	}{}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Mode != "ordinary" || len(decoded.Items) != 2 ||
		decoded.Items[0].PositionID != first.PositionID || decoded.Items[0].Disposition != "create" ||
		decoded.Items[0].ResolvedPath != "Artist/Album/Track.flac" || !decoded.Items[0].PathClash ||
		decoded.Items[1].PositionID != second.PositionID || decoded.Items[1].Disposition != "blocked" || decoded.Items[1].Reason != "source_selection_required" {
		t.Fatalf("preview response = %+v", decoded)
	}
	for _, forbidden := range []string{"preview_digest", "expected_epoch", "expected_generation", "output_directory", "installation", "admission"} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Errorf("preview response leaked %q: %s", forbidden, response.Body)
		}
	}

	invalid := []struct {
		name   string
		body   string
		status int
	}{
		{name: "unknown mode", body: `{"mode":"unknown","positions":[{"release_id":"` + first.ReleaseID.String() + `","position_id":"` + first.PositionID.String() + `"}]}`, status: http.StatusBadRequest},
		{name: "zero identity", body: `{"positions":[{"release_id":"00000000-0000-0000-0000-000000000000","position_id":"` + first.PositionID.String() + `"}]}`, status: http.StatusBadRequest},
		{name: "duplicate pair", body: `{"positions":[{"release_id":"` + first.ReleaseID.String() + `","position_id":"` + first.PositionID.String() + `"},{"release_id":"` + first.ReleaseID.String() + `","position_id":"` + first.PositionID.String() + `"}]}`, status: http.StatusBadRequest},
		{name: "empty selection", body: `{"positions":[]}`, status: http.StatusUnprocessableEntity},
		{name: "oversized selection", body: oversizedSelectionPreviewBody(first), status: http.StatusUnprocessableEntity},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			before := fake.calls
			response := libraryPublicationRequest(t, handler, http.MethodPost, "/library/publication/preview", test.body)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.status, response.Body)
			}
			if fake.calls != before {
				t.Fatalf("invalid request reached the service")
			}
		})
	}

	fake.err = service.ErrLibraryPublicationSelectionPreviewInvalid
	response = libraryPublicationRequest(t, handler, http.MethodPost, "/library/publication/preview", string(body))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid service request status = %d: %s", response.Code, response.Body)
	}
	fake.err = service.ErrPublicationArtwork
	response = libraryPublicationRequest(t, handler, http.MethodPost, "/library/publication/preview", string(body))
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), service.ErrPublicationArtwork.Error()) {
		t.Fatalf("pending artwork status/body = %d: %s", response.Code, response.Body)
	}
	fake.err = errors.New("postgres connection detail")
	response = libraryPublicationRequest(t, handler, http.MethodPost, "/library/publication/preview", string(body))
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "postgres connection detail") {
		t.Fatalf("infrastructure failure status/body = %d: %s", response.Code, response.Body)
	}
}

func oversizedSelectionPreviewBody(position service.LibraryPublicationSelectionPosition) string {
	entries := make([]string, 0, service.LibraryPublicationSelectionPreviewMaxPositions+1)
	for index := 0; index <= service.LibraryPublicationSelectionPreviewMaxPositions; index++ {
		entries = append(entries, fmt.Sprintf(`{"release_id":%q,"position_id":%q}`, position.ReleaseID, uuid.New()))
	}
	return `{"positions":[` + strings.Join(entries, ",") + `]}`
}
