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

func TestWriteWithOptionalLyricsStatusesAndDetachedTags(t *testing.T) {
	for _, tc := range []struct {
		name   string
		lyrics []string
		want   OptionalLyricsWriteStatus
	}{
		{name: "verified", lyrics: []string{"text"}, want: OptionalLyricsVerified},
		{name: "absent", want: OptionalLyricsAbsent},
		{name: "wrong text", lyrics: []string{"other"}, want: OptionalLyricsMismatch},
		{name: "empty", lyrics: []string{""}, want: OptionalLyricsMismatch},
		{name: "wrong cardinality", lyrics: []string{"text", "other"}, want: OptionalLyricsMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actual := map[string][]string{"TITLE": {"title"}}
			if tc.lyrics != nil {
				actual["LYRICS"] = append([]string(nil), tc.lyrics...)
			}
			writer := Writer{
				writeTags: func(_ string, tags map[string][]string, _ taglib.WriteOption) error {
					if tags["LYRICS"][0] != "text" {
						t.Fatalf("writer didn't receive requested lyrics: %#v", tags)
					}
					return nil
				},
				readTags: func(string) (map[string][]string, error) { return actual, nil },
			}
			result, err := writer.WriteWithOptionalLyrics(context.Background(), "staged", map[string][]string{"TITLE": {"title"}, "LYRICS": {"text"}}, nil)
			if err != nil || result.Status != tc.want {
				t.Fatalf("result = %#v, error = %v; want status %v", result, err, tc.want)
			}
			result.Tags["TITLE"][0] = "changed"
			if actual["TITLE"][0] != "title" {
				t.Fatal("result tags were not detached")
			}
		})
	}
}

func TestWriteWithOptionalLyricsRejectsMandatoryFailures(t *testing.T) {
	requested := map[string][]string{"TITLE": {"title"}, "LYRICS": {"text"}}
	for name, test := range map[string]struct {
		requested map[string][]string
		actual    map[string][]string
	}{
		"required mismatch": {requested: requested, actual: map[string][]string{"TITLE": {"wrong"}, "LYRICS": {"text"}}},
		"unexpected tag":    {requested: requested, actual: map[string][]string{"TITLE": {"title"}, "ARTIST": {"unexpected"}, "LYRICS": {"text"}}},
		"deletion mismatch": {requested: map[string][]string{"TITLE": {"title"}, "DELETE-ME": {""}, "LYRICS": {"text"}}, actual: map[string][]string{"TITLE": {"title"}, "DELETE-ME": {"unexpected"}, "LYRICS": {"text"}}},
	} {
		t.Run(name, func(t *testing.T) {
			writer := Writer{
				writeTags: func(string, map[string][]string, taglib.WriteOption) error { return nil },
				readTags:  func(string) (map[string][]string, error) { return test.actual, nil },
			}
			result, err := writer.WriteWithOptionalLyrics(context.Background(), "staged", test.requested, nil)
			if err == nil || result.Status != OptionalLyricsUnobserved || result.Tags != nil {
				t.Fatalf("result = %#v, error = %v; expected fatal error with zero result", result, err)
			}
		})
	}
	for _, lyrics := range [][]string{nil, {}, {"", "text"}, {"  \n"}} {
		calls := 0
		writer := Writer{writeTags: func(string, map[string][]string, taglib.WriteOption) error { calls++; return nil }}
		if _, err := writer.WriteWithOptionalLyrics(context.Background(), "staged", map[string][]string{"LYRICS": lyrics}, nil); err == nil || calls != 0 {
			t.Fatalf("invalid lyrics %q: error=%v mutation calls=%d", lyrics, err, calls)
		}
	}
}

func TestWriteWithOptionalLyricsFatalNativeCoverReadAndCancellationFailures(t *testing.T) {
	image := generatedPNG(t)
	cover := &Cover{Data: image, MIME: "image/png"}
	requested := map[string][]string{"TITLE": {"title"}, "LYRICS": {"text"}}
	fatal := func(name string, writer Writer, ctx context.Context, requestedCover *Cover) {
		t.Helper()
		result, err := writer.WriteWithOptionalLyrics(ctx, "staged", requested, requestedCover)
		if err == nil || result.Status != OptionalLyricsUnobserved || result.Tags != nil {
			t.Fatalf("%s: result = %#v, error = %v; expected fatal error", name, result, err)
		}
	}
	baseRead := func(string) (map[string][]string, error) {
		return map[string][]string{"TITLE": {"title"}, "LYRICS": {"text"}}, nil
	}
	fatal("native error", Writer{writeTags: func(string, map[string][]string, taglib.WriteOption) error { return errors.New("opaque") }}, context.Background(), nil)
	fatal("native panic", Writer{writeTags: func(string, map[string][]string, taglib.WriteOption) error { panic("opaque") }}, context.Background(), nil)
	reads := 0
	fatal("read error", Writer{
		writeTags: func(string, map[string][]string, taglib.WriteOption) error { return nil },
		readTags:  func(string) (map[string][]string, error) { reads++; return nil, errors.New("opaque") },
	}, context.Background(), nil)
	if reads != 1 {
		t.Fatalf("read error case reached tag reader %d times, want 1", reads)
	}
	reads = 0
	fatal("read panic", Writer{
		writeTags: func(string, map[string][]string, taglib.WriteOption) error { return nil },
		readTags:  func(string) (map[string][]string, error) { reads++; panic("opaque") },
	}, context.Background(), nil)
	if reads != 1 {
		t.Fatalf("read panic case reached tag reader %d times, want 1", reads)
	}
	fatal("cover error", Writer{writeTags: func(string, map[string][]string, taglib.WriteOption) error { return nil }, readTags: baseRead, writeImage: func(string, []byte) error { return errors.New("opaque") }}, context.Background(), cover)
	fatal("cover read error", Writer{writeTags: func(string, map[string][]string, taglib.WriteOption) error { return nil }, readTags: baseRead, writeImage: func(string, []byte) error { return nil }, readImage: func(string) ([]byte, error) { return nil, errors.New("opaque") }}, context.Background(), cover)
	// The cover write mutates the shared observation from valid to corrupt; the
	// final read must happen after that mutation and reject the required tag.
	observed := map[string][]string{"TITLE": {"title"}, "LYRICS": {"text"}}
	reads = 0
	fatal("cover-mutated required tag", Writer{
		writeTags: func(string, map[string][]string, taglib.WriteOption) error { return nil },
		readTags:  func(string) (map[string][]string, error) { reads++; return observed, nil },
		writeImage: func(string, []byte) error {
			observed["TITLE"] = []string{"corrupt"}
			return nil
		},
		readImage: func(string) ([]byte, error) { return image, nil },
	}, context.Background(), cover)
	if reads != 1 {
		t.Fatalf("cover-mutated required tag reached final reader %d times, want 1", reads)
	}
	canceled, cancel := context.WithCancel(context.Background())
	fatal("canceled", Writer{writeTags: func(string, map[string][]string, taglib.WriteOption) error { cancel(); return nil }}, canceled, nil)
	if _, err := (Writer{writeTags: func(string, map[string][]string, taglib.WriteOption) error { return nil }, readTags: func(string) (map[string][]string, error) {
		return map[string][]string{"TITLE": {"title"}, "LYRICS": {"wrong"}}, nil
	}}).Write(context.Background(), "staged", requested, nil); err == nil {
		t.Fatal("ordinary Write must retain fatal lyrics mismatch behavior")
	}
}

func TestWriteWithOptionalLyricsReadsFinalTagsAfterCover(t *testing.T) {
	image := generatedPNG(t)
	observed := map[string][]string{"TITLE": {"title"}, "LYRICS": {"text"}}
	var order []string
	writer := Writer{
		writeTags:  func(string, map[string][]string, taglib.WriteOption) error { order = append(order, "tags"); return nil },
		writeImage: func(string, []byte) error { order = append(order, "image"); return nil },
		readImage:  func(string) ([]byte, error) { return image, nil },
		readTags:   func(string) (map[string][]string, error) { order = append(order, "read"); return observed, nil },
	}
	result, err := writer.WriteWithOptionalLyrics(context.Background(), "staged",
		map[string][]string{"TITLE": {"title"}, "LYRICS": {"text"}}, &Cover{Data: image, MIME: "image/png"})
	if err != nil || result.Status != OptionalLyricsVerified {
		t.Fatalf("result = %#v, error = %v; want verified", result, err)
	}
	if !reflect.DeepEqual(order, []string{"tags", "image", "read"}) {
		t.Fatalf("mutation/read order = %#v", order)
	}
	result.Tags["LYRICS"][0] = "changed"
	if observed["LYRICS"][0] != "text" {
		t.Fatal("result tags were not detached")
	}
}
