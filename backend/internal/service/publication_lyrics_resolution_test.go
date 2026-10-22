package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/lrclib"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
)

type publicationLyricsSettingsFixture struct {
	enabled bool
	err     error
	reads   int
	cancel  context.CancelFunc
}

func (settings *publicationLyricsSettingsFixture) GetLRCLIBEnabled(context.Context) (bool, error) {
	settings.reads++
	if settings.cancel != nil {
		settings.cancel()
	}
	return settings.enabled, settings.err
}

type publicationLyricsLookupFixture struct {
	metadata []lrclib.Metadata
	result   lrclib.LookupResult
	err      error
	cancel   context.CancelFunc
}

func (lookup *publicationLyricsLookupFixture) Lookup(_ context.Context, metadata lrclib.Metadata) (lrclib.LookupResult, error) {
	lookup.metadata = append(lookup.metadata, metadata)
	if lookup.cancel != nil {
		lookup.cancel()
	}
	return lookup.result, lookup.err
}

func TestPublicationLyricsResolverPrefersExactSelectedSourceBytes(t *testing.T) {
	const lyrics = "[ar:Artist]\r\n[00:01.250]Original timing\r\n"
	settings := &publicationLyricsSettingsFixture{}
	lookup := &publicationLyricsLookupFixture{}
	resolver := NewPublicationLyricsResolver(settings, lookup)
	result, err := resolver.Resolve(context.Background(), PublicationLyricsResolutionRequest{SelectedSourceTags: map[string][]string{"lyrics": {lyrics, lyrics}}})
	wantHash := sha256.Sum256([]byte(lyrics))
	if err != nil || result.Source != "source" || result.Lyrics != lyrics || result.SHA256 != hex.EncodeToString(wantHash[:]) || result.WarningCode != "" {
		t.Fatalf("selected source resolution = %+v, %v", result, err)
	}
	if len(lookup.metadata) != 0 || settings.reads != 0 {
		t.Fatal("source lyrics unexpectedly consulted LRCLIB")
	}
}

func TestPublicationLyricsResolverUsesOnlyExactLookupAndRereadsSettings(t *testing.T) {
	settings := &publicationLyricsSettingsFixture{enabled: true}
	lookup := &publicationLyricsLookupFixture{result: lrclib.LookupResult{Found: true, Lyrics: "[00:02.01]Provider"}}
	resolver := NewPublicationLyricsResolver(settings, lookup)
	metadata := lrclib.Metadata{Artist: "Artist", Title: "Title", Album: "Album", Duration: 123}
	request := PublicationLyricsResolutionRequest{SelectedSourceTags: map[string][]string{"LYRICS": {"plain unsynced text"}}, Metadata: metadata}
	result, err := resolver.Resolve(context.Background(), request)
	if err != nil || result.Source != "lrclib" || result.Lyrics != lookup.result.Lyrics || len(lookup.metadata) != 1 || lookup.metadata[0] != metadata {
		t.Fatalf("exact lookup = %+v, requests %+v, %v", result, lookup.metadata, err)
	}
	settings.enabled = false
	result, err = resolver.Resolve(context.Background(), request)
	if err != nil || result.Source != "none" || result.Lyrics != "" || result.WarningCode != "" || settings.reads != 2 || len(lookup.metadata) != 1 {
		t.Fatalf("disabled lookup reused stale settings: %+v, %v", result, err)
	}
}

func TestAutomaticLyricsRetryUsesFractionalProbeDurationAndNeverCurrentLyrics(t *testing.T) {
	settings := &publicationLyricsSettingsFixture{enabled: true}
	lookup := &publicationLyricsLookupFixture{result: lrclib.LookupResult{Found: true, Lyrics: "[00:01.00]replacement"}}
	resolver := NewPublicationLyricsResolver(settings, lookup)
	capture := metadata.Capture{Tags: map[string][]string{
		"ARTIST": {"Artist"}, "TITLE": {"Title"}, "ALBUM": {"Album"}, "LYRICS": {"old current lyrics"},
	}}
	tags, exact := automaticLyricsRetryLookupInputs(capture, 123.456789)
	if _, present := tags["LYRICS"]; present {
		t.Fatal("current publication lyrics leaked into retry source tags")
	}
	result, err := resolver.Resolve(context.Background(), PublicationLyricsResolutionRequest{SelectedSourceTags: tags, Metadata: exact})
	want := lrclib.Metadata{Artist: "Artist", Title: "Title", Album: "Album", Duration: 123.456789}
	if err != nil || result.Source != "lrclib" || len(lookup.metadata) != 1 || lookup.metadata[0] != want {
		t.Fatalf("retry lookup request = %#v, result=%#v err=%v", lookup.metadata, result, err)
	}
}

func TestPublicationLyricsResolverReportsOptionalFailuresWithoutInventingText(t *testing.T) {
	metadata := lrclib.Metadata{Artist: "Artist", Title: "Title", Album: "Album", Duration: 123}
	for _, test := range []struct {
		name     string
		settings publicationLyricsSettingsFixture
		lookup   publicationLyricsLookupFixture
		metadata lrclib.Metadata
		warning  string
	}{
		{name: "settings failure", settings: publicationLyricsSettingsFixture{err: errors.New("database unavailable")}, metadata: metadata, warning: "lyrics_settings_unavailable"},
		{name: "provider failure", settings: publicationLyricsSettingsFixture{enabled: true}, lookup: publicationLyricsLookupFixture{err: errors.New("provider response must not leak")}, metadata: metadata, warning: "lyrics_lookup_failed"},
		{name: "invalid synced result", settings: publicationLyricsSettingsFixture{enabled: true}, lookup: publicationLyricsLookupFixture{result: lrclib.LookupResult{Found: true, Lyrics: "plain text"}}, metadata: metadata, warning: "lyrics_lookup_invalid_synced_text"},
		{name: "normal absence", settings: publicationLyricsSettingsFixture{enabled: true}, metadata: metadata},
		{name: "instrumental", settings: publicationLyricsSettingsFixture{enabled: true}, lookup: publicationLyricsLookupFixture{result: lrclib.LookupResult{Found: true, Lyrics: "[00:01]Text", Track: lrclib.Track{Instrumental: true}}}, metadata: metadata},
		{name: "missing duration", settings: publicationLyricsSettingsFixture{enabled: true}, metadata: lrclib.Metadata{Artist: "Artist", Title: "Title", Album: "Album"}, warning: "lyrics_lookup_metadata_incomplete"},
		{name: "NaN duration", settings: publicationLyricsSettingsFixture{enabled: true}, metadata: lrclib.Metadata{Artist: "Artist", Title: "Title", Album: "Album", Duration: math.NaN()}, warning: "lyrics_lookup_metadata_incomplete"},
		{name: "infinite duration", settings: publicationLyricsSettingsFixture{enabled: true}, metadata: lrclib.Metadata{Artist: "Artist", Title: "Title", Album: "Album", Duration: math.Inf(1)}, warning: "lyrics_lookup_metadata_incomplete"},
	} {
		t.Run(test.name, func(t *testing.T) {
			resolver := NewPublicationLyricsResolver(&test.settings, &test.lookup)
			result, err := resolver.Resolve(context.Background(), PublicationLyricsResolutionRequest{Metadata: test.metadata})
			if err != nil || result.Source != "none" || result.Lyrics != "" || result.SHA256 != "" || result.WarningCode != test.warning {
				t.Fatalf("optional acquisition failure = %+v, %v", result, err)
			}
		})
	}
}

func TestPublicationLyricsResolverDoesNotChooseAmbiguousSourceText(t *testing.T) {
	lookup := &publicationLyricsLookupFixture{}
	resolver := NewPublicationLyricsResolver(&publicationLyricsSettingsFixture{enabled: true}, lookup)
	result, err := resolver.Resolve(context.Background(), PublicationLyricsResolutionRequest{SelectedSourceTags: map[string][]string{"LYRICS": {"[00:01]One", "[00:02]Another"}}})
	if err != nil || result.WarningCode != "lyrics_source_selection_required" || result.Lyrics != "" || len(lookup.metadata) != 0 {
		t.Fatalf("ambiguous source resolution = %+v, %v", result, err)
	}
}

func TestPublicationLyricsResolverPropagatesOperationCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	lookup := &publicationLyricsLookupFixture{cancel: cancel, err: context.Canceled}
	resolver := NewPublicationLyricsResolver(&publicationLyricsSettingsFixture{enabled: true}, lookup)
	result, err := resolver.Resolve(ctx, PublicationLyricsResolutionRequest{Metadata: lrclib.Metadata{Artist: "Artist", Title: "Title", Album: "Album", Duration: 123}})
	if !errors.Is(err, context.Canceled) || result.Lyrics != "" || result.WarningCode != "" {
		t.Fatalf("operation cancellation became an optional warning: %+v, %v", result, err)
	}
}

func TestPublicationLyricsResolverDoesNotRequireFilledLocalMetadata(t *testing.T) {
	lookup := &publicationLyricsLookupFixture{}
	resolver := NewPublicationLyricsResolver(&publicationLyricsSettingsFixture{enabled: true}, lookup)
	metadata := lrclib.Metadata{Duration: 123}
	result, err := resolver.Resolve(context.Background(), PublicationLyricsResolutionRequest{Metadata: metadata})
	if err != nil || result.WarningCode != "" || len(lookup.metadata) != 1 || lookup.metadata[0] != metadata {
		t.Fatalf("explicit empty metadata was replaced or rejected: %+v, %+v, %v", result, lookup.metadata, err)
	}
}

func TestPublicationLyricsResolverHandlesUnavailableDependencies(t *testing.T) {
	metadata := lrclib.Metadata{Duration: 123}
	for _, test := range []struct {
		name     string
		resolver *PublicationLyricsResolver
		warning  string
	}{
		{name: "nil resolver", warning: "lyrics_settings_unavailable"},
		{name: "nil settings", resolver: NewPublicationLyricsResolver(nil, nil), warning: "lyrics_settings_unavailable"},
		{name: "nil lookup", resolver: NewPublicationLyricsResolver(&publicationLyricsSettingsFixture{enabled: true}, nil), warning: "lyrics_provider_unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := test.resolver.Resolve(context.Background(), PublicationLyricsResolutionRequest{Metadata: metadata})
			if err != nil || result.WarningCode != test.warning || result.Lyrics != "" {
				t.Fatalf("unavailable dependency resolution = %+v, %v", result, err)
			}
		})
	}
}

func TestPublicationLyricsResolverStopsIfSettingsReadIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	settings := &publicationLyricsSettingsFixture{enabled: true, cancel: cancel}
	lookup := &publicationLyricsLookupFixture{}
	resolver := NewPublicationLyricsResolver(settings, lookup)
	result, err := resolver.Resolve(ctx, PublicationLyricsResolutionRequest{Metadata: lrclib.Metadata{Duration: 123}})
	if !errors.Is(err, context.Canceled) || len(lookup.metadata) != 0 || result.WarningCode != "" {
		t.Fatalf("settings cancellation became optional absence: %+v, %v", result, err)
	}
}
