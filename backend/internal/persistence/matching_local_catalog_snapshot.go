package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// The approved shortlist is limited to ten releases. Selection hydration is
// deliberately targeted: callers may request only the small set they ranked,
// never the whole Local catalog.
const matchingLocalSelectionMaximumReleases = 10

// MatchingLocalSelectionHeader is the cheap first-read shape of one Local
// release. It contains no recording hydration: PositionCount is the number of
// present release positions, not a tracklist.
type MatchingLocalSelectionHeader struct {
	ProviderID     uuid.UUID               `json:"provider_id"`
	ReleaseID      uuid.UUID               `json:"release_id"`
	ProviderKey    string                  `json:"provider_key"`
	Title          string                  `json:"title"`
	Credits        []MatchingReleaseCredit `json:"credits"`
	ReleaseDate    string                  `json:"release_date,omitempty"`
	Disambiguation string                  `json:"disambiguation,omitempty"`
	CatalogNumbers []string                `json:"catalog_numbers"`
	PositionCount  int                     `json:"position_count"`
}

// MatchingLocalSelectionCatalog is a consistent header-only read of the Local
// catalog. Digest covers the complete header evidence set for later apply
// validation.
type MatchingLocalSelectionCatalog struct {
	Headers []MatchingLocalSelectionHeader `json:"headers"`
	Digest  string                         `json:"digest"`
}

// MatchingLocalSelectionSnapshot is the normalized two-stage matching view of
// explicitly selected Local releases. Releases use zero-based medium/position
// ordinals, matching MatchingRelease.
type MatchingLocalSelectionSnapshot struct {
	Releases []MatchingRelease `json:"releases"`
	Digest   string            `json:"digest"`
}

// MatchingLocalCatalogRepository is a narrow read-only Local selection adapter.
// It never writes, enqueues work, or reads source tags.
type MatchingLocalCatalogRepository struct{ db *bun.DB }

// NewMatchingLocalCatalogRepository returns a read-only Local selection adapter.
func NewMatchingLocalCatalogRepository(db *bun.DB) *MatchingLocalCatalogRepository {
	return &MatchingLocalCatalogRepository{db: db}
}

type matchingLocalSelectionHeaderRow struct {
	ID            uuid.UUID `bun:"id"`
	ProviderID    uuid.UUID `bun:"provider_id"`
	ProviderKey   string    `bun:"provider_key"`
	Title         string    `bun:"title"`
	Metadata      string    `bun:"metadata"`
	PositionCount int       `bun:"position_count"`
}

type matchingLocalSelectionCreditRow struct {
	ReleaseID      uuid.UUID `bun:"release_id"`
	Position       int       `bun:"position"`
	ArtistKey      string    `bun:"artist_key"`
	Name           string    `bun:"name"`
	NameJoinPhrase string    `bun:"name_join_phrase"`
}

type matchingLocalSelectionKeyRow struct {
	ID          uuid.UUID `bun:"id"`
	ProviderKey string    `bun:"provider_key"`
}

// ListMatchingLocalSelectionHeaders reads one consistent, cheap Local release
// header list. Release date and catalog number come only from the explicitly
// saved Local edition fields; missing values remain absent.
func (repository *MatchingLocalCatalogRepository) ListMatchingLocalSelectionHeaders(ctx context.Context) (MatchingLocalSelectionCatalog, error) {
	if repository == nil || repository.db == nil {
		return MatchingLocalSelectionCatalog{}, errors.New("matching local catalog snapshot is unavailable")
	}
	var result MatchingLocalSelectionCatalog
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		rows := make([]matchingLocalSelectionHeaderRow, 0)
		if err := tx.NewRaw(`SELECT r.id, r.provider_id, r.provider_key, r.title, r.metadata::text AS metadata,
			(SELECT count(*) FROM provider_release_track t
				WHERE t.provider_id=r.provider_id AND t.release_id=r.id AND t.present) AS position_count
			FROM provider_release r
			WHERE r.provider_id=?
			ORDER BY r.id`, localProviderID).Scan(ctx, &rows); err != nil {
			return fmt.Errorf("list matching local selection headers: %w", err)
		}
		credits := make([]matchingLocalSelectionCreditRow, 0)
		if len(rows) != 0 {
			if err := tx.NewRaw(`SELECT c.release_id, c.position, a.provider_key AS artist_key,
				COALESCE(NULLIF(c.credit_name,''), NULLIF(a.name,''), '') AS name,
				c.name_join_phrase
				FROM provider_release_artist_credit c
				JOIN provider_artist a ON a.id=c.artist_id
				WHERE c.provider_id=? AND c.present
				ORDER BY c.release_id, c.position`, localProviderID).Scan(ctx, &credits); err != nil {
				return fmt.Errorf("list matching local selection credits: %w", err)
			}
		}
		creditsByRelease := make(map[uuid.UUID][]MatchingReleaseCredit, len(rows))
		for _, row := range credits {
			creditsByRelease[row.ReleaseID] = append(creditsByRelease[row.ReleaseID], MatchingReleaseCredit{
				Position:          row.Position,
				ArtistProviderKey: row.ArtistKey,
				Name:              row.Name,
				NameJoinPhrase:    row.NameJoinPhrase,
			})
		}
		headers := make([]MatchingLocalSelectionHeader, 0, len(rows))
		for _, row := range rows {
			if row.ProviderKey == "" {
				return fmt.Errorf("list matching local selection headers: release %s has no provider key", row.ID)
			}
			releaseDate, catalogNumbers, disambiguation, err := matchingLocalSelectionEdition(row.Metadata)
			if err != nil {
				return fmt.Errorf("list matching local selection headers: %w", err)
			}
			headerCredits := creditsByRelease[row.ID]
			if headerCredits == nil {
				headerCredits = []MatchingReleaseCredit{}
			}
			headers = append(headers, MatchingLocalSelectionHeader{
				ProviderID:     row.ProviderID,
				ReleaseID:      row.ID,
				ProviderKey:    row.ProviderKey,
				Title:          row.Title,
				Credits:        headerCredits,
				ReleaseDate:    releaseDate,
				Disambiguation: disambiguation,
				CatalogNumbers: catalogNumbers,
				PositionCount:  row.PositionCount,
			})
		}
		sort.Slice(headers, func(i, j int) bool {
			return headers[i].ReleaseID.String() < headers[j].ReleaseID.String()
		})
		digest, err := matchingLocalSelectionDigest(headers)
		if err != nil {
			return fmt.Errorf("digest matching local selection headers: %w", err)
		}
		result = MatchingLocalSelectionCatalog{Headers: headers, Digest: digest}
		return nil
	})
	if err != nil {
		return MatchingLocalSelectionCatalog{}, err
	}
	return result, nil
}

// HydrateMatchingLocalSelection reads only the explicitly selected Local
// releases in one consistent transaction. Every selected release must still
// exist; a stale selection is an error rather than a partial layout.
func (repository *MatchingLocalCatalogRepository) HydrateMatchingLocalSelection(ctx context.Context, releaseIDs []uuid.UUID) (MatchingLocalSelectionSnapshot, error) {
	if repository == nil || repository.db == nil {
		return MatchingLocalSelectionSnapshot{}, errors.New("matching local catalog snapshot is unavailable")
	}
	ids, err := matchingLocalSelectionIDs(releaseIDs)
	if err != nil {
		return MatchingLocalSelectionSnapshot{}, err
	}
	var result MatchingLocalSelectionSnapshot
	err = repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		keys := make([]matchingLocalSelectionKeyRow, 0, len(ids))
		if err := tx.NewRaw(`SELECT id, provider_key FROM provider_release WHERE provider_id=? AND id IN (?)`,
			localProviderID, bun.List(ids)).Scan(ctx, &keys); err != nil {
			return fmt.Errorf("hydrate matching local selection: read release keys: %w", err)
		}
		releaseKeys := make(map[uuid.UUID]string, len(keys))
		for _, row := range keys {
			releaseKeys[row.ID] = row.ProviderKey
		}
		localReleases := make([]MatchingLocalRelease, 0, len(ids))
		for _, id := range ids {
			local, err := loadMatchingProviderRelease(ctx, tx, id, false)
			if err != nil {
				return fmt.Errorf("hydrate matching local selection: %w", err)
			}
			if local == nil {
				return fmt.Errorf("hydrate matching local selection: release %s is unavailable", id)
			}
			if releaseKeys[id] == "" {
				return fmt.Errorf("hydrate matching local selection: release %s has no provider key", id)
			}
			localReleases = append(localReleases, *local)
		}
		artistIDs := make([]uuid.UUID, 0)
		recordingIDs := make([]uuid.UUID, 0)
		for _, release := range localReleases {
			for _, credit := range release.Artists {
				artistIDs = append(artistIDs, credit.ProviderArtistID)
			}
			for _, track := range release.Tracks {
				recordingIDs = append(recordingIDs, track.RecordingID)
			}
		}
		artistKeys, err := matchingLocalSelectionKeys(ctx, tx, "artist", `SELECT id, provider_key FROM provider_artist WHERE id IN (?)`, artistIDs)
		if err != nil {
			return fmt.Errorf("hydrate matching local selection: %w", err)
		}
		recordingKeys, err := matchingLocalSelectionKeys(ctx, tx, "recording", `SELECT id, provider_key FROM provider_recording WHERE id IN (?)`, recordingIDs)
		if err != nil {
			return fmt.Errorf("hydrate matching local selection: %w", err)
		}
		releases := make([]MatchingRelease, 0, len(localReleases))
		for _, local := range localReleases {
			release, err := matchingLocalSelectionLayout(local, releaseKeys[local.ID], artistKeys, recordingKeys)
			if err != nil {
				return fmt.Errorf("hydrate matching local selection: %w", err)
			}
			releases = append(releases, release)
		}
		sort.Slice(releases, func(i, j int) bool {
			return releases[i].ProviderKey < releases[j].ProviderKey
		})
		digest, err := matchingLocalSelectionDigest(releases)
		if err != nil {
			return fmt.Errorf("digest matching local selection layouts: %w", err)
		}
		result = MatchingLocalSelectionSnapshot{Releases: releases, Digest: digest}
		return nil
	})
	if err != nil {
		return MatchingLocalSelectionSnapshot{}, err
	}
	return result, nil
}

func matchingLocalSelectionIDs(values []uuid.UUID) ([]uuid.UUID, error) {
	if len(values) == 0 || len(values) > matchingLocalSelectionMaximumReleases {
		return nil, fmt.Errorf("matching local selection requires between 1 and %d release ids", matchingLocalSelectionMaximumReleases)
	}
	seen := make(map[uuid.UUID]struct{}, len(values))
	ids := make([]uuid.UUID, 0, len(values))
	for _, value := range values {
		if value == uuid.Nil {
			return nil, errors.New("matching local selection release id is invalid")
		}
		if _, duplicate := seen[value]; duplicate {
			return nil, errors.New("matching local selection release ids must be unique")
		}
		seen[value] = struct{}{}
		ids = append(ids, value)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	return ids, nil
}

func matchingLocalSelectionKeys(ctx context.Context, tx bun.Tx, kind, query string, values []uuid.UUID) (map[uuid.UUID]string, error) {
	unique := make([]uuid.UUID, 0, len(values))
	seen := make(map[uuid.UUID]struct{}, len(values))
	for _, value := range values {
		if value == uuid.Nil {
			return nil, errors.New("matching local selection related identity is invalid")
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	keys := make(map[uuid.UUID]string, len(unique))
	if len(unique) == 0 {
		return keys, nil
	}
	sort.Slice(unique, func(i, j int) bool { return unique[i].String() < unique[j].String() })
	rows := make([]matchingLocalSelectionKeyRow, 0, len(unique))
	if err := tx.NewRaw(query, bun.List(unique)).Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("read %s provider keys: %w", kind, err)
	}
	for _, row := range rows {
		keys[row.ID] = row.ProviderKey
	}
	return keys, nil
}

// matchingLocalSelectionLayout normalizes the existing Local release graph for
// two-stage matching. The provider-track identity is preserved, positions are
// converted to the zero-based ordinals used by MatchingRelease, and a track
// title/duration override falls back to the recording value.
func matchingLocalSelectionLayout(local MatchingLocalRelease, providerKey string, artistKeys, recordingKeys map[uuid.UUID]string) (MatchingRelease, error) {
	if local.ID == uuid.Nil || providerKey == "" {
		return MatchingRelease{}, errors.New("matching local selection release identity is invalid")
	}
	release := MatchingRelease{
		ID:            local.ID,
		ProviderKey:   providerKey,
		Name:          local.Title,
		Fields:        json.RawMessage(`{}`),
		FieldEvidence: map[string]ProviderFieldEvidence{},
		RawSource:     json.RawMessage(`{}`),
		Complete:      true,
		Credits:       make([]MatchingReleaseCredit, 0, len(local.Artists)),
		Tracks:        make([]MatchingReleaseTrack, 0, len(local.Tracks)),
	}
	for _, credit := range local.Artists {
		artistKey := artistKeys[credit.ProviderArtistID]
		if artistKey == "" {
			return MatchingRelease{}, fmt.Errorf("release credit artist %s is unavailable", credit.ProviderArtistID)
		}
		name := credit.Name
		if name == "" {
			name = credit.ArtistName
		}
		release.Credits = append(release.Credits, MatchingReleaseCredit{
			Position:          credit.Position,
			ArtistProviderKey: artistKey,
			Name:              name,
			NameJoinPhrase:    credit.JoinPhrase,
		})
	}
	for _, track := range local.Tracks {
		if track.ProviderTrackID == uuid.Nil || track.Medium < 1 || track.Position < 1 {
			return MatchingRelease{}, errors.New("matching local selection release layout is invalid")
		}
		recordingKey := recordingKeys[track.RecordingID]
		if recordingKey == "" {
			return MatchingRelease{}, fmt.Errorf("release recording %s is unavailable", track.RecordingID)
		}
		duration := track.DurationOverrideMS
		if duration == nil {
			duration = track.RecordingDurationMS
		}
		// RecordingArtists intentionally includes every recording credit,
		// including artists owned by another provider. MatchingRelease carries
		// only their display names at this boundary.
		artists := make([]string, 0, len(track.RecordingCredits))
		for _, credit := range track.RecordingCredits {
			name := credit.Name
			if name == "" {
				name = credit.ArtistName
			}
			if name != "" {
				artists = append(artists, name)
			}
		}
		release.Tracks = append(release.Tracks, MatchingReleaseTrack{
			ID:                   track.ProviderTrackID,
			Medium:               track.Medium - 1,
			Position:             track.Position - 1,
			DisplayedNumber:      track.DisplayedNumber,
			RecordingProviderKey: recordingKey,
			RecordingTitle:       track.Title,
			RecordingArtists:     artists,
			RecordingLengthMS:    duration,
			ReleaseLengthMS:      duration,
		})
	}
	return release, nil
}

func matchingLocalSelectionEdition(raw string) (string, []string, string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", []string{}, "", nil
	}
	var metadata struct {
		Edition LocalProviderEdition `json:"edition"`
	}
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return "", nil, "", fmt.Errorf("decode local release metadata: %w", err)
	}
	edition := metadata.Edition
	catalogNumbers := []string{}
	if catalogNumber := strings.TrimSpace(edition.CatalogNumber); catalogNumber != "" {
		catalogNumbers = append(catalogNumbers, catalogNumber)
	}
	return strings.TrimSpace(edition.ReleaseDate), catalogNumbers, strings.TrimSpace(edition.Disambiguation), nil
}

func matchingLocalSelectionDigest(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}
