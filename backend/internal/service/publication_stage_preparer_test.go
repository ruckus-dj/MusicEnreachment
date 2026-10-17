package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type stagePreparationRepository struct {
	input      persistence.PublicationInputs
	hold       persistence.PublicationSourceHold
	records    []persistence.PublicationPreparationRecord
	checks     int
	journalErr error
	onCheck    func(int)
}

func (repository *stagePreparationRepository) PublicationPreparationInputs(context.Context, persistence.PublicationDeliveryGuard) (persistence.PublicationInputs, bool, error) {
	return repository.input, true, nil
}

func (repository *stagePreparationRepository) CheckPublicationSourceHoldForDelivery(_ context.Context, _ persistence.PublicationDeliveryGuard, _ uuid.UUID) (persistence.PublicationSourceHold, bool, error) {
	repository.checks++
	if repository.onCheck != nil {
		repository.onCheck(repository.checks)
	}
	return repository.hold, true, nil
}

func (repository *stagePreparationRepository) RecordPublicationPreparation(_ context.Context, _ persistence.PublicationDeliveryGuard, record persistence.PublicationPreparationRecord) (persistence.PublicationPreparationOutcome, error) {
	if repository.journalErr != nil {
		return persistence.PublicationPreparationStale, repository.journalErr
	}
	repository.records = append(repository.records, record)
	return persistence.PublicationPreparationRecorded, nil
}

type closeErrorOutputDirectory struct {
	sourcefs.OutputDirectory
	err error
}

func (directory *closeErrorOutputDirectory) CreateDirExclusive(ctx context.Context, name string) (sourcefs.OutputDirectory, error) {
	child, err := directory.OutputDirectory.CreateDirExclusive(ctx, name)
	if err != nil {
		return nil, err
	}
	return &closeErrorOutputDirectory{OutputDirectory: child, err: directory.err}, nil
}

func (directory *closeErrorOutputDirectory) Close() error {
	return errors.Join(directory.OutputDirectory.Close(), directory.err)
}

func (directory *closeErrorOutputDirectory) VerifyChild(ctx context.Context, name string, expected sourcefs.DirectoryIdentity) error {
	return directory.OutputDirectory.VerifyChild(ctx, name, expected)
}

type publicationStageFixture struct {
	preparer PublicationStagePreparer
	repo     *stagePreparationRepository
	guard    persistence.PublicationDeliveryGuard
	holdID   uuid.UUID
	source   string
	output   string
}

func newPublicationStageFixture(t *testing.T) publicationStageFixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sourceRoot, output := filepath.Join(base, "source"), filepath.Join(base, "output")
	if err := os.Mkdir(sourceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(sourceRoot, "album", "track.flac")
	if err := os.Mkdir(filepath.Dir(source), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("audio source bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	mtime := info.ModTime().UTC().Truncate(time.Microsecond)
	holdID := uuid.New()
	input := persistence.PublicationInputs{
		IntentKind: "audio", Format: "source", Audio: &persistence.PublicationAudioInput{
			InventoryRootPath: sourceRoot, RelativePath: filepath.ToSlash(filepath.Join("album", "track.flac")), SizeBytes: info.Size(), MTime: mtime,
		},
	}
	repo := &stagePreparationRepository{input: input, hold: persistence.PublicationSourceHold{
		RelativePath: input.Audio.RelativePath, SizeBytes: input.Audio.SizeBytes, Mtime: input.Audio.MTime,
	}}
	guard := persistence.PublicationDeliveryGuard{OperationID: uuid.New(), ClaimUUID: uuid.New()}
	parent, err := sourcefs.NewOutputOpener().OpenRoot(context.Background(), output)
	if err != nil {
		t.Fatal(err)
	}
	return publicationStageFixture{
		preparer: PublicationStagePreparer{Repository: repo, SourceOpener: sourcefs.NewOpener(), OutputParent: parent, ParentPath: output, HoldUUID: holdID},
		repo:     repo, guard: guard, holdID: holdID, source: source, output: output,
	}
}

func (fixture publicationStageFixture) prepare(t *testing.T) (PreparedPublicationStage, error) {
	t.Helper()
	return fixture.preparer.Prepare(context.Background(), fixture.guard, persistence.PublicationDeliveryAcquired, true)
}

func TestPublicationStagePreparerJournalFailureAndParentMismatchDoNotMutateOutput(t *testing.T) {
	t.Run("journal failure", func(t *testing.T) {
		fixture := newPublicationStageFixture(t)
		t.Cleanup(func() {
			if err := fixture.preparer.OutputParent.Close(); err != nil {
				t.Errorf("close output parent: %v", err)
			}
		})
		fixture.repo.journalErr = errors.New("journal unavailable")
		if _, err := fixture.prepare(t); !errors.Is(err, fixture.repo.journalErr) {
			t.Fatalf("Prepare error = %v, want journal failure", err)
		}
		entries, err := os.ReadDir(fixture.output)
		if err != nil || len(entries) != 0 {
			t.Fatalf("output after journal failure = %v, %v; want empty", entries, err)
		}
	})

	t.Run("mismatched parent", func(t *testing.T) {
		fixture := newPublicationStageFixture(t)
		t.Cleanup(func() {
			if err := fixture.preparer.OutputParent.Close(); err != nil {
				t.Errorf("close output parent: %v", err)
			}
		})
		other := filepath.Join(filepath.Dir(fixture.output), "other")
		if err := os.Mkdir(other, 0o700); err != nil {
			t.Fatal(err)
		}
		fixture.preparer.ParentPath = other
		if _, err := fixture.prepare(t); err == nil {
			t.Fatal("Prepare accepted a path unrelated to the pinned output capability")
		}
		if len(fixture.repo.records) != 0 {
			t.Fatalf("journal records = %d, want none", len(fixture.repo.records))
		}
		for _, path := range []string{fixture.output, other} {
			entries, err := os.ReadDir(path)
			if err != nil || len(entries) != 0 {
				t.Fatalf("path %q changed: entries=%v err=%v", path, entries, err)
			}
		}
	})
}

func TestPublicationStagePreparerDoesNotAdoptExistingDirectory(t *testing.T) {
	fixture := newPublicationStageFixture(t)
	t.Cleanup(func() {
		if err := fixture.preparer.OutputParent.Close(); err != nil {
			t.Errorf("close output parent: %v", err)
		}
	})
	private := fixture.guard.OperationID.String() + "-" + fixture.guard.ClaimUUID.String()
	existing := filepath.Join(fixture.output, private)
	if err := os.Mkdir(existing, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(existing, "do-not-adopt")
	if err := os.WriteFile(marker, []byte("owned elsewhere"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.prepare(t); err == nil {
		t.Fatal("Prepare adopted an existing directory")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("pre-existing marker was changed or removed: %v", err)
	}
	if len(fixture.repo.records) != 1 || fixture.repo.records[0].Step != "intent" {
		t.Fatalf("journal records = %#v, want intent only", fixture.repo.records)
	}
}

func TestPublicationStagePreparerValidatesCopiedInputAndJournalsInOrder(t *testing.T) {
	fixture := newPublicationStageFixture(t)
	t.Cleanup(func() {
		if err := fixture.preparer.OutputParent.Close(); err != nil {
			t.Errorf("close output parent: %v", err)
		}
	})
	stage, err := fixture.prepare(t)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := stage.Close(); err != nil {
			t.Errorf("close prepared stage: %v", err)
		}
	})
	var steps []string
	for _, record := range fixture.repo.records {
		steps = append(steps, record.Step)
	}
	if want := []string{"intent", "directory_created", "input_ready"}; !reflect.DeepEqual(steps, want) {
		t.Fatalf("journal steps = %v, want %v", steps, want)
	}
	parentIdentity, err := fixture.preparer.OutputParent.Identity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	createdIdentity := fixture.repo.records[1].DirectoryIdentity
	if createdIdentity == nil || createdIdentity.Platform != runtime.GOOS || createdIdentity.Platform != parentIdentity.Platform || createdIdentity.Volume != parentIdentity.Volume || createdIdentity.FileID == "" {
		t.Fatalf("journaled directory identity = %#v, want actual %s identity under parent %#v", createdIdentity, runtime.GOOS, parentIdentity)
	}
	if err := stage.Validate(context.Background()); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	inputPath := stage.Paths().InputPath
	if err := os.Rename(inputPath, inputPath+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inputPath, []byte("replacement audio bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := stage.Validate(context.Background()); err == nil || !strings.Contains(err.Error(), "prepared publication input") {
		t.Fatalf("Validate after input replacement = %v, want namespace/facts rejection", err)
	}
}

func TestPublicationStagePreparerRejectsSourceGrowthAndClosesOwnedHandles(t *testing.T) {
	fixture := newPublicationStageFixture(t)
	t.Cleanup(func() {
		if err := fixture.preparer.OutputParent.Close(); err != nil {
			t.Errorf("close output parent: %v", err)
		}
	})
	fixture.repo.onCheck = func(check int) {
		if check == 2 {
			file, err := os.OpenFile(fixture.source, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := file.Write([]byte("growth")); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
	stage, err := fixture.prepare(t)
	if err == nil || stage != nil {
		t.Fatalf("Prepare = (%v, %v), want copy failure and no stage", stage, err)
	}
	if len(fixture.repo.records) != 2 || fixture.repo.records[1].Step != "directory_created" {
		t.Fatalf("journal records = %#v, want no input_ready adoption", fixture.repo.records)
	}
}

func TestPublicationStagePreparerRejectsSourceReplacementDuringCopy(t *testing.T) {
	fixture := newPublicationStageFixture(t)
	t.Cleanup(func() {
		if err := fixture.preparer.OutputParent.Close(); err != nil {
			t.Errorf("close output parent: %v", err)
		}
	})
	fixture.repo.onCheck = func(check int) {
		if check != 2 {
			return
		}
		if err := os.Rename(fixture.source, fixture.source+".old"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fixture.source, []byte("audio source bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stage, err := fixture.prepare(t)
	if err == nil || stage != nil {
		t.Fatalf("Prepare = (%v, %v), want source replacement failure and no stage", stage, err)
	}
	if len(fixture.repo.records) != 2 || fixture.repo.records[1].Step != "directory_created" {
		t.Fatalf("journal records = %#v, want no input_ready adoption", fixture.repo.records)
	}
}

func TestPreparedPublicationStageCloseIsIdempotentAndReturnsCloseErrors(t *testing.T) {
	fixture := newPublicationStageFixture(t)
	outputParent := fixture.preparer.OutputParent
	t.Cleanup(func() {
		if err := outputParent.Close(); err != nil {
			t.Errorf("close output parent: %v", err)
		}
	})
	closeErr := errors.New("close failed")
	fixture.preparer.OutputParent = &closeErrorOutputDirectory{OutputDirectory: fixture.preparer.OutputParent, err: closeErr}
	stage, err := fixture.prepare(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.Close(); !errors.Is(err, closeErr) {
		t.Fatalf("first Close error = %v, want close error", err)
	}
	if err := stage.Close(); err != nil {
		t.Fatalf("second Close error = %v, want nil", err)
	}
}
