package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type candidateRebuildTestRepo struct {
	records []persistence.PublicationCandidateRebuildRecord
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
	repo.records = append(repo.records, record)
	return nil
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
