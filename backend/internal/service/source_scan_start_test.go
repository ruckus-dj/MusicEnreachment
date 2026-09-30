package service_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	completed bool
	calls     int
}

func (fixture *sourceScanSetupFixture) SetupCompleted(context.Context) (bool, error) {
	fixture.calls++
	return fixture.completed, nil
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

	reads      int
	operations []*persistence.Operation
	args       []river.JobArgs
	enqueueErr error
}

func (fixture *sourceScanStartRepositoryFixture) GetSourceRoot(ctx context.Context, id uuid.UUID) (*persistence.SourceRoot, error) {
	fixture.reads++
	return fixture.sourceRootRepositoryFixture.GetSourceRoot(ctx, id)
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
}

func newSourceScanStartFixture(t *testing.T, setup service.SourceScanSetup, platform settings.PlatformState) sourceScanStartFixture {
	t.Helper()
	repository := &sourceScanStartRepositoryFixture{
		sourceRootRepositoryFixture: &sourceRootRepositoryFixture{locations: map[uuid.UUID]int64{}},
	}
	tools, output := t.TempDir(), t.TempDir()
	roots := service.NewSourceRoots(repository, managedPathsFixture{tools: tools, output: output})
	root, err := roots.Create(context.Background(), "Music", t.TempDir())
	if err != nil {
		t.Fatalf("create the source root: %v", err)
	}
	inserter := &riverInserterFixture{}
	return sourceScanStartFixture{
		scans:      service.NewSourceScanOperations(repository, roots, setup, platform, inserter),
		repository: repository,
		river:      inserter,
		root:       root,
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
	fixture := newSourceScanStartFixture(t, &sourceScanSetupFixture{completed: true}, supportedScanStartPlatform())

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
		ConfiguredPath: fixture.root.ConfiguredPath,
	}
	if snapshot != want {
		t.Fatalf("scan snapshot = %+v, want %+v", snapshot, want)
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
