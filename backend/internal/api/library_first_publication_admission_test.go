package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

func TestLibraryFirstPublicationAdmissionHTTP(t *testing.T) {
	releaseID, positionID, epoch := uuid.New(), uuid.New(), uuid.New()
	operationID := uuid.New()
	store := apiSettingsStore{}
	registry := settings.New(store, nil)
	if err := registry.CompleteSetup(context.Background()); err != nil {
		t.Fatal(err)
	}
	setup := service.NewSetup(&store, registry, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, nil, nil)
	fake := &libraryPublicationHTTPFake{operationID: operationID}
	operationsRepo := libraryPublicationOperationSnapshotRepository{operation: &persistence.Operation{
		ID: operationID, Kind: "publish_first", State: "queued", Stage: "queued",
	}}
	handler := HandlerWithDependencies(Dependencies{
		Setup: setup, LibraryPublications: fake, Operations: service.NewOperations(operationsRepo),
	})
	path := "/library/releases/" + releaseID.String() + "/positions/" + positionID.String() + "/publication"
	digest := strings.Repeat("a", 64)

	response := libraryPublicationRequest(t, handler, http.MethodPost, path,
		`{"preview_digest":"`+digest+`","expected_epoch":"`+epoch.String()+`","frozen_inputs":{},"source_id":"`+uuid.NewString()+`"}`)
	if response.Code != http.StatusUnprocessableEntity || fake.admitCalls != 0 {
		t.Fatalf("client-supplied frozen inputs status=%d admit calls=%d: %s", response.Code, fake.admitCalls, response.Body)
	}
	response = libraryPublicationRequest(t, handler, http.MethodPost, path,
		`{"preview_digest":"bad","expected_epoch":"`+epoch.String()+`"}`)
	if response.Code != http.StatusUnprocessableEntity || fake.admitCalls != 0 {
		t.Fatalf("bad digest status=%d admit calls=%d: %s", response.Code, fake.admitCalls, response.Body)
	}

	response = libraryPublicationRequest(t, handler, http.MethodPost, path,
		`{"preview_digest":"`+digest+`","expected_epoch":"`+epoch.String()+`"}`)
	if response.Code != http.StatusAccepted {
		t.Fatalf("POST status = %d: %s", response.Code, response.Body)
	}
	if fake.admitCalls != 1 || fake.admitReleaseID != releaseID || fake.admitPositionID != positionID ||
		fake.admitRequest.PreviewDigest != digest || fake.admitRequest.ExpectedEpoch != epoch {
		t.Fatalf("AdmitFirst arguments = calls:%d release:%s position:%s request:%+v", fake.admitCalls, fake.admitReleaseID, fake.admitPositionID, fake.admitRequest)
	}
	var queued struct {
		ID    uuid.UUID `json:"id"`
		Kind  string    `json:"kind"`
		State string    `json:"state"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &queued); err != nil {
		t.Fatal(err)
	}
	if queued.ID != operationID || queued.State != "queued" {
		t.Fatalf("queued operation = %+v", queued)
	}

	locationID, variantID := uuid.New(), uuid.New()
	response = libraryPublicationRequest(t, handler, http.MethodPost, path,
		`{"preview_digest":"`+digest+`","expected_epoch":"`+epoch.String()+`","location_id":"`+locationID.String()+`"}`)
	if response.Code != http.StatusBadRequest || fake.admitCalls != 1 {
		t.Fatalf("partial source choice status=%d admit calls=%d: %s", response.Code, fake.admitCalls, response.Body)
	}
	response = libraryPublicationRequest(t, handler, http.MethodPost, path,
		`{"preview_digest":"`+digest+`","expected_epoch":"`+epoch.String()+`","location_id":"`+locationID.String()+`","media_variant_id":"`+variantID.String()+`","stream_index":3}`)
	if response.Code != http.StatusAccepted || fake.admitCalls != 2 || fake.admitRequest.SourceChoice == nil ||
		fake.admitRequest.SourceChoice.LocationID != locationID || fake.admitRequest.SourceChoice.MediaVariantID != variantID || fake.admitRequest.SourceChoice.StreamIndex != 3 {
		t.Fatalf("full source choice status=%d admit calls=%d request=%+v: %s", response.Code, fake.admitCalls, fake.admitRequest, response.Body)
	}

	fake.admitErr = persistence.ErrPublicationAdmissionConflict
	response = libraryPublicationRequest(t, handler, http.MethodPost, path,
		`{"preview_digest":"`+digest+`","expected_epoch":"`+epoch.String()+`"}`)
	if response.Code != http.StatusConflict || strings.Contains(response.Body.String(), persistence.ErrPublicationAdmissionConflict.Error()) {
		t.Fatalf("stale admission status=%d: %s", response.Code, response.Body)
	}

	fake.admitErr = service.ErrPublicationArtwork
	response = libraryPublicationRequest(t, handler, http.MethodPost, path,
		`{"preview_digest":"`+digest+`","expected_epoch":"`+epoch.String()+`"}`)
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), service.ErrPublicationArtwork.Error()) {
		t.Fatalf("pending artwork status=%d: %s", response.Code, response.Body)
	}
}

func TestMatchingReceiptPublicationHTTPUsesOnlyExactOrdinaryWorkflow(t *testing.T) {
	releaseID, positionID, epoch, generation := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	choice := service.FirstPublicationSourceChoice{LocationID: uuid.New(), MediaVariantID: uuid.New(), StreamIndex: 3}
	receipt := persistence.MatchingReceiptPublicationSource{Kind: "local", RequestID: uuid.New(), TrackSourceID: uuid.New()}
	operationID := uuid.New()
	store := apiSettingsStore{}
	registry := settings.New(store, nil)
	if err := registry.CompleteSetup(context.Background()); err != nil {
		t.Fatal(err)
	}
	setup := service.NewSetup(&store, registry, settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}, nil, nil)
	fake := &libraryPublicationHTTPFake{operationID: operationID}
	operations := service.NewOperations(libraryPublicationOperationSnapshotRepository{operation: &persistence.Operation{
		ID: operationID, Kind: "publish_track_v1", State: "queued", Stage: "queued",
	}})
	handler := HandlerWithDependencies(Dependencies{Setup: setup, LibraryPublications: fake, Operations: operations})
	target := fmt.Sprintf("/library/releases/%s/positions/%s/publication", releaseID, positionID)
	query := fmt.Sprintf("?workflow=matching_receipt&receipt_kind=%s&receipt_request_id=%s&receipt_track_source_id=%s&location_id=%s&media_variant_id=%s&stream_index=%d",
		receipt.Kind, receipt.RequestID, receipt.TrackSourceID, choice.LocationID, choice.MediaVariantID, choice.StreamIndex)
	preview := libraryPublicationRequest(t, handler, http.MethodGet, target+"/preview"+query, "")
	if preview.Code != http.StatusOK || fake.matchingReceiptPreviewCalls != 1 || fake.previewCalls != 0 ||
		fake.matchingReceiptPreviewSource != receipt || fake.matchingReceiptPreviewChoice == nil || *fake.matchingReceiptPreviewChoice != choice {
		t.Fatalf("exact receipt preview = %d, %s", preview.Code, preview.Body)
	}
	for _, invalid := range []string{query + "&mode=forced", strings.Replace(query, "matching_receipt", "first", 1)} {
		rejected := libraryPublicationRequest(t, handler, http.MethodGet, target+"/preview"+invalid, "")
		if rejected.Code != http.StatusBadRequest || fake.matchingReceiptPreviewCalls != 1 || fake.previewCalls != 0 {
			t.Fatalf("incompatible receipt query = %d, %s", rejected.Code, rejected.Body)
		}
	}
	digest := strings.Repeat("a", 64)
	body := fmt.Sprintf(`{"workflow":"matching_receipt","mode":"ordinary","preview_digest":"%s","expected_epoch":"%s","expected_generation":"%s","location_id":"%s","media_variant_id":"%s","stream_index":%d,"receipt":{"kind":"%s","request_id":"%s","track_source_id":"%s"}}`,
		digest, epoch, generation, choice.LocationID, choice.MediaVariantID, choice.StreamIndex, receipt.Kind, receipt.RequestID, receipt.TrackSourceID)
	admitted := libraryPublicationRequest(t, handler, http.MethodPost, target, body)
	confirmation := fake.matchingReceiptAdmitRequest
	if admitted.Code != http.StatusAccepted || fake.matchingReceiptAdmitCalls != 1 || fake.admitCalls != 0 ||
		confirmation.Receipt == nil || *confirmation.Receipt != receipt || confirmation.SourceChoice == nil || *confirmation.SourceChoice != choice ||
		confirmation.ExpectedEpoch != epoch || confirmation.ExpectedGeneration == nil || *confirmation.ExpectedGeneration != generation ||
		confirmation.PreviewDigest != digest {
		t.Fatalf("exact receipt admission = %d, %s; confirmation %+v", admitted.Code, admitted.Body, confirmation)
	}
	rejected := libraryPublicationRequest(t, handler, http.MethodPost, target, strings.Replace(body, `"ordinary"`, `"forced"`, 1))
	if rejected.Code != http.StatusBadRequest || fake.matchingReceiptAdmitCalls != 1 || fake.admitCalls != 0 {
		t.Fatalf("forced receipt admission = %d, %s", rejected.Code, rejected.Body)
	}
}
