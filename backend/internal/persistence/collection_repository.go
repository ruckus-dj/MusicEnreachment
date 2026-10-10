package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type CollectionRepository struct {
	db *bun.DB
}

type ProviderIdentityLink struct {
	ProviderID       uuid.UUID
	ProviderEntityID uuid.UUID
	LocalEntityID    uuid.UUID
	Method           string
	PolicyVersion    *string
}

func NewCollectionRepository(db *bun.DB) *CollectionRepository {
	return &CollectionRepository{db: db}
}

// UpsertTrackSourceDecision persists an explicit track/variant association.
// Automatic decisions cannot replace a manual decision; selection remains false
// unless the caller explicitly asks to select this row.
func (repository *CollectionRepository) UpsertTrackSourceDecision(ctx context.Context, decision TrackSourceDecision) error {
	if decision.TrackID == uuid.Nil || decision.MediaVariantID == uuid.Nil {
		return fmt.Errorf("upsert track source: track and media variant are required")
	}
	if decision.ID == uuid.Nil {
		decision.ID = uuid.New()
	}
	if err := validateCollectionDecision(decision.Method, decision.PolicyVersion); err != nil {
		return fmt.Errorf("upsert track source: %w", err)
	}
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("upsert track source: lock source mutation gate: %w", err)
		}
		var existing TrackSource
		err := tx.NewSelect().Model(&existing).
			Where("track_id = ? AND media_variant_id = ?", decision.TrackID, decision.MediaVariantID).
			For("UPDATE").Scan(ctx)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("upsert track source: read existing decision: %w", err)
		}
		if err == nil && existing.DecisionMethod == "manual" && decision.Method != "manual" {
			return fmt.Errorf("upsert track source: automatic decision cannot replace manual decision")
		}
		row := TrackSource{ID: decision.ID, TrackID: decision.TrackID, MediaVariantID: decision.MediaVariantID,
			Selected: decision.Selected, Compatible: decision.Compatible, DecisionMethod: decision.Method,
			PolicyVersion: decision.PolicyVersion, Metadata: decision.Metadata, UpdatedAt: time.Now().UTC()}
		if err == nil {
			row.ID = existing.ID
			row.CreatedAt = existing.CreatedAt
			_, err = tx.NewUpdate().Model(&row).Column("selected", "compatible", "decision_method", "policy_version", "metadata", "updated_at").
				Where("id = ?", existing.ID).Exec(ctx)
		} else {
			row.CreatedAt = row.UpdatedAt
			_, err = tx.NewInsert().Model(&row).Exec(ctx)
		}
		if err != nil {
			return fmt.Errorf("upsert track source: write decision: %w", err)
		}
		return nil
	})
}

// RemoveTrackSourceDecision deletes an association only when the caller's
// decision authority permits it. Manual rows require a manual removal.
func (repository *CollectionRepository) RemoveTrackSourceDecision(ctx context.Context, trackID, mediaVariantID uuid.UUID, method string) error {
	if trackID == uuid.Nil || mediaVariantID == uuid.Nil {
		return fmt.Errorf("remove track source: track and media variant are required")
	}
	if method != "manual" && method != "automatic" {
		return fmt.Errorf("remove track source: method must be manual or automatic")
	}
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("remove track source: lock source mutation gate: %w", err)
		}
		var existing TrackSource
		err := tx.NewSelect().Model(&existing).
			Where("track_id = ? AND media_variant_id = ?", trackID, mediaVariantID).
			For("UPDATE").Scan(ctx)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return fmt.Errorf("remove track source: read existing decision: %w", err)
		}
		if existing.DecisionMethod == "manual" && method != "manual" {
			return fmt.Errorf("remove track source: automatic removal cannot remove manual decision")
		}
		if _, err := tx.NewDelete().Model(&existing).Where("id = ?", existing.ID).Exec(ctx); err != nil {
			return fmt.Errorf("remove track source: delete decision: %w", err)
		}
		return nil
	})
}

// UpsertProviderIdentityLink writes one provider-scoped identity decision.
// entityKind is restricted to the four local/provider entity pairs represented
// by this schema; provider and provider-entity IDs are never rewritten.
func (repository *CollectionRepository) UpsertProviderIdentityLink(ctx context.Context, entityKind string, link ProviderIdentityLink) error {
	table, providerColumn, localColumn, err := providerLinkTable(entityKind)
	if err != nil {
		return err
	}
	if link.ProviderID == uuid.Nil || link.ProviderEntityID == uuid.Nil || link.LocalEntityID == uuid.Nil {
		return fmt.Errorf("upsert provider identity link: provider and entity IDs are required")
	}
	if err := validateCollectionDecision(link.Method, link.PolicyVersion); err != nil {
		return fmt.Errorf("upsert provider identity link: %w", err)
	}
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("upsert provider identity link: lock source mutation gate: %w", err)
		}
		var existingMethod string
		query := fmt.Sprintf(`SELECT method FROM %s WHERE provider_id=? AND %s=? FOR UPDATE`, table, providerColumn)
		err := tx.NewRaw(query, link.ProviderID, link.ProviderEntityID).Scan(ctx, &existingMethod)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("upsert provider identity link: read existing decision: %w", err)
		}
		if err == nil && existingMethod == "manual" && link.Method != "manual" {
			return fmt.Errorf("upsert provider identity link: automatic decision cannot replace manual decision")
		}
		write := fmt.Sprintf(`INSERT INTO %s (provider_id,%s,%s,method,policy_version) VALUES (?,?,?,?,?)
			ON CONFLICT (provider_id,%s) DO UPDATE SET %s=EXCLUDED.%s,method=EXCLUDED.method,policy_version=EXCLUDED.policy_version
			WHERE %s.method <> 'manual' OR EXCLUDED.method = 'manual'`,
			table, providerColumn, localColumn, providerColumn, localColumn, localColumn, table)
		result, err := tx.ExecContext(ctx, write, link.ProviderID, link.ProviderEntityID, link.LocalEntityID, link.Method, link.PolicyVersion)
		if err != nil {
			return fmt.Errorf("upsert provider identity link: write decision: %w", err)
		}
		if affected, err := result.RowsAffected(); err != nil {
			return fmt.Errorf("upsert provider identity link: check write result: %w", err)
		} else if affected == 0 && link.Method != "manual" {
			return fmt.Errorf("upsert provider identity link: automatic decision cannot replace manual decision")
		}
		return nil
	})
}

// RemoveProviderIdentityLink refuses automatic removal of a manual identity.
func (repository *CollectionRepository) RemoveProviderIdentityLink(ctx context.Context, entityKind string, providerID, providerEntityID uuid.UUID, method string) error {
	table, providerColumn, _, err := providerLinkTable(entityKind)
	if err != nil {
		return err
	}
	if providerID == uuid.Nil || providerEntityID == uuid.Nil {
		return fmt.Errorf("remove provider identity link: provider and entity IDs are required")
	}
	if method != "manual" && method != "automatic" {
		return fmt.Errorf("remove provider identity link: method must be manual or automatic")
	}
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("remove provider identity link: lock source mutation gate: %w", err)
		}
		var existingMethod string
		query := fmt.Sprintf(`SELECT method FROM %s WHERE provider_id=? AND %s=? FOR UPDATE`, table, providerColumn)
		err := tx.NewRaw(query, providerID, providerEntityID).Scan(ctx, &existingMethod)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return fmt.Errorf("remove provider identity link: read existing decision: %w", err)
		}
		if existingMethod == "manual" && method != "manual" {
			return fmt.Errorf("remove provider identity link: automatic removal cannot remove manual decision")
		}
		deleteQuery := fmt.Sprintf(`DELETE FROM %s WHERE provider_id=? AND %s=?`, table, providerColumn)
		if _, err := tx.ExecContext(ctx, deleteQuery, providerID, providerEntityID); err != nil {
			return fmt.Errorf("remove provider identity link: delete decision: %w", err)
		}
		return nil
	})
}

func providerLinkTable(entityKind string) (table, providerColumn, localColumn string, err error) {
	switch entityKind {
	case "artist":
		return "provider_local_artist", "provider_artist_id", "local_artist_id", nil
	case "release":
		return "provider_local_release", "provider_release_id", "local_release_id", nil
	case "recording":
		return "provider_local_recording", "provider_recording_id", "local_recording_id", nil
	case "track":
		return "provider_local_track", "provider_track_id", "local_track_id", nil
	default:
		return "", "", "", fmt.Errorf("provider identity link: unsupported entity kind %q", entityKind)
	}
}

func validateCollectionDecision(method string, policyVersion *string) error {
	switch method {
	case "manual":
		if policyVersion != nil {
			return fmt.Errorf("manual decision must not have a policy version")
		}
	case "automatic":
		// The policy version is an opaque algorithm token owned by the caller;
		// the repository only requires that an automatic decision carries one.
		if policyVersion == nil || strings.TrimSpace(*policyVersion) == "" {
			return fmt.Errorf("automatic decision requires a non-blank policy version")
		}
	default:
		return fmt.Errorf("method must be manual or automatic")
	}
	return nil
}
