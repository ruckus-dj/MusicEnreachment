package persistence

import (
	"bytes"
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

// matchingProviderRefreshReviewEvidence is the server-owned marker that turns
// an otherwise automatic artist-catalog evaluation into review-only evidence.
// It keeps the persisted authority mode automatic while revoking every direct
// apply and automatic enqueue path.
const matchingProviderRefreshReviewEvidence = "provider_refresh_review"

type matchingProviderRefreshReviewMarker struct {
	Version int `json:"version"`
}

// ArtistMatchingReviewRefreshCandidate identifies one current, automatic
// version-2 artist-catalog evaluation whose committed acquisition scope can be
// re-evaluated against the current provider projection without another network
// request. Provider is the evaluation fence persisted with that revision and is
// the drift baseline for callers.
type ArtistMatchingReviewRefreshCandidate struct {
	GroupID            uuid.UUID
	RunID              uuid.UUID
	EvaluationRevision string
	Provider           IncomingMatchProviderFence
	PairThreshold      string
}

// ListArtistMatchingReviewRefreshCandidates returns one page of eligible
// current automatic artist-catalog evaluations in ascending group order. The
// keyset cursor is the last group ID from the previous page.
func (repository *SetupManagerRepository) ListArtistMatchingReviewRefreshCandidates(ctx context.Context, after uuid.UUID, limit int) ([]ArtistMatchingReviewRefreshCandidate, error) {
	if limit < 1 || limit > 256 {
		return nil, fmt.Errorf("list artist matching review refresh candidates: limit must be between 1 and 256")
	}
	var rows []struct {
		GroupID            uuid.UUID `bun:"group_id"`
		RunID              uuid.UUID `bun:"run_id"`
		EvaluationRevision string    `bun:"evaluation_revision"`
		ProviderFences     []byte    `bun:"provider_fences"`
		ProviderSourceID   uuid.UUID `bun:"provider_source_id"`
		Configuration      string    `bun:"provider_configuration_identity"`
		ProviderID         uuid.UUID `bun:"provider_id"`
		Namespace          string    `bun:"namespace"`
		Endpoint           string    `bun:"endpoint"`
		PairThreshold      string    `bun:"pair_threshold"`
	}
	if err := repository.db.NewRaw(`SELECT evaluation.group_id, evaluation.target_run_id AS run_id,
			evaluation.evaluation_revision, evaluation.provider_fences,
			run.provider_source_id, run.provider_configuration_identity,
			source.provider_id, source.namespace, source.endpoint,
			COALESCE(evaluation.evaluation_evidence->>'layout_pair_threshold',
				acquisition.intent->'layout_target'->>'pair_threshold', '') AS pair_threshold
		FROM incoming_match_evaluation evaluation
		JOIN matching_run run ON run.id=evaluation.target_run_id
		JOIN provider_source source ON source.id=run.provider_source_id
		JOIN matching_acquisition acquisition ON acquisition.run_id=run.id
		JOIN matching_run_target target ON target.run_id=run.id AND target.group_id=evaluation.group_id
		JOIN incoming_matching_generation generation ON generation.group_id=evaluation.group_id
		JOIN incoming_group current_group ON current_group.id=evaluation.group_id
		JOIN app_setting config ON config.setting_name='musicbrainz_config_identity'
			AND config.setting_value=run.provider_configuration_identity
		LEFT JOIN incoming_group_matching_authority authority ON authority.group_id=evaluation.group_id
		WHERE evaluation.group_id > ?
			AND evaluation.evaluation_revision IS NOT NULL
			AND evaluation.fence_version=2
			AND evaluation.target_run_id IS NOT NULL
			AND evaluation.frozen_authority_version IS NULL
			AND evaluation.matching_authority_mode='automatic'
			AND evaluation.matching_authority_revision=target.authority_revision
			AND evaluation.group_revision=target.captured_group_revision
			AND evaluation.provider_source_id=run.provider_source_id
			AND evaluation.provider_configuration_identity=run.provider_configuration_identity
			AND run.fence_version=2
			AND run.state='succeeded'
			AND run.quiesced_at IS NULL
			AND run.active_river_job_id IS NULL
			AND acquisition.intent_version=2
			AND acquisition.current_phase='evaluation_sealed'
			AND run.immutable_plan->>'schema_version'='4'
			AND run.immutable_plan->>'intent'='automatic_artist_acquisition'
			AND run.immutable_plan->>'mode'='automatic'
			AND target.target_capture IS NOT NULL
			AND generation.current_run_id=run.id
			AND generation.high_water=target.target_generation
			AND (target.reconciliation_generation IS NULL OR generation.requested_generation=target.reconciliation_generation)
			AND current_group.revision=target.captured_group_revision
			AND COALESCE(authority.mode,'automatic')='automatic'
			AND COALESCE(authority.revision,0)=target.authority_revision
			AND EXISTS (SELECT 1 FROM matching_acquisition_seal seal WHERE seal.run_id=run.id)
			AND NOT EXISTS (SELECT 1 FROM incoming_grouping_state state WHERE state.singleton=true AND state.needs_refresh=true)
			AND NOT EXISTS (
				SELECT 1 FROM matching_delivery delivery JOIN river_job job ON job.id=delivery.river_job_id
				WHERE delivery.run_id=run.id AND delivery.consumed_at IS NULL AND job.finalized_at IS NULL
			)
		ORDER BY evaluation.group_id
		LIMIT ?`, after, limit).Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("list artist matching review refresh candidates: %w", err)
	}
	candidates := make([]ArtistMatchingReviewRefreshCandidate, 0, len(rows))
	for _, row := range rows {
		var provider IncomingMatchProviderFence
		if err := json.Unmarshal(row.ProviderFences, &provider); err != nil ||
			validateArtistMatchingReviewProviderFence(provider) != nil ||
			provider.ProviderSourceID != row.ProviderSourceID || provider.ConfigurationIdentity != row.Configuration ||
			provider.ProviderID != row.ProviderID || provider.Namespace != row.Namespace || provider.Endpoint != row.Endpoint {
			// Return the row's key so a malformed fence does not stall the scan.
			// The detector reports the invalid candidate without evaluating it.
			provider = IncomingMatchProviderFence{}
		}
		candidates = append(candidates, ArtistMatchingReviewRefreshCandidate{
			GroupID: row.GroupID, RunID: row.RunID,
			EvaluationRevision: row.EvaluationRevision, Provider: provider, PairThreshold: row.PairThreshold,
		})
	}
	return candidates, nil
}

func validateArtistMatchingReviewProviderFence(fence IncomingMatchProviderFence) error {
	if fence.ProviderID == uuid.Nil || fence.ProviderSourceID == uuid.Nil ||
		strings.TrimSpace(fence.Namespace) == "" || fence.Endpoint == "" || fence.ConfigurationIdentity == "" ||
		fence.CacheKey == "" || len(mustDecodeSHA256(fence.AcceptedProjectionRevision)) != sha256.Size ||
		len(fence.ReleaseKeys) == 0 || !canonicalReleaseKeys(fence.ReleaseKeys) ||
		len(fence.AcoustID) != 0 || len(fence.AcoustIDSources) != 0 {
		return fmt.Errorf("artist matching review provider fence is incomplete")
	}
	revisions := effectiveMatchCacheRevisions(fence)
	if len(revisions) < 2 {
		return fmt.Errorf("artist matching review provider fence has an incomplete response scope")
	}
	if _, ok := revisions[fence.CacheKey]; !ok {
		return fmt.Errorf("artist matching review provider fence lacks its primary cache key")
	}
	for key := range revisions {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("artist matching review provider fence contains an empty cache key")
		}
	}
	return nil
}

func validateArtistMatchingReviewCandidate(candidate ArtistMatchingReviewRefreshCandidate) error {
	if candidate.GroupID == uuid.Nil || candidate.RunID == uuid.Nil || candidate.EvaluationRevision == "" {
		return fmt.Errorf("artist matching review refresh candidate is incomplete")
	}
	return validateArtistMatchingReviewProviderFence(candidate.Provider)
}

// LoadArtistMatchingReviewRefreshScope returns the exact immutable version-2
// artist-catalog scope saved by the acquisition seal for the candidate run. It
// revalidates the canonical seal digest, the sealed phase binding, the accepted
// catalog manifest digest, and the historical membership of the exact release
// lookup dependencies. It deliberately never compares the historical payload
// digests with today's mutable response cache: a reviewer refresh exists
// precisely because the provider projection changed.
func (repository *SetupManagerRepository) LoadArtistMatchingReviewRefreshScope(ctx context.Context, candidate ArtistMatchingReviewRefreshCandidate) (MatchingAcquisitionEvaluationInput, bool, error) {
	if err := validateArtistMatchingReviewCandidate(candidate); err != nil {
		return MatchingAcquisitionEvaluationInput{}, false, fmt.Errorf("load artist matching review refresh scope: %w", err)
	}
	var input MatchingAcquisitionEvaluationInput
	var exists bool
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		binding, found, err := readArtistMatchingReviewRefreshRun(ctx, tx, candidate.RunID)
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		if binding.GroupID != candidate.GroupID {
			return fmt.Errorf("load artist matching review refresh scope: immutable target differs from candidate")
		}
		scope, found, err := readArtistMatchingReviewRefreshSealedScope(ctx, tx, candidate.RunID)
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		if len(scope.Input.GroupIDs) != 1 || scope.Input.GroupIDs[0] != candidate.GroupID {
			return fmt.Errorf("load artist matching review refresh scope: sealed scope differs from candidate")
		}
		input, exists = scope.Input, true
		return nil
	})
	if err != nil {
		return MatchingAcquisitionEvaluationInput{}, false, fmt.Errorf("load artist matching review refresh scope: %w", err)
	}
	return input, exists, nil
}

type artistMatchingReviewRefreshRunBinding struct {
	ProviderID                    uuid.UUID
	ProviderSourceID              uuid.UUID
	ProviderConfigurationIdentity string
	PolicyVersion                 string
	FenceVersion                  int
	GroupID                       uuid.UUID
	GroupRevision                 string
	TargetGeneration              int64
	AuthorityRevision             int64
	TargetCapture                 json.RawMessage
	TargetCaptureDigest           string
}

func readArtistMatchingReviewRefreshRun(ctx context.Context, tx bun.Tx, runID uuid.UUID) (artistMatchingReviewRefreshRunBinding, bool, error) {
	var binding artistMatchingReviewRefreshRunBinding
	var row struct {
		PlanDigest        []byte    `bun:"plan_digest"`
		Plan              []byte    `bun:"immutable_plan"`
		PolicyVersion     string    `bun:"policy_version"`
		FenceVersion      int       `bun:"fence_version"`
		ProviderSourceID  uuid.UUID `bun:"provider_source_id"`
		Configuration     string    `bun:"provider_configuration_identity"`
		ProviderID        uuid.UUID `bun:"provider_id"`
		Intent            []byte    `bun:"intent"`
		IntentDigest      []byte    `bun:"intent_digest"`
		IntentVersion     int       `bun:"intent_version"`
		GroupID           uuid.UUID `bun:"group_id"`
		GroupRevision     string    `bun:"captured_group_revision"`
		TargetGeneration  int64     `bun:"target_generation"`
		AuthorityRevision int64     `bun:"authority_revision"`
		Capture           []byte    `bun:"target_capture"`
		CaptureDigest     []byte    `bun:"target_capture_digest"`
	}
	err := tx.NewRaw(`SELECT run.plan_digest, run.immutable_plan, run.policy_version, run.fence_version,
			run.provider_source_id, run.provider_configuration_identity, source.provider_id,
			acquisition.intent, acquisition.intent_digest, acquisition.intent_version,
			target.group_id, target.captured_group_revision, target.target_generation,
			target.authority_revision, target.target_capture, target.target_capture_digest
		FROM matching_run run
		JOIN provider_source source ON source.id=run.provider_source_id
		JOIN matching_acquisition acquisition ON acquisition.run_id=run.id
		JOIN matching_run_target target ON target.run_id=run.id
		WHERE run.id=? AND run.state='succeeded' AND run.quiesced_at IS NULL
			AND run.active_river_job_id IS NULL`, runID).Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return binding, false, nil
	}
	if err != nil {
		return binding, false, fmt.Errorf("read artist matching review run: %w", err)
	}
	if row.FenceVersion != 2 || row.IntentVersion != matchingAutomaticAcquisitionIntentVersion {
		return binding, false, fmt.Errorf("read artist matching review run: run is not a version-2 artist-catalog acquisition")
	}
	var intent MatchingAcquisitionIntent
	if err := json.Unmarshal(row.Intent, &intent); err != nil || intent.SchemaVersion != matchingAutomaticAcquisitionIntentVersion || intent.FenceVersion != 2 {
		return binding, false, fmt.Errorf("read artist matching review run: saved acquisition intent is invalid")
	}
	if err := validateCanonicalMatchingAcquisitionIntent(intent, row.IntentDigest, row.PlanDigest, row.Plan); err != nil {
		return binding, false, fmt.Errorf("read artist matching review run: %w", err)
	}
	var plan struct {
		SchemaVersion       int    `json:"schema_version"`
		Intent              string `json:"intent"`
		Mode                string `json:"mode"`
		TargetCaptureDigest string `json:"target_capture_digest"`
		PolicyVersion       string `json:"policy_version"`
	}
	if err := json.Unmarshal(row.Plan, &plan); err != nil || plan.SchemaVersion != 4 || plan.Intent != "automatic_artist_acquisition" ||
		plan.Mode != "automatic" || plan.PolicyVersion != row.PolicyVersion || plan.TargetCaptureDigest == "" {
		return binding, false, fmt.Errorf("read artist matching review run: immutable artist-catalog plan is invalid")
	}
	if intent.TargetCaptureDigest != plan.TargetCaptureDigest || intent.PolicyVersion != row.PolicyVersion ||
		len(intent.Targets) != 1 || intent.Targets[0].GroupID != row.GroupID || intent.Targets[0].Revision != row.GroupRevision {
		return binding, false, fmt.Errorf("read artist matching review run: immutable target differs from saved intent")
	}
	canonicalCapture, digest, err := automaticArtistTargetCaptureDigest(row.Capture)
	if err != nil || !equalDigest(digest[:], row.CaptureDigest) || hex.EncodeToString(digest[:]) != plan.TargetCaptureDigest {
		return binding, false, fmt.Errorf("read artist matching review run: immutable target capture digest changed")
	}
	var capture automaticArtistTargetCapture
	if err := json.Unmarshal(canonicalCapture, &capture); err != nil || capture.SchemaVersion != 2 ||
		capture.GroupID != row.GroupID || capture.GroupRevision != row.GroupRevision {
		return binding, false, fmt.Errorf("read artist matching review run: immutable target capture is invalid")
	}
	binding = artistMatchingReviewRefreshRunBinding{
		ProviderID: row.ProviderID, ProviderSourceID: row.ProviderSourceID,
		ProviderConfigurationIdentity: row.Configuration, PolicyVersion: row.PolicyVersion,
		FenceVersion: row.FenceVersion, GroupID: row.GroupID, GroupRevision: row.GroupRevision,
		TargetGeneration: row.TargetGeneration, AuthorityRevision: row.AuthorityRevision,
		TargetCapture: append(json.RawMessage(nil), canonicalCapture...), TargetCaptureDigest: hex.EncodeToString(digest[:]),
	}
	return binding, true, nil
}

type artistMatchingReviewRefreshSealedScope struct {
	Input       MatchingAcquisitionEvaluationInput
	CatalogKeys []string
	LookupKeys  []string
}

func readArtistMatchingReviewRefreshSealedScope(ctx context.Context, tx bun.Tx, runID uuid.UUID) (artistMatchingReviewRefreshSealedScope, bool, error) {
	var scope artistMatchingReviewRefreshSealedScope
	var row struct {
		Input           []byte `bun:"input"`
		InputDigest     []byte `bun:"input_digest"`
		CatalogDigest   []byte `bun:"catalog_digest"`
		SealedRevision  int64  `bun:"acquisition_revision"`
		CurrentRevision int64  `bun:"revision"`
		CurrentPhase    string `bun:"current_phase"`
		PhaseName       string `bun:"phase"`
	}
	err := tx.NewRaw(`SELECT seal.input, seal.input_digest, seal.catalog_digest, seal.acquisition_revision,
			acquisition.revision, acquisition.current_phase, phase.phase
		FROM matching_acquisition_seal seal
		JOIN matching_acquisition acquisition ON acquisition.run_id=seal.run_id
		JOIN matching_acquisition_phase phase ON phase.run_id=seal.run_id AND phase.revision=acquisition.revision
		WHERE seal.run_id=?`, runID).Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return scope, false, nil
	}
	if err != nil {
		return scope, false, fmt.Errorf("read artist matching review seal: %w", err)
	}
	if row.CurrentPhase != "evaluation_sealed" || row.PhaseName != "evaluation_sealed" || row.SealedRevision != row.CurrentRevision {
		return scope, false, fmt.Errorf("read artist matching review seal: ledger phase does not match immutable seal")
	}
	var decoded MatchingAcquisitionEvaluationInput
	if err := json.Unmarshal(row.Input, &decoded); err != nil {
		return scope, false, fmt.Errorf("read artist matching review seal: decode input: %w", err)
	}
	input, _, digest, err := canonicalMatchingAcquisitionInput(decoded)
	if err != nil || !equalDigest(row.InputDigest, digest[:]) {
		return scope, false, fmt.Errorf("read artist matching review seal: input digest or encoding is invalid")
	}
	if input.SchemaVersion != 2 || len(input.GroupIDs) != 1 || len(input.ReleaseIDs) == 0 || len(input.CatalogCacheKeys) == 0 {
		return scope, false, fmt.Errorf("read artist matching review seal: saved scope is not a version-2 artist-catalog selection")
	}
	catalogDigest, catalogKeys, err := artistMatchingReviewCatalogManifest(ctx, tx, runID)
	if err != nil {
		return scope, false, err
	}
	if !equalDigest(row.CatalogDigest, catalogDigest[:]) {
		return scope, false, fmt.Errorf("read artist matching review seal: accepted catalog manifest changed")
	}
	if !equalSortedStrings(input.CatalogCacheKeys, catalogKeys) {
		return scope, false, fmt.Errorf("read artist matching review seal: sealed catalog keys differ from catalog manifest")
	}
	lookupKeys, err := validateArtistMatchingReviewHydrationMembership(ctx, tx, runID, row.SealedRevision, input)
	if err != nil {
		return scope, false, err
	}
	if !equalSortedStrings(input.ReleaseLookupCacheKeys, lookupKeys) {
		return scope, false, fmt.Errorf("read artist matching review seal: sealed lookup keys differ from hydration manifest")
	}
	scope = artistMatchingReviewRefreshSealedScope{Input: input, CatalogKeys: catalogKeys, LookupKeys: lookupKeys}
	return scope, true, nil
}

type artistMatchingReviewManifestPage struct {
	Revision   int64     `json:"revision"`
	RequestID  uuid.UUID `json:"request_id"`
	CacheKey   string    `json:"cache_key"`
	PayloadSHA string    `json:"payload_sha256"`
}

// artistMatchingReviewCatalogManifest recomputes the accepted catalog digest
// from the append-only phase provenance alone. Response payloads are immutable
// historical evidence for this path, so today's cache must not participate.
func artistMatchingReviewCatalogManifest(ctx context.Context, tx bun.Tx, runID uuid.UUID) ([32]byte, []string, error) {
	var phases []struct {
		Revision   int64  `bun:"revision"`
		Phase      string `bun:"phase"`
		Provenance []byte `bun:"response_provenance"`
	}
	if err := tx.NewRaw(`SELECT revision, phase, response_provenance FROM matching_acquisition_phase
		WHERE run_id=? AND phase IN ('artist_catalog_page','catalog_ready') ORDER BY revision`, runID).Scan(ctx, &phases); err != nil {
		return [32]byte{}, nil, fmt.Errorf("read artist matching review catalog phases: %w", err)
	}
	if len(phases) < 2 || phases[0].Revision != 1 || phases[len(phases)-1].Phase != "catalog_ready" {
		return [32]byte{}, nil, fmt.Errorf("read artist matching review catalog phases: durable catalog is incomplete")
	}
	manifest := make([]artistMatchingReviewManifestPage, 0, len(phases))
	keys := make([]string, 0, len(phases))
	seen := make(map[string]bool, len(phases))
	for index, phase := range phases {
		if phase.Revision != int64(index+1) {
			return [32]byte{}, nil, fmt.Errorf("read artist matching review catalog phases: phase sequence has a gap")
		}
		var provenance []matchingAcquisitionResponseProvenance
		if err := json.Unmarshal(phase.Provenance, &provenance); err != nil || len(provenance) != 1 {
			return [32]byte{}, nil, fmt.Errorf("read artist matching review catalog phases: provenance is invalid")
		}
		entry := provenance[0]
		if entry.RequestID == uuid.Nil || entry.CacheKey == "" || len(mustDecodeSHA256(entry.PayloadSHA)) != sha256.Size || seen[entry.CacheKey] {
			return [32]byte{}, nil, fmt.Errorf("read artist matching review catalog phases: provenance entry is invalid")
		}
		seen[entry.CacheKey] = true
		manifest = append(manifest, artistMatchingReviewManifestPage{
			Revision: phase.Revision, RequestID: entry.RequestID,
			CacheKey: entry.CacheKey, PayloadSHA: entry.PayloadSHA,
		})
		keys = append(keys, entry.CacheKey)
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return [32]byte{}, nil, fmt.Errorf("read artist matching review catalog phases: encode manifest: %w", err)
	}
	sort.Strings(keys)
	return sha256.Sum256(encoded), keys, nil
}

func validateArtistMatchingReviewHydrationMembership(ctx context.Context, tx bun.Tx, runID uuid.UUID, sealedRevision int64, input MatchingAcquisitionEvaluationInput) ([]string, error) {
	if sealedRevision < 2 {
		return nil, fmt.Errorf("read artist matching review hydration: sealed revision is invalid")
	}
	var phase struct {
		Input  []byte `bun:"input"`
		Output []byte `bun:"output"`
	}
	if err := tx.NewRaw(`SELECT input, output FROM matching_acquisition_phase
		WHERE run_id=? AND revision=? AND phase=?`, runID, sealedRevision-1, MatchingAcquisitionReleaseHydration).Scan(ctx, &phase); err != nil {
		return nil, fmt.Errorf("read artist matching review hydration manifest: %w", err)
	}
	var manifest struct {
		ReleaseIDs []string `json:"release_ids"`
	}
	var output struct {
		Lookups []struct {
			ReleaseID string    `json:"release_id"`
			RequestID uuid.UUID `json:"request_id"`
			CacheKey  string    `json:"cache_key"`
		} `json:"lookups"`
	}
	if json.Unmarshal(phase.Input, &manifest) != nil || json.Unmarshal(phase.Output, &output) != nil ||
		len(manifest.ReleaseIDs) != len(input.ReleaseIDs) || len(output.Lookups) != len(input.ReleaseIDs) {
		return nil, fmt.Errorf("read artist matching review hydration: manifest is incomplete")
	}
	lookupKeys := make([]string, 0, len(output.Lookups))
	for index, releaseID := range input.ReleaseIDs {
		lookup := output.Lookups[index]
		if manifest.ReleaseIDs[index] != releaseID || lookup.ReleaseID != releaseID || lookup.RequestID == uuid.Nil ||
			lookup.CacheKey != "release-lookup:"+releaseID {
			return nil, fmt.Errorf("read artist matching review hydration: lookup differs from selected releases")
		}
		lookupKeys = append(lookupKeys, lookup.CacheKey)
	}
	if len(input.ReleaseLookupPayloadSHA256) != len(lookupKeys) {
		return nil, fmt.Errorf("read artist matching review hydration: payload digest scope is incomplete")
	}
	var dependencies []struct {
		RequestID uuid.UUID `bun:"request_id"`
		CacheKey  string    `bun:"cache_key"`
		Outcome   *string   `bun:"terminal_outcome"`
		Kind      string    `bun:"kind"`
	}
	if err := tx.NewRaw(`SELECT dependency.request_id, dependency.cache_key, dependency.terminal_outcome, request.kind
		FROM matching_provider_dependency dependency
		JOIN operation request ON request.id=dependency.request_id
		WHERE dependency.parent_run_id=?`, runID).Scan(ctx, &dependencies); err != nil {
		return nil, fmt.Errorf("read artist matching review hydration dependencies: %w", err)
	}
	type dependencyIdentity struct {
		outcome *string
		kind    string
	}
	byKey := make(map[string]dependencyIdentity, len(dependencies))
	for _, dependency := range dependencies {
		byKey[dependency.RequestID.String()+"\x00"+dependency.CacheKey] = dependencyIdentity{outcome: dependency.Outcome, kind: dependency.Kind}
	}
	for _, lookup := range output.Lookups {
		dependency, ok := byKey[lookup.RequestID.String()+"\x00"+lookup.CacheKey]
		if !ok || dependency.outcome == nil || *dependency.outcome != "succeeded" || dependency.kind != "provider_release_lookup" {
			return nil, fmt.Errorf("read artist matching review hydration: selected lookup dependency is not a successful release lookup")
		}
	}
	return lookupKeys, nil
}

func equalSortedStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	l := append([]string(nil), left...)
	r := append([]string(nil), right...)
	sort.Strings(l)
	sort.Strings(r)
	for index := range l {
		if l[index] != r[index] {
			return false
		}
	}
	return true
}

// PublishArtistMatchingReviewRefresh persists a freshly evaluated artist-catalog
// candidate as review-only evidence. It never reopens the original run, never
// creates an apply request or River job, and never bypasses the shared
// lock-and-CAS publisher path used by durable delivery publication.
func (repository *SetupManagerRepository) PublishArtistMatchingReviewRefresh(ctx context.Context, candidate ArtistMatchingReviewRefreshCandidate, evaluation IncomingMatchEvaluation) (bool, error) {
	if err := validateArtistMatchingReviewCandidate(candidate); err != nil {
		return false, fmt.Errorf("publish artist matching review refresh: %w", err)
	}
	if err := validateIncomingMatchEvaluationStructure(evaluation, false); err != nil {
		return false, fmt.Errorf("publish artist matching review refresh: %w", err)
	}
	if evaluation.ScopeVersion != 4 || evaluation.TargetFenceVersion != 2 || len(evaluation.Groups) != 1 ||
		evaluation.Groups[0].GroupID != candidate.GroupID {
		return false, fmt.Errorf("publish artist matching review refresh: evaluation is not one version-2 sealed group")
	}
	if evaluation.TargetRunID != uuid.Nil && evaluation.TargetRunID != candidate.RunID {
		return false, fmt.Errorf("publish artist matching review refresh: evaluation target run differs from candidate")
	}
	if evaluation.MatchingAuthorityMode != "" && evaluation.MatchingAuthorityMode != "automatic" {
		return false, fmt.Errorf("publish artist matching review refresh: automatic authority is required")
	}
	changed := false
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		binding, found, err := readArtistMatchingReviewRefreshRun(ctx, tx, candidate.RunID)
		if err != nil {
			return err
		}
		if !found || binding.GroupID != candidate.GroupID {
			return fmt.Errorf("publish artist matching review refresh: immutable target changed: %w", ErrIncomingMatchConflict)
		}
		scope, found, err := readArtistMatchingReviewRefreshSealedScope(ctx, tx, candidate.RunID)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("publish artist matching review refresh: immutable scope is unavailable: %w", ErrIncomingMatchConflict)
		}
		if err := validateArtistMatchingReviewRefreshCurrent(ctx, tx, candidate); err != nil {
			return err
		}
		if err := bindArtistMatchingReviewRefreshEvaluation(binding, scope, candidate, &evaluation); err != nil {
			return err
		}
		drifted, err := artistMatchingReviewProviderDrifted(candidate.Provider, evaluation.Provider)
		if err != nil {
			return err
		}
		var layout struct {
			PairThreshold string `json:"layout_pair_threshold"`
		}
		if err := json.Unmarshal(evaluation.Groups[0].Evidence, &layout); err != nil {
			return err
		}
		if !drifted && layout.PairThreshold == candidate.PairThreshold {
			return nil
		}
		evidence, err := markMatchingProviderRefreshReview(evaluation.Groups[0].Evidence)
		if err != nil {
			return err
		}
		evaluation.Groups[0].Evidence = evidence
		evaluation.MatchingAuthorityMode = "automatic"
		if err := validateIncomingMatchEvaluationStructure(evaluation, true); err != nil {
			return fmt.Errorf("publish artist matching review refresh: %w", err)
		}
		expected := map[uuid.UUID]*string{candidate.GroupID: &candidate.EvaluationRevision}
		if err := replaceIncomingMatchesTx(ctx, tx, evaluation, expected, "automatic"); err != nil {
			return fmt.Errorf("publish artist matching review refresh: %w", err)
		}
		changed = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return changed, nil
}

func validateArtistMatchingReviewRefreshCurrent(ctx context.Context, tx bun.Tx, candidate ArtistMatchingReviewRefreshCandidate) error {
	var header struct {
		EvaluationRevision     string     `bun:"evaluation_revision"`
		ProviderFences         []byte     `bun:"provider_fences"`
		TargetRunID            *uuid.UUID `bun:"target_run_id"`
		FenceVersion           int        `bun:"fence_version"`
		FrozenAuthorityVersion *int       `bun:"frozen_authority_version"`
		AuthorityMode          string     `bun:"matching_authority_mode"`
	}
	err := tx.NewRaw(`SELECT evaluation_revision, provider_fences, target_run_id, fence_version,
			frozen_authority_version, matching_authority_mode
		FROM incoming_match_evaluation WHERE group_id=?`, candidate.GroupID).Scan(ctx, &header)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("publish artist matching review refresh: candidate evaluation is no longer current: %w", ErrIncomingMatchConflict)
	}
	if err != nil {
		return fmt.Errorf("publish artist matching review refresh: read current evaluation: %w", err)
	}
	if header.EvaluationRevision != candidate.EvaluationRevision || header.TargetRunID == nil || *header.TargetRunID != candidate.RunID ||
		header.FenceVersion != 2 || header.FrozenAuthorityVersion != nil || header.AuthorityMode != "automatic" {
		return fmt.Errorf("publish artist matching review refresh: candidate evaluation changed: %w", ErrIncomingMatchConflict)
	}
	var persisted IncomingMatchProviderFence
	if err := json.Unmarshal(header.ProviderFences, &persisted); err != nil {
		return fmt.Errorf("publish artist matching review refresh: decode persisted provider fence: %w", err)
	}
	if !equalIncomingMatchProviderFence(persisted, candidate.Provider) {
		return fmt.Errorf("publish artist matching review refresh: candidate provider fence changed: %w", ErrIncomingMatchConflict)
	}
	var runCurrent bool
	if err := tx.NewRaw(`SELECT run.state='succeeded' AND run.quiesced_at IS NULL AND run.active_river_job_id IS NULL
		FROM matching_run run WHERE run.id=?`, candidate.RunID).Scan(ctx, &runCurrent); err != nil {
		return fmt.Errorf("publish artist matching review refresh: read run state: %w", err)
	}
	var liveDelivery bool
	if err := tx.NewRaw(`SELECT EXISTS(
		SELECT 1 FROM matching_delivery delivery JOIN river_job job ON job.id=delivery.river_job_id
		WHERE delivery.run_id=? AND delivery.consumed_at IS NULL AND job.finalized_at IS NULL
	)`, candidate.RunID).Scan(ctx, &liveDelivery); err != nil {
		return fmt.Errorf("publish artist matching review refresh: read live delivery: %w", err)
	}
	if !runCurrent || liveDelivery {
		return fmt.Errorf("publish artist matching review refresh: run is no longer eligible: %w", ErrIncomingMatchConflict)
	}
	return nil
}

func bindArtistMatchingReviewRefreshEvaluation(binding artistMatchingReviewRefreshRunBinding, scope artistMatchingReviewRefreshSealedScope, candidate ArtistMatchingReviewRefreshCandidate, evaluation *IncomingMatchEvaluation) error {
	group := &evaluation.Groups[0]
	if group.GroupRevision != binding.GroupRevision {
		return fmt.Errorf("publish artist matching review refresh: evaluated group revision differs from immutable target")
	}
	if evaluation.PolicyVersion != binding.PolicyVersion {
		return fmt.Errorf("publish artist matching review refresh: evaluated policy differs from immutable plan")
	}
	if evaluation.TargetCaptureDigest != binding.TargetCaptureDigest {
		return fmt.Errorf("publish artist matching review refresh: target capture digest differs from immutable target")
	}
	if len(evaluation.TargetCapture) != 0 {
		provided, err := canonicalMatchingJSON(evaluation.TargetCapture)
		if err != nil || !bytes.Equal(provided, binding.TargetCapture) {
			return fmt.Errorf("publish artist matching review refresh: caller target capture differs from immutable target")
		}
	}
	if evaluation.Provider.ProviderID != binding.ProviderID || evaluation.Provider.ProviderSourceID != binding.ProviderSourceID ||
		evaluation.Provider.ConfigurationIdentity != binding.ProviderConfigurationIdentity {
		return fmt.Errorf("publish artist matching review refresh: evaluated provider identity differs from immutable scope")
	}
	if !equalSortedStrings(evaluation.Provider.ReleaseKeys, scope.Input.ReleaseIDs) {
		return fmt.Errorf("publish artist matching review refresh: evaluated releases differ from sealed selection")
	}
	expectedKeys := make(map[string]bool, len(scope.CatalogKeys)+len(scope.LookupKeys))
	for _, key := range scope.CatalogKeys {
		expectedKeys[key] = true
	}
	for _, key := range scope.LookupKeys {
		expectedKeys[key] = true
	}
	if len(evaluation.Provider.CacheRevisions) != len(expectedKeys) {
		return fmt.Errorf("publish artist matching review refresh: evaluated response scope differs from sealed selection")
	}
	for key := range evaluation.Provider.CacheRevisions {
		if !expectedKeys[key] {
			return fmt.Errorf("publish artist matching review refresh: evaluated response is outside sealed selection")
		}
	}
	if _, ok := evaluation.Provider.CacheRevisions[evaluation.Provider.CacheKey]; !ok {
		return fmt.Errorf("publish artist matching review refresh: evaluated primary response is outside sealed selection")
	}
	if len(evaluation.Provider.AcoustID) != 0 || len(evaluation.Provider.AcoustIDSources) != 0 {
		return fmt.Errorf("publish artist matching review refresh: artist-catalog review cannot carry AcoustID authority")
	}
	var capture automaticArtistTargetCapture
	if err := json.Unmarshal(binding.TargetCapture, &capture); err != nil || capture.SchemaVersion != 2 ||
		capture.GroupID != candidate.GroupID || capture.GroupRevision != binding.GroupRevision {
		return fmt.Errorf("publish artist matching review refresh: immutable target capture is invalid")
	}
	if len(capture.Members) != len(group.Members) {
		return fmt.Errorf("publish artist matching review refresh: evaluated members differ from immutable target")
	}
	captured := make([]string, 0, len(capture.Members))
	for _, member := range capture.Members {
		captured = append(captured, member.VariantID.String())
	}
	evaluated := make([]string, 0, len(group.Members))
	for _, member := range group.Members {
		evaluated = append(evaluated, member.FileMemberID.String())
	}
	sort.Strings(captured)
	sort.Strings(evaluated)
	if !equalSortedStrings(captured, evaluated) {
		return fmt.Errorf("publish artist matching review refresh: evaluated members differ from immutable target")
	}
	evaluation.TargetRunID = candidate.RunID
	evaluation.TargetCapture = append(json.RawMessage(nil), binding.TargetCapture...)
	evaluation.TargetCaptureDigest = binding.TargetCaptureDigest
	evaluation.MatchingAuthorityMode = "automatic"
	evaluation.MatchingAuthorityRevisions = map[uuid.UUID]int64{candidate.GroupID: binding.AuthorityRevision}
	return nil
}

func artistMatchingReviewProviderDrifted(previous, next IncomingMatchProviderFence) (bool, error) {
	if previous.ProviderID != next.ProviderID || previous.ProviderSourceID != next.ProviderSourceID ||
		previous.Namespace != next.Namespace || previous.Endpoint != next.Endpoint ||
		previous.ConfigurationIdentity != next.ConfigurationIdentity {
		return false, fmt.Errorf("publish artist matching review refresh: evaluated provider identity changed")
	}
	if !equalSortedStrings(previous.ReleaseKeys, next.ReleaseKeys) {
		return false, fmt.Errorf("publish artist matching review refresh: evaluated release scope changed")
	}
	if previous.AcceptedProjectionRevision != next.AcceptedProjectionRevision {
		return true, nil
	}
	previousRevisions := effectiveMatchCacheRevisions(previous)
	nextRevisions := effectiveMatchCacheRevisions(next)
	if len(previousRevisions) != len(nextRevisions) {
		return true, nil
	}
	for key, revision := range previousRevisions {
		nextRevision, ok := nextRevisions[key]
		if !ok || !equalOptionalString(revision, nextRevision) {
			return true, nil
		}
	}
	return false, nil
}

func equalIncomingMatchProviderFence(left, right IncomingMatchProviderFence) bool {
	leftBytes, leftErr := json.Marshal(left)
	rightBytes, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftBytes, rightBytes)
}

func markMatchingProviderRefreshReview(evidence json.RawMessage) (json.RawMessage, error) {
	envelope := make(map[string]json.RawMessage)
	if len(evidence) != 0 && string(evidence) != "null" {
		if err := json.Unmarshal(evidence, &envelope); err != nil || envelope == nil {
			return nil, fmt.Errorf("publish artist matching review refresh: evaluation evidence is not a JSON object")
		}
	}
	marker, err := json.Marshal(matchingProviderRefreshReviewMarker{Version: 1})
	if err != nil {
		return nil, fmt.Errorf("publish artist matching review refresh: encode review marker: %w", err)
	}
	envelope[matchingProviderRefreshReviewEvidence] = marker
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("publish artist matching review refresh: encode evaluation evidence: %w", err)
	}
	return encoded, nil
}

// matchingProviderRefreshReviewOnly reports whether persisted evidence was
// produced by the review-only refresh path. A malformed marker is treated as an
// error so no corrupted evidence can become applicable.
func matchingProviderRefreshReviewOnly(evidence json.RawMessage) (bool, error) {
	if len(evidence) == 0 || string(evidence) == "null" {
		return false, nil
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(evidence, &envelope); err != nil || envelope == nil {
		return false, fmt.Errorf("provider refresh review evidence is not a JSON object")
	}
	raw, exists := envelope[matchingProviderRefreshReviewEvidence]
	if !exists {
		return false, nil
	}
	var marker matchingProviderRefreshReviewMarker
	if err := json.Unmarshal(raw, &marker); err != nil || marker.Version != 1 {
		return false, fmt.Errorf("provider refresh review marker is invalid")
	}
	return true, nil
}
