package metadata

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.senan.xyz/taglib"
)

func TestReadReturnsTagLibNormalizedTextAndProvenance(t *testing.T) {
	t.Run("extensionless prepared path", func(t *testing.T) {
		const recordingID = "550e8400-e29b-41d4-a716-446655440000"
		path := writeMetadataFixture(t, "", tagLibTrialFLACFixture(
			"ARTIST", "First", "ARTIST", "Second", "CUSTOM-NAME", "opaque-value",
			"MUSICBRAINZ_TRACKID", recordingID,
		))
		capture, err := (Reader{}).Read(context.Background(), path, ReadRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if capture.Provenance.Name != "go.senan.xyz/taglib" || capture.Provenance.Version != "v0.14.0" {
			t.Fatalf("provenance = %#v", capture.Provenance)
		}
		assertValues(t, capture.Tags, taglib.Artist, []string{"First", "Second"})
		assertValues(t, capture.Tags, "CUSTOM-NAME", []string{"opaque-value"})
		assertValues(t, capture.Tags, taglib.MusicBrainzTrackID, []string{recordingID})
		capture.Tags[taglib.Artist][0] = "mutated"
		if capture.Tags[taglib.Artist][1] != "Second" {
			t.Fatalf("returned tag values alias each other: %#v", capture.Tags[taglib.Artist])
		}
	})

	t.Run("ID3 repeats", func(t *testing.T) {
		path := writeMetadataFixture(t, ".mp3", id3Fixture(
			id3Frame("TPE1", []byte{0, 'F', 'i', 'r', 's', 't'}),
			id3Frame("TPE1", []byte{0, 'S', 'e', 'c', 'o', 'n', 'd'}),
			id3Frame("TXXX", []byte{0, 'C', 'u', 's', 't', 'o', 'm', 0, 'v', 'a', 'l', 'u', 'e'}),
		))
		capture, err := (Reader{}).Read(context.Background(), path, ReadRequest{})
		if err != nil {
			t.Fatal(err)
		}
		assertValues(t, capture.Tags, taglib.Artist, []string{"First", "Second"})
		assertValues(t, capture.Tags, "CUSTOM", []string{"value"})
	})

	t.Run("MP4 first-value normalization is accepted", func(t *testing.T) {
		path := writeMetadataFixture(t, ".m4a", mp4Fixture("First", "Second"))
		capture, err := (Reader{}).Read(context.Background(), path, ReadRequest{})
		if err != nil {
			t.Fatal(err)
		}
		assertValues(t, capture.Tags, taglib.Artist, []string{"First"})
		assertValues(t, capture.Tags, taglib.Title, []string{"fixture-title"})
	})
}

func TestReadMatroskaPreservesScopedNestedAndBinaryTags(t *testing.T) {
	global := mkvElement(0x7373,
		mkvElement(0x63c0,
			mkvElement(0x63ca, []byte("ALBUM")),
			mkvElement(0x68ca, []byte{50}),
		),
		mkvElement(0x67c8,
			mkvElement(0x45a3, []byte("TITLE")),
			mkvElement(0x447a, []byte("eng")),
			mkvElement(0x4487, []byte("global title")),
			mkvElement(0x67c8,
				mkvElement(0x45a3, []byte("ALBUMARTIST")),
				mkvElement(0x4487, []byte("global album artist")),
			),
			mkvElement(0x67c8,
				mkvElement(0x45a3, []byte("ARTIST")),
				mkvElement(0x4487, []byte("nested artist")),
			),
		),
	)
	scoped := mkvElement(0x7373,
		mkvElement(0x63c0, mkvElement(0x63c5, []byte{1})),
		mkvElement(0x67c8,
			mkvElement(0x45a3, []byte("TITLE")),
			mkvElement(0x4487, []byte("track title one")),
		),
		mkvElement(0x67c8,
			mkvElement(0x45a3, []byte("TITLE")),
			mkvElement(0x4487, []byte("track title two")),
		),
		mkvElement(0x67c8,
			mkvElement(0x45a3, []byte("OPAQUE")),
			mkvElement(0x4485, []byte{0, 0xff, 1}),
		),
	)
	segmentBody := append(mkvTracks(mkvTestTrack{uid: 1, trackType: 2}, mkvTestTrack{uid: 2, trackType: 1}), global...)
	segmentBody = append(segmentBody, scoped...)
	segment := mkvElement(0x18538067, segmentBody)
	path := writeMetadataFixture(t, "", append(mkvElement(0x1a45dfa3, nil), segment...))
	capture, err := (Reader{}).Read(context.Background(), path, ReadRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if capture.Format != "mka" || capture.Provenance.Name != "github.com/remko/go-mkvparse" {
		t.Fatalf("Matroska provenance/format = %q %#v", capture.Format, capture.Provenance)
	}
	if capture.MatroskaProjectionUnresolved {
		t.Fatalf("unique audio-track projection unexpectedly unresolved: %#v", capture.MatroskaProjectionDiagnostics)
	}
	assertValues(t, capture.Tags, "TITLE", []string{"global title", "track title one", "track title two"})
	assertValues(t, capture.Tags, "ARTIST", []string{"nested artist"})
	assertValues(t, capture.Tags, "ALBUMARTIST", []string{"global album artist"})
	if hasTargets(capture.Matroska.Tags[0].Targets) {
		t.Fatalf("TargetType metadata incorrectly made a tag scoped: %#v", capture.Matroska.Tags[0].Targets)
	}
	if len(capture.Matroska.Tags) != 2 {
		t.Fatalf("native Matroska tags = %#v", capture.Matroska.Tags)
	}
	if got := capture.Matroska.Tags[0].SimpleTags[0].Language; got != "eng" {
		t.Fatalf("language = %q", got)
	}
	if got := capture.Matroska.Tags[0].SimpleTags[0].Children[1].Name; got != "ARTIST" {
		t.Fatalf("nested name = %q", got)
	}
	if got := capture.Matroska.Tags[1].SimpleTags[2].Binary; len(got) != 1 || got[0] != "AP8B" {
		t.Fatalf("binary values = %#v", got)
	}
	if len(capture.Matroska.Tracks) != 2 || capture.Matroska.Tracks[0].Type == nil || *capture.Matroska.Tracks[0].Type != 2 {
		t.Fatalf("tracks = %#v", capture.Matroska.Tracks)
	}
}

func TestMatroskaMultipleAudioTracksKeepOnlyGlobalProjection(t *testing.T) {
	metadata := writeMkvCapture(t,
		[]mkvTestTrack{{uid: 10, trackType: 2}, {uid: 20, trackType: 2}},
		mkvGlobalTag("TITLE", "global"),
		mkvTrackTag(10, mkvSimpleTag("TITLE", "first audio")),
		mkvTrackTag(20, mkvSimpleTag("TITLE", "second audio")),
		mkvTrackTag(0, mkvSimpleTag("TITLE", "all tracks")),
	)
	assertValues(t, metadata.Tags, "TITLE", []string{"global"})
	if !metadata.MatroskaProjectionUnresolved || !containsDiagnostic(metadata, "multiple audio tracks") {
		t.Fatalf("multiple-audio projection status: %#v", metadata)
	}
	if len(metadata.Matroska.Tags) != 4 {
		t.Fatalf("all-tracks native tag was lost: %#v", metadata.Matroska.Tags)
	}
}

func TestMatroskaExcludesVideoAndMixedEntityTargets(t *testing.T) {
	metadata := writeMkvCapture(t,
		[]mkvTestTrack{{uid: 10, trackType: 2}, {uid: 20, trackType: 1}},
		mkvGlobalTag("TITLE", "global"),
		mkvTrackTag(20, mkvSimpleTag("TITLE", "video")),
		mkvElement(0x7373,
			mkvElement(0x63c0, mkvElement(0x63c5, []byte{10}), mkvElement(0x63c4, []byte{1})),
			mkvSimpleTag("TITLE", "track and chapter"),
		),
		mkvElement(0x7373,
			mkvElement(0x63c0, mkvElement(0x63c5, []byte{10}), mkvElement(0x63c9, []byte{1})),
			mkvSimpleTag("TITLE", "track and edition"),
		),
		mkvElement(0x7373,
			mkvElement(0x63c0, mkvElement(0x63c5, []byte{10}), mkvElement(0x63c6, []byte{1})),
			mkvSimpleTag("TITLE", "track and attachment"),
		),
	)
	assertValues(t, metadata.Tags, "TITLE", []string{"global"})
	if !metadata.MatroskaProjectionUnresolved || !containsDiagnostic(metadata, "mixed or non-track") {
		t.Fatalf("mixed/non-audio projection status: %#v", metadata)
	}
	if len(metadata.Matroska.Tags) != 5 {
		t.Fatalf("native scoped tags were lost: %#v", metadata.Matroska.Tags)
	}
}

func TestMatroskaUnknownTrackUIDRemainsNativeAndUnprojected(t *testing.T) {
	metadata := writeMkvCapture(t,
		[]mkvTestTrack{{uid: 10, trackType: 2}},
		mkvGlobalTag("TITLE", "global"),
		mkvTrackTag(99, mkvSimpleTag("TITLE", "unmatched")),
	)
	assertValues(t, metadata.Tags, "TITLE", []string{"global"})
	if !metadata.MatroskaProjectionUnresolved || !containsDiagnostic(metadata, "unknown track UID") {
		t.Fatalf("unknown track projection status: %#v", metadata)
	}
	if len(metadata.Matroska.Tags) != 2 || metadata.Matroska.Tags[1].Targets.TrackUIDs[0] != 99 {
		t.Fatalf("unknown track native tag was lost: %#v", metadata.Matroska.Tags)
	}
}

func TestMatroskaAllTracksUIDProjectsForSoleAudioTrack(t *testing.T) {
	metadata := writeMkvCapture(t,
		[]mkvTestTrack{{uid: 10, trackType: 2}},
		mkvGlobalTag("TITLE", "global"),
		mkvTrackTag(0, mkvSimpleTag("TITLE", "all tracks")),
	)
	assertValues(t, metadata.Tags, "TITLE", []string{"global", "all tracks"})
	if metadata.MatroskaProjectionUnresolved {
		t.Fatalf("sole-audio all-tracks projection unexpectedly unresolved: %#v", metadata.MatroskaProjectionDiagnostics)
	}
	if len(metadata.Matroska.Tags) != 2 || metadata.Matroska.Tags[1].Targets.TrackUIDs[0] != 0 {
		t.Fatalf("all-tracks native tag was lost: %#v", metadata.Matroska.Tags)
	}
}

func TestReadAACADTSWithAndWithoutID3AndExtension(t *testing.T) {
	tagged := append(id3Fixture(id3Frame("TIT2", []byte{0, 'T', 'a', 'g', 'g', 'e', 'd'})), aacADTSFixture()...)
	for _, test := range []struct {
		name      string
		extension string
		contents  []byte
		wantTitle []string
	}{
		{name: "tagged with extension", extension: ".aac", contents: tagged, wantTitle: []string{"Tagged"}},
		{name: "tagged extensionless", contents: tagged, wantTitle: []string{"Tagged"}},
		{name: "untagged extensionless", contents: aacADTSFixture(), wantTitle: []string{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := writeMetadataFixture(t, test.extension, test.contents)
			capture, err := (Reader{}).Read(context.Background(), path, ReadRequest{})
			if err != nil {
				t.Fatal(err)
			}
			assertValues(t, capture.Tags, taglib.Title, test.wantTitle)
		})
	}
}

func writeMkvCapture(t *testing.T, tracks []mkvTestTrack, tags ...[]byte) Capture {
	t.Helper()
	segment := append(mkvTracks(tracks...), bytes.Join(tags, nil)...)
	path := writeMetadataFixture(t, "", append(mkvElement(0x1a45dfa3, nil), mkvElement(0x18538067, segment)...))
	capture, err := (Reader{}).Read(context.Background(), path, ReadRequest{})
	if err != nil {
		t.Fatal(err)
	}
	return capture
}

type mkvTestTrack struct {
	uid       byte
	trackType byte
}

func mkvTracks(tracks ...mkvTestTrack) []byte {
	var entries []byte
	for _, track := range tracks {
		entries = append(entries, mkvElement(0xae,
			mkvElement(0x73c5, []byte{track.uid}),
			mkvElement(0x83, []byte{track.trackType}),
		)...)
	}
	return mkvElement(0x1654ae6b, entries)
}

func mkvGlobalTag(name, value string) []byte {
	return mkvElement(0x7373, mkvSimpleTag(name, value))
}

func mkvTrackTag(uid byte, simpleTags ...[]byte) []byte {
	targets := mkvElement(0x63c0, mkvElement(0x63c5, []byte{uid}))
	return mkvElement(0x7373, append(targets, bytes.Join(simpleTags, nil)...))
}

func mkvSimpleTag(name, value string) []byte {
	return mkvElement(0x67c8,
		mkvElement(0x45a3, []byte(name)),
		mkvElement(0x4487, []byte(value)),
	)
}

func containsDiagnostic(capture Capture, want string) bool {
	for _, diagnostic := range capture.MatroskaProjectionDiagnostics {
		if strings.Contains(diagnostic, want) {
			return true
		}
	}
	return false
}

func aacADTSFixture() []byte {
	// Two synthetic ADTS frames with a 7-byte header and four opaque payload bytes:
	// content sniffing needs a second header after the first frame to confirm the format.
	frame := []byte{0xff, 0xf1, 0x50, 0x80, 0x01, 0x7f, 0xfc, 0x21, 0x10, 0x04, 0x60}
	return bytes.Repeat(frame, 2)
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
		return taglib.Properties{Format: "flac", BitRate: 192, BitDepth: 0}, nil
	}
	capture, err = reader.Read(context.Background(), path, ReadRequest{Properties: true})
	if err != nil {
		t.Fatal(err)
	}
	if capture.Properties == nil || capture.Properties.BitRateBitsPerSecond != 192000 || capture.Properties.BitDepth != nil {
		t.Fatalf("converted properties = %#v", capture.Properties)
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

func TestReadMalformedZeroLengthSignedMatroskaIntegerReturnsError(t *testing.T) {
	badTrackOffset := mkvElement(0x1654ae6b,
		mkvElement(0xae, mkvElement(0x537f, nil)),
	)
	segment := mkvElement(0x18538067, badTrackOffset)
	path := writeMetadataFixture(t, "", append(mkvElement(0x1a45dfa3, nil), segment...))
	_, err := (Reader{}).Read(context.Background(), path, ReadRequest{})
	if err == nil || err.Error() != "read Matroska tags: matroska parser panicked" {
		t.Fatalf("malformed signed integer error = %v", err)
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
	body := bytes.Join(frames, nil)
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

func mp4Fixture(artists ...string) []byte {
	wrap := func(name string, body []byte) []byte {
		atom := make([]byte, 8+len(body))
		binary.BigEndian.PutUint32(atom[:4], uint32(len(atom)))
		copy(atom[4:8], name)
		copy(atom[8:], body)
		return atom
	}
	var items []byte
	for _, artist := range artists {
		items = append(items, mp4Item(wrap, []byte{0xa9, 'A', 'R', 'T'}, artist)...)
	}
	items = append(items, mp4Item(wrap, []byte{0xa9, 'n', 'a', 'm'}, "fixture-title")...)
	return bytes.Join([][]byte{
		wrap("ftyp", []byte("M4A \x00\x00\x00\x00")),
		wrap("moov", wrap("udta", wrap("meta", append([]byte{0, 0, 0, 0}, wrap("ilst", items)...)))),
	}, nil)
}

func mp4Item(wrap func(string, []byte) []byte, name []byte, value string) []byte {
	data := append([]byte{0, 0, 0, 1, 0, 0, 0, 0}, []byte(value)...)
	return wrap(string(name), wrap("data", data))
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

func mkvElement(id uint32, parts ...[]byte) []byte {
	body := bytes.Join(parts, nil)
	var encodedID []byte
	switch {
	case id > 0xffffff:
		encodedID = []byte{byte(id >> 24), byte(id >> 16), byte(id >> 8), byte(id)}
	case id > 0xffff:
		encodedID = []byte{byte(id >> 16), byte(id >> 8), byte(id)}
	case id > 0xff:
		encodedID = []byte{byte(id >> 8), byte(id)}
	default:
		encodedID = []byte{byte(id)}
	}
	size := uint64(len(body))
	sizeLength := 1
	for size >= (uint64(1)<<(7*sizeLength))-1 && sizeLength < 8 {
		sizeLength++
	}
	encodedSize := size | uint64(1)<<(7*sizeLength)
	for shift := (sizeLength - 1) * 8; shift >= 0; shift -= 8 {
		encodedID = append(encodedID, byte(encodedSize>>shift))
	}
	return append(encodedID, body...)
}
