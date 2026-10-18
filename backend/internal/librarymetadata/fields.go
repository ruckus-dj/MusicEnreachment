package librarymetadata

import (
	"fmt"
	"sort"
)

// Scope identifies the local entity that owns a metadata value.
type Scope string

const (
	ScopeRelease   Scope = "release"
	ScopeRecording Scope = "recording"
	ScopeTrack     Scope = "track"
	ScopePosition  Scope = "position"
)

// ValueType describes the JSON representation accepted for a field.
type ValueType string

const (
	TypeString       ValueType = "string"
	TypeStringList   ValueType = "string_list"
	TypeArtistCredit ValueType = "artist_credit_list"
	TypeContributors ValueType = "contributor_list"
	TypeWork         ValueType = "work_list"
	TypeMovement     ValueType = "movement"
	TypeInteger      ValueType = "positive_integer"
	TypeNumber       ValueType = "positive_number"
	TypeBoolean      ValueType = "boolean"
	TypeDate         ValueType = "partial_date"
	TypeUUID         ValueType = "uuid"
	TypeUUIDList     ValueType = "uuid_list"
	TypeAdvisory     ValueType = "content_advisory"
)

// FieldDefinition is the stable registry contract for one local metadata field.
type FieldDefinition struct {
	Name   string    `json:"name"`
	Type   ValueType `json:"type"`
	Scopes []Scope   `json:"scopes"`
	// Canonical means the value is held in the existing local model rather than
	// duplicated in local_metadata.desired.
	Canonical bool `json:"canonical,omitempty"`
}

var fieldDefinitions = []FieldDefinition{
	{Name: "title", Type: TypeString, Scopes: []Scope{ScopeRelease, ScopeRecording, ScopeTrack, ScopePosition}, Canonical: true},
	{Name: "sort_title", Type: TypeString, Scopes: []Scope{ScopeRelease, ScopeRecording, ScopeTrack, ScopePosition}},
	{Name: "artist_credit", Type: TypeArtistCredit, Scopes: []Scope{ScopeRelease, ScopeRecording, ScopeTrack, ScopePosition}, Canonical: true},
	{Name: "artists", Type: TypeStringList, Scopes: []Scope{ScopeRelease, ScopeRecording, ScopeTrack, ScopePosition}},
	{Name: "artist_sort_names", Type: TypeStringList, Scopes: []Scope{ScopeRelease, ScopeRecording, ScopeTrack, ScopePosition}},
	{Name: "album_artist_sort_names", Type: TypeStringList, Scopes: []Scope{ScopeRelease}},
	{Name: "release_date", Type: TypeDate, Scopes: []Scope{ScopeRelease}},
	{Name: "original_release_date", Type: TypeDate, Scopes: []Scope{ScopeRelease}},
	{Name: "release_primary_type", Type: TypeString, Scopes: []Scope{ScopeRelease}},
	{Name: "release_secondary_types", Type: TypeStringList, Scopes: []Scope{ScopeRelease}},
	{Name: "release_version", Type: TypeString, Scopes: []Scope{ScopeRelease}},
	{Name: "release_mbid", Type: TypeUUID, Scopes: []Scope{ScopeRelease}},
	{Name: "release_group_mbid", Type: TypeUUID, Scopes: []Scope{ScopeRelease}},
	{Name: "recording_mbid", Type: TypeUUID, Scopes: []Scope{ScopeRecording, ScopeTrack, ScopePosition}},
	{Name: "release_track_mbid", Type: TypeUUID, Scopes: []Scope{ScopePosition}},
	{Name: "artist_mbid", Type: TypeUUIDList, Scopes: []Scope{ScopeRelease, ScopeRecording, ScopeTrack, ScopePosition}},
	{Name: "album_artist_mbid", Type: TypeUUIDList, Scopes: []Scope{ScopeRelease}},
	{Name: "isrc", Type: TypeStringList, Scopes: []Scope{ScopeRecording, ScopeTrack, ScopePosition}},
	{Name: "label", Type: TypeStringList, Scopes: []Scope{ScopeRelease}},
	{Name: "catalog_number", Type: TypeString, Scopes: []Scope{ScopeRelease}},
	{Name: "barcode", Type: TypeString, Scopes: []Scope{ScopeRelease}},
	{Name: "release_country", Type: TypeString, Scopes: []Scope{ScopeRelease}},
	{Name: "release_status", Type: TypeString, Scopes: []Scope{ScopeRelease}},
	{Name: "contributors", Type: TypeContributors, Scopes: []Scope{ScopeRecording, ScopeTrack, ScopePosition}},
	{Name: "works", Type: TypeWork, Scopes: []Scope{ScopeRecording, ScopeTrack, ScopePosition}},
	{Name: "movement", Type: TypeMovement, Scopes: []Scope{ScopeRecording, ScopeTrack, ScopePosition}},
	{Name: "compilation", Type: TypeBoolean, Scopes: []Scope{ScopeRelease}},
	{Name: "content_advisory", Type: TypeAdvisory, Scopes: []Scope{ScopeRelease, ScopeRecording, ScopeTrack, ScopePosition}},
	{Name: "genre", Type: TypeStringList, Scopes: []Scope{ScopeRelease, ScopeRecording, ScopeTrack, ScopePosition}},
	{Name: "mood", Type: TypeStringList, Scopes: []Scope{ScopeRelease, ScopeRecording, ScopeTrack, ScopePosition}},
	{Name: "grouping", Type: TypeString, Scopes: []Scope{ScopeRelease, ScopeRecording, ScopeTrack, ScopePosition}},
	{Name: "comment", Type: TypeString, Scopes: []Scope{ScopeRelease, ScopeRecording, ScopeTrack, ScopePosition}},
	{Name: "bpm", Type: TypeNumber, Scopes: []Scope{ScopeRecording, ScopeTrack, ScopePosition}},
	{Name: "copyright", Type: TypeString, Scopes: []Scope{ScopeRelease, ScopeRecording, ScopeTrack, ScopePosition}},
	{Name: "license", Type: TypeString, Scopes: []Scope{ScopeRelease, ScopeRecording, ScopeTrack, ScopePosition}},
	{Name: "website", Type: TypeString, Scopes: []Scope{ScopeRelease, ScopeRecording, ScopeTrack, ScopePosition}},
	{Name: "language", Type: TypeString, Scopes: []Scope{ScopeRecording, ScopeTrack, ScopePosition}},
	{Name: "media", Type: TypeStringList, Scopes: []Scope{ScopeRelease}},
	{Name: "disc_title", Type: TypeString, Scopes: []Scope{ScopePosition}},
	{Name: "track_number", Type: TypeInteger, Scopes: []Scope{ScopePosition}},
	{Name: "track_total", Type: TypeInteger, Scopes: []Scope{ScopePosition}},
	{Name: "disc_number", Type: TypeInteger, Scopes: []Scope{ScopePosition}},
	{Name: "disc_total", Type: TypeInteger, Scopes: []Scope{ScopePosition}},
	{Name: "synced_lyrics", Type: TypeString, Scopes: []Scope{ScopePosition}},
}

var fieldByName = func() map[string]FieldDefinition {
	result := make(map[string]FieldDefinition, len(fieldDefinitions))
	for _, definition := range fieldDefinitions {
		result[definition.Name] = definition
	}
	return result
}()

// Fields returns the stable registry in its canonical order.
func Fields() []FieldDefinition {
	result := make([]FieldDefinition, len(fieldDefinitions))
	for index, definition := range fieldDefinitions {
		result[index] = definition
		result[index].Scopes = append([]Scope(nil), definition.Scopes...)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Name < result[right].Name })
	return result
}

// Field returns a copy of the registered definition with the given name.
func Field(name string) (FieldDefinition, bool) {
	definition, ok := fieldByName[name]
	if !ok {
		return FieldDefinition{}, false
	}
	definition.Scopes = append([]Scope(nil), definition.Scopes...)
	return definition, true
}

func validScope(scope Scope) bool {
	switch scope {
	case ScopeRelease, ScopeRecording, ScopeTrack, ScopePosition:
		return true
	default:
		return false
	}
}

func validateScope(scope Scope) error {
	if !validScope(scope) {
		return fmt.Errorf("unknown metadata scope %q", scope)
	}
	return nil
}
