package metadata

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"go.senan.xyz/taglib"
)

func TestWriterRoundTripsTagsAndPreservesAudioProperties(t *testing.T) {
	for _, ext := range []string{".flac", ".mp3", ".m4a", ".mka"} {
		t.Run(ext, func(t *testing.T) {
			contents, err := os.ReadFile(filepath.Join("testdata", "eg"+ext))
			if err != nil {
				t.Fatal(err)
			}
			path := writeMetadataFixture(t, ext, contents)
			before, err := (Reader{}).Read(context.Background(), path, ReadRequest{Properties: true})
			if err != nil {
				t.Fatal(err)
			}
			if before.Properties == nil {
				t.Fatal("audio properties were not read before writing")
			}
			if before.Properties.SampleRateHz == 0 || before.Properties.Channels == 0 {
				t.Fatalf("audio properties are not useful: %#v", before.Properties)
			}
			if before.Properties.BitRateBitsPerSecond == 0 {
				t.Fatalf("audio bitrate is not useful before writing: %#v", before.Properties)
			}
			requested := map[string][]string{
				taglib.Title:                     {""},
				taglib.Artist:                    {"First", "Second"},
				taglib.MusicBrainzTrackID:        {"recording-mbid"},
				taglib.MusicBrainzReleaseTrackID: {"release-track-mbid"},
				"EMPTY-KEY":                      {""},
			}
			expected := map[string][]string{
				taglib.Artist:                    {"First", "Second"},
				taglib.MusicBrainzTrackID:        {"recording-mbid"},
				taglib.MusicBrainzReleaseTrackID: {"release-track-mbid"},
			}
			imageBytes := generatedPNG(t)
			result, err := (Writer{}).Write(context.Background(), path, requested, &Cover{Data: imageBytes, MIME: "image/png"})
			if err != nil {
				t.Fatal(err)
			}
			for key, values := range expected {
				if got := result.Tags[key]; !reflect.DeepEqual(got, values) {
					t.Fatalf("%s values = %#v, want %#v", key, got, values)
				}
			}
			for _, key := range []string{taglib.Title, "EMPTY-KEY"} {
				if got, exists := result.Tags[key]; exists {
					t.Fatalf("deleted tag %q remained after write: %#v", key, got)
				}
			}
			if result.Cover == nil || result.Cover.Error != "" || result.Cover.MIME != "image/png" || !bytes.Equal(result.Cover.Data, imageBytes) {
				t.Fatalf("cover observation = %#v", result.Cover)
			}
			after, err := (Reader{}).Read(context.Background(), path, ReadRequest{Properties: true})
			if err != nil {
				t.Fatal(err)
			}
			if after.Properties == nil {
				t.Fatal("audio properties were not read after writing")
			}
			if after.Properties.BitRateBitsPerSecond == 0 {
				t.Fatalf("audio bitrate is not useful after writing: %#v", after.Properties)
			}
			beforeComparable, afterComparable := *before.Properties, *after.Properties
			if ext == ".mka" {
				// TagLib 2.3.2 computes Matroska bitrate as whole-file bytes * 8 / duration;
				// adding tags or cover art therefore changes this container-derived value.
				beforeComparable.BitRateBitsPerSecond = 0
				afterComparable.BitRateBitsPerSecond = 0
			}
			if !reflect.DeepEqual(beforeComparable, afterComparable) {
				t.Fatalf("audio properties changed: before %#v after %#v", before.Properties, after.Properties)
			}
		})
	}
}

func TestWriterRejectsTagReadbackMismatchAndContainsPanics(t *testing.T) {
	write := func(string, map[string][]string, taglib.WriteOption) error { return nil }
	_, err := (Writer{writeTags: write, readTags: func(string) (map[string][]string, error) {
		return map[string][]string{}, nil
	}}).Write(context.Background(), "staged", map[string][]string{taglib.Title: {"expected"}}, nil)
	if err == nil {
		t.Fatal("expected tag readback mismatch")
	}
	if _, err := (Writer{writeTags: write, readTags: func(string) (map[string][]string, error) {
		return map[string][]string{}, nil
	}}).Write(context.Background(), "staged", map[string][]string{taglib.Title: {""}}, nil); err != nil {
		t.Fatalf("absent deleted tag should pass: %v", err)
	}
	for name, stale := range map[string]map[string][]string{
		"stale value": {taglib.Title: {"stale"}},
		"nil value":   {taglib.Title: nil},
	} {
		_, err := (Writer{writeTags: write, readTags: func(string) (map[string][]string, error) {
			return stale, nil
		}}).Write(context.Background(), "staged", map[string][]string{taglib.Title: {""}}, nil)
		if err == nil {
			t.Fatalf("%s: expected deleted tag readback mismatch", name)
		}
	}
	_, err = (Writer{writeTags: func(string, map[string][]string, taglib.WriteOption) error { panic("private payload") }}).
		Write(context.Background(), "staged", nil, nil)
	if err == nil || err.Error() != "write normalized metadata tags: metadata tag writer panicked" {
		t.Fatalf("panic error = %v", err)
	}
}

func TestWriterHandlesFailuresAndContext(t *testing.T) {
	_, err := (Writer{}).Write(context.Background(), filepath.Join(t.TempDir(), "missing.flac"), nil, nil)
	if err == nil {
		t.Fatal("expected invalid staged path error")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = (Writer{}).Write(canceled, "unused", nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled write error = %v", err)
	}
	_, err = (Writer{writeTags: func(string, map[string][]string, taglib.WriteOption) error { return errors.New("failed") }}).
		Write(context.Background(), "staged", nil, nil)
	if err == nil {
		t.Fatal("expected upstream write failure")
	}
}
