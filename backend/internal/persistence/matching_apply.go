package persistence

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// ApplyCurrentMatchingEvaluation materializes only the persisted, unique
// evaluation decision for groupID. Evaluation evidence and all current source
// and provider fences are re-read and locked in the same transaction as writes.
func (repository *IncomingMatchCandidateRepository) ApplyCurrentMatchingEvaluation(ctx context.Context, groupID uuid.UUID, expectedEvaluationRevision string) error {
	if groupID == uuid.Nil || expectedEvaluationRevision == "" {
		return fmt.Errorf("apply matching evaluation: group and evaluation revision are required")
	}
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		var header IncomingMatchEvaluationHeader
		if err := tx.NewRaw(`SELECT group_id, evaluation_revision, evaluation_evidence, group_revision, groups_revision, grouping_epoch,
			policy_version, provider_id, provider_source_id, provider_configuration_identity, provider_namespace, provider_endpoint,
			cache_key, cache_revision, provider_snapshot_revision, provider_fences, evaluated_at
			FROM incoming_match_evaluation WHERE group_id=?`, groupID).Scan(ctx, &header); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrIncomingMatchConflict
			}
			return fmt.Errorf("apply matching evaluation: read evaluation: %w", err)
		}
		if header.EvaluationRevision != expectedEvaluationRevision {
			return ErrIncomingMatchConflict
		}
		decision, err := decodePersistedMatchingDecision(header.EvaluationEvidence)
		if err != nil {
			return err
		}
		if decision.GroupID != groupID || decision.PolicyVersion != header.PolicyVersion || decision.Outcome != "unique" || decision.SelectedRelease == nil {
			return fmt.Errorf("apply matching evaluation: persisted decision is not uniquely applicable")
		}
		var fence IncomingMatchProviderFence
		if err := json.Unmarshal(header.ProviderFences, &fence); err != nil {
			return fmt.Errorf("apply matching evaluation: decode provider fences: %w", err)
		}
		if fence.ProviderID != header.ProviderID || fence.ProviderSourceID != header.ProviderSourceID || fence.ConfigurationIdentity != header.ProviderConfigurationIdentity || fence.Namespace != header.ProviderNamespace || fence.Endpoint != header.ProviderEndpoint || fence.CacheKey != header.CacheKey || !equalOptionalString(fence.CacheRevision, header.CacheRevision) || fence.AcceptedProjectionRevision != header.ProviderSnapshotRevision {
			return ErrIncomingMatchConflict
		}
		group := IncomingMatchGroupEvaluation{GroupID: groupID, GroupRevision: header.GroupRevision, Evidence: header.EvaluationEvidence}
		if err := tx.NewRaw(`SELECT file_member_id, work_id, source_sha256 FROM incoming_match_evaluation_member WHERE group_id=? ORDER BY file_member_id`, groupID).Scan(ctx, &group.Members); err != nil {
			return fmt.Errorf("apply matching evaluation: read evaluated members: %w", err)
		}
		if len(group.Members) == 0 {
			return ErrIncomingMatchConflict
		}
		evaluation := IncomingMatchEvaluation{
			ExpectedGroupsRevision: header.GroupsRevision, ExpectedGroupingEpoch: header.GroupingEpoch,
			PolicyVersion: header.PolicyVersion, Provider: fence, Groups: []IncomingMatchGroupEvaluation{group},
		}
		if err := lockAndValidateMatchingPrelude(ctx, tx, evaluation, "apply matching evaluation"); err != nil {
			return err
		}
		if err := lockAndValidateMatchProvider(ctx, tx, evaluation); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext(?))`, "incoming-match-evaluation:"+groupID.String()); err != nil {
			return fmt.Errorf("apply matching evaluation: lock evaluation: %w", err)
		}
		var currentRevision string
		if err := tx.NewRaw(`SELECT evaluation_revision FROM incoming_match_evaluation WHERE group_id=? FOR UPDATE`, groupID).Scan(ctx, &currentRevision); err != nil || currentRevision != expectedEvaluationRevision {
			return ErrIncomingMatchConflict
		}
		return applyPersistedMatchingDecision(ctx, tx, group, decision, fence, header.PolicyVersion)
	})
}

type persistedMatchingDecision struct {
	Version         int       `json:"version"`
	GroupID         uuid.UUID `json:"group_id"`
	PolicyVersion   string    `json:"policy_version"`
	Outcome         string    `json:"outcome"`
	SelectedRelease *struct {
		ProviderReleaseID  uuid.UUID `json:"provider_release_id"`
		ProviderReleaseKey string    `json:"provider_release_key"`
	} `json:"selected_release,omitempty"`
	Assignments []struct {
		FileMemberID    uuid.UUID `json:"file_member_id"`
		ProviderTrackID uuid.UUID `json:"provider_track_id"`
		Medium          int       `json:"medium"`
		Position        int       `json:"position"`
	} `json:"assignments"`
}

func decodePersistedMatchingDecision(evidence json.RawMessage) (persistedMatchingDecision, error) {
	var envelope struct {
		Decision json.RawMessage `json:"evaluation_decision"`
	}
	if err := json.Unmarshal(evidence, &envelope); err != nil || len(envelope.Decision) == 0 {
		return persistedMatchingDecision{}, fmt.Errorf("apply matching evaluation: persisted decision is missing or invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(envelope.Decision))
	decoder.DisallowUnknownFields()
	var decision persistedMatchingDecision
	if err := decoder.Decode(&decision); err != nil {
		return persistedMatchingDecision{}, fmt.Errorf("apply matching evaluation: decode persisted decision: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return persistedMatchingDecision{}, fmt.Errorf("apply matching evaluation: trailing decision data")
	}
	if decision.Version != 1 || decision.GroupID == uuid.Nil || decision.PolicyVersion == "" || decision.Outcome != "unique" || decision.SelectedRelease == nil || decision.SelectedRelease.ProviderReleaseID == uuid.Nil || decision.SelectedRelease.ProviderReleaseKey == "" || len(decision.Assignments) == 0 {
		return persistedMatchingDecision{}, fmt.Errorf("apply matching evaluation: persisted decision is not a valid unique assignment")
	}
	seen := make(map[uuid.UUID]bool, len(decision.Assignments))
	for _, assignment := range decision.Assignments {
		if assignment.FileMemberID == uuid.Nil || assignment.ProviderTrackID == uuid.Nil || assignment.Medium < 1 || assignment.Position < 1 || seen[assignment.FileMemberID] {
			return persistedMatchingDecision{}, fmt.Errorf("apply matching evaluation: persisted assignment is invalid")
		}
		seen[assignment.FileMemberID] = true
	}
	return decision, nil
}

func applyPersistedMatchingDecision(ctx context.Context, tx bun.Tx, group IncomingMatchGroupEvaluation, decision persistedMatchingDecision, fence IncomingMatchProviderFence, policy string) error {
	if len(decision.Assignments) != len(group.Members) {
		return fmt.Errorf("apply matching evaluation: assignments do not cover every group member")
	}
	selectedReleaseEvaluated := false
	for _, key := range fence.ReleaseKeys {
		if key == decision.SelectedRelease.ProviderReleaseKey {
			selectedReleaseEvaluated = true
			break
		}
	}
	if !selectedReleaseEvaluated {
		return ErrIncomingMatchConflict
	}
	var releaseTitle, releaseKey string
	var releaseFields, releaseEvidence string
	var releaseID uuid.UUID
	if err := tx.NewRaw(`SELECT e.name, r.id, r.provider_key, e.fields, e.field_evidence FROM provider_release r JOIN provider_source_entity e ON e.provider_id=r.provider_id AND e.provider_key=r.provider_key
		WHERE r.provider_id=? AND r.id=? AND e.provider_source_id=? AND e.entity_kind='release' AND e.complete=true`, fence.ProviderID, decision.SelectedRelease.ProviderReleaseID, fence.ProviderSourceID).Scan(ctx, &releaseTitle, &releaseID, &releaseKey, &releaseFields, &releaseEvidence); err != nil || releaseID != decision.SelectedRelease.ProviderReleaseID {
		return ErrIncomingMatchConflict
	}
	if releaseKey != decision.SelectedRelease.ProviderReleaseKey {
		return ErrIncomingMatchConflict
	}
	localReleaseID, releaseCreated, err := ensureAutomaticLocalRelease(ctx, tx, fence.ProviderID, releaseID, releaseTitle, policy)
	if err != nil {
		return err
	}
	if releaseCreated {
		if err := writeAutomaticMetadata(ctx, tx, "local_release", localReleaseID, "title", releaseKey, fence.ProviderSourceID, fence.ProviderID, fence.AcceptedProjectionRevision, json.RawMessage(releaseEvidence)); err != nil {
			return err
		}
		credits, err := acceptedReleaseArtistCredits(ctx, tx, fence.ProviderID, fence.ProviderSourceID, releaseID)
		if err != nil {
			return err
		}
		if err := materializeArtistCredits(ctx, tx, fence.ProviderID, fence.ProviderSourceID, localReleaseID, "release_artist", credits, policy, fence.AcceptedProjectionRevision); err != nil {
			return err
		}
	}
	memberByID := make(map[uuid.UUID]IncomingMatchMemberFence, len(group.Members))
	for _, member := range group.Members {
		memberByID[member.FileMemberID] = member
	}
	for _, assignment := range decision.Assignments {
		member, ok := memberByID[assignment.FileMemberID]
		if !ok {
			return fmt.Errorf("apply matching evaluation: assignment references a non-member")
		}
		var providerTrack, providerRecording uuid.UUID
		var medium, position int
		var recordingTitle string
		var recordingEvidence string
		if err := tx.NewRaw(`SELECT t.id, t.recording_id, st.medium, st.position, COALESCE(NULLIF(e.name,''), e.fields->>'title',''), e.field_evidence
			FROM provider_release_track t JOIN provider_source_release_track st ON st.provider_id=t.provider_id AND st.track_id=t.id
			LEFT JOIN provider_source_entity e ON e.provider_source_id=st.provider_source_id AND e.provider_id=t.provider_id AND e.entity_kind='recording' AND e.provider_key=(SELECT provider_key FROM provider_recording WHERE id=t.recording_id AND provider_id=t.provider_id)
				WHERE t.id=? AND t.provider_id=? AND st.release_id=? AND st.present=true AND st.provider_source_id=?`, assignment.ProviderTrackID, fence.ProviderID, releaseID, fence.ProviderSourceID).Scan(ctx, &providerTrack, &providerRecording, &medium, &position, &recordingTitle, &recordingEvidence); err != nil {
			return ErrIncomingMatchConflict
		}
		if medium+1 != assignment.Medium || position+1 != assignment.Position {
			return ErrIncomingMatchConflict
		}
		localTrackID, localRecordingID, recordingCreated, trackCreated, err := ensureAutomaticLocalTrack(ctx, tx, fence.ProviderID, providerTrack, providerRecording, localReleaseID, medium, position, recordingTitle, policy)
		if err != nil {
			return err
		}
		if recordingCreated {
			credits, err := acceptedRecordingArtistCredits(ctx, tx, fence.ProviderID, fence.ProviderSourceID, providerRecording)
			if err != nil {
				return err
			}
			if err := writeAutomaticMetadata(ctx, tx, "local_recording", localRecordingID, "title", recordingProviderKey(ctx, tx, fence.ProviderID, providerRecording), fence.ProviderSourceID, fence.ProviderID, fence.AcceptedProjectionRevision, json.RawMessage(recordingEvidence)); err != nil {
				return err
			}
			if err := materializeArtistCredits(ctx, tx, fence.ProviderID, fence.ProviderSourceID, localRecordingID, "recording_artist", credits, policy, fence.AcceptedProjectionRevision); err != nil {
				return err
			}
		}
		if trackCreated {
			recordingKey := recordingProviderKey(ctx, tx, fence.ProviderID, providerRecording)
			titleField, titleKey := "title", recordingKey
			titleEvidence := json.RawMessage(recordingEvidence)
			if title := acceptedTrackTitle(json.RawMessage(releaseFields), medium, position, recordingKey); title != "" {
				if _, err := tx.NewRaw(`UPDATE local_track SET title=? WHERE id=?`, title, localTrackID).Exec(ctx); err != nil {
					return err
				}
				titleField, titleKey = "normalized_tracks", releaseKey
				titleEvidence = json.RawMessage(releaseEvidence)
			}
			if err := writeAutomaticMetadata(ctx, tx, "local_track", localTrackID, titleField, titleKey, fence.ProviderSourceID, fence.ProviderID, fence.AcceptedProjectionRevision, titleEvidence); err != nil {
				return err
			}
			credits, err := acceptedTrackArtistCredits(json.RawMessage(releaseFields), json.RawMessage(releaseEvidence), medium, position, recordingKey, releaseKey)
			if credits == nil {
				credits, err = acceptedRecordingArtistCredits(ctx, tx, fence.ProviderID, fence.ProviderSourceID, providerRecording)
			}
			if err != nil {
				return err
			}
			if err := materializeArtistCredits(ctx, tx, fence.ProviderID, fence.ProviderSourceID, localTrackID, "track_artist", credits, policy, fence.AcceptedProjectionRevision); err != nil {
				return err
			}
		}
		var variants []uuid.UUID
		if err := tx.NewRaw(`SELECT DISTINCT media_variant_id FROM source_work_variant_identity WHERE work_id=? ORDER BY media_variant_id`, member.WorkID).Scan(ctx, &variants); err != nil {
			return fmt.Errorf("apply matching evaluation: read source work identity: %w", err)
		}
		if len(variants) == 0 {
			return ErrIncomingMatchConflict
		}
		for _, variantID := range variants {
			if err := attachAutomaticTrackSource(ctx, tx, localTrackID, variantID, policy); err != nil {
				return err
			}
		}
	}
	return nil
}

func ensureAutomaticLocalRelease(ctx context.Context, tx bun.Tx, providerID, providerReleaseID uuid.UUID, title, policy string) (uuid.UUID, bool, error) {
	var localID uuid.UUID
	err := tx.NewRaw(`SELECT local_release_id FROM provider_local_release WHERE provider_id=? AND provider_release_id=? FOR UPDATE`, providerID, providerReleaseID).Scan(ctx, &localID)
	if err == nil {
		return localID, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, false, err
	}
	localID = uuid.New()
	if _, err := tx.NewRaw(`INSERT INTO local_release(id,title) VALUES (?,?)`, localID, title).Exec(ctx); err != nil {
		return uuid.Nil, false, fmt.Errorf("apply matching evaluation: create local release: %w", err)
	}
	if _, err := tx.NewRaw(`INSERT INTO provider_local_release(provider_id,provider_release_id,local_release_id,method,policy_version) VALUES (?,?,?,'automatic',?)`, providerID, providerReleaseID, localID, policy).Exec(ctx); err != nil {
		return uuid.Nil, false, fmt.Errorf("apply matching evaluation: map provider release: %w", err)
	}
	return localID, true, nil
}

func ensureAutomaticLocalTrack(ctx context.Context, tx bun.Tx, providerID, providerTrackID, providerRecordingID, releaseID uuid.UUID, medium, position int, title, policy string) (uuid.UUID, uuid.UUID, bool, bool, error) {
	var localID uuid.UUID
	err := tx.NewRaw(`SELECT local_track_id FROM provider_local_track WHERE provider_id=? AND provider_track_id=? FOR UPDATE`, providerID, providerTrackID).Scan(ctx, &localID)
	if err == nil {
		var recordingID uuid.UUID
		if err := tx.NewRaw(`SELECT local_recording_id FROM provider_local_recording WHERE provider_id=? AND provider_recording_id=?`, providerID, providerRecordingID).Scan(ctx, &recordingID); err != nil {
			return uuid.Nil, uuid.Nil, false, false, err
		}
		return localID, recordingID, false, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, uuid.Nil, false, false, err
	}
	var recordingCreated bool
	var recordingID uuid.UUID
	err = tx.NewRaw(`SELECT local_recording_id FROM provider_local_recording WHERE provider_id=? AND provider_recording_id=? FOR UPDATE`, providerID, providerRecordingID).Scan(ctx, &recordingID)
	if errors.Is(err, sql.ErrNoRows) {
		recordingID = uuid.New()
		if _, err := tx.NewRaw(`INSERT INTO local_recording(id,title) VALUES (?,?)`, recordingID, title).Exec(ctx); err != nil {
			return uuid.Nil, uuid.Nil, false, false, fmt.Errorf("apply matching evaluation: create local recording: %w", err)
		}
		if _, err := tx.NewRaw(`INSERT INTO provider_local_recording(provider_id,provider_recording_id,local_recording_id,method,policy_version) VALUES (?,?,?,'automatic',?)`, providerID, providerRecordingID, recordingID, policy).Exec(ctx); err != nil {
			return uuid.Nil, uuid.Nil, false, false, fmt.Errorf("apply matching evaluation: map provider recording: %w", err)
		}
		recordingCreated = true
	} else if err != nil {
		return uuid.Nil, uuid.Nil, false, false, err
	}
	localID = uuid.New()
	if _, err := tx.NewRaw(`INSERT INTO local_track(id,title) VALUES (?,?)`, localID, title).Exec(ctx); err != nil {
		return uuid.Nil, uuid.Nil, false, false, fmt.Errorf("apply matching evaluation: create local track: %w", err)
	}
	if _, err := tx.NewRaw(`INSERT INTO provider_local_track(provider_id,provider_track_id,local_track_id,method,policy_version) VALUES (?,?,?,'automatic',?)`, providerID, providerTrackID, localID, policy).Exec(ctx); err != nil {
		return uuid.Nil, uuid.Nil, false, false, fmt.Errorf("apply matching evaluation: map provider track: %w", err)
	}
	if _, err := tx.NewRaw(`INSERT INTO release_track(release_id,medium,position,track_id,recording_id) VALUES (?,?,?,?,?)`, releaseID, medium, position, localID, recordingID).Exec(ctx); err != nil {
		return uuid.Nil, uuid.Nil, false, false, fmt.Errorf("apply matching evaluation: add local release track: %w", err)
	}
	return localID, recordingID, recordingCreated, true, nil
}

type acceptedArtistCredit struct {
	Position    int    `bun:"position"`
	ProviderKey string `bun:"provider_key"`
	Name        string `bun:"name"`
	JoinPhrase  string `bun:"join_phrase"`
	Authority   int    `bun:"authority"`
	FetchOrder  int64  `bun:"fetch_order"`
	SourceKind  string `bun:"source_kind"`
	SourceKey   string `bun:"source_key"`
	SourceField string `bun:"source_field"`
}

func acceptedReleaseArtistCredits(ctx context.Context, tx bun.Tx, providerID, sourceID, releaseID uuid.UUID) ([]acceptedArtistCredit, error) {
	var credits []acceptedArtistCredit
	err := tx.NewRaw(`SELECT c.position, a.provider_key, c.name, c.name_join_phrase AS join_phrase, c.authority, c.fetch_order, 'release' AS source_kind, r.provider_key AS source_key, 'artist-credit' AS source_field
		FROM provider_source_release_artist_credit c
		JOIN provider_artist a ON a.provider_id=c.provider_id AND a.id=c.artist_id
		JOIN provider_release r ON r.provider_id=c.provider_id AND r.id=c.release_id
		JOIN provider_source_entity e ON e.provider_source_id=c.provider_source_id AND e.provider_id=c.provider_id AND e.entity_kind='release' AND e.provider_key=r.provider_key
		WHERE c.provider_id=? AND c.provider_source_id=? AND c.release_id=? AND c.present=true
		AND COALESCE((e.field_evidence->'__credits'->>'authority')::integer, -1)=c.authority
		AND COALESCE((e.field_evidence->'__credits'->>'fetch_order')::bigint, -1)=c.fetch_order
		ORDER BY c.position`, providerID, sourceID, releaseID).Scan(ctx, &credits)
	if err != nil {
		return nil, fmt.Errorf("apply matching evaluation: read accepted release artist credits: %w", err)
	}
	return credits, nil
}

func acceptedRecordingArtistCredits(ctx context.Context, tx bun.Tx, providerID, sourceID, recordingID uuid.UUID) ([]acceptedArtistCredit, error) {
	var fields, evidence, recordingKey string
	err := tx.NewRaw(`SELECT e.fields, e.field_evidence, r.provider_key FROM provider_recording r
		JOIN provider_source_entity e ON e.provider_id=r.provider_id AND e.provider_key=r.provider_key AND e.entity_kind='recording'
	WHERE r.provider_id=? AND r.id=? AND e.provider_source_id=?`, providerID, recordingID, sourceID).Scan(ctx, &fields, &evidence, &recordingKey)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("apply matching evaluation: read accepted recording artist credits: %w", err)
	}
	var fieldEvidence map[string]ProviderFieldEvidence
	if err := json.Unmarshal([]byte(evidence), &fieldEvidence); err != nil {
		return nil, fmt.Errorf("apply matching evaluation: decode recording field evidence: %w", err)
	}
	creditEvidence := fieldEvidence["artist-credit"]
	var projection map[string]json.RawMessage
	if json.Unmarshal([]byte(fields), &projection) != nil {
		return nil, fmt.Errorf("apply matching evaluation: decode accepted recording projection")
	}
	var values []json.RawMessage
	if len(projection["artist-credit"]) == 0 || string(projection["artist-credit"]) == "null" {
		return nil, nil
	}
	if err := json.Unmarshal(projection["artist-credit"], &values); err != nil {
		return nil, fmt.Errorf("apply matching evaluation: decode accepted recording artist credits: %w", err)
	}
	credits := make([]acceptedArtistCredit, 0, len(values))
	for position, value := range values {
		var credit struct {
			Name       string          `json:"name"`
			JoinPhrase string          `json:"joinphrase"`
			Artist     json.RawMessage `json:"artist"`
		}
		if err := json.Unmarshal(value, &credit); err != nil {
			return nil, fmt.Errorf("apply matching evaluation: decode accepted recording artist credit: %w", err)
		}
		var artist struct {
			ID string `json:"id"`
		}
		if len(credit.Artist) == 0 || json.Unmarshal(credit.Artist, &artist) != nil || artist.ID == "" {
			continue
		}
		credits = append(credits, acceptedArtistCredit{
			Position: position, ProviderKey: strings.ToLower(artist.ID), Name: credit.Name, JoinPhrase: credit.JoinPhrase,
			Authority: creditEvidence.Authority, FetchOrder: creditEvidence.FetchOrder,
			SourceKind: "recording", SourceKey: recordingKey, SourceField: "artist-credit",
		})
	}
	return credits, nil
}

func acceptedTrackArtistCredits(fields, evidence json.RawMessage, medium, position int, recordingKey, releaseKey string) ([]acceptedArtistCredit, error) {
	var collection []struct {
		Medium               int              `json:"medium"`
		Position             int              `json:"position"`
		RecordingProviderKey string           `json:"recording_provider_key"`
		ArtistCredit         *json.RawMessage `json:"artist-credit"`
	}
	var projection map[string]json.RawMessage
	if err := json.Unmarshal(fields, &projection); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(projection["normalized_tracks"], &collection); err != nil {
		return nil, err
	}
	for _, track := range collection {
		if track.Medium != medium || track.Position != position || !strings.EqualFold(track.RecordingProviderKey, recordingKey) {
			continue
		}
		if track.ArtistCredit == nil {
			return nil, nil
		}
		var items []struct {
			Name       string `json:"name"`
			JoinPhrase string `json:"joinphrase"`
			Artist     struct {
				ID string `json:"id"`
			} `json:"artist"`
		}
		if err := json.Unmarshal(*track.ArtistCredit, &items); err != nil {
			return nil, err
		}
		var sourceEvidence map[string]ProviderFieldEvidence
		if err := json.Unmarshal(evidence, &sourceEvidence); err != nil {
			return nil, err
		}
		selected := sourceEvidence["normalized_tracks"]
		credits := make([]acceptedArtistCredit, 0, len(items))
		for index, item := range items {
			if item.Artist.ID == "" {
				continue
			}
			credits = append(credits, acceptedArtistCredit{
				Position: index, ProviderKey: strings.ToLower(item.Artist.ID), Name: item.Name, JoinPhrase: item.JoinPhrase,
				Authority: selected.Authority, FetchOrder: selected.FetchOrder,
				SourceKind: "release", SourceKey: releaseKey, SourceField: "normalized_tracks",
			})
		}
		return credits, nil
	}
	return nil, nil
}

func acceptedTrackTitle(fields json.RawMessage, medium, position int, recordingKey string) string {
	var projection struct {
		Tracks []struct {
			Medium               int    `json:"medium"`
			Position             int    `json:"position"`
			RecordingProviderKey string `json:"recording_provider_key"`
			Title                string `json:"title"`
		} `json:"normalized_tracks"`
	}
	if json.Unmarshal(fields, &projection) != nil {
		return ""
	}
	for _, track := range projection.Tracks {
		if track.Medium == medium && track.Position == position && strings.EqualFold(track.RecordingProviderKey, recordingKey) {
			return track.Title
		}
	}
	return ""
}

func writeAutomaticMetadata(ctx context.Context, tx bun.Tx, table string, id uuid.UUID, field, entityKey string, sourceID, providerID uuid.UUID, revision string, fieldEvidence json.RawMessage) error {
	if table != "local_artist" && table != "local_release" && table != "local_recording" && table != "local_track" {
		return fmt.Errorf("apply matching evaluation: invalid provenance table")
	}
	var evidence map[string]ProviderFieldEvidence
	if err := json.Unmarshal(fieldEvidence, &evidence); err != nil {
		return err
	}
	entityKind := map[string]string{"local_release": "release", "local_recording": "recording", "local_track": "release"}[table]
	if table == "local_track" && field == "title" {
		entityKind = "recording"
	}
	metadata, err := json.Marshal(map[string]any{"automatic_matching": map[string]any{
		"source_id": sourceID, "provider_id": providerID, "source_entity_id": entityKey,
		"projection_revision": revision, "field_evidence": map[string]ProviderFieldEvidence{field: evidence[field]},
		"provider_fields_reference": map[string]any{
			"entity_kind":  entityKind,
			"provider_key": entityKey, "fields": []string{field},
		},
	}})
	if err != nil {
		return err
	}
	_, err = tx.NewRaw(`UPDATE `+table+` SET metadata=?::jsonb WHERE id=?`, string(metadata), id).Exec(ctx)
	return err
}

func recordingProviderKey(ctx context.Context, tx bun.Tx, providerID, recordingID uuid.UUID) string {
	var key string
	_ = tx.NewRaw(`SELECT provider_key FROM provider_recording WHERE provider_id=? AND id=?`, providerID, recordingID).Scan(ctx, &key)
	return key
}

func materializeArtistCredits(ctx context.Context, tx bun.Tx, providerID, sourceID, parentID uuid.UUID, relation string, credits []acceptedArtistCredit, policy, projectionRevision string) error {
	parentColumn := map[string]string{"release_artist": "release_id", "recording_artist": "recording_id", "track_artist": "track_id"}[relation]
	if parentColumn == "" {
		return fmt.Errorf("apply matching evaluation: invalid artist relation")
	}
	for _, credit := range credits {
		var providerArtistID uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM provider_artist WHERE provider_id=? AND provider_key=?`, providerID, credit.ProviderKey).Scan(ctx, &providerArtistID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return fmt.Errorf("apply matching evaluation: resolve provider artist: %w", err)
		}
		var acceptedName string
		var artistFields, artistEvidence string
		err := tx.NewRaw(`SELECT name, fields, field_evidence FROM provider_source_entity
			WHERE provider_source_id=? AND provider_id=? AND entity_kind='artist' AND provider_key=?`, sourceID, providerID, credit.ProviderKey).Scan(ctx, &acceptedName, &artistFields, &artistEvidence)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("apply matching evaluation: read accepted artist projection: %w", err)
		}
		var projection map[string]json.RawMessage
		_ = json.Unmarshal([]byte(artistFields), &projection)
		if acceptedName == "" {
			_ = json.Unmarshal(projection["name"], &acceptedName)
		}
		var fieldEvidence map[string]ProviderFieldEvidence
		_ = json.Unmarshal([]byte(artistEvidence), &fieldEvidence)
		nameEvidence, hasNameEvidence := fieldEvidence["name"]
		nameUsable := strings.TrimSpace(acceptedName) != "" && hasNameEvidence
		if !nameUsable {
			acceptedName = credit.Name
		}
		var artistID uuid.UUID
		err = tx.NewRaw(`SELECT local_artist_id FROM provider_local_artist WHERE provider_id=? AND provider_artist_id=? FOR UPDATE`, providerID, providerArtistID).Scan(ctx, &artistID)
		if errors.Is(err, sql.ErrNoRows) {
			if acceptedName == "" {
				continue
			}
			artistID = uuid.New()
			nameField := "name"
			sourceKind, sourceKey := "artist", credit.ProviderKey
			if !nameUsable {
				nameEvidence = ProviderFieldEvidence{Authority: credit.Authority, FetchOrder: credit.FetchOrder}
				nameField = credit.SourceField
				if nameField == "" {
					nameField = "artist-credit"
				}
				if credit.SourceKind != "" {
					sourceKind = credit.SourceKind
				}
				if credit.SourceKey != "" {
					sourceKey = credit.SourceKey
				}
			}
			provenance, marshalErr := json.Marshal(map[string]any{"automatic_matching": map[string]any{
				"source_id": sourceID, "provider_id": providerID, "provider_artist_id": providerArtistID,
				"projection_revision": projectionRevision, "policy_version": policy,
				"source_entity_id": sourceKey, "field_evidence": map[string]ProviderFieldEvidence{nameField: nameEvidence},
				"name_source":               nameField,
				"provider_fields_reference": map[string]any{"entity_kind": sourceKind, "provider_key": sourceKey, "fields": []string{nameField}},
			}})
			if marshalErr != nil {
				return fmt.Errorf("apply matching evaluation: encode artist provenance: %w", marshalErr)
			}
			if _, err := tx.NewRaw(`INSERT INTO local_artist(id,name,metadata) VALUES (?,?,?::jsonb)`, artistID, acceptedName, string(provenance)).Exec(ctx); err != nil {
				return fmt.Errorf("apply matching evaluation: create local artist: %w", err)
			}
			if _, err := tx.NewRaw(`INSERT INTO provider_local_artist(provider_id,provider_artist_id,local_artist_id,method,policy_version) VALUES (?,?,?,'automatic',?)`, providerID, providerArtistID, artistID, policy).Exec(ctx); err != nil {
				return fmt.Errorf("apply matching evaluation: map provider artist: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("apply matching evaluation: read local artist mapping: %w", err)
		}
		if _, err := tx.NewRaw(`INSERT INTO `+relation+`(`+parentColumn+`,position,artist_id,name,name_join_phrase) VALUES (?,?,?,?,?) ON CONFLICT DO NOTHING`, parentID, credit.Position, artistID, credit.Name, credit.JoinPhrase).Exec(ctx); err != nil {
			return fmt.Errorf("apply matching evaluation: add %s credit: %w", relation, err)
		}
	}
	return nil
}

func attachAutomaticTrackSource(ctx context.Context, tx bun.Tx, trackID, variantID uuid.UUID, policy string) error {
	var existing []struct {
		TrackID uuid.UUID `bun:"track_id"`
		Method  string    `bun:"decision_method"`
	}
	if err := tx.NewRaw(`SELECT track_id, decision_method FROM track_source WHERE media_variant_id=? ORDER BY track_id FOR UPDATE`, variantID).Scan(ctx, &existing); err != nil {
		return fmt.Errorf("apply matching evaluation: inspect track source: %w", err)
	}
	for _, source := range existing {
		if source.Method == "manual" && source.TrackID != trackID {
			return fmt.Errorf("apply matching evaluation: source variant is manually mapped to another track")
		}
	}
	for _, source := range existing {
		if source.TrackID == trackID {
			return nil
		}
	}
	if _, err := tx.NewRaw(`INSERT INTO track_source(id,track_id,media_variant_id,selected,compatible,decision_method,policy_version) VALUES (?,?,?,false,true,'automatic',?)`, uuid.New(), trackID, variantID, policy).Exec(ctx); err != nil {
		return fmt.Errorf("apply matching evaluation: attach source variant: %w", err)
	}
	return nil
}
