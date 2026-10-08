package service_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

var (
	_ service.SourceScanStartRepository = (*persistence.SourceInventoryRepository)(nil)
	_ service.SourceScanPathValidator   = (*service.SourceRoots)(nil)
	_ service.SourceScanSetup           = (*settings.Registry)(nil)
)

type sourceScanSetupFixture struct {
	completed  bool
	calls      int
	runtime    settings.RuntimeSettings
	runtimeErr error
	sha256     bool
	sha256Err  error
	shaCalls   int
}

func (fixture *sourceScanSetupFixture) SetupCompleted(context.Context) (bool, error) {
	fixture.calls++
	return fixture.completed, nil
}

func (fixture *sourceScanSetupFixture) ReadRuntimeSettings(context.Context) (settings.RuntimeSettings, error) {
	return fixture.runtime, fixture.runtimeErr
}

func (fixture *sourceScanSetupFixture) GetSHA256Enabled(context.Context) (bool, error) {
	fixture.shaCalls++
	return fixture.sha256, fixture.sha256Err
}

// riverInserterFixture stands in for the River client of a scan start. The
// service passes it to the repository, so a call here means the service tried to
// enqueue outside the transaction that stores the operation; err fails the job
// insert of a real repository instead.
type riverInserterFixture struct {
	calls int
	err   error
}

func (fixture *riverInserterFixture) InsertTx(context.Context, *sql.Tx, river.JobArgs, *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	fixture.calls++
	if fixture.err != nil {
		return nil, fixture.err
	}
	return nil, errors.New("the service must enqueue through the repository")
}

type sourceScanStartRepositoryFixture struct {
	*sourceRootRepositoryFixture

	reads          int
	operations     []*persistence.Operation
	args           []river.JobArgs
	enqueueErr     error
	unavailable    []persistence.SourceRootUnavailable
	unavailableErr error
	listErr        error
}

func (fixture *sourceScanStartRepositoryFixture) GetSourceRoot(ctx context.Context, id uuid.UUID) (*persistence.SourceRoot, error) {
	fixture.reads++
	return fixture.sourceRootRepositoryFixture.GetSourceRoot(ctx, id)
}

func (*sourceScanStartRepositoryFixture) GetInstallation(context.Context, uuid.UUID) (*persistence.ToolInstallation, error) {
	return nil, sql.ErrNoRows
}

func (fixture *sourceScanStartRepositoryFixture) ListSourceRoots(ctx context.Context) ([]persistence.SourceRoot, error) {
	if fixture.listErr != nil {
		return nil, fixture.listErr
	}
	return fixture.sourceRootRepositoryFixture.ListSourceRoots(ctx)
}

func (fixture *sourceScanStartRepositoryFixture) MarkSourceRootUnavailableForRoot(_ context.Context, unavailable persistence.SourceRootUnavailable) error {
	if fixture.unavailableErr != nil {
		return fixture.unavailableErr
	}
	fixture.unavailable = append(fixture.unavailable, unavailable)
	return nil
}

func (fixture *sourceScanStartRepositoryFixture) CreateSourceScanOperationAndEnqueue(_ context.Context, operation *persistence.Operation, _ persistence.RiverInserter, args river.JobArgs, _ *river.InsertOpts) error {
	if fixture.enqueueErr != nil {
		return fixture.enqueueErr
	}
	fixture.operations = append(fixture.operations, operation)
	fixture.args = append(fixture.args, args)
	return nil
}

type sourceScanStartFixture struct {
	scans      *service.SourceScanOperations
	repository *sourceScanStartRepositoryFixture
	river      *riverInserterFixture
	root       service.SourceRoot
	tools      string
}

func newSourceScanStartFixture(t *testing.T, setup service.SourceScanSetup, platform settings.PlatformState) sourceScanStartFixture {
	t.Helper()
	repository := &sourceScanStartRepositoryFixture{
		sourceRootRepositoryFixture: &sourceRootRepositoryFixture{locations: map[uuid.UUID]int64{}},
	}
	tools, output := t.TempDir(), t.TempDir()
	roots := service.NewSourceRoots(repository, managedPathsFixture{tools: tools, output: output})
	root, err := roots.Create(context.Background(), "Music", t.TempDir(), "in_place")
	if err != nil {
		t.Fatalf("create the source root: %v", err)
	}
	inserter := &riverInserterFixture{}
	return sourceScanStartFixture{
		scans:      service.NewSourceScanOperations(repository, roots, setup, platform, inserter),
		repository: repository,
		river:      inserter,
		root:       root,
		tools:      tools,
	}
}

func supportedScanStartPlatform() settings.PlatformState {
	return settings.PlatformState{Platform: settings.Platform{GOOS: "linux", GOARCH: "amd64"}}
}

// TestSourceScanStartRecordsOnlyTheRootSnapshotAndTheOperationID pins the
// durable contract of a start: the operation carries the root, its configured
// path and the snapshot version, and the River job carries nothing but the
// operation ID the worker reloads everything else from.
func TestSourceScanStartRecordsOnlyTheRootSnapshotAndTheOperationID(t *testing.T) {
	setup := &sourceScanSetupFixture{completed: true}
	fixture := newSourceScanStartFixture(t, setup, supportedScanStartPlatform())

	operation, err := fixture.scans.Start(context.Background(), fixture.root.ID)
	if err != nil {
		t.Fatalf("start a scan: %v", err)
	}
	if operation.Kind != service.SourceScanOperationKind || operation.State != "queued" ||
		operation.Stage != service.SourceScanStageQueued || operation.TargetSourceRootID == nil ||
		*operation.TargetSourceRootID != fixture.root.ID {
		t.Fatalf("started operation = %+v, want a queued scan of %s", operation, fixture.root.ID)
	}
	var snapshot service.ScanSourceSnapshot
	if err := json.Unmarshal(operation.InputSnapshot, &snapshot); err != nil {
		t.Fatalf("decode the scan snapshot: %v", err)
	}
	want := service.ScanSourceSnapshot{
		SchemaVersion: service.SourceScanSnapshotVersion, SourceRootID: fixture.root.ID,
		ConfiguredPath: fixture.root.ConfiguredPath, ScanGeneration: fixture.root.ScanGeneration,
		SHA256Enabled: new(false), Tools: []persistence.SourceAnalysisToolSelection{},
	}
	if !reflect.DeepEqual(snapshot, want) {
		t.Fatalf("scan snapshot = %+v, want %+v", snapshot, want)
	}
	if setup.shaCalls != 1 {
		t.Fatalf("SHA-256 policy reads = %d, want one explicit read (including false)", setup.shaCalls)
	}
	if len(fixture.repository.operations) != 1 || fixture.repository.operations[0] != operation {
		t.Fatalf("enqueued operations = %+v, want the returned operation stored once", fixture.repository.operations)
	}
	if len(fixture.repository.args) != 1 {
		t.Fatalf("enqueued job args = %+v, want one", fixture.repository.args)
	}
	args, ok := fixture.repository.args[0].(service.ScanSourceJobArgs)
	if !ok || args.OperationID != operation.ID {
		t.Fatalf("job args = %#v, want the operation id alone", fixture.repository.args[0])
	}
	if args.Kind() != service.SourceScanJobKind {
		t.Fatalf("job kind = %q, want %q", args.Kind(), service.SourceScanJobKind)
	}
	if fixture.river.calls != 0 {
		t.Fatalf("the service inserted %d River jobs itself, want the repository to own the insert", fixture.river.calls)
	}
}

func TestSourceScanStartPinsSHA256PolicyAndRefusesPolicyReadErrors(t *testing.T) {
	t.Run("enabled policy is pinned", func(t *testing.T) {
		setup := &sourceScanSetupFixture{completed: true, sha256: true}
		fixture := newSourceScanStartFixture(t, setup, supportedScanStartPlatform())
		operation, err := fixture.scans.Start(context.Background(), fixture.root.ID)
		if err != nil {
			t.Fatalf("start with SHA-256 enabled: %v", err)
		}
		var snapshot service.ScanSourceSnapshot
		if err := json.Unmarshal(operation.InputSnapshot, &snapshot); err != nil {
			t.Fatalf("decode snapshot: %v", err)
		}
		if snapshot.SHA256Enabled == nil || !*snapshot.SHA256Enabled {
			t.Fatalf("snapshot SHA-256 policy = %v, want explicitly true", snapshot.SHA256Enabled)
		}
	})

	t.Run("policy read error creates no operation", func(t *testing.T) {
		setup := &sourceScanSetupFixture{completed: true, sha256Err: errors.New("settings unavailable")}
		fixture := newSourceScanStartFixture(t, setup, supportedScanStartPlatform())
		operation, err := fixture.scans.Start(context.Background(), fixture.root.ID)
		if err == nil || !strings.Contains(err.Error(), "read SHA-256 setting") {
			t.Fatalf("start after policy read failure = %+v, %v; want wrapped setting error", operation, err)
		}
		if operation != nil || len(fixture.repository.operations) != 0 || len(fixture.repository.args) != 0 {
			t.Fatalf("failed policy read created operation/job: result=%+v operations=%d jobs=%d", operation, len(fixture.repository.operations), len(fixture.repository.args))
		}
		if setup.shaCalls != 1 {
			t.Fatalf("SHA-256 policy reads = %d, want one", setup.shaCalls)
		}
	})
}

// TestSourceScanStartRefusesAnUnusablePlatformAndAnUnfinishedSetup covers the
// two instance-level refusals: both read the root and record nothing, and an
// unusable platform is decided before the Setup state is even consulted.
func TestSourceScanStartRefusesAnUnusablePlatformAndAnUnfinishedSetup(t *testing.T) {
	for _, refusal := range []struct {
		name           string
		completed      bool
		platform       settings.PlatformState
		wantSetupCalls int
	}{
		{
			name: "unsupported platform", completed: true,
			platform:       settings.PlatformState{Platform: settings.Platform{GOOS: "windows", GOARCH: "arm64"}, Diagnostic: true, Reason: "unsupported platform"},
			wantSetupCalls: 0,
		},
		{
			name: "unfinished setup", completed: false, platform: supportedScanStartPlatform(),
			wantSetupCalls: 1,
		},
	} {
		t.Run(refusal.name, func(t *testing.T) {
			setup := &sourceScanSetupFixture{completed: refusal.completed}
			fixture := newSourceScanStartFixture(t, setup, refusal.platform)
			readsBefore := fixture.repository.reads

			operation, err := fixture.scans.Start(context.Background(), fixture.root.ID)
			if !errors.Is(err, service.ErrSourceScanNotReady) {
				t.Fatalf("start on %s = %+v, %v; want ErrSourceScanNotReady", refusal.name, operation, err)
			}
			if operation != nil {
				t.Fatalf("refused start returned the operation %+v, want none", operation)
			}
			if reads := fixture.repository.reads - readsBefore; reads != 1 {
				t.Fatalf("root reads during the start = %d, want 1", reads)
			}
			if setup.calls != refusal.wantSetupCalls {
				t.Fatalf("setup reads during the start = %d, want %d", setup.calls, refusal.wantSetupCalls)
			}
			if len(fixture.repository.operations) != 0 {
				t.Fatalf("refused start stored %+v, want no operation", fixture.repository.operations)
			}
		})
	}
}

// TestSourceScanStartRefusesADisabledRootThroughTheRepository pins the mapping
// the start applies to the root state only the transaction can decide: the
// disabled refusal is reported as ErrSourceScanDisabled and carries no
// operation.
func TestSourceScanStartRefusesADisabledRootThroughTheRepository(t *testing.T) {
	fixture := newSourceScanStartFixture(t, &sourceScanSetupFixture{completed: true}, supportedScanStartPlatform())
	fixture.repository.enqueueErr = fmt.Errorf("enqueue source scan: the root %q is disabled: %w", fixture.root.DisplayName, persistence.ErrSourceRootDisabled)

	operation, err := fixture.scans.Start(context.Background(), fixture.root.ID)
	if !errors.Is(err, service.ErrSourceScanDisabled) {
		t.Fatalf("start on a disabled root = %+v, %v; want ErrSourceScanDisabled", operation, err)
	}
	if operation != nil {
		t.Fatalf("refused start returned the operation %+v, want none", operation)
	}
}

// TestSourceScanStartRecordsAProvenInaccessibleRootWithoutAnOperation pins the
// start half of the unavailable state: a registered directory the validator
// proves inaccessible is recorded on the root with a nonempty safe reason, and
// the refusal still creates neither an operation nor a River job.
func TestSourceScanStartRecordsAProvenInaccessibleRootWithoutAnOperation(t *testing.T) {
	fixture := newSourceScanStartFixture(t, &sourceScanSetupFixture{completed: true}, supportedScanStartPlatform())
	if err := os.RemoveAll(fixture.root.ConfiguredPath); err != nil {
		t.Fatalf("remove the source directory: %v", err)
	}

	operation, err := fixture.scans.Start(context.Background(), fixture.root.ID)

	if !errors.Is(err, service.ErrSourceRootInaccessible) {
		t.Fatalf("start on a missing directory = %+v, %v; want ErrSourceRootInaccessible", operation, err)
	}
	if operation != nil || len(fixture.repository.operations) != 0 {
		t.Fatalf("refused start returned %+v and stored %+v, want no operation", operation, fixture.repository.operations)
	}
	if len(fixture.repository.unavailable) != 1 {
		t.Fatalf("unavailable reports = %+v, want exactly one", fixture.repository.unavailable)
	}
	report := fixture.repository.unavailable[0]
	if report.RootID != fixture.root.ID || report.SafeError != service.SourceScanDirectoryUnavailableReason {
		t.Fatalf("unavailable report = %+v, want root %s with the safe reason", report, fixture.root.ID)
	}
	if report.ExpectedConfiguredPath != fixture.root.ConfiguredPath || report.ExpectedLastSuccess != nil {
		t.Fatalf("unavailable guard = path %q success %v, want the root the attempt observed", report.ExpectedConfiguredPath, report.ExpectedLastSuccess)
	}
	if strings.Contains(report.SafeError, fixture.root.ConfiguredPath) {
		t.Fatalf("safe reason %q leaks the configured path", report.SafeError)
	}
}

// TestSourceScanStartDoesNotBlameTheRootForAnUnrelatedValidationFailure proves
// the start converts only a proven directory access failure into the unavailable
// state: a managed-path overlap, a duplicate configured path, a failed database
// read and the active-scan conflict of the enqueue all refuse the scan without
// touching the root's availability.
func TestSourceScanStartDoesNotBlameTheRootForAnUnrelatedValidationFailure(t *testing.T) {
	ctx := context.Background()
	t.Run("managed path overlap", func(t *testing.T) {
		fixture := newSourceScanStartFixture(t, &sourceScanSetupFixture{completed: true}, supportedScanStartPlatform())
		overlap := filepath.Join(fixture.tools, "inner")
		if err := os.MkdirAll(overlap, 0o755); err != nil {
			t.Fatalf("create the overlapping directory: %v", err)
		}
		fixture.repository.roots[0].ConfiguredPath = overlap

		operation, err := fixture.scans.Start(ctx, fixture.root.ID)

		if err == nil || errors.Is(err, service.ErrSourceRootInaccessible) {
			t.Fatalf("start on a managed overlap = %+v, %v; want a refusal without the access marker", operation, err)
		}
		if len(fixture.repository.unavailable) != 0 {
			t.Fatalf("the managed overlap recorded %+v, want the root untouched", fixture.repository.unavailable)
		}
	})
	t.Run("duplicate configured path", func(t *testing.T) {
		fixture := newSourceScanStartFixture(t, &sourceScanSetupFixture{completed: true}, supportedScanStartPlatform())
		fixture.repository.roots = append(fixture.repository.roots, &persistence.SourceRoot{
			ID: uuid.New(), DisplayName: "Duplicate", ConfiguredPath: fixture.root.ConfiguredPath, Enabled: true,
		})

		operation, err := fixture.scans.Start(ctx, fixture.root.ID)

		if err == nil || errors.Is(err, service.ErrSourceRootInaccessible) {
			t.Fatalf("start on a duplicate path = %+v, %v; want a refusal without the access marker", operation, err)
		}
		if len(fixture.repository.unavailable) != 0 {
			t.Fatalf("the duplicate path recorded %+v, want the root untouched", fixture.repository.unavailable)
		}
	})
	t.Run("database read failure", func(t *testing.T) {
		fixture := newSourceScanStartFixture(t, &sourceScanSetupFixture{completed: true}, supportedScanStartPlatform())
		fixture.repository.listErr = errors.New("the source roots could not be read")

		operation, err := fixture.scans.Start(ctx, fixture.root.ID)

		if err == nil || errors.Is(err, service.ErrSourceRootInaccessible) {
			t.Fatalf("start with a failed root read = %+v, %v; want a refusal without the access marker", operation, err)
		}
		if len(fixture.repository.unavailable) != 0 {
			t.Fatalf("the database failure recorded %+v, want the root untouched", fixture.repository.unavailable)
		}
	})
	t.Run("root with an active scan", func(t *testing.T) {
		fixture := newSourceScanStartFixture(t, &sourceScanSetupFixture{completed: true}, supportedScanStartPlatform())
		fixture.repository.enqueueErr = fmt.Errorf("enqueue source scan: %w", persistence.ErrSourceRootActiveScan)

		operation, err := fixture.scans.Start(ctx, fixture.root.ID)

		if !errors.Is(err, service.ErrSourceRootBusy) {
			t.Fatalf("start on a root with an active scan = %+v, %v; want ErrSourceRootBusy", operation, err)
		}
		if errors.Is(err, service.ErrSourceRootInaccessible) {
			t.Fatalf("the active-scan conflict = %v, want no access marker", err)
		}
		if len(fixture.repository.unavailable) != 0 {
			t.Fatalf("the active-scan conflict recorded %+v, want the root untouched", fixture.repository.unavailable)
		}
	})
}
