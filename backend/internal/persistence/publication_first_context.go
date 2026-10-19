package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/uptrace/bun"
)

// FirstPublicationSource is the current inventory selection and the facts
// proven by its successful probe. It contains no filesystem-derived values.
type FirstPublicationSource struct {
	Selection PublicationSourceSelection
	Input     PublicationAudioInput
	Quality   PersistedAudioFacts
}

// LibraryFirstPublicationContext is a factual snapshot for constructing a
// first publication request. Policy decisions (quality selection and path
// rendering) remain with the caller.
type LibraryFirstPublicationContext struct {
	Library                         *LibraryPublicationContext
	Sources                         []FirstPublicationSource
	Tags                            publication.TagProjectionInput
	TemplateTokens                  map[string]string
	Metadata                        FirstPublicationMetadata
	Blockers                        []string
	ReleasePrimaryCredits           []librarymetadata.ArtistCredit
	PositionPrimaryCredits          []librarymetadata.ArtistCredit
	SelectedReleasePrimaryArtistID  *uuid.UUID
	SelectedPositionPrimaryArtistID *uuid.UUID
	ResolvedOptionalFields          map[string]librarymetadata.ResolvedField
}

type FirstPublicationMetadata struct {
	Release, Track, Recording, Position LibraryMetadata
}

// ReadLibraryFirstPublicationContext reads all first-publication inputs from a
// single read-only repeatable-read snapshot.
func (repository *SetupManagerRepository) ReadLibraryFirstPublicationContext(ctx context.Context, releaseID, positionID uuid.UUID) (*LibraryFirstPublicationContext, error) {
	var result *LibraryFirstPublicationContext
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		var library *LibraryPublicationContext
		err := readLibraryPublicationContextTx(ctx, tx, releaseID, positionID, &library)
		if err != nil {
			return err
		}
		result = &LibraryFirstPublicationContext{Library: library, Sources: []FirstPublicationSource{}, TemplateTokens: map[string]string{}, Blockers: []string{}}
		var release struct {
			ID              uuid.UUID       `bun:"id,type:uuid"`
			PrimaryArtistID *uuid.UUID      `bun:"primary_artist_id,type:uuid"`
			Title           string          `bun:"title"`
			Metadata        json.RawMessage `bun:"metadata,type:jsonb"`
			Revision        int64           `bun:"metadata_revision"`
		}
		if err := tx.NewRaw(`SELECT id,primary_artist_id,title,metadata,metadata_revision FROM local_release WHERE id=?`, releaseID).Scan(ctx, &release); err != nil {
			return fmt.Errorf("read first publication release metadata: %w", err)
		}
		var position struct {
			PositionID      uuid.UUID  `bun:"id,type:uuid"`
			PrimaryArtistID *uuid.UUID `bun:"primary_artist_id,type:uuid"`
			TrackID         uuid.UUID  `bun:"track_id,type:uuid"`
			RecordingID     *uuid.UUID `bun:"recording_id,type:uuid"`
			Medium          int        `bun:"medium"`
			Number          string     `bun:"displayed_number"`
		}
		if err := tx.NewRaw(`SELECT id,primary_artist_id,track_id, recording_id, medium, displayed_number FROM release_track WHERE id=? AND release_id=?`, positionID, releaseID).Scan(ctx, &position); err != nil {
			return fmt.Errorf("read first publication position: %w", err)
		}
		var track struct {
			ID       uuid.UUID       `bun:"id,type:uuid"`
			Title    string          `bun:"title"`
			Metadata json.RawMessage `bun:"metadata,type:jsonb"`
			Revision int64           `bun:"metadata_revision"`
		}
		if err := tx.NewRaw(`SELECT id,title,metadata,metadata_revision FROM local_track WHERE id=?`, position.TrackID).Scan(ctx, &track); err != nil {
			return fmt.Errorf("read first publication track metadata: %w", err)
		}
		releaseEnv, err := decodeFirstPublicationEnvelope(release.Metadata, librarymetadata.ScopeRelease)
		if err != nil {
			return err
		}
		releaseTitle := release.Title
		if title, exists := releaseEnv.Desired["title"]; exists {
			switch title.State {
			case librarymetadata.StateExplicitEmpty:
				releaseTitle = ""
			case librarymetadata.StateValue:
				if value, ok := title.Value.(string); ok {
					releaseTitle = value
				}
			}
		}
		positionEnv := &library.Metadata.Envelope
		result.Metadata.Release = LibraryMetadata{Revision: release.Revision}
		result.Metadata.Release.Envelope, err = decodeFirstPublicationEnvelopeValue(release.Metadata, librarymetadata.ScopeRelease)
		if err != nil {
			return err
		}
		result.Metadata.Track = LibraryMetadata{Revision: track.Revision}
		result.Metadata.Track.Envelope, err = decodeFirstPublicationEnvelopeValue(track.Metadata, librarymetadata.ScopeTrack)
		if err != nil {
			return err
		}
		result.Metadata.Position = library.Metadata
		result.Metadata.Position.Envelope = *positionEnv
		positionTagsEnvelope := cloneFirstPublicationEnvelope(positionEnv)
		result.TemplateTokens["ALBUM_NAME"] = releaseTitle
		result.TemplateTokens["TRACK_NAME"] = track.Title
		result.TemplateTokens["POSITION_ID"] = positionID.String()
		result.TemplateTokens["ALBUM_ID"] = releaseID.String()
		result.TemplateTokens["TRACK_NUMBER"] = position.Number
		result.TemplateTokens["DISC_NUMBER"] = strconv.Itoa(position.Medium)
		result.Tags = publication.TagProjectionInput{
			Release:  publication.ReleaseTags{Title: releaseTitle, Desired: releaseEnv},
			Position: publication.PositionTags{Title: track.Title, Desired: positionTagsEnvelope, DiscNumber: firstPublicationIntPointer(position.Medium)},
		}
		result.Tags.Release.Artist, err = firstPublicationCredits(ctx, tx, "release_artist", "release_id", releaseID)
		if err != nil {
			return err
		}
		result.Tags.Position.Artist, err = firstPublicationCredits(ctx, tx, "track_artist", "track_id", position.TrackID)
		if err != nil {
			return err
		}
		result.ReleasePrimaryCredits = append([]librarymetadata.ArtistCredit(nil), result.Tags.Release.Artist...)
		result.PositionPrimaryCredits = append([]librarymetadata.ArtistCredit(nil), result.Tags.Position.Artist...)
		result.SelectedReleasePrimaryArtistID = release.PrimaryArtistID
		result.SelectedPositionPrimaryArtistID = position.PrimaryArtistID
		setPrimaryArtistTemplateTokens(result.TemplateTokens, "ALBUM", result.ReleasePrimaryCredits, release.PrimaryArtistID)
		setPrimaryArtistTemplateTokens(result.TemplateTokens, "TRACK", result.PositionPrimaryCredits, position.PrimaryArtistID)
		result.Tags.Release.ID = firstPublicationUUID(releaseEnv, "release_mbid")
		result.Tags.Release.GroupID = firstPublicationUUID(releaseEnv, "release_group_mbid")
		result.Tags.Release.Compilation = firstPublicationBool(releaseEnv, "compilation")
		result.Tags.Position.ReleaseTrackID = firstPublicationUUID(positionEnv, "release_track_mbid")
		result.Tags.Position.RecordingID = firstPublicationUUID(positionEnv, "recording_mbid")
		if result.Tags.Release.ID != nil {
			result.TemplateTokens["ALBUM_MBID"] = result.Tags.Release.ID.String()
			result.TemplateTokens["ALBUM_MBID_PART"] = "[" + result.Tags.Release.ID.String() + "]"
		}
		if position.Number != "" {
			if n, parseErr := strconv.Atoi(position.Number); parseErr == nil {
				result.Tags.Position.TrackNumber = &n
			}
		}
		result.Tags.Position.TrackTotal = nil
		copyFirstPublicationTokens(result.TemplateTokens, releaseEnv)
		if position.RecordingID != nil {
			var recording struct {
				ID       uuid.UUID       `bun:"id,type:uuid"`
				Title    string          `bun:"title"`
				Metadata json.RawMessage `bun:"metadata,type:jsonb"`
				Revision int64           `bun:"metadata_revision"`
			}
			if err := tx.NewRaw(`SELECT id,title,metadata,metadata_revision FROM local_recording WHERE id=?`, *position.RecordingID).Scan(ctx, &recording); err != nil {
				return fmt.Errorf("read first publication recording metadata: %w", err)
			}
			result.Metadata.Recording = LibraryMetadata{Revision: recording.Revision}
			result.Metadata.Recording.Envelope, err = decodeFirstPublicationEnvelopeValue(recording.Metadata, librarymetadata.ScopeRecording)
			if err != nil {
				return err
			}
			if result.Tags.Position.RecordingID == nil {
				result.Tags.Position.RecordingID = firstPublicationUUID(&result.Metadata.Recording.Envelope, "recording_mbid")
			}
		}
		if err := resolveFirstPublicationOptionalFields(result); err != nil {
			return err
		}
		if err := readFirstPublicationSources(ctx, tx, position.TrackID, &result.Sources, &result.Blockers); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func resolveFirstPublicationOptionalFields(result *LibraryFirstPublicationContext) error {
	position := &result.Metadata.Position.Envelope
	track := &result.Metadata.Track.Envelope
	release := &result.Metadata.Release.Envelope
	var recording *librarymetadata.Envelope
	if result.Metadata.Recording.Envelope.Scope == librarymetadata.ScopeRecording {
		recording = &result.Metadata.Recording.Envelope
	}
	result.ResolvedOptionalFields = make(map[string]librarymetadata.ResolvedField)
	for _, definition := range librarymetadata.Fields() {
		if definition.Canonical || definition.Type == librarymetadata.TypeUUID || definition.Type == librarymetadata.TypeUUIDList || definition.Type == librarymetadata.TypeInteger {
			continue
		}
		resolved, err := librarymetadata.ResolveOptionalField(definition.Name, position, track, recording, release)
		if err != nil {
			return fmt.Errorf("resolve first publication metadata %q: %w", definition.Name, err)
		}
		// Optional values reach ProjectTags only through the two projection
		// envelopes, and position is applied after release. A value that lost
		// resolution in one scope (for example an empty position list) would
		// otherwise still be projected and overwrite the resolved winner, so
		// drop the field from both projections before placing the winner. The
		// raw owner envelopes are the audit snapshot and share their desired
		// maps with the cloned projection envelopes, so they stay untouched.
		delete(result.Tags.Release.Desired.Desired, definition.Name)
		delete(result.Tags.Position.Desired.Desired, definition.Name)
		if !resolved.Present {
			continue
		}
		result.ResolvedOptionalFields[definition.Name] = resolved
		fieldScope := librarymetadata.ScopePosition
		if resolved.Scope == librarymetadata.ScopeRelease || len(definition.Scopes) == 1 && definition.Scopes[0] == librarymetadata.ScopeRelease {
			fieldScope = librarymetadata.ScopeRelease
		}
		if fieldScope == librarymetadata.ScopeRelease {
			result.Tags.Release.Desired.Desired[definition.Name] = resolved.Field
		} else {
			result.Tags.Position.Desired.Desired[definition.Name] = resolved.Field
		}
	}
	return nil
}

func cloneFirstPublicationEnvelope(envelope *librarymetadata.Envelope) *librarymetadata.Envelope {
	if envelope == nil {
		return nil
	}
	clone := *envelope
	clone.Desired = make(map[string]librarymetadata.FieldValue, len(envelope.Desired))
	for name, value := range envelope.Desired {
		clone.Desired[name] = value
	}
	return &clone
}

func decodeFirstPublicationEnvelope(raw json.RawMessage, scope librarymetadata.Scope) (*librarymetadata.Envelope, error) {
	envelope, err := decodeFirstPublicationEnvelopeValue(raw, scope)
	if err != nil {
		return nil, err
	}
	if envelope.Scope == "" {
		return nil, nil
	}
	return &envelope, nil
}

func copyFirstPublicationTokens(tokens map[string]string, envelope *librarymetadata.Envelope) {
	if envelope == nil {
		return
	}
	for field, token := range map[string]string{"release_primary_type": "ALBUM_PRIMARY_TYPE", "release_secondary_types": "ALBUM_SECONDARY_TYPES"} {
		value, exists := envelope.Desired[field]
		if !exists || value.State != librarymetadata.StateValue {
			continue
		}
		switch typed := value.Value.(type) {
		case string:
			tokens[token] = typed
		case json.Number:
			tokens[token] = typed.String()
		case []any:
			parts := make([]string, 0, len(typed))
			for _, item := range typed {
				if text, ok := item.(string); ok {
					parts = append(parts, text)
				}
			}
			tokens[token] = strings.Join(parts, ", ")
		}
	}
	date, exists := envelope.Desired["release_date"]
	if exists && date.State == librarymetadata.StateValue {
		switch typed := date.Value.(type) {
		case map[string]any:
			if year, ok := typed["year"].(json.Number); ok {
				tokens["ALBUM_YEAR"] = year.String()
			}
		case string:
			if len(typed) >= 4 {
				tokens["ALBUM_YEAR"] = typed[:4]
			}
		}
	}
}

func setPrimaryArtistTemplateTokens(tokens map[string]string, prefix string, credits []librarymetadata.ArtistCredit, selected *uuid.UUID) {
	if len(credits) > 0 {
		var full strings.Builder
		for _, credit := range credits {
			creditedName := strings.TrimSpace(credit.CreditName)
			if creditedName == "" {
				creditedName = credit.ArtistName
			}
			full.WriteString(creditedName)
			full.WriteString(credit.JoinPhrase)
		}
		tokens[prefix+"_ARTIST"] = full.String()
	}
	if selected == nil {
		return
	}
	for _, credit := range credits {
		if credit.ArtistID != selected.String() {
			continue
		}
		name := strings.TrimSpace(credit.CreditName)
		if name == "" {
			name = credit.ArtistName
		}
		tokens[prefix+"_PRIMARY_ARTIST"] = name
		tokens[prefix+"_PRIMARY_ARTIST_ID"] = credit.ArtistID
		if credit.MBID != nil {
			tokens[prefix+"_PRIMARY_ARTIST_MBID"] = *credit.MBID
			tokens[prefix+"_PRIMARY_ARTIST_MBID_PART"] = "[" + *credit.MBID + "]"
		}
		return
	}
}

func firstPublicationUUID(envelope *librarymetadata.Envelope, field string) *uuid.UUID {
	if envelope == nil {
		return nil
	}
	value, ok := envelope.Desired[field]
	if !ok || value.State != librarymetadata.StateValue {
		return nil
	}
	text, ok := value.Value.(string)
	if !ok {
		return nil
	}
	id, err := uuid.Parse(text)
	if err != nil {
		return nil
	}
	return &id
}

func firstPublicationBool(envelope *librarymetadata.Envelope, field string) *bool {
	if envelope == nil {
		return nil
	}
	value, ok := envelope.Desired[field]
	if !ok || value.State != librarymetadata.StateValue {
		return nil
	}
	flag, ok := value.Value.(bool)
	if !ok {
		return nil
	}
	return &flag
}

func decodeFirstPublicationEnvelopeValue(raw json.RawMessage, scope librarymetadata.Scope) (librarymetadata.Envelope, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return librarymetadata.Envelope{}, fmt.Errorf("decode first publication metadata: %w", err)
	}
	encoded := object["local_metadata"]
	if len(encoded) == 0 {
		return librarymetadata.Envelope{Version: librarymetadata.EnvelopeVersion, Scope: scope, Desired: map[string]librarymetadata.FieldValue{}}, nil
	}
	envelope, err := librarymetadata.DecodeEnvelope(encoded)
	if err != nil {
		return librarymetadata.Envelope{}, fmt.Errorf("decode first publication metadata envelope: %w", err)
	}
	if envelope.Scope != scope {
		return librarymetadata.Envelope{}, fmt.Errorf("first publication metadata envelope has wrong scope")
	}
	return envelope, nil
}

func firstPublicationCredits(ctx context.Context, tx bun.IDB, relation, parentColumn string, parent uuid.UUID) ([]librarymetadata.ArtistCredit, error) {
	if (relation != "release_artist" && relation != "track_artist") || (parentColumn != "release_id" && parentColumn != "track_id") {
		return nil, errors.New("invalid first publication artist relation")
	}
	var rows []struct {
		ID     uuid.UUID `bun:"artist_id,type:uuid"`
		Name   string    `bun:"name"`
		Credit string    `bun:"credit_name"`
		Join   string    `bun:"name_join_phrase"`
	}
	query := `SELECT credit.artist_id,artist.name,credit.name AS credit_name,credit.name_join_phrase
		FROM ` + relation + ` credit JOIN local_artist artist ON artist.id=credit.artist_id WHERE credit.` + parentColumn + `=? ORDER BY credit.position`
	if err := tx.NewRaw(query, parent).Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("read first publication artist credits: %w", err)
	}
	ids := make([]uuid.UUID, 0, len(rows))
	seen := map[uuid.UUID]bool{}
	for _, row := range rows {
		if !seen[row.ID] {
			seen[row.ID] = true
			ids = append(ids, row.ID)
		}
	}
	mbids, err := loadMusicBrainzArtistMBIDs(ctx, tx, ids)
	if err != nil {
		return nil, fmt.Errorf("read first publication artist MBIDs: %w", err)
	}
	credits := make([]librarymetadata.ArtistCredit, 0, len(rows))
	for _, row := range rows {
		credit := librarymetadata.ArtistCredit{ArtistID: row.ID.String(), ArtistName: row.Name, CreditName: row.Credit, JoinPhrase: row.Join}
		if mbid := mbids[row.ID]; mbid != nil {
			value := mbid.String()
			credit.MBID = &value
		}
		credits = append(credits, credit)
	}
	return credits, nil
}

func readFirstPublicationSources(ctx context.Context, tx bun.IDB, trackID uuid.UUID, result *[]FirstPublicationSource, blockers *[]string) error {
	// Candidate selection is only the persisted selected track_source link. The
	// source work must still point at the inventory location and have a successful
	// probe step attached to that exact current variant.
	var rows []struct {
		RootID     uuid.UUID          `bun:"root_id,type:uuid"`
		LocationID uuid.UUID          `bun:"location_id,type:uuid"`
		WorkID     uuid.UUID          `bun:"work_id,type:uuid"`
		VariantID  uuid.UUID          `bun:"variant_id,type:uuid"`
		Configured string             `bun:"configured_path"`
		Inventory  string             `bun:"inventory_path"`
		Relative   string             `bun:"relative_path"`
		Size       int64              `bun:"size_bytes"`
		MTime      time.Time          `bun:"mtime"`
		Variant    SourceMediaVariant `bun:"embed:variant__"`
	}
	var rejected struct {
		UnavailableRoots int `bun:"unavailable_roots"`
		StaleLocations   int `bun:"stale_locations"`
		Unprobed         int `bun:"unprobed"`
	}
	if err := tx.NewRaw(`SELECT
		count(*) FILTER (WHERE root.id IS NULL OR NOT root.enabled OR root.status<>'available' OR root.inventory_path IS DISTINCT FROM work.inventory_path OR root.configured_path IS DISTINCT FROM work.configured_path) AS unavailable_roots,
			count(*) FILTER (WHERE location.id IS NULL OR work.current_location_id IS DISTINCT FROM location.id OR location.media_variant_id IS DISTINCT FROM identity.media_variant_id OR location.relative_path IS DISTINCT FROM work.relative_path OR location.size_bytes<>work.size_bytes OR location.mtime<>work.mtime OR location.probe_status<>'audio') AS stale_locations,
		count(*) FILTER (WHERE probe.work_id IS NULL OR probe.state<>'succeeded' OR probe.success_probe_variant_id IS DISTINCT FROM identity.media_variant_id) AS unprobed
		FROM track_source selected
		LEFT JOIN source_work_variant_identity identity ON identity.media_variant_id=selected.media_variant_id
		LEFT JOIN source_analysis_work work ON work.id=identity.work_id
		LEFT JOIN source_root root ON root.id=work.source_root_id
		LEFT JOIN source_location location ON location.id=work.current_location_id
		LEFT JOIN source_analysis_step probe ON probe.work_id=work.id AND probe.step='probe'
		WHERE selected.track_id=? AND selected.selected`, trackID).Scan(ctx, &rejected); err != nil {
		return fmt.Errorf("read first publication source blockers: %w", err)
	}
	if rejected.UnavailableRoots > 0 {
		*blockers = append(*blockers, "one or more selected source roots are unavailable or changed")
	}
	if rejected.StaleLocations > 0 {
		*blockers = append(*blockers, "one or more selected source inventory locations are stale")
	}
	if rejected.Unprobed > 0 {
		*blockers = append(*blockers, "one or more selected sources lack a fresh successful probe")
	}
	err := tx.NewRaw(`SELECT r.id AS root_id, l.id AS location_id, w.id AS work_id, v.id AS variant_id,
		w.configured_path, w.inventory_path, w.relative_path, l.size_bytes, l.mtime,
		v.id AS "variant__id", v.size_bytes AS "variant__size_bytes", v.analysis_policy_version AS "variant__analysis_policy_version",
		v.ffprobe_version AS "variant__ffprobe_version", v.ffprobe_json AS "variant__ffprobe_json", v.observed_tags AS "variant__observed_tags",
		v.inspected_at AS "variant__inspected_at", v.applied_operation_id AS "variant__applied_operation_id", v.audio_stream_count AS "variant__audio_stream_count"
		FROM track_source ts JOIN media_variant v ON v.id=ts.media_variant_id
		JOIN source_work_variant_identity i ON i.media_variant_id=v.id JOIN source_analysis_work w ON w.id=i.work_id
		JOIN source_location l ON l.id=w.current_location_id AND l.id=w.location_id AND l.source_root_id=w.source_root_id
		JOIN source_root r ON r.id=w.source_root_id
		JOIN source_analysis_step p ON p.work_id=w.id AND p.step='probe' AND p.state='succeeded' AND p.success_probe_variant_id=v.id
		WHERE ts.track_id=? AND ts.selected AND r.enabled AND r.status='available' AND r.inventory_path=w.inventory_path
		AND r.configured_path=w.configured_path AND l.probe_status='audio' AND v.size_bytes=w.size_bytes
		AND v.analysis_policy_version IS NOT NULL AND v.ffprobe_version IS NOT NULL AND v.inspected_at IS NOT NULL
		AND v.applied_operation_id IS NOT NULL AND v.audio_stream_count IS NOT NULL
		AND l.media_variant_id=v.id AND l.size_bytes=w.size_bytes AND l.mtime=w.mtime
		AND w.relative_path=l.relative_path
		ORDER BY ts.created_at, ts.id`, trackID).Scan(ctx, &rows)
	if err != nil {
		return fmt.Errorf("read first publication source candidates: %w", err)
	}
	for _, row := range rows {
		index, ok := uniquePublicationAudioIndex(row.Variant.FFProbeJSON)
		if !ok {
			*blockers = append(*blockers, fmt.Sprintf("selected source %s has multiple or invalid audio streams; explicit stream selection is required", row.LocationID))
			continue
		}
		facts, err := publicationAudioFactsFromProbe(row.Variant, index)
		if err != nil {
			*blockers = append(*blockers, fmt.Sprintf("selected source %s has unusable probe facts: %v", row.LocationID, err))
			continue
		}
		selection := PublicationSourceSelection{RootID: row.RootID, LocationID: row.LocationID, WorkID: row.WorkID, MediaVariantID: row.VariantID, ConfiguredPath: row.Configured, InventoryPath: row.Inventory, RelativePath: row.Relative, SizeBytes: row.Size, MTime: row.MTime}
		input := PublicationAudioInput{
			RootID: row.RootID, LocationID: row.LocationID, WorkID: row.WorkID, MediaVariantID: row.VariantID,
			ProbeResultID: row.VariantID, ConfiguredRootPath: row.Configured, InventoryRootPath: row.Inventory,
			RelativePath: row.Relative, SizeBytes: row.Size, MTime: row.MTime, AudioFacts: facts,
		}
		if row.Variant.AnalysisPolicyVersion != nil {
			input.AnalysisPolicyVersion = *row.Variant.AnalysisPolicyVersion
		}
		if row.Variant.FFProbeVersion != nil {
			input.FFProbeVersion = *row.Variant.FFProbeVersion
		}
		if row.Variant.AnalysisPolicyVersion == nil || row.Variant.FFProbeVersion == nil || row.Variant.InspectedAt == nil || row.Variant.AppliedOperationID == nil || row.Variant.SizeBytes != row.Size {
			*blockers = append(*blockers, fmt.Sprintf("selected source %s has stale or incomplete probe provenance", row.LocationID))
			continue
		}
		*result = append(*result, FirstPublicationSource{Selection: selection, Input: input, Quality: facts})
	}
	if len(*result) == 0 && len(*blockers) == 0 {
		*blockers = append(*blockers, "no selected source has a current inventory identity and successful probe")
	}
	return nil
}

func uniquePublicationAudioIndex(raw json.RawMessage) (int, bool) {
	var probe struct {
		Streams []struct {
			Index json.Number `json:"index"`
			Type  string      `json:"codec_type"`
		} `json:"streams"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if decoder.Decode(&probe) != nil {
		return 0, false
	}
	index := -1
	for _, stream := range probe.Streams {
		if stream.Type != "audio" {
			continue
		}
		if index >= 0 {
			return 0, false
		}
		n, err := stream.Index.Int64()
		if err != nil || n < 0 || int64(int(n)) != n {
			return 0, false
		}
		index = int(n)
	}
	return index, index >= 0
}

func firstPublicationIntPointer(value int) *int { return &value }
