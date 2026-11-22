package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

const MusicBrainzUnavailableStage = "provider_temporarily_unavailable"

// matchingLocalFallbackPendingStage is written on the exact failed parent
// operation that also issued the dedicated outage fallback generation. It is
// never a generic failure stage: consumers require it together with the exact
// original-generation-plus-one request.
const matchingLocalFallbackPendingStage = "local_fallback_pending"

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

// currentMatchingMusicBrainzOutageFallback identifies the failed automatic run
// that owns one outage-specific pending generation. The parent must carry the
// explicit local_fallback_pending marker written with the dedicated generation
// bump, and requestedGeneration must be exactly the run target's admission
// generation plus one. A later independent request therefore cannot consume the
// stale outage: its generation no longer matches, and ordinary reconciliation
// runs when it is requested.
func currentMatchingMusicBrainzOutageFallback(ctx context.Context, db bun.IDB, groupID uuid.UUID, requestedGeneration int64) (uuid.UUID, error) {
	if requestedGeneration < 1 {
		return uuid.Nil, nil
	}
	runID, err := currentMatchingMusicBrainzOutage(ctx, db, groupID)
	if err != nil || runID == uuid.Nil {
		return uuid.Nil, err
	}
	var evidence struct {
		Stage                    string `bun:"stage"`
		ReconciliationGeneration *int64 `bun:"reconciliation_generation"`
	}
	if err := db.NewRaw(`SELECT parent.stage, target.reconciliation_generation
		FROM matching_run run
		JOIN operation parent ON parent.id=run.operation_id
		JOIN matching_run_target target ON target.run_id=run.id AND target.group_id=?
		WHERE run.id=?`, groupID, runID).Scan(ctx, &evidence); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return uuid.Nil, nil
		}
		return uuid.Nil, fmt.Errorf("read MusicBrainz matching outage fallback evidence: %w", err)
	}
	if evidence.Stage != matchingLocalFallbackPendingStage || evidence.ReconciliationGeneration == nil ||
		*evidence.ReconciliationGeneration < 1 || *evidence.ReconciliationGeneration == math.MaxInt64 ||
		*evidence.ReconciliationGeneration+1 != requestedGeneration {
		return uuid.Nil, nil
	}
	return runID, nil
}

// CurrentMatchingMusicBrainzOutageFallback returns the failed automatic run that
// owns this exact outage-specific request generation, or uuid.Nil when the
// group's pending generation is not the dedicated fallback generation.
func (repository *IncomingMatchCandidateRepository) CurrentMatchingMusicBrainzOutageFallback(ctx context.Context, groupID uuid.UUID, requestedGeneration int64) (uuid.UUID, error) {
	if repository == nil || repository.db == nil {
		return uuid.Nil, errors.New("matching outage fallback evidence is unavailable")
	}
	return currentMatchingMusicBrainzOutageFallback(ctx, repository.db, groupID, requestedGeneration)
}
