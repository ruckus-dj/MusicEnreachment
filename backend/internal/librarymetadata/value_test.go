package librarymetadata

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeEnvelopeValidatesReleaseValuesAndPreservesEmptyFalseAndDates(t *testing.T) {
	data := `{"version":1,"scope":"release","desired":{
		"title":{"state":"value","value":"","authority":"manual","provenance":{"kind":"manual"}},
		"genre":{"state":"value","value":[],"authority":"automatic","provenance":{"kind":"provider"}},
		"compilation":{"state":"value","value":false,"authority":"automatic","provenance":{"kind":"source_metadata","source_field":"COMPILATION"}},
		"release_date":{"state":"value","value":"2024-02-29","authority":"automatic","provenance":{"kind":"provider"}},
		"comment":{"state":"explicit_empty","authority":"manual","provenance":{"kind":"manual"}},
		"website":{"state":"absent","authority":"automatic","provenance":{"kind":"source_metadata"}}
	}}`
	envelope, err := DecodeEnvelope([]byte(data))
	if err != nil {
		t.Fatalf("DecodeEnvelope() error = %v", err)
	}
	if envelope.Desired["title"].Value != "" {
		t.Fatalf("empty string value was not preserved: %#v", envelope.Desired["title"])
	}
	if envelope.Desired["compilation"].Value != false {
		t.Fatalf("false boolean value was not preserved: %#v", envelope.Desired["compilation"])
	}
	if envelope.Desired["genre"].Value == nil {
		t.Fatal("empty list value was not preserved")
	}
	if envelope.Desired["release_date"].Value != "2024-02-29" {
		t.Fatalf("release date value was not preserved: %#v", envelope.Desired["release_date"])
	}
	if envelope.Desired["comment"].Value != nil || envelope.Desired["comment"].State != StateExplicitEmpty {
		t.Fatalf("explicit empty was not represented distinctly: %#v", envelope.Desired["comment"])
	}
	if envelope.Desired["website"].State != StateAbsent {
		t.Fatalf("absent state was not preserved: %#v", envelope.Desired["website"])
	}
}

func TestDecodeEnvelopeValidatesPositionValuesAndPreservesEmptyAndFalse(t *testing.T) {
	data := `{"version":1,"scope":"position","desired":{
		"title":{"state":"value","value":"","authority":"manual","provenance":{"kind":"manual"}},
		"genre":{"state":"value","value":[],"authority":"automatic","provenance":{"kind":"provider"}},
		"bpm":{"state":"value","value":120.5,"authority":"automatic","provenance":{"kind":"provider"}},
		"track_number":{"state":"value","value":1,"authority":"manual","provenance":{"kind":"manual"}},
		"comment":{"state":"explicit_empty","authority":"manual","provenance":{"kind":"manual"}},
		"website":{"state":"absent","authority":"automatic","provenance":{"kind":"source_metadata"}}
	}}`
	envelope, err := DecodeEnvelope([]byte(data))
	if err != nil {
		t.Fatalf("DecodeEnvelope() error = %v", err)
	}
	if envelope.Desired["title"].Value != "" {
		t.Fatalf("empty string value was not preserved: %#v", envelope.Desired["title"])
	}
	if envelope.Desired["genre"].Value == nil {
		t.Fatal("empty list value was not preserved")
	}
	if envelope.Desired["track_number"].Value == nil {
		t.Fatal("position integer value was not preserved")
	}
	if envelope.Desired["comment"].Value != nil || envelope.Desired["comment"].State != StateExplicitEmpty {
		t.Fatalf("explicit empty was not represented distinctly: %#v", envelope.Desired["comment"])
	}
	if envelope.Desired["website"].State != StateAbsent {
		t.Fatalf("absent state was not preserved: %#v", envelope.Desired["website"])
	}
}

func TestDecodeEnvelopeValidatesIdentityContributorWorkAndMovementValues(t *testing.T) {
	data := `{"version":1,"scope":"position","desired":{
		"artist_credit":{"state":"value","value":[{"artist_id":"123e4567-e89b-12d3-a456-426614174000","artist_name":"Artist","credit_name":"A","join_phrase":" & ","mbid":"123e4567-e89b-12d3-a456-426614174001"},{"artist_id":"123e4567-e89b-12d3-a456-426614174000","artist_name":"Artist","credit_name":"Artist","join_phrase":""}],"authority":"automatic","provenance":{"kind":"provider"}},
		"recording_mbid":{"state":"value","value":"123e4567-e89b-12d3-a456-426614174002","authority":"automatic","provenance":{"kind":"provider"}},
		"contributors":{"state":"value","value":[{"name":"Composer","role":"composer"},{"name":"Performer","role":"vocals","artist_id":"123e4567-e89b-12d3-a456-426614174000"}],"authority":"automatic","provenance":{"kind":"source_metadata"}},
		"works":{"state":"value","value":[{"name":"Work","mbid":"123e4567-e89b-12d3-a456-426614174003"}],"authority":"automatic","provenance":{"kind":"provider"}},
		"movement":{"state":"value","value":{"name":"Allegro","number":1,"count":3},"authority":"manual","provenance":{"kind":"manual"}}
	}}`
	if _, err := DecodeEnvelope([]byte(data)); err != nil {
		t.Fatalf("DecodeEnvelope() error = %v", err)
	}
}

func TestDecodeEnvelopeRejectsUnknownFieldsAndScopes(t *testing.T) {
	tests := []struct {
		name string
		json string
		want string
	}{
		{"unknown field", `{"version":1,"scope":"release","desired":{"invented":{"state":"absent","authority":"automatic","provenance":{"kind":"provider"}}}}`, "unknown metadata field"},
		{"unsupported scope", `{"version":1,"scope":"album","desired":{}}`, "unknown metadata scope"},
		{"field outside scope", `{"version":1,"scope":"release","desired":{"track_number":{"state":"absent","authority":"automatic","provenance":{"kind":"provider"}}}}`, "not valid for scope"},
		{"unknown envelope property", `{"version":1,"scope":"release","desired":{},"pending":{}}`, "decode local metadata"},
		{"unknown field value property", `{"version":1,"scope":"release","desired":{"title":{"state":"absent","authority":"automatic","provenance":{"kind":"provider"},"pending":true}}}`, "decode local metadata"},
		{"unknown provenance property", `{"version":1,"scope":"release","desired":{"title":{"state":"absent","authority":"automatic","provenance":{"kind":"provider","guessed":true}}}}`, "decode local metadata"},
		{"unknown nested value property", `{"version":1,"scope":"position","desired":{"artist_credit":{"state":"value","value":[{"artist_id":"123e4567-e89b-12d3-a456-426614174000","artist_name":"Name","credit_name":"Name","join_phrase":"","guessed":true}],"authority":"automatic","provenance":{"kind":"provider"}}}}`, "unknown value field"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeEnvelope([]byte(test.json))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("DecodeEnvelope() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestFieldValueRejectsInvalidStatesAuthorityAndTypes(t *testing.T) {
	tests := []struct {
		name  string
		field string
		json  string
		want  string
	}{
		{"value without payload", "title", `{"state":"value","authority":"manual","provenance":{"kind":"manual"}}`, "requires a value"},
		{"value null payload", "title", `{"state":"value","value":null,"authority":"manual","provenance":{"kind":"manual"}}`, "requires a value"},
		{"absent with payload", "title", `{"state":"absent","value":"x","authority":"manual","provenance":{"kind":"manual"}}`, "must not include a value"},
		{"absent null payload", "title", `{"state":"absent","value":null,"authority":"manual","provenance":{"kind":"manual"}}`, "must not include a value"},
		{"explicit empty payload", "title", `{"state":"explicit_empty","value":"x","authority":"manual","provenance":{"kind":"manual"}}`, "must not include a value"},
		{"explicit empty null payload", "title", `{"state":"explicit_empty","value":null,"authority":"manual","provenance":{"kind":"manual"}}`, "must not include a value"},
		{"bad authority", "title", `{"state":"absent","authority":"maybe","provenance":{"kind":"provider"}}`, "unknown metadata authority"},
		{"authority provenance mismatch", "title", `{"state":"absent","authority":"manual","provenance":{"kind":"provider"}}`, "requires automatic authority"},
		{"bad string type", "title", `{"state":"value","value":12,"authority":"manual","provenance":{"kind":"manual"}}`, "must be a string"},
		{"bad list type", "genre", `{"state":"value","value":["ok",false],"authority":"automatic","provenance":{"kind":"provider"}}`, "list entries must be strings"},
		{"bad bpm", "bpm", `{"state":"value","value":0,"authority":"automatic","provenance":{"kind":"provider"}}`, "positive finite number"},
		{"bad integer", "track_number", `{"state":"value","value":1.5,"authority":"manual","provenance":{"kind":"manual"}}`, "positive integer"},
		{"bad UUID", "artist_credit", `{"state":"value","value":[{"artist_id":"not-a-uuid","artist_name":"A","credit_name":"A","join_phrase":""}],"authority":"automatic","provenance":{"kind":"provider"}}`, "artist_id must be a uuid"},
		{"bad movement count", "movement", `{"state":"value","value":{"name":"I","number":1,"count":0},"authority":"automatic","provenance":{"kind":"provider"}}`, "movement count must be a positive integer"},
		{"bad advisory", "content_advisory", `{"state":"value","value":"maybe","authority":"automatic","provenance":{"kind":"provider"}}`, "value must be explicit or clean"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var fieldValue FieldValue
			decoder := json.NewDecoder(strings.NewReader(test.json))
			if err := decoder.Decode(&fieldValue); err != nil {
				t.Fatalf("decode test value: %v", err)
			}
			definition, _ := Field(test.field)
			if err := fieldValue.validate(definition); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validate() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestFieldValueDirectConstructionKeepsPresenceConsistent(t *testing.T) {
	tests := []struct {
		name       string
		field      string
		fieldValue FieldValue
	}{
		{"absent", "title", FieldValue{State: StateAbsent, Authority: AuthorityAutomatic, Provenance: Provenance{Kind: ProvenanceProvider}}},
		{"explicit empty", "title", FieldValue{State: StateExplicitEmpty, Authority: AuthorityManual, Provenance: Provenance{Kind: ProvenanceManual}}},
		{"false value", "compilation", FieldValue{State: StateValue, Value: false, Authority: AuthorityAutomatic, Provenance: Provenance{Kind: ProvenanceProvider}}},
		{"empty string value", "title", FieldValue{State: StateValue, Value: "", Authority: AuthorityManual, Provenance: Provenance{Kind: ProvenanceManual}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			definition, ok := Field(test.field)
			if !ok {
				t.Fatalf("Field(%q) was not found", test.field)
			}
			if err := test.fieldValue.validate(definition); err != nil {
				t.Fatalf("validate() error = %v", err)
			}
		})
	}
}

func TestPartialDatesValidateCalendar(t *testing.T) {
	for _, value := range []string{"2024", "2024-02", "2024-02-29", "2000-02-29", "2026-10-17"} {
		if !validPartialDate(value) {
			t.Errorf("validPartialDate(%q) = false", value)
		}
	}
	for _, value := range []string{"", "24", "2024-2", "2023-02-29", "2024-13", "2024-00-01", "2024-04-31", "2024-01-01T00:00:00Z"} {
		if validPartialDate(value) {
			t.Errorf("validPartialDate(%q) = true", value)
		}
	}
}

func TestFieldRegistryIsStableAndProtectsCanonicalFields(t *testing.T) {
	fields := Fields()
	if len(fields) == 0 {
		t.Fatal("Fields() returned no definitions")
	}
	for index := range fields {
		if index > 0 && fields[index-1].Name >= fields[index].Name {
			t.Fatalf("registry is not in canonical order at %q then %q", fields[index-1].Name, fields[index].Name)
		}
	}
	for _, name := range []string{"title", "artist_credit"} {
		definition, ok := Field(name)
		if !ok || !definition.Canonical {
			t.Errorf("Field(%q) = %#v, %v; want canonical field", name, definition, ok)
		}
	}
	fields[0].Scopes[0] = "corrupted"
	definition, _ := Field("title")
	if !containsScope(definition.Scopes, ScopeRelease) {
		t.Fatal("registry definitions were mutated through returned slice")
	}
}
