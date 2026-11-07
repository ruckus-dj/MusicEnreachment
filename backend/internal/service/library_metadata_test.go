package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type libraryMetadataUpdateCall struct {
	scope              librarymetadata.Scope
	releaseID, ownerID uuid.UUID
	expected           int64
	envelope           librarymetadata.Envelope
}

type fakeLibraryMetadataRepository struct {
	current      persistence.LibraryMetadata
	readErr      error
	updateErr    error
	updates      []libraryMetadataUpdateCall
	catalog      []librarymetadata.GenreReference
	catalogReads int
}

var rockGenreID = uuid.MustParse("11111111-1111-4111-8111-111111111111")

func (repository *fakeLibraryMetadataRepository) GenreCatalogItems(context.Context) ([]librarymetadata.GenreReference, error) {
	repository.catalogReads++
	if repository.catalog != nil {
		return repository.catalog, nil
	}
	return []librarymetadata.GenreReference{{ID: rockGenreID.String(), SourceName: "rock", Name: "Rock"}}, nil
}

type repositoryWithoutGenreCatalog struct {
	current persistence.LibraryMetadata
	updates int
}

type displayLibraryMetadataRepository struct {
	*fakeLibraryMetadataRepository
	display      persistence.LibraryMetadataDisplay
	displayErr   error
	displayReads int
}

func (repository *displayLibraryMetadataRepository) ReadMetadataDisplay(context.Context, librarymetadata.Scope, uuid.UUID, uuid.UUID) (persistence.LibraryMetadataDisplay, error) {
	repository.displayReads++
	return repository.display, repository.displayErr
}

func (repository *repositoryWithoutGenreCatalog) ReadMetadata(context.Context, librarymetadata.Scope, uuid.UUID, uuid.UUID) (persistence.LibraryMetadata, error) {
	return repository.current, nil
}

func (repository *repositoryWithoutGenreCatalog) UpdateMetadata(_ context.Context, _ librarymetadata.Scope, _, _ uuid.UUID, expected int64, envelope librarymetadata.Envelope) (persistence.LibraryMetadata, error) {
	repository.updates++
	return persistence.LibraryMetadata{Envelope: envelope, Revision: expected + 1}, nil
}

func (repository *fakeLibraryMetadataRepository) ReadMetadata(context.Context, librarymetadata.Scope, uuid.UUID, uuid.UUID) (persistence.LibraryMetadata, error) {
	return repository.current, repository.readErr
}

func (repository *fakeLibraryMetadataRepository) UpdateMetadata(_ context.Context, scope librarymetadata.Scope, releaseID, ownerID uuid.UUID, expected int64, envelope librarymetadata.Envelope) (persistence.LibraryMetadata, error) {
	repository.updates = append(repository.updates, libraryMetadataUpdateCall{scope: scope, releaseID: releaseID, ownerID: ownerID, expected: expected, envelope: envelope})
	if repository.updateErr != nil {
		return persistence.LibraryMetadata{}, repository.updateErr
	}
	repository.current.Envelope = envelope
	repository.current.Revision = expected + 1
	return repository.current, nil
}

func libraryMetadataChanges(t *testing.T, document string) map[string]json.RawMessage {
	t.Helper()
	var changes map[string]json.RawMessage
	if err := json.Unmarshal([]byte(document), &changes); err != nil {
		t.Fatalf("decode changes: %v", err)
	}
	return changes
}

func TestLibraryMetadataUpdateAppliesManualValuesAndPreservesExistingFields(t *testing.T) {
	for _, test := range []struct {
		name, changes, field string
		state                librarymetadata.ValueState
		value                string
	}{
		{name: "false compilation", field: "compilation", changes: `{"compilation":{"state":"value","value":false}}`, state: librarymetadata.StateValue, value: "false"},
		{name: "genre selection", field: "genre", changes: `{"genre":{"state":"value","value":[{"id":"11111111-1111-4111-8111-111111111111","source_name":"rock","name":"Rock"}]}}`, state: librarymetadata.StateValue, value: `[{"id":"11111111-1111-4111-8111-111111111111","source_name":"rock","name":"Rock"}]`},
		{name: "explicit empty genre", field: "genre", changes: `{"genre":{"state":"explicit_empty"}}`, state: librarymetadata.StateExplicitEmpty, value: "null"},
		{name: "absent genre", field: "genre", changes: `{"genre":{"state":"absent"}}`, state: librarymetadata.StateAbsent, value: "null"},
	} {
		t.Run(test.name, func(t *testing.T) {
			comment := librarymetadata.ManualFieldValue(librarymetadata.StateValue, "keep", true)
			repository := &fakeLibraryMetadataRepository{current: persistence.LibraryMetadata{Revision: 3, Envelope: librarymetadata.Envelope{
				Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopeRelease,
				Desired: map[string]librarymetadata.FieldValue{"comment": comment},
			}}}
			service := NewLibraryMetadataService(repository)
			result, err := service.Update(context.Background(), librarymetadata.ScopeRelease, uuid.New(), uuid.New(), 3, libraryMetadataChanges(t, test.changes))
			if err != nil {
				t.Fatalf("update: %v", err)
			}
			if len(repository.updates) != 1 {
				t.Fatalf("repository updates = %d, want 1 immediate update", len(repository.updates))
			}
			field, ok := result.Envelope.Desired[test.field]
			if !ok {
				t.Fatal("updated field is missing")
			}
			if field.State != test.state || field.Authority != librarymetadata.AuthorityManual || field.Provenance.Kind != librarymetadata.ProvenanceManual {
				t.Fatalf("field = %#v, want manual authority and provenance", field)
			}
			encoded, err := json.Marshal(field.Value)
			if err != nil || string(encoded) != test.value {
				t.Fatalf("field value = %s (error %v), want %s", encoded, err, test.value)
			}
			if kept := result.Envelope.Desired["comment"]; kept.State != comment.State || kept.Value != comment.Value {
				t.Fatalf("unrelated field = %#v, want preserved %#v", kept, comment)
			}
			if result.Revision != 4 {
				t.Fatalf("revision = %d, want 4", result.Revision)
			}
		})
	}
}

func TestLibraryMetadataUpdateRejectsInvalidChanges(t *testing.T) {
	for _, test := range []struct {
		name, scope, changes string
	}{
		{name: "unknown field", changes: `{"unknown":{"state":"value","value":"x"}}`},
		{name: "canonical title", changes: `{"title":{"state":"value","value":"x"}}`},
		{name: "wrong scope", scope: "recording", changes: `{"release_date":{"state":"value","value":"2020"}}`},
		{name: "forged authority", changes: `{"genre":{"state":"value","value":[{"id":"11111111-1111-4111-8111-111111111111","source_name":"rock","name":"Rock"}],"authority":"automatic"}}`},
		{name: "forged provenance", changes: `{"genre":{"state":"value","value":[{"id":"11111111-1111-4111-8111-111111111111","source_name":"rock","name":"Rock"}],"provenance":{"kind":"provider"}}}`},
		{name: "null absent value", changes: `{"genre":{"state":"absent","value":null}}`},
		{name: "null value", changes: `{"genre":{"state":"value","value":null}}`},
		{name: "invalid synced lyrics", scope: "position", changes: `{"synced_lyrics":{"state":"value","value":"not LRC"}}`},
		{name: "empty synced lyrics", scope: "position", changes: `{"synced_lyrics":{"state":"value","value":""}}`},
		{name: "whitespace synced lyrics", scope: "position", changes: `{"synced_lyrics":{"state":"value","value":" \t\r\n "}}`},
		{name: "synced lyrics wrong scope", changes: `{"synced_lyrics":{"state":"value","value":"[00:01.00]x"}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeLibraryMetadataRepository{current: persistence.LibraryMetadata{Revision: 1, Envelope: librarymetadata.Envelope{
				Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopeRelease, Desired: map[string]librarymetadata.FieldValue{},
			}}}
			service := NewLibraryMetadataService(repository)
			scope := librarymetadata.ScopeRelease
			if test.scope != "" {
				scope = librarymetadata.Scope(test.scope)
			}
			_, err := service.Update(context.Background(), scope, uuid.New(), uuid.New(), 1, libraryMetadataChanges(t, test.changes))
			if !errors.Is(err, ErrLibraryMetadataInvalidRequest) {
				t.Fatalf("error = %v, want invalid request", err)
			}
			if len(repository.updates) != 0 {
				t.Fatalf("repository updates = %d, want 0", len(repository.updates))
			}
		})
	}
}

func TestLibraryMetadataUpdatePreservesManualSyncedLyricsBytesAndRemoval(t *testing.T) {
	lyrics := "[00:01.00]雪\r\n[00:02.00]café"
	repository := &fakeLibraryMetadataRepository{current: persistence.LibraryMetadata{Revision: 4, Envelope: librarymetadata.Envelope{
		Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopePosition, Desired: map[string]librarymetadata.FieldValue{},
	}}}
	service := NewLibraryMetadataService(repository)
	result, err := service.Update(context.Background(), librarymetadata.ScopePosition, uuid.New(), uuid.New(), 4,
		libraryMetadataChanges(t, `{"synced_lyrics":{"state":"value","value":"[00:01.00]雪\r\n[00:02.00]café"}}`))
	if err != nil {
		t.Fatal(err)
	}
	field := result.Envelope.Desired["synced_lyrics"]
	if field.Value != lyrics || field.Authority != librarymetadata.AuthorityManual || field.Provenance.Kind != librarymetadata.ProvenanceManual {
		t.Fatalf("saved field = %#v, want exact manual lyrics", field)
	}
	removed, err := service.Update(context.Background(), librarymetadata.ScopePosition, uuid.New(), uuid.New(), 4,
		libraryMetadataChanges(t, `{"synced_lyrics":{"state":"explicit_empty"}}`))
	if err != nil {
		t.Fatal(err)
	}
	field = removed.Envelope.Desired["synced_lyrics"]
	if field.State != librarymetadata.StateExplicitEmpty || field.Authority != librarymetadata.AuthorityManual || field.Provenance.Kind != librarymetadata.ProvenanceManual {
		t.Fatalf("removed field = %#v, want manual explicit-empty", field)
	}
}

func TestLibraryMetadataUpdateForwardsRevisionAndIdentity(t *testing.T) {
	repository := &fakeLibraryMetadataRepository{current: persistence.LibraryMetadata{Revision: 7, Envelope: librarymetadata.Envelope{
		Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopeRelease, Desired: map[string]librarymetadata.FieldValue{},
	}}}
	releaseID, ownerID := uuid.New(), uuid.New()
	result, err := NewLibraryMetadataService(repository).Update(context.Background(), librarymetadata.ScopeRelease, releaseID, ownerID, 7, libraryMetadataChanges(t, `{"genre":{"state":"value","value":[{"id":"11111111-1111-4111-8111-111111111111","source_name":"forged","name":"not canonical"}]}}`))
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(repository.updates) != 1 {
		t.Fatalf("repository updates = %d, want 1", len(repository.updates))
	}
	call := repository.updates[0]
	if call.scope != librarymetadata.ScopeRelease || call.releaseID != releaseID || call.ownerID != ownerID || call.expected != 7 {
		t.Fatalf("call = %#v, want scope, owner identity, and revision 7", call)
	}
	if call.envelope.Version != librarymetadata.EnvelopeVersion || call.envelope.Scope != librarymetadata.ScopeRelease {
		t.Fatalf("envelope = %#v, want current envelope", call.envelope)
	}
	genres, ok := call.envelope.Desired["genre"].Value.([]librarymetadata.GenreReference)
	if !ok || len(genres) != 1 || genres[0].SourceName != "rock" || genres[0].Name != "Rock" {
		t.Fatalf("persisted genres = %#v, want catalog-canonical genre", call.envelope.Desired["genre"].Value)
	}
	if result.Revision != 8 {
		t.Fatalf("revision = %d, want 8", result.Revision)
	}
}

func TestLibraryMetadataGenreSelectionRequiresCatalogButClearingDoesNot(t *testing.T) {
	for _, test := range []struct {
		name      string
		change    string
		wantError bool
	}{
		{name: "nonempty unavailable", change: `{"genre":{"state":"value","value":[{"id":"11111111-1111-4111-8111-111111111111","source_name":"rock","name":"Rock"}]}}`, wantError: true},
		{name: "explicit empty unavailable", change: `{"genre":{"state":"explicit_empty"}}`},
		{name: "absent unavailable", change: `{"genre":{"state":"absent"}}`},
		{name: "empty value rejected", change: `{"genre":{"state":"value","value":[]}}`, wantError: true},
		{name: "unrelated field unavailable", change: `{"comment":{"state":"value","value":"note"}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &repositoryWithoutGenreCatalog{current: persistence.LibraryMetadata{Revision: 1, Envelope: librarymetadata.Envelope{
				Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopeRelease, Desired: map[string]librarymetadata.FieldValue{},
			}}}
			_, err := NewLibraryMetadataService(repository).Update(context.Background(), librarymetadata.ScopeRelease, uuid.New(), uuid.New(), 1, libraryMetadataChanges(t, test.change))
			if (err != nil) != test.wantError {
				t.Fatalf("update error = %v, wantError %v", err, test.wantError)
			}
			wantUpdates := 1
			if test.wantError {
				wantUpdates = 0
			}
			if repository.updates != wantUpdates {
				t.Fatalf("repository updates = %d, want %d", repository.updates, wantUpdates)
			}
		})
	}
}

func TestLibraryMetadataGenreSelectionPreservesRetainedReferences(t *testing.T) {
	const (
		genreA = "11111111-1111-4111-8111-111111111111"
		genreB = "22222222-2222-4222-8222-222222222222"
		genreC = "33333333-3333-4333-8333-333333333333"
	)
	savedA := librarymetadata.GenreReference{ID: genreA, SourceName: "old source", Name: "Old Saved Name"}
	savedB := librarymetadata.GenreReference{ID: genreB, SourceName: "b source", Name: "B Saved Name"}
	catalogC := librarymetadata.GenreReference{ID: genreC, SourceName: "new source", Name: "New Canonical Name"}
	for _, test := range []struct {
		name      string
		saved     []librarymetadata.GenreReference
		selected  string
		catalog   []librarymetadata.GenreReference
		want      []librarymetadata.GenreReference
		wantReads int
		wantError bool
	}{
		{name: "retained missing id and spoofed labels", saved: []librarymetadata.GenreReference{savedA}, selected: `[{"id":"11111111-1111-4111-8111-111111111111","source_name":"spoofed","name":"spoofed"}]`, want: []librarymetadata.GenreReference{savedA}},
		{name: "remove disappeared member", saved: []librarymetadata.GenreReference{savedA, savedB}, selected: `[{"id":"11111111-1111-4111-8111-111111111111","source_name":"client","name":"client"}]`, want: []librarymetadata.GenreReference{savedA}},
		{name: "retained plus new normalized", saved: []librarymetadata.GenreReference{savedA}, selected: `[{"id":"11111111-1111-4111-8111-111111111111","source_name":"client","name":"client"},{"id":"33333333-3333-4333-8333-333333333333","source_name":"spoofed","name":"spoofed"}]`, catalog: []librarymetadata.GenreReference{catalogC}, want: []librarymetadata.GenreReference{savedA, catalogC}, wantReads: 1},
		{name: "unknown new id rejected", selected: `[{"id":"33333333-3333-4333-8333-333333333333","source_name":"client","name":"client"}]`, wantReads: 1, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			currentGenres := librarymetadata.ManualFieldValue(librarymetadata.StateValue, test.saved, true)
			repository := &fakeLibraryMetadataRepository{
				current: persistence.LibraryMetadata{Revision: 5, Envelope: librarymetadata.Envelope{
					Version: librarymetadata.EnvelopeVersion,
					Scope:   librarymetadata.ScopeRelease,
					Desired: map[string]librarymetadata.FieldValue{"genre": currentGenres},
				}},
				catalog: test.catalog,
			}
			change := `{"genre":{"state":"value","value":` + test.selected + `}}`
			result, err := NewLibraryMetadataService(repository).Update(context.Background(), librarymetadata.ScopeRelease, uuid.New(), uuid.New(), 5, libraryMetadataChanges(t, change))
			if (err != nil) != test.wantError {
				t.Fatalf("update error = %v, wantError %v", err, test.wantError)
			}
			if repository.catalogReads != test.wantReads {
				t.Fatalf("catalog reads = %d, want %d", repository.catalogReads, test.wantReads)
			}
			if test.wantError {
				if len(repository.updates) != 0 {
					t.Fatalf("repository updates = %d, want none", len(repository.updates))
				}
				return
			}
			got, ok := result.Envelope.Desired["genre"].Value.([]librarymetadata.GenreReference)
			if !ok || len(got) != len(test.want) {
				t.Fatalf("genres = %#v, want %#v", result.Envelope.Desired["genre"].Value, test.want)
			}
			for index := range got {
				if got[index] != test.want[index] {
					t.Fatalf("genres = %#v, want %#v", got, test.want)
				}
			}
		})
	}
}

func TestLibraryMetadataRetainedGenreCanBeEditedWithoutCatalog(t *testing.T) {
	saved := librarymetadata.GenreReference{ID: "11111111-1111-4111-8111-111111111111", SourceName: "saved source", Name: "Saved Name"}
	repository := &repositoryWithoutGenreCatalog{current: persistence.LibraryMetadata{
		Revision: 2,
		Envelope: librarymetadata.Envelope{
			Version: librarymetadata.EnvelopeVersion,
			Scope:   librarymetadata.ScopeRelease,
			Desired: map[string]librarymetadata.FieldValue{
				"genre": librarymetadata.ManualFieldValue(librarymetadata.StateValue, []librarymetadata.GenreReference{saved}, true),
			},
		},
	}}
	result, err := NewLibraryMetadataService(repository).Update(
		context.Background(),
		librarymetadata.ScopeRelease,
		uuid.New(),
		uuid.New(),
		2,
		libraryMetadataChanges(t, `{"genre":{"state":"value","value":[{"id":"11111111-1111-4111-8111-111111111111","source_name":"spoofed","name":"spoofed"}]}}`),
	)
	if err != nil {
		t.Fatalf("update retained genre without catalog: %v", err)
	}
	genres, ok := result.Envelope.Desired["genre"].Value.([]librarymetadata.GenreReference)
	if !ok || len(genres) != 1 || genres[0] != saved {
		t.Fatalf("saved genres = %#v, want retained reference %#v", result.Envelope.Desired["genre"].Value, saved)
	}
	if repository.updates != 1 {
		t.Fatalf("repository updates = %d, want 1", repository.updates)
	}
}

func TestLibraryMetadataUpdateMapsRepositoryOwnerErrors(t *testing.T) {
	for _, test := range []struct {
		name               string
		readErr, updateErr error
		want               error
	}{
		{name: "missing owner", readErr: persistence.ErrLibraryMetadataNotFound, want: ErrLibraryMetadataNotFound},
		{name: "stale revision", updateErr: persistence.ErrLibraryMetadataConflict, want: ErrLibraryMetadataConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeLibraryMetadataRepository{
				current: persistence.LibraryMetadata{Revision: 1, Envelope: librarymetadata.Envelope{
					Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopeRelease, Desired: map[string]librarymetadata.FieldValue{},
				}},
				readErr: test.readErr, updateErr: test.updateErr,
			}
			_, err := NewLibraryMetadataService(repository).Update(context.Background(), librarymetadata.ScopeRelease, uuid.New(), uuid.New(), 1, libraryMetadataChanges(t, `{"comment":{"state":"value","value":"rock"}}`))
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestLibraryMetadataResultExposesNoPublicationFlag(t *testing.T) {
	encoded, err := json.Marshal(LibraryMetadataResult{Envelope: librarymetadata.Envelope{Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopeRelease, Desired: map[string]librarymetadata.FieldValue{}}, Revision: 2})
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(result) != 2 || result["envelope"] == nil || result["revision"] == nil {
		t.Fatalf("result keys = %v, want only envelope and revision", result)
	}
}

func TestLibraryMetadataDisplayReadErrorsAndUpdatesRemainDesiredOnly(t *testing.T) {
	base := &fakeLibraryMetadataRepository{current: persistence.LibraryMetadata{Revision: 3, Envelope: librarymetadata.Envelope{
		Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopeArtist, Desired: map[string]librarymetadata.FieldValue{},
	}}}
	repository := &displayLibraryMetadataRepository{fakeLibraryMetadataRepository: base, displayErr: errors.New("display read failed")}
	metadata := NewLibraryMetadataService(repository)
	if _, err := metadata.ReadDisplay(context.Background(), librarymetadata.ScopeArtist, uuid.Nil, uuid.New()); err == nil {
		t.Fatal("ReadDisplay succeeded despite display repository error")
	}
	if repository.displayReads != 1 {
		t.Fatalf("display reads = %d, want 1", repository.displayReads)
	}
	if _, err := metadata.Update(context.Background(), librarymetadata.ScopeArtist, uuid.Nil, uuid.New(), 3, libraryMetadataChanges(t, `{"genre":{"state":"explicit_empty"}}`)); err != nil {
		t.Fatalf("Update used failed display reader: %v", err)
	}
	if repository.displayReads != 1 {
		t.Fatalf("Update called display reader: reads = %d", repository.displayReads)
	}
}
