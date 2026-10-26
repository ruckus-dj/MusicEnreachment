package persistence

import (
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

// BuildPublicationProviderCanonicalTagPatch converts only nonempty accepted
// canonical fields with field-level evidence into normalized tag values. It
// intentionally does not decide protection or apply the patch to current tags.
func BuildPublicationProviderCanonicalTagPatch(context PublicationProviderTagContext) (map[string][]string, error) {
	values := publication.ProviderCanonicalTagValues{}
	releaseFields, err := decodeFrozenObject(context.Release.Fields)
	if err != nil {
		return nil, err
	}
	if hasProviderFieldEvidence(context.Release.FieldEvidence, "title") {
		if title := jsonString(releaseFields["title"]); strings.TrimSpace(title) != "" {
			values.ReleaseTitle = &title
		}
	}
	if hasProviderFieldEvidence(context.Release.FieldEvidence, "date") {
		date, present, decodeErr := acceptedProviderString(releaseFields, "date")
		if decodeErr != nil {
			return nil, decodeErr
		}
		if present {
			values.ReleaseDate = date
		}
	}
	values.ReleaseMBID, _ = canonicalProviderMBID(context.Release.ProviderKey)
	if hasProviderFieldEvidence(context.Release.FieldEvidence, "release-group") {
		group, present, decodeErr := acceptedProviderObject(releaseFields, "release-group")
		if decodeErr != nil {
			return nil, decodeErr
		}
		if present {
			groupID, _, decodeErr := acceptedProviderString(group, "id")
			if decodeErr != nil {
				return nil, decodeErr
			}
			if id, ok := canonicalProviderMBID(groupID); ok {
				values.ReleaseGroupMBID = id
			}
			values.OriginalDate, _, decodeErr = acceptedProviderString(group, "first-release-date")
			if decodeErr != nil {
				return nil, decodeErr
			}
			values.ReleaseType, _, decodeErr = acceptedProviderString(group, "primary-type")
			if decodeErr != nil {
				return nil, decodeErr
			}
			values.ReleaseTypes, _, decodeErr = acceptedProviderStringList(group, "secondary-types")
			if decodeErr != nil {
				return nil, decodeErr
			}
		}
	}
	if hasProviderFieldEvidence(context.Release.FieldEvidence, "artist-credit") {
		credits, _, decodeErr := decodeFrozenCredits(releaseFields["artist-credit"])
		if decodeErr != nil {
			return nil, decodeErr
		}
		values.ReleaseCredits = canonicalCredits(credits, context.ArtistEntities)
	} else if hasProviderFieldEvidence(context.Release.FieldEvidence, "__credits") {
		values.ReleaseCredits = canonicalRelationalCredits(context.Release.Credits, context.ArtistEntities)
	}

	positionTitleAccepted := false
	if hasProviderFieldEvidence(context.Release.FieldEvidence, "normalized_tracks") {
		if title, present := acceptedNormalizedTrackTitle(releaseFields["normalized_tracks"], context.Position.Medium, context.Position.Position, context.Position.RecordingProviderKey); present {
			positionTitleAccepted = true
			if strings.TrimSpace(title) != "" {
				values.PositionTitle = &title
			}
		}
	}
	if !positionTitleAccepted && context.RecordingBound && context.Recording != nil && hasProviderFieldEvidence(decodeEvidence(context.Recording.FieldEvidence), "title") {
		fields, decodeErr := decodeFrozenObject(context.Recording.Fields)
		if decodeErr != nil {
			return nil, decodeErr
		}
		if title := jsonString(fields["title"]); strings.TrimSpace(title) != "" {
			values.PositionTitle = &title
		}
	}

	positionCreditsAccepted := false
	if hasProviderFieldEvidence(context.Release.FieldEvidence, "normalized_tracks") {
		credits, present, decodeErr := acceptedNormalizedTrackCredits(releaseFields["normalized_tracks"], context.Position.Medium, context.Position.Position, context.Position.RecordingProviderKey, &context.Release)
		if decodeErr != nil {
			return nil, decodeErr
		}
		if present {
			positionCreditsAccepted = true
			values.PositionCredits = canonicalCredits(credits, context.ArtistEntities)
		}
	}
	if !positionCreditsAccepted && context.RecordingBound && context.Recording != nil {
		if hasProviderFieldEvidence(decodeEvidence(context.Recording.FieldEvidence), "artist-credit") {
			fields, decodeErr := decodeFrozenObject(context.Recording.Fields)
			if decodeErr != nil {
				return nil, decodeErr
			}
			credits, _, decodeErr := decodeFrozenCredits(fields["artist-credit"])
			if decodeErr != nil {
				return nil, decodeErr
			}
			values.PositionCredits = canonicalCredits(credits, context.ArtistEntities)
		}
	}
	if context.RecordingBound && context.Recording != nil && context.Recording.Kind == "recording" &&
		strings.EqualFold(context.Recording.ProviderKey, context.Position.RecordingProviderKey) {
		if recordingMBID, valid := canonicalProviderMBID(context.Recording.ProviderKey); valid {
			values.RecordingMBID = recordingMBID
			if hasProviderFieldEvidence(decodeEvidence(context.Recording.FieldEvidence), "isrcs") {
				recordingFields, decodeErr := decodeFrozenObject(context.Recording.Fields)
				if decodeErr != nil {
					return nil, decodeErr
				}
				values.ISRCs, _, decodeErr = acceptedProviderStringList(recordingFields, "isrcs")
				if decodeErr != nil {
					return nil, decodeErr
				}
			}
		}
	}
	return publication.RenderProviderCanonicalTagValues(values), nil
}

func canonicalProviderMBID(value string) (string, bool) {
	parsed, err := uuid.Parse(value)
	if err != nil || !strings.EqualFold(value, parsed.String()) {
		return "", false
	}
	return parsed.String(), true
}

func acceptedProviderObject(fields map[string]json.RawMessage, key string) (map[string]json.RawMessage, bool, error) {
	raw, exists := fields[key]
	if !exists || len(raw) == 0 || string(raw) == "null" {
		return nil, false, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, false, err
	}
	if object == nil {
		return nil, false, nil
	}
	return object, true, nil
}

func acceptedProviderString(fields map[string]json.RawMessage, key string) (string, bool, error) {
	raw, exists := fields[key]
	if !exists || len(raw) == 0 || string(raw) == "null" {
		return "", false, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false, err
	}
	return value, true, nil
}

func acceptedProviderStringList(fields map[string]json.RawMessage, key string) ([]string, bool, error) {
	raw, exists := fields[key]
	if !exists || len(raw) == 0 || string(raw) == "null" {
		return nil, false, nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, false, err
	}
	if values == nil {
		return nil, false, nil
	}
	return values, true, nil
}

func hasProviderFieldEvidence(evidence map[string]ProviderFieldEvidence, field string) bool {
	_, ok := evidence[field]
	return ok
}

func canonicalRelationalCredits(source []MatchingReleaseCredit, artists []MatchingProjectionEntity) []librarymetadata.ArtistCredit {
	credits := make([]FrozenArtistCredit, 0, len(source))
	for _, credit := range source {
		credits = append(credits, FrozenArtistCredit{Position: credit.Position, ArtistProviderKey: credit.ArtistProviderKey, Name: credit.Name, JoinPhrase: credit.NameJoinPhrase})
	}
	return canonicalCredits(credits, artists)
}

func canonicalCredits(source []FrozenArtistCredit, artists []MatchingProjectionEntity) []librarymetadata.ArtistCredit {
	credits := make([]librarymetadata.ArtistCredit, 0, len(source))
	for _, credit := range source {
		artistName := credit.Name
		if entity, ok := findProjectionEntity(artists, "artist", credit.ArtistProviderKey); ok && entity != nil {
			fields, err := decodeFrozenObject(entity.Fields)
			if err == nil && hasProviderFieldEvidence(decodeEvidence(entity.FieldEvidence), "name") {
				if canonical := jsonString(fields["name"]); strings.TrimSpace(canonical) != "" {
					artistName = canonical
				}
			}
		}
		if strings.TrimSpace(artistName) == "" {
			continue
		}
		mbid, err := uuid.Parse(credit.ArtistProviderKey)
		var artistMBID *string
		if err == nil {
			parsed := mbid.String()
			artistMBID = &parsed
		}
		credits = append(credits, librarymetadata.ArtistCredit{
			ArtistName: artistName, CreditName: credit.Name, JoinPhrase: credit.JoinPhrase, MBID: artistMBID,
		})
	}
	return credits
}
