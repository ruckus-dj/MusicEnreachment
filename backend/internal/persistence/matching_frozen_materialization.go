package persistence

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// FrozenMaterialization is a provider-data-only projection. A later publisher
// must combine it with database-owned authority and identity mappings.
type FrozenMaterialization struct {
	SchemaVersion int                    `json:"schema_version"`
	Release       FrozenSelectedRelease  `json:"release"`
	Tracks        []FrozenRecordingTrack `json:"tracks"`
}

type FrozenSelectedRelease struct {
	ID           uuid.UUID                    `json:"id"`
	ProviderKey  string                       `json:"provider_key"`
	Title        string                       `json:"title"`
	Fields       json.RawMessage              `json:"fields"`
	FieldSources map[string]FrozenFieldSource `json:"field_sources"`
	Credits      []FrozenArtistCredit         `json:"credits"`
}

type FrozenRecordingTrack struct {
	ProviderTrackID      uuid.UUID            `json:"provider_track_id"`
	ProviderReleaseID    uuid.UUID            `json:"provider_release_id"`
	RecordingProviderKey string               `json:"recording_provider_key"`
	Medium               int                  `json:"medium"`
	Position             int                  `json:"position"`
	DisplayedNumber      string               `json:"displayed_number"`
	Title                string               `json:"title"`
	TitleSource          *FrozenFieldSource   `json:"title_source,omitempty"`
	Credits              []FrozenArtistCredit `json:"credits"`
	CreditSource         *FrozenFieldSource   `json:"credit_source,omitempty"`
}

type FrozenArtistCredit struct {
	Position          int                `json:"position"`
	ArtistProviderKey string             `json:"artist_provider_key"`
	Name              string             `json:"name"`
	ArtistName        string             `json:"artist_name"`
	ArtistNameSource  *FrozenFieldSource `json:"artist_name_source,omitempty"`
	JoinPhrase        string             `json:"join_phrase"`
	Source            *FrozenFieldSource `json:"source,omitempty"`
}

// FrozenFieldSource states which accepted projection field supplied a value.
// Authority and fetch order are deliberately copied from accepted evidence.
type FrozenFieldSource struct {
	EntityKind  string `json:"entity_kind"`
	ProviderKey string `json:"provider_key"`
	Field       string `json:"field"`
	Authority   int    `json:"authority"`
	FetchOrder  int64  `json:"fetch_order"`
}

// FrozenTrackAssignment binds a persisted provider track identity to its
// release-owned zero-based coordinate. IDs are supplied by the caller; this
// converter never invents replacement provider identities.
type FrozenTrackAssignment struct {
	ProviderTrackID   uuid.UUID
	ProviderReleaseID uuid.UUID
	ReleaseKey        string
	RecordingKey      string
	Medium            int
	Position          int
}

var errInvalidFrozenMaterialization = errors.New("invalid frozen matching materialization")

func acceptedProjectionToMaterialization(snapshot ProviderMatchingSnapshot, selectedReleaseKey string, assignments []FrozenTrackAssignment) (FrozenMaterialization, error) {
	var selected *MatchingRelease
	for index := range snapshot.Releases {
		if strings.EqualFold(snapshot.Releases[index].ProviderKey, selectedReleaseKey) {
			if selected != nil {
				return FrozenMaterialization{}, fmt.Errorf("%w: duplicate selected release", errInvalidFrozenMaterialization)
			}
			selected = &snapshot.Releases[index]
		}
	}
	if selected == nil || selected.ID == uuid.Nil || selected.ProviderKey == "" {
		return FrozenMaterialization{}, fmt.Errorf("%w: selected release is missing", errInvalidFrozenMaterialization)
	}
	releaseFields, err := decodeFrozenObject(selected.Fields)
	if err != nil {
		return FrozenMaterialization{}, fmt.Errorf("%w: decode selected release fields: %v", errInvalidFrozenMaterialization, err)
	}
	releaseTitle := selected.Name
	result := FrozenMaterialization{
		SchemaVersion: 2,
		Release: FrozenSelectedRelease{
			ID: selected.ID, ProviderKey: selected.ProviderKey, Title: releaseTitle,
			Fields: cloneRawJSON(selected.Fields), FieldSources: make(map[string]FrozenFieldSource),
			Credits: make([]FrozenArtistCredit, 0),
		},
		Tracks: make([]FrozenRecordingTrack, 0, len(assignments)),
	}
	for _, field := range []string{"title", "artist-credit", "date", "label-info"} {
		if _, present := releaseFields[field]; present {
			if evidence, ok := selected.FieldEvidence[field]; ok {
				result.Release.FieldSources[field] = frozenFieldSource("release", selected.ProviderKey, field, evidence)
			}
		}
	}
	releaseCredits, creditPresent, err := decodeFrozenCredits(releaseFields["artist-credit"])
	if err != nil {
		return FrozenMaterialization{}, fmt.Errorf("%w: decode release credits: %v", errInvalidFrozenMaterialization, err)
	}
	if creditPresent {
		result.Release.Credits = releaseCredits
		if evidence, ok := selected.FieldEvidence["artist-credit"]; ok {
			source := frozenFieldSource("release", selected.ProviderKey, "artist-credit", evidence)
			for index := range result.Release.Credits {
				result.Release.Credits[index].Source = &source
			}
		}
	} else {
		for _, credit := range selected.Credits {
			source := frozenFieldSource("release", selected.ProviderKey, "artist-credit", ProviderFieldEvidence{Authority: credit.Authority, FetchOrder: credit.FetchOrder})
			result.Release.Credits = append(result.Release.Credits, FrozenArtistCredit{Position: credit.Position, ArtistProviderKey: strings.ToLower(credit.ArtistProviderKey), Name: credit.Name, JoinPhrase: credit.NameJoinPhrase, Source: &source})
		}
		if len(selected.Credits) > 0 {
			result.Release.FieldSources["artist-credit"] = FrozenFieldSource{EntityKind: "release", ProviderKey: selected.ProviderKey, Field: "artist-credit", Authority: selected.Credits[0].Authority, FetchOrder: selected.Credits[0].FetchOrder}
		}
	}
	for index := range result.Release.Credits {
		applyFrozenArtistName(snapshot.ProjectionEntities, &result.Release.Credits[index])
	}

	trackByCoordinate := make(map[string]MatchingReleaseTrack, len(selected.Tracks))
	for _, track := range selected.Tracks {
		key := frozenMaterializationCoordinate(track.Medium, track.Position)
		if _, exists := trackByCoordinate[key]; exists {
			return FrozenMaterialization{}, fmt.Errorf("%w: duplicate accepted track coordinate", errInvalidFrozenMaterialization)
		}
		trackByCoordinate[key] = track
	}
	seenIDs := make(map[uuid.UUID]string, len(assignments))
	for _, assignment := range assignments {
		if assignment.ProviderTrackID == uuid.Nil || assignment.ProviderReleaseID == uuid.Nil || assignment.Medium < 0 || assignment.Position < 0 || !strings.EqualFold(assignment.ReleaseKey, selected.ProviderKey) || assignment.ProviderReleaseID != selected.ID {
			return FrozenMaterialization{}, fmt.Errorf("%w: foreign or missing track assignment", errInvalidFrozenMaterialization)
		}
		coordinate := frozenMaterializationCoordinate(assignment.Medium, assignment.Position)
		if previous, exists := seenIDs[assignment.ProviderTrackID]; exists && previous != coordinate {
			return FrozenMaterialization{}, fmt.Errorf("%w: provider track ID has conflicting coordinates", errInvalidFrozenMaterialization)
		}
		seenIDs[assignment.ProviderTrackID] = coordinate
		track, exists := trackByCoordinate[coordinate]
		if !exists || track.ID != assignment.ProviderTrackID || !strings.EqualFold(track.RecordingProviderKey, assignment.RecordingKey) || assignment.RecordingKey == "" {
			return FrozenMaterialization{}, fmt.Errorf("%w: assignment has no accepted selected-release track", errInvalidFrozenMaterialization)
		}
		// A repeated edge at the same position is permitted for reuse by folders.
		if previous, exists := seenIDs[assignment.ProviderTrackID]; exists && previous == coordinate {
			already := false
			for _, materialized := range result.Tracks {
				if materialized.ProviderTrackID == assignment.ProviderTrackID {
					already = true
					break
				}
			}
			if already {
				continue
			}
		}
		trackFields, err := decodeFrozenObject(findProjectionFields(snapshot.ProjectionEntities, "recording", assignment.RecordingKey))
		if err != nil {
			return FrozenMaterialization{}, fmt.Errorf("%w: decode recording fields: %v", errInvalidFrozenMaterialization, err)
		}
		recordingEntity, _ := findProjectionEntity(snapshot.ProjectionEntities, "recording", assignment.RecordingKey)
		title := track.RecordingTitle
		var titleSource *FrozenFieldSource
		if recordingEntity != nil {
			if recordingEntity.Name != "" {
				title = recordingEntity.Name
			} else if value := jsonString(trackFields["title"]); value != "" {
				title = value
			}
			if evidence, ok := decodeEvidence(recordingEntity.FieldEvidence)["title"]; ok && (recordingEntity.Name != "" || jsonString(trackFields["title"]) != "") {
				source := frozenFieldSource("recording", recordingEntity.ProviderKey, "title", evidence)
				titleSource = &source
			}
		}
		if normalizedTitle, exists := acceptedNormalizedTrackTitle(releaseFields["normalized_tracks"], track.Medium, track.Position, assignment.RecordingKey); exists && normalizedTitle != "" {
			title = normalizedTitle
			titleSource = nil
			if evidence, ok := selected.FieldEvidence["normalized_tracks"]; ok {
				source := frozenFieldSource("release", selected.ProviderKey, "normalized_tracks", evidence)
				titleSource = &source
			}
		}
		credits, trackCreditOverride, err := acceptedNormalizedTrackCredits(releaseFields["normalized_tracks"], track.Medium, track.Position, assignment.RecordingKey, selected)
		if err != nil {
			return FrozenMaterialization{}, fmt.Errorf("%w: decode normalized track credits: %v", errInvalidFrozenMaterialization, err)
		}
		var creditSource *FrozenFieldSource
		if trackCreditOverride {
			if evidence, ok := selected.FieldEvidence["normalized_tracks"]; ok {
				source := frozenFieldSource("release", selected.ProviderKey, "normalized_tracks", evidence)
				creditSource = &source
			}
		} else {
			credits, _, err = decodeFrozenCredits(trackFields["artist-credit"])
			if err != nil {
				return FrozenMaterialization{}, fmt.Errorf("%w: decode recording credits: %v", errInvalidFrozenMaterialization, err)
			}
			if recordingEntity != nil {
				if evidence, ok := decodeEvidence(recordingEntity.FieldEvidence)["artist-credit"]; ok {
					source := frozenFieldSource("recording", recordingEntity.ProviderKey, "artist-credit", evidence)
					creditSource = &source
				}
			}
		}
		for index := range credits {
			if creditSource != nil {
				source := *creditSource
				credits[index].Source = &source
			}
			applyFrozenArtistName(snapshot.ProjectionEntities, &credits[index])
		}
		result.Tracks = append(result.Tracks, FrozenRecordingTrack{ProviderTrackID: assignment.ProviderTrackID, ProviderReleaseID: selected.ID, RecordingProviderKey: track.RecordingProviderKey, Medium: assignment.Medium, Position: assignment.Position, DisplayedNumber: track.DisplayedNumber, Title: title, TitleSource: titleSource, Credits: credits, CreditSource: creditSource})
	}
	return result, nil
}

func frozenMaterializationCoordinate(medium, position int) string {
	return fmt.Sprintf("%d\x00%d", medium, position)
}

func decodeFrozenObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(raw) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, errors.New("expected object")
	}
	return object, nil
}

func jsonString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}
func cloneRawJSON(raw json.RawMessage) json.RawMessage { return append(json.RawMessage(nil), raw...) }
func frozenFieldSource(kind, key, field string, evidence ProviderFieldEvidence) FrozenFieldSource {
	return FrozenFieldSource{EntityKind: kind, ProviderKey: key, Field: field, Authority: evidence.Authority, FetchOrder: evidence.FetchOrder}
}

func decodeFrozenCredits(raw json.RawMessage) ([]FrozenArtistCredit, bool, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, false, nil
	}
	var values []struct {
		Name       string `json:"name"`
		JoinPhrase string `json:"joinphrase"`
		Artist     struct {
			ID string `json:"id"`
		} `json:"artist"`
	}
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, false, err
	}
	credits := make([]FrozenArtistCredit, 0, len(values))
	for position, item := range values {
		if item.Artist.ID == "" {
			continue
		}
		credits = append(credits, FrozenArtistCredit{Position: position, ArtistProviderKey: strings.ToLower(item.Artist.ID), Name: item.Name, JoinPhrase: item.JoinPhrase})
	}
	return credits, true, nil
}

func findProjectionEntity(entities []MatchingProjectionEntity, kind, key string) (*MatchingProjectionEntity, bool) {
	var found *MatchingProjectionEntity
	for index := range entities {
		if entities[index].Kind == kind && strings.EqualFold(entities[index].ProviderKey, key) {
			if found != nil {
				return nil, false
			}
			found = &entities[index]
		}
	}
	return found, found != nil
}
func findProjectionFields(entities []MatchingProjectionEntity, kind, key string) json.RawMessage {
	entity, _ := findProjectionEntity(entities, kind, key)
	if entity == nil {
		return nil
	}
	return entity.Fields
}
func decodeEvidence(raw json.RawMessage) map[string]ProviderFieldEvidence {
	var evidence map[string]ProviderFieldEvidence
	_ = json.Unmarshal(raw, &evidence)
	return evidence
}

func applyFrozenArtistName(entities []MatchingProjectionEntity, credit *FrozenArtistCredit) {
	credit.ArtistName = credit.Name
	entity, ok := findProjectionEntity(entities, "artist", credit.ArtistProviderKey)
	if !ok || entity == nil {
		return
	}
	name := entity.Name
	if name == "" {
		fields, err := decodeFrozenObject(entity.Fields)
		if err == nil {
			name = jsonString(fields["name"])
		}
	}
	evidence, hasEvidence := decodeEvidence(entity.FieldEvidence)["name"]
	if strings.TrimSpace(name) == "" || !hasEvidence {
		return
	}
	credit.ArtistName = name
	source := frozenFieldSource("artist", entity.ProviderKey, "name", evidence)
	credit.ArtistNameSource = &source
}

func acceptedNormalizedTrackTitle(raw json.RawMessage, medium, position int, recordingKey string) (string, bool) {
	var tracks []ProviderNormalizedTrack
	if json.Unmarshal(raw, &tracks) != nil {
		return "", false
	}
	for _, track := range tracks {
		if track.Medium == medium && track.Position == position && strings.EqualFold(track.RecordingProviderKey, recordingKey) {
			if track.Title != "" {
				return track.Title, true
			}
			return "", true
		}
	}
	return "", false
}
func acceptedNormalizedTrackCredits(raw json.RawMessage, medium, position int, recordingKey string, release *MatchingRelease) ([]FrozenArtistCredit, bool, error) {
	var tracks []struct {
		Medium               int              `json:"medium"`
		Position             int              `json:"position"`
		RecordingProviderKey string           `json:"recording_provider_key"`
		ArtistCredit         *json.RawMessage `json:"artist-credit"`
	}
	if len(raw) == 0 {
		return nil, false, nil
	}
	if err := json.Unmarshal(raw, &tracks); err != nil {
		return nil, false, err
	}
	for _, track := range tracks {
		if track.Medium == medium && track.Position == position && strings.EqualFold(track.RecordingProviderKey, recordingKey) {
			if track.ArtistCredit == nil {
				return nil, false, nil
			}
			credits, _, err := decodeFrozenCredits(*track.ArtistCredit)
			return credits, true, err
		}
	}
	return nil, false, nil
}
