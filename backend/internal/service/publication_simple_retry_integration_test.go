//go:build integration

package service

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/uptrace/bun"
)

func TestPublicationRejectsChangedSourceAfterToolResolutionPostgreSQL(t *testing.T) {
	t.Run("unchanged baseline publishes", func(t *testing.T) {
		testPublicationSourceExecution(t, "Track.flac", true)
	})
	for _, changed := range []string{"size", "mtime"} {
		t.Run(changed, func(t *testing.T) {
			var once sync.Once
			var mutated bool
			scenario := publicationDeliveryScenario{
				afterToolVersion: func(sourceFile string) {
					once.Do(func() {
						if err := mutatePublicationSourceForFreshnessTest(sourceFile, changed); err != nil {
							t.Fatal(err)
						}
						mutated = true
					})
				},
				run: func(ctx context.Context, db *bun.DB, manager *persistence.SetupManagerRepository, executor *PublicationExecutor, operationID uuid.UUID, riverJobID int64, _ string, output, target string, _ []byte) {
					attempt, err := manager.ReadPublicationAttempt(ctx, operationID)
					if err != nil || attempt == nil {
						t.Fatalf("read rejected publication attempt = %+v, %v", attempt, err)
					}
					err = executor.ExecutePublicationDelivery(ctx, operationID, riverJobID, 1)
					if !mutated {
						t.Fatal("tool-version barrier did not mutate the source")
					}
					if err == nil || !strings.Contains(err.Error(), "held publication source changed") {
						t.Fatalf("changed source delivery error = %v, want safe freshness rejection", err)
					}
					if _, err := os.Lstat(filepath.Join(output, filepath.FromSlash(target))); !os.IsNotExist(err) {
						t.Fatalf("changed-source publication was installed: %v", err)
					}
					var current persistence.TrackPublication
					err = db.NewSelect().Model(&current).Where("position_id=?", attempt.PositionID).Scan(ctx)
					if !errors.Is(err, sql.ErrNoRows) {
						t.Fatalf("CURRENT after rejected source mutation = %+v, %v; want no row", current, err)
					}
				},
			}
			testPublicationSourceExecution(t, "Track.flac", true, scenario)
		})
	}
}

func mutatePublicationSourceForFreshnessTest(sourceFile, changed string) error {
	contents, err := os.ReadFile(sourceFile)
	if err != nil {
		return err
	}
	info, err := os.Stat(sourceFile)
	if err != nil {
		return err
	}
	if changed == "size" {
		contents = append(contents, '!')
	} else {
		contents[0] ^= 1
	}
	if err := os.WriteFile(sourceFile, contents, 0o600); err != nil {
		return err
	}
	modified := info.ModTime()
	if changed == "mtime" {
		modified = modified.Add(2 * time.Second)
	}
	return os.Chtimes(sourceFile, modified, modified)
}

func TestPublicationSimpleDeliveryRetriesSettlementAndAcknowledgesRedeliveryPostgreSQL(t *testing.T) {
	testPublicationSourceExecution(t, "Track.flac", true, false, publicationDeliveryScenario{run: func(ctx context.Context, db *bun.DB, manager *persistence.SetupManagerRepository, executor *PublicationExecutor, operationID uuid.UUID, riverJobID int64, sourceFile, output, target string, sourceBytes []byte) {
		writer := &countingPublicationMetadataWriter{delegate: metadata.Writer{}}
		executor.metadata = writer
		if _, err := db.ExecContext(ctx, `CREATE FUNCTION reject_test_publication_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected publication settlement failure'; END $$`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `CREATE TRIGGER reject_test_publication_insert BEFORE INSERT ON track_publication FOR EACH ROW EXECUTE FUNCTION reject_test_publication_insert()`); err != nil {
			t.Fatal(err)
		}
		defer func() {
			_, _ = db.ExecContext(context.Background(), `DROP TRIGGER IF EXISTS reject_test_publication_insert ON track_publication`)
			_, _ = db.ExecContext(context.Background(), `DROP FUNCTION IF EXISTS reject_test_publication_insert()`)
		}()

		err := executor.ExecutePublicationDelivery(ctx, operationID, riverJobID, 1)
		if err == nil || !strings.Contains(err.Error(), "injected publication settlement failure") {
			t.Fatalf("first delivery error = %v, want injected publication settlement failure", err)
		}
		destination := filepath.Join(output, filepath.FromSlash(target))
		if _, err := os.Stat(destination); err != nil {
			t.Fatalf("installed destination after settlement rollback: %v", err)
		}
		actualSource, err := os.ReadFile(sourceFile)
		if err != nil || string(actualSource) != string(sourceBytes) {
			t.Fatalf("source after failed delivery changed: %v", err)
		}
		attempt, err := manager.ReadPublicationAttempt(ctx, operationID)
		if err != nil || attempt == nil {
			t.Fatalf("attempt after settlement rollback = %+v, %v", attempt, err)
		}
		if _, err := db.ExecContext(ctx, `DROP TRIGGER reject_test_publication_insert ON track_publication`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `DROP FUNCTION reject_test_publication_insert()`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE river_job SET attempt=2,state='running' WHERE id=?`, riverJobID); err != nil {
			t.Fatal(err)
		}
		if err := executor.ExecutePublicationDelivery(ctx, operationID, riverJobID, 2); err != nil {
			t.Fatalf("retry delivery: %v", err)
		}
		if writer.calls != 2 {
			t.Fatalf("metadata preparation calls = %d, want fresh preparation on retry (2)", writer.calls)
		}
		if err := os.Remove(sourceFile); err != nil {
			t.Fatal(err)
		}
		if err := executor.ExecutePublicationDelivery(ctx, operationID, riverJobID, 2); err != nil {
			t.Fatalf("completed delivery redelivery: %v", err)
		}
		if writer.calls != 2 {
			t.Fatalf("completed delivery redid preparation: metadata calls=%d", writer.calls)
		}
		attempt, err = manager.ReadPublicationAttempt(ctx, operationID)
		if err != nil || attempt != nil {
			t.Fatalf("attempt after successful delivery = %+v, %v; want none", attempt, err)
		}
	}})
}

type countingPublicationMetadataWriter struct {
	delegate PublicationMetadataWriter
	calls    int
}

func (writer *countingPublicationMetadataWriter) Write(ctx context.Context, path string, tags map[string][]string, cover *metadata.Cover) (metadata.WriteResult, error) {
	writer.calls++
	return writer.delegate.Write(ctx, path, tags, cover)
}

func (writer *countingPublicationMetadataWriter) WriteFlacTagsPreservingArtwork(ctx context.Context, path string, tags map[string][]string, before metadata.FlacArtworkSnapshot) (metadata.WriteResult, error) {
	writer.calls++
	return metadata.WriteFlacTagsPreservingArtwork(ctx, path, tags, before)
}
