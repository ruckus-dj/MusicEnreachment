package service

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/coverart"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type acquisitionCandidateFixture struct {
	mu       sync.Mutex
	snapshot persistence.ReleaseCoverCandidates
	err      error
	calls    int
}

func (fixture *acquisitionCandidateFixture) ReadReleaseCoverCandidates(context.Context, uuid.UUID) (*persistence.ReleaseCoverCandidates, error) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	fixture.calls++
	snapshot := fixture.snapshot
	snapshot.Candidates = append([]persistence.ReleaseCoverCandidate(nil), fixture.snapshot.Candidates...)
	return &snapshot, fixture.err
}

func (fixture *acquisitionCandidateFixture) callCount() int {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	return fixture.calls
}

func (fixture *acquisitionCandidateFixture) setSnapshot(snapshot persistence.ReleaseCoverCandidates) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	fixture.snapshot = snapshot
}

type acquisitionProviderFixture struct {
	mu    sync.Mutex
	calls int
	run   func(context.Context, io.Writer) (coverart.Result, error)
}

func (fixture *acquisitionProviderFixture) DownloadFront(ctx context.Context, _ string, out io.Writer) (coverart.Result, error) {
	fixture.mu.Lock()
	fixture.calls++
	run := fixture.run
	fixture.mu.Unlock()
	return run(ctx, out)
}

func (fixture *acquisitionProviderFixture) callCount() int {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	return fixture.calls
}

// acquisitionRepositoryFixture mocks only the persistence boundary used by
// Ensure; the service and resolver both execute their production behavior.
type acquisitionRepositoryFixture struct {
	mu      sync.Mutex
	cover   persistence.ReleaseCover
	readErr error

	linkedMBID string

	recordErr        error
	recordCalls      int
	recordedRevision int64
	recordedBytes    []byte
	recordedMIME     string
	recordedWarning  *string

	fencedCalls int
	fencedMBID  string

	beforeRead func()
	readCalls  int
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
	fixture.mu.Lock()
	fixture.readCalls++
	hook := fixture.beforeRead
	cover := fixture.cover
	err := fixture.readErr
	fixture.mu.Unlock()
	if hook != nil {
		hook()
	}
	return cover, err
}

func (fixture *acquisitionRepositoryFixture) RecordAutomaticReleaseCoverSelection(_ context.Context, _ uuid.UUID, expected int64, data []byte, mime string, warning *string) (persistence.ReleaseCover, error) {
	return fixture.record(expected, data, mime, warning, nil)
}

func (fixture *acquisitionRepositoryFixture) RecordAutomaticReleaseCoverSelectionForMBID(_ context.Context, _ uuid.UUID, expected int64, data []byte, mime string, warning *string, expectedMBID string) (persistence.ReleaseCover, error) {
	fixture.mu.Lock()
	fixture.fencedCalls++
	fixture.fencedMBID = expectedMBID
	fixture.mu.Unlock()
	return fixture.record(expected, data, mime, warning, &expectedMBID)
}

func (fixture *acquisitionRepositoryFixture) record(expected int64, data []byte, mime string, warning *string, expectedMBID *string) (persistence.ReleaseCover, error) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	fixture.recordCalls++
	fixture.recordedRevision = expected
	fixture.recordedBytes = append([]byte(nil), data...)
	fixture.recordedMIME = mime
	fixture.recordedWarning = cloneAcquisitionWarning(warning)
	if fixture.recordErr != nil {
		return persistence.ReleaseCover{}, fixture.recordErr
	}
	if expected != fixture.cover.Revision || fixture.cover.Mode != "automatic" || fixture.cover.AcquisitionOwner != nil {
		return persistence.ReleaseCover{}, persistence.ErrReleaseCoverConflict
	}
	if expectedMBID != nil && fixture.linkedMBID != *expectedMBID {
		return persistence.ReleaseCover{}, persistence.ErrReleaseCoverConflict
	}
	fixture.cover.Revision++
	if len(data) == 0 {
		fixture.cover.Warning = cloneAcquisitionWarning(warning)
		return fixture.cover, nil
	}
	generation := uuid.New()
	fixture.cover.SelectedGeneration = &generation
	fixture.cover.SelectedBytes = append([]byte(nil), data...)
	fixture.cover.SelectedMIME = &mime
	size := int64(len(data))
	fixture.cover.SelectedSize = &size
	fixture.cover.Warning = nil
	return fixture.cover, nil
}

func cloneAcquisitionWarning(warning *string) *string {
	if warning == nil {
		return nil
	}
	value := *warning
	return &value
}

func (fixture *acquisitionRepositoryFixture) currentCover() persistence.ReleaseCover {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	return fixture.cover
}

func (fixture *acquisitionRepositoryFixture) counts() (reads, records, fenced int) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	return fixture.readCalls, fixture.recordCalls, fixture.fencedCalls
}

func (fixture *acquisitionRepositoryFixture) recorded() ([]byte, string, *string, int64) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	return append([]byte(nil), fixture.recordedBytes...), fixture.recordedMIME, cloneAcquisitionWarning(fixture.recordedWarning), fixture.recordedRevision
}

func (fixture *acquisitionRepositoryFixture) fenced() (int, string) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	return fixture.fencedCalls, fixture.fencedMBID
}

func TestAutomaticCoverAcquisitionUsesProviderThenSavedArtwork(t *testing.T) {
	localImage := releaseCoverPNG(t)
	providerImage := releaseCoverPNGAtSize(t, 2, 2, color.RGBA{R: 255, A: 255})

	for _, test := range []struct {
		name          string
		mbid          string
		provider      bool
		run           func(context.Context, io.Writer) (coverart.Result, error)
		wantProvider  bool
		wantFence     bool
		wantProviders int
	}{
		{name: "saved local only", wantProvider: false},
		{name: "provider success", mbid: uuid.NewString(), provider: true, wantProvider: true, wantFence: true, wantProviders: 1, run: func(context.Context, io.Writer) (coverart.Result, error) {
			return coverart.Result{Found: true, MIME: "image/png", Size: int64(len(providerImage))}, nil
		}},
		{name: "provider failure", mbid: uuid.NewString(), provider: true, wantProviders: 1, run: func(_ context.Context, out io.Writer) (coverart.Result, error) {
			_, _ = out.Write([]byte("partial"))
			return coverart.Result{}, errors.New("provider failure")
		}},
		{name: "provider truncated", mbid: uuid.NewString(), provider: true, wantProviders: 1, run: func(_ context.Context, out io.Writer) (coverart.Result, error) {
			_, err := out.Write(providerImage[:len(providerImage)/2])
			return coverart.Result{Found: true, MIME: "image/png"}, err
		}},
		{name: "provider oversized", mbid: uuid.NewString(), provider: true, wantProviders: 1, run: func(_ context.Context, out io.Writer) (coverart.Result, error) {
			data := oversizedAcquisitionCoverPNG(t)
			_, err := out.Write(data)
			return coverart.Result{Found: true, MIME: "image/png", Size: int64(len(data))}, err
		}},
		{name: "provider size mismatch", mbid: uuid.NewString(), provider: true, wantProviders: 1, run: func(_ context.Context, out io.Writer) (coverart.Result, error) {
			_, err := out.Write(providerImage)
			return coverart.Result{Found: true, MIME: "image/png", Size: int64(len(providerImage) + 1)}, err
		}},
		{name: "provider MIME mismatch", mbid: uuid.NewString(), provider: true, wantProviders: 1, run: func(_ context.Context, out io.Writer) (coverart.Result, error) {
			_, err := out.Write(providerImage)
			return coverart.Result{Found: true, MIME: "image/jpeg"}, err
		}},
		{name: "invalid MBID", mbid: "not-a-mbid", provider: true, wantProviders: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			releaseID := uuid.New()
			reader := &acquisitionCandidateFixture{snapshot: persistence.ReleaseCoverCandidates{Candidates: []persistence.ReleaseCoverCandidate{{ID: uuid.New(), WinningResultID: uuid.New(), MIME: "image/png", Data: localImage}}}}
			if test.mbid != "" {
				mbid := test.mbid
				reader.snapshot.MusicBrainzMBID = &mbid
			}
			repository := &acquisitionRepositoryFixture{cover: persistence.ReleaseCover{ReleaseID: releaseID, Revision: 7, Mode: "automatic"}}
			repository.linkedMBID = test.mbid
			var provider *acquisitionProviderFixture
			if test.provider {
				run := test.run
				provider = &acquisitionProviderFixture{run: func(ctx context.Context, out io.Writer) (coverart.Result, error) {
					if test.wantProvider {
						if _, err := out.Write(providerImage); err != nil {
							return coverart.Result{}, err
						}
					}
					if run == nil {
						return coverart.Result{Found: false}, nil
					}
					return run(ctx, out)
				}}
			}
			var resolverProvider releaseCoverProvider
			if provider != nil {
				resolverProvider = provider
			}
			acquisition := NewReleaseCoverAcquisition(repository, NewReleaseCoverResolver(reader, resolverProvider))
			if err := acquisition.Ensure(context.Background(), releaseID); err != nil {
				t.Fatal(err)
			}
			bytesRecorded, mime, warning, revision := repository.recorded()
			if revision != 7 {
				t.Fatalf("recorded revision = %d", revision)
			}
			if warning != nil {
				t.Fatalf("warning = %q", *warning)
			}
			if test.wantProvider {
				if !bytes.Equal(bytesRecorded, providerImage) || mime != "image/png" {
					t.Fatalf("provider image was not selected: bytes=%d MIME=%q", len(bytesRecorded), mime)
				}
			} else if !bytes.Equal(bytesRecorded, localImage) || mime != "image/png" {
				t.Fatalf("saved artwork was not selected: bytes=%d MIME=%q", len(bytesRecorded), mime)
			}
			_, records, fenced := repository.counts()
			if records != 1 {
				t.Fatalf("record calls = %d", records)
			}
			if test.wantFence != (fenced == 1) {
				t.Fatalf("fenced calls = %d, want %v", fenced, test.wantFence)
			}
			if test.wantFence {
				fencedCalls, fencedMBID := repository.fenced()
				if fencedCalls != 1 || fencedMBID != test.mbid {
					t.Fatalf("fenced write = calls %d MBID %q, want %q", fencedCalls, fencedMBID, test.mbid)
				}
			}
			providerCalls := 0
			if provider != nil {
				providerCalls = provider.callCount()
			}
			if providerCalls != test.wantProviders {
				t.Fatalf("provider calls = %d want %d", providerCalls, test.wantProviders)
			}
		})
	}
}

func TestAutomaticCoverAcquisitionPreservesSelectedAndManualAuthority(t *testing.T) {
	localImage := releaseCoverPNG(t)
	mbid := uuid.NewString()
	for _, test := range []struct {
		name  string
		cover persistence.ReleaseCover
	}{
		{name: "selected automatic", cover: persistence.ReleaseCover{Revision: 7, Mode: "automatic", SelectedGeneration: new(uuid.UUID), SelectedBytes: localImage, SelectedMIME: new(string), SelectedSize: new(int64)}},
		{name: "manual pending", cover: persistence.ReleaseCover{Revision: 7, Mode: "manual", PendingGeneration: new(uuid.UUID), PendingBytes: localImage, PendingMIME: new(string), PendingSize: new(int64)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			releaseID := uuid.New()
			test.cover.ReleaseID = releaseID
			reader := &acquisitionCandidateFixture{snapshot: persistence.ReleaseCoverCandidates{MusicBrainzMBID: &mbid, Candidates: []persistence.ReleaseCoverCandidate{{ID: uuid.New(), WinningResultID: uuid.New(), MIME: "image/png", Data: localImage}}}}
			repository := &acquisitionRepositoryFixture{cover: test.cover}
			provider := &acquisitionProviderFixture{run: func(context.Context, io.Writer) (coverart.Result, error) {
				return coverart.Result{Found: true, MIME: "image/png"}, nil
			}}
			acquisition := NewReleaseCoverAcquisition(repository, NewReleaseCoverResolver(reader, provider))
			if err := acquisition.Ensure(context.Background(), releaseID); err != nil {
				t.Fatal(err)
			}
			reads, records, _ := repository.counts()
			if reads != 1 || records != 0 || reader.callCount() != 0 || provider.callCount() != 0 {
				t.Fatalf("existing authority did work: reads=%d records=%d candidates=%d provider=%d", reads, records, reader.callCount(), provider.callCount())
			}
		})
	}
}

func TestAutomaticCoverAcquisitionRecordsAbsentWarningOnceAndRetriesLater(t *testing.T) {
	releaseID := uuid.New()
	localImage := releaseCoverPNG(t)
	reader := &acquisitionCandidateFixture{snapshot: persistence.ReleaseCoverCandidates{}}
	repository := &acquisitionRepositoryFixture{cover: persistence.ReleaseCover{ReleaseID: releaseID, Revision: 7, Mode: "automatic"}}
	acquisition := NewReleaseCoverAcquisition(repository, NewReleaseCoverResolver(reader, nil))

	if err := acquisition.Ensure(context.Background(), releaseID); err != nil {
		t.Fatal(err)
	}
	_, _, warning, revision := repository.recorded()
	if revision != 7 || warning == nil || *warning != automaticReleaseCoverWarning {
		t.Fatalf("absent warning = %v at revision %d", warning, revision)
	}
	if current := repository.currentCover(); current.Revision != 8 || current.Warning == nil || current.SelectedGeneration != nil {
		t.Fatalf("absent authority after first ensure: %+v", current)
	}

	if err := acquisition.Ensure(context.Background(), releaseID); err != nil {
		t.Fatal(err)
	}
	if _, records, _ := repository.counts(); records != 1 {
		t.Fatalf("identical absent warning churned the revision: records=%d", records)
	}
	if current := repository.currentCover(); current.Revision != 8 {
		t.Fatalf("identical absent warning changed revision to %d", current.Revision)
	}

	reader.setSnapshot(persistence.ReleaseCoverCandidates{Candidates: []persistence.ReleaseCoverCandidate{{ID: uuid.New(), WinningResultID: uuid.New(), MIME: "image/png", Data: localImage}}})
	if err := acquisition.Ensure(context.Background(), releaseID); err != nil {
		t.Fatal(err)
	}
	recorded, mime, warning, revision := repository.recorded()
	if revision != 8 || warning != nil || mime != "image/png" || !bytes.Equal(recorded, localImage) {
		t.Fatalf("retry did not select new saved artwork: bytes=%d MIME=%q warning=%v revision=%d", len(recorded), mime, warning, revision)
	}
	current := repository.currentCover()
	if current.Revision != 9 || current.Warning != nil || current.SelectedGeneration == nil {
		t.Fatalf("retry authority = %+v", current)
	}
}

func TestAutomaticCoverAcquisitionProviderTimeoutStillUsesSavedArtwork(t *testing.T) {
	localImage := releaseCoverPNG(t)
	mbid := uuid.NewString()
	reader := &acquisitionCandidateFixture{snapshot: persistence.ReleaseCoverCandidates{MusicBrainzMBID: &mbid, Candidates: []persistence.ReleaseCoverCandidate{{ID: uuid.New(), WinningResultID: uuid.New(), MIME: "image/png", Data: localImage}}}}
	repository := &acquisitionRepositoryFixture{cover: persistence.ReleaseCover{ReleaseID: uuid.New(), Revision: 7, Mode: "automatic"}}
	provider := &acquisitionProviderFixture{run: func(ctx context.Context, _ io.Writer) (coverart.Result, error) {
		<-ctx.Done()
		return coverart.Result{}, ctx.Err()
	}}
	acquisition := NewReleaseCoverAcquisition(repository, NewReleaseCoverResolver(reader, provider))
	acquisition.timeout = 20 * time.Millisecond
	if err := acquisition.Ensure(context.Background(), repository.cover.ReleaseID); err != nil {
		t.Fatal(err)
	}
	recorded, _, warning, _ := repository.recorded()
	if warning != nil || !bytes.Equal(recorded, localImage) {
		t.Fatalf("provider timeout did not fall back to saved artwork: bytes=%d warning=%v", len(recorded), warning)
	}
	if reader.callCount() != 2 {
		t.Fatalf("saved fallback reads = %d, want 2", reader.callCount())
	}
}

func TestAutomaticCoverAcquisitionProviderTimeoutWithoutSavedArtworkIsIdempotent(t *testing.T) {
	mbid := uuid.NewString()
	reader := &acquisitionCandidateFixture{snapshot: persistence.ReleaseCoverCandidates{MusicBrainzMBID: &mbid}}
	repository := &acquisitionRepositoryFixture{cover: persistence.ReleaseCover{ReleaseID: uuid.New(), Revision: 7, Mode: "automatic"}}
	provider := &acquisitionProviderFixture{run: func(ctx context.Context, _ io.Writer) (coverart.Result, error) {
		<-ctx.Done()
		return coverart.Result{}, ctx.Err()
	}}
	acquisition := NewReleaseCoverAcquisition(repository, NewReleaseCoverResolver(reader, provider))
	acquisition.timeout = 20 * time.Millisecond
	for range 2 {
		if err := acquisition.Ensure(context.Background(), repository.cover.ReleaseID); err != nil {
			t.Fatal(err)
		}
	}
	recorded, _, warning, _ := repository.recorded()
	if len(recorded) != 0 || warning == nil || *warning != automaticReleaseCoverWarning {
		t.Fatalf("timeout warning = %v, bytes=%d", warning, len(recorded))
	}
	if _, records, _ := repository.counts(); records != 1 {
		t.Fatalf("repeated timeout churned the revision: records=%d", records)
	}
	if current := repository.currentCover(); current.Revision != 8 {
		t.Fatalf("repeated timeout revision = %d", current.Revision)
	}
}

func TestAutomaticCoverAcquisitionFencesReboundMBID(t *testing.T) {
	releaseID := uuid.New()
	localImage := releaseCoverPNG(t)
	providerImage := releaseCoverPNGAtSize(t, 2, 2, color.RGBA{G: 255, A: 255})
	mbid := uuid.NewString()
	reader := &acquisitionCandidateFixture{snapshot: persistence.ReleaseCoverCandidates{MusicBrainzMBID: &mbid, Candidates: []persistence.ReleaseCoverCandidate{{ID: uuid.New(), WinningResultID: uuid.New(), MIME: "image/png", Data: localImage}}}}
	repository := &acquisitionRepositoryFixture{
		cover:      persistence.ReleaseCover{ReleaseID: releaseID, Revision: 7, Mode: "automatic"},
		linkedMBID: uuid.NewString(),
	}
	provider := &acquisitionProviderFixture{run: func(_ context.Context, out io.Writer) (coverart.Result, error) {
		_, err := out.Write(providerImage)
		return coverart.Result{Found: true, MIME: "image/png", Size: int64(len(providerImage))}, err
	}}
	acquisition := NewReleaseCoverAcquisition(repository, NewReleaseCoverResolver(reader, provider))
	if err := acquisition.Ensure(context.Background(), releaseID); err != nil {
		t.Fatal(err)
	}
	if _, records, fenced := repository.counts(); records != 1 || fenced != 1 {
		t.Fatalf("fenced write calls = records %d fenced %d", records, fenced)
	}
	current := repository.currentCover()
	if current.Revision != 7 || current.SelectedGeneration != nil {
		t.Fatalf("rebound MBID image changed authority: %+v", current)
	}
}

func TestAutomaticCoverAcquisitionPropagatesRepositoryError(t *testing.T) {
	reader := &acquisitionCandidateFixture{}
	repository := &acquisitionRepositoryFixture{readErr: errors.New("database unavailable")}
	acquisition := NewReleaseCoverAcquisition(repository, NewReleaseCoverResolver(reader, nil))
	err := acquisition.Ensure(context.Background(), uuid.New())
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("database unavailable")) {
		t.Fatalf("repository error = %v", err)
	}
	if _, records, _ := repository.counts(); records != 0 {
		t.Fatalf("repository error recorded a result: %d", records)
	}
}

func TestAutomaticCoverAcquisitionCancellation(t *testing.T) {
	releaseID := uuid.New()
	localImage := releaseCoverPNG(t)
	reader := &acquisitionCandidateFixture{snapshot: persistence.ReleaseCoverCandidates{Candidates: []persistence.ReleaseCoverCandidate{{ID: uuid.New(), WinningResultID: uuid.New(), MIME: "image/png", Data: localImage}}}}
	repository := &acquisitionRepositoryFixture{cover: persistence.ReleaseCover{ReleaseID: releaseID, Revision: 7, Mode: "automatic"}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	acquisition := NewReleaseCoverAcquisition(repository, NewReleaseCoverResolver(reader, nil))
	if err := acquisition.Ensure(ctx, releaseID); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled error = %v", err)
	}
	if _, records, _ := repository.counts(); records != 0 {
		t.Fatalf("pre-canceled acquisition recorded %d results", records)
	}

	mbid := uuid.NewString()
	reader = &acquisitionCandidateFixture{snapshot: persistence.ReleaseCoverCandidates{MusicBrainzMBID: &mbid, Candidates: []persistence.ReleaseCoverCandidate{{ID: uuid.New(), WinningResultID: uuid.New(), MIME: "image/png", Data: localImage}}}}
	repository = &acquisitionRepositoryFixture{cover: persistence.ReleaseCover{ReleaseID: releaseID, Revision: 7, Mode: "automatic"}}
	ctx, cancel = context.WithCancel(context.Background())
	provider := &acquisitionProviderFixture{run: func(ctx context.Context, _ io.Writer) (coverart.Result, error) {
		cancel()
		return coverart.Result{}, context.Canceled
	}}
	acquisition = NewReleaseCoverAcquisition(repository, NewReleaseCoverResolver(reader, provider))
	if err := acquisition.Ensure(ctx, releaseID); !errors.Is(err, context.Canceled) {
		t.Fatalf("provider cancellation error = %v", err)
	}
	if _, records, _ := repository.counts(); records != 0 {
		t.Fatalf("provider cancellation recorded %d results", records)
	}
}

func TestAutomaticCoverAcquisitionCoalescesConcurrentEnsureCalls(t *testing.T) {
	reader := &acquisitionCandidateFixture{}
	repository := &acquisitionRepositoryFixture{cover: persistence.ReleaseCover{ReleaseID: uuid.New(), Revision: 7, Mode: "automatic"}}
	acquisition := NewReleaseCoverAcquisition(repository, NewReleaseCoverResolver(reader, nil))
	releaseID := repository.cover.ReleaseID
	leader, err := acquisition.join(context.Background(), releaseID)
	if err != nil || !leader {
		t.Fatalf("leader join = %v %v", leader, err)
	}
	done := make(chan error, 1)
	go func() {
		done <- acquisition.Ensure(context.Background(), releaseID)
	}()
	select {
	case err := <-done:
		t.Fatalf("duplicate ensure did not wait: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	acquisition.finish(releaseID, nil)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if reads, records, _ := repository.counts(); reads != 0 || records != 0 {
		t.Fatalf("coalesced follower did work: reads=%d records=%d", reads, records)
	}
	if reader.callCount() != 0 {
		t.Fatalf("coalesced follower read candidates: %d", reader.callCount())
	}
}
