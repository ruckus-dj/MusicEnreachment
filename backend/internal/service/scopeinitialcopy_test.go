package service

import (
	"context"
	"sync"
	"testing"
)

func TestPublicationScopeInitialCopyPinsReservationAgainstReleaseAndFinalize(t *testing.T) {
	repository := &scopeRepositoryFake{valid: true, marked: true}
	scope := newScopeForTest(t, &scopeCommandFake{}, repository)
	reservation := scope.reserveSynchronousWork()
	if reservation == nil || !scope.beginInitialOutputCopy(reservation) {
		t.Fatal("initial output copy did not acquire its reservation fence")
	}

	var wg sync.WaitGroup
	wg.Add(2)
	var finalizeErr error
	go func() {
		defer wg.Done()
		finalizeErr = scope.Finalize(context.Background(), func() error { return nil })
	}()
	go func() {
		defer wg.Done()
		scope.releaseSynchronousWork(reservation)
	}()
	wg.Wait()
	if finalizeErr == nil {
		t.Fatal("finalize succeeded during initial output copy")
	}
	if !reservation.active || !reservation.borrowed || scope.syncWork != 1 || scope.finalized || repository.marks != 0 {
		t.Fatalf("initial-copy reservation was not retained: active=%v borrowed=%v syncWork=%d finalized=%v marks=%d", reservation.active, reservation.borrowed, scope.syncWork, scope.finalized, repository.marks)
	}

	scope.endInitialOutputCopy(reservation, false)
	if reservation.borrowed || !reservation.active || scope.initialCopy {
		t.Fatal("initial output copy did not release borrow ownership")
	}
	scope.releaseSynchronousWork(reservation)
	if scope.syncWork != 0 {
		t.Fatalf("synchronous reservation remains after release: %d", scope.syncWork)
	}
	if err := scope.Finalize(context.Background(), func() error { return nil }); err != nil {
		t.Fatalf("finalize after copy completion: %v", err)
	}
}

func TestPublicationScopeInitialCopyQuarantinesEvenIfReservationWasDeactivated(t *testing.T) {
	scope := newScopeForTest(t, &scopeCommandFake{}, &scopeRepositoryFake{valid: true, marked: true})
	reservation := scope.reserveSynchronousWork()
	if reservation == nil || !scope.beginInitialOutputCopy(reservation) {
		t.Fatal("initial output copy did not acquire its reservation fence")
	}

	// Simulate an invariant violation: some erroneous path deactivated the token
	// while the copy was in progress. End must still quarantine the scope.
	scope.mu.Lock()
	reservation.active = false
	scope.syncWork--
	scope.mu.Unlock()
	scope.endInitialOutputCopy(reservation, true)

	if !scope.unknown || scope.release == nil {
		t.Fatalf("uncertain copy did not quarantine and retain scope ownership: unknown=%v release=%v", scope.unknown, scope.release != nil)
	}
	if err := scope.Finalize(context.Background(), func() error { return nil }); err == nil {
		t.Fatal("finalize succeeded after uncertain initial output copy")
	}
}
