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

func TestPublicationToolsForDeliveryFrozenPairAndHoldLifecyclePostgreSQL(t *testing.T) {
	ctx := context.Background()

	// Invalid identity and an unclaimed attempt must not reveal tool data.
	var repo *SetupManagerRepository
	invalid := PublicationDeliveryGuard{BootUUID: uuid.Nil}
	selections, installation, root, valid, err := repo.PublicationToolsForDelivery(ctx, invalid)
	if err == nil || valid || selections != nil || installation != nil || root != "" {
		t.Fatalf("invalid guard returned selections=%v installation=%v root=%q valid=%t err=%v", selections, installation, root, valid, err)
	}

	f := newPublicationExecutionFixture(t)
	repo = NewSetupManagerRepository(f.db)
	selections, installation, root, valid, err = repo.PublicationToolsForDelivery(ctx, f.guard)
	if err != nil || valid || selections != nil || installation != nil || root != "" {
		t.Fatalf("unclaimed attempt returned selections=%v installation=%v root=%q valid=%t err=%v", selections, installation, root, valid, err)
	}

	toolRoot := filepath.Join(t.TempDir(), "managed-tools")
	if _, err := f.db.ExecContext(ctx, `INSERT INTO app_setting(setting_name,setting_value,updated_at) VALUES ('tools_directory',?,now())`, toolRoot); err != nil {
		t.Fatal(err)
	}
	installationID := uuid.New()
	verified := time.Now().UTC()
	versions, ffmpegBanner, ffprobeBanner := publicationFFmpegVersionFixture()
	installed := &ToolInstallation{
		ID: installationID, PackageKind: "ffmpeg", PlatformGOOS: runtime.GOOS, PlatformGOARCH: runtime.GOARCH,
		SourceName: "fixture", ReleaseIdentity: "release-1", RelativePath: filepath.Join("ffmpeg", "release-1"),
		State: "ready", ExecutableVerificationOutput: versions, Version: "ffmpeg 7.0 + ffprobe 7.0",
		ArtifactIdentities: json.RawMessage(`{}`), VerifiedAt: &verified,
	}
	frozenPath := filepath.ToSlash(installed.RelativePath)
	if err := repo.CreateInstallation(ctx, installed); err != nil {
		t.Fatal(err)
	}
	want := []SourceAnalysisToolSelection{
		{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: frozenPath, Executable: "ffmpeg", Version: installed.Version, VersionBanner: ffmpegBanner},
		{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: frozenPath, Executable: "ffprobe", Version: installed.Version, VersionBanner: ffprobeBanner},
	}
	input := publicationInputsFixture(t, "rewrite")
	input.Tools = want
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
	got, gotInstallation, gotRoot, valid, err := repo.PublicationToolsForDelivery(ctx, f.guard)
	if err != nil || !valid {
		t.Fatalf("resolve frozen pair valid=%t err=%v", valid, err)
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("resolved selections = %#v, want frozen %#v", got, want)
	}
	if gotInstallation == nil || gotInstallation.ID != installationID || gotInstallation.State != "ready" || gotInstallation.VerifiedAt == nil {
		t.Fatalf("resolved installation = %#v", gotInstallation)
	}
	if gotRoot != toolRoot {
		t.Fatalf("resolved root = %q, want %q", gotRoot, toolRoot)
	}

	// Retiring the hold and its obligation is atomic; even a claimed delivery
	// cannot resolve frozen tools after the ownership fence is removed.
	tx, err = f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM operation_tool_read_hold WHERE operation_id=?`, f.guard.OperationID); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE operation SET tools_read_required=false WHERE id=?`, f.guard.OperationID); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	selections, installation, root, valid, err = repo.PublicationToolsForDelivery(ctx, f.guard)
	if err != nil || valid || selections != nil || installation != nil || root != "" {
		t.Fatalf("claimed missing-hold attempt returned selections=%v installation=%v root=%q valid=%t err=%v", selections, installation, root, valid, err)
	}
}
