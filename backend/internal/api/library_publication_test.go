package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type libraryPublicationHTTPFake struct {
	view                  service.LibraryPublicationView
	readErr               error
	applyErr              error
	operationID           uuid.UUID
	readCalls             int
	applyCalls            int
	readReleaseID         uuid.UUID
	readPositionID        uuid.UUID
	applyReleaseID        uuid.UUID
	applyPositionID       uuid.UUID
	applyRequest          service.LibraryPublicationApplyManualLyricsRequest
	preview               service.FirstPublicationPreview
	previewErr            error
	previewCalls          int
	selectedPreviewCalls  int
	selectedPreviewChoice *service.FirstPublicationSourceChoice
	admitErr              error
	admitCalls            int
	admitRequest          service.FirstPublicationConfirmation
	admitReleaseID        uuid.UUID
	admitPositionID       uuid.UUID
}

type libraryPublicationOperationSnapshotRepository struct {
	service.OperationRepository
	operation *persistence.Operation
}

func (repository libraryPublicationOperationSnapshotRepository) GetOperation(context.Context, uuid.UUID) (*persistence.Operation, error) {
	return repository.operation, nil
}

func (fake *libraryPublicationHTTPFake) Read(_ context.Context, releaseID, positionID uuid.UUID) (service.LibraryPublicationView, error) {
	fake.readCalls++
	fake.readReleaseID, fake.readPositionID = releaseID, positionID
	return fake.view, fake.readErr
}

func (fake *libraryPublicationHTTPFake) ApplyManualLyrics(_ context.Context, releaseID, positionID uuid.UUID, request service.LibraryPublicationApplyManualLyricsRequest) (uuid.UUID, error) {
	fake.applyCalls++
	fake.applyReleaseID, fake.applyPositionID, fake.applyRequest = releaseID, positionID, request
	return fake.operationID, fake.applyErr
}

func (fake *libraryPublicationHTTPFake) PreviewFirst(_ context.Context, releaseID, positionID uuid.UUID) (service.FirstPublicationPreview, error) {
	fake.previewCalls++
	return fake.preview, fake.previewErr
}

func (fake *libraryPublicationHTTPFake) PreviewFirstSelected(_ context.Context, releaseID, positionID uuid.UUID, choice *service.FirstPublicationSourceChoice) (service.FirstPublicationPreview, error) {
	fake.selectedPreviewCalls++
	fake.selectedPreviewChoice = choice
	return fake.preview, fake.previewErr
}

func (fake *libraryPublicationHTTPFake) AdmitFirst(_ context.Context, releaseID, positionID uuid.UUID, request service.FirstPublicationConfirmation) (uuid.UUID, error) {
	fake.admitCalls++
	fake.admitReleaseID, fake.admitPositionID, fake.admitRequest = releaseID, positionID, request
	return fake.operationID, fake.admitErr
}

func TestLibraryPublicationHTTPReadAndApply(t *testing.T) {
	releaseID, positionID := uuid.New(), uuid.New()
	epoch, generation := uuid.New(), uuid.New()
	candidate := uuid.New()
	phase := "validating"
	qualityPolicyVersion := 3
	validatedAt := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	operationID := uuid.New()
	store := apiSettingsStore{}
	registry := settings.New(store, nil)
	if err := registry.CompleteSetup(context.Background()); err != nil {
		t.Fatal(err)
	}
	setup := service.NewSetup(&store, registry, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, nil, nil)
	fake := &libraryPublicationHTTPFake{
		view: service.LibraryPublicationView{
			ReleaseID: releaseID, PositionID: positionID, ActiveEpoch: epoch, MetadataRevision: 11,
			CurrentGeneration:           &generation,
			CurrentMetadata:             &persistence.PublicationCommitMetadata{Version: 2, Tags: map[string][]string{"TITLE": {"Example"}}},
			CurrentAssets:               &persistence.PublicationAssetOutcomes{},
			CurrentQualityPolicyVersion: &qualityPolicyVersion, CurrentValidatedAt: &validatedAt,
			ActiveAttemptCandidate: &candidate, ActiveAttemptPhase: &phase,
			ManualLyrics:         &service.LibraryPublicationManualLyrics{State: "value", Value: "[00:01.00]line", Pending: true},
			ManualLyricsReceipt:  &service.LibraryPublicationManualLyricsReceipt{Epoch: epoch, DesiredRevision: 10, ConfirmedGeneration: generation, PublishedGeneration: generation, DesiredState: "value"},
			CanApplyManualLyrics: true,
		},
		operationID: operationID,
	}
	operationsRepo := libraryPublicationOperationSnapshotRepository{operation: &persistence.Operation{
		ID: operationID, Kind: "publish_manual_lyrics", State: "queued", Stage: "queued",
	}}
	handler := HandlerWithDependencies(Dependencies{
		Setup: setup, LibraryPublications: fake, Operations: service.NewOperations(operationsRepo),
	})
	base := "/library/releases/" + releaseID.String() + "/positions/" + positionID.String() + "/publication"

	response := libraryPublicationRequest(t, handler, http.MethodGet, base, "")
	if response.Code != http.StatusOK {
		t.Fatalf("GET status = %d: %s", response.Code, response.Body)
	}
	var got map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	body := got
	if nested, ok := got["body"].(map[string]any); ok {
		body = nested
	}
	if body["release_id"] != releaseID.String() || body["position_id"] != positionID.String() || body["metadata_revision"] != float64(11) || body["can_apply_manual_lyrics"] != true {
		t.Fatalf("GET body = %#v", got)
	}
	for _, projected := range []string{"manual_lyrics", "manual_lyrics_receipt", "current_metadata", "current_assets", "current_quality_policy_version", "current_validated_at", "active_attempt_candidate", "active_attempt_phase"} {
		if body[projected] == nil {
			t.Errorf("GET omitted bounded projection %q: %#v", projected, body)
		}
	}
	for _, forbidden := range []string{"internal_namespace", "frozen_tools", "journal", "source_path"} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Errorf("GET leaked %q: %s", forbidden, response.Body)
		}
	}
	if fake.readCalls != 1 || fake.readReleaseID != releaseID || fake.readPositionID != positionID {
		t.Fatalf("Read calls/identities = %d %s/%s", fake.readCalls, fake.readReleaseID, fake.readPositionID)
	}

	postBody := `{"expected_metadata_revision":11,"expected_generation":"` + generation.String() + `","expected_epoch":"` + epoch.String() + `","frozen_tools":["client-controlled"]}`
	response = libraryPublicationRequest(t, handler, http.MethodPost, base+"/manual-lyrics", postBody)
	if response.Code != http.StatusUnprocessableEntity || fake.applyCalls != 0 {
		t.Fatalf("client-supplied frozen inputs status=%d apply calls=%d: %s", response.Code, fake.applyCalls, response.Body)
	}
	postBody = `{"expected_metadata_revision":11,"expected_generation":"` + generation.String() + `","expected_epoch":"` + epoch.String() + `"}`
	response = libraryPublicationRequest(t, handler, http.MethodPost, base+"/manual-lyrics", postBody)
	if response.Code != http.StatusAccepted {
		t.Fatalf("POST status = %d: %s", response.Code, response.Body)
	}
	var operation struct {
		Body struct {
			ID    uuid.UUID `json:"id"`
			Kind  string    `json:"kind"`
			State string    `json:"state"`
		} `json:"body"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &operation); err != nil {
		t.Fatal(err)
	}
	if operation.Body.ID == uuid.Nil {
		var direct struct {
			ID    uuid.UUID `json:"id"`
			Kind  string    `json:"kind"`
			State string    `json:"state"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &direct); err != nil {
			t.Fatal(err)
		}
		operation.Body = direct
	}
	if operation.Body.ID != fake.operationID || operation.Body.State != "queued" {
		t.Fatalf("POST operation = %+v", operation.Body)
	}
	if fake.applyCalls != 1 || fake.applyReleaseID != releaseID || fake.applyPositionID != positionID || fake.applyRequest.ExpectedMetadataRevision != 11 || fake.applyRequest.ExpectedGeneration != generation || fake.applyRequest.ExpectedEpoch != epoch {
		t.Fatalf("ApplyManualLyrics captured unexpected request: %+v", fake)
	}
}

func TestLibraryPublicationHTTPErrorsAndGuards(t *testing.T) {
	releaseID, positionID := uuid.New(), uuid.New()
	base := "/library/releases/" + releaseID.String() + "/positions/" + positionID.String() + "/publication"
	for _, test := range []struct {
		name       string
		readErr    error
		applyErr   error
		method     string
		path       string
		wantStatus int
	}{
		{name: "missing membership", readErr: persistence.ErrPublicationPositionNotFound, method: http.MethodGet, path: base, wantStatus: http.StatusNotFound},
		{name: "stale state", applyErr: service.ErrLibraryPublicationConflict, method: http.MethodPost, path: base + "/manual-lyrics", wantStatus: http.StatusConflict},
		{name: "admission conflict", applyErr: persistence.ErrPublicationAdmissionConflict, method: http.MethodPost, path: base + "/manual-lyrics", wantStatus: http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := apiSettingsStore{}
			registry := settings.New(store, nil)
			if err := registry.CompleteSetup(context.Background()); err != nil {
				t.Fatal(err)
			}
			setup := service.NewSetup(&store, registry, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, nil, nil)
			fake := &libraryPublicationHTTPFake{readErr: test.readErr, applyErr: test.applyErr}
			handler := HandlerWithDependencies(Dependencies{
				Setup: setup, LibraryPublications: fake,
				Operations: service.NewOperations(libraryPublicationOperationSnapshotRepository{}),
			})
			response := libraryPublicationRequest(t, handler, test.method, test.path, `{"expected_metadata_revision":1,"expected_generation":"`+uuid.NewString()+`","expected_epoch":"`+uuid.NewString()+`"}`)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d: %s", response.Code, response.Body)
			}
		})
	}

	for _, test := range []struct {
		name       string
		completed  bool
		platform   settings.PlatformState
		method     string
		wantStatus int
	}{
		{name: "unfinished setup", platform: settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, method: http.MethodGet, wantStatus: http.StatusConflict},
		{name: "unsupported platform", completed: true, platform: settings.PlatformState{Platform: settings.Platform{GOOS: "plan9", GOARCH: "mips"}, Diagnostic: true}, method: http.MethodPost, wantStatus: http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := apiSettingsStore{}
			registry := settings.New(store, nil)
			if test.completed {
				if err := registry.CompleteSetup(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			setup := service.NewSetup(&store, registry, test.platform, nil, nil)
			fake := &libraryPublicationHTTPFake{}
			handler := HandlerWithDependencies(Dependencies{Setup: setup, LibraryPublications: fake})
			path := base
			body := ""
			if test.method == http.MethodPost {
				path += "/manual-lyrics"
				body = `{"expected_metadata_revision":1,"expected_generation":"` + uuid.NewString() + `","expected_epoch":"` + uuid.NewString() + `"}`
			}
			response := libraryPublicationRequest(t, handler, test.method, path, body)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d: %s", response.Code, response.Body)
			}
			if fake.readCalls != 0 || fake.applyCalls != 0 {
				t.Fatalf("guard called publication service: read=%d apply=%d", fake.readCalls, fake.applyCalls)
			}
		})
	}

	response := libraryPublicationRequest(t, HandlerWithDependencies(Dependencies{}), http.MethodGet, "/library/releases/not-a-uuid/positions/"+positionID.String()+"/publication", "")
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid UUID status = %d: %s", response.Code, response.Body)
	}
	response = libraryPublicationRequest(t, HandlerWithDependencies(Dependencies{}), http.MethodPost, base+"/manual-lyrics", `{}`)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid body status = %d: %s", response.Code, response.Body)
	}
}

func libraryPublicationRequest(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
