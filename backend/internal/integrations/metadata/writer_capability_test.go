package metadata

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"go.senan.xyz/taglib"
)

// These are round-trip capability checks, not mocks: TagLib must write the
// copied real container and a fresh TagLib read must observe the values.
func TestTagLibWriteTagsRoundTripsKnownStandardFields(t *testing.T) {
	for _, ext := range []string{".flac", ".mp3", ".m4a", ".mka"} {
		t.Run(ext, func(t *testing.T) {
			fixture := "eg" + ext
			contents, err := os.ReadFile(filepath.Join("testdata", fixture))
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
				taglib.Lyrics:                    {"[00:01.00]First synced line\n[00:02.50]Second synced line\n"},
			}
			if err := taglib.WriteTags(path, want, taglib.Clear); err != nil {
				t.Fatalf("WriteTags: %v", err)
			}
			got, err := taglib.ReadTags(path)
			if err != nil {
				t.Fatalf("fresh ReadTags: %v", err)
			}
			for key, values := range want {
				assertValues(t, got, key, values)
			}

			imageBytes := generatedPNG(t)
			if err := taglib.WriteImage(path, imageBytes); err != nil {
				t.Fatalf("WriteImage: %v", err)
			}
			gotImage, err := taglib.ReadImage(path)
			if err != nil {
				t.Fatalf("fresh ReadImage: %v", err)
			}
			if !bytes.Equal(gotImage, imageBytes) {
				t.Fatal("fresh ReadImage did not return the written cover image bytes")
			}
		})
	}
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
