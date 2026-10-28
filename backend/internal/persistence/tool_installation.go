package persistence

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/uptrace/bun"
)

// ToolInstallationRepository owns managed tool installation records and their
// activation. SetupManagerRepository embeds it temporarily for existing callers.
type ToolInstallationRepository struct {
	db *bun.DB
}

func NewToolInstallationRepository(db *bun.DB) *ToolInstallationRepository {
	return &ToolInstallationRepository{db: db}
}

func (repository *ToolInstallationRepository) ActivateInstallation(ctx context.Context, id uuid.UUID, packageKind, goos, goarch, activeSetting string) error {
	return repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockToolsMoveGateShared(ctx, tx); err != nil {
			return fmt.Errorf("lock tools move gate: %w", err)
		}
		moving, err := activeToolsMove(ctx, tx)
		if err != nil {
			return fmt.Errorf("check active tools root move: %w", err)
		}
		if moving {
			return fmt.Errorf("cannot activate installation during an active tools root move")
		}
		if err := lockSetupCompletion(ctx, tx); err != nil {
			return fmt.Errorf("lock setup completion: %w", err)
		}
		if err := lockInstallationPackages(ctx, tx, packageKind); err != nil {
			return fmt.Errorf("lock active installation: %w", err)
		}
		var completedAt string
		if err := tx.NewRaw("SELECT setting_value FROM app_setting WHERE setting_name = ?", "setup_completed_at").Scan(ctx, &completedAt); err == sql.ErrNoRows {
			return fmt.Errorf("explicit activation is available only after setup completion")
		} else if err != nil {
			return fmt.Errorf("read setup completion: %w", err)
		}
		return repository.activateInstallationTx(ctx, tx, id, packageKind, goos, goarch, activeSetting)
	})
}

func (repository *ToolInstallationRepository) ActivateInstallationDuringSetup(ctx context.Context, id uuid.UUID, packageKind, goos, goarch, activeSetting string) (bool, error) {
	activated := false
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockToolsMoveGateShared(ctx, tx); err != nil {
			return fmt.Errorf("lock tools move gate: %w", err)
		}
		if err := lockSetupCompletion(ctx, tx); err != nil {
			return fmt.Errorf("lock setup completion: %w", err)
		}
		if err := lockInstallationPackages(ctx, tx, "ffmpeg", "fpcalc"); err != nil {
			return fmt.Errorf("lock active installations: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext(?))", "musicbrainz-config"); err != nil {
			return fmt.Errorf("lock MusicBrainz configuration: %w", err)
		}
		var completedAt string
		err := tx.NewRaw("SELECT setting_value FROM app_setting WHERE setting_name = ?", "setup_completed_at").Scan(ctx, &completedAt)
		if err == nil {
			return nil
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("read setup completion: %w", err)
		}
		moving, err := activeToolsMove(ctx, tx)
		if err != nil {
			return fmt.Errorf("check active tools root move: %w", err)
		}
		if moving {
			return nil
		}
		if err := refuseOtherReadyInstallationTx(ctx, tx, packageKind, id); err != nil {
			return fmt.Errorf("refuse ambiguous ready installations: %w", err)
		}
		var currentID string
		err = tx.NewRaw("SELECT setting_value FROM app_setting WHERE setting_name = ?", activeSetting).Scan(ctx, &currentID)
		if err == nil {
			// A second successful selection is never silently substituted during
			// the one-time wizard. Re-delivery of the selected ID is idempotent.
			activated = currentID == id.String()
			return nil
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("read active installation: %w", err)
		}
		if err := repository.activateInstallationTx(ctx, tx, id, packageKind, goos, goarch, activeSetting); err != nil {
			return err
		}
		activated = true
		return nil
	})
	return activated, err
}

// FinalizeInstallation atomically records verified readiness, performs the
// one-time Setup activation when still appropriate, and settles only the
// current River delivery. Verification and filesystem cleanup happen outside
// this transaction.

func (repository *ToolInstallationRepository) CreateInstallation(ctx context.Context, installation *ToolInstallation) error {
	return repository.CreateInstallationWith(ctx, repository.db, installation)
}

func (repository *ToolInstallationRepository) CreateInstallationWith(ctx context.Context, database bun.IDB, installation *ToolInstallation) error {
	if !validInstallationRelativePath(installation) {
		return fmt.Errorf("create tool installation: relative path must match its package and release identity")
	}
	if _, err := database.NewInsert().Model(installation).Exec(ctx); err != nil {
		return fmt.Errorf("create tool installation: %w", err)
	}
	return nil
}

func (repository *ToolInstallationRepository) DeleteInstallation(ctx context.Context, id uuid.UUID, packageKind, goos, goarch, activeSetting string, removeFiles func(*ToolInstallation, string) error) error {
	if removeFiles == nil {
		return fmt.Errorf("delete installation: filesystem remover is required")
	}
	return repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := lockToolsMoveGateShared(ctx, tx); err != nil {
			return fmt.Errorf("lock tools operations for installation deletion: %w", err)
		}
		moving, err := activeToolsMove(ctx, tx)
		if err != nil {
			return fmt.Errorf("check active tools root move: %w", err)
		}
		if moving {
			return fmt.Errorf("cannot delete installation during an active tools root move")
		}
		if err := lockInstallationPackages(ctx, tx, packageKind); err != nil {
			return fmt.Errorf("lock active installation: %w", err)
		}
		installation, err := repository.GetInstallationForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		if installation.PackageKind != packageKind || installation.PlatformGOOS != goos ||
			installation.PlatformGOARCH != goarch || (installation.State != "ready" && installation.State != "failed") {
			return fmt.Errorf("installation is not deletable for %s/%s", goos, goarch)
		}
		var activeSettingValue string
		err = tx.NewRaw("SELECT setting_value FROM app_setting WHERE setting_name = ?", activeSetting).
			Scan(ctx, &activeSettingValue)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("read active installation setting: %w", err)
		}
		if activeSettingValue == id.String() {
			return fmt.Errorf("cannot delete active installation")
		}
		conflicts, err := repository.ListActiveOperationConflictsForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		if len(conflicts) != 0 {
			return fmt.Errorf("cannot delete installation with an active operation")
		}
		var toolsRoot string
		err = tx.NewRaw("SELECT setting_value FROM app_setting WHERE setting_name = ?", "tools_directory").Scan(ctx, &toolsRoot)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("read tools directory: %w", err)
		}
		if err := removeFiles(installation, toolsRoot); err != nil {
			return fmt.Errorf("remove managed installation files: %w", err)
		}
		if _, err := tx.NewDelete().Model(installation).WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("delete tool installation: %w", err)
		}
		return nil
	})
}

func (repository *ToolInstallationRepository) GetInstallation(ctx context.Context, id uuid.UUID) (*ToolInstallation, error) {
	installation := new(ToolInstallation)
	if err := repository.db.NewSelect().Model(installation).Where("id = ?", id).Scan(ctx); err != nil {
		return nil, fmt.Errorf("get tool installation: %w", err)
	}
	return installation, nil
}

func (repository *ToolInstallationRepository) GetInstallationByIdentity(ctx context.Context, packageKind, sourceName, releaseIdentity, goos, goarch string) (*ToolInstallation, error) {
	installation := new(ToolInstallation)
	if err := repository.db.NewSelect().Model(installation).
		Where("package_kind = ?", packageKind).Where("source_name = ?", sourceName).
		Where("release_identity = ?", releaseIdentity).Where("platform_goos = ?", goos).
		Where("platform_goarch = ?", goarch).Scan(ctx); err != nil {
		return nil, fmt.Errorf("get tool installation by identity: %w", err)
	}
	return installation, nil
}

func (repository *ToolInstallationRepository) GetInstallationForUpdate(ctx context.Context, tx bun.Tx, id uuid.UUID) (*ToolInstallation, error) {
	installation := new(ToolInstallation)
	if err := tx.NewSelect().Model(installation).Where("id = ?", id).For("UPDATE").Scan(ctx); err != nil {
		return nil, fmt.Errorf("lock tool installation: %w", err)
	}
	return installation, nil
}

func (repository *ToolInstallationRepository) ListActiveOperationConflictsForUpdate(ctx context.Context, tx bun.Tx, targetInstallationID uuid.UUID) ([]Operation, error) {
	operations := make([]Operation, 0)
	if err := tx.NewSelect().Model(&operations).Where("state IN ('queued', 'running')").
		Where("(kind = 'move_tools_root' OR target_installation_id = ? OR EXISTS (SELECT 1 FROM operation_tool_read_hold hold WHERE hold.operation_id=operation.id AND hold.installation_id=?))", targetInstallationID, targetInstallationID).For("UPDATE").Scan(ctx); err != nil {
		return nil, fmt.Errorf("list active operation conflicts: %w", err)
	}
	retainedPublications := make([]Operation, 0)
	if err := tx.NewSelect().Model(&retainedPublications).
		Where("state='failed' AND kind='publish_track_v1'").
		Where("EXISTS (SELECT 1 FROM operation_tool_read_hold hold WHERE hold.operation_id=operation.id AND hold.installation_id=?)", targetInstallationID).
		For("UPDATE").Scan(ctx); err != nil {
		return nil, fmt.Errorf("list retained publication installation conflicts: %w", err)
	}
	operations = append(operations, retainedPublications...)
	return operations, nil
}

func (repository *ToolInstallationRepository) ListInstallations(ctx context.Context, packageKind, goos, goarch string) ([]ToolInstallation, error) {
	installations := make([]ToolInstallation, 0)
	query := repository.db.NewSelect().Model(&installations).Order("created_at ASC")
	if packageKind != "" {
		query.Where("package_kind = ?", packageKind)
	}
	if goos != "" {
		query.Where("platform_goos = ?", goos)
	}
	if goarch != "" {
		query.Where("platform_goarch = ?", goarch)
	}
	if err := query.Scan(ctx); err != nil {
		return nil, fmt.Errorf("list tool installations: %w", err)
	}
	return installations, nil
}

func (repository *ToolInstallationRepository) MarkInstallationFailed(ctx context.Context, id uuid.UUID) error {
	result, err := repository.db.NewUpdate().Model((*ToolInstallation)(nil)).Set("state = 'failed'").Set("updated_at = now()").Where("id = ?", id).Where("state = 'preparing'").Exec(ctx)
	if err != nil {
		return fmt.Errorf("mark tool installation failed: %w", err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return fmt.Errorf("mark tool installation failed: installation must exist and be preparing")
	}
	return nil
}

// ActivateInstallation validates a ready installation for the immutable
// platform and updates its package's active setting in the same transaction.

func (repository *ToolInstallationRepository) MarkInstallationReady(ctx context.Context, id uuid.UUID, versions json.RawMessage, verifiedAt time.Time) error {
	var evidence map[string]string
	if err := json.Unmarshal(versions, &evidence); err != nil {
		return fmt.Errorf("mark tool installation ready: decode fresh verification output: %w", err)
	}
	return repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		installation, err := repository.GetInstallationForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		canonical, err := tools.CanonicalPackageVersion(evidence, tools.PackageKind(installation.PackageKind), installation.PlatformGOOS)
		if err != nil {
			return fmt.Errorf("mark tool installation ready: %w", err)
		}
		if installation.State == "ready" {
			if installation.Version != canonical || !sameVerificationEvidence(installation.ExecutableVerificationOutput, versions) {
				return fmt.Errorf("mark tool installation ready: ready installation canonical identity changed")
			}
			return nil
		}
		if installation.State != "preparing" {
			return fmt.Errorf("mark tool installation ready: installation must be preparing or ready")
		}
		if _, err := tx.NewUpdate().Model((*ToolInstallation)(nil)).
			Set("state = 'ready'").Set("executable_verification_output = ?", versions).Set("version = ?", canonical).Set("verified_at = ?", verifiedAt).
			Set("updated_at = ?", verifiedAt).Where("id = ?", id).Where("state = 'preparing'").Exec(ctx); err != nil {
			return fmt.Errorf("mark tool installation ready: %w", err)
		}
		return nil
	})
}

func sameVerificationEvidence(left, right json.RawMessage) bool {
	var leftEvidence, rightEvidence map[string]string
	if json.Unmarshal(left, &leftEvidence) != nil || json.Unmarshal(right, &rightEvidence) != nil {
		return false
	}
	leftCanonical, leftErr := json.Marshal(leftEvidence)
	rightCanonical, rightErr := json.Marshal(rightEvidence)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftCanonical, rightCanonical)
}

func (repository *ToolInstallationRepository) UpdateInstallation(ctx context.Context, installation *ToolInstallation) error {
	if !validInstallationRelativePath(installation) {
		return fmt.Errorf("update tool installation: relative path must match its package and release identity")
	}
	_, err := repository.db.NewUpdate().Model(installation).
		Column("relative_path", "artifact_identities", "updated_at").WherePK().Exec(ctx)
	if err != nil {
		return fmt.Errorf("update tool installation: %w", err)
	}
	return nil
}

func (repository *ToolInstallationRepository) activateInstallationTx(ctx context.Context, tx bun.Tx, id uuid.UUID, packageKind, goos, goarch, activeSetting string) error {
	installation, err := repository.GetInstallationForUpdate(ctx, tx, id)
	if err != nil {
		return err
	}
	if installation.PackageKind != packageKind || installation.PlatformGOOS != goos || installation.PlatformGOARCH != goarch || installation.State != "ready" {
		return fmt.Errorf("installation is not a ready %s installation for %s/%s", packageKind, goos, goarch)
	}
	if _, err := tx.NewInsert().Model(&AppSetting{Name: activeSetting, Value: id.String()}).
		On("CONFLICT (setting_name) DO UPDATE").Set("setting_value = EXCLUDED.setting_value").Set("updated_at = now()").Exec(ctx); err != nil {
		return fmt.Errorf("set active installation: %w", err)
	}
	return nil
}

func readyInstallationsCountTx(ctx context.Context, tx bun.Tx, packageKind string, exclude *uuid.UUID) (int, error) {
	query := tx.NewSelect().Model((*ToolInstallation)(nil)).Where("package_kind = ?", packageKind).Where("state = ?", "ready")
	if exclude != nil {
		query = query.Where("id <> ?", *exclude)
	}
	count, err := query.Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("count ready %s installations: %w", packageKind, err)
	}
	return int(count), nil
}

func refuseOtherReadyInstallationTx(ctx context.Context, tx bun.Tx, packageKind string, id uuid.UUID) error {
	count, err := readyInstallationsCountTx(ctx, tx, packageKind, &id)
	if err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("another ready %s installation already exists", packageKind)
	}
	return nil
}

func validInstallationRelativePath(installation *ToolInstallation) bool {
	if installation == nil || (installation.PackageKind != "ffmpeg" && installation.PackageKind != "fpcalc") {
		return false
	}
	release := installation.ReleaseIdentity
	separator := "/"
	if installation.PlatformGOOS == "windows" {
		separator = `\`
	}
	return release != "" && release != "." && release != ".." &&
		!strings.ContainsAny(release, `/\:`) &&
		installation.RelativePath == installation.PackageKind+separator+release
}
