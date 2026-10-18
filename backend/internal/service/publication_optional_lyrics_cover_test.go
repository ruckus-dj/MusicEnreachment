package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"os"
	"testing"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestApplyOptionalLyricsRealTagLibPreservesRequiredCover(t *testing.T) {
	ctx := context.Background()
	fixture, err := os.ReadFile("../integrations/metadata/testdata/eg.flac")
	if err != nil {
		t.Fatal(err)
	}
	fixture, err = flacFixtureWithoutPictures(fixture)
	if err != nil {
		t.Fatal(err)
	}
	lyrics := "[00:01.00]exact line\r\n[00:02.00]♫ café\r\n"
	var encodedCover bytes.Buffer
	coverImage := image.NewRGBA(image.Rect(0, 0, 2, 2))
	draw.Draw(coverImage, coverImage.Bounds(), image.NewUniform(color.RGBA{R: 30, G: 90, B: 150, A: 255}), image.Point{}, draw.Src)
	if err := jpeg.Encode(&encodedCover, coverImage, nil); err != nil {
		t.Fatal(err)
	}
	cover := encodedCover.Bytes()
	mandatory := map[string][]string{"TITLE": {"Track"}, "ARTIST": {"Artist"}}
	stageFixture := newPublicationStageFixture(t)
	if err := os.WriteFile(stageFixture.source, fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (metadata.Writer{}).Write(ctx, stageFixture.source, mandatory, &metadata.Cover{Data: cover, MIME: "image/jpeg"}); err != nil {
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

	preparedStage := stage.(*preparedPublicationStage)
	rebuildRepo := &candidateRebuildTestRepo{}
	preparedStage.repository = rebuildRepo
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
	if err := os.WriteFile(paths.OutputPath, fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	writer := optionalLyricsTestWriter{write: func(ctx context.Context, path string, tags map[string][]string) error {
		_, err := (metadata.Writer{}).Write(ctx, path, tags, nil)
		return err
	}}
	reader := optionalLyricsTestReader{read: func(ctx context.Context, path string, request metadata.ReadRequest) (metadata.Capture, error) {
		return (metadata.Reader{}).Read(ctx, path, request)
	}}
	validations, restores := 0, 0
	result, err := applyPublicationOptionalLyrics(ctx, scope, reservation, stage, persistence.PublicationInputs{
		Version:      persistence.PublicationInputsVersion3,
		ManualLyrics: &persistence.PublicationManualLyricsInput{State: "value", Value: lyrics},
		DesiredTags:  mandatory,
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
			capture, err := (metadata.Reader{}).Read(ctx, path, metadata.ReadRequest{CoverInspection: true})
			if err != nil {
				return err
			}
			if !equalTags(withoutLyrics(capture.Tags), mandatory) {
				return errors.New("required metadata missing at audio and asset validation")
			}
			if capture.CoverInspection == nil || capture.CoverInspection.Cover == nil || !bytes.Equal(capture.CoverInspection.Cover.Data, cover) {
				return errors.New("required cover missing at audio and asset validation")
			}
			return nil
		},
		restoreRequiredAssets: func(ctx context.Context, path string, expectedTags map[string][]string) error {
			restores++
			if restores != 1 {
				return errors.New("required cover restoration ran more than once")
			}
			_, err := (metadata.Writer{}).Write(ctx, path, expectedTags, &metadata.Cover{Data: cover, MIME: "image/jpeg"})
			return err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if validations != 2 || restores != 1 {
		t.Fatalf("validation/restoration calls = %d/%d, want 2/1", validations, restores)
	}
	if result.OptionalDiagnostic != "" || result.Lyrics.State != persistence.PublicationAssetEmbedded || result.Lyrics.Format != "lrc" || result.Lyrics.SHA256 != publicationLyricsDigest(lyrics) {
		t.Fatalf("optional lyrics outcome = %#v", result)
	}
	if got := result.Tags["LYRICS"]; len(got) != 1 || got[0] != lyrics {
		t.Fatalf("lyrics did not round-trip exactly: %q", got)
	}
	final, err := (metadata.Reader{}).Read(ctx, paths.OutputPath, metadata.ReadRequest{CoverInspection: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := final.Tags["LYRICS"]; len(got) != 1 || got[0] != lyrics {
		t.Fatalf("final LRC readback = %q", got)
	}
	if final.CoverInspection == nil || final.CoverInspection.Cover == nil || !bytes.Equal(final.CoverInspection.Cover.Data, cover) {
		t.Fatal("required cover was not preserved exactly")
	}
	inputAfter, err := os.ReadFile(paths.InputPath)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(inputAfter) != inputDigest {
		t.Fatal("immutable prepared input changed")
	}
	if len(rebuildRepo.records) != 0 {
		t.Fatalf("unexpected native rebuild records: %#v", rebuildRepo.records)
	}
	scope.releaseSynchronousWork(reservation)
	if err := scope.Finalize(ctx, stage.Close); err != nil {
		t.Fatal(err)
	}
}
