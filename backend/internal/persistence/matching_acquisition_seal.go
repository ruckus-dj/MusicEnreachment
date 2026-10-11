package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// MatchingAcquisitionEvaluationInput is the immutable, neutral handoff from
// catalog acquisition to the future evaluator. It contains no scores or policy
// decisions; callers must explicitly select the releases to consider.
type MatchingAcquisitionEvaluationInput struct {
	SchemaVersion              int                             `json:"schema_version"`
	GroupIDs                   []uuid.UUID                     `json:"group_ids"`
	ReleaseIDs                 []string                        `json:"release_ids"`
	CatalogCacheKeys           []string                        `json:"catalog_cache_keys,omitempty"`
	ReleaseLookupCacheKeys     []string                        `json:"release_lookup_cache_keys,omitempty"`
	ReleaseLookupPayloadSHA256 map[string]string               `json:"release_lookup_payload_sha256,omitempty"`
	EvidenceCacheKeysByFile    map[uuid.UUID][]string          `json:"evidence_cache_keys_by_file,omitempty"`
	SearchKeysByFile           map[uuid.UUID]map[string]string `json:"search_keys_by_file,omitempty"`
}

type MatchingAcquisitionSealRequest struct {
	ExpectedRevision   int64
	ExpectedCatalogSHA string
	Input              MatchingAcquisitionEvaluationInput
	Delivery           *MatchingDeliveryClaim
	RiverJobID         int64
	Attempt            int
}

// SealMatchingAcquisitionEvaluation freezes explicit catalog selections and
// atomically schedules the one continuation delivery. The original matching
// plan and its target generations are never rewritten.
func (repository *SetupManagerRepository) SealMatchingAcquisitionEvaluation(
	ctx context.Context,
	client RiverInserter,
	runID uuid.UUID,
	request MatchingAcquisitionSealRequest,
) (int64, bool, error) {
	if client == nil || runID == uuid.Nil || request.ExpectedRevision < 0 || request.Delivery == nil && len(request.ExpectedCatalogSHA) != 64 || request.Delivery != nil && request.ExpectedCatalogSHA != "" {
		return 0, false, fmt.Errorf("seal matching acquisition: invalid seal fence")
	}
	input, encoded, digest, err := canonicalMatchingAcquisitionInput(request.Input)
	if err != nil {
		return 0, false, err
	}
	var jobID int64
	created := false
	err = repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("seal matching acquisition: lock output gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("seal matching acquisition: lock source mutation gate: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "musicbrainz-config"); err != nil {
			return fmt.Errorf("seal matching acquisition: lock provider configuration: %w", err)
		}
		var run struct {
			SourceID     uuid.UUID `bun:"provider_source_id"`
			Identity     string    `bun:"provider_configuration_identity"`
			Provider     uuid.UUID `bun:"provider_id"`
			Endpoint     string    `bun:"endpoint"`
			FenceVersion int       `bun:"fence_version"`
		}
		if err := tx.NewRaw(`SELECT run.provider_source_id, run.provider_configuration_identity, source.provider_id, source.endpoint, run.fence_version
			FROM matching_run run JOIN provider_source source ON source.id=run.provider_source_id WHERE run.id=?`, runID).Scan(ctx, &run); err != nil {
			return fmt.Errorf("seal matching acquisition: read source fence: %w", err)
		}
		var configIdentity string
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &configIdentity); err != nil || configIdentity != run.Identity {
			return fmt.Errorf("seal matching acquisition: provider configuration changed")
		}
		var sourceIdentity string
		if err := tx.NewRaw(`SELECT configuration_identity FROM provider_source WHERE id=? FOR SHARE`, run.SourceID).Scan(ctx, &sourceIdentity); err != nil || sourceIdentity != run.Identity {
			return fmt.Errorf("seal matching acquisition: provider source changed")
		}
		var lockedGroups []uuid.UUID
		if err := tx.NewRaw(`SELECT generation.group_id FROM incoming_matching_generation generation
			JOIN matching_run_target target ON target.group_id=generation.group_id AND target.run_id=?
			JOIN incoming_group current_group ON current_group.id=target.group_id
			ORDER BY generation.group_id FOR UPDATE OF generation, current_group`, runID).Scan(ctx, &lockedGroups); err != nil {
			return fmt.Errorf("seal matching acquisition: lock target groups: %w", err)
		}
		var stale bool
		switch run.FenceVersion {
		case 2:
			if err := validateMatchingRunFence(ctx, tx, runID, run.FenceVersion); err != nil {
				return err
			}
			if err := tx.NewRaw(`SELECT EXISTS (
				SELECT 1 FROM matching_run_target target
				LEFT JOIN incoming_matching_generation generation ON generation.group_id=target.group_id
				LEFT JOIN incoming_group current_group ON current_group.id=target.group_id
				WHERE target.run_id=? AND (generation.current_run_id IS DISTINCT FROM target.run_id OR
					generation.high_water IS DISTINCT FROM target.target_generation OR current_group.revision IS DISTINCT FROM target.captured_group_revision))`, runID).Scan(ctx, &stale); err != nil {
				return fmt.Errorf("seal matching acquisition: validate target generations: %w", err)
			}
		case 1:
			if err := tx.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM matching_run_target target
			LEFT JOIN incoming_matching_generation generation ON generation.group_id=target.group_id
			LEFT JOIN incoming_group current_group ON current_group.id=target.group_id
			WHERE target.run_id=? AND (generation.current_run_id IS DISTINCT FROM target.run_id OR
				generation.high_water IS DISTINCT FROM target.target_generation OR current_group.revision IS DISTINCT FROM target.captured_group_revision))
			OR EXISTS (SELECT 1 FROM incoming_grouping_state WHERE singleton=true AND needs_refresh=true)`, runID).Scan(ctx, &stale); err != nil {
				return fmt.Errorf("seal matching acquisition: validate target generations: %w", err)
			}
		default:
			return fmt.Errorf("seal matching acquisition: unsupported fence version %d", run.FenceVersion)
		}
		if stale {
			return fmt.Errorf("seal matching acquisition: captured targets are stale")
		}
		var capturedGroups []uuid.UUID
		if err := tx.NewRaw(`SELECT group_id FROM matching_run_target WHERE run_id=? ORDER BY group_id`, runID).Scan(ctx, &capturedGroups); err != nil {
			return fmt.Errorf("seal matching acquisition: read captured groups: %w", err)
		}
		if len(capturedGroups) != len(input.GroupIDs) {
			return fmt.Errorf("seal matching acquisition: selected groups differ from captured targets")
		}
		for i := range capturedGroups {
			if capturedGroups[i] != input.GroupIDs[i] {
				return fmt.Errorf("seal matching acquisition: selected groups differ from captured targets")
			}
		}
		operation, err := repository.GetOperationForUpdate(ctx, tx, runID)
		if err != nil {
			return err
		}
		var acquisition struct {
			Phase    string `bun:"current_phase"`
			Revision int64  `bun:"revision"`
		}
		if err := tx.NewRaw(`SELECT current_phase, revision FROM matching_acquisition WHERE run_id=? FOR UPDATE`, runID).Scan(ctx, &acquisition); err != nil {
			return fmt.Errorf("seal matching acquisition: lock acquisition cursor: %w", err)
		}
		var existing struct {
			Digest        []byte `bun:"input_digest"`
			CatalogDigest []byte `bun:"catalog_digest"`
			JobID         int64  `bun:"river_job_id"`
		}
		if err := tx.NewRaw(`SELECT input_digest, catalog_digest, river_job_id FROM matching_acquisition_seal WHERE run_id=?`, runID).Scan(ctx, &existing); err == nil {
			if !equalDigest(existing.Digest, digest[:]) {
				return fmt.Errorf("seal matching acquisition: a different evaluation input is already sealed")
			}
			if hex.EncodeToString(existing.CatalogDigest) != strings.ToLower(request.ExpectedCatalogSHA) {
				return fmt.Errorf("seal matching acquisition: accepted catalog changed after sealing")
			}
			jobID = existing.JobID
			return nil
		} else if err != sql.ErrNoRows {
			return fmt.Errorf("seal matching acquisition: read existing seal: %w", err)
		}
		hydrating := acquisition.Phase == MatchingAcquisitionReleaseHydration && request.Delivery != nil
		if (acquisition.Phase != "catalog_ready" && !hydrating) || acquisition.Revision != request.ExpectedRevision || operation.Kind != "matching_evaluation_v1" ||
			(operation.State != "running" && operation.State != "queued") || operation.RiverJobID == nil {
			return fmt.Errorf("seal matching acquisition: catalog or operation fence changed")
		}
		var currentDelivery bool
		if err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM matching_delivery WHERE run_id=? AND consumed_at IS NULL)`, runID).Scan(ctx, &currentDelivery); err != nil {
			return err
		}
		if hydrating {
			if !currentDelivery || request.Attempt != operation.Attempt || request.RiverJobID < 1 || request.Delivery.ExecutionEpoch < 1 || request.Delivery.RiverAttempt < 1 {
				return fmt.Errorf("seal matching acquisition: hydration delivery fence changed")
			}
			var run struct {
				State       string `bun:"state"`
				Epoch       int64  `bun:"execution_epoch"`
				JobID       *int64 `bun:"active_river_job_id"`
				RiverTry    *int   `bun:"active_river_attempt"`
				ActiveEpoch *int64 `bun:"active_execution_epoch"`
			}
			if err := tx.NewRaw(`SELECT state, execution_epoch, active_river_job_id, active_river_attempt, active_execution_epoch FROM matching_run WHERE id=? FOR NO KEY UPDATE`, runID).Scan(ctx, &run); err != nil || run.State != "running" || run.Epoch != request.Delivery.ExecutionEpoch || run.JobID == nil || *run.JobID != request.RiverJobID || run.RiverTry == nil || *run.RiverTry != request.Delivery.RiverAttempt || run.ActiveEpoch == nil || *run.ActiveEpoch != request.Delivery.ExecutionEpoch {
				return fmt.Errorf("seal matching acquisition: hydration execution claim changed")
			}
			if err := validateMatchingCatalogCacheKeys(ctx, tx, runID, input.CatalogCacheKeys); err != nil {
				return err
			}
			if err := validateMatchingReleaseHydration(ctx, tx, runID, acquisition.Revision, input.ReleaseIDs, input.ReleaseLookupCacheKeys, input.ReleaseLookupPayloadSHA256); err != nil {
				return err
			}
			consumed, err := tx.NewRaw(`UPDATE matching_delivery SET consumed_at=? WHERE run_id=? AND river_job_id=? AND operation_attempt=? AND consumed_at IS NULL`, time.Now().UTC(), runID, request.RiverJobID, request.Attempt).Exec(ctx)
			if err != nil {
				return err
			}
			if count, err := consumed.RowsAffected(); err != nil || count != 1 {
				return fmt.Errorf("seal matching acquisition: hydration delivery is no longer active")
			}
			currentDelivery = false
		}
		if currentDelivery {
			return fmt.Errorf("seal matching acquisition: an active delivery remains")
		}
		catalogDigest, catalogReleaseIDs, err := matchingAcquisitionCatalogManifest(ctx, tx, runID)
		if err != nil {
			return err
		}
		if request.ExpectedCatalogSHA != "" && hex.EncodeToString(catalogDigest[:]) != strings.ToLower(request.ExpectedCatalogSHA) {
			return fmt.Errorf("seal matching acquisition: accepted catalog changed")
		}
		for _, id := range input.ReleaseIDs {
			if _, ok := catalogReleaseIDs[id]; !ok {
				return fmt.Errorf("seal matching acquisition: selected release is absent from accepted catalog")
			}
		}
		if !hydrating && len(input.ReleaseLookupCacheKeys) != 0 {
			return fmt.Errorf("seal matching acquisition: release lookup cache keys require a hydration phase")
		}
		job, err := client.InsertTx(ctx, tx.Tx, MatchingEvaluationWakeArgs{OperationID: runID}, nil)
		if err != nil {
			return fmt.Errorf("insert matching evaluation continuation: %w", err)
		}
		jobID = job.Job.ID
		now := time.Now().UTC()
		if _, err := tx.NewRaw(`INSERT INTO matching_acquisition_seal(run_id, acquisition_revision, input, input_digest, catalog_digest, river_job_id, operation_attempt)
			VALUES (?, ?, ?::jsonb, ?, ?, ?, ?)`, runID, acquisition.Revision+1, string(encoded), digest[:], catalogDigest[:], jobID, operation.Attempt).Exec(ctx); err != nil {
			return fmt.Errorf("seal matching acquisition: persist immutable input: %w", err)
		}
		phaseOutput, _ := json.Marshal(map[string]any{"input_sha256": hex.EncodeToString(digest[:]), "catalog_sha256": hex.EncodeToString(catalogDigest[:])})
		if _, err := tx.NewRaw(`INSERT INTO matching_acquisition_phase(run_id, revision, phase, input, output, response_provenance)
			VALUES (?, ?, 'evaluation_sealed', ?::jsonb, ?::jsonb, '[]'::jsonb)`, runID, acquisition.Revision+1, string(encoded), string(phaseOutput)).Exec(ctx); err != nil {
			return fmt.Errorf("seal matching acquisition: append phase: %w", err)
		}
		if _, err := tx.NewRaw(`UPDATE matching_acquisition SET current_phase='evaluation_sealed', revision=revision+1 WHERE run_id=? AND revision=?`, runID, acquisition.Revision).Exec(ctx); err != nil {
			return fmt.Errorf("seal matching acquisition: advance acquisition cursor: %w", err)
		}
		if _, err := tx.NewRaw(`UPDATE matching_run SET state='queued', current_river_job_id=?, active_river_job_id=NULL, active_river_attempt=NULL, active_execution_epoch=NULL, updated_at=? WHERE id=?`, jobID, now, runID).Exec(ctx); err != nil {
			return fmt.Errorf("seal matching acquisition: queue continuation: %w", err)
		}
		operation.State, operation.Stage, operation.RiverJobID, operation.UpdatedAt = "queued", "evaluation_queued", &jobID, now
		if _, err := tx.NewUpdate().Model(operation).Column("state", "stage", "river_job_id", "updated_at").WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("seal matching acquisition: register continuation: %w", err)
		}
		if _, err := tx.NewRaw(`INSERT INTO matching_delivery(run_id, river_job_id, operation_attempt) VALUES (?, ?, ?)`, runID, jobID, operation.Attempt).Exec(ctx); err != nil {
			return fmt.Errorf("seal matching acquisition: register continuation delivery: %w", err)
		}
		created = true
		return nil
	})
	return jobID, created, err
}

func canonicalMatchingAcquisitionInput(input MatchingAcquisitionEvaluationInput) (MatchingAcquisitionEvaluationInput, []byte, [32]byte, error) {
	if (input.SchemaVersion != 1 && input.SchemaVersion != 2) || len(input.GroupIDs) == 0 || len(input.ReleaseIDs) == 0 || len(input.ReleaseIDs) > 10 ||
		input.SchemaVersion == 1 && len(input.ReleaseLookupCacheKeys) != 0 || input.SchemaVersion == 2 && len(input.ReleaseLookupCacheKeys) == 0 {
		return input, nil, [32]byte{}, fmt.Errorf("seal matching acquisition: evaluation input scope is invalid")
	}
	input.GroupIDs = append([]uuid.UUID(nil), input.GroupIDs...)
	sort.Slice(input.GroupIDs, func(i, j int) bool { return input.GroupIDs[i].String() < input.GroupIDs[j].String() })
	for i, id := range input.GroupIDs {
		if id == uuid.Nil || i > 0 && input.GroupIDs[i-1] == id {
			return input, nil, [32]byte{}, fmt.Errorf("seal matching acquisition: group ids must be unique")
		}
	}
	input.ReleaseIDs = append([]string(nil), input.ReleaseIDs...)
	sort.Strings(input.ReleaseIDs)
	for i, id := range input.ReleaseIDs {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed.String() != id || i > 0 && input.ReleaseIDs[i-1] == id {
			return input, nil, [32]byte{}, fmt.Errorf("seal matching acquisition: release ids must be canonical unique UUIDs")
		}
	}
	input.ReleaseLookupCacheKeys = append([]string(nil), input.ReleaseLookupCacheKeys...)
	sort.Strings(input.ReleaseLookupCacheKeys)
	input.CatalogCacheKeys = append([]string(nil), input.CatalogCacheKeys...)
	sort.Strings(input.CatalogCacheKeys)
	if input.SchemaVersion == 1 && (len(input.CatalogCacheKeys) != 0 || len(input.ReleaseLookupPayloadSHA256) != 0) ||
		input.SchemaVersion == 2 && (len(input.ReleaseLookupPayloadSHA256) != len(input.ReleaseLookupCacheKeys) || len(input.CatalogCacheKeys) == 0) {
		return input, nil, [32]byte{}, fmt.Errorf("seal matching acquisition: release lookup payload digests do not match lookup keys")
	}
	for i, key := range input.ReleaseLookupCacheKeys {
		if strings.TrimSpace(key) == "" || i > 0 && input.ReleaseLookupCacheKeys[i-1] == key {
			return input, nil, [32]byte{}, fmt.Errorf("seal matching acquisition: release lookup cache keys must be non-empty and unique")
		}
		digest, ok := input.ReleaseLookupPayloadSHA256[key]
		decoded, decodeErr := hex.DecodeString(digest)
		if input.SchemaVersion == 2 && (!ok || decodeErr != nil || len(decoded) != sha256.Size) {
			return input, nil, [32]byte{}, fmt.Errorf("seal matching acquisition: release lookup payload digest is invalid")
		}
	}
	for i, key := range input.CatalogCacheKeys {
		if strings.TrimSpace(key) == "" || i > 0 && input.CatalogCacheKeys[i-1] == key {
			return input, nil, [32]byte{}, fmt.Errorf("seal matching acquisition: catalog cache keys must be non-empty and unique")
		}
	}
	for key := range input.ReleaseLookupPayloadSHA256 {
		if !containsString(input.ReleaseLookupCacheKeys, key) {
			return input, nil, [32]byte{}, fmt.Errorf("seal matching acquisition: release lookup payload digest is outside lookup scope")
		}
	}
	for _, keys := range input.EvidenceCacheKeysByFile {
		for _, key := range keys {
			if strings.TrimSpace(key) == "" {
				return input, nil, [32]byte{}, fmt.Errorf("seal matching acquisition: evidence cache keys must be non-empty")
			}
		}
	}
	for _, keys := range input.SearchKeysByFile {
		for kind, key := range keys {
			if strings.TrimSpace(kind) == "" || strings.TrimSpace(key) == "" {
				return input, nil, [32]byte{}, fmt.Errorf("seal matching acquisition: search keys must be non-empty")
			}
		}
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return input, nil, [32]byte{}, fmt.Errorf("seal matching acquisition: encode input: %w", err)
	}
	return input, encoded, sha256.Sum256(encoded), nil
}

func validateMatchingCatalogCacheKeys(ctx context.Context, tx bun.Tx, runID uuid.UUID, supplied []string) error {
	var rows []struct {
		ResponseProvenance []byte `bun:"response_provenance"`
	}
	if err := tx.NewRaw(`SELECT response_provenance FROM matching_acquisition_phase WHERE run_id=? AND phase IN ('artist_catalog_page','catalog_ready') ORDER BY revision`, runID).Scan(ctx, &rows); err != nil {
		return fmt.Errorf("seal matching acquisition: read accepted catalog keys: %w", err)
	}
	accepted := make([]string, 0, len(rows))
	for _, row := range rows {
		var provenance []matchingAcquisitionResponseProvenance
		if json.Unmarshal(row.ResponseProvenance, &provenance) != nil || len(provenance) != 1 || provenance[0].CacheKey == "" {
			return fmt.Errorf("seal matching acquisition: accepted catalog cache keys are invalid")
		}
		accepted = append(accepted, provenance[0].CacheKey)
	}
	sort.Strings(accepted)
	keys := append([]string(nil), supplied...)
	sort.Strings(keys)
	if len(keys) != len(accepted) {
		return fmt.Errorf("seal matching acquisition: catalog cache key scope is incomplete")
	}
	for i := range keys {
		if keys[i] != accepted[i] {
			return fmt.Errorf("seal matching acquisition: catalog cache key scope changed")
		}
	}
	return nil
}

func validateMatchingReleaseHydration(ctx context.Context, tx bun.Tx, runID uuid.UUID, revision int64, releaseIDs, suppliedKeys []string, suppliedDigests map[string]string) error {
	var phase struct {
		Input  []byte `bun:"input"`
		Output []byte `bun:"output"`
	}
	if err := tx.NewRaw(`SELECT input, output FROM matching_acquisition_phase WHERE run_id=? AND revision=? AND phase=? FOR SHARE`, runID, revision, MatchingAcquisitionReleaseHydration).Scan(ctx, &phase); err != nil {
		return fmt.Errorf("seal matching acquisition: read hydration manifest: %w", err)
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
	if json.Unmarshal(phase.Input, &manifest) != nil || json.Unmarshal(phase.Output, &output) != nil || len(manifest.ReleaseIDs) != len(releaseIDs) || len(output.Lookups) != len(releaseIDs) || len(suppliedKeys) != len(releaseIDs) || len(suppliedDigests) != len(releaseIDs) {
		return fmt.Errorf("seal matching acquisition: hydration manifest is incomplete")
	}
	for i, id := range releaseIDs {
		if manifest.ReleaseIDs[i] != id || output.Lookups[i].ReleaseID != id || output.Lookups[i].RequestID == uuid.Nil || output.Lookups[i].CacheKey != "release-lookup:"+id {
			return fmt.Errorf("seal matching acquisition: hydration manifest differs from selected releases")
		}
	}
	keys := append([]string(nil), suppliedKeys...)
	sort.Strings(keys)
	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		if seen[key] {
			return fmt.Errorf("seal matching acquisition: duplicate release lookup cache key")
		}
		seen[key] = true
	}
	for _, lookup := range output.Lookups {
		if !seen[lookup.CacheKey] {
			return fmt.Errorf("seal matching acquisition: accepted lookup key does not match hydration manifest")
		}
		var dependency struct {
			Outcome string `bun:"terminal_outcome"`
		}
		if err := tx.NewRaw(`SELECT dependency.terminal_outcome FROM matching_provider_dependency dependency WHERE dependency.parent_run_id=? AND dependency.request_id=? AND dependency.cache_key=? FOR SHARE`, runID, lookup.RequestID, lookup.CacheKey).Scan(ctx, &dependency); err != nil || dependency.Outcome != "succeeded" {
			return fmt.Errorf("seal matching acquisition: a selected release lookup has not succeeded")
		}
		var accepted struct {
			Kind string `bun:"kind"`
			Body []byte `bun:"payload"`
		}
		if err := tx.NewRaw(`SELECT operation.kind, cache.payload FROM matching_provider_dependency dependency
			JOIN operation ON operation.id=dependency.request_id
			JOIN matching_run run ON run.id=dependency.parent_run_id
			JOIN provider_response_cache cache ON cache.provider_source_id=run.provider_source_id AND cache.cache_key=dependency.cache_key
			WHERE dependency.parent_run_id=? AND dependency.request_id=? AND dependency.cache_key=? AND dependency.terminal_outcome='succeeded' AND cache.payload IS NOT NULL FOR SHARE OF cache`, runID, lookup.RequestID, lookup.CacheKey).Scan(ctx, &accepted); err != nil {
			return fmt.Errorf("seal matching acquisition: accepted release lookup is unavailable: %w", err)
		}
		if accepted.Kind != "provider_release_lookup" {
			return fmt.Errorf("seal matching acquisition: selected dependency is not a release lookup")
		}
		digest := sha256.Sum256(accepted.Body)
		payloadSHA := fmt.Sprintf("%x", digest[:])
		if suppliedDigests[lookup.CacheKey] != payloadSHA {
			return fmt.Errorf("seal matching acquisition: accepted release lookup payload changed")
		}
		response := matchingAcquisitionResponseProvenance{RequestID: lookup.RequestID, CacheKey: lookup.CacheKey, PayloadSHA: payloadSHA}
		acceptedKind, payload, err := validateMatchingAcquisitionResponse(ctx, tx, runID, response)
		if err != nil || acceptedKind != accepted.Kind {
			return fmt.Errorf("seal matching acquisition: accepted release lookup changed")
		}
		var release struct {
			ID    string `json:"id"`
			Media *[]struct {
				Tracks *[]json.RawMessage `json:"tracks"`
			} `json:"media"`
		}
		if json.Unmarshal(payload, &release) != nil || release.ID != lookup.ReleaseID || release.Media == nil {
			return fmt.Errorf("seal matching acquisition: accepted release lookup is incomplete")
		}
		for _, medium := range *release.Media {
			if medium.Tracks == nil {
				return fmt.Errorf("seal matching acquisition: accepted release track list is incomplete")
			}
		}
	}
	return nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func matchingAcquisitionCatalogManifest(ctx context.Context, tx bun.Tx, runID uuid.UUID) ([32]byte, map[string]struct{}, error) {
	var phases []MatchingAcquisitionPhase
	if err := tx.NewRaw(`SELECT revision, phase, input, output, response_provenance FROM matching_acquisition_phase WHERE run_id=? AND phase IN ('artist_catalog_page', 'catalog_ready') ORDER BY revision`, runID).Scan(ctx, &phases); err != nil {
		return [32]byte{}, nil, fmt.Errorf("seal matching acquisition: read catalog phases: %w", err)
	}
	if len(phases) < 2 || phases[0].Revision != 1 || phases[len(phases)-1].Phase != "catalog_ready" {
		return [32]byte{}, nil, fmt.Errorf("seal matching acquisition: durable catalog is incomplete")
	}
	type manifestPage struct {
		Revision   int64     `json:"revision"`
		RequestID  uuid.UUID `json:"request_id"`
		CacheKey   string    `json:"cache_key"`
		PayloadSHA string    `json:"payload_sha256"`
	}
	manifest := make([]manifestPage, 0, len(phases))
	ids := make(map[string]struct{})
	var priorIDs []string
	var catalogCount *int
	var priorOutput struct {
		ArtistMBID string    `json:"artist_mbid"`
		RequestID  uuid.UUID `json:"next_request_id"`
		CacheKey   string    `json:"next_cache_key"`
	}
	for index, phase := range phases {
		if phase.Revision != int64(index+1) {
			return [32]byte{}, nil, fmt.Errorf("seal matching acquisition: catalog phase sequence has a gap")
		}
		var provenance []matchingAcquisitionResponseProvenance
		if err := json.Unmarshal(phase.ResponseProvenance, &provenance); err != nil || len(provenance) != 1 {
			return [32]byte{}, nil, fmt.Errorf("seal matching acquisition: catalog provenance is invalid")
		}
		kind, payload, err := validateMatchingAcquisitionResponse(ctx, tx, runID, provenance[0])
		if err != nil {
			return [32]byte{}, nil, fmt.Errorf("seal matching acquisition: accepted catalog response changed: %w", err)
		}
		if (phase.Revision == phases[0].Revision && kind != "provider_artist_search") || (phase.Revision != phases[0].Revision && kind != "provider_artist_release_browse") {
			return [32]byte{}, nil, fmt.Errorf("seal matching acquisition: accepted catalog response changed")
		}
		if index > 0 && (priorOutput.RequestID != uuid.Nil && priorOutput.RequestID != provenance[0].RequestID || priorOutput.CacheKey != "" && priorOutput.CacheKey != provenance[0].CacheKey) {
			return [32]byte{}, nil, fmt.Errorf("seal matching acquisition: catalog response does not follow persisted request")
		}
		if phase.Revision == phases[0].Revision {
			var firstOutput struct {
				ArtistMBID string    `json:"artist_mbid"`
				NextOffset int       `json:"next_offset"`
				Complete   bool      `json:"complete"`
				ReleaseIDs []string  `json:"release_ids"`
				RequestID  uuid.UUID `json:"next_request_id"`
				CacheKey   string    `json:"next_cache_key"`
			}
			var browseInput struct {
				ArtistMBID string `json:"artist_mbid"`
				Offset     int    `json:"offset"`
				Limit      int    `json:"limit"`
			}
			if json.Unmarshal(phase.Output, &firstOutput) != nil || json.Unmarshal(phase.Input, &browseInput) != nil ||
				!responseIsTopArtist(payload, phase.Output) || !strings.EqualFold(firstOutput.ArtistMBID, browseInput.ArtistMBID) || browseInput.Offset != 0 || browseInput.Limit < 1 || browseInput.Limit > 100 ||
				firstOutput.NextOffset != 0 || firstOutput.Complete || len(firstOutput.ReleaseIDs) != 0 ||
				(firstOutput.RequestID == uuid.Nil) != (firstOutput.CacheKey == "") {
				return [32]byte{}, nil, fmt.Errorf("seal matching acquisition: initial catalog cursor is invalid")
			}
			priorOutput.ArtistMBID, priorOutput.RequestID, priorOutput.CacheKey = firstOutput.ArtistMBID, firstOutput.RequestID, firstOutput.CacheKey
		} else {
			var page struct {
				Count    *int               `json:"release-count"`
				Offset   *int               `json:"release-offset"`
				Releases *[]json.RawMessage `json:"releases"`
			}
			var input struct {
				ArtistMBID string `json:"artist_mbid"`
				Offset     int    `json:"offset"`
				Limit      int    `json:"limit"`
			}
			var output struct {
				ArtistMBID   string    `json:"artist_mbid"`
				NextOffset   int       `json:"next_offset"`
				Complete     bool      `json:"complete"`
				CatalogCount *int      `json:"catalog_count"`
				Covered      int       `json:"covered_releases"`
				ReleaseIDs   []string  `json:"release_ids"`
				RequestID    uuid.UUID `json:"next_request_id"`
				CacheKey     string    `json:"next_cache_key"`
			}
			if json.Unmarshal(payload, &page) != nil || json.Unmarshal(phase.Input, &input) != nil || json.Unmarshal(phase.Output, &output) != nil ||
				page.Count == nil || page.Offset == nil || page.Releases == nil || output.CatalogCount == nil ||
				*page.Count < 0 || *page.Offset != input.Offset || input.Offset != len(priorIDs) || input.Limit < 1 || input.Limit > 100 ||
				!strings.EqualFold(input.ArtistMBID, priorOutput.ArtistMBID) || !strings.EqualFold(output.ArtistMBID, priorOutput.ArtistMBID) ||
				len(*page.Releases) > input.Limit || output.NextOffset != *page.Offset+len(*page.Releases) || output.Covered != output.NextOffset ||
				*output.CatalogCount != *page.Count || output.Complete != (output.NextOffset >= *page.Count) || output.Complete != (phase.Phase == "catalog_ready") ||
				len(*page.Releases) == 0 && *page.Offset < *page.Count || output.NextOffset > *page.Count || output.RequestID == uuid.Nil && !output.Complete || output.CacheKey == "" && !output.Complete {
				return [32]byte{}, nil, fmt.Errorf("seal matching acquisition: accepted catalog page count or cursor is invalid")
			}
			var request struct {
				Input json.RawMessage `bun:"input_snapshot"`
			}
			if err := tx.NewRaw(`SELECT input_snapshot FROM operation WHERE id=? AND kind='provider_artist_release_browse'`, provenance[0].RequestID).Scan(ctx, &request); err != nil {
				return [32]byte{}, nil, fmt.Errorf("seal matching acquisition: resolve accepted browse request: %w", err)
			}
			var requestInput struct {
				MBID   string `json:"mbid"`
				Offset int    `json:"offset"`
				Limit  int    `json:"limit"`
			}
			if json.Unmarshal(request.Input, &requestInput) != nil || !strings.EqualFold(requestInput.MBID, input.ArtistMBID) || requestInput.Offset != input.Offset || requestInput.Limit != input.Limit {
				return [32]byte{}, nil, fmt.Errorf("seal matching acquisition: accepted browse request does not match catalog cursor")
			}
			if catalogCount != nil && *catalogCount != *page.Count {
				return [32]byte{}, nil, fmt.Errorf("seal matching acquisition: accepted catalog count changed")
			}
			count := *page.Count
			catalogCount = &count
			pageIDs := make([]string, 0, len(*page.Releases))
			for _, raw := range *page.Releases {
				var release struct {
					ID string `json:"id"`
				}
				parsed, parseErr := func() (uuid.UUID, error) {
					if json.Unmarshal(raw, &release) != nil {
						return uuid.Nil, fmt.Errorf("invalid")
					}
					return uuid.Parse(release.ID)
				}()
				if parseErr != nil || parsed.String() != strings.ToLower(release.ID) {
					return [32]byte{}, nil, fmt.Errorf("seal matching acquisition: accepted release identity is invalid")
				}
				if _, duplicate := ids[parsed.String()]; duplicate {
					return [32]byte{}, nil, fmt.Errorf("seal matching acquisition: accepted catalog contains duplicate releases")
				}
				ids[parsed.String()] = struct{}{}
				pageIDs = append(pageIDs, parsed.String())
			}
			if len(output.ReleaseIDs) != output.NextOffset {
				return [32]byte{}, nil, fmt.Errorf("seal matching acquisition: persisted catalog prefix length is invalid")
			}
			for i, id := range priorIDs {
				if output.ReleaseIDs[i] != id {
					return [32]byte{}, nil, fmt.Errorf("seal matching acquisition: persisted catalog prefix changed")
				}
			}
			for i, id := range pageIDs {
				if output.ReleaseIDs[len(priorIDs)+i] != id {
					return [32]byte{}, nil, fmt.Errorf("seal matching acquisition: persisted catalog page differs from accepted response")
				}
			}
			priorIDs = append(priorIDs, pageIDs...)
			priorOutput.ArtistMBID, priorOutput.RequestID, priorOutput.CacheKey = output.ArtistMBID, output.RequestID, output.CacheKey
		}
		manifest = append(manifest, manifestPage{phase.Revision, provenance[0].RequestID, provenance[0].CacheKey, provenance[0].PayloadSHA})
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return [32]byte{}, nil, fmt.Errorf("seal matching acquisition: encode catalog manifest: %w", err)
	}
	return sha256.Sum256(encoded), ids, nil
}
