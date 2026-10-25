package persistence

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
)

func TestCurrentPublicationProviderTagProtection(t *testing.T) {
	tests := []struct {
		name       string
		release    string
		track      string
		recording  string
		position   string
		override   string
		wantKeys   []string
		wantAbsent []string
	}{
		{
			name:     "manual provider provenance protects only its field footprint",
			release:  `{"local_metadata":{"version":1,"scope":"release","desired":{"title":{"state":"value","value":"local","authority":"manual","provenance":{"kind":"provider"}},"release_date":{"state":"value","value":"2020","authority":"automatic","provenance":{"kind":"provider"}}}}}`,
			wantKeys: []string{"ALBUM"},
		},
		{
			name:       "position manual absent protects its footprint",
			position:   `{"local_metadata":{"version":1,"scope":"position","desired":{"synced_lyrics":{"state":"absent","authority":"manual","provenance":{"kind":"manual"}}}}}`,
			wantAbsent: []string{"synced_lyrics"},
		},
		{
			name:       "position manual absent title protects title footprint",
			position:   `{"local_metadata":{"version":1,"scope":"position","desired":{"title":{"state":"absent","authority":"manual","provenance":{"kind":"manual"}}}}}`,
			wantKeys:   []string{"TITLE"},
			wantAbsent: []string{"title"},
		},
		{
			name:     "position manual title protects position footprint",
			position: `{"local_metadata":{"version":1,"scope":"position","desired":{"title":{"state":"value","value":"local","authority":"manual","provenance":{"kind":"manual"}}}}}`,
			wantKeys: []string{"TITLE"},
		},
		{
			name:     "position explicit empty manual title protects title",
			position: `{"local_metadata":{"version":1,"scope":"position","desired":{"title":{"state":"explicit_empty","authority":"manual","provenance":{"kind":"manual"}}}}}`,
			wantKeys: []string{"TITLE"},
		},
		{
			name:     "position manual credit protects credit footprint",
			position: `{"local_metadata":{"version":1,"scope":"position","desired":{"artist_credit":{"state":"value","value":[],"authority":"manual","provenance":{"kind":"manual"}}}}}`,
			wantKeys: []string{"ARTIST", "ARTISTS", "MUSICBRAINZ_ARTISTID"},
		},
		{
			name:     "empty title override protected by presence and artists fan out",
			override: `{"title_override":"","artist_overrides":[]}`,
			wantKeys: []string{"ARTIST", "ARTISTS", "MUSICBRAINZ_ARTISTID", "TITLE"},
		},
		{
			name:       "manual absent returned and protects matching provider key",
			track:      `{"local_metadata":{"version":1,"scope":"track","desired":{"title":{"state":"absent","authority":"manual","provenance":{"kind":"manual"}}}}}`,
			wantKeys:   []string{"TITLE"},
			wantAbsent: []string{"title"},
		},
		{
			name:      "recording manual metadata protects even when unbound",
			recording: `{"local_metadata":{"version":1,"scope":"recording","desired":{"title":{"state":"explicit_empty","authority":"manual","provenance":{"kind":"source_metadata"}}}}}`,
			wantKeys:  []string{"TITLE"},
		},
		{
			name:     "manual association alone does not lock tags",
			wantKeys: []string{},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			context := &PublicationProviderTagContext{TrackOwner: PublicationProviderTagOwner{BindingMethod: "manual"}}
			context.ReleaseOwner.Metadata = json.RawMessage(test.release)
			context.TrackOwner.Metadata = json.RawMessage(test.track)
			context.RecordingOwner.Metadata = json.RawMessage(test.recording)
			context.PositionOwner.Owner.Metadata = json.RawMessage(test.position)
			if test.override != "" {
				context.TrackOwner.ManualOverridePresent = true
				context.TrackOwner.ManualOverride = json.RawMessage(test.override)
			}
			got, err := BuildPublicationProviderTagProtection(context)
			if err != nil {
				t.Fatalf("BuildPublicationProviderTagProtection() error = %v", err)
			}
			if got.Keys == nil || !slices.Equal(got.Keys, test.wantKeys) {
				t.Errorf("Keys = %v, want %v", got.Keys, test.wantKeys)
			}
			if got.ManualAbsentFields == nil || !slices.Equal(got.ManualAbsentFields, test.wantAbsent) {
				t.Errorf("ManualAbsentFields = %v, want %v", got.ManualAbsentFields, test.wantAbsent)
			}
		})
	}
}

func TestCurrentPublicationProviderTagProtectionAliasesAndMissingEnvelope(t *testing.T) {
	context := &PublicationProviderTagContext{
		ReleaseOwner:   PublicationProviderTagOwner{Metadata: json.RawMessage(`{"unrelated":true}`)},
		TrackOwner:     PublicationProviderTagOwner{Metadata: json.RawMessage(`{}`), ManualOverridePresent: true, ManualOverride: json.RawMessage(`{"artist_overrides":[]}`)},
		RecordingOwner: PublicationProviderTagOwner{Metadata: json.RawMessage(`{}`)},
	}
	got, err := BuildPublicationProviderTagProtection(context)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ARTIST", "ARTISTS", "MUSICBRAINZ_ARTISTID"}
	if !reflect.DeepEqual(got.Keys, want) || len(got.ManualAbsentFields) != 0 {
		t.Fatalf("protection = %+v, want keys %v and no absent fields", got, want)
	}
}
