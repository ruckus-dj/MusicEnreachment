package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// MatchingFallbackLookupReceipt preserves both the accepted raw response and
// the source projection that was materialized when that response was accepted.
type MatchingFallbackLookupReceipt struct {
	RunID                 uuid.UUID       `json:"run_id"`
	RequestID             uuid.UUID       `json:"request_id"`
	StageIndex            int             `json:"stage_index"`
	ProviderSourceID      uuid.UUID       `json:"provider_source_id"`
	ConfigurationIdentity string          `json:"configuration_identity"`
	CacheKey              string          `json:"cache_key"`
	RequestInput          json.RawMessage `json:"request_input"`
	Payload               json.RawMessage `json:"payload"`
	PayloadSHA256         string          `json:"payload_sha256"`
	AcceptedProjection    json.RawMessage `json:"accepted_projection"`
	ProjectionSHA256      string          `json:"projection_sha256"`
	CacheRevision         *string         `json:"cache_revision,omitempty"`
	CacheGeneration       int64           `json:"cache_generation"`
	FetchOrder            int64           `json:"fetch_order"`
	ExecutionEpoch        int64           `json:"execution_epoch"`
	ProviderAttempt       int             `json:"provider_attempt"`
	RiverJobID            int64           `json:"river_job_id"`
	AcceptedAt            string          `json:"accepted_at"`
}

// GetMatchingFallbackLookupReceipt reads immutable accepted authority only.
func (repository *SetupManagerRepository) GetMatchingFallbackLookupReceipt(ctx context.Context, runID, requestID uuid.UUID) (*MatchingFallbackLookupReceipt, error) {
	return getMatchingFallbackLookupReceipt(ctx, repository.db, runID, requestID)
}

func getMatchingFallbackLookupReceipt(ctx context.Context, database bun.IDB, runID, requestID uuid.UUID) (*MatchingFallbackLookupReceipt, error) {
	if runID == uuid.Nil || requestID == uuid.Nil {
		return nil, fmt.Errorf("matching fallback lookup receipt identity is required")
	}
	r := new(MatchingFallbackLookupReceipt)
	var payload, projection string
	var payloadDigest, projectionDigest []byte
	var runSource uuid.UUID
	var runIdentity string
	var intentBytes []byte
	var operationInput []byte
	var operationSource uuid.UUID
	var operationIdentity, operationCacheKey, operationKind string
	err := database.NewRaw(`SELECT receipt.run_id,receipt.request_id,receipt.stage_index,receipt.provider_source_id,receipt.configuration_identity,receipt.cache_key,
		receipt.request_input,receipt.payload::text,receipt.payload_sha256,receipt.accepted_projection::text,receipt.projection_sha256,receipt.cache_revision,
		receipt.cache_generation,receipt.fetch_order,receipt.execution_epoch,receipt.provider_attempt,receipt.river_job_id,receipt.accepted_at::text,
		run.provider_source_id,run.provider_configuration_identity,acquisition.intent,
		operation.input_snapshot,operation.provider_source_id,operation.provider_configuration_identity,operation.provider_cache_key,operation.kind
		FROM matching_fallback_lookup_receipt receipt
		JOIN matching_run run ON run.id=receipt.run_id
		JOIN matching_acquisition acquisition ON acquisition.run_id=run.id AND acquisition.intent_version=3
		JOIN matching_provider_dependency dependency ON dependency.parent_run_id=run.id AND dependency.request_id=receipt.request_id
		JOIN operation ON operation.id=dependency.request_id
		WHERE receipt.run_id=? AND receipt.request_id=?`, runID, requestID).Scan(ctx,
		&r.RunID, &r.RequestID, &r.StageIndex, &r.ProviderSourceID, &r.ConfigurationIdentity, &r.CacheKey,
		&r.RequestInput, &payload, &payloadDigest, &projection, &projectionDigest, &r.CacheRevision,
		&r.CacheGeneration, &r.FetchOrder, &r.ExecutionEpoch, &r.ProviderAttempt, &r.RiverJobID, &r.AcceptedAt,
		&runSource, &runIdentity, &intentBytes, &operationInput, &operationSource, &operationIdentity, &operationCacheKey, &operationKind)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read matching fallback lookup receipt: %w", err)
	}
	r.Payload, err = canonicalMatchingJSON(json.RawMessage(payload))
	if err != nil {
		return nil, fmt.Errorf("read matching fallback lookup receipt: saved payload is invalid")
	}
	r.AcceptedProjection, err = canonicalMatchingJSON(json.RawMessage(projection))
	if err != nil {
		return nil, fmt.Errorf("read matching fallback lookup receipt: saved projection is invalid")
	}
	ph := sha256.Sum256(r.Payload)
	if len(payloadDigest) != sha256.Size || len(projectionDigest) != sha256.Size || !equalDigest(payloadDigest, ph[:]) {
		return nil, fmt.Errorf("read matching fallback lookup receipt: digest is invalid")
	}
	sh := sha256.Sum256(r.AcceptedProjection)
	if !equalDigest(projectionDigest, sh[:]) || r.RequestID != requestID || r.RunID != runID || r.StageIndex != 0 {
		return nil, fmt.Errorf("read matching fallback lookup receipt: projection digest or scope is invalid")
	}
	var intent MatchingAcquisitionIntent
	var snapshot ProviderMatchingSnapshot
	var request struct {
		SchemaVersion int    `json:"schema_version"`
		MBID          string `json:"mbid"`
	}
	if json.Unmarshal(intentBytes, &intent) != nil || intent.SchemaVersion != matchingAutomaticFallbackAcquisitionIntentVersion ||
		json.Unmarshal(r.RequestInput, &request) != nil || request.SchemaVersion != 1 || request.MBID == "" ||
		"release-lookup:"+request.MBID != r.CacheKey ||
		r.ProviderSourceID != runSource || r.ConfigurationIdentity != runIdentity || json.Unmarshal(r.AcceptedProjection, &snapshot) != nil ||
		snapshot.Source.ID != r.ProviderSourceID || snapshot.Source.ConfigurationIdentity != r.ConfigurationIdentity ||
		len(snapshot.Releases) != 1 || snapshot.Releases[0].ProviderKey != strings.TrimPrefix(r.CacheKey, "release-lookup:") ||
		snapshot.Responses[r.CacheKey] == nil || !equalPayload(snapshot.Responses[r.CacheKey].Payload, r.Payload) ||
		operationKind != "provider_release_lookup" || operationSource != r.ProviderSourceID || operationIdentity != r.ConfigurationIdentity || operationCacheKey != r.CacheKey ||
		!matchingJSONEqual(operationInput, r.RequestInput) {
		return nil, fmt.Errorf("read matching fallback lookup receipt: immutable source scope is invalid")
	}
	if err := validateFallbackLookupRunAuthority(ctx, database, runID, intent); err != nil {
		return nil, fmt.Errorf("read matching fallback lookup receipt: %w", err)
	}
	var manifest struct {
		Input  []byte `bun:"input"`
		Output []byte `bun:"output"`
	}
	if err := database.NewRaw(`SELECT input,output FROM matching_acquisition_phase
		WHERE run_id=? AND phase='fallback_release_hydration' ORDER BY revision DESC LIMIT 1`, runID).Scan(ctx, &manifest); err != nil {
		return nil, fmt.Errorf("read matching fallback lookup receipt: registered manifest is unavailable: %w", err)
	}
	var input struct {
		ReleaseIDs []string `json:"release_ids"`
	}
	var output struct {
		Lookups []struct {
			ReleaseID string    `json:"release_id"`
			RequestID uuid.UUID `json:"request_id"`
			CacheKey  string    `json:"cache_key"`
		} `json:"lookups"`
	}
	if json.Unmarshal(manifest.Input, &input) != nil || json.Unmarshal(manifest.Output, &output) != nil ||
		!containsString(input.ReleaseIDs, strings.TrimPrefix(r.CacheKey, "release-lookup:")) ||
		!fallbackLookupManifestOwns(output.Lookups, requestID, strings.TrimPrefix(r.CacheKey, "release-lookup:"), r.CacheKey) {
		return nil, fmt.Errorf("read matching fallback lookup receipt: registered manifest scope changed")
	}
	r.PayloadSHA256 = hex.EncodeToString(payloadDigest)
	r.ProjectionSHA256 = hex.EncodeToString(projectionDigest)
	return r, nil
}

func validateFallbackLookupRunAuthority(ctx context.Context, database bun.IDB, runID uuid.UUID, intent MatchingAcquisitionIntent) error {
	var authority struct {
		IntentDigest []byte `bun:"intent_digest"`
		PlanDigest   []byte `bun:"plan_digest"`
		Plan         []byte `bun:"immutable_plan"`
	}
	if err := database.NewRaw(`SELECT acquisition.intent_digest,run.plan_digest,run.immutable_plan
		FROM matching_acquisition acquisition JOIN matching_run run ON run.id=acquisition.run_id
		WHERE acquisition.run_id=? AND acquisition.intent_version=3`, runID).Scan(ctx, &authority); err != nil {
		return fmt.Errorf("registered intent or plan is unavailable: %w", err)
	}
	if intent.SchemaVersion != matchingAutomaticFallbackAcquisitionIntentVersion || intent.FenceVersion != 2 ||
		validateCanonicalMatchingAcquisitionIntent(intent, authority.IntentDigest, authority.PlanDigest, authority.Plan) != nil {
		return fmt.Errorf("registered intent or plan is invalid")
	}
	var target struct {
		GroupID       uuid.UUID       `bun:"group_id"`
		Revision      string          `bun:"captured_group_revision"`
		Capture       json.RawMessage `bun:"target_capture"`
		CaptureDigest []byte          `bun:"target_capture_digest"`
	}
	if err := database.NewRaw(`SELECT group_id,captured_group_revision,target_capture,target_capture_digest
		FROM matching_run_target WHERE run_id=?`, runID).Scan(ctx, &target); err != nil {
		return fmt.Errorf("immutable target capture is unavailable: %w", err)
	}
	canonicalCapture, digest, err := automaticArtistTargetCaptureDigest(target.Capture)
	if err != nil || !equalDigest(digest[:], target.CaptureDigest) || fmt.Sprintf("%x", digest[:]) != intent.TargetCaptureDigest {
		return fmt.Errorf("immutable target capture digest changed")
	}
	var capture automaticArtistTargetCapture
	if json.Unmarshal(canonicalCapture, &capture) != nil || capture.SchemaVersion != 2 || capture.GroupID != target.GroupID || capture.GroupRevision != target.Revision ||
		len(intent.Targets) != 1 || intent.Targets[0] != (MatchingTarget{GroupID: target.GroupID, Revision: target.Revision}) {
		return fmt.Errorf("immutable target capture is invalid")
	}
	return nil
}

func fallbackLookupManifestOwns(lookups []struct {
	ReleaseID string    `json:"release_id"`
	RequestID uuid.UUID `json:"request_id"`
	CacheKey  string    `json:"cache_key"`
}, requestID uuid.UUID, releaseID, cacheKey string) bool {
	for _, lookup := range lookups {
		if lookup.RequestID == requestID && lookup.ReleaseID == releaseID && lookup.CacheKey == cacheKey {
			return true
		}
	}
	return false
}

// ReplayAutomaticFallbackLookupReceipt is fenced by the currently claimed child
// delivery and its registered fallback-hydration manifest.
func (repository *ProviderCacheRepository) ReplayAutomaticFallbackLookupReceipt(ctx context.Context, fence ProviderDeliveryFence) (*MatchingFallbackLookupReceipt, error) {
	if fence.OperationID == uuid.Nil || fence.Attempt < 1 || fence.RiverJobID < 1 || fence.ExecutionEpoch < 1 {
		return nil, fmt.Errorf("replay automatic fallback lookup receipt: delivery fence is required")
	}
	var runID uuid.UUID
	var kind, state, identity, cacheKey string
	var attempt int
	var jobID sql.NullInt64
	var epoch int64
	var sourceID uuid.UUID
	err := repository.db.NewRaw(`SELECT operation.kind,operation.state,operation.attempt,operation.river_job_id,
		operation.provider_execution_epoch,operation.provider_source_id,operation.provider_configuration_identity,
		operation.provider_cache_key,run.id
		FROM operation JOIN matching_provider_dependency dependency ON dependency.request_id=operation.id
		JOIN matching_run run ON run.id=dependency.parent_run_id
		JOIN matching_acquisition acquisition ON acquisition.run_id=run.id AND acquisition.intent_version=3
		WHERE operation.id=?`, fence.OperationID).Scan(ctx, &kind, &state, &attempt, &jobID, &epoch, &sourceID, &identity, &cacheKey, &runID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("replay automatic fallback lookup receipt: read delivery: %w", err)
	}
	if kind != "provider_release_lookup" {
		return nil, nil
	}
	if state != "running" || attempt != fence.Attempt || !jobID.Valid || jobID.Int64 != fence.RiverJobID || epoch != fence.ExecutionEpoch {
		return nil, fmt.Errorf("replay automatic fallback lookup receipt: delivery is stale")
	}
	r, err := repository.getMatchingFallbackLookupReceipt(ctx, runID, fence.OperationID)
	if err != nil || r == nil {
		return r, err
	}
	if r.ProviderSourceID != sourceID || r.ConfigurationIdentity != identity || r.CacheKey != cacheKey {
		return nil, fmt.Errorf("replay automatic fallback lookup receipt: provider scope changed")
	}
	return r, nil
}

func (repository *ProviderCacheRepository) getMatchingFallbackLookupReceipt(ctx context.Context, runID, requestID uuid.UUID) (*MatchingFallbackLookupReceipt, error) {
	return getMatchingFallbackLookupReceipt(ctx, repository.db, runID, requestID)
}

func (repository *ProviderCacheRepository) captureAutomaticFallbackLookupReceipt(
	ctx context.Context, tx bun.Tx, fence ProviderDeliveryFence, ticket ProviderRefreshTicket,
	payload []byte, revision string,
) error {
	var runID, sourceID uuid.UUID
	var identity, cacheKey string
	var inputBytes []byte
	var operationSource uuid.UUID
	var operationIdentity, operationCacheKey, operationKind string
	err := tx.NewRaw(`SELECT run.id,run.provider_source_id,run.provider_configuration_identity,
		dependency.cache_key,operation.input_snapshot,operation.provider_source_id,operation.provider_configuration_identity,
		operation.provider_cache_key,operation.kind FROM matching_run run
		JOIN matching_acquisition acquisition ON acquisition.run_id=run.id AND acquisition.intent_version=3
		JOIN matching_provider_dependency dependency ON dependency.parent_run_id=run.id AND dependency.request_id=?
	JOIN operation ON operation.id=dependency.request_id
	WHERE run.id=dependency.parent_run_id AND acquisition.current_phase='fallback_release_hydration'
	AND operation.id=? AND operation.kind='provider_release_lookup' FOR SHARE OF run,acquisition,dependency`, fence.OperationID, fence.OperationID).
		Scan(ctx, &runID, &sourceID, &identity, &cacheKey, &inputBytes, &operationSource, &operationIdentity, &operationCacheKey, &operationKind)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var request struct {
		SchemaVersion int    `json:"schema_version"`
		MBID          string `json:"mbid"`
	}
	if json.Unmarshal(inputBytes, &request) != nil || request.SchemaVersion != 1 || request.MBID == "" ||
		cacheKey != "release-lookup:"+request.MBID || sourceID != ticket.ProviderSourceID || identity != ticket.ConfigurationIdentity || cacheKey != ticket.CacheKey ||
		operationKind != "provider_release_lookup" || operationSource != sourceID || operationIdentity != identity || operationCacheKey != cacheKey {
		return fmt.Errorf("accepted fallback lookup differs from its registered request")
	}
	canonicalInput, err := canonicalMatchingJSON(inputBytes)
	if err != nil || !matchingJSONEqual(canonicalInput, json.RawMessage(inputBytes)) {
		return fmt.Errorf("accepted fallback lookup request input is invalid")
	}
	var intent MatchingAcquisitionIntent
	var intentBytes []byte
	if err := tx.NewRaw(`SELECT intent FROM matching_acquisition WHERE run_id=? AND intent_version=3`, runID).Scan(ctx, &intentBytes); err != nil || json.Unmarshal(intentBytes, &intent) != nil {
		return fmt.Errorf("registered fallback lookup intent is invalid")
	}
	if err := validateFallbackLookupRunAuthority(ctx, tx, runID, intent); err != nil {
		return fmt.Errorf("accepted fallback lookup run authority is invalid: %w", err)
	}
	var manifest struct {
		Input  []byte `bun:"input"`
		Output []byte `bun:"output"`
	}
	if err := tx.NewRaw(`SELECT phase.input,phase.output FROM matching_acquisition_phase phase
		JOIN matching_acquisition acquisition ON acquisition.run_id=phase.run_id AND acquisition.revision=phase.revision
		WHERE phase.run_id=? AND phase.phase='fallback_release_hydration'`, runID).Scan(ctx, &manifest); err != nil {
		return fmt.Errorf("read fallback lookup manifest: %w", err)
	}
	var in struct {
		ReleaseIDs []string `json:"release_ids"`
	}
	var out struct {
		Lookups []struct {
			ReleaseID string    `json:"release_id"`
			RequestID uuid.UUID `json:"request_id"`
			CacheKey  string    `json:"cache_key"`
		} `json:"lookups"`
	}
	if json.Unmarshal(manifest.Input, &in) != nil || json.Unmarshal(manifest.Output, &out) != nil || len(in.ReleaseIDs) != len(out.Lookups) {
		return fmt.Errorf("registered fallback lookup manifest is invalid")
	}
	if !containsString(in.ReleaseIDs, request.MBID) || !fallbackLookupManifestOwns(out.Lookups, fence.OperationID, request.MBID, cacheKey) {
		return fmt.Errorf("lookup is not owned by the registered fallback hydration manifest")
	}
	var source ProviderSource
	if err := tx.NewRaw(`SELECT id,provider_id,endpoint,configuration_identity FROM provider_source WHERE id=?`, sourceID).Scan(ctx, &source); err != nil {
		return err
	}
	snapshot, err := readMusicBrainzMatchingSnapshot(ctx, tx, source.Endpoint, identity, []string{request.MBID}, []string{cacheKey}, false)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	canonicalProjection, err := canonicalMatchingJSON(encoded)
	if err != nil {
		return err
	}
	canonicalPayload, err := canonicalMatchingJSON(payload)
	if err != nil {
		return err
	}
	pd, sd := sha256.Sum256(canonicalPayload), sha256.Sum256(canonicalProjection)
	r := MatchingFallbackLookupReceipt{RunID: runID, RequestID: fence.OperationID, StageIndex: 0,
		ProviderSourceID: sourceID, ConfigurationIdentity: identity, CacheKey: cacheKey,
		RequestInput: canonicalInput, Payload: canonicalPayload, PayloadSHA256: hex.EncodeToString(pd[:]),
		AcceptedProjection: canonicalProjection, ProjectionSHA256: hex.EncodeToString(sd[:]),
		CacheRevision: nullablePointer(revision), CacheGeneration: ticket.Generation, FetchOrder: ticket.FetchOrder,
		ExecutionEpoch: fence.ExecutionEpoch, ProviderAttempt: fence.Attempt, RiverJobID: fence.RiverJobID}
	return insertOrVerifyFallbackLookupReceipt(ctx, tx, r, pd[:], sd[:])
}

func insertOrVerifyFallbackLookupReceipt(ctx context.Context, tx bun.Tx, r MatchingFallbackLookupReceipt, payloadDigest, projectionDigest []byte) error {
	var exists bool
	var oldPayload, oldProjection, oldInput []byte
	var oldRevision sql.NullString
	var oldSource uuid.UUID
	var oldIdentity, oldCacheKey string
	var oldStage, oldAttempt int
	var oldGeneration, oldOrder, oldEpoch, oldJob int64
	err := tx.NewRaw(`SELECT payload_sha256,projection_sha256,request_input,stage_index,provider_source_id,configuration_identity,cache_key,
		cache_revision,cache_generation,fetch_order,execution_epoch,provider_attempt,river_job_id
		FROM matching_fallback_lookup_receipt WHERE run_id=? AND request_id=? FOR UPDATE`, r.RunID, r.RequestID).Scan(ctx,
		&oldPayload, &oldProjection, &oldInput, &oldStage, &oldSource, &oldIdentity, &oldCacheKey,
		&oldRevision, &oldGeneration, &oldOrder, &oldEpoch, &oldAttempt, &oldJob)
	if err == nil {
		exists = true
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if exists {
		if !equalDigest(oldPayload, payloadDigest) || !equalDigest(oldProjection, projectionDigest) || !matchingJSONEqual(oldInput, r.RequestInput) ||
			oldStage != r.StageIndex || oldSource != r.ProviderSourceID || oldIdentity != r.ConfigurationIdentity || oldCacheKey != r.CacheKey ||
			oldRevision.String != nullableString(r.CacheRevision) || oldRevision.Valid != (r.CacheRevision != nil) ||
			oldGeneration != r.CacheGeneration || oldOrder != r.FetchOrder || oldEpoch != r.ExecutionEpoch || oldAttempt != r.ProviderAttempt || oldJob != r.RiverJobID {
			return errors.New("conflicting fallback lookup receipt recapture")
		}
		return nil
	}
	_, err = tx.NewRaw(`INSERT INTO matching_fallback_lookup_receipt
		(run_id,request_id,stage_index,provider_source_id,configuration_identity,cache_key,request_input,payload,payload_sha256,
		accepted_projection,projection_sha256,cache_revision,cache_generation,fetch_order,execution_epoch,provider_attempt,river_job_id)
		VALUES (?,?,?,?,?,?,?::jsonb,?::jsonb,?,?::jsonb,?,NULLIF(?,''),?,?,?,?,?)`, r.RunID, r.RequestID, r.StageIndex, r.ProviderSourceID,
		r.ConfigurationIdentity, r.CacheKey, string(r.RequestInput), string(r.Payload), payloadDigest, string(r.AcceptedProjection), projectionDigest,
		nullableString(r.CacheRevision), r.CacheGeneration, r.FetchOrder, r.ExecutionEpoch, r.ProviderAttempt, r.RiverJobID).Exec(ctx)
	return err
}
