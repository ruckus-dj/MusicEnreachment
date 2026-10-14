package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// materializeFrozenManualArtistCredits uses only the accepted-projection values
// frozen into the manual proposal. Database reads here resolve provider
// identities and existing local mappings; mutable provider-source projections
// are deliberately not consulted.
func materializeFrozenManualArtistCredits(ctx context.Context, tx bun.Tx, providerID, sourceID, parentID uuid.UUID, relation string, credits []FrozenArtistCredit, projectionRevision string) error {
	parentColumn := map[string]string{"release_artist": "release_id", "recording_artist": "recording_id", "track_artist": "track_id"}[relation]
	if parentColumn == "" {
		return fmt.Errorf("manual matching: invalid frozen artist relation")
	}
	for _, credit := range credits {
		providerKey := strings.ToLower(strings.TrimSpace(credit.ArtistProviderKey))
		if providerKey == "" {
			return ErrIncomingMatchConflict
		}
		var providerArtistID uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM provider_artist WHERE provider_id=? AND provider_key=?`, providerID, providerKey).Scan(ctx, &providerArtistID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrIncomingMatchConflict
			}
			return fmt.Errorf("manual matching: resolve frozen artist identity: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext(?))`, "frozen-apply-artist:"+providerID.String()+":"+providerArtistID.String()); err != nil {
			return fmt.Errorf("manual matching: lock frozen artist identity: %w", err)
		}

		var localArtistID uuid.UUID
		err := tx.NewRaw(`SELECT local_artist_id FROM provider_local_artist WHERE provider_id=? AND provider_artist_id=? FOR UPDATE`, providerID, providerArtistID).Scan(ctx, &localArtistID)
		if errors.Is(err, sql.ErrNoRows) {
			canonicalName := credit.ArtistName
			nameSource := credit.ArtistNameSource
			if strings.TrimSpace(canonicalName) == "" {
				canonicalName = credit.Name
				nameSource = nil
			}
			if strings.TrimSpace(canonicalName) == "" {
				return ErrIncomingMatchConflict
			}

			sourceKind, sourceKey, field := "release", providerKey, "artist-credit"
			var evidence ProviderFieldEvidence
			if nameSource != nil {
				sourceKind, sourceKey, field = nameSource.EntityKind, nameSource.ProviderKey, nameSource.Field
				evidence = ProviderFieldEvidence{Authority: nameSource.Authority, FetchOrder: nameSource.FetchOrder}
			} else if credit.Source != nil {
				sourceKind, sourceKey, field = credit.Source.EntityKind, credit.Source.ProviderKey, credit.Source.Field
				evidence = ProviderFieldEvidence{Authority: credit.Source.Authority, FetchOrder: credit.Source.FetchOrder}
			}
			if sourceKind == "" {
				sourceKind = "release"
			}
			if sourceKey == "" {
				sourceKey = providerKey
			}
			if field == "" {
				field = "artist-credit"
			}
			metadata, err := json.Marshal(map[string]any{"manual_matching": map[string]any{
				"source_id": sourceID, "provider_id": providerID, "provider_artist_id": providerArtistID,
				"projection_revision": projectionRevision, "policy_version": nil,
				"source_entity_id": sourceKey, "field_evidence": map[string]ProviderFieldEvidence{field: evidence},
				"name_source": field, "provider_fields_reference": map[string]any{
					"entity_kind": sourceKind, "provider_key": sourceKey, "fields": []string{field},
				},
			}})
			if err != nil {
				return fmt.Errorf("manual matching: encode frozen artist provenance: %w", err)
			}
			localArtistID = uuid.New()
			if _, err := tx.NewRaw(`INSERT INTO local_artist(id,name,metadata) VALUES (?,?,?::jsonb)`, localArtistID, canonicalName, string(metadata)).Exec(ctx); err != nil {
				return fmt.Errorf("manual matching: create frozen artist: %w", err)
			}
			if _, err := tx.NewRaw(`INSERT INTO provider_local_artist(provider_id,provider_artist_id,local_artist_id,method,policy_version) VALUES (?,?,?,'manual',NULL)`, providerID, providerArtistID, localArtistID).Exec(ctx); err != nil {
				return fmt.Errorf("manual matching: map frozen artist: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("manual matching: read frozen artist mapping: %w", err)
		}
		if _, err := tx.NewRaw(`INSERT INTO `+relation+`(`+parentColumn+`,position,artist_id,name,name_join_phrase) VALUES (?,?,?,?,?) ON CONFLICT DO NOTHING`, parentID, credit.Position, localArtistID, credit.Name, credit.JoinPhrase).Exec(ctx); err != nil {
			return fmt.Errorf("manual matching: add frozen %s credit: %w", relation, err)
		}
	}
	return nil
}
