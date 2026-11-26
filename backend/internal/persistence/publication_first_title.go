package persistence

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
)

// FirstPublicationPositionTitle is applied only to a new publication plan.
// The template token, source evidence and owner envelopes remain unchanged.
type FirstPublicationPositionTitle struct {
	Baseline string
	Desired  *librarymetadata.FieldValue
}

const positionTitleOriginOverride = "position_override"

func resolveFirstPublicationPositionTitle(
	metadata FirstPublicationMetadata, rawTrackMetadata json.RawMessage, trackTitle, recordingTitle string,
) *FirstPublicationPositionTitle {
	result := &FirstPublicationPositionTitle{Baseline: trackTitle}
	if strings.TrimSpace(result.Baseline) == "" && strings.TrimSpace(recordingTitle) != "" {
		result.Baseline = recordingTitle
	}
	for _, envelope := range []librarymetadata.Envelope{metadata.Position.Envelope, metadata.Track.Envelope} {
		if title := desiredPublicationTitle(envelope); title != nil {
			result.Desired = title
			return result
		}
	}

	manual, positionOverride := materializedPositionTitle(rawTrackMetadata)
	if manual != nil {
		result.Desired = &librarymetadata.FieldValue{
			State: librarymetadata.StateValue, Value: *manual,
			Authority:  librarymetadata.AuthorityManual,
			Provenance: librarymetadata.Provenance{Kind: librarymetadata.ProvenanceManual},
		}
		return result
	}
	if positionOverride {
		if strings.TrimSpace(trackTitle) != "" {
			return result
		}
		result.Desired = &librarymetadata.FieldValue{
			State: librarymetadata.StateValue, Value: trackTitle,
			Authority:  librarymetadata.AuthorityAutomatic,
			Provenance: librarymetadata.Provenance{Kind: librarymetadata.ProvenanceProvider},
		}
		return result
	}
	if title := desiredPublicationTitle(metadata.Recording.Envelope); title != nil {
		result.Desired = title
	} else if strings.TrimSpace(recordingTitle) != "" {
		result.Baseline = recordingTitle
	}
	return result
}

func desiredPublicationTitle(envelope librarymetadata.Envelope) *librarymetadata.FieldValue {
	if value := envelope.Desired["title"]; value.State == librarymetadata.StateValue || value.State == librarymetadata.StateExplicitEmpty {
		return &value
	}
	return nil
}

func materializedPositionTitle(metadata json.RawMessage) (manual *string, override bool) {
	var root map[string]json.RawMessage
	if json.Unmarshal(metadata, &root) != nil {
		return nil, false
	}
	var manualEvidence struct {
		Authority string  `json:"authority"`
		Method    string  `json:"method"`
		Title     *string `json:"title_override"`
	}
	if json.Unmarshal(root["manual_matching_override"], &manualEvidence) == nil &&
		manualEvidence.Authority == "manual" && manualEvidence.Method == "manual_matching" && manualEvidence.Title != nil {
		return manualEvidence.Title, true
	}
	var origin string
	if json.Unmarshal(root["title_origin"], &origin) == nil && origin == positionTitleOriginOverride {
		return nil, true
	}
	for _, key := range []string{"manual_matching", "automatic_matching"} {
		var matching struct {
			Reference struct {
				EntityKind string   `json:"entity_kind"`
				Fields     []string `json:"fields"`
			} `json:"provider_fields_reference"`
		}
		if json.Unmarshal(root[key], &matching) == nil &&
			matching.Reference.EntityKind == "release" && slices.Contains(matching.Reference.Fields, "normalized_tracks") {
			return nil, true
		}
	}
	return nil, false
}
