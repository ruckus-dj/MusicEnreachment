package service

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/coverart"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
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

// acquisitionRepositoryFixture mocks only the persistence boundary used by
// Ensure; the service and resolver both execute their production behavior.
type acquisitionRepositoryFixture struct {
	cover            persistence.ReleaseCover
	recordedRevision int64
	recordedBytes    []byte
	recordedMIME     string
	recordedWarning  *string
	recordCalls      int
	recordErr        error
}

func oversizedAcquisitionCoverPNG(t *testing.T) []byte {
	t.Helper()
	imageData := image.NewRGBA(image.Rect(0, 0, 10001, 1))
	for x := 0; x < 10001; x++ {
		imageData.SetRGBA(x, 0, color.RGBA{R: 255, A: 255})
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, imageData); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func (fixture *acquisitionRepositoryFixture) ReadReleaseCover(context.Context, uuid.UUID) (persistence.ReleaseCover, error) {
	return fixture.cover, nil
}

func (fixture *acquisitionRepositoryFixture) RecordAutomaticReleaseCoverSelection(_ context.Context, _ uuid.UUID, expected int64, data []byte, mime string, warning *string) (persistence.ReleaseCover, error) {
	fixture.recordCalls++
	fixture.recordedRevision = expected
	fixture.recordedBytes = append([]byte(nil), data...)
	fixture.recordedMIME = mime
	if warning != nil {
		value := *warning
		fixture.recordedWarning = &value
	}
	if fixture.recordErr != nil {
		return persistence.ReleaseCover{}, fixture.recordErr
	}
	fixture.cover.Revision++
	fixture.cover.Warning = warning
	if len(data) > 0 {
		fixture.cover.SelectedBytes = append([]byte(nil), data...)
		fixture.cover.SelectedMIME = &mime
		generation := uuid.New()
		fixture.cover.SelectedGeneration = &generation
	}
	return fixture.cover, nil
}

func TestInitialCoverAcquisitionUsesSavedBytesAndProtectsManualSave(t *testing.T) {
	for _, scenario := range []string{"saved_local", "provider_failure", "provider_truncated", "provider_oversized", "manual_race", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			releaseID := uuid.New()
			image := releaseCoverPNG(t)
			reader := &acquisitionCandidateFixture{snapshot: persistence.ReleaseCoverCandidates{Candidates: []persistence.ReleaseCoverCandidate{{ID: uuid.New(), WinningResultID: uuid.New(), MIME: "image/png", Data: image}}}}
			mbid := uuid.NewString()
			if scenario != "saved_local" {
				reader.snapshot.MusicBrainzMBID = &mbid
			}
			repository := &acquisitionRepositoryFixture{cover: persistence.ReleaseCover{ReleaseID: releaseID, Revision: 7, Mode: "automatic"}}
			providerCalls := 0
			provider := acquisitionProviderFixture{run: func(out io.Writer) (coverart.Result, error) {
				providerCalls++
				switch scenario {
				case "provider_failure":
					_, _ = out.Write([]byte("partial"))
					return coverart.Result{}, errors.New("provider failure")
				case "provider_truncated":
					_, err := out.Write(image[:len(image)/2])
					return coverart.Result{Found: true, MIME: "image/png"}, err
				case "provider_oversized":
					_, err := out.Write(oversizedAcquisitionCoverPNG(t))
					return coverart.Result{Found: true, MIME: "image/png"}, err
				case "canceled":
					cancel()
					return coverart.Result{}, context.Canceled
				case "manual_race":
					repository.cover.Mode = "manual"
					repository.cover.Revision++
					generation := uuid.New()
					repository.cover.PendingGeneration = &generation
					repository.recordErr = persistence.ErrReleaseCoverConflict
				}
				_, err := out.Write(image)
				return coverart.Result{Found: true, MIME: "image/png"}, err
			}}
			acquisition := NewReleaseCoverAcquisition(repository, NewReleaseCoverResolver(reader, provider))
			err := acquisition.Ensure(ctx, releaseID)
			if scenario == "canceled" {
				if !errors.Is(err, context.Canceled) || repository.recordCalls != 0 {
					t.Fatalf("canceled acquisition: err=%v records=%d", err, repository.recordCalls)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if repository.recordCalls != 1 || repository.recordedRevision != 7 {
				t.Fatalf("record calls/revision = %d/%d", repository.recordCalls, repository.recordedRevision)
			}
			switch scenario {
			case "saved_local":
				if !bytes.Equal(repository.recordedBytes, image) || repository.recordedMIME != "image/png" || repository.recordedWarning != nil || providerCalls != 0 {
					t.Fatalf("saved local result = bytes %d MIME %q warning %v provider calls %d", len(repository.recordedBytes), repository.recordedMIME, repository.recordedWarning, providerCalls)
				}
			case "provider_failure", "provider_truncated", "provider_oversized":
				if len(repository.recordedBytes) != 0 || repository.recordedWarning == nil || *repository.recordedWarning == "" || providerCalls != 1 {
					t.Fatalf("provider failure result bytes=%d warning=%v calls=%d", len(repository.recordedBytes), repository.recordedWarning, providerCalls)
				}
			case "manual_race":
				if len(repository.recordedBytes) == 0 || repository.cover.Mode != "manual" || repository.cover.PendingGeneration == nil {
					t.Fatalf("manual save race lost authority: %+v", repository.cover)
				}
				if err := acquisition.Ensure(ctx, releaseID); err != nil {
					t.Fatal(err)
				}
				if reader.calls != 1 || providerCalls != 1 || repository.recordCalls != 1 {
					t.Fatalf("manual authority did not suppress retry: readers=%d providers=%d records=%d", reader.calls, providerCalls, repository.recordCalls)
				}
			}
		})
	}
}
