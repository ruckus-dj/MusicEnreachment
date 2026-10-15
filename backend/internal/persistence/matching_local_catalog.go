package persistence

import (
	"context"
	"database/sql"
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
	err := repository.db.NewRaw(`SELECT release.id FROM local_release release
		WHERE NOT EXISTS (SELECT 1 FROM provider_local_release link WHERE link.local_release_id=release.id)
		AND NOT EXISTS (SELECT 1 FROM release_track rt JOIN provider_local_track pt ON pt.local_track_id=rt.track_id WHERE rt.release_id=release.id)
		AND NOT EXISTS (SELECT 1 FROM release_track rt JOIN provider_local_recording pr ON pr.local_recording_id=rt.recording_id WHERE rt.release_id=release.id)
		ORDER BY release.title,release.id`).Scan(ctx, &ids)
	if err != nil {
		return MatchingLocalReleaseCatalog{}, fmt.Errorf("list local releases: %w", err)
	}
	result := MatchingLocalReleaseCatalog{Releases: make([]MatchingLocalRelease, 0, len(ids))}
	for _, id := range ids {
		release, err := loadMatchingLocalRelease(ctx, repository.db, id, false)
		if err != nil {
			return MatchingLocalReleaseCatalog{}, fmt.Errorf("list local releases: %w", err)
		}
		if release != nil {
			result.Releases = append(result.Releases, *release)
		}
	}
	return result, nil
}

func loadMatchingLocalRelease(ctx context.Context, db bun.IDB, id uuid.UUID, locking bool) (*MatchingLocalRelease, error) {
	if id == uuid.Nil {
		return nil, errors.New("local release id is invalid")
	}
	var release MatchingLocalRelease
	query := `SELECT id,title FROM local_release WHERE id=?`
	if locking {
		query += ` FOR UPDATE`
	}
	err := db.NewRaw(query, id).Scan(ctx, &release.ID, &release.Title)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var linked bool
	if err := db.NewRaw(`SELECT EXISTS(SELECT 1 FROM provider_local_release WHERE local_release_id=? UNION ALL
		SELECT 1 FROM release_track rt JOIN provider_local_track pt ON pt.local_track_id=rt.track_id WHERE rt.release_id=? UNION ALL
		SELECT 1 FROM release_track rt JOIN provider_local_recording pr ON pr.local_recording_id=rt.recording_id WHERE rt.release_id=?)`, id, id, id).Scan(ctx, &linked); err != nil {
		return nil, err
	}
	if linked {
		return nil, nil
	}
	if locking {
		for _, query := range []string{
			`SELECT track_id FROM release_track WHERE release_id=? ORDER BY medium,position FOR UPDATE`,
			`SELECT track.id FROM local_track track JOIN release_track rt ON rt.track_id=track.id WHERE rt.release_id=? ORDER BY track.id FOR UPDATE OF track`,
			`SELECT recording.id FROM local_recording recording JOIN release_track rt ON rt.recording_id=recording.id WHERE rt.release_id=? ORDER BY recording.id FOR UPDATE OF recording`,
			`SELECT artist_id FROM release_artist WHERE release_id=? ORDER BY position FOR UPDATE`,
			`SELECT artist_id FROM track_artist WHERE track_id IN (SELECT track_id FROM release_track WHERE release_id=?) ORDER BY track_id,position FOR UPDATE`,
			`SELECT id FROM track_source WHERE track_id IN (SELECT track_id FROM release_track WHERE release_id=?) ORDER BY id FOR UPDATE`,
		} {
			var lockedIDs []uuid.UUID
			if err := db.NewRaw(query, id).Scan(ctx, &lockedIDs); err != nil {
				return nil, err
			}
		}
		// Provider links can be inserted through foreign keys to a local entity;
		// recheck after taking the entity locks so a concurrent link cannot make a
		// previously pure-local target eligible for materialization.
		if err := db.NewRaw(`SELECT EXISTS(SELECT 1 FROM provider_local_release WHERE local_release_id=? UNION ALL
			SELECT 1 FROM release_track rt JOIN provider_local_track pt ON pt.local_track_id=rt.track_id WHERE rt.release_id=? UNION ALL
			SELECT 1 FROM release_track rt JOIN provider_local_recording pr ON pr.local_recording_id=rt.recording_id WHERE rt.release_id=?)`, id, id, id).Scan(ctx, &linked); err != nil {
			return nil, err
		}
		if linked {
			return nil, nil
		}
	}
	if err := db.NewRaw(`SELECT name AS name FROM release_artist WHERE release_id=? ORDER BY position`, id).Scan(ctx, &release.Artists); err != nil {
		return nil, err
	}
	release.Tracks = make([]MatchingLocalTrack, 0)
	var rows []struct {
		TrackID         uuid.UUID `bun:"track_id"`
		Medium          int       `bun:"medium"`
		Position        int       `bun:"position"`
		DisplayedNumber string    `bun:"displayed_number"`
		Title           string    `bun:"title"`
	}
	if err := db.NewRaw(`SELECT rt.track_id,rt.medium,rt.position,rt.displayed_number,track.title
		FROM release_track rt JOIN local_track track ON track.id=rt.track_id
		WHERE rt.release_id=? ORDER BY rt.medium,rt.position`, id).Scan(ctx, &rows); err != nil {
		return nil, err
	}
	for _, row := range rows {
		trackID := row.TrackID
		track := MatchingLocalTrack{LocalTrackID: &trackID, Medium: row.Medium + 1, Position: row.Position + 1,
			DisplayedNumber: row.DisplayedNumber, Title: row.Title, Artists: make([]MatchingLocalArtist, 0), SourceMediaIDs: make([]uuid.UUID, 0)}
		if err := db.NewRaw(`SELECT name AS name FROM track_artist WHERE track_id=? ORDER BY position`, trackID).Scan(ctx, &track.Artists); err != nil {
			return nil, err
		}
		if err := db.NewRaw(`SELECT media_variant_id FROM track_source WHERE track_id=? ORDER BY media_variant_id`, trackID).Scan(ctx, &track.SourceMediaIDs); err != nil {
			return nil, err
		}
		release.Tracks = append(release.Tracks, track)
	}
	return &release, nil
}

func validateMatchingLocalTarget(intent MatchingLocalIntent, release MatchingLocalRelease) (map[string]uuid.UUID, error) {
	coordinates := make(map[string]uuid.UUID, len(release.Tracks))
	for _, track := range release.Tracks {
		key := fmt.Sprintf("%d/%d", track.Medium, track.Position)
		if _, exists := coordinates[key]; exists {
			return nil, errors.New("local release has duplicate coordinates")
		}
		coordinates[key] = *track.LocalTrackID
	}
	requested := map[uuid.UUID]bool{}
	for _, track := range intent.Tracks {
		key := fmt.Sprintf("%d/%d", track.Medium, track.Position)
		occupied, exists := coordinates[key]
		if track.LocalTrackID == nil {
			if exists {
				return nil, errors.New("local release coordinate is occupied")
			}
			continue
		}
		if *track.LocalTrackID == uuid.Nil || requested[*track.LocalTrackID] || !exists || occupied != *track.LocalTrackID {
			return nil, errors.New("local track does not match release coordinate")
		}
		requested[*track.LocalTrackID] = true
	}
	return coordinates, nil
}
