package persistence

import (
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
	return publication.RenderProviderCanonicalTagValues(values), nil
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
