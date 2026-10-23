//go:build integration

package service

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/uptrace/bun"
)

func TestPublicationPreInputFailureRetainsInputsAndRetriesPostgreSQL(t *testing.T) {
	testPublicationSourceExecution(t, "Track.flac", true, false,
		func(db *bun.DB, releaseID, positionID, trackID, epoch uuid.UUID, publications *LibraryPublications) {
			ctx := context.Background()
			preview, err := publications.PreviewFirst(ctx, releaseID, positionID)
			if err != nil || len(preview.Blockers) != 0 || preview.SelectedSource == nil {
				t.Fatalf("first publication preview = %+v, %v", preview, err)
			}
			operationID, err := publications.AdmitFirst(ctx, releaseID, positionID, FirstPublicationConfirmation{PreviewDigest: preview.ContextDigest, ExpectedEpoch: epoch})
			if err != nil {
				t.Fatal(err)
			}
			manager := persistence.NewSetupManagerRepository(db)
			attempt, err := manager.ReadPublicationAttempt(ctx, operationID)
			if err != nil || attempt == nil {
				t.Fatalf("read admitted attempt = %+v, %v", attempt, err)
			}
			input, err := persistence.DecodePublicationInputs(attempt.FrozenInputs)
			if err != nil || input.Audio == nil {
				t.Fatalf("read admitted input: %v", err)
			}
			source := filepath.Join(input.Audio.ConfiguredRootPath, filepath.FromSlash(input.Audio.RelativePath))
			original, err := os.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}
			backup := source + ".retained"
			if err := os.Rename(source, backup); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Rename(backup, source) })
			op, err := manager.GetOperation(ctx, operationID)
			if err != nil || op == nil || op.RiverJobID == nil {
				t.Fatalf("read operation = %+v, %v", op, err)
			}
			if _, err := db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=1 WHERE id=?`, *op.RiverJobID); err != nil {
				t.Fatal(err)
			}
			registry := settings.New(persistence.NewSettingsRepository(db), nil)
			executor := NewPublicationExecutor(manager, registry, tools.Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, publication.NewCoordinator(), uuid.New(), audioDeliveryTestCommands{})
			if err := executor.ExecutePublicationDelivery(ctx, operationID, *op.RiverJobID, 1); err == nil {
				t.Fatal("expected missing source error")
			}
			retained, err := manager.ReadPublicationAttempt(ctx, operationID)
			if err != nil || retained == nil || !bytes.Equal(retained.FrozenInputs, attempt.FrozenInputs) {
				t.Fatalf("retry lost frozen inputs: %v", err)
			}
			live, err := manager.GetOperation(ctx, operationID)
			if err != nil || live == nil || live.State != "running" {
				t.Fatalf("retryable operation = %+v, %v", live, err)
			}
			actual, err := os.ReadFile(backup)
			if err != nil || !bytes.Equal(actual, original) {
				t.Fatalf("source changed: %v", err)
			}
			if err := os.Rename(backup, source); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=2 WHERE id=?`, *op.RiverJobID); err != nil {
				t.Fatal(err)
			}
			if err := executor.ExecutePublicationDelivery(ctx, operationID, *op.RiverJobID, 2); err != nil {
				t.Fatalf("retry after restoring source: %v", err)
			}
		})
}
