package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

func TestScopeBoundPublicationStageProbeRejectsForeignAuthority(t *testing.T) {
	repo1, repo2 := &pubStageRepo{}, &pubStageRepo{}
	_, scope1, _, _, _ := pubStageSetup(t, repo1, pubStageCommand{}, nil, nil)
	_, scope2, _, _, _ := pubStageSetup(t, repo2, pubStageCommand{}, nil, nil)

	pinned, err := NewScopeBoundPublicationPinnedTools(repo1, scope1, scope1.guard, tools.Platform{GOOS: "darwin", GOARCH: "arm64"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewScopeBoundPublicationStageProbe(scope2, pinned, nil); err == nil {
		t.Fatal("expected foreign scope to be rejected")
	}

	foreignResolver := *pinned.resolver
	foreignResolver.scope = scope2
	foreignPinned := &GuardedPublicationPinnedTools{resolver: &foreignResolver, guard: pinned.guard, scope: scope1}
	if _, err := NewScopeBoundPublicationStageProbe(scope1, foreignPinned, nil); err == nil {
		t.Fatal("expected foreign resolver scope to be rejected")
	}
}

func TestPublicationStageProbeRejectsInjectedFunctionInSupervisedValidator(t *testing.T) {
	_, scope, _, _, _ := pubStageSetup(t, &pubStageRepo{}, pubStageCommand{}, nil, nil)
	validator := RealPublicationStageValidator{Probe: func(context.Context, string, sourcefs.RegularFile, publication.AudioContainer) ([]byte, error) {
		return []byte(`{}`), nil
	}}
	if err := validator.validateSupervised(scope); err == nil {
		t.Fatal("expected arbitrary probe function to be rejected")
	}
}

func TestScopeRunFileWithoutBackendDoesNotLaunch(t *testing.T) {
	_, selections, pinnedRepo := pinnedToolFixture(t, true)
	command := &probeNoFileCommand{}
	repo := &probePinnedRepository{pinnedToolRepo: pinnedRepo}
	guard := persistence.PublicationDeliveryGuard{DeliveryToken: "no-file-backend"}
	scope, err := NewPublicationExecutionScope(context.Background(), publication.NewCoordinator(), nil, guard,
		func(context.Context, persistence.PublicationDeliveryGuard) (persistence.PublicationDeliveryClaimOutcome, error) {
			return persistence.PublicationDeliveryAcquired, nil
		}, command, repo)
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := NewScopeBoundPublicationPinnedTools(repo, scope, guard, tools.Platform{GOOS: "linux", GOARCH: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	probe, err := NewScopeBoundPublicationStageProbe(scope, pinned, selections)
	if err != nil {
		t.Fatal(err)
	}
	file := newPublicationProbeTestFile(t)
	t.Cleanup(func() {
		if err := file.file.Close(); err != nil {
			t.Errorf("close input: %v", err)
		}
	})

	path, err := pinned.Executable(context.Background(), selections, "ffprobe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := probe.Probe(context.Background(), path, file, publication.AudioContainerMP3); err == nil {
		t.Fatal("expected unavailable file-command backend")
	}
	if command.runs == 0 || scope.unknown {
		t.Fatalf("backend absence launched or fenced a command: runs=%d unknown=%v", command.runs, scope.unknown)
	}
	if err := scope.Finalize(context.Background(), func() error { return nil }); err != nil {
		t.Fatalf("finalize after unavailable backend: %v", err)
	}
}

type probeNoFileCommand struct{ runs int }

func (command *probeNoFileCommand) Run(_ context.Context, executable string, _ ...string) (PublicationCommandResult, error) {
	command.runs++
	version := "ffmpeg version 7.0 build\n"
	if filepath.Base(executable) == "ffprobe" {
		version = "ffprobe version 7.0 build\n"
	}
	return PublicationCommandResult{Output: []byte(version), QuiescenceEstablished: true}, nil
}

func TestScopeRunFileUnknownQuiescenceRetainsDuplicate(t *testing.T) {
	command := &probeUnknownFileCommand{}
	repository := &probeQuiescenceRepository{}
	scope := &PublicationExecutionScope{commands: command, repository: repository}
	t.Cleanup(func() { closeTestScopeOwnedFiles(t, scope) })
	file := newPublicationProbeTestFile(t)
	t.Cleanup(func() {
		if err := file.file.Close(); err != nil {
			t.Errorf("close input: %v", err)
		}
	})

	_, err := scope.RunFile(context.Background(), "/probe", nil, file)
	if err == nil || !errors.Is(err, errProbeUnknown) {
		t.Fatalf("expected unknown quiescence error, got %v", err)
	}
	if !scope.unknown || len(scope.ownedFiles) != 1 {
		t.Fatalf("unknown command did not retain the duplicate: unknown=%v owned=%d", scope.unknown, len(scope.ownedFiles))
	}
	if _, statErr := scope.ownedFiles[0].Stat(); statErr != nil {
		t.Fatalf("retained descriptor was closed: %v", statErr)
	}
	if err := scope.Finalize(context.Background(), func() error { t.Error("closed stage during unknown execution"); return nil }); err == nil {
		t.Fatal("unknown command unexpectedly finalized")
	}
	if repository.quiescent {
		t.Fatal("unknown execution was recorded as quiescent")
	}
}

func TestScopeBoundPublicationStageProbeUsesAllStreamFileRequest(t *testing.T) {
	root, selections, pinnedRepo := pinnedToolFixture(t, true)
	command := &probeFileBackend{output: []byte(`{"streams":[{"codec_type":"audio"}],"format":{"format_name":"matroska"}}`)}
	repo := &probePinnedRepository{pinnedToolRepo: pinnedRepo}
	guard := persistence.PublicationDeliveryGuard{DeliveryToken: "probe-test"}
	scope, err := NewPublicationExecutionScope(context.Background(), publication.NewCoordinator(), nil, guard,
		func(context.Context, persistence.PublicationDeliveryGuard) (persistence.PublicationDeliveryClaimOutcome, error) {
			return persistence.PublicationDeliveryAcquired, nil
		}, command, repo)
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := NewScopeBoundPublicationPinnedTools(repo, scope, guard, tools.Platform{GOOS: "linux", GOARCH: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	probe, err := NewScopeBoundPublicationStageProbe(scope, pinned, selections)
	if err != nil {
		t.Fatal(err)
	}
	input := newPublicationProbeTestFile(t)
	t.Cleanup(func() {
		if err := input.file.Close(); err != nil {
			t.Errorf("close input: %v", err)
		}
	})
	path, err := pinned.Executable(context.Background(), selections, "ffprobe")
	if err != nil {
		t.Fatal(err)
	}
	got, err := probe.Probe(context.Background(), path, input, publication.AudioContainerMKA)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(command.output) {
		t.Fatalf("probe output %q, want %q", got, command.output)
	}
	args := strings.Join(command.request.Args, " ")
	for _, expected := range []string{"-f matroska", "-show_format", "-show_streams", "-show_chapters", "-of json", "fd:"} {
		if !strings.Contains(args, expected) {
			t.Errorf("file probe arguments %q omit %q", args, expected)
		}
	}
	if !strings.Contains(args, "-protocol_whitelist fd") || !strings.Contains(args, "-fd ") || command.request.Executable != filepath.Join(root, "ffmpeg", "release-1", "ffprobe") {
		t.Fatalf("unexpected file probe request: %+v", command.request)
	}
	if command.request.Input == nil {
		t.Fatal("file probe did not receive the scope-owned duplicate")
	}
	if _, err := command.request.Input.Stat(); err == nil {
		t.Fatal("scope did not close quiescent duplicate")
	}
	if err := scope.Finalize(context.Background(), func() error { return nil }); err != nil {
		t.Fatalf("finalize after quiescent probe: %v", err)
	}
}

type probePinnedRepository struct{ *pinnedToolRepo }

func (*probePinnedRepository) MarkPublicationQuiescent(context.Context, persistence.PublicationDeliveryGuard) (bool, error) {
	return true, nil
}

type probeFileBackend struct {
	request tools.FileCommandRequest
	output  []byte
}

func (backend *probeFileBackend) Run(_ context.Context, executable string, _ ...string) (PublicationCommandResult, error) {
	if filepath.Base(executable) == "ffprobe" {
		return PublicationCommandResult{Output: []byte("ffprobe version 7.0 build\n"), QuiescenceEstablished: true}, nil
	}
	return PublicationCommandResult{Output: []byte("ffmpeg version 7.0 build\n"), QuiescenceEstablished: true}, nil
}

func (backend *probeFileBackend) RunFile(_ context.Context, request tools.FileCommandRequest) (PublicationCommandResult, error) {
	backend.request = request
	return PublicationCommandResult{Output: backend.output, QuiescenceEstablished: true}, nil
}

func newPublicationProbeTestFile(t *testing.T) publicationStageFile {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "probe-input-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("input"); err != nil {
		if closeErr := file.Close(); closeErr != nil {
			t.Errorf("close failed probe input: %v", closeErr)
		}
		t.Fatal(err)
	}
	return publicationStageFile{file: file}
}

var _ sourcefs.RegularFile = publicationStageFile{}

var errProbeUnknown = errors.New("probe backend lost process result")

type probeUnknownFileCommand struct{}

func (probeUnknownFileCommand) Run(context.Context, string, ...string) (PublicationCommandResult, error) {
	return PublicationCommandResult{}, errors.New("unexpected command")
}

type probeQuiescenceRepository struct{ quiescent bool }

func (*probeQuiescenceRepository) CheckPublicationDelivery(context.Context, persistence.PublicationDeliveryGuard) (bool, error) {
	return true, nil
}

func (repository *probeQuiescenceRepository) MarkPublicationQuiescent(context.Context, persistence.PublicationDeliveryGuard) (bool, error) {
	repository.quiescent = true
	return true, nil
}

func (probeUnknownFileCommand) RunFile(_ context.Context, request tools.FileCommandRequest) (PublicationCommandResult, error) {
	if request.Input == nil {
		return PublicationCommandResult{}, errors.New("missing duplicated input")
	}
	return PublicationCommandResult{}, errProbeUnknown
}
