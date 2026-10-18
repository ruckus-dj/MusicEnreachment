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
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type replacementRecordingRepository struct {
	steps    []string
	evidence []persistence.PublicationReplacementEvidence
	err      error
}

type replacementRegularFileWithoutSync struct {
	sourcefs.RegularFile
}

func TestSyncManagedRegularFileRequiresOptionalCapability(t *testing.T) {
	err := syncManagedRegularFile(context.Background(), replacementRegularFileWithoutSync{})
	if !errors.Is(err, sourcefs.ErrUnsupported) {
		t.Fatalf("syncManagedRegularFile error = %v, want ErrUnsupported", err)
	}
}

func (r *replacementRecordingRepository) RecordPublicationReplacement(_ context.Context, _ persistence.PublicationDeliveryGuard, evidence persistence.PublicationReplacementEvidence) (persistence.PublicationReplacementOutcome, error) {
	r.steps = append(r.steps, evidence.To)
	r.evidence = append(r.evidence, evidence)
	if r.err != nil {
		return persistence.PublicationReplacementStale, r.err
	}
	return persistence.PublicationReplacementRecorded, nil
}

func TestManagedTargetReplacerNativeReplacement(t *testing.T) {
	fixture := makeRewriteFixture(t)
	output := fixture.output
	parent := fixture.preparer.OutputParent
	t.Cleanup(func() { _ = parent.Close() })
	prepared, err := fixture.prepare(t)
	if err != nil {
		t.Fatal(err)
	}
	stage := prepared.(*preparedPublicationStage)
	t.Cleanup(func() { _ = stage.Close() })
	paths := stage.Paths()
	candidate := []byte("new encoded audio payload")
	if err := os.WriteFile(paths.OutputPath, candidate, 0600); err != nil {
		t.Fatal(err)
	}
	// The output's mtime is captured by validation, which also proves stage ownership.
	if err := stage.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(output, "current.mp3")
	oldInfo, err := os.Stat(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	stage.rewriteIdentity = persistence.PublicationFileIdentity{SizeBytes: replacementInt64Pointer(oldInfo.Size()), MTime: replacementInt64Pointer(oldInfo.ModTime().UnixMicro()), SHA256: fixture.repo.input.Rewrite.Ownership.SHA256}
	root := parent
	stageDir, err := parent.OpenOrCreateDir(context.Background(), stage.directoryName)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stageDir.Close() })
	targetDir := parent
	rootID, err := root.Identity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	stage.guard = fixture.guard
	stage.hold = fixture.holdID
	stageInfo, err := os.Stat(paths.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	size, mtime := stageInfo.Size(), stageInfo.ModTime().UnixMicro()
	digest := sha256.Sum256(candidate)
	validated := persistence.PublicationStageObservation{Version: 1, RelativePath: filepath.ToSlash(paths.RelativePath), PayloadSHA256: hex.EncodeToString(digest[:]), Ownership: persistence.ArtifactOwnership{ExpectedSize: &size, ExpectedMTime: &mtime}}
	scope := &PublicationExecutionScope{guard: fixture.guard, commands: &scopeCommandFake{}, repository: &scopeRepositoryFake{}}
	repo := &replacementRecordingRepository{}
	generation := uuid.New()
	intent := persistence.PublicationExposureIntent{PrepOwner: stage.hold, Expectation: "expected_current", StagedPath: filepath.ToSlash(paths.RelativePath), TargetRelativePath: filepath.ToSlash(fixture.repo.input.Rewrite.RelativePath), OutputRootParent: persistence.PublicationDirectoryIdentity{Platform: rootID.Platform, Volume: rootID.Volume, FileID: rootID.FileID}, TargetParent: persistence.PublicationDirectoryIdentity{Platform: rootID.Platform, Volume: rootID.Volume, FileID: rootID.FileID}}
	replacer := &newPublicationManagedTargetReplacer{scope: scope, repository: repo, guard: fixture.guard, intent: intent, validated: validated, expectedGeneration: &generation, stage: stage, root: root, stageDir: stageDir, targetDir: targetDir, stageName: stage.directoryName, targetName: "current.mp3"}
	err = replacer.Replace(context.Background())
	if runtime.GOOS == "windows" {
		if !errors.Is(err, sourcefs.ErrUnsupported) {
			t.Fatalf("Replace error = %v, want ErrUnsupported", err)
		}
		got, readErr := os.ReadFile(oldPath)
		if readErr != nil || string(got) != "current published MP3 bytes" {
			t.Fatalf("original target bytes = %q, err=%v", got, readErr)
		}
		if len(repo.steps) != 0 {
			t.Fatalf("journal steps before unsupported sync = %v, want none", repo.steps)
		}
		if _, statErr := os.Stat(paths.OutputPath); statErr != nil {
			t.Fatalf("candidate missing after unsupported sync: %v", statErr)
		}
		return
	}
	if err == nil || err.Error() != "managed publication replacement is installed and awaits phase-two completion" {
		t.Fatalf("Replace error = %v", err)
	}
	got, err := os.ReadFile(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, candidate) {
		t.Fatalf("installed bytes = %q", got)
	}
	if _, err := os.Stat(paths.OutputPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("candidate still exists: %v", err)
	}
	if got, err := os.ReadFile(paths.InputPath); err != nil || string(got) != "current published MP3 bytes" {
		t.Fatalf("backup bytes = %q, err=%v", got, err)
	}
	if want := []string{"backup_intent", "backup_ready", "replacement_intent", "installed"}; !reflect.DeepEqual(repo.steps, want) {
		t.Fatalf("journal sequence = %v, want %v", repo.steps, want)
	}
	backup := repo.evidence[0]
	if backup.BackupPath != filepath.ToSlash(filepath.Join(stage.directoryName, stage.inputName)) || backup.BackupIdentity.SizeBytes == nil || backup.BackupIdentity.MTime == nil || backup.BackupIdentity.SHA256 == "" {
		t.Fatalf("backup identity/path evidence = %+v", backup)
	}
	backupInfo, err := os.Stat(paths.InputPath)
	if err != nil {
		t.Fatal(err)
	}
	if *backup.BackupIdentity.SizeBytes != backupInfo.Size() || *backup.BackupIdentity.MTime != backupInfo.ModTime().UnixMicro() {
		t.Fatalf("backup facts = %+v, want copied input facts size=%d mtime=%d", backup.BackupIdentity, backupInfo.Size(), backupInfo.ModTime().UnixMicro())
	}
	installed := repo.evidence[len(repo.evidence)-1].Identity
	if installed.SHA256 != validated.PayloadSHA256 || installed.SHA256 == backup.BackupIdentity.SHA256 {
		t.Fatalf("installed candidate identity = %+v, backup identity = %+v", installed, backup.BackupIdentity)
	}
	if !scope.unknown || scope.finalized {
		t.Fatalf("scope state unknown=%v finalized=%v; installed phase-one result must retain locks", scope.unknown, scope.finalized)
	}
}

func TestVerifyManagedTargetParentUsesReadOnlyTraversal(t *testing.T) {
	ctx := context.Background()
	rootPath := t.TempDir()
	rootPath, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(rootPath, "existing"), 0700); err != nil {
		t.Fatal(err)
	}
	root, err := sourcefs.NewOutputOpener().OpenRoot(ctx, rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()

	traversal, ok := root.(sourcefs.OutputDirectoryTraversal)
	if !ok {
		t.Fatal("native output directory lacks read-only traversal capability")
	}
	nested, err := traversal.OpenExistingDir(ctx, "existing")
	if err != nil {
		t.Fatal(err)
	}
	expected, err := nested.Identity(ctx)
	_ = nested.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyManagedTargetParent(ctx, root, "existing", expected); err != nil {
		t.Fatalf("verify existing target parent: %v", err)
	}

	if err := verifyManagedTargetParent(ctx, root, "missing/intermediate", expected); err == nil {
		t.Fatal("expected missing target parent to fail verification")
	}
	if _, err := os.Lstat(filepath.Join(rootPath, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("verification created missing parent: lstat error = %v", err)
	}
}
