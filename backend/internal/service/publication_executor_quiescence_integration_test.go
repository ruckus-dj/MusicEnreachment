package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type quiescenceExecutorFixture struct {
	executor    *PublicationExecutor
	repository  *scriptedPublicationExecutorRepository
	operationID uuid.UUID
	riverJobID  int64
	positionID  uuid.UUID
	outputPath  string
}

func newQuiescenceExecutorFixture(t *testing.T, commands PublicationCommandRunner) quiescenceExecutorFixture {
	t.Helper()
	ctx := context.Background()
	output, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	toolsRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := newScriptedSettingsStore(map[string]string{})
	registry := settings.New(store, nil)
	if err := registry.SetToolsDirectory(ctx, toolsRoot, ""); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetOutputDirectory(ctx, output, toolsRoot); err != nil {
		t.Fatal(err)
	}
	currentPath := filepath.Join(output, "Track.flac")
	current, err := os.ReadFile("../integrations/metadata/testdata/eg.flac")
	if err != nil {
		t.Fatal(err)
	}
	current, err = flacFixtureWithoutPictures(current)
	if err != nil {
		t.Fatalf("prepare valid FLAC publication fixture: %v", err)
	}
	if err := os.WriteFile(currentPath, current, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(current)
	size, mtime := info.Size(), info.ModTime().UnixMicro()
	installationID, operationID := uuid.New(), uuid.New()
	positionID, epoch, generation, candidate := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	version := "ffmpeg 7.0 + ffprobe 7.0"
	toolRelative := filepath.ToSlash(filepath.Join("ffmpeg", "7.0"))
	selections := []persistence.SourceAnalysisToolSelection{
		{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: toolRelative, Executable: "ffmpeg", Version: version, VersionBanner: "ffmpeg version 7.0"},
		{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: toolRelative, Executable: "ffprobe", Version: version, VersionBanner: "ffprobe version 7.0"},
	}
	input := persistence.PublicationInputs{
		Version: persistence.PublicationInputsVersion, QualityPolicyVersion: publication.QualityPolicyVersion,
		IntentKind: "rewrite", Format: publication.AudioFormatSource,
		DesiredTags: map[string][]string{"TITLE": {"Track"}, "ARTIST": {"Artist"}}, Tools: selections,
		Rewrite: &persistence.PublicationRewriteInput{
			CurrentGeneration: generation, RelativePath: "Track.flac",
			Ownership:  persistence.PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: hex.EncodeToString(digest[:])},
			AudioFacts: persistence.PersistedAudioFacts{StreamIndex: 0, Codec: "flac", Lossless: quiescenceBoolPointer(true), Channels: quiescenceIntPointer(2), Container: "flac", SupportedSourceContainer: true},
		},
	}
	if err := input.Validate(); err != nil {
		t.Fatalf("validate fixture inputs: %v", err)
	}
	frozen, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(map[string]any{"version": 1, "nodes": []persistence.PublicationNamespaceNode{{CanonicalKey: "track.flac", RelativePath: "Track.flac", NodeKind: "file"}}})
	if err != nil {
		t.Fatal(err)
	}
	riverJobID := int64(9001)
	operation := &persistence.Operation{ID: operationID, Kind: "publish_track_v1", State: "queued", Attempt: 1, RiverJobID: &riverJobID}
	attempt := &persistence.TrackPublicationAttempt{OperationID: operationID, PositionID: positionID, Epoch: epoch, ExpectedGeneration: &generation, CandidateGeneration: candidate, IntentKind: "rewrite", Phase: "queued", ResumeMode: persistence.PublicationResumeModePrepare, FrozenInputs: frozen, NamespaceManifest: manifest}
	toolDir := filepath.Join(toolsRoot, filepath.FromSlash(toolRelative))
	if err := os.MkdirAll(toolDir, 0o700); err != nil {
		t.Fatal(err)
	}
	versions := map[string]string{}
	for _, executable := range tools.ExpectedExecutables(tools.PackageFFmpeg, runtime.GOOS) {
		name := executable
		banner := "ffmpeg version 7.0"
		if strings.HasPrefix(name, "ffprobe") {
			banner = "ffprobe version 7.0"
		}
		versions[executable] = banner
		if err := os.WriteFile(filepath.Join(toolDir, name), []byte("controlled executable"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	versionOutput, err := json.Marshal(versions)
	if err != nil {
		t.Fatal(err)
	}
	verifiedAt := info.ModTime()
	installation := &persistence.ToolInstallation{ID: installationID, PackageKind: "ffmpeg", PlatformGOOS: runtime.GOOS, PlatformGOARCH: runtime.GOARCH, RelativePath: filepath.FromSlash(toolRelative), Version: version, State: "ready", VerifiedAt: &verifiedAt, ExecutableVerificationOutput: versionOutput}
	repository := &scriptedPublicationExecutorRepository{}
	repository.getOperation = func(_ context.Context, id uuid.UUID) (*persistence.Operation, error) {
		if id != operationID {
			t.Fatalf("operation ID=%s", id)
		}
		return operation, nil
	}
	repository.readAttempt = func(_ context.Context, id uuid.UUID) (*persistence.TrackPublicationAttempt, error) {
		if id != operationID {
			t.Fatalf("attempt ID=%s", id)
		}
		return attempt, nil
	}
	repository.claim = func(_ context.Context, guard persistence.PublicationDeliveryGuard) (persistence.PublicationDeliveryClaimOutcome, error) {
		return persistence.PublicationDeliveryAcquired, nil
	}
	repository.preparationInputs = func(_ context.Context, _ persistence.PublicationDeliveryGuard) (persistence.PublicationInputs, bool, error) {
		return input, true, nil
	}
	repository.publicationToolsForDelivery = func(_ context.Context, _ persistence.PublicationDeliveryGuard) ([]persistence.SourceAnalysisToolSelection, *persistence.ToolInstallation, string, bool, error) {
		return selections, installation, toolsRoot, true, nil
	}
	repository.checkDelivery = func(_ context.Context, _ persistence.PublicationDeliveryGuard) (bool, error) { return true, nil }
	coordinator := publication.NewCoordinator()
	executor := newPublicationExecutor(repository, registry, tools.Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, coordinator, uuid.New(), commands)
	return quiescenceExecutorFixture{executor: executor, repository: repository, operationID: operationID, riverJobID: riverJobID, positionID: positionID, outputPath: output}
}

func quiescenceBoolPointer(value bool) *bool { return &value }
func quiescenceIntPointer(value int) *int    { return &value }

type interruptedRewriteValidator struct {
	observation PublicationStageObservation
	input       persistence.PublicationInputs
	calls       int
}

func (validator *interruptedRewriteValidator) Validate(_ context.Context, _ string, input persistence.PublicationInputs) (PublicationStageObservation, error) {
	validator.calls++
	validator.input = input
	return validator.observation, nil
}

type interruptedRewriteCommandRunner struct{ calls int }

func (runner *interruptedRewriteCommandRunner) Run(_ context.Context, executable string, _ ...string) (PublicationCommandResult, error) {
	runner.calls++
	banner := "ffmpeg version 7.0"
	if strings.HasPrefix(filepath.Base(executable), "ffprobe") {
		banner = "ffprobe version 7.0"
	}
	return PublicationCommandResult{Output: []byte(banner), QuiescenceEstablished: true}, nil
}

type interruptedRewriteMetadataWriter struct{ calls int }

func (writer *interruptedRewriteMetadataWriter) Write(context.Context, string, map[string][]string, *metadata.Cover) (metadata.WriteResult, error) {
	writer.calls++
	return metadata.WriteResult{}, nil
}

func TestInterruptedRewriteAdoptsValidatedTargetWithoutRewriting(t *testing.T) {
	cases := []struct {
		name              string
		cover             bool
		manualLyricsState string
		automaticLyrics   bool
	}{
		{name: "plain tags"},
		{name: "manual cover", cover: true},
		{name: "manual lyrics value", manualLyricsState: "value"},
		{name: "manual lyrics explicit empty", manualLyricsState: "explicit_empty"},
		{name: "automatic lyrics retry", automaticLyrics: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			commands := &interruptedRewriteCommandRunner{}
			fixture := newQuiescenceExecutorFixture(t, commands)
			ctx := context.Background()
			attempt, err := fixture.repository.ReadPublicationAttempt(ctx, fixture.operationID)
			if err != nil || attempt == nil {
				t.Fatalf("read fixture attempt: attempt=%v err=%v", attempt, err)
			}
			input, err := persistence.DecodePublicationInputs(attempt.FrozenInputs)
			if err != nil {
				t.Fatal(err)
			}
			var thumbnail *metadata.Cover
			var receipt *persistence.PublicationAttemptCoverInput
			if testCase.cover {
				requested := image.NewRGBA(image.Rect(0, 0, 64, 64))
				for y := 0; y < 64; y++ {
					for x := 0; x < 64; x++ {
						requested.Set(x, y, color.RGBA{R: uint8(x * 3), G: uint8(y * 3), B: uint8(x + y), A: 255})
					}
				}
				var encoded bytes.Buffer
				if err := jpeg.Encode(&encoded, requested, nil); err != nil {
					t.Fatal(err)
				}
				coverBytes := encoded.Bytes()
				releaseID, requestedGeneration := uuid.New(), uuid.New()
				mime := "image/jpeg"
				coverSize := int64(len(coverBytes))
				coverDigest := sha256.Sum256(coverBytes)
				coverReceipt := persistence.PublicationAttemptCoverInput{
					OperationID: fixture.operationID, PositionID: fixture.positionID, ReleaseID: releaseID,
					Revision: 2, Mode: "manual", State: "value", Generation: &requestedGeneration,
					Bytes: coverBytes, MIME: &mime, SHA256: coverDigest[:], Size: &coverSize,
				}
				receipt = &coverReceipt
				input.ManualCover = &persistence.PublicationManualCoverInput{
					ReleaseID: releaseID, AuthorityRevision: coverReceipt.Revision, SelectedGeneration: requestedGeneration, AuthorityMode: coverReceipt.Mode,
				}
				inputBytes, err := json.Marshal(input)
				if err != nil {
					t.Fatal(err)
				}
				attempt.FrozenInputs = inputBytes
				fixture.repository.coverInput = func(_ context.Context, guard persistence.PublicationDeliveryGuard) (persistence.PublicationAttemptCoverInput, bool, error) {
					return coverReceipt, guard.OperationID == fixture.operationID && guard.ExpectedPositionID == fixture.positionID, nil
				}
				thumbnail, err = publicationCoverThumbnail(ctx, t.TempDir(), receipt)
				if err != nil {
					t.Fatalf("derive frozen cover thumbnail: %v", err)
				}
			}
			var frozenInput persistence.PublicationInputs
			var frozenAutomaticRetry persistence.PublicationInputs
			if testCase.manualLyricsState != "" {
				lyrics := &persistence.PublicationManualLyricsInput{
					ExpectedMetadataRevision: 2, ConfirmedCurrentGeneration: input.Rewrite.CurrentGeneration,
					State: testCase.manualLyricsState,
				}
				if testCase.manualLyricsState == "value" {
					lyrics.Value = "[00:01.00]Interrupted lyrics\n"
				}
				input.ManualLyrics = lyrics
				frozenInput = input.Clone()
				inputBytes, err := json.Marshal(input)
				if err != nil {
					t.Fatal(err)
				}
				attempt.FrozenInputs = inputBytes
				fixture.repository.manualLyricsInputs = func(_ context.Context, guard persistence.PublicationDeliveryGuard) (persistence.PublicationInputs, bool, error) {
					return input, guard.OperationID == fixture.operationID && guard.ExpectedPositionID == fixture.positionID, nil
				}
			}
			if testCase.automaticLyrics {
				currentCapture, err := (metadata.Reader{}).Read(ctx, filepath.Join(fixture.outputPath, filepath.FromSlash(input.Rewrite.RelativePath)), metadata.ReadRequest{CoverInspection: true})
				if err != nil {
					t.Fatalf("read frozen current publication assets: %v", err)
				}
				currentAssets, err := ObservePublicationAssets(currentCapture)
				if err != nil {
					t.Fatalf("observe frozen current publication assets: %v", err)
				}
				input.AutomaticLyricsRetry = &persistence.PublicationAutomaticLyricsRetryInput{
					ExpectedMetadataRevision: 2, ConfirmedCurrentGeneration: input.Rewrite.CurrentGeneration,
					CurrentAssets: currentAssets,
				}
				frozenAutomaticRetry = input.Clone()
				inputBytes, err := json.Marshal(input)
				if err != nil {
					t.Fatal(err)
				}
				attempt.FrozenInputs = inputBytes
			}
			// Model the exact same-path FLAC left after the native tag rewrite completed
			// but before its result transaction. Establish effective tags before recovery.
			target := filepath.Join(fixture.outputPath, filepath.FromSlash(input.Rewrite.RelativePath))
			effectiveTags := input.DesiredTags
			if input.ManualLyrics != nil {
				effectiveTags, _, err = optionalLyricsTags(input.DesiredTags, input.ManualLyrics)
				if err != nil {
					t.Fatalf("derive frozen effective lyrics tags: %v", err)
				}
			}
			if testCase.automaticLyrics {
				effectiveTags = cloneTagMap(input.DesiredTags)
				effectiveTags["LYRICS"] = []string{"[00:02.00]Acquired after interruption\n"}
			}
			tagWrite, err := (metadata.Writer{}).Write(ctx, target, effectiveTags, thumbnail)
			if err != nil {
				t.Fatalf("prepare interrupted target tags: %v", err)
			}
			if !equalTags(tagWrite.Tags, effectiveTags) {
				t.Fatalf("interrupted target tags=%v, want effective frozen tags %v", tagWrite.Tags, effectiveTags)
			}
			contents, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(contents)
			info, err := os.Stat(target)
			if err != nil {
				t.Fatal(err)
			}
			size, mtime := info.Size(), info.ModTime().UnixMicro()
			audio, err := json.Marshal(struct {
				Version int                             `json:"version"`
				Facts   persistence.PersistedAudioFacts `json:"facts"`
			}{Version: 1, Facts: input.Rewrite.AudioFacts})
			if err != nil {
				t.Fatal(err)
			}
			capture, err := (metadata.Reader{}).Read(ctx, target, metadata.ReadRequest{CoverInspection: true})
			if err != nil {
				t.Fatalf("read actual interrupted target metadata: %v", err)
			}
			actualMetadata, err := json.Marshal(persistence.PublicationCommitMetadata{Version: 1, Tags: capture.Tags})
			if err != nil {
				t.Fatal(err)
			}
			observedAssets, err := ObservePublicationAssets(capture)
			if err != nil {
				t.Fatalf("observe actual interrupted target assets: %v", err)
			}
			assets, err := json.Marshal(observedAssets)
			if err != nil {
				t.Fatal(err)
			}
			var thumbnailDigest [sha256.Size]byte
			var thumbnailSize int64
			if testCase.cover {
				thumbnailDigest = sha256.Sum256(thumbnail.Data)
				thumbnailSize = int64(len(thumbnail.Data))
				if observedAssets.Cover.SHA256 != hex.EncodeToString(thumbnailDigest[:]) || observedAssets.Cover.SizeBytes == nil || *observedAssets.Cover.SizeBytes != thumbnailSize {
					t.Fatalf("observed cover=%+v, want derivative digest and size", observedAssets.Cover)
				}
			}
			validator := &interruptedRewriteValidator{observation: PublicationStageObservation{
				SHA256: hex.EncodeToString(digest[:]), Audio: audio, Metadata: actualMetadata,
				Assets: assets, QualityPolicyVersion: publication.QualityPolicyVersion,
			}}
			fixture.executor.stageValidator = validator
			metadataWriter := &interruptedRewriteMetadataWriter{}
			fixture.executor.metadata = metadataWriter
			fixture.repository.resultCurrent = func(context.Context, persistence.PublicationDeliveryGuard) (bool, error) { return true, nil }
			fixture.repository.preparationInputs = func(_ context.Context, _ persistence.PublicationDeliveryGuard) (persistence.PublicationInputs, bool, error) {
				return input, true, nil
			}
			fixture.repository.claim = func(context.Context, persistence.PublicationDeliveryGuard) (persistence.PublicationDeliveryClaimOutcome, error) {
				return persistence.PublicationDeliveryInterrupted, nil
			}
			fixture.repository.commit = func(_ context.Context, guard persistence.PublicationDeliveryGuard, result persistence.PublicationResult) (bool, error) {
				if guard.ExpectedPositionID != fixture.positionID {
					t.Fatalf("committed position=%s, want %s", guard.ExpectedPositionID, fixture.positionID)
				}
				if result.Observation.RelativePath != input.Rewrite.RelativePath {
					t.Fatalf("committed target=%q, want frozen target %q", result.Observation.RelativePath, input.Rewrite.RelativePath)
				}
				if !bytes.Equal(result.Observation.ActualAssets, assets) {
					t.Fatalf("committed assets=%s, want %s", result.Observation.ActualAssets, assets)
				}
				if testCase.cover {
					projection := result.CoverEmbedding
					if projection == nil || projection.Version != persistence.PublicationCoverEmbeddingDerivativeProjectionVersion || projection.Outcome != "verified" || projection.RequestedSHA256 == projection.EmbeddedSHA256 || projection.RequestedSHA256 != hex.EncodeToString(receipt.SHA256) || projection.EmbeddedSHA256 != hex.EncodeToString(thumbnailDigest[:]) || projection.EmbeddedMIME != "image/png" || projection.EmbeddedSize == nil || *projection.EmbeddedSize != thumbnailSize {
						t.Fatalf("cover embedding projection=%+v, want independently verified derivative", projection)
					}
				} else if result.CoverEmbedding != nil {
					t.Fatalf("plain recovery produced cover embedding %+v", result.CoverEmbedding)
				}
				if testCase.automaticLyrics {
					projection := result.InitialLyrics
					actualLyricsDigest := sha256.Sum256([]byte(effectiveTags["LYRICS"][0]))
					if projection == nil || projection.Generation != attempt.CandidateGeneration || projection.Source != "unknown" ||
						projection.Outcome != persistence.PublicationInitialLyricsObservedAfterInterruption || projection.SHA256 != hex.EncodeToString(actualLyricsDigest[:]) ||
						projection.ResolutionWarningCode != "" || projection.OutcomeWarningCode != "" ||
						projection.AuthorityMetadataRevision != frozenAutomaticRetry.AutomaticLyricsRetry.ExpectedMetadataRevision ||
						projection.AuthorityState != "absent" || projection.Authority != "automatic" || projection.AuthorityProvenance != "source_metadata" {
						t.Fatalf("initial lyrics projection=%+v, want observed acquired lyrics with frozen authority", projection)
					}
				} else if result.InitialLyrics != nil {
					t.Fatalf("non-lyrics recovery produced initial lyrics projection %+v", result.InitialLyrics)
				}
				if !bytes.Equal(result.Observation.ActualMetadata, actualMetadata) {
					t.Fatalf("committed metadata=%s, want %s", result.Observation.ActualMetadata, actualMetadata)
				}
				if result.InstalledOwnership.Identity.SHA256 != hex.EncodeToString(digest[:]) || result.InstalledOwnership.Identity.SizeBytes == nil || *result.InstalledOwnership.Identity.SizeBytes != size || result.InstalledOwnership.Identity.MTime == nil || *result.InstalledOwnership.Identity.MTime != mtime {
					t.Fatalf("committed ownership does not describe interrupted target: %+v", result.InstalledOwnership.Identity)
				}
				return true, nil
			}
			if err := fixture.executor.ExecutePublicationDelivery(ctx, fixture.operationID, fixture.riverJobID, 1); err != nil {
				t.Fatalf("execute interrupted rewrite recovery: %v", err)
			}
			if validator.calls != 1 {
				t.Fatalf("stage validation calls=%d, want 1", validator.calls)
			}
			if input.ManualLyrics != nil {
				if validator.input.ManualLyrics != nil || !validator.input.SupportsExecution() || validator.input.Validate() != nil || !equalTags(validator.input.DesiredTags, effectiveTags) {
					t.Fatalf("inspection validator input=%+v, want executable detached effective tags without manual lyrics", validator.input)
				}
				if validator.input.Rewrite == input.Rewrite {
					t.Fatal("inspection validator input aliases frozen rewrite data")
				}
				validator.input.DesiredTags["TITLE"][0] = "inspection-only mutation"
				if input.DesiredTags["TITLE"][0] == "inspection-only mutation" {
					t.Fatal("inspection validator input aliases frozen desired tags")
				}
				if input.ManualLyrics == nil || input.ManualLyrics.State != frozenInput.ManualLyrics.State || input.ManualLyrics.Value != frozenInput.ManualLyrics.Value || !equalTags(input.DesiredTags, frozenInput.DesiredTags) {
					t.Fatal("inspection preparation mutated frozen manual-lyrics inputs")
				}
			}
			if testCase.automaticLyrics {
				if validator.input.AutomaticLyricsRetry != nil || !validator.input.SupportsExecution() || validator.input.Validate() != nil ||
					!equalTags(validator.input.DesiredTags, effectiveTags) {
					t.Fatalf("automatic lyrics validator input=%+v, want detached generic input using actual readback tags", validator.input)
				}
				if validator.input.Rewrite == input.Rewrite || !equalTags(input.DesiredTags, frozenAutomaticRetry.DesiredTags) ||
					input.AutomaticLyricsRetry == nil || input.AutomaticLyricsRetry.ExpectedMetadataRevision != frozenAutomaticRetry.AutomaticLyricsRetry.ExpectedMetadataRevision ||
					!equalTags(input.DesiredTags, map[string][]string{"TITLE": {"Track"}, "ARTIST": {"Artist"}}) {
					t.Fatal("automatic lyrics recovery mutated the frozen retry inputs")
				}
				if !reflect.DeepEqual(input.AutomaticLyricsRetry, frozenAutomaticRetry.AutomaticLyricsRetry) {
					t.Fatal("automatic lyrics recovery changed frozen retry authority")
				}
			}
			if commands.calls != 2 {
				t.Fatalf("command calls=%d, want only pinned ffmpeg/ffprobe version checks", commands.calls)
			}
			if metadataWriter.calls != 0 {
				t.Fatalf("recovery performed %d metadata writes", metadataWriter.calls)
			}
			afterRecovery, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(afterRecovery, contents) {
				t.Fatal("recovery rewrote the interrupted target")
			}
			if fixture.repository.committedResult == nil {
				t.Fatal("recovery did not commit the actual installed result")
			}
		})
	}
}
