package persistence

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type manualTrackOverrides struct {
	TitleOverride   *string
	ArtistOverrides *[]string
}

// manualOverridesByTrack resolves per-member values into one explicit override
// per provider track. A nil pointer means "not supplied"; pointers to "" or an
// empty slice remain explicit values. Conflicts are rejected by
// validateManualAssignments before this resolver is used.
func manualOverridesByTrack(assignments []MatchingManualAssignment) map[uuid.UUID]manualTrackOverrides {
	result := make(map[uuid.UUID]manualTrackOverrides)
	for _, assignment := range assignments {
		resolved := result[assignment.ProviderTrackID]
		if resolved.TitleOverride == nil && assignment.TitleOverride != nil {
			resolved.TitleOverride = cloneManualString(assignment.TitleOverride)
		}
		if resolved.ArtistOverrides == nil && assignment.ArtistOverrides != nil {
			resolved.ArtistOverrides = cloneManualStrings(assignment.ArtistOverrides)
		}
		result[assignment.ProviderTrackID] = resolved
	}
	return result
}

func applyManualTrackOverrides(ctx context.Context, tx bun.Tx, trackID uuid.UUID, title *string, artists *[]string) error {
	if title == nil && artists == nil {
		return nil
	}
	if title != nil {
		if _, err := tx.NewRaw(`UPDATE local_track SET title=? WHERE id=?`, *title, trackID).Exec(ctx); err != nil {
			return fmt.Errorf("manual matching: apply track title override: %w", err)
		}
	}
	if artists != nil {
		if _, err := tx.NewRaw(`DELETE FROM track_artist WHERE track_id=?`, trackID).Exec(ctx); err != nil {
			return fmt.Errorf("manual matching: replace track artist overrides: %w", err)
		}
		values := make([]MatchingLocalArtist, 0, len(*artists))
		for _, name := range *artists {
			values = append(values, MatchingLocalArtist{Name: name})
		}
		if err := insertLocalCredits(ctx, tx, trackID, "track_artist", values); err != nil {
			return fmt.Errorf("manual matching: insert track artist overrides: %w", err)
		}
	}
	provenance := map[string]any{"authority": "manual", "method": "manual_matching"}
	if title != nil {
		provenance["title_override"] = *title
	}
	if artists != nil {
		provenance["artist_overrides"] = *artists
	}
	encoded, err := json.Marshal(provenance)
	if err != nil {
		return err
	}
	if _, err := tx.NewRaw(`UPDATE local_track SET metadata=COALESCE(metadata,'{}'::jsonb) || jsonb_build_object('manual_matching_override', COALESCE(metadata->'manual_matching_override','{}'::jsonb) || ?::jsonb) WHERE id=?`, string(encoded), trackID).Exec(ctx); err != nil {
		return fmt.Errorf("manual matching: record track override provenance: %w", err)
	}
	return nil
}
