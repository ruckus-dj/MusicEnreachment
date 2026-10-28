package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type libraryReleaseCreateRepositoryFixture struct {
	request persistence.LibraryReleaseCreateRequest
	result  persistence.LibraryReleaseCreateResult
	err     error
}

func (repository *libraryReleaseCreateRepositoryFixture) CreateStandaloneRelease(_ context.Context, request persistence.LibraryReleaseCreateRequest) (persistence.LibraryReleaseCreateResult, error) {
	repository.request = request
	return repository.result, repository.err
}

func TestLibraryReleaseCreationAllowsEmptyRequestAndReturnsNonNilCollections(t *testing.T) {
	releaseID := uuid.New()
	repository := &libraryReleaseCreateRepositoryFixture{result: persistence.LibraryReleaseCreateResult{ReleaseID: releaseID}}
	result, err := NewLibraryReleaseCreation(repository).Create(context.Background(), LibraryReleaseCreateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if repository.request.Title != "" || repository.request.ReleaseID != nil {
		t.Fatalf("request values were not preserved: %#v", repository.request)
	}
	if result.ReleaseID != releaseID || result.Media == nil || result.Positions == nil {
		t.Fatalf("result must preserve ID and initialize arrays: %#v", result)
	}
}

func TestLibraryReleaseCreationMapsCleanDTOAndPreservesEmptyStrings(t *testing.T) {
	providerID, providerSourceID, providerRecordingID := uuid.New(), uuid.New(), uuid.New()
	releaseID, sourceVariantID, selectedID := uuid.New(), uuid.New(), uuid.New()
	duration := int64(0)
	request := LibraryReleaseCreateRequest{
		ReleaseID: &releaseID, Title: "", ReleaseDate: "2024-02", Country: "", Label: "", CatalogNumber: "", ReleaseType: "",
		NewArtists:    []LibraryReleaseCreateArtist{{Key: "artist", Name: ""}},
		NewRecordings: []LibraryReleaseCreateRecording{{Key: "recording", Title: ""}},
		Credits:       []LibraryReleaseCreateCredit{{ArtistRef: LibraryReleaseCreateArtistRef{Kind: "new", Key: "artist"}, CreditName: "", Role: "", JoinPhrase: ""}},
		Media: []LibraryReleaseCreateMedium{{Key: "media", Title: "", Format: "", Positions: []LibraryReleaseCreatePosition{
			{Key: "position", DisplayedNumber: "", Title: "", DurationMS: &duration,
				RecordingRef: &LibraryReleaseCreateRecordingRef{Kind: "provider", ProviderID: &providerID, ProviderSourceID: &providerSourceID, ProviderRecordingID: &providerRecordingID, ConfigurationIdentity: "configuration"},
				Sources:      []LibraryReleaseCreateSource{{MediaVariantID: sourceVariantID}, {MediaVariantID: selectedID}}, SelectedMediaVariantID: &selectedID},
			{Key: "blank-recording-position", RecordingRef: &LibraryReleaseCreateRecordingRef{Kind: "new", Key: "recording"}},
		}}},
	}
	repository := &libraryReleaseCreateRepositoryFixture{result: persistence.LibraryReleaseCreateResult{ReleaseID: releaseID}}
	result, err := NewLibraryReleaseCreation(repository).Create(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if repository.request.Title != "" || repository.request.ReleaseID == nil || *repository.request.ReleaseID != releaseID || repository.request.NewArtists[0].Name != "" || repository.request.NewRecordings[0].Title != "" {
		t.Fatalf("release fields did not round-trip: %#v", repository.request)
	}
	position := repository.request.Media[0].Positions[0]
	if position.Title != "" || position.DisplayedNumber != "" || position.DurationMS == nil || *position.DurationMS != 0 || position.RecordingRef.ProviderRecordingID == uuid.Nil || position.RecordingRef.ProviderRecordingID != providerRecordingID || position.Sources[0].MediaVariantID != sourceVariantID || *position.SelectedMediaVariantID != selectedID {
		t.Fatalf("position fields did not map: %#v", position)
	}
	newRecordingPosition := repository.request.Media[0].Positions[1]
	if newRecordingPosition.RecordingRef == nil || newRecordingPosition.RecordingRef.Kind != "new" || newRecordingPosition.RecordingRef.Key != "recording" {
		t.Fatalf("new blank recording reference did not map: %#v", newRecordingPosition)
	}
	if result.Media == nil || result.Positions == nil {
		t.Fatalf("nil result collections: %#v", result)
	}
	encoded, err := json.Marshal(LibraryReleaseCreatePositionResult{RecordingID: nil})
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"position_key":"","position_id":"00000000-0000-0000-0000-000000000000","track_id":"00000000-0000-0000-0000-000000000000","recording_id":null}` {
		t.Fatalf("recording ID must serialize as null: %s", encoded)
	}
}

func TestLibraryReleaseCreationRejectsInvalidRequests(t *testing.T) {
	zero := uuid.Nil
	negative := int64(-1)
	for name, request := range map[string]LibraryReleaseCreateRequest{
		"duplicate media key":    {Media: []LibraryReleaseCreateMedium{{Key: "same"}, {Key: "same"}}},
		"negative duration":      {Media: []LibraryReleaseCreateMedium{{Key: "media", Positions: []LibraryReleaseCreatePosition{{Key: "position", DurationMS: &negative}}}}},
		"invalid date":           {ReleaseDate: "2024-13"},
		"zero release ID":        {ReleaseID: &zero},
		"dangling artist":        {Credits: []LibraryReleaseCreateCredit{{ArtistRef: LibraryReleaseCreateArtistRef{Kind: "new", Key: "missing"}}}},
		"conflicting artist ref": {Credits: []LibraryReleaseCreateCredit{{ArtistRef: LibraryReleaseCreateArtistRef{Kind: "existing", ID: &zero, Key: "unexpected"}}}},
		"selected unattached":    {Media: []LibraryReleaseCreateMedium{{Key: "media", Positions: []LibraryReleaseCreatePosition{{Key: "position", SelectedMediaVariantID: &zero}}}}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewLibraryReleaseCreation(&libraryReleaseCreateRepositoryFixture{}).Create(context.Background(), request)
			if !errors.Is(err, ErrLibraryReleaseCreateInvalidRequest) {
				t.Fatalf("Create() error = %v, want invalid request", err)
			}
		})
	}
}

func TestLibraryReleaseCreationUnavailable(t *testing.T) {
	if _, err := (*LibraryReleaseCreation)(nil).Create(context.Background(), LibraryReleaseCreateRequest{}); err == nil {
		t.Fatal("nil service should fail")
	}
	if _, err := NewLibraryReleaseCreation(nil).Create(context.Background(), LibraryReleaseCreateRequest{}); err == nil {
		t.Fatal("nil repository should fail")
	}
}
