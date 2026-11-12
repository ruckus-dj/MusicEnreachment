package librarymetadata

import (
	"reflect"
	"testing"
)

func testGenreReference(name string) GenreReference {
	return GenreReference{ID: "11111111-1111-4111-8111-111111111111", SourceName: name, Name: name}
}

func optionalEnvelope(scope Scope, desired map[string]FieldValue) *Envelope {
	return &Envelope{Version: EnvelopeVersion, Scope: scope, Desired: desired}
}

func optionalValue(value any) FieldValue {
	return FieldValue{State: StateValue, Value: value, Authority: AuthorityAutomatic, Provenance: Provenance{Kind: ProvenanceSourceMetadata}}
}

func TestResolveOptionalFieldPrecedenceAndExplicitEmpty(t *testing.T) {
	t.Run("absent value falls through to track", func(t *testing.T) {
		resolved, err := ResolveOptionalField("genre",
			optionalEnvelope(ScopePosition, map[string]FieldValue{"genre": {State: StateAbsent, Authority: AuthorityAutomatic, Provenance: Provenance{Kind: ProvenanceProvider}}}),
			optionalEnvelope(ScopeTrack, map[string]FieldValue{"genre": optionalValue([]GenreReference{testGenreReference("track")})}), nil,
			optionalEnvelope(ScopeRelease, map[string]FieldValue{"genre": optionalValue([]GenreReference{testGenreReference("release")})}))
		if err != nil || !resolved.Present || resolved.Scope != ScopeTrack || resolved.Field.Value.([]GenreReference)[0].Name != "track" {
			t.Fatalf("unexpected resolution: %#v, %v", resolved, err)
		}
	})

	t.Run("explicit empty blocks broader value", func(t *testing.T) {
		empty := FieldValue{State: StateExplicitEmpty, Authority: AuthorityManual, Provenance: Provenance{Kind: ProvenanceManual}}
		resolved, err := ResolveOptionalField("genre", optionalEnvelope(ScopePosition, map[string]FieldValue{"genre": empty}), nil, nil,
			optionalEnvelope(ScopeRelease, map[string]FieldValue{"genre": optionalValue([]GenreReference{testGenreReference("release")})}))
		if err != nil || !resolved.Present || resolved.Scope != ScopePosition || resolved.Field.State != StateExplicitEmpty {
			t.Fatalf("unexpected explicit-empty resolution: %#v, %v", resolved, err)
		}
	})
}

func TestResolveReleaseIdentifierDistinguishesMissingAndTerminalDesiredStates(t *testing.T) {
	provider := FieldValue{State: StateValue, Value: "Latn", Authority: AuthorityAutomatic, Provenance: Provenance{Kind: ProvenanceProvider, SourceField: "text-representation.script"}}
	manual := FieldValue{State: StateValue, Value: "Manual", Authority: AuthorityManual, Provenance: Provenance{Kind: ProvenanceManual}}
	emptyManual := FieldValue{State: StateValue, Value: "", Authority: AuthorityManual, Provenance: Provenance{Kind: ProvenanceManual}}
	absentManual := FieldValue{State: StateAbsent, Authority: AuthorityManual, Provenance: Provenance{Kind: ProvenanceManual}}
	explicitEmptyManual := FieldValue{State: StateExplicitEmpty, Authority: AuthorityManual, Provenance: Provenance{Kind: ProvenanceManual}}
	for _, test := range []struct {
		name    string
		desired *FieldValue
		want    *FieldValue
	}{
		{name: "missing desired uses provider baseline", want: &provider},
		{name: "manual value wins exactly", desired: &manual, want: &manual},
		{name: "manual empty value wins exactly", desired: &emptyManual, want: &emptyManual},
		{name: "explicit absent suppresses provider", desired: &absentManual, want: &absentManual},
		{name: "explicit empty suppresses provider", desired: &explicitEmptyManual, want: &explicitEmptyManual},
	} {
		t.Run(test.name, func(t *testing.T) {
			resolved, err := ResolveReleaseIdentifier("script", test.desired, &provider)
			if err != nil {
				t.Fatal(err)
			}
			if test.want == nil {
				if resolved.Present {
					t.Fatalf("terminal state resolved to %#v", resolved)
				}
				return
			}
			if !resolved.Present || !reflect.DeepEqual(resolved.Field, *test.want) {
				t.Fatalf("resolved = %#v, want %#v", resolved, *test.want)
			}
		})
	}
}

func TestResolveOptionalFieldAbsentFallbackAndBoolean(t *testing.T) {
	absent := FieldValue{State: StateAbsent, Authority: AuthorityAutomatic, Provenance: Provenance{Kind: ProvenanceProvider}}
	resolved, err := ResolveOptionalField("genre",
		optionalEnvelope(ScopePosition, map[string]FieldValue{"genre": absent}),
		optionalEnvelope(ScopeTrack, map[string]FieldValue{"genre": absent}),
		optionalEnvelope(ScopeRecording, map[string]FieldValue{"genre": absent}),
		optionalEnvelope(ScopeRelease, map[string]FieldValue{"genre": optionalValue([]GenreReference{testGenreReference("release")})}))
	if err != nil || !resolved.Present || resolved.Scope != ScopeRelease {
		t.Fatalf("unexpected fallback resolution: %#v, %v", resolved, err)
	}

	resolved, err = ResolveOptionalField("compilation", nil, nil, nil,
		optionalEnvelope(ScopeRelease, map[string]FieldValue{"compilation": optionalValue(false)}))
	if err != nil || !resolved.Present || resolved.Scope != ScopeRelease || resolved.Field.Value != false {
		t.Fatalf("false boolean should be present: %#v, %v", resolved, err)
	}
}

func TestResolveOptionalFieldKeepsSpecificityAndProvenance(t *testing.T) {
	position := optionalValue("specific")
	position.Authority = AuthorityAutomatic
	position.Provenance = Provenance{Kind: ProvenanceProvider, SourceField: "position-tag", Reference: "evidence"}
	track := optionalValue("manual broad")
	track.Authority = AuthorityManual
	track.Provenance = Provenance{Kind: ProvenanceManual}
	before := position
	resolved, err := ResolveOptionalField("comment", optionalEnvelope(ScopePosition, map[string]FieldValue{"comment": position}),
		optionalEnvelope(ScopeTrack, map[string]FieldValue{"comment": track}), nil, nil)
	if err != nil || !resolved.Present || resolved.Scope != ScopePosition || !reflect.DeepEqual(resolved.Field, before) {
		t.Fatalf("unexpected provenance/authority resolution: %#v, %v", resolved, err)
	}
	if position != before || track.Value != "manual broad" {
		t.Fatal("resolver mutated input envelopes")
	}
}

func TestResolveOptionalFieldEmptySlicesFallThrough(t *testing.T) {
	cases := []struct {
		field    string
		empty    any
		nonempty any
	}{
		{field: "contributors", empty: []Contributor{}, nonempty: []Contributor{{Name: "track contributor", Role: "producer"}}},
		{field: "works", empty: []Work{}, nonempty: []Work{{Name: "track work"}}},
		{field: "works", empty: [0]Work{}, nonempty: []Work{{Name: "track work"}}},
		{field: "contributors", empty: []any{}, nonempty: []any{map[string]any{"name": "decoded contributor", "role": "producer"}}},
		{field: "works", empty: []any{}, nonempty: []any{map[string]any{"name": "decoded work"}}},
	}
	for _, testCase := range cases {
		resolved, err := ResolveOptionalField(testCase.field,
			optionalEnvelope(ScopePosition, map[string]FieldValue{testCase.field: optionalValue(testCase.empty)}),
			optionalEnvelope(ScopeTrack, map[string]FieldValue{testCase.field: optionalValue(testCase.nonempty)}), nil, nil)
		if err != nil || !resolved.Present || resolved.Scope != ScopeTrack {
			t.Errorf("field %q with empty %#v should fall through to track: %#v, %v", testCase.field, testCase.empty, resolved, err)
		}
	}
}

func TestTypedNilOptionalListRejected(t *testing.T) {
	// A typed nil slice marshals to JSON null, which Envelope.Validate rejects as
	// "not a list of objects". It must stay an error rather than being normalized
	// to an empty list, so no scope can silently inherit fallback metadata.
	_, err := ResolveOptionalField("contributors",
		optionalEnvelope(ScopePosition, map[string]FieldValue{"contributors": optionalValue([]Contributor(nil))}),
		optionalEnvelope(ScopeTrack, map[string]FieldValue{"contributors": optionalValue([]Contributor{{Name: "track contributor", Role: "producer"}})}), nil, nil)
	if err == nil {
		t.Fatalf("typed nil contributor list must be rejected, not treated as an empty fallback")
	}
}

func TestResolveOptionalFieldKeepsNonemptyTypedSliceWithProvenance(t *testing.T) {
	winning := optionalValue([]Contributor{{Name: "position contributor", Role: "composer"}})
	winning.Authority = AuthorityAutomatic
	winning.Provenance = Provenance{Kind: ProvenanceProvider, SourceField: "position-credit", Reference: "evidence"}
	before := winning
	resolved, err := ResolveOptionalField("contributors",
		optionalEnvelope(ScopePosition, map[string]FieldValue{"contributors": winning}),
		optionalEnvelope(ScopeTrack, map[string]FieldValue{"contributors": optionalValue([]Contributor{})}), nil, nil)
	if err != nil || !resolved.Present || resolved.Scope != ScopePosition || !reflect.DeepEqual(resolved.Field, before) {
		t.Fatalf("unexpected nonempty typed contributor resolution: %#v, %v", resolved, err)
	}

	resolved, err = ResolveOptionalField("works",
		optionalEnvelope(ScopePosition, map[string]FieldValue{"works": optionalValue([]Work{})}),
		optionalEnvelope(ScopeTrack, map[string]FieldValue{"works": optionalValue([]Work{{Name: "track work"}})}), nil, nil)
	if err != nil || !resolved.Present || resolved.Scope != ScopeTrack {
		t.Fatalf("unexpected nonempty typed work resolution: %#v, %v", resolved, err)
	}
}

func TestResolveOptionalFieldValidatesEnvelopesAndFieldEligibility(t *testing.T) {
	badScope := optionalEnvelope(ScopeTrack, map[string]FieldValue{"genre": optionalValue([]GenreReference{testGenreReference("x")})})
	if _, err := ResolveOptionalField("genre", badScope, nil, nil, nil); err == nil {
		t.Fatal("expected envelope scope mismatch")
	}
	wrongFieldScope := optionalEnvelope(ScopeRelease, map[string]FieldValue{"disc_title": optionalValue("x")})
	if _, err := ResolveOptionalField("genre", nil, nil, nil, wrongFieldScope); err == nil {
		t.Fatal("expected invalid field scope to fail closed")
	}
	for _, name := range []string{"title", "artist_credit", "recording_mbid", "track_number"} {
		if _, err := ResolveOptionalField(name, nil, nil, nil, nil); err == nil {
			t.Errorf("expected %q to be rejected", name)
		}
	}
}
