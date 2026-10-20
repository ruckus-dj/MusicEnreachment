package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"strings"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/lrclib"
)

type publicationLyricsSettings interface {
	GetLRCLIBEnabled(context.Context) (bool, error)
}

// PublicationLyricsLookup deliberately excludes Search: automatic acquisition
// may request only the exact metadata lookup, never choose an alternative match.
type PublicationLyricsLookup interface {
	Lookup(context.Context, lrclib.Metadata) (lrclib.LookupResult, error)
}

type PublicationLyricsResolver struct {
	settings publicationLyricsSettings
	lookup   PublicationLyricsLookup
}

// PublicationLyricsResolutionRequest must contain tags read from the held,
// selected audio source, not another linked source or pending manual metadata.
// Duration is already expressed in the exact lookup client's units; the resolver
// does not invent rounding, retiming or missing metadata.
type PublicationLyricsResolutionRequest struct {
	SelectedSourceTags map[string][]string
	Metadata           lrclib.Metadata
}

// PublicationLyricsResolution is acquisition evidence, not a publication receipt.
// The stage writer still must validate the actual embedded bytes independently.
type PublicationLyricsResolution struct {
	Source      string
	Lyrics      string
	SHA256      string
	WarningCode string
}

func NewPublicationLyricsResolver(settings publicationLyricsSettings, lookup PublicationLyricsLookup) *PublicationLyricsResolver {
	return &PublicationLyricsResolver{settings: settings, lookup: lookup}
}

func (resolver *PublicationLyricsResolver) Resolve(ctx context.Context, request PublicationLyricsResolutionRequest) (PublicationLyricsResolution, error) {
	if err := ctx.Err(); err != nil {
		return PublicationLyricsResolution{}, err
	}
	versions := make(map[string]struct{})
	for key, values := range request.SelectedSourceTags {
		if !strings.EqualFold(strings.TrimSpace(key), "LYRICS") {
			continue
		}
		for _, value := range values {
			lyrics, err := lrclib.ValidateSyncedLyrics(value)
			if err == nil && lyrics != "" {
				versions[lyrics] = struct{}{}
			}
		}
	}
	if len(versions) > 1 {
		// The bounded automatic capability cannot choose a language/version from
		// multiple synced texts. Do not hide that ambiguity with a provider choice.
		return PublicationLyricsResolution{Source: "none", WarningCode: "lyrics_source_selection_required"}, nil
	}
	for lyrics := range versions {
		return resolvedPublicationLyrics("source", lyrics), nil
	}

	if resolver == nil || resolver.settings == nil {
		return PublicationLyricsResolution{Source: "none", WarningCode: "lyrics_settings_unavailable"}, nil
	}
	enabled, err := resolver.settings.GetLRCLIBEnabled(ctx)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return PublicationLyricsResolution{}, ctxErr
	}
	if err != nil {
		return PublicationLyricsResolution{Source: "none", WarningCode: "lyrics_settings_unavailable"}, nil
	}
	if !enabled {
		return PublicationLyricsResolution{Source: "none"}, nil
	}
	metadata := request.Metadata
	if metadata.Duration <= 0 || math.IsNaN(metadata.Duration) || math.IsInf(metadata.Duration, 0) {
		return PublicationLyricsResolution{Source: "none", WarningCode: "lyrics_lookup_metadata_incomplete"}, nil
	}
	if resolver.lookup == nil {
		return PublicationLyricsResolution{Source: "none", WarningCode: "lyrics_provider_unavailable"}, nil
	}
	result, err := resolver.lookup.Lookup(ctx, metadata)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return PublicationLyricsResolution{}, ctxErr
	}
	if err != nil {
		return PublicationLyricsResolution{Source: "none", WarningCode: "lyrics_lookup_failed"}, nil
	}
	if !result.Found || result.Track.Instrumental {
		return PublicationLyricsResolution{Source: "none"}, nil
	}
	lyrics, err := lrclib.ValidateSyncedLyrics(result.Lyrics)
	if err != nil || lyrics == "" {
		return PublicationLyricsResolution{Source: "none", WarningCode: "lyrics_lookup_invalid_synced_text"}, nil
	}
	return resolvedPublicationLyrics("lrclib", lyrics), nil
}

func resolvedPublicationLyrics(source, lyrics string) PublicationLyricsResolution {
	digest := sha256.Sum256([]byte(lyrics))
	return PublicationLyricsResolution{Source: source, Lyrics: lyrics, SHA256: hex.EncodeToString(digest[:])}
}
