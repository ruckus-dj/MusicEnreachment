package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

type publicationReconciliationServiceRepository struct {
	*automaticAdmissionServiceRepository
	facts         *persistence.PublicationReconciliationContext
	parked        bool
	parkResult    bool
	parkReason    string
	inputsParked  bool
	inputsResult  bool
	inputsCalls   int
	decision      persistence.PublicationReconciliationDecision
	evaluateErr   error
	evaluateCalls int
	ackResult     bool
	ackErr        error
	ackCalls      int
}

func (repository *publicationReconciliationServiceRepository) ResolvePublicationReconciliationContext(context.Context, persistence.PublicationReconciliationDeliveryGuard) (*persistence.PublicationReconciliationContext, error) {
	return repository.facts, nil
}

func (repository *publicationReconciliationServiceRepository) EvaluatePublicationReconciliationCurrent(context.Context, uuid.UUID, persistence.PublicationReconciliationDeliveryGuard, string, string) (persistence.PublicationReconciliationDecision, error) {
	repository.evaluateCalls++
	return repository.decision, repository.evaluateErr
}

func (repository *publicationReconciliationServiceRepository) AcknowledgePublicationReconciliationNoop(_ context.Context, _ persistence.PublicationReconciliationDeliveryGuard, _ *persistence.PublicationReconciliationNoop) (bool, error) {
	repository.ackCalls++
	return repository.ackResult, repository.ackErr
}

func (repository *publicationReconciliationServiceRepository) ParkPublicationReconciliationBusyAttempt(_ context.Context, _ persistence.PublicationReconciliationDeliveryGuard, generation int64, reason string) (bool, error) {
	if generation != repository.facts.RequestedGeneration {
		return false, nil
	}
	repository.parked = repository.parkResult
	repository.parkReason = reason
	return repository.parkResult, nil
}

func (repository *publicationReconciliationServiceRepository) ParkPublicationReconciliationInputsPending(_ context.Context, witness *persistence.PublicationReconciliationInputsPending) (bool, error) {
	repository.inputsCalls++
	repository.inputsParked = witness != nil && repository.inputsResult
	return repository.inputsParked, nil
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

func TestReconcilePublicationCurrentRoutesOnlyProvenNoopToAcknowledgement(t *testing.T) {
	t.Run("pending accepted graph parks without acknowledgement", func(t *testing.T) {
		base, runtime, _, _, guard := automaticAdmissionFixture(t)
		repository := &publicationReconciliationServiceRepository{
			automaticAdmissionServiceRepository: base,
			facts:                               &persistence.PublicationReconciliationContext{ReleaseID: base.snapshot.Library.ReleaseID, RequestedGeneration: 1, HasCurrentPublication: true},
			decision:                            persistence.PublicationReconciliationDecision{Action: persistence.InputsPending, InputsPending: &persistence.PublicationReconciliationInputsPending{}},
			inputsResult:                        true,
		}
		service := newLibraryPublications(repository, runtime, libraryPublicationsTestRiver{}, tools.Platform{GOOS: "darwin", GOARCH: "arm64"}).WithPublicationCoordinator(publication.NewCoordinator())
		if err := service.ReconcilePublication(context.Background(), guard); err != nil {
			t.Fatalf("ReconcilePublication() error = %v", err)
		}
		if repository.inputsCalls != 1 || !repository.inputsParked || repository.ackCalls != 0 || base.automaticCalls != 0 {
			t.Fatalf("pending parks=%d/%v acknowledgements=%d producer admissions=%d", repository.inputsCalls, repository.inputsParked, repository.ackCalls, base.automaticCalls)
		}
	})

	t.Run("all noop acknowledges without producer", func(t *testing.T) {
		base, runtime, _, _, guard := automaticAdmissionFixture(t)
		repository := &publicationReconciliationServiceRepository{
			automaticAdmissionServiceRepository: base,
			facts:                               &persistence.PublicationReconciliationContext{ReleaseID: base.snapshot.Library.ReleaseID, RequestedGeneration: 1, HasCurrentPublication: true},
			decision:                            persistence.PublicationReconciliationDecision{Action: persistence.AllNoop, Noop: &persistence.PublicationReconciliationNoop{}},
			ackResult:                           true,
		}
		service := newLibraryPublications(repository, runtime, libraryPublicationsTestRiver{}, tools.Platform{GOOS: "darwin", GOARCH: "arm64"}).WithPublicationCoordinator(publication.NewCoordinator())
		if err := service.ReconcilePublication(context.Background(), guard); err != nil {
			t.Fatalf("ReconcilePublication() error = %v", err)
		}
		if repository.evaluateCalls != 1 || repository.ackCalls != 1 || base.automaticCalls != 0 {
			t.Fatalf("evaluations=%d acknowledgements=%d producer admissions=%d", repository.evaluateCalls, repository.ackCalls, base.automaticCalls)
		}
	})

	t.Run("stale acknowledgement retries", func(t *testing.T) {
		base, runtime, _, _, guard := automaticAdmissionFixture(t)
		repository := &publicationReconciliationServiceRepository{
			automaticAdmissionServiceRepository: base,
			facts:                               &persistence.PublicationReconciliationContext{ReleaseID: base.snapshot.Library.ReleaseID, RequestedGeneration: 1, HasCurrentPublication: true},
			decision:                            persistence.PublicationReconciliationDecision{Action: persistence.AllNoop, Noop: &persistence.PublicationReconciliationNoop{}},
		}
		service := newLibraryPublications(repository, runtime, libraryPublicationsTestRiver{}, tools.Platform{GOOS: "darwin", GOARCH: "arm64"}).WithPublicationCoordinator(publication.NewCoordinator())
		if err := service.ReconcilePublication(context.Background(), guard); err == nil {
			t.Fatal("stale no-op acknowledgement succeeded")
		}
		if repository.ackCalls != 1 || base.automaticCalls != 0 {
			t.Fatalf("acknowledgements=%d producer admissions=%d", repository.ackCalls, base.automaticCalls)
		}
	})

	t.Run("acknowledgement error retries", func(t *testing.T) {
		base, runtime, _, _, guard := automaticAdmissionFixture(t)
		repository := &publicationReconciliationServiceRepository{
			automaticAdmissionServiceRepository: base,
			facts:                               &persistence.PublicationReconciliationContext{ReleaseID: base.snapshot.Library.ReleaseID, RequestedGeneration: 1, HasCurrentPublication: true},
			decision:                            persistence.PublicationReconciliationDecision{Action: persistence.AllNoop, Noop: &persistence.PublicationReconciliationNoop{}},
			ackErr:                              errors.New("acknowledgement failed"),
		}
		service := newLibraryPublications(repository, runtime, libraryPublicationsTestRiver{}, tools.Platform{GOOS: "darwin", GOARCH: "arm64"}).WithPublicationCoordinator(publication.NewCoordinator())
		if err := service.ReconcilePublication(context.Background(), guard); err == nil {
			t.Fatal("acknowledgement error was ignored")
		}
		if repository.ackCalls != 1 || base.automaticCalls != 0 {
			t.Fatalf("acknowledgements=%d producer admissions=%d", repository.ackCalls, base.automaticCalls)
		}
	})

	t.Run("invalid decision never acknowledges", func(t *testing.T) {
		base, runtime, _, _, guard := automaticAdmissionFixture(t)
		repository := &publicationReconciliationServiceRepository{
			automaticAdmissionServiceRepository: base,
			facts:                               &persistence.PublicationReconciliationContext{ReleaseID: base.snapshot.Library.ReleaseID, RequestedGeneration: 1, HasCurrentPublication: true},
			decision:                            persistence.PublicationReconciliationDecision{Action: persistence.AllNoop},
		}
		service := newLibraryPublications(repository, runtime, libraryPublicationsTestRiver{}, tools.Platform{GOOS: "darwin", GOARCH: "arm64"}).WithPublicationCoordinator(publication.NewCoordinator())
		if err := service.ReconcilePublication(context.Background(), guard); err == nil {
			t.Fatal("all-noop decision without witness succeeded")
		}
		if repository.ackCalls != 0 || base.automaticCalls != 0 {
			t.Fatalf("acknowledgements=%d producer admissions=%d", repository.ackCalls, base.automaticCalls)
		}
	})

	t.Run("evaluation error never acknowledges", func(t *testing.T) {
		base, runtime, _, _, guard := automaticAdmissionFixture(t)
		repository := &publicationReconciliationServiceRepository{
			automaticAdmissionServiceRepository: base,
			facts:                               &persistence.PublicationReconciliationContext{ReleaseID: base.snapshot.Library.ReleaseID, RequestedGeneration: 1, HasCurrentPublication: true},
			evaluateErr:                         errors.New("evaluation failed"),
		}
		service := newLibraryPublications(repository, runtime, libraryPublicationsTestRiver{}, tools.Platform{GOOS: "darwin", GOARCH: "arm64"}).WithPublicationCoordinator(publication.NewCoordinator())
		if err := service.ReconcilePublication(context.Background(), guard); err == nil {
			t.Fatal("evaluation error was ignored")
		}
		if repository.ackCalls != 0 || base.automaticCalls != 0 {
			t.Fatalf("acknowledgements=%d producer admissions=%d", repository.ackCalls, base.automaticCalls)
		}
	})
}

func TestReconcilePublicationRetriesForRetainedAttemptAndMissingTools(t *testing.T) {
	t.Run("retained attempt", func(t *testing.T) {
		base, runtime, _, _, guard := automaticAdmissionFixture(t)
		repository := &publicationReconciliationServiceRepository{
			automaticAdmissionServiceRepository: base,
			parkResult:                          true,
			facts: &persistence.PublicationReconciliationContext{
				ReleaseID: base.snapshot.Library.ReleaseID, RequestedGeneration: 2, HasActiveAttempt: true,
			},
		}
		service := newLibraryPublications(repository, runtime, libraryPublicationsTestRiver{}, tools.Platform{GOOS: "darwin", GOARCH: "arm64"})
		if err := service.ReconcilePublication(context.Background(), guard); err != nil {
			t.Fatalf("retained attempt was not safely parked: %v", err)
		}
		if !repository.parked || repository.parkReason != persistence.PublicationReconciliationBusyAttemptReason || base.automaticCalls != 0 {
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
	t.Run("busy condition changed", func(t *testing.T) {
		base, runtime, _, _, guard := automaticAdmissionFixture(t)
		repository := &publicationReconciliationServiceRepository{
			automaticAdmissionServiceRepository: base,
			facts: &persistence.PublicationReconciliationContext{
				ReleaseID: base.snapshot.Library.ReleaseID, RequestedGeneration: 2, HasActiveAttempt: true,
			},
		}
		service := newLibraryPublications(repository, runtime, libraryPublicationsTestRiver{}, tools.Platform{GOOS: "darwin", GOARCH: "arm64"})
		if err := service.ReconcilePublication(context.Background(), guard); err == nil {
			t.Fatal("changed busy condition was reported as success")
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
