package service

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/musicbrainz"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type localProviderHydrationStore struct {
	calls         *[]string
	unhydrated    []persistence.LocalProviderRecordingIdentity
	unhydratedErr error
	unhydratedIDs []uuid.UUID
	read          persistence.LocalProviderItem
	readErr       error
	saved         *persistence.LocalProviderItem
	saveErr       error
}

func (store *localProviderHydrationStore) List(context.Context, string, string, string, int, int) (persistence.LocalProviderList, error) {
	return persistence.LocalProviderList{}, nil
}

func (store *localProviderHydrationStore) Read(context.Context, string, uuid.UUID) (persistence.LocalProviderItem, error) {
	return store.read, store.readErr
}

func (store *localProviderHydrationStore) Save(_ context.Context, item persistence.LocalProviderItem, _ bool) (persistence.LocalProviderItem, error) {
	*store.calls = append(*store.calls, "save")
	store.saved = &item
	if store.saveErr != nil {
		return persistence.LocalProviderItem{}, store.saveErr
	}
	if item.ID == uuid.Nil {
		item.ID = uuid.New()
	}
	item.ProviderID, item.ProviderCode, item.ProviderKey = uuid.New(), "local", "local-key"
	item.Genres = json.RawMessage("[]")
	return item, nil
}

func (store *localProviderHydrationStore) UnhydratedMusicBrainzRecordings(_ context.Context, recordingIDs []uuid.UUID) ([]persistence.LocalProviderRecordingIdentity, error) {
	*store.calls = append(*store.calls, "unhydrated")
	store.unhydratedIDs = append([]uuid.UUID(nil), recordingIDs...)
	return store.unhydrated, store.unhydratedErr
}

type localProviderHydrationLookup struct {
	calls     *[]string
	source    persistence.AutomaticFallbackProviderSource
	sourceErr error
	lookupErr error
}

func (lookup *localProviderHydrationLookup) ResolveCurrentMusicBrainzSource(context.Context) (persistence.AutomaticFallbackProviderSource, error) {
	*lookup.calls = append(*lookup.calls, "resolve")
	return lookup.source, lookup.sourceErr
}

func (lookup *localProviderHydrationLookup) LookupRecording(_ context.Context, mbid string) (musicbrainz.LookupResult, error) {
	*lookup.calls = append(*lookup.calls, "lookup:"+mbid)
	return musicbrainz.LookupResult{}, lookup.lookupErr
}

func TestLocalProviderReleaseCreateHydratesUnhydratedMusicBrainzRecordings(t *testing.T) {
	providerID := uuid.New()
	first, second := uuid.New(), uuid.New()
	calls := make([]string, 0, 8)
	store := &localProviderHydrationStore{
		calls: &calls,
		unhydrated: []persistence.LocalProviderRecordingIdentity{
			{ProviderID: providerID, ProviderKey: "mb-first"},
			{ProviderID: providerID, ProviderKey: "mb-second"},
			{ProviderID: uuid.New(), ProviderKey: "mb-stale-source"},
		},
	}
	lookup := &localProviderHydrationLookup{calls: &calls, source: persistence.AutomaticFallbackProviderSource{ProviderID: providerID}}
	catalog := NewLocalProviderCatalog(store, nil).WithRecordingHydrator(lookup)

	if _, err := catalog.Create(context.Background(), "release", LocalProviderItem{
		Title: "Release", ReleaseGroupTitle: "Group",
		Tracks: []LocalProviderTrack{{RecordingID: first}, {RecordingID: second}, {RecordingID: first}},
	}); err != nil {
		t.Fatal(err)
	}

	if want := []string{"unhydrated", "resolve", "lookup:mb-first", "lookup:mb-second", "save"}; !slices.Equal(calls, want) {
		t.Fatalf("hydration order = %v, want %v", calls, want)
	}
	if want := []uuid.UUID{first, second}; !slices.Equal(store.unhydratedIDs, want) {
		t.Fatalf("hydrated recording ids = %v, want %v", store.unhydratedIDs, want)
	}
}

func TestLocalProviderReleaseUpdateHydratesBeforeSave(t *testing.T) {
	providerID, releaseID, recordingID := uuid.New(), uuid.New(), uuid.New()
	calls := make([]string, 0, 6)
	store := &localProviderHydrationStore{
		calls:      &calls,
		read:       persistence.LocalProviderItem{ID: releaseID, Kind: "release"},
		unhydrated: []persistence.LocalProviderRecordingIdentity{{ProviderID: providerID, ProviderKey: "mb-recording"}},
	}
	lookup := &localProviderHydrationLookup{calls: &calls, source: persistence.AutomaticFallbackProviderSource{ProviderID: providerID}}
	catalog := NewLocalProviderCatalog(store, nil).WithRecordingHydrator(lookup)

	if _, err := catalog.Update(context.Background(), "release", releaseID, LocalProviderItem{
		Title: "Release", ReleaseGroupTitle: "Group",
		Tracks: []LocalProviderTrack{{RecordingID: recordingID}},
	}); err != nil {
		t.Fatal(err)
	}

	if want := []string{"unhydrated", "resolve", "lookup:mb-recording", "save"}; !slices.Equal(calls, want) {
		t.Fatalf("hydration order = %v, want %v", calls, want)
	}
	if store.saved == nil || store.saved.ID != releaseID {
		t.Fatalf("saved release = %#v", store.saved)
	}
}

func TestLocalProviderReleaseHydrationSkipsMissingConfigurationAndHydratedTracks(t *testing.T) {
	providerID, recordingID := uuid.New(), uuid.New()
	calls := make([]string, 0, 4)
	store := &localProviderHydrationStore{
		calls:      &calls,
		unhydrated: []persistence.LocalProviderRecordingIdentity{{ProviderID: providerID, ProviderKey: "mb-recording"}},
	}
	lookup := &localProviderHydrationLookup{calls: &calls, source: persistence.AutomaticFallbackProviderSource{ProviderID: providerID}}
	catalog := NewLocalProviderCatalog(store, nil).WithRecordingHydrator(lookup)

	// No configured MusicBrainz target keeps a Local release editable without
	// provider I/O.
	lookup.sourceErr = ErrMusicBrainzConfigurationMissing
	if _, err := catalog.Create(context.Background(), "release", LocalProviderItem{
		Title: "Release", ReleaseGroupTitle: "Group",
		Tracks: []LocalProviderTrack{{RecordingID: recordingID}},
	}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"unhydrated", "resolve", "save"}; !slices.Equal(calls, want) {
		t.Fatalf("missing-configuration calls = %v, want %v", calls, want)
	}

	// Already hydrated identities are not looked up again.
	calls = calls[:0]
	store.unhydrated = nil
	lookup.sourceErr = nil
	if _, err := catalog.Create(context.Background(), "release", LocalProviderItem{
		Title: "Release", ReleaseGroupTitle: "Group",
		Tracks: []LocalProviderTrack{{RecordingID: recordingID}},
	}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"unhydrated", "save"}; !slices.Equal(calls, want) {
		t.Fatalf("hydrated calls = %v, want %v", calls, want)
	}
}

func TestLocalProviderReleaseHydrationFailurePreventsSave(t *testing.T) {
	providerID, recordingID := uuid.New(), uuid.New()
	calls := make([]string, 0, 4)
	store := &localProviderHydrationStore{
		calls:      &calls,
		unhydrated: []persistence.LocalProviderRecordingIdentity{{ProviderID: providerID, ProviderKey: "mb-recording"}},
	}
	lookup := &localProviderHydrationLookup{
		calls:     &calls,
		source:    persistence.AutomaticFallbackProviderSource{ProviderID: providerID},
		lookupErr: errors.New("provider unavailable"),
	}
	catalog := NewLocalProviderCatalog(store, nil).WithRecordingHydrator(lookup)

	_, err := catalog.Create(context.Background(), "release", LocalProviderItem{
		Title: "Release", ReleaseGroupTitle: "Group",
		Tracks: []LocalProviderTrack{{RecordingID: recordingID}},
	})
	if err == nil {
		t.Fatal("hydration failure did not fail the release save")
	}
	if store.saved != nil {
		t.Fatalf("hydration failure still saved the release: %#v", store.saved)
	}
	if want := []string{"unhydrated", "resolve", "lookup:mb-recording"}; !slices.Equal(calls, want) {
		t.Fatalf("failed hydration calls = %v, want %v", calls, want)
	}
}

func TestLocalProviderHydrationDoesNotRunForLocalOnlyPaths(t *testing.T) {
	providerID := uuid.New()
	for name, test := range map[string]struct {
		kind  string
		item  LocalProviderItem
		calls []string
	}{
		"non-release": {
			kind: "recording", item: LocalProviderItem{Title: "Recording"},
			calls: []string{"save"},
		},
		"release without tracks": {
			kind: "release", item: LocalProviderItem{Title: "Release", ReleaseGroupTitle: "Group"},
			calls: []string{"save"},
		},
		"release with only hydrated or local recordings": {
			kind: "release",
			item: LocalProviderItem{
				Title: "Release", ReleaseGroupTitle: "Group",
				Tracks: []LocalProviderTrack{{RecordingID: uuid.New()}},
			},
			calls: []string{"unhydrated", "save"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			calls := make([]string, 0, 4)
			store := &localProviderHydrationStore{calls: &calls}
			lookup := &localProviderHydrationLookup{calls: &calls, source: persistence.AutomaticFallbackProviderSource{ProviderID: providerID}}
			catalog := NewLocalProviderCatalog(store, nil).WithRecordingHydrator(lookup)
			if _, err := catalog.Create(context.Background(), test.kind, test.item); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(calls, test.calls) {
				t.Fatalf("calls = %v, want %v", calls, test.calls)
			}
		})
	}
}
