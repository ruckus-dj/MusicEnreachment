package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type MatchingLocalReleaseCatalog struct {
	Releases []MatchingLocalRelease `json:"releases"`
}

func (repository *IncomingMatchCandidateRepository) ListLocalReleases(ctx context.Context) (MatchingLocalReleaseCatalog, error) {
	var ids []uuid.UUID
	err := repository.db.NewRaw(`SELECT id FROM provider_release WHERE provider_id=? ORDER BY title,id`, localProviderID).Scan(ctx, &ids)
	if err != nil {
		return MatchingLocalReleaseCatalog{}, fmt.Errorf("list local releases: %w", err)
	}
	result := MatchingLocalReleaseCatalog{Releases: make([]MatchingLocalRelease, 0, len(ids))}
	for _, id := range ids {
		release, err := loadMatchingProviderRelease(ctx, repository.db, id, false)
		if err != nil {
			return MatchingLocalReleaseCatalog{}, fmt.Errorf("list local releases: %w", err)
		}
		if release != nil {
			result.Releases = append(result.Releases, *release)
		}
	}
	return result, nil
}

func loadMatchingProviderRelease(ctx context.Context, db bun.IDB, id uuid.UUID, locking bool) (*MatchingLocalRelease, error) {
	if id == uuid.Nil {
		return nil, errors.New("local provider release id is invalid")
	}
	var release MatchingLocalRelease
	query := `SELECT id,title FROM provider_release WHERE id=? AND provider_id=?`
	if locking {
		query += ` FOR UPDATE`
	}
	if err := db.NewRaw(query, id, localProviderID).Scan(ctx, &release.ID, &release.Title); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	release.Artists = make([]MatchingLocalArtist, 0)
	creditQuery := `SELECT credit.position,artist.provider_id,artist.id AS provider_artist_id,credit.credit_name,artist.name AS artist_name,credit.name_join_phrase AS join_phrase FROM provider_release_artist_credit credit
		JOIN provider_artist artist ON artist.id=credit.artist_id
		WHERE credit.provider_id=? AND credit.release_id=? AND credit.present ORDER BY credit.position`
	if locking {
		creditQuery += ` FOR UPDATE OF credit,artist`
	}
	if err := db.NewRaw(creditQuery, localProviderID, id).Scan(ctx, &release.Artists); err != nil {
		return nil, err
	}
	release.Tracks = make([]MatchingLocalTrack, 0)
	trackQuery := `SELECT track.id AS provider_track_id,track.recording_id,recording.provider_id AS recording_provider_id,track.medium+1 AS medium,track.position+1 AS position,track.displayed_number,
		CASE WHEN jsonb_typeof(track.metadata->'title_override')='string' THEN track.metadata->>'title_override' ELSE recording.title END AS title,
		CASE WHEN jsonb_typeof(track.metadata->'title_override')='string' THEN track.metadata->>'title_override' END AS title_override,
		NULLIF(track.metadata->>'duration_override_ms','')::bigint AS duration_override_ms
		FROM provider_release_track track JOIN provider_recording recording ON recording.id=track.recording_id
		WHERE track.provider_id=? AND track.release_id=? AND track.present ORDER BY track.medium,track.position`
	if locking {
		trackQuery += ` FOR UPDATE OF track,recording`
	}
	if err := db.NewRaw(trackQuery, localProviderID, id).Scan(ctx, &release.Tracks); err != nil {
		return nil, err
	}
	for i := range release.Tracks {
		credits, duration, err := loadMatchingProviderRecordingData(ctx, db, release.Tracks[i].RecordingProviderID, release.Tracks[i].RecordingID, locking)
		if err != nil {
			return nil, err
		}
		release.Tracks[i].RecordingDurationMS = duration
		release.Tracks[i].RecordingCredits = credits
		release.Tracks[i].Artists = credits
	}
	return &release, nil
}

func loadMatchingProviderRecordingData(ctx context.Context, db bun.IDB, providerID, recordingID uuid.UUID, locking bool) ([]MatchingLocalArtist, *int64, error) {
	var providerKey, metadata, fields, evidence string
	if err := db.NewRaw(`SELECT provider_key,metadata::text FROM provider_recording WHERE provider_id=? AND id=?`, providerID, recordingID).Scan(ctx, &providerKey, &metadata); err != nil {
		return nil, nil, fmt.Errorf("read local matching recording metadata: %w", err)
	}
	var record struct {
		DurationMS *int64 `json:"duration_ms"`
		Credits    []struct {
			ArtistID   uuid.UUID `json:"artist_id"`
			Name       string    `json:"credit_name"`
			JoinPhrase string    `json:"join_phrase"`
		} `json:"credits"`
	}
	if err := json.Unmarshal([]byte(metadata), &record); err != nil {
		return nil, nil, fmt.Errorf("decode local matching recording metadata: %w", err)
	}
	var sourceID uuid.UUID
	projectionQuery := `SELECT provider_source_id,fields::text,field_evidence::text FROM provider_source_entity
		WHERE provider_id=? AND provider_key=? AND entity_kind='recording'
		AND (jsonb_exists(field_evidence,'artist-credit') OR jsonb_exists(field_evidence,'length'))
		ORDER BY authority DESC NULLS LAST,fetch_order DESC NULLS LAST,provider_source_id LIMIT 1`
	if locking {
		projectionQuery += ` FOR SHARE`
	}
	if err := db.NewRaw(projectionQuery, providerID, providerKey).Scan(ctx, &sourceID, &fields, &evidence); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, nil, fmt.Errorf("read accepted local matching recording projection: %w", err)
	}
	credits := make([]MatchingLocalArtist, 0, len(record.Credits))
	duration := record.DurationMS
	acceptedCreditsPresent := false
	if sourceID != uuid.Nil {
		var fieldEvidence map[string]ProviderFieldEvidence
		var projection map[string]json.RawMessage
		if json.Unmarshal([]byte(evidence), &fieldEvidence) != nil || json.Unmarshal([]byte(fields), &projection) != nil || projection == nil {
			return nil, nil, errors.New("decode accepted local matching recording projection")
		}
		if _, accepted := fieldEvidence["length"]; accepted {
			duration = nil
			if len(projection["length"]) != 0 && string(projection["length"]) != "null" {
				var value int64
				if json.Unmarshal(projection["length"], &value) == nil && value >= 0 {
					duration = &value
				}
			}
		}
		if _, accepted := fieldEvidence["artist-credit"]; accepted {
			acceptedCreditsPresent = true
			acceptedCredits, err := acceptedRecordingArtistCredits(ctx, db, providerID, sourceID, recordingID)
			if err != nil {
				return nil, nil, err
			}
			credits = make([]MatchingLocalArtist, 0, len(acceptedCredits))
			for _, acceptedCredit := range acceptedCredits {
				var credit MatchingLocalArtist
				artistQuery := `SELECT id,name FROM provider_artist WHERE provider_id=? AND provider_key=?`
				if locking {
					artistQuery += ` FOR SHARE`
				}
				if err := db.NewRaw(artistQuery, providerID, acceptedCredit.ProviderKey).Scan(ctx, &credit.ProviderArtistID, &credit.ArtistName); err != nil {
					return nil, nil, fmt.Errorf("resolve accepted local matching recording artist: %w", err)
				}
				credit.Position = acceptedCredit.Position
				credit.ProviderID = providerID
				credit.Name = acceptedCredit.Name
				credit.JoinPhrase = acceptedCredit.JoinPhrase
				credits = append(credits, credit)
			}
		}
	}
	if !acceptedCreditsPresent {
		credits = make([]MatchingLocalArtist, 0, len(record.Credits))
		for position, value := range record.Credits {
			if value.ArtistID == uuid.Nil {
				return nil, nil, errors.New("local matching recording credit has no artist identity")
			}
			credit := MatchingLocalArtist{Position: position, ProviderArtistID: value.ArtistID, Name: value.Name, JoinPhrase: value.JoinPhrase}
			query := `SELECT provider_id,name FROM provider_artist WHERE id=?`
			if locking {
				query += ` FOR UPDATE`
			}
			if err := db.NewRaw(query, value.ArtistID).Scan(ctx, &credit.ProviderID, &credit.ArtistName); err != nil {
				return nil, nil, fmt.Errorf("resolve local matching recording artist: %w", err)
			}
			credits = append(credits, credit)
		}
	}
	return credits, duration, nil
}

func validateMatchingProviderTarget(intent MatchingLocalIntent, release MatchingLocalRelease) error {
	byID := make(map[uuid.UUID]MatchingLocalTrack, len(release.Tracks))
	for _, track := range release.Tracks {
		if track.ProviderTrackID == uuid.Nil || track.Medium < 1 || track.Position < 1 {
			return errors.New("local provider release layout is invalid")
		}
		byID[track.ProviderTrackID] = track
	}
	for _, assignment := range intent.Assignments {
		if _, ok := byID[assignment.ProviderTrackID]; !ok {
			return errors.New("local provider assignment is outside selected release layout")
		}
	}
	return nil
}
