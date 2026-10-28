package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type libraryPublicationsTestRepository struct {
	projection                               *persistence.LibraryPublicationContext
	installation                             *persistence.ToolInstallation
	readErr, installationErr, admitErr       error
	readCount, installationCount, admitCount int
	admission                                persistence.ManualLyricsAdmission
}

func (repository *libraryPublicationsTestRepository) ReadLibraryPublicationContext(context.Context, uuid.UUID, uuid.UUID) (*persistence.LibraryPublicationContext, error) {
	repository.readCount++
	return repository.projection, repository.readErr
}
func (repository *libraryPublicationsTestRepository) GetInstallation(context.Context, uuid.UUID) (*persistence.ToolInstallation, error) {
	repository.installationCount++
	return repository.installation, repository.installationErr
}
func (repository *libraryPublicationsTestRepository) AdmitManualLyricsRewrite(_ context.Context, admission persistence.ManualLyricsAdmission, _ persistence.RiverInserter) (uuid.UUID, error) {
	repository.admitCount++
	repository.admission = admission
	return uuid.New(), repository.admitErr
}

type libraryPublicationsTestSettings struct{ activeID string }

func (runtime libraryPublicationsTestSettings) ReadRuntimeSettings(context.Context) (settings.RuntimeSettings, error) {
	return settings.RuntimeSettings{ActiveFFmpegInstallation: runtime.activeID}, nil
}

type libraryPublicationsTestRiver struct{}

func (libraryPublicationsTestRiver) InsertTx(context.Context, *sql.Tx, river.JobArgs, *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	panic("River InsertTx must not be called directly")
}

func TestLibraryPublicationsApplyManualLyrics(t *testing.T) {
	const lyrics = "[00:01.00]first\r\n[00:02.00]second\r\n"
	for _, test := range []struct {
		name, state, value string
		wantManualState    string
	}{
		{name: "manual value preserves exact LRC", state: "value", value: lyrics, wantManualState: "value"},
		{name: "explicit empty", state: "explicit_empty", wantManualState: "explicit_empty"},
	} {
		t.Run(test.name, func(t *testing.T) {
			releaseID, positionID, epoch, generation := uuid.New(), uuid.New(), uuid.New(), uuid.New()
			projection := publicationTestContext(releaseID, positionID, epoch, generation, test.state, test.value)
			repository, runtime := publicationTestDependencies(t, projection)
			service := newLibraryPublications(repository, runtime, libraryPublicationsTestRiver{}, tools.Platform{GOOS: "darwin", GOARCH: "arm64"})
			operationID, err := service.ApplyManualLyrics(context.Background(), releaseID, positionID, LibraryPublicationApplyManualLyricsRequest{
				ExpectedEpoch: epoch, ExpectedGeneration: generation, ExpectedMetadataRevision: 7,
			})
			if err != nil || operationID == uuid.Nil {
				t.Fatalf("ApplyManualLyrics() = %s, %v", operationID, err)
			}
			if repository.admitCount != 1 || repository.installationCount != 1 {
				t.Fatalf("admit/get installation calls = %d/%d", repository.admitCount, repository.installationCount)
			}
			admission := repository.admission
			if admission.ExpectedReleaseID != releaseID || admission.ExpectedMetadataRevision != 7 || admission.PositionID != positionID || admission.ExpectedEpoch != epoch || admission.ExpectedGeneration == nil || *admission.ExpectedGeneration != generation {
				t.Fatalf("admission fences = %+v", admission)
			}
			if admission.CandidateGeneration == uuid.Nil || admission.CandidateGeneration == generation || admission.IntentKind != "rewrite" || !admission.ToolsReadRequired {
				t.Fatalf("admission identity/tools = %+v", admission)
			}
			if len(admission.Tools) != 2 || admission.Tools[0].Executable != "ffmpeg" || admission.Tools[1].Executable != "ffprobe" {
				t.Fatalf("tool selections = %+v", admission.Tools)
			}
			if len(admission.NamespaceManifest) != 1 || admission.NamespaceManifest[0].CanonicalKey != "canonical/spelling" || admission.NamespaceManifest[0].RelativePath != "canonical/spelling.flac" {
				t.Fatalf("namespace manifest = %+v", admission.NamespaceManifest)
			}
			inputs, err := persistence.DecodePublicationInputs(admission.FrozenInputs)
			if err != nil {
				t.Fatalf("DecodePublicationInputs: %v", err)
			}
			if err := inputs.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if inputs.Audio != nil || inputs.Rewrite == nil || inputs.ManualLyrics == nil || inputs.Rewrite.CurrentGeneration != generation || inputs.ManualLyrics.ConfirmedCurrentGeneration != generation || inputs.ManualLyrics.ExpectedMetadataRevision != 7 || inputs.ManualLyrics.State != test.wantManualState || inputs.ManualLyrics.Value != test.value {
				t.Fatalf("frozen lyrics input = %+v", inputs)
			}
			if inputs.DesiredTags["TITLE"][0] != "old" || inputs.DesiredTags["ARTIST"][0] != "artist" || len(inputs.DesiredTags) != 2 {
				t.Fatalf("manual metadata must preserve current mandatory tags only: %#v", inputs.DesiredTags)
			}
			if inputs.Rewrite.RelativePath != "library/canonical.flac" || inputs.Rewrite.Ownership.SHA256 != strings.Repeat("a", 64) || inputs.Rewrite.Ownership.SizeBytes == nil || *inputs.Rewrite.Ownership.SizeBytes != 1234 || inputs.Rewrite.Ownership.MTime == nil || *inputs.Rewrite.Ownership.MTime != 5678 {
				t.Fatalf("rewrite source facts = %+v", inputs.Rewrite)
			}
			if inputs.Rewrite.AudioFacts.Container != publication.AudioContainerFLAC || inputs.Rewrite.AudioFacts.Codec != "flac" || inputs.Rewrite.AudioFacts.Lossless == nil || !*inputs.Rewrite.AudioFacts.Lossless || !inputs.Rewrite.AudioFacts.SupportedSourceContainer {
				t.Fatalf("rewrite audio facts = %+v", inputs.Rewrite.AudioFacts)
			}
			if len(inputs.Tools) != 2 {
				t.Fatalf("frozen tools = %+v", inputs.Tools)
			}
		})
	}
}

func TestManualLyricsAudioSupportIsBoundedToNativeRewriteContainers(t *testing.T) {
	for _, test := range []struct {
		container publication.AudioContainer
		codec     string
		supported bool
		want      bool
	}{
		{publication.AudioContainerFLAC, "flac", true, true},
		{publication.AudioContainerMP3, "mp3", true, false},
		{publication.AudioContainerMKA, "opus", true, false},
		{publication.AudioContainerMKA, "mp3", true, true},
		{publication.AudioContainerMKA, "pcm_s16le", true, false},
		{publication.AudioContainerM4A, "aac", true, false},
		{publication.AudioContainerMKA, "opus", false, false},
	} {
		got := manualLyricsAudioSupported(persistence.PersistedAudioFacts{Container: test.container, Codec: test.codec, SupportedSourceContainer: test.supported})
		if got != test.want {
			t.Errorf("manualLyricsAudioSupported(%s/%s, supported=%t) = %t, want %t", test.container, test.codec, test.supported, got, test.want)
		}
	}
}

func TestLibraryPublicationsApplyManualLyricsRefusesStaleOrIneligibleState(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*persistence.LibraryPublicationContext)
		want   error
	}{
		{name: "stale epoch", mutate: func(p *persistence.LibraryPublicationContext) { p.ActiveEpoch = uuid.New() }, want: ErrLibraryPublicationConflict},
		{name: "stale generation", mutate: func(p *persistence.LibraryPublicationContext) { p.Current.Record.Generation = uuid.New() }, want: ErrLibraryPublicationConflict},
		{name: "active attempt", mutate: func(p *persistence.LibraryPublicationContext) {
			p.ActiveAttempt = &persistence.LibraryPublicationOperation{ID: uuid.New()}
		}, want: ErrLibraryPublicationConflict},
		{name: "not pending", mutate: func(p *persistence.LibraryPublicationContext) { p.ManualLyrics.Pending = false }, want: ErrLibraryPublicationConflict},
		{name: "unsupported audio", mutate: func(p *persistence.LibraryPublicationContext) {
			p.Current.Audio.Facts.Container = publication.AudioContainerMP3
		}, want: ErrLibraryPublicationUnavailable},
		{name: "MKA artwork", mutate: func(p *persistence.LibraryPublicationContext) {
			p.Current.Audio.Facts.Container = publication.AudioContainerMKA
			p.Current.Audio.Facts.Codec = "mp3"
			p.Current.Assets.Cover.State = persistence.PublicationAssetEmbedded
		}, want: ErrLibraryPublicationUnavailable},
		{name: "missing digest", mutate: func(p *persistence.LibraryPublicationContext) { p.Current.Ownership.Identity.SHA256 = "" }, want: ErrLibraryPublicationUnavailable},
		{name: "not manual provenance", mutate: func(p *persistence.LibraryPublicationContext) {
			field := p.Metadata.Envelope.Desired["synced_lyrics"]
			field.Provenance.Kind = librarymetadata.ProvenanceProvider
			p.Metadata.Envelope.Desired["synced_lyrics"] = field
		}, want: nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			releaseID, positionID, epoch, generation := uuid.New(), uuid.New(), uuid.New(), uuid.New()
			projection := publicationTestContext(releaseID, positionID, epoch, generation, "value", "[00:01.00]line\n")
			test.mutate(projection)
			repository, runtime := publicationTestDependencies(t, projection)
			service := newLibraryPublications(repository, runtime, libraryPublicationsTestRiver{}, tools.Platform{GOOS: "darwin", GOARCH: "arm64"})
			_, err := service.ApplyManualLyrics(context.Background(), releaseID, positionID, LibraryPublicationApplyManualLyricsRequest{ExpectedEpoch: epoch, ExpectedGeneration: generation, ExpectedMetadataRevision: 7})
			if err == nil || test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("ApplyManualLyrics error = %v, want %v", err, test.want)
			}
			if repository.admitCount != 0 {
				t.Fatalf("admitted %d times", repository.admitCount)
			}
		})
	}
}

func TestLibraryPublicationsReadManualLyricsPendingWithoutDesiredValue(t *testing.T) {
	releaseID, positionID, epoch, generation := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	projection := publicationTestContext(releaseID, positionID, epoch, generation, "value", "[00:01.00]line\n")
	delete(projection.Metadata.Envelope.Desired, "synced_lyrics")
	projection.ManualLyrics.Pending = true
	repository, runtime := publicationTestDependencies(t, projection)
	service := newLibraryPublications(repository, runtime, libraryPublicationsTestRiver{}, tools.Platform{GOOS: "darwin", GOARCH: "arm64"})
	view, err := service.Read(context.Background(), releaseID, positionID)
	if err != nil {
		t.Fatal(err)
	}
	if view.ManualLyrics != nil || view.CanApplyManualLyrics {
		t.Fatalf("view = %+v", view)
	}
}

func TestLibraryPublicationsReadCurrentInitialLyricsStatus(t *testing.T) {
	releaseID, positionID, epoch, generation := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	projection := publicationTestContext(releaseID, positionID, epoch, generation, "value", "[00:01.00]line\n")
	projection.Current.InitialLyrics = &persistence.PublicationInitialLyricsProjection{
		Version: persistence.PublicationInitialLyricsVersion, Generation: generation,
		Source: "none", Outcome: persistence.PublicationInitialLyricsNotRequested,
		ResolutionWarningCode:     "lyrics_lookup_metadata_incomplete",
		AuthorityMetadataRevision: 7, AuthorityState: "absent", Authority: "automatic", AuthorityProvenance: "source_metadata",
	}
	repository, runtime := publicationTestDependencies(t, projection)
	publications := newLibraryPublications(repository, runtime, libraryPublicationsTestRiver{}, tools.Platform{GOOS: "darwin", GOARCH: "arm64"})
	view, err := publications.Read(context.Background(), releaseID, positionID)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := view.CurrentInitialLyrics, (&LibraryPublicationInitialLyrics{
		Source: "none", Outcome: persistence.PublicationInitialLyricsNotRequested,
		ResolutionWarningCode: "lyrics_lookup_metadata_incomplete",
	}); got == nil || *got != *want {
		t.Fatalf("initial lyrics status = %+v, want %+v", got, want)
	}

	projection.Current.InitialLyrics.Generation = uuid.New()
	view, err = publications.Read(context.Background(), releaseID, positionID)
	if err != nil {
		t.Fatal(err)
	}
	if view.CurrentInitialLyrics != nil {
		t.Fatalf("stale-generation initial lyrics status = %+v", view.CurrentInitialLyrics)
	}
}

func publicationTestContext(releaseID, positionID, epoch, generation uuid.UUID, state, value string) *persistence.LibraryPublicationContext {
	lyricsField := librarymetadata.ManualFieldValue(librarymetadata.StateValue, value, true)
	if state == "explicit_empty" {
		lyricsField = librarymetadata.ManualFieldValue(librarymetadata.StateExplicitEmpty, nil, false)
	}
	lossless, size, mtime := true, int64(1234), int64(5678)
	return &persistence.LibraryPublicationContext{
		ReleaseID: releaseID, PositionID: positionID, ActiveEpoch: epoch,
		Metadata: persistence.LibraryMetadata{Revision: 7, Envelope: librarymetadata.Envelope{Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopePosition, Desired: map[string]librarymetadata.FieldValue{
			"synced_lyrics": lyricsField,
			"title":         librarymetadata.ManualFieldValue(librarymetadata.StateValue, "changed", true),
		}}},
		Current: &persistence.LibraryPublicationCurrent{
			Record:    persistence.TrackPublication{PositionID: positionID, Epoch: epoch, Generation: generation, RelativePath: "library/canonical.flac", QualityPolicyVersion: publication.QualityPolicyVersion},
			Ownership: persistence.PublicationCurrentOwnership{Identity: persistence.PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: strings.Repeat("a", 64)}},
			Audio:     persistence.PublicationCurrentAudio{Facts: persistence.PersistedAudioFacts{Codec: "flac", Container: publication.AudioContainerFLAC, Lossless: &lossless, SupportedSourceContainer: true}},
			Metadata:  persistence.PublicationCommitMetadata{Version: 1, Tags: map[string][]string{"TITLE": {"old"}, "ARTIST": {"artist"}, "LYRICS": {"older"}}},
		},
		NamespaceManifest: []persistence.LibraryPublicationNamespaceNode{{Claim: persistence.PublicationNamespaceClaim{CanonicalKey: "canonical/spelling", RelativePath: "canonical/spelling.flac", NodeKind: "file"}, PositionID: &positionID, Generation: &generation}},
		ManualLyrics:      persistence.ManualLyricsReceiptProjection{Pending: true},
	}
}

func publicationTestDependencies(t *testing.T, projection *persistence.LibraryPublicationContext) (*libraryPublicationsTestRepository, libraryPublicationsTestSettings) {
	t.Helper()
	id := uuid.New()
	verifiedAt := time.Now()
	versions, _ := json.Marshal(map[string]string{"ffmpeg": "ffmpeg version 7.0\n", "ffprobe": "ffprobe version 7.0\n"})
	repository := &libraryPublicationsTestRepository{projection: projection, installation: &persistence.ToolInstallation{
		ID: id, PackageKind: string(tools.PackageFFmpeg), PlatformGOOS: "darwin", PlatformGOARCH: "arm64", RelativePath: "ffmpeg/7", Version: "ffmpeg 7.0 + ffprobe 7.0", State: "ready", VerifiedAt: &verifiedAt, ExecutableVerificationOutput: versions,
	}}
	return repository, libraryPublicationsTestSettings{activeID: id.String()}
}
