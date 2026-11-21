package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

const MusicBrainzUnavailableStage = "provider_temporarily_unavailable"

// currentMatchingMusicBrainzOutage identifies the failed automatic run that
// owns this group's request. Only terminal failures of registered MusicBrainz
// deliveries can establish an outage, not missing configuration or empty hits.
func currentMatchingMusicBrainzOutage(ctx context.Context, db bun.IDB, groupID uuid.UUID) (uuid.UUID, error) {
	var runID uuid.UUID
	err := db.NewRaw(`SELECT run.id
		FROM incoming_matching_generation generation
		JOIN matching_run run ON run.id=generation.current_run_id
		JOIN matching_run_target target ON target.run_id=run.id AND target.group_id=generation.group_id
		JOIN matching_acquisition acquisition ON acquisition.run_id=run.id AND acquisition.intent_version IN (2,3)
		JOIN operation parent ON parent.id=run.operation_id
		JOIN incoming_group groups ON groups.id=generation.group_id
		JOIN app_setting config ON config.setting_name='musicbrainz_config_identity'
			AND config.setting_value=run.provider_configuration_identity
		LEFT JOIN incoming_group_matching_authority authority ON authority.group_id=groups.id
		WHERE groups.id=? AND NOT groups.manual
			AND COALESCE(authority.mode,'automatic')='automatic'
			AND COALESCE(authority.revision,0)=target.authority_revision
			AND groups.revision=target.captured_group_revision
			AND generation.high_water=target.target_generation
			AND run.immutable_plan->>'mode'='automatic'
			AND run.state='failed' AND parent.state='failed' AND run.quiesced_at IS NULL
			AND EXISTS (
				SELECT 1 FROM matching_provider_dependency dependency
				JOIN operation child ON child.id=dependency.request_id
				WHERE dependency.parent_run_id=run.id
					AND dependency.terminal_outcome='failed'
					AND child.state='failed' AND child.stage=?
					AND child.provider_source_id=run.provider_source_id
					AND child.provider_configuration_identity=run.provider_configuration_identity
			)
			AND NOT EXISTS (
				SELECT 1 FROM matching_provider_dependency dependency
				JOIN operation child ON child.id=dependency.request_id
				WHERE dependency.parent_run_id=run.id
					AND dependency.terminal_outcome='failed'
					AND (child.stage IS DISTINCT FROM ? OR child.state IS DISTINCT FROM 'failed')
			)`, groupID, MusicBrainzUnavailableStage, MusicBrainzUnavailableStage).Scan(ctx, &runID)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("read current MusicBrainz matching outage: %w", err)
	}
	return runID, nil
}

func (repository *IncomingMatchCandidateRepository) CurrentMatchingMusicBrainzOutage(ctx context.Context, groupID uuid.UUID) (uuid.UUID, error) {
	return currentMatchingMusicBrainzOutage(ctx, repository.db, groupID)
}
