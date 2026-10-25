package persistence

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// PublicationReconciliationToolsSnapshot is a detached view of the verified
// pair held by one live reconciliation delivery.
type PublicationReconciliationToolsSnapshot struct {
	Tools        []SourceAnalysisToolSelection
	Installation *ToolInstallation
	Root         string
}

// AcquirePublicationReconciliationTools pins the selected installation for
// the lifetime of native CURRENT inspection. It performs no filesystem work.
func (repository *SetupManagerRepository) AcquirePublicationReconciliationTools(ctx context.Context, guard PublicationReconciliationDeliveryGuard, selections []SourceAnalysisToolSelection) (PublicationReconciliationToolsSnapshot, bool, error) {
	var snapshot PublicationReconciliationToolsSnapshot
	if len(selections) != 2 || validateFrozenPublicationTools(selections) != nil {
		return snapshot, false, fmt.Errorf("acquire publication reconciliation tools: verified ffmpeg pair is required")
	}
	installationID := selections[0].InstallationID
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("acquire publication reconciliation tools: lock output admission: %w", err)
		}
		if err := lockToolsMoveGateShared(ctx, tx); err != nil {
			return fmt.Errorf("acquire publication reconciliation tools: lock tools root: %w", err)
		}
		if err := lockPackageSelections(ctx, tx, []string{"ffmpeg"}); err != nil {
			return fmt.Errorf("acquire publication reconciliation tools: lock package: %w", err)
		}
		var moving bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM operation WHERE kind='move_tools_root' AND state IN ('queued','running'))`).Scan(ctx, &moving); err != nil {
			return err
		}
		if moving {
			return nil
		}
		if err := lockCompatibleInstallations(ctx, tx, []uuid.UUID{installationID}); err != nil {
			return fmt.Errorf("acquire publication reconciliation tools: lock installation: %w", err)
		}
		event, err := repository.lockPublicationReconciliationDelivery(ctx, tx, guard)
		if err != nil {
			return err
		}
		if event == nil {
			return nil
		}
		var installation ToolInstallation
		if err := tx.NewSelect().Model(&installation).Where("id=?", installationID).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return nil
			}
			return err
		}
		if err := validatePublicationInstallation(&installation, selections); err != nil {
			return nil
		}
		var root string
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='tools_directory'`).Scan(ctx, &root); err != nil {
			if err == sql.ErrNoRows {
				return nil
			}
			return err
		}
		if root == "" {
			return nil
		}
		if _, err := tx.NewRaw(`INSERT INTO operation_tool_read_hold(operation_id,installation_id) VALUES (?,?) ON CONFLICT DO NOTHING`, guard.OperationID, installationID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`UPDATE operation SET tools_read_required=true,updated_at=now() WHERE id=?`, guard.OperationID).Exec(ctx); err != nil {
			return err
		}
		copyInstallation := installation
		snapshot = PublicationReconciliationToolsSnapshot{Tools: append([]SourceAnalysisToolSelection(nil), selections...), Installation: &copyInstallation, Root: root}
		return nil
	})
	if err != nil {
		return PublicationReconciliationToolsSnapshot{}, false, err
	}
	return snapshot, snapshot.Installation != nil, nil
}

// CheckPublicationReconciliationTools revalidates the complete delivery fence
// and its durable hold; it does not inspect the filesystem.
func (repository *SetupManagerRepository) CheckPublicationReconciliationTools(ctx context.Context, guard PublicationReconciliationDeliveryGuard, installationID uuid.UUID) (bool, error) {
	var valid bool
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return err
		}
		if err := lockToolsMoveGateShared(ctx, tx); err != nil {
			return err
		}
		if err := lockPackageSelections(ctx, tx, []string{"ffmpeg"}); err != nil {
			return err
		}
		event, err := repository.lockPublicationReconciliationDelivery(ctx, tx, guard)
		if err != nil || event == nil {
			return err
		}
		return tx.NewRaw(`SELECT o.tools_read_required AND EXISTS(SELECT 1 FROM operation_tool_read_hold h WHERE h.operation_id=o.id AND h.installation_id=?) FROM operation o WHERE o.id=?`, installationID, guard.OperationID).Scan(ctx, &valid)
	})
	return valid && err == nil, err
}

// ReleasePublicationReconciliationTools removes only this delivery's exact
// hold. The caller must first establish command quiescence.
func (repository *SetupManagerRepository) ReleasePublicationReconciliationTools(ctx context.Context, guard PublicationReconciliationDeliveryGuard, installationID uuid.UUID) (bool, error) {
	var released bool
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return err
		}
		if err := lockToolsMoveGateShared(ctx, tx); err != nil {
			return err
		}
		if err := lockPackageSelections(ctx, tx, []string{"ffmpeg"}); err != nil {
			return err
		}
		event, err := repository.lockPublicationReconciliationToolHoldOwner(ctx, tx, guard)
		if err != nil || event == nil {
			return err
		}
		result, err := tx.NewRaw(`DELETE FROM operation_tool_read_hold WHERE operation_id=? AND installation_id=?`, guard.OperationID, installationID).Exec(ctx)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("release publication reconciliation tools: exact hold is not present")
		}
		if _, err := tx.NewRaw(`UPDATE operation SET tools_read_required=EXISTS(SELECT 1 FROM operation_tool_read_hold WHERE operation_id=?),updated_at=now() WHERE id=?`, guard.OperationID, guard.OperationID).Exec(ctx); err != nil {
			return err
		}
		released = true
		return nil
	})
	return released && err == nil, err
}
