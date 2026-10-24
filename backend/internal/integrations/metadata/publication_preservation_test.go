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

func TestArtworkSnapshotRestoresEveryOpaqueImageAndDescriptor(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("testdata", "eg.flac"))
	if err != nil {
		t.Fatal(err)
	}
	path := writeMetadataFixture(t, ".flac", contents)
	large := image.NewRGBA(image.Rect(0, 0, 10001, 1))
	large.Set(0, 0, color.RGBA{R: 255, A: 255})
	var oversized bytes.Buffer
	if err := png.Encode(&oversized, large); err != nil {
		t.Fatal(err)
	}
	images := [][]byte{generatedClearArtworkPNG(t, color.RGBA{G: 180, A: 255}), oversized.Bytes()}
	if err := taglib.WriteImageOptions(path, images[0], 0, "Front Cover", "front", "image/png"); err != nil {
		t.Fatal(err)
	}
	if err := taglib.WriteImageOptions(path, images[1], 1, "Back Cover", "oversized legacy", "image/png"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadArtworkSnapshot(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Images) != 2 || snapshot.Images[0].Type != "Front Cover" || snapshot.Images[1].Type != "Back Cover" ||
		snapshot.Images[1].Description != "oversized legacy" || !bytes.Equal(snapshot.Images[1].Data, images[1]) {
		t.Fatalf("captured artwork = %+v", snapshot.ArtworkInventory())
	}
	if got := snapshot.ArtworkInventory()[1]; got.Size != len(images[1]) {
		t.Fatalf("oversized inventory = %+v", got)
	}
	if err := taglib.WriteImageOptions(path, generatedClearArtworkPNG(t, color.RGBA{B: 220, A: 255}), 0, "", "", "image/png"); err != nil {
		t.Fatal(err)
	}
	if err := RestoreArtworkSnapshot(context.Background(), path, snapshot); err != nil {
		t.Fatal(err)
	}
	actual, err := ReadArtworkSnapshot(context.Background(), path)
	if err != nil || !equalArtworkSnapshots(snapshot, actual) {
		t.Fatalf("restored artwork mismatch: error=%v, inventory=%+v", err, actual.ArtworkInventory())
	}
}

func TestRestoreArtworkSnapshotClearsAllCandidateImagesForEmptySnapshot(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("testdata", "eg.flac"))
	if err != nil {
		t.Fatal(err)
	}
	path := writeMetadataFixture(t, ".flac", contents)
	if err := taglib.WriteImage(path, generatedClearArtworkPNG(t, color.RGBA{R: 120, A: 255})); err != nil {
		t.Fatal(err)
	}
	if err := RestoreArtworkSnapshot(context.Background(), path, ArtworkSnapshot{}); err != nil {
		t.Fatal(err)
	}
	actual, err := ReadArtworkSnapshot(context.Background(), path)
	if err != nil || len(actual.Images) != 0 {
		t.Fatalf("artwork after empty restore = %+v, error=%v", actual.ArtworkInventory(), err)
	}
}
