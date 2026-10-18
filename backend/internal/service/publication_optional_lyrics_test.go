package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

type optionalLyricsTestStage struct{ path string }

func (stage optionalLyricsTestStage) Paths() PublicationStagePaths {
	return PublicationStagePaths{OutputPath: stage.path}
}
func (optionalLyricsTestStage) Validate(context.Context) error { return nil }
func (optionalLyricsTestStage) Close() error                   { return nil }

type optionalLyricsTestWriter struct {
	write func(context.Context, string, map[string][]string) error
}

func (writer optionalLyricsTestWriter) Write(ctx context.Context, path string, tags map[string][]string, _ *metadata.Cover) (metadata.WriteResult, error) {
	if writer.write != nil {
		return metadata.WriteResult{}, writer.write(ctx, path, tags)
	}
	return metadata.WriteResult{}, nil
}

type optionalLyricsTestReader struct {
	read func(context.Context, string, metadata.ReadRequest) (metadata.Capture, error)
}

func (reader optionalLyricsTestReader) Read(ctx context.Context, path string, request metadata.ReadRequest) (metadata.Capture, error) {
	return reader.read(ctx, path, request)
}

func runOptionalLyricsTest(t *testing.T, writer PublicationMetadataWriter, reader interface {
	Read(context.Context, string, metadata.ReadRequest) (metadata.Capture, error)
}, lyrics string, recorders ...func(context.Context, publicationOptionalLyricsOutcomeEvidence) error) (*PublicationExecutionScope, *scopeRepositoryFake, publicationOptionalLyricsResult, error) {
	t.Helper()
	repository := &scopeRepositoryFake{valid: true, marked: true}
	scope := newScopeForTest(t, &scopeCommandFake{result: PublicationCommandResult{QuiescenceEstablished: true}}, repository)
	reservation := scope.reserveSynchronousWork()
	path := filepath.Join(t.TempDir(), "output.flac")
	input := persistence.PublicationInputs{
		Version:              persistence.PublicationInputsVersion,
		QualityPolicyVersion: publication.QualityPolicyVersion,
		ManualLyrics:         &persistence.PublicationManualLyricsInput{State: "value", Value: lyrics},
		DesiredTags:          map[string][]string{"TITLE": {"Track"}, "ARTIST": {"Artist"}},
	}
	validations := 0
	requirements := publicationOptionalLyricsRequirements{
		validateAudioAndAssets: func(context.Context, string) error { validations++; return nil },
		restoreRequiredAssets:  func(context.Context, string, map[string][]string) error { return nil },
	}
	if len(recorders) > 0 {
		requirements.recordOutcome = recorders[0]
	}
	result, err := applyPublicationOptionalLyrics(context.Background(), scope, reservation, optionalLyricsTestStage{path}, input, writer, reader, requirements)
	return scope, repository, result, err
}

func TestApplyOptionalLyricsRecordsActualHelperReadbackBeforeReturning(t *testing.T) {
	lyrics := "[00:01.00]line\n"
	var recorded *publicationOptionalLyricsOutcomeEvidence
	_, _, result, err := runOptionalLyricsTest(t, optionalLyricsTestWriter{}, optionalLyricsTestReader{read: func(context.Context, string, metadata.ReadRequest) (metadata.Capture, error) {
		return metadata.Capture{Tags: map[string][]string{"TITLE": {"Track"}, "ARTIST": {"Artist"}, "LYRICS": {lyrics}}}, nil
	}}, lyrics, func(_ context.Context, evidence publicationOptionalLyricsOutcomeEvidence) error {
		recorded = &evidence
		return nil
	})
	if err != nil {
		t.Fatalf("apply optional lyrics: %v", err)
	}
	if recorded == nil || recorded.Kind != "exact_roundtrip" || recorded.Lyrics != result.Lyrics || !equalTags(recorded.Tags, result.Tags) {
		t.Fatalf("recorded helper evidence = %#v, result = %#v", recorded, result)
	}
}

func TestApplyOptionalLyricsRejectsMissingRequiredAuthority(t *testing.T) {
	repository := &scopeRepositoryFake{valid: true, marked: true}
	scope := newScopeForTest(t, &scopeCommandFake{}, repository)
	result, err := applyPublicationOptionalLyrics(context.Background(), scope, nil, nil, persistence.PublicationInputs{}, nil, nil, publicationOptionalLyricsRequirements{})
	if err == nil || result.Lyrics.State != "" {
		t.Fatalf("helper result = %#v, %v; want authority rejection", result, err)
	}
	if repository.checks != 0 || repository.marks != 0 || !scope.CanExecute() {
		t.Fatalf("rejected helper caused side effects: repo=%+v executable=%v", repository, scope.CanExecute())
	}
}

func TestApplyOptionalLyricsOpaqueWriterErrorQuarantinesWithoutFallback(t *testing.T) {
	wantErr := errors.New("opaque writer error")
	readerCalls := 0
	scope, repository, _, err := runOptionalLyricsTest(t,
		optionalLyricsTestWriter{write: func(context.Context, string, map[string][]string) error { return wantErr }},
		optionalLyricsTestReader{read: func(context.Context, string, metadata.ReadRequest) (metadata.Capture, error) {
			readerCalls++
			return metadata.Capture{}, nil
		}}, "[00:01.00]line\n")
	if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "write publication metadata") {
		t.Fatalf("writer error = %v, want wrapped opaque error", err)
	}
	if scope.CanExecute() || repository.marks != 0 || readerCalls != 0 {
		t.Fatalf("unsafe fallback/finalization after writer error: executable=%v repo=%+v readerCalls=%d", scope.CanExecute(), repository, readerCalls)
	}
	if err := scope.Finalize(context.Background(), func() error { return nil }); err == nil {
		t.Fatal("unknown writer outcome finalized normally")
	}
}

func TestApplyOptionalLyricsPanicQuarantinesAndRetainsBorrowedReservation(t *testing.T) {
	scope := newScopeForTest(t, &scopeCommandFake{}, &scopeRepositoryFake{valid: true, marked: true})
	reservation := scope.reserveSynchronousWork()
	path := filepath.Join(t.TempDir(), "output.flac")
	defer func() {
		if recovered := recover(); recovered != "writer panic" {
			t.Fatalf("panic = %v", recovered)
		}
		scope.mu.Lock()
		borrowed, unknown, work := reservation.borrowed, scope.unknown, scope.syncWork
		scope.mu.Unlock()
		if !unknown || !borrowed || work != 1 {
			t.Fatalf("panic state unknown=%v borrowed=%v syncWork=%d", unknown, borrowed, work)
		}
		if err := scope.Finalize(context.Background(), func() error { return nil }); err == nil {
			t.Fatal("panicked writer scope finalized normally")
		}
	}()
	_, _ = applyPublicationOptionalLyrics(context.Background(), scope, reservation, optionalLyricsTestStage{path}, persistence.PublicationInputs{
		Version: persistence.PublicationInputsVersion, QualityPolicyVersion: publication.QualityPolicyVersion, ManualLyrics: &persistence.PublicationManualLyricsInput{State: "value", Value: "[00:01.00]line\n"},
	}, optionalLyricsTestWriter{write: func(context.Context, string, map[string][]string) error { panic("writer panic") }}, optionalLyricsTestReader{}, publicationOptionalLyricsRequirements{
		validateAudioAndAssets: func(context.Context, string) error { return nil },
		restoreRequiredAssets:  func(context.Context, string, map[string][]string) error { return nil },
	})
}

func TestApplyOptionalLyricsRejectsConcurrentBorrowWhileWriterBlocked(t *testing.T) {
	entered, unblock := make(chan struct{}), make(chan struct{})
	scope := newScopeForTest(t, &scopeCommandFake{}, &scopeRepositoryFake{valid: true, marked: true})
	reservation := scope.reserveSynchronousWork()
	path := filepath.Join(t.TempDir(), "output.flac")
	input := persistence.PublicationInputs{Version: persistence.PublicationInputsVersion, QualityPolicyVersion: publication.QualityPolicyVersion, ManualLyrics: &persistence.PublicationManualLyricsInput{State: "value", Value: "[00:01.00]line\n"}}
	writer := optionalLyricsTestWriter{write: func(context.Context, string, map[string][]string) error { close(entered); <-unblock; return nil }}
	reader := optionalLyricsTestReader{read: func(context.Context, string, metadata.ReadRequest) (metadata.Capture, error) {
		return metadata.Capture{Tags: map[string][]string{"LYRICS": {"[00:01.00]line\n"}}}, nil
	}}
	requirements := publicationOptionalLyricsRequirements{validateAudioAndAssets: func(context.Context, string) error { return nil }, restoreRequiredAssets: func(context.Context, string, map[string][]string) error { return nil }}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = applyPublicationOptionalLyrics(context.Background(), scope, reservation, optionalLyricsTestStage{path}, input, writer, reader, requirements)
	}()
	<-entered
	scope.releaseReplacementWork(reservation, false)
	scope.mu.Lock()
	borrowRetained := reservation.borrowed && reservation.borrow != nil && scope.optionalLyrics != nil
	scope.mu.Unlock()
	if !borrowRetained {
		close(unblock)
		wg.Wait()
		t.Fatal("replacement release cleared an optional lyrics borrow")
	}
	if scope.reserveSynchronousWork() != nil {
		close(unblock)
		wg.Wait()
		t.Fatal("new synchronous work was admitted during optional lyrics borrow")
	}
	if _, err := applyPublicationOptionalLyrics(context.Background(), scope, reservation, optionalLyricsTestStage{path}, input, writer, reader, requirements); err == nil {
		close(unblock)
		wg.Wait()
		t.Fatal("concurrent helper borrowed an already-borrowed reservation")
	}
	close(unblock)
	wg.Wait()
}

func TestApplyOptionalLyricsErrorRetainsBorrowAndSuccessfulReturnReleasesIt(t *testing.T) {
	t.Run("error retains quarantined borrow", func(t *testing.T) {
		wantErr := errors.New("read failed")
		reader := optionalLyricsTestReader{read: func(context.Context, string, metadata.ReadRequest) (metadata.Capture, error) {
			return metadata.Capture{}, wantErr
		}}
		scope, _, _, err := runOptionalLyricsTest(t, optionalLyricsTestWriter{}, reader, "[00:01.00]line\n")
		if !errors.Is(err, wantErr) {
			t.Fatalf("helper error = %v", err)
		}
		scope.mu.Lock()
		unknown, work, active := scope.unknown, scope.syncWork, scope.optionalLyrics != nil
		scope.mu.Unlock()
		if !unknown || work != 1 || !active {
			t.Fatalf("error state unknown=%v syncWork=%d optionalBorrow=%v", unknown, work, active)
		}
	})

	t.Run("success releases borrow and preserves outer work", func(t *testing.T) {
		scope, _, _, err := runOptionalLyricsTest(t, optionalLyricsTestWriter{}, optionalLyricsTestReader{read: func(context.Context, string, metadata.ReadRequest) (metadata.Capture, error) {
			return metadata.Capture{Tags: map[string][]string{"TITLE": {"Track"}, "ARTIST": {"Artist"}, "LYRICS": {"[00:01.00]line\n"}}}, nil
		}}, "[00:01.00]line\n")
		if err != nil {
			t.Fatal(err)
		}
		scope.mu.Lock()
		work, borrow := scope.syncWork, scope.optionalLyrics
		scope.mu.Unlock()
		if work != 1 || borrow != nil {
			t.Fatalf("success state syncWork=%d optionalBorrow=%v", work, borrow)
		}
	})
}

func TestApplyOptionalLyricsRealTagLibFLACRoundTrip(t *testing.T) {
	fixture, err := os.ReadFile("../integrations/metadata/testdata/eg.flac")
	if err != nil {
		t.Fatal(err)
	}
	lyrics := "[00:01.00]exact line\r\n[00:02.00]♫ café\r\n"
	stageFixture := newPublicationStageFixture(t)
	stage, err := stageFixture.prepare(t)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := stage.Close(); err != nil {
			t.Errorf("close owned stage: %v", err)
		}
		if err := stageFixture.preparer.OutputParent.Close(); err != nil {
			t.Errorf("close output parent: %v", err)
		}
	}()
	scope := newScopeForTest(t, &scopeCommandFake{}, &scopeRepositoryFake{valid: true, marked: true})
	reservation := scope.reserveSynchronousWork()
	writer := optionalLyricsTestWriter{write: func(ctx context.Context, path string, tags map[string][]string) error {
		if err := os.WriteFile(path, fixture, 0o600); err != nil {
			return err
		}
		_, err := (metadata.Writer{}).Write(ctx, path, tags, nil)
		return err
	}}
	reader := optionalLyricsTestReader{read: func(ctx context.Context, path string, request metadata.ReadRequest) (metadata.Capture, error) {
		return (metadata.Reader{}).Read(ctx, path, request)
	}}
	validations := 0
	result, err := applyPublicationOptionalLyrics(context.Background(), scope, reservation, stage, persistence.PublicationInputs{
		Version:              persistence.PublicationInputsVersion,
		QualityPolicyVersion: publication.QualityPolicyVersion,
		ManualLyrics:         &persistence.PublicationManualLyricsInput{State: "value", Value: lyrics},
		DesiredTags:          map[string][]string{"TITLE": {"Track"}, "ARTIST": {"Artist"}},
	}, writer, reader, publicationOptionalLyricsRequirements{
		validateAudioAndAssets: func(context.Context, string) error { validations++; return nil },
		restoreRequiredAssets:  func(context.Context, string, map[string][]string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if validations < 2 {
		t.Fatalf("required validation callback ran %d times", validations)
	}
	if result.Lyrics.State != persistence.PublicationAssetEmbedded || result.Lyrics.Format != "lrc" || result.Lyrics.SHA256 != publicationLyricsDigest(lyrics) {
		t.Fatalf("lyrics outcome = %#v", result.Lyrics)
	}
	if result.Tags["LYRICS"][0] != lyrics {
		t.Fatalf("lyrics did not round-trip exactly: %q", result.Tags["LYRICS"])
	}
	scope.releaseSynchronousWork(reservation)
	if err := scope.Finalize(context.Background(), stage.Close); err != nil {
		t.Fatal(err)
	}
}

// flacFixtureWithoutPictures removes FLAC picture metadata blocks while
// preserving the stream info, other metadata, and encoded audio frames.
func flacFixtureWithoutPictures(contents []byte) ([]byte, error) {
	if len(contents) < 8 || !bytes.Equal(contents[:4], []byte("fLaC")) {
		return nil, errors.New("fixture is not a FLAC stream")
	}
	type metadataBlock struct {
		kind byte
		data []byte
	}
	blocks := make([]metadataBlock, 0)
	offset := 4
	lastBlock := false
	for !lastBlock {
		if len(contents)-offset < 4 {
			return nil, errors.New("fixture has truncated FLAC metadata")
		}
		header := contents[offset]
		lastBlock = header&0x80 != 0
		kind := header & 0x7f
		length := int(contents[offset+1])<<16 | int(contents[offset+2])<<8 | int(contents[offset+3])
		offset += 4
		if length > len(contents)-offset {
			return nil, errors.New("fixture has truncated FLAC metadata block")
		}
		if kind != 6 {
			blocks = append(blocks, metadataBlock{kind: kind, data: contents[offset : offset+length]})
		}
		offset += length
	}
	if len(blocks) == 0 {
		return nil, errors.New("fixture has no non-picture FLAC metadata")
	}
	result := append([]byte(nil), "fLaC"...)
	for index, block := range blocks {
		header := block.kind
		if index == len(blocks)-1 {
			header |= 0x80
		}
		length := len(block.data)
		result = append(result, header, byte(length>>16), byte(length>>8), byte(length))
		result = append(result, block.data...)
	}
	return append(result, contents[offset:]...), nil
}

// flacAudioPayloadOffset returns the first byte after the complete FLAC metadata
// chain. It bounds-checks each block so payload comparisons cannot accidentally
// hash metadata (or accept a truncated stream).
func flacAudioPayloadOffset(contents []byte) (int, error) {
	if len(contents) < 8 || !bytes.Equal(contents[:4], []byte("fLaC")) {
		return 0, errors.New("fixture is not a FLAC stream")
	}
	offset := 4
	for {
		if len(contents)-offset < 4 {
			return 0, errors.New("fixture has truncated FLAC metadata")
		}
		header := contents[offset]
		length := int(contents[offset+1])<<16 | int(contents[offset+2])<<8 | int(contents[offset+3])
		offset += 4
		if length > len(contents)-offset {
			return 0, errors.New("fixture has truncated FLAC metadata block")
		}
		offset += length
		if header&0x80 != 0 {
			if offset >= len(contents) {
				return 0, errors.New("fixture has no encoded FLAC audio payload")
			}
			return offset, nil
		}
	}
}

func flacStreamInfo(contents []byte) ([]byte, error) {
	if len(contents) < 8 || !bytes.Equal(contents[:4], []byte("fLaC")) {
		return nil, errors.New("fixture is not a FLAC stream")
	}
	offset := 4
	for {
		if len(contents)-offset < 4 {
			return nil, errors.New("fixture has truncated FLAC metadata")
		}
		header := contents[offset]
		kind := header & 0x7f
		length := int(contents[offset+1])<<16 | int(contents[offset+2])<<8 | int(contents[offset+3])
		offset += 4
		if length > len(contents)-offset {
			return nil, errors.New("fixture has truncated FLAC metadata block")
		}
		if kind == 0 {
			if length != 34 {
				return nil, errors.New("fixture has invalid FLAC STREAMINFO length")
			}
			return contents[offset : offset+length], nil
		}
		offset += length
		if header&0x80 != 0 {
			return nil, errors.New("fixture has no FLAC STREAMINFO block")
		}
	}
}

func TestApplyOptionalLyricsRealTagLibMismatchRestoresNativeOutput(t *testing.T) {
	ctx := context.Background()
	fixture, err := os.ReadFile("../integrations/metadata/testdata/eg.flac")
	if err != nil {
		t.Fatal(err)
	}
	fixture, err = flacFixtureWithoutPictures(fixture)
	if err != nil {
		t.Fatal(err)
	}
	lyrics := "[00:01.00]requested line\n"
	wrongLyrics := "[00:01.00]wrong but valid line\n"
	var encodedCover bytes.Buffer
	coverImage := image.NewRGBA(image.Rect(0, 0, 2, 2))
	draw.Draw(coverImage, coverImage.Bounds(), image.NewUniform(color.RGBA{R: 30, G: 90, B: 150, A: 255}), image.Point{}, draw.Src)
	if err := jpeg.Encode(&encodedCover, coverImage, nil); err != nil {
		t.Fatal(err)
	}
	cover := encodedCover.Bytes()
	stageFixture := newPublicationStageFixture(t)
	if err := os.WriteFile(stageFixture.source, fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (metadata.Writer{}).Write(ctx, stageFixture.source, map[string][]string{"TITLE": {"Track"}, "ARTIST": {"Artist"}}, nil); err != nil {
		t.Fatal(err)
	}
	fixture, err = os.ReadFile(stageFixture.source)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(stageFixture.source)
	if err != nil {
		t.Fatal(err)
	}
	stageFixture.repo.input.Audio.SizeBytes = info.Size()
	stageFixture.repo.input.Audio.MTime = info.ModTime().UTC().Truncate(time.Microsecond)
	stageFixture.repo.hold.SizeBytes = info.Size()
	stageFixture.repo.hold.Mtime = stageFixture.repo.input.Audio.MTime
	stage, err := stageFixture.prepare(t)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := stage.Close(); err != nil {
			t.Errorf("close owned stage: %v", err)
		}
		if err := stageFixture.preparer.OutputParent.Close(); err != nil {
			t.Errorf("close output parent: %v", err)
		}
	}()
	rebuildRepo := &candidateRebuildTestRepo{}
	preparedStage, ok := stage.(*preparedPublicationStage)
	if !ok {
		t.Fatalf("prepared stage type = %T", stage)
	}
	preparedStage.repository = rebuildRepo
	trace := []string{}
	expectedParent, err := preparedStage.directory.Identity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	preparedStage.directory = &observedRebuildDirectory{OutputDirectory: preparedStage.directory, trace: &trace, outputName: filepath.Base(preparedStage.paths.OutputPath), expectedParent: expectedParent}
	scope := newScopeForTest(t, &scopeCommandFake{}, &scopeRepositoryFake{valid: true, marked: true})
	preparedStage.guard = scope.guard
	reservation := scope.reserveSynchronousWork()
	paths := stage.Paths()
	inputDigest := sha256.Sum256(fixture)
	inputAudioOffset, err := flacAudioPayloadOffset(fixture)
	if err != nil {
		t.Fatal(err)
	}
	inputAudioDigest := sha256.Sum256(fixture[inputAudioOffset:])
	inputStreamInfo, err := flacStreamInfo(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.OutputPath, fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	mutation := preparedStage.directory.(sourcefs.OutputMutation)
	inputHandle, err := mutation.OpenExistingRegular(ctx, filepath.Base(paths.InputPath))
	if err != nil {
		t.Fatal(err)
	}
	inputInfo, err := inputHandle.Stat(ctx)
	if err != nil {
		_ = inputHandle.Close()
		t.Fatal(err)
	}
	candidateHandle, err := mutation.OpenExistingRegular(ctx, filepath.Base(paths.OutputPath))
	if err != nil {
		_ = inputHandle.Close()
		t.Fatal(err)
	}
	candidateInfo, err := candidateHandle.Stat(ctx)
	if err != nil {
		_ = inputHandle.Close()
		_ = candidateHandle.Close()
		t.Fatal(err)
	}
	if os.SameFile(inputInfo, candidateInfo) {
		_ = inputHandle.Close()
		_ = candidateHandle.Close()
		t.Fatal("prepared input and output candidate alias the same native file")
	}
	if err := candidateHandle.Close(); err != nil {
		_ = inputHandle.Close()
		t.Fatal(err)
	}
	if err := inputHandle.Close(); err != nil {
		t.Fatal(err)
	}
	initialCapture, err := (metadata.Reader{}).Read(ctx, paths.OutputPath, metadata.ReadRequest{CoverInspection: true})
	if err != nil {
		t.Fatal(err)
	}
	if initialCapture.CoverInspection == nil || initialCapture.CoverInspection.Cover != nil {
		t.Fatal("fixture unexpectedly contains an initial cover")
	}
	writer := optionalLyricsTestWriter{write: func(ctx context.Context, path string, tags map[string][]string) error {
		written := cloneTagMap(tags)
		if _, requested := tags["LYRICS"]; requested {
			written["LYRICS"] = []string{wrongLyrics}
		}
		_, err := (metadata.Writer{}).Write(ctx, path, written, nil)
		return err
	}}
	reader := optionalLyricsTestReader{read: func(ctx context.Context, path string, request metadata.ReadRequest) (metadata.Capture, error) {
		return (metadata.Reader{}).Read(ctx, path, request)
	}}
	rebuildRepo.onRecord = func(record persistence.PublicationCandidateRebuildRecord) error {
		switch record.Step {
		case "intent":
			trace = append(trace, "db-intent-ack")
		case "complete":
			trace = append(trace, "db-complete-ack")
			candidate, err := os.ReadFile(paths.OutputPath)
			if err != nil {
				return err
			}
			if !bytes.Equal(candidate, fixture) {
				return errors.New("rebuild completion acknowledged before exact immutable input bytes were restored")
			}
		}
		return nil
	}
	restoreCalled := false
	validations := 0
	result, err := applyPublicationOptionalLyrics(ctx, scope, reservation, stage, persistence.PublicationInputs{
		Version:              persistence.PublicationInputsVersion,
		QualityPolicyVersion: publication.QualityPolicyVersion,
		ManualLyrics:         &persistence.PublicationManualLyricsInput{State: "value", Value: lyrics},
		DesiredTags:          map[string][]string{"TITLE": {"Track"}, "ARTIST": {"Artist"}},
	}, writer, reader, publicationOptionalLyricsRequirements{
		validateAudioAndAssets: func(ctx context.Context, path string) error {
			validations++
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			audioOffset, err := flacAudioPayloadOffset(data)
			if err != nil {
				return err
			}
			if sha256.Sum256(data[audioOffset:]) != inputAudioDigest {
				return errors.New("encoded FLAC audio payload changed")
			}
			streamInfo, err := flacStreamInfo(data)
			if err != nil {
				return err
			}
			if !bytes.Equal(streamInfo, inputStreamInfo) {
				return errors.New("required FLAC audio facts changed")
			}
			if validations >= 2 {
				capture, err := (metadata.Reader{}).Read(ctx, path, metadata.ReadRequest{CoverInspection: true})
				if err != nil {
					return err
				}
				if !equalTags(withoutLyrics(capture.Tags), map[string][]string{"TITLE": {"Track"}, "ARTIST": {"Artist"}}) {
					return errors.New("required metadata missing at final asset validation")
				}
				if validations == 2 && len(capture.Tags["LYRICS"]) == 0 {
					return errors.New("observed lyrics missing at required-asset validation")
				}
				if capture.CoverInspection == nil || capture.CoverInspection.Cover == nil || !bytes.Equal(capture.CoverInspection.Cover.Data, cover) {
					return errors.New("required cover missing at final asset validation")
				}
			}
			return nil
		},
		restoreRequiredAssets: func(ctx context.Context, path string, expectedTags map[string][]string) error {
			beforeRestore, err := (metadata.Reader{}).Read(ctx, path, metadata.ReadRequest{})
			if err != nil {
				return err
			}
			mandatory := map[string][]string{"TITLE": {"Track"}, "ARTIST": {"Artist"}}
			if !equalTags(withoutLyrics(beforeRestore.Tags), mandatory) {
				return errors.New("mandatory tags were not restored before required asset restoration")
			}
			restoreCalled = true
			tagsToRestore := expectedTags
			traceEvent := "fallback-assets-restored"
			if len(trace) == 0 {
				// Preserve the actual mismatch readback while restoring the cover;
				// repairing lyrics here would prevent the fallback classifier from
				// ever observing the intentional TagLib mismatch.
				tagsToRestore = cloneTagMap(mandatory)
				if actualLyrics, ok := beforeRestore.Tags["LYRICS"]; ok {
					tagsToRestore["LYRICS"] = append([]string(nil), actualLyrics...)
				}
				traceEvent = "initial-assets-restored"
			}
			_, err = (metadata.Writer{}).Write(ctx, path, tagsToRestore, &metadata.Cover{Data: cover, MIME: "image/jpeg"})
			if err == nil {
				trace = append(trace, traceEvent)
			}
			return err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !restoreCalled || len(rebuildRepo.records) != 2 || rebuildRepo.records[0].Step != "intent" || rebuildRepo.records[1].Step != "complete" {
		t.Fatalf("fallback/rebuild journal = restored %v, records %#v", restoreCalled, rebuildRepo.records)
	}
	wantTrace := []string{"initial-assets-restored", "db-intent-ack", "native-remove-success", "native-exclusive-create-success", "db-complete-ack", "fallback-assets-restored"}
	if !reflect.DeepEqual(trace, wantTrace) {
		t.Fatalf("native rebuild trace = %#v, want %#v", trace, wantTrace)
	}
	if result.OptionalDiagnostic == "" || result.Lyrics.State != persistence.PublicationAssetAbsent {
		t.Fatalf("lyrics round-trip result = %#v", result)
	}
	final, err := (metadata.Reader{}).Read(ctx, paths.OutputPath, metadata.ReadRequest{CoverInspection: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, present := final.Tags["LYRICS"]; present {
		t.Fatalf("lyrics metadata remained after native fallback: %#v", final.Tags["LYRICS"])
	}
	if final.CoverInspection == nil || final.CoverInspection.Cover == nil || !bytes.Equal(final.CoverInspection.Cover.Data, cover) {
		t.Fatal("required cover was not restored after native fallback")
	}
	inputAfter, err := os.ReadFile(paths.InputPath)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(inputAfter) != inputDigest {
		t.Fatal("immutable prepared input changed during fallback")
	}
	scope.releaseSynchronousWork(reservation)
	if err := scope.Finalize(ctx, stage.Close); err != nil {
		t.Fatal(err)
	}
}

func TestOptionalLyricsTagsPreserveFrozenStateAndMandatoryTags(t *testing.T) {
	mandatory := map[string][]string{"TITLE": {"A title"}, "ARTIST": {"An artist"}}
	lyrics := "[00:01.00]line\r\n[00:02.00]♫ café\r\n"

	requested, expected, err := optionalLyricsTags(mandatory, &persistence.PublicationManualLyricsInput{State: "value", Value: lyrics})
	if err != nil {
		t.Fatal(err)
	}
	if expected != lyrics || len(requested["LYRICS"]) != 1 || requested["LYRICS"][0] != lyrics {
		t.Fatalf("lyrics value changed: expected=%q requested=%q", expected, requested["LYRICS"])
	}
	requested["TITLE"][0] = "mutated"
	if mandatory["TITLE"][0] != "A title" {
		t.Fatal("requested metadata aliases mandatory metadata")
	}

	requested, expected, err = optionalLyricsTags(mandatory, &persistence.PublicationManualLyricsInput{State: "explicit_empty"})
	if err != nil {
		t.Fatal(err)
	}
	if expected != "" || len(requested["LYRICS"]) != 0 {
		t.Fatalf("explicit_empty was not represented as a deletion: %#v", requested["LYRICS"])
	}
	if _, _, err := optionalLyricsTags(mandatory, &persistence.PublicationManualLyricsInput{State: "absent"}); err == nil {
		t.Fatal("absent lyrics state was accepted as manual intent")
	}
}

func TestInspectOptionalLyricsUsesExactReadback(t *testing.T) {
	lyrics := "[00:01.00]line\r\n"
	digest := sha256.Sum256([]byte(lyrics))
	want := persistence.PublicationLyricsOutcome{State: persistence.PublicationAssetEmbedded, SHA256: hex.EncodeToString(digest[:]), Format: "lrc"}
	got, mismatch := inspectOptionalLyrics(map[string][]string{"LYRICS": {lyrics}}, lyrics)
	if mismatch || got != want {
		t.Fatalf("exact lyrics readback = %#v, mismatch=%v; want %#v", got, mismatch, want)
	}
	if _, mismatch := inspectOptionalLyrics(map[string][]string{"LYRICS": {"[00:01.00]line\n"}}, lyrics); !mismatch {
		t.Fatal("readback line-ending normalization was accepted")
	}
	got, mismatch = inspectOptionalLyrics(map[string][]string{}, "")
	if mismatch || got.State != persistence.PublicationAssetAbsent {
		t.Fatalf("explicit empty readback = %#v, mismatch=%v", got, mismatch)
	}
	if _, mismatch := inspectOptionalLyrics(map[string][]string{"LYRICS": {""}}, ""); !mismatch {
		t.Fatal("present empty LYRICS key was treated as absence")
	}
}
