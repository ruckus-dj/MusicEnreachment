package api_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/api"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

// sourceAnalysisAPIRepository is the in-memory store behind the analysis
// endpoints. It extends the source store with the stored variant, the managed
// installation and the active-analysis reads and with the enqueue/retry
// contracts the analysis start and operation retry use. Durability, locks and
// uniqueness remain properties of PostgreSQL and are proven by the persistence
// integration tests, not by this fake.
type sourceAnalysisAPIRepository struct {
	*sourceAPIRepository
	variants      map[uuid.UUID]*persistence.MediaVariant
	installations map[uuid.UUID]*persistence.ToolInstallation
}

// GetSourceLocation answers only for a location the named root owns: a foreign
// location and a missing one fail identically, so the API can never address a
// file through a root it does not belong to.
func (repository *sourceAnalysisAPIRepository) GetSourceLocation(_ context.Context, rootID, locationID uuid.UUID) (*persistence.SourceLocation, error) {
	for index := range repository.locations[rootID] {
		if repository.locations[rootID][index].ID == locationID {
			location := repository.locations[rootID][index]
			return &location, nil
		}
	}
	return nil, fmt.Errorf("get source location: %w", persistence.ErrSourceLocationNotFound)
}

func (repository *sourceAnalysisAPIRepository) GetMediaVariant(_ context.Context, id uuid.UUID) (*persistence.MediaVariant, error) {
	variant := repository.variants[id]
	if variant == nil {
		return nil, fmt.Errorf("get media variant: %w", persistence.ErrMediaVariantNotFound)
	}
	return variant, nil
}

func (repository *sourceAnalysisAPIRepository) ActiveSourceAnalysisOperationID(_ context.Context, rootID, locationID uuid.UUID) (*uuid.UUID, error) {
	repository.operations.mu.Lock()
	defer repository.operations.mu.Unlock()
	var newest *persistence.Operation
	for _, operation := range repository.operations.operations {
		if operation.Kind != service.SourceAnalysisOperationKind || (operation.State != "queued" && operation.State != "running") {
			continue
		}
		if operation.TargetSourceRootID == nil || *operation.TargetSourceRootID != rootID ||
			operation.TargetSourceLocationID == nil || *operation.TargetSourceLocationID != locationID {
			continue
		}
		if newest == nil || operation.CreatedAt.After(newest.CreatedAt) {
			newest = operation
		}
	}
	if newest == nil {
		return nil, nil
	}
	id := newest.ID
	return &id, nil
}

func (repository *sourceAnalysisAPIRepository) GetInstallation(_ context.Context, id uuid.UUID) (*persistence.ToolInstallation, error) {
	installation := repository.installations[id]
	if installation == nil {
		return nil, fmt.Errorf("get installation: %w", sql.ErrNoRows)
	}
	return installation, nil
}

func (repository *sourceAnalysisAPIRepository) CreateSourceAnalysisOperationAndEnqueue(ctx context.Context, operation *persistence.Operation, _, _, _ string, _ persistence.RiverInserter, _ river.JobArgs, _ *river.InsertOpts) error {
	repository.operations.mu.Lock()
	active := false
	for _, existing := range repository.operations.operations {
		if existing.State != "queued" && existing.State != "running" {
			continue
		}
		if existing.TargetSourceRootID != nil && operation.TargetSourceRootID != nil && *existing.TargetSourceRootID == *operation.TargetSourceRootID {
			active = true
			break
		}
	}
	repository.operations.mu.Unlock()
	if active {
		return fmt.Errorf("create analysis operation: %w", persistence.ErrSourceRootActiveAnalysis)
	}
	return repository.operations.CreateOperation(ctx, operation)
}

func (repository *sourceAnalysisAPIRepository) CreateOperation(ctx context.Context, operation *persistence.Operation) error {
	return repository.operations.CreateOperation(ctx, operation)
}

func (repository *sourceAnalysisAPIRepository) GetOperation(ctx context.Context, id uuid.UUID) (*persistence.Operation, error) {
	return repository.operations.GetOperation(ctx, id)
}

func (repository *sourceAnalysisAPIRepository) ListOperations(ctx context.Context, states ...string) ([]persistence.Operation, error) {
	return repository.operations.ListOperations(ctx, states...)
}

func (repository *sourceAnalysisAPIRepository) UpdateOperation(ctx context.Context, operation *persistence.Operation) error {
	return repository.operations.UpdateOperation(ctx, operation)
}

func (repository *sourceAnalysisAPIRepository) TransitionOperation(ctx context.Context, id uuid.UUID, transition func(*persistence.Operation) error) error {
	return repository.operations.TransitionOperation(ctx, id, transition)
}

func (repository *sourceAnalysisAPIRepository) DismissOperation(ctx context.Context, id uuid.UUID) error {
	return repository.operations.DismissOperation(ctx, id)
}

func (repository *sourceAnalysisAPIRepository) DeleteSucceededBefore(ctx context.Context, before time.Time) error {
	return repository.operations.DeleteSucceededBefore(ctx, before)
}

func (repository *sourceAnalysisAPIRepository) CreateOperationAndEnqueue(ctx context.Context, operation *persistence.Operation, _ persistence.RiverInserter, _ river.JobArgs, _ *river.InsertOpts) error {
	return repository.operations.CreateOperation(ctx, operation)
}

func (repository *sourceAnalysisAPIRepository) RetryOperationAndEnqueue(ctx context.Context, id uuid.UUID, _ persistence.RiverInserter, _ river.JobArgs, _ *river.InsertOpts) (*persistence.Operation, error) {
	return repository.requeueFailed(ctx, id)
}

func (repository *sourceAnalysisAPIRepository) RetrySourceAnalysisOperationAndEnqueue(ctx context.Context, id uuid.UUID, _ persistence.RiverInserter, _ river.JobArgs, _ *river.InsertOpts) (*persistence.Operation, error) {
	return repository.requeueFailed(ctx, id)
}

func (repository *sourceAnalysisAPIRepository) requeueFailed(ctx context.Context, id uuid.UUID) (*persistence.Operation, error) {
	operation, err := repository.operations.GetOperation(ctx, id)
	if err != nil {
		return nil, err
	}
	if operation.State != "failed" {
		return nil, errors.New("only failed operations can be retried")
	}
	operation.State = "queued"
	operation.Stage = service.SourceAnalysisStageQueued
	operation.SafeError = nil
	operation.FinishedAt = nil
	return operation, nil
}

type sourceAnalysisAPIFixture struct {
	handler    http.Handler
	repository *sourceAnalysisAPIRepository
	operations *operationRepositoryFixture
	store      apiSettingsStore
}

func newSourceAnalysisAPIFixture(t *testing.T, platform settings.PlatformState, completeSetup bool) sourceAnalysisAPIFixture {
	t.Helper()
	store := apiSettingsStore{}
	registry := settings.New(store, nil)
	if completeSetup {
		if err := registry.CompleteSetup(context.Background()); err != nil {
			t.Fatalf("complete the first-run setup: %v", err)
		}
	}
	operations := &operationRepositoryFixture{operations: map[uuid.UUID]*persistence.Operation{}}
	base := &sourceAPIRepository{
		operations: operations, locations: map[uuid.UUID][]persistence.SourceLocation{},
		candidates: map[uuid.UUID][]persistence.SourceScanCandidate{},
	}
	repository := &sourceAnalysisAPIRepository{
		sourceAPIRepository: base,
		variants:            map[uuid.UUID]*persistence.MediaVariant{},
		installations:       map[uuid.UUID]*persistence.ToolInstallation{},
	}
	roots := service.NewSourceRoots(repository, sourceAPIManagedPaths{tools: t.TempDir(), output: t.TempDir()})
	handler := api.HandlerWithDependencies(api.Dependencies{
		Setup:                 service.NewSetup(store, registry, platform, nil, nil),
		SourceRoots:           roots,
		SourceLocations:       service.NewSourceLocations(repository),
		SourceScan:            service.NewSourceScanOperations(repository, roots, registry, platform, sourceAPIRiverInserter{}),
		SourceAnalysis:        service.NewSourceAnalysisOperations(repository, registry, registry, platform, sourceAPIRiverInserter{}),
		SourceLocationDetails: service.NewSourceLocationDetails(repository),
		Operations:            service.NewOperationsWithRiver(repository, sourceAPIRiverInserter{}),
	})
	return sourceAnalysisAPIFixture{handler: handler, repository: repository, operations: operations, store: store}
}

// activateFFmpeg registers one ready FFmpeg installation of the instance
// platform and makes it the active selection, so a start can resolve it.
func (fixture sourceAnalysisAPIFixture) activateFFmpeg(t *testing.T, platform settings.PlatformState) uuid.UUID {
	t.Helper()
	id := uuid.New()
	fixture.repository.installations[id] = &persistence.ToolInstallation{
		ID: id, PackageKind: "ffmpeg", State: "ready",
		PlatformGOOS: platform.Platform.GOOS, PlatformGOARCH: platform.Platform.GOARCH,
	}
	fixture.store[settings.ActiveFFmpegInstallationKey] = id.String()
	return id
}

// seedAnalysisRoot stores an available, enabled, non-stale root that owns the
// given locations.
func (fixture sourceAnalysisAPIFixture) seedAnalysisRoot(t *testing.T, locations []persistence.SourceLocation) *persistence.SourceRoot {
	t.Helper()
	configured := normalizedSourcePath(t, t.TempDir())
	root := &persistence.SourceRoot{
		ID: uuid.New(), DisplayName: "Music", ConfiguredPath: configured, Enabled: true,
		Status: persistence.SourceRootStatusAvailable, InventoryPath: &configured,
	}
	fixture.repository.roots = append(fixture.repository.roots, root)
	fixture.repository.locations[root.ID] = locations
	return root
}

func analyzeBody(sizeBytes int64, mtime string) string {
	return fmt.Sprintf(`{"expected_size_bytes":%d,"expected_mtime":%q}`, sizeBytes, mtime)
}
