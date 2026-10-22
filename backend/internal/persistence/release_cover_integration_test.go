//go:build integration

package persistence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestReleaseCoverAuthorityCASAndManualPendingIsolationPostgreSQL(t *testing.T) {
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	releaseID := uuid.New()
	if _, err := db.NewRaw(`INSERT INTO local_release(id) VALUES (?)`, releaseID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	repository := NewLibraryRepository(db)
	initial, err := repository.ReadReleaseCover(ctx, releaseID)
	if err != nil || initial.Revision != 1 || initial.Mode != "automatic" || initial.SelectedGeneration != nil {
		t.Fatalf("initial cover authority = %#v, %v", initial, err)
	}

	firstGeneration := uuid.New()
	manualBytes := releaseCoverPNG(t)
	pending, err := repository.SetPendingReleaseCover(ctx, releaseID, initial.Revision, firstGeneration, manualBytes, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if pending.Revision != 2 || pending.Mode != "manual" || pending.SelectedGeneration != nil || pending.PendingGeneration == nil || *pending.PendingGeneration != firstGeneration {
		t.Fatalf("manual pending selection changed effective selection: %#v", pending)
	}
	if _, err := repository.ApplyPendingReleaseCover(ctx, releaseID, initial.Revision, firstGeneration); !errors.Is(err, ErrReleaseCoverConflict) {
		t.Fatalf("stale apply error = %v, want conflict", err)
	}
	selected, err := repository.ApplyPendingReleaseCover(ctx, releaseID, pending.Revision, firstGeneration)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Revision != 3 || selected.Mode != "manual" || selected.PendingGeneration != nil || selected.SelectedGeneration == nil || *selected.SelectedGeneration != firstGeneration || !bytes.Equal(selected.SelectedBytes, manualBytes) {
		t.Fatalf("applied manual selection = %#v", selected)
	}
	if _, err := repository.SetPendingReleaseCover(ctx, releaseID, 2, uuid.New(), releaseCoverPNG(t), "image/png"); !errors.Is(err, ErrReleaseCoverConflict) {
		t.Fatalf("stale manual choice error = %v, want conflict", err)
	}

	automatic, err := repository.ReturnReleaseCoverToAutomatic(ctx, releaseID, selected.Revision)
	if err != nil || automatic.Mode != "automatic" || automatic.Revision != 4 || automatic.SelectedGeneration == nil {
		t.Fatalf("return to automatic = %#v, %v", automatic, err)
	}
	owner := uuid.New()
	claimed, err := repository.ClaimReleaseCoverAcquisition(ctx, releaseID, automatic.Revision, owner)
	if err != nil || claimed.AcquisitionOwner == nil || *claimed.AcquisitionOwner != owner {
		t.Fatalf("acquisition claim = %#v, %v", claimed, err)
	}
	if _, err := repository.ClaimReleaseCoverAcquisition(ctx, releaseID, claimed.Revision, uuid.New()); !errors.Is(err, ErrReleaseCoverConflict) {
		t.Fatalf("second acquisition claim error = %v, want conflict", err)
	}
	failed, err := repository.FailReleaseCoverAcquisition(ctx, releaseID, claimed.Revision, owner, "provider unavailable")
	if err != nil || failed.AcquisitionOwner != nil || failed.Warning == nil || *failed.Warning != "provider unavailable" || !bytes.Equal(failed.SelectedBytes, manualBytes) {
		t.Fatalf("failed acquisition did not retain current selection/warning: %#v, %v", failed, err)
	}
	if _, err := repository.FinishReleaseCoverAcquisition(ctx, releaseID, failed.Revision, owner, releaseCoverPNG(t), "image/png"); !errors.Is(err, ErrReleaseCoverConflict) {
		t.Fatalf("late acquisition completion error = %v, want conflict", err)
	}
	secondOwner := uuid.New()
	secondClaim, err := repository.ClaimReleaseCoverAcquisition(ctx, releaseID, failed.Revision, secondOwner)
	if err != nil {
		t.Fatal(err)
	}
	manualPending, err := repository.SetPendingReleaseCover(ctx, releaseID, secondClaim.Revision, uuid.New(), releaseCoverPNG(t), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if manualPending.AcquisitionOwner != nil || manualPending.Mode != "manual" {
		t.Fatalf("manual choice did not revoke the automatic acquisition owner: %#v", manualPending)
	}
	if _, err := repository.FinishReleaseCoverAcquisition(ctx, releaseID, manualPending.Revision, secondOwner, releaseCoverPNG(t), "image/png"); !errors.Is(err, ErrReleaseCoverConflict) {
		t.Fatalf("late acquisition completion after manual choice = %v, want conflict", err)
	}
	if _, err := repository.FailReleaseCoverAcquisition(ctx, releaseID, manualPending.Revision, secondOwner, "late failure"); !errors.Is(err, ErrReleaseCoverConflict) {
		t.Fatalf("late acquisition failure after manual choice = %v, want conflict", err)
	}
	unchanged, err := repository.ReadReleaseCover(ctx, releaseID)
	if err != nil || unchanged.Revision != manualPending.Revision || unchanged.Mode != "manual" || unchanged.AcquisitionOwner != nil || unchanged.Warning == nil || *unchanged.Warning != "provider unavailable" {
		t.Fatalf("late acquisition mutated current manual authority: %#v, %v", unchanged, err)
	}
	applied, err := repository.ApplyPendingReleaseCover(ctx, releaseID, manualPending.Revision, *manualPending.PendingGeneration)
	if err != nil || applied.Warning != nil || applied.PendingGeneration != nil || applied.SelectedGeneration == nil || *applied.SelectedGeneration != *manualPending.PendingGeneration || !bytes.Equal(applied.SelectedBytes, manualPending.PendingBytes) {
		t.Fatalf("manual apply did not resolve obsolete acquisition warning: %#v, %v", applied, err)
	}
	if _, err := repository.FailReleaseCoverAcquisition(ctx, releaseID, applied.Revision, secondOwner, "late failure"); !errors.Is(err, ErrReleaseCoverConflict) {
		t.Fatalf("late failure after manual apply = %v, want conflict", err)
	}
	if _, err := repository.FinishReleaseCoverAcquisition(ctx, releaseID, applied.Revision, secondOwner, releaseCoverPNG(t), "image/png"); !errors.Is(err, ErrReleaseCoverConflict) {
		t.Fatalf("late completion after manual apply = %v, want conflict", err)
	}
	resolved, err := repository.ReadReleaseCover(ctx, releaseID)
	if err != nil || resolved.Warning != nil || resolved.Revision != applied.Revision {
		t.Fatalf("late acquisition restored obsolete warning: %#v, %v", resolved, err)
	}
}

func releaseCoverPNG(t *testing.T) []byte {
	t.Helper()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func TestReleaseCoverImageValidationRequiresDecodableMatchingMIME(t *testing.T) {
	var jpegData bytes.Buffer
	jpegImage := image.NewRGBA(image.Rect(0, 0, 1, 1))
	jpegImage.Set(0, 0, color.White)
	if err := jpeg.Encode(&jpegData, jpegImage, nil); err != nil {
		t.Fatal(err)
	}
	pngData := releaseCoverPNG(t)
	for _, test := range []struct {
		name  string
		data  []byte
		mime  string
		valid bool
	}{
		{name: "valid png", data: pngData, mime: "image/png", valid: true},
		{name: "valid jpeg", data: jpegData.Bytes(), mime: "image/jpeg", valid: true},
		{name: "text declared png", data: []byte("not an image"), mime: "image/png"},
		{name: "truncated png", data: pngData[:len(pngData)-5], mime: "image/png"},
		{name: "png declared jpeg", data: pngData, mime: "image/jpeg"},
		{name: "jpeg declared png", data: jpegData.Bytes(), mime: "image/png"},
		{name: "unsupported mime", data: pngData, mime: "image/gif"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateReleaseCoverImage(uuid.New(), test.data, test.mime)
			if (err == nil) != test.valid {
				t.Fatalf("validateReleaseCoverImage() error = %v, valid = %t", err, test.valid)
			}
		})
	}
}

func TestReleaseCoverAuthorityRejectsPartialImageTuplesPostgreSQL(t *testing.T) {
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	data := releaseCoverPNG(t)
	digest := sha256.Sum256(data)
	columns := []string{"generation", "bytes", "mime", "sha256", "size"}
	values := []any{uuid.New(), data, "image/png", digest[:], len(data)}

	insert := func(t *testing.T, asset string, omit int, mutation func([]any)) error {
		t.Helper()
		releaseID := uuid.New()
		if _, err := db.NewRaw(`INSERT INTO local_release(id) VALUES (?)`, releaseID).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		fields := []string{"release_id"}
		args := []any{releaseID}
		for index, column := range columns {
			if index == omit {
				continue
			}
			fields = append(fields, asset+"_"+column)
			args = append(args, values[index])
		}
		if asset == "pending" {
			fields = append(fields, "mode")
			args = append(args, "manual")
		}
		if mutation != nil {
			mutation(args)
		}
		placeholders := make([]string, len(args))
		for index := range placeholders {
			placeholders[index] = "?"
		}
		query := fmt.Sprintf("INSERT INTO release_publication_cover(%s) VALUES (%s)", strings.Join(fields, ","), strings.Join(placeholders, ","))
		_, err := db.NewRaw(query, args...).Exec(ctx)
		return err
	}

	for _, asset := range []string{"selected", "pending"} {
		if err := insert(t, asset, -1, nil); err != nil {
			t.Fatalf("valid %s cover tuple rejected: %v", asset, err)
		}
		for omitted, column := range columns {
			if err := insert(t, asset, omitted, nil); err == nil {
				t.Errorf("%s tuple with %s omitted was accepted", asset, column)
			}
		}
		if err := insert(t, asset, -1, func(args []any) {
			index := len(args) - 2
			if asset == "pending" {
				index = len(args) - 3
			}
			badDigest := append([]byte(nil), digest[:]...)
			badDigest[0] ^= 0xff
			args[index] = badDigest
		}); err == nil {
			t.Errorf("%s tuple with an incorrect same-length SHA-256 was accepted", asset)
		}
		if err := insert(t, asset, -1, func(args []any) { args[1] = uuid.Nil }); err == nil {
			t.Errorf("%s tuple with a zero generation UUID was accepted", asset)
		}
	}
	if err := insert(t, "pending", -1, func(args []any) { args[len(args)-1] = "automatic" }); err == nil {
		t.Fatal("automatic mode with pending manual choice was accepted")
	}
	releaseID := uuid.New()
	if _, err := db.NewRaw(`INSERT INTO local_release(id) VALUES (?)`, releaseID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO release_publication_cover(release_id,mode,acquisition_owner) VALUES (?,'manual',?)`, releaseID, uuid.New()).Exec(ctx); err == nil {
		t.Fatal("manual mode with active automatic acquisition owner was accepted")
	}
}
