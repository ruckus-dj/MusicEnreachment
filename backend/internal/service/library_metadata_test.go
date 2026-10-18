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
	current   persistence.LibraryMetadata
	readErr   error
	updateErr error
	updates   []libraryMetadataUpdateCall
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
		{name: "empty genre list", field: "genre", changes: `{"genre":{"state":"value","value":[]}}`, state: librarymetadata.StateValue, value: "[]"},
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
		{name: "forged authority", changes: `{"genre":{"state":"value","value":["rock"],"authority":"automatic"}}`},
		{name: "forged provenance", changes: `{"genre":{"state":"value","value":["rock"],"provenance":{"kind":"provider"}}}`},
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
	result, err := NewLibraryMetadataService(repository).Update(context.Background(), librarymetadata.ScopeRelease, releaseID, ownerID, 7, libraryMetadataChanges(t, `{"genre":{"state":"value","value":["rock"]}}`))
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
	if result.Revision != 8 {
		t.Fatalf("revision = %d, want 8", result.Revision)
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
			_, err := NewLibraryMetadataService(repository).Update(context.Background(), librarymetadata.ScopeRelease, uuid.New(), uuid.New(), 1, libraryMetadataChanges(t, `{"genre":{"state":"value","value":["rock"]}}`))
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
