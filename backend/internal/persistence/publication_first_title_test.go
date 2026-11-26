package persistence

import (
	"encoding/json"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
)

func TestFirstPublicationPositionTitlePrefersExactOverride(t *testing.T) {
	metadata := FirstPublicationMetadata{
		Position: LibraryMetadata{
			Envelope: resolutionEnvelope(librarymetadata.ScopePosition, nil),
		},
		Recording: LibraryMetadata{
			Envelope: resolutionEnvelope(librarymetadata.ScopeRecording, map[string]librarymetadata.FieldValue{
				"title": librarymetadata.ManualFieldValue(librarymetadata.StateValue, "Saved recording", true),
			}),
		},
	}
	fallback := json.RawMessage(`{"automatic_matching":{"provider_fields_reference":{"entity_kind":"recording","fields":["title"]}}}`)
	got := resolveFirstPublicationPositionTitle(metadata, fallback, "Copied title", "Provider recording")
	if got.Desired == nil || got.Desired.Value != "Saved recording" {
		t.Fatalf("recording fallback = %+v", got.Desired)
	}

	providerOverride := json.RawMessage(`{"automatic_matching":{"provider_fields_reference":{"entity_kind":"release","fields":["normalized_tracks"]}}}`)
	got = resolveFirstPublicationPositionTitle(metadata, providerOverride, "Position from release", "Provider recording")
	if got.Baseline != "Position from release" || got.Desired != nil {
		t.Fatalf("provider position title = %+v", got)
	}

	metadata.Position.Envelope.Desired["title"] = librarymetadata.ManualFieldValue(librarymetadata.StateExplicitEmpty, nil, false)
	got = resolveFirstPublicationPositionTitle(metadata, providerOverride, "Position from release", "Provider recording")
	if got.Desired == nil || got.Desired.State != librarymetadata.StateExplicitEmpty || got.Baseline != "Position from release" {
		t.Fatalf("explicit clear = %+v", got)
	}
}
