package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

type stagePreparationRepository struct {
	input          persistence.PublicationInputs
	rewriteValid   bool
	hold           persistence.PublicationSourceHold
	records        []persistence.PublicationPreparationRecord
	checks         int
	journalErr     error
	pendingJournal bool
	onCheck        func(int)
}

func (repository *stagePreparationRepository) CheckPublicationRewriteForDelivery(context.Context, persistence.PublicationDeliveryGuard) (persistence.PublicationRewritePreparationFacts, bool, error) {
	return persistence.PublicationRewritePreparationFacts{Input: repository.input}, repository.rewriteValid, nil
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
	if repository.pendingJournal {
		return persistence.PublicationPreparationStale, nil
	}
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
	lossless := true
	holdID := uuid.New()
	input := persistence.PublicationInputs{
		Version: persistence.PublicationInputsVersion, QualityPolicyVersion: publication.QualityPolicyVersion,
		IntentKind: "audio", InitialLyrics: &persistence.PublicationInitialLyricsInput{ExpectedMetadataRevision: 0}, Format: "source", Audio: &persistence.PublicationAudioInput{
			InventoryRootPath: sourceRoot, RelativePath: filepath.ToSlash(filepath.Join("album", "track.flac")), SizeBytes: info.Size(), MTime: mtime,
			AudioFacts: persistence.PersistedAudioFacts{StreamIndex: 0, Codec: "flac", Lossless: &lossless, Container: "flac", SupportedSourceContainer: true},
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

func makeRewriteFixture(t *testing.T) publicationStageFixture {
	t.Helper()
	fixture := newPublicationStageFixture(t)
	if err := os.Remove(fixture.source); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(fixture.output, "current.mp3")
	contents := []byte("current published MP3 bytes")
	if err := os.WriteFile(current, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(current)
	if err != nil {
		t.Fatal(err)
	}
	size, mtime := info.Size(), info.ModTime().UnixMicro()
	digest := sha256.Sum256(contents)
	lossless := false
	fixture.repo.input = persistence.PublicationInputs{
		Version: persistence.PublicationInputsVersion, QualityPolicyVersion: publication.QualityPolicyVersion,
		IntentKind: "rewrite", Format: "source", Rewrite: &persistence.PublicationRewriteInput{
			CurrentGeneration: uuid.New(), RelativePath: "current.mp3",
			Ownership:  persistence.PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: hex.EncodeToString(digest[:])},
			AudioFacts: persistence.PersistedAudioFacts{StreamIndex: 0, Codec: "mp3", Lossless: &lossless, Container: "mp3", SupportedSourceContainer: true},
		},
	}
	fixture.repo.rewriteValid = true
	fixture.repo.hold = persistence.PublicationSourceHold{}
	fixture.preparer.HoldUUID = uuid.Nil
	return fixture
}

func TestPreparedRewriteStageRejectsSameSizeRestoredMtimeTampering(t *testing.T) {
	fixture := makeRewriteFixture(t)
	t.Cleanup(func() { _ = fixture.preparer.OutputParent.Close() })
	stage, err := fixture.prepare(t)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stage.Close() })
	path := stage.Paths().InputPath
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	contents[0] ^= 0xff
	if err := os.WriteFile(path, contents, info.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := stage.Validate(context.Background()); err == nil {
		t.Fatal("Validate accepted same-size staged input tampering with restored mtime")
	}
}

func TestPublicationStageExtensionUsesVerifiedContainer(t *testing.T) {
	fixture := makeRewriteFixture(t)
	if err := os.Rename(filepath.Join(fixture.output, "current.mp3"), filepath.Join(fixture.output, "current")); err != nil {
		t.Fatal(err)
	}
	fixture.repo.input.Rewrite.RelativePath = "current"
	fixture.repo.input.Rewrite.AudioFacts.Container = "mp3"
	t.Cleanup(func() { _ = fixture.preparer.OutputParent.Close() })
	stage, err := fixture.prepare(t)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stage.Close() })
	if inputExt, outputExt := filepath.Ext(stage.Paths().InputPath), filepath.Ext(stage.Paths().OutputPath); inputExt != ".mp3" || outputExt != ".mp3" {
		t.Fatalf("stage extensions = input %q output %q, want canonical .mp3", inputExt, outputExt)
	}
}

func TestPublicationStagePreparerUsesVerifiedInputContainerForIneligibleSource(t *testing.T) {
	fixture := newPublicationStageFixture(t)
	t.Cleanup(func() { _ = fixture.preparer.OutputParent.Close() })
	lossless := false
	channels := 2
	fixture.repo.input.Format = publication.AudioFormatMKA
	fixture.repo.input.Audio.AudioFacts = persistence.PersistedAudioFacts{
		StreamIndex: 0, Codec: "ac3", Lossless: &lossless, Channels: &channels,
		Container: publication.AudioContainerMKA, SupportedSourceContainer: false,
	}
	stage, err := fixture.prepare(t)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stage.Close() })
	if inputExt, outputExt := filepath.Ext(stage.Paths().InputPath), filepath.Ext(stage.Paths().OutputPath); inputExt != ".mka" || outputExt != ".mka" {
		t.Fatalf("stage extensions = input %q output %q, want .mka/.mka", inputExt, outputExt)
	}
}

func TestPublicationStagePreparerKeepsMKAInputForM4APlan(t *testing.T) {
	fixture := newPublicationStageFixture(t)
	t.Cleanup(func() { _ = fixture.preparer.OutputParent.Close() })
	lossless := false
	channels := 2
	fixture.repo.input.Format = publication.AudioFormatM4A
	fixture.repo.input.Audio.AudioFacts = persistence.PersistedAudioFacts{
		StreamIndex: 0, Codec: "ac3", Lossless: &lossless, Channels: &channels,
		Container: publication.AudioContainerMKA, SupportedSourceContainer: false,
	}
	stage, err := fixture.prepare(t)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stage.Close() })
	if inputExt, outputExt := filepath.Ext(stage.Paths().InputPath), filepath.Ext(stage.Paths().OutputPath); inputExt != ".mka" || outputExt != ".m4a" {
		t.Fatalf("stage extensions = input %q output %q, want .mka/.m4a", inputExt, outputExt)
	}
}

func TestPublicationStagePreparerRejectsUnknownInputContainerBeforeMutation(t *testing.T) {
	fixture := newPublicationStageFixture(t)
	t.Cleanup(func() { _ = fixture.preparer.OutputParent.Close() })
	fixture.repo.input.Format = publication.AudioFormatMKA
	fixture.repo.input.Audio.AudioFacts.Container = "unknown"
	if _, err := fixture.prepare(t); err == nil {
		t.Fatal("Prepare accepted an unknown input container")
	}
	if len(fixture.repo.records) != 0 {
		t.Fatalf("preparation records = %d, want no mutations before container validation", len(fixture.repo.records))
	}
	entries, err := os.ReadDir(fixture.output)
	if err != nil || len(entries) != 0 {
		t.Fatalf("output after invalid container = %v, %v; want empty", entries, err)
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
	ready := fixture.repo.records[2]
	if ready.InputSize == nil || *ready.InputSize != fixture.repo.input.Audio.SizeBytes || ready.InputMTime == nil || *ready.InputMTime != fixture.repo.input.Audio.MTime.UnixMicro() {
		t.Fatalf("input_ready source facts = size %v mtime %v, want size %d mtime %d", ready.InputSize, ready.InputMTime, fixture.repo.input.Audio.SizeBytes, fixture.repo.input.Audio.MTime.UnixMicro())
	}
	if ready.CopiedInput == nil || ready.CopiedInput.SizeBytes == nil || ready.CopiedInput.MTime == nil {
		t.Fatalf("input_ready copied input facts = %#v, want copied size, mtime, and digest", ready.CopiedInput)
	}
	sourceBytes, err := os.ReadFile(fixture.source)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(sourceBytes)
	if ready.CopiedInput.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("copied input SHA-256 = %q, want digest of copied bytes %q", ready.CopiedInput.SHA256, hex.EncodeToString(digest[:]))
	}
	inputInfo, err := os.Stat(stage.Paths().InputPath)
	if err != nil {
		t.Fatal(err)
	}
	if *ready.CopiedInput.SizeBytes != inputInfo.Size() || *ready.CopiedInput.MTime != inputInfo.ModTime().UnixMicro() {
		t.Fatalf("copied input facts = size %d mtime %d, want staged file size %d mtime %d", *ready.CopiedInput.SizeBytes, *ready.CopiedInput.MTime, inputInfo.Size(), inputInfo.ModTime().UnixMicro())
	}
	parentIdentity, err := fixture.preparer.OutputParent.Identity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	createdIdentity := fixture.repo.records[1].DirectoryIdentity
	if createdIdentity == nil || createdIdentity.Platform != runtime.GOOS || createdIdentity.Platform != parentIdentity.Platform || createdIdentity.Volume != parentIdentity.Volume || createdIdentity.FileID == "" {
		t.Fatalf("journaled directory identity = %#v, want actual %s identity under parent %#v", createdIdentity, runtime.GOOS, parentIdentity)
	}
	paths := stage.Paths()
	wantOutputRel := filepath.ToSlash(filepath.Join(fixture.repo.records[0].PrivateDirRel, "output.flac"))
	if paths.RelativePath != fixture.repo.records[0].OutputRel || paths.RelativePath != wantOutputRel || paths.RelativePath == fixture.repo.input.Audio.RelativePath {
		t.Fatalf("audio stage relative path = %q, journal output = %q, want canonical private output %q distinct from source %q", paths.RelativePath, fixture.repo.records[0].OutputRel, wantOutputRel, fixture.repo.input.Audio.RelativePath)
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

func TestPublicationStagePreparerStagesCurrentRewriteArtifact(t *testing.T) {
	fixture := makeRewriteFixture(t)
	t.Cleanup(func() { _ = fixture.preparer.OutputParent.Close() })
	stage, err := fixture.prepare(t)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stage.Close() })
	paths := stage.Paths()
	wantOutputRel := filepath.ToSlash(filepath.Join(fixture.repo.records[0].PrivateDirRel, "output.mp3"))
	if filepath.Ext(paths.InputPath) != ".mp3" || paths.RelativePath != fixture.repo.records[0].OutputRel || paths.RelativePath != wantOutputRel || paths.RelativePath == fixture.repo.input.Rewrite.RelativePath {
		t.Fatalf("rewrite stage paths = %#v", stage.Paths())
	}
	if err := stage.Validate(context.Background()); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestPreparedRewriteStageRejectsRenamedCurrentArtifact(t *testing.T) {
	fixture := makeRewriteFixture(t)
	t.Cleanup(func() { _ = fixture.preparer.OutputParent.Close() })
	stage, err := fixture.prepare(t)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stage.Close() })
	if err := os.Rename(filepath.Join(fixture.output, "current.mp3"), filepath.Join(fixture.output, "renamed.mp3")); err != nil {
		t.Fatal(err)
	}
	if err := stage.Validate(context.Background()); err == nil {
		t.Fatal("Validate accepted renamed current publication")
	}
}

func TestPublicationStagePreparerRejectsRewriteEvidenceBeforeJournal(t *testing.T) {
	for _, test := range []struct {
		name  string
		alter func(*persistence.PublicationRewriteInput)
	}{
		{name: "wrong size", alter: func(input *persistence.PublicationRewriteInput) { n := int64(1); input.Ownership.SizeBytes = &n }},
		{name: "wrong digest", alter: func(input *persistence.PublicationRewriteInput) { input.Ownership.SHA256 = strings.Repeat("0", 64) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := makeRewriteFixture(t)
			t.Cleanup(func() { _ = fixture.preparer.OutputParent.Close() })
			test.alter(fixture.repo.input.Rewrite)
			if _, err := fixture.prepare(t); err == nil {
				t.Fatal("Prepare accepted mismatched current artifact facts")
			}
			entries, err := os.ReadDir(fixture.output)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 || len(fixture.repo.records) != 0 {
				t.Fatalf("entries=%v journal=%v, want only pre-existing current artifact", entries, fixture.repo.records)
			}
		})
	}
}

func TestPublicationStagePreparerRejectsStaleRewriteBeforeFilesystemWrite(t *testing.T) {
	fixture := makeRewriteFixture(t)
	t.Cleanup(func() { _ = fixture.preparer.OutputParent.Close() })
	fixture.repo.rewriteValid = false
	if _, err := fixture.prepare(t); err == nil {
		t.Fatal("Prepare accepted stale rewrite guard")
	}
	entries, err := os.ReadDir(fixture.output)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || len(fixture.repo.records) != 0 {
		t.Fatalf("entries=%v journal=%v, want no filesystem/journal mutation", entries, fixture.repo.records)
	}
}

func TestPublicationStagePreparerDoesNotAdoptRewriteJournal(t *testing.T) {
	fixture := makeRewriteFixture(t)
	t.Cleanup(func() { _ = fixture.preparer.OutputParent.Close() })
	fixture.repo.pendingJournal = true
	if _, err := fixture.prepare(t); err == nil || !strings.Contains(err.Error(), "recovery required") {
		t.Fatalf("Prepare error=%v, want pending-journal refusal", err)
	}
	entries, err := os.ReadDir(fixture.output)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || len(fixture.repo.records) != 0 {
		t.Fatalf("entries=%v, want only original current artifact", entries)
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
