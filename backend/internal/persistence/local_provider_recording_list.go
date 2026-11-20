package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// readListedRecordingCredits fills the credits of the recording rows on the
// current Local catalog page once.
//
// Local recording credits are stored in provider_recording.metadata as an
// ordered array, so their order, credit names, and join phrases are
// authoritative; only the artist identity is read from provider_artist/provider.
//
// External recordings use the configured MusicBrainz source's accepted
// projection in provider_source_entity. Its caption is independent of whether
// every credited artist has a stored catalog identity.
func (r *LocalProviderRepository) readListedRecordingCredits(ctx context.Context, items []LocalProviderItem) error {
	if err := r.readListedLocalRecordingCredits(ctx, items); err != nil {
		return err
	}
	return r.readListedExternalRecordingCredits(ctx, items)
}

func (r *LocalProviderRepository) readListedLocalRecordingCredits(ctx context.Context, items []LocalProviderItem) error {
	ids := make([]uuid.UUID, 0, len(items))
	seen := make(map[uuid.UUID]struct{}, len(items))
	indexes := make([]int, 0, len(items))
	for i := range items {
		if items[i].Kind != "recording" || items[i].ProviderCode != "local" || len(items[i].Credits) == 0 {
			continue
		}
		indexes = append(indexes, i)
		for _, credit := range items[i].Credits {
			if credit.ArtistID == uuid.Nil {
				continue
			}
			if _, ok := seen[credit.ArtistID]; ok {
				continue
			}
			seen[credit.ArtistID] = struct{}{}
			ids = append(ids, credit.ArtistID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	type artistIdentity struct {
		ProviderID   uuid.UUID
		ProviderCode string
		Name         string
	}
	identities := make(map[uuid.UUID]artistIdentity, len(ids))
	rows, err := r.db.QueryContext(ctx, `SELECT a.id,a.provider_id,p.code,a.name FROM provider_artist a JOIN provider p ON p.id=a.provider_id WHERE a.id IN (?)`, bun.List(ids))
	if err != nil {
		return fmt.Errorf("read listed recording credit artists: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id uuid.UUID
		var identity artistIdentity
		if err := rows.Scan(&id, &identity.ProviderID, &identity.ProviderCode, &identity.Name); err != nil {
			return fmt.Errorf("scan listed recording credit artist: %w", err)
		}
		identities[id] = identity
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate listed recording credit artists: %w", err)
	}
	for _, index := range indexes {
		for c := range items[index].Credits {
			identity, ok := identities[items[index].Credits[c].ArtistID]
			if !ok {
				// A dangling metadata reference keeps the stored credit text but
				// cannot supply an artist identity from the provider catalog.
				continue
			}
			items[index].Credits[c].ProviderID = identity.ProviderID
			items[index].Credits[c].ProviderCode = identity.ProviderCode
			items[index].Credits[c].ArtistName = identity.Name
		}
		items[index].ArtistCredit = localCatalogCreditCaption(items[index].Credits)
	}
	return nil
}

type listedExternalCredit struct {
	Name       string `json:"name"`
	JoinPhrase string `json:"joinphrase"`
	Artist     struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"artist"`
}

func (r *LocalProviderRepository) readListedExternalRecordingCredits(ctx context.Context, items []LocalProviderItem) error {
	indexes := make([]int, 0, len(items))
	for i := range items {
		if items[i].Kind == "recording" && items[i].ProviderCode == "musicbrainz" {
			indexes = append(indexes, i)
		}
	}
	if len(indexes) == 0 {
		return nil
	}
	source, found, err := catalogMusicBrainzSource(ctx, r.db)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	positions := make(map[uuid.UUID]int, len(indexes))
	recordingIDs := make([]uuid.UUID, 0, len(indexes))
	for _, index := range indexes {
		if items[index].ProviderID != source.ProviderID {
			continue
		}
		positions[items[index].ID] = index
		recordingIDs = append(recordingIDs, items[index].ID)
	}
	if len(recordingIDs) == 0 {
		return nil
	}
	type projection struct {
		RecordingID uuid.UUID `bun:"recording_id"`
		Credits     string    `bun:"credits"`
		Evidence    string    `bun:"evidence"`
	}
	var projections []projection
	err = r.db.NewRaw(`SELECT r.id AS recording_id,
		COALESCE((e.fields->'artist-credit')::text,'null') AS credits,
		COALESCE((e.field_evidence->'artist-credit')::text,'null') AS evidence
		FROM provider_recording r
		JOIN provider_source_entity e ON e.provider_id=r.provider_id AND e.provider_key=r.provider_key AND e.provider_source_id=? AND e.entity_kind='recording'
		WHERE r.provider_id=? AND r.id IN (?)`, source.ID, source.ProviderID, bun.List(recordingIDs)).Scan(ctx, &projections)
	if err != nil {
		return fmt.Errorf("read listed recording credit projections: %w", err)
	}
	parsed := make(map[uuid.UUID][]listedExternalCredit, len(projections))
	keys := make([]string, 0, len(projections))
	seenKeys := make(map[string]struct{}, len(projections))
	for _, projection := range projections {
		var evidence *ProviderFieldEvidence
		if err := json.Unmarshal([]byte(projection.Evidence), &evidence); err != nil {
			return fmt.Errorf("decode listed recording field evidence: %w", err)
		}
		if evidence == nil {
			continue
		}
		var credits []listedExternalCredit
		if err := json.Unmarshal([]byte(projection.Credits), &credits); err != nil {
			return fmt.Errorf("decode listed recording credits: %w", err)
		}
		parsed[projection.RecordingID] = credits
		for _, credit := range credits {
			if credit.Name != "" || credit.Artist.Name != "" {
				continue
			}
			artistID, err := uuid.Parse(credit.Artist.ID)
			if err != nil {
				continue
			}
			key := artistID.String()
			if _, ok := seenKeys[key]; ok {
				continue
			}
			seenKeys[key] = struct{}{}
			keys = append(keys, key)
		}
	}
	names := make(map[string]string, len(keys))
	if len(keys) > 0 {
		rows, err := r.db.QueryContext(ctx, `SELECT provider_key,name FROM provider_artist WHERE provider_id=? AND provider_key IN (?)`, source.ProviderID, bun.List(keys))
		if err != nil {
			return fmt.Errorf("read listed recording credit provider artists: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var key, name string
			if err := rows.Scan(&key, &name); err != nil {
				return fmt.Errorf("scan listed recording credit provider artist: %w", err)
			}
			names[key] = name
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate listed recording credit provider artists: %w", err)
		}
	}
	for recordingID, credits := range parsed {
		index, ok := positions[recordingID]
		if !ok {
			continue
		}
		var caption strings.Builder
		for _, credit := range credits {
			name := credit.Name
			if name == "" {
				name = credit.Artist.Name
			}
			if name == "" {
				if id, err := uuid.Parse(credit.Artist.ID); err == nil {
					name = names[id.String()]
				}
			}
			caption.WriteString(name)
			caption.WriteString(credit.JoinPhrase)
		}
		items[index].ArtistCredit = caption.String()
	}
	return nil
}
