package metadata

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.senan.xyz/taglib"
)

func TestReadUsesUnifiedTagLibReaderAcrossFormats(t *testing.T) {
	for _, test := range []struct {
		name      string
		extension string
		contents  []byte
		want      map[string][]string
	}{
		{
			name: "FLAC", extension: ".flac",
			contents: tagLibTrialFLACFixture("ARTIST", "First", "ARTIST", "Второй исполнитель 🎵", "CUSTOM-NAME", "opaque-value"),
			want:     map[string][]string{"ARTIST": {"First", "Второй исполнитель 🎵"}, "CUSTOM-NAME": {"opaque-value"}},
		},
		{
			name: "MP3", extension: ".mp3",
			contents: id3Fixture(
				id3Frame("TPE1", []byte{0, 'F', 'i', 'r', 's', 't'}),
				id3Frame("TPE1", []byte{0, 'S', 'e', 'c', 'o', 'n', 'd'}),
			),
			want: map[string][]string{taglib.Artist: {"First", "Second"}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := writeMetadataFixture(t, test.extension, test.contents)
			capture, err := (Reader{}).Read(context.Background(), path, ReadRequest{})
			if err != nil {
				t.Fatal(err)
			}
			if capture.Format != "" {
				t.Fatalf("format = %q", capture.Format)
			}
			if capture.Provenance.Name != "github.com/deluan/go-taglib" ||
				capture.Provenance.Version != "v0.0.0-20260926163408-32d7614f30ec (TagLib v2.3.2)" {
				t.Fatalf("provenance = %#v", capture.Provenance)
			}
			for key, values := range test.want {
				assertValues(t, capture.Tags, key, values)
			}
		})
	}
}

func TestReadMKAUsesNormalizedTextAndPreservesRepeatedValues(t *testing.T) {
	fixture, err := os.ReadFile("testdata/eg.mka")
	if err != nil {
		t.Fatal(err)
	}
	path := writeMetadataFixture(t, ".mka", fixture)
	want := map[string][]string{
		taglib.Title:  {"A repeated MKA title", "Second title value"},
		taglib.Artist: {"First artist", "Second artist"},
	}
	if err := taglib.WriteTags(path, want, taglib.Clear); err != nil {
		t.Fatal(err)
	}

	capture, err := (Reader{}).Read(context.Background(), path, ReadRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if capture.Format != "" || capture.Provenance.Name != "github.com/deluan/go-taglib" {
		t.Fatalf("MKA format/provenance = %q %#v", capture.Format, capture.Provenance)
	}
	for key, values := range want {
		assertValues(t, capture.Tags, key, values)
	}
}

func TestReadReturnsNonNilEmptyTagsAndHonorsContext(t *testing.T) {
	path := writeMetadataFixture(t, ".flac", tagLibTrialFLACFixture())
	capture, err := (Reader{}).Read(context.Background(), path, ReadRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if capture.Tags == nil {
		t.Fatal("successful empty tags map is nil")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Reader{}).Read(ctx, path, ReadRequest{}); err == nil {
		t.Fatal("Read succeeded with a canceled context")
	}
}

func TestReadFormatUsesTagLibDetectionForPreparedPaths(t *testing.T) {
	for _, extension := range []string{"", ".bin"} {
		t.Run("suffix="+extension, func(t *testing.T) {
			path := writeMetadataFixture(t, extension, tagLibTrialFLACFixture("TITLE", "fixture"))
			capture, err := (Reader{}).Read(context.Background(), path, ReadRequest{Properties: true})
			if err != nil {
				t.Fatal(err)
			}
			if capture.Format != taglib.FormatFLAC.String() || capture.Properties == nil || capture.Properties.Format != capture.Format {
				t.Fatalf("format/properties = %q %#v", capture.Format, capture.Properties)
			}
		})
	}
}

func TestReadPropertiesAreExplicitAndOptional(t *testing.T) {
	path := writeMetadataFixture(t, ".flac", tagLibTrialFLACFixture("TITLE", "fixture"))
	propertyCalls := 0
	reader := Reader{
		readTags: func(string) (map[string][]string, error) {
			return map[string][]string{"TITLE": {"fixture"}}, nil
		},
		readProperties: func(string) (taglib.Properties, error) {
			propertyCalls++
			return taglib.Properties{}, os.ErrInvalid
		},
	}
	capture, err := reader.Read(context.Background(), path, ReadRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if propertyCalls != 0 || capture.Properties != nil {
		t.Fatalf("unrequested properties were read: calls=%d result=%#v", propertyCalls, capture.Properties)
	}
	capture, err = reader.Read(context.Background(), path, ReadRequest{Properties: true})
	if err != nil {
		t.Fatalf("optional property error failed tag read: %v", err)
	}
	if propertyCalls != 1 || capture.Properties != nil {
		t.Fatalf("requested property failure result: calls=%d result=%#v", propertyCalls, capture.Properties)
	}
	reader.readProperties = func(string) (taglib.Properties, error) {
		return taglib.Properties{Bitrate: 192, BitsPerSample: 24, Codec: "FLAC"}, nil
	}
	capture, err = reader.Read(context.Background(), path, ReadRequest{Properties: true})
	if err != nil {
		t.Fatal(err)
	}
	if capture.Properties == nil || capture.Properties.BitRateBitsPerSecond != 192000 ||
		capture.Properties.BitDepth == nil || *capture.Properties.BitDepth != 24 || capture.Properties.InnerCodec != "FLAC" {
		t.Fatalf("converted properties = %#v", capture.Properties)
	}
}

func TestReadCoverInspectionIsExplicitDetachedAndObserved(t *testing.T) {
	path := writeMetadataFixture(t, ".flac", tagLibTrialFLACFixture())
	image := generatedPNG(t)
	tags := map[string][]string{"TITLE": {"before"}}
	imageCalls := 0
	reader := Reader{
		readTags:  func(string) (map[string][]string, error) { return tags, nil },
		readImage: func(string) ([]byte, error) { imageCalls++; return image, nil },
	}
	capture, err := reader.Read(context.Background(), path, ReadRequest{})
	if err != nil || imageCalls != 0 || capture.CoverInspection != nil {
		t.Fatalf("unrequested image inspection: calls=%d capture=%#v err=%v", imageCalls, capture, err)
	}
	capture, err = reader.Read(context.Background(), path, ReadRequest{CoverInspection: true})
	if err != nil {
		t.Fatal(err)
	}
	if imageCalls != 1 || capture.CoverInspection == nil || !capture.CoverInspection.Observed || capture.CoverInspection.Cover == nil {
		t.Fatalf("inspection = %#v, calls=%d", capture.CoverInspection, imageCalls)
	}
	cover := capture.CoverInspection.Cover
	if cover.MIME != "image/png" || !bytes.Equal(cover.Data, image) {
		t.Fatalf("cover = %#v", cover)
	}
	image[0] ^= 0xff
	if bytes.Equal(cover.Data, image) {
		t.Fatal("captured image aliases reader-owned bytes")
	}
	capture.Tags["TITLE"][0] = "changed"
	if capture.Tags["TITLE"][0] == "before" {
		t.Fatal("test failed to mutate captured tags")
	}
	if tags["TITLE"][0] != "before" {
		t.Fatal("captured tags mutation changed the source map")
	}
}

func TestReadCoverInspectionAbsenceFailuresAndCancellation(t *testing.T) {
	path := writeMetadataFixture(t, ".flac", tagLibTrialFLACFixture())
	reader := Reader{readTags: func(string) (map[string][]string, error) { return map[string][]string{}, nil }, readImage: func(string) ([]byte, error) { return []byte{}, nil }}
	capture, err := reader.Read(context.Background(), path, ReadRequest{CoverInspection: true})
	if err != nil || capture.CoverInspection == nil || !capture.CoverInspection.Observed || capture.CoverInspection.Cover != nil {
		t.Fatalf("empty image inspection = %#v, err=%v", capture.CoverInspection, err)
	}
	for name, readImage := range map[string]func(string) ([]byte, error){
		"error": func(string) ([]byte, error) { return nil, errors.New("private read failure") },
		"panic": func(string) ([]byte, error) { panic("private image payload") },
	} {
		t.Run(name, func(t *testing.T) {
			reader.readImage = readImage
			if _, err := reader.Read(context.Background(), path, ReadRequest{CoverInspection: true}); err == nil || strings.Contains(err.Error(), "private image payload") {
				t.Fatalf("inspection error = %v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	reader.readImage = func(string) ([]byte, error) { cancel(); return []byte{1}, nil }
	if _, err := reader.Read(ctx, path, ReadRequest{CoverInspection: true}); !errors.Is(err, context.Canceled) {
		t.Fatalf("post-read cancellation error = %v", err)
	}
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := reader.Read(cancelled, path, ReadRequest{CoverInspection: true}); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-read cancellation error = %v", err)
	}
}

func TestReadCoverInspectionRoundTripsAcrossFormats(t *testing.T) {
	for _, extension := range []string{".flac", ".mp3", ".m4a", ".mka"} {
		t.Run(extension, func(t *testing.T) {
			contents, err := os.ReadFile("testdata/eg" + extension)
			if err != nil {
				t.Fatal(err)
			}
			path := writeMetadataFixture(t, extension, contents)
			want := generatedPNG(t)
			mime := http.DetectContentType(want)
			if _, err := (Writer{}).Write(context.Background(), path, nil, &Cover{Data: want, MIME: mime}); err != nil {
				t.Fatal(err)
			}
			capture, err := (Reader{}).Read(context.Background(), path, ReadRequest{CoverInspection: true})
			if err != nil || capture.CoverInspection == nil || !capture.CoverInspection.Observed || capture.CoverInspection.Cover == nil {
				t.Fatalf("inspection = %#v, err=%v", capture.CoverInspection, err)
			}
			if capture.CoverInspection.Cover.MIME != mime || !bytes.Equal(capture.CoverInspection.Cover.Data, want) {
				t.Fatalf("cover did not round-trip: %#v", capture.CoverInspection.Cover)
			}
		})
	}
}

func TestReaderContainsUpstreamPanics(t *testing.T) {
	path := writeMetadataFixture(t, ".flac", tagLibTrialFLACFixture())
	reader := Reader{readTags: func(string) (map[string][]string, error) { panic("private path and payload") }}
	_, err := reader.Read(context.Background(), path, ReadRequest{})
	if err == nil || strings.Contains(err.Error(), "private path") || strings.Contains(err.Error(), path) {
		t.Fatalf("panic was not converted to a fixed safe error: %v", err)
	}

	reader = Reader{
		readTags: func(string) (map[string][]string, error) { return map[string][]string{}, nil },
		readProperties: func(string) (taglib.Properties, error) {
			panic("private property payload")
		},
	}
	if _, err := reader.Read(context.Background(), path, ReadRequest{Properties: true}); err != nil {
		t.Fatalf("optional property panic failed tag read: %v", err)
	}
}

func assertValues(t *testing.T, tags map[string][]string, key string, want []string) {
	t.Helper()
	got := tags[key]
	if len(got) != len(want) {
		t.Fatalf("tag %q = %#v, want %#v", key, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tag %q = %#v, want %#v", key, got, want)
		}
	}
}

func tagLibTrialFLACFixture(fields ...string) []byte {
	comments := flacFixture(fields...)
	streamInfo := []byte{0x00, 0x00, 0x00, 0x22}
	streamInfo = append(streamInfo, make([]byte, 34)...)
	return append(append([]byte("fLaC"), streamInfo...), comments[4:]...)
}

func id3Fixture(frames ...[]byte) []byte {
	body := make([]byte, 0)
	for _, frame := range frames {
		body = append(body, frame...)
	}
	size := len(body)
	header := []byte{'I', 'D', '3', 4, 0, 0, byte(size >> 21), byte(size>>14) & 0x7F, byte(size>>7) & 0x7F, byte(size) & 0x7F}
	return append(header, body...)
}

func id3Frame(identifier string, body []byte) []byte {
	size := len(body)
	frame := []byte{identifier[0], identifier[1], identifier[2], identifier[3], byte(size >> 21), byte(size>>14) & 0x7F, byte(size>>7) & 0x7F, byte(size) & 0x7F, 0, 0}
	return append(frame, body...)
}

func flacFixture(fields ...string) []byte {
	comment := make([]byte, 0)
	appendText := func(value string) {
		var size [4]byte
		binary.LittleEndian.PutUint32(size[:], uint32(len(value)))
		comment = append(comment, size[:]...)
		comment = append(comment, value...)
	}
	appendText("fixture")
	var count [4]byte
	binary.LittleEndian.PutUint32(count[:], uint32(len(fields)/2))
	comment = append(comment, count[:]...)
	for index := 0; index < len(fields); index += 2 {
		appendText(fields[index] + "=" + fields[index+1])
	}
	block := []byte{0x84, byte(len(comment) >> 16), byte(len(comment) >> 8), byte(len(comment))}
	block = append(block, comment...)
	return append([]byte("fLaC"), block...)
}

func writeMetadataFixture(t *testing.T, extension string, contents []byte) string {
	t.Helper()
	name := "fixture" + extension
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
