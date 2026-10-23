package metadata

import (
	"bytes"
	"context"
	"crypto/sha256"
	"image/color"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"go.senan.xyz/taglib"
)

func TestWriteCoverOnlyPreservesNativeTagsAudioAndReplacesEveryPicture(t *testing.T) {
	for _, extension := range []string{".flac", ".mp3", ".m4a", ".mka"} {
		t.Run(extension, func(t *testing.T) {
			contents, err := os.ReadFile(filepath.Join("testdata", "eg"+extension))
			if err != nil {
				t.Fatal(err)
			}
			path := writeMetadataFixture(t, extension, contents)
			tags := map[string][]string{taglib.Title: {"original title", "second value"}, taglib.Lyrics: {"sentinel lyrics\nline two"}, "COMMENT": {"sentinel comment"}}
			if _, err := (Writer{}).Write(context.Background(), path, tags, nil); err != nil {
				t.Fatalf("write fixture tags: %v", err)
			}
			coverA := generatedClearArtworkPNG(t, color.RGBA{R: 190, A: 255})
			if _, err := (Writer{}).Write(context.Background(), path, tags, &Cover{Data: coverA, MIME: "image/png"}); err != nil {
				t.Fatalf("write initial artwork: %v", err)
			}
			coverSecond := generatedClearArtworkPNG(t, color.RGBA{G: 160, A: 255})
			if err := taglib.WriteImageOptions(path, coverSecond, 1, "Back Cover", "secondary artwork", "image/png"); err != nil {
				t.Fatalf("write secondary artwork: %v", err)
			}
			beforeTags, err := taglib.ReadTags(path)
			if err != nil {
				t.Fatal(err)
			}
			beforeProperties, err := taglib.ReadProperties(path)
			if err != nil || len(beforeProperties.Images) != 2 {
				t.Fatalf("fixture properties = %+v, error = %v", beforeProperties, err)
			}
			beforeFile, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			beforePayload, err := encodedAudioPayload(extension, beforeFile)
			if err != nil {
				t.Fatalf("extract encoded audio payload: %v", err)
			}
			beforePayloadHash := sha256.Sum256(beforePayload)

			coverB := generatedClearArtworkPNG(t, color.RGBA{B: 220, A: 255})
			writeTagsCalls := 0
			result, err := (Writer{writeTags: func(string, map[string][]string, taglib.WriteOption) error {
				writeTagsCalls++
				return nil
			}}).WriteCoverOnly(context.Background(), path, &Cover{Data: coverB, MIME: "image/png"})
			if err != nil || result.CoverStatus != OptionalCoverVerified {
				t.Fatalf("cover-only write result = %+v, error = %v", result, err)
			}
			if writeTagsCalls != 0 {
				t.Fatalf("tag writes = %d, want none", writeTagsCalls)
			}
			afterTags, err := taglib.ReadTags(path)
			if err != nil || !reflect.DeepEqual(beforeTags, afterTags) {
				t.Fatalf("native tags changed: before=%v after=%v error=%v", beforeTags, afterTags, err)
			}
			observed, err := taglib.ReadImage(path)
			if err != nil || !bytes.Equal(observed, coverB) || bytes.Equal(observed, coverA) {
				t.Fatalf("replacement cover mismatch: error=%v", err)
			}
			afterProperties, err := taglib.ReadProperties(path)
			if err != nil || len(afterProperties.Images) != 1 {
				t.Fatalf("pictures after replacement = %+v, error = %v", afterProperties.Images, err)
			}
			afterFile, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			afterPayload, err := encodedAudioPayload(extension, afterFile)
			if err != nil || sha256.Sum256(afterPayload) != beforePayloadHash {
				t.Fatalf("encoded audio payload changed: error=%v", err)
			}
		})
	}
}
