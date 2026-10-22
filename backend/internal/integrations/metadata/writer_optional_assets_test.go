package metadata

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"go.senan.xyz/taglib"
)

func TestWriteWithOptionalAssetsRequestCombinations(t *testing.T) {
	coverData := generatedOptionalAssetsPNG(t)
	for _, test := range []struct {
		name         string
		requested    map[string][]string
		cover        *Cover
		lyricsStatus OptionalLyricsWriteStatus
		coverStatus  OptionalCoverWriteStatus
	}{
		{name: "neither", requested: map[string][]string{"TITLE": {"title"}}, lyricsStatus: OptionalLyricsNotRequested, coverStatus: OptionalCoverNotRequested},
		{name: "lyrics only", requested: map[string][]string{"TITLE": {"title"}, "LYRICS": {"text"}}, lyricsStatus: OptionalLyricsVerified, coverStatus: OptionalCoverNotRequested},
		{name: "cover only", requested: map[string][]string{"TITLE": {"title"}}, cover: &Cover{Data: coverData, MIME: "image/png"}, lyricsStatus: OptionalLyricsNotRequested, coverStatus: OptionalCoverVerified},
		{name: "both", requested: map[string][]string{"TITLE": {"title"}, "LYRICS": {"text"}}, cover: &Cover{Data: coverData, MIME: "image/png"}, lyricsStatus: OptionalLyricsVerified, coverStatus: OptionalCoverVerified},
	} {
		t.Run(test.name, func(t *testing.T) {
			actual := map[string][]string{"TITLE": {"title"}}
			if lyrics, ok := test.requested["LYRICS"]; ok {
				actual["LYRICS"] = append([]string(nil), lyrics...)
			}
			coverWrites := 0
			writer := Writer{
				writeTags: func(_ string, got map[string][]string, _ taglib.WriteOption) error {
					if !reflect.DeepEqual(got, test.requested) {
						t.Errorf("tag write got %v, want %v", got, test.requested)
					}
					return nil
				},
				readTags: func(string) (map[string][]string, error) { return actual, nil },
				writeImage: func(_ string, got []byte) error {
					coverWrites++
					if !bytes.Equal(got, coverData) {
						t.Error("cover bytes differ")
					}
					return nil
				},
				readImage: func(string) ([]byte, error) { return coverData, nil },
			}
			result, err := writer.WriteWithOptionalAssets(context.Background(), "staged", test.requested, test.cover)
			if err != nil || result.LyricsStatus != test.lyricsStatus || result.CoverStatus != test.coverStatus {
				t.Fatalf("result = %+v, error = %v", result, err)
			}
			wantCoverWrites := 0
			if test.cover != nil {
				wantCoverWrites = 1
			}
			if coverWrites != wantCoverWrites {
				t.Fatalf("cover writes = %d, want %d", coverWrites, wantCoverWrites)
			}
			result.Tags["TITLE"][0] = "changed"
			if actual["TITLE"][0] != "title" {
				t.Fatal("result tags were not detached")
			}
		})
	}
}

func TestWriteWithOptionalAssetsClassifiesOnlyIsolatedCoverFailures(t *testing.T) {
	cover := &Cover{Data: generatedOptionalAssetsPNG(t), MIME: "image/png"}
	requested := map[string][]string{"TITLE": {"title"}, "LYRICS": {"text"}}
	readLyrics := map[string][]string{"TITLE": {"title"}, "LYRICS": {"text"}}
	for _, test := range []struct {
		name       string
		writeImage func(string, []byte) error
		readImage  func(string) ([]byte, error)
		finalTags  map[string][]string
		wantCover  OptionalCoverWriteStatus
		wantLyrics OptionalLyricsWriteStatus
	}{
		{name: "write failed while lyrics verified", writeImage: func(string, []byte) error { return errors.New("opaque native image write") }, readImage: func(string) ([]byte, error) { return nil, nil }, finalTags: readLyrics, wantCover: OptionalCoverWriteFailed, wantLyrics: OptionalLyricsVerified},
		{name: "read failed while lyrics verified", writeImage: func(string, []byte) error { return nil }, readImage: func(string) ([]byte, error) { return nil, errors.New("opaque native image read") }, finalTags: readLyrics, wantCover: OptionalCoverReadFailed, wantLyrics: OptionalLyricsVerified},
		{name: "cover mismatch and lyrics absent", writeImage: func(string, []byte) error { return nil }, readImage: func(string) ([]byte, error) { return []byte("different"), nil }, finalTags: map[string][]string{"TITLE": {"title"}}, wantCover: OptionalCoverMismatch, wantLyrics: OptionalLyricsAbsent},
		{name: "both optional assets fail", writeImage: func(string, []byte) error { return errors.New("cover unavailable") }, readImage: func(string) ([]byte, error) { return nil, nil }, finalTags: map[string][]string{"TITLE": {"title"}}, wantCover: OptionalCoverWriteFailed, wantLyrics: OptionalLyricsAbsent},
	} {
		t.Run(test.name, func(t *testing.T) {
			reads := 0
			writer := Writer{
				writeTags: func(string, map[string][]string, taglib.WriteOption) error { return nil },
				readTags: func(string) (map[string][]string, error) {
					reads++
					if reads == 1 {
						return readLyrics, nil
					}
					return test.finalTags, nil
				},
				writeImage: test.writeImage, readImage: test.readImage,
			}
			result, err := writer.WriteWithOptionalAssets(context.Background(), "staged", requested, cover)
			if err != nil || result.CoverStatus != test.wantCover || result.LyricsStatus != test.wantLyrics {
				t.Fatalf("result = %+v, error = %v", result, err)
			}
			if reads != 2 {
				t.Fatalf("fresh tag reads = %d, want 2", reads)
			}
		})
	}
}

func TestWriteWithOptionalAssetsFatalIntegrityNativeAndMandatoryFailures(t *testing.T) {
	cover := &Cover{Data: generatedOptionalAssetsPNG(t), MIME: "image/png"}
	requested := map[string][]string{"TITLE": {"title"}, "LYRICS": {"text"}}
	validTags := map[string][]string{"TITLE": {"title"}, "LYRICS": {"text"}}
	for _, test := range []struct {
		name   string
		cover  *Cover
		writer Writer
		ctx    func() (context.Context, context.CancelFunc)
	}{
		{name: "invalid image bytes", cover: &Cover{Data: []byte("not png"), MIME: "image/png"}},
		{name: "MIME mismatch", cover: &Cover{Data: cover.Data, MIME: "image/jpeg"}},
		{name: "mandatory tag write failure", cover: cover, writer: Writer{writeTags: func(string, map[string][]string, taglib.WriteOption) error {
			return errors.New("opaque combined native error")
		}}},
		{name: "mandatory tag write panic", cover: cover, writer: Writer{writeTags: func(string, map[string][]string, taglib.WriteOption) error { panic("native panic") }}},
		{name: "mandatory tag read failure", cover: cover, writer: Writer{writeTags: func(string, map[string][]string, taglib.WriteOption) error { return nil }, readTags: func(string) (map[string][]string, error) { return nil, errors.New("opaque tag read") }}},
		{name: "mandatory corruption after cover", cover: cover, writer: Writer{writeTags: func(string, map[string][]string, taglib.WriteOption) error { return nil }, readTags: func(string) (map[string][]string, error) { return validTags, nil }, writeImage: func(string, []byte) error { return nil }, readImage: func(string) ([]byte, error) { return cover.Data, nil }}},
		{name: "cover writer panic", cover: cover, writer: Writer{writeTags: func(string, map[string][]string, taglib.WriteOption) error { return nil }, readTags: func(string) (map[string][]string, error) { return validTags, nil }, writeImage: func(string, []byte) error { panic("native image panic") }}},
		{name: "cover reader panic", cover: cover, writer: Writer{writeTags: func(string, map[string][]string, taglib.WriteOption) error { return nil }, readTags: func(string) (map[string][]string, error) { return validTags, nil }, writeImage: func(string, []byte) error { return nil }, readImage: func(string) ([]byte, error) { panic("native image panic") }}},
		{name: "cancellation during cover write", cover: cover, writer: Writer{writeTags: func(string, map[string][]string, taglib.WriteOption) error { return nil }, readTags: func(string) (map[string][]string, error) { return validTags, nil }, writeImage: func(string, []byte) error { return errors.New("cover failed") }}, ctx: func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			cancel := func() {}
			if test.ctx != nil {
				ctx, cancel = test.ctx()
			}
			defer cancel()
			writer := test.writer
			if test.name == "mandatory corruption after cover" {
				reads := 0
				writer.readTags = func(string) (map[string][]string, error) {
					reads++
					if reads == 1 {
						return validTags, nil
					}
					return map[string][]string{"TITLE": {"corrupted"}, "LYRICS": {"text"}}, nil
				}
			}
			if writer.writeTags == nil {
				writer.writeTags = func(string, map[string][]string, taglib.WriteOption) error { return nil }
			}
			if writer.readTags == nil {
				writer.readTags = func(string) (map[string][]string, error) { return validTags, nil }
			}
			if writer.writeImage != nil && test.name == "cancellation during cover write" {
				writer.writeImage = func(string, []byte) error { cancel(); return errors.New("cover error raced with cancel") }
			}
			result, err := writer.WriteWithOptionalAssets(ctx, "staged", requested, test.cover)
			if err == nil || result.Tags != nil || result.LyricsStatus != OptionalLyricsUnobserved || result.CoverStatus != OptionalCoverNotRequested {
				t.Fatalf("result = %+v, error = %v; fatal path must return no optional authority", result, err)
			}
		})
	}
}

func TestWriteWithOptionalAssetsRoundTripsRealContainers(t *testing.T) {
	for _, extension := range []string{".flac", ".mp3", ".m4a", ".mka"} {
		t.Run(extension, func(t *testing.T) {
			contents, err := os.ReadFile(filepath.Join("testdata", "eg"+extension))
			if err != nil {
				t.Fatal(err)
			}
			path := writeMetadataFixture(t, extension, contents)
			beforeBytes, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			beforePayload, err := encodedAudioPayload(extension, beforeBytes)
			if err != nil {
				t.Fatalf("extract original encoded audio payload: %v", err)
			}
			if len(beforePayload) == 0 {
				t.Fatal("fixture has no encoded audio payload")
			}
			beforePayloadHash := sha256.Sum256(beforePayload)
			before, err := (Reader{}).Read(context.Background(), path, ReadRequest{Properties: true})
			if err != nil || before.Properties == nil {
				t.Fatalf("read initial audio properties: %+v, %v", before.Properties, err)
			}
			coverData := generatedOptionalAssetsPNG(t)
			requested := map[string][]string{"TITLE": {"optional assets"}, "LYRICS": {"[00:01.00]Line one\n[00:02.00]Line two\n"}}
			result, err := (Writer{}).WriteWithOptionalAssets(context.Background(), path, requested, &Cover{Data: coverData, MIME: "image/png"})
			if err != nil || result.LyricsStatus != OptionalLyricsVerified || result.CoverStatus != OptionalCoverVerified {
				t.Fatalf("write result = %+v, %v", result, err)
			}
			after, err := (Reader{}).Read(context.Background(), path, ReadRequest{Properties: true, CoverInspection: true})
			if err != nil || after.Properties == nil || after.CoverInspection == nil || after.CoverInspection.Cover == nil {
				t.Fatalf("fresh readback = %+v, %v", after, err)
			}
			if !bytes.Equal(after.CoverInspection.Cover.Data, coverData) || !reflect.DeepEqual(after.Tags["LYRICS"], requested["LYRICS"]) {
				t.Fatalf("fresh optional assets differ: cover=%v lyrics=%v", bytes.Equal(after.CoverInspection.Cover.Data, coverData), after.Tags["LYRICS"])
			}
			afterBytes, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			afterPayload, err := encodedAudioPayload(extension, afterBytes)
			if err != nil {
				t.Fatalf("extract rewritten encoded audio payload: %v", err)
			}
			if got := sha256.Sum256(afterPayload); got != beforePayloadHash {
				t.Fatalf("encoded audio payload changed: before sha256=%x after sha256=%x", beforePayloadHash, got)
			}
			corrupted := append([]byte(nil), afterBytes...)
			corruptAt, err := firstEncodedAudioByteOffset(extension, corrupted)
			if err != nil {
				t.Fatalf("locate encoded audio payload for corruption check: %v", err)
			}
			corrupted[corruptAt] ^= 0x01
			corruptedPayload, err := encodedAudioPayload(extension, corrupted)
			if err != nil {
				t.Fatalf("extract deliberately corrupted payload: %v", err)
			}
			if got := sha256.Sum256(corruptedPayload); got == beforePayloadHash {
				t.Fatal("audio payload digest accepted a deliberately corrupted encoded audio byte")
			}
			beforeProperties, afterProperties := *before.Properties, *after.Properties
			if extension == ".mka" {
				beforeProperties.BitRateBitsPerSecond, afterProperties.BitRateBitsPerSecond = 0, 0
			}
			if !reflect.DeepEqual(beforeProperties, afterProperties) {
				t.Fatalf("audio properties changed: before=%+v after=%+v", before.Properties, after.Properties)
			}
		})
	}
}

func generatedOptionalAssetsPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 200, A: 255})
	img.Set(1, 1, color.RGBA{B: 150, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}
