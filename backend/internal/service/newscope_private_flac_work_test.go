package service

import (
	"context"
	"os"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func privateWorkFixture() (*PublicationExecutionScope, *publicationSyncReservation, *preparedPublicationStage, *publicationInitialOutputCopy, *scopeFileCommandFake, *scopeRepositoryFake) {
	commands := &scopeFileCommandFake{result: PublicationCommandResult{QuiescenceEstablished: true}}
	repository := &scopeRepositoryFake{marked: true}
	guard := persistence.PublicationDeliveryGuard{DeliveryToken: "private-work"}
	scope := &PublicationExecutionScope{guard: guard, commands: commands, repository: repository, release: func() {}}
	reservation := &publicationSyncReservation{owner: scope, active: true}
	scope.syncWork = 1
	stage := &preparedPublicationStage{guard: guard, privatePrepMode: true, rewrite: true}
	proof := &publicationInitialOutputCopy{stage: stage}
	return scope, reservation, stage, proof, commands, repository
}

func TestPrivateManualLyricsWorkExclusivelyOwnsScope(t *testing.T) {
	scope, reservation, stage, proof, commands, repository := privateWorkFixture()
	work := scope.acquirePrivateManualLyricsWork(reservation, stage, proof)
	if work == nil || !scope.validPrivateManualLyricsWork(work, stage) {
		t.Fatal("private work capability was not issued")
	}
	if scope.acquirePrivateManualLyricsWork(reservation, stage, proof) != nil {
		t.Fatal("a competing private work borrow was accepted")
	}
	if scope.borrowOptionalLyricsWork(reservation) != nil {
		t.Fatal("a competing optional lyrics borrow was accepted")
	}
	if scope.beginInitialOutputCopy(reservation) {
		t.Fatal("initial output copy started during private work")
	}
	if _, err := scope.Run(context.Background(), "tool"); err == nil {
		t.Fatal("Run launched during private work")
	}
	inputFile, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := inputFile.Close(); err != nil {
			t.Errorf("close private-work test input: %v", err)
		}
	})
	if _, err := scope.RunFile(context.Background(), "tool", nil, scopeFile{file: inputFile}); err == nil {
		t.Fatal("RunFile launched during private work")
	}
	if commands.calls != 0 {
		t.Fatalf("command backend called %d times", commands.calls)
	}
	scope.releaseSynchronousWork(reservation)
	if !reservation.active || !reservation.borrowed || scope.syncWork != 1 {
		t.Fatalf("borrowed reservation lost its active pin: active=%v borrowed=%v syncWork=%d", reservation.active, reservation.borrowed, scope.syncWork)
	}
	if err := scope.Finalize(context.Background(), func() error { return nil }); err == nil {
		t.Fatal("scope finalized during private work")
	}

	cloned := *work
	if scope.validPrivateManualLyricsWork(&cloned, stage) || scope.finishPrivateManualLyricsWork(&cloned, true) {
		t.Fatal("copied capability was accepted")
	}
	if !scope.validPrivateManualLyricsWork(work, stage) {
		t.Fatal("forged capability disturbed authentic work")
	}
	if scope.validPrivateManualLyricsWork(work, &preparedPublicationStage{guard: scope.guard, privatePrepMode: true, rewrite: true}) {
		t.Fatal("foreign stage was accepted")
	}
	if scope.validPrivateManualLyricsWork(work, &preparedPublicationStage{guard: persistence.PublicationDeliveryGuard{DeliveryToken: "foreign"}, privatePrepMode: true, rewrite: true}) {
		t.Fatal("foreign proof stage was accepted")
	}
	if !scope.finishPrivateManualLyricsWork(work, true) {
		t.Fatal("authentic work was not released")
	}
	if scope.privateManualLyricsWork != nil || scope.optionalLyrics != nil || reservation.borrowed {
		t.Fatal("accepted finish retained the private borrow")
	}
	scope.releaseSynchronousWork(reservation)
	if err := scope.Finalize(context.Background(), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if repository.marks != 1 {
		t.Fatalf("expected one quiescence acknowledgement, got %d repository calls", repository.marks)
	}
}

func TestPrivateManualLyricsWorkRequiresExactPreparedProofAndIdleScope(t *testing.T) {
	scope, reservation, stage, proof, _, _ := privateWorkFixture()
	if scope.acquirePrivateManualLyricsWork(reservation, stage, &publicationInitialOutputCopy{}) != nil {
		t.Fatal("proof from another stage was accepted")
	}
	stage.privatePrepMode = false
	if scope.acquirePrivateManualLyricsWork(reservation, stage, proof) != nil {
		t.Fatal("non-private stage was accepted")
	}
	stage.privatePrepMode = true
	scope.unresolved = 1
	if scope.acquirePrivateManualLyricsWork(reservation, stage, proof) != nil {
		t.Fatal("unresolved command was accepted")
	}
	scope.unresolved = 0
	reservation.borrowed = true
	if scope.acquirePrivateManualLyricsWork(reservation, stage, proof) != nil {
		t.Fatal("already borrowed reservation was accepted")
	}
}
