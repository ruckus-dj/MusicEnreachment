package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type sourceRepublishHTTPFake struct {
	*libraryPublicationHTTPFake
	previewMode      string
	retainedPreviews int
	retainedAdmits   int
	retainedRequest  service.SourceRepublishConfirmation
}

func (fake *sourceRepublishHTTPFake) PreviewSourceRepublishRetainingSelection(
	_ context.Context,
	releaseID, positionID uuid.UUID,
	mode string,
) (service.FirstPublicationPreview, error) {
	fake.retainedPreviews++
	fake.previewMode = mode
	return fake.preview, fake.previewErr
}

func (fake *sourceRepublishHTTPFake) AdmitSourceRepublishRetainingSelection(
	_ context.Context,
	releaseID, positionID uuid.UUID,
	request service.SourceRepublishConfirmation,
) (uuid.UUID, error) {
	fake.retainedAdmits++
	fake.admitReleaseID, fake.admitPositionID = releaseID, positionID
	fake.retainedRequest = request
	return fake.operationID, fake.admitErr
}

func TestLibrarySourceRepublishHTTP(t *testing.T) {
	releaseID, positionID, epoch, generation := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	operationID := uuid.New()
	store := apiSettingsStore{}
	registry := settings.New(store, nil)
	if err := registry.CompleteSetup(context.Background()); err != nil {
		t.Fatal(err)
	}
	setup := service.NewSetup(&store, registry, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, nil, nil)
	baseFake := &libraryPublicationHTTPFake{operationID: operationID, preview: service.FirstPublicationPreview{
		ReleaseID: releaseID, PositionID: positionID, Epoch: epoch, ContextDigest: strings.Repeat("a", 64),
		ConfirmationCapability: &service.SourceRepublishConfirmationCapability{
			Kind: "retained_same_path", Status: "ready", CurrentGeneration: generation,
		},
	}}
	fake := &sourceRepublishHTTPFake{libraryPublicationHTTPFake: baseFake}
	operations := service.NewOperations(libraryPublicationOperationSnapshotRepository{operation: &persistence.Operation{
		ID: operationID, Kind: "publish_source_republish", State: "queued", Stage: "queued",
	}})
	handler := HandlerWithDependencies(Dependencies{Setup: setup, LibraryPublications: fake, Operations: operations})
	previewPath := "/library/releases/" + releaseID.String() + "/positions/" + positionID.String() + "/publication/preview"
	admissionPath := "/library/releases/" + releaseID.String() + "/positions/" + positionID.String() + "/publication"
	digest := strings.Repeat("a", 64)

	response := libraryPublicationRequest(t, handler, http.MethodGet, previewPath+"?workflow=retained_same_path", "")
	if response.Code != http.StatusOK || fake.retainedPreviews != 1 || fake.previewMode != "ordinary" || !strings.Contains(response.Body.String(), `"confirmation_capability"`) {
		t.Fatalf("retained preview default status=%d calls=%d mode=%q: %s", response.Code, fake.retainedPreviews, fake.previewMode, response.Body)
	}
	response = libraryPublicationRequest(t, handler, http.MethodGet, previewPath+"?workflow=retained_same_path&mode=forced", "")
	if response.Code != http.StatusOK || fake.retainedPreviews != 2 || fake.previewMode != "forced" {
		t.Fatalf("forced retained preview status=%d calls=%d mode=%q: %s", response.Code, fake.retainedPreviews, fake.previewMode, response.Body)
	}

	postBody, err := json.Marshal(map[string]any{
		"workflow": "retained_same_path", "mode": "forced", "preview_digest": digest,
		"expected_epoch": epoch, "expected_generation": generation,
	})
	if err != nil {
		t.Fatal(err)
	}
	response = libraryPublicationRequest(t, handler, http.MethodPost, admissionPath, string(postBody))
	if response.Code != http.StatusAccepted || fake.retainedAdmits != 1 || fake.admitCalls != 0 ||
		fake.admitReleaseID != releaseID || fake.admitPositionID != positionID ||
		fake.retainedRequest.PreviewDigest != digest || fake.retainedRequest.ExpectedEpoch != epoch ||
		fake.retainedRequest.ExpectedGeneration != generation || fake.retainedRequest.Mode != "forced" {
		t.Fatalf("retained admission status=%d calls=%d request=%+v first calls=%d: %s", response.Code, fake.retainedAdmits, fake.retainedRequest, fake.admitCalls, response.Body)
	}

	invalidRequests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "unknown preview workflow", method: http.MethodGet, path: previewPath + "?workflow=unknown"},
		{name: "unknown preview mode", method: http.MethodGet, path: previewPath + "?workflow=retained_same_path&mode=unknown"},
		{name: "retained preview source choice", method: http.MethodGet, path: previewPath + "?workflow=retained_same_path&location_id=" + uuid.NewString()},
		{name: "explicit first forced preview", method: http.MethodGet, path: previewPath + "?workflow=first&mode=forced"},
		{name: "unknown admission workflow", method: http.MethodPost, path: admissionPath, body: `{"workflow":"unknown","preview_digest":"` + digest + `","expected_epoch":"` + epoch.String() + `"}`},
		{name: "unknown admission mode", method: http.MethodPost, path: admissionPath, body: `{"workflow":"retained_same_path","mode":"unknown","preview_digest":"` + digest + `","expected_epoch":"` + epoch.String() + `","expected_generation":"` + generation.String() + `"}`},
		{name: "missing retained mode", method: http.MethodPost, path: admissionPath, body: `{"workflow":"retained_same_path","preview_digest":"` + digest + `","expected_epoch":"` + epoch.String() + `","expected_generation":"` + generation.String() + `"}`},
		{name: "missing retained generation", method: http.MethodPost, path: admissionPath, body: `{"workflow":"retained_same_path","mode":"ordinary","preview_digest":"` + digest + `","expected_epoch":"` + epoch.String() + `"}`},
		{name: "zero retained generation", method: http.MethodPost, path: admissionPath, body: `{"workflow":"retained_same_path","mode":"ordinary","preview_digest":"` + digest + `","expected_epoch":"` + epoch.String() + `","expected_generation":"00000000-0000-0000-0000-000000000000"}`},
		{name: "retained admission source choice", method: http.MethodPost, path: admissionPath, body: `{"workflow":"retained_same_path","mode":"ordinary","preview_digest":"` + digest + `","expected_epoch":"` + epoch.String() + `","expected_generation":"` + generation.String() + `","location_id":"` + uuid.NewString() + `"}`},
		{name: "explicit first forced admission", method: http.MethodPost, path: admissionPath, body: `{"workflow":"first","mode":"forced","preview_digest":"` + digest + `","expected_epoch":"` + epoch.String() + `"}`},
	}
	for _, test := range invalidRequests {
		t.Run(test.name, func(t *testing.T) {
			response := libraryPublicationRequest(t, handler, test.method, test.path, test.body)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status=%d, want %d: %s", response.Code, http.StatusBadRequest, response.Body)
			}
		})
	}
	if fake.retainedPreviews != 2 || fake.retainedAdmits != 1 {
		t.Fatalf("invalid retained requests reached service: previews=%d admits=%d", fake.retainedPreviews, fake.retainedAdmits)
	}

	withoutOptionalService := HandlerWithDependencies(Dependencies{Setup: setup, LibraryPublications: baseFake, Operations: operations})
	response = libraryPublicationRequest(t, withoutOptionalService, http.MethodGet, previewPath+"?workflow=retained_same_path", "")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing retained preview capability status=%d: %s", response.Code, response.Body)
	}
	response = libraryPublicationRequest(t, withoutOptionalService, http.MethodPost, admissionPath,
		`{"workflow":"retained_same_path","mode":"ordinary","preview_digest":"`+digest+`","expected_epoch":"`+epoch.String()+`","expected_generation":"`+generation.String()+`"}`)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing retained admission capability status=%d: %s", response.Code, response.Body)
	}
}
