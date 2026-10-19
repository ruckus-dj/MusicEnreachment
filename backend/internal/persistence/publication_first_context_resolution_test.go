package persistence

import (
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

// cloneTagDesired returns a detached copy of a desired-values map. Resolution
// mutates the projection envelopes in place (deleting and re-placing the winning
// value), so a test must snapshot the raw owner envelopes before calling it to
// prove the audit snapshot stays untouched.
func cloneTagDesired(desired map[string]librarymetadata.FieldValue) map[string]librarymetadata.FieldValue {
	clone := make(map[string]librarymetadata.FieldValue, len(desired))
	for name, value := range desired {
		clone[name] = value
	}
	return clone
}

func resolutionEnvelope(scope librarymetadata.Scope, desired map[string]librarymetadata.FieldValue) librarymetadata.Envelope {
	if desired == nil {
		desired = map[string]librarymetadata.FieldValue{}
	}
	return librarymetadata.Envelope{Version: librarymetadata.EnvelopeVersion, Scope: scope, Desired: desired}
}

func resolutionGenreValue(values ...string) librarymetadata.FieldValue {
	list := make([]any, 0, len(values))
	for _, value := range values {
		list = append(list, value)
	}
	return librarymetadata.FieldValue{
		State:      librarymetadata.StateValue,
		Value:      list,
		Authority:  librarymetadata.AuthorityManual,
		Provenance: librarymetadata.Provenance{Kind: librarymetadata.ProvenanceManual},
	}
}

func resolutionExplicitEmpty() librarymetadata.FieldValue {
	return librarymetadata.FieldValue{
		State:      librarymetadata.StateExplicitEmpty,
		Authority:  librarymetadata.AuthorityManual,
		Provenance: librarymetadata.Provenance{Kind: librarymetadata.ProvenanceManual},
	}
}

func resolutionCredits() []librarymetadata.ArtistCredit {
	return []librarymetadata.ArtistCredit{{ArtistName: "Arist", CreditName: "Arist", JoinPhrase: ""}}
}

// resolutionContext wires a LibraryFirstPublicationContext the way
// ReadLibraryFirstPublicationContext does: the raw owner envelopes
// (Metadata.*.Envelope) and the cloned projection envelopes (Tags.*.Desired)
// are distinct objects with equal contents, and position is projected through a
// clone (cloneFirstPublicationEnvelope) rather than the owner envelope.
func resolutionContext(releaseDesired, positionDesired map[string]librarymetadata.FieldValue) *LibraryFirstPublicationContext {
	metadataRelease := resolutionEnvelope(librarymetadata.ScopeRelease, cloneTagDesired(releaseDesired))
	projectionRelease := resolutionEnvelope(librarymetadata.ScopeRelease, cloneTagDesired(releaseDesired))
	metadataPosition := resolutionEnvelope(librarymetadata.ScopePosition, cloneTagDesired(positionDesired))
	projectionPosition := resolutionEnvelope(librarymetadata.ScopePosition, cloneTagDesired(positionDesired))
	return &LibraryFirstPublicationContext{
		Metadata: FirstPublicationMetadata{
			Release:   LibraryMetadata{Envelope: metadataRelease, Revision: 1},
			Track:     LibraryMetadata{Envelope: resolutionEnvelope(librarymetadata.ScopeTrack, nil), Revision: 1},
			Position:  LibraryMetadata{Envelope: metadataPosition, Revision: 1},
			Recording: LibraryMetadata{},
		},
		Tags: publication.TagProjectionInput{
			Release:  publication.ReleaseTags{Title: "Album", Artist: resolutionCredits(), Desired: &projectionRelease},
			Position: publication.PositionTags{Title: "Track", Artist: resolutionCredits(), Desired: &projectionPosition},
		},
	}
}

// TestResolveFirstPublicationOptionalFieldsEmptyPositionGenreFallsBackToRelease
// pins the priority rule: an empty value in a higher-priority scope loses
// resolution, so the release genre wins and the raw owner envelopes keep the
// unresolved empty list unchanged.
func TestResolveFirstPublicationOptionalFieldsEmptyPositionGenreFallsBackToRelease(t *testing.T) {
	releaseDesired := map[string]librarymetadata.FieldValue{"genre": resolutionGenreValue("Rock")}
	positionDesired := map[string]librarymetadata.FieldValue{"genre": resolutionGenreValue()}
	result := resolutionContext(releaseDesired, positionDesired)

	rawRelease := cloneTagDesired(result.Metadata.Release.Envelope.Desired)
	rawPosition := cloneTagDesired(result.Metadata.Position.Envelope.Desired)

	if err := resolveFirstPublicationOptionalFields(result); err != nil {
		t.Fatalf("resolve first publication optional fields: %v", err)
	}

	resolved, ok := result.ResolvedOptionalFields["genre"]
	if !ok || !resolved.Present || resolved.Scope != librarymetadata.ScopeRelease {
		t.Fatalf("resolved genre = %+v, present=%v", resolved, ok)
	}
	if _, ok := result.Tags.Position.Desired.Desired["genre"]; ok {
		t.Fatal("position projection retained an unresolved empty genre")
	}
	releaseField, ok := result.Tags.Release.Desired.Desired["genre"]
	if !ok || releaseField.State != librarymetadata.StateValue {
		t.Fatalf("release projection genre = %#v, present=%v", releaseField, ok)
	}

	assertDesiredUnchanged(t, "release", rawRelease, result.Metadata.Release.Envelope.Desired)
	assertDesiredUnchanged(t, "position", rawPosition, result.Metadata.Position.Envelope.Desired)

	projection, err := publication.ProjectTags(result.Tags)
	if err != nil {
		t.Fatalf("project tags: %v", err)
	}
	if got := projection.Tags["GENRE"]; len(got) != 1 || got[0] != "Rock" {
		t.Fatalf("GENRE = %#v, want [Rock]", got)
	}
}

// TestResolveFirstPublicationOptionalFieldsExplicitEmptyPositionGenreClearsRelease
// pins the explicit-empty rule: an explicit empty in a higher-priority scope
// wins outright, clears GENRE, and must not fall back to the release value.
func TestResolveFirstPublicationOptionalFieldsExplicitEmptyPositionGenreClearsRelease(t *testing.T) {
	releaseDesired := map[string]librarymetadata.FieldValue{"genre": resolutionGenreValue("Rock")}
	positionDesired := map[string]librarymetadata.FieldValue{"genre": resolutionExplicitEmpty()}
	result := resolutionContext(releaseDesired, positionDesired)

	rawRelease := cloneTagDesired(result.Metadata.Release.Envelope.Desired)
	rawPosition := cloneTagDesired(result.Metadata.Position.Envelope.Desired)

	if err := resolveFirstPublicationOptionalFields(result); err != nil {
		t.Fatalf("resolve first publication optional fields: %v", err)
	}

	resolved, ok := result.ResolvedOptionalFields["genre"]
	if !ok || !resolved.Present || resolved.Scope != librarymetadata.ScopePosition || resolved.Field.State != librarymetadata.StateExplicitEmpty {
		t.Fatalf("resolved genre = %+v, present=%v", resolved, ok)
	}
	if _, ok := result.Tags.Release.Desired.Desired["genre"]; ok {
		t.Fatal("release projection retained a genre that the explicit empty overrides")
	}
	positionField, ok := result.Tags.Position.Desired.Desired["genre"]
	if !ok || positionField.State != librarymetadata.StateExplicitEmpty {
		t.Fatalf("position projection genre = %#v, present=%v", positionField, ok)
	}

	assertDesiredUnchanged(t, "release", rawRelease, result.Metadata.Release.Envelope.Desired)
	assertDesiredUnchanged(t, "position", rawPosition, result.Metadata.Position.Envelope.Desired)

	projection, err := publication.ProjectTags(result.Tags)
	if err != nil {
		t.Fatalf("project tags: %v", err)
	}
	got, ok := projection.Tags["GENRE"]
	if !ok || len(got) != 0 {
		t.Fatalf("GENRE = %#v, present=%v, want present and empty", got, ok)
	}
	if status := projection.Fields["position.genre"]; status.Status != "explicit_empty" {
		t.Fatalf("position.genre status = %+v, want explicit_empty", status)
	}
}

func assertDesiredUnchanged(t *testing.T, scope string, before, after map[string]librarymetadata.FieldValue) {
	t.Helper()
	if len(before) != len(after) {
		t.Fatalf("%s raw metadata size changed: before=%d after=%d", scope, len(before), len(after))
	}
	for name, expected := range before {
		actual, ok := after[name]
		if !ok {
			t.Fatalf("%s raw metadata dropped %q", scope, name)
		}
		if actual.State != expected.State || valueLength(actual.Value) != valueLength(expected.Value) {
			t.Fatalf("%s raw metadata %q changed: before=%#v after=%#v", scope, name, expected, actual)
		}
	}
}

func valueLength(value any) int {
	items, ok := value.([]any)
	if !ok {
		return -1
	}
	return len(items)
}
