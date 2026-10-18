package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type candidateRebuildTestRepo struct {
	records  []persistence.PublicationCandidateRebuildRecord
	onRecord func(persistence.PublicationCandidateRebuildRecord) error
}

func (*candidateRebuildTestRepo) PublicationPreparationInputs(context.Context, persistence.PublicationDeliveryGuard) (persistence.PublicationInputs, bool, error) {
	return persistence.PublicationInputs{}, false, nil
}
func (*candidateRebuildTestRepo) CheckPublicationRewriteForDelivery(context.Context, persistence.PublicationDeliveryGuard) (persistence.PublicationRewritePreparationFacts, bool, error) {
	return persistence.PublicationRewritePreparationFacts{}, false, nil
}
func (repo *candidateRebuildTestRepo) CheckPublicationSourceHoldForDelivery(context.Context, persistence.PublicationDeliveryGuard, uuid.UUID) (persistence.PublicationSourceHold, bool, error) {
	return persistence.PublicationSourceHold{}, true, nil
}
func (*candidateRebuildTestRepo) RecordPublicationPreparation(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationPreparationRecord) (persistence.PublicationPreparationOutcome, error) {
	return persistence.PublicationPreparationRecorded, nil
}
func (repo *candidateRebuildTestRepo) RecordPublicationCandidateRebuild(_ context.Context, _ persistence.PublicationDeliveryGuard, record persistence.PublicationCandidateRebuildRecord) error {
	if repo.onRecord != nil {
		if err := repo.onRecord(record); err != nil {
			return err
		}
	}
	repo.records = append(repo.records, record)
	return nil
}

// observedRebuildDirectory forwards the native capability set while recording
// successful namespace mutations. Embedding OutputDirectory alone would hide
// optional capabilities and accidentally test a fallback path instead.
type observedRebuildDirectory struct {
	sourcefs.OutputDirectory
	trace          *[]string
	outputName     string
	expectedParent sourcefs.DirectoryIdentity
}

func (d *observedRebuildDirectory) OpenExistingDir(ctx context.Context, name string) (sourcefs.OutputDirectory, error) {
	child, err := d.OutputDirectory.(sourcefs.OutputDirectoryTraversal).OpenExistingDir(ctx, name)
	if err != nil {
		return nil, err
	}
	return &observedRebuildDirectory{OutputDirectory: child, trace: d.trace, outputName: d.outputName, expectedParent: d.expectedParent}, nil
}
func (d *observedRebuildDirectory) RenameNoReplace(ctx context.Context, from sourcefs.OutputDirectory, fromName string, to sourcefs.OutputDirectory, toName string) error {
	return d.OutputDirectory.(sourcefs.OutputMutation).RenameNoReplace(ctx, unwrapRebuildDirectory(from), fromName, unwrapRebuildDirectory(to), toName)
}
func unwrapRebuildDirectory(dir sourcefs.OutputDirectory) sourcefs.OutputDirectory {
	if wrapped, ok := dir.(*observedRebuildDirectory); ok {
		return wrapped.OutputDirectory
	}
	return dir
}
func (d *observedRebuildDirectory) OpenExistingRegular(ctx context.Context, name string) (sourcefs.RegularFile, error) {
	return d.OutputDirectory.(sourcefs.OutputMutation).OpenExistingRegular(ctx, name)
}
func (d *observedRebuildDirectory) RemoveOwnedRegular(ctx context.Context, name string, parent sourcefs.DirectoryIdentity, expected fs.FileInfo) error {
	if name != d.outputName || parent != d.expectedParent || expected == nil || !expected.Mode().IsRegular() {
		return errors.New("native removal did not receive the expected output name, parent, and regular-file facts")
	}
	if err := d.OutputDirectory.(sourcefs.OutputCleanup).RemoveOwnedRegular(ctx, name, parent, expected); err != nil {
		return err
	}
	*d.trace = append(*d.trace, "native-remove-success")
	return nil
}
func (d *observedRebuildDirectory) RemoveEmptyChildDir(ctx context.Context, name string, parent, child sourcefs.DirectoryIdentity) error {
	return d.OutputDirectory.(sourcefs.OutputCleanup).RemoveEmptyChildDir(ctx, name, parent, child)
}
func (d *observedRebuildDirectory) CreateExclusive(ctx context.Context, name string) (sourcefs.OutputFile, error) {
	if name != d.outputName {
		return nil, errors.New("native exclusive creation did not receive the expected output name")
	}
	file, err := d.OutputDirectory.CreateExclusive(ctx, name)
	if err == nil {
		*d.trace = append(*d.trace, "native-exclusive-create-success")
	}
	return file, err
}
func (d *observedRebuildDirectory) SyncDirectory(ctx context.Context) error {
	return d.OutputDirectory.(sourcefs.OutputDirectorySync).SyncDirectory(ctx)
}
func (d *observedRebuildDirectory) AtomicReplaceExisting(ctx context.Context, from sourcefs.OutputDirectory, fromName string, fromInfo fs.FileInfo, toName string, toInfo fs.FileInfo) error {
	return d.OutputDirectory.(sourcefs.OutputAtomicReplace).AtomicReplaceExisting(ctx, unwrapRebuildDirectory(from), fromName, fromInfo, toName, toInfo)
}

func TestRebuildPublicationOutputCopiesPreparedInputNatively(t *testing.T) {
	ctx := context.Background()
	rootPath := t.TempDir()
	rootPath, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	privateName := "private"
	if err := os.Mkdir(filepath.Join(rootPath, privateName), 0o700); err != nil {
		t.Fatal(err)
	}
	const body = "prepared-original-bytes"
	if err := os.WriteFile(filepath.Join(rootPath, "source.audio"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootPath, privateName, "input.audio"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	outputOpener := sourcefs.NewOutputOpener()
	parent, err := outputOpener.OpenRoot(ctx, rootPath)
	if err != nil {
		t.Fatal(err)
	}
	opener := sourcefs.NewOpener()
	defer func() {
		if err := parent.Close(); err != nil {
			t.Errorf("close output root: %v", err)
		}
	}()
	directory, err := parent.(sourcefs.OutputDirectoryTraversal).OpenExistingDir(ctx, privateName)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := directory.Close(); err != nil {
			t.Errorf("close output directory: %v", err)
		}
	}()
	identity, err := directory.Identity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sourceInfo, err := os.Stat(filepath.Join(rootPath, "source.audio"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(rootPath, privateName, "input.audio"), sourceInfo.ModTime(), sourceInfo.ModTime()); err != nil {
		t.Fatal(err)
	}
	mutation := directory.(sourcefs.OutputMutation)
	inputFile, err := mutation.OpenExistingRegular(ctx, "input.audio")
	if err != nil {
		t.Fatal(err)
	}
	inputInfo, err := inputFile.Stat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := inputFile.Close(); err != nil {
		t.Fatal(err)
	}
	const partialCandidate = "partial-candidate"
	if err := os.WriteFile(filepath.Join(rootPath, privateName, "output.audio"), []byte(partialCandidate), 0o600); err != nil {
		t.Fatal(err)
	}
	oldCandidate, err := mutation.OpenExistingRegular(ctx, "output.audio")
	if err != nil {
		t.Fatal(err)
	}
	oldCandidateInfo, err := oldCandidate.Stat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := oldCandidate.Close(); err != nil {
		t.Fatal(err)
	}
	guard := persistence.PublicationDeliveryGuard{OperationID: uuid.New(), ApplicationAttempt: 1, RiverJobID: 1, RiverAttempt: 1, ClaimUUID: uuid.New(), BootUUID: uuid.New(), DeliveryToken: "delivery", Epoch: uuid.New(), CandidateGeneration: uuid.New(), ExpectedPositionID: uuid.New()}
	repo := &candidateRebuildTestRepo{}
	scope := &PublicationExecutionScope{guard: guard, commands: PublicationCommandRunner(nil)}
	reservation := &publicationSyncReservation{owner: scope, active: true}
	scope.syncWork = 1
	borrow := scope.borrowOptionalLyricsWork(reservation)
	if borrow == nil {
		t.Fatal("could not borrow synchronous reservation")
	}
	defer scope.releaseOptionalLyricsWork(borrow)
	stage := &preparedPublicationStage{paths: PublicationStagePaths{InputPath: filepath.Join(rootPath, privateName, "input.audio"), OutputPath: filepath.Join(rootPath, privateName, "output.audio"), RelativePath: privateName + "/output.audio"}, parent: parent, directory: directory, directoryName: privateName, identity: persistence.PublicationDirectoryIdentity{Platform: identity.Platform, Volume: identity.Volume, FileID: identity.FileID}, parentIdentity: identity, repository: repo, opener: opener, sourceRootPath: rootPath, sourceRelativePath: "source.audio", sourceSize: sourceInfo.Size(), sourceMTime: sourceInfo.ModTime(), sourceFileInfo: inputInfo, inputPath: filepath.Join(rootPath, privateName), inputName: "input.audio", guard: guard, hold: uuid.New()}
	if err := stage.rebuildOutputCopy(ctx, scope, borrow); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(stage.paths.InputPath)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := os.ReadFile(stage.paths.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(original) != body || string(candidate) != body {
		t.Fatalf("original/candidate bytes = %q/%q", original, candidate)
	}
	outputFile, err := mutation.OpenExistingRegular(ctx, "output.audio")
	if err != nil {
		t.Fatal(err)
	}
	candidateInfo, err := outputFile.Stat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := outputFile.Close(); err != nil {
		t.Fatal(err)
	}
	inputFile, err = mutation.OpenExistingRegular(ctx, "input.audio")
	if err != nil {
		t.Fatal(err)
	}
	originalInfo, err := inputFile.Stat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := inputFile.Close(); err != nil {
		t.Fatal(err)
	}
	if os.SameFile(originalInfo, candidateInfo) {
		t.Fatal("candidate aliases prepared input")
	}
	if len(repo.records) != 2 || repo.records[0].Step != "intent" || repo.records[1].Step != "complete" {
		t.Fatalf("journal records = %#v", repo.records)
	}
	partialDigest := sha256.Sum256([]byte(partialCandidate))
	bodyDigest := sha256.Sum256([]byte(body))
	assertCandidateFact := func(record persistence.PublicationCandidateRebuildRecord, info os.FileInfo, digest [sha256.Size]byte) {
		t.Helper()
		fact := record.Candidate
		if fact == nil || fact.SizeBytes == nil || *fact.SizeBytes != info.Size() || fact.MTime == nil || *fact.MTime != info.ModTime().UnixMicro() || fact.SHA256 != hex.EncodeToString(digest[:]) {
			t.Fatalf("journal candidate identity = %#v, want native handle facts for size %d", fact, info.Size())
		}
	}
	assertCandidateFact(repo.records[0], oldCandidateInfo, partialDigest)
	assertCandidateFact(repo.records[1], candidateInfo, bodyDigest)
}
