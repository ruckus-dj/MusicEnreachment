package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/uptrace/bun"
)

func readProviderGenreBaseline(ctx context.Context, tx bun.Tx, scope librarymetadata.Scope, ownerID uuid.UUID) (*ProviderGenreBaseline, error) {
	source, found, err := catalogMusicBrainzSource(ctx, tx)
	if err != nil || !found {
		return nil, err
	}
	query := ""
	switch scope {
	case librarymetadata.ScopeArtist:
		query = `SELECT entity.provider_key,entity.fields->'genres' AS genres,entity.field_evidence->'genres' AS evidence
			FROM provider_local_artist binding JOIN provider_artist projection ON projection.id=binding.provider_artist_id AND projection.provider_id=binding.provider_id
			JOIN provider_source_entity entity ON entity.provider_id=binding.provider_id AND entity.provider_source_id=? AND entity.entity_kind='artist' AND entity.provider_key=projection.provider_key
			WHERE binding.provider_id=? AND binding.local_artist_id=?`
	case librarymetadata.ScopeRelease:
		query = `SELECT entity.provider_key,entity.fields->'genres' AS genres,entity.field_evidence->'genres' AS evidence
			FROM provider_local_release binding JOIN provider_release projection ON projection.id=binding.provider_release_id AND projection.provider_id=binding.provider_id
			JOIN provider_source_entity entity ON entity.provider_id=binding.provider_id AND entity.provider_source_id=? AND entity.entity_kind='release' AND entity.provider_key=projection.provider_key
			WHERE binding.provider_id=? AND binding.local_release_id=?`
	case librarymetadata.ScopeRecording:
		query = `SELECT entity.provider_key,entity.fields->'genres' AS genres,entity.field_evidence->'genres' AS evidence
			FROM provider_local_recording binding JOIN provider_recording projection ON projection.id=binding.provider_recording_id AND projection.provider_id=binding.provider_id
			JOIN provider_source_entity entity ON entity.provider_id=binding.provider_id AND entity.provider_source_id=? AND entity.entity_kind='recording' AND entity.provider_key=projection.provider_key
			WHERE binding.provider_id=? AND binding.local_recording_id=?`
	default:
		return nil, nil
	}
	var row struct {
		ProviderKey string          `bun:"provider_key"`
		Genres      json.RawMessage `bun:"genres"`
		Evidence    json.RawMessage `bun:"evidence"`
	}
	err = tx.NewRaw(query, source.ID, source.ProviderID, ownerID).Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read accepted provider genre projection: %w", err)
	}
	if len(row.Evidence) == 0 {
		return nil, nil
	}
	var evidence map[string]json.RawMessage
	if err := json.Unmarshal(row.Evidence, &evidence); err != nil || evidence == nil {
		return nil, errors.New("invalid accepted provider genre evidence")
	}
	var authority int
	var fetchOrder int64
	if err := strictNonnegativeInteger(evidence, "authority", &authority); err != nil {
		return nil, err
	}
	if err := strictNonnegativeInteger(evidence, "fetch_order", &fetchOrder); err != nil {
		return nil, err
	}
	if len(row.Genres) == 0 || string(row.Genres) == "null" {
		return nil, nil
	}
	genres, err := acceptedProviderGenreReferences(row.Genres)
	if err != nil {
		return nil, fmt.Errorf("decode accepted provider genres: %w", err)
	}
	identity := struct {
		Version               int                              `json:"version"`
		Scope                 librarymetadata.Scope            `json:"scope"`
		Owner                 uuid.UUID                        `json:"owner"`
		Source                uuid.UUID                        `json:"source"`
		ConfigurationIdentity string                           `json:"configuration_identity"`
		Provider              string                           `json:"provider"`
		Key                   string                           `json:"key"`
		Authority             int                              `json:"authority"`
		FetchOrder            int64                            `json:"fetch_order"`
		Genres                []librarymetadata.GenreReference `json:"genres"`
	}{1, scope, ownerID, source.ID, source.ConfigurationIdentity, "musicbrainz", row.ProviderKey, authority, fetchOrder, genres}
	encoded, err := json.Marshal(identity)
	if err != nil {
		return nil, fmt.Errorf("encode provider genre baseline identity: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return &ProviderGenreBaseline{ProviderCode: "musicbrainz", SourceID: source.ID, ProviderKey: row.ProviderKey, Authority: authority, FetchOrder: fetchOrder, Genres: genres, Digest: hex.EncodeToString(digest[:])}, nil
}

func strictNonnegativeInteger(object map[string]json.RawMessage, name string, target any) error {
	raw, ok := object[name]
	if !ok || string(raw) == "null" {
		return fmt.Errorf("accepted provider genre evidence is missing %s", name)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("accepted provider genre evidence has invalid %s", name)
	}
	if number, ok := target.(*int); ok && *number < 0 {
		return fmt.Errorf("accepted provider genre evidence has negative %s", name)
	}
	if number, ok := target.(*int64); ok && *number < 0 {
		return fmt.Errorf("accepted provider genre evidence has negative %s", name)
	}
	return nil
}
