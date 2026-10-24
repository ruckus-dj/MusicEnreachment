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
}

func (s *coverProviderStub) DownloadFront(_ context.Context, mbid string, writer io.Writer) (coverart.Result, error) {
	s.calls++
	s.mbid = mbid
	_, _ = writer.Write(s.data)
	return s.result, s.err
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
	mbid := "release-mbid"
	providerBytes := encodedCoverForTest("png", 2, 2)
	provider := &coverProviderStub{result: coverart.Result{Found: true, MIME: "image/png"}, data: providerBytes}
	got, err := NewReleaseCoverResolver(coverCandidatesStub{snapshot: &persistence.ReleaseCoverCandidates{MusicBrainzMBID: &mbid, Candidates: []persistence.ReleaseCoverCandidate{local}}}, provider).Resolve(context.Background(), uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "provider" || !bytes.Equal(got.Data, providerBytes) || provider.mbid != mbid {
		t.Fatalf("provider was not preferred: %#v", got)
	}
	provider.result = coverart.Result{Found: false}
	provider.data = nil
	fallback, err := NewReleaseCoverResolver(coverCandidatesStub{snapshot: &persistence.ReleaseCoverCandidates{MusicBrainzMBID: &mbid, Candidates: []persistence.ReleaseCoverCandidate{local}}}, provider).Resolve(context.Background(), uuid.New())
	if err != nil || fallback.Source != "local" || fallback.CandidateID != local.ID {
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
	if err == nil || got.Source != "" {
		t.Fatalf("diagnostic did not block silent absence: result=%#v err=%v", got, err)
	}
}

func imagepkgConfig(data []byte) (image.Config, string, error) {
	return image.DecodeConfig(bytes.NewReader(data))
}

func TestReleaseCoverResolverDiscardsPartialProviderError(t *testing.T) {
	mbid := "mbid"
	partial := encodedCoverForTest("png", 2, 2)
	provider := &coverProviderStub{data: partial, err: errors.New("network failed")}
	_, err := NewReleaseCoverResolver(coverCandidatesStub{snapshot: &persistence.ReleaseCoverCandidates{MusicBrainzMBID: &mbid, Candidates: []persistence.ReleaseCoverCandidate{coverCandidate("png", 1, 1, uuid.New(), uuid.New(), 1)}}}, provider).Resolve(context.Background(), uuid.New())
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("network failed")) {
		t.Fatalf("expected provider error, got %v", err)
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
}
