//go:build integration

package service

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/uptrace/bun"
)

func TestPublicationPreInputFailureSettlementPostgreSQL(t *testing.T) {
	testPublicationSourceExecution(t, "Track.flac", true, false,
		func(db *bun.DB, releaseID, positionID, trackID, epoch uuid.UUID, publications *LibraryPublications) {
			ctx := context.Background()
			preview, err := publications.PreviewFirst(ctx, releaseID, positionID)
			if err != nil || len(preview.Blockers) != 0 || preview.SelectedSource == nil {
				t.Fatalf("first publication preview = %+v, %v", preview, err)
			}
			operationID, err := publications.AdmitFirst(ctx, releaseID, positionID, FirstPublicationConfirmation{PreviewDigest: preview.ContextDigest, ExpectedEpoch: epoch})
			if err != nil {
				t.Fatalf("admit first publication: %v", err)
			}
			manager := persistence.NewSetupManagerRepository(db)
			attempt, err := manager.ReadPublicationAttempt(ctx, operationID)
			if err != nil || attempt == nil {
				t.Fatalf("read admitted attempt = %+v, %v", attempt, err)
			}
			input, err := persistence.DecodePublicationInputs(attempt.FrozenInputs)
			if err != nil || input.Audio == nil {
				t.Fatalf("decode admitted inputs = %+v, %v", input, err)
			}
			root := input.Audio.ConfiguredRootPath
			source := filepath.Join(root, filepath.FromSlash(input.Audio.RelativePath))
			original, err := os.ReadFile(source)
			if err != nil {
				t.Fatalf("read admitted source: %v", err)
			}
			backup := source + ".retained"
			if err := os.Rename(source, backup); err != nil {
				t.Fatalf("move admitted source aside: %v", err)
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
			err = executor.ExecutePublicationDelivery(ctx, operationID, *op.RiverJobID, 1)
			if err == nil {
				t.Fatal("expected source-open failure")
			}
			if runtime.GOOS == "windows" {
				if !errors.Is(err, sourcefs.ErrUnsupported) || !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "open held publication source:") || !strings.Contains(err.Error(), "sync publication cleanup parent:") {
					t.Fatalf("expected source-open failure followed by unsupported cleanup sync, got %v", err)
				}
				retainedAttempt, readErr := manager.ReadPublicationAttempt(ctx, operationID)
				if readErr != nil || retainedAttempt == nil {
					t.Fatalf("read retained cleanup attempt: %v", readErr)
				}
				var intents, results int
				if readErr := db.NewRaw(`SELECT count(*) FILTER (WHERE entry->>'step'='intent'),count(*) FILTER (WHERE entry->>'step'='result') FROM track_publication_attempt a CROSS JOIN LATERAL jsonb_array_elements(a.mutation_journal) entry WHERE a.operation_id=? AND entry->>'kind'='publication_preparation_cleanup'`, operationID).Scan(ctx, &intents, &results); readErr != nil || intents != 1 || results != 0 {
					t.Fatalf("unacknowledged cleanup journal: intents=%d results=%d err=%v", intents, results, readErr)
				}
				output, _, readErr := registry.GetOutputDirectory(ctx)
				if readErr != nil {
					t.Fatal(readErr)
				}
				entries, readErr := os.ReadDir(filepath.Join(output, "analysis"))
				if readErr != nil || len(entries) != 0 {
					t.Fatalf("cleanup did not remove private directory: %v, %v", entries, readErr)
				}
				// Native directory sync is unsupported, so removal cannot be
				// durably acknowledged and the attempt must remain recoverable.
				for _, table := range []string{"track_publication_attempt", "publication_execution_claim", "publication_source_hold"} {
					var count int
					if err := db.NewRaw(`SELECT count(*) FROM `+table+` WHERE operation_id=?`, operationID).Scan(ctx, &count); err != nil || count != 1 {
						t.Fatalf("Windows uncertain cleanup lost %s: count=%d err=%v", table, count, err)
					}
				}
				retained, err := os.ReadFile(backup)
				if err != nil || !bytes.Equal(retained, original) {
					t.Fatalf("retained original source changed: %v", err)
				}
				return
			}
			failed, err := manager.GetOperation(ctx, operationID)
			if err != nil || failed == nil || failed.State != "failed" || failed.SafeError == nil || *failed.SafeError == "" {
				t.Fatalf("settled operation = %+v, %v", failed, err)
			}
			// The private child is removed; the stable analysis namespace parent remains.
			output, configured, err := registry.GetOutputDirectory(ctx)
			if err != nil || !configured {
				t.Fatalf("output directory = %q, %v, %v", output, configured, err)
			}
			if _, err := os.Stat(filepath.Join(output, "analysis")); err != nil {
				t.Fatalf("analysis namespace parent not retained: %v", err)
			}
			entries, err := os.ReadDir(filepath.Join(output, "analysis"))
			if err != nil || len(entries) != 0 {
				t.Fatalf("private preparation children = %v, %v", entries, err)
			}
			for _, table := range []string{"track_publication_attempt", "publication_execution_claim", "publication_source_hold", "operation_tool_read_hold"} {
				var count int
				if err := db.NewRaw(`SELECT count(*) FROM `+table+` WHERE operation_id=?`, operationID).Scan(ctx, &count); err != nil || count != 0 {
					t.Fatalf("retained %s = %d, %v", table, count, err)
				}
			}
			var reservations int
			if err := db.NewRaw(`SELECT count(*) FROM publication_namespace_reservation WHERE position_id=?`, positionID).Scan(ctx, &reservations); err != nil || reservations != 0 {
				t.Fatalf("retained reservations = %d, %v", reservations, err)
			}
			retained, err := os.ReadFile(backup)
			if err != nil || !bytes.Equal(retained, original) {
				t.Fatalf("retained original source changed: %v", err)
			}
			// The execution-claim check above also proves that error handling did
			// not quarantine/recreate ownership after acknowledged settlement.
		})
}
