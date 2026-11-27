//go:build integration

package persistence_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// The former CollectionRepository had no production callers. It existed only
// to exercise the live track_source and provider_local_* tables from tests, so
// its write helpers now live here as test fixtures.

type collectionTestProviderIdentityLink struct {
	ProviderID       uuid.UUID
	ProviderEntityID uuid.UUID
	LocalEntityID    uuid.UUID
	Method           string
	PolicyVersion    *string
}

type collectionTestTrackSourceDecision struct {
	ID                     uuid.UUID
	TrackID                uuid.UUID
	MediaVariantID         uuid.UUID
	Selected               bool
	Compatible             bool
	Method                 string
	PolicyVersion          *string
	Metadata               json.RawMessage
	SourceMetadataResultID *uuid.UUID
}

type collectionTestRepository struct {
	db *bun.DB
}

func newCollectionTestRepository(db *bun.DB) *collectionTestRepository {
	return &collectionTestRepository{db: db}
}

// upsertTrackSourceDecision persists an explicit track/variant association.
// Automatic decisions cannot replace a manual decision; selection remains false
// unless the caller explicitly asks to select this row.
func (repository *collectionTestRepository) upsertTrackSourceDecision(ctx context.Context, decision collectionTestTrackSourceDecision) error {
	if decision.TrackID == uuid.Nil || decision.MediaVariantID == uuid.Nil {
		return fmt.Errorf("upsert track source: track and media variant are required")
	}
	if decision.ID == uuid.Nil {
		decision.ID = uuid.New()
	}
	if err := validateCollectionTestDecision(decision.Method, decision.PolicyVersion); err != nil {
		return fmt.Errorf("upsert track source: %w", err)
	}
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		var existing struct {
			ID             uuid.UUID `bun:"id"`
			DecisionMethod string    `bun:"decision_method"`
		}
		err := tx.NewRaw(`SELECT id,decision_method FROM track_source WHERE track_id=? AND media_variant_id=? FOR UPDATE`,
			decision.TrackID, decision.MediaVariantID).Scan(ctx, &existing)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("upsert track source: read existing decision: %w", err)
		}
		if err == nil && existing.DecisionMethod == "manual" && decision.Method != "manual" {
			return fmt.Errorf("upsert track source: automatic decision cannot replace manual decision")
		}
		metadata := decision.Metadata
		if len(metadata) == 0 {
			metadata = json.RawMessage(`{}`)
		}
		now := time.Now().UTC()
		if err == nil {
			_, err = tx.NewRaw(`UPDATE track_source SET selected=?,compatible=?,decision_method=?,policy_version=?,metadata=?::jsonb,
				source_metadata_result_id=?,updated_at=? WHERE id=?`,
				decision.Selected, decision.Compatible, decision.Method, decision.PolicyVersion, string(metadata),
				decision.SourceMetadataResultID, now, existing.ID).Exec(ctx)
		} else {
			_, err = tx.NewRaw(`INSERT INTO track_source(id,track_id,media_variant_id,selected,compatible,decision_method,policy_version,metadata,source_metadata_result_id,created_at,updated_at)
				VALUES (?,?,?,?,?,?,?,?::jsonb,?,?,?)`,
				decision.ID, decision.TrackID, decision.MediaVariantID, decision.Selected, decision.Compatible,
				decision.Method, decision.PolicyVersion, string(metadata), decision.SourceMetadataResultID, now, now).Exec(ctx)
		}
		if err != nil {
			return fmt.Errorf("upsert track source: write decision: %w", err)
		}
		return nil
	})
}

// removeTrackSourceDecision deletes an association only when the caller's
// decision authority permits it. Manual rows require a manual removal.
func (repository *collectionTestRepository) removeTrackSourceDecision(ctx context.Context, trackID, mediaVariantID uuid.UUID, method string) error {
	if trackID == uuid.Nil || mediaVariantID == uuid.Nil {
		return fmt.Errorf("remove track source: track and media variant are required")
	}
	if method != "manual" && method != "automatic" {
		return fmt.Errorf("remove track source: method must be manual or automatic")
	}
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		var existing struct {
			ID             uuid.UUID `bun:"id"`
			DecisionMethod string    `bun:"decision_method"`
		}
		err := tx.NewRaw(`SELECT id,decision_method FROM track_source WHERE track_id=? AND media_variant_id=? FOR UPDATE`,
			trackID, mediaVariantID).Scan(ctx, &existing)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return fmt.Errorf("remove track source: read existing decision: %w", err)
		}
		if existing.DecisionMethod == "manual" && method != "manual" {
			return fmt.Errorf("remove track source: automatic removal cannot remove manual decision")
		}
		if _, err := tx.NewRaw(`DELETE FROM track_source WHERE id=?`, existing.ID).Exec(ctx); err != nil {
			return fmt.Errorf("remove track source: delete decision: %w", err)
		}
		return nil
	})
}

// upsertProviderIdentityLink writes one provider-scoped identity decision.
// entityKind is restricted to the four local/provider entity pairs represented
// by this schema; provider and provider-entity IDs are never rewritten.
func (repository *collectionTestRepository) upsertProviderIdentityLink(ctx context.Context, entityKind string, link collectionTestProviderIdentityLink) error {
	table, providerColumn, localColumn, err := collectionTestProviderLinkTable(entityKind)
	if err != nil {
		return err
	}
	if link.ProviderID == uuid.Nil || link.ProviderEntityID == uuid.Nil || link.LocalEntityID == uuid.Nil {
		return fmt.Errorf("upsert provider identity link: provider and entity IDs are required")
	}
	if err := validateCollectionTestDecision(link.Method, link.PolicyVersion); err != nil {
		return fmt.Errorf("upsert provider identity link: %w", err)
	}
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
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

func collectionTestProviderLinkTable(entityKind string) (table, providerColumn, localColumn string, err error) {
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

func validateCollectionTestDecision(method string, policyVersion *string) error {
	switch method {
	case "manual":
		if policyVersion != nil {
			return fmt.Errorf("manual decision must not have a policy version")
		}
	case "automatic":
		// The policy version is an opaque algorithm token owned by the caller;
		// the fixture only requires that an automatic decision carries one.
		if policyVersion == nil || strings.TrimSpace(*policyVersion) == "" {
			return fmt.Errorf("automatic decision requires a non-blank policy version")
		}
	default:
		return fmt.Errorf("method must be manual or automatic")
	}
	return nil
}
