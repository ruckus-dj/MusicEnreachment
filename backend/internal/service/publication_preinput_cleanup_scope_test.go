package service

import (
	"context"
	"os"
	"testing"
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
