//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPublicationToolsForDeliveryRejectsInvalidAndUnheldAttemptsPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	repo := NewSetupManagerRepository(f.db)

	// Invalid guards are rejected before a transaction can return any pinned data.
	invalid := f.guard
	invalid.BootUUID = [16]byte{}
	selections, installation, root, valid, err := repo.PublicationToolsForDelivery(context.Background(), invalid)
	if err == nil || valid || selections != nil || installation != nil || root != "" {
		t.Fatalf("invalid guard returned selections=%v installation=%v root=%q valid=%v err=%v", selections, installation, root, valid, err)
	}

	// The execution fixture intentionally has no tool hold (nor frozen tool
	// selections); it must never authorize a worker to use a current installation.
	selections, installation, root, valid, err = repo.PublicationToolsForDelivery(context.Background(), f.guard)
	if err != nil || valid || selections != nil || installation != nil || root != "" {
		t.Fatalf("unheld attempt returned selections=%v installation=%v root=%q valid=%v err=%v", selections, installation, root, valid, err)
	}
}

func TestPublicationToolsForDeliveryReturnsFrozenVerifiedPairPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	repo := NewSetupManagerRepository(f.db)
	ctx := context.Background()

	root := filepath.Join(t.TempDir(), "managed-tools")
	if _, err := f.db.ExecContext(ctx, `INSERT INTO app_setting(setting_name,setting_value,updated_at) VALUES ('tools_directory',?,now())`, root); err != nil {
		t.Fatal(err)
	}
	installationID := uuid.New()
	verified := time.Now().UTC()
	versions, ffmpegBanner, ffprobeBanner := publicationFFmpegVersionFixture()
	installation := &ToolInstallation{
		ID: installationID, PackageKind: "ffmpeg", PlatformGOOS: runtime.GOOS, PlatformGOARCH: runtime.GOARCH,
		SourceName: "fixture", ReleaseIdentity: "release-1", RelativePath: filepath.Join("ffmpeg", "release-1"),
		State: "ready", ExecutableVersions: versions, ArtifactIdentities: json.RawMessage(`{}`), VerifiedAt: &verified,
	}
	frozenRelativePath := filepath.ToSlash(installation.RelativePath)
	if err := repo.CreateInstallation(ctx, installation); err != nil {
		t.Fatal(err)
	}
	selections := []SourceAnalysisToolSelection{
		{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: frozenRelativePath, Executable: "ffmpeg", Version: "7.0", VersionBanner: ffmpegBanner},
		{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: frozenRelativePath, Executable: "ffprobe", Version: "7.0", VersionBanner: ffprobeBanner},
	}
	input := publicationInputsFixture(t, "rewrite")
	input.Tools = selections
	frozen, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE track_publication_attempt SET frozen_inputs=?::jsonb WHERE operation_id=?`, string(frozen), f.guard.OperationID); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO operation_tool_read_hold(operation_id,installation_id) VALUES (?,?)`, f.guard.OperationID, installationID); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE operation SET tools_read_required=true WHERE id=?`, f.guard.OperationID); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if outcome, err := repo.ClaimPublicationDelivery(ctx, f.guard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim = %q, %v", outcome, err)
	}
	got, installed, gotRoot, valid, err := repo.PublicationToolsForDelivery(ctx, f.guard)
	if err != nil || !valid {
		t.Fatalf("resolve frozen pair valid=%t err=%v", valid, err)
	}
	if len(got) != 2 || got[0] != selections[0] || got[1] != selections[1] {
		t.Fatalf("resolved selections = %#v, want frozen %#v", got, selections)
	}
	if installed == nil || installed.ID != installationID || installed.State != "ready" || installed.VerifiedAt == nil {
		t.Fatalf("resolved installation = %#v", installed)
	}
	if gotRoot != root {
		t.Fatalf("resolved root = %q, want %q", gotRoot, root)
	}
}

func TestPublicationToolsForDeliveryRejectsMissingHoldPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	repo := NewSetupManagerRepository(f.db)
	ctx := context.Background()
	input := publicationInputsFixture(t, "rewrite")
	frozen, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE track_publication_attempt SET frozen_inputs=?::jsonb WHERE operation_id=?`, string(frozen), f.guard.OperationID); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE operation SET tools_read_required=false WHERE id=?`, f.guard.OperationID); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM operation_tool_read_hold WHERE operation_id=?`, f.guard.OperationID); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if outcome, err := repo.ClaimPublicationDelivery(ctx, f.guard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim = %q, %v", outcome, err)
	}
	selections, installation, root, valid, err := repo.PublicationToolsForDelivery(ctx, f.guard)
	if err != nil || valid || selections != nil || installation != nil || root != "" {
		t.Fatalf("missing hold returned selections=%v installation=%v root=%q valid=%v err=%v", selections, installation, root, valid, err)
	}
}
