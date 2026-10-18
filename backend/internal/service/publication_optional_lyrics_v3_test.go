package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type optionalLyricsV3Repository struct {
	*stagePreparationRepository
	outcome persistence.PublicationOptionalLyricsOutcome
	records []persistence.PublicationOptionalLyricsOutcomeEvidence
	called  int
}

func (repository *optionalLyricsV3Repository) GuardedRecordPublicationOptionalLyricsOutcome(_ context.Context, _ persistence.PublicationDeliveryGuard, evidence persistence.PublicationOptionalLyricsOutcomeEvidence) (persistence.PublicationOptionalLyricsOutcome, error) {
	repository.called++
	repository.records = append(repository.records, evidence)
	return repository.outcome, nil
}

func (repository *optionalLyricsV3Repository) PublicationManualLyricsPreparationInputs(_ context.Context, _ persistence.PublicationDeliveryGuard) (persistence.PublicationInputs, bool, error) {
	return repository.input, true, nil
}

func TestApplyAndRecordPublicationManualLyricsUsesNativeEvidence(t *testing.T) {
	for _, test := range []struct {
		name       string
		writer     PublicationMetadataWriter
		ack        persistence.PublicationOptionalLyricsOutcome
		wantRecord bool
	}{
		{name: "recorded", writer: metadata.Writer{}, ack: persistence.PublicationOptionalLyricsRecorded, wantRecord: true},
		{name: "stale acknowledgement", writer: metadata.Writer{}, ack: persistence.PublicationOptionalLyricsStale},
		{name: "opaque writer failure", writer: optionalLyricsTestWriter{write: func(context.Context, string, map[string][]string) error { return errors.New("opaque writer failure") }}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			fixture := makeRewriteFixture(t)
			t.Cleanup(func() { _ = fixture.preparer.OutputParent.Close() })
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
			if _, err := (metadata.Writer{}).Write(ctx, current, map[string][]string{"TITLE": {"Track"}, "ARTIST": {"Artist"}}, nil); err != nil {
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
			fixture.repo.input.Version = persistence.PublicationInputsVersion3
			fixture.repo.input.QualityPolicyVersion = 1
			fixture.repo.input.Format = "source"
			lossless := true
			fixture.repo.input.Rewrite.AudioFacts = persistence.PersistedAudioFacts{StreamIndex: 0, Codec: "flac", Lossless: &lossless, Container: "flac", SupportedSourceContainer: true}
			lyrics := "[ar:Artist]\r\n[00:01.00]Words\r\n"
			fixture.repo.input.ManualLyrics = &persistence.PublicationManualLyricsInput{State: "value", Value: lyrics, ConfirmedCurrentGeneration: fixture.repo.input.Rewrite.CurrentGeneration}
			fixture.repo.input.DesiredTags = map[string][]string{"TITLE": {"Track"}, "ARTIST": {"Artist"}}

			repository := &optionalLyricsV3Repository{stagePreparationRepository: fixture.repo, outcome: test.ack}
			fixture.preparer.Repository = repository
			stage, err := fixture.preparer.prepareManualLyricsFLAC(ctx, fixture.guard, persistence.PublicationDeliveryAcquired, true)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = stage.Close() })
			prepared := stage.(*preparedPublicationStage)
			copiedInfo, err := os.Stat(prepared.paths.InputPath)
			if err != nil {
				t.Fatal(err)
			}
			copiedBytes, err := os.ReadFile(prepared.paths.InputPath)
			if err != nil {
				t.Fatal(err)
			}
			copiedDigest := sha256.Sum256(copiedBytes)
			copiedMTime := copiedInfo.ModTime().UnixMicro()
			if err := os.WriteFile(prepared.paths.OutputPath, contents, 0o600); err != nil {
				t.Fatal(err)
			}
			scopeRepository := &scopeRepositoryFake{valid: true, marked: true}
			scope := newScopeForTest(t, &scopeCommandFake{}, scopeRepository)
			prepared.guard = scope.guard
			reservation := scope.reserveSynchronousWork()
			reader := metadata.Reader{}
			result, callErr := applyAndRecordPublicationManualLyrics(ctx, scope, reservation, stage, fixture.repo.input, test.writer, reader,
				func(context.Context, string) error { return nil }, func(context.Context, string, map[string][]string) error { return nil },
				func(ctx context.Context, path string, _ persistence.PublicationInputs) (persistence.PublicationStageObservation, error) {
					data, err := os.ReadFile(path)
					if err != nil {
						return persistence.PublicationStageObservation{}, err
					}
					info, err := os.Stat(path)
					if err != nil {
						return persistence.PublicationStageObservation{}, err
					}
					sum := sha256.Sum256(data)
					return persistence.PublicationStageObservation{Version: 1, RelativePath: prepared.paths.RelativePath, Ownership: persistence.ArtifactOwnership{ExpectedSize: lyricsPtr(info.Size()), ExpectedMTime: lyricsPtr(info.ModTime().UnixMicro())}, PayloadSHA256: hex.EncodeToString(sum[:]), ActualAudio: []byte(`{}`), ActualMetadata: []byte(`{}`)}, nil
				})
			if test.wantRecord {
				if callErr != nil {
					t.Fatal(callErr)
				}
				if repository.called != 1 || len(repository.records) != 1 {
					t.Fatalf("record calls = %d, evidence = %d", repository.called, len(repository.records))
				}
				evidence := repository.records[0]
				if evidence.InputIdentity.SHA256 != hex.EncodeToString(copiedDigest[:]) || evidence.InputIdentity.SizeBytes == nil || *evidence.InputIdentity.SizeBytes != copiedInfo.Size() || evidence.InputIdentity.MTime == nil || *evidence.InputIdentity.MTime != copiedMTime || prepared.preparationRecord.CopiedInput == nil || prepared.preparationRecord.CopiedInput.MTime == nil || *evidence.InputIdentity.MTime != *prepared.preparationRecord.CopiedInput.MTime {
					t.Fatalf("input identity is not the copied immutable input: %#v", evidence.InputIdentity)
				}
				if evidence.InputIdentity.MTime == prepared.preparationRecord.CopiedInput.MTime {
					t.Fatal("captured input mtime aliases the copied preparation identity")
				}
				if evidence.InputIdentity.MTime == fixture.repo.input.Rewrite.Ownership.MTime {
					t.Fatal("captured input mtime aliases the frozen original identity")
				}
				if evidence.OutputIdentity.SHA256 != evidence.FinalObservation.PayloadSHA256 || evidence.OutputIdentity.SizeBytes == nil || evidence.FinalObservation.Ownership.ExpectedSize == nil || *evidence.OutputIdentity.SizeBytes != *evidence.FinalObservation.Ownership.ExpectedSize || evidence.OutputIdentity.MTime == nil || evidence.FinalObservation.Ownership.ExpectedMTime == nil || *evidence.OutputIdentity.MTime != *evidence.FinalObservation.Ownership.ExpectedMTime || evidence.FinalObservation.RelativePath != prepared.paths.RelativePath {
					t.Fatalf("output identity does not match final authority: %#v %#v", evidence.OutputIdentity, evidence.FinalObservation)
				}
				if evidence.InputIdentity.MTime == evidence.OutputIdentity.MTime {
					t.Fatal("input and output mtime pointers were not detached")
				}
				if result.Lyrics.State != persistence.PublicationAssetEmbedded {
					t.Fatalf("lyrics result = %#v", result.Lyrics)
				}
			} else {
				if callErr == nil {
					t.Fatal("expected V3 application failure")
				}
				wantCalls := 0
				if test.name == "stale acknowledgement" {
					wantCalls = 1
				}
				if repository.called != wantCalls {
					t.Fatalf("outcome recorder called %d times, want %d", repository.called, wantCalls)
				}
				scope.mu.Lock()
				unknown, borrowed := scope.unknown, reservation.borrowed
				scope.mu.Unlock()
				if !unknown || !borrowed {
					t.Fatalf("failed attempt fence: unknown=%v borrowed=%v", unknown, borrowed)
				}
				if test.name == "stale acknowledgement" && !strings.Contains(callErr.Error(), "stale") {
					t.Fatalf("stale acknowledgement error = %v", callErr)
				}
			}
		})
	}
}

func TestPublicationStagePreparerRejectsV3ThroughGenericReaderBeforeMutation(t *testing.T) {
	fixture := makeRewriteFixture(t)
	t.Cleanup(func() { _ = fixture.preparer.OutputParent.Close() })
	if err := os.Remove(filepath.Join(fixture.output, "current.mp3")); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(fixture.output, "current.flac")
	contents, err := os.ReadFile("../integrations/metadata/testdata/eg.flac")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(current, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(current)
	if err != nil {
		t.Fatal(err)
	}
	size, mtime := info.Size(), info.ModTime().UnixMicro()
	digest := sha256.Sum256(contents)
	fixture.repo.input.Version = persistence.PublicationInputsVersion3
	fixture.repo.input.QualityPolicyVersion = 1
	fixture.repo.input.Rewrite.RelativePath = "current.flac"
	fixture.repo.input.Rewrite.Ownership = persistence.PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: hex.EncodeToString(digest[:])}
	lossless := true
	fixture.repo.input.Rewrite.AudioFacts = persistence.PersistedAudioFacts{StreamIndex: 0, Codec: "flac", Lossless: &lossless, Container: "flac", SupportedSourceContainer: true}
	fixture.repo.input.ManualLyrics = &persistence.PublicationManualLyricsInput{State: "value", Value: "[00:01.00]Words", ConfirmedCurrentGeneration: fixture.repo.input.Rewrite.CurrentGeneration}

	if _, err := fixture.prepare(t); err == nil || !strings.Contains(err.Error(), "private manual lyrics FLAC preparation path") {
		t.Fatalf("generic Prepare error = %v, want V3 capability rejection", err)
	}
	entries, err := os.ReadDir(fixture.output)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "current.flac" || len(fixture.repo.records) != 0 {
		t.Fatalf("generic V3 preparation mutated output: entries=%v records=%v", entries, fixture.repo.records)
	}
}

func lyricsPtr(value int64) *int64 { return &value }
