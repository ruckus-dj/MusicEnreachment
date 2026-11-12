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
	Position         int       `bun:"position" json:"position,omitempty"`
	ProviderID       uuid.UUID `bun:"provider_id" json:"provider_id"`
	ProviderArtistID uuid.UUID `bun:"provider_artist_id" json:"provider_artist_id"`
	Name             string    `bun:"credit_name" json:"name"`
	ArtistName       string    `bun:"artist_name" json:"artist_name"`
	JoinPhrase       string    `bun:"join_phrase" json:"join_phrase"`
}

type MatchingLocalTrack struct {
	LocalTrackID        *uuid.UUID            `json:"local_track_id,omitempty"`
	ProviderTrackID     uuid.UUID             `bun:"provider_track_id" json:"provider_track_id,omitempty"`
	RecordingID         uuid.UUID             `bun:"recording_id" json:"recording_id,omitempty"`
	RecordingProviderID uuid.UUID             `bun:"recording_provider_id" json:"recording_provider_id,omitempty"`
	SourceMediaIDs      []uuid.UUID           `json:"source_media_ids,omitempty"`
	Medium              int                   `bun:"medium" json:"medium"`
	Position            int                   `bun:"position" json:"position"`
	DisplayedNumber     string                `bun:"displayed_number" json:"displayed_number"`
	Title               string                `bun:"title" json:"title"`
	TitleOverride       *string               `bun:"title_override" json:"title_override,omitempty"`
	RecordingDurationMS *int64                `bun:"recording_duration_ms" json:"recording_duration_ms,omitempty"`
	DurationOverrideMS  *int64                `bun:"duration_override_ms" json:"duration_override_ms,omitempty"`
	RecordingCredits    []MatchingLocalArtist `json:"recording_credits,omitempty"`
	Artists             []MatchingLocalArtist `json:"artists"`
	FileMemberIDs       []uuid.UUID           `json:"file_member_ids"`
}

type MatchingLocalAssignment struct {
	FileMemberID    uuid.UUID `json:"file_member_id"`
	ProviderTrackID uuid.UUID `json:"provider_track_id"`
}

type MatchingLocalIntent struct {
	ProviderReleaseID uuid.UUID                 `json:"provider_release_id,omitempty"`
	Assignments       []MatchingLocalAssignment `json:"assignments,omitempty"`
	// Legacy direct-library fields are retained for the pending frontend cutover.
	LocalReleaseID            *uuid.UUID            `json:"local_release_id,omitempty"`
	ExpectedGroupRevision     string                `json:"expected_group_revision"`
	ExpectedAuthorityRevision int64                 `json:"expected_authority_revision"`
	ReleaseTitle              string                `json:"release_title"`
	ReleaseArtists            []MatchingLocalArtist `json:"release_artists"`
	Tracks                    []MatchingLocalTrack  `json:"tracks"`
}

type MatchingLocalSource struct {
	FileMemberID          uuid.UUID                `json:"file_member_id"`
	SourceMetadataResults map[uuid.UUID]*uuid.UUID `json:"-"`
	MediaIDs              []uuid.UUID              `json:"media_ids"`
	Locations             []string                 `json:"locations"`
	CurrentLocalTrackIDs  []uuid.UUID              `json:"current_local_track_ids"`
}

type MatchingLocalPreview struct {
	GroupID              uuid.UUID                 `json:"group_id"`
	ProviderReleaseID    uuid.UUID                 `json:"provider_release_id,omitempty"`
	Assignments          []MatchingLocalAssignment `json:"assignments,omitempty"`
	GroupRevision        string                    `json:"group_revision"`
	AuthorityRevision    int64                     `json:"authority_revision"`
	ReleaseTitle         string                    `json:"release_title"`
	ReleaseArtists       []MatchingLocalArtist     `json:"release_artists"`
	Tracks               []MatchingLocalTrack      `json:"tracks"`
	Sources              []MatchingLocalSource     `json:"sources"`
	OmittedFileMemberIDs []uuid.UUID               `json:"omitted_file_member_ids"`
	PreviewDigest        string                    `json:"preview_digest"`
	LocalReleaseID       *uuid.UUID                `json:"local_release_id,omitempty"`
	LocalCatalog         *MatchingLocalRelease     `json:"local_catalog,omitempty"`
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
		localReleaseID, err := materializeMatchingProviderRelease(ctx, tx, result, intent)
		if err != nil {
			return err
		}
		result.LocalReleaseID = &localReleaseID
		authorityRevision, err := promoteIncomingGroupMatchingManual(ctx, tx, groupID)
		if err != nil {
			return err
		}
		result.AuthorityRevision = authorityRevision
		positions, err := matchingLocalPublicationPositions(ctx, tx, localReleaseID, intent)
		if err != nil {
			return err
		}
		var outputEpoch uuid.UUID
		if err := tx.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &outputEpoch); err != nil {
			return fmt.Errorf("confirm local matching: read output epoch: %w", err)
		}
		if err := NewSetupManagerRepository(repository.db).RequestPublicationReconciliationWith(ctx, tx, outputEpoch, positions, []string{"local_confirmation"}); err != nil {
			return fmt.Errorf("confirm local matching: request publication reconciliation: %w", err)
		}
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

func matchingLocalPublicationPositions(ctx context.Context, tx bun.Tx, releaseID uuid.UUID, intent MatchingLocalIntent) ([]uuid.UUID, error) {
	providerTrackIDs := make([]uuid.UUID, 0, len(intent.Assignments))
	for _, assignment := range intent.Assignments {
		providerTrackIDs = append(providerTrackIDs, assignment.ProviderTrackID)
	}
	trackIDs := make([]uuid.UUID, 0, len(providerTrackIDs))
	if len(providerTrackIDs) > 0 {
		if err := tx.NewRaw(`SELECT local_track_id FROM provider_local_track WHERE provider_id=? AND provider_track_id IN (?) ORDER BY local_track_id`, localProviderID, bun.List(providerTrackIDs)).Scan(ctx, &trackIDs); err != nil {
			return nil, fmt.Errorf("read local matching track links: %w", err)
		}
	}
	var positions []uuid.UUID
	query := `SELECT DISTINCT affected.id
		FROM release_track affected
		WHERE affected.release_id=?`
	args := []any{releaseID}
	if len(trackIDs) > 0 {
		query += ` OR affected.track_id IN (?)`
		args = append(args, bun.List(trackIDs))
	}
	query += ` ORDER BY affected.id`
	if err := tx.NewRaw(query, args...).Scan(ctx, &positions); err != nil {
		return nil, fmt.Errorf("read local matching publication positions: %w", err)
	}
	return positions, nil
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
	release, err := loadMatchingProviderRelease(ctx, tx, intent.ProviderReleaseID, locking)
	if err != nil || release == nil {
		return result, ErrIncomingMatchConflict
	}
	result = MatchingLocalPreview{GroupID: groupID, GroupRevision: group.Revision, AuthorityRevision: authority.Revision,
		ProviderReleaseID: intent.ProviderReleaseID, ReleaseTitle: release.Title, ReleaseArtists: release.Artists, Tracks: release.Tracks, Assignments: intent.Assignments,
		Sources: make([]MatchingLocalSource, 0, len(memberIDs)), OmittedFileMemberIDs: make([]uuid.UUID, 0), LocalCatalog: release}
	if err := validateMatchingProviderTarget(intent, *release); err != nil {
		return result, ErrIncomingMatchConflict
	}
	var localReleaseID uuid.UUID
	localReleaseQuery := `SELECT local_release_id FROM provider_local_release WHERE provider_id=? AND provider_release_id=?`
	if locking {
		localReleaseQuery += ` FOR UPDATE`
	}
	err = tx.NewRaw(localReleaseQuery, localProviderID, intent.ProviderReleaseID).Scan(ctx, &localReleaseID)
	if err == nil {
		result.LocalReleaseID = &localReleaseID
	} else if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	used := map[uuid.UUID]bool{}
	memberSet := make(map[uuid.UUID]bool, len(memberIDs))
	for _, id := range memberIDs {
		memberSet[id] = true
	}
	for _, assignment := range intent.Assignments {
		id := assignment.FileMemberID
		if !memberSet[id] {
			return result, ErrIncomingMatchConflict
		}
		used[id] = true
	}
	for _, id := range memberIDs {
		var source MatchingLocalSource
		source.FileMemberID = id
		source.SourceMetadataResults = make(map[uuid.UUID]*uuid.UUID)
		for _, capture := range groupCaptures {
			if capture.VariantID != id || capture.MediaVariantID == nil {
				continue
			}
			if _, exists := source.SourceMetadataResults[*capture.MediaVariantID]; !exists {
				if capture.CaptureAnalysisID != nil {
					value := *capture.CaptureAnalysisID
					source.SourceMetadataResults[*capture.MediaVariantID] = &value
				} else {
					source.SourceMetadataResults[*capture.MediaVariantID] = nil
				}
			} else if !sameOptionalUUID(source.SourceMetadataResults[*capture.MediaVariantID], capture.CaptureAnalysisID) {
				source.SourceMetadataResults[*capture.MediaVariantID] = nil
			}
		}
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
		Group           uuid.UUID
		Revision        string
		Authority       int64
		Intent          json.RawMessage
		Sources         []MatchingLocalSource
		Captures        []IncomingGroupingCapture
		Catalog         *MatchingLocalRelease
		LinkedReleaseID *uuid.UUID
	}{groupID, group.Revision, authority.Revision, intentJSON, result.Sources, groupCaptures, result.LocalCatalog, result.LocalReleaseID})
	if err != nil {
		return result, err
	}
	digest := sha256.Sum256(digestPayload)
	result.PreviewDigest = hex.EncodeToString(digest[:])
	return result, nil
}

func materializeMatchingProviderRelease(ctx context.Context, tx bun.Tx, preview MatchingLocalPreview, intent MatchingLocalIntent) (uuid.UUID, error) {
	if preview.LocalCatalog == nil || preview.LocalCatalog.ID != intent.ProviderReleaseID || preview.ProviderReleaseID != intent.ProviderReleaseID {
		return uuid.Nil, ErrIncomingMatchConflict
	}
	release, _, err := ensureLocalRelease(ctx, tx, localProviderID, intent.ProviderReleaseID, preview.ReleaseTitle, materializationAuthority{method: "manual"})
	if err != nil {
		return uuid.Nil, err
	}
	if err := tx.NewRaw(`SELECT id FROM local_release WHERE id=? FOR UPDATE`, release).Scan(ctx, &release); err != nil {
		return uuid.Nil, err
	}
	if err := materializeMatchingLocalProviderCredits(ctx, tx, release, "release_artist", preview.LocalCatalog.Artists); err != nil {
		return uuid.Nil, err
	}
	tracks := make(map[uuid.UUID]MatchingLocalTrack, len(preview.Tracks))
	for _, track := range preview.Tracks {
		tracks[track.ProviderTrackID] = track
	}
	for _, assignment := range intent.Assignments {
		track, ok := tracks[assignment.ProviderTrackID]
		if !ok {
			return uuid.Nil, ErrIncomingMatchConflict
		}
		var recordingProviderID uuid.UUID
		var recordingTitle string
		if err := tx.NewRaw(`SELECT provider_id,title FROM provider_recording WHERE id=? FOR UPDATE`, track.RecordingID).Scan(ctx, &recordingProviderID, &recordingTitle); err != nil || recordingProviderID != track.RecordingProviderID {
			return uuid.Nil, ErrIncomingMatchConflict
		}
		localRecordingID, recordingCreated, err := ensureMatchingLocalRecording(ctx, tx, recordingProviderID, track.RecordingID, recordingTitle, track.RecordingDurationMS)
		if err != nil {
			return uuid.Nil, err
		}
		if err := reactivateMatchingLocalProviderCredits(ctx, tx, track.RecordingCredits); err != nil {
			return uuid.Nil, err
		}
		if recordingCreated {
			if err := materializeMatchingLocalProviderCredits(ctx, tx, localRecordingID, "recording_artist", track.RecordingCredits); err != nil {
				return uuid.Nil, err
			}
		}
		effectiveDuration := track.DurationOverrideMS
		if effectiveDuration == nil {
			effectiveDuration = track.RecordingDurationMS
		}
		localTrackID, trackCreated, err := ensureLocalTrackForRecording(ctx, tx, localProviderID, track.ProviderTrackID, localRecordingID, release, track.Medium-1, track.Position-1, track.Title, effectiveDuration, materializationAuthority{method: "manual"})
		if err != nil {
			return uuid.Nil, err
		}
		if trackCreated {
			if err := materializeMatchingLocalProviderCredits(ctx, tx, localTrackID, "track_artist", track.RecordingCredits); err != nil {
				return uuid.Nil, err
			}
			if err := writeArtistCreditOrigin(ctx, tx, localTrackID, artistCreditOriginRecordingFallback); err != nil {
				return uuid.Nil, err
			}
		}
		variants := previewSourceIDs(preview.Sources, assignment.FileMemberID)
		if len(variants) == 0 {
			return uuid.Nil, ErrIncomingMatchConflict
		}
		for _, variantID := range variants {
			if err := attachTrackSource(ctx, tx, localTrackID, variantID, materializationAuthority{method: "manual"}, previewSourceMetadataResultID(preview.Sources, assignment.FileMemberID, variantID)); err != nil {
				return uuid.Nil, err
			}
		}
	}
	return release, nil
}

func ensureMatchingLocalRecording(ctx context.Context, tx bun.Tx, providerID, providerRecordingID uuid.UUID, title string, durationMS *int64) (uuid.UUID, bool, error) {
	var localID uuid.UUID
	var method string
	err := tx.NewRaw(`SELECT local_recording_id,method FROM provider_local_recording WHERE provider_id=? AND provider_recording_id=? FOR UPDATE`, providerID, providerRecordingID).Scan(ctx, &localID, &method)
	if err == nil {
		if method == "automatic" {
			_, err = tx.NewRaw(`UPDATE provider_local_recording SET method='manual',policy_version=NULL WHERE provider_id=? AND provider_recording_id=?`, providerID, providerRecordingID).Exec(ctx)
		}
		return localID, false, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, false, err
	}
	localID = uuid.New()
	if durationMS == nil {
		if _, err := tx.NewRaw(`INSERT INTO local_recording(id,title) VALUES(?,?)`, localID, title).Exec(ctx); err != nil {
			return uuid.Nil, false, err
		}
	} else if _, err := tx.NewRaw(`INSERT INTO local_recording(id,title,metadata) VALUES(?,?,jsonb_build_object('duration_ms',?))`, localID, title, *durationMS).Exec(ctx); err != nil {
		return uuid.Nil, false, err
	}
	if _, err := tx.NewRaw(`INSERT INTO provider_local_recording(provider_id,provider_recording_id,local_recording_id,method) VALUES(?,?,?,'manual')`, providerID, providerRecordingID, localID).Exec(ctx); err != nil {
		return uuid.Nil, false, err
	}
	return localID, true, nil
}

func materializeMatchingLocalProviderCredits(ctx context.Context, tx bun.Tx, parentID uuid.UUID, relation string, credits []MatchingLocalArtist) error {
	parentColumn := map[string]string{"release_artist": "release_id", "recording_artist": "recording_id", "track_artist": "track_id"}[relation]
	if parentColumn == "" {
		return errors.New("invalid local provider credit relation")
	}
	for _, credit := range credits {
		position := credit.Position
		var artistID uuid.UUID
		var method string
		err := tx.NewRaw(`SELECT local_artist_id,method FROM provider_local_artist WHERE provider_id=? AND provider_artist_id=? FOR UPDATE`, credit.ProviderID, credit.ProviderArtistID).Scan(ctx, &artistID, &method)
		if errors.Is(err, sql.ErrNoRows) {
			artistID = uuid.New()
			if _, err := tx.NewRaw(`INSERT INTO local_artist(id,name) VALUES(?,?)`, artistID, credit.ArtistName).Exec(ctx); err != nil {
				return err
			}
			if _, err := tx.NewRaw(`INSERT INTO provider_local_artist(provider_id,provider_artist_id,local_artist_id,method) VALUES(?,?,?,'manual')`, credit.ProviderID, credit.ProviderArtistID, artistID).Exec(ctx); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if method == "automatic" {
			if _, err := tx.NewRaw(`UPDATE provider_local_artist SET method='manual',policy_version=NULL WHERE provider_id=? AND provider_artist_id=?`, credit.ProviderID, credit.ProviderArtistID).Exec(ctx); err != nil {
				return err
			}
		}
		if _, err := tx.NewRaw(`UPDATE local_artist SET catalog_visible=true WHERE id=? AND catalog_visible IS FALSE`, artistID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO `+relation+`(`+parentColumn+`,position,artist_id,name,name_join_phrase) VALUES(?,?,?,?,?) ON CONFLICT DO NOTHING`, parentID, position, artistID, credit.Name, credit.JoinPhrase).Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

func reactivateMatchingLocalProviderCredits(ctx context.Context, tx bun.Tx, credits []MatchingLocalArtist) error {
	for _, credit := range credits {
		if _, err := tx.NewRaw(`UPDATE local_artist artist SET catalog_visible=true
			FROM provider_local_artist binding
			WHERE binding.provider_id=? AND binding.provider_artist_id=?
			AND artist.id=binding.local_artist_id AND artist.catalog_visible IS NOT TRUE`, credit.ProviderID, credit.ProviderArtistID).Exec(ctx); err != nil {
			return fmt.Errorf("reactivate matching local artist: %w", err)
		}
	}
	return nil
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

func previewSourceIDs(sources []MatchingLocalSource, memberID uuid.UUID) []uuid.UUID {
	for _, source := range sources {
		if source.FileMemberID == memberID {
			return source.MediaIDs
		}
	}
	return nil
}

func previewSourceMetadataResultID(sources []MatchingLocalSource, memberID, mediaID uuid.UUID) *uuid.UUID {
	for _, source := range sources {
		if source.FileMemberID == memberID {
			return source.SourceMetadataResults[mediaID]
		}
	}
	return nil
}

func canonicalMatchingLocalIntent(intent MatchingLocalIntent) (MatchingLocalIntent, error) {
	intent.Assignments = append([]MatchingLocalAssignment(nil), intent.Assignments...)
	if strings.TrimSpace(intent.ExpectedGroupRevision) == "" || intent.ExpectedAuthorityRevision < 0 || len(intent.Assignments) == 0 || intent.ProviderReleaseID == uuid.Nil {
		return intent, errors.New("local matching intent is invalid")
	}
	seenMembers := map[uuid.UUID]bool{}
	for i := range intent.Assignments {
		assignment := &intent.Assignments[i]
		if assignment.FileMemberID == uuid.Nil || assignment.ProviderTrackID == uuid.Nil || seenMembers[assignment.FileMemberID] {
			return intent, errors.New("local matching intent is invalid")
		}
		seenMembers[assignment.FileMemberID] = true
	}
	sort.Slice(intent.Assignments, func(i, j int) bool {
		return intent.Assignments[i].FileMemberID.String() < intent.Assignments[j].FileMemberID.String()
	})
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
