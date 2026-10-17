package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

var ErrLibraryReleaseNotFound = errors.New("library release not found")

type LibraryReleaseDetail struct {
	ID                      uuid.UUID
	Title                   string
	Metadata                json.RawMessage
	MetadataVersion         int
	MetadataRevision        int64
	PrimaryArtistID         *uuid.UUID
	PrimaryArtistCandidates []uuid.UUID
	PrimaryArtistMBIDs      map[uuid.UUID]*uuid.UUID
	Artists                 []LibraryArtistCredit
	Tracks                  []LibraryReleaseTrackDetail
}

type LibraryArtistCredit struct {
	ArtistID   uuid.UUID
	ArtistName *string
	CreditName string
	JoinPhrase string
}

type LibraryReleaseTrackDetail struct {
	ID                        uuid.UUID
	LocalTrackID              uuid.UUID
	RecordingID               *uuid.UUID
	Medium                    int
	Position                  int
	DisplayedNumber           string
	Title                     string
	Metadata                  json.RawMessage
	MetadataVersion           int
	MetadataRevision          int64
	TrackMetadataRevision     int64
	RecordingMetadataRevision *int64
	PrimaryArtistID           *uuid.UUID
	PrimaryArtistCandidates   []uuid.UUID
	PrimaryArtistMBIDs        map[uuid.UUID]*uuid.UUID
	Artists                   []LibraryArtistCredit
	Sources                   []LibraryTrackSourceDetail
}

type LibraryTrackSourceDetail struct {
	MediaVariantID uuid.UUID
	Selected       bool
	Compatible     bool
	DecisionMethod string
	PolicyVersion  *string
	Metadata       json.RawMessage
	Locations      []LibrarySourceLocationDetail
}

type LibrarySourceLocationDetail struct {
	RootID       uuid.UUID
	LocationID   uuid.UUID
	RelativePath string
	SizeBytes    int64
	Mtime        sql.NullTime
	ProbeStatus  string
	RootStatus   string
	RootEnabled  bool
}

type LibraryRepository struct {
	db bun.IDB
}

func NewLibraryRepository(db *bun.DB) *LibraryRepository {
	return &LibraryRepository{db: db}
}

// ReadReleaseDetail reads all local release detail in one read-only snapshot.
// Related rows are batch-loaded so releases with many positions do not cause
// per-track queries.
func (repository *LibraryRepository) ReadReleaseDetail(ctx context.Context, id uuid.UUID) (*LibraryReleaseDetail, error) {
	detail := new(LibraryReleaseDetail)
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		result := new(LibraryReleaseDetail)
		err := tx.NewRaw(`SELECT id,title,metadata,metadata_version,metadata_revision,primary_artist_id FROM local_release WHERE id=?`, id).
			Scan(ctx, &result.ID, &result.Title, &result.Metadata, &result.MetadataVersion, &result.MetadataRevision, &result.PrimaryArtistID)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read library release: %w", ErrLibraryReleaseNotFound)
		}
		if err != nil {
			return fmt.Errorf("read library release: %w", err)
		}

		result.Artists = make([]LibraryArtistCredit, 0)
		if err := tx.NewRaw(`SELECT credit.artist_id,artist.name AS artist_name,credit.name AS credit_name,credit.name_join_phrase AS join_phrase
				FROM release_artist credit LEFT JOIN local_artist artist ON artist.id=credit.artist_id
				WHERE credit.release_id=? ORDER BY credit.position`, id).
			Scan(ctx, &result.Artists); err != nil {
			return fmt.Errorf("read library release artists: %w", err)
		}
		if err := tx.NewRaw(`SELECT DISTINCT artist_id FROM release_artist WHERE release_id=? ORDER BY artist_id`, id).Scan(ctx, &result.PrimaryArtistCandidates); err != nil {
			return fmt.Errorf("read library release primary artist candidates: %w", err)
		}

		result.Tracks = make([]LibraryReleaseTrackDetail, 0)
		if err := tx.NewRaw(`SELECT rt.id,rt.track_id AS local_track_id,rt.recording_id,rt.medium,rt.position,rt.displayed_number,
				track.title,track.metadata,track.metadata_version,rt.metadata_revision,track.metadata_revision AS track_metadata_revision,
				recording.metadata_revision AS recording_metadata_revision,rt.primary_artist_id
				FROM release_track rt JOIN local_track track ON track.id=rt.track_id LEFT JOIN local_recording recording ON recording.id=rt.recording_id
			WHERE rt.release_id=? ORDER BY rt.medium,rt.position`, id).Scan(ctx, &result.Tracks); err != nil {
			return fmt.Errorf("read library release tracks: %w", err)
		}
		if len(result.Tracks) == 0 {
			result.PrimaryArtistMBIDs, err = loadMusicBrainzArtistMBIDs(ctx, tx, result.PrimaryArtistCandidates)
			if err != nil {
				return fmt.Errorf("read library primary artist identities: %w", err)
			}
			*detail = *result
			return nil
		}
		trackIDs := make([]uuid.UUID, 0, len(result.Tracks))
		for i := range result.Tracks {
			trackIDs = append(trackIDs, result.Tracks[i].LocalTrackID)
			result.Tracks[i].Medium++
			result.Tracks[i].Position++
			result.Tracks[i].Artists = make([]LibraryArtistCredit, 0)
			result.Tracks[i].Sources = make([]LibraryTrackSourceDetail, 0)
		}
		var positionArtists []struct {
			ReleaseTrackID uuid.UUID `bun:"release_track_id"`
			ArtistID       uuid.UUID `bun:"artist_id"`
		}
		if err := tx.NewRaw(`SELECT DISTINCT rt.id AS release_track_id,credit.artist_id FROM release_track rt
				JOIN track_artist credit ON credit.track_id=rt.track_id WHERE rt.release_id=? ORDER BY rt.id,credit.artist_id`, id).Scan(ctx, &positionArtists); err != nil {
			return fmt.Errorf("read library position primary artist candidates: %w", err)
		}
		positionIndex := make(map[uuid.UUID]int, len(result.Tracks))
		for i := range result.Tracks {
			positionIndex[result.Tracks[i].ID] = i
		}
		for _, candidate := range positionArtists {
			index := positionIndex[candidate.ReleaseTrackID]
			result.Tracks[index].PrimaryArtistCandidates = append(result.Tracks[index].PrimaryArtistCandidates, candidate.ArtistID)
		}
		artistIDs := make([]uuid.UUID, 0, len(result.PrimaryArtistCandidates)+len(positionArtists))
		seenArtistIDs := make(map[uuid.UUID]struct{}, cap(artistIDs))
		addArtistIDs := func(candidates []uuid.UUID) {
			for _, artistID := range candidates {
				if _, exists := seenArtistIDs[artistID]; exists {
					continue
				}
				seenArtistIDs[artistID] = struct{}{}
				artistIDs = append(artistIDs, artistID)
			}
		}
		addArtistIDs(result.PrimaryArtistCandidates)
		for i := range result.Tracks {
			addArtistIDs(result.Tracks[i].PrimaryArtistCandidates)
		}
		artistMBIDs, err := loadMusicBrainzArtistMBIDs(ctx, tx, artistIDs)
		if err != nil {
			return fmt.Errorf("read library primary artist identities: %w", err)
		}
		result.PrimaryArtistMBIDs = artistMBIDs
		for i := range result.Tracks {
			result.Tracks[i].PrimaryArtistMBIDs = artistMBIDs
		}

		var credits []struct {
			TrackID uuid.UUID `bun:"track_id"`
			LibraryArtistCredit
		}
		if err := tx.NewRaw(`SELECT credit.track_id,credit.artist_id,artist.name AS artist_name,credit.name AS credit_name,
			credit.name_join_phrase AS join_phrase FROM track_artist credit LEFT JOIN local_artist artist ON artist.id=credit.artist_id
			WHERE credit.track_id IN (?) ORDER BY credit.track_id,credit.position`, bun.List(trackIDs)).Scan(ctx, &credits); err != nil {
			return fmt.Errorf("read library release track artists: %w", err)
		}
		trackIndex := make(map[uuid.UUID][]int, len(result.Tracks))
		for i := range result.Tracks {
			trackIndex[result.Tracks[i].LocalTrackID] = append(trackIndex[result.Tracks[i].LocalTrackID], i)
		}
		for _, credit := range credits {
			for _, index := range trackIndex[credit.TrackID] {
				result.Tracks[index].Artists = append(result.Tracks[index].Artists, credit.LibraryArtistCredit)
			}
		}

		var sources []struct {
			TrackID uuid.UUID `bun:"track_id"`
			LibraryTrackSourceDetail
		}
		if err := tx.NewRaw(`SELECT track_id,media_variant_id,selected,compatible,decision_method,policy_version,metadata
			FROM track_source WHERE track_id IN (?) ORDER BY track_id,media_variant_id`, bun.List(trackIDs)).Scan(ctx, &sources); err != nil {
			return fmt.Errorf("read library release track sources: %w", err)
		}
		variantIDs := make([]uuid.UUID, 0, len(sources))
		sourceByVariant := make(map[uuid.UUID][]*LibraryTrackSourceDetail, len(sources))
		for _, source := range sources {
			variantIDs = append(variantIDs, source.MediaVariantID)
			source.Locations = make([]LibrarySourceLocationDetail, 0)
			for _, index := range trackIndex[source.TrackID] {
				track := &result.Tracks[index]
				track.Sources = append(track.Sources, source.LibraryTrackSourceDetail)
			}
		}
		for i := range result.Tracks {
			for j := range result.Tracks[i].Sources {
				source := &result.Tracks[i].Sources[j]
				sourceByVariant[source.MediaVariantID] = append(sourceByVariant[source.MediaVariantID], source)
			}
		}

		if len(variantIDs) > 0 {
			var locations []struct {
				MediaVariantID uuid.UUID `bun:"media_variant_id"`
				LibrarySourceLocationDetail
			}
			if err := tx.NewRaw(`SELECT location.media_variant_id,root.id AS root_id,location.id AS location_id,
				location.relative_path,location.size_bytes,location.mtime,location.probe_status,root.status AS root_status,root.enabled AS root_enabled
				FROM source_location location JOIN source_root root ON root.id=location.source_root_id
				WHERE location.media_variant_id IN (?)
				ORDER BY location.media_variant_id,root.id,location.relative_path,location.id`, bun.List(variantIDs)).Scan(ctx, &locations); err != nil {
				return fmt.Errorf("read library release source locations: %w", err)
			}
			for _, location := range locations {
				for _, source := range sourceByVariant[location.MediaVariantID] {
					source.Locations = append(source.Locations, location.LibrarySourceLocationDetail)
				}
			}
		}
		*detail = *result
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read library release detail: %w", err)
	}
	return detail, nil
}

func loadMusicBrainzArtistMBIDs(ctx context.Context, tx bun.Tx, artistIDs []uuid.UUID) (map[uuid.UUID]*uuid.UUID, error) {
	result := make(map[uuid.UUID]*uuid.UUID, len(artistIDs))
	if len(artistIDs) == 0 {
		return result, nil
	}
	var mappings []struct {
		ArtistID    uuid.UUID `bun:"local_artist_id"`
		ProviderKey string    `bun:"provider_key"`
	}
	if err := tx.NewRaw(`SELECT link.local_artist_id,provider_artist.provider_key
		FROM provider_local_artist link
		JOIN provider ON provider.id=link.provider_id AND provider.code='musicbrainz'
		JOIN provider_artist ON provider_artist.id=link.provider_artist_id AND provider_artist.provider_id=link.provider_id
		WHERE link.local_artist_id IN (?) ORDER BY link.local_artist_id,provider_artist.provider_key`, bun.List(artistIDs)).Scan(ctx, &mappings); err != nil {
		return nil, err
	}
	keysByArtist := make(map[uuid.UUID]map[string]struct{}, len(mappings))
	for _, mapping := range mappings {
		keys := keysByArtist[mapping.ArtistID]
		if keys == nil {
			keys = make(map[string]struct{})
			keysByArtist[mapping.ArtistID] = keys
		}
		keys[mapping.ProviderKey] = struct{}{}
	}
	for _, artistID := range artistIDs {
		keys := keysByArtist[artistID]
		if len(keys) != 1 {
			continue
		}
		for key := range keys {
			mbid, err := uuid.Parse(key)
			if err != nil || !strings.EqualFold(key, mbid.String()) {
				continue
			}
			value := mbid
			result[artistID] = &value
		}
	}
	return result, nil
}
