package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestRecoveryStateOnlyClassifiesKnownFilesystemOutcomes(t *testing.T) {
	for _, test := range []struct {
		name  string
		err   error
		state string
		ok    bool
	}{
		{name: "missing", err: os.ErrNotExist, state: "missing", ok: true},
		{name: "permission", err: os.ErrPermission, state: "inaccessible", ok: true},
		{name: "link", err: sourcefs.ErrLink, state: "unsafe", ok: true},
		{name: "not directory", err: sourcefs.ErrNotDirectory, state: "unsafe", ok: true},
		{name: "not regular", err: sourcefs.ErrNotRegular, state: "unsafe", ok: true},
		{name: "invalid path", err: sourcefs.ErrInvalidPath, state: "unsafe", ok: true},
		{name: "I/O", err: syscall.EIO},
		{name: "closed", err: os.ErrClosed},
		{name: "unsupported", err: sourcefs.ErrUnsupported},
	} {
		t.Run(test.name, func(t *testing.T) {
			state, ok := recoveryState(test.err)
			if state != test.state || ok != test.ok {
				t.Fatalf("recoveryState(%v) = (%q, %v), want (%q, %v)", test.err, state, ok, test.state, test.ok)
			}
		})
	}
}

func TestObserveRecoveryPathUsesAnchoredRegularFilesAndReportsMissing(t *testing.T) {
	ctx := context.Background()
	rootPath := t.TempDir()
	rootPath, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(rootPath, "private"), 0o700); err != nil {
		t.Fatal(err)
	}
	contents := []byte("verified input")
	if err := os.WriteFile(filepath.Join(rootPath, "private", "input.bin"), contents, 0o600); err != nil {
		t.Fatal(err)
	}
	opener := sourcefs.NewOpener()
	root, err := opener.OpenRoot(ctx, rootPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	retained := []*retainedRecoveryDir{}
	item, err := observeRecoveryPath(ctx, root, "private/input.bin", &retained)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for i := len(retained) - 1; i >= 0; i-- {
			_ = retained[i].dir.Close()
		}
	}()
	if item.State != "present" || item.Size == nil || *item.Size != int64(len(contents)) || item.SHA256 == "" {
		t.Fatalf("unexpected present-file observation: %#v", item)
	}
	if len(retained) != 1 {
		t.Fatalf("got %d retained directories, want 1", len(retained))
	}
	if _, err := retained[0].dir.Stat(ctx); err != nil {
		t.Fatalf("observation closed a directory transferred to its caller: %v", err)
	}
	missing, err := observeRecoveryPath(ctx, root, "private/missing.bin", &retained)
	if err != nil {
		t.Fatal(err)
	}
	if missing.State != "missing" || missing.SHA256 != "" {
		t.Fatalf("unexpected missing-file observation: %#v", missing)
	}
}

func TestRecoveryFileMatchesStageUsesJournalMicroseconds(t *testing.T) {
	mtime := int64(1_700_000_000_123_456)
	size := int64(12)
	stage := &persistence.PublicationStageObservation{
		PayloadSHA256: "digest",
		Ownership:     persistence.ArtifactOwnership{ExpectedMTime: &mtime, ExpectedSize: &size},
	}
	item := PublicationRecoveryFile{SHA256: "digest", MTimeUnixMicro: &mtime, Size: &size}
	if !recoveryFileMatchesStage(item, stage) {
		t.Fatal("matching journal stage identity was rejected")
	}
}

func TestObserveRecoveryPathRejectsSymlinkParent(t *testing.T) {
	ctx := context.Background()
	rootPath := t.TempDir()
	rootPath, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "input.bin"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(rootPath, "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	root, err := sourcefs.NewOpener().OpenRoot(ctx, rootPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Errorf("close recovery root: %v", err)
		}
	})
	item, err := observeRecoveryPath(ctx, root, "linked/input.bin", &[]*retainedRecoveryDir{})
	if err != nil || item.State != "unsafe" || item.SHA256 != "" {
		t.Fatalf("symlink parent must be observed as unsafe without reading its target: item=%+v err=%v", item, err)
	}
}

func TestObservePublicationRecoveryDoesNotInspectPrivateChildrenOnIdentityMismatch(t *testing.T) {
	ctx := context.Background()
	rootPath := t.TempDir()
	rootPath, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	privatePath := filepath.Join(rootPath, "private")
	if err := os.Mkdir(privatePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(privatePath, "input.bin"), []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	opener := sourcefs.NewOpener()
	root, err := opener.OpenRoot(ctx, rootPath)
	if err != nil {
		if errors.Is(err, sourcefs.ErrUnsupported) {
			t.Skipf("source filesystem root opening unavailable: %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	rootID, err := sourcefsDirectoryIdentifier(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	wrongID := persistence.PublicationDirectoryIdentity{Platform: rootID.Platform, Volume: rootID.Volume, FileID: rootID.FileID + "-wrong"}
	op, claim, epoch, position, generation := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	owner := uuid.New()
	inputSize := int64(len("source"))
	inputMTime := int64(1)
	journal := make([]json.RawMessage, 0, 3)
	for _, record := range []persistence.PublicationPreparationRecord{
		{Version: persistence.PublicationPreparationVersion, Kind: "publication_preparation", Step: "intent", OperationID: op, ClaimUUID: claim, OwnerToken: owner, PrivateDirRel: "private", InputRel: "private/input.bin", OutputRel: "private/output.bin", ParentIdentity: rootID},
		{Version: persistence.PublicationPreparationVersion, Kind: "publication_preparation", Step: "directory_created", OperationID: op, ClaimUUID: claim, OwnerToken: owner, PrivateDirRel: "private", InputRel: "private/input.bin", OutputRel: "private/output.bin", ParentIdentity: rootID, DirectoryIdentity: &wrongID},
		{Version: persistence.PublicationPreparationVersion, Kind: "publication_preparation", Step: "input_ready", OperationID: op, ClaimUUID: claim, OwnerToken: owner, PrivateDirRel: "private", InputRel: "private/input.bin", OutputRel: "private/output.bin", ParentIdentity: rootID, DirectoryIdentity: &wrongID, InputSize: &inputSize, InputMTime: &inputMTime},
	} {
		encoded, marshalErr := json.Marshal(record)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		journal = append(journal, encoded)
	}
	journalBytes, err := json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := persistence.PublicationRecoverySnapshot{
		Attempt:     persistence.TrackPublicationAttempt{OperationID: op, PositionID: position, Epoch: epoch, CandidateGeneration: generation, IntentKind: "audio", Phase: "preparing", MutationJournal: journalBytes},
		Operation:   persistence.PublicationCommitOperationSnapshot{State: "failed", Stage: "preparing"},
		Claim:       persistence.PublicationExecutionClaim{OperationID: op, ApplicationAttempt: 1, RiverJobID: 1, RiverAttempt: 1, ClaimUUID: claim, BootUUID: uuid.New(), DeliveryToken: "delivery", Epoch: epoch, State: "quiescent"},
		OperationID: op, ApplicationAttempt: 1, RiverJobID: 1, CurrentEpoch: epoch, CurrentKnown: true,
	}
	inventory, err := ObservePublicationRecovery(ctx, snapshot, root, rootPath, opener)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Private) != 1 || inventory.Private[0].Path != "private" || inventory.Private[0].State != "present" || inventory.Private[0].ExpectedMatches == nil || *inventory.Private[0].ExpectedMatches {
		t.Fatalf("private mismatch observation = %#v", inventory.Private)
	}
	if inventory.Stage != nil || inventory.Private[0].Path == "private/input.bin" || inventory.Private[0].Path == "private/output.bin" {
		t.Fatalf("inspected a child despite private identity mismatch: %#v", inventory)
	}
}
