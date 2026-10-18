package service

import (
	"context"
	"os"
	"testing"
)

func TestSealPrivateManualLyricsReplacementTransfersExactInstalledWork(t *testing.T) {
	scope, reservation, stage, initialProof, _, _ := privateWorkFixture()
	work := scope.acquirePrivateManualLyricsWork(reservation, stage, initialProof)
	if work == nil {
		t.Fatal("private manual lyrics work was not acquired")
	}
	installed := scope.recordPrivateManualLyricsReplacementInstalled(work)
	if installed == nil {
		t.Fatal("installed acknowledgement was not recorded")
	}

	clonedWork := *work
	if token, err := scope.sealPrivateManualLyricsReplacement(&clonedWork, installed); err == nil || token != nil {
		t.Fatal("copied work capability was accepted")
	}
	foreignScope, foreignReservation, foreignStage, foreignInitialProof, _, _ := privateWorkFixture()
	foreignWork := foreignScope.acquirePrivateManualLyricsWork(foreignReservation, foreignStage, foreignInitialProof)
	if token, err := scope.sealPrivateManualLyricsReplacement(foreignWork, installed); err == nil || token != nil {
		t.Fatal("foreign work capability was accepted")
	}
	if scope.privateManualLyricsWork != work || !reservation.active || !reservation.borrowed || scope.syncWork != 1 {
		t.Fatal("rejected transfer disturbed the authentic private work")
	}

	token, err := scope.sealPrivateManualLyricsReplacement(work, installed)
	if err != nil || token == nil || !token.active {
		t.Fatalf("authentic installed work did not transfer: token=%v err=%v", token, err)
	}
	if scope.privateManualLyricsWork != nil || scope.optionalLyrics != nil || scope.privateLyricsInstalled != nil ||
		reservation.active || reservation.borrowed || reservation.borrow != nil || scope.syncWork != 0 ||
		scope.replacement != token || !scope.finalizing || scope.finalized {
		t.Fatal("private work and completion fence were not transferred atomically")
	}
	if scope.CanExecute() {
		t.Fatal("commands remained available after replacement completion was sealed")
	}
	released := false
	scope.release = func() { released = true }
	if err := scope.Finalize(context.Background(), func() error { return nil }); err == nil {
		t.Fatal("legacy finalization accepted an active replacement completion")
	}
	if released {
		t.Fatal("scope released local ownership before completion acknowledgement")
	}
	if err := scope.acknowledgeReplacementCompletion(token); err != nil {
		t.Fatal(err)
	}
	if !released || !scope.finalized || token.active {
		t.Fatal("replacement acknowledgement did not release and finalize the scope")
	}
}

func TestSealPrivateManualLyricsReplacementRequiresRecordedInstalledProof(t *testing.T) {
	scope, reservation, stage, initialProof, _, _ := privateWorkFixture()
	work := scope.acquirePrivateManualLyricsWork(reservation, stage, initialProof)
	if work == nil {
		t.Fatal("private manual lyrics work was not acquired")
	}
	if token, err := scope.sealPrivateManualLyricsReplacement(work, nil); err == nil || token != nil {
		t.Fatal("missing installed proof was accepted")
	}
	if !scope.validPrivateManualLyricsWork(work, stage) || scope.syncWork != 1 || !reservation.active {
		t.Fatal("rejected transfer disturbed private work")
	}
}

func TestInstalledPrivateLyricsWorkCannotUseOrdinaryFinish(t *testing.T) {
	scope, reservation, stage, initialProof, _, repository := privateWorkFixture()
	released := false
	scope.release = func() { released = true }
	work := scope.acquirePrivateManualLyricsWork(reservation, stage, initialProof)
	if work == nil {
		t.Fatal("private manual lyrics work was not acquired")
	}
	installed := scope.recordPrivateManualLyricsReplacementInstalled(work)
	if installed == nil {
		t.Fatal("installed acknowledgement was not recorded")
	}
	if scope.finishPrivateManualLyricsWork(work, true) {
		t.Fatal("ordinary finish consumed installed private work")
	}
	scope.releaseSynchronousWork(reservation)
	if !scope.validPrivateManualLyricsWork(work, stage) || !reservation.active || !reservation.borrowed || scope.syncWork != 1 {
		t.Fatal("ordinary finish or release dropped the installed work reservation")
	}
	if _, err := scope.Run(context.Background(), "tool"); err == nil {
		t.Fatal("Run launched while installed private work awaited completion fencing")
	}
	input, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := input.Close(); err != nil {
			t.Errorf("close test input: %v", err)
		}
	}()
	if _, err := scope.RunFile(context.Background(), "tool", nil, scopeFile{file: input}); err == nil {
		t.Fatal("RunFile launched while installed private work awaited completion fencing")
	}
	if err := scope.Finalize(context.Background(), func() error { return nil }); err == nil {
		t.Fatal("legacy finalization accepted installed private work")
	}
	if released || repository.marks != 0 {
		t.Fatalf("scope ownership was released before replacement settlement: released=%v marks=%d", released, repository.marks)
	}
	token, err := scope.sealPrivateManualLyricsReplacement(work, installed)
	if err != nil || token == nil {
		t.Fatalf("authentic completion seal failed after rejected ordinary finish: token=%v err=%v", token, err)
	}
}
