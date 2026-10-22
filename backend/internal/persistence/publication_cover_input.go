package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// PublicationAttemptCoverInput is the attempt-owned effective cover receipt.
// Absent receipts are explicit; unknown is represented only by no row on an
// older unfinished attempt.
type PublicationAttemptCoverInput struct {
	OperationID uuid.UUID
	PositionID  uuid.UUID
	ReleaseID   uuid.UUID
	Revision    int64
	Mode        string
	State       string
	Generation  *uuid.UUID
	Bytes       []byte
	MIME        *string
	SHA256      []byte
	Size        *int64
}

func clonePublicationAttemptCoverInput(input PublicationAttemptCoverInput) PublicationAttemptCoverInput {
	result := input
	result.Bytes = append([]byte(nil), input.Bytes...)
	result.SHA256 = append([]byte(nil), input.SHA256...)
	if input.Generation != nil {
		value := *input.Generation
		result.Generation = &value
	}
	if input.MIME != nil {
		value := *input.MIME
		result.MIME = &value
	}
	if input.Size != nil {
		value := *input.Size
		result.Size = &value
	}
	return result
}

func readEffectiveReleaseCoverSummary(ctx context.Context, database bun.IDB, releaseID uuid.UUID) (EffectiveReleaseCoverSummary, error) {
	cover, err := readReleaseCover(ctx, database, releaseID)
	if errors.Is(err, sql.ErrNoRows) {
		return EffectiveReleaseCoverSummary{Revision: 1, Mode: "automatic", State: "absent"}, nil
	}
	if err != nil {
		return EffectiveReleaseCoverSummary{}, err
	}
	result := EffectiveReleaseCoverSummary{Revision: cover.Revision, Mode: cover.Mode, State: "absent"}
	if cover.SelectedGeneration != nil {
		generation := *cover.SelectedGeneration
		result.State, result.Generation = "value", &generation
	}
	return result, nil
}

func readEffectiveReleaseCoverInput(ctx context.Context, database bun.IDB, releaseID uuid.UUID, summary EffectiveReleaseCoverSummary) (PublicationAttemptCoverInput, error) {
	cover, err := readReleaseCover(ctx, database, releaseID)
	if errors.Is(err, sql.ErrNoRows) {
		if summary.Revision != 1 || summary.Mode != "automatic" || summary.State != "absent" {
			return PublicationAttemptCoverInput{}, ErrPublicationAdmissionConflict
		}
		return PublicationAttemptCoverInput{ReleaseID: releaseID, Revision: 1, Mode: "automatic", State: "absent"}, nil
	}
	if err != nil {
		return PublicationAttemptCoverInput{}, err
	}
	actual := EffectiveReleaseCoverSummary{Revision: cover.Revision, Mode: cover.Mode, State: "absent"}
	result := PublicationAttemptCoverInput{ReleaseID: releaseID, Revision: cover.Revision, Mode: cover.Mode, State: "absent"}
	if cover.SelectedGeneration != nil {
		generation := *cover.SelectedGeneration
		actual.State, actual.Generation = "value", &generation
		mime := *cover.SelectedMIME
		size := *cover.SelectedSize
		result.State, result.Generation, result.Bytes, result.MIME, result.SHA256, result.Size = "value", &generation, append([]byte(nil), cover.SelectedBytes...), &mime, append([]byte(nil), cover.SelectedSHA256...), &size
	}
	if actual.Revision != summary.Revision || actual.Mode != summary.Mode || actual.State != summary.State || !sameOptionalUUID(actual.Generation, summary.Generation) {
		return PublicationAttemptCoverInput{}, ErrPublicationAdmissionConflict
	}
	return result, nil
}

func insertPublicationAttemptCoverInput(ctx context.Context, tx bun.Tx, input PublicationAttemptCoverInput) error {
	switch input.State {
	case "absent", "value":
		if err := validateAttemptCoverInput(input); err != nil {
			return err
		}
	default:
		return errors.New("publication attempt cover input state is invalid")
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO publication_attempt_cover_input
		(operation_id,position_id,release_id,authority_revision,authority_mode,state,generation,image_bytes,image_mime,image_sha256,image_size)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`, input.OperationID, input.PositionID, input.ReleaseID, input.Revision, input.Mode, input.State,
		input.Generation, nullableCoverBytes(input), input.MIME, nullableCoverDigest(input), input.Size)
	return err
}

func nullableCoverBytes(input PublicationAttemptCoverInput) []byte {
	if input.State == "absent" {
		return nil
	}
	return input.Bytes
}
func nullableCoverDigest(input PublicationAttemptCoverInput) []byte {
	if input.State == "absent" {
		return nil
	}
	return input.SHA256
}

// PublicationAttemptCoverInputForDelivery exposes only the immutable receipt of
// a currently claimed first-audio attempt. It does not consult mutable authority.
func (repository *SetupManagerRepository) PublicationAttemptCoverInputForDelivery(ctx context.Context, guard PublicationDeliveryGuard) (PublicationAttemptCoverInput, bool, error) {
	var result PublicationAttemptCoverInput
	valid := false
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		attempt, _, _, ok, err := lockAndValidatePublicationDelivery(ctx, tx, guard, "read publication attempt cover input")
		if err != nil || !ok || attempt.Phase != "preparing" || attempt.IntentKind != "audio" {
			return err
		}
		result, err = scanPublicationAttemptCoverInput(ctx, tx, guard.OperationID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read publication attempt cover input: %w", err)
		}
		if result.PositionID != guard.ExpectedPositionID || result.OperationID != guard.OperationID || validateAttemptCoverInput(result) != nil {
			return errors.New("publication attempt cover input is inconsistent")
		}
		valid = true
		return nil
	})
	if err != nil {
		return PublicationAttemptCoverInput{}, false, err
	}
	return result, valid, nil
}

func scanPublicationAttemptCoverInput(ctx context.Context, tx bun.IDB, operationID uuid.UUID) (PublicationAttemptCoverInput, error) {
	var input PublicationAttemptCoverInput
	err := tx.NewRaw(`SELECT operation_id,position_id,release_id,authority_revision,authority_mode,state,generation,image_bytes,image_mime,image_sha256,image_size
		FROM publication_attempt_cover_input WHERE operation_id=?`, operationID).Scan(ctx, &input.OperationID, &input.PositionID,
		&input.ReleaseID, &input.Revision, &input.Mode, &input.State, &input.Generation, &input.Bytes, &input.MIME, &input.SHA256, &input.Size)
	return input, err
}

func validateAttemptCoverInput(input PublicationAttemptCoverInput) error {
	if input.OperationID == uuid.Nil || input.PositionID == uuid.Nil || input.ReleaseID == uuid.Nil || input.Revision < 1 || (input.Mode != "automatic" && input.Mode != "manual") {
		return errors.New("publication attempt cover input identity is invalid")
	}
	if input.State == "absent" {
		if input.Generation != nil || len(input.Bytes) != 0 || input.MIME != nil || len(input.SHA256) != 0 || input.Size != nil {
			return errors.New("absent publication attempt cover input contains image data")
		}
		return nil
	}
	if input.State != "value" || input.Generation == nil || *input.Generation == uuid.Nil || input.MIME == nil || input.Size == nil || *input.Size != int64(len(input.Bytes)) || len(input.Bytes) == 0 {
		return errors.New("value publication attempt cover input is incomplete")
	}
	digest := sha256.Sum256(input.Bytes)
	if string(digest[:]) != string(input.SHA256) {
		return errors.New("publication attempt cover input digest does not match bytes")
	}
	return validateReleaseCoverImage(*input.Generation, input.Bytes, *input.MIME)
}
