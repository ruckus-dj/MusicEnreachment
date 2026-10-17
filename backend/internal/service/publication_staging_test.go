package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

type pubStageRepo struct {
	events  *[]string
	checks  int
	staleAt int
	phases  []string
}

func (f *pubStageRepo) CheckPublicationDelivery(context.Context, persistence.PublicationDeliveryGuard) (bool, error) {
	f.checks++
	*f.events = append(*f.events, "check")
	return f.staleAt == 0 || f.checks < f.staleAt, nil
}
func (f *pubStageRepo) PublicationToolsForDelivery(context.Context, persistence.PublicationDeliveryGuard) ([]persistence.SourceAnalysisToolSelection, *persistence.ToolInstallation, string, bool, error) {
	return nil, nil, "", false, nil
}
func (f *pubStageRepo) AdvancePublicationStage(_ context.Context, _ persistence.PublicationDeliveryGuard, phase string, _ persistence.PublicationStageObservation) (bool, error) {
	f.phases = append(f.phases, phase)
	*f.events = append(*f.events, phase)
	return true, nil
}
func (f *pubStageRepo) MarkPublicationQuiescent(context.Context, persistence.PublicationDeliveryGuard) (bool, error) {
	*f.events = append(*f.events, "quiescent")
	return true, nil
}

type pubStagePrepared struct {
	paths    PublicationStagePaths
	events   *[]string
	closeErr error
}

func (f *pubStagePrepared) Paths() PublicationStagePaths { return f.paths }
func (f *pubStagePrepared) Validate(context.Context) error {
	*f.events = append(*f.events, "validate-paths")
	return nil
}
func (f *pubStagePrepared) Close() error { *f.events = append(*f.events, "close"); return f.closeErr }

type pubStageTools struct{ events *[]string }

func (f pubStageTools) Executable(context.Context, []persistence.SourceAnalysisToolSelection, string) (string, error) {
	*f.events = append(*f.events, "resolve")
	return "/managed/ffmpeg", nil
}

type pubStageCommand struct {
	events *[]string
	output string
	run    func(context.Context) error
}

type pubStageScopedCommand struct{ pubStageCommand }

func (f pubStageScopedCommand) Run(ctx context.Context, name string, args ...string) (PublicationCommandResult, error) {
	out, err := f.pubStageCommand.Run(ctx, name, args...)
	return PublicationCommandResult{Output: out, QuiescenceEstablished: true}, err
}

func (f pubStageCommand) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	*f.events = append(*f.events, "ffmpeg")
	if name != "/managed/ffmpeg" || len(args) == 0 {
		return nil, errors.New("unexpected command")
	}
	if f.run != nil {
		return nil, f.run(ctx)
	}
	if err := os.WriteFile(f.output, []byte("encoded"), 0600); err != nil {
		return nil, err
	}
	return nil, nil
}

type pubStageValidator struct {
	events *[]string
	err    error
}

type pubStageWriter struct {
	events *[]string
	err    error
}

func (f pubStageWriter) Write(_ context.Context, path string, _ map[string][]string, _ *metadata.Cover) (metadata.WriteResult, error) {
	*f.events = append(*f.events, "writer")
	return metadata.WriteResult{Tags: map[string][]string{"title": {"actual"}}}, f.err
}

func (f pubStageValidator) Validate(context.Context, string, persistence.PublicationInputs) (PublicationStageObservation, error) {
	*f.events = append(*f.events, "probe")
	size := int64(7)
	return PublicationStageObservation{Ownership: persistence.ArtifactOwnership{ExpectedSize: &size}, Audio: []byte(`{"codec":"aac"}`), Metadata: []byte(`{"title":"ok"}`), SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}, f.err
}

func pubStageSetup(t *testing.T, repo *pubStageRepo, command pubStageCommand, validateErr, closeErr error) (PublicationStagingRunner, *PublicationExecutionScope, persistence.PublicationInputs, *pubStagePrepared, *[]string) {
	t.Helper()
	events := []string{}
	repo.events = &events
	command.events = &events
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "input.flac")
	outputPath := filepath.Join(dir, "output.mka")
	installation := uuid.New()
	if err := os.WriteFile(inputPath, []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	stage := &pubStagePrepared{paths: PublicationStagePaths{InputPath: inputPath, OutputPath: outputPath, RelativePath: "album/track.mka"}, events: &events, closeErr: closeErr}
	command.output = outputPath
	guard := persistence.PublicationDeliveryGuard{DeliveryToken: "stage-test"}
	scope, err := NewPublicationExecutionScope(context.Background(), publication.NewCoordinator(), []publication.ResourceLock{{Key: "stage-test", Mode: publication.Exclusive}}, guard, func(context.Context, persistence.PublicationDeliveryGuard) (persistence.PublicationDeliveryClaimOutcome, error) {
		return persistence.PublicationDeliveryAcquired, nil
	}, pubStageScopedCommand{command}, repo)
	if err != nil {
		t.Fatal(err)
	}
	runner := PublicationStagingRunner{Repository: repo, PinnedTools: pubStageTools{&events}, Metadata: pubStageWriter{events: &events}, Validator: pubStageValidator{&events, validateErr}}
	return runner, scope, persistence.PublicationInputs{Version: persistence.PublicationInputsVersion, IntentKind: "audio", Format: publication.AudioFormatMKA, DesiredTags: map[string][]string{"title": {"track"}}, Tools: []persistence.SourceAnalysisToolSelection{{PackageKind: "ffmpeg", InstallationID: installation, RelativePath: "bin/ffmpeg", Executable: "ffmpeg", Version: "7", VersionBanner: "ffmpeg version 7"}, {PackageKind: "ffmpeg", InstallationID: installation, RelativePath: "bin/ffprobe", Executable: "ffprobe", Version: "7", VersionBanner: "ffmpeg version 7"}}, Audio: &persistence.PublicationAudioInput{WorkID: uuid.New(), RootID: uuid.New(), LocationID: uuid.New(), MediaVariantID: uuid.New(), ProbeResultID: uuid.New(), ConfiguredRootPath: dir, InventoryRootPath: dir, RelativePath: "input.flac", MTime: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), AnalysisPolicyVersion: 1, FFProbeVersion: "7", AudioFacts: persistence.PersistedAudioFacts{StreamIndex: 0, Codec: "flac", Lossless: pubStageBool(true), Container: publication.AudioContainerFLAC, SupportedSourceContainer: true}}}, stage, &events
}
func pubStageBool(v bool) *bool { return &v }

func containsPubStage(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func indexPubStage(xs []string, want string) int {
	for i, x := range xs {
		if x == want {
			return i
		}
	}
	return -1
}

func TestPublicationStagingSuccessTransitionsInOrderAndClosesBeforeQuiescence(t *testing.T) {
	repo := &pubStageRepo{}
	command := pubStageCommand{}
	runner, scope, input, stage, events := pubStageSetup(t, repo, command, nil, nil)
	got, err := runner.Run(context.Background(), scope, input, stage)
	if err != nil {
		t.Fatal(err)
	}
	if len(repo.phases) != 2 || repo.phases[0] != "preparing" || repo.phases[1] != "staged" {
		t.Fatalf("phases %v", repo.phases)
	}
	if len(got.Audio) == 0 || len(got.Metadata) == 0 || got.Ownership.ExpectedSize == nil || *got.Ownership.ExpectedSize != 7 || len(got.SHA256) != 64 {
		t.Fatalf("incomplete returned observation: %+v", got)
	}
	for _, pair := range [][2]string{{"ffmpeg", "writer"}, {"writer", "preparing"}, {"preparing", "probe"}, {"probe", "staged"}, {"staged", "close"}, {"close", "quiescent"}} {
		if indexPubStage(*events, pair[0]) < 0 || indexPubStage(*events, pair[0]) >= indexPubStage(*events, pair[1]) {
			t.Fatalf("wrong order %q before %q: %v", pair[0], pair[1], *events)
		}
	}
}

func TestPublicationStagingCommandFailureWaitsForReturnBeforeClose(t *testing.T) {
	failure := errors.New("ffmpeg failure")
	repo := &pubStageRepo{}
	var returned bool
	command := pubStageCommand{run: func(context.Context) error { returned = true; return failure }}
	runner, scope, input, stage, events := pubStageSetup(t, repo, command, nil, nil)
	_, err := runner.Run(context.Background(), scope, input, stage)
	if !errors.Is(err, failure) {
		t.Fatalf("error %v", err)
	}
	if !returned || indexPubStage(*events, "ffmpeg") < 0 || indexPubStage(*events, "close") <= indexPubStage(*events, "ffmpeg") || indexPubStage(*events, "quiescent") <= indexPubStage(*events, "close") {
		t.Fatalf("cleanup preceded command return: %v", *events)
	}
	if containsPubStage(*events, "writer") || containsPubStage(*events, "probe") {
		t.Fatalf("continued after command error: %v", *events)
	}
}

func TestPublicationStagingCancellationClosesOnlyAfterSynchronousCommandReturns(t *testing.T) {
	repo := &pubStageRepo{}
	ctx, cancel := context.WithCancel(context.Background())
	returned := false
	command := pubStageCommand{run: func(context.Context) error { cancel(); returned = true; return ctx.Err() }}
	runner, scope, input, stage, events := pubStageSetup(t, repo, command, nil, nil)
	_, err := runner.Run(ctx, scope, input, stage)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error %v", err)
	}
	if !returned || indexPubStage(*events, "close") < indexPubStage(*events, "ffmpeg") {
		t.Fatalf("closed before command returned: %v", *events)
	}
}

func TestPublicationStagingCloseFailureIsReturnedAndDoesNotQuiesce(t *testing.T) {
	closeErr := errors.New("close failed")
	repo := &pubStageRepo{}
	runner, scope, input, stage, _ := pubStageSetup(t, repo, pubStageCommand{}, nil, closeErr)
	_, err := runner.Run(context.Background(), scope, input, stage)
	if !errors.Is(err, closeErr) {
		t.Fatalf("close error lost: %v", err)
	}
	if containsPubStage(*repo.events, "quiescent") {
		t.Fatalf("quiesced after close failure: %v", *repo.events)
	}
}

func TestPublicationStagingProbeFailureDoesNotRecordValidatedTransition(t *testing.T) {
	probeErr := errors.New("probe failed")
	repo := &pubStageRepo{}
	command := pubStageCommand{}
	runner, scope, input, stage, _ := pubStageSetup(t, repo, command, probeErr, nil)
	_, err := runner.Run(context.Background(), scope, input, stage)
	if !errors.Is(err, probeErr) {
		t.Fatalf("error %v", err)
	}
	if len(repo.phases) != 1 || repo.phases[0] != "preparing" {
		t.Fatalf("transitions %v", repo.phases)
	}
}

func TestPublicationStagingStaleGuardAfterCommandSkipsMetadataWrite(t *testing.T) {
	repo := &pubStageRepo{staleAt: 3}
	command := pubStageCommand{}
	runner, scope, input, stage, events := pubStageSetup(t, repo, command, nil, nil)
	if _, err := runner.Run(context.Background(), scope, input, stage); err == nil {
		t.Fatal("expected stale guard")
	}
	if containsPubStage(*events, "writer") || containsPubStage(*events, "probe") {
		t.Fatalf("continued after stale guard: %v", *events)
	}
}

func TestPublicationStagingAlreadyOwnedDoesNotMutate(t *testing.T) {
	repo := &pubStageRepo{}
	runner, _, input, stage, events := pubStageSetup(t, repo, pubStageCommand{}, nil, nil)
	if _, err := runner.Run(context.Background(), nil, input, stage); err == nil {
		t.Fatal("expected claim rejection")
	}
	if containsPubStage(*events, "ffmpeg") || containsPubStage(*events, "writer") || containsPubStage(*events, "probe") {
		t.Fatalf("mutated already-owned delivery: %v", *events)
	}
}

func TestScopeBoundPublicationToolsRejectGuardMismatch(t *testing.T) {
	repo := &pubStageRepo{}
	_, scope, _, _, _ := pubStageSetup(t, repo, pubStageCommand{}, nil, nil)
	guard := scope.guard
	guard.DeliveryToken = "other-delivery"
	if _, err := NewScopeBoundPublicationPinnedTools(repo, scope, guard, tools.Platform{GOOS: "darwin", GOARCH: "arm64"}); err == nil {
		t.Fatal("expected mismatched guard rejection")
	}
	if scope.finalized || scope.unknown {
		t.Fatal("guard mismatch changed execution scope")
	}
}

func TestScopeBoundPublicationToolsRequireCapturedScope(t *testing.T) {
	repo := &pubStageRepo{}
	_, scope, _, _, _ := pubStageSetup(t, repo, pubStageCommand{}, nil, nil)
	guard := scope.guard
	adapter, err := NewScopeBoundPublicationPinnedTools(repo, scope, guard, tools.Platform{GOOS: "darwin", GOARCH: "arm64"})
	if err != nil {
		t.Fatal(err)
	}
	if adapter.scope != scope || adapter.resolver.scope != scope {
		t.Fatal("scoped adapter did not capture its execution scope")
	}

	otherRepo := &pubStageRepo{}
	runner, otherScope, input, stage, _ := pubStageSetup(t, otherRepo, pubStageCommand{}, nil, nil)
	runner.PinnedTools = adapter
	if _, err := runner.Run(context.Background(), otherScope, input, stage); err == nil {
		t.Fatal("scope-bound adapter was accepted for a foreign scope")
	}
}

func TestPublicationStagingUnknownCommandRetainsStageAndScope(t *testing.T) {
	repo := &pubStageRepo{}
	command := pubStageCommand{run: func(context.Context) error { return errors.New("lost process result") }}
	runner, scope, input, stage, events := pubStageSetup(t, repo, command, nil, nil)
	// Model a runner that cannot establish child-process quiescence.
	scope.commands = unknownPubStageCommand{}
	_, err := runner.Run(context.Background(), scope, input, stage)
	if err == nil || !scope.unknown {
		t.Fatalf("expected fenced unknown execution, got error=%v unknown=%v", err, scope.unknown)
	}
	if containsPubStage(*events, "close") || containsPubStage(*events, "quiescent") {
		t.Fatalf("unknown execution released stage authority: %v", *events)
	}
}

type unknownPubStageCommand struct{}

func (unknownPubStageCommand) Run(context.Context, string, ...string) (PublicationCommandResult, error) {
	return PublicationCommandResult{}, errors.New("quiescence unknown")
}

func TestPublicationStagingUnavailableDependencyClosesAcceptedStage(t *testing.T) {
	repo := &pubStageRepo{}
	runner, scope, input, stage, events := pubStageSetup(t, repo, pubStageCommand{}, nil, nil)
	runner.Validator = nil
	_, err := runner.Run(context.Background(), scope, input, stage)
	if err == nil {
		t.Fatal("expected missing dependency error")
	}
	if !containsPubStage(*events, "close") || !containsPubStage(*events, "quiescent") {
		t.Fatalf("accepted stage was not cleaned up: %v", *events)
	}
}

func TestPublicationStagingRejectsUnsupervisedRealValidatorPointer(t *testing.T) {
	for _, test := range []struct {
		name      string
		validator PublicationStageValidator
	}{
		{name: "pointer", validator: &RealPublicationStageValidator{}},
		{name: "nil pointer", validator: (*RealPublicationStageValidator)(nil)},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := &pubStageRepo{}
			runner, scope, input, stage, events := pubStageSetup(t, repo, pubStageCommand{}, nil, nil)
			runner.Validator = test.validator
			_, err := runner.Run(context.Background(), scope, input, stage)
			if err == nil {
				t.Fatal("expected unsupervised validator to be rejected")
			}
			if containsPubStage(*events, "ffmpeg") || len(repo.phases) != 0 {
				t.Fatalf("validator rejection occurred after staging work: events=%v phases=%v", *events, repo.phases)
			}
		})
	}
}

func TestPublicationStagingRejectsInvalidFrozenInputsBeforeMutation(t *testing.T) {
	repo := &pubStageRepo{}
	runner, scope, input, stage, events := pubStageSetup(t, repo, pubStageCommand{}, nil, nil)
	input.Version++
	if _, err := runner.Run(context.Background(), scope, input, stage); err == nil {
		t.Fatal("expected invalid frozen inputs")
	}
	if containsPubStage(*events, "ffmpeg") || containsPubStage(*events, "writer") || containsPubStage(*events, "probe") || !containsPubStage(*events, "close") || !containsPubStage(*events, "quiescent") {
		t.Fatalf("mutated with invalid frozen inputs: %v", *events)
	}
}

func TestPublicationStagingRejectsUnanchoredPathsBeforeMutation(t *testing.T) {
	repo := &pubStageRepo{}
	runner, scope, input, stage, events := pubStageSetup(t, repo, pubStageCommand{}, nil, nil)
	stage.paths.OutputPath = "relative/output.mka"
	if _, err := runner.Run(context.Background(), scope, input, stage); err == nil {
		t.Fatal("expected invalid stage paths")
	}
	if containsPubStage(*events, "ffmpeg") || containsPubStage(*events, "writer") || containsPubStage(*events, "probe") || !containsPubStage(*events, "close") || !containsPubStage(*events, "quiescent") {
		t.Fatalf("used invalid stage paths: %v", *events)
	}
}

func TestPublicationStageAudioPlanRewritesMP3WithoutTranscoding(t *testing.T) {
	facts := persistence.PersistedAudioFacts{StreamIndex: 0, Codec: "mp3", Lossless: pubStageBool(false), Container: publication.AudioContainerMP3, SupportedSourceContainer: true}
	input := persistence.PublicationInputs{Format: publication.AudioFormatM4A, Rewrite: &persistence.PublicationRewriteInput{AudioFacts: facts}}
	plan, err := publicationStageAudioPlan(input, facts)
	if err != nil {
		t.Fatal(err)
	}
	args, err := plan.FFmpegArgs(filepath.Join(t.TempDir(), "input.mp3"), filepath.Join(t.TempDir(), "output.mp3"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !containsPubStagePair(args, "-c:a", "copy") || !containsPubStagePair(args, "-f", "mp3") || containsPubStagePair(args, "-c:a", "aac") {
		t.Fatalf("MP3 rewrite arguments = %v", args)
	}
}

func containsPubStagePair(args []string, first, second string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == first && args[i+1] == second {
			return true
		}
	}
	return false
}

var _ tools.CommandRunner = pubStageCommand{}
