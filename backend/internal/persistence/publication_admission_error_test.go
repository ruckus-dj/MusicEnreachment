package persistence

import (
	"errors"
	"fmt"
	"testing"
)

func TestPublicationAdmissionConflictSentinel(t *testing.T) {
	err := fmt.Errorf("admit publication: current generation changed: %w", ErrPublicationAdmissionConflict)
	if !errors.Is(err, ErrPublicationAdmissionConflict) {
		t.Fatalf("wrapped conflict = %v, want ErrPublicationAdmissionConflict", err)
	}
	if errors.Is(fmt.Errorf("admit publication: invalid frozen input"), ErrPublicationAdmissionConflict) {
		t.Fatal("invalid frozen input must not be classified as an admission conflict")
	}
}
