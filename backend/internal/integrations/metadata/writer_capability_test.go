package metadata

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"go.senan.xyz/taglib"
)

// These are round-trip capability checks, not mocks: the production metadata
// writer must mutate the copied real container and an independent production
// reader must observe the requested canonical values.
func TestMetadataWriterRoundTripsKnownStandardFields(t *testing.T) {
	for _, ext := range []string{".flac", ".mp3", ".m4a", ".mka"} {
		t.Run(ext, func(t *testing.T) {
			contents, err := os.ReadFile(filepath.Join("testdata", "eg"+ext))
			if err != nil {
				t.Fatal(err)
			}
			path := writeMetadataFixture(t, ext, contents)
			want := map[string][]string{
				taglib.Title:                     {"D09 title"},
				taglib.Album:                     {"D09 album"},
				taglib.Artist:                    {"Artist One", "Artist Two"},
				taglib.Artists:                   {"Artist One", "Artist Two"},
				taglib.AlbumArtist:               {"Album Artist One", "Album Artist Two"},
				"ALBUMARTISTS":                   {"Album Artist One", "Album Artist Two"},
				taglib.MusicBrainzArtistID:       {"artist-mbid-one", "artist-mbid-two"},
				taglib.MusicBrainzAlbumArtistID:  {"albumartist-mbid-one", "albumartist-mbid-two"},
				taglib.MusicBrainzAlbumID:        {"album-mbid"},
				taglib.MusicBrainzReleaseGroupID: {"releasegroup-mbid"},
				taglib.MusicBrainzTrackID:        {"recording-mbid"},
				taglib.MusicBrainzReleaseTrackID: {"releasetrack-mbid"},
				taglib.ReleaseType:               {"album", "compilation"},
				"ITUNESADVISORY":                 {"1"},
				taglib.Barcode:                   {"0123456789012"},
				taglib.ReleaseCountry:            {"XW"},
				taglib.ReleaseStatus:             {"non-official"},
				"COMPOSER":                       {"Composer One 音楽", "Composer, Two"},
				"LYRICIST":                       {"Lyricist One 作詞者", "Lyricist, Two"},
				"CONDUCTOR":                      {"Conductor One", "Conductor, Two"},
				"ARRANGER":                       {"Arranger One", "Arranger, Two"},
				"ENGINEER":                       {"Engineer One", "Engineer, Two"},
				"PRODUCER":                       {"Producer One", "Producer, Two"},
				"REMIXER":                        {"Remixer One", "Remixer, Two"},
				"DJMIXER":                        {"DJ Mixer One", "DJ Mixer, Two"},
				"MIXER":                          {"Mixer One", "Mixer, Two"},
				taglib.Lyrics:                    {"[00:01.00]First synced line\n[00:02.50]Second synced line\n"},
			}
			// Contributor names deliberately include commas, Unicode, and ordered
			// multiple values. A lossy TIPL parse is a capability failure, not a
			// reason to rewrite requested values. Duplicate-value behavior is
			// covered separately below so a container's de-duplication is visible.
			imageBytes := generatedPNG(t)
			result, err := (Writer{}).Write(context.Background(), path, want, &Cover{Data: imageBytes, MIME: "image/png"})
			if err != nil {
				t.Fatalf("Write: %v", err)
			}
			for key, values := range want {
				assertValues(t, result.Tags, key, values)
			}
			fresh, err := (Reader{}).Read(context.Background(), path, ReadRequest{CoverInspection: true})
			if err != nil {
				t.Fatalf("fresh Read: %v", err)
			}
			for key, values := range want {
				assertValues(t, fresh.Tags, key, values)
			}
			if result.Cover == nil || result.Cover.Error != "" || !bytes.Equal(result.Cover.Data, imageBytes) {
				t.Fatalf("write cover observation = %#v", result.Cover)
			}
			if fresh.CoverInspection == nil || !fresh.CoverInspection.Observed || fresh.CoverInspection.Cover == nil {
				t.Fatalf("fresh cover inspection = %#v", fresh.CoverInspection)
			}
			if !bytes.Equal(fresh.CoverInspection.Cover.Data, imageBytes) {
				t.Fatal("fresh cover inspection did not return the written cover image bytes")
			}
		})
	}
}

func TestMetadataWriterRoundTripsDuplicateContributorValues(t *testing.T) {
	for _, ext := range []string{".flac", ".mp3", ".m4a", ".mka"} {
		t.Run(ext, func(t *testing.T) {
			contents, err := os.ReadFile(filepath.Join("testdata", "eg"+ext))
			if err != nil {
				t.Fatal(err)
			}
			path := writeMetadataFixture(t, ext, contents)
			want := map[string][]string{
				"COMPOSER":  {"Same Contributor", "Same Contributor"},
				"LYRICIST":  {"Same Contributor", "Same Contributor"},
				"CONDUCTOR": {"Same Contributor", "Same Contributor"},
				"ARRANGER":  {"Same Contributor", "Same Contributor"},
				"ENGINEER":  {"Same Contributor", "Same Contributor"},
				"PRODUCER":  {"Same Contributor", "Same Contributor"},
				"REMIXER":   {"Same Contributor", "Same Contributor"},
				"DJMIXER":   {"Same Contributor", "Same Contributor"},
				"MIXER":     {"Same Contributor", "Same Contributor"},
			}
			result, err := (Writer{}).Write(context.Background(), path, want, nil)
			if err != nil {
				t.Fatalf("Write: %v", err)
			}
			for key, values := range want {
				assertValues(t, result.Tags, key, values)
			}
			fresh, err := (Reader{}).Read(context.Background(), path, ReadRequest{})
			if err != nil {
				t.Fatalf("fresh Read: %v", err)
			}
			for key, values := range want {
				assertValues(t, fresh.Tags, key, values)
			}
		})
	}
}

// TestTagLibDirectMPEGProducerCommaIsAnUpstreamLimitation records the TagLib
// behavior that the production writer adapter exists to avoid. TagLib's
// normalized MPEG writer serializes PRODUCER as a comma-joined TIPL value, so a
// direct fresh TagLib read splits "Producer, Two" into separate values. This
// regression documents why Writer.Write routes canonical contributor roles
// through native aliases; the production round-trip matrix above is the hard
// contract, not this upstream serialization.
func TestTagLibDirectMPEGProducerCommaIsAnUpstreamLimitation(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("testdata", "eg.mp3"))
	if err != nil {
		t.Fatal(err)
	}
	path := writeMetadataFixture(t, ".mp3", contents)
	if err := taglib.WriteTags(path, map[string][]string{"PRODUCER": {"Producer One", "Producer, Two"}}, taglib.Clear); err != nil {
		t.Fatalf("direct WriteTags: %v", err)
	}
	got, err := taglib.ReadTags(path)
	if err != nil {
		t.Fatalf("direct fresh ReadTags: %v", err)
	}
	assertValues(t, got, "PRODUCER", []string{"Producer One", "Producer", " Two"})
}

func generatedPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	img.Set(1, 0, color.RGBA{G: 255, A: 255})
	img.Set(0, 1, color.RGBA{B: 255, A: 255})
	img.Set(1, 1, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatalf("encode test cover: %v", err)
	}
	return encoded.Bytes()
}
