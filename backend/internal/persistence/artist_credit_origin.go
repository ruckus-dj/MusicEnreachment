package persistence

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

const (
	artistCreditOriginProviderPosition  = "provider_position"
	artistCreditOriginRecordingFallback = "recording_fallback"
	artistCreditOriginManualMatching    = "manual_matching"
)

func writeArtistCreditOrigin(ctx context.Context, tx bun.IDB, trackID uuid.UUID, kind string) error {
	if kind != artistCreditOriginProviderPosition && kind != artistCreditOriginRecordingFallback && kind != artistCreditOriginManualMatching {
		return fmt.Errorf("invalid artist credit origin %q", kind)
	}
	if _, err := tx.NewRaw(`UPDATE local_track SET metadata=jsonb_set(COALESCE(metadata,'{}'::jsonb),'{artist_credit_origin}',jsonb_build_object('version',1,'kind',?),true) WHERE id=?`, kind, trackID).Exec(ctx); err != nil {
		return fmt.Errorf("write materialized artist credit origin: %w", err)
	}
	return nil
}
