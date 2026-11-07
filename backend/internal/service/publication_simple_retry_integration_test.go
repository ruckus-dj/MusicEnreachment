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

func TestPublicationInterruptedFirstAudioRecoversInstalledTargetPostgreSQL(t *testing.T) {
	testPublicationSourceExecution(t, "Track.flac", true, false, publicationDeliveryScenario{run: func(ctx context.Context, db *bun.DB, manager *persistence.SetupManagerRepository, executor *PublicationExecutor, operationID uuid.UUID, riverJobID int64, sourceFile, output, target string, _ []byte) {
		writer := &countingPublicationMetadataWriter{delegate: metadata.Writer{}}
		executor.metadata = writer
		if _, err := db.ExecContext(ctx, `CREATE FUNCTION reject_test_publication_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected publication settlement failure'; END $$`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `CREATE TRIGGER reject_test_publication_insert BEFORE INSERT ON track_publication FOR EACH ROW EXECUTE FUNCTION reject_test_publication_insert()`); err != nil {
			t.Fatal(err)
		}
		err := executor.ExecutePublicationDelivery(ctx, operationID, riverJobID, 1)
		if err == nil || !strings.Contains(err.Error(), "injected publication settlement failure") {
			t.Fatalf("interrupted delivery error = %v, want injected settlement failure", err)
		}
		attempt, err := manager.ReadPublicationAttempt(ctx, operationID)
		if err != nil || attempt == nil {
			t.Fatalf("attempt after interrupted settlement = %+v, %v", attempt, err)
		}
		destination := filepath.Join(output, filepath.FromSlash(target))
		capture, err := (metadata.Reader{}).Read(ctx, destination, metadata.ReadRequest{})
		if err != nil {
			t.Fatalf("read installed audio before interruption: %v", err)
		}
		capture.Tags["LYRICS"] = []string{"[00:01.00]Observed lyrics"}
		if _, err := (metadata.Writer{}).Write(ctx, destination, capture.Tags, nil); err != nil {
			t.Fatalf("write observed lyrics to installed audio: %v", err)
		}
		if _, err := db.ExecContext(ctx, `DROP TRIGGER reject_test_publication_insert ON track_publication`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `DROP FUNCTION reject_test_publication_insert()`); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(sourceFile); err != nil {
			t.Fatal(err)
		}
		// Model a process restart while the old delivery still owns an executing
		// claim. The failed settlement deferred this claim to quiescent, so restore
		// the old executing state and boot identity explicitly to model interruption.
		var priorState string
		var priorBoot uuid.UUID
		if err := db.NewRaw(`SELECT state, boot_uuid FROM publication_execution_claim WHERE operation_id=?`, operationID).Scan(ctx, &priorState, &priorBoot); err != nil {
			t.Fatalf("read quiescent execution claim: %v", err)
		}
		if priorState != "quiescent" || priorBoot == uuid.Nil {
			t.Fatalf("claim after settlement error = state %q, boot %s; want quiescent claim with prior boot identity", priorState, priorBoot)
		}
		priorBoot = uuid.New()
		if priorBoot == executor.bootUUID {
			t.Fatal("simulated prior boot identity matches the current executor")
		}
		result, err := db.ExecContext(ctx, `UPDATE publication_execution_claim SET state='executing', boot_uuid=? WHERE operation_id=? AND state='quiescent'`, priorBoot, operationID)
		if err != nil {
			t.Fatal(err)
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			t.Fatalf("restore interrupted execution claim rows=%d, err=%v; want exactly one", affected, err)
		}
		var restoredState string
		var restoredBoot uuid.UUID
		if err := db.NewRaw(`SELECT state, boot_uuid FROM publication_execution_claim WHERE operation_id=?`, operationID).Scan(ctx, &restoredState, &restoredBoot); err != nil || restoredState != "executing" || restoredBoot != priorBoot {
			t.Fatalf("restored execution claim = state %q, boot %s, err=%v", restoredState, restoredBoot, err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=2 WHERE id=?`, riverJobID); err != nil {
			t.Fatal(err)
		}
		if err := executor.ExecutePublicationDelivery(ctx, operationID, riverJobID, 2); err != nil {
			t.Fatalf("recover installed first-audio output: %v", err)
		}
		if writer.calls != 1 {
			t.Fatalf("metadata preparation calls = %d, want only original interrupted preparation", writer.calls)
		}
		capture, err = (metadata.Reader{}).Read(ctx, destination, metadata.ReadRequest{CoverInspection: true})
		if err != nil || capture.Tags["TITLE"][0] != "Published track" || len(capture.Tags["LYRICS"]) != 1 || capture.Tags["LYRICS"][0] != "[00:01.00]Observed lyrics" {
			t.Fatalf("recovered installed metadata/lyrics = %#v, %v", capture.Tags, err)
		}
		var current persistence.TrackPublication
		if err := db.NewSelect().Model(&current).Where("position_id=?", attempt.PositionID).Scan(ctx); err != nil {
			t.Fatalf("read persisted publication after recovery: %v", err)
		}
		if len(current.InitialLyrics) != 0 && string(current.InitialLyrics) != "null" {
			t.Fatalf("persisted initial lyrics projection = %s; want unknown (nil)", current.InitialLyrics)
		}
		attempt, err = manager.ReadPublicationAttempt(ctx, operationID)
		if err != nil || attempt != nil {
			t.Fatalf("attempt after recovery = %+v, %v; want consumed", attempt, err)
		}
		var claims int
		if err := db.NewRaw(`SELECT count(*) FROM publication_execution_claim WHERE operation_id=?`, operationID).Scan(ctx, &claims); err != nil || claims != 0 {
			t.Fatalf("execution claims after recovery = %d, %v; want consumed", claims, err)
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
