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

type libraryPrimaryArtistWriterFixture struct {
	revision int64
	err      error
	calls    *int
}

func (fixture libraryPrimaryArtistWriterFixture) SetPrimaryArtist(context.Context, uuid.UUID, uuid.UUID, *uuid.UUID, int64, *int64) (int64, error) {
	if fixture.calls != nil {
		(*fixture.calls)++
	}
	return fixture.revision, fixture.err
}

func (fixture libraryReleaseDetailRepositoryFixture) ReadReleaseDetail(context.Context, uuid.UUID) (*persistence.LibraryReleaseDetail, error) {
	return fixture.detail, fixture.err
}

func TestLibraryReleaseDetailHTTP(t *testing.T) {
	releaseID, artistID, trackID, positionID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	artistMBID := uuid.New()
	artistName := "Artist"
	serviceReader := service.NewLibraryReleaseDetails(libraryReleaseDetailRepositoryFixture{detail: &persistence.LibraryReleaseDetail{
		ID: releaseID, Title: "Album", Metadata: json.RawMessage(`{"year":2024}`), MetadataVersion: 2,
		PrimaryArtistID: &artistID, PrimaryArtistCandidates: []uuid.UUID{artistID}, PrimaryArtistMBIDs: map[uuid.UUID]*uuid.UUID{artistID: &artistMBID},
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
		ID            uuid.UUID `json:"id"`
		PrimaryArtist struct {
			Status              string     `json:"status"`
			EffectiveArtistID   *uuid.UUID `json:"effective_artist_id"`
			EffectiveArtistMBID *uuid.UUID `json:"effective_artist_mbid"`
		} `json:"primary_artist"`
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
	if body.ID != releaseID || body.PrimaryArtist.Status != string(service.LibraryArtistExplicit) || body.PrimaryArtist.EffectiveArtistID == nil || *body.PrimaryArtist.EffectiveArtistID != artistID || body.PrimaryArtist.EffectiveArtistMBID == nil || *body.PrimaryArtist.EffectiveArtistMBID != artistMBID || len(body.Artists) != 1 || body.Artists[0].ArtistID != artistID || body.Artists[0].CreditName != "Credit" || len(body.Tracks) != 1 || body.Tracks[0].ID != positionID || body.Tracks[0].Medium != 1 || body.Tracks[0].Position != 2 {
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

func TestLibraryPrimaryArtistMutationHTTP(t *testing.T) {
	releaseID := uuid.New()
	zeroClearCalls, zeroSetCalls := new(int), new(int)
	for _, test := range []struct {
		name       string
		path       string
		writer     *service.LibraryPrimaryArtists
		body       string
		wantStatus int
		wantReply  bool
		calls      *int
	}{
		{name: "success", writer: service.NewLibraryPrimaryArtists(libraryPrimaryArtistWriterFixture{revision: 2}), body: `{"expected_revision":1,"artist_id":null}`, wantStatus: http.StatusOK, wantReply: true},
		{name: "artist field required", writer: service.NewLibraryPrimaryArtists(libraryPrimaryArtistWriterFixture{}), body: `{"expected_revision":1}`, wantStatus: http.StatusUnprocessableEntity},
		{name: "position track revision required", path: "/library/releases/" + releaseID.String() + "/positions/" + uuid.NewString() + "/primary-artist", writer: service.NewLibraryPrimaryArtists(libraryPrimaryArtistWriterFixture{}), body: `{"expected_revision":1,"artist_id":null}`, wantStatus: http.StatusBadRequest},
		{name: "position success", path: "/library/releases/" + releaseID.String() + "/positions/" + uuid.NewString() + "/primary-artist", writer: service.NewLibraryPrimaryArtists(libraryPrimaryArtistWriterFixture{revision: 2}), body: `{"expected_revision":1,"expected_track_revision":2,"artist_id":null}`, wantStatus: http.StatusOK, wantReply: true},
		{name: "position zero UUID clear", path: "/library/releases/" + releaseID.String() + "/positions/" + uuid.Nil.String() + "/primary-artist", writer: service.NewLibraryPrimaryArtists(libraryPrimaryArtistWriterFixture{calls: zeroClearCalls}), body: `{"expected_revision":1,"expected_track_revision":2,"artist_id":null}`, wantStatus: http.StatusBadRequest, calls: zeroClearCalls},
		{name: "position zero UUID set", path: "/library/releases/" + releaseID.String() + "/positions/" + uuid.Nil.String() + "/primary-artist", writer: service.NewLibraryPrimaryArtists(libraryPrimaryArtistWriterFixture{calls: zeroSetCalls}), body: `{"expected_revision":1,"expected_track_revision":2,"artist_id":"` + uuid.NewString() + `"}`, wantStatus: http.StatusBadRequest, calls: zeroSetCalls},
		{name: "position malformed UUID", path: "/library/releases/" + releaseID.String() + "/positions/not-a-uuid/primary-artist", writer: service.NewLibraryPrimaryArtists(libraryPrimaryArtistWriterFixture{}), body: `{"expected_revision":1,"expected_track_revision":2,"artist_id":null}`, wantStatus: http.StatusBadRequest},
		{name: "invalid revision", writer: service.NewLibraryPrimaryArtists(libraryPrimaryArtistWriterFixture{}), body: `{"expected_revision":0,"artist_id":null}`, wantStatus: http.StatusBadRequest},
		{name: "missing release", writer: service.NewLibraryPrimaryArtists(libraryPrimaryArtistWriterFixture{err: persistence.ErrLibraryReleaseNotFound}), body: `{"expected_revision":1,"artist_id":null}`, wantStatus: http.StatusNotFound},
		{name: "stale revision", writer: service.NewLibraryPrimaryArtists(libraryPrimaryArtistWriterFixture{err: persistence.ErrLibraryPrimaryArtistConflict}), body: `{"expected_revision":1,"artist_id":null}`, wantStatus: http.StatusConflict},
		{name: "unavailable", writer: service.NewLibraryPrimaryArtists(libraryPrimaryArtistWriterFixture{err: errors.New("database unavailable")}), body: `{"expected_revision":1,"artist_id":null}`, wantStatus: http.StatusServiceUnavailable},
		{name: "missing dependency", writer: nil, body: `{"expected_revision":1,"artist_id":null}`, wantStatus: http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := HandlerWithDependencies(Dependencies{LibraryPrimaryArtists: test.writer})
			path := test.path
			if path == "" {
				path = "/library/releases/" + releaseID.String() + "/primary-artist"
			}
			request := httptest.NewRequest(http.MethodPut, path, strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("PUT status = %d, want %d: %s", response.Code, test.wantStatus, response.Body)
			}
			if test.calls != nil && *test.calls != 0 {
				t.Fatalf("writer called %d times for invalid position ID", *test.calls)
			}
			if test.wantReply {
				var result struct {
					Revision           int64 `json:"revision"`
					PublicationApplied bool  `json:"publication_applied"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result.Revision != 2 || result.PublicationApplied {
					t.Fatalf("mutation reply = %+v", result)
				}
			}
		})
	}
}
