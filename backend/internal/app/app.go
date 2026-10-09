package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/riverqueue/river"

	"github.com/ruckus/MusicEnreachment/backend/internal/api"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/musicbrainz"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/jobs"
	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/static"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/migrate"
)

type operationRepository interface {
	service.OperationRepository
	CreateOperationAndEnqueue(context.Context, *persistence.Operation, persistence.RiverInserter, river.JobArgs, *river.InsertOpts) error
	RetryOperationAndEnqueue(context.Context, uuid.UUID, persistence.RiverInserter, river.JobArgs, *river.InsertOpts) (*persistence.Operation, error)
}

type riverClientSlot struct {
	persistence.RiverInserter
}

type analysisConsumerHandle struct {
	client       *river.Client[*sql.Tx]
	listenerPool interface{ Close() }
	closeOnce    sync.Once
}

func (consumer *analysisConsumerHandle) Stop(ctx context.Context) error {
	err := consumer.client.Stop(ctx)
	if err == nil {
		<-consumer.client.Stopped()
		consumer.closeOnce.Do(consumer.listenerPool.Close)
	}
	return err
}

// scanWorkerRepository joins the two repositories a scan worker reads: the
// operation and managed installation records, and the source inventory the
// traversal and the atomic apply write to.
type scanWorkerRepository struct {
	*persistence.SetupManagerRepository
	*persistence.SourceInventoryRepository
}

func (repository scanWorkerRepository) GetInstallation(ctx context.Context, id uuid.UUID) (*persistence.ToolInstallation, error) {
	return repository.SourceInventoryRepository.GetInstallation(ctx, id)
}

// analysisWorkerRepository joins the two repositories an analysis worker reads:
// the operation and managed installation records, and the source inventory whose
// apply commits the result and both read holds.
type analysisWorkerRepository struct {
	*persistence.SetupManagerRepository
	*persistence.SourceInventoryRepository
}

func (repository analysisWorkerRepository) GetInstallation(ctx context.Context, id uuid.UUID) (*persistence.ToolInstallation, error) {
	return repository.SourceInventoryRepository.GetInstallation(ctx, id)
}

func (repository analysisWorkerRepository) LookupSourceProbe(ctx context.Context, digest [sha256.Size]byte, version string, policy int) (*persistence.SourceMediaVariant, bool, error) {
	return repository.SourceInventoryRepository.LookupSourceProbe(ctx, digest, version, policy)
}

func (repository analysisWorkerRepository) LookupSourceFingerprint(ctx context.Context, digest [sha256.Size]byte) (*persistence.SourceFingerprintResult, bool, error) {
	return repository.SourceInventoryRepository.LookupSourceFingerprint(ctx, digest)
}

func newOperationServices(repository operationRepository, client *riverClientSlot, analysisRetry interface {
	RetryOperation(context.Context, uuid.UUID) (*persistence.Operation, error)
}) (*service.Operations, *service.Operations, *riverClientSlot) {
	operations := service.NewOperationsWithRiver(repository, client, analysisRetry)
	return operations, operations, client
}

type Config struct {
	DatabaseURL string
	HTTPAddress string
	Logger      *slog.Logger
	LogLevel    *slog.LevelVar
}

func Run(ctx context.Context, config Config) error {
	if config.DatabaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	level := config.LogLevel
	if level == nil {
		level = new(slog.LevelVar)
		level.Set(slog.LevelInfo)
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	}
	if config.HTTPAddress == "" {
		config.HTTPAddress = ":8080"
	}

	sqldb, err := sql.Open("pgx", config.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open PostgreSQL: %w", err)
	}
	if err := sqldb.PingContext(ctx); err != nil {
		_ = sqldb.Close()
		return fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	db := bun.NewDB(sqldb, pgdialect.New())
	defer func() { _ = db.Close() }()
	if err := applyMigrations(ctx, db); err != nil {
		return err
	}
	logger.InfoContext(ctx, "application migrations complete")
	settingsRepository := persistence.NewSettingsRepository(db)
	registry := settings.New(settingsRepository, level)
	platform, err := registry.InitializePlatform(ctx, settings.CurrentPlatform())
	if err != nil {
		return fmt.Errorf("initialize instance platform: %w", err)
	}
	if err := registry.LoadLogLevel(ctx); err != nil {
		return fmt.Errorf("load runtime log level: %w", err)
	}
	setupManagerRepository := persistence.NewSetupManagerRepository(db)
	outputReset := service.NewOutputReset(setupManagerRepository, settings.NewResetFilesystem(), []string{"analysis", "publication", "checks", "media"})
	setup := service.NewSetup(settingsRepository, registry, platform, setupManagerRepository, musicbrainz.NewClient()).WithOutputReset(outputReset)
	if err := outputReset.RecoverOutputReset(ctx); err != nil {
		return fmt.Errorf("recover output reset: %w", err)
	}
	riverSlot := &riverClientSlot{}
	sourceInventory := persistence.NewSourceInventoryRepository(db)
	sourceAnalysis := service.NewSourceAnalysisOperations(
		analysisWorkerRepository{SetupManagerRepository: setupManagerRepository, SourceInventoryRepository: sourceInventory},
		registry, registry, platform, riverSlot,
	)
	operationService, apiOperations, riverSlot := newOperationServices(setupManagerRepository, riverSlot, sourceAnalysis)
	operationService.SetPendingDispatcher(sourceAnalysis)
	artifactCleanup := service.NewSourceAnalysisArtifactCleanup(setupManagerRepository, registry, riverSlot)
	catalog := tools.NewDefaultCatalog(nil)
	installWorker := jobs.NewInstallationWorker(setupManagerRepository, operationService, catalog, registry, tools.Platform{
		GOOS: platform.Platform.GOOS, GOARCH: platform.Platform.GOARCH,
	}, tools.NewLifecycle(nil))
	moveWorker := jobs.NewMoveWorker(setupManagerRepository, operationService, registry, tools.Platform{
		GOOS: platform.Platform.GOOS, GOARCH: platform.Platform.GOARCH,
	}, tools.NewLifecycle(nil))
	moveWorker.SetPendingDispatcher(sourceAnalysis)
	installWorker.SetMoveWorker(moveWorker)
	sourceRoots := service.NewSourceRoots(sourceInventory, registry)
	sourceRoots.SetPendingDispatcher(sourceAnalysis)
	scanWorker := jobs.NewSourceScanWorker(
		scanWorkerRepository{SetupManagerRepository: setupManagerRepository, SourceInventoryRepository: sourceInventory},
		operationService, sourceRoots, registry, platform, tools.NewLifecycle(nil),
	)
	scanWorker.SetPendingDispatcher(sourceAnalysis)
	analysisWorker := jobs.NewSourceAnalysisWorker(
		analysisWorkerRepository{SetupManagerRepository: setupManagerRepository, SourceInventoryRepository: sourceInventory},
		operationService, registry, registry, platform,
	)
	analysisWorker.WithInputPreparer(service.NewSourceAnalysisInputPreparer(
		sourceInventory, persistence.NewSourceAnalysisArtifactRepository(db), registry, nil, nil,
	))
	analysisWorker.SetPendingDispatcher(sourceAnalysis)
	artifactCleanupWorker := jobs.NewSourceAnalysisArtifactCleanupWorker(setupManagerRepository, operationService, artifactCleanup)

	registerWorkers := func(workers *river.Workers) {
		if !platform.Diagnostic && platform.Platform.Supported() {
			river.AddWorker(workers, installWorker)
		}
		// A queued scan is always registered: the worker re-checks the platform,
		// the Setup, the root and the managed tools itself, so a scan that can no
		// longer run fails with a safe reason instead of staying queued forever.
		river.AddWorker(workers, scanWorker)
		// A queued analysis is registered for the same reason; queue concurrency
		// is initialized from the source-file setting and bounded live by worker.
		river.AddWorker(workers, analysisWorker)
		river.AddWorker(workers, artifactCleanupWorker)
		river.AddWorker(workers, jobs.NewCleanupWorker(setupManagerRepository))
	}
	// Keep exactly one base analysis slot on the main client. Additional River
	// clients are provisioned only for actual queued/running demand.
	primaryAnalysisCapacity := 1
	riverClient, riverListenerPool, err := jobs.PrepareWithWorkersAndAnalysisConcurrency(ctx, config.DatabaseURL, sqldb, registerWorkers, primaryAnalysisCapacity, jobs.NewCleanupPeriodicJob())
	if err != nil {
		return err
	}
	defer riverListenerPool.Close()
	riverSlot.RiverInserter = riverClient

	if err := jobs.ReconcileInterruptedOperations(ctx, setupManagerRepository, operationService,
		setupManagerRepository.RiverJobLiveness, registry); err != nil {
		return fmt.Errorf("reconcile interrupted operations: %w", err)
	}
	if err := sourceAnalysis.DispatchPending(ctx); err != nil {
		return fmt.Errorf("dispatch pending source analysis: %w", err)
	}

	if err := riverClient.Start(ctx); err != nil {
		return fmt.Errorf("start River: %w", err)
	}
	logger.InfoContext(ctx, "River started")
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = riverClient.Stop(shutdown)
	}()
	consumerLifetimeContext, cancelConsumerLifetime := context.WithCancel(ctx)
	defer cancelConsumerLifetime()
	consumerPool, err := jobs.NewAnalysisConsumerPool(primaryAnalysisCapacity, func(startCtx context.Context, capacity int) (jobs.AnalysisConsumer, error) {
		// Consumer clients live for the application lifetime, not for the
		// controller reconciliation that happened to create them.
		client, listenerPool, startErr := jobs.StartAnalysisConsumerWithLifetime(startCtx, consumerLifetimeContext, config.DatabaseURL, sqldb, registerWorkers, capacity)
		if startErr != nil {
			return nil, startErr
		}
		return &analysisConsumerHandle{client: client, listenerPool: listenerPool}, nil
	})
	if err != nil {
		return err
	}
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := consumerPool.Stop(shutdown); err != nil {
			logger.Warn("stop additional River analysis consumers", "error", err)
		}
	}()
	registry.WithConcurrencyObserver(func() {
		if err := analysisWorker.RefreshSourceFileConcurrency(ctx); err != nil {
			logger.Warn("refresh source file concurrency after settings update", "error", err)
		}
		consumerPool.Wake()
	})
	consumerContext, stopConsumerMonitor := context.WithCancel(ctx)
	consumerMonitorDone := make(chan struct{})
	defer func() {
		stopConsumerMonitor()
		<-consumerMonitorDone
	}()
	consumerPool.Wake()
	go func() {
		defer close(consumerMonitorDone)
		// This timer reconciles queue demand as jobs arrive. Settings changes use
		// the post-commit observer for immediate wakeups rather than polling.
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-consumerContext.Done():
				return
			case <-consumerPool.Wakeups():
			case <-ticker.C:
			}
			generation := consumerPool.Generation()
			controllerContext, cancelController := context.WithCancel(consumerContext)
			if refreshErr := analysisWorker.RefreshSourceFileConcurrency(controllerContext); refreshErr != nil {
				cancelController()
				logger.Warn("refresh source file concurrency for River consumers", "error", refreshErr)
				continue
			}
			limit, readErr := registry.GetSourceFileConcurrency(controllerContext)
			if readErr != nil {
				cancelController()
				logger.Warn("read source file concurrency for River consumers", "error", readErr)
				continue
			}
			demand, readErr := jobs.AnalysisQueueDemand(controllerContext, riverClient)
			if readErr != nil {
				cancelController()
				logger.Warn("read source analysis queue demand", "error", readErr)
				continue
			}
			if reconcileErr := consumerPool.SetDemandForGeneration(controllerContext, generation, limit, demand); reconcileErr != nil {
				logger.Warn("reconcile River analysis consumers", "error", reconcileErr)
			}
			cancelController()
		}
	}()
	toolCatalog := service.NewCatalogService(catalog, tools.Platform{
		GOOS: platform.Platform.GOOS, GOARCH: platform.Platform.GOARCH,
	})
	installOperations := service.NewInstallOperations(setupManagerRepository, catalog, tools.Platform{
		GOOS: platform.Platform.GOOS, GOARCH: platform.Platform.GOARCH,
	}, riverClient, registry)
	installations := service.NewInstallations(setupManagerRepository, platform.Platform, registry, tools.NewLifecycle(nil), registry)
	moveTools := service.NewMoveTools(setupManagerRepository, registry, tools.Platform{
		GOOS: platform.Platform.GOOS, GOARCH: platform.Platform.GOARCH,
	}, riverClient)
	sourceLocations := service.NewSourceLocations(sourceInventory)
	sourceScan := service.NewSourceScanOperations(sourceInventory, sourceRoots, registry, platform, riverClient)
	sourceLocationDetails := service.NewSourceLocationDetails(sourceInventory)

	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(api.RequestIDHeader)
	router.Use(api.RequestLogger(logger))
	router.Use(api.RecoverPanics(logger))
	router.Get("/health/live", api.Liveness)
	router.Get("/health/ready", api.Readiness(sqldb, platform))
	router.Mount("/api", api.HandlerWithDependencies(api.Dependencies{
		Setup: setup, Catalog: toolCatalog, InstallOperations: installOperations,
		Installations: installations, MoveTools: moveTools, Operations: apiOperations,
		SourceRoots: sourceRoots, SourceLocations: sourceLocations, SourceScan: sourceScan,
		SourceAnalysis: sourceAnalysis, SourceLocationDetails: sourceLocationDetails,
		SourceArtifactCleanup: artifactCleanup,
	}))
	router.Handle("/*", static.Handler())
	server := &http.Server{
		Addr:              config.HTTPAddress,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	serverDone := make(chan struct{})
	shutdownResult := make(chan error, 1)
	go func() {
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			shutdownResult <- server.Shutdown(shutdown)
		case <-serverDone:
			shutdownResult <- nil
		}
	}()

	logger.InfoContext(ctx, "HTTP server starting", "address", server.Addr)
	serveErr := server.ListenAndServe()
	close(serverDone)
	if shutdownErr := <-shutdownResult; shutdownErr != nil {
		logger.Warn("HTTP server did not finish graceful shutdown", "error", shutdownErr)
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP: %w", serveErr)
	}
	return nil
}

func applyMigrations(ctx context.Context, db *bun.DB) error {
	collection, err := migrations.Collection()
	if err != nil {
		return err
	}
	migrator := migrate.NewMigrator(db, collection, migrate.WithMarkAppliedOnSuccess(true))
	if err := migrator.Init(ctx); err != nil {
		return fmt.Errorf("initialize migrations: %w", err)
	}
	if _, err := migrator.Migrate(ctx); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
