package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type quiescenceCommand struct {
	unknown bool
	fail    bool
	calls   int
}

func (runner *quiescenceCommand) Run(_ context.Context, _ string, args ...string) (PublicationCommandResult, error) {
	runner.calls++
	if len(args) > 0 && args[len(args)-1] == "-version" {
		if runner.unknown {
			return PublicationCommandResult{}, errors.New("version command did not establish quiescence")
		}
		if runner.fail {
			return PublicationCommandResult{QuiescenceEstablished: true}, errors.New("known quiescent version failure")
		}
	}
	return PublicationCommandResult{Output: []byte("version 7"), QuiescenceEstablished: true}, nil
}

func TestPublicationExecutorUnknownVersionQuiescenceRetainsCoordinator(t *testing.T) {
	commands := &quiescenceCommand{unknown: true}
	fixture := newQuiescenceExecutorFixture(t, commands)
	err := fixture.executor.ExecutePublicationDelivery(context.Background(), fixture.operationID, fixture.riverJobID, 1)
	if err == nil || !strings.Contains(err.Error(), "quiescence") {
		t.Fatalf("unknown version-command delivery error = %v", err)
	}
	if commands.calls == 0 {
		t.Fatal("version command was not invoked")
	}
	fixture.assertOneGuardPropagated(t)
	before := commands.calls
	freePosition := uuid.NewString()
	synctest.Test(t, func(t *testing.T) {
		// A live delivery can only stay pending because the failed delivery
		// retained the global mutation resource. synctest.Wait returns only when
		// every bubble goroutine is durably blocked, so a free resource would have
		// delivered a result instead of remaining pending.
		assertQuiescenceDeliveryBlocksUntilCanceled(t, fixture.executor, fixture.operationID, fixture.riverJobID)
		retained := []publication.ResourceLock{
			{Key: GlobalPublicationMutationResource, Mode: publication.Exclusive},
			{Key: "publication-output:" + filepath.Clean(fixture.outputPath), Mode: publication.Exclusive},
			{Key: "publication-position:" + fixture.positionID.String(), Mode: publication.Exclusive},
		}
		for _, resource := range retained {
			assertQuiescenceAcquireBlocksUntilCanceled(t, fixture.executor.coordinator, resource)
		}
		// Controls: distinct output-root and position resources are granted in the
		// same bubble, so the retained probes cannot pass as an artifact of the
		// bubble or the coordinator.
		free := []publication.ResourceLock{
			{Key: "publication-output:" + filepath.Clean(filepath.Join(fixture.outputPath, "control")), Mode: publication.Exclusive},
			{Key: "publication-position:" + freePosition, Mode: publication.Exclusive},
		}
		for _, resource := range free {
			assertQuiescenceAcquireSucceeds(t, fixture.executor.coordinator, resource)
		}
	})
	if commands.calls != before {
		t.Fatalf("backend calls while delivery blocked on the retained coordinator = %d, want unchanged %d", commands.calls, before)
	}
}

func TestPublicationExecutorKnownQuiescentFailureReleasesCoordinator(t *testing.T) {
	fixture := newQuiescenceExecutorFixture(t, &quiescenceCommand{fail: true})
	if err := fixture.executor.ExecutePublicationDelivery(context.Background(), fixture.operationID, fixture.riverJobID, 1); err == nil {
		t.Fatal("delivery with known-quiescent command failure unexpectedly succeeded")
	}
	fixture.assertOneGuardPropagated(t)
	acquired := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var release func()
	var acquireErr error
	done := make(chan struct{})
	go func() {
		close(acquired)
		release, acquireErr = fixture.executor.coordinator.Acquire(ctx, []publication.ResourceLock{{Key: GlobalPublicationMutationResource, Mode: publication.Exclusive}})
		close(done)
	}()
	<-acquired
	<-done
	if acquireErr != nil {
		t.Fatalf("coordinator remained held after quiescent failure: %v", acquireErr)
	}
	release()
}

type quiescenceExecutorFixture struct {
	executor    *PublicationExecutor
	repository  *scriptedPublicationExecutorRepository
	operationID uuid.UUID
	riverJobID  int64
	positionID  uuid.UUID
	outputPath  string
}

func (fixture quiescenceExecutorFixture) assertOneGuardPropagated(t *testing.T) {
	t.Helper()
	if len(fixture.repository.guards) < 3 {
		t.Fatalf("guarded repository calls = %d, want claim, preparation, and tools", len(fixture.repository.guards))
	}
	first := fixture.repository.guards[0]
	for _, guard := range fixture.repository.guards[1:] {
		if guard != first {
			t.Fatalf("delivery guard changed between repository calls: first=%+v next=%+v", first, guard)
		}
	}
	if first.OperationID != fixture.operationID || first.RiverJobID != fixture.riverJobID || first.RiverAttempt != 1 || first.ClaimUUID == uuid.Nil || first.BootUUID == uuid.Nil || first.DeliveryToken == "" {
		t.Fatalf("incomplete generated delivery guard: %+v", first)
	}
	if fixture.repository.committedResult != nil {
		t.Fatalf("failed command unexpectedly committed a result: %+v", fixture.repository.committedResult)
	}
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

// assertQuiescenceAcquireBlocksUntilCanceled must run inside a synctest bubble.
// synctest.Wait returns only when every goroutine in the bubble is durably
// blocked, so a free resource (whose acquire returns immediately) would already
// have delivered a result. Only a retained resource can leave the result
// pending, and it may change only after the live context is canceled.
func assertQuiescenceAcquireBlocksUntilCanceled(t *testing.T, coordinator *publication.Coordinator, resource publication.ResourceLock) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		release, err := coordinator.Acquire(ctx, []publication.ResourceLock{resource})
		if release != nil {
			release()
		}
		done <- err
	}()
	synctest.Wait()
	select {
	case err := <-done:
		t.Fatalf("free resource %q acquisition returned %v, want a durably blocked waiter", resource.Key, err)
	default:
	}
	cancel()
	synctest.Wait()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled resource %q acquisition error = %v, want context canceled", resource.Key, err)
		}
	default:
		t.Fatalf("retained resource %q acquisition did not return after cancellation", resource.Key)
	}
}

// assertQuiescenceDeliveryBlocksUntilCanceled must run inside a synctest bubble.
// The retained global mutation resource is acquired before any repository or
// command work, so a pending result proves the delivery never reached commands.
func assertQuiescenceDeliveryBlocksUntilCanceled(t *testing.T, executor *PublicationExecutor, operationID uuid.UUID, riverJobID int64) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- executor.ExecutePublicationDelivery(ctx, operationID, riverJobID, 2) }()
	synctest.Wait()
	select {
	case err := <-done:
		t.Fatalf("delivery completed while the coordinator was retained: %v", err)
	default:
	}
	cancel()
	synctest.Wait()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled delivery error = %v, want context canceled", err)
		}
	default:
		t.Fatal("delivery did not return after cancellation")
	}
}

// assertQuiescenceAcquireSucceeds is the free-resource control in the same
// bubble: an unretained resource must be granted immediately.
func assertQuiescenceAcquireSucceeds(t *testing.T, coordinator *publication.Coordinator, resource publication.ResourceLock) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release, err := coordinator.Acquire(ctx, []publication.ResourceLock{resource})
	if err != nil {
		t.Fatalf("free resource %q acquisition = %v", resource.Key, err)
	}
	if release == nil {
		t.Fatalf("free resource %q acquisition returned no release function", resource.Key)
	}
	release()
}

func quiescenceBoolPointer(value bool) *bool { return &value }
func quiescenceIntPointer(value int) *int    { return &value }
