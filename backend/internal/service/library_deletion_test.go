package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

type libraryDeletionConfirmationRepoFake struct {
	committed   *persistence.Operation
	digest      string
	findCalls   int
	admitCalls  int
	commitError error
}

func (repo *libraryDeletionConfirmationRepoFake) GetOperation(context.Context, uuid.UUID) (*persistence.Operation, error) {
	return nil, nil
}

func (repo *libraryDeletionConfirmationRepoFake) CaptureLibraryDeletionPreview(context.Context, string, uuid.UUID, uuid.UUID, func(context.Context, string, persistence.LibraryDeletionPreview, map[uuid.UUID]uuid.UUID) ([]json.RawMessage, error)) (persistence.LibraryDeletionPreview, []json.RawMessage, error) {
	return persistence.LibraryDeletionPreview{}, nil, errors.New("unexpected preview capture")
}

func (repo *libraryDeletionConfirmationRepoFake) FindLibraryDeletionConfirmation(_ context.Context, operationID uuid.UUID, digest string) (*persistence.Operation, error) {
	repo.findCalls++
	if repo.committed != nil && repo.committed.ID == operationID && repo.digest == digest {
		return repo.committed, nil
	}
	return nil, nil
}

func (repo *libraryDeletionConfirmationRepoFake) AdmitLibraryDeletionOperation(_ context.Context, operationID uuid.UUID, digest string, _ time.Time, _ persistence.LibraryDeletionPreview, _ []json.RawMessage, _ persistence.RiverInserter, _ *river.InsertOpts) (*persistence.Operation, error) {
	repo.admitCalls++
	repo.digest = digest
	repo.committed = &persistence.Operation{ID: operationID, Kind: persistence.LibraryDeletionOperationKind, State: "queued"}
	return nil, repo.commitError
}

func (repo *libraryDeletionConfirmationRepoFake) ClaimLibraryDeletionDelivery(context.Context, persistence.LibraryDeletionDeliveryGuard) (persistence.LibraryDeletionDeliveryClaimOutcome, error) {
	return persistence.LibraryDeletionDeliveryBlocked, errors.New("unexpected delivery claim")
}

func (repo *libraryDeletionConfirmationRepoFake) SettleLibraryDeletionPublication(context.Context, persistence.LibraryDeletionDeliveryGuard, uuid.UUID, func(context.Context, json.RawMessage) error) (bool, error) {
	return false, errors.New("unexpected settlement")
}

func (repo *libraryDeletionConfirmationRepoFake) FinalizeLibraryDeletion(context.Context, persistence.LibraryDeletionDeliveryGuard) (bool, error) {
	return false, errors.New("unexpected finalization")
}

func (repo *libraryDeletionConfirmationRepoFake) FailLibraryDeletionDelivery(context.Context, persistence.LibraryDeletionDeliveryGuard, string) (bool, error) {
	return false, errors.New("unexpected failure")
}

func (repo *libraryDeletionConfirmationRepoFake) RetryLibraryDeletionOperationAndEnqueue(context.Context, uuid.UUID, persistence.RiverInserter) (*persistence.Operation, error) {
	return nil, errors.New("unexpected retry")
}

func TestLibraryDeletionConfirmRetainsCapabilityAcrossCancellationAndResolvesCommittedResult(t *testing.T) {
	ctx := context.Background()
	repo := &libraryDeletionConfirmationRepoFake{commitError: errors.New("commit acknowledgement lost")}
	coordinator := publication.NewCoordinator()
	bootID := uuid.New()
	newService := func() *LibraryDeletionService {
		return &LibraryDeletionService{
			repository: repo, coordinator: coordinator, bootUUID: bootID,
			opener: sourcefs.NewOutputOpener(), previews: make(map[string]cachedLibraryDeletionPreview),
		}
	}
	digestBytes := sha256.Sum256([]byte(uuid.NewString()))
	digest := hex.EncodeToString(digestBytes[:])
	preview := LibraryDeletionPreview{
		ConfirmationDigest: digest,
		Blockers:           make([]LibraryDeletionBlocker, 0),
		persistencePreview: persistence.LibraryDeletionPreview{
			Scope: persistence.LibraryDeletionScope{
				Kind: "release", ReleaseID: uuid.New(), PublicationIDs: []uuid.UUID{},
				ManualReceiptRequestIDs: []uuid.UUID{}, DependencyDigest: hex.EncodeToString(digestBytes[:]),
			},
			ScopeDigest:  hex.EncodeToString(digestBytes[:]),
			Publications: []persistence.TrackPublication{},
		},
		witnesses: []json.RawMessage{}, expiresAt: time.Now().Add(time.Minute),
	}
	service := newService()
	service.cachePreview(preview)
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := service.Confirm(canceledCtx, digest, nil, nil); err == nil {
		t.Fatal("confirmation with canceled context unexpectedly succeeded")
	}
	if repo.findCalls != 0 || repo.admitCalls != 0 {
		t.Fatalf("canceled confirmation touched repository: find=%d admit=%d", repo.findCalls, repo.admitCalls)
	}
	if _, err := service.Confirm(ctx, digest, nil, nil); !errors.Is(err, repo.commitError) {
		t.Fatalf("confirmation did not propagate simulated lost acknowledgement: %v", err)
	}
	if repo.admitCalls != 1 || repo.committed == nil {
		t.Fatalf("simulated committed admission missing: calls=%d operation=%v", repo.admitCalls, repo.committed)
	}
	if _, ok := service.previewForConfirmation(digest); !ok {
		t.Fatal("lost admission acknowledgement consumed the cached confirmation capability")
	}

	freshService := newService()
	operation, err := freshService.Confirm(ctx, digest, nil, nil)
	if err != nil {
		t.Fatalf("fresh service did not resolve committed confirmation: %v", err)
	}
	if operation == nil || operation.ID != repo.committed.ID || repo.admitCalls != 1 {
		t.Fatalf("fresh service result=%v, admission calls=%d", operation, repo.admitCalls)
	}
}
