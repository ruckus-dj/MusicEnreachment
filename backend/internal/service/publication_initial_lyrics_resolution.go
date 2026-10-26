package service

import (
	"context"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/lrclib"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// resolveInitialPublicationLyrics classifies frozen local authority before
// consulting automatic sources. The supplied source tags and lookup metadata
// must come from the held selected source; this function never applies a local
// manual value to the output.
func resolveInitialPublicationLyrics(
	ctx context.Context,
	resolver *PublicationLyricsResolver,
	authority persistence.PublicationInitialLyricsInput,
	preparedSourceTags map[string][]string,
	lookupMetadata lrclib.Metadata,
) (persistence.PublicationInitialLyricsAcquisitionRecord, error) {
	record := persistence.PublicationInitialLyricsAcquisitionRecord{
		Version:   persistence.PublicationInitialLyricsVersion,
		Authority: authority,
		Source:    "none",
	}

	field := authority.Desired
	if field != nil && field.State != librarymetadata.StateAbsent && (field.Authority == librarymetadata.AuthorityManual || field.State == librarymetadata.StateExplicitEmpty) {
		return record, nil
	}

	resolution, err := resolver.Resolve(ctx, PublicationLyricsResolutionRequest{
		SelectedSourceTags: cloneTagMap(preparedSourceTags),
		Metadata:           lookupMetadata,
	})
	if err != nil {
		return persistence.PublicationInitialLyricsAcquisitionRecord{}, err
	}
	record.Source = resolution.Source
	record.Lyrics = resolution.Lyrics
	record.SHA256 = resolution.SHA256
	record.WarningCode = resolution.WarningCode
	return record, nil
}
