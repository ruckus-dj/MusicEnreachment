package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

type publicationReconciliationServiceRepository struct {
	*automaticAdmissionServiceRepository
	facts      *persistence.PublicationReconciliationContext
	parked     bool
	parkReason string
}

func (repository *publicationReconciliationServiceRepository) ResolvePublicationReconciliationContext(context.Context, persistence.PublicationReconciliationDeliveryGuard) (*persistence.PublicationReconciliationContext, error) {
	return repository.facts, nil
}

func (repository *publicationReconciliationServiceRepository) ParkPublicationReconciliationDelivery(_ context.Context, _ persistence.PublicationReconciliationDeliveryGuard, generation int64, reason string) (bool, error) {
	if generation != repository.facts.RequestedGeneration {
		return false, nil
	}
	repository.parked = true
	repository.parkReason = reason
	return true, nil
}

func TestReconcilePublicationDefersExistingPublicationWithoutReplacingIt(t *testing.T) {
	base, runtime, _, _, guard := automaticAdmissionFixture(t)
	repository := &publicationReconciliationServiceRepository{
		automaticAdmissionServiceRepository: base,
		facts:                               &persistence.PublicationReconciliationContext{ReleaseID: uuid.New(), RequestedGeneration: 7, HasCurrentPublication: true},
	}
	service := newLibraryPublications(repository, runtime, libraryPublicationsTestRiver{}, tools.Platform{GOOS: "darwin", GOARCH: "arm64"})
	if err := service.ReconcilePublication(context.Background(), guard); err == nil {
		t.Fatal("existing publication did not preserve intent for retry")
	}
	if repository.parked || base.automaticCalls != 0 {
		t.Fatalf("parked=%v reason=%q automatic admissions=%d", repository.parked, repository.parkReason, base.automaticCalls)
	}
}

func TestReconcilePublicationRetriesForRetainedAttemptAndMissingTools(t *testing.T) {
	t.Run("retained attempt", func(t *testing.T) {
		base, runtime, _, _, guard := automaticAdmissionFixture(t)
		repository := &publicationReconciliationServiceRepository{
			automaticAdmissionServiceRepository: base,
			facts: &persistence.PublicationReconciliationContext{
				ReleaseID: base.snapshot.Library.ReleaseID, RequestedGeneration: 2, HasActiveAttempt: true,
			},
		}
		service := newLibraryPublications(repository, runtime, libraryPublicationsTestRiver{}, tools.Platform{GOOS: "darwin", GOARCH: "arm64"})
		if err := service.ReconcilePublication(context.Background(), guard); err == nil {
			t.Fatal("retained attempt was not deferred")
		}
		if repository.parked || base.automaticCalls != 0 {
			t.Fatalf("parked=%v automatic admissions=%d", repository.parked, base.automaticCalls)
		}
	})

	t.Run("missing tools", func(t *testing.T) {
		base, runtime, _, _, guard := automaticAdmissionFixture(t)
		runtime.ffmpeg = ""
		repository := &publicationReconciliationServiceRepository{
			automaticAdmissionServiceRepository: base,
			facts:                               &persistence.PublicationReconciliationContext{ReleaseID: base.snapshot.Library.ReleaseID, RequestedGeneration: 2},
		}
		service := newLibraryPublications(repository, runtime, libraryPublicationsTestRiver{}, tools.Platform{GOOS: "darwin", GOARCH: "arm64"})
		if err := service.ReconcilePublication(context.Background(), guard); err == nil {
			t.Fatal("missing tools were not retried")
		}
		if repository.parked || base.automaticCalls != 0 {
			t.Fatalf("parked=%v automatic admissions=%d", repository.parked, base.automaticCalls)
		}
	})
}

func TestReconcilePublicationDefersAmbiguousQualityAndAdmitsUniqueWinner(t *testing.T) {
	t.Run("ambiguous quality retries", func(t *testing.T) {
		base, runtime, _, _, guard := automaticAdmissionFixture(t)
		// A channel-count conflict is incomparable under the existing selector.
		channels := 6
		base.snapshot.Sources[1].Quality.Channels = &channels
		base.snapshot.Sources[1].ChannelLayout = "5.1"
		repository := &publicationReconciliationServiceRepository{
			automaticAdmissionServiceRepository: base,
			facts:                               &persistence.PublicationReconciliationContext{ReleaseID: base.snapshot.Library.ReleaseID, RequestedGeneration: 4},
		}
		service := newLibraryPublications(repository, runtime, libraryPublicationsTestRiver{}, tools.Platform{GOOS: "darwin", GOARCH: "arm64"})
		if err := service.ReconcilePublication(context.Background(), guard); err == nil {
			t.Fatal("ambiguous quality did not preserve intent for retry")
		}
		if repository.parked || base.automaticCalls != 0 {
			t.Fatalf("parked=%v reason=%q automatic admissions=%d", repository.parked, repository.parkReason, base.automaticCalls)
		}
	})

	t.Run("unique winner admits", func(t *testing.T) {
		base, runtime, _, positionID, guard := automaticAdmissionFixture(t)
		repository := &publicationReconciliationServiceRepository{
			automaticAdmissionServiceRepository: base,
			facts:                               &persistence.PublicationReconciliationContext{ReleaseID: base.snapshot.Library.ReleaseID, RequestedGeneration: 3},
		}
		service := newLibraryPublications(repository, runtime, libraryPublicationsTestRiver{}, tools.Platform{GOOS: "darwin", GOARCH: "arm64"})
		if err := service.ReconcilePublication(context.Background(), guard); err != nil {
			t.Fatal(err)
		}
		if repository.parked || base.automaticCalls != 1 || base.manualCalls != 0 {
			t.Fatalf("parked=%v automatic/manual admissions=%d/%d", repository.parked, base.automaticCalls, base.manualCalls)
		}
		if guard.PositionID != positionID {
			t.Fatalf("guard position=%s, expected %s", guard.PositionID, positionID)
		}
	})
}

func TestAutomaticFirstSourceChoiceReportsManualStreamOnlyWhenNoSafeCandidate(t *testing.T) {
	base, _, _, _, _ := automaticAdmissionFixture(t)
	stream := base.snapshot.Sources[0]
	stream.AudioStreamCount = 2
	stream.RequiresExplicitStreamSelection = true
	_, reason, err := automaticFirstSourceChoiceWithReason(&persistence.LibraryFirstPublicationContext{Sources: []persistence.FirstPublicationSource{stream}}, publication.AudioFormatMKA)
	if err == nil || reason != "manual_stream_selection" {
		t.Fatalf("choice reason/error = %q/%v, want manual stream selection", reason, err)
	}
}
