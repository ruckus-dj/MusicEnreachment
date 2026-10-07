//go:build integration

package persistence_test

import (
	"context"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

type sourceAnalysisControlSetup bool

func (setup sourceAnalysisControlSetup) SetupCompleted(context.Context) (bool, error) {
	return bool(setup), nil
}

type sourceAnalysisControlRuntime struct{}

func (sourceAnalysisControlRuntime) ReadRuntimeSettings(context.Context) (settings.RuntimeSettings, error) {
	return settings.RuntimeSettings{}, nil
}

// Exercise the service-produced normalized contract against the real admission
// transaction, rather than accepting it only through the service/API fakes.
func TestSourceAnalysisRetryServiceNormalizedAdmissionWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(database)
	root := createInventoryRoot(t, ctx, repository, "/srv/source-analysis-control")
	location := insertAnalysisLocation(t, ctx, database, root.ID, "album/track.flac", 1024, probeMtime())
	establishInventory(t, ctx, database, root)
	work := normalizedWork(t, ctx, repository, root, location, true, persistence.SourceAnalysisStepInput{
		Step: persistence.SourceStepSHA256, State: "failed", SafeError: sourceAnalysisControlString("digest unavailable"),
	})

	platform := settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}
	operations := service.NewSourceAnalysisOperations(
		persistence.NewSourceAnalysisStartStore(database), sourceAnalysisControlSetup(true), sourceAnalysisControlRuntime{},
		platform, openScanEnqueueRiver(t, database),
	)
	operation, err := operations.RetryStep(ctx, service.SourceAnalysisStepRequest{
		RootID: root.ID, LocationID: location.ID, Step: persistence.SourceStepSHA256,
		ExpectedSizeBytes: location.SizeBytes, ExpectedMtime: location.Mtime,
	})
	if err != nil {
		t.Fatalf("admit service-produced SHA retry: %v", err)
	}
	if operation.SourceAnalysisMode != persistence.SourceAnalysisModeSingleStep || operation.TargetWorkID == nil || *operation.TargetWorkID != work.ID ||
		operation.TargetStep == nil || *operation.TargetStep != string(persistence.SourceStepSHA256) || operation.ToolsReadRequired || operation.RiverJobID == nil {
		t.Fatalf("admitted operation = %+v", operation)
	}
	assertOperationHoldCounts(t, ctx, database, operation.ID, 1, 0)
	var queued int
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_step WHERE work_id=? AND step='sha256' AND state='queued' AND execution_operation_id=?`, work.ID, operation.ID).Scan(ctx, &queued); err != nil {
		t.Fatal(err)
	}
	if queued != 1 {
		t.Fatalf("exact target step queued count = %d, want 1", queued)
	}
}

func sourceAnalysisControlString(value string) *string { return &value }
