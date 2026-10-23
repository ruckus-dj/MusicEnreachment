package metadata

import (
	"bytes"
	"context"
	"crypto/sha256"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"go.senan.xyz/taglib"
)

func TestClearArtworkAcrossRealContainers(t *testing.T) {
	for _, extension := range []string{".flac", ".mp3", ".m4a", ".mka"} {
		t.Run(extension, func(t *testing.T) {
			contents, err := os.ReadFile(filepath.Join("testdata", "eg"+extension))
			if err != nil {
				t.Fatal(err)
			}
			path := writeMetadataFixture(t, extension, contents)
			tags := map[string][]string{taglib.Title: {"artwork clearing"}, taglib.Lyrics: {"lyrics remain"}}
			coverA := generatedClearArtworkPNG(t, color.RGBA{R: 190, A: 255})
			if _, err := (Writer{}).Write(context.Background(), path, tags, &Cover{Data: coverA, MIME: "image/png"}); err != nil {
				t.Fatalf("add source artwork: %v", err)
			}
			secondCover := generatedClearArtworkPNG(t, color.RGBA{G: 170, A: 255})
			if err := taglib.WriteImageOptions(path, secondCover, 1, "Back Cover", "second source picture", "image/png"); err != nil {
				t.Fatalf("add second source picture: %v", err)
			}

			properties, err := taglib.ReadProperties(path)
			if err != nil || len(properties.Images) != 2 {
				t.Fatalf("source artwork properties = %+v, %v", properties, err)
			}
			beforeBytes, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			beforePayload, err := encodedAudioPayload(extension, beforeBytes)
			if err != nil || len(beforePayload) == 0 {
				t.Fatalf("extract encoded audio payload: %v", err)
			}
			beforePayloadHash := sha256.Sum256(beforePayload)

			if err := ClearArtwork(context.Background(), path); err != nil {
				t.Fatalf("clear source artwork: %v", err)
			}
			properties, err = taglib.ReadProperties(path)
			if err != nil || len(properties.Images) != 0 {
				t.Fatalf("artwork after clearing = %+v, %v", properties.Images, err)
			}
			gotTags, err := taglib.ReadTags(path)
			if err != nil || !reflect.DeepEqual(gotTags[taglib.Title], tags[taglib.Title]) || !reflect.DeepEqual(gotTags[taglib.Lyrics], tags[taglib.Lyrics]) {
				t.Fatalf("text tags changed while clearing artwork: tags=%v, error=%v", gotTags, err)
			}

			coverB := generatedClearArtworkPNG(t, color.RGBA{B: 210, A: 255})
			if _, err := (Writer{}).Write(context.Background(), path, tags, &Cover{Data: coverB, MIME: "image/png"}); err != nil {
				t.Fatalf("write database artwork replacement: %v", err)
			}
			observed, err := taglib.ReadImage(path)
			if err != nil || !bytes.Equal(observed, coverB) || bytes.Equal(observed, coverA) {
				t.Fatalf("replacement artwork did not match the requested cover: error=%v", err)
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
		})
	}
}

func TestClearArtworkValidatesContextAndPath(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ClearArtwork(ctx, "candidate"); err != context.Canceled {
		t.Fatalf("canceled context error = %v", err)
	}
	if err := ClearArtwork(context.Background(), ""); err == nil {
		t.Fatal("empty candidate path was accepted")
	}
}

func generatedClearArtworkPNG(t *testing.T, pixel color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.SetRGBA(0, 0, pixel)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}
