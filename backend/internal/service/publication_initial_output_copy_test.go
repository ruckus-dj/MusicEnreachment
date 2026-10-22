package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

func preparedManualLyricsFLACStage(t *testing.T, automaticRetry ...bool) (publicationStageFixture, *preparedPublicationStage, []byte) {
	t.Helper()
	fixture := makeRewriteFixture(t)
	if err := os.Remove(filepath.Join(fixture.output, "current.mp3")); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(fixture.output, "current.flac")
	contents, err := os.ReadFile("../integrations/metadata/testdata/eg.flac")
	if err != nil {
		t.Fatal(err)
	}
	contents, err = flacFixtureWithoutPictures(contents)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(current, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (metadata.Writer{}).Write(context.Background(), current, map[string][]string{"TITLE": {"Track"}, "ARTIST": {"Artist"}}, nil); err != nil {
		t.Fatal(err)
	}
	contents, err = os.ReadFile(current)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(current)
	if err != nil {
		t.Fatal(err)
	}
	size, mtime := info.Size(), info.ModTime().UnixMicro()
	digest := sha256.Sum256(contents)
	fixture.repo.input.Rewrite.RelativePath = "current.flac"
	fixture.repo.input.Rewrite.Ownership = persistence.PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: hex.EncodeToString(digest[:])}
	fixture.repo.input.Version = persistence.PublicationInputsVersion
	fixture.repo.input.QualityPolicyVersion = publication.QualityPolicyVersion
	fixture.repo.input.Format = "source"
	lossless := true
	fixture.repo.input.Rewrite.AudioFacts = persistence.PersistedAudioFacts{StreamIndex: 0, Codec: "flac", Lossless: &lossless, Container: "flac", SupportedSourceContainer: true}
	fixture.repo.input.ManualLyrics = &persistence.PublicationManualLyricsInput{State: "explicit_empty", ConfirmedCurrentGeneration: fixture.repo.input.Rewrite.CurrentGeneration}
	retry := len(automaticRetry) == 1 && automaticRetry[0]
	if retry {
		capture, err := (metadata.Reader{}).Read(context.Background(), current, metadata.ReadRequest{CoverInspection: true})
		if err != nil {
			t.Fatal(err)
		}
		assets, err := ObservePublicationAssets(capture)
		if err != nil {
			t.Fatal(err)
		}
		fixture.repo.input.ManualLyrics = nil
		fixture.repo.input.AutomaticLyricsRetry = &persistence.PublicationAutomaticLyricsRetryInput{ExpectedMetadataRevision: 7, ConfirmedCurrentGeneration: fixture.repo.input.Rewrite.CurrentGeneration, CurrentAssets: assets}
	}
	repository := &optionalLyricsV3Repository{stagePreparationRepository: fixture.repo}
	fixture.preparer.Repository = repository
	var stageValue PreparedPublicationStage
	if retry {
		stageValue, err = fixture.preparer.prepare(context.Background(), fixture.guard, persistence.PublicationDeliveryAcquired, true, false, nil)
	} else {
		stageValue, err = fixture.preparer.prepareManualLyricsFLAC(context.Background(), fixture.guard, persistence.PublicationDeliveryAcquired, true)
	}
	if err != nil {
		t.Fatal(err)
	}
	stage := stageValue.(*preparedPublicationStage)
	t.Cleanup(func() { _ = stage.Close(); _ = fixture.preparer.OutputParent.Close() })
	return fixture, stage, contents
}

func initialOutputCopyScope(t *testing.T, guard persistence.PublicationDeliveryGuard) (*PublicationExecutionScope, *publicationSyncReservation) {
	t.Helper()
	scope := newScopeForTest(t, &scopeCommandFake{}, &scopeRepositoryFake{valid: true, marked: true})
	scope.guard = guard
	return scope, scope.reserveSynchronousWork()
}

func TestInitialOutputCopyCreatesNativePrivateOutput(t *testing.T) {
	fixture, stage, expected := preparedManualLyricsFLACStage(t)
	scope, reservation := initialOutputCopyScope(t, fixture.guard)
	proof, err := stage.initialOutputCopy(context.Background(), scope, reservation)
	if err != nil {
		t.Fatal(err)
	}
	if proof == nil || proof.stage != stage {
		t.Fatalf("copy proof = %#v", proof)
	}
	got, err := os.ReadFile(stage.paths.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	wantHash, gotHash := sha256.Sum256(expected), sha256.Sum256(got)
	if gotHash != wantHash {
		t.Fatal("initial output bytes differ from the prepared FLAC")
	}
	inputInfo, err := os.Stat(stage.paths.InputPath)
	if err != nil {
		t.Fatal(err)
	}
	outputInfo, err := os.Stat(stage.paths.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(inputInfo, outputInfo) {
		t.Fatal("initial output reused the input native file identity")
	}
	scope.releaseSynchronousWork(reservation)
}

func TestInitialOutputCopyRejectsExistingOutputAndSymlinkWithoutRemoving(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		t.Run(map[bool]string{false: "existing", true: "symlink"}[symlink], func(t *testing.T) {
			fixture, stage, _ := preparedManualLyricsFLACStage(t)
			outside := filepath.Join(fixture.output, "sentinel")
			if err := os.WriteFile(outside, []byte("preserve"), 0o600); err != nil {
				t.Fatal(err)
			}
			if symlink {
				if err := os.Symlink(outside, stage.paths.OutputPath); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(stage.paths.OutputPath, []byte("existing"), 0o600); err != nil {
				t.Fatal(err)
			}
			scope, reservation := initialOutputCopyScope(t, fixture.guard)
			if _, err := stage.initialOutputCopy(context.Background(), scope, reservation); err == nil {
				t.Fatal("expected exclusive creation failure")
			}
			got, err := os.ReadFile(outside)
			if err != nil || string(got) != "preserve" {
				t.Fatalf("outside sentinel changed: %q, %v", got, err)
			}
			if !symlink {
				got, err = os.ReadFile(stage.paths.OutputPath)
				if err != nil || string(got) != "existing" {
					t.Fatalf("existing output changed: %q, %v", got, err)
				}
			} else if info, err := os.Lstat(stage.paths.OutputPath); err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("symlink was not preserved: %v", err)
			}
		})
	}
}

func TestInitialOutputCopyRejectsRestoredMtimeCurrentTamperingBeforeCreation(t *testing.T) {
	fixture, stage, _ := preparedManualLyricsFLACStage(t)
	current := filepath.Join(fixture.output, "current.flac")
	info, err := os.Stat(current)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(current)
	if err != nil {
		t.Fatal(err)
	}
	contents[len(contents)-1] ^= 0x1
	if err := os.WriteFile(current, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(current, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	scope, reservation := initialOutputCopyScope(t, fixture.guard)
	if _, err := stage.initialOutputCopy(context.Background(), scope, reservation); err == nil {
		t.Fatal("expected frozen SHA-256 ownership rejection")
	}
	if _, err := os.Lstat(stage.paths.OutputPath); !os.IsNotExist(err) {
		t.Fatalf("output was created despite tampered current input: %v", err)
	}
}
