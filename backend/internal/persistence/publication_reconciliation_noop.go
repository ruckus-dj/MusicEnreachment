package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/uptrace/bun"
)

type PublicationReconciliationAction uint8

const (
	AudioUpgradeRequired PublicationReconciliationAction = iota + 1
	ProviderTagsRequired
	AllNoop
)

// PublicationReconciliationNoop is an ephemeral evaluation capability. It is
// never persisted; acknowledgement repeats both decisions in its transaction.
type PublicationReconciliationNoop struct {
	guard         PublicationReconciliationDeliveryGuard
	releaseID     uuid.UUID
	endpoint      string
	configuration string
	digest        string
}

type PublicationReconciliationDecision struct {
	Action PublicationReconciliationAction
	Noop   *PublicationReconciliationNoop
}

func (repository *SetupManagerRepository) EvaluatePublicationReconciliationCurrent(ctx context.Context, releaseID uuid.UUID, guard PublicationReconciliationDeliveryGuard, endpoint, configuration string) (PublicationReconciliationDecision, error) {
	var decision PublicationReconciliationDecision
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		var err error
		decision, err = repository.evaluatePublicationReconciliationCurrentTx(ctx, tx, releaseID, guard, endpoint, configuration)
		return err
	})
	if err != nil {
		return PublicationReconciliationDecision{}, err
	}
	return decision, nil
}

func (repository *SetupManagerRepository) AcknowledgePublicationReconciliationNoop(ctx context.Context, guard PublicationReconciliationDeliveryGuard, witness *PublicationReconciliationNoop) (bool, error) {
	if witness == nil || witness.guard != guard {
		return false, fmt.Errorf("acknowledge publication no-op: invalid witness")
	}
	var acknowledged bool
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		decision, err := repository.evaluatePublicationReconciliationCurrentTx(ctx, tx, witness.releaseID, guard, witness.endpoint, witness.configuration)
		if err != nil {
			return err
		}
		if decision.Action != AllNoop || decision.Noop == nil || *decision.Noop != *witness {
			return nil
		}
		acknowledged, err = repository.AcknowledgePublicationReconciliationDeliveryWith(ctx, tx, guard)
		return err
	})
	return acknowledged && err == nil, err
}

type reconciliationPosition struct {
	ReleaseID   uuid.UUID  `bun:"release_id,type:uuid"`
	TrackID     uuid.UUID  `bun:"track_id,type:uuid"`
	RecordingID *uuid.UUID `bun:"recording_id,type:uuid,nullzero"`
}

func (repository *SetupManagerRepository) evaluatePublicationReconciliationCurrentTx(ctx context.Context, tx bun.Tx, releaseID uuid.UUID, guard PublicationReconciliationDeliveryGuard, endpoint, configuration string) (PublicationReconciliationDecision, error) {
	if releaseID == uuid.Nil || guard.PositionID == uuid.Nil {
		return PublicationReconciliationDecision{}, fmt.Errorf("evaluate publication reconciliation: position and release are required")
	}
	if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
		return PublicationReconciliationDecision{}, err
	}
	if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
		return PublicationReconciliationDecision{}, err
	}
	var selector reconciliationPosition
	if err := tx.NewRaw(`SELECT release_id,track_id,recording_id FROM release_track WHERE id=?`, guard.PositionID).Scan(ctx, &selector); err != nil {
		return PublicationReconciliationDecision{}, err
	}
	if selector.ReleaseID != releaseID || selector.TrackID == uuid.Nil {
		return PublicationReconciliationDecision{}, ErrPublicationAdmissionConflict
	}
	if err := lockPublicationReconciliationSourceRoots(ctx, tx, selector.TrackID); err != nil {
		return PublicationReconciliationDecision{}, err
	}
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "musicbrainz-config"); err != nil {
		return PublicationReconciliationDecision{}, err
	}
	// Use the existing projection-before-binding prefix, including when proving
	// absence. No table locks are taken on source or local-owner tables.
	if _, err := tx.ExecContext(ctx, `LOCK TABLE provider_source_entity,provider_source_release_track,provider_source_release_artist_credit,provider_release_track,provider_release_artist_credit IN SHARE MODE`); err != nil {
		return PublicationReconciliationDecision{}, err
	}
	if _, err := tx.ExecContext(ctx, `LOCK TABLE provider_local_release,provider_local_recording,provider_local_track IN SHARE MODE`); err != nil {
		return PublicationReconciliationDecision{}, err
	}
	var activeConfiguration string
	err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &activeConfiguration)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return PublicationReconciliationDecision{}, err
	}
	var hasBindings bool
	if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM provider_local_release binding JOIN provider ON provider.id=binding.provider_id AND provider.code='musicbrainz' WHERE binding.local_release_id=?)
		OR EXISTS(SELECT 1 FROM provider_local_track binding JOIN provider ON provider.id=binding.provider_id AND provider.code='musicbrainz' WHERE binding.local_track_id=?)
		OR EXISTS(SELECT 1 FROM provider_local_recording binding JOIN provider ON provider.id=binding.provider_id AND provider.code='musicbrainz' WHERE binding.local_recording_id=?)`, releaseID, selector.TrackID, selector.RecordingID).Scan(ctx, &hasBindings); err != nil {
		return PublicationReconciliationDecision{}, err
	}
	var providerContext *PublicationProviderTagContext
	owners, err := lockReconciliationLocalOwners(ctx, tx, releaseID, guard.PositionID, selector)
	if err != nil {
		return PublicationReconciliationDecision{}, err
	}
	var ownerWitness any = owners
	var position reconciliationPosition
	if err := tx.NewRaw(`SELECT release_id,track_id,recording_id FROM release_track WHERE id=? FOR SHARE`, guard.PositionID).Scan(ctx, &position); err != nil {
		return PublicationReconciliationDecision{}, err
	}
	if !reflect.DeepEqual(position, selector) {
		return PublicationReconciliationDecision{}, ErrPublicationAdmissionConflict
	}
	var epoch uuid.UUID
	if err := tx.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &epoch); err != nil || epoch != guard.OutputEpoch {
		return PublicationReconciliationDecision{}, errors.Join(err, ErrPublicationAdmissionConflict)
	}
	var format string
	if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='publication_format' FOR SHARE`).Scan(ctx, &format); err != nil {
		return PublicationReconciliationDecision{}, err
	}
	if format != string(publication.AudioFormatSource) && format != string(publication.AudioFormatMKA) {
		return PublicationReconciliationDecision{}, fmt.Errorf("evaluate publication reconciliation: publication format is invalid")
	}
	var current TrackPublication
	if err := tx.NewSelect().Model(&current).Where("position_id=?", guard.PositionID).For("UPDATE").Scan(ctx); err != nil {
		return PublicationReconciliationDecision{}, err
	}
	if current.Epoch != epoch || current.QualityPolicyVersion != publication.QualityPolicyVersion {
		return PublicationReconciliationDecision{}, ErrPublicationAdmissionConflict
	}
	var retained bool
	if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM track_publication_attempt WHERE position_id=?)`, guard.PositionID).Scan(ctx, &retained); err != nil || retained {
		return PublicationReconciliationDecision{}, errors.Join(err, ErrPublicationAdmissionConflict)
	}
	var authority PositionSourceAuthority
	var audioRequired bool
	var audioWitness string
	var audioErr error
	if err := tx.NewSelect().Model(&authority).Where("position_id=?", guard.PositionID).For("UPDATE").Scan(ctx); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return PublicationReconciliationDecision{}, err
		}
		audioErr = fmt.Errorf("evaluate publication reconciliation: current source authority is required: %w", err)
	} else {
		audioRequired, audioWitness, audioErr = publicationReconciliationAudioUpgradeRequired(ctx, tx, position.TrackID, current, authority, publication.AudioPublicationFormat(format))
	}
	action := AllNoop
	if audioRequired {
		action = AudioUpgradeRequired
	} else if hasBindings {
		if endpoint == "" || configuration == "" || activeConfiguration != configuration {
			return PublicationReconciliationDecision{}, ErrPublicationAdmissionConflict
		}
		if err := readPublicationProviderTagContextForPositionTx(ctx, tx, releaseID, guard.PositionID, endpoint, configuration, true, &providerContext); err != nil {
			return PublicationReconciliationDecision{}, err
		}
		ownerWitness = providerContext.Digest
		protection, err := BuildPublicationProviderTagProtection(providerContext)
		if err != nil {
			return PublicationReconciliationDecision{}, err
		}
		patch, err := BuildPublicationProviderCanonicalTagPatch(*providerContext)
		if err != nil {
			return PublicationReconciliationDecision{}, err
		}
		var metadata PublicationCommitMetadata
		if err := strictMetadata(current.ActualMetadata, &metadata); err != nil {
			return PublicationReconciliationDecision{}, err
		}
		desired, err := publication.ApplyProviderTagPatch(metadata.Tags, patch, protection.Keys)
		if err != nil {
			return PublicationReconciliationDecision{}, err
		}
		if !reflect.DeepEqual(desired, metadata.Tags) {
			action = ProviderTagsRequired
		}
	}
	// One blocked branch must not prevent an independent admitted change. Both
	// kinds of admission preserve continuation; only an all-no-op ACK requires
	// successful evaluation of both branches.
	if action == AllNoop && audioErr != nil {
		return PublicationReconciliationDecision{}, audioErr
	}
	// Delivery locks are always last, after domain and policy facts. No-op ACK
	// subsequently reuses this exact guard, not a newer requested generation.
	event, err := repository.lockPublicationReconciliationDelivery(ctx, tx, guard)
	if err != nil || event == nil {
		return PublicationReconciliationDecision{}, errors.Join(err, ErrPublicationAdmissionConflict)
	}
	decision := PublicationReconciliationDecision{Action: action}
	if action == AllNoop {
		encoded, err := json.Marshal([]any{current, authority, format, activeConfiguration, hasBindings, ownerWitness, audioWitness, publication.QualityPolicyVersion})
		if err != nil {
			return PublicationReconciliationDecision{}, err
		}
		digest := sha256.Sum256(encoded)
		decision.Noop = &PublicationReconciliationNoop{guard: guard, releaseID: releaseID, endpoint: endpoint, configuration: configuration, digest: hex.EncodeToString(digest[:])}
	}
	return decision, nil
}

func lockPublicationReconciliationSourceRoots(ctx context.Context, tx bun.Tx, trackID uuid.UUID) error {
	var roots []uuid.UUID
	if err := tx.NewRaw(`SELECT DISTINCT work.source_root_id FROM track_source association
		JOIN source_work_variant_identity identity ON identity.media_variant_id=association.media_variant_id
		JOIN source_analysis_work work ON work.id=identity.work_id
		WHERE association.track_id=? ORDER BY work.source_root_id`, trackID).Scan(ctx, &roots); err != nil {
		return err
	}
	for _, id := range roots {
		var locked uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM source_root WHERE id=? FOR SHARE`, id).Scan(ctx, &locked); err != nil {
			return err
		}
	}
	return nil
}

type reconciliationOwner struct {
	ID       uuid.UUID       `bun:"id,type:uuid"`
	Metadata json.RawMessage `bun:"metadata,type:jsonb"`
	Revision int64           `bun:"metadata_revision"`
}

func lockReconciliationLocalOwners(ctx context.Context, tx bun.Tx, releaseID, positionID uuid.UUID, position reconciliationPosition) ([]reconciliationOwner, error) {
	owners := []reconciliationOwner{}
	for _, selected := range []struct {
		table string
		id    uuid.UUID
	}{{"local_release", releaseID}, {"release_track", positionID}, {"local_track", position.TrackID}} {
		var owner reconciliationOwner
		if err := tx.NewRaw(`SELECT id,metadata,metadata_revision FROM `+selected.table+` WHERE id=? FOR SHARE`, selected.id).Scan(ctx, &owner); err != nil {
			return nil, err
		}
		owners = append(owners, owner)
	}
	if position.RecordingID != nil {
		var owner reconciliationOwner
		if err := tx.NewRaw(`SELECT id,metadata,metadata_revision FROM local_recording WHERE id=? FOR SHARE`, *position.RecordingID).Scan(ctx, &owner); err != nil {
			return nil, err
		}
		owners = append(owners, owner)
	}
	return owners, nil
}
