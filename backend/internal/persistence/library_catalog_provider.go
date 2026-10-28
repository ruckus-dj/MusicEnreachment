package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/uptrace/bun"
)

const libraryCatalogMusicBrainzEndpoint = "https://musicbrainz.org/ws/2"

type catalogProviderSource struct {
	ID         uuid.UUID `bun:"id,type:uuid"`
	ProviderID uuid.UUID `bun:"provider_id,type:uuid"`
}

// enrichLibraryCatalogReleaseProvider reads only the configured MusicBrainz
// source's accepted projections. A missing or incomplete release projection
// leaves provider totals unknown rather than inferring them from local rows.
func enrichLibraryCatalogReleaseProvider(ctx context.Context, tx bun.Tx, rows []LibraryCatalogRelease) error {
	if len(rows) == 0 {
		return nil
	}
	source, found, err := catalogMusicBrainzSource(ctx, tx)
	if err != nil || !found {
		return err
	}
	releaseIDs := make([]uuid.UUID, 0, len(rows))
	for i := range rows {
		releaseIDs = append(releaseIDs, rows[i].ID)
	}
	type projection struct {
		LocalID     uuid.UUID       `bun:"local_id,type:uuid"`
		ProviderKey string          `bun:"provider_key"`
		Fields      json.RawMessage `bun:"fields"`
		Evidence    json.RawMessage `bun:"field_evidence"`
		Complete    bool            `bun:"complete"`
		Count       int             `bun:"provider_position_count"`
		HasCount    bool            `bun:"has_count"`
	}
	projections := make([]projection, 0)
	query := `SELECT pl.local_release_id AS local_id,e.provider_key,e.fields,e.field_evidence,e.complete,
		(SELECT count(*) FROM provider_source_release_track t
		 WHERE t.provider_source_id=e.provider_source_id AND t.provider_id=e.provider_id AND t.release_id=pr.id AND t.present
		 AND COALESCE((e.field_evidence->'__tracks'->>'authority')::integer,-1)=t.authority
		 AND COALESCE((e.field_evidence->'__tracks'->>'fetch_order')::bigint,-1)=t.fetch_order) AS provider_position_count,
		(e.complete AND jsonb_exists_all(e.field_evidence->'__tracks', ARRAY['authority','fetch_order'])
		 AND e.field_evidence#>>'{__tracks,authority}' IS NOT NULL
		 AND e.field_evidence#>>'{__tracks,fetch_order}' IS NOT NULL) IS TRUE AS has_count
		FROM provider_local_release pl JOIN provider_release pr ON pr.id=pl.provider_release_id AND pr.provider_id=pl.provider_id
		JOIN provider_source_entity e ON e.provider_id=pl.provider_id AND e.provider_source_id=? AND e.entity_kind='release' AND e.provider_key=pr.provider_key
		WHERE pl.provider_id=? AND pl.local_release_id IN (?)`
	if err := tx.NewRaw(query, source.ID, source.ProviderID, bun.List(releaseIDs)).Scan(ctx, &projections); err != nil {
		return fmt.Errorf("read library catalog provider releases: %w", err)
	}
	byLocal := make(map[uuid.UUID]projection, len(projections))
	for _, value := range projections {
		byLocal[value.LocalID] = value
	}
	for i := range rows {
		projection, ok := byLocal[rows[i].ID]
		rows[i].Counts.ProviderPositionCount = nil
		rows[i].Edition = libraryCatalogLocalEdition(rows[i].Metadata)
		if !ok {
			continue
		}
		if projection.HasCount {
			count := projection.Count
			rows[i].Counts.ProviderPositionCount = &count
		}
		providerEdition, err := libraryCatalogProviderEdition(projection.Fields, projection.Evidence)
		if err != nil {
			return fmt.Errorf("read library catalog provider edition: %w", err)
		}
		mergeLibraryCatalogEdition(&rows[i].Edition, providerEdition, rows[i].Metadata)
	}
	return nil
}

// enrichLibraryCatalogPositionProvider uses current-source accepted release
// tracks, then accepted recording lengths. It never consults the global provider
// registry as a fallback.
func enrichLibraryCatalogPositionProvider(ctx context.Context, tx bun.Tx, rows []LibraryCatalogPosition) error {
	if len(rows) == 0 {
		return nil
	}
	source, found, err := catalogMusicBrainzSource(ctx, tx)
	if err != nil || !found {
		return err
	}
	positionIDs := make([]uuid.UUID, 0, len(rows))
	for i := range rows {
		positionIDs = append(positionIDs, rows[i].ID)
	}
	type durationRow struct {
		PositionID uuid.UUID `bun:"position_id,type:uuid"`
		Duration   *int64    `bun:"duration"`
	}
	var durations []durationRow
	query := `SELECT rt.id AS position_id,
		COALESCE(
			(SELECT NULLIF(track_entry.value->>'length','')::bigint
			 FROM provider_local_track plt
			 JOIN provider_source_release_track st ON st.provider_id=plt.provider_id AND st.track_id=plt.provider_track_id
			 JOIN provider_source_entity release_entity ON release_entity.provider_source_id=st.provider_source_id AND release_entity.provider_id=st.provider_id AND release_entity.entity_kind='release'
			 JOIN provider_release pr ON pr.id=st.release_id AND pr.provider_id=st.provider_id AND pr.provider_key=release_entity.provider_key
			 CROSS JOIN LATERAL jsonb_array_elements(release_entity.fields->'normalized_tracks') track_entry(value)
			 WHERE plt.local_track_id=rt.track_id AND plt.provider_id=? AND st.provider_source_id=? AND st.present
			AND st.release_id=(SELECT provider_release_id FROM provider_local_release WHERE local_release_id=rt.release_id AND provider_id=plt.provider_id LIMIT 1)
			AND st.medium=rt.medium AND st.position=rt.position
			 AND (track_entry.value->>'medium')::integer=st.medium AND (track_entry.value->>'position')::integer=st.position
			 AND COALESCE((release_entity.field_evidence->'__tracks'->>'authority')::integer,-1)=st.authority
			 AND COALESCE((release_entity.field_evidence->'__tracks'->>'fetch_order')::bigint,-1)=st.fetch_order
			 ORDER BY st.authority DESC,st.fetch_order DESC LIMIT 1),
			(SELECT NULLIF(recording_entity.fields->>'length','')::bigint
			 FROM provider_local_recording plr JOIN provider_recording recording ON recording.id=plr.provider_recording_id AND recording.provider_id=plr.provider_id
			 JOIN provider_source_entity recording_entity ON recording_entity.provider_source_id=? AND recording_entity.provider_id=plr.provider_id AND recording_entity.entity_kind='recording' AND recording_entity.provider_key=recording.provider_key
			 WHERE plr.local_recording_id=rt.recording_id AND plr.provider_id=? AND jsonb_exists(recording_entity.field_evidence, 'length')
			 ORDER BY recording_entity.authority DESC,recording_entity.fetch_order DESC LIMIT 1)
		) AS duration
		FROM release_track rt WHERE rt.id IN (?)`
	if err := tx.NewRaw(query, source.ProviderID, source.ID, source.ID, source.ProviderID, bun.List(positionIDs)).Scan(ctx, &durations); err != nil {
		return fmt.Errorf("read library catalog provider durations: %w", err)
	}
	byID := make(map[uuid.UUID]*int64, len(durations))
	for _, duration := range durations {
		if duration.Duration != nil && *duration.Duration >= 0 {
			value := *duration.Duration
			byID[duration.PositionID] = &value
		}
	}
	for i := range rows {
		rows[i].ProviderDurationMS = byID[rows[i].ID]
	}
	return nil
}

func catalogMusicBrainzSource(ctx context.Context, tx bun.Tx) (catalogProviderSource, bool, error) {
	var config struct {
		Mode     string `bun:"mode"`
		BaseURL  string `bun:"base_url"`
		Identity string `bun:"identity"`
	}
	if err := tx.NewRaw(`SELECT
		COALESCE((SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_mode'),'') AS mode,
		COALESCE((SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_base_url'),'') AS base_url,
		COALESCE((SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'),'') AS identity`).Scan(ctx, &config); err != nil {
		if err == sql.ErrNoRows {
			return catalogProviderSource{}, false, nil
		}
		return catalogProviderSource{}, false, fmt.Errorf("read configured MusicBrainz source: %w", err)
	}
	endpoint := config.BaseURL
	if config.Mode == "public" {
		endpoint = libraryCatalogMusicBrainzEndpoint
	}
	if config.Identity == "" || endpoint == "" {
		return catalogProviderSource{}, false, nil
	}
	var source catalogProviderSource
	err := tx.NewRaw(`SELECT source.id,source.provider_id FROM provider_source source JOIN provider ON provider.id=source.provider_id
		WHERE provider.code='musicbrainz' AND source.endpoint=? AND source.configuration_identity=?`, strings.TrimSuffix(endpoint, "/"), config.Identity).Scan(ctx, &source)
	if err == sql.ErrNoRows {
		return catalogProviderSource{}, false, nil
	}
	if err != nil {
		return catalogProviderSource{}, false, fmt.Errorf("resolve configured MusicBrainz source: %w", err)
	}
	return source, true, nil
}

type catalogLocalMetadata struct {
	LocalMetadata json.RawMessage `json:"local_metadata"`
}

func libraryCatalogLocalEdition(metadata json.RawMessage) LibraryCatalogEdition {
	edition := LibraryCatalogEdition{Labels: []string{}, CatalogNumbers: []string{}, SecondaryTypes: []string{}}
	var root catalogLocalMetadata
	if json.Unmarshal(metadata, &root) != nil || len(root.LocalMetadata) == 0 {
		return edition
	}
	envelope, err := librarymetadata.DecodeEnvelope(root.LocalMetadata)
	if err != nil || envelope.Scope != librarymetadata.ScopeRelease {
		return edition
	}
	setLocalString := func(name string, target **string) {
		field, ok := envelope.Desired[name]
		if !ok || field.State != librarymetadata.StateValue {
			return
		}
		if value, ok := field.Value.(string); ok {
			*target = &value
		}
	}
	setLocalString("release_date", &edition.ReleaseDate)
	setLocalString("original_release_date", &edition.OriginalReleaseDate)
	setLocalString("release_country", &edition.Country)
	setLocalString("release_primary_type", &edition.PrimaryType)
	setLocalString("release_version", &edition.Disambiguation)
	if field, ok := envelope.Desired["label"]; ok && field.State == librarymetadata.StateValue {
		if values, ok := field.Value.([]any); ok {
			for _, item := range values {
				if value, ok := item.(string); ok {
					edition.Labels = append(edition.Labels, value)
				}
			}
		}
	}
	if field, ok := envelope.Desired["catalog_number"]; ok && field.State == librarymetadata.StateValue {
		if value, ok := field.Value.(string); ok {
			edition.CatalogNumbers = append(edition.CatalogNumbers, value)
		}
	}
	if field, ok := envelope.Desired["release_secondary_types"]; ok && field.State == librarymetadata.StateValue {
		if values, ok := field.Value.([]any); ok {
			for _, item := range values {
				if value, ok := item.(string); ok {
					edition.SecondaryTypes = append(edition.SecondaryTypes, value)
				}
			}
		}
	}
	return edition
}

func libraryCatalogProviderEdition(rawFields, rawEvidence json.RawMessage) (LibraryCatalogEdition, error) {
	fields, err := decodeFrozenObject(rawFields)
	if err != nil {
		return LibraryCatalogEdition{}, err
	}
	evidence := decodeEvidence(rawEvidence)
	result := LibraryCatalogEdition{Labels: []string{}, CatalogNumbers: []string{}, SecondaryTypes: []string{}}
	stringField := func(providerName string, target **string) error {
		if !hasProviderFieldEvidence(evidence, providerName) {
			return nil
		}
		value, present, err := acceptedProviderString(fields, providerName)
		if err != nil {
			return err
		}
		if present {
			*target = &value
		}
		return nil
	}
	for _, field := range []struct {
		name   string
		target **string
	}{{"date", &result.ReleaseDate}, {"country", &result.Country}, {"disambiguation", &result.Disambiguation}} {
		if err := stringField(field.name, field.target); err != nil {
			return LibraryCatalogEdition{}, err
		}
	}
	if hasProviderFieldEvidence(evidence, "label-info") {
		result.Labels, result.CatalogNumbers, err = acceptedReleaseLabelInfo(fields["label-info"])
		if err != nil {
			return LibraryCatalogEdition{}, err
		}
	}
	if hasProviderFieldEvidence(evidence, "release-group") {
		group, present, decodeErr := acceptedProviderObject(fields, "release-group")
		if decodeErr != nil {
			return LibraryCatalogEdition{}, decodeErr
		}
		if !present {
			return result, nil
		}
		for _, field := range []struct {
			name   string
			target **string
		}{{"first-release-date", &result.OriginalReleaseDate}, {"primary-type", &result.PrimaryType}} {
			value, present, decodeErr := acceptedProviderString(group, field.name)
			if decodeErr != nil {
				return LibraryCatalogEdition{}, decodeErr
			}
			if present {
				*field.target = &value
			}
		}
		result.SecondaryTypes, _, err = acceptedProviderStringList(group, "secondary-types")
		if err != nil {
			return LibraryCatalogEdition{}, err
		}
	}
	return result, nil
}

func mergeLibraryCatalogEdition(target *LibraryCatalogEdition, provider LibraryCatalogEdition, metadata json.RawMessage) {
	var root catalogLocalMetadata
	if json.Unmarshal(metadata, &root) != nil {
		return
	}
	local := librarymetadata.Envelope{Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopeRelease, Desired: map[string]librarymetadata.FieldValue{}}
	if len(root.LocalMetadata) > 0 {
		var err error
		local, err = librarymetadata.DecodeEnvelope(root.LocalMetadata)
		if err != nil || local.Scope != librarymetadata.ScopeRelease {
			return
		}
	}
	localBlocks := func(field string) bool {
		value, exists := local.Desired[field]
		if !exists {
			return false
		}
		return value.State == librarymetadata.StateExplicitEmpty || (value.State == librarymetadata.StateAbsent && value.Authority == librarymetadata.AuthorityManual) || value.State == librarymetadata.StateValue
	}
	if target.ReleaseDate == nil && !localBlocks("release_date") {
		target.ReleaseDate = provider.ReleaseDate
	}
	if target.OriginalReleaseDate == nil && !localBlocks("original_release_date") {
		target.OriginalReleaseDate = provider.OriginalReleaseDate
	}
	if target.Country == nil && !localBlocks("release_country") {
		target.Country = provider.Country
	}
	if target.PrimaryType == nil && !localBlocks("release_primary_type") {
		target.PrimaryType = provider.PrimaryType
	}
	if target.Disambiguation == nil && !localBlocks("release_version") {
		target.Disambiguation = provider.Disambiguation
	}
	if len(target.Labels) == 0 && !localBlocks("label") {
		target.Labels = provider.Labels
	}
	if len(target.CatalogNumbers) == 0 && !localBlocks("catalog_number") {
		target.CatalogNumbers = provider.CatalogNumbers
	}
	if len(target.SecondaryTypes) == 0 && !localBlocks("release_secondary_types") {
		target.SecondaryTypes = provider.SecondaryTypes
	}
}
