package service

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestObservePublicationAssetsRequiresObservedInputs(t *testing.T) {
	for name, capture := range map[string]metadata.Capture{
		"missing cover inspection":    {Tags: map[string][]string{}},
		"unobserved cover inspection": {Tags: map[string][]string{}, CoverInspection: &metadata.CoverInspection{}},
		"missing tags":                {CoverInspection: &metadata.CoverInspection{Observed: true}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ObservePublicationAssets(capture); err == nil {
				t.Fatal("expected unavailable observation error")
			}
		})
	}
}

func TestObservePublicationAssetsCoverUsesObservedBytes(t *testing.T) {
	for _, test := range []struct {
		name, mime string
		data       []byte
	}{
		{"png", "image/png", []byte("\x89PNG\r\n\x1a\n")},
		{"jpeg", "image/jpeg", []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00")},
	} {
		t.Run(test.name, func(t *testing.T) {
			capture := metadata.Capture{
				Tags: map[string][]string{},
				CoverInspection: &metadata.CoverInspection{Observed: true, Cover: &metadata.Cover{
					Data: test.data, MIME: "image/forged",
				}},
			}
			outcome, err := ObservePublicationAssets(capture)
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(test.data)
			if outcome.Cover.State != persistence.PublicationAssetEmbedded || outcome.Cover.MIME != test.mime || outcome.Cover.SHA256 != hex.EncodeToString(digest[:]) || outcome.Cover.SizeBytes == nil || *outcome.Cover.SizeBytes != int64(len(test.data)) {
				t.Fatalf("unexpected cover outcome: %+v", outcome.Cover)
			}
			if got := http.DetectContentType(test.data); got != outcome.Cover.MIME {
				t.Fatalf("MIME = %q, detected %q", outcome.Cover.MIME, got)
			}
		})
	}
}

func TestObservePublicationAssetsAbsentAndExactLyrics(t *testing.T) {
	capture := metadata.Capture{
		Tags:            map[string][]string{"LYRICS": {"[00:01.00]Café\r\n[00:02.00]歌"}},
		CoverInspection: &metadata.CoverInspection{Observed: true},
	}
	outcome, err := ObservePublicationAssets(capture)
	if err != nil {
		t.Fatal(err)
	}
	lyrics := capture.Tags["LYRICS"][0]
	digest := sha256.Sum256([]byte(lyrics))
	if outcome.Version != persistence.PublicationAssetOutcomesVersion || outcome.Cover.State != persistence.PublicationAssetAbsent || outcome.Lyrics.State != persistence.PublicationAssetEmbedded || outcome.Lyrics.Format != "lrc" || outcome.Lyrics.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("unexpected outcomes: %+v", outcome)
	}
	for _, tags := range []map[string][]string{{}, {"LYRICS": {""}}} {
		absent, err := ObservePublicationAssets(metadata.Capture{Tags: tags, CoverInspection: &metadata.CoverInspection{Observed: true}})
		if err != nil || absent.Lyrics.State != persistence.PublicationAssetAbsent {
			t.Fatalf("expected absent lyrics, got %+v, %v", absent.Lyrics, err)
		}
	}
}

func TestObservePublicationAssetsRejectsInvalidLyricsAndCover(t *testing.T) {
	for name, tags := range map[string]map[string][]string{
		"invalid UTF-8": {"LYRICS": {string([]byte{0xff})}},
		"unsynced":      {"LYRICS": {"just words"}},
		"multiple":      {"LYRICS": {"[00:01.00]one", "[00:02.00]two"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ObservePublicationAssets(metadata.Capture{Tags: tags, CoverInspection: &metadata.CoverInspection{Observed: true}})
			if err == nil {
				t.Fatal("expected invalid lyrics error")
			}
		})
	}
	_, err := ObservePublicationAssets(metadata.Capture{
		Tags:            map[string][]string{},
		CoverInspection: &metadata.CoverInspection{Observed: true, Cover: &metadata.Cover{}},
	})
	if err == nil {
		t.Fatal("expected empty cover error")
	}
}
