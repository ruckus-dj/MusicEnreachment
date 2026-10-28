package service

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

func TestAutomaticFirstSourceChoiceUsesOnlySuitableSingleAudioCandidates(t *testing.T) {
	firstLocation, secondLocation := uuid.New(), uuid.New()
	firstVariant, secondVariant := uuid.New(), uuid.New()
	channels := 2
	lossless := false
	snapshot := &persistence.LibraryFirstPublicationContext{Sources: []persistence.FirstPublicationSource{
		{Selection: persistence.PublicationSourceSelection{LocationID: firstLocation, MediaVariantID: firstVariant}, Selected: true, Quality: persistence.PersistedAudioFacts{StreamIndex: 0, Codec: "aac", Lossless: &lossless, Channels: &channels}, ChannelLayout: "stereo", AudioStreamCount: 1},
		{Selection: persistence.PublicationSourceSelection{LocationID: secondLocation, MediaVariantID: secondVariant}, Selected: true, Quality: persistence.PersistedAudioFacts{StreamIndex: 1, Codec: "aac", Lossless: &lossless, Channels: &channels}, ChannelLayout: "stereo", AudioStreamCount: 2, RequiresExplicitStreamSelection: true},
	}}
	choice, err := automaticFirstSourceChoice(snapshot, publication.AudioFormatMKA)
	if err != nil {
		t.Fatalf("automaticFirstSourceChoice() error = %v", err)
	}
	if choice.LocationID != firstLocation || choice.MediaVariantID != firstVariant || choice.StreamIndex != 0 {
		t.Fatalf("selected %#v, want the sole suitable single-stream candidate", choice)
	}
}

func TestAutomaticFirstSourceChoiceUsesEligibleUnselectedSources(t *testing.T) {
	channels := 2
	lossless, lossy := true, false
	bitDepth, sampleRate := 24, 96000
	selected := persistence.FirstPublicationSource{
		Selected: true, Selection: persistence.PublicationSourceSelection{LocationID: uuid.New(), MediaVariantID: uuid.New()},
		Quality: persistence.PersistedAudioFacts{StreamIndex: 0, Codec: "aac", Lossless: &lossy, Channels: &channels}, ChannelLayout: "stereo", AudioStreamCount: 1,
	}
	unselected := persistence.FirstPublicationSource{
		Selected: false, Selection: persistence.PublicationSourceSelection{LocationID: uuid.New(), MediaVariantID: uuid.New()},
		Quality: persistence.PersistedAudioFacts{StreamIndex: 0, Codec: "flac", Lossless: &lossless, Channels: &channels,
			BitDepth: &bitDepth, SampleRate: &sampleRate}, ChannelLayout: "stereo", AudioStreamCount: 1,
	}
	snapshot := &persistence.LibraryFirstPublicationContext{Sources: []persistence.FirstPublicationSource{selected, unselected}, DefaultSourceBlockers: []string{"selected source root unavailable"}}
	choice, err := automaticFirstSourceChoice(snapshot, publication.AudioFormatMKA)
	if err != nil {
		t.Fatalf("automaticFirstSourceChoice() error = %v", err)
	}
	if choice.LocationID != unselected.Selection.LocationID || choice.MediaVariantID != unselected.Selection.MediaVariantID || choice.StreamIndex != 0 {
		t.Fatalf("automatic choice = %#v, want the eligible unselected source proven better by SelectQuality", choice)
	}
	if !snapshot.Sources[0].Selected || snapshot.Sources[1].Selected {
		t.Fatalf("automatic choice mutated persisted association decisions: %#v", snapshot.Sources)
	}
}

type automaticAdmissionServiceRepository struct {
	firstPreviewRepository
	authoritative  *persistence.LibraryFirstPublicationContext
	settings       map[string]string
	installation   *persistence.ToolInstallation
	automaticCalls int
	manualCalls    int
	prepareErr     error
	lastFirst      *persistence.FirstPublicationAdmission
}

func (repository *automaticAdmissionServiceRepository) AdmitAutomaticFirstPublicationFromPreview(_ context.Context, first persistence.FirstPublicationAdmission, guard persistence.PublicationReconciliationDeliveryGuard, _ persistence.RiverInserter) (uuid.UUID, error) {
	repository.automaticCalls++
	repository.lastFirst = &first
	if guard.PositionID != first.PositionID || guard.OutputEpoch != first.ExpectedEpoch {
		return uuid.Nil, fmt.Errorf("mismatched guard")
	}
	_, repository.prepareErr = first.PrepareFirst(context.Background(), persistence.FirstPublicationAdmissionSnapshot{
		Context: repository.authoritative, Settings: repository.settings,
		Installations: []persistence.ToolInstallation{*repository.installation},
	}, first.PublicationAdmission)
	if repository.prepareErr != nil {
		return uuid.Nil, repository.prepareErr
	}
	return uuid.New(), nil
}

func (repository *automaticAdmissionServiceRepository) AdmitFirstPublicationFromPreview(_ context.Context, first persistence.FirstPublicationAdmission, _ persistence.RiverInserter) (uuid.UUID, error) {
	repository.manualCalls++
	repository.lastFirst = &first
	_, err := first.PrepareFirst(context.Background(), persistence.FirstPublicationAdmissionSnapshot{
		Context: repository.authoritative, Settings: repository.settings,
		Installations: []persistence.ToolInstallation{*repository.installation},
	}, first.PublicationAdmission)
	return uuid.New(), err
}

func automaticAdmissionFixture(t *testing.T) (*automaticAdmissionServiceRepository, firstPreviewSettings, uuid.UUID, uuid.UUID, persistence.PublicationReconciliationDeliveryGuard) {
	t.Helper()
	releaseID, positionID, epoch := uuid.New(), uuid.New(), uuid.New()
	installationID := uuid.New()
	verifiedAt := time.Unix(1, 0).UTC()
	installation := &persistence.ToolInstallation{
		ID: installationID, PackageKind: "ffmpeg", PlatformGOOS: "darwin", PlatformGOARCH: "arm64",
		RelativePath: "ffmpeg", State: "ready", VerifiedAt: &verifiedAt,
		Version:                      "ffmpeg 7.0.0 + ffprobe 7.0.0",
		ExecutableVerificationOutput: json.RawMessage(`{"ffmpeg":"ffmpeg version 7.0.0 verified","ffprobe":"ffprobe version 7.0.0 verified"}`),
	}
	makeSource := func(location, variant uuid.UUID, codec string, lossless bool, streamCount int) persistence.FirstPublicationSource {
		channels := 2
		root, work := uuid.New(), uuid.New()
		sourceRoot := t.TempDir()
		mtime := time.Unix(2, 0).UTC()
		source := persistence.FirstPublicationSource{
			Selection: persistence.PublicationSourceSelection{RootID: root, LocationID: location, WorkID: work, MediaVariantID: variant,
				ConfiguredPath: sourceRoot, InventoryPath: sourceRoot, RelativePath: "track.flac", SizeBytes: 10, MTime: mtime},
			AssociationID: uuid.New(), Selected: true,
			Input: persistence.PublicationAudioInput{RootID: root, LocationID: location, WorkID: work, MediaVariantID: variant,
				ProbeResultID: uuid.New(), ConfiguredRootPath: sourceRoot, InventoryRootPath: sourceRoot, RelativePath: "track.flac", SizeBytes: 10,
				MTime: mtime, AnalysisPolicyVersion: 1, FFProbeVersion: "7.0.0"},
			Quality: persistence.PersistedAudioFacts{StreamIndex: 0, Codec: codec, Lossless: &lossless, Channels: &channels,
				Container: publication.AudioContainerFLAC, SupportedSourceContainer: true},
			ChannelLayout:    "stereo",
			AudioStreamCount: streamCount, RequiresExplicitStreamSelection: streamCount != 1,
		}
		source.Input.AudioFacts = source.Quality
		return source
	}
	first := makeSource(uuid.New(), uuid.New(), "flac", true, 1)
	second := makeSource(uuid.New(), uuid.New(), "aac", false, 1)
	makeSnapshot := func(sources []persistence.FirstPublicationSource) *persistence.LibraryFirstPublicationContext {
		associations := make([]persistence.PublicationSourceAssociation, 0, len(sources))
		for _, source := range sources {
			associations = append(associations, persistence.PublicationSourceAssociation{
				ID: source.AssociationID, TrackID: uuid.New(), MediaVariantID: source.Selection.MediaVariantID,
				Selected: source.Selected, Compatible: true, DecisionMethod: "manual", Metadata: json.RawMessage(`{}`), UpdatedAt: time.Unix(3, 0).UTC(),
			})
		}
		return &persistence.LibraryFirstPublicationContext{
			Library:        &persistence.LibraryPublicationContext{ReleaseID: releaseID, PositionID: positionID, ActiveEpoch: epoch},
			TemplateTokens: map[string]string{"ALBUM_PRIMARY_ARTIST": "Artist", "TRACK_NAME": "Track"},
			Tags: publication.TagProjectionInput{Release: publication.ReleaseTags{Title: "Album", Artist: []librarymetadata.ArtistCredit{{ArtistID: uuid.NewString(), ArtistName: "Artist"}}},
				Position: publication.PositionTags{Title: "Track", Artist: []librarymetadata.ArtistCredit{{ArtistID: uuid.NewString(), ArtistName: "Artist"}}}},
			ReleasePrimaryCredits: []librarymetadata.ArtistCredit{{ArtistID: uuid.NewString(), ArtistName: "Artist"}}, Sources: sources, SourceAssociations: associations,
		}
	}
	base := makeSnapshot([]persistence.FirstPublicationSource{first, second})
	outputDir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(outputDir); err == nil {
		outputDir = resolved
	}
	runtime := firstPreviewSettings{format: "mka", template: "{TRACK_NAME}.{EXT}", output: outputDir, caseSensitive: true, ffmpeg: installationID.String(), fpcalc: uuid.Nil.String()}
	repository := &automaticAdmissionServiceRepository{
		firstPreviewRepository: firstPreviewRepository{snapshot: base}, authoritative: base, installation: installation,
		settings: map[string]string{settings.OutputDirectoryKey: runtime.output, settings.PublicationFormatKey: "mka",
			settings.PublicationPathTemplateKey: runtime.template, settings.OutputCaseSensitiveKey: "true",
			settings.ActiveFFmpegInstallationKey: installationID.String()},
	}
	guard := persistence.PublicationReconciliationDeliveryGuard{OperationID: uuid.New(), PositionID: positionID, OutputEpoch: epoch, ClaimToken: uuid.New(), RiverJobID: 1, RiverAttempt: 1, ApplicationAttempt: 1, DeliveryEpoch: 1}
	return repository, runtime, releaseID, positionID, guard
}

func TestAdmitAutomaticFirstPublicationUsesFinalizedBindingAndAuthoritativeWinner(t *testing.T) {
	t.Run("successful admission", func(t *testing.T) {
		repository, runtime, releaseID, positionID, guard := automaticAdmissionFixture(t)
		service := newLibraryPublications(repository, runtime, libraryPublicationsTestRiver{}, tools.Platform{GOOS: "darwin", GOARCH: "arm64"})
		if _, err := service.admitAutomaticFirstPublication(context.Background(), releaseID, positionID, guard); err != nil {
			t.Fatalf("automatic admission: %v", err)
		}
		if repository.automaticCalls != 1 || repository.manualCalls != 0 {
			t.Fatalf("automatic/manual calls = %d/%d", repository.automaticCalls, repository.manualCalls)
		}
		if repository.lastFirst == nil || repository.lastFirst.SourceSelection == nil {
			t.Fatal("automatic admission omitted source-selection witness")
		}
		selection := repository.lastFirst.SourceSelection
		if !reflect.DeepEqual(selection.Associations, repository.snapshot.SourceAssociations) {
			t.Fatal("automatic admission omitted complete association witness")
		}
		frozen, err := persistence.DecodePublicationInputs(repository.lastFirst.FrozenInputs)
		if err != nil || frozen.SourceAuthorityMode != "automatic" {
			t.Fatalf("automatic source authority missing: mode=%s err=%v", frozen.SourceAuthorityMode, err)
		}
	})
	t.Run("authoritative settings change winner", func(t *testing.T) {
		repository, runtime, releaseID, positionID, guard := automaticAdmissionFixture(t)
		// Under M4A the lossless FLAC candidate is unsuitable without verified
		// ALAC capability; the AAC candidate becomes the winner instead.
		repository.settings[settings.PublicationFormatKey] = "m4a"
		service := newLibraryPublications(repository, runtime, libraryPublicationsTestRiver{}, tools.Platform{GOOS: "darwin", GOARCH: "arm64"})
		if _, err := service.admitAutomaticFirstPublication(context.Background(), releaseID, positionID, guard); err == nil {
			t.Fatal("authoritative settings/winner change was accepted")
		}
		if repository.automaticCalls != 1 {
			t.Fatalf("automatic calls = %d, want 1", repository.automaticCalls)
		}
	})
}

func TestManualFirstAdmissionRemainsOnPublicConfirmationPath(t *testing.T) {
	repository, runtime, releaseID, positionID, guard := automaticAdmissionFixture(t)
	service := newLibraryPublications(repository, runtime, libraryPublicationsTestRiver{}, tools.Platform{GOOS: "darwin", GOARCH: "arm64"})
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
	if err != nil {
		t.Fatalf("manual admission: %v", err)
	}
	if repository.manualCalls != 1 || repository.automaticCalls != 0 {
		t.Fatalf("manual/automatic calls = %d/%d", repository.manualCalls, repository.automaticCalls)
	}
	if repository.lastFirst == nil || repository.lastFirst.SourceSelection == nil || repository.lastFirst.SourceSelection.AssociationID != preview.SelectedSource.AssociationID {
		t.Fatalf("explicit manual source intent = %#v", repository.lastFirst)
	}
}

func TestAutomaticFirstSourceChoiceRequiresUnambiguousQualityWinner(t *testing.T) {
	makeSource := func(channelCount int) persistence.FirstPublicationSource {
		channels := channelCount
		lossless := false
		return persistence.FirstPublicationSource{Selection: persistence.PublicationSourceSelection{LocationID: uuid.New(), MediaVariantID: uuid.New()}, Selected: true, Quality: persistence.PersistedAudioFacts{StreamIndex: 0, Codec: "aac", Lossless: &lossless, Channels: &channels}, ChannelLayout: "stereo", AudioStreamCount: 1}
	}
	snapshot := &persistence.LibraryFirstPublicationContext{Sources: []persistence.FirstPublicationSource{makeSource(2), makeSource(6)}}
	if _, err := automaticFirstSourceChoice(snapshot, publication.AudioFormatMKA); err == nil {
		t.Fatal("automaticFirstSourceChoice() accepted tied quality candidates")
	}
}
