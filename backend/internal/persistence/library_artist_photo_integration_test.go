//go:build integration

package persistence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func libraryArtistPhotoTestPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	value := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			value.Set(x, y, color.RGBA{R: 10, G: 20, B: 30, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, value); err != nil {
		t.Fatalf("encode artist photo: %v", err)
	}
	return encoded.Bytes()
}

func TestLibraryArtistPhotoStoragePostgreSQL(t *testing.T) {
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := NewLibraryRepository(db)

	mbid := uuid.NewString()
	data := libraryArtistPhotoTestPNG(t, 8, 8)
	digest := sha256.Sum256(data)
	licenseURL := "https://creativecommons.org/licenses/by-sa/4.0/"
	record := LibraryArtistPhotoRecord{
		ConfigIdentity: "config-identity-1", ArtistMBID: uuid.MustParse(mbid),
		ImageURL:  "https://upload.wikimedia.org/wikipedia/commons/thumb/a/ab/Photo.png/330px-Photo.png",
		SourceURL: "https://commons.wikimedia.org/wiki/File:Photo.png", Author: "Photographer",
		License: "CC BY-SA 4.0", LicenseURL: &licenseURL,
		Data: data, MIME: "image/png", SHA256: digest[:], SizeBytes: int64(len(data)),
		Width: 8, Height: 8, FetchedAt: time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC),
	}
	if err := repository.SaveLibraryArtistPhoto(ctx, record); err != nil {
		t.Fatalf("save: %v", err)
	}
	stored, err := repository.ReadLibraryArtistPhoto(ctx, record.ConfigIdentity, mbid)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if stored.ArtistMBID != record.ArtistMBID || stored.ImageURL != record.ImageURL ||
		stored.SourceURL != record.SourceURL || stored.Author != record.Author ||
		stored.License != record.License || stored.LicenseURL == nil || *stored.LicenseURL != licenseURL ||
		!bytes.Equal(stored.Data, data) || stored.MIME != "image/png" ||
		!bytes.Equal(stored.SHA256, digest[:]) || stored.SizeBytes != int64(len(data)) ||
		stored.Width != 8 || stored.Height != 8 || stored.FetchedAt.IsZero() {
		t.Fatalf("stored record = %#v", stored)
	}

	replacement := libraryArtistPhotoTestPNG(t, 4, 6)
	replacementDigest := sha256.Sum256(replacement)
	record.Data = replacement
	record.MIME = "image/png"
	record.SHA256 = replacementDigest[:]
	record.SizeBytes = int64(len(replacement))
	record.Width, record.Height = 4, 6
	record.Author = "Another Photographer"
	if err := repository.SaveLibraryArtistPhoto(ctx, record); err != nil {
		t.Fatalf("replace: %v", err)
	}
	stored, err = repository.ReadLibraryArtistPhoto(ctx, record.ConfigIdentity, mbid)
	if err != nil {
		t.Fatalf("read replacement: %v", err)
	}
	if stored.Author != "Another Photographer" || !bytes.Equal(stored.Data, replacement) ||
		stored.Width != 4 || stored.Height != 6 {
		t.Fatalf("replacement record = %#v", stored)
	}

	otherIdentity, err := repository.ReadLibraryArtistPhoto(ctx, "config-identity-2", mbid)
	if !errors.Is(err, ErrLibraryArtistPhotoNotFound) {
		t.Fatalf("other identity record = %#v, %v", otherIdentity, err)
	}
	otherMBID, err := repository.ReadLibraryArtistPhoto(ctx, record.ConfigIdentity, uuid.NewString())
	if !errors.Is(err, ErrLibraryArtistPhotoNotFound) {
		t.Fatalf("other MBID record = %#v, %v", otherMBID, err)
	}

	for _, test := range []struct {
		name  string
		apply func(*LibraryArtistPhotoRecord)
	}{
		{name: "unverified image URL", apply: func(record *LibraryArtistPhotoRecord) {
			record.ImageURL = "https://example.invalid/photo.png"
		}},
		{name: "unverified source URL", apply: func(record *LibraryArtistPhotoRecord) {
			record.SourceURL = "https://example.invalid/photo"
		}},
		{name: "unsupported MIME", apply: func(record *LibraryArtistPhotoRecord) {
			record.MIME = "text/html"
		}},
		{name: "oversized dimensions", apply: func(record *LibraryArtistPhotoRecord) {
			record.Width = MaxLibraryArtistPhotoDimension + 1
		}},
		{name: "empty image", apply: func(record *LibraryArtistPhotoRecord) {
			record.Data = nil
		}},
		{name: "missing license", apply: func(record *LibraryArtistPhotoRecord) {
			record.License = " "
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalid := record
			test.apply(&invalid)
			if err := repository.SaveLibraryArtistPhoto(ctx, invalid); err == nil {
				t.Fatal("save succeeded, want an error")
			}
		})
	}
}
