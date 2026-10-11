package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/matchingpolicy"
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
	ReleaseKeys     []string                           `json:"release_keys"`
	AcoustID        []IncomingMatchAcoustIDFence       `json:"acoustid,omitempty"`
	AcoustIDSources []IncomingMatchAcoustIDSourceFence `json:"acoustid_sources,omitempty"`
}

// IncomingMatchAcoustIDFence binds optional scoring evidence to its exact
// per-file request key and accepted payload revision.
type IncomingMatchAcoustIDFence struct {
	FileMemberID     uuid.UUID                               `json:"file_member_id"`
	CacheKey         string                                  `json:"cache_key"`
	AcceptedRevision string                                  `json:"accepted_revision"`
	SourceInputs     []IncomingMatchAcoustIDSourceInputFence `json:"source_inputs"`
}

// IncomingMatchAcoustIDSourceFence retains every current source-work identity
// used for a requested per-file key, even when no accepted response exists.
type IncomingMatchAcoustIDSourceFence struct {
	FileMemberID             uuid.UUID                               `json:"file_member_id"`
	CacheKey                 string                                  `json:"cache_key"`
	ExpectedAcceptedRevision *string                                 `json:"expected_accepted_revision"`
	SourceInputs             []IncomingMatchAcoustIDSourceInputFence `json:"source_inputs"`
}

type IncomingMatchAcoustIDSourceInputFence struct {
	Capture         IncomingGroupingCapture `json:"capture"`
	RequestCacheKey string                  `json:"request_cache_key"`
}

type IncomingMatchEvaluation struct {
	// ScopeVersion 4 marks a persisted acquisition seal, rather than an
	// immutable search-key plan, as the authority for candidate scope.
	ScopeVersion           int
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

type IncomingMatchReviewSnapshot struct {
	Header     *IncomingMatchEvaluationHeader
	Candidates []IncomingMatchCandidate
	Members    []uuid.UUID
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

// lockAndValidateMatchingPrelude establishes the shared mutation/configuration
// gates before locking source, grouping, and provider state. Both evaluation
// publication and decision application must follow this exact order.
func lockAndValidateMatchingPrelude(ctx context.Context, tx bun.Tx, evaluation IncomingMatchEvaluation, operation string) error {
	if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
		return fmt.Errorf("%s: lock source mutation gate: %w", operation, err)
	}
	keys := matchingAcoustIDCacheKeys(evaluation.Provider)
	if len(keys) != 0 {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(hashtext(?))`, "acoustid-config"); err != nil {
			return fmt.Errorf("%s: lock AcoustID configuration: %w", operation, err)
		}
		if err := lockAcoustIDCacheKeys(ctx, tx, officialAcoustIDSourceID(), keys, false); err != nil {
			return fmt.Errorf("%s: lock AcoustID response keys: %w", operation, err)
		}
	}
	if err := lockCurrentIncomingSourceRows(ctx, tx); err != nil {
		return err
	}
	var state struct {
		Revision     int64 `bun:"revision"`
		NeedsRefresh bool  `bun:"needs_refresh"`
	}
	if err := tx.NewRaw(`SELECT revision, needs_refresh FROM incoming_grouping_state WHERE singleton=true FOR SHARE`).Scan(ctx, &state); err != nil {
		return fmt.Errorf("%s: lock grouping state: %w", operation, err)
	}
	if state.Revision != evaluation.ExpectedGroupingEpoch || state.NeedsRefresh {
		return ErrIncomingMatchConflict
	}
	if err := validateCurrentMatchGroups(ctx, tx, evaluation); err != nil {
		return err
	}
	return nil
}

func matchingAcoustIDCacheKeys(fence IncomingMatchProviderFence) []string {
	unique := make(map[string]bool, len(fence.AcoustID)+len(fence.AcoustIDSources))
	for _, response := range fence.AcoustID {
		if response.CacheKey != "" {
			unique[response.CacheKey] = true
		}
	}
	for _, source := range fence.AcoustIDSources {
		if source.CacheKey != "" {
			unique[source.CacheKey] = true
		}
	}
	keys := make([]string, 0, len(unique))
	for key := range unique {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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
		return replaceIncomingMatchesTx(ctx, tx, evaluation, expected)
	})
}

// replaceIncomingMatchesTx publishes evaluated results inside a caller-owned
// transaction. Keep the shared validation, lock, CAS, and write path here so
// standalone evaluation and durable delivery publication have identical fences.
func replaceIncomingMatchesTx(ctx context.Context, tx bun.Tx, evaluation IncomingMatchEvaluation, expected map[uuid.UUID]*string) error {
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
	// Canonical lock order: source mutation gate, AcoustID configuration,
	// ordered cache-key gates, current source/group state, provider rows, then
	// evaluation revisions. Apply and publication share this exact prelude.
	if err := lockAndValidateMatchingPrelude(ctx, tx, evaluation, "replace incoming matches"); err != nil {
		return err
	}
	if err := lockAndValidateMatchProvider(ctx, tx, evaluation); err != nil {
		return err
	}
	orderedGroups := append([]IncomingMatchGroupEvaluation(nil), evaluation.Groups...)
	sort.Slice(orderedGroups, func(i, j int) bool { return orderedGroups[i].GroupID.String() < orderedGroups[j].GroupID.String() })
	for _, group := range orderedGroups {
		if expected == nil {
			continue
		}
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
	// All group revisions are locked and compared before the first header is
	// removed, so no group can be published from a partially acquired snapshot.
	for _, group := range evaluation.Groups {
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
}

func validateIncomingMatchEvaluation(evaluation IncomingMatchEvaluation) error {
	if (evaluation.ScopeVersion != 0 && evaluation.ScopeVersion != 4) || evaluation.ExpectedGroupsRevision == "" || evaluation.ExpectedGroupingEpoch < 0 || !matchingpolicy.Supported(evaluation.PolicyVersion) ||
		evaluation.Provider.ProviderID == uuid.Nil || evaluation.Provider.ProviderSourceID == uuid.Nil ||
		evaluation.Provider.Namespace == "" || evaluation.Provider.Endpoint == "" || evaluation.Provider.ConfigurationIdentity == "" || evaluation.Provider.CacheKey == "" || evaluation.Provider.AcceptedProjectionRevision == "" || len(evaluation.Groups) == 0 {
		return fmt.Errorf("replace incoming matches: complete snapshot and provider fences are required")
	}
	if !canonicalReleaseKeys(evaluation.Provider.ReleaseKeys) {
		return fmt.Errorf("replace incoming matches: evaluated release keys must be sorted, deduplicated, and individually non-empty")
	}
	seenAcoustID := make(map[uuid.UUID]bool, len(evaluation.Provider.AcoustID))
	for _, fence := range evaluation.Provider.AcoustID {
		if fence.FileMemberID == uuid.Nil || fence.CacheKey == "" || fence.AcceptedRevision == "" || len(fence.SourceInputs) == 0 || seenAcoustID[fence.FileMemberID] {
			return fmt.Errorf("replace incoming matches: AcoustID cache fence is invalid")
		}
		matchedKey := false
		seenWork := make(map[uuid.UUID]bool, len(fence.SourceInputs))
		for _, input := range fence.SourceInputs {
			capture := input.Capture
			if capture.WorkID == uuid.Nil || capture.VariantID != fence.FileMemberID || capture.FingerprintResultID == nil || capture.FingerprintWinner == nil || capture.Fingerprint == nil || capture.FingerprintVersion == nil || capture.FingerprintAlgorithmNamespace == nil || capture.FingerprintAlgorithmID == nil || capture.FingerprintParserVersion == nil || input.RequestCacheKey == "" || seenWork[capture.WorkID] {
				return fmt.Errorf("replace incoming matches: AcoustID source input is invalid")
			}
			seenWork[capture.WorkID] = true
			if input.RequestCacheKey == fence.CacheKey {
				matchedKey = true
			}
			key, err := acoustIDRequestCacheKey(capture)
			if err != nil || key != input.RequestCacheKey {
				return fmt.Errorf("replace incoming matches: AcoustID request key differs from copied source input")
			}
		}
		if !matchedKey {
			return fmt.Errorf("replace incoming matches: AcoustID request key does not identify a current source input")
		}
		seenAcoustID[fence.FileMemberID] = true
	}
	seenAcoustIDSources := make(map[uuid.UUID]bool, len(evaluation.Provider.AcoustIDSources))
	for _, fence := range evaluation.Provider.AcoustIDSources {
		if fence.FileMemberID == uuid.Nil || fence.CacheKey == "" || fence.ExpectedAcceptedRevision != nil && *fence.ExpectedAcceptedRevision == "" || len(fence.SourceInputs) == 0 || seenAcoustIDSources[fence.FileMemberID] {
			return fmt.Errorf("replace incoming matches: AcoustID source fence is invalid")
		}
		matchedKey := false
		seenWork := make(map[uuid.UUID]bool, len(fence.SourceInputs))
		for _, input := range fence.SourceInputs {
			capture := input.Capture
			if capture.WorkID == uuid.Nil || capture.VariantID != fence.FileMemberID || input.RequestCacheKey == "" || seenWork[capture.WorkID] {
				return fmt.Errorf("replace incoming matches: AcoustID source input is invalid")
			}
			seenWork[capture.WorkID] = true
			if input.RequestCacheKey == fence.CacheKey {
				matchedKey = true
			}
			key, err := acoustIDRequestCacheKey(capture)
			if err != nil || key != input.RequestCacheKey {
				return fmt.Errorf("replace incoming matches: AcoustID request key differs from copied source input")
			}
		}
		if !matchedKey {
			return fmt.Errorf("replace incoming matches: AcoustID source key does not identify a current source input")
		}
		seenAcoustIDSources[fence.FileMemberID] = true
	}
	for _, fence := range evaluation.Provider.AcoustID {
		sourceFound := false
		for _, source := range evaluation.Provider.AcoustIDSources {
			if source.FileMemberID == fence.FileMemberID && source.CacheKey == fence.CacheKey &&
				source.ExpectedAcceptedRevision != nil && *source.ExpectedAcceptedRevision == fence.AcceptedRevision && reflect.DeepEqual(source.SourceInputs, fence.SourceInputs) {
				sourceFound = true
				break
			}
		}
		if !sourceFound {
			return fmt.Errorf("replace incoming matches: AcoustID response fence lacks its source fence")
		}
	}
	seenGroups := make(map[uuid.UUID]bool, len(evaluation.Groups))
	evaluatedMembers := make(map[uuid.UUID]bool)
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
			evaluatedMembers[member.FileMemberID] = true
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
	for _, acoustID := range evaluation.Provider.AcoustIDSources {
		if !evaluatedMembers[acoustID.FileMemberID] {
			return fmt.Errorf("replace incoming matches: AcoustID source input is outside evaluated members")
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
	var currentGroups []struct {
		ID       uuid.UUID `bun:"id"`
		Revision string    `bun:"revision"`
	}
	if err := tx.NewRaw(`SELECT id, revision FROM incoming_group ORDER BY id FOR SHARE`).Scan(ctx, &currentGroups); err != nil {
		return fmt.Errorf("replace incoming matches: lock current groups: %w", err)
	}
	identities := make([]struct {
		ID       string   `json:"id"`
		Revision string   `json:"revision"`
		Members  []string `json:"members"`
	}, 0, len(currentGroups))
	for _, group := range currentGroups {
		var members []uuid.UUID
		if err := tx.NewRaw(`SELECT variant_id FROM incoming_group_member WHERE group_id=? AND ready=true ORDER BY variant_id FOR SHARE`, group.ID).Scan(ctx, &members); err != nil {
			return fmt.Errorf("replace incoming matches: lock current group members: %w", err)
		}
		if len(members) == 0 {
			continue
		}
		identity := struct {
			ID       string   `json:"id"`
			Revision string   `json:"revision"`
			Members  []string `json:"members"`
		}{ID: group.ID.String(), Revision: group.Revision, Members: make([]string, len(members))}
		for memberIndex, member := range members {
			identity.Members[memberIndex] = member.String()
		}
		identities = append(identities, identity)
	}
	encoded, err := json.Marshal(identities)
	if err != nil {
		return fmt.Errorf("replace incoming matches: encode current groups revision: %w", err)
	}
	revision := sha256.Sum256(encoded)
	if evaluation.ExpectedGroupsRevision == "" || hex.EncodeToString(revision[:]) != evaluation.ExpectedGroupsRevision {
		return ErrIncomingMatchConflict
	}
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
	byVariant := make(map[uuid.UUID][]IncomingGroupingCapture, len(current))
	for _, capture := range current {
		byVariant[capture.VariantID] = append(byVariant[capture.VariantID], capture)
	}
	for _, group := range evaluation.Groups {
		for _, member := range group.Members {
			captures := byVariant[member.FileMemberID]
			matched := false
			for _, capture := range captures {
				if capture.Ready && capture.WorkID == member.WorkID && equalOptionalBytes(capture.SourceSHA256, member.SourceSHA256) {
					matched = true
					break
				}
			}
			if !matched {
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
	queryRevisions := effectiveMatchCacheRevisions(fence)
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
	if len(fence.AcoustID) != 0 || len(fence.AcoustIDSources) != 0 {
		var sourceID, providerID uuid.UUID
		var endpoint, identity string
		if err := tx.NewRaw(`SELECT source.id, source.provider_id, source.endpoint, source.configuration_identity
			FROM provider_source source JOIN provider ON provider.id=source.provider_id WHERE provider.code='acoustid'`).Scan(ctx, &sourceID, &providerID, &endpoint, &identity); err != nil || identity != acoustIDSourceIdentity || endpoint != acoustIDOfficialEndpoint {
			return ErrIncomingMatchConflict
		}
		seenFiles := make(map[uuid.UUID]bool, len(fence.AcoustIDSources))
		currentCaptures, err := loadIncomingGroupingCaptures(ctx, tx)
		if err != nil {
			return err
		}
		capturesByFile := make(map[uuid.UUID][]IncomingGroupingCapture)
		for _, capture := range currentCaptures {
			capturesByFile[capture.VariantID] = append(capturesByFile[capture.VariantID], capture)
		}
		for _, expected := range fence.AcoustIDSources {
			if seenFiles[expected.FileMemberID] {
				return ErrIncomingMatchConflict
			}
			seenFiles[expected.FileMemberID] = true
			current := capturesByFile[expected.FileMemberID]
			if len(current) != len(expected.SourceInputs) {
				return ErrIncomingMatchConflict
			}
			sort.Slice(current, func(i, j int) bool { return current[i].WorkID.String() < current[j].WorkID.String() })
			copied := append([]IncomingMatchAcoustIDSourceInputFence(nil), expected.SourceInputs...)
			sort.Slice(copied, func(i, j int) bool { return copied[i].Capture.WorkID.String() < copied[j].Capture.WorkID.String() })
			for index, capture := range current {
				// CurrentCaptures joins only a work item whose root, location, and
				// inventory facts still agree. Recheck the operator-visible root
				// state as well: a disabled or stale root is not a current source.
				if !capture.Ready || !capture.RootEnabled || capture.RootStale ||
					capture.LocationID == uuid.Nil || capture.RootID == uuid.Nil ||
					capture.ConfiguredPath == "" || capture.InventoryPath != capture.ConfiguredPath ||
					capture.RelativePath == "" || capture.SizeBytes < 0 ||
					!reflect.DeepEqual(capture, copied[index].Capture) {
					return ErrIncomingMatchConflict
				}
				requestKey, keyErr := acoustIDRequestCacheKey(capture)
				if keyErr != nil || requestKey != copied[index].RequestCacheKey {
					return ErrIncomingMatchConflict
				}
			}
			if len(copied) == 0 {
				return ErrIncomingMatchConflict
			}
		}
		for _, expected := range fence.AcoustIDSources {
			var payload []byte
			err := tx.NewRaw(`SELECT payload FROM provider_response_cache WHERE provider_source_id=? AND provider_id=? AND cache_key=? FOR SHARE`, sourceID, providerID, expected.CacheKey).Scan(ctx, &payload)
			if errors.Is(err, sql.ErrNoRows) {
				if expected.ExpectedAcceptedRevision != nil {
					return ErrIncomingMatchConflict
				}
				continue
			}
			if err != nil {
				return ErrIncomingMatchConflict
			}
			if len(payload) == 0 {
				if expected.ExpectedAcceptedRevision != nil {
					return ErrIncomingMatchConflict
				}
				continue
			}
			digest := sha256.Sum256(payload)
			if expected.ExpectedAcceptedRevision == nil || hex.EncodeToString(digest[:]) != *expected.ExpectedAcceptedRevision {
				return ErrIncomingMatchConflict
			}
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

// GetReviewSnapshot reads only persisted evaluation state in one consistent
// transaction; candidate positions are batched to avoid per-candidate queries.
func (repository *IncomingMatchCandidateRepository) GetReviewSnapshot(ctx context.Context, groupID uuid.UUID) (*IncomingMatchReviewSnapshot, error) {
	result := &IncomingMatchReviewSnapshot{}
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		var header IncomingMatchEvaluationHeader
		err := tx.NewRaw(`SELECT group_id, evaluation_revision, evaluation_evidence, group_revision, groups_revision, grouping_epoch,
		policy_version, provider_id, provider_source_id, provider_configuration_identity, provider_namespace, provider_endpoint,
		cache_key, cache_revision, provider_snapshot_revision, provider_fences, evaluated_at FROM incoming_match_evaluation WHERE group_id=?`, groupID).Scan(ctx, &header)
		if errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err != nil {
			return fmt.Errorf("get incoming match review: %w", err)
		}
		result.Header = &header
		result.Candidates = make([]IncomingMatchCandidate, 0)
		if err := tx.NewRaw(`SELECT file_member_id, provider_id, provider_source_id, release_id, score, evidence FROM incoming_match_candidate WHERE group_id=? ORDER BY file_member_id, release_id`, groupID).Scan(ctx, &result.Candidates); err != nil {
			return fmt.Errorf("get incoming match review candidates: %w", err)
		}
		result.Members = make([]uuid.UUID, 0)
		if err := tx.NewRaw(`SELECT file_member_id FROM incoming_match_evaluation_member WHERE group_id=? ORDER BY file_member_id`, groupID).Scan(ctx, &result.Members); err != nil {
			return fmt.Errorf("get incoming match review members: %w", err)
		}
		type positionRow struct {
			FileMemberID     uuid.UUID `bun:"file_member_id"`
			ProviderSourceID uuid.UUID `bun:"provider_source_id"`
			ReleaseID        uuid.UUID `bun:"release_id"`
			TrackID          uuid.UUID `bun:"track_id"`
			Medium           int
			Position         int
		}
		positions := make([]positionRow, 0)
		if err := tx.NewRaw(`SELECT file_member_id, provider_source_id, release_id, track_id, medium, position FROM incoming_match_candidate_track WHERE group_id=? ORDER BY file_member_id, release_id, medium, position, track_id`, groupID).Scan(ctx, &positions); err != nil {
			return fmt.Errorf("get incoming match review positions: %w", err)
		}
		byCandidate := make(map[string]int, len(result.Candidates))
		for i := range result.Candidates {
			c := &result.Candidates[i]
			byCandidate[c.FileMemberID.String()+"/"+c.ReleaseID.String()] = i
			c.Positions = make([]IncomingMatchPosition, 0)
		}
		for _, position := range positions {
			if i, ok := byCandidate[position.FileMemberID.String()+"/"+position.ReleaseID.String()]; ok {
				result.Candidates[i].Positions = append(result.Candidates[i].Positions, IncomingMatchPosition{TrackID: position.TrackID, Medium: position.Medium, Position: position.Position})
			}
		}
		return nil
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}
