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

// readLibraryTagComparisonOverrides returns only the normalized file-tag values
// that are forced by explicitly manual metadata on the exact release, position,
// track, and optional recording owners. It reads the caller's read-only
// transaction and never consults automatic/provider metadata, CURRENT
// publication snapshots, source inventory, or derived hydration.
//
// Keys are uppercase normalized TagLib names. Explicit-empty overrides are
// represented as non-nil empty lists; an explicit empty string is represented
// as a one-element list containing "".
func readLibraryTagComparisonOverrides(ctx context.Context, tx bun.Tx, releaseID, positionID, localTrackID uuid.UUID, recordingID *uuid.UUID) (map[string][]string, error) {
	if releaseID == uuid.Nil || positionID == uuid.Nil || localTrackID == uuid.Nil {
		return nil, errors.New("read library tag comparison overrides: required owner identity is missing")
	}
	tags := make(map[string][]string)

	var releaseRow struct {
		Metadata json.RawMessage `bun:"metadata,type:jsonb"`
	}
	if err := tx.NewRaw(`SELECT metadata FROM local_release WHERE id=?`, releaseID).Scan(ctx, &releaseRow); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("read library tag comparison overrides: release owner not found")
		}
		return nil, fmt.Errorf("read library tag comparison release metadata: %w", err)
	}
	releaseEnvelope, err := readTagComparisonOwnerEnvelope(releaseRow.Metadata, librarymetadata.ScopeRelease)
	if err != nil {
		return nil, err
	}

	var positionRow struct {
		TrackID     uuid.UUID       `bun:"track_id,type:uuid"`
		RecordingID *uuid.UUID      `bun:"recording_id,type:uuid,nullzero"`
		Metadata    json.RawMessage `bun:"metadata,type:jsonb"`
	}
	if err := tx.NewRaw(`SELECT track_id,recording_id,metadata FROM release_track WHERE id=? AND release_id=?`, positionID, releaseID).Scan(ctx, &positionRow); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("read library tag comparison overrides: position owner not found")
		}
		return nil, fmt.Errorf("read library tag comparison position metadata: %w", err)
	}
	if positionRow.TrackID != localTrackID {
		return nil, errors.New("read library tag comparison overrides: position does not belong to the local track")
	}
	if recordingID != nil && (positionRow.RecordingID == nil || *positionRow.RecordingID != *recordingID) {
		return nil, errors.New("read library tag comparison overrides: recording does not belong to the position")
	}
	positionEnvelope, err := readTagComparisonOwnerEnvelope(positionRow.Metadata, librarymetadata.ScopePosition)
	if err != nil {
		return nil, err
	}

	var trackRow struct {
		Metadata json.RawMessage `bun:"metadata,type:jsonb"`
	}
	if err := tx.NewRaw(`SELECT metadata FROM local_track WHERE id=?`, localTrackID).Scan(ctx, &trackRow); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("read library tag comparison overrides: track owner not found")
		}
		return nil, fmt.Errorf("read library tag comparison track metadata: %w", err)
	}
	trackEnvelope, err := readTagComparisonOwnerEnvelope(trackRow.Metadata, librarymetadata.ScopeTrack)
	if err != nil {
		return nil, err
	}

	var recordingEnvelope *librarymetadata.Envelope
	if recordingID != nil {
		var recordingRow struct {
			Metadata json.RawMessage `bun:"metadata,type:jsonb"`
		}
		if err := tx.NewRaw(`SELECT metadata FROM local_recording WHERE id=?`, *recordingID).Scan(ctx, &recordingRow); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, errors.New("read library tag comparison overrides: recording owner not found")
			}
			return nil, fmt.Errorf("read library tag comparison recording metadata: %w", err)
		}
		decoded, err := readTagComparisonOwnerEnvelope(recordingRow.Metadata, librarymetadata.ScopeRecording)
		if err != nil {
			return nil, err
		}
		recordingEnvelope = &decoded
	}

	owners := tagComparisonOwners{
		release:   tagComparisonOwner{scope: librarymetadata.ScopeRelease, envelope: releaseEnvelope},
		position:  tagComparisonOwner{scope: librarymetadata.ScopePosition, envelope: positionEnvelope},
		track:     tagComparisonOwner{scope: librarymetadata.ScopeTrack, envelope: trackEnvelope},
		recording: recordingEnvelope,
	}
	for _, definition := range librarymetadata.Fields() {
		switch definition.Name {
		case "artist_credit", "genre", "works", "movement", "contributors":
			continue
		}
		owners.applyManualField(tags, definition)
	}
	owners.applyManualArtistCredits(tags)
	if err := owners.applyManualGenres(tags); err != nil {
		return nil, err
	}
	if err := owners.applyManualStructuredFields(tags); err != nil {
		return nil, err
	}
	if err := owners.applyManualMatchingOverrides(tags, trackRow.Metadata); err != nil {
		return nil, err
	}
	return tags, nil
}

type tagComparisonOwner struct {
	scope    librarymetadata.Scope
	envelope librarymetadata.Envelope
}

type tagComparisonOwners struct {
	release   tagComparisonOwner
	position  tagComparisonOwner
	track     tagComparisonOwner
	recording *librarymetadata.Envelope
}

func readTagComparisonOwnerEnvelope(metadata json.RawMessage, scope librarymetadata.Scope) (librarymetadata.Envelope, error) {
	envelope, err := decodeMetadataOwnerEnvelope(metadata, scope)
	if err != nil {
		return librarymetadata.Envelope{}, fmt.Errorf("read library tag comparison %s metadata: %w", scope, err)
	}
	if envelope.Scope != scope {
		return librarymetadata.Envelope{}, fmt.Errorf("read library tag comparison %s metadata: stored envelope scope %q does not match owner", scope, envelope.Scope)
	}
	return envelope, nil
}

func (owners tagComparisonOwners) nonRelease() []tagComparisonOwner {
	result := []tagComparisonOwner{owners.position, owners.track}
	if owners.recording != nil {
		result = append(result, tagComparisonOwner{scope: librarymetadata.ScopeRecording, envelope: *owners.recording})
	}
	return result
}

func (owners tagComparisonOwners) applyManualField(tags map[string][]string, definition librarymetadata.FieldDefinition) {
	if tagComparisonFieldAllowsScope(definition, librarymetadata.ScopeRelease) {
		if field, _, found := resolveManualTagComparisonField([]tagComparisonOwner{owners.release}, definition.Name); found {
			applyManualScalarTag(tags, librarymetadata.ScopeRelease, definition.Name, field)
		}
	}
	nonRelease := make([]tagComparisonOwner, 0, 3)
	for _, owner := range owners.nonRelease() {
		if tagComparisonFieldAllowsScope(definition, owner.scope) {
			nonRelease = append(nonRelease, owner)
		}
	}
	if len(nonRelease) == 0 {
		return
	}
	if field, _, found := resolveManualTagComparisonField(nonRelease, definition.Name); found {
		applyManualScalarTag(tags, librarymetadata.ScopePosition, definition.Name, field)
	}
}

func tagComparisonFieldAllowsScope(definition librarymetadata.FieldDefinition, scope librarymetadata.Scope) bool {
	for _, candidate := range definition.Scopes {
		if candidate == scope {
			return true
		}
	}
	return false
}

func resolveManualTagComparisonField(owners []tagComparisonOwner, name string) (librarymetadata.FieldValue, librarymetadata.Scope, bool) {
	for _, owner := range owners {
		field, exists := owner.envelope.Desired[name]
		if !exists || field.Authority != librarymetadata.AuthorityManual {
			continue
		}
		switch field.State {
		case librarymetadata.StateAbsent:
			if name == "artist_credit" || name == "asin" || name == "script" {
				return librarymetadata.FieldValue{}, "", false
			}
			continue
		case librarymetadata.StateValue, librarymetadata.StateExplicitEmpty:
			return field, owner.scope, true
		default:
			continue
		}
	}
	return librarymetadata.FieldValue{}, "", false
}

func applyManualScalarTag(tags map[string][]string, footprintScope librarymetadata.Scope, name string, field librarymetadata.FieldValue) {
	keys := publication.PublicationTagKeysForField(footprintScope, name)
	if name == "key" {
		// KEY is the neutral normalized property. INITIALKEY is container-specific
		// and must not be invented without an output container.
		keys = []string{"KEY"}
	}
	if len(keys) == 0 {
		return
	}
	if field.State == librarymetadata.StateExplicitEmpty {
		for _, key := range keys {
			tags[key] = []string{}
		}
		return
	}
	values, ok := tagComparisonScalarValues(name, field.Value)
	if !ok || len(values) == 0 {
		return
	}
	for _, key := range keys {
		tags[key] = append([]string{}, values...)
	}
}

func tagComparisonScalarValues(name string, value any) ([]string, bool) {
	if name == "compilation" {
		compilation, ok := value.(bool)
		if !ok {
			return nil, false
		}
		if compilation {
			return []string{"1"}, true
		}
		return []string{"0"}, true
	}
	if name == "content_advisory" {
		advisory, ok := value.(string)
		if !ok || (advisory != "explicit" && advisory != "clean") {
			return nil, false
		}
		if advisory == "clean" {
			return []string{"2"}, true
		}
		return []string{"1"}, true
	}
	switch typed := value.(type) {
	case string:
		return []string{typed}, true
	case []string:
		return append([]string{}, typed...), true
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			values = append(values, text)
		}
		return values, true
	case json.Number:
		return []string{typed.String()}, true
	case int:
		return []string{strconv.Itoa(typed)}, true
	case int64:
		return []string{strconv.FormatInt(typed, 10)}, true
	case float64:
		return []string{strconv.FormatFloat(typed, 'f', -1, 64)}, true
	default:
		return nil, false
	}
}

func (owners tagComparisonOwners) applyManualArtistCredits(tags map[string][]string) {
	if field, _, found := resolveManualTagComparisonField([]tagComparisonOwner{owners.release}, "artist_credit"); found {
		applyManualCreditTags(tags, librarymetadata.ScopeRelease, field)
	}
	if field, _, found := resolveManualTagComparisonField(owners.nonRelease(), "artist_credit"); found {
		applyManualCreditTags(tags, librarymetadata.ScopePosition, field)
	}
}

func applyManualCreditTags(tags map[string][]string, footprintScope librarymetadata.Scope, field librarymetadata.FieldValue) {
	fullKey, listKey, mbidKey, sortKey, sortListKey := "ARTIST", "ARTISTS", "MUSICBRAINZ_ARTISTID", "ARTISTSORT", "ARTISTSSORT"
	if footprintScope == librarymetadata.ScopeRelease {
		fullKey, listKey, mbidKey, sortKey, sortListKey = "ALBUMARTIST", "ALBUMARTISTS", "MUSICBRAINZ_ALBUMARTISTID", "ALBUMARTISTSORT", "ALBUMARTISTSSORT"
	}
	if field.State == librarymetadata.StateExplicitEmpty {
		for _, key := range []string{fullKey, listKey, mbidKey, sortKey, sortListKey} {
			tags[key] = []string{}
		}
		return
	}
	credits, ok := tagComparisonArtistCredits(field.Value)
	if !ok {
		return
	}
	if len(credits) == 0 {
		return
	}
	full := make([]string, 0, len(credits))
	names := make([]string, 0, len(credits))
	mbids := make([]string, 0, len(credits))
	completeMBIDs := true
	for _, credit := range credits {
		full = append(full, credit.CreditName+credit.JoinPhrase)
		name := credit.ArtistName
		if name == "" {
			name = credit.CreditName
		}
		names = append(names, name)
		if credit.MBID == nil || strings.TrimSpace(*credit.MBID) == "" {
			completeMBIDs = false
		} else {
			mbids = append(mbids, *credit.MBID)
		}
	}
	tags[fullKey] = []string{strings.Join(full, "")}
	tags[listKey] = names
	if completeMBIDs {
		tags[mbidKey] = mbids
	}
	// Sort names are hydrated from artist relations, not saved in the manual
	// credit envelope, so they are not inferred here.
}

func tagComparisonArtistCredits(value any) ([]librarymetadata.ArtistCredit, bool) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var credits []librarymetadata.ArtistCredit
	if err := json.Unmarshal(encoded, &credits); err != nil {
		return nil, false
	}
	return credits, true
}

func (owners tagComparisonOwners) applyManualGenres(tags map[string][]string) error {
	field, _, found := resolveManualTagComparisonField(owners.nonReleaseWithRelease(), "genre")
	if !found {
		return nil
	}
	if field.State == librarymetadata.StateExplicitEmpty {
		tags["GENRE"] = []string{}
		return nil
	}
	genres, err := genreReferencesFromField(field)
	if err != nil {
		return fmt.Errorf("read library tag comparison manual genre override: %w", err)
	}
	values := make([]string, 0, len(genres))
	for _, genre := range genres {
		if strings.TrimSpace(genre.Name) == "" {
			continue
		}
		values = append(values, genre.Name)
	}
	if len(values) != 0 {
		tags["GENRE"] = values
	}
	return nil
}

func (owners tagComparisonOwners) nonReleaseWithRelease() []tagComparisonOwner {
	result := owners.nonRelease()
	result = append(result, owners.release)
	return result
}

func (owners tagComparisonOwners) applyManualStructuredFields(tags map[string][]string) error {
	for _, name := range []string{"works", "movement", "contributors"} {
		field, _, found := resolveManualTagComparisonField(owners.nonRelease(), name)
		if !found {
			continue
		}
		switch name {
		case "works":
			if !applyManualWorks(tags, field) {
				return fmt.Errorf("read library tag comparison manual works override: unsupported value")
			}
		case "movement":
			if !applyManualMovement(tags, field) {
				return fmt.Errorf("read library tag comparison manual movement override: unsupported value")
			}
		case "contributors":
			if !applyManualContributors(tags, field) {
				return fmt.Errorf("read library tag comparison manual contributors override: unsupported value")
			}
		}
	}
	return nil
}

func applyManualWorks(tags map[string][]string, field librarymetadata.FieldValue) bool {
	if field.State == librarymetadata.StateExplicitEmpty {
		tags["WORK"] = []string{}
		tags["MUSICBRAINZ_WORKID"] = []string{}
		return true
	}
	encoded, err := json.Marshal(field.Value)
	if err != nil {
		return false
	}
	var works []librarymetadata.Work
	if err := json.Unmarshal(encoded, &works); err != nil {
		return false
	}
	if len(works) == 0 {
		return true
	}
	names := make([]string, 0, len(works))
	ids := make([]string, 0, len(works))
	allIDs := len(works) != 0
	for _, work := range works {
		names = append(names, work.Name)
		if work.MBID == nil || strings.TrimSpace(*work.MBID) == "" {
			allIDs = false
		} else {
			ids = append(ids, *work.MBID)
		}
	}
	tags["WORK"] = names
	if allIDs {
		tags["MUSICBRAINZ_WORKID"] = ids
	}
	return true
}

func applyManualMovement(tags map[string][]string, field librarymetadata.FieldValue) bool {
	if field.State == librarymetadata.StateExplicitEmpty {
		for _, key := range []string{"MOVEMENTNAME", "MOVEMENTNUMBER", "MOVEMENTCOUNT"} {
			tags[key] = []string{}
		}
		return true
	}
	encoded, err := json.Marshal(field.Value)
	if err != nil {
		return false
	}
	var movement librarymetadata.Movement
	if err := json.Unmarshal(encoded, &movement); err != nil {
		return false
	}
	tags["MOVEMENTNAME"] = []string{movement.Name}
	tags["MOVEMENTNUMBER"] = []string{strconv.Itoa(movement.Number)}
	tags["MOVEMENTCOUNT"] = []string{strconv.Itoa(movement.Count)}
	return true
}

var tagComparisonContributorRoles = map[string]string{
	"composer": "COMPOSER", "lyricist": "LYRICIST", "conductor": "CONDUCTOR",
	"arranger": "ARRANGER", "engineer": "ENGINEER", "producer": "PRODUCER",
	"remixer": "REMIXER", "djmixer": "DJMIXER", "mixer": "MIXER", "director": "DIRECTOR",
}

var tagComparisonContributorMBIDRoles = map[string]string{
	"composer": "MUSICBRAINZ_COMPOSERID", "lyricist": "MUSICBRAINZ_LYRICISTID",
	"conductor": "MUSICBRAINZ_CONDUCTORID", "arranger": "MUSICBRAINZ_ARRANGERID",
	"engineer": "MUSICBRAINZ_ENGINEERID", "producer": "MUSICBRAINZ_PRODUCERID",
	"remixer": "MUSICBRAINZ_REMIXERID", "djmixer": "MUSICBRAINZ_DJMIXERID",
	"mixer": "MUSICBRAINZ_MIXERID", "director": "MUSICBRAINZ_DIRECTORID",
}

func applyManualContributors(tags map[string][]string, field librarymetadata.FieldValue) bool {
	if field.State == librarymetadata.StateExplicitEmpty {
		for _, key := range publication.PublicationTagKeysForField(librarymetadata.ScopePosition, "contributors") {
			tags[key] = []string{}
		}
		tags["PERFORMER"] = []string{}
		return true
	}
	encoded, err := json.Marshal(field.Value)
	if err != nil {
		return false
	}
	var contributors []librarymetadata.Contributor
	if err := json.Unmarshal(encoded, &contributors); err != nil {
		return false
	}
	namesByRole := make(map[string][]string)
	mbidsByRole := make(map[string][]string)
	completeMBIDs := make(map[string]bool)
	for _, contributor := range contributors {
		role := strings.ToLower(strings.TrimSpace(contributor.Role))
		nameKey, known := tagComparisonContributorRoles[role]
		if role == "performer" {
			nameKey, known = "PERFORMER", true
		}
		if !known {
			continue
		}
		namesByRole[nameKey] = append(namesByRole[nameKey], contributor.Name)
		if _, exists := completeMBIDs[nameKey]; !exists {
			completeMBIDs[nameKey] = true
		}
		if contributor.MBID == nil || strings.TrimSpace(*contributor.MBID) == "" {
			completeMBIDs[nameKey] = false
		} else {
			mbidsByRole[nameKey] = append(mbidsByRole[nameKey], *contributor.MBID)
		}
	}
	for role, nameKey := range tagComparisonContributorRoles {
		if values, exists := namesByRole[nameKey]; exists {
			tags[nameKey] = values
			if completeMBIDs[nameKey] {
				tags[tagComparisonContributorMBIDRoles[role]] = mbidsByRole[nameKey]
			}
		}
	}
	if values, exists := namesByRole["PERFORMER"]; exists {
		// Instrument-qualified aliases are container-specific. The neutral
		// PERFORMER property is the only portable name group.
		tags["PERFORMER"] = values
		if completeMBIDs["PERFORMER"] {
			tags["MUSICBRAINZ_PERFORMERID"] = mbidsByRole["PERFORMER"]
		}
	}
	return true
}

type tagComparisonManualOverride struct {
	Title   *string
	Artists *[]string
}

func (owners tagComparisonOwners) applyManualMatchingOverrides(tags map[string][]string, metadata json.RawMessage) error {
	override, err := decodeTagComparisonManualOverride(metadata)
	if err != nil {
		return err
	}
	explicitTrackOwners := []tagComparisonOwner{owners.position, owners.track}
	if _, _, found := resolveManualTagComparisonField(explicitTrackOwners, "title"); override.Title != nil && !found {
		tags["TITLE"] = []string{*override.Title}
	}
	if override.Artists == nil {
		return nil
	}
	for _, owner := range explicitTrackOwners {
		if field, exists := owner.envelope.Desired["artist_credit"]; exists && field.Authority == librarymetadata.AuthorityManual {
			return nil
		}
	}
	artists := *override.Artists
	if len(artists) == 0 {
		for _, key := range []string{"ARTIST", "ARTISTS", "MUSICBRAINZ_ARTISTID", "ARTISTSORT", "ARTISTSSORT"} {
			tags[key] = []string{}
		}
		return nil
	}
	tags["ARTIST"] = []string{strings.Join(artists, "")}
	tags["ARTISTS"] = append([]string{}, artists...)
	return nil
}

func decodeTagComparisonManualOverride(metadata json.RawMessage) (tagComparisonManualOverride, error) {
	var root map[string]json.RawMessage
	if len(metadata) == 0 {
		return tagComparisonManualOverride{}, nil
	}
	if err := json.Unmarshal(metadata, &root); err != nil {
		return tagComparisonManualOverride{}, fmt.Errorf("read library tag comparison manual matching override: %w", err)
	}
	raw, exists := root["manual_matching_override"]
	if !exists || len(raw) == 0 || string(raw) == "null" {
		return tagComparisonManualOverride{}, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return tagComparisonManualOverride{}, errors.New("read library tag comparison manual matching override: expected a JSON object")
	}
	var provenance struct {
		Authority string `json:"authority"`
		Method    string `json:"method"`
	}
	if err := json.Unmarshal(raw, &provenance); err != nil {
		return tagComparisonManualOverride{}, fmt.Errorf("read library tag comparison matching override provenance: %w", err)
	}
	if provenance.Authority != "manual" || provenance.Method != "manual_matching" {
		return tagComparisonManualOverride{}, nil
	}
	var result tagComparisonManualOverride
	if title, exists := fields["title_override"]; exists {
		var value string
		if err := json.Unmarshal(title, &value); err != nil || string(title) == "null" {
			return tagComparisonManualOverride{}, errors.New("read library tag comparison manual matching override: invalid title override")
		}
		result.Title = &value
	}
	if artists, exists := fields["artist_overrides"]; exists {
		var values []string
		if err := json.Unmarshal(artists, &values); err != nil || string(artists) == "null" {
			return tagComparisonManualOverride{}, errors.New("read library tag comparison manual matching override: invalid artist overrides")
		}
		result.Artists = &values
	}
	return result, nil
}
