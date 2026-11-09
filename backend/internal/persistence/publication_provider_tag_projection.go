package persistence

import (
	"encoding/json"
	"errors"
	"fmt"
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
	for _, field := range []struct {
		name  string
		value *string
	}{
		{name: "barcode", value: &values.Barcode},
		{name: "country", value: &values.ReleaseCountry},
		{name: "status", value: &values.ReleaseStatus},
	} {
		if !hasProviderFieldEvidence(context.Release.FieldEvidence, field.name) {
			continue
		}
		value, present, decodeErr := acceptedProviderString(releaseFields, field.name)
		if decodeErr != nil {
			return nil, decodeErr
		}
		if present {
			*field.value = value
		}
	}
	if hasProviderFieldEvidence(context.Release.FieldEvidence, "label-info") {
		values.Labels, values.CatalogNumbers, err = acceptedReleaseLabelInfo(releaseFields["label-info"])
		if err != nil {
			return nil, err
		}
	}
	genreReferences, explicitEmptyGenre, err := resolvePublicationTagGenres(context, releaseFields)
	if err != nil {
		return nil, err
	}
	for _, genre := range genreReferences {
		values.Genres = append(values.Genres, genre.Name)
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
		values.ReleaseCredits, err = canonicalCredits(credits, context.ArtistEntities, context.ArtistFacts)
		if err != nil {
			return nil, err
		}
	} else if hasProviderFieldEvidence(context.Release.FieldEvidence, "__credits") {
		values.ReleaseCredits, err = canonicalRelationalCredits(context.Release.Credits, context.ArtistEntities, context.ArtistFacts)
		if err != nil {
			return nil, err
		}
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
			values.PositionCredits, err = canonicalCredits(credits, context.ArtistEntities, context.ArtistFacts)
			if err != nil {
				return nil, err
			}
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
			values.PositionCredits, err = canonicalCredits(credits, context.ArtistEntities, context.ArtistFacts)
			if err != nil {
				return nil, err
			}
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
	patch := publication.RenderProviderCanonicalTagValues(values)
	if explicitEmptyGenre {
		patch["GENRE"] = []string{}
	}
	return patch, nil
}

// BuildPublicationProviderCanonicalTagProjection also returns the notices
// associated with intentionally cleared artist tag groups.
func BuildPublicationProviderCanonicalTagProjection(context PublicationProviderTagContext) (publication.ProviderCanonicalTagProjection, error) {
	patch, err := BuildPublicationProviderCanonicalTagPatch(context)
	if err != nil {
		return publication.ProviderCanonicalTagProjection{}, err
	}
	projection := publication.ProviderCanonicalTagProjection{Tags: patch, Notices: []string{}}
	for _, group := range []struct {
		key    string
		notice string
	}{
		{key: "ARTISTS", notice: "recording artist names or MBIDs could not be represented without ambiguity; existing artist tags were cleared"},
		{key: "ALBUMARTISTS", notice: "release artist names or MBIDs could not be represented without ambiguity; existing artist tags were cleared"},
		{key: "ARTISTSORT", notice: "recording artist sort names collide; existing sort tags were cleared"},
		{key: "ALBUMARTISTSORT", notice: "release artist sort names collide; existing sort tags were cleared"},
	} {
		if values, exists := patch[group.key]; exists && len(values) == 0 {
			projection.Notices = append(projection.Notices, group.notice)
		}
	}
	return projection, nil
}

func resolvePublicationTagGenres(context PublicationProviderTagContext, releaseFields map[string]json.RawMessage) ([]librarymetadata.GenreReference, bool, error) {
	for _, candidate := range []struct {
		owner PublicationProviderTagOwner
		scope librarymetadata.Scope
	}{{context.PositionOwner.Owner, librarymetadata.ScopePosition}, {context.TrackOwner, librarymetadata.ScopeTrack}, {context.RecordingOwner, librarymetadata.ScopeRecording}} {
		genres, explicitEmpty, found, err := manualPublicationGenreOverride(candidate.owner, candidate.scope)
		if err != nil {
			return nil, false, err
		}
		if found {
			return genres, explicitEmpty, nil
		}
	}

	providerGenres, providerScope, err := acceptedPublicationGenresWithScope(context, releaseFields)
	if err != nil {
		return nil, false, err
	}
	if providerScope == librarymetadata.ScopeRecording {
		return providerGenres, false, nil
	}
	genres, explicitEmpty, found, err := manualPublicationGenreOverride(context.ReleaseOwner, librarymetadata.ScopeRelease)
	if err != nil {
		return nil, false, err
	}
	if found {
		return genres, explicitEmpty, nil
	}
	if providerScope == librarymetadata.ScopeArtist && len(providerGenres) == 0 {
		return providerGenres, true, nil
	}
	return providerGenres, false, nil
}

func manualPublicationGenreOverride(owner PublicationProviderTagOwner, scope librarymetadata.Scope) ([]librarymetadata.GenreReference, bool, bool, error) {
	if len(owner.Metadata) == 0 {
		return nil, false, false, nil
	}
	envelope, err := decodeFirstPublicationEnvelopeValue(owner.Metadata, scope)
	if err != nil {
		return nil, false, false, err
	}
	field, exists := envelope.Desired["genre"]
	if !exists || field.Authority != librarymetadata.AuthorityManual || field.State == librarymetadata.StateAbsent {
		return nil, false, false, nil
	}
	if field.State == librarymetadata.StateExplicitEmpty {
		return nil, true, true, nil
	}
	if field.State != librarymetadata.StateValue {
		return nil, false, false, nil
	}
	genres, err := genreReferencesFromField(field)
	if err != nil {
		return nil, false, false, fmt.Errorf("decode manual %s genre override: %w", scope, err)
	}
	if len(genres) == 0 {
		return nil, false, false, nil
	}
	return genres, false, true, nil
}

func acceptedReleaseLabelInfo(raw json.RawMessage) ([]string, []string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil, nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, nil, err
	}
	var labels, catalogNumbers []string
	for _, entry := range entries {
		object, err := decodeFrozenObject(entry)
		if err != nil {
			return nil, nil, err
		}
		label, present, err := acceptedProviderObject(object, "label")
		if err != nil {
			return nil, nil, err
		}
		if present {
			name, _, err := acceptedProviderString(label, "name")
			if err != nil {
				return nil, nil, err
			}
			labels = append(labels, name)
		}
		catalog, _, err := acceptedProviderString(object, "catalog-number")
		if err != nil {
			return nil, nil, err
		}
		catalogNumbers = append(catalogNumbers, catalog)
	}
	return labels, catalogNumbers, nil
}

func acceptedPublicationGenres(context PublicationProviderTagContext, releaseFields map[string]json.RawMessage) ([]librarymetadata.GenreReference, error) {
	genres, _, err := acceptedPublicationGenresWithScope(context, releaseFields)
	return genres, err
}

func acceptedPublicationGenresWithScope(context PublicationProviderTagContext, releaseFields map[string]json.RawMessage) ([]librarymetadata.GenreReference, librarymetadata.Scope, error) {
	// A genre list is authoritative only when the provider supplied field-level
	// evidence. The recording must also be explicitly bound to this local edge;
	// otherwise its projection cannot participate in publication.
	var recordingGenres []librarymetadata.GenreReference
	if context.RecordingBound && context.Recording != nil && context.Recording.Kind == "recording" &&
		strings.EqualFold(context.Recording.ProviderKey, context.Position.RecordingProviderKey) {
		recordingEvidence := decodeEvidence(context.Recording.FieldEvidence)
		if hasProviderFieldEvidence(recordingEvidence, "genres") {
			recordingFields, err := decodeFrozenObject(context.Recording.Fields)
			if err != nil {
				return nil, "", err
			}
			genres, err := acceptedProviderGenreReferences(recordingFields["genres"])
			if err != nil {
				return nil, "", err
			}
			recordingGenres = genres
		}
	}
	var releaseGenres []librarymetadata.GenreReference
	if hasProviderFieldEvidence(context.Release.FieldEvidence, "genres") {
		genres, err := acceptedProviderGenreReferences(releaseFields["genres"])
		if err != nil {
			return nil, "", err
		}
		releaseGenres = genres
	}
	resolved, scope := resolvePublicationGenreReferences(recordingGenres, releaseGenres, nil)
	if len(resolved) != 0 {
		return resolved, scope, nil
	}
	// Release artist credits are not a substitute for recording artist credits:
	// MusicBrainz may omit nested recording artist genres for deduplicated artists.
	genres, explicitEmpty, err := acceptedRecordingArtistGenres(context)
	if err != nil {
		return nil, "", err
	}
	if explicitEmpty {
		return []librarymetadata.GenreReference{}, librarymetadata.ScopeArtist, nil
	}
	resolved, scope = resolvePublicationGenreReferences(nil, nil, [][]librarymetadata.GenreReference{genres})
	return resolved, scope, nil
}

func acceptedRecordingArtistGenres(context PublicationProviderTagContext) ([]librarymetadata.GenreReference, bool, error) {
	providerGenres, creditOrder, err := acceptedRecordingArtistGenreMap(context)
	if err != nil {
		return nil, false, err
	}
	return resolvePublicationRecordingArtistGenres(context.GenreArtists, providerGenres, creditOrder)
}

func acceptedRecordingArtistGenreMap(context PublicationProviderTagContext) (map[string][]librarymetadata.GenreReference, []string, error) {
	genresByMBID := make(map[string][]librarymetadata.GenreReference)
	var creditOrder []string
	if !context.RecordingBound || context.Recording == nil || context.Recording.Kind != "recording" ||
		!strings.EqualFold(context.Recording.ProviderKey, context.Position.RecordingProviderKey) ||
		!hasProviderFieldEvidence(decodeEvidence(context.Recording.FieldEvidence), "artist-credit") {
		return genresByMBID, creditOrder, nil
	}
	recordingFields, err := decodeFrozenObject(context.Recording.Fields)
	if err != nil {
		return nil, nil, err
	}
	credits, _, err := decodeFrozenCredits(recordingFields["artist-credit"])
	if err != nil {
		return nil, nil, err
	}
	seenArtists := make(map[string]struct{}, len(credits))
	for _, credit := range credits {
		mbid, parseErr := uuid.Parse(credit.ArtistProviderKey)
		if parseErr != nil || mbid == uuid.Nil {
			continue
		}
		key := strings.ToLower(mbid.String())
		if _, exists := seenArtists[key]; exists {
			continue
		}
		seenArtists[key] = struct{}{}
		creditOrder = append(creditOrder, key)
		artist, ok := findProjectionEntity(context.ArtistEntities, "artist", key)
		if !ok || !hasProviderFieldEvidence(decodeEvidence(artist.FieldEvidence), "genres") {
			continue
		}
		artistFields, decodeErr := decodeFrozenObject(artist.Fields)
		if decodeErr != nil {
			return nil, nil, decodeErr
		}
		genres, decodeErr := acceptedProviderGenreReferences(artistFields["genres"])
		if decodeErr != nil {
			return nil, nil, decodeErr
		}
		genresByMBID[key] = genres
	}
	return genresByMBID, creditOrder, nil
}

func acceptedProviderGenreReferences(raw json.RawMessage) ([]librarymetadata.GenreReference, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, err
	}
	genres := make([]librarymetadata.GenreReference, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		object, err := decodeFrozenObject(entry)
		if err != nil {
			return nil, err
		}
		id, hasID, err := acceptedProviderString(object, "id")
		if err != nil {
			return nil, err
		}
		name, hasName, err := acceptedProviderString(object, "name")
		if err != nil {
			return nil, err
		}
		parsedID, parseErr := uuid.Parse(id)
		if !hasID || !hasName || parseErr != nil || parsedID == uuid.Nil || strings.TrimSpace(name) == "" {
			return nil, errors.New("accepted MusicBrainz genre is missing a valid id or name")
		}
		canonicalID := parsedID.String()
		if _, exists := seen[canonicalID]; exists {
			continue
		}
		seen[canonicalID] = struct{}{}
		genres = append(genres, librarymetadata.GenreReference{ID: canonicalID, SourceName: name, Name: librarymetadata.NormalizeGenreName(name)})
	}
	return genres, nil
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

func canonicalRelationalCredits(source []MatchingReleaseCredit, artists []MatchingProjectionEntity, facts []PublicationProviderTagArtist) ([]librarymetadata.ArtistCredit, error) {
	credits := make([]FrozenArtistCredit, 0, len(source))
	for _, credit := range source {
		credits = append(credits, FrozenArtistCredit{Position: credit.Position, ArtistProviderKey: credit.ArtistProviderKey, Name: credit.Name, JoinPhrase: credit.NameJoinPhrase})
	}
	return canonicalCredits(credits, artists, facts)
}

func canonicalCredits(source []FrozenArtistCredit, artists []MatchingProjectionEntity, facts []PublicationProviderTagArtist) ([]librarymetadata.ArtistCredit, error) {
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
		disambiguation := localArtistDisambiguation(facts, credit.ArtistProviderKey)
		if entity, ok := findProjectionEntity(artists, "artist", credit.ArtistProviderKey); ok && entity != nil && hasProviderFieldEvidence(decodeEvidence(entity.FieldEvidence), "disambiguation") {
			fields, decodeErr := decodeFrozenObject(entity.Fields)
			if decodeErr != nil {
				return nil, decodeErr
			}
			disambiguation, _, decodeErr = acceptedProviderString(fields, "disambiguation")
			if decodeErr != nil {
				return nil, decodeErr
			}
		} else if providerDisambiguation, present, decodeErr := providerArtistMetadataString(facts, credit.ArtistProviderKey, "disambiguation"); decodeErr != nil {
			return nil, decodeErr
		} else if present {
			disambiguation = providerDisambiguation
		}
		credits = append(credits, librarymetadata.ArtistCredit{
			ArtistName: artistName, CreditName: credit.Name, JoinPhrase: credit.JoinPhrase, MBID: artistMBID,
			SortName: localArtistSortName(facts, credit.ArtistProviderKey), Disambiguation: disambiguation,
		})
	}
	return credits, nil
}

func localArtistSortName(facts []PublicationProviderTagArtist, providerKey string) string {
	for _, artist := range facts {
		if !strings.EqualFold(artist.ProviderKey, providerKey) {
			continue
		}
		envelope, err := decodeFirstPublicationEnvelopeValue(artist.Metadata, librarymetadata.ScopeArtist)
		if err != nil {
			return ""
		}
		field, exists := envelope.Desired["sort_name"]
		if !exists || field.State != librarymetadata.StateValue {
			return ""
		}
		name, _ := field.Value.(string)
		return name
	}
	return ""
}

func localArtistDisambiguation(facts []PublicationProviderTagArtist, providerKey string) string {
	for _, artist := range facts {
		if !strings.EqualFold(artist.ProviderKey, providerKey) {
			continue
		}
		fields, err := decodeFrozenObject(artist.Metadata)
		if err != nil {
			return ""
		}
		value, _, err := acceptedProviderString(fields, "disambiguation")
		if err != nil {
			return ""
		}
		return value
	}
	return ""
}

func providerArtistMetadataString(facts []PublicationProviderTagArtist, providerKey, field string) (string, bool, error) {
	for _, artist := range facts {
		if !strings.EqualFold(artist.ProviderKey, providerKey) {
			continue
		}
		fields, err := decodeFrozenObject(artist.ProviderMetadata)
		if err != nil {
			return "", false, err
		}
		return acceptedProviderString(fields, field)
	}
	return "", false, nil
}
