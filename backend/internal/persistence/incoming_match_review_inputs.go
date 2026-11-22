package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// ReadMatchingReviewInputs reads the exact saved capture set and provider
// evidence referenced by one current evaluation revision. Provider projection is
// optional: an obsolete configuration or unavailable accepted graph returns the
// captures with no derived provider snapshot.
func (repository *IncomingMatchCandidateRepository) ReadMatchingReviewInputs(
	ctx context.Context,
	groupID uuid.UUID,
	revision string,
	releases []string,
	cacheKeys []string,
) ([]IncomingGroupingCapture, ProviderMatchingSnapshot, error) {
	var captures []IncomingGroupingCapture
	var snapshot ProviderMatchingSnapshot
	if repository == nil || groupID == uuid.Nil || revision == "" {
		return nil, snapshot, errors.New("matching review inputs require a group and evaluation revision")
	}
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		var header struct {
			ProviderSourceID              uuid.UUID `bun:"provider_source_id"`
			ProviderConfigurationIdentity string    `bun:"provider_configuration_identity"`
			ProviderEndpoint              string    `bun:"provider_endpoint"`
		}
		err := tx.NewRaw(`SELECT provider_source_id, provider_configuration_identity, provider_endpoint
			FROM incoming_match_evaluation WHERE group_id=? AND evaluation_revision=?`, groupID, revision).Scan(ctx, &header)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrIncomingMatchConflict
		}
		if err != nil {
			return fmt.Errorf("read matching review inputs: read evaluation header: %w", err)
		}
		locationIDs := make([]uuid.UUID, 0)
		if err := tx.NewRaw(`SELECT member_location.location_id
			FROM incoming_group_member_location member_location
			JOIN incoming_group_member member ON member.group_id=member_location.group_id AND member.variant_id=member_location.variant_id
			WHERE member_location.group_id=? AND member.ready
			ORDER BY member_location.location_id`, groupID).Scan(ctx, &locationIDs); err != nil {
			return fmt.Errorf("read matching review inputs: read member locations: %w", err)
		}
		captures = make([]IncomingGroupingCapture, 0)
		if len(locationIDs) == 0 {
			return nil
		}
		captures, err = loadIncomingGroupingCapturesForLocations(ctx, tx, locationIDs)
		if err != nil {
			return err
		}
		if header.ProviderEndpoint == "" || header.ProviderConfigurationIdentity == "" {
			return nil
		}
		var identity string
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &identity); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("read matching review inputs: read configuration identity: %w", err)
		}
		if identity != header.ProviderConfigurationIdentity || header.ProviderSourceID == uuid.Nil {
			return nil
		}
		derived, err := readMusicBrainzMatchingSnapshot(ctx, tx, header.ProviderEndpoint, header.ProviderConfigurationIdentity, releases, cacheKeys, false)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if derived.Source.ID != header.ProviderSourceID {
			return nil
		}
		snapshot = derived
		return nil
	})
	if err != nil {
		return nil, ProviderMatchingSnapshot{}, err
	}
	return captures, snapshot, nil
}
