// Package testpostgres provides isolated PostgreSQL databases for integration tests.
package testpostgres

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
	"github.com/uptrace/bun/migrate"
)

const (
	postgresImage  = "postgres:17"
	poolMaxOpen    = 8
	adminPoolLimit = 4
	resourcePrefix = "melotrove_test_"
)

var process struct {
	sync.Mutex
	resourcesMu sync.Mutex
	serverOnce  sync.Once
	serverErr   error
	container   testcontainers.Container
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
	database := openPool(databaseURL)
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
	database := openPool(databaseURL(t, name))
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
			container, err := postgres.Run(context.Background(), postgresImage,
				postgres.WithDatabase("postgres"),
				postgres.WithUsername("postgres"),
				postgres.WithPassword("postgres"),
				testcontainers.WithTmpfs(map[string]string{"/var/lib/postgresql/data": "rw"}),
				testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(30*time.Second)),
			)
			if err != nil {
				process.serverErr = fmt.Errorf("start PostgreSQL Testcontainer: %w", err)
				return
			}
			process.container = container
			process.adminURL, err = container.ConnectionString(context.Background(), "sslmode=disable")
			if err != nil {
				process.serverErr = fmt.Errorf("get PostgreSQL Testcontainer connection string: %w", err)
				return
			}
		}
		parsed, err := url.Parse(process.adminURL)
		if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Host == "" {
			process.serverErr = fmt.Errorf("TEST_DATABASE_URL must be a PostgreSQL URL with a host")
			return
		}
		process.adminDB = openPoolLimit(process.adminURL, adminPoolLimit)
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
	templateDB := openPool(databaseURL(t, name))
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

func openPool(databaseURL string) *bun.DB {
	return openPoolLimit(databaseURL, poolMaxOpen)
}

func openPoolLimit(databaseURL string, maxOpen int) *bun.DB {
	database := bun.NewDB(sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(databaseURL))), pgdialect.New())
	database.SetMaxOpenConns(maxOpen)
	database.SetMaxIdleConns(maxOpen / 2)
	return database
}

func registerResource(name string) {
	process.resourcesMu.Lock()
	process.resources = append(process.resources, name)
	process.resourcesMu.Unlock()
}

func dropDatabase(name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
	container := process.container
	admin := process.adminDB
	process.Unlock()
	var failures []string
	for index := len(cleanups) - 1; index >= 0; index-- {
		if err := cleanups[index](); err != nil {
			failures = append(failures, err.Error())
		}
	}
	for index := len(resources) - 1; index >= 0; index-- {
		queryCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		_, err := admin.ExecContext(queryCtx, "DROP DATABASE IF EXISTS "+quoteIdentifier(resources[index])+" WITH (FORCE)")
		cancel()
		if err != nil {
			failures = append(failures, err.Error())
		}
	}
	if admin != nil {
		if err := admin.Close(); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if container != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := container.Terminate(ctx); err != nil {
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
