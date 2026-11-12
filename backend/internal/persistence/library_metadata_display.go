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

func readProviderReleaseIdentifierBaseline(ctx context.Context, tx bun.Tx, ownerID uuid.UUID) (*ProviderReleaseIdentifierBaseline, error) {
	source, found, err := catalogMusicBrainzSource(ctx, tx)
	if err != nil || !found {
		return nil, err
	}
	var row struct {
		ProviderKey string          `bun:"provider_key"`
		Fields      json.RawMessage `bun:"fields"`
		Evidence    json.RawMessage `bun:"evidence"`
	}
	err = tx.NewRaw(`SELECT projection.provider_key,entity.fields,entity.field_evidence AS evidence
		FROM provider_local_release binding
		JOIN provider_release projection ON projection.id=binding.provider_release_id AND projection.provider_id=binding.provider_id
		JOIN provider_source_entity entity ON entity.provider_id=binding.provider_id AND entity.provider_source_id=? AND entity.entity_kind='release' AND entity.provider_key=projection.provider_key
		WHERE binding.provider_id=? AND binding.local_release_id=?`, source.ID, source.ProviderID, ownerID).Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read accepted provider release identifiers: %w", err)
	}
	fields, err := decodeFrozenObject(row.Fields)
	if err != nil {
		return nil, fmt.Errorf("decode accepted provider release identifier fields: %w", err)
	}
	var rawEvidence map[string]json.RawMessage
	if err := json.Unmarshal(row.Evidence, &rawEvidence); err != nil || rawEvidence == nil {
		return nil, errors.New("invalid accepted provider release identifier evidence")
	}
	result := &ProviderReleaseIdentifierBaseline{ProviderCode: "musicbrainz", SourceID: source.ID, ProviderKey: row.ProviderKey, Evidence: make(map[string]ProviderFieldEvidence)}
	if raw, present := rawEvidence["asin"]; present {
		evidence, err := acceptedReleaseIdentifierEvidence(raw)
		if err != nil {
			return nil, fmt.Errorf("accepted provider ASIN evidence: %w", err)
		}
		result.ASIN, err = decodeNonemptyAcceptedString(fields["asin"])
		if err != nil {
			return nil, fmt.Errorf("decode accepted provider ASIN: %w", err)
		}
		if result.ASIN != nil {
			result.Evidence["asin"] = evidence
		}
	}
	if raw, present := rawEvidence["text-representation"]; present {
		evidence, err := acceptedReleaseIdentifierEvidence(raw)
		if err != nil {
			return nil, fmt.Errorf("accepted provider script evidence: %w", err)
		}
		result.Script, err = acceptedReleaseScript(fields["text-representation"])
		if err != nil {
			return nil, fmt.Errorf("decode accepted provider script: %w", err)
		}
		if result.Script != nil {
			result.Evidence["text-representation"] = evidence
		}
	}
	if result.ASIN == nil && result.Script == nil {
		return nil, nil
	}
	identity := struct {
		Version               int                              `json:"version"`
		Owner                 uuid.UUID                        `json:"owner"`
		Source                uuid.UUID                        `json:"source"`
		ConfigurationIdentity string                           `json:"configuration_identity"`
		Provider              string                           `json:"provider"`
		Key                   string                           `json:"key"`
		ASIN                  *string                          `json:"asin,omitempty"`
		Script                *string                          `json:"script,omitempty"`
		Evidence              map[string]ProviderFieldEvidence `json:"evidence"`
	}{1, ownerID, source.ID, source.ConfigurationIdentity, "musicbrainz", row.ProviderKey, result.ASIN, result.Script, result.Evidence}
	encoded, err := json.Marshal(identity)
	if err != nil {
		return nil, fmt.Errorf("encode provider release identifier baseline identity: %w", err)
	}
	digest := sha256.Sum256(encoded)
	result.Digest = hex.EncodeToString(digest[:])
	return result, nil
}

func acceptedReleaseIdentifierEvidence(raw json.RawMessage) (ProviderFieldEvidence, error) {
	var object map[string]json.RawMessage
	if len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &object) != nil || object == nil {
		return ProviderFieldEvidence{}, errors.New("expected an evidence object")
	}
	var result ProviderFieldEvidence
	if err := strictNonnegativeInteger(object, "authority", &result.Authority); err != nil {
		return ProviderFieldEvidence{}, err
	}
	if err := strictNonnegativeInteger(object, "fetch_order", &result.FetchOrder); err != nil {
		return ProviderFieldEvidence{}, err
	}
	return result, nil
}

func decodeNonemptyAcceptedString(raw json.RawMessage) (*string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, errors.New("expected a string or null")
	}
	if value == "" {
		return nil, nil
	}
	return &value, nil
}

func acceptedReleaseScript(raw json.RawMessage) (*string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var representation map[string]json.RawMessage
	if err := json.Unmarshal(raw, &representation); err != nil || representation == nil {
		return nil, errors.New("expected an object or null")
	}
	return decodeNonemptyAcceptedString(representation["script"])
}

func strictNonnegativeInteger(object map[string]json.RawMessage, name string, target any) error {
	raw, ok := object[name]
	if !ok || string(raw) == "null" {
		return fmt.Errorf("accepted provider field evidence is missing %s", name)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("accepted provider field evidence has invalid %s", name)
	}
	if number, ok := target.(*int); ok && *number < 0 {
		return fmt.Errorf("accepted provider field evidence has negative %s", name)
	}
	if number, ok := target.(*int64); ok && *number < 0 {
		return fmt.Errorf("accepted provider field evidence has negative %s", name)
	}
	return nil
}
