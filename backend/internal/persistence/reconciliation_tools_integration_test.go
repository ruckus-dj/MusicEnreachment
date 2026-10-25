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
	"github.com/uptrace/bun"
)

func TestPublicationReconciliationToolsAcquireCheckReleasePostgreSQL(t *testing.T) {
	t.Parallel()
	repo, ctx, epoch, position := reconciliationDeliveryFixture(t)
	reconciliationDeliveryRequest(t, repo, ctx, epoch, position, []string{"album"})
	inserter := new(reconciliationDeliveryInserter)
	if count, err := repo.DispatchPendingPublicationReconciliations(ctx, inserter); err != nil || count != 1 {
		t.Fatalf("dispatch=%d err=%v", count, err)
	}
	opID, jobID := inserter.args[0].OperationID, inserter.ids[0]
	if _, err := repo.db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=1,attempted_at=now() WHERE id=?`, jobID); err != nil {
		t.Fatal(err)
	}
	guard, result, err := repo.ClaimPublicationReconciliationDelivery(ctx, opID, jobID, 1)
	if err != nil || result != PublicationReconciliationClaimed {
		t.Fatalf("claim=%s err=%v", result, err)
	}

	root := filepath.Join(t.TempDir(), "managed-tools")
	if _, err := repo.db.ExecContext(ctx, `INSERT INTO app_setting(setting_name,setting_value,updated_at) VALUES ('tools_directory',?,now())`, root); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	verified := time.Now().UTC()
	versions, ffmpegBanner, ffprobeBanner := publicationFFmpegVersionFixture()
	installation := &ToolInstallation{ID: id, PackageKind: "ffmpeg", PlatformGOOS: runtime.GOOS, PlatformGOARCH: runtime.GOARCH,
		SourceName: "fixture", ReleaseIdentity: "release-1", RelativePath: filepath.Join("ffmpeg", "release-1"),
		State: "ready", ExecutableVersions: versions, ArtifactIdentities: json.RawMessage(`{}`), VerifiedAt: &verified}
	if err := repo.CreateInstallation(ctx, installation); err != nil {
		t.Fatal(err)
	}
	path := filepath.ToSlash(installation.RelativePath)
	selections := []SourceAnalysisToolSelection{
		{PackageKind: "ffmpeg", InstallationID: id, RelativePath: path, Executable: "ffmpeg", Version: "7.0", VersionBanner: ffmpegBanner},
		{PackageKind: "ffmpeg", InstallationID: id, RelativePath: path, Executable: "ffprobe", Version: "7.0", VersionBanner: ffprobeBanner},
	}
	snapshot, acquired, err := repo.AcquirePublicationReconciliationTools(ctx, guard, selections)
	if err != nil || !acquired {
		t.Fatalf("acquired=%v err=%v", acquired, err)
	}
	if snapshot.Installation == nil || snapshot.Installation.ID != id || snapshot.Root != root || len(snapshot.Tools) != 2 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if ok, err := repo.CheckPublicationReconciliationTools(ctx, guard, id); err != nil || !ok {
		t.Fatalf("check=%v err=%v", ok, err)
	}
	if _, result, err := repo.ClaimPublicationReconciliationDelivery(ctx, opID, jobID, 1); err != nil || result != PublicationReconciliationBusy {
		t.Fatalf("claim while held=%s err=%v", result, err)
	}
	if released, err := repo.ReleasePublicationReconciliationDelivery(ctx, guard); err == nil || released {
		t.Fatalf("delivery released with live tool hold: released=%v err=%v", released, err)
	}
	if parked, err := repo.ParkPublicationReconciliationDelivery(ctx, guard, 1, "evaluated"); err == nil || parked {
		t.Fatalf("delivery parked with live tool hold: parked=%v err=%v", parked, err)
	}
	acknowledged := false
	err = repo.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var e error
		acknowledged, e = repo.AcknowledgePublicationReconciliationDeliveryWith(ctx, tx, guard)
		return e
	})
	if err == nil || acknowledged {
		t.Fatalf("delivery acknowledged with hold: acknowledged=%v err=%v", acknowledged, err)
	}
	wrongToken := guard
	wrongToken.ClaimToken = uuid.New()
	if released, err := repo.ReleasePublicationReconciliationTools(ctx, wrongToken, id); err != nil || released {
		t.Fatalf("release with wrong claim token=%v err=%v", released, err)
	}
	// Native callers release only after quiescence. Model a retryable River
	// delivery after the original command has stopped: strict checking must fail,
	// while exact-claim release must still be permitted to clear its hold.
	if _, err := repo.db.ExecContext(ctx, `UPDATE river_job SET state='retryable',attempt=2,finalized_at=NULL WHERE id=?`, jobID); err != nil {
		t.Fatal(err)
	}
	if ok, err := repo.CheckPublicationReconciliationTools(ctx, guard, id); err != nil || ok {
		t.Fatalf("check after River attempt changed=%v err=%v", ok, err)
	}
	if released, err := repo.ReleasePublicationReconciliationTools(ctx, guard, id); err != nil || !released {
		t.Fatalf("original claim release after River retry=%v err=%v", released, err)
	}
	var holdCount int
	var required bool
	if err := repo.db.NewRaw(`SELECT count(*) FROM operation_tool_read_hold WHERE operation_id=? AND installation_id=?`, opID, id).Scan(ctx, &holdCount); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.NewRaw(`SELECT tools_read_required FROM operation WHERE id=?`, opID).Scan(ctx, &required); err != nil {
		t.Fatal(err)
	}
	if holdCount != 0 || required {
		t.Fatalf("hold cleanup incomplete: count=%d required=%v", holdCount, required)
	}
	if released, err := repo.ReleasePublicationReconciliationTools(ctx, guard, id); err == nil || released {
		t.Fatalf("duplicate release=%v err=%v", released, err)
	}
	// Ordinary delivery release still requires a live River fence.
	if released, err := repo.ReleasePublicationReconciliationDelivery(ctx, guard); err != nil || released {
		t.Fatalf("stale River delivery release=%v err=%v", released, err)
	}
	if _, err := repo.db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=1 WHERE id=?`, jobID); err != nil {
		t.Fatal(err)
	}
	if released, err := repo.ReleasePublicationReconciliationDelivery(ctx, guard); err != nil || !released {
		t.Fatalf("delivery release after tool release=%v err=%v", released, err)
	}
}
