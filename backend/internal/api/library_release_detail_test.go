package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type libraryReleaseDetailRepositoryFixture struct {
	detail *persistence.LibraryReleaseDetail
	err    error
}

func (fixture libraryReleaseDetailRepositoryFixture) ReadReleaseDetail(context.Context, uuid.UUID) (*persistence.LibraryReleaseDetail, error) {
	return fixture.detail, fixture.err
}

func TestLibraryReleaseDetailHTTP(t *testing.T) {
	releaseID, artistID, trackID, positionID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	artistName := "Artist"
	serviceReader := service.NewLibraryReleaseDetails(libraryReleaseDetailRepositoryFixture{detail: &persistence.LibraryReleaseDetail{
		ID: releaseID, Title: "Album", Metadata: json.RawMessage(`{"year":2024}`), MetadataVersion: 2,
		Artists: []persistence.LibraryArtistCredit{{ArtistID: artistID, ArtistName: &artistName, CreditName: "Credit", JoinPhrase: " feat. "}},
		Tracks:  []persistence.LibraryReleaseTrackDetail{{ID: positionID, LocalTrackID: trackID, Medium: 1, Position: 2, Title: "Song", Metadata: json.RawMessage(`{}`), MetadataVersion: 3}},
	}})
	handler := HandlerWithDependencies(Dependencies{LibraryReleaseDetails: serviceReader})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/library/releases/"+releaseID.String(), nil))
	if response.Code != http.StatusOK {
		t.Fatalf("GET status = %d: %s", response.Code, response.Body)
	}
	var body struct {
		ID      uuid.UUID `json:"id"`
		Artists []struct {
			ArtistID   uuid.UUID `json:"artist_id"`
			ArtistName string    `json:"artist_name"`
			CreditName string    `json:"credit_name"`
			JoinPhrase string    `json:"join_phrase"`
		} `json:"artists"`
		Tracks []LibraryReleaseTrackResponse `json:"tracks"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ID != releaseID || len(body.Artists) != 1 || body.Artists[0].ArtistID != artistID || body.Artists[0].CreditName != "Credit" || len(body.Tracks) != 1 || body.Tracks[0].ID != positionID || body.Tracks[0].Medium != 1 || body.Tracks[0].Position != 2 {
		t.Fatalf("release detail body = %+v", body)
	}

	for _, test := range []struct {
		name       string
		path       string
		dependency *service.LibraryReleaseDetails
		wantStatus int
	}{
		{name: "invalid UUID", path: "/library/releases/not-a-uuid", dependency: serviceReader, wantStatus: http.StatusBadRequest},
		{name: "invalid UUID before dependency check", path: "/library/releases/not-a-uuid", dependency: nil, wantStatus: http.StatusBadRequest},
		{name: "missing release", path: "/library/releases/" + uuid.NewString(), dependency: service.NewLibraryReleaseDetails(libraryReleaseDetailRepositoryFixture{err: persistence.ErrLibraryReleaseNotFound}), wantStatus: http.StatusNotFound},
		{name: "unavailable repository", path: "/library/releases/" + uuid.NewString(), dependency: service.NewLibraryReleaseDetails(libraryReleaseDetailRepositoryFixture{err: errors.New("db unavailable")}), wantStatus: http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := HandlerWithDependencies(Dependencies{LibraryReleaseDetails: test.dependency})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != test.wantStatus {
				t.Fatalf("GET status = %d, want %d: %s", response.Code, test.wantStatus, response.Body)
			}
		})
	}
	if strings.Contains(response.Body.String(), "ffprobe_json") {
		t.Fatal("detail response must not expose raw ffprobe snapshots")
	}
}
