package persistence

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

// LibraryArtistPhotoBinding is the minimal identity a photo lookup needs: whether
// the local artist exists and every MusicBrainz provider key linked to it.
type LibraryArtistPhotoBinding struct {
	ArtistExists bool
	ProviderKeys []string
}

// ReadLibraryArtistPhotoBinding reads only the MusicBrainz bindings of one local
// artist. It performs no provider I/O and does not load catalog, audio or
// release data, so a per-artist photo request stays cheap.
func (repository *LibraryRepository) ReadLibraryArtistPhotoBinding(ctx context.Context, artistID uuid.UUID) (LibraryArtistPhotoBinding, error) {
	var binding LibraryArtistPhotoBinding
	var keysJSON string
	err := repository.db.NewRaw(`SELECT EXISTS(SELECT 1 FROM local_artist WHERE id=?),
		COALESCE((SELECT jsonb_agg(DISTINCT provider_artist.provider_key)
			FROM provider_local_artist link
			JOIN provider ON provider.id=link.provider_id AND provider.code='musicbrainz'
			JOIN provider_artist ON provider_artist.id=link.provider_artist_id AND provider_artist.provider_id=link.provider_id
			WHERE link.local_artist_id=?), '[]'::jsonb)::text`, artistID, artistID).
		Scan(ctx, &binding.ArtistExists, &keysJSON)
	if err != nil {
		return LibraryArtistPhotoBinding{}, fmt.Errorf("read library artist photo binding: %w", err)
	}
	if err := json.Unmarshal([]byte(keysJSON), &binding.ProviderKeys); err != nil {
		return LibraryArtistPhotoBinding{}, fmt.Errorf("decode library artist photo binding: %w", err)
	}
	return binding, nil
}
