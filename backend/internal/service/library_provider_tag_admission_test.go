package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// The automatic provider-tag producer is intentionally not wired into a runtime
// caller yet. These tests exercise its admission entry point and private command
// wrapper without enabling that runtime path.
func TestAdmitAutomaticProviderTagsRejectsMissingDependencies(t *testing.T) {
	ctx := context.Background()
	releaseID := uuid.New()
	positionID := uuid.New()
	guard := persistence.PublicationReconciliationDeliveryGuard{}

	tests := []struct {
		name    string
		service *LibraryPublications
	}{
		{name: "nil receiver"},
		{name: "empty service", service: &LibraryPublications{}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			operationID, err := testCase.service.admitAutomaticProviderTags(ctx, releaseID, positionID, guard)
			if operationID != uuid.Nil {
				t.Fatalf("operation ID = %s, want nil UUID", operationID)
			}
			if err == nil || err.Error() != "provider-tag admission dependencies are unavailable" {
				t.Fatalf("error = %v, want missing dependency rejection", err)
			}
		})
	}
}

type providerTagScriptedCommandResult struct {
	result PublicationCommandResult
	err    error
}

type providerTagScriptedCommandRunner struct {
	responses []providerTagScriptedCommandResult
	calls     int
}

func (runner *providerTagScriptedCommandRunner) Run(context.Context, string, ...string) (PublicationCommandResult, error) {
	if runner.calls >= len(runner.responses) {
		return PublicationCommandResult{}, errors.New("unexpected provider-tag command")
	}
	response := runner.responses[runner.calls]
	runner.calls++
	return response.result, response.err
}

func TestPublicationProducerCommandsKeepQuiescenceLossSticky(t *testing.T) {
	commandErr := errors.New("ffmpeg failed")
	runner := &providerTagScriptedCommandRunner{responses: []providerTagScriptedCommandResult{
		{result: PublicationCommandResult{Output: []byte("first"), QuiescenceEstablished: true}},
		{result: PublicationCommandResult{Output: []byte("second"), QuiescenceEstablished: false}},
		{result: PublicationCommandResult{Output: []byte("third"), QuiescenceEstablished: true}},
		{result: PublicationCommandResult{Output: []byte("fourth"), QuiescenceEstablished: true}, err: commandErr},
	}}
	commands := &publicationProducerCommands{delegate: runner, quiescent: true}
	ctx := context.Background()

	result, err := commands.Run(ctx, "ffmpeg", "-version")
	if err != nil || string(result.Output) != "first" || !commands.quiescent {
		t.Fatalf("quiescent result: output=%q err=%v quiescent=%v", result.Output, err, commands.quiescent)
	}

	result, err = commands.Run(ctx, "ffmpeg", "-version")
	if err != nil || string(result.Output) != "second" || commands.quiescent {
		t.Fatalf("unknown quiescence result: output=%q err=%v quiescent=%v", result.Output, err, commands.quiescent)
	}

	if _, err := commands.Run(ctx, "ffmpeg", "-version"); err != nil {
		t.Fatalf("later quiescent command: %v", err)
	}
	if commands.quiescent {
		t.Fatal("a later quiescent result restored unreliable quiescence")
	}

	result, err = commands.Run(ctx, "ffmpeg", "-version")
	if !errors.Is(err, commandErr) || string(result.Output) != "fourth" {
		t.Fatalf("errored command: output=%q err=%v", result.Output, err)
	}
	if commands.quiescent {
		t.Fatal("an errored quiescent result restored unreliable quiescence")
	}
	if runner.calls != len(runner.responses) {
		t.Fatalf("delegate calls = %d, want %d", runner.calls, len(runner.responses))
	}
}

func TestPrivatePublicationSHA256HashesOwnedBytesAndRejectsMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "current.flac")
	contents := []byte("provider-tag private CURRENT\x00\xff")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("write private CURRENT: %v", err)
	}
	wantDigest := sha256.Sum256(contents)
	want := hex.EncodeToString(wantDigest[:])

	digest, err := privatePublicationSHA256(path)
	if err != nil {
		t.Fatalf("hash private CURRENT: %v", err)
	}
	if digest != want {
		t.Fatalf("digest = %q, want %q", digest, want)
	}

	missing := filepath.Join(t.TempDir(), "missing.flac")
	digest, err = privatePublicationSHA256(missing)
	if err == nil {
		t.Fatal("hashing a missing private CURRENT returned no error")
	}
	if digest != "" {
		t.Fatalf("missing private CURRENT digest = %q, want empty", digest)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing private CURRENT error = %v, want not-exist", err)
	}
}
