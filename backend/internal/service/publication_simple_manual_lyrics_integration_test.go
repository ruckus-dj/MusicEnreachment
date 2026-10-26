//go:build integration

package service

import (
	"context"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	integrationtools "github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/uptrace/bun"
)

func TestPublicationExecutorAppliesSavedManualLyricsThroughSimpleDeliveryPostgreSQL(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		state  librarymetadata.ValueState
		lyrics string
	}{
		{name: "value", state: librarymetadata.StateValue, lyrics: "[00:01.250]live manual lyrics\n"},
		{name: "explicit empty", state: librarymetadata.StateExplicitEmpty},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			testPublicationSourceExecution(t, "Track.flac", true, false, publicationDeliveryScenario{
				run: func(ctx context.Context, db *bun.DB, manager *persistence.SetupManagerRepository, executor *PublicationExecutor, firstOperationID uuid.UUID, firstJobID int64, _, output, target string, _ []byte) {
					attempt, err := manager.ReadPublicationAttempt(ctx, firstOperationID)
					if err != nil || attempt == nil {
						t.Fatalf("read first publication attempt: %+v, %v", attempt, err)
					}
					positionID := attempt.PositionID
					if err := executor.ExecutePublicationDelivery(ctx, firstOperationID, firstJobID, 1); err != nil {
						t.Fatalf("first-audio delivery: %v", err)
					}
					var releaseID uuid.UUID
					var metadataRevision int64
					if err := db.NewRaw(`SELECT release_id,metadata_revision FROM release_track WHERE id=?`, positionID).Scan(ctx, &releaseID, &metadataRevision); err != nil {
						t.Fatalf("read publication position: %v", err)
					}
					var current persistence.TrackPublication
					if err := db.NewSelect().Model(&current).Where("position_id=?", positionID).Scan(ctx); err != nil {
						t.Fatalf("read first publication: %v", err)
					}
					var desiredLyrics any
					if scenario.state == librarymetadata.StateValue {
						desiredLyrics = scenario.lyrics
					}
					envelope := librarymetadata.Envelope{
						Version: librarymetadata.EnvelopeVersion,
						Scope:   librarymetadata.ScopePosition,
						Desired: map[string]librarymetadata.FieldValue{
							"synced_lyrics": librarymetadata.ManualFieldValue(scenario.state, desiredLyrics, scenario.state == librarymetadata.StateValue),
						},
					}
					metadataResult, err := persistence.NewLibraryRepository(db).UpdateMetadata(ctx, librarymetadata.ScopePosition, releaseID, positionID, metadataRevision, envelope)
					if err != nil {
						t.Fatalf("save manual lyrics authority: %v", err)
					}
					publications := NewLibraryPublications(
						manager,
						settings.New(persistence.NewSettingsRepository(db), nil),
						libraryPublicationTestInserter{},
						integrationtools.Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH},
					)
					operationID, err := publications.ApplyManualLyrics(ctx, releaseID, positionID, LibraryPublicationApplyManualLyricsRequest{
						ExpectedEpoch: current.Epoch, ExpectedGeneration: current.Generation,
						ExpectedMetadataRevision: metadataResult.Revision,
					})
					if err != nil {
						t.Fatalf("admit manual lyrics rewrite: %v", err)
					}
					operation, err := manager.GetOperation(ctx, operationID)
					if err != nil || operation == nil || operation.RiverJobID == nil {
						t.Fatalf("read admitted manual lyrics operation: %+v, %v", operation, err)
					}
					jobID := *operation.RiverJobID
					if _, err := db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=1 WHERE id=?`, jobID); err != nil {
						t.Fatal(err)
					}
					if err := executor.ExecutePublicationDelivery(ctx, operationID, jobID, 1); err != nil {
						t.Fatalf("manual lyrics delivery: %v", err)
					}

					published := filepath.Join(output, filepath.FromSlash(target))
					capture, err := (metadata.Reader{}).Read(ctx, published, metadata.ReadRequest{})
					if err != nil || scenario.state == librarymetadata.StateValue && !reflect.DeepEqual(capture.Tags["LYRICS"], []string{scenario.lyrics}) ||
						scenario.state == librarymetadata.StateExplicitEmpty && len(capture.Tags["LYRICS"]) != 0 {
						t.Fatalf("published manual lyrics = %#v, %v", capture.Tags["LYRICS"], err)
					}
					projection, err := manager.ReadManualLyricsReceiptProjection(ctx, positionID)
					if err != nil || projection.Pending || projection.Receipt == nil || projection.CurrentGeneration == nil || *projection.CurrentGeneration == current.Generation {
						t.Fatalf("manual lyrics receipt/current projection = %+v, %v", projection, err)
					}
					operation, err = manager.GetOperation(ctx, operationID)
					if err != nil || operation == nil || operation.State != "succeeded" {
						t.Fatalf("manual lyrics operation = %+v, %v", operation, err)
					}
				},
			})
		})
	}
}
