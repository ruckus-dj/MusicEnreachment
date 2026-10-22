package service

import (
	"context"
	"os"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestPublicationPreInputCleanupCapabilitySealsScopeUntilConsumed(t *testing.T) {
	scope := newScopeForTest(t, &scopeCommandFake{}, &scopeRepositoryFake{marked: true})
	capability := scope.beginPreInputCleanup()
	if capability == nil || !scope.validPreInputCleanup(capability) {
		t.Fatal("pre-input cleanup capability was not reserved")
	}

	if got := scope.reserveSynchronousWork(); got != nil {
		t.Fatal("synchronous work reservation crossed cleanup capability")
	}
	if got := scope.beginPreInputCleanup(); got != nil {
		t.Fatal("second cleanup capability was granted")
	}
	if got := scope.beginInitialOutputCopy(&publicationSyncReservation{}); got {
		t.Fatal("initial output copy crossed cleanup capability")
	}
	if got := scope.borrowOptionalLyricsWork(nil); got != nil {
		t.Fatal("optional lyrics work crossed cleanup capability")
	}
	if err := scope.Finalize(context.Background(), func() error { return nil }); err == nil {
		t.Fatal("finalize crossed cleanup capability")
	}
	if _, err := scope.Run(context.Background(), "command"); err == nil {
		t.Fatal("command launch crossed cleanup capability")
	}
	file, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := scope.RunFile(context.Background(), "command", nil, scopeFile{file: file}); err == nil {
		t.Fatal("file command launch crossed cleanup capability")
	}

	if !scope.finishPreInputCleanup(capability, false) {
		t.Fatal("cleanup capability could not be consumed")
	}
	if scope.finishPreInputCleanup(capability, false) {
		t.Fatal("cleanup capability was consumed more than once")
	}
	if !scope.CanExecute() {
		t.Fatal("scope remained fenced after non-settling cleanup")
	}
	reservation := scope.reserveSynchronousWork()
	if reservation == nil {
		t.Fatal("scope did not become available after cleanup capability was consumed")
	}
	scope.releaseSynchronousWork(reservation)
}

func TestPublicationInitialOutputCleanupTransfersOnlySoleOriginalReservation(t *testing.T) {
	scope := newScopeForTest(t, &scopeCommandFake{}, &scopeRepositoryFake{marked: true})
	stage := &preparedPublicationStage{guard: scope.guard, preparationRecord: persistence.PublicationPreparationRecord{Step: "input_ready", CopiedInput: &persistence.PublicationFileIdentity{}, DirectoryIdentity: &persistence.PublicationDirectoryIdentity{}}}
	reservation := scope.reserveSynchronousWork()
	if reservation == nil {
		t.Fatal("staging reservation unavailable")
	}
	capability := scope.beginInitialOutputCleanup(stage, reservation)
	if capability == nil {
		t.Fatal("original-owner cleanup capability unavailable")
	}
	if scope.CanExecute() || scope.reserveSynchronousWork() != nil {
		t.Fatal("scope admitted work while output cleanup owned its reservation")
	}
	if !scope.finishInitialOutputCleanup(capability, false) {
		t.Fatal("cleanup capability could not be returned after a failed ACK path")
	}
	if scope.beginInitialOutputCleanup(stage, reservation) != nil {
		t.Fatal("consumed cleanup capability was replayed")
	}
	if !scope.CanExecute() {
		t.Fatal("scope remained fenced after returning the capability")
	}
	scope.releaseSynchronousWork(reservation)
}
