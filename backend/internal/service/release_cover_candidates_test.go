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
)

type releaseCoverCandidateRepositoryFixture struct {
	mbid   string
	linked bool
	err    error
	read   func() (string, bool, error)
	calls  int
}

func (fixture *releaseCoverCandidateRepositoryFixture) ReadReleaseMusicBrainzReleaseID(context.Context, uuid.UUID) (string, bool, error) {
	fixture.calls++
	if fixture.read != nil {
		return fixture.read()
	}
	return fixture.mbid, fixture.linked, fixture.err
}

type releaseCoverCandidateProviderFixture struct {
	images        []coverart.Image
	listErr       error
	listCalls     int
	listMBIDs     []string
	downloadRun   func(io.Writer) (coverart.Result, error)
	downloadCalls []string
	downloadMBIDs []string
}

func (fixture *releaseCoverCandidateProviderFixture) ListImages(_ context.Context, releaseMBID string) ([]coverart.Image, error) {
	fixture.listCalls++
	fixture.listMBIDs = append(fixture.listMBIDs, releaseMBID)
	return fixture.images, fixture.listErr
}

func (fixture *releaseCoverCandidateProviderFixture) DownloadImage(_ context.Context, releaseMBID, imageID string, destination io.Writer) (coverart.Result, error) {
	fixture.downloadCalls = append(fixture.downloadCalls, imageID)
	fixture.downloadMBIDs = append(fixture.downloadMBIDs, releaseMBID)
	return fixture.downloadRun(destination)
}

type releaseCoverCandidateSaverFixture struct {
	calls     int
	releaseID uuid.UUID
	expected  int64
	data      []byte
	mime      string
	err       error
	summary   ReleaseCoverSummary
}

func (fixture *releaseCoverCandidateSaverFixture) Save(_ context.Context, releaseID uuid.UUID, expectedRevision int64, data []byte, mime string) (ReleaseCoverSummary, error) {
	fixture.calls++
	fixture.releaseID, fixture.expected = releaseID, expectedRevision
	fixture.data, fixture.mime = append([]byte(nil), data...), mime
	return fixture.summary, fixture.err
}

func releaseCoverCandidateProviderFor(t *testing.T, data []byte, mime string) *releaseCoverCandidateProviderFixture {
	t.Helper()
	return &releaseCoverCandidateProviderFixture{
		images: []coverart.Image{{ID: "123", Front: true, Types: []string{"Front"}, ThumbnailURL: "https://example.test/thumb"}},
		downloadRun: func(destination io.Writer) (coverart.Result, error) {
			if _, err := destination.Write(data); err != nil {
				return coverart.Result{}, err
			}
			return coverart.Result{Found: true, MIME: mime, Size: int64(len(data))}, nil
		},
	}
}

func TestReleaseCoverCandidatesListUsesExactMusicBrainzLink(t *testing.T) {
	releaseID := uuid.New()
	repository := &releaseCoverCandidateRepositoryFixture{mbid: "release-mbid", linked: true}
	provider := &releaseCoverCandidateProviderFixture{images: []coverart.Image{
		{ID: "1", Front: true, Types: []string{"Front"}, Comment: "front", ThumbnailURL: "https://example.test/1"},
		{ID: "2", Back: true, Types: []string{"Back"}, Comment: "back", ThumbnailURL: "https://example.test/2"},
	}}
	items, err := NewReleaseCoverCandidates(repository, provider, nil).List(context.Background(), releaseID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].ID != "1" || !items[0].Front || items[0].ThumbnailURL != "https://example.test/1" ||
		items[1].ID != "2" || !items[1].Back || items[1].Types[0] != "Back" {
		t.Fatalf("listed candidates = %+v", items)
	}
	if len(provider.listMBIDs) != 1 || provider.listMBIDs[0] != "release-mbid" {
		t.Fatalf("provider MBIDs = %v", provider.listMBIDs)
	}
}

func TestReleaseCoverCandidatesListEmptyWithoutExactLink(t *testing.T) {
	releaseID := uuid.New()
	repository := &releaseCoverCandidateRepositoryFixture{}
	provider := &releaseCoverCandidateProviderFixture{listErr: errors.New("must not be called")}
	items, err := NewReleaseCoverCandidates(repository, provider, nil).List(context.Background(), releaseID)
	if err != nil || items == nil || len(items) != 0 {
		t.Fatalf("without link: items=%v err=%v", items, err)
	}
	if provider.listCalls != 0 {
		t.Fatal("provider was called without an exact MusicBrainz release link")
	}
}

func TestReleaseCoverCandidatesListMapsMissingReleaseAndProviderFailure(t *testing.T) {
	releaseID := uuid.New()
	missing := &releaseCoverCandidateRepositoryFixture{err: persistence.ErrLibraryReleaseNotFound}
	if _, err := NewReleaseCoverCandidates(missing, &releaseCoverCandidateProviderFixture{}, nil).List(context.Background(), releaseID); !IsLibraryReleaseCoverNotFound(err) {
		t.Fatalf("missing release error = %v", err)
	}
	providerFailure := &releaseCoverCandidateProviderFixture{listErr: errors.New("provider down")}
	if _, err := NewReleaseCoverCandidates(&releaseCoverCandidateRepositoryFixture{mbid: "mbid", linked: true}, providerFailure, nil).List(context.Background(), releaseID); err == nil {
		t.Fatal("provider failure was ignored")
	}
	if _, err := NewReleaseCoverCandidates(&releaseCoverCandidateRepositoryFixture{}, nil, nil).List(context.Background(), uuid.Nil); !IsLibraryReleaseCoverInvalid(err) {
		t.Fatalf("nil release ID error = %v", err)
	}
}

func TestReleaseCoverCandidatesSelectDownloadsEligibleImageAndSavesPending(t *testing.T) {
	releaseID, expectedRevision := uuid.New(), int64(4)
	data := releaseCoverPNG(t)
	summary := ReleaseCoverSummary{ReleaseID: releaseID, Revision: expectedRevision + 1, Mode: "manual"}
	repository := &releaseCoverCandidateRepositoryFixture{mbid: "release-mbid", linked: true}
	provider := releaseCoverCandidateProviderFor(t, data, "image/png")
	saver := &releaseCoverCandidateSaverFixture{summary: summary}
	result, err := NewReleaseCoverCandidates(repository, provider, saver).Select(context.Background(), releaseID, "123", expectedRevision)
	if err != nil {
		t.Fatal(err)
	}
	if result != summary {
		t.Fatalf("select result = %+v, want %+v", result, summary)
	}
	if saver.calls != 1 || saver.releaseID != releaseID || saver.expected != expectedRevision || saver.mime != "image/png" || !bytes.Equal(saver.data, data) {
		t.Fatalf("save request = calls %d release %s revision %d mime %q bytes %d", saver.calls, saver.releaseID, saver.expected, saver.mime, len(saver.data))
	}
	if len(provider.downloadCalls) != 1 || provider.downloadCalls[0] != "123" || provider.downloadMBIDs[0] != "release-mbid" {
		t.Fatalf("download request = %v %v", provider.downloadMBIDs, provider.downloadCalls)
	}
}

func TestReleaseCoverCandidatesSelectRejectsIneligibleImageWithoutIO(t *testing.T) {
	releaseID := uuid.New()
	provider := &releaseCoverCandidateProviderFixture{images: []coverart.Image{{ID: "123"}}}
	saver := &releaseCoverCandidateSaverFixture{}
	service := NewReleaseCoverCandidates(&releaseCoverCandidateRepositoryFixture{mbid: "mbid", linked: true}, provider, saver)
	if _, err := service.Select(context.Background(), releaseID, "999", 1); !IsLibraryReleaseCoverNotFound(err) {
		t.Fatalf("unlisted image error = %v", err)
	}
	if len(provider.downloadCalls) != 0 || saver.calls != 0 {
		t.Fatal("unlisted image caused a download or write")
	}
	unlinked := NewReleaseCoverCandidates(&releaseCoverCandidateRepositoryFixture{}, provider, saver)
	if _, err := unlinked.Select(context.Background(), releaseID, "123", 1); !IsLibraryReleaseCoverNotFound(err) {
		t.Fatalf("release without MusicBrainz link error = %v", err)
	}
	if _, err := unlinked.List(context.Background(), releaseID); err != nil {
		t.Fatalf("unlinked listing error = %v", err)
	}
	if len(provider.downloadCalls) != 0 || saver.calls != 0 {
		t.Fatal("unlinked release caused a download or write")
	}
}

func TestReleaseCoverCandidatesSelectRejectsInvalidRequest(t *testing.T) {
	releaseID := uuid.New()
	repository := &releaseCoverCandidateRepositoryFixture{mbid: "mbid", linked: true}
	provider := &releaseCoverCandidateProviderFixture{}
	saver := &releaseCoverCandidateSaverFixture{}
	service := NewReleaseCoverCandidates(repository, provider, saver)
	for _, test := range []struct {
		name      string
		releaseID uuid.UUID
		imageID   string
		revision  int64
	}{
		{name: "nil release", releaseID: uuid.Nil, imageID: "123", revision: 1},
		{name: "empty image", releaseID: releaseID, imageID: "", revision: 1},
		{name: "padded image", releaseID: releaseID, imageID: " 123 ", revision: 1},
		{name: "zero revision", releaseID: releaseID, imageID: "123"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.Select(context.Background(), test.releaseID, test.imageID, test.revision); !IsLibraryReleaseCoverInvalid(err) {
				t.Fatalf("error = %v, want invalid", err)
			}
		})
	}
	if repository.calls != 0 || provider.listCalls != 0 || saver.calls != 0 {
		t.Fatal("invalid request reached the repository, provider or saver")
	}
}

func TestReleaseCoverCandidatesSelectRejectsUnvalidatedDownloads(t *testing.T) {
	releaseID := uuid.New()
	t.Run("not found", func(t *testing.T) {
		provider := releaseCoverCandidateProviderFor(t, nil, "")
		provider.downloadRun = func(io.Writer) (coverart.Result, error) { return coverart.Result{Found: false}, nil }
		saver := &releaseCoverCandidateSaverFixture{}
		_, err := NewReleaseCoverCandidates(&releaseCoverCandidateRepositoryFixture{mbid: "mbid", linked: true}, provider, saver).Select(context.Background(), releaseID, "123", 1)
		if !IsLibraryReleaseCoverNotFound(err) || saver.calls != 0 {
			t.Fatalf("not found: err=%v saves=%d", err, saver.calls)
		}
	})
	t.Run("MIME mismatch", func(t *testing.T) {
		provider := releaseCoverCandidateProviderFor(t, releaseCoverPNG(t), "image/jpeg")
		saver := &releaseCoverCandidateSaverFixture{}
		_, err := NewReleaseCoverCandidates(&releaseCoverCandidateRepositoryFixture{mbid: "mbid", linked: true}, provider, saver).Select(context.Background(), releaseID, "123", 1)
		if !IsLibraryReleaseCoverInvalid(err) || saver.calls != 0 {
			t.Fatalf("MIME mismatch: err=%v saves=%d", err, saver.calls)
		}
	})
	t.Run("byte bound", func(t *testing.T) {
		provider := releaseCoverCandidateProviderFor(t, releaseCoverPNG(t), "image/png")
		saver := &releaseCoverCandidateSaverFixture{}
		service := NewReleaseCoverCandidates(&releaseCoverCandidateRepositoryFixture{mbid: "mbid", linked: true}, provider, saver)
		service.maxBytes = 8
		_, err := service.Select(context.Background(), releaseID, "123", 1)
		if !IsLibraryReleaseCoverInvalid(err) || saver.calls != 0 {
			t.Fatalf("byte bound: err=%v saves=%d", err, saver.calls)
		}
	})
	t.Run("dimension bound", func(t *testing.T) {
		data := releaseCoverPNGAtSize(t, 10001, 1, color.White)
		provider := releaseCoverCandidateProviderFor(t, data, "image/png")
		saver := &releaseCoverCandidateSaverFixture{}
		_, err := NewReleaseCoverCandidates(&releaseCoverCandidateRepositoryFixture{mbid: "mbid", linked: true}, provider, saver).Select(context.Background(), releaseID, "123", 1)
		if !IsLibraryReleaseCoverInvalid(err) || saver.calls != 0 {
			t.Fatalf("dimension bound: err=%v saves=%d", err, saver.calls)
		}
	})
	t.Run("corrupt image", func(t *testing.T) {
		provider := releaseCoverCandidateProviderFor(t, []byte("not an image"), "image/png")
		saver := &releaseCoverCandidateSaverFixture{}
		_, err := NewReleaseCoverCandidates(&releaseCoverCandidateRepositoryFixture{mbid: "mbid", linked: true}, provider, saver).Select(context.Background(), releaseID, "123", 1)
		if !IsLibraryReleaseCoverInvalid(err) || saver.calls != 0 {
			t.Fatalf("corrupt image: err=%v saves=%d", err, saver.calls)
		}
	})
}

func TestReleaseCoverCandidatesSelectPreservesSelectedAndManualAuthority(t *testing.T) {
	releaseID, selectedGeneration, previousPending := uuid.New(), uuid.New(), uuid.New()
	selectedBytes, previousPendingBytes := []byte("selected"), []byte("previous-pending")
	selectedMIME, previousPendingMIME := "image/png", "image/png"
	cover := persistence.ReleaseCover{
		ReleaseID: releaseID, Revision: 6, Mode: "manual",
		SelectedGeneration: &selectedGeneration, SelectedBytes: selectedBytes, SelectedMIME: &selectedMIME,
		PendingGeneration: &previousPending, PendingBytes: previousPendingBytes, PendingMIME: &previousPendingMIME,
	}
	covers := NewReleaseCovers(&releaseCoverRepositoryFixture{cover: cover, musicBrainzMBID: "mbid"})
	provider := releaseCoverCandidateProviderFor(t, releaseCoverPNGFilled(t, color.Black), "image/png")
	result, err := NewReleaseCoverCandidates(
		&releaseCoverCandidateRepositoryFixture{mbid: "mbid", linked: true}, provider, covers,
	).Select(context.Background(), releaseID, "123", 6)
	if err != nil {
		t.Fatal(err)
	}
	if result.Revision != 7 || result.Mode != "manual" || result.PendingGeneration == nil || *result.PendingGeneration == previousPending {
		t.Fatalf("selected candidate summary = %+v", result)
	}
	if result.SelectedGeneration == nil || *result.SelectedGeneration != selectedGeneration {
		t.Fatal("selecting a candidate changed the effective selected cover")
	}
}

func TestReleaseCoverCandidatesSelectKeepsAutomaticSelectedCover(t *testing.T) {
	releaseID, selectedGeneration := uuid.New(), uuid.New()
	selectedBytes := []byte("local-selected")
	selectedMIME := "image/png"
	fixture := &releaseCoverRepositoryFixture{
		musicBrainzMBID: "mbid",
		cover: persistence.ReleaseCover{
			ReleaseID: releaseID, Revision: 2, Mode: "automatic",
			SelectedGeneration: &selectedGeneration, SelectedBytes: selectedBytes, SelectedMIME: &selectedMIME,
		},
	}
	result, err := NewReleaseCoverCandidates(
		&releaseCoverCandidateRepositoryFixture{mbid: "mbid", linked: true},
		releaseCoverCandidateProviderFor(t, releaseCoverPNGFilled(t, color.Black), "image/png"),
		NewReleaseCovers(fixture),
	).Select(context.Background(), releaseID, "123", 2)
	if err != nil {
		t.Fatal(err)
	}
	if result.Revision != 3 || result.Mode != "manual" || result.PendingGeneration == nil {
		t.Fatalf("candidate pending summary = %+v", result)
	}
	if result.SelectedGeneration == nil || *result.SelectedGeneration != selectedGeneration || !bytes.Equal(fixture.selectedBefore, selectedBytes) {
		t.Fatal("selecting a candidate changed the effective local/source cover")
	}
}

func TestReleaseCoverCandidatesSelectRejectsStaleRevision(t *testing.T) {
	releaseID := uuid.New()
	fixture := &releaseCoverRepositoryFixture{cover: persistence.ReleaseCover{ReleaseID: releaseID, Revision: 4, Mode: "automatic"}, musicBrainzMBID: "mbid"}
	provider := releaseCoverCandidateProviderFor(t, releaseCoverPNG(t), "image/png")
	saver := NewReleaseCovers(fixture)
	_, err := NewReleaseCoverCandidates(
		&releaseCoverCandidateRepositoryFixture{mbid: "mbid", linked: true}, provider, saver,
	).Select(context.Background(), releaseID, "123", 3)
	if !IsLibraryReleaseCoverConflict(err) {
		t.Fatalf("stale revision error = %v", err)
	}
	if fixture.setCalls != 1 || fixture.cover.Revision != 4 || fixture.cover.PendingGeneration != nil {
		t.Fatalf("stale select mutated authority: calls=%d cover=%+v", fixture.setCalls, fixture.cover)
	}
}

type releaseCoverCandidateBoundSaverFixture struct {
	releaseCoverCandidateSaverFixture
	boundCalls     int
	boundReleaseID uuid.UUID
	boundExpected  int64
	boundMBID      string
	boundData      []byte
	boundMIME      string
	boundSummary   ReleaseCoverSummary
	boundErr       error
}

func (fixture *releaseCoverCandidateBoundSaverFixture) SaveFromMusicBrainz(_ context.Context, releaseID uuid.UUID, expectedRevision int64, releaseMBID string, data []byte, mime string) (ReleaseCoverSummary, error) {
	fixture.boundCalls++
	fixture.boundReleaseID, fixture.boundExpected, fixture.boundMBID = releaseID, expectedRevision, releaseMBID
	fixture.boundData, fixture.boundMIME = append([]byte(nil), data...), mime
	return fixture.boundSummary, fixture.boundErr
}

func TestReleaseCoverCandidatesSelectUsesBindingFencedSave(t *testing.T) {
	releaseID, expectedRevision := uuid.New(), int64(4)
	data := releaseCoverPNG(t)
	summary := ReleaseCoverSummary{ReleaseID: releaseID, Revision: expectedRevision + 1, Mode: "manual"}
	repository := &releaseCoverCandidateRepositoryFixture{mbid: "mbid", linked: true}
	saver := &releaseCoverCandidateBoundSaverFixture{boundSummary: summary}
	result, err := NewReleaseCoverCandidates(repository, releaseCoverCandidateProviderFor(t, data, "image/png"), saver).
		Select(context.Background(), releaseID, "123", expectedRevision)
	if err != nil || result != summary {
		t.Fatalf("bound select result = %+v, %v", result, err)
	}
	if saver.boundCalls != 1 || saver.calls != 0 || saver.boundReleaseID != releaseID || saver.boundExpected != expectedRevision ||
		saver.boundMBID != "mbid" || saver.boundMIME != "image/png" || !bytes.Equal(saver.boundData, data) {
		t.Fatalf("binding-fenced save = %+v", saver)
	}
	if repository.calls != 1 {
		t.Fatalf("binding-fenced save re-read identity %d times", repository.calls)
	}
}

func TestReleaseCoverCandidatesSelectRejectsBindingChangeAfterDownload(t *testing.T) {
	releaseID := uuid.New()
	for name, changed := range map[string]struct {
		mbid   string
		linked bool
	}{
		"relinked": {mbid: "other-mbid", linked: true},
		"unlinked": {},
	} {
		t.Run(name, func(t *testing.T) {
			reads := 0
			repository := &releaseCoverCandidateRepositoryFixture{read: func() (string, bool, error) {
				reads++
				if reads == 1 {
					return "mbid", true, nil
				}
				return changed.mbid, changed.linked, nil
			}}
			saver := &releaseCoverCandidateSaverFixture{}
			_, err := NewReleaseCoverCandidates(repository, releaseCoverCandidateProviderFor(t, releaseCoverPNG(t), "image/png"), saver).
				Select(context.Background(), releaseID, "123", 1)
			if !IsLibraryReleaseCoverConflict(err) {
				t.Fatalf("binding change error = %v, want conflict", err)
			}
			if saver.calls != 0 || repository.calls != 2 {
				t.Fatalf("binding change reached Save: saves=%d reads=%d", saver.calls, repository.calls)
			}
		})
	}
}
