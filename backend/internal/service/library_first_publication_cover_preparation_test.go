package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type coverPreparationRepository struct {
	firstPreviewRepository
	snapshots []*persistence.LibraryFirstPublicationContext
	readErr   error
	reads     int
}

func (repository *coverPreparationRepository) ReadLibraryFirstPublicationContext(context.Context, uuid.UUID, uuid.UUID) (*persistence.LibraryFirstPublicationContext, error) {
	if repository.readErr != nil {
		return nil, repository.readErr
	}
	if len(repository.snapshots) == 0 {
		return nil, nil
	}
	index := repository.reads
	if index >= len(repository.snapshots) {
		index = len(repository.snapshots) - 1
	}
	repository.reads++
	return repository.snapshots[index], nil
}

func firstPublicationCoverSnapshot(releaseID, positionID uuid.UUID, cover persistence.EffectiveReleaseCoverSummary) *persistence.LibraryFirstPublicationContext {
	return &persistence.LibraryFirstPublicationContext{
		Library:        &persistence.LibraryPublicationContext{ReleaseID: releaseID, PositionID: positionID},
		EffectiveCover: cover,
	}
}

func TestPrepareFirstCoverPreparesEveryOrdinaryFirstPublication(t *testing.T) {
	releaseID, positionID := uuid.New(), uuid.New()
	generation := uuid.New()
	automaticAbsent := persistence.EffectiveReleaseCoverSummary{Revision: 1, Mode: "automatic", State: "absent"}
	tests := []struct {
		name         string
		snapshot     *persistence.LibraryFirstPublicationContext
		acquisition  *artworkPreparationFixture
		wantCalls    int
		wantReads    int
		wantError    error
		wantContains string
	}{
		{name: "automatic absent first publication", snapshot: firstPublicationCoverSnapshot(releaseID, positionID, automaticAbsent), acquisition: &artworkPreparationFixture{}, wantCalls: 1, wantReads: 2},
		{name: "current publication is read-only", snapshot: func() *persistence.LibraryFirstPublicationContext {
			snapshot := firstPublicationCoverSnapshot(releaseID, positionID, automaticAbsent)
			snapshot.Library.Current = &persistence.LibraryPublicationCurrent{}
			return snapshot
		}(), acquisition: &artworkPreparationFixture{}, wantReads: 1},
		{name: "active attempt is read-only", snapshot: func() *persistence.LibraryFirstPublicationContext {
			snapshot := firstPublicationCoverSnapshot(releaseID, positionID, automaticAbsent)
			snapshot.Library.ActiveAttempt = &persistence.LibraryPublicationOperation{}
			return snapshot
		}(), acquisition: &artworkPreparationFixture{}, wantReads: 1},
		{name: "manual authority still prepares gallery", snapshot: firstPublicationCoverSnapshot(releaseID, positionID, persistence.EffectiveReleaseCoverSummary{Revision: 2, Mode: "manual", State: "absent"}), acquisition: &artworkPreparationFixture{}, wantCalls: 1, wantReads: 2},
		{name: "selected automatic cover still prepares gallery", snapshot: firstPublicationCoverSnapshot(releaseID, positionID, persistence.EffectiveReleaseCoverSummary{Revision: 2, Mode: "automatic", State: "value", Generation: &generation}), acquisition: &artworkPreparationFixture{}, wantCalls: 1, wantReads: 2},
		{name: "missing acquisition service is a no-op", snapshot: firstPublicationCoverSnapshot(releaseID, positionID, automaticAbsent), acquisition: nil, wantReads: 1},
		{name: "artwork preparation failure is classified as pending artwork", snapshot: firstPublicationCoverSnapshot(releaseID, positionID, automaticAbsent), acquisition: &artworkPreparationFixture{err: errors.New("provider unavailable")}, wantCalls: 1, wantReads: 1, wantError: ErrPublicationArtwork, wantContains: "provider unavailable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &coverPreparationRepository{snapshots: []*persistence.LibraryFirstPublicationContext{test.snapshot, refreshedCoverSnapshot(releaseID, positionID)}}
			service := newLibraryPublications(repository, firstPreviewSettings{}, nil, tools.Platform{})
			if test.acquisition != nil {
				service.WithPublicationArtwork(test.acquisition)
			}
			err := service.PrepareFirstCover(context.Background(), releaseID, positionID)
			if test.wantError != nil && !errors.Is(err, test.wantError) {
				t.Fatalf("error = %v, want %v", err, test.wantError)
			}
			if test.wantContains != "" && (err == nil || !strings.Contains(err.Error(), test.wantContains)) {
				t.Fatalf("error = %v, want it to contain %q", err, test.wantContains)
			}
			if test.wantError == nil && test.wantContains == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if test.acquisition != nil && test.acquisition.calls != test.wantCalls {
				t.Fatalf("acquisition calls = %d, want %d", test.acquisition.calls, test.wantCalls)
			}
			if repository.reads != test.wantReads {
				t.Fatalf("context reads = %d, want %d", repository.reads, test.wantReads)
			}
		})
	}
}

func refreshedCoverSnapshot(releaseID, positionID uuid.UUID) *persistence.LibraryFirstPublicationContext {
	generation := uuid.New()
	return firstPublicationCoverSnapshot(releaseID, positionID, persistence.EffectiveReleaseCoverSummary{Revision: 2, Mode: "automatic", State: "value", Generation: &generation})
}

func TestPrepareFirstCoverHonorsCancellation(t *testing.T) {
	releaseID, positionID := uuid.New(), uuid.New()
	repository := &coverPreparationRepository{snapshots: []*persistence.LibraryFirstPublicationContext{
		firstPublicationCoverSnapshot(releaseID, positionID, persistence.EffectiveReleaseCoverSummary{Revision: 1, Mode: "automatic", State: "absent"}),
	}}
	acquisition := &artworkPreparationFixture{err: errors.New("ignored")}
	service := newLibraryPublications(repository, firstPreviewSettings{}, nil, tools.Platform{}).WithPublicationArtwork(acquisition)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.PrepareFirstCover(ctx, releaseID, positionID); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
	if acquisition.calls != 0 || repository.reads != 1 {
		t.Fatalf("acquisition calls/context reads = %d/%d, want canceled preparation to make no provider call", acquisition.calls, repository.reads)
	}
}

func TestPrepareFirstCoverRejectsMissingIdentity(t *testing.T) {
	service := newLibraryPublications(&coverPreparationRepository{}, firstPreviewSettings{}, nil, tools.Platform{})
	if err := service.PrepareFirstCover(context.Background(), uuid.Nil, uuid.New()); !errors.Is(err, persistence.ErrPublicationAdmissionConflict) {
		t.Fatalf("error = %v, want admission conflict", err)
	}
}

func TestPrepareSelectionCoversAcquiresOncePerEligibleRelease(t *testing.T) {
	releaseA, releaseB, releaseC := uuid.New(), uuid.New(), uuid.New()
	positions := []LibraryPublicationSelectionPosition{
		{ReleaseID: releaseA, PositionID: uuid.New()},
		{ReleaseID: releaseA, PositionID: uuid.New()},
		{ReleaseID: releaseA, PositionID: uuid.New()},
		{ReleaseID: releaseB, PositionID: uuid.New()},
		{ReleaseID: releaseC, PositionID: uuid.New()},
	}
	automaticAbsent := persistence.EffectiveReleaseCoverSummary{Revision: 1, Mode: "automatic", State: "absent"}
	snapshots := map[LibraryPublicationSelectionPosition]*persistence.LibraryFirstPublicationContext{
		positions[0]: func() *persistence.LibraryFirstPublicationContext {
			snapshot := firstPublicationCoverSnapshot(releaseA, positions[0].PositionID, automaticAbsent)
			snapshot.Library.Current = &persistence.LibraryPublicationCurrent{}
			return snapshot
		}(),
		positions[1]: firstPublicationCoverSnapshot(releaseA, positions[1].PositionID, automaticAbsent),
		positions[2]: firstPublicationCoverSnapshot(releaseA, positions[2].PositionID, automaticAbsent),
		positions[3]: firstPublicationCoverSnapshot(releaseB, positions[3].PositionID, automaticAbsent),
		positions[4]: firstPublicationCoverSnapshot(releaseC, positions[4].PositionID, persistence.EffectiveReleaseCoverSummary{Revision: 2, Mode: "manual", State: "absent"}),
	}
	repository := &selectionCoverPreparationRepository{snapshots: snapshots}
	acquisition := &artworkPreparationFixture{}
	service := newLibraryPublications(repository, firstPreviewSettings{}, nil, tools.Platform{}).WithPublicationArtwork(acquisition)
	if err := service.prepareSelectionCovers(context.Background(), positions, repository); err != nil {
		t.Fatal(err)
	}
	if acquisition.calls != 3 {
		t.Fatalf("acquisition calls = %d, want one per eligible release including manual authority", acquisition.calls)
	}
	if len(acquisition.releases) != 3 || acquisition.releases[0] != releaseA || acquisition.releases[1] != releaseB || acquisition.releases[2] != releaseC {
		t.Fatalf("acquisition releases = %v", acquisition.releases)
	}
}

func TestPrepareSelectionCoversReportsPendingArtworkAndHonorsCancellation(t *testing.T) {
	releaseA, releaseB := uuid.New(), uuid.New()
	automaticAbsent := persistence.EffectiveReleaseCoverSummary{Revision: 1, Mode: "automatic", State: "absent"}
	positions := []LibraryPublicationSelectionPosition{
		{ReleaseID: releaseA, PositionID: uuid.New()},
		{ReleaseID: releaseB, PositionID: uuid.New()},
	}
	snapshots := map[LibraryPublicationSelectionPosition]*persistence.LibraryFirstPublicationContext{
		positions[0]: firstPublicationCoverSnapshot(releaseA, positions[0].PositionID, automaticAbsent),
		positions[1]: firstPublicationCoverSnapshot(releaseB, positions[1].PositionID, automaticAbsent),
	}
	repository := &selectionCoverPreparationRepository{snapshots: snapshots}

	acquisition := &artworkPreparationFixture{err: errors.New("provider failure")}
	service := newLibraryPublications(repository, firstPreviewSettings{}, nil, tools.Platform{}).WithPublicationArtwork(acquisition)
	if err := service.prepareSelectionCovers(context.Background(), positions, repository); !errors.Is(err, ErrPublicationArtwork) {
		t.Fatalf("pending artwork error = %v, want ErrPublicationArtwork", err)
	}
	if acquisition.calls != 1 {
		t.Fatalf("acquisition calls = %d, want pending artwork to stop the batch", acquisition.calls)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.prepareSelectionCovers(ctx, positions, repository); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want cancellation", err)
	}
}

type selectionCoverPreparationRepository struct {
	firstPreviewRepository
	snapshots map[LibraryPublicationSelectionPosition]*persistence.LibraryFirstPublicationContext
}

func (repository *selectionCoverPreparationRepository) ReadLibraryFirstPublicationContext(_ context.Context, releaseID, positionID uuid.UUID) (*persistence.LibraryFirstPublicationContext, error) {
	return repository.snapshots[LibraryPublicationSelectionPosition{ReleaseID: releaseID, PositionID: positionID}], nil
}
