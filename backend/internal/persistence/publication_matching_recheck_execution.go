package persistence

import (
	"context"
	"errors"
	"fmt"

	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/uptrace/bun"
)

// InstallMatchingRecheckSamePath takes the short native mutation and read-back
// callback under the same gates and transaction as settlement. Preparation,
// including all external processes, has already completed. The callback
// must not read the repository or current settings.
func (repository *SetupManagerRepository) InstallMatchingRecheckSamePath(
	ctx context.Context, guard PublicationDeliveryGuard, relativePath string,
	install func() (PublicationResult, error),
) error {
	if err := validatePublicationDeliveryGuard(guard); err != nil {
		return err
	}
	if install == nil || !frozenPublicationRelativePath(relativePath) {
		return ErrPublicationAdmissionConflict
	}
	return repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return err
		}
		if err := lockPublicationSourceCleanupPolicy(ctx, tx, true); err != nil {
			return err
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return err
		}
		attempt, _, _, live, err := lockAndValidatePublicationDelivery(ctx, tx, guard, "install matching recheck publication")
		if err != nil || !live {
			return errors.Join(ErrPublicationAdmissionConflict, err)
		}
		member, err := lockPublicationMatchingRecheckMemberTx(ctx, tx, attempt)
		if err != nil || member == nil {
			return errors.Join(ErrPublicationAdmissionConflict, err)
		}
		if err := validateMatchingRecheckNamespaceReplacementTx(ctx, tx, attempt, member, relativePath); err != nil {
			return err
		}
		var manifest struct {
			Version int                        `json:"version"`
			Nodes   []PublicationNamespaceNode `json:"nodes"`
		}
		if decodeStrictJSON(attempt.NamespaceManifest, &manifest) != nil || manifest.Version != 1 || validatePublicationManifest(manifest.Nodes) != nil {
			return ErrPublicationAdmissionConflict
		}
		target := ""
		for _, node := range manifest.Nodes {
			if node.NodeKind != "file" {
				continue
			}
			if target != "" || node.RelativePath != relativePath {
				return ErrPublicationAdmissionConflict
			}
			target = node.CanonicalKey
		}
		if target == "" {
			return ErrPublicationAdmissionConflict
		}
		if err := validatePublicationTargetNamespace(ctx, tx, attempt, target, relativePath); err != nil {
			return err
		}
		result, err := install()
		if err != nil {
			return err
		}
		committed, err := repository.commitPublicationResultTx(ctx, tx, guard, result)
		if err != nil || !committed {
			return errors.Join(ErrPublicationAdmissionConflict, err)
		}
		return nil
	})
}

// MatchingRecheckPublicationMoveInput is the guarded predecessor of a
// cross-position publication. It is not sufficient namespace authority by
// itself; the installer must also prove the native target identity.
type MatchingRecheckPublicationMoveInput struct {
	SamePath               bool
	Current                PublicationCurrentWitness
	CandidatePayloadSHA256 string
}

func (input MatchingRecheckPublicationMoveInput) ValidateQuality(observation PublicationStageObservation) error {
	actual, ok := publicationAudioFactsFromObservation(observation.ActualAudio)
	if !ok || publication.CompareQuality(qualityCandidateFromPersisted(actual), qualityCandidateFromPersisted(input.Current.AudioFacts)) == publication.QualityWorse {
		return fmt.Errorf("matching recheck replacement would reduce publication quality: %w", ErrPublicationAdmissionConflict)
	}
	return nil
}

func (repository *SetupManagerRepository) ReadMatchingRecheckPublicationMoveInput(
	ctx context.Context, guard PublicationDeliveryGuard,
) (*MatchingRecheckPublicationMoveInput, error) {
	if err := validatePublicationDeliveryGuard(guard); err != nil {
		return nil, err
	}
	var result *MatchingRecheckPublicationMoveInput
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		attempt, _, _, live, err := lockAndValidatePublicationDelivery(ctx, tx, guard, "read matching move publication")
		if err != nil {
			return err
		}
		if !live {
			return ErrPublicationAdmissionConflict
		}
		member, err := lockPublicationMatchingRecheckMemberTx(ctx, tx, attempt)
		if err != nil || member == nil {
			return err
		}
		if member.PredecessorPositionID == nil {
			return ErrPublicationAdmissionConflict
		}
		if *member.PredecessorPositionID == member.PositionID {
			return nil
		}
		if member.CommittedAt != nil || member.PositionID != attempt.PositionID ||
			member.OutputEpoch != attempt.Epoch || member.CandidateGeneration != attempt.CandidateGeneration ||
			attempt.ExpectedGeneration != nil || attempt.IntentKind != "audio" {
			return fmt.Errorf("read matching move publication: membership changed: %w", ErrPublicationAdmissionConflict)
		}
		_, witness, err := lockMatchingRecheckMovedCurrentTx(ctx, tx, member)
		if err != nil {
			return err
		}
		result = &MatchingRecheckPublicationMoveInput{SamePath: member.SamePath, Current: witness.Current}
		if member.CandidatePayloadSHA256 != nil {
			result.CandidatePayloadSHA256 = *member.CandidatePayloadSHA256
		}
		return nil
	})
	return result, err
}

// RecordMatchingRecheckMoveCandidate fences inspection-only recovery to the
// exact prepared bytes of a different-path install. An occupied destination
// rejected before RenameNoReplace must never be adopted on a later delivery.
// An empty digest clears the proof after an explicit no-replace collision.
func (repository *SetupManagerRepository) RecordMatchingRecheckMoveCandidate(
	ctx context.Context, guard PublicationDeliveryGuard, relativePath, digest string,
) error {
	if err := validatePublicationDeliveryGuard(guard); err != nil {
		return err
	}
	if !frozenPublicationRelativePath(relativePath) || (digest != "" && !validSHA256(digest)) {
		return ErrPublicationAdmissionConflict
	}
	return repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		attempt, _, claim, live, err := lockAndValidatePublicationDelivery(ctx, tx, guard, "record matching move candidate")
		if err != nil || !live || claim.State != "executing" {
			return errors.Join(ErrPublicationAdmissionConflict, err)
		}
		member, err := lockPublicationMatchingRecheckMemberTx(ctx, tx, attempt)
		if err != nil {
			return err
		}
		if member == nil || member.SamePath || member.CommittedAt != nil || !publicationResultPathMatches(attempt.NamespaceManifest, relativePath) {
			return ErrPublicationAdmissionConflict
		}
		_, err = tx.NewRaw(`UPDATE matching_local_recheck_publication SET candidate_payload_sha256=NULLIF(?,'') WHERE operation_id=?`,
			digest, member.OperationID).Exec(ctx)
		return err
	})
}
