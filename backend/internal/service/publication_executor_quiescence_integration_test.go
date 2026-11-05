package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"
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
	attempt := &persistence.TrackPublicationAttempt{OperationID: operationID, PositionID: positionID, Epoch: epoch, ExpectedGeneration: &generation, CandidateGeneration: candidate, IntentKind: "rewrite", Phase: "queued", FrozenInputs: frozen, NamespaceManifest: manifest}
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
