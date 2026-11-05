// Package testpostgres provides isolated PostgreSQL databases for integration tests.
package testpostgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	postgres "github.com/fergusstrange/embedded-postgres/v2"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/riverqueue/river"
	riverdatabasesql "github.com/riverqueue/river/riverdriver/riverdatabasesql"
	rivermigrate "github.com/riverqueue/river/rivermigrate"
	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/migrate"
)

const (
	poolMaxOpen          = 8
	adminPoolLimit       = 4
	resourcePrefix       = "melotrove_test_"
	cleanupTimeout       = time.Minute
	clusterDeleteWorkers = 4
)

var process struct {
	sync.Mutex
	resourcesMu     sync.Mutex
	serverOnce      sync.Once
	serverErr       error
	cloneEnabled    bool
	cloneDiagnostic string
	server          *postgres.EmbeddedPostgres
	ownedDisposable bool
	clusterWorkDir  string
	clusterPIDReady bool
	adminURL        string
	adminDB         *bun.DB
	template        string
	riverTemplate   string
	resources       []string
	cleanups        []func() error

	familiesMu sync.Mutex
	families   map[string]int

	createdTest      atomic.Int64
	createdTemplate  atomic.Int64
	fixtureID        atomic.Int64
	dropAttempts     atomic.Int64
	dropped          atomic.Int64
	createNanos      atomic.Int64
	dropNanos        atomic.Int64
	clusterReclaimed atomic.Int64
	// shutdownCompleted is recorded by a Ready cleanup hook. The upstream
	// embedded-postgres runs it immediately after the server has shut down and
	// before session lock release and work-directory removal.
	shutdownCompleted atomic.Pointer[time.Time]
}

type cloneProbeCleanupError struct{ err error }

func (err cloneProbeCleanupError) Error() string { return err.err.Error() }
func (err cloneProbeCleanupError) Unwrap() error { return err.err }

// Open creates an empty, uniquely named database owned by this test. The
// TEST_DATABASE_URL value, when supplied, is treated only as an admin endpoint:
// tests never reset or drop the database named by that URL.
func Open(t *testing.T) *bun.DB {
	t.Helper()
	span := newFixtureSpan(t)
	defer span.finishOpen()
	t.Cleanup(func() { span.log(t) })
	admin := adminDatabase(t)
	name := resourcePrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
	createDatabaseForFixture(t, admin, name, "", span)
	process.createdTest.Add(1)
	recordFamily(t.Name())
	databaseURL := databaseURL(t, name)
	poolStart := time.Now()
	database, err := openPool(databaseURL)
	if err != nil {
		span.record("pool_open_ping", time.Since(poolStart), err)
		t.Fatalf("open isolated PostgreSQL database: %v", err)
	}
	if err := database.PingContext(context.Background()); err != nil {
		closeStart := time.Now()
		closeErr := database.Close()
		span.record("pool_close", time.Since(closeStart), closeErr)
		span.record("pool_open_ping", time.Since(poolStart), err)
		t.Fatalf("connect to isolated PostgreSQL database: %v", err)
	}
	span.record("pool_open_ping", time.Since(poolStart), nil)
	ownedDisposable := process.ownedDisposable
	t.Cleanup(func() {
		if err := releaseFixtureDatabase(name, span, ownedDisposable); err != nil {
			t.Errorf("drop isolated PostgreSQL database %s: %v", name, err)
		}
	})
	t.Cleanup(func() {
		start := time.Now()
		err := database.Close()
		span.record("pool_close", time.Since(start), err)
	})
	return database
}

// OpenMigrated creates an isolated database cloned from the process-local
// application-schema template. The template is migrated once and is never
// modified by individual tests.
func OpenMigrated(t *testing.T) *bun.DB {
	t.Helper()
	return openTemplate(t, false)
}

// OpenMigratedWithRiver clones the process-local application and River schema
// template. River migrations are applied once to the template, never per test.
func OpenMigratedWithRiver(t *testing.T) *bun.DB {
	t.Helper()
	return openTemplate(t, true)
}

func openTemplate(t *testing.T, withRiver bool) *bun.DB {
	t.Helper()
	span := newFixtureSpan(t)
	defer span.finishOpen()
	t.Cleanup(func() { span.log(t) })
	admin := adminDatabase(t)
	template := migratedTemplate(t, admin, withRiver)
	name := resourcePrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
	createDatabaseForFixture(t, admin, name, template, span)
	process.createdTest.Add(1)
	recordFamily(t.Name())
	poolStart := time.Now()
	database, err := openPool(databaseURL(t, name))
	if err != nil {
		span.record("pool_open_ping", time.Since(poolStart), err)
		t.Fatalf("open migrated PostgreSQL database: %v", err)
	}
	if err := database.PingContext(context.Background()); err != nil {
		closeStart := time.Now()
		closeErr := database.Close()
		span.record("pool_close", time.Since(closeStart), closeErr)
		span.record("pool_open_ping", time.Since(poolStart), err)
		t.Fatalf("connect to migrated PostgreSQL database: %v", err)
	}
	span.record("pool_open_ping", time.Since(poolStart), nil)
	ownedDisposable := process.ownedDisposable
	t.Cleanup(func() {
		if err := releaseFixtureDatabase(name, span, ownedDisposable); err != nil {
			t.Errorf("drop isolated PostgreSQL database %s: %v", name, err)
		}
	})
	t.Cleanup(func() {
		start := time.Now()
		err := database.Close()
		span.record("pool_close", time.Since(start), err)
	})
	return database
}

// URL returns a PostgreSQL URL for River and other clients connected to db.
func URL(t *testing.T, db *bun.DB) string {
	t.Helper()
	var name string
	if err := db.NewRaw("SELECT current_database()").Scan(context.Background(), &name); err != nil {
		t.Fatalf("read isolated PostgreSQL database name: %v", err)
	}
	return databaseURL(t, name)
}

// AddCleanup registers process-scoped cleanup such as cached fixture removal.
// It is run by Run after tests and before database/server teardown.
func AddCleanup(cleanup func() error) {
	process.resourcesMu.Lock()
	defer process.resourcesMu.Unlock()
	process.cleanups = append(process.cleanups, cleanup)
}

// Run executes a package's test suite and removes only databases and templates
// created by this helper, then stops its lazily started server.
func Run(m *testing.M) int {
	code := m.Run()
	if err := cleanupProcess(); err != nil {
		fmt.Fprintf(os.Stderr, "clean up PostgreSQL integration resources: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	fmt.Fprintf(os.Stderr, "testpostgres: created=%d dropped=%d create_time=%s drop_time=%s templates=%d drop_attempts=%d",
		process.createdTest.Load(), process.dropped.Load(),
		time.Duration(process.createNanos.Load()).Round(time.Millisecond),
		time.Duration(process.dropNanos.Load()).Round(time.Millisecond),
		process.createdTemplate.Load(), process.dropAttempts.Load())
	if process.clusterReclaimed.Load() > 0 {
		fmt.Fprintf(os.Stderr, " cluster_reclaimed=%d cluster_path=%s", process.clusterReclaimed.Load(), process.clusterWorkDir)
	}
	fmt.Fprintln(os.Stderr)
	if families := largestFixtureFamilies(); families != "" {
		fmt.Fprintf(os.Stderr, "testpostgres: largest_fixture_families=%s\n", families)
	}
	return code
}

func recordFamily(name string) {
	name, _, _ = strings.Cut(name, "/")
	process.familiesMu.Lock()
	defer process.familiesMu.Unlock()
	if process.families == nil {
		process.families = make(map[string]int)
	}
	process.families[name]++
}

func largestFixtureFamilies() string {
	process.familiesMu.Lock()
	counts := make(map[string]int, len(process.families))
	for name, count := range process.families {
		counts[name] = count
	}
	process.familiesMu.Unlock()
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if counts[names[i]] != counts[names[j]] {
			return counts[names[i]] > counts[names[j]]
		}
		return names[i] < names[j]
	})
	if len(names) > 10 {
		names = names[:10]
	}
	parts := make([]string, len(names))
	for index, name := range names {
		parts[index] = fmt.Sprintf("%s:%d", name, counts[name])
	}
	return strings.Join(parts, ",")
}

// Reset replaces the public schema without applying migrations.
func Reset(t *testing.T, database *bun.DB) {
	t.Helper()
	if _, err := database.ExecContext(context.Background(), "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"); err != nil {
		t.Fatalf("reset PostgreSQL integration schema: %v", err)
	}
}

// ResetAndMigrate retains the explicit schema-reset behavior required by
// rollback and migration-order tests. It only operates on the supplied DB.
func ResetAndMigrate(t *testing.T, database *bun.DB) {
	t.Helper()
	Reset(t, database)
	applyMigrations(t, database)
}

func adminDatabase(t *testing.T) *bun.DB {
	t.Helper()
	process.serverOnce.Do(func() {
		process.adminURL = os.Getenv("TEST_DATABASE_URL")
		if process.adminURL == "" {
			// These clusters are disposable; crash durability is not under test.
			config := postgres.DefaultConfig().Version(postgres.V18).Port(0).StartParameters(map[string]string{
				"fsync": "off", "full_page_writes": "off", "synchronous_commit": "off",
				"wal_level": "minimal", "max_wal_senders": "0",
			})
			config = config.Hooks(postgres.Hooks{Ready: []postgres.Hook{
				func(_ context.Context, info postgres.InstanceInfo) (func(context.Context) error, error) {
					process.clusterWorkDir = info.WorkDir
					pidInfo, err := os.Lstat(filepath.Join(info.DataDir, "postmaster.pid"))
					process.clusterPIDReady = err == nil && pidInfo.Mode().IsRegular()
					return func(context.Context) error {
						completed := time.Now()
						process.shutdownCompleted.Store(&completed)
						return pruneStoppedClusterData(info.WorkDir, info.DataDir, process.clusterPIDReady)
					}, nil
				},
			}})
			if runtime.GOOS == "windows" {
				// Embedded PostgreSQL creates restricted child-process tokens. Keep
				// the test process's default DACL usable by the current user so those
				// children can create their pipes and start initdb/postgres.
				if err := ensureCurrentUserDefaultDACL(); err != nil {
					process.serverErr = fmt.Errorf("prepare Windows token default DACL for embedded PostgreSQL: %w", err)
					return
				}
				config = config.Environment("PG_RESTRICT_EXEC=1")
			}
			server := postgres.NewDatabase(config)
			if err := server.Start(); err != nil {
				process.serverErr = fmt.Errorf("start embedded PostgreSQL: %w", err)
				return
			}
			process.server = server
			info := server.Info()
			process.clusterWorkDir = info.WorkDir
			process.ownedDisposable = info.WorkDir != "" && info.DataDir != "" && filepath.Clean(filepath.Dir(info.DataDir)) == filepath.Clean(info.WorkDir)
			process.adminURL = server.ConnectionURL()
		}
		parsed, err := url.Parse(process.adminURL)
		if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Host == "" {
			process.serverErr = fmt.Errorf("TEST_DATABASE_URL must be a PostgreSQL URL with a host")
			return
		}
		process.adminDB, err = openPoolLimit(process.adminURL, adminPoolLimit)
		if err != nil {
			process.serverErr = fmt.Errorf("open PostgreSQL admin endpoint: %w", err)
			return
		}
		if err := process.adminDB.PingContext(context.Background()); err != nil {
			_ = process.adminDB.Close()
			process.adminDB = nil
			process.serverErr = fmt.Errorf("connect to PostgreSQL admin endpoint: %w", err)
			return
		}
		if process.ownedDisposable && runtime.GOOS == "darwin" {
			var err error
			process.cloneEnabled, process.cloneDiagnostic, err = initializeCloneStrategy(process.adminDB, process.clusterWorkDir)
			if err != nil {
				process.serverErr = fmt.Errorf("initialize PostgreSQL clone strategy: %w", err)
				return
			}
		}
		if process.cloneEnabled {
			fmt.Fprintln(os.Stderr, process.cloneDiagnostic)
		} else {
			fmt.Fprintln(os.Stderr, "PostgreSQL database clone strategy: ordinary COPY")
			if process.cloneDiagnostic != "" {
				fmt.Fprintf(os.Stderr, "PostgreSQL clone fallback: %s\n", process.cloneDiagnostic)
			}
		}
	})
	if process.serverErr != nil {
		t.Fatalf("initialize PostgreSQL integration server: %v", process.serverErr)
	}
	return process.adminDB
}

func migratedTemplate(t *testing.T, admin *bun.DB, withRiver bool) string {
	t.Helper()
	process.Lock()
	defer process.Unlock()
	template := &process.template
	if withRiver {
		template = &process.riverTemplate
	}
	if *template != "" {
		return *template
	}
	name := resourcePrefix + "template_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	createDatabase(t, admin, name, "")
	process.createdTemplate.Add(1)
	registerResource(name)
	templateDB, err := openPool(databaseURL(t, name))
	if err != nil {
		t.Fatalf("open migrated PostgreSQL template: %v", err)
	}
	closed := false
	defer func() {
		if closed {
			return
		}
		if err := templateDB.Close(); err != nil {
			t.Errorf("close migrated PostgreSQL template: %v", err)
		}
	}()
	if err := templateDB.PingContext(context.Background()); err != nil {
		t.Fatalf("connect to migrated PostgreSQL template: %v", err)
	}
	applyMigrations(t, templateDB)
	if withRiver {
		driver := riverdatabasesql.New(templateDB.DB)
		migrator, err := rivermigrate.New(driver, nil)
		if err != nil {
			t.Fatalf("create River template migrator: %v", err)
		}
		if _, err := migrator.Migrate(context.Background(), rivermigrate.DirectionUp, nil); err != nil {
			t.Fatalf("apply River template migrations: %v", err)
		}
		if _, err := river.NewClient(driver, &river.Config{}); err != nil {
			t.Fatalf("verify River template client: %v", err)
		}
	}
	if err := templateDB.Close(); err != nil {
		t.Fatalf("close migrated PostgreSQL template: %v", err)
	}
	closed = true
	*template = name
	return name
}

func applyMigrations(t *testing.T, database *bun.DB) {
	t.Helper()
	collection, err := migrations.Collection()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	migrator := migrate.NewMigrator(database, collection, migrate.WithMarkAppliedOnSuccess(true))
	if err := migrator.Init(context.Background()); err != nil {
		t.Fatalf("initialize migrations: %v", err)
	}
	if _, err := migrator.Migrate(context.Background()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
}

type fixturePhase struct {
	DurationNS int64  `json:"duration_ns"`
	Error      string `json:"error,omitempty"`
}

type fixturePhaseLog struct {
	Event     string                  `json:"event"`
	FixtureID int64                   `json:"fixture_id"`
	TestName  string                  `json:"test_name"`
	Phases    map[string]fixturePhase `json:"phases"`
}

type fixtureSpan struct {
	mu        sync.Mutex
	id        int64
	testName  string
	started   time.Time
	openTotal time.Duration
	phases    map[string]fixturePhase
}

func newFixtureSpan(t *testing.T) *fixtureSpan {
	return &fixtureSpan{
		id:       process.fixtureID.Add(1),
		testName: t.Name(),
		started:  time.Now(),
		phases:   make(map[string]fixturePhase),
	}
}

func (span *fixtureSpan) finishOpen() {
	span.mu.Lock()
	span.openTotal = time.Since(span.started)
	span.mu.Unlock()
}

func (span *fixtureSpan) record(name string, duration time.Duration, err error) {
	phase := fixturePhase{DurationNS: int64(duration)}
	if err != nil {
		phase.Error = err.Error()
	}
	span.mu.Lock()
	span.phases[name] = phase
	span.mu.Unlock()
}

func (span *fixtureSpan) log(t *testing.T) {
	span.mu.Lock()
	phases := make(map[string]fixturePhase, len(span.phases)+1)
	for name, phase := range span.phases {
		phases[name] = phase
	}
	phases["open_total"] = fixturePhase{DurationNS: int64(span.openTotal)}
	span.mu.Unlock()
	encoded, err := json.Marshal(fixturePhaseLog{
		Event: "testpostgres.phase", FixtureID: span.id, TestName: span.testName, Phases: phases,
	})
	if err != nil {
		t.Errorf("encode PostgreSQL fixture phase log: %v", err)
		return
	}
	t.Logf("testpostgres.phase %s", encoded)
}

func createDatabase(t *testing.T, admin *bun.DB, name, template string) {
	t.Helper()
	query := "CREATE DATABASE " + quoteIdentifier(name)
	if template != "" {
		query += " TEMPLATE " + quoteIdentifier(template)
	}
	start := time.Now()
	_, err := admin.ExecContext(context.Background(), query)
	if err == nil {
		process.createNanos.Add(int64(time.Since(start)))
	}
	if err != nil {
		t.Fatalf("create isolated PostgreSQL database: %v", err)
	}
}

func createDatabaseForFixture(t *testing.T, admin *bun.DB, name, template string, span *fixtureSpan) {
	t.Helper()
	totalStarted := time.Now()
	useClone := template != "" && process.cloneEnabled
	query := fixtureDatabaseCreateQuery(name, template, useClone)
	ctx := context.Background()
	start := time.Now()
	connection, err := admin.Conn(ctx)
	span.record("clone_connection_acquire", time.Since(start), err)
	if err != nil {
		t.Fatalf("acquire PostgreSQL clone connection: %v", err)
	}
	start = time.Now()
	createErr := runFixtureCreateSession(useClone,
		func() error {
			_, err := connection.ExecContext(ctx, "SET file_copy_method = 'clone'")
			return err
		},
		func() error {
			_, err := connection.ExecContext(ctx, query)
			return err
		},
		func() error {
			resetCtx, cancelReset := context.WithTimeout(context.Background(), cleanupTimeout)
			defer cancelReset()
			_, err := connection.ExecContext(resetCtx, "RESET file_copy_method")
			return err
		},
		func() error { return connection.Raw(func(any) error { return driver.ErrBadConn }) },
		func() { registerResource(name) },
	)
	span.record("clone_exec", time.Since(start), createErr)
	closeErr := connection.Close()
	if createErr != nil {
		t.Fatalf("create isolated PostgreSQL database: %v", createErr)
	}
	if closeErr != nil {
		t.Fatalf("close PostgreSQL clone connection: %v", closeErr)
	}
	process.createNanos.Add(int64(time.Since(totalStarted)))
}

func runFixtureCreateSession(useClone bool, setClone, create, reset, discard func() error, onCreated func()) error {
	if useClone {
		if err := setClone(); err != nil {
			_ = discard()
			return fmt.Errorf("enable PostgreSQL file clone strategy: %w", err)
		}
	}
	createErr := create()
	if createErr == nil {
		onCreated()
	}
	if useClone {
		if err := reset(); err != nil {
			_ = discard()
			if createErr == nil {
				return fmt.Errorf("reset PostgreSQL file clone strategy: %w", err)
			}
		}
	}
	if createErr != nil {
		return fmt.Errorf("create isolated PostgreSQL database: %w", createErr)
	}
	return nil
}

func fixtureDatabaseCreateQuery(name, template string, useClone bool) string {
	query := "CREATE DATABASE " + quoteIdentifier(name)
	if template != "" {
		query += " TEMPLATE " + quoteIdentifier(template)
		if useClone {
			query += " STRATEGY FILE_COPY"
		}
	}
	return query
}

func initializeCloneStrategy(admin *bun.DB, workDir string) (bool, string, error) {
	var cloneAvailable bool
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	if err := admin.NewRaw("SELECT EXISTS (SELECT 1 FROM pg_settings WHERE name = 'file_copy_method' AND 'clone' = ANY(enumvals))").Scan(ctx, &cloneAvailable); err != nil {
		return false, "", fmt.Errorf("query PostgreSQL file_copy_method support: %w", err)
	}
	if !cloneAvailable {
		return false, "PostgreSQL file_copy_method does not offer clone", nil
	}
	available, diagnostic, err := probeCloneFileCopy(workDir)
	if err != nil {
		var cleanupErr cloneProbeCleanupError
		if errors.As(err, &cleanupErr) {
			return false, "", err
		}
		return false, fmt.Sprintf("filesystem clone probe failed: %v", err), nil
	}
	if !available {
		return false, diagnostic, nil
	}
	return true, "PostgreSQL database clone strategy: macOS file clone", nil
}

func databaseURL(t *testing.T, name string) string {
	t.Helper()
	parsed, err := url.Parse(process.adminURL)
	if err != nil {
		t.Fatalf("parse PostgreSQL admin URL: %v", err)
	}
	parsed.Path = "/" + name
	parsed.RawPath = ""
	return parsed.String()
}

func openPool(databaseURL string) (*bun.DB, error) {
	return openPoolLimit(databaseURL, poolMaxOpen)
}

func openPoolLimit(databaseURL string, maxOpen int) (*bun.DB, error) {
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL pool: %w", err)
	}
	database.SetMaxOpenConns(maxOpen)
	database.SetMaxIdleConns(maxOpen / 2)
	return bun.NewDB(database, pgdialect.New()), nil
}

func registerResource(name string) {
	process.resourcesMu.Lock()
	process.resources = append(process.resources, name)
	process.resourcesMu.Unlock()
}

func unregisterResource(name string) {
	process.resourcesMu.Lock()
	defer process.resourcesMu.Unlock()
	for index, resource := range process.resources {
		if resource == name {
			process.resources = append(process.resources[:index], process.resources[index+1:]...)
			return
		}
	}
}

func dropDatabase(name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	process.dropAttempts.Add(1)
	start := time.Now()
	_, err := process.adminDB.ExecContext(ctx, "DROP DATABASE IF EXISTS "+quoteIdentifier(name)+" WITH (FORCE)")
	if err == nil {
		process.dropped.Add(1)
		process.dropNanos.Add(int64(time.Since(start)))
	}
	return err
}

func dropRegisteredDatabase(name string) error {
	return dropAndUnregister(name, dropDatabase)
}

func dropRegisteredDatabaseForFixture(name string, span *fixtureSpan) error {
	return dropAndUnregister(name, func(name string) error { return dropDatabaseForFixture(name, span) })
}

func dropDatabaseForFixture(name string, span *fixtureSpan) error {
	totalStarted := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	process.dropAttempts.Add(1)
	start := time.Now()
	connection, err := process.adminDB.Conn(ctx)
	span.record("drop_connection_acquire", time.Since(start), err)
	if err != nil {
		return err
	}
	start = time.Now()
	_, err = connection.ExecContext(ctx, "DROP DATABASE IF EXISTS "+quoteIdentifier(name)+" WITH (FORCE)")
	span.record("drop_exec", time.Since(start), err)
	closeErr := connection.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	process.dropped.Add(1)
	process.dropNanos.Add(int64(time.Since(totalStarted)))
	return nil
}

func dropAndUnregister(name string, drop func(string) error) error {
	if err := drop(name); err != nil {
		return err
	}
	unregisterResource(name)
	return nil
}

func releaseFixtureDatabase(name string, span *fixtureSpan, ownedDisposable bool) error {
	return releaseFixture(name, ownedDisposable, func() error {
		return dropRegisteredDatabaseForFixture(name, span)
	})
}

func releaseFixture(_ string, ownedDisposable bool, drop func() error) error {
	if ownedDisposable {
		return nil
	}
	return drop()
}

func cleanupProcess() error {
	process.resourcesMu.Lock()
	resources := append([]string(nil), process.resources...)
	cleanups := append([]func() error(nil), process.cleanups...)
	process.resourcesMu.Unlock()
	process.Lock()
	server := process.server
	admin := process.adminDB
	if server != nil {
		// Info can be cleared by Stop; capture the work directory while the
		// cluster is still running.
		process.clusterWorkDir = server.Info().WorkDir
	}
	process.Unlock()
	ownedDisposable := process.ownedDisposable

	var callbacksTotal, externalDropsTotal, closeAdminTotal, stopTotal, cleanupTotal time.Duration
	var shutdownDuration, removeAndReleaseDuration time.Duration
	var shutdownObserved bool
	timedCleanups := make([]func() error, len(cleanups))
	for index, callback := range cleanups {
		timedCleanups[index] = func() error {
			callbackStart := time.Now()
			defer func() { callbacksTotal += time.Since(callbackStart) }()
			return callback()
		}
	}
	defer func() {
		split := ""
		if shutdownObserved {
			split = fmt.Sprintf(" shutdown=%s remove_and_release=%s",
				shutdownDuration.Round(time.Millisecond), removeAndReleaseDuration.Round(time.Millisecond))
		}
		fmt.Fprintf(os.Stderr, "testpostgres.cleanup callbacks=%s external_drops=%s close_admin=%s stop=%s total=%s owned_disposable=%t resources=%d%s\n",
			callbacksTotal.Round(time.Millisecond), externalDropsTotal.Round(time.Millisecond),
			closeAdminTotal.Round(time.Millisecond), stopTotal.Round(time.Millisecond),
			cleanupTotal.Round(time.Millisecond), ownedDisposable, len(resources), split)
	}()
	cleanupStart := time.Now()
	err := cleanupResources(cleanupPlan{
		resources: resources, cleanups: timedCleanups, ownedDisposable: ownedDisposable,
		drop: func(name string) error {
			dropStart := time.Now()
			defer func() { externalDropsTotal += time.Since(dropStart) }()
			return dropRegisteredDatabase(name)
		},
		closeAdmin: func() error {
			closeStart := time.Now()
			defer func() { closeAdminTotal = time.Since(closeStart) }()
			if admin == nil {
				return nil
			}
			return admin.Close()
		},
		stop: func() error {
			stopStart := time.Now()
			defer func() { stopTotal = time.Since(stopStart) }()
			if server == nil {
				return nil
			}
			process.shutdownCompleted.Store(nil)
			err := server.Stop()
			if completed := process.shutdownCompleted.Load(); completed != nil {
				shutdownObserved = true
				shutdownDuration = completed.Sub(stopStart)
				// Not a pure filesystem measure: this covers the remaining Ready
				// cleanups, session lock release and work-directory removal.
				removeAndReleaseDuration = time.Since(*completed)
			}
			return err
		},
		clear: func() {
			process.resourcesMu.Lock()
			process.resources = nil
			process.resourcesMu.Unlock()
			if ownedDisposable && server != nil {
				process.clusterReclaimed.Add(int64(len(resources)))
			}
		},
	})
	cleanupTotal = time.Since(cleanupStart)
	return err
}

type cleanupPlan struct {
	resources       []string
	cleanups        []func() error
	ownedDisposable bool
	drop            func(string) error
	closeAdmin      func() error
	stop            func() error
	clear           func()
}

func cleanupResources(plan cleanupPlan) error {
	var failures []string
	for index := len(plan.cleanups) - 1; index >= 0; index-- {
		if err := plan.cleanups[index](); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if !plan.ownedDisposable {
		for index := len(plan.resources) - 1; index >= 0; index-- {
			if err := plan.drop(plan.resources[index]); err != nil {
				failures = append(failures, err.Error())
			}
		}
	}
	if err := plan.closeAdmin(); err != nil {
		failures = append(failures, err.Error())
	}
	stopErr := plan.stop()
	if stopErr != nil {
		failures = append(failures, stopErr.Error())
	}
	if plan.ownedDisposable && stopErr == nil {
		plan.clear()
	}
	if len(failures) > 0 {
		return fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return nil
}

func pruneStoppedClusterData(workDir, dataDir string, pidPresentAtReady bool) error {
	if !pidPresentAtReady || !filepath.IsAbs(workDir) || !filepath.IsAbs(dataDir) ||
		filepath.Clean(workDir) == string(filepath.Separator) || filepath.Clean(dataDir) == string(filepath.Separator) ||
		filepath.VolumeName(workDir) != filepath.VolumeName(dataDir) ||
		filepath.Clean(filepath.Dir(dataDir)) != filepath.Clean(workDir) || filepath.Base(dataDir) != "data" {
		return nil
	}
	for _, path := range []string{workDir, dataDir, filepath.Join(dataDir, "base")} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
	}
	_, err := os.Lstat(filepath.Join(dataDir, "postmaster.pid"))
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		return nil
	}

	baseDir := filepath.Join(dataDir, "base")
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil
	}
	type target struct {
		path string
		name string
	}
	targets := make([]target, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !numericDirectoryName(entry.Name()) {
			continue
		}
		path := filepath.Join(baseDir, entry.Name())
		info, err := os.Lstat(path)
		if err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			targets = append(targets, target{path: path, name: entry.Name()})
		}
	}

	started := time.Now()
	work := make(chan int)
	errorsByTarget := make([]error, len(targets))
	var workers sync.WaitGroup
	workerCount := min(clusterDeleteWorkers, len(targets))
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range work {
				errorsByTarget[index] = os.RemoveAll(targets[index].path)
			}
		}()
	}
	for index := range targets {
		work <- index
	}
	close(work)
	workers.Wait()
	var failures []error
	for index, err := range errorsByTarget {
		if err != nil {
			failures = append(failures, fmt.Errorf("remove PostgreSQL database directory %s: %w", targets[index].name, err))
		}
	}
	fmt.Fprintf(os.Stderr, "testpostgres.parallel_delete directories=%d duration=%s\n", len(targets), time.Since(started).Round(time.Millisecond))
	return errors.Join(failures...)
}

func numericDirectoryName(name string) bool {
	if name == "" {
		return false
	}
	for _, char := range name {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func quoteIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}
