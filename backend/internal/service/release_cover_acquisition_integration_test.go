//go:build integration

package service

import (
	"bytes"
	"context"
	"errors"
	"image/color"
	"io"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/coverart"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

type acquisitionCandidateFixture struct {
	snapshot persistence.ReleaseCoverCandidates
	calls    int
}

func (fixture *acquisitionCandidateFixture) ReadReleaseCoverCandidates(context.Context, uuid.UUID) (*persistence.ReleaseCoverCandidates, error) {
	fixture.calls++
	return &fixture.snapshot, nil
}

type acquisitionProviderFixture struct {
	run func(io.Writer) (coverart.Result, error)
}

func (fixture acquisitionProviderFixture) DownloadFront(_ context.Context, _ string, out io.Writer) (coverart.Result, error) {
	return fixture.run(out)
}

func TestInitialCoverAcquisitionUsesSavedBytesAndProtectsManualSavePostgreSQL(t *testing.T) {
	for _, scenario := range []string{"saved_local", "provider_failure", "provider_truncated", "provider_oversized", "manual_race", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			db := testpostgres.OpenMigrated(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			releaseID := uuid.New()
			if _, err := db.ExecContext(ctx, `INSERT INTO local_release(id,title) VALUES (?,'cover acquisition')`, releaseID); err != nil {
				t.Fatal(err)
			}
			repository := persistence.NewLibraryRepository(db)
			image := simpleCoverPNG(t, color.RGBA{R: 180, A: 255})
			reader := &acquisitionCandidateFixture{snapshot: persistence.ReleaseCoverCandidates{Candidates: []persistence.ReleaseCoverCandidate{{ID: uuid.New(), WinningResultID: uuid.New(), MIME: "image/png", Data: image}}}}
			mbid := uuid.NewString()
			if scenario != "saved_local" {
				reader.snapshot.MusicBrainzMBID = &mbid
			}
			pendingGeneration := uuid.New()
			provider := acquisitionProviderFixture{run: func(out io.Writer) (coverart.Result, error) {
				if scenario == "provider_failure" {
					_, _ = out.Write([]byte("partial"))
					return coverart.Result{}, errors.New("provider failure")
				}
				if scenario == "provider_truncated" {
					_, err := out.Write(image[:len(image)/2])
					return coverart.Result{Found: true, MIME: "image/png"}, err
				}
				if scenario == "provider_oversized" {
					_, err := out.Write(oversizedBaselineCoverPNG(t))
					return coverart.Result{Found: true, MIME: "image/png"}, err
				}
				if scenario == "canceled" {
					cancel()
					return coverart.Result{}, context.Canceled
				}
				if _, err := repository.SetPendingReleaseCover(ctx, releaseID, 1, pendingGeneration, image, "image/png"); err != nil {
					t.Fatal(err)
				}
				_, err := out.Write(image)
				return coverart.Result{Found: true, MIME: "image/png"}, err
			}}
			acquisition := NewReleaseCoverAcquisition(repository, NewReleaseCoverResolver(reader, provider))
			err := acquisition.Ensure(ctx, releaseID)
			if scenario == "canceled" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled acquisition: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			cover, err := repository.ReadReleaseCover(ctx, releaseID)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "saved_local":
				if cover.SelectedGeneration == nil || !bytes.Equal(cover.SelectedBytes, image) || cover.Warning != nil {
					t.Fatalf("saved local cover not acquired: %+v", cover)
				}
			case "provider_failure", "provider_truncated", "provider_oversized":
				if cover.SelectedGeneration != nil || cover.Warning == nil {
					t.Fatalf("optional provider failure not recorded: %+v", cover)
				}
			case "manual_race":
				if cover.Mode != "manual" || cover.SelectedGeneration != nil || cover.PendingGeneration == nil || *cover.PendingGeneration != pendingGeneration {
					t.Fatalf("late acquisition overwrote manual choice: %+v", cover)
				}
				if err := acquisition.Ensure(ctx, releaseID); err != nil {
					t.Fatal(err)
				}
				if reader.calls != 1 {
					t.Fatal("manual cover allowed automatic acquisition")
				}
			}
		})
	}
}
