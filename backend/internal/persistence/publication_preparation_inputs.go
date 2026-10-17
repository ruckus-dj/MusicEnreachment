package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/uptrace/bun"
)

// PublicationPreparationInputs returns frozen inputs only while the complete
// delivery guard is current and the attempt remains in preparation.
func (repository *SetupManagerRepository) PublicationPreparationInputs(ctx context.Context, guard PublicationDeliveryGuard) (PublicationInputs, bool, error) {
	var input PublicationInputs
	valid := false
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		attempt, operation, _, ok, err := lockAndValidatePublicationDelivery(ctx, tx, guard, "read publication preparation inputs")
		if err != nil || !ok || attempt.Phase != "preparing" || operation.Stage != "preparing" {
			return err
		}
		decoded, err := DecodePublicationInputs(attempt.FrozenInputs)
		if err != nil {
			return fmt.Errorf("read publication preparation inputs: decode frozen inputs: %w", err)
		}
		input, valid = decoded, true
		return nil
	})
	if err != nil {
		return PublicationInputs{}, false, err
	}
	if !valid {
		return PublicationInputs{}, false, nil
	}
	if err := input.Validate(); err != nil {
		return PublicationInputs{}, false, errors.New("read publication preparation inputs: frozen inputs are invalid")
	}
	return input.Clone(), true, nil
}
