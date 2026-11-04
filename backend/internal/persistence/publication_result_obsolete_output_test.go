package persistence

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestValidateObsoletePublicationOutputCleanupProof(t *testing.T) {
	size, mtime := int64(10), int64(20)
	position, epoch, generation := uuid.New(), uuid.New(), uuid.New()
	digest := sha256.Sum256([]byte("old output"))
	previous := &TrackPublication{
		PositionID: position, Epoch: epoch, Generation: generation, RelativePath: "album/track-old.flac",
		Ownership: mustPublicationJSON(PublicationCurrentOwnership{Version: PublicationCurrentOwnershipVersion, Identity: PublicationFileIdentity{
			SizeBytes: &size, MTime: &mtime, SHA256: hex.EncodeToString(digest[:]),
		}}),
	}
	attempt := &TrackPublicationAttempt{PositionID: position, CandidateGeneration: uuid.New()}
	proof := &ObsoletePublicationOutputCleanupAdmission{
		Epoch: epoch, PositionID: position, Generation: generation, RelativePath: previous.RelativePath,
		NamespaceClaimID: uuid.New(), FileSize: size, ModifiedAt: time.UnixMicro(mtime), SHA256: append([]byte(nil), digest[:]...),
	}
	if err := validateObsoletePublicationOutputCleanup(proof, previous, attempt, "album/track.flac"); err != nil {
		t.Fatalf("valid proof rejected: %v", err)
	}
	if err := validateObsoletePublicationOutputCleanup(nil, previous, attempt, "album/track.flac"); err == nil {
		t.Fatal("missing proof accepted")
	}
	for _, rejection := range []struct {
		name   string
		mutate func(*ObsoletePublicationOutputCleanupAdmission)
	}{
		{name: "wrong mtime", mutate: func(proof *ObsoletePublicationOutputCleanupAdmission) {
			proof.ModifiedAt = proof.ModifiedAt.Add(time.Microsecond)
		}},
		{name: "wrong size", mutate: func(proof *ObsoletePublicationOutputCleanupAdmission) { proof.FileSize++ }},
		{name: "wrong epoch", mutate: func(proof *ObsoletePublicationOutputCleanupAdmission) { proof.Epoch = uuid.New() }},
		{name: "wrong path", mutate: func(proof *ObsoletePublicationOutputCleanupAdmission) { proof.RelativePath = "album/track-other.flac" }},
	} {
		t.Run(rejection.name, func(t *testing.T) {
			mutated := *proof
			mutated.SHA256 = append([]byte(nil), proof.SHA256...)
			rejection.mutate(&mutated)
			if err := validateObsoletePublicationOutputCleanup(&mutated, previous, attempt, "album/track.flac"); err == nil {
				t.Fatal("inconsistent obsolete output cleanup proof accepted")
			}
		})
	}
	wrongOwnership := *proof
	wrongOwnership.SHA256 = append([]byte(nil), proof.SHA256...)
	wrongOwnership.SHA256[0] ^= 1
	if err := validateObsoletePublicationOutputCleanup(&wrongOwnership, previous, attempt, "album/track.flac"); err == nil {
		t.Fatal("ownership digest mismatch accepted")
	}
	guard := PublicationDeliveryGuard{
		OperationID: uuid.New(), ApplicationAttempt: 1, RiverJobID: 1, RiverAttempt: 1,
		ClaimUUID: uuid.New(), BootUUID: uuid.New(), DeliveryToken: "delivery",
		Epoch: epoch, CandidateGeneration: uuid.New(), ExpectedPositionID: position,
	}
	if err := validatePublicationDeliveryGuard(guard); err != nil {
		t.Fatalf("complete publication delivery guard rejected: %v", err)
	}
	if err := validatePublicationDeliveryGuard(PublicationDeliveryGuard{}); err == nil {
		t.Fatal("incomplete publication delivery guard accepted")
	}
}
