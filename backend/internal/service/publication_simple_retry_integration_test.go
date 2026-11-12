//go:build integration

package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	integrationtools "github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/uptrace/bun"
	"runtime"
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

func TestSourceRepublishRetainsSelectedSourceAndCompletedRedeliveryIsNoopPostgreSQL(t *testing.T) {
	testPublicationSourceExecution(t, "Track.flac", true, false, sourceEncoderTagFixture{}, publicationDeliveryScenario{run: func(ctx context.Context, db *bun.DB, manager *persistence.SetupManagerRepository, executor *PublicationExecutor, firstOperationID uuid.UUID, firstRiverJobID int64, sourceFile, output, target string, _ []byte) {
		if err := executor.ExecutePublicationDelivery(ctx, firstOperationID, firstRiverJobID, 1); err != nil {
			t.Fatalf("first publication delivery: %v", err)
		}
		firstOperation, err := manager.GetOperation(ctx, firstOperationID)
		if err != nil || firstOperation.State != "succeeded" {
			t.Fatalf("first publication operation = %+v, %v", firstOperation, err)
		}
		var before persistence.TrackPublication
		if err := db.NewSelect().Model(&before).Order("generation DESC").Limit(1).Scan(ctx); err != nil {
			t.Fatalf("read initial CURRENT: %v", err)
		}
		var releaseID uuid.UUID
		if err := db.NewRaw(`SELECT release_id FROM release_track WHERE id=?`, before.PositionID).Scan(ctx, &releaseID); err != nil {
			t.Fatalf("read release for CURRENT position: %v", err)
		}
		metadataService := NewLibraryMetadataService(persistence.NewLibraryRepository(db))
		positionMetadata, err := metadataService.Read(ctx, librarymetadata.ScopePosition, releaseID, before.PositionID)
		if err != nil {
			t.Fatalf("read position metadata for lyrics authority: %v", err)
		}
		lyrics, _ := json.Marshal(map[string]any{"state": "explicit_empty"})
		if _, err := metadataService.Update(ctx, librarymetadata.ScopePosition, releaseID, before.PositionID, positionMetadata.Revision, map[string]json.RawMessage{"synced_lyrics": lyrics}); err != nil {
			t.Fatalf("set explicit empty lyrics authority: %v", err)
		}
		registry := settings.New(persistence.NewSettingsRepository(db), nil)
		if err := registry.SetLRCLIBEnabled(ctx, false); err != nil {
			t.Fatalf("disable LRCLIB for explicit-empty lyrics fixture: %v", err)
		}
		publications := NewLibraryPublications(manager, registry, libraryPublicationTestInserter{}, integrationtools.Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH})
		updateComment := func(value string) {
			current, err := metadataService.Read(ctx, librarymetadata.ScopeRelease, releaseID, releaseID)
			if err != nil {
				t.Fatalf("read desired comment metadata: %v", err)
			}
			comment, _ := json.Marshal(map[string]any{"state": "value", "value": value})
			updated, err := metadataService.Update(ctx, librarymetadata.ScopeRelease, releaseID, releaseID, current.Revision, map[string]json.RawMessage{"comment": comment})
			if err != nil || updated.Revision <= current.Revision {
				t.Fatalf("update desired comment revision %d => %d: %v", current.Revision, updated.Revision, err)
			}
		}
		admitRepublish := func() (*persistence.TrackPublicationAttempt, int64) {
			prepared, err := publications.prepareRetainedSourceRepublish(ctx, releaseID, before.PositionID, firstPublicationPreviewModeOrdinary)
			if err != nil || len(prepared.Preview.Blockers) != 0 || prepared.Selected == nil ||
				prepared.Preview.ConfirmationCapability == nil || prepared.Preview.ConfirmationCapability.CurrentGeneration == uuid.Nil || prepared.Preview.Epoch == uuid.Nil {
				t.Fatalf("prepare ordinary selected-source republish: %+v, %v", prepared, err)
			}
			operationID, err := publications.AdmitSourceRepublishRetainingSelection(ctx, releaseID, before.PositionID, SourceRepublishConfirmation{
				PreviewDigest: prepared.Preview.ContextDigest, ExpectedEpoch: prepared.Preview.Epoch,
				ExpectedGeneration: prepared.Preview.ConfirmationCapability.CurrentGeneration, Mode: firstPublicationPreviewModeOrdinary,
			})
			if err != nil {
				t.Fatalf("admit selected-source republish: %v", err)
			}
			attempt, err := manager.ReadPublicationAttempt(ctx, operationID)
			if err != nil || attempt == nil {
				t.Fatalf("read admitted source-republish attempt: %+v, %v", attempt, err)
			}
			var frozen persistence.PublicationInputs
			if err := json.Unmarshal(attempt.FrozenInputs, &frozen); err != nil || frozen.SourceRepublish == nil || frozen.SourceRepublish.Association.LocationID != prepared.Selected.Selection.LocationID || frozen.SourceRepublish.Association.MediaVariantID != prepared.Selected.Selection.MediaVariantID {
				t.Fatalf("frozen selected-source association = %+v, %v", frozen.SourceRepublish, err)
			}
			for key, want := range map[string]string{
				"ENCODEDBY":       "fixture source encoder",
				"ENCODING":        "fixture encoder version",
				"ENCODERSETTINGS": "fixture parameters",
			} {
				if got := frozen.DesiredTags[key]; len(got) != 1 || got[0] != want {
					t.Fatalf("frozen copied-source tag %s = %#v, want %q", key, got, want)
				}
			}
			op, err := manager.GetOperation(ctx, operationID)
			if err != nil || op.RiverJobID == nil {
				t.Fatalf("read admitted source-republish operation: %+v, %v", op, err)
			}
			if _, err := db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=1 WHERE id=?`, *op.RiverJobID); err != nil {
				t.Fatal(err)
			}
			return attempt, *op.RiverJobID
		}
		baselineAuthority, err := manager.ReadPositionSourceAuthority(ctx, before.PositionID)
		if err != nil || baselineAuthority == nil {
			t.Fatalf("read baseline source authority = %+v, %v", baselineAuthority, err)
		}
		updateComment("before-move recovery")
		beforeMoveAttempt, beforeMoveJobID := admitRepublish()
		writer := &countingSourceRepublishMetadataWriter{delegate: metadata.Writer{}}
		executor.metadata = writer
		priorBoot := uuid.New()
		executor.commands = audioDeliveryTestCommands{afterVersion: func() { panic("simulated process interruption before candidate installation") }}
		if err := executor.ExecutePublicationDelivery(ctx, beforeMoveAttempt.OperationID, beforeMoveJobID, 1); err == nil || !strings.Contains(err.Error(), "simulated process interruption") {
			t.Fatalf("before-move interrupted delivery error = %v", err)
		}
		oldTarget, err := (metadata.Reader{}).Read(ctx, filepath.Join(output, filepath.FromSlash(target)), metadata.ReadRequest{})
		if err != nil || len(oldTarget.Tags["COMMENT"]) != 0 {
			t.Fatalf("CURRENT target after before-move interruption = %#v, %v; want original metadata", oldTarget.Tags, err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE publication_execution_claim SET state='executing',boot_uuid=? WHERE operation_id=?`, priorBoot, beforeMoveAttempt.OperationID); err != nil {
			t.Fatal(err)
		}
		partial := filepath.Join(output, ".publication-delivery-interrupted")
		if err := os.Mkdir(partial, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(partial, "partial"), []byte("partial candidate"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=1 WHERE id=?`, beforeMoveJobID); err != nil {
			t.Fatal(err)
		}
		executor = NewPublicationExecutor(manager, registry, integrationtools.Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, publication.NewCoordinator(), uuid.New(), audioDeliveryTestCommands{})
		executor.metadata = writer
		if err := executor.ExecutePublicationDelivery(ctx, beforeMoveAttempt.OperationID, beforeMoveJobID, 1); err != nil {
			t.Fatalf("before-move source-backed recovery: %v", err)
		}
		if writer.calls != 1 {
			t.Fatalf("writer calls after before-move source-backed recovery = %d, want 1", writer.calls)
		}
		var firstRepublish persistence.TrackPublication
		if err := db.NewSelect().Model(&firstRepublish).Where("position_id=?", before.PositionID).Scan(ctx); err != nil || firstRepublish.Generation != beforeMoveAttempt.CandidateGeneration {
			t.Fatalf("CURRENT after before-move recovery = %+v, %v", firstRepublish, err)
		}
		firstOutput, err := (metadata.Reader{}).Read(ctx, filepath.Join(output, filepath.FromSlash(target)), metadata.ReadRequest{})
		if err != nil || len(firstOutput.Tags["COMMENT"]) != 1 || firstOutput.Tags["COMMENT"][0] != "before-move recovery" {
			t.Fatalf("native before-move recovery output = %#v, %v", firstOutput.Tags, err)
		}

		updateComment("after-move recovery")
		afterMoveAttempt, afterMoveJobID := admitRepublish()
		var baselineRows int
		if err := db.NewRaw(`SELECT count(*) FROM track_publication WHERE position_id=? AND generation=?`, before.PositionID, firstRepublish.Generation).Scan(ctx, &baselineRows); err != nil || baselineRows != 1 {
			t.Fatalf("prove committed before-fault baseline: %v", err)
		}
		if _, err := db.ExecContext(ctx, `CREATE FUNCTION reject_test_source_republish_current_update() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected source-republish CURRENT update failure'; END $$`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `CREATE TRIGGER reject_test_source_republish_current_update BEFORE UPDATE ON track_publication FOR EACH ROW EXECUTE FUNCTION reject_test_source_republish_current_update()`); err != nil {
			t.Fatal(err)
		}
		err = executor.ExecutePublicationDelivery(ctx, afterMoveAttempt.OperationID, afterMoveJobID, 1)
		if err == nil || !strings.Contains(err.Error(), "injected source-republish CURRENT update failure") {
			t.Fatalf("after-move settlement error = %v; want injected CURRENT update failure", err)
		}
		if _, err := db.ExecContext(ctx, `DROP TRIGGER reject_test_source_republish_current_update ON track_publication`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `DROP FUNCTION reject_test_source_republish_current_update()`); err != nil {
			t.Fatal(err)
		}
		attempt, err := manager.ReadPublicationAttempt(ctx, afterMoveAttempt.OperationID)
		if err != nil || attempt == nil || attempt.ResumeMode != persistence.PublicationResumeModePrepare {
			t.Fatalf("after-move attempt before redelivery = %+v, %v; want preparation mode before prior-boot recovery", attempt, err)
		}
		var committedBeforeFault persistence.TrackPublication
		if err := db.NewSelect().Model(&committedBeforeFault).Where("position_id=?", before.PositionID).Scan(ctx); err != nil || committedBeforeFault.Generation != firstRepublish.Generation {
			t.Fatalf("CURRENT after injected result rollback = %+v, %v; want prior committed generation", committedBeforeFault, err)
		}
		installed, err := (metadata.Reader{}).Read(ctx, filepath.Join(output, filepath.FromSlash(target)), metadata.ReadRequest{})
		if err != nil || len(installed.Tags["COMMENT"]) != 1 || installed.Tags["COMMENT"][0] != "after-move recovery" {
			t.Fatalf("installed after-move candidate before recovery = %#v, %v", installed.Tags, err)
		}
		// Ordinary redelivery after the result transaction rolls back leaves the
		// claim quiescent. It must freshly prepare from the still-present source,
		// even though the candidate had already replaced CURRENT.
		if _, err := db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=2 WHERE id=?`, afterMoveJobID); err != nil {
			t.Fatal(err)
		}
		if err := executor.ExecutePublicationDelivery(ctx, afterMoveAttempt.OperationID, afterMoveJobID, 2); err != nil {
			t.Fatalf("ordinary quiescent after-move redelivery: %v", err)
		}
		if writer.calls != 3 {
			t.Fatalf("writer calls after ordinary after-move redelivery = %d, want full fresh preparation (3)", writer.calls)
		}
		var ordinarilySettled persistence.TrackPublication
		if err := db.NewSelect().Model(&ordinarilySettled).Where("position_id=?", before.PositionID).Scan(ctx); err != nil || ordinarilySettled.Generation != afterMoveAttempt.CandidateGeneration {
			t.Fatalf("CURRENT after ordinary after-move redelivery = %+v, %v", ordinarilySettled, err)
		}
		attempt, err = manager.ReadPublicationAttempt(ctx, afterMoveAttempt.OperationID)
		if err != nil || attempt != nil {
			t.Fatalf("attempt after ordinary after-move redelivery = %+v, %v; want settled", attempt, err)
		}

		// A second valid operation proves the missing-final-target path. Fail
		// settlement after the native replacement, remove that installed final
		// using the fixture's real output path, then redeliver with source intact.
		updateComment("missing-final retry")
		afterMoveAttempt, afterMoveJobID = admitRepublish()
		if _, err := db.ExecContext(ctx, `CREATE FUNCTION reject_test_source_republish_current_update() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected source-republish CURRENT update failure'; END $$`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `CREATE TRIGGER reject_test_source_republish_current_update BEFORE UPDATE ON track_publication FOR EACH ROW EXECUTE FUNCTION reject_test_source_republish_current_update()`); err != nil {
			t.Fatal(err)
		}
		if err := executor.ExecutePublicationDelivery(ctx, afterMoveAttempt.OperationID, afterMoveJobID, 1); err == nil || !strings.Contains(err.Error(), "injected source-republish CURRENT update failure") {
			t.Fatalf("missing-final setup settlement error = %v", err)
		}
		if _, err := db.ExecContext(ctx, `DROP TRIGGER reject_test_source_republish_current_update ON track_publication`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `DROP FUNCTION reject_test_source_republish_current_update()`); err != nil {
			t.Fatal(err)
		}
		finalPath := filepath.Join(output, filepath.FromSlash(target))
		if err := os.Remove(finalPath); err != nil {
			t.Fatalf("remove installed final target before retry: %v", err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=2 WHERE id=?`, afterMoveJobID); err != nil {
			t.Fatal(err)
		}
		if err := executor.ExecutePublicationDelivery(ctx, afterMoveAttempt.OperationID, afterMoveJobID, 2); err != nil {
			t.Fatalf("missing-final source-backed retry: %v", err)
		}
		if writer.calls != 5 {
			t.Fatalf("writer calls after missing-final retry = %d, want full source preparation (5)", writer.calls)
		}
		missingFinalOutput, err := (metadata.Reader{}).Read(ctx, finalPath, metadata.ReadRequest{})
		if err != nil || len(missingFinalOutput.Tags["COMMENT"]) != 1 || missingFinalOutput.Tags["COMMENT"][0] != "missing-final retry" {
			t.Fatalf("missing-final retry output = %#v, %v", missingFinalOutput.Tags, err)
		}
		var missingFinalCurrent persistence.TrackPublication
		if err := db.NewSelect().Model(&missingFinalCurrent).Where("position_id=?", before.PositionID).Scan(ctx); err != nil || missingFinalCurrent.Generation != afterMoveAttempt.CandidateGeneration {
			t.Fatalf("CURRENT after missing-final retry = %+v, %v", missingFinalCurrent, err)
		}
		attempt, err = manager.ReadPublicationAttempt(ctx, afterMoveAttempt.OperationID)
		if err != nil || attempt != nil {
			t.Fatalf("attempt after missing-final retry = %+v, %v; want settled", attempt, err)
		}

		// An interrupted republish must reprepare from its frozen source when the
		// same-path target was externally modified and fails content validation.
		// The observed identity must still fence the eventual atomic replacement.
		oldCurrentBytes, err := os.ReadFile(finalPath)
		if err != nil {
			t.Fatalf("read CURRENT before ownership-drift scenario: %v", err)
		}
		updateComment("ownership drift replacement")
		afterMoveAttempt, afterMoveJobID = admitRepublish()
		if _, err := db.ExecContext(ctx, `CREATE FUNCTION reject_test_source_republish_current_update() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected source-republish CURRENT update failure'; END $$`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `CREATE TRIGGER reject_test_source_republish_current_update BEFORE UPDATE ON track_publication FOR EACH ROW EXECUTE FUNCTION reject_test_source_republish_current_update()`); err != nil {
			t.Fatal(err)
		}
		if err := executor.ExecutePublicationDelivery(ctx, afterMoveAttempt.OperationID, afterMoveJobID, 1); err == nil || !strings.Contains(err.Error(), "injected source-republish CURRENT update failure") {
			t.Fatalf("external-target setup settlement error = %v", err)
		}
		if _, err := db.ExecContext(ctx, `DROP TRIGGER reject_test_source_republish_current_update ON track_publication`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `DROP FUNCTION reject_test_source_republish_current_update()`); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(finalPath, oldCurrentBytes, 0o600); err != nil {
			t.Fatalf("restore old CURRENT bytes: %v", err)
		}
		externallyModified, err := (metadata.Reader{}).Read(ctx, finalPath, metadata.ReadRequest{})
		if err != nil {
			t.Fatalf("read target for external modification: %v", err)
		}
		externallyModified.Tags["COMMENT"] = []string{"external modification"}
		if _, err := (metadata.Writer{}).Write(ctx, finalPath, externallyModified.Tags, nil); err != nil {
			t.Fatalf("externally modify interrupted target: %v", err)
		}
		oldInfo, err := os.Stat(finalPath)
		if err != nil {
			t.Fatal(err)
		}
		driftedMtime := oldInfo.ModTime().Add(2 * time.Second)
		if err := os.Chtimes(finalPath, driftedMtime, driftedMtime); err != nil {
			t.Fatalf("drift old CURRENT mtime: %v", err)
		}
		oldBoot := uuid.New()
		if oldBoot == executor.bootUUID {
			t.Fatal("simulated prior boot identity matches current executor")
		}
		result, err := db.ExecContext(ctx, `UPDATE publication_execution_claim SET state='executing',boot_uuid=? WHERE operation_id=? AND state='quiescent'`, oldBoot, afterMoveAttempt.OperationID)
		if err != nil {
			t.Fatal(err)
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			t.Fatalf("restore external-target old claim rows=%d err=%v", affected, err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=1 WHERE id=?`, afterMoveJobID); err != nil {
			t.Fatal(err)
		}
		executor = NewPublicationExecutor(manager, registry, integrationtools.Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, publication.NewCoordinator(), uuid.New(), audioDeliveryTestCommands{})
		executor.metadata = writer
		writer.failNextOptionalAssets = true
		if err := executor.ExecutePublicationDelivery(ctx, afterMoveAttempt.OperationID, afterMoveJobID, 1); err == nil || !strings.Contains(err.Error(), "injected source-republish preparation failure") {
			t.Fatalf("interrupted source-republish preparation error = %v", err)
		}
		attempt, err = manager.ReadPublicationAttempt(ctx, afterMoveAttempt.OperationID)
		if err != nil || attempt == nil || attempt.ResumeMode != persistence.PublicationResumeModeInspectSourceRepublishTarget {
			t.Fatalf("inspection mode after failed source-backed preparation = %+v, %v", attempt, err)
		}
		stillExternal, err := (metadata.Reader{}).Read(ctx, finalPath, metadata.ReadRequest{})
		if err != nil || len(stillExternal.Tags["COMMENT"]) != 1 || stillExternal.Tags["COMMENT"][0] != "external modification" {
			t.Fatalf("target after failed source-backed preparation = %#v, %v", stillExternal.Tags, err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=2 WHERE id=?`, afterMoveJobID); err != nil {
			t.Fatal(err)
		}
		if err := executor.ExecutePublicationDelivery(ctx, afterMoveAttempt.OperationID, afterMoveJobID, 2); err != nil {
			t.Fatalf("externally modified target source-backed recovery retry: %v", err)
		}
		if writer.calls != 8 {
			t.Fatalf("writer calls after external-target recovery = %d, want failed and successful preparations (8)", writer.calls)
		}
		driftedOutput, err := (metadata.Reader{}).Read(ctx, finalPath, metadata.ReadRequest{})
		if err != nil || len(driftedOutput.Tags["COMMENT"]) != 1 || driftedOutput.Tags["COMMENT"][0] != "ownership drift replacement" {
			t.Fatalf("external-target replacement output = %#v, %v", driftedOutput.Tags, err)
		}
		var driftedCurrent persistence.TrackPublication
		if err := db.NewSelect().Model(&driftedCurrent).Where("position_id=?", before.PositionID).Scan(ctx); err != nil || driftedCurrent.Generation != afterMoveAttempt.CandidateGeneration {
			t.Fatalf("CURRENT after external-target recovery = %+v, %v", driftedCurrent, err)
		}

		// Keep source-gone interrupted adoption independent: create a fresh
		// attempt, fail after replacement again, then model process interruption.
		updateComment("after-move source-gone recovery")
		afterMoveAttempt, afterMoveJobID = admitRepublish()
		if _, err := db.ExecContext(ctx, `CREATE FUNCTION reject_test_source_republish_current_update() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected source-republish CURRENT update failure'; END $$`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `CREATE TRIGGER reject_test_source_republish_current_update BEFORE UPDATE ON track_publication FOR EACH ROW EXECUTE FUNCTION reject_test_source_republish_current_update()`); err != nil {
			t.Fatal(err)
		}
		if err := executor.ExecutePublicationDelivery(ctx, afterMoveAttempt.OperationID, afterMoveJobID, 1); err == nil || !strings.Contains(err.Error(), "injected source-republish CURRENT update failure") {
			t.Fatalf("source-gone setup settlement error = %v", err)
		}
		if _, err := db.ExecContext(ctx, `DROP TRIGGER reject_test_source_republish_current_update ON track_publication`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `DROP FUNCTION reject_test_source_republish_current_update()`); err != nil {
			t.Fatal(err)
		}
		oldBoot = uuid.New()
		if oldBoot == executor.bootUUID {
			t.Fatal("simulated prior boot identity matches current executor")
		}
		result, err = db.ExecContext(ctx, `UPDATE publication_execution_claim SET state='executing',boot_uuid=? WHERE operation_id=? AND state='quiescent'`, oldBoot, afterMoveAttempt.OperationID)
		if err != nil {
			t.Fatal(err)
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			t.Fatalf("restore after-move old claim rows=%d err=%v", affected, err)
		}
		if err := os.Remove(sourceFile); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=1 WHERE id=?`, afterMoveJobID); err != nil {
			t.Fatal(err)
		}
		executor = NewPublicationExecutor(manager, registry, integrationtools.Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, publication.NewCoordinator(), uuid.New(), audioDeliveryTestCommands{})
		executor.metadata = writer
		inspectedStickyMode := false
		executor.commands = audioDeliveryTestCommands{afterVersion: func() {
			liveAttempt, err := manager.ReadPublicationAttempt(ctx, afterMoveAttempt.OperationID)
			if err != nil || liveAttempt == nil || liveAttempt.ResumeMode != persistence.PublicationResumeModeInspectSourceRepublishTarget {
				t.Fatalf("sticky source-republish recovery mode during source-free redelivery = %+v, %v", liveAttempt, err)
			}
			inspectedStickyMode = true
		}}
		if err := executor.ExecutePublicationDelivery(ctx, afterMoveAttempt.OperationID, afterMoveJobID, 1); err != nil {
			t.Fatalf("source-free after-move inspection recovery: %v", err)
		}
		if !inspectedStickyMode {
			t.Fatal("source-free recovery did not observe sticky inspection mode")
		}
		if writer.calls != 9 {
			t.Fatalf("writer calls after source-free inspection recovery = %d, want no re-preparation", writer.calls)
		}
		var recovered persistence.TrackPublication
		if err := db.NewSelect().Model(&recovered).Where("position_id=?", before.PositionID).Scan(ctx); err != nil || recovered.Generation != afterMoveAttempt.CandidateGeneration {
			t.Fatalf("CURRENT after source-free inspection = %+v, %v", recovered, err)
		}
		assets, err := persistence.DecodePublicationAssetOutcomes(recovered.ActualAssets)
		if err != nil || assets.Cover.State != persistence.PublicationAssetAbsent || assets.Lyrics.State != persistence.PublicationAssetAbsent {
			t.Fatalf("recovered actual optional-asset outcomes = %+v, %v", assets, err)
		}
		if err := executor.ExecutePublicationDelivery(ctx, afterMoveAttempt.OperationID, afterMoveJobID, 1); err != nil {
			t.Fatalf("completed source-free redelivery: %v", err)
		}
		if writer.calls != 9 {
			t.Fatalf("writer calls after completed redelivery = %d, want 9", writer.calls)
		}
	}})
}

func TestPublicationInterruptedFirstAudioRecoversInstalledTargetPostgreSQL(t *testing.T) {
	run := func(t *testing.T, selectedCover []byte) {
		testPublicationSourceExecution(t, "Track.flac", true, false, publicationDeliveryScenario{
			beforeAdmission: func(ctx context.Context, db *bun.DB, _ *persistence.SetupManagerRepository, releaseID uuid.UUID) {
				if len(selectedCover) == 0 {
					return
				}
				library := persistence.NewLibraryRepository(db)
				coverService := NewReleaseCovers(library)
				saved, err := coverService.Save(ctx, releaseID, 1, selectedCover, "image/png")
				if err != nil || saved.PendingGeneration == nil {
					t.Fatalf("save selected cover = %+v, %v", saved, err)
				}
				if _, err := library.ApplyPendingReleaseCover(ctx, releaseID, saved.Revision, *saved.PendingGeneration); err != nil {
					t.Fatalf("apply selected cover: %v", err)
				}
			},
			run: func(ctx context.Context, db *bun.DB, manager *persistence.SetupManagerRepository, executor *PublicationExecutor, operationID uuid.UUID, riverJobID int64, sourceFile, output, target string, _ []byte) {
				if len(selectedCover) != 0 {
					executor.commands = simpleCoverDeliveryCommands{}
				}
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
				initialPreparationCalls := writer.calls
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
				if initialPreparationCalls == 0 || writer.calls != initialPreparationCalls {
					t.Fatalf("metadata preparation calls = %d, want only original interrupted preparation (%d)", writer.calls, initialPreparationCalls)
				}
				capture, err = (metadata.Reader{}).Read(ctx, destination, metadata.ReadRequest{CoverInspection: true})
				if err != nil || capture.Tags["TITLE"][0] != "Published track" || len(capture.Tags["LYRICS"]) != 1 || capture.Tags["LYRICS"][0] != "[00:01.00]Observed lyrics" {
					t.Fatalf("recovered installed metadata/lyrics = %#v, %v", capture.Tags, err)
				}
				if len(selectedCover) != 0 && (capture.CoverInspection == nil || capture.CoverInspection.Cover == nil) {
					t.Fatal("recovered selected cover is absent from installed audio")
				}
				var current persistence.TrackPublication
				if err := db.NewSelect().Model(&current).Where("position_id=?", attempt.PositionID).Scan(ctx); err != nil {
					t.Fatalf("read persisted publication after recovery: %v", err)
				}
				if len(selectedCover) != 0 {
					var projection persistence.PublicationCoverEmbeddingProjection
					if err := json.Unmarshal(current.CoverEmbedding, &projection); err != nil {
						t.Fatalf("decode persisted cover embedding: %v", err)
					}
					requestedDigest := sha256.Sum256(selectedCover)
					observedAssets, err := ObservePublicationAssets(capture)
					if err != nil || projection.Version != persistence.PublicationCoverEmbeddingDerivativeProjectionVersion ||
						projection.Outcome != "verified" || projection.RequestedSHA256 != hex.EncodeToString(requestedDigest[:]) ||
						projection.EmbeddedSHA256 != observedAssets.Cover.SHA256 || projection.EmbeddedMIME != observedAssets.Cover.MIME {
						t.Fatalf("persisted cover projection = %+v, observed assets = %+v, err=%v", projection, observedAssets, err)
					}
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
			},
		})
	}
	t.Run("absent receipt", func(t *testing.T) { run(t, nil) })
	t.Run("selected cover", func(t *testing.T) {
		run(t, simpleCoverPNG(t, color.RGBA{R: 20, G: 40, B: 60, A: 255}))
	})
}

type countingPublicationMetadataWriter struct {
	delegate metadata.Writer
	calls    int
}

func (writer *countingPublicationMetadataWriter) Write(ctx context.Context, path string, tags map[string][]string, cover *metadata.Cover) (metadata.WriteResult, error) {
	writer.calls++
	return writer.delegate.Write(ctx, path, tags, cover)
}

func (writer *countingPublicationMetadataWriter) WriteWithOptionalAssets(ctx context.Context, path string, tags map[string][]string, cover *metadata.Cover) (metadata.OptionalAssetsWriteResult, error) {
	writer.calls++
	return writer.delegate.WriteWithOptionalAssets(ctx, path, tags, cover)
}

// This wrapper is deliberately limited to the source-republish scenario: it
// observes optional-asset writes without changing the capabilities advertised
// by the first-publication retry writer used by other tests.
type countingSourceRepublishMetadataWriter struct {
	delegate               metadata.Writer
	calls                  int
	failNextOptionalAssets bool
}

func (writer *countingSourceRepublishMetadataWriter) Write(ctx context.Context, path string, tags map[string][]string, cover *metadata.Cover) (metadata.WriteResult, error) {
	writer.calls++
	return writer.delegate.Write(ctx, path, tags, cover)
}

func (writer *countingSourceRepublishMetadataWriter) WriteWithOptionalAssets(ctx context.Context, path string, tags map[string][]string, cover *metadata.Cover) (metadata.OptionalAssetsWriteResult, error) {
	writer.calls++
	if writer.failNextOptionalAssets {
		writer.failNextOptionalAssets = false
		return metadata.OptionalAssetsWriteResult{}, errors.New("injected source-republish preparation failure")
	}
	return writer.delegate.WriteWithOptionalAssets(ctx, path, tags, cover)
}

func (writer *countingPublicationMetadataWriter) WriteFlacTagsPreservingArtwork(ctx context.Context, path string, tags map[string][]string, before metadata.FlacArtworkSnapshot) (metadata.WriteResult, error) {
	writer.calls++
	return metadata.WriteFlacTagsPreservingArtwork(ctx, path, tags, before)
}
