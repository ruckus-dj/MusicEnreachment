package service

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/coverart"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"golang.org/x/image/tiff"
)

type coverCandidatesStub struct {
	snapshot *persistence.ReleaseCoverCandidates
	err      error
}

func (s coverCandidatesStub) ReadReleaseCoverCandidates(context.Context, uuid.UUID) (*persistence.ReleaseCoverCandidates, error) {
	return s.snapshot, s.err
}

type coverProviderStub struct {
	result coverart.Result
	data   []byte
	err    error
	calls  int
	mbid   string
	run    func(context.Context, io.Writer) (coverart.Result, error)
}

func (s *coverProviderStub) DownloadFront(ctx context.Context, mbid string, writer io.Writer) (coverart.Result, error) {
	s.calls++
	s.mbid = mbid
	if s.run != nil {
		return s.run(ctx, writer)
	}
	_, writeErr := writer.Write(s.data)
	if s.err != nil {
		return s.result, s.err
	}
	return s.result, writeErr
}

func encodedCover(t *testing.T, format string, width, height int, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, c)
		}
	}
	var out bytes.Buffer
	var err error
	switch format {
	case "png":
		err = png.Encode(&out, img)
	case "jpeg":
		err = jpeg.Encode(&out, img, &jpeg.Options{Quality: 10})
	case "gif":
		err = gif.Encode(&out, img, nil)
	case "tiff":
		err = tiff.Encode(&out, img, nil)
	default:
		t.Fatalf("unsupported test format %q", format)
	}
	if err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func coverCandidate(format string, w, h int, id, winning uuid.UUID, size int64) persistence.ReleaseCoverCandidate {
	return persistence.ReleaseCoverCandidate{ID: id, WinningResultID: winning, MIME: "image/" + format, Data: encodedCoverForTest(format, w, h), SizeBytes: size}
}
func encodedCoverForTest(format string, w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 90, 255})
		}
	}
	var b bytes.Buffer
	switch format {
	case "png":
		_ = png.Encode(&b, img)
	case "jpeg":
		_ = jpeg.Encode(&b, img, &jpeg.Options{Quality: 10})
	case "gif":
		_ = gif.Encode(&b, img, nil)
	}
	return b.Bytes()
}

func TestReleaseCoverResolverProviderFirstAndNoMBID(t *testing.T) {
	local := coverCandidate("png", 100, 100, uuid.New(), uuid.New(), 1)
	mbid := uuid.NewString()
	providerBytes := encodedCoverForTest("png", 2, 2)
	provider := &coverProviderStub{result: coverart.Result{Found: true, MIME: "image/png"}, data: providerBytes}
	got, err := NewReleaseCoverResolver(coverCandidatesStub{snapshot: &persistence.ReleaseCoverCandidates{MusicBrainzMBID: &mbid, Candidates: []persistence.ReleaseCoverCandidate{local}}}, provider).Resolve(context.Background(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "provider" || !bytes.Equal(got.Data, providerBytes) || provider.mbid != mbid || got.ProviderMBID != mbid {
		t.Fatalf("provider was not preferred: %#v mbid=%q", got, provider.mbid)
	}
	provider.result = coverart.Result{Found: false}
	provider.data = nil
	fallback, err := NewReleaseCoverResolver(coverCandidatesStub{snapshot: &persistence.ReleaseCoverCandidates{MusicBrainzMBID: &mbid, Candidates: []persistence.ReleaseCoverCandidate{local}}}, provider).Resolve(context.Background(), uuid.New())
	if err != nil || fallback.Source != "local" || fallback.CandidateID != local.ID || fallback.ProviderMBID != "" {
		t.Fatalf("missing provider image did not use saved local image: %+v %v", fallback, err)
	}
	provider.calls = 0
	_, err = NewReleaseCoverResolver(coverCandidatesStub{snapshot: &persistence.ReleaseCoverCandidates{Candidates: []persistence.ReleaseCoverCandidate{local}}}, provider).Resolve(context.Background(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if provider.calls != 0 {
		t.Fatal("provider called without MBID")
	}
}

func TestReleaseCoverResolverFallsBackToSavedArtwork(t *testing.T) {
	validLocal := coverCandidate("png", 50, 50, uuid.New(), uuid.New(), 1)
	oversized := oversizedAcquisitionCoverPNG(t)
	for _, test := range []struct {
		name   string
		mbid   string
		result coverart.Result
		data   []byte
		err    error
	}{
		{name: "provider partial with error", mbid: uuid.NewString(), data: encodedCoverForTest("png", 2, 2)[:20], err: errors.New("network failed")},
		{name: "provider truncated", mbid: uuid.NewString(), result: coverart.Result{Found: true, MIME: "image/png"}, data: encodedCoverForTest("png", 100, 100)[:40]},
		{name: "provider oversized", mbid: uuid.NewString(), result: coverart.Result{Found: true, MIME: "image/png"}, data: oversized},
		{name: "provider MIME mismatch", mbid: uuid.NewString(), result: coverart.Result{Found: true, MIME: "image/jpeg"}, data: encodedCoverForTest("png", 2, 2)},
		{name: "provider size mismatch", mbid: uuid.NewString(), result: coverart.Result{Found: true, MIME: "image/png", Size: 1 << 20}, data: encodedCoverForTest("png", 2, 2)},
		{name: "invalid MBID never calls provider", mbid: "not-a-mbid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := &coverProviderStub{result: test.result, data: test.data, err: test.err}
			mbid := test.mbid
			got, err := NewReleaseCoverResolver(coverCandidatesStub{snapshot: &persistence.ReleaseCoverCandidates{MusicBrainzMBID: &mbid, Candidates: []persistence.ReleaseCoverCandidate{validLocal}}}, provider).Resolve(context.Background(), uuid.New())
			if err != nil {
				t.Fatal(err)
			}
			if got.Source != "local" || got.CandidateID != validLocal.ID {
				t.Fatalf("saved artwork was not used after unusable front: %+v", got)
			}
			if test.mbid != "not-a-mbid" && provider.calls != 1 {
				t.Fatalf("provider calls = %d", provider.calls)
			}
			if test.mbid == "not-a-mbid" && provider.calls != 0 {
				t.Fatalf("invalid MBID made a provider request: %d", provider.calls)
			}
		})
	}
}

func TestReleaseCoverResolverOptionalProviderCanBeAbsent(t *testing.T) {
	local := coverCandidate("png", 20, 20, uuid.New(), uuid.New(), 1)
	mbid := uuid.NewString()
	got, err := NewReleaseCoverResolver(coverCandidatesStub{snapshot: &persistence.ReleaseCoverCandidates{MusicBrainzMBID: &mbid, Candidates: []persistence.ReleaseCoverCandidate{local}}}, nil).Resolve(context.Background(), uuid.New())
	if err != nil || got.Source != "local" || got.CandidateID != local.ID {
		t.Fatalf("nil provider did not fall back locally: %+v %v", got, err)
	}
}

func TestReleaseCoverResolverBoundsProviderOriginal(t *testing.T) {
	local := coverCandidate("png", 10, 10, uuid.New(), uuid.New(), 1)
	providerBytes := encodedCoverForTest("png", 100, 100)
	provider := &coverProviderStub{result: coverart.Result{Found: true, MIME: "image/png", Size: int64(len(providerBytes))}, data: providerBytes}
	mbid := uuid.NewString()
	resolver := NewReleaseCoverResolver(coverCandidatesStub{snapshot: &persistence.ReleaseCoverCandidates{MusicBrainzMBID: &mbid, Candidates: []persistence.ReleaseCoverCandidate{local}}}, provider)
	resolver.maxBytes = int64(len(providerBytes) - 1)
	got, err := resolver.Resolve(context.Background(), uuid.New())
	if err != nil || got.Source != "local" {
		t.Fatalf("oversized provider original was not bounded: %+v %v", got, err)
	}
}

func TestReleaseCoverResolverChoosesPixelCountAndUUIDStable(t *testing.T) {
	low := coverCandidate("png", 10, 10, uuid.MustParse("00000000-0000-0000-0000-000000000003"), uuid.New(), 1<<20)
	win := coverCandidate("png", 20, 10, uuid.MustParse("00000000-0000-0000-0000-000000000001"), uuid.New(), 1)
	tie := coverCandidate("png", 10, 20, uuid.MustParse("00000000-0000-0000-0000-000000000002"), uuid.New(), 100)
	for _, candidates := range [][]persistence.ReleaseCoverCandidate{{low, win, tie}, {tie, low, win}} {
		got, err := NewReleaseCoverResolver(coverCandidatesStub{snapshot: &persistence.ReleaseCoverCandidates{Candidates: candidates}}, nil).Resolve(context.Background(), uuid.New())
		if err != nil {
			t.Fatal(err)
		}
		if got.CandidateID != win.ID {
			t.Fatalf("winner=%s want %s", got.CandidateID, win.ID)
		}
		if !bytes.Equal(got.Data, win.Data) {
			t.Fatal("selected bytes changed")
		}
	}
}

func TestReleaseCoverResolverSkipsInvalidSavedCandidate(t *testing.T) {
	valid := coverCandidate("png", 2, 2, uuid.New(), uuid.New(), 1)
	invalidMIME := coverCandidate("png", 100, 100, uuid.New(), uuid.New(), 1)
	invalidMIME.MIME = "image/jpeg"
	truncated := coverCandidate("png", 200, 200, uuid.New(), uuid.New(), 1)
	truncated.Data = truncated.Data[:len(truncated.Data)/2]
	for _, candidates := range [][]persistence.ReleaseCoverCandidate{{invalidMIME, valid}, {truncated, valid}} {
		got, err := resolveLocalReleaseCover(context.Background(), candidates)
		if err != nil || got.CandidateID != valid.ID {
			t.Fatalf("valid saved candidate was hidden by an invalid one: %+v %v", got, err)
		}
	}
}

func TestReleaseCoverResolverPreservesOriginalGIFBytesAndMIME(t *testing.T) {
	original := encodedCover(t, "gif", 4, 3, color.RGBA{R: 200, A: 255})
	candidate := persistence.ReleaseCoverCandidate{ID: uuid.New(), WinningResultID: uuid.New(), MIME: "image/gif", Data: append([]byte(nil), original...), SizeBytes: int64(len(original))}
	got, err := NewReleaseCoverResolver(coverCandidatesStub{snapshot: &persistence.ReleaseCoverCandidates{Candidates: []persistence.ReleaseCoverCandidate{candidate}}}, nil).Resolve(context.Background(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if got.MIME != "image/gif" {
		t.Fatalf("MIME=%q", got.MIME)
	}
	if !bytes.Equal(got.Data, original) || !bytes.Equal(candidate.Data, original) {
		t.Fatal("selected or saved original bytes changed")
	}
}

func TestReleaseCoverResolverRanksAndPreservesSavedTIFF(t *testing.T) {
	data := encodedCover(t, "tiff", 5, 7, color.RGBA{G: 210, A: 255})
	image := persistence.ReleaseCoverCandidate{ID: uuid.New(), WinningResultID: uuid.New(), MIME: "image/tiff", Data: data}
	got, err := resolveLocalReleaseCover(context.Background(), []persistence.ReleaseCoverCandidate{coverCandidate("png", 2, 3, uuid.New(), uuid.New(), 1000), image})
	if err != nil || got.CandidateID != image.ID || got.MIME != "image/tiff" || !bytes.Equal(got.Data, data) {
		t.Fatalf("TIFF candidate was not preserved: %+v %v", got, err)
	}
	config, _, err := imagepkgConfig(got.Data)
	if err != nil || config.Width != 5 || config.Height != 7 {
		t.Fatalf("TIFF dimensions changed: %+v %v", config, err)
	}
}

func TestReleaseCoverResolverRejectsSavedCoverDiagnostics(t *testing.T) {
	got, err := NewReleaseCoverResolver(coverCandidatesStub{snapshot: &persistence.ReleaseCoverCandidates{CoverErrors: []string{"cover dimensions exceed the maximum"}}}, nil).Resolve(context.Background(), uuid.New())
	if !errors.Is(err, errReleaseCoverUnavailable) || got.Source != "" {
		t.Fatalf("diagnostic did not block silent absence: result=%#v err=%v", got, err)
	}
	if _, err := NewReleaseCoverResolver(coverCandidatesStub{snapshot: &persistence.ReleaseCoverCandidates{
		CoverErrors: []string{"cover dimensions exceed the maximum"},
		Candidates:  []persistence.ReleaseCoverCandidate{coverCandidate("png", 2, 2, uuid.New(), uuid.New(), 1)},
	}}, nil).Resolve(context.Background(), uuid.New()); err != nil {
		t.Fatalf("diagnostic hid a valid saved candidate: %v", err)
	}
}

func TestReleaseCoverResolverProviderFailureWithoutSavedArtworkIsUnavailable(t *testing.T) {
	mbid := uuid.NewString()
	provider := &coverProviderStub{data: encodedCoverForTest("png", 2, 2)[:5], err: errors.New("network failed")}
	_, err := NewReleaseCoverResolver(coverCandidatesStub{snapshot: &persistence.ReleaseCoverCandidates{MusicBrainzMBID: &mbid}}, provider).Resolve(context.Background(), uuid.New())
	if !errors.Is(err, errReleaseCoverUnavailable) || !bytes.Contains([]byte(err.Error()), []byte("network failed")) {
		t.Fatalf("expected optional provider failure, got %v", err)
	}
	provider = &coverProviderStub{result: coverart.Result{Found: false}}
	got, err := NewReleaseCoverResolver(coverCandidatesStub{snapshot: &persistence.ReleaseCoverCandidates{MusicBrainzMBID: &mbid}}, provider).Resolve(context.Background(), uuid.New())
	if err != nil || got.Source != "" {
		t.Fatalf("provider absence should be an empty result: %+v %v", got, err)
	}
}

func imagepkgConfig(data []byte) (image.Config, string, error) {
	return image.DecodeConfig(bytes.NewReader(data))
}

func TestReleaseCoverResolverDiscardsPartialProviderError(t *testing.T) {
	mbid := uuid.NewString()
	partial := encodedCoverForTest("png", 2, 2)
	local := coverCandidate("png", 1, 1, uuid.New(), uuid.New(), 1)
	provider := &coverProviderStub{data: partial, err: errors.New("network failed")}
	got, err := NewReleaseCoverResolver(coverCandidatesStub{snapshot: &persistence.ReleaseCoverCandidates{MusicBrainzMBID: &mbid, Candidates: []persistence.ReleaseCoverCandidate{local}}}, provider).Resolve(context.Background(), uuid.New())
	if err != nil || got.Source != "local" || got.CandidateID != local.ID {
		t.Fatalf("partial provider bytes were not discarded for saved artwork: %+v %v", got, err)
	}
}

func TestReleaseCoverResolverEmptyAndCanceled(t *testing.T) {
	got, err := NewReleaseCoverResolver(coverCandidatesStub{snapshot: &persistence.ReleaseCoverCandidates{}}, nil).Resolve(context.Background(), uuid.New())
	if err != nil || got.Source != "" || len(got.Data) != 0 {
		t.Fatalf("empty result=%#v err=%v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = NewReleaseCoverResolver(coverCandidatesStub{snapshot: &persistence.ReleaseCoverCandidates{Candidates: []persistence.ReleaseCoverCandidate{coverCandidate("png", 2, 2, uuid.New(), uuid.New(), 1)}}}, nil).Resolve(ctx, uuid.New())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	mbid := uuid.NewString()
	ctx, cancel = context.WithCancel(context.Background())
	provider := &coverProviderStub{run: func(context.Context, io.Writer) (coverart.Result, error) {
		cancel()
		return coverart.Result{}, context.Canceled
	}}
	_, err = NewReleaseCoverResolver(coverCandidatesStub{snapshot: &persistence.ReleaseCoverCandidates{MusicBrainzMBID: &mbid, Candidates: []persistence.ReleaseCoverCandidate{coverCandidate("png", 2, 2, uuid.New(), uuid.New(), 1)}}}, provider).Resolve(ctx, uuid.New())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("provider cancellation did not cancel despite saved artwork: %v", err)
	}
}
