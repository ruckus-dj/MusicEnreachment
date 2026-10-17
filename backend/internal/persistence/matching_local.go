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

var ErrMatchingLocalConflict = ErrIncomingMatchConflict

type MatchingLocalArtist struct {
	Name string `bun:"name" json:"name"`
}

type MatchingLocalTrack struct {
	LocalTrackID    *uuid.UUID            `json:"local_track_id,omitempty"`
	SourceMediaIDs  []uuid.UUID           `json:"source_media_ids,omitempty"`
	Medium          int                   `json:"medium"`
	Position        int                   `json:"position"`
	DisplayedNumber string                `json:"displayed_number"`
	Title           string                `json:"title"`
	Artists         []MatchingLocalArtist `json:"artists"`
	FileMemberIDs   []uuid.UUID           `json:"file_member_ids"`
}

type MatchingLocalIntent struct {
	LocalReleaseID            *uuid.UUID            `json:"local_release_id,omitempty"`
	ExpectedGroupRevision     string                `json:"expected_group_revision"`
	ExpectedAuthorityRevision int64                 `json:"expected_authority_revision"`
	ReleaseTitle              string                `json:"release_title"`
	ReleaseArtists            []MatchingLocalArtist `json:"release_artists"`
	Tracks                    []MatchingLocalTrack  `json:"tracks"`
}

type MatchingLocalSource struct {
	FileMemberID         uuid.UUID   `json:"file_member_id"`
	MediaIDs             []uuid.UUID `json:"media_ids"`
	Locations            []string    `json:"locations"`
	CurrentLocalTrackIDs []uuid.UUID `json:"current_local_track_ids"`
}

type MatchingLocalPreview struct {
	GroupID              uuid.UUID             `json:"group_id"`
	GroupRevision        string                `json:"group_revision"`
	AuthorityRevision    int64                 `json:"authority_revision"`
	ReleaseTitle         string                `json:"release_title"`
	ReleaseArtists       []MatchingLocalArtist `json:"release_artists"`
	Tracks               []MatchingLocalTrack  `json:"tracks"`
	Sources              []MatchingLocalSource `json:"sources"`
	OmittedFileMemberIDs []uuid.UUID           `json:"omitted_file_member_ids"`
	PreviewDigest        string                `json:"preview_digest"`
	LocalReleaseID       *uuid.UUID            `json:"local_release_id,omitempty"`
	LocalCatalog         *MatchingLocalRelease `json:"local_catalog,omitempty"`
}

type MatchingLocalRelease struct {
	ID      uuid.UUID             `json:"id"`
	Title   string                `json:"title"`
	Artists []MatchingLocalArtist `json:"artists"`
	Tracks  []MatchingLocalTrack  `json:"tracks"`
}

func (repository *IncomingMatchCandidateRepository) PreviewMatchingLocal(ctx context.Context, groupID uuid.UUID, intent MatchingLocalIntent) (MatchingLocalPreview, error) {
	var err error
	intent, err = canonicalMatchingLocalIntent(intent)
	if groupID == uuid.Nil || err != nil {
		return MatchingLocalPreview{}, errors.New("local matching intent is invalid")
	}
	var result MatchingLocalPreview
	err = repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("preview local matching: lock output admission gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("preview local matching: lock source mutations: %w", err)
		}
		if err := lockCurrentIncomingSourceRows(ctx, tx); err != nil {
			return err
		}
		var err error
		result, err = previewMatchingLocalTx(ctx, tx, groupID, intent, false)
		return err
	})
	return result, err
}

func (repository *IncomingMatchCandidateRepository) ConfirmMatchingLocal(ctx context.Context, groupID uuid.UUID, intent MatchingLocalIntent, previewDigest string, requestID uuid.UUID) (MatchingLocalPreview, error) {
	var result MatchingLocalPreview
	if requestID == uuid.Nil || previewDigest == "" {
		return result, errors.New("local matching confirmation identity is invalid")
	}
	var err error
	intent, err = canonicalMatchingLocalIntent(intent)
	if groupID == uuid.Nil || err != nil {
		return result, errors.New("local matching intent is invalid")
	}
	err = repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("confirm local matching: lock output admission gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("confirm local matching: lock source mutations: %w", err)
		}
		if err := lockCurrentIncomingSourceRows(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext(?))`, "local-matching-request:"+requestID.String()); err != nil {
			return err
		}
		canonical, err := json.Marshal(struct {
			Group  uuid.UUID
			Intent MatchingLocalIntent
			Digest string
		}{groupID, intent, previewDigest})
		if err != nil {
			return err
		}
		hash := sha256.Sum256(canonical)
		intentHash := hex.EncodeToString(hash[:])
		var receipt struct {
			IntentHash string `bun:"intent_hash"`
			Response   []byte `bun:"response"`
		}
		err = tx.NewRaw(`SELECT intent_hash,response FROM local_matching_receipt WHERE request_id=?`, requestID).Scan(ctx, &receipt)
		if err == nil {
			if receipt.IntentHash != intentHash {
				return ErrIncomingMatchConflict
			}
			return json.Unmarshal(receipt.Response, &result)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("confirm local matching: read receipt: %w", err)
		}
		result, err = previewMatchingLocalTx(ctx, tx, groupID, intent, true)
		if err != nil {
			return err
		}
		if result.PreviewDigest != previewDigest {
			return ErrIncomingMatchConflict
		}
		localReleaseID, err := materializeMatchingLocal(ctx, tx, result, intent)
		if err != nil {
			return err
		}
		result.LocalReleaseID = &localReleaseID
		authorityRevision, err := promoteIncomingGroupMatchingManual(ctx, tx, groupID)
		if err != nil {
			return err
		}
		result.AuthorityRevision = authorityRevision
		encoded, err := json.Marshal(result)
		if err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO local_matching_receipt(request_id,group_id,intent_hash,preview_digest,response) VALUES (?,?,?, ?,?::jsonb)`, requestID, groupID, intentHash, previewDigest, string(encoded)).Exec(ctx); err != nil {
			return fmt.Errorf("confirm local matching: write receipt: %w", err)
		}
		return nil
	})
	if errors.Is(err, ErrIncomingMatchConflict) {
		return result, ErrMatchingLocalConflict
	}
	return result, err
}

func previewMatchingLocalTx(ctx context.Context, tx bun.Tx, groupID uuid.UUID, intent MatchingLocalIntent, locking bool) (MatchingLocalPreview, error) {
	var result MatchingLocalPreview
	var group struct {
		Revision string `bun:"revision"`
	}
	query := `SELECT revision FROM incoming_group WHERE id=?`
	if locking {
		query += ` FOR UPDATE`
	}
	if err := tx.NewRaw(query, groupID).Scan(ctx, &group.Revision); err != nil || group.Revision != intent.ExpectedGroupRevision {
		return result, ErrIncomingMatchConflict
	}
	var needsRefresh bool
	if err := tx.NewRaw(`SELECT needs_refresh FROM incoming_grouping_state WHERE singleton=true`).Scan(ctx, &needsRefresh); err != nil || needsRefresh {
		return result, ErrIncomingMatchConflict
	}
	authority, err := lockIncomingGroupMatchingAuthority(ctx, tx, groupID)
	if err != nil {
		return result, err
	}
	if authority.Revision != intent.ExpectedAuthorityRevision {
		return result, ErrIncomingMatchConflict
	}
	var memberIDs []uuid.UUID
	if err := tx.NewRaw(`SELECT variant_id FROM incoming_group_member WHERE group_id=? ORDER BY variant_id`, groupID).Scan(ctx, &memberIDs); err != nil || len(memberIDs) == 0 {
		return result, ErrIncomingMatchConflict
	}
	groupSnapshots, err := loadIncomingGroupingGroups(ctx, tx)
	if err != nil {
		return result, err
	}
	var currentGroup *IncomingGroupingGroup
	for i := range groupSnapshots {
		if groupSnapshots[i].ID == groupID {
			currentGroup = &groupSnapshots[i]
			break
		}
	}
	if currentGroup == nil {
		return result, ErrIncomingMatchConflict
	}
	captures, err := loadIncomingGroupingCaptures(ctx, tx)
	if err != nil {
		return result, err
	}
	groupCaptures, err := validateMatchingLocalCaptures(*currentGroup, captures)
	if err != nil {
		return result, err
	}
	result = MatchingLocalPreview{GroupID: groupID, GroupRevision: group.Revision, AuthorityRevision: authority.Revision,
		ReleaseTitle: intent.ReleaseTitle, ReleaseArtists: intent.ReleaseArtists, Tracks: intent.Tracks,
		Sources: make([]MatchingLocalSource, 0, len(memberIDs)), OmittedFileMemberIDs: make([]uuid.UUID, 0)}
	if intent.LocalReleaseID != nil {
		catalog, err := loadMatchingLocalRelease(ctx, tx, *intent.LocalReleaseID, locking)
		if err != nil || catalog == nil {
			return result, ErrIncomingMatchConflict
		}
		result.LocalReleaseID, result.LocalCatalog = intent.LocalReleaseID, catalog
		if _, err := validateMatchingLocalTarget(intent, *catalog); err != nil {
			return result, ErrIncomingMatchConflict
		}
	}
	used := map[uuid.UUID]bool{}
	memberSet := make(map[uuid.UUID]bool, len(memberIDs))
	for _, id := range memberIDs {
		memberSet[id] = true
	}
	for _, track := range intent.Tracks {
		for _, id := range track.FileMemberIDs {
			if !memberSet[id] {
				return result, ErrIncomingMatchConflict
			}
			used[id] = true
		}
	}
	for _, id := range memberIDs {
		var source MatchingLocalSource
		source.FileMemberID = id
		if err := tx.NewRaw(`SELECT DISTINCT identity.media_variant_id FROM incoming_group_member_location member_location
			JOIN source_work_variant_identity identity ON identity.work_id=member_location.work_id
				WHERE member_location.group_id=? AND member_location.variant_id=? AND identity.media_variant_id IS NOT NULL ORDER BY identity.media_variant_id`, groupID, id).Scan(ctx, &source.MediaIDs); err != nil {
			return result, err
		}
		if err := tx.NewRaw(`SELECT root.configured_path || '/' || location.relative_path FROM incoming_group_member_location member_location
			JOIN source_root root ON root.id=member_location.source_root_id JOIN source_location location ON location.id=member_location.location_id
			WHERE member_location.group_id=? AND member_location.variant_id=? AND root.enabled=true
			AND root.configured_path=member_location.configured_path AND root.inventory_path=member_location.inventory_path
			AND location.relative_path=member_location.relative_path AND location.size_bytes=member_location.size_bytes AND location.mtime=member_location.mtime
				ORDER BY root.configured_path,location.relative_path`, groupID, id).Scan(ctx, &source.Locations); err != nil {
			return result, err
		}
		if err := tx.NewRaw(`SELECT DISTINCT link.track_id FROM track_source link WHERE link.media_variant_id IN (?) ORDER BY link.track_id`, bun.List(source.MediaIDs)).Scan(ctx, &source.CurrentLocalTrackIDs); err != nil {
			return result, err
		}
		if len(source.MediaIDs) == 0 || len(source.Locations) == 0 {
			return result, ErrIncomingMatchConflict
		}
		if !used[id] {
			result.OmittedFileMemberIDs = append(result.OmittedFileMemberIDs, id)
		}
		result.Sources = append(result.Sources, source)
	}
	intentJSON, err := json.Marshal(intent)
	if err != nil {
		return result, err
	}
	digestPayload, err := json.Marshal(struct {
		Group     uuid.UUID
		Revision  string
		Authority int64
		Intent    json.RawMessage
		Sources   []MatchingLocalSource
		Captures  []IncomingGroupingCapture
		Catalog   *MatchingLocalRelease
	}{groupID, group.Revision, authority.Revision, intentJSON, result.Sources, groupCaptures, result.LocalCatalog})
	if err != nil {
		return result, err
	}
	digest := sha256.Sum256(digestPayload)
	result.PreviewDigest = hex.EncodeToString(digest[:])
	return result, nil
}

func validateMatchingLocalCaptures(group IncomingGroupingGroup, captures []IncomingGroupingCapture) ([]IncomingGroupingCapture, error) {
	readyByMember := make(map[uuid.UUID]bool, len(group.Members)+len(group.UnreadyMembers))
	for _, id := range group.Members {
		readyByMember[id] = true
	}
	for _, id := range group.UnreadyMembers {
		readyByMember[id] = false
	}
	expected := make(map[string]IncomingGroupingMemberLocation, len(group.Locations))
	for _, location := range group.Locations {
		key := fmt.Sprintf("%s/%s/%s", location.VariantID, location.RootID, location.LocationID)
		expected[key] = location
	}
	actual := make([]IncomingGroupingCapture, 0, len(group.Locations))
	for _, capture := range captures {
		ready, member := readyByMember[capture.VariantID]
		if !member {
			continue
		}
		if !capture.RootEnabled || capture.RootStale || capture.RootID == uuid.Nil || capture.LocationID == uuid.Nil || capture.WorkID == uuid.Nil ||
			capture.ConfiguredPath == "" || capture.InventoryPath != capture.ConfiguredPath || capture.RelativePath == "" || ready != capture.Ready {
			return nil, ErrIncomingMatchConflict
		}
		key := fmt.Sprintf("%s/%s/%s", capture.VariantID, capture.RootID, capture.LocationID)
		fence, exists := expected[key]
		if !exists || fence.WorkID != capture.WorkID || fence.ConfiguredPath != capture.ConfiguredPath || fence.InventoryPath != capture.InventoryPath ||
			fence.RelativePath != capture.RelativePath || fence.SizeBytes != capture.SizeBytes || !fence.Mtime.Equal(capture.Mtime) {
			return nil, ErrIncomingMatchConflict
		}
		actual = append(actual, capture)
		delete(expected, key)
	}
	if len(expected) != 0 || len(actual) != len(group.Locations) {
		return nil, ErrIncomingMatchConflict
	}
	return actual, nil
}

func materializeMatchingLocal(ctx context.Context, tx bun.Tx, preview MatchingLocalPreview, intent MatchingLocalIntent) (uuid.UUID, error) {
	if intent.LocalReleaseID != nil {
		// Keep this helper safe independently of the preview's existing locks:
		// release, positions, then UUID-ordered shared local tracks.
		var releaseID uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM local_release WHERE id=? FOR UPDATE`, *intent.LocalReleaseID).Scan(ctx, &releaseID); err != nil {
			return uuid.Nil, fmt.Errorf("lock existing local matching release: %w", err)
		}
		var positions []uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM release_track WHERE release_id=? ORDER BY medium,position,id FOR UPDATE`, releaseID).Scan(ctx, &positions); err != nil {
			return uuid.Nil, fmt.Errorf("lock existing local matching positions: %w", err)
		}
	}
	tracks := append([]MatchingLocalTrack(nil), intent.Tracks...)
	sort.Slice(tracks, func(i, j int) bool {
		if tracks[i].Medium != tracks[j].Medium {
			return tracks[i].Medium < tracks[j].Medium
		}
		return tracks[i].Position < tracks[j].Position
	})
	localTrackIDs := make([]uuid.UUID, 0, len(tracks))
	seenLocalTrackIDs := make(map[uuid.UUID]struct{}, len(tracks))
	for _, track := range tracks {
		if track.LocalTrackID == nil {
			continue
		}
		if _, exists := seenLocalTrackIDs[*track.LocalTrackID]; exists {
			continue
		}
		seenLocalTrackIDs[*track.LocalTrackID] = struct{}{}
		localTrackIDs = append(localTrackIDs, *track.LocalTrackID)
	}
	if len(localTrackIDs) > 0 {
		var locked []uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM local_track WHERE id IN (?) ORDER BY id FOR UPDATE`, bun.List(localTrackIDs)).Scan(ctx, &locked); err != nil {
			return uuid.Nil, fmt.Errorf("lock existing local matching tracks: %w", err)
		}
	}

	releaseID := uuid.New()
	if intent.LocalReleaseID != nil {
		releaseID = *intent.LocalReleaseID
		if _, err := tx.NewRaw(`UPDATE local_release SET title=?,metadata_revision=metadata_revision+1,updated_at=now() WHERE id=?`, intent.ReleaseTitle, releaseID).Exec(ctx); err != nil {
			return uuid.Nil, err
		}
		if _, err := tx.NewRaw(`DELETE FROM release_artist WHERE release_id=?`, releaseID).Exec(ctx); err != nil {
			return uuid.Nil, err
		}
	} else if _, err := tx.NewRaw(`INSERT INTO local_release(id,title) VALUES (?,?)`, releaseID, intent.ReleaseTitle).Exec(ctx); err != nil {
		return uuid.Nil, err
	}
	if err := insertLocalCredits(ctx, tx, releaseID, "release_artist", intent.ReleaseArtists); err != nil {
		return uuid.Nil, err
	}
	for _, track := range tracks {
		trackID := uuid.New()
		if track.LocalTrackID != nil {
			trackID = *track.LocalTrackID
			if _, err := tx.NewRaw(`UPDATE local_track SET title=?,metadata_revision=metadata_revision+1,updated_at=now() WHERE id=?`, track.Title, trackID).Exec(ctx); err != nil {
				return uuid.Nil, err
			}
			if _, err := tx.NewRaw(`DELETE FROM track_artist WHERE track_id=?`, trackID).Exec(ctx); err != nil {
				return uuid.Nil, err
			}
			if _, err := tx.NewRaw(`UPDATE release_track SET displayed_number=? WHERE release_id=? AND medium=? AND position=? AND track_id=?`, track.DisplayedNumber, releaseID, track.Medium-1, track.Position-1, trackID).Exec(ctx); err != nil {
				return uuid.Nil, err
			}
		} else {
			recordingID := uuid.New()
			if _, err := tx.NewRaw(`INSERT INTO local_recording(id,title) VALUES (?,?)`, recordingID, track.Title).Exec(ctx); err != nil {
				return uuid.Nil, err
			}
			if _, err := tx.NewRaw(`INSERT INTO local_track(id,title) VALUES (?,?)`, trackID, track.Title).Exec(ctx); err != nil {
				return uuid.Nil, err
			}
			if err := insertLocalCredits(ctx, tx, recordingID, "recording_artist", track.Artists); err != nil {
				return uuid.Nil, err
			}
			if _, err := tx.NewRaw(`INSERT INTO release_track(release_id,position,track_id,recording_id,medium,displayed_number) VALUES (?,?,?,?,?,?)`, releaseID, track.Position-1, trackID, recordingID, track.Medium-1, track.DisplayedNumber).Exec(ctx); err != nil {
				return uuid.Nil, err
			}
		}
		if err := insertLocalCredits(ctx, tx, trackID, "track_artist", track.Artists); err != nil {
			return uuid.Nil, err
		}
		for _, memberID := range track.FileMemberIDs {
			for _, mediaID := range previewSourceIDs(preview.Sources, memberID) {
				if _, err := tx.NewRaw(`INSERT INTO track_source(id,track_id,media_variant_id,decision_method) VALUES (?,?,?,'manual') ON CONFLICT (track_id,media_variant_id) DO NOTHING`, uuid.New(), trackID, mediaID).Exec(ctx); err != nil {
					return uuid.Nil, err
				}
			}
		}
	}
	return releaseID, nil
}

func previewSourceIDs(sources []MatchingLocalSource, memberID uuid.UUID) []uuid.UUID {
	for _, source := range sources {
		if source.FileMemberID == memberID {
			return source.MediaIDs
		}
	}
	return nil
}

func canonicalMatchingLocalIntent(intent MatchingLocalIntent) (MatchingLocalIntent, error) {
	intent.ReleaseArtists = append([]MatchingLocalArtist(nil), intent.ReleaseArtists...)
	intent.Tracks = append([]MatchingLocalTrack(nil), intent.Tracks...)
	if intent.LocalReleaseID != nil {
		id := *intent.LocalReleaseID
		intent.LocalReleaseID = &id
	}
	if strings.TrimSpace(intent.ExpectedGroupRevision) == "" || intent.ExpectedAuthorityRevision < 0 || len(intent.Tracks) == 0 ||
		(intent.LocalReleaseID != nil && *intent.LocalReleaseID == uuid.Nil) {
		return intent, errors.New("local matching intent is invalid")
	}
	coordinates := map[string]bool{}
	localTrackIDs := map[uuid.UUID]bool{}
	for i := range intent.Tracks {
		track := &intent.Tracks[i]
		if track.LocalTrackID != nil {
			id := *track.LocalTrackID
			track.LocalTrackID = &id
		}
		track.Artists = append([]MatchingLocalArtist(nil), track.Artists...)
		track.FileMemberIDs = append([]uuid.UUID(nil), track.FileMemberIDs...)
		track.SourceMediaIDs = nil
		if track.LocalTrackID != nil && *track.LocalTrackID == uuid.Nil {
			return intent, errors.New("local matching intent is invalid")
		}
		if track.LocalTrackID != nil {
			if localTrackIDs[*track.LocalTrackID] {
				return intent, errors.New("local matching intent is invalid")
			}
			localTrackIDs[*track.LocalTrackID] = true
		}
		if track.Medium < 1 || track.Position < 1 || len(track.FileMemberIDs) == 0 {
			return intent, errors.New("local matching intent is invalid")
		}
		coord := fmt.Sprintf("%d/%d", track.Medium, track.Position)
		if coordinates[coord] {
			return intent, errors.New("local matching intent is invalid")
		}
		coordinates[coord] = true
		seen := map[uuid.UUID]bool{}
		for _, id := range track.FileMemberIDs {
			if id == uuid.Nil || seen[id] {
				return intent, errors.New("local matching intent is invalid")
			}
			seen[id] = true
		}
		sort.Slice(track.FileMemberIDs, func(a, b int) bool { return track.FileMemberIDs[a].String() < track.FileMemberIDs[b].String() })
	}
	if intent.LocalReleaseID == nil {
		for _, track := range intent.Tracks {
			if track.LocalTrackID != nil {
				return intent, errors.New("local matching intent is invalid")
			}
		}
	}
	return intent, nil
}

func insertLocalCredits(ctx context.Context, tx bun.Tx, ownerID uuid.UUID, table string, artists []MatchingLocalArtist) error {
	if table != "release_artist" && table != "recording_artist" && table != "track_artist" {
		return fmt.Errorf("invalid local artist credit table")
	}
	for position, artist := range artists {
		artistID := uuid.New()
		if _, err := tx.NewRaw(`INSERT INTO local_artist(id,name) VALUES (?,?)`, artistID, artist.Name).Exec(ctx); err != nil {
			return err
		}
		ownerColumn := map[string]string{"release_artist": "release_id", "recording_artist": "recording_id", "track_artist": "track_id"}[table]
		query := `INSERT INTO ` + table + `(` + ownerColumn + `,position,artist_id,name,name_join_phrase) VALUES (?,?,?,?,'')`
		if _, err := tx.NewRaw(query, ownerID, position, artistID, artist.Name).Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}
