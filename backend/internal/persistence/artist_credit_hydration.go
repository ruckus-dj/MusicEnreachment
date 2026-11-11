package persistence

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/uptrace/bun"
)

type hydratedArtistIdentity struct {
	ID               uuid.UUID       `bun:"id,type:uuid"`
	Name             string          `bun:"name"`
	Metadata         json.RawMessage `bun:"metadata,type:jsonb"`
	MetadataRevision int64           `bun:"metadata_revision"`
}

// hydrateArtistCredits resolves identity fields in one batch. Submitted names
// and MBIDs are deliberately ignored as identity sources; edit-specific credit
// text, order, roles, and join phrases remain as supplied.
func hydrateArtistCredits(ctx context.Context, tx bun.IDB, credits []librarymetadata.ArtistCredit) ([]librarymetadata.ArtistCredit, error) {
	ids := make([]uuid.UUID, 0, len(credits))
	seen := make(map[uuid.UUID]bool, len(credits))
	for _, credit := range credits {
		id, err := uuid.Parse(credit.ArtistID)
		if err != nil || id == uuid.Nil {
			return nil, ErrLibraryMetadataArtistNotFound
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return []librarymetadata.ArtistCredit{}, nil
	}
	var rows []hydratedArtistIdentity
	if err := tx.NewRaw(`SELECT id,name,metadata,metadata_revision FROM local_artist WHERE id IN (?) ORDER BY id`, bun.List(ids)).Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("hydrate artist credit identities: %w", err)
	}
	if len(rows) != len(ids) {
		return nil, ErrLibraryMetadataArtistNotFound
	}
	mbids, err := loadMusicBrainzArtistMBIDs(ctx, tx, ids)
	if err != nil {
		return nil, fmt.Errorf("hydrate artist credit MBIDs: %w", err)
	}
	disambiguations, err := firstPublicationProviderArtistDisambiguations(ctx, tx, ids)
	if err != nil {
		return nil, fmt.Errorf("hydrate artist credit disambiguations: %w", err)
	}
	identities := make(map[uuid.UUID]hydratedArtistIdentity, len(rows))
	for _, row := range rows {
		identities[row.ID] = row
	}
	result := make([]librarymetadata.ArtistCredit, len(credits))
	for index, input := range credits {
		id, _ := uuid.Parse(input.ArtistID)
		identity := identities[id]
		input.ArtistName = identity.Name
		input.MetadataRevision = identity.MetadataRevision
		input.MBID = nil
		if mbid := mbids[id]; mbid != nil {
			value := mbid.String()
			input.MBID = &value
		}
		input.SortName = ""
		localFields, decodeErr := decodeFrozenObject(identity.Metadata)
		if decodeErr != nil {
			return nil, fmt.Errorf("decode local artist identity: %w", decodeErr)
		}
		localDisambiguation, localDisambiguationPresent, decodeErr := acceptedProviderString(localFields, "disambiguation")
		if decodeErr != nil {
			return nil, fmt.Errorf("decode local artist disambiguation: %w", decodeErr)
		}
		input.Disambiguation = localDisambiguation
		if !localDisambiguationPresent {
			input.Disambiguation = disambiguations[id]
		}
		artistMetadata, decodeErr := decodeFirstPublicationEnvelopeValue(identity.Metadata, librarymetadata.ScopeArtist)
		if decodeErr != nil {
			return nil, fmt.Errorf("decode local artist metadata: %w", decodeErr)
		}
		if sortName, exists := artistMetadata.Desired["sort_name"]; exists && sortName.State == librarymetadata.StateValue {
			input.SortName, _ = sortName.Value.(string)
		}
		result[index] = input
	}
	return result, nil
}
