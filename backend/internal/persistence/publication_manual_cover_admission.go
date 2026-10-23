package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/uptrace/bun"
)

// ReleaseCoverApplyAdmission is a compare-and-swap request for applying the
// pending release cover to every currently published position in the release.
type ReleaseCoverApplyAdmission struct {
	ReleaseID                 uuid.UUID
	ExpectedEpoch             uuid.UUID
	ExpectedRevision          int64
	ExpectedPendingGeneration uuid.UUID
	Tools                     []SourceAnalysisToolSelection
}

// ReleaseCoverRetryAdmission retries the currently selected cover without
// changing any pending cover authority.
type ReleaseCoverRetryAdmission struct {
	ReleaseID                  uuid.UUID
	ExpectedEpoch              uuid.UUID
	ExpectedRevision           int64
	ExpectedSelectedGeneration uuid.UUID
	Tools                      []SourceAnalysisToolSelection
}

type ReleaseCoverBatchAdmission struct {
	ReleaseID          uuid.UUID
	SelectedGeneration uuid.UUID
	Revision           int64
	Operations         []ReleaseCoverPositionOperation
}

type ReleaseCoverPositionOperation struct {
	PositionID          uuid.UUID
	OperationID         uuid.UUID
	CandidateGeneration uuid.UUID
}

func (repository *SetupManagerRepository) AdmitReleaseCoverApply(ctx context.Context, request ReleaseCoverApplyAdmission, client RiverInserter) (ReleaseCoverBatchAdmission, error) {
	return repository.admitReleaseCoverBatch(ctx, request.ReleaseID, request.ExpectedEpoch, request.ExpectedRevision, request.ExpectedPendingGeneration, request.Tools, client, false)
}

func (repository *SetupManagerRepository) AdmitReleaseCoverRetry(ctx context.Context, request ReleaseCoverRetryAdmission, client RiverInserter) (ReleaseCoverBatchAdmission, error) {
	return repository.admitReleaseCoverBatch(ctx, request.ReleaseID, request.ExpectedEpoch, request.ExpectedRevision, request.ExpectedSelectedGeneration, request.Tools, client, true)
}

func (repository *SetupManagerRepository) admitReleaseCoverBatch(ctx context.Context, releaseID, epoch uuid.UUID, revision int64, generation uuid.UUID, tools []SourceAnalysisToolSelection, client RiverInserter, retry bool) (ReleaseCoverBatchAdmission, error) {
	if releaseID == uuid.Nil || epoch == uuid.Nil || generation == uuid.Nil || revision < 1 || client == nil {
		return ReleaseCoverBatchAdmission{}, errors.New("release cover admission requires identities, revision and River client")
	}
	if err := validatePublicationToolSelections(tools); err != nil {
		return ReleaseCoverBatchAdmission{}, err
	}
	result := ReleaseCoverBatchAdmission{ReleaseID: releaseID, Operations: make([]ReleaseCoverPositionOperation, 0)}
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		// Match publication admission's lock order before taking the namespace
		// singleton. Per-position admissions reacquire these same transaction locks.
		if err := lockReleaseCoverAdmission(ctx, tx, epoch, tools); err != nil {
			return err
		}
		var existing uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM local_release WHERE id=? FOR UPDATE`, releaseID).Scan(ctx, &existing); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrLibraryReleaseNotFound
			}
			return err
		}
		var positions []uuid.UUID
		if err := tx.NewRaw(`SELECT p.position_id FROM track_publication p JOIN release_track t ON t.id=p.position_id WHERE t.release_id=? ORDER BY p.position_id FOR UPDATE OF p,t`, releaseID).Scan(ctx, &positions); err != nil {
			return fmt.Errorf("lock release publications: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `SELECT release_id FROM release_publication_cover WHERE release_id=? FOR UPDATE`, releaseID); err != nil {
			return err
		}
		cover, err := readReleaseCover(ctx, tx, releaseID)
		if err != nil {
			return err
		}
		if cover.Revision != revision || cover.Mode != "manual" {
			return ErrReleaseCoverConflict
		}
		if retry {
			if cover.SelectedGeneration == nil || *cover.SelectedGeneration != generation {
				return ErrReleaseCoverConflict
			}
		} else {
			if err := applyPendingReleaseCoverTx(ctx, tx, cover, generation); err != nil {
				return err
			}
			cover, err = readReleaseCover(ctx, tx, releaseID)
			if err != nil {
				return err
			}
		}
		result.Revision, result.SelectedGeneration = cover.Revision, generation
		for _, positionID := range positions {
			var current TrackPublication
			if err := tx.NewSelect().Model(&current).Where("position_id=?", positionID).For("UPDATE").Scan(ctx); err != nil {
				return err
			}
			if current.Epoch != epoch {
				return ErrPublicationAdmissionConflict
			}
			var ownership PublicationCurrentOwnership
			var audio PublicationCurrentAudio
			var metadata PublicationCommitMetadata
			if err := decodePublicationCurrentContract(current.Ownership, &ownership); err != nil {
				return err
			}
			if err := decodePublicationCurrentContract(current.ActualAudio, &audio); err != nil {
				return err
			}
			if err := strictMetadata(current.ActualMetadata, &metadata); err != nil {
				return err
			}
			input := PublicationInputs{Version: PublicationInputsVersion, QualityPolicyVersion: publication.QualityPolicyVersion, IntentKind: "rewrite", Format: publication.AudioFormatSource,
				Rewrite:     &PublicationRewriteInput{CurrentGeneration: current.Generation, RelativePath: current.RelativePath, Ownership: ownership.Identity, AudioFacts: audio.Facts},
				ManualCover: &PublicationManualCoverInput{ReleaseID: releaseID, AuthorityRevision: cover.Revision, SelectedGeneration: generation}, DesiredTags: metadata.Tags, Tools: tools}
			if err := input.Validate(); err != nil {
				return fmt.Errorf("validate cover rewrite: %w", err)
			}
			var manifest []PublicationNamespaceNode
			if err := tx.NewRaw(`SELECT c.canonical_key,c.relative_path,c.node_kind,COALESCE(c.directory_scope_key,'') AS directory_scope FROM publication_namespace_claim c JOIN publication_namespace_reservation r ON r.namespace_claim_id=c.id WHERE c.epoch=? AND r.position_id=? AND r.generation=? ORDER BY c.canonical_key`, epoch, positionID, current.Generation).Scan(ctx, &manifest); err != nil {
				return err
			}
			if err := validatePublicationManifest(manifest); err != nil {
				return err
			}
			frozen, err := json.Marshal(input)
			if err != nil {
				return err
			}
			operationID, candidate := uuid.New(), uuid.New()
			request := PublicationAdmission{PositionID: positionID, ExpectedReleaseID: releaseID, ExpectedEpoch: epoch, ExpectedGeneration: &current.Generation, CandidateGeneration: candidate, IntentKind: "rewrite", ToolsReadRequired: true, Tools: tools, FrozenInputs: frozen, NamespaceManifest: manifest, validatedManualCover: true}
			if _, err := repository.admitPublicationTx(ctx, tx, request, input, operationID, client); err != nil {
				return err
			}
			receipt, err := readEffectiveReleaseCoverInput(ctx, tx, releaseID, EffectiveReleaseCoverSummary{Revision: cover.Revision, Mode: cover.Mode, State: "value", Generation: cover.SelectedGeneration})
			if err != nil {
				return err
			}
			receipt.OperationID, receipt.PositionID = operationID, positionID
			if err := insertPublicationAttemptCoverInput(ctx, tx, receipt); err != nil {
				return err
			}
			result.Operations = append(result.Operations, ReleaseCoverPositionOperation{PositionID: positionID, OperationID: operationID, CandidateGeneration: candidate})
		}
		return nil
	})
	if err != nil {
		return ReleaseCoverBatchAdmission{}, fmt.Errorf("admit release cover batch: %w", err)
	}
	return result, nil
}

func lockReleaseCoverAdmission(ctx context.Context, tx bun.Tx, epoch uuid.UUID, tools []SourceAnalysisToolSelection) error {
	if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
		return err
	}
	if err := lockToolsMoveReaders(ctx, tx); err != nil {
		return err
	}
	if moving, err := activeToolsMoveExists(ctx, tx); err != nil {
		return err
	} else if moving {
		return ErrToolsRootMoveActive
	}
	if err := lockPackageSelections(ctx, tx, []string{"ffmpeg"}); err != nil {
		return err
	}
	if err := lockCompatibleInstallations(ctx, tx, []uuid.UUID{tools[0].InstallationID}); err != nil {
		return err
	}
	var installation ToolInstallation
	if err := tx.NewSelect().Model(&installation).Where("id=?", tools[0].InstallationID).Scan(ctx); err != nil {
		return err
	}
	if err := validatePublicationInstallation(&installation, tools); err != nil {
		return err
	}
	var actual uuid.UUID
	if err := tx.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true FOR UPDATE`).Scan(ctx, &actual); err != nil {
		return err
	}
	if actual != epoch {
		return ErrPublicationAdmissionConflict
	}
	return nil
}
