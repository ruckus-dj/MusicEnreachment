package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/uptrace/bun"
)

// PublicationToolsForDelivery returns only the tool pair frozen in this exact
// attempt, while holding the same coordination fences used by tool mutators.
func (repository *SetupManagerRepository) PublicationToolsForDelivery(ctx context.Context, guard PublicationDeliveryGuard) ([]SourceAnalysisToolSelection, *ToolInstallation, string, bool, error) {
	if err := validatePublicationDeliveryGuard(guard); err != nil {
		return nil, nil, "", false, err
	}
	var selections []SourceAnalysisToolSelection
	var installation *ToolInstallation
	var root string
	valid := false
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("resolve publication tools: acquire output admission: %w", err)
		}
		if err := lockToolsMoveGateShared(ctx, tx); err != nil {
			return fmt.Errorf("resolve publication tools: lock tools root: %w", err)
		}
		if err := lockInstallationPackages(ctx, tx, "ffmpeg"); err != nil {
			return fmt.Errorf("resolve publication tools: lock package: %w", err)
		}
		attempt, _, _, ok, err := lockAndValidatePublicationDelivery(ctx, tx, guard, "resolve publication tools")
		if err != nil {
			return err
		}
		if !ok || (attempt.Phase != "preparing" && attempt.Phase != "staged") {
			return nil
		}
		input, err := DecodePublicationInputs(attempt.FrozenInputs)
		if err != nil {
			return fmt.Errorf("resolve publication tools: decode frozen inputs: %w", err)
		}
		selections = append([]SourceAnalysisToolSelection(nil), input.Tools...)
		if err := validateFrozenPublicationTools(selections); err != nil {
			return fmt.Errorf("resolve publication tools: %w", err)
		}
		var held bool
		if err := tx.NewRaw(`SELECT count(*) = 1 AND bool_and(installation_id = ?) FROM operation_tool_read_hold WHERE operation_id=?`, selections[0].InstallationID, guard.OperationID).Scan(ctx, &held); err != nil {
			return fmt.Errorf("resolve publication tools: check installation hold: %w", err)
		}
		if !held {
			return nil
		}
		installation = new(ToolInstallation)
		if err := tx.NewSelect().Model(installation).Where("id=?", selections[0].InstallationID).Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				installation = nil
				return nil
			}
			return fmt.Errorf("resolve publication tools: read installation: %w", err)
		}
		if err := validatePublicationInstallation(installation, selections); err != nil {
			installation = nil
			return nil
		}
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='tools_directory'`).Scan(ctx, &root); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("resolve publication tools: read tools root: %w", err)
		}
		valid = true
		return nil
	})
	if err != nil || !valid {
		return nil, nil, "", false, err
	}
	return selections, installation, root, true, nil
}
