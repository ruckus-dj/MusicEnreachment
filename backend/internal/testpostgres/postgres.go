// Package testpostgres provides isolated PostgreSQL databases for integration tests.
package testpostgres

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	postgres "github.com/fergusstrange/embedded-postgres/v2"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/migrate"
)

const (
	poolMaxOpen    = 8
	adminPoolLimit = 4
	resourcePrefix = "melotrove_test_"
	cleanupTimeout = time.Minute
)

var process struct {
	sync.Mutex
	resourcesMu sync.Mutex
	serverOnce  sync.Once
	serverErr   error
	server      *postgres.EmbeddedPostgres
	adminURL    string
	adminDB     *bun.DB
	template    string
	resources   []string
	cleanups    []func() error
}

// Open creates an empty, uniquely named database owned by this test. The
// TEST_DATABASE_URL value, when supplied, is treated only as an admin endpoint:
// tests never reset or drop the database named by that URL.
func Open(t *testing.T) *bun.DB {
	t.Helper()
	admin := adminDatabase(t)
	name := resourcePrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
	createDatabase(t, admin, name, "")
	registerResource(name)
	databaseURL := databaseURL(t, name)
	database, err := openPool(databaseURL)
	if err != nil {
		t.Fatalf("open isolated PostgreSQL database: %v", err)
	}
	if err := database.PingContext(context.Background()); err != nil {
		_ = database.Close()
		t.Fatalf("connect to isolated PostgreSQL database: %v", err)
	}
	t.Cleanup(func() {
		if err := dropDatabase(name); err != nil {
			t.Errorf("drop isolated PostgreSQL database %s: %v", name, err)
		}
	})
	t.Cleanup(func() { _ = database.Close() })
	return database
}

// OpenMigrated creates an isolated database cloned from the process-local
// application-schema template. The template is migrated once and is never
// modified by individual tests.
func OpenMigrated(t *testing.T) *bun.DB {
	t.Helper()
	admin := adminDatabase(t)
	template := migratedTemplate(t, admin)
	name := resourcePrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
	createDatabase(t, admin, name, template)
	registerResource(name)
	database, err := openPool(databaseURL(t, name))
	if err != nil {
		t.Fatalf("open migrated PostgreSQL database: %v", err)
	}
	if err := database.PingContext(context.Background()); err != nil {
		_ = database.Close()
		t.Fatalf("connect to migrated PostgreSQL database: %v", err)
	}
	t.Cleanup(func() {
		if err := dropDatabase(name); err != nil {
			t.Errorf("drop isolated PostgreSQL database %s: %v", name, err)
		}
	})
	t.Cleanup(func() { _ = database.Close() })
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
// It is run by Run after tests and before database/container teardown.
func AddCleanup(cleanup func() error) {
	process.resourcesMu.Lock()
	defer process.resourcesMu.Unlock()
	process.cleanups = append(process.cleanups, cleanup)
}

// Run executes a package's test suite and removes only databases and templates
// created by this helper, then stops its lazily started container.
func Run(m *testing.M) int {
	code := m.Run()
	if err := cleanupProcess(); err != nil {
		fmt.Fprintf(os.Stderr, "clean up PostgreSQL integration resources: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	return code
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
			config := postgres.DefaultConfig().Version(postgres.V18).Port(0).StartParameters(map[string]string{"fsync": "off"})
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
		}
	})
	if process.serverErr != nil {
		t.Fatalf("initialize PostgreSQL integration server: %v", process.serverErr)
	}
	return process.adminDB
}

func migratedTemplate(t *testing.T, admin *bun.DB) string {
	t.Helper()
	process.Lock()
	defer process.Unlock()
	if process.template != "" {
		return process.template
	}
	name := resourcePrefix + "template_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	createDatabase(t, admin, name, "")
	registerResource(name)
	templateDB, err := openPool(databaseURL(t, name))
	if err != nil {
		t.Fatalf("open migrated PostgreSQL template: %v", err)
	}
	defer func() {
		if err := templateDB.Close(); err != nil {
			t.Errorf("close migrated PostgreSQL template: %v", err)
		}
	}()
	if err := templateDB.PingContext(context.Background()); err != nil {
		t.Fatalf("connect to migrated PostgreSQL template: %v", err)
	}
	applyMigrations(t, templateDB)
	process.template = name
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

func createDatabase(t *testing.T, admin *bun.DB, name, template string) {
	t.Helper()
	query := "CREATE DATABASE " + quoteIdentifier(name)
	if template != "" {
		query += " TEMPLATE " + quoteIdentifier(template)
	}
	if _, err := admin.ExecContext(context.Background(), query); err != nil {
		t.Fatalf("create isolated PostgreSQL database: %v", err)
	}
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

func dropDatabase(name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	_, err := process.adminDB.ExecContext(ctx, "DROP DATABASE IF EXISTS "+quoteIdentifier(name)+" WITH (FORCE)")
	return err
}

func cleanupProcess() error {
	process.resourcesMu.Lock()
	resources := append([]string(nil), process.resources...)
	cleanups := append([]func() error(nil), process.cleanups...)
	process.resourcesMu.Unlock()
	process.Lock()
	server := process.server
	admin := process.adminDB
	process.Unlock()
	var failures []string
	for index := len(cleanups) - 1; index >= 0; index-- {
		if err := cleanups[index](); err != nil {
			failures = append(failures, err.Error())
		}
	}
	for index := len(resources) - 1; index >= 0; index-- {
		if err := dropDatabase(resources[index]); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if admin != nil {
		if err := admin.Close(); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if server != nil {
		if err := server.Stop(); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return nil
}

func quoteIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}
