package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type firstPreviewRepository struct {
	snapshot *persistence.LibraryFirstPublicationContext
	version  string
}

func (repository firstPreviewRepository) ReadLibraryPublicationContext(context.Context, uuid.UUID, uuid.UUID) (*persistence.LibraryPublicationContext, error) {
	return repository.snapshot.Library, nil
}
func (repository firstPreviewRepository) GetInstallation(_ context.Context, id uuid.UUID) (*persistence.ToolInstallation, error) {
	version := repository.version
	if version == "" {
		version = "7.0.0"
	}
	verifiedAt := time.Unix(1, 0).UTC()
	return &persistence.ToolInstallation{
		ID: id, PackageKind: "ffmpeg", PlatformGOOS: "darwin", PlatformGOARCH: "arm64",
		RelativePath: "ffmpeg", State: "ready", VerifiedAt: &verifiedAt,
		Version:                      "ffmpeg " + version + " + ffprobe " + version,
		ExecutableVerificationOutput: json.RawMessage(fmt.Sprintf(`{"ffmpeg":"ffmpeg version %s verified","ffprobe":"ffprobe version %s verified"}`, version, version)),
	}, nil
}
func (firstPreviewRepository) AdmitManualLyricsRewrite(context.Context, persistence.ManualLyricsAdmission, persistence.RiverInserter) (uuid.UUID, error) {
	panic("preview must not admit publication")
}
func (repository firstPreviewRepository) ReadLibraryFirstPublicationContext(context.Context, uuid.UUID, uuid.UUID) (*persistence.LibraryFirstPublicationContext, error) {
	return repository.snapshot, nil
}

type firstPreviewSettings struct {
	format, template, output, unicode, ffmpeg, fpcalc string
	caseSensitive                                     bool
	reads                                             *int
}

func (runtime firstPreviewSettings) ReadRuntimeSettings(context.Context) (settings.RuntimeSettings, error) {
	return settings.RuntimeSettings{}, nil
}
func (runtime firstPreviewSettings) ReadPublicationPlanningSettings(context.Context) (settings.RuntimeSettings, error) {
	if runtime.reads != nil {
		*runtime.reads++
	}
	return settings.RuntimeSettings{
		PublicationFormat: runtime.format, PublicationPathTemplate: runtime.template,
		OutputDirectory: runtime.output, OutputCaseSensitive: &runtime.caseSensitive,
		OutputUnicodeNormalization: runtime.unicode, ActiveFFmpegInstallation: runtime.ffmpeg,
		ActiveFPCalcInstallation: runtime.fpcalc,
	}, nil
}

func TestPreviewFirstPlansExactSourceAndRussianPath(t *testing.T) {
	releaseID, positionID, epoch, rootID, locationID, workID, variantID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	artistID := uuid.New()
	lossless := true
	snapshot := &persistence.LibraryFirstPublicationContext{
		Library:        &persistence.LibraryPublicationContext{ReleaseID: releaseID, PositionID: positionID, ActiveEpoch: epoch},
		TemplateTokens: map[string]string{"ALBUM_PRIMARY_ARTIST": "Звери", "TRACK_NUMBER": "01", "TRACK_NAME": "Районы-кварталы"},
		Tags: publication.TagProjectionInput{
			Release:  publication.ReleaseTags{Title: "Дальше", Artist: []librarymetadata.ArtistCredit{{ArtistID: artistID.String(), ArtistName: "Звери"}}},
			Position: publication.PositionTags{Title: "Районы-кварталы", Artist: []librarymetadata.ArtistCredit{{ArtistID: uuid.NewString(), ArtistName: "Звери"}}},
		},
		ReleasePrimaryCredits: []librarymetadata.ArtistCredit{{ArtistID: artistID.String(), ArtistName: "Звери"}},
		Sources: []persistence.FirstPublicationSource{{
			Selection: persistence.PublicationSourceSelection{RootID: rootID, LocationID: locationID, WorkID: workID, MediaVariantID: variantID}, Selected: true,
			Quality: persistence.PersistedAudioFacts{StreamIndex: 2, Codec: "flac", Lossless: &lossless, Container: publication.AudioContainerFLAC, SupportedSourceContainer: true},
		}},
	}
	settingsReadCount := 0
	service := newLibraryPublications(firstPreviewRepository{snapshot: snapshot}, firstPreviewSettings{format: "mka", template: "{ALBUM_PRIMARY_ARTIST} - {TRACK_NUMBER} - {TRACK_NAME}.{EXT}", caseSensitive: true, ffmpeg: "7f6ec99d-7d5d-43c2-9392-6db32ed4bd42", reads: &settingsReadCount}, nil, tools.Platform{GOOS: "darwin", GOARCH: "arm64"})
	preview, err := service.PreviewFirst(context.Background(), releaseID, positionID)
	if err != nil {
		t.Fatal(err)
	}
	if preview.SelectedSource == nil || preview.SelectedSource.MediaVariantID != variantID || preview.SelectedSource.StreamIndex != 2 {
		t.Fatalf("selected source = %#v", preview.SelectedSource)
	}
	if preview.Transform == nil || preview.Transform.Action != publication.AudioCodecCopy || preview.Transform.Extension != "mka" {
		t.Fatalf("transform = %#v", preview.Transform)
	}
	want := []string{"Звери - 01 - Районы-кварталы.mka"}
	if len(preview.PathComponents) != len(want) || preview.PathComponents[0] != want[0] {
		t.Fatalf("path components = %#v", preview.PathComponents)
	}
	if preview.ContextDigest == "" || preview.NativeStatus != "native verification required" || len(preview.Blockers) != 0 {
		t.Fatalf("preview state = digest %q status %q blockers %#v", preview.ContextDigest, preview.NativeStatus, preview.Blockers)
	}
	if settingsReadCount != 1 {
		t.Fatalf("planning settings reads = %d, want one coherent snapshot", settingsReadCount)
	}
	changedToolPreview, err := newLibraryPublications(firstPreviewRepository{snapshot: snapshot, version: "8.0.0"}, firstPreviewSettings{
		format: "mka", template: "{ALBUM_PRIMARY_ARTIST} - {TRACK_NUMBER} - {TRACK_NAME}.{EXT}",
		caseSensitive: true, ffmpeg: "7f6ec99d-7d5d-43c2-9392-6db32ed4bd42",
	}, nil, tools.Platform{GOOS: "darwin", GOARCH: "arm64"}).PreviewFirst(context.Background(), releaseID, positionID)
	if err != nil {
		t.Fatal(err)
	}
	if changedToolPreview.ContextDigest == preview.ContextDigest {
		t.Fatal("digest did not bind selected FFmpeg executable versions")
	}
	for _, changed := range []firstPreviewSettings{
		{format: "mka", template: "{ALBUM_PRIMARY_ARTIST}/{TRACK_NUMBER} - {TRACK_NAME}", output: "/other", caseSensitive: true},
		{format: "mka", template: "{ALBUM_PRIMARY_ARTIST}/{TRACK_NUMBER} - {TRACK_NAME}", unicode: "NFC", caseSensitive: true},
		{format: "mka", template: "{ALBUM_PRIMARY_ARTIST}/{TRACK_NUMBER} - {TRACK_NAME}", caseSensitive: false},
	} {
		changedPreview, err := newLibraryPublications(firstPreviewRepository{snapshot: snapshot}, changed, nil, tools.Platform{GOOS: "darwin", GOARCH: "arm64"}).PreviewFirst(context.Background(), releaseID, positionID)
		if err != nil {
			t.Fatal(err)
		}
		if changedPreview.ContextDigest == preview.ContextDigest {
			t.Fatalf("digest did not bind planning settings %#v", changed)
		}
	}
}

func TestPreviewFirstBlocksAmbiguousQualityAndExistingCurrent(t *testing.T) {
	releaseID, positionID, epoch := uuid.New(), uuid.New(), uuid.New()
	known := true
	snapshot := &persistence.LibraryFirstPublicationContext{
		Library: &persistence.LibraryPublicationContext{ReleaseID: releaseID, PositionID: positionID, ActiveEpoch: epoch, Current: &persistence.LibraryPublicationCurrent{}},
		Sources: []persistence.FirstPublicationSource{
			{Selection: persistence.PublicationSourceSelection{LocationID: uuid.New(), MediaVariantID: uuid.New()}, Selected: true, Quality: persistence.PersistedAudioFacts{Codec: "flac", StreamIndex: 0, Lossless: &known}},
			{Selection: persistence.PublicationSourceSelection{LocationID: uuid.New(), MediaVariantID: uuid.New()}, Selected: true, Quality: persistence.PersistedAudioFacts{Codec: "flac", StreamIndex: 0, Lossless: &known}},
		},
	}
	service := newLibraryPublications(firstPreviewRepository{snapshot: snapshot}, firstPreviewSettings{format: "mka", template: "Artist/Track", caseSensitive: true}, nil, tools.Platform{})
	preview, err := service.PreviewFirst(context.Background(), releaseID, positionID)
	if err != nil {
		t.Fatal(err)
	}
	if preview.SelectedSource != nil {
		t.Fatalf("ambiguous candidates selected arbitrarily: %#v", preview.SelectedSource)
	}
	if len(preview.Blockers) < 2 {
		t.Fatalf("expected ambiguity and current-publication blockers, got %#v", preview.Blockers)
	}
}

func TestPreviewFirstRequiresAndBindsExplicitSourceStreamChoice(t *testing.T) {
	releaseID, positionID := uuid.New(), uuid.New()
	location1, variant1, location2, variant2 := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	known := true
	settings := firstPreviewSettings{format: "mka", template: "Artist/Track", caseSensitive: true}
	makeService := func(sources []persistence.FirstPublicationSource) *LibraryPublications {
		return newLibraryPublications(firstPreviewRepository{snapshot: &persistence.LibraryFirstPublicationContext{
			Library: &persistence.LibraryPublicationContext{ReleaseID: releaseID, PositionID: positionID, ActiveEpoch: uuid.New()}, Sources: sources,
		}}, settings, nil, tools.Platform{GOOS: "darwin", GOARCH: "arm64"})
	}
	stream := func(location, variant uuid.UUID, index int, title string) persistence.FirstPublicationSource {
		return persistence.FirstPublicationSource{Selection: persistence.PublicationSourceSelection{LocationID: location, MediaVariantID: variant}, Selected: true,
			Quality: persistence.PersistedAudioFacts{Codec: "flac", StreamIndex: index, Lossless: &known, Container: publication.AudioContainerFLAC, SupportedSourceContainer: true}, StreamTitle: title, StreamLanguage: "eng"}
	}
	// Multiple verified source files are never selected without an explicit choice.
	service := makeService([]persistence.FirstPublicationSource{stream(location1, variant1, 0, "main"), stream(location2, variant2, 0, "alternate")})
	withoutChoice, err := service.PreviewFirst(context.Background(), releaseID, positionID)
	if err != nil {
		t.Fatal(err)
	}
	if withoutChoice.SelectedSource != nil || len(withoutChoice.SourceCandidates) != 2 {
		t.Fatalf("implicit multi-source selection: %#v", withoutChoice)
	}
	choice := &FirstPublicationSourceChoice{LocationID: location2, MediaVariantID: variant2, StreamIndex: 0}
	selected, err := service.PreviewFirstSelected(context.Background(), releaseID, positionID, choice)
	if err != nil {
		t.Fatal(err)
	}
	if selected.SelectedSource == nil || selected.SelectedSource.LocationID != location2 || selected.SelectedSource.Title != "alternate" {
		t.Fatalf("selected source = %#v", selected.SelectedSource)
	}
	if selected.ContextDigest == withoutChoice.ContextDigest {
		t.Fatal("selection did not bind digest")
	}
	// Two streams in one file are also separate choices; neither is implicit.
	multiStreamService := makeService([]persistence.FirstPublicationSource{stream(location1, variant1, 0, "English"), stream(location1, variant1, 1, "Commentary")})
	multiStream, err := multiStreamService.PreviewFirst(context.Background(), releaseID, positionID)
	if err != nil {
		t.Fatal(err)
	}
	if multiStream.SelectedSource != nil || len(multiStream.SourceCandidates) != 2 {
		t.Fatalf("implicit multistream selection: %#v", multiStream)
	}
	streamChoice := &FirstPublicationSourceChoice{LocationID: location1, MediaVariantID: variant1, StreamIndex: 1}
	selectedStream, err := multiStreamService.PreviewFirstSelected(context.Background(), releaseID, positionID, streamChoice)
	if err != nil {
		t.Fatal(err)
	}
	if selectedStream.SelectedSource == nil || selectedStream.SelectedSource.StreamIndex != 1 {
		t.Fatalf("stream selection = %#v", selectedStream.SelectedSource)
	}
	if _, err := multiStreamService.PreviewFirstSelected(context.Background(), releaseID, positionID, &FirstPublicationSourceChoice{LocationID: location1, MediaVariantID: uuid.New(), StreamIndex: 1}); err == nil {
		t.Fatal("stale/substituted source choice was accepted")
	}
	if _, err := multiStreamService.PreviewFirstSelected(context.Background(), releaseID, positionID, &FirstPublicationSourceChoice{LocationID: location1, MediaVariantID: variant1, StreamIndex: 7}); err == nil {
		t.Fatal("substituted stream index was accepted")
	}
	// A second raw audio stream may be unsupported and therefore absent from
	// SourceCandidates. Its presence still requires explicit user confirmation.
	validOnly := stream(location1, variant1, 2, "supported stream")
	validOnly.RequiresExplicitStreamSelection = true
	validOnly.AudioStreamCount = 2
	validOnly.UnsupportedAudioStreamCount = 1
	unsupportedService := makeService([]persistence.FirstPublicationSource{validOnly})
	implicitSupported, err := unsupportedService.PreviewFirst(context.Background(), releaseID, positionID)
	if err != nil {
		t.Fatal(err)
	}
	if implicitSupported.SelectedSource != nil || len(implicitSupported.SourceCandidates) != 1 || !implicitSupported.SourceCandidates[0].RequiresExplicitStreamSelection || implicitSupported.SourceCandidates[0].UnsupportedAudioStreamCount != 1 {
		t.Fatalf("unsupported second stream was hidden by unique eligible stream: %#v", implicitSupported)
	}
	explicitSupported, err := unsupportedService.PreviewFirstSelected(context.Background(), releaseID, positionID, &FirstPublicationSourceChoice{LocationID: location1, MediaVariantID: variant1, StreamIndex: 2})
	if err != nil || explicitSupported.SelectedSource == nil || explicitSupported.SelectedSource.StreamIndex != 2 {
		t.Fatalf("explicit supported stream choice = %#v, %v", explicitSupported.SelectedSource, err)
	}
}

func TestPreviewFirstUsesEligibleUnselectedAssociationAutomatically(t *testing.T) {
	releaseID, positionID, locationID, variantID, associationID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	known := true
	source := persistence.FirstPublicationSource{
		Selection:        persistence.PublicationSourceSelection{LocationID: locationID, MediaVariantID: variantID},
		AssociationID:    associationID,
		AudioStreamCount: 1,
		Quality:          persistence.PersistedAudioFacts{Codec: "flac", StreamIndex: 0, Lossless: &known, Container: publication.AudioContainerFLAC, SupportedSourceContainer: true},
	}
	snapshot := &persistence.LibraryFirstPublicationContext{
		Library: &persistence.LibraryPublicationContext{ReleaseID: releaseID, PositionID: positionID, ActiveEpoch: uuid.New()},
		Tags: publication.TagProjectionInput{
			Release:  publication.ReleaseTags{Title: "Album", Artist: []librarymetadata.ArtistCredit{{ArtistID: uuid.NewString(), ArtistName: "Artist"}}},
			Position: publication.PositionTags{Title: "Track", Artist: []librarymetadata.ArtistCredit{{ArtistID: uuid.NewString(), ArtistName: "Artist"}}},
		},
		Sources:               []persistence.FirstPublicationSource{source},
		DefaultSourceBlockers: []string{"one or more selected source roots are unavailable or changed"},
	}
	service := newLibraryPublications(firstPreviewRepository{snapshot: snapshot}, firstPreviewSettings{format: "mka", template: "Track.{EXT}", caseSensitive: true, ffmpeg: uuid.NewString()}, nil, tools.Platform{GOOS: "darwin", GOARCH: "arm64"})
	preview, err := service.PreviewFirst(context.Background(), releaseID, positionID)
	if err != nil {
		t.Fatal(err)
	}
	if preview.SelectedSource == nil || preview.SelectedSource.AssociationID != associationID ||
		preview.SelectedSource.LocationID != locationID || preview.SelectedSource.MediaVariantID != variantID || preview.SelectedSource.StreamIndex != 0 {
		t.Fatalf("eligible unselected association was not chosen automatically: %#v", preview.SelectedSource)
	}
	if len(preview.SourceCandidates) != 1 || preview.SourceCandidates[0].AssociationID != associationID || preview.SourceCandidates[0].Selected {
		t.Fatalf("automatic selection changed the persisted association decision: %#v", preview.SourceCandidates)
	}
	if len(preview.Blockers) != 0 {
		t.Fatalf("automatic eligible selection retained stale blockers: %#v", preview.Blockers)
	}
	if snapshot.Sources[0].Selected {
		t.Fatal("automatic preview mutated the persisted association decision")
	}
	choice := &FirstPublicationSourceChoice{LocationID: locationID, MediaVariantID: variantID, StreamIndex: 0}
	explicit, err := service.PreviewFirstSelected(context.Background(), releaseID, positionID, choice)
	if err != nil || explicit.SelectedSource == nil || explicit.SelectedSource.AssociationID != associationID || len(explicit.Blockers) != 0 {
		t.Fatalf("explicit eligible association choice = %#v, blockers=%v, %v", explicit.SelectedSource, explicit.Blockers, err)
	}
	if explicit.ContextDigest != preview.ContextDigest {
		t.Fatalf("automatic and explicit preview digests differ: %q != %q", preview.ContextDigest, explicit.ContextDigest)
	}
	if snapshot.Sources[0].Selected {
		t.Fatal("explicit preview mutated the persisted association decision")
	}
}

func TestPreviewFirstLazilyResolvesPrimaryArtistTokens(t *testing.T) {
	releaseID, positionID := uuid.New(), uuid.New()
	first, second := uuid.New(), uuid.New()
	base := &persistence.LibraryFirstPublicationContext{
		Library:        &persistence.LibraryPublicationContext{ReleaseID: releaseID, PositionID: positionID, ActiveEpoch: uuid.New()},
		TemplateTokens: map[string]string{"ALBUM_ARTIST": "One & Two"},
		ReleasePrimaryCredits: []librarymetadata.ArtistCredit{
			{ArtistID: first.String(), ArtistName: "One", CreditName: "One", JoinPhrase: " & "},
			{ArtistID: second.String(), ArtistName: "Two", CreditName: "Two"},
		},
	}
	run := func(template string) FirstPublicationPreview {
		t.Helper()
		preview, err := newLibraryPublications(
			firstPreviewRepository{snapshot: base}, firstPreviewSettings{format: "mka", template: template}, nil, tools.Platform{GOOS: "darwin", GOARCH: "arm64"},
		).PreviewFirst(context.Background(), releaseID, positionID)
		if err != nil {
			t.Fatal(err)
		}
		return preview
	}
	withoutPrimary := run("{ALBUM_ARTIST}/Album")
	if len(withoutPrimary.PathComponents) != 2 || withoutPrimary.PathComponents[0] != "One & Two" {
		t.Fatalf("full-credit template path = %#v", withoutPrimary.PathComponents)
	}
	if strings.Contains(strings.Join(withoutPrimary.Blockers, " "), "primary artist selection required") {
		t.Fatalf("unused ambiguous primary artist blocked preview: %#v", withoutPrimary.Blockers)
	}
	withPrimary := run("{ALBUM_PRIMARY_ARTIST}/Album")
	if !strings.Contains(strings.Join(withPrimary.Blockers, " "), "primary artist selection required") {
		t.Fatalf("requested ambiguous primary artist did not block path rendering: %#v", withPrimary.Blockers)
	}
	base.ReleasePrimaryCredits = base.ReleasePrimaryCredits[:1]
	base.TemplateTokens = map[string]string{}
	single := run("{ALBUM_PRIMARY_ARTIST}/{ALBUM_PRIMARY_ARTIST_ID}")
	if len(single.PathComponents) != 2 || single.PathComponents[0] != "One" || single.PathComponents[1] != first.String() {
		t.Fatalf("single-credit primary artist was not resolved lazily: %#v", single.PathComponents)
	}
}
