package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/api"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type libraryDeletionRoutesFake struct {
	preview      *service.LibraryDeletionPreview
	operationID  uuid.UUID
	rows         map[uuid.UUID]*persistence.Operation
	previewCalls int
	confirmCalls int
	retryCalls   int
}

func (routes *libraryDeletionRoutesFake) Preview(_ context.Context, kind string, releaseID, positionID uuid.UUID) (*service.LibraryDeletionPreview, error) {
	routes.previewCalls++
	if routes.preview.Kind != kind || routes.preview.ReleaseID != releaseID || len(routes.preview.PublicationIDs) == 1 && routes.preview.PublicationIDs[0] != positionID {
		return nil, context.Canceled
	}
	return routes.preview, nil
}

func (routes *libraryDeletionRoutesFake) Confirm(context.Context, string) (uuid.UUID, error) {
	routes.confirmCalls++
	return routes.operationID, nil
}

func (routes *libraryDeletionRoutesFake) Retry(_ context.Context, operationID uuid.UUID) (uuid.UUID, error) {
	routes.retryCalls++
	if operationID != routes.operationID {
		return uuid.Nil, context.Canceled
	}
	operation := routes.rows[operationID]
	operation.State = "queued"
	operation.Stage = "retry:deletion_recovery_required"
	operation.Attempt++
	return operationID, nil
}

func TestLibraryDeletionHTTPProjectionConfirmationAndOperationRetryRouting(t *testing.T) {
	releaseID, positionID, operationID := uuid.New(), uuid.New(), uuid.New()
	operation := &persistence.Operation{
		ID: operationID, Kind: service.LibraryDeletionOperationKind, State: "queued", Stage: "queued", Attempt: 1,
	}
	operationsRepository := &transitionOperationsRepository{rows: map[uuid.UUID]*persistence.Operation{operationID: operation}}
	routes := &libraryDeletionRoutesFake{
		operationID: operationID, rows: operationsRepository.rows,
		preview: &service.LibraryDeletionPreview{
			Kind: "publication", ReleaseID: releaseID, Title: "Fixture release", PublicationIDs: []uuid.UUID{positionID},
			OutputFacts:        []service.LibraryDeletionOutputFact{{PositionID: positionID, RelativePath: "album/track.flac", Capturable: true}},
			Blockers:           []service.LibraryDeletionBlocker{{PositionID: positionID, Code: "output_not_capturable", Message: "current output does not match its registered ownership"}},
			ConfirmationDigest: strings.Repeat("a", 64),
		},
	}
	store := apiSettingsStore{}
	registry := settings.New(store, nil)
	setup := service.NewSetup(store, registry, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, nil, nil)
	router := chi.NewRouter()
	openAPI := api.New(router)
	api.RegisterAll(openAPI, api.Dependencies{
		Setup: setup, Operations: service.NewOperations(operationsRepository), LibraryDeletion: routes,
	})

	previewResponse := transitionRequest(t, router, http.MethodGet,
		"/library/releases/"+releaseID.String()+"/positions/"+positionID.String()+"/deletion/preview", "")
	if previewResponse.Code != http.StatusOK {
		t.Fatalf("deletion preview status=%d body=%s", previewResponse.Code, previewResponse.Body.String())
	}
	var previewBody map[string]any
	if err := json.Unmarshal(previewResponse.Body.Bytes(), &previewBody); err != nil {
		t.Fatalf("decode deletion preview: %v", err)
	}
	if strings.Contains(previewResponse.Body.String(), "native_path") || strings.Contains(previewResponse.Body.String(), "output_root") || strings.Contains(previewResponse.Body.String(), "ownership_witnesses") {
		t.Fatalf("deletion preview exposed internal filesystem proof: %s", previewResponse.Body.String())
	}
	if previewBody["kind"] != "publication" || previewBody["release_id"] != releaseID.String() || previewBody["confirmation_digest"] != routes.preview.ConfirmationDigest {
		t.Fatalf("deletion preview response = %#v", previewBody)
	}
	blockers, ok := previewBody["blockers"].([]any)
	if !ok || len(blockers) != 1 || blockers[0].(map[string]any)["code"] != "output_not_capturable" {
		t.Fatalf("deletion blockers are not structured: %#v", previewBody["blockers"])
	}

	artistID := uuid.New()
	releasePreview := routes.preview
	routes.preview = &service.LibraryDeletionPreview{
		Kind: "artist", ReleaseID: artistID, Title: "Artist",
		ArtistReleases: []service.LibraryDeletionArtistRelease{{Title: "Primary release"}},
		CoartistWarnings: []service.LibraryDeletionCoartistWarning{{
			ReleaseTitle: "Primary release", ArtistNames: []string{"Coartist"},
		}},
	}
	artistPreviewResponse := transitionRequest(t, router, http.MethodGet,
		"/library/artists/"+artistID.String()+"/deletion/preview", "")
	if artistPreviewResponse.Code != http.StatusOK {
		t.Fatalf("artist deletion preview status=%d body=%s", artistPreviewResponse.Code, artistPreviewResponse.Body.String())
	}
	if strings.Contains(artistPreviewResponse.Body.String(), artistID.String()) ||
		!strings.Contains(artistPreviewResponse.Body.String(), "Primary release") ||
		!strings.Contains(artistPreviewResponse.Body.String(), "Coartist") {
		t.Fatalf("artist deletion preview did not project human-readable context: %s", artistPreviewResponse.Body.String())
	}
	routes.preview = releasePreview

	confirmBody, err := json.Marshal(map[string]string{"confirmation_digest": routes.preview.ConfirmationDigest})
	if err != nil {
		t.Fatal(err)
	}
	confirmResponse := transitionRequest(t, router, http.MethodPost, "/library/deletion/confirm", string(confirmBody))
	if confirmResponse.Code != http.StatusAccepted || routes.confirmCalls != 1 {
		t.Fatalf("deletion confirmation status=%d calls=%d body=%s", confirmResponse.Code, routes.confirmCalls, confirmResponse.Body.String())
	}

	operation.State = "failed"
	operation.Stage = "deletion_recovery_required"
	retryResponse := transitionRequest(t, router, http.MethodPost, "/operations/"+operationID.String()+"/retry", "")
	if retryResponse.Code != http.StatusOK || routes.retryCalls != 1 || operation.Attempt != 2 || operation.State != "queued" {
		t.Fatalf("deletion retry status=%d calls=%d operation=%+v body=%s", retryResponse.Code, routes.retryCalls, operation, retryResponse.Body.String())
	}
}
