package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

var ErrIncomingMatchConflict = errors.New("incoming matching snapshot changed")

// IncomingMatchMemberFence identifies a file member without retaining a
// foreign-key reference to the inventory or media-variant tables.
type IncomingMatchMemberFence struct {
	FileMemberID uuid.UUID
	WorkID       uuid.UUID
	SourceSHA256 []byte
}

type IncomingMatchPosition struct {
	TrackID  uuid.UUID
	Medium   int
	Position int
}

type IncomingMatchCandidate struct {
	FileMemberID     uuid.UUID
	ProviderID       uuid.UUID
	ProviderSourceID uuid.UUID
	ReleaseID        uuid.UUID
	Score            float64
	Evidence         json.RawMessage
	Positions        []IncomingMatchPosition
}

type IncomingMatchGroupEvaluation struct {
	GroupID       uuid.UUID
	GroupRevision string
	Members       []IncomingMatchMemberFence
	Candidates    []IncomingMatchCandidate
	Evidence      json.RawMessage
}

// IncomingMatchProviderFence is an accepted configuration/cache identity. The
// repository independently re-reads all three values under the config lock.
type IncomingMatchProviderFence struct {
	ProviderID                 uuid.UUID
	ProviderSourceID           uuid.UUID
	Namespace                  string
	Endpoint                   string
	ConfigurationIdentity      string
	CacheKey                   string
	CacheRevision              *string
	CacheRevisions             map[string]*string
	AcceptedProjectionRevision string
	// ReleaseKeys is the exact canonical set of provider release keys the
	// evaluation covered. It is carried and persisted, never inferred from the
	// candidate outputs, so an evaluated-empty result still fences the releases
	// it read. Keys are strictly ascending and individually non-empty. The set
	// itself may be empty when the evaluated search inspected no releases.
	ReleaseKeys []string `json:"release_keys"`
}

type IncomingMatchEvaluation struct {
	ExpectedGroupsRevision string
	ExpectedGroupingEpoch  int64
	PolicyVersion          string
	Provider               IncomingMatchProviderFence
	Groups                 []IncomingMatchGroupEvaluation
}

type IncomingMatchEvaluationHeader struct {
	GroupID                       uuid.UUID
	EvaluationRevision            string
	EvaluationEvidence            json.RawMessage
	GroupRevision                 string
	GroupsRevision                string
	GroupingEpoch                 int64
	PolicyVersion                 string
	ProviderID                    uuid.UUID
	ProviderSourceID              uuid.UUID
	ProviderConfigurationIdentity string
	ProviderNamespace             string
	ProviderEndpoint              string
	CacheKey                      string
	CacheRevision                 *string
	ProviderSnapshotRevision      string
	ProviderFences                json.RawMessage
	EvaluatedAt                   time.Time
}

type IncomingMatchCandidateRepository struct{ db *bun.DB }

func NewIncomingMatchCandidateRepository(db *bun.DB) *IncomingMatchCandidateRepository {
	return &IncomingMatchCandidateRepository{db: db}
}

// Replace atomically publishes evaluated results after validating the current
// group partition, analysis identities, provider configuration, and cache
// revision. Existing results remain untouched on every validation/write error.
func (repository *IncomingMatchCandidateRepository) Replace(ctx context.Context, evaluation IncomingMatchEvaluation) error {
	return repository.replace(ctx, evaluation, nil)
}

// ReplaceIfCurrent publishes only if every group's prior evaluation revision
// still matches the caller's snapshot. A nil revision means never evaluated.
func (repository *IncomingMatchCandidateRepository) ReplaceIfCurrent(ctx context.Context, evaluation IncomingMatchEvaluation, expected map[uuid.UUID]*string) error {
	if expected == nil {
		return fmt.Errorf("replace incoming matches: expected evaluation revisions are required")
	}
	return repository.replace(ctx, evaluation, expected)
}

func (repository *IncomingMatchCandidateRepository) replace(ctx context.Context, evaluation IncomingMatchEvaluation, expected map[uuid.UUID]*string) error {
	if err := validateIncomingMatchEvaluation(evaluation); err != nil {
		return err
	}
	if expected != nil {
		if len(expected) != len(evaluation.Groups) {
			return fmt.Errorf("replace incoming matches: expected one prior revision per group")
		}
		for _, group := range evaluation.Groups {
			if _, ok := expected[group.GroupID]; !ok {
				return fmt.Errorf("replace incoming matches: prior revision is missing for group")
			}
		}
	}
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		// Match source mutations' canonical order: gate, current source/group state,
		// then provider configuration. Provider cache writers use config before
		// their own source row locks; this transaction does not acquire those rows.
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("replace incoming matches: lock source mutation gate: %w", err)
		}
		if err := lockCurrentIncomingSourceRows(ctx, tx); err != nil {
			return err
		}
		var epoch int64
		if err := tx.NewRaw(`SELECT revision FROM incoming_grouping_state WHERE singleton=true FOR SHARE`).Scan(ctx, &epoch); err != nil {
			return fmt.Errorf("replace incoming matches: lock grouping state: %w", err)
		}
		if epoch != evaluation.ExpectedGroupingEpoch {
			return ErrIncomingMatchConflict
		}
		if err := validateCurrentMatchGroups(ctx, tx, evaluation); err != nil {
			return err
		}
		if err := lockAndValidateMatchProvider(ctx, tx, evaluation); err != nil {
			return err
		}
		for _, group := range evaluation.Groups {
			if expected != nil {
				if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext(?))`, "incoming-match-evaluation:"+group.GroupID.String()); err != nil {
					return fmt.Errorf("replace incoming matches: lock prior evaluation: %w", err)
				}
				var current sql.NullString
				err := tx.NewRaw(`SELECT evaluation_revision FROM incoming_match_evaluation WHERE group_id=? FOR UPDATE`, group.GroupID).Scan(ctx, &current)
				if errors.Is(err, sql.ErrNoRows) {
					if expected[group.GroupID] != nil {
						return ErrIncomingMatchConflict
					}
				} else if err != nil {
					return fmt.Errorf("replace incoming matches: read prior evaluation revision: %w", err)
				} else if !current.Valid || expected[group.GroupID] == nil || current.String != *expected[group.GroupID] {
					return ErrIncomingMatchConflict
				}
			}
			if _, err := tx.NewRaw(`DELETE FROM incoming_match_evaluation WHERE group_id=?`, group.GroupID).Exec(ctx); err != nil {
				return fmt.Errorf("replace incoming match evaluation: remove previous result: %w", err)
			}
			providerFences, err := json.Marshal(evaluation.Provider)
			if err != nil {
				return fmt.Errorf("replace incoming match evaluation: encode provider fences: %w", err)
			}
			evaluationRevision := uuid.NewString()
			groupEvidence := group.Evidence
			if len(groupEvidence) == 0 {
				groupEvidence = json.RawMessage(`{}`)
			}
			if _, err := tx.NewRaw(`INSERT INTO incoming_match_evaluation
				(group_id, evaluation_revision, evaluation_evidence, group_revision, groups_revision, grouping_epoch, policy_version,
				 provider_source_id, provider_id, provider_configuration_identity, provider_namespace, provider_endpoint,
					cache_key, cache_revision, provider_snapshot_revision, provider_fences)
					VALUES (?, ?, ?::jsonb, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?::jsonb)`, group.GroupID, evaluationRevision, string(groupEvidence), group.GroupRevision,
				evaluation.ExpectedGroupsRevision, evaluation.ExpectedGroupingEpoch, evaluation.PolicyVersion,
				evaluation.Provider.ProviderSourceID, evaluation.Provider.ProviderID,
				evaluation.Provider.ConfigurationIdentity, evaluation.Provider.Namespace, evaluation.Provider.Endpoint,
				evaluation.Provider.CacheKey, evaluation.Provider.CacheRevision, evaluation.Provider.AcceptedProjectionRevision, string(providerFences)).Exec(ctx); err != nil {
				return fmt.Errorf("replace incoming match evaluation: insert header: %w", err)
			}
			if err := copyIncomingMatchMembers(ctx, tx, group); err != nil {
				return err
			}
			if err := insertIncomingMatchCandidates(ctx, tx, group); err != nil {
				return err
			}
		}
		return nil
	})
}

func validateIncomingMatchEvaluation(evaluation IncomingMatchEvaluation) error {
	if evaluation.ExpectedGroupsRevision == "" || evaluation.ExpectedGroupingEpoch < 0 || evaluation.PolicyVersion == "" ||
		evaluation.Provider.ProviderID == uuid.Nil || evaluation.Provider.ProviderSourceID == uuid.Nil ||
		evaluation.Provider.Namespace == "" || evaluation.Provider.Endpoint == "" || evaluation.Provider.ConfigurationIdentity == "" || evaluation.Provider.CacheKey == "" || evaluation.Provider.AcceptedProjectionRevision == "" || len(evaluation.Groups) == 0 {
		return fmt.Errorf("replace incoming matches: complete snapshot and provider fences are required")
	}
	if !canonicalReleaseKeys(evaluation.Provider.ReleaseKeys) {
		return fmt.Errorf("replace incoming matches: evaluated release keys must be sorted, deduplicated, and individually non-empty")
	}
	seenGroups := make(map[uuid.UUID]bool, len(evaluation.Groups))
	for _, group := range evaluation.Groups {
		if group.GroupID == uuid.Nil || group.GroupRevision == "" || len(group.Members) == 0 || seenGroups[group.GroupID] {
			return fmt.Errorf("replace incoming matches: group identity or members are invalid")
		}
		seenGroups[group.GroupID] = true
		members := make(map[uuid.UUID]bool, len(group.Members))
		for _, member := range group.Members {
			if member.FileMemberID == uuid.Nil || member.WorkID == uuid.Nil || members[member.FileMemberID] || len(member.SourceSHA256) != 0 && len(member.SourceSHA256) != 32 {
				return fmt.Errorf("replace incoming matches: member fence is invalid")
			}
			members[member.FileMemberID] = true
		}
		if len(group.Evidence) != 0 && !jsonObject(group.Evidence) {
			return fmt.Errorf("replace incoming matches: group evidence must be a JSON object")
		}
		for _, candidate := range group.Candidates {
			if !members[candidate.FileMemberID] || candidate.ProviderID != evaluation.Provider.ProviderID || candidate.ProviderSourceID != evaluation.Provider.ProviderSourceID || candidate.ReleaseID == uuid.Nil || math.IsNaN(candidate.Score) || math.IsInf(candidate.Score, 0) || candidate.Score < 0 || candidate.Score > 1 || !jsonObject(candidate.Evidence) {
				return fmt.Errorf("replace incoming matches: candidate is invalid")
			}
			positions := make(map[[2]int]bool, len(candidate.Positions))
			for _, position := range candidate.Positions {
				key := [2]int{position.Medium, position.Position}
				if position.TrackID == uuid.Nil || position.Medium < 0 || position.Position < 0 || positions[key] {
					return fmt.Errorf("replace incoming matches: candidate position is invalid")
				}
				positions[key] = true
			}
		}
	}
	return nil
}

func canonicalReleaseKeys(keys []string) bool {
	for index, key := range keys {
		if key == "" || index > 0 && keys[index-1] >= key {
			return false
		}
	}
	return true
}

func jsonObject(data json.RawMessage) bool {
	var value map[string]json.RawMessage
	return len(data) > 0 && json.Unmarshal(data, &value) == nil && value != nil
}

func validateCurrentMatchGroups(ctx context.Context, tx bun.Tx, evaluation IncomingMatchEvaluation) error {
	for _, expected := range evaluation.Groups {
		var actualRevision string
		if err := tx.NewRaw(`SELECT revision FROM incoming_group WHERE id=? FOR SHARE`, expected.GroupID).Scan(ctx, &actualRevision); err != nil {
			return ErrIncomingMatchConflict
		}
		if actualRevision != expected.GroupRevision {
			return ErrIncomingMatchConflict
		}
		var actual []struct {
			VariantID uuid.UUID `bun:"variant_id"`
			Ready     bool      `bun:"ready"`
		}
		if err := tx.NewRaw(`SELECT variant_id, ready FROM incoming_group_member WHERE group_id=? ORDER BY variant_id FOR SHARE`, expected.GroupID).Scan(ctx, &actual); err != nil {
			return fmt.Errorf("replace incoming matches: read current group members: %w", err)
		}
		if len(actual) != len(expected.Members) {
			return ErrIncomingMatchConflict
		}
		members := append([]IncomingMatchMemberFence(nil), expected.Members...)
		sort.Slice(members, func(i, j int) bool { return members[i].FileMemberID.String() < members[j].FileMemberID.String() })
		for i, member := range actual {
			if !member.Ready || member.VariantID != members[i].FileMemberID {
				return ErrIncomingMatchConflict
			}
		}
	}
	current, err := loadIncomingGroupingCaptures(ctx, tx)
	if err != nil {
		return err
	}
	byVariant := make(map[uuid.UUID]IncomingGroupingCapture, len(current))
	for _, capture := range current {
		byVariant[capture.VariantID] = capture
	}
	for _, group := range evaluation.Groups {
		for _, member := range group.Members {
			capture, ok := byVariant[member.FileMemberID]
			if !ok || !capture.Ready || capture.WorkID != member.WorkID || !equalOptionalBytes(capture.SourceSHA256, member.SourceSHA256) {
				return ErrIncomingMatchConflict
			}
		}
	}
	return nil
}

func equalOptionalBytes(left, right []byte) bool {
	if len(left) == 0 || len(right) == 0 {
		return len(left) == 0 && len(right) == 0
	}
	return string(left) == string(right)
}

func lockAndValidateMatchProvider(ctx context.Context, tx bun.Tx, evaluation IncomingMatchEvaluation) error {
	fence := evaluation.Provider
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(hashtext(?))`, "musicbrainz-config"); err != nil {
		return fmt.Errorf("replace incoming matches: lock provider configuration: %w", err)
	}
	var identity string
	if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &identity); err != nil || identity != fence.ConfigurationIdentity {
		return ErrIncomingMatchConflict
	}
	var source struct {
		ConfigurationIdentity string `bun:"configuration_identity"`
		Namespace             string `bun:"namespace"`
		Endpoint              string `bun:"endpoint"`
	}
	if err := tx.NewRaw(`SELECT configuration_identity, namespace, endpoint FROM provider_source WHERE id=? AND provider_id=? FOR SHARE`, fence.ProviderSourceID, fence.ProviderID).Scan(ctx, &source); err != nil || source.ConfigurationIdentity != fence.ConfigurationIdentity || source.Namespace != fence.Namespace || source.Endpoint != fence.Endpoint {
		return ErrIncomingMatchConflict
	}
	queryRevisions := fence.CacheRevisions
	if len(queryRevisions) == 0 {
		queryRevisions = map[string]*string{fence.CacheKey: fence.CacheRevision}
	}
	if revision, ok := queryRevisions[fence.CacheKey]; !ok || !equalOptionalString(revision, fence.CacheRevision) {
		return ErrIncomingMatchConflict
	}
	keys := make([]string, 0, len(queryRevisions))
	for key := range queryRevisions {
		if key == "" {
			return ErrIncomingMatchConflict
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		var storedRevision sql.NullString
		err := tx.NewRaw(`SELECT revision FROM provider_response_cache WHERE provider_source_id=? AND cache_key=? FOR SHARE`, fence.ProviderSourceID, key).Scan(ctx, &storedRevision)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("replace incoming matches: read provider cache revision: %w", err)
		}
		var revision *string
		if err == nil && storedRevision.Valid {
			revision = &storedRevision.String
		}
		if !equalOptionalString(revision, queryRevisions[key]) {
			return ErrIncomingMatchConflict
		}
	}
	// The evaluated release set is carried by the fence, not inferred from the
	// candidate outputs, so an evaluated-empty result still reads and fences the
	// exact releases it covered.
	releaseKeys := fence.ReleaseKeys
	releaseKeyByID := make(map[uuid.UUID]string, len(releaseKeys))
	for _, key := range releaseKeys {
		var id uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM provider_release WHERE provider_id=? AND provider_key=?`, fence.ProviderID, key).Scan(ctx, &id); err != nil {
			return ErrIncomingMatchConflict
		}
		releaseKeyByID[id] = key
	}
	snapshot, err := readMusicBrainzMatchingSnapshot(ctx, tx, fence.Endpoint, fence.ConfigurationIdentity, releaseKeys, keys, false)
	if err != nil || snapshot.Source.ID != fence.ProviderSourceID || len(snapshot.Releases) != len(releaseKeys) || snapshot.AcceptedProjectionRevision != fence.AcceptedProjectionRevision {
		return ErrIncomingMatchConflict
	}
	type currentTrack struct {
		releaseKey string
		track      MatchingReleaseTrack
	}
	tracks := make(map[uuid.UUID]currentTrack)
	for _, release := range snapshot.Releases {
		for _, track := range release.Tracks {
			tracks[track.ID] = currentTrack{release.ProviderKey, track}
		}
	}
	for _, group := range evaluation.Groups {
		for _, candidate := range group.Candidates {
			releaseKey, ok := releaseKeyByID[candidate.ReleaseID]
			if !ok {
				return ErrIncomingMatchConflict
			}
			for _, position := range candidate.Positions {
				current, ok := tracks[position.TrackID]
				if !ok || current.releaseKey != releaseKey || current.track.Medium != position.Medium || current.track.Position != position.Position {
					return ErrIncomingMatchConflict
				}
			}
		}
	}
	return nil
}

func equalOptionalString(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func copyIncomingMatchMembers(ctx context.Context, tx bun.Tx, group IncomingMatchGroupEvaluation) error {
	for _, member := range group.Members {
		if _, err := tx.NewRaw(`INSERT INTO incoming_match_evaluation_member(group_id, file_member_id, work_id, source_sha256) VALUES (?, ?, ?, ?)`, group.GroupID, member.FileMemberID, member.WorkID, nullableBytes(member.SourceSHA256)).Exec(ctx); err != nil {
			return fmt.Errorf("replace incoming matches: copy member fence: %w", err)
		}
		if _, err := tx.NewRaw(`INSERT INTO incoming_match_evaluation_location
			(group_id, file_member_id, source_root_id, location_id, configured_path, inventory_path, relative_path, size_bytes, mtime)
			SELECT group_id, variant_id, source_root_id, location_id, configured_path, inventory_path, relative_path, size_bytes, mtime
			FROM incoming_group_member_location WHERE group_id=? AND variant_id=?`, group.GroupID, member.FileMemberID).Exec(ctx); err != nil {
			return fmt.Errorf("replace incoming matches: copy member locations: %w", err)
		}
	}
	return nil
}

func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func insertIncomingMatchCandidates(ctx context.Context, tx bun.Tx, group IncomingMatchGroupEvaluation) error {
	for _, candidate := range group.Candidates {
		if _, err := tx.NewRaw(`INSERT INTO incoming_match_candidate
			(group_id, file_member_id, provider_id, provider_source_id, release_id, score, evidence)
			VALUES (?, ?, ?, ?, ?, ?, ?::jsonb)`, group.GroupID, candidate.FileMemberID, candidate.ProviderID,
			candidate.ProviderSourceID, candidate.ReleaseID, candidate.Score, string(candidate.Evidence)).Exec(ctx); err != nil {
			return fmt.Errorf("replace incoming matches: insert candidate: %w", err)
		}
		for _, position := range candidate.Positions {
			if _, err := tx.NewRaw(`INSERT INTO incoming_match_candidate_track
				(group_id, file_member_id, provider_source_id, provider_id, release_id, track_id, medium, position)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, group.GroupID, candidate.FileMemberID,
				candidate.ProviderSourceID, candidate.ProviderID, candidate.ReleaseID, position.TrackID, position.Medium, position.Position).Exec(ctx); err != nil {
				return fmt.Errorf("replace incoming matches: insert candidate position: %w", err)
			}
		}
	}
	return nil
}

// Get returns nil for not evaluated and a non-nil header (possibly without any
// candidates) for evaluated, including evaluated-empty.
func (repository *IncomingMatchCandidateRepository) Get(ctx context.Context, groupID uuid.UUID) (*IncomingMatchEvaluationHeader, []IncomingMatchCandidate, error) {
	var header IncomingMatchEvaluationHeader
	var candidates []IncomingMatchCandidate
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		err := tx.NewRaw(`SELECT group_id, evaluation_revision, evaluation_evidence, group_revision, groups_revision, grouping_epoch,
		policy_version, provider_id, provider_source_id, provider_configuration_identity, provider_namespace, provider_endpoint,
		cache_key, cache_revision, provider_snapshot_revision, provider_fences, evaluated_at
		FROM incoming_match_evaluation WHERE group_id=?`, groupID).Scan(ctx, &header)
		if errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err != nil {
			return fmt.Errorf("get incoming match evaluation: %w", err)
		}
		candidates = make([]IncomingMatchCandidate, 0)
		if err := tx.NewRaw(`SELECT file_member_id, provider_id, provider_source_id, release_id, score, evidence
		FROM incoming_match_candidate WHERE group_id=? ORDER BY file_member_id, score DESC, release_id`, groupID).Scan(ctx, &candidates); err != nil {
			return fmt.Errorf("get incoming match candidates: %w", err)
		}
		for i := range candidates {
			if err := tx.NewRaw(`SELECT track_id, medium, position FROM incoming_match_candidate_track
			WHERE group_id=? AND file_member_id=? AND provider_source_id=? AND release_id=? ORDER BY medium, position`,
				groupID, candidates[i].FileMemberID, candidates[i].ProviderSourceID, candidates[i].ReleaseID).Scan(ctx, &candidates[i].Positions); err != nil {
				return fmt.Errorf("get incoming match candidate positions: %w", err)
			}
		}
		return nil
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return &header, candidates, nil
}
