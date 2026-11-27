package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type artworkPreparationFixture struct {
	calls    int
	releases []uuid.UUID
	err      error
}

func (fixture *artworkPreparationFixture) PrepareForPublication(ctx context.Context, releaseID uuid.UUID) error {
	fixture.calls++
	fixture.releases = append(fixture.releases, releaseID)
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return fixture.err
}

func TestPrepareFirstCoverUsesNarrowPrimaryArtworkContract(t *testing.T) {
	releaseID, positionID := uuid.New(), uuid.New()
	repository := &coverPreparationRepository{snapshots: []*persistence.LibraryFirstPublicationContext{
		firstPublicationCoverSnapshot(releaseID, positionID, persistence.EffectiveReleaseCoverSummary{Revision: 1, Mode: "automatic", State: "absent"}),
		refreshedCoverSnapshot(releaseID, positionID),
	}}
	artwork := &artworkPreparationFixture{}
	service := newLibraryPublications(repository, firstPreviewSettings{}, nil, tools.Platform{}).WithPublicationArtwork(artwork)
	if err := service.PrepareFirstCover(context.Background(), releaseID, positionID); err != nil {
		t.Fatal(err)
	}
	if artwork.calls != 1 || len(artwork.releases) != 1 || artwork.releases[0] != releaseID {
		t.Fatalf("artwork calls/releases = %d/%v", artwork.calls, artwork.releases)
	}
}

func TestPrepareFirstCoverReportsPendingPrimaryArtwork(t *testing.T) {
	releaseID, positionID := uuid.New(), uuid.New()
	repository := &coverPreparationRepository{snapshots: []*persistence.LibraryFirstPublicationContext{
		firstPublicationCoverSnapshot(releaseID, positionID, persistence.EffectiveReleaseCoverSummary{Revision: 1, Mode: "automatic", State: "absent"}),
	}}
	artwork := &artworkPreparationFixture{err: ErrProviderArtworkPending}
	service := newLibraryPublications(repository, firstPreviewSettings{}, nil, tools.Platform{}).WithPublicationArtwork(artwork)
	err := service.PrepareFirstCover(context.Background(), releaseID, positionID)
	if !errors.Is(err, ErrPublicationArtwork) || !errors.Is(err, ErrProviderArtworkPending) {
		t.Fatalf("error = %v, want pending artwork classification", err)
	}
	status := PublicationArtworkStatusFromError(err)
	if status.State != LibraryPublicationArtworkStatePending || !status.Retryable {
		t.Fatalf("status = %#v, want retryable pending", status)
	}
}

func TestPrepareFirstCoverSkipsCurrentAndActiveAttempt(t *testing.T) {
	releaseID := uuid.New()
	tests := []struct {
		name     string
		snapshot *persistence.LibraryFirstPublicationContext
	}{
		{name: "current", snapshot: func() *persistence.LibraryFirstPublicationContext {
			snapshot := firstPublicationCoverSnapshot(releaseID, uuid.New(), persistence.EffectiveReleaseCoverSummary{Revision: 1, Mode: "automatic", State: "absent"})
			snapshot.Library.Current = &persistence.LibraryPublicationCurrent{}
			return snapshot
		}()},
		{name: "active attempt", snapshot: func() *persistence.LibraryFirstPublicationContext {
			snapshot := firstPublicationCoverSnapshot(releaseID, uuid.New(), persistence.EffectiveReleaseCoverSummary{Revision: 1, Mode: "automatic", State: "absent"})
			snapshot.Library.ActiveAttempt = &persistence.LibraryPublicationOperation{}
			return snapshot
		}()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &coverPreparationRepository{snapshots: []*persistence.LibraryFirstPublicationContext{test.snapshot}}
			artwork := &artworkPreparationFixture{}
			service := newLibraryPublications(repository, firstPreviewSettings{}, nil, tools.Platform{}).WithPublicationArtwork(artwork)
			if err := service.PrepareFirstCover(context.Background(), releaseID, test.snapshot.Library.PositionID); err != nil {
				t.Fatal(err)
			}
			if artwork.calls != 0 {
				t.Fatalf("CURRENT/active preparation reached primary artwork %d times", artwork.calls)
			}
		})
	}
}

func TestAdmitFirstPreparesArtworkBeforePlanning(t *testing.T) {
	repository, runtime, releaseID, positionID, guard := automaticAdmissionFixture(t)
	artwork := &artworkPreparationFixture{}
	service := newLibraryPublications(repository, runtime, libraryPublicationsTestRiver{}, tools.Platform{GOOS: "darwin", GOARCH: "arm64"}).WithPublicationArtwork(artwork)
	choice, err := automaticFirstSourceChoice(repository.snapshot, publication.AudioFormatMKA)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := buildFirstPublicationPreviewSelected(repository.snapshot, settings.RuntimeSettings{
		PublicationFormat: runtime.format, PublicationPathTemplate: runtime.template, OutputDirectory: runtime.output,
		OutputCaseSensitive: &runtime.caseSensitive, ActiveFFmpegInstallation: runtime.ffmpeg, ActiveFPCalcInstallation: runtime.fpcalc,
	}, []persistence.SourceAnalysisToolSelection{{PackageKind: "ffmpeg", InstallationID: repository.installation.ID, RelativePath: "ffmpeg", Executable: "ffmpeg", Version: "ffmpeg 7.0.0 + ffprobe 7.0.0", VersionBanner: "ffmpeg version 7.0.0 verified"},
		{PackageKind: "ffmpeg", InstallationID: repository.installation.ID, RelativePath: "ffmpeg", Executable: "ffprobe", Version: "ffmpeg 7.0.0 + ffprobe 7.0.0", VersionBanner: "ffprobe version 7.0.0 verified"}}, releaseID, positionID, &choice)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := finalizeFirstPublicationNamespace(context.Background(), repository.snapshot, runtime.output, runtime.template, &preview); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AdmitFirst(context.Background(), releaseID, positionID, FirstPublicationConfirmation{PreviewDigest: preview.ContextDigest, ExpectedEpoch: guard.OutputEpoch, SourceChoice: &choice}); err != nil {
		t.Fatalf("admission: %v", err)
	}
	if artwork.calls != 1 || artwork.releases[0] != releaseID {
		t.Fatalf("artwork calls/releases = %d/%v", artwork.calls, artwork.releases)
	}
}

func TestAdmitFirstBlocksOnPendingArtworkBeforeAdmission(t *testing.T) {
	repository, runtime, releaseID, positionID, guard := automaticAdmissionFixture(t)
	artwork := &artworkPreparationFixture{err: ErrProviderArtworkPending}
	service := newLibraryPublications(repository, runtime, libraryPublicationsTestRiver{}, tools.Platform{GOOS: "darwin", GOARCH: "arm64"}).WithPublicationArtwork(artwork)
	choice, err := automaticFirstSourceChoice(repository.snapshot, publication.AudioFormatMKA)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := buildFirstPublicationPreviewSelected(repository.snapshot, settings.RuntimeSettings{
		PublicationFormat: runtime.format, PublicationPathTemplate: runtime.template, OutputDirectory: runtime.output,
		OutputCaseSensitive: &runtime.caseSensitive, ActiveFFmpegInstallation: runtime.ffmpeg, ActiveFPCalcInstallation: runtime.fpcalc,
	}, []persistence.SourceAnalysisToolSelection{{PackageKind: "ffmpeg", InstallationID: repository.installation.ID, RelativePath: "ffmpeg", Executable: "ffmpeg", Version: "ffmpeg 7.0.0 + ffprobe 7.0.0", VersionBanner: "ffmpeg version 7.0.0 verified"},
		{PackageKind: "ffmpeg", InstallationID: repository.installation.ID, RelativePath: "ffmpeg", Executable: "ffprobe", Version: "ffmpeg 7.0.0 + ffprobe 7.0.0", VersionBanner: "ffprobe version 7.0.0 verified"}}, releaseID, positionID, &choice)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.AdmitFirst(context.Background(), releaseID, positionID, FirstPublicationConfirmation{PreviewDigest: preview.ContextDigest, ExpectedEpoch: guard.OutputEpoch, SourceChoice: &choice})
	if !errors.Is(err, ErrPublicationArtwork) {
		t.Fatalf("admission error = %v, want ErrPublicationArtwork", err)
	}
	if repository.manualCalls != 0 {
		t.Fatalf("pending artwork still reached admission %d times", repository.manualCalls)
	}
}

func TestAdmitFirstRejectsReleaseRelinkAfterArtworkPreparation(t *testing.T) {
	repository, runtime, releaseID, positionID, guard := automaticAdmissionFixture(t)
	oldMBID, newMBID := uuid.New(), uuid.New()
	repository.snapshot.Tags.Release.ID = &oldMBID
	authoritative := *repository.snapshot
	authoritative.Tags.Release.ID = &newMBID
	repository.authoritative = &authoritative
	artwork := &artworkPreparationFixture{}
	service := newLibraryPublications(repository, runtime, libraryPublicationsTestRiver{}, tools.Platform{GOOS: "darwin", GOARCH: "arm64"}).WithPublicationArtwork(artwork)
	choice, err := automaticFirstSourceChoice(repository.snapshot, publication.AudioFormatMKA)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := buildFirstPublicationPreviewSelected(repository.snapshot, settings.RuntimeSettings{
		PublicationFormat: runtime.format, PublicationPathTemplate: runtime.template, OutputDirectory: runtime.output,
		OutputCaseSensitive: &runtime.caseSensitive, ActiveFFmpegInstallation: runtime.ffmpeg, ActiveFPCalcInstallation: runtime.fpcalc,
	}, []persistence.SourceAnalysisToolSelection{{PackageKind: "ffmpeg", InstallationID: repository.installation.ID, RelativePath: "ffmpeg", Executable: "ffmpeg", Version: "ffmpeg 7.0.0 + ffprobe 7.0.0", VersionBanner: "ffmpeg version 7.0.0 verified"},
		{PackageKind: "ffmpeg", InstallationID: repository.installation.ID, RelativePath: "ffmpeg", Executable: "ffprobe", Version: "ffmpeg 7.0.0 + ffprobe 7.0.0", VersionBanner: "ffprobe version 7.0.0 verified"}}, releaseID, positionID, &choice)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := finalizeFirstPublicationNamespace(context.Background(), repository.snapshot, runtime.output, runtime.template, &preview); err != nil {
		t.Fatal(err)
	}
	_, err = service.AdmitFirst(context.Background(), releaseID, positionID, FirstPublicationConfirmation{PreviewDigest: preview.ContextDigest, ExpectedEpoch: guard.OutputEpoch, SourceChoice: &choice})
	if !errors.Is(err, ErrPublicationArtwork) {
		t.Fatalf("relinked admission error = %v, want ErrPublicationArtwork", err)
	}
}

type relinkingArtworkFixture struct {
	repository *automaticAdmissionServiceRepository
	mbid       uuid.UUID
}

func (fixture *relinkingArtworkFixture) PrepareForPublication(context.Context, uuid.UUID) error {
	fixture.repository.snapshot.Tags.Release.ID = &fixture.mbid
	return nil
}

func TestAdmitFirstRejectsRelinkDuringArtworkPreparation(t *testing.T) {
	repository, runtime, releaseID, positionID, guard := automaticAdmissionFixture(t)
	oldMBID, newMBID := uuid.New(), uuid.New()
	repository.snapshot.Tags.Release.ID = &oldMBID
	artwork := &relinkingArtworkFixture{repository: repository, mbid: newMBID}
	service := newLibraryPublications(repository, runtime, libraryPublicationsTestRiver{}, tools.Platform{GOOS: "darwin", GOARCH: "arm64"}).WithPublicationArtwork(artwork)
	choice, err := automaticFirstSourceChoice(repository.snapshot, publication.AudioFormatMKA)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := buildFirstPublicationPreviewSelected(repository.snapshot, settings.RuntimeSettings{
		PublicationFormat: runtime.format, PublicationPathTemplate: runtime.template, OutputDirectory: runtime.output,
		OutputCaseSensitive: &runtime.caseSensitive, ActiveFFmpegInstallation: runtime.ffmpeg, ActiveFPCalcInstallation: runtime.fpcalc,
	}, []persistence.SourceAnalysisToolSelection{{PackageKind: "ffmpeg", InstallationID: repository.installation.ID, RelativePath: "ffmpeg", Executable: "ffmpeg", Version: "ffmpeg 7.0.0 + ffprobe 7.0.0", VersionBanner: "ffmpeg version 7.0.0 verified"},
		{PackageKind: "ffmpeg", InstallationID: repository.installation.ID, RelativePath: "ffmpeg", Executable: "ffprobe", Version: "ffmpeg 7.0.0 + ffprobe 7.0.0", VersionBanner: "ffprobe version 7.0.0 verified"}}, releaseID, positionID, &choice)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := finalizeFirstPublicationNamespace(context.Background(), repository.snapshot, runtime.output, runtime.template, &preview); err != nil {
		t.Fatal(err)
	}
	_, err = service.AdmitFirst(context.Background(), releaseID, positionID, FirstPublicationConfirmation{PreviewDigest: preview.ContextDigest, ExpectedEpoch: guard.OutputEpoch, SourceChoice: &choice})
	if !errors.Is(err, ErrPublicationArtwork) || !strings.Contains(err.Error(), "during artwork preparation") {
		t.Fatalf("relinked-during-prep error = %v, want artwork scope rejection", err)
	}
	if repository.manualCalls != 0 {
		t.Fatalf("relinked-during-prep admission reached publication %d times", repository.manualCalls)
	}
}

func TestAdmitAutomaticAlbumPropagatesPendingArtwork(t *testing.T) {
	repository, runtime, releaseID, positionID, guard := automaticAdmissionFixture(t)
	repository.snapshot.EffectiveCover = persistence.EffectiveReleaseCoverSummary{Revision: 1, Mode: "automatic", State: "absent"}
	repository.authoritative.EffectiveCover = repository.snapshot.EffectiveCover
	repository.snapshot.Sources[0].Selected = true
	repository.snapshot.Sources[1].Selected = false
	for index := range repository.snapshot.SourceAssociations {
		repository.snapshot.SourceAssociations[index].DecisionMethod = "automatic"
		repository.snapshot.SourceAssociations[index].Selected = index == 0
	}
	request := persistence.PublicationAutoAlbumRequest{
		OutputEpoch: guard.OutputEpoch, ReleaseID: releaseID,
		Scope: []persistence.PublicationAutoAlbumPosition{{PositionID: positionID, WorkIDs: []uuid.UUID{repository.snapshot.Sources[0].Input.WorkID}}},
	}
	artwork := &artworkPreparationFixture{err: ErrProviderArtworkPending}
	service := newLibraryPublications(repository, runtime, libraryPublicationsTestRiver{}, tools.Platform{GOOS: "darwin", GOARCH: "arm64"}).WithPublicationArtwork(artwork)
	err := service.AdmitAutomaticAlbum(context.Background(), request)
	if !errors.Is(err, ErrPublicationArtwork) {
		t.Fatalf("automatic album error = %v, want ErrPublicationArtwork", err)
	}
	if artwork.calls != 1 {
		t.Fatalf("automatic album artwork calls = %d", artwork.calls)
	}
}

func TestPublicationArtworkStatusContract(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		state     string
		retryable bool
	}{
		{name: "ready", err: nil, state: LibraryPublicationArtworkStateReady},
		{name: "pending", err: errors.Join(ErrPublicationArtwork, ErrProviderArtworkPending), state: LibraryPublicationArtworkStatePending, retryable: true},
		{name: "unavailable", err: ErrPublicationArtwork, state: LibraryPublicationArtworkStateUnavailable, retryable: true},
		{name: "failure", err: errors.New("boom"), state: LibraryPublicationArtworkStateUnavailable, retryable: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status := PublicationArtworkStatusFromError(test.err)
			if status.State != test.state || status.Retryable != test.retryable {
				t.Fatalf("status = %#v, want state=%s retryable=%t", status, test.state, test.retryable)
			}
		})
	}
}
