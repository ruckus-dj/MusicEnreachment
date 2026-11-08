//go:build integration

package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	integrationtools "github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
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
	testPublicationSourceExecution(t, "Track.flac", true, false, publicationDeliveryScenario{run: func(ctx context.Context, db *bun.DB, manager *persistence.SetupManagerRepository, executor *PublicationExecutor, firstOperationID uuid.UUID, firstRiverJobID int64, sourceFile, output, target string, _ []byte) {
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
		currentMetadata, err := metadataService.Read(ctx, librarymetadata.ScopeRelease, releaseID, releaseID)
		if err != nil {
			t.Fatalf("read release metadata: %v", err)
		}
		comment, _ := json.Marshal(map[string]any{"state": "value", "value": "republished comment"})
		updated, err := metadataService.Update(ctx, librarymetadata.ScopeRelease, releaseID, releaseID, currentMetadata.Revision, map[string]json.RawMessage{
			"comment": comment,
		})
		if err != nil || updated.Revision <= currentMetadata.Revision {
			t.Fatalf("update desired comment revision %d => %d: %v", currentMetadata.Revision, updated.Revision, err)
		}
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
		baselineAuthority, err := manager.ReadPositionSourceAuthority(ctx, before.PositionID)
		if err != nil || baselineAuthority == nil {
			t.Fatalf("read baseline source authority = %+v, %v", baselineAuthority, err)
		}
		preparation, err := publications.prepareRetainedSourceRepublish(ctx, releaseID, before.PositionID, firstPublicationPreviewModeOrdinary)
		if err != nil || len(preparation.Preview.Blockers) != 0 || preparation.Selected == nil || preparation.Preview.SelectedSource == nil || preparation.Preview.SelectedSource.LocationID != baselineAuthority.SourceLocationID || preparation.Preview.SelectedSource.MediaVariantID != baselineAuthority.MediaVariantID {
			t.Fatalf("ordinary retained-source republish preparation = %+v, %v", preparation, err)
		}
		republishID, err := publications.AdmitSourceRepublishRetainingSelection(ctx, releaseID, before.PositionID, SourceRepublishConfirmation{PreviewDigest: preparation.Preview.ContextDigest, ExpectedEpoch: before.Epoch, Mode: firstPublicationPreviewModeOrdinary})
		if err != nil {
			t.Fatalf("admit selected-source republish: %v", err)
		}
		attempt, err := manager.ReadPublicationAttempt(ctx, republishID)
		var frozen persistence.PublicationInputs
		if attempt != nil {
			err = json.Unmarshal(attempt.FrozenInputs, &frozen)
		}
		if err != nil || attempt == nil || frozen.SourceRepublish == nil || frozen.SourceRepublish.Association.LocationID != preparation.Selected.Selection.LocationID || frozen.SourceRepublish.Association.MediaVariantID != preparation.Selected.Selection.MediaVariantID {
			t.Fatalf("republish frozen selected-source association = %+v, %v", attempt, err)
		}
		op, err := manager.GetOperation(ctx, republishID)
		if err != nil || op.RiverJobID == nil {
			t.Fatalf("read admitted republish operation = %+v, %v", op, err)
		}
		riverJobID := *op.RiverJobID
		if _, err := db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=1 WHERE id=?`, riverJobID); err != nil {
			t.Fatal(err)
		}
		writer := &countingSourceRepublishMetadataWriter{delegate: metadata.Writer{}}
		executor.metadata = writer
		if err := executor.ExecutePublicationDelivery(ctx, republishID, riverJobID, 1); err != nil {
			t.Fatalf("source republish delivery: %v", err)
		}
		published, err := (metadata.Reader{}).Read(ctx, filepath.Join(output, filepath.FromSlash(target)), metadata.ReadRequest{CoverInspection: true})
		if err != nil || len(published.Tags["COMMENT"]) != 1 || published.Tags["COMMENT"][0] != "republished comment" {
			t.Fatalf("native republished tags = %#v, %v", published.Tags, err)
		}
		var after persistence.TrackPublication
		if err := db.NewSelect().Model(&after).Where("position_id=?", before.PositionID).Scan(ctx); err != nil || after.Generation != attempt.CandidateGeneration {
			t.Fatalf("CURRENT generation after same-path republish = %s, %v; want candidate %s", after.Generation, err, attempt.CandidateGeneration)
		}
		authority, err := manager.ReadPositionSourceAuthority(ctx, before.PositionID)
		if err != nil || authority == nil || authority.Mode != baselineAuthority.Mode || authority.Revision == baselineAuthority.Revision || authority.PublicationGeneration != attempt.CandidateGeneration || authority.SourceLocationID != baselineAuthority.SourceLocationID || authority.MediaVariantID != baselineAuthority.MediaVariantID {
			t.Fatalf("retained CURRENT source authority = %+v, %v", authority, err)
		}
		assets, err := persistence.DecodePublicationAssetOutcomes(after.ActualAssets)
		if err != nil || assets.Cover.State != persistence.PublicationAssetAbsent || assets.Lyrics.State != persistence.PublicationAssetAbsent {
			t.Fatalf("source republish actual optional-asset outcomes = %+v, %v; want absent cover and lyrics", assets, err)
		}
		baselineAssets, err := persistence.DecodePublicationAssetOutcomes(before.ActualAssets)
		if err != nil || !reflect.DeepEqual(assets, baselineAssets) {
			t.Fatalf("source republish changed actual asset outcomes: before=%+v after=%+v, %v", baselineAssets, assets, err)
		}
		if err := os.Remove(sourceFile); err != nil {
			t.Fatal(err)
		}
		if err := executor.ExecutePublicationDelivery(ctx, republishID, riverJobID, 1); err != nil {
			t.Fatalf("completed republish redelivery: %v", err)
		}
		if writer.calls != 1 {
			t.Fatalf("metadata preparations after completed redelivery = %d, want 1", writer.calls)
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

// This wrapper is deliberately limited to the source-republish scenario: it
// observes optional-asset writes without changing the capabilities advertised
// by the first-publication retry writer used by other tests.
type countingSourceRepublishMetadataWriter struct {
	delegate metadata.Writer
	calls    int
}

func (writer *countingSourceRepublishMetadataWriter) Write(ctx context.Context, path string, tags map[string][]string, cover *metadata.Cover) (metadata.WriteResult, error) {
	writer.calls++
	return writer.delegate.Write(ctx, path, tags, cover)
}

func (writer *countingSourceRepublishMetadataWriter) WriteWithOptionalAssets(ctx context.Context, path string, tags map[string][]string, cover *metadata.Cover) (metadata.OptionalAssetsWriteResult, error) {
	writer.calls++
	return writer.delegate.WriteWithOptionalAssets(ctx, path, tags, cover)
}

func (writer *countingPublicationMetadataWriter) WriteFlacTagsPreservingArtwork(ctx context.Context, path string, tags map[string][]string, before metadata.FlacArtworkSnapshot) (metadata.WriteResult, error) {
	writer.calls++
	return metadata.WriteFlacTagsPreservingArtwork(ctx, path, tags, before)
}
