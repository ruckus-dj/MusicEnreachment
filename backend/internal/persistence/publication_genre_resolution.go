package persistence

import (
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
)

// resolvePublicationGenreReferences applies the shared recording, release,
// then recording-artist precedence. Artist references are unioned in credit and
// source order, deduplicating by canonical genre MBID.
func resolvePublicationGenreReferences(recording, release []librarymetadata.GenreReference, artists [][]librarymetadata.GenreReference) ([]librarymetadata.GenreReference, librarymetadata.Scope) {
	if len(recording) != 0 {
		return append([]librarymetadata.GenreReference(nil), recording...), librarymetadata.ScopeRecording
	}
	if len(release) != 0 {
		return append([]librarymetadata.GenreReference(nil), release...), librarymetadata.ScopeRelease
	}
	var result []librarymetadata.GenreReference
	seen := make(map[string]struct{})
	for _, artistGenres := range artists {
		for _, genre := range artistGenres {
			id, err := uuid.Parse(genre.ID)
			if err != nil || id == uuid.Nil {
				continue
			}
			key := strings.ToLower(id.String())
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			genre.ID = id.String()
			result = append(result, genre)
		}
	}
	if len(result) == 0 {
		return nil, ""
	}
	return result, librarymetadata.ScopeArtist
}

// resolvePublicationRecordingArtistGenres applies per-artist local metadata
// before accepted provider artist genres, retaining recording credit order.
func resolvePublicationRecordingArtistGenres(artists []FirstPublicationGenreArtist, providerGenres map[string][]librarymetadata.GenreReference, providerCreditOrder []string) ([]librarymetadata.GenreReference, bool, error) {
	artistGenres := make([][]librarymetadata.GenreReference, 0, len(artists)+len(providerCreditOrder))
	seenArtists := make(map[string]struct{}, len(artists))
	explicitEmpty := false
	for _, artist := range artists {
		key := ""
		if artist.MBID != nil {
			if id, err := uuid.Parse(*artist.MBID); err == nil && id != uuid.Nil {
				key = strings.ToLower(id.String())
				seenArtists[key] = struct{}{}
			}
		}
		field, exists := artist.Metadata.Desired["genre"]
		if exists && field.State == librarymetadata.StateExplicitEmpty {
			explicitEmpty = true
			artistGenres = append(artistGenres, nil)
			continue
		}
		if exists && field.State == librarymetadata.StateValue {
			localGenres, err := genreReferencesFromField(field)
			if err != nil {
				return nil, false, err
			}
			if len(localGenres) != 0 {
				artistGenres = append(artistGenres, localGenres)
				continue
			}
		}
		artistGenres = append(artistGenres, providerGenres[key])
	}
	for _, key := range providerCreditOrder {
		if _, hasLocalCredit := seenArtists[key]; hasLocalCredit {
			continue
		}
		artistGenres = append(artistGenres, providerGenres[key])
	}
	genres, _ := resolvePublicationGenreReferences(nil, nil, artistGenres)
	return genres, explicitEmpty && len(genres) == 0, nil
}
