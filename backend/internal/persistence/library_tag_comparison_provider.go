package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/uptrace/bun"
)

// LibraryTagComparisonProvider is the saved tag evidence of the provider that
// owns one exact local release/track edge. It is a read-only projection; the
// explicit local overrides are read separately.
type LibraryTagComparisonProvider struct {
	Name   string
	Status string
	Tags   map[string][]string
}

const (
	libraryTagComparisonNameMusicBrainz = "MusicBrainz"
	libraryTagComparisonNameLocal       = "Local"

	libraryTagComparisonStatusUnlinked    = "unlinked"
	libraryTagComparisonStatusUnavailable = "unavailable"
	libraryTagComparisonStatusSaved       = "saved"
)

// readLibraryTagComparisonProvider selects provider evidence for one exact
// local release/position edge.
//
// MusicBrainz wins only when both the release and the position have exact
// MusicBrainz bindings. A release-only or unrelated track-only link never moves
// another exact Local position under MusicBrainz. Once that binding pair
// exists, a missing configured source or an absent/stale accepted edge yields
// "unavailable"; it never falls back to Local.
//
// Intentionally unsupported provider tags: Local has no MusicBrainz identities,
// barcode, release status, original release date, or ISRC, so those keys are
// omitted instead of invented. Artist-level genre fan-out and
// MUSICBRAINZ_RELEASETRACKID are not projected here.
func readLibraryTagComparisonProvider(ctx context.Context, tx bun.Tx, releaseID, localTrackID uuid.UUID) (LibraryTagComparisonProvider, error) {
	if releaseID == uuid.Nil || localTrackID == uuid.Nil {
		return LibraryTagComparisonProvider{}, errors.New("read library tag comparison provider: required identity is missing")
	}
	musicBrainz, err := readLibraryTagComparisonMusicBrainzBinding(ctx, tx, releaseID, localTrackID)
	if err != nil {
		return LibraryTagComparisonProvider{}, err
	}
	if musicBrainz.TrackID != uuid.Nil {
		return readLibraryTagComparisonMusicBrainz(ctx, tx, musicBrainz)
	}
	return readLibraryTagComparisonLocal(ctx, tx, releaseID, localTrackID)
}

type libraryTagComparisonMusicBrainzBinding struct {
	ProviderID uuid.UUID
	ReleaseID  uuid.UUID
	ReleaseKey string
	TrackID    uuid.UUID
}

// readLibraryTagComparisonMusicBrainzBinding requires the exact release/track
// binding pair. A partial pair is not a MusicBrainz position and is left to the
// Local provider.
func readLibraryTagComparisonMusicBrainzBinding(ctx context.Context, tx bun.Tx, releaseID, localTrackID uuid.UUID) (libraryTagComparisonMusicBrainzBinding, error) {
	var binding libraryTagComparisonMusicBrainzBinding
	err := tx.NewRaw(`SELECT release_binding.provider_id,release_binding.provider_release_id,release.provider_key,track_binding.provider_track_id
		FROM provider_local_release release_binding
		JOIN provider ON provider.id=release_binding.provider_id AND provider.code='musicbrainz'
		JOIN provider_release release ON release.id=release_binding.provider_release_id AND release.provider_id=release_binding.provider_id
		JOIN provider_local_track track_binding ON track_binding.provider_id=provider.id AND track_binding.local_track_id=?
		JOIN provider_release_track track ON track.id=track_binding.provider_track_id AND track.provider_id=provider.id AND track.release_id=release.id
		WHERE release_binding.local_release_id=?`, localTrackID, releaseID).Scan(ctx, &binding.ProviderID, &binding.ReleaseID, &binding.ReleaseKey, &binding.TrackID)
	if errors.Is(err, sql.ErrNoRows) {
		return libraryTagComparisonMusicBrainzBinding{}, nil
	}
	if err != nil {
		return libraryTagComparisonMusicBrainzBinding{}, fmt.Errorf("read exact MusicBrainz tag binding: %w", err)
	}
	return binding, nil
}

func readLibraryTagComparisonMusicBrainz(ctx context.Context, tx bun.Tx, binding libraryTagComparisonMusicBrainzBinding) (LibraryTagComparisonProvider, error) {
	unavailable := LibraryTagComparisonProvider{Name: libraryTagComparisonNameMusicBrainz, Status: libraryTagComparisonStatusUnavailable, Tags: map[string][]string{}}
	source, found, err := catalogMusicBrainzSource(ctx, tx)
	if err != nil {
		return LibraryTagComparisonProvider{}, err
	}
	if !found || source.ProviderID != binding.ProviderID {
		return unavailable, nil
	}
	snapshot, err := readMusicBrainzMatchingSnapshot(ctx, tx, source.Endpoint, source.ConfigurationIdentity, []string{binding.ReleaseKey}, nil, false)
	if err != nil {
		return LibraryTagComparisonProvider{}, fmt.Errorf("read accepted tag comparison projection: %w", err)
	}
	var release *MatchingRelease
	for index := range snapshot.Releases {
		if snapshot.Releases[index].ID != binding.ReleaseID || !strings.EqualFold(snapshot.Releases[index].ProviderKey, binding.ReleaseKey) {
			continue
		}
		if release != nil {
			return unavailable, nil
		}
		release = &snapshot.Releases[index]
	}
	if release == nil {
		return unavailable, nil
	}
	if _, hasTracks := releaseEvidence(release, "__tracks"); !hasTracks {
		return unavailable, nil
	}
	var track *MatchingReleaseTrack
	for index := range release.Tracks {
		if release.Tracks[index].ID != binding.TrackID {
			continue
		}
		if track != nil {
			return unavailable, nil
		}
		track = &release.Tracks[index]
	}
	if track == nil || track.RecordingProviderKey == "" {
		return unavailable, nil
	}
	tags, err := libraryTagComparisonMusicBrainzTags(*release, *track, snapshot.ProjectionEntities)
	if err != nil {
		return unavailable, nil
	}
	if track.DisplayedNumber != "" {
		tags["TRACKNUMBER"] = []string{track.DisplayedNumber}
	}
	tags["DISCNUMBER"] = []string{strconv.Itoa(track.Medium + 1)}
	return LibraryTagComparisonProvider{Name: libraryTagComparisonNameMusicBrainz, Status: libraryTagComparisonStatusSaved, Tags: nonEmptyProviderTags(tags)}, nil
}

// The caller resolved this track from the exact release_track row, so the
// provider binding pair alone identifies the position.

func libraryTagComparisonMusicBrainzTags(release MatchingRelease, track MatchingReleaseTrack, entities []MatchingProjectionEntity) (map[string][]string, error) {
	values := publication.ProviderCanonicalTagValues{}
	releaseFields, err := decodeFrozenObject(release.Fields)
	if err != nil {
		return nil, err
	}
	if hasProviderFieldEvidence(release.FieldEvidence, "title") {
		if title := jsonString(releaseFields["title"]); strings.TrimSpace(title) != "" {
			values.ReleaseTitle = &title
		}
	}
	if hasProviderFieldEvidence(release.FieldEvidence, "date") {
		if date, present, decodeErr := acceptedProviderString(releaseFields, "date"); decodeErr != nil {
			return nil, decodeErr
		} else if present {
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
		if !hasProviderFieldEvidence(release.FieldEvidence, field.name) {
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
	if hasProviderFieldEvidence(release.FieldEvidence, "label-info") {
		values.Labels, values.CatalogNumbers, err = acceptedReleaseLabelInfo(releaseFields["label-info"])
		if err != nil {
			return nil, err
		}
	}
	recording, _ := findProjectionEntity(entities, "recording", track.RecordingProviderKey)
	var recordingFields map[string]json.RawMessage
	var recordingEvidence map[string]ProviderFieldEvidence
	if recording != nil {
		recordingFields, err = decodeFrozenObject(recording.Fields)
		if err != nil {
			return nil, err
		}
		recordingEvidence = decodeEvidence(recording.FieldEvidence)
	}
	if hasProviderFieldEvidence(recordingEvidence, "genres") {
		genres, decodeErr := acceptedProviderGenreReferences(recordingFields["genres"])
		if decodeErr != nil {
			return nil, decodeErr
		}
		for _, genre := range genres {
			values.Genres = append(values.Genres, genre.Name)
		}
	}
	if len(values.Genres) == 0 && hasProviderFieldEvidence(release.FieldEvidence, "genres") {
		genres, decodeErr := acceptedProviderGenreReferences(releaseFields["genres"])
		if decodeErr != nil {
			return nil, decodeErr
		}
		for _, genre := range genres {
			values.Genres = append(values.Genres, genre.Name)
		}
	}
	values.ReleaseMBID, _ = canonicalProviderMBID(release.ProviderKey)
	if hasProviderFieldEvidence(release.FieldEvidence, "release-group") {
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
	if hasProviderFieldEvidence(release.FieldEvidence, "artist-credit") {
		credits, _, decodeErr := decodeFrozenCredits(releaseFields["artist-credit"])
		if decodeErr != nil {
			return nil, decodeErr
		}
		values.ReleaseCredits, err = canonicalCredits(credits, entities, nil)
		if err != nil {
			return nil, err
		}
	} else if hasProviderFieldEvidence(release.FieldEvidence, "__credits") {
		values.ReleaseCredits, err = canonicalRelationalCredits(release.Credits, entities, nil)
		if err != nil {
			return nil, err
		}
	}

	positionTitleAccepted := false
	positionCreditsAccepted := false
	if hasProviderFieldEvidence(release.FieldEvidence, "normalized_tracks") {
		var normalized []ProviderNormalizedTrack
		if err := json.Unmarshal(releaseFields["normalized_tracks"], &normalized); err != nil {
			return nil, fmt.Errorf("decode accepted normalized tracks: %w", err)
		}
		for _, item := range normalized {
			if item.ID != track.ID || item.Medium != track.Medium || item.Position != track.Position ||
				!strings.EqualFold(item.RecordingProviderKey, track.RecordingProviderKey) {
				continue
			}
			if item.Title != "" {
				positionTitleAccepted = true
				values.PositionTitle = &item.Title
			}
			if item.ArtistCredit != nil {
				positionCreditsAccepted = true
				credits, _, err := decodeFrozenCredits(*item.ArtistCredit)
				if err != nil {
					return nil, err
				}
				values.PositionCredits, err = canonicalCredits(credits, entities, nil)
				if err != nil {
					return nil, err
				}
			}
			break
		}
	}

	if !positionTitleAccepted && hasProviderFieldEvidence(recordingEvidence, "title") {
		if title := jsonString(recordingFields["title"]); title != "" {
			values.PositionTitle = &title
		}
	}
	if !positionCreditsAccepted && hasProviderFieldEvidence(recordingEvidence, "artist-credit") {
		credits, _, decodeErr := decodeFrozenCredits(recordingFields["artist-credit"])
		if decodeErr != nil {
			return nil, decodeErr
		}
		values.PositionCredits, err = canonicalCredits(credits, entities, nil)
		if err != nil {
			return nil, err
		}
	}
	if recordingMBID, ok := canonicalProviderMBID(track.RecordingProviderKey); ok {
		values.RecordingMBID = recordingMBID
		if hasProviderFieldEvidence(recordingEvidence, "isrcs") {
			values.ISRCs, _, err = acceptedProviderStringList(recordingFields, "isrcs")
			if err != nil {
				return nil, err
			}
		}
	}
	return renderTagComparisonProviderValues(values), nil
}

func readLibraryTagComparisonLocal(ctx context.Context, tx bun.Tx, releaseID, localTrackID uuid.UUID) (LibraryTagComparisonProvider, error) {
	unlinked := LibraryTagComparisonProvider{Status: libraryTagComparisonStatusUnlinked, Tags: map[string][]string{}}
	unavailable := LibraryTagComparisonProvider{Name: libraryTagComparisonNameLocal, Status: libraryTagComparisonStatusUnavailable, Tags: map[string][]string{}}
	var releaseProviderID uuid.UUID
	var providerReleaseID uuid.UUID
	releaseFound := true
	err := tx.NewRaw(`SELECT provider_id,provider_release_id FROM provider_local_release WHERE provider_id=? AND local_release_id=?`, localProviderID, releaseID).Scan(ctx, &releaseProviderID, &providerReleaseID)
	if errors.Is(err, sql.ErrNoRows) {
		releaseFound = false
	} else if err != nil {
		return LibraryTagComparisonProvider{}, fmt.Errorf("read library tag comparison Local release binding: %w", err)
	}
	var trackProviderID uuid.UUID
	var providerTrackID uuid.UUID
	trackFound := true
	err = tx.NewRaw(`SELECT provider_id,provider_track_id FROM provider_local_track WHERE provider_id=? AND local_track_id=?`, localProviderID, localTrackID).Scan(ctx, &trackProviderID, &providerTrackID)
	if errors.Is(err, sql.ErrNoRows) {
		trackFound = false
	} else if err != nil {
		return LibraryTagComparisonProvider{}, fmt.Errorf("read library tag comparison Local track binding: %w", err)
	}
	if !releaseFound && !trackFound {
		return unlinked, nil
	}
	if !releaseFound || !trackFound || releaseProviderID != localProviderID || trackProviderID != localProviderID || providerReleaseID == uuid.Nil || providerTrackID == uuid.Nil {
		// A partial Local binding does not identify one exact position edge.
		return unavailable, nil
	}
	type localEdge struct {
		TitleOverride     *string         `bun:"title_override"`
		RecordingTitle    string          `bun:"recording_title"`
		RecordingMetadata json.RawMessage `bun:"recording_metadata,type:jsonb"`
	}
	var edge localEdge
	err = tx.NewRaw(`SELECT track.metadata->>'title_override' AS title_override,
		recording.title AS recording_title,recording.metadata AS recording_metadata
		FROM provider_release_track track
		JOIN provider_recording recording ON recording.id=track.recording_id
		WHERE track.provider_id=? AND track.release_id=? AND track.id=? AND track.present`, localProviderID, providerReleaseID, providerTrackID).Scan(ctx, &edge)
	if errors.Is(err, sql.ErrNoRows) {
		return unavailable, nil
	}
	if err != nil {
		return LibraryTagComparisonProvider{}, fmt.Errorf("read library tag comparison Local edge: %w", err)
	}
	var release struct {
		Title    string          `bun:"title"`
		Metadata json.RawMessage `bun:"metadata,type:jsonb"`
	}
	if err := tx.NewRaw(`SELECT title,metadata FROM provider_release WHERE provider_id=? AND id=?`, localProviderID, providerReleaseID).Scan(ctx, &release); err != nil {
		return LibraryTagComparisonProvider{}, fmt.Errorf("read library tag comparison Local release: %w", err)
	}
	values := publication.ProviderCanonicalTagValues{ReleaseTitle: &release.Title}
	if edge.TitleOverride != nil {
		values.PositionTitle = edge.TitleOverride
	} else {
		values.PositionTitle = &edge.RecordingTitle
	}
	var localReleaseFields struct {
		Genres         json.RawMessage `json:"genres"`
		PrimaryType    *string         `json:"release_group_primary_type"`
		SecondaryTypes []string        `json:"release_group_secondary_types"`
		Edition        *struct {
			ReleaseDate   string `json:"release_date"`
			Country       string `json:"country"`
			Label         string `json:"label"`
			CatalogNumber string `json:"catalog_number"`
		} `json:"edition"`
	}
	if err := json.Unmarshal(release.Metadata, &localReleaseFields); err != nil {
		return unavailable, nil
	}
	if localReleaseFields.PrimaryType != nil {
		values.ReleaseType = *localReleaseFields.PrimaryType
	}
	values.ReleaseTypes = localReleaseFields.SecondaryTypes
	if edition := localReleaseFields.Edition; edition != nil {
		values.ReleaseDate = edition.ReleaseDate
		values.ReleaseCountry = edition.Country
		if strings.TrimSpace(edition.Label) != "" {
			values.Labels = []string{edition.Label}
		}
		if strings.TrimSpace(edition.CatalogNumber) != "" {
			values.CatalogNumbers = []string{edition.CatalogNumber}
		}
	}
	var localRecordingFields struct {
		Genres  json.RawMessage                `json:"genres"`
		Credits []localProviderRecordingCredit `json:"credits"`
	}
	if err := json.Unmarshal(edge.RecordingMetadata, &localRecordingFields); err != nil {
		return unavailable, nil
	}
	values.Genres = localProviderGenreNames(localRecordingFields.Genres)
	if len(values.Genres) == 0 {
		values.Genres = localProviderGenreNames(localReleaseFields.Genres)
	}
	values.PositionCredits, err = localProviderArtistCredits(ctx, tx, localRecordingFields.Credits)
	if err != nil {
		return LibraryTagComparisonProvider{}, err
	}
	values.ReleaseCredits, err = localProviderReleaseCredits(ctx, tx, providerReleaseID)
	if err != nil {
		return LibraryTagComparisonProvider{}, err
	}
	return LibraryTagComparisonProvider{Name: libraryTagComparisonNameLocal, Status: libraryTagComparisonStatusSaved, Tags: nonEmptyProviderTags(renderTagComparisonProviderValues(values))}, nil
}

type localProviderRecordingCredit struct {
	ArtistID   uuid.UUID `json:"artist_id"`
	CreditName string    `json:"credit_name"`
	JoinPhrase string    `json:"join_phrase"`
}

type localProviderTagCredit struct {
	ArtistName string `bun:"artist_name"`
	CreditName string `bun:"credit_name"`
	JoinPhrase string `bun:"join_phrase"`
	SortName   string `bun:"sort_name"`
}

func localProviderArtistCredits(ctx context.Context, tx bun.Tx, credits []localProviderRecordingCredit) ([]librarymetadata.ArtistCredit, error) {
	result := make([]librarymetadata.ArtistCredit, 0, len(credits))
	for _, credit := range credits {
		if credit.ArtistID == uuid.Nil {
			return nil, errors.New("read library tag comparison Local recording credit: artist identity is missing")
		}
		var artist localProviderTagCredit
		if err := tx.NewRaw(`SELECT name AS artist_name, COALESCE(metadata->>'sort_name','') AS sort_name
			FROM provider_artist WHERE id=?`, credit.ArtistID).Scan(ctx, &artist); err != nil {
			return nil, fmt.Errorf("read library tag comparison Local credit artist: %w", err)
		}
		creditName := credit.CreditName
		if strings.TrimSpace(creditName) == "" {
			creditName = artist.ArtistName
		}
		result = append(result, librarymetadata.ArtistCredit{
			ArtistName: artist.ArtistName, CreditName: creditName, JoinPhrase: credit.JoinPhrase, SortName: artist.SortName,
		})
	}
	return result, nil
}

func localProviderReleaseCredits(ctx context.Context, tx bun.Tx, releaseID uuid.UUID) ([]librarymetadata.ArtistCredit, error) {
	var credits []localProviderTagCredit
	if err := tx.NewRaw(`SELECT artist.name AS artist_name,
		COALESCE(NULLIF(credit.credit_name,''),artist.name) AS credit_name,credit.name_join_phrase AS join_phrase,
		COALESCE(artist.metadata->>'sort_name','') AS sort_name
		FROM provider_release_artist_credit credit
		JOIN provider_artist artist ON artist.id=credit.artist_id
		WHERE credit.provider_id=? AND credit.release_id=? AND credit.present ORDER BY credit.position`, localProviderID, releaseID).Scan(ctx, &credits); err != nil {
		return nil, fmt.Errorf("read library tag comparison Local release credits: %w", err)
	}
	result := make([]librarymetadata.ArtistCredit, 0, len(credits))
	for _, credit := range credits {
		result = append(result, librarymetadata.ArtistCredit{
			ArtistName: credit.ArtistName, CreditName: credit.CreditName, JoinPhrase: credit.JoinPhrase, SortName: credit.SortName,
		})
	}
	return result, nil
}

func localProviderGenreNames(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var entries []struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &entries) != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		name := strings.TrimSpace(entry.Name)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}

// The publication renderer shares scalar mappings, but its credit policy can
// drop names without MBIDs or synthesize sort values. This read preserves the
// provider's saved credits independently of that policy.
func renderTagComparisonProviderValues(values publication.ProviderCanonicalTagValues) map[string][]string {
	releaseCredits, positionCredits := values.ReleaseCredits, values.PositionCredits
	values.ReleaseCredits, values.PositionCredits = nil, nil
	tags := publication.RenderProviderCanonicalTagValues(values)
	setTagComparisonProviderCredits(tags, "ALBUMARTIST", releaseCredits)
	setTagComparisonProviderCredits(tags, "ARTIST", positionCredits)
	return tags
}

func setTagComparisonProviderCredits(tags map[string][]string, key string, credits []librarymetadata.ArtistCredit) {
	if len(credits) == 0 {
		return
	}
	var display, sortDisplay strings.Builder
	names := make([]string, 0, len(credits))
	sorts := make([]string, 0, len(credits))
	ids := make([]string, 0, len(credits))
	for _, credit := range credits {
		display.WriteString(credit.CreditName + credit.JoinPhrase)
		names = append(names, credit.ArtistName)
		if credit.SortName != "" {
			sortDisplay.WriteString(credit.SortName + credit.JoinPhrase)
			sorts = append(sorts, credit.SortName)
		}
		if credit.MBID != nil {
			ids = append(ids, *credit.MBID)
		}
	}
	tags[key] = []string{display.String()}
	tags[key+"S"] = names
	if len(sorts) == len(credits) {
		tags[key+"SORT"] = []string{sortDisplay.String()}
		tags[key+"SSORT"] = sorts
	}
	if len(ids) == len(credits) {
		tags["MUSICBRAINZ_"+key+"ID"] = ids
	}
}

func nonEmptyProviderTags(tags map[string][]string) map[string][]string {
	result := make(map[string][]string, len(tags))
	for key, values := range tags {
		if len(values) == 0 {
			continue
		}
		result[key] = values
	}
	return result
}
