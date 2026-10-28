package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type libraryReleaseCreateAPIRepository struct {
	request persistence.LibraryReleaseCreateRequest
	result  persistence.LibraryReleaseCreateResult
	err     error
}

func (repository *libraryReleaseCreateAPIRepository) CreateStandaloneRelease(_ context.Context, request persistence.LibraryReleaseCreateRequest) (persistence.LibraryReleaseCreateResult, error) {
	repository.request = request
	return repository.result, repository.err
}

func libraryReleaseCreateHandler(creation *service.LibraryReleaseCreation) (http.Handler, huma.API) {
	router := chi.NewRouter()
	humaAPI := New(router)
	registerLibraryReleaseCreation(humaAPI, creation)
	return router, humaAPI
}

func TestCreateLibraryReleaseAPIEmptyBodyAndUnavailable(t *testing.T) {
	releaseID := uuid.New()
	repository := &libraryReleaseCreateAPIRepository{result: persistence.LibraryReleaseCreateResult{ReleaseID: releaseID}}
	handler, _ := libraryReleaseCreateHandler(service.NewLibraryReleaseCreation(repository))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/library/releases", strings.NewReader(`{}`)))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", recorder.Code, recorder.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["release_id"] != releaseID.String() || len(result["media"].([]any)) != 0 || len(result["positions"].([]any)) != 0 {
		t.Fatalf("unexpected empty create result: %#v", result)
	}
	for _, forbidden := range []string{"operation", "job", "publication"} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("unexpected %q in result: %s", forbidden, recorder.Body.String())
		}
	}
	if repository.request.Title != "" {
		t.Fatalf("empty title was changed: %#v", repository.request)
	}

	unavailable, _ := libraryReleaseCreateHandler(nil)
	recorder = httptest.NewRecorder()
	unavailable.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/library/releases", strings.NewReader(`{}`)))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil service status = %d, want 503", recorder.Code)
	}
}

func TestCreateLibraryReleaseAPIFieldRoundTripAndNullableRecording(t *testing.T) {
	releaseID, mediaID, positionID, trackID, recordingID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	positionKey := "position-client-key"
	repository := &libraryReleaseCreateAPIRepository{result: persistence.LibraryReleaseCreateResult{
		ReleaseID: releaseID,
		Media:     []persistence.LibraryReleaseCreateMediaResult{{MediaKey: "media-client-key", MediaID: mediaID}},
		Positions: []persistence.LibraryReleaseCreatePositionResult{
			{PositionKey: positionKey, PositionID: positionID, TrackID: trackID},
			{PositionKey: "linked-position", PositionID: uuid.New(), TrackID: uuid.New(), RecordingID: &recordingID},
		},
	}}
	handler, _ := libraryReleaseCreateHandler(service.NewLibraryReleaseCreation(repository))
	payload := `{"release_id":"` + releaseID.String() + `","title":"","release_date":"2024-02","country":"","label":"","catalog_number":"","release_type":"","new_artists":[{"key":"artist-key","name":""}],"new_recordings":[{"key":"recording-key","title":""}],"credits":[{"artist_ref":{"kind":"new","key":"artist-key"},"credit_name":"","role":"","join_phrase":""}],"media":[{"key":"media-client-key","title":"","format":"","positions":[{"key":"position-client-key","displayed_number":"","title":"","duration_ms":0,"recording_ref":{"kind":"new","key":"recording-key"},"sources":[],"selected_media_variant_id":null}]}]}`
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/library/releases", strings.NewReader(payload)))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", recorder.Code, recorder.Body.String())
	}
	if repository.request.Title != "" || repository.request.NewArtists[0].Name != "" || repository.request.NewRecordings[0].Title != "" || repository.request.Media[0].Positions[0].DurationMS == nil || *repository.request.Media[0].Positions[0].DurationMS != 0 {
		t.Fatalf("fields were not passed through: %#v", repository.request)
	}
	var result map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	media := result["media"].([]any)[0].(map[string]any)
	position := result["positions"].([]any)[0].(map[string]any)
	if media["media_key"] != "media-client-key" || media["media_id"] != mediaID.String() || position["position_key"] != positionKey || position["position_id"] != positionID.String() || position["track_id"] != trackID.String() {
		t.Fatalf("response identity mapping failed: %#v", result)
	}
	if recordingID, exists := position["recording_id"]; !exists || recordingID != nil {
		t.Fatalf("recording_id must be explicitly null: %#v", position)
	}
	linkedPosition := result["positions"].([]any)[1].(map[string]any)
	if linkedPosition["recording_id"] != recordingID.String() {
		t.Fatalf("recording_id UUID string mapping failed: %#v", linkedPosition)
	}
}

func TestCreateLibraryReleaseAPIValidationConflictAndSchema(t *testing.T) {
	repository := &libraryReleaseCreateAPIRepository{}
	handler, humaAPI := libraryReleaseCreateHandler(service.NewLibraryReleaseCreation(repository))
	for _, body := range []string{
		`{"media":[{"key":"duplicate"},{"key":"duplicate"}]}`,
		`{"media":[{"key":"m","positions":[{"key":"p","duration_ms":-1}]}]}`,
		`{"release_date":"2024-02-31"}`,
		`{"release_id":"00000000-0000-0000-0000-000000000000"}`,
		`{"credits":[{"artist_ref":{"kind":"new","key":"missing"}}]}`,
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/library/releases", strings.NewReader(body)))
		if recorder.Code != http.StatusBadRequest && recorder.Code != http.StatusUnprocessableEntity {
			t.Errorf("body %s status = %d, want 400/422: %s", body, recorder.Code, recorder.Body.String())
		}
	}

	requestedID := uuid.New()
	repository.err = persistence.ErrLibraryReleaseCreateConflict
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/library/releases", strings.NewReader(`{"release_id":"`+requestedID.String()+`"}`)))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d, want 409: %s", recorder.Code, recorder.Body.String())
	}

	encoded, err := json.Marshal(humaAPI.OpenAPI())
	if err != nil {
		t.Fatal(err)
	}
	schema := string(encoded)
	for _, text := range []string{`"operationId":"create-library-release"`, `"/library/releases"`, `"201"`, `"recording_id"`} {
		if !strings.Contains(schema, text) {
			t.Errorf("OpenAPI schema missing %s: %s", text, schema)
		}
	}
	fieldIndex := strings.Index(schema, `"recording_id":{`)
	if fieldIndex < 0 {
		t.Fatal("OpenAPI schema has no recording_id property")
	}
	fieldSchema := schema[fieldIndex+len(`"recording_id":`):]
	depth, fieldEnd := 0, -1
	for index, character := range fieldSchema {
		if character == '{' {
			depth++
		} else if character == '}' {
			depth--
			if depth == 0 {
				fieldEnd = index + 1
				break
			}
		}
	}
	if fieldEnd < 0 {
		t.Fatalf("could not parse OpenAPI recording_id property: %s", fieldSchema)
	}
	fieldSchema = fieldSchema[:fieldEnd]
	var property struct {
		Type []string `json:"type"`
	}
	if err := json.Unmarshal([]byte(fieldSchema), &property); err != nil {
		t.Fatalf("decode nullable recording_id schema: %v", err)
	}
	hasString, hasNull := false, false
	for _, kind := range property.Type {
		hasString = hasString || kind == "string"
		hasNull = hasNull || kind == "null"
	}
	if !hasString || !hasNull {
		t.Errorf("OpenAPI recording_id must be a nullable string: %s", fieldSchema)
	}
}

func TestCreateLibraryReleaseAPIUnknownRepositoryFailureIsUnavailable(t *testing.T) {
	repository := &libraryReleaseCreateAPIRepository{err: errors.New("internal database detail")}
	handler, _ := libraryReleaseCreateHandler(service.NewLibraryReleaseCreation(repository))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/library/releases", strings.NewReader(`{}`)))
	if recorder.Code != http.StatusServiceUnavailable || strings.Contains(recorder.Body.String(), "internal database detail") {
		t.Fatalf("unexpected failure response %d: %s", recorder.Code, recorder.Body.String())
	}
}
