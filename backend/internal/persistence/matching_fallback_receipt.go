package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/policy"
	"github.com/uptrace/bun"
)

// MatchingFallbackSearchReceipt is the accepted authority for a run's first
// fallback provider request. Cache refreshes do not alter this snapshot.
type MatchingFallbackSearchReceipt struct {
	RunID                 uuid.UUID       `json:"run_id"`
	RequestID             uuid.UUID       `json:"request_id"`
	StageIndex            int             `json:"stage_index"`
	ProviderSourceID      uuid.UUID       `json:"provider_source_id"`
	ConfigurationIdentity string          `json:"configuration_identity"`
	CacheKey              string          `json:"cache_key"`
	RequestInput          json.RawMessage `json:"request_input"`
	Payload               json.RawMessage `json:"payload"`
	PayloadSHA256         string          `json:"payload_sha256"`
	CacheRevision         *string         `json:"cache_revision,omitempty"`
	CacheGeneration       int64           `json:"cache_generation"`
	FetchOrder            int64           `json:"fetch_order"`
	ExecutionEpoch        int64           `json:"execution_epoch"`
	ProviderAttempt       int             `json:"provider_attempt"`
	RiverJobID            int64           `json:"river_job_id"`
	AcceptedAt            string          `json:"accepted_at"`
}

// GetMatchingFallbackSearchReceipt reads only the immutable receipt and its
// immutable request/run scope; it never consults the mutable provider cache.
func (repository *SetupManagerRepository) GetMatchingFallbackSearchReceipt(ctx context.Context, runID, requestID uuid.UUID) (*MatchingFallbackSearchReceipt, error) {
	return getMatchingFallbackSearchReceipt(ctx, repository.db, runID, requestID)
}

func getMatchingFallbackSearchReceipt(ctx context.Context, database bun.IDB, runID, requestID uuid.UUID) (*MatchingFallbackSearchReceipt, error) {
	if runID == uuid.Nil || requestID == uuid.Nil {
		return nil, fmt.Errorf("matching fallback search receipt identity is required")
	}
	receipt := new(MatchingFallbackSearchReceipt)
	var digest []byte
	var payloadText string
	var intentBytes []byte
	var runSourceID uuid.UUID
	var runConfigurationIdentity string
	err := database.NewRaw(`SELECT receipt.run_id,receipt.request_id,receipt.stage_index,
		receipt.provider_source_id,receipt.configuration_identity,receipt.cache_key,
		receipt.request_input,receipt.payload::text,receipt.payload_sha256,receipt.cache_revision,
		receipt.cache_generation,receipt.fetch_order,receipt.execution_epoch,
		receipt.provider_attempt,receipt.river_job_id,receipt.accepted_at::text,acquisition.intent,
		run.provider_source_id,run.provider_configuration_identity
		FROM matching_fallback_search_receipt receipt
		JOIN matching_run run ON run.id=receipt.run_id
		JOIN matching_acquisition acquisition ON acquisition.run_id=run.id AND acquisition.intent_version=3
		JOIN matching_provider_dependency dependency ON dependency.parent_run_id=run.id AND dependency.request_id=receipt.request_id
		WHERE receipt.run_id=? AND receipt.request_id=?`, runID, requestID).Scan(ctx,
		&receipt.RunID, &receipt.RequestID, &receipt.StageIndex, &receipt.ProviderSourceID,
		&receipt.ConfigurationIdentity, &receipt.CacheKey, &receipt.RequestInput, &payloadText,
		&digest, &receipt.CacheRevision, &receipt.CacheGeneration, &receipt.FetchOrder,
		&receipt.ExecutionEpoch, &receipt.ProviderAttempt, &receipt.RiverJobID, &receipt.AcceptedAt, &intentBytes,
		&runSourceID, &runConfigurationIdentity)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read matching fallback search receipt: %w", err)
	}
	if len(digest) != sha256.Size || receipt.StageIndex != 0 || receipt.CacheGeneration < 1 || receipt.FetchOrder < 1 ||
		receipt.ExecutionEpoch < 1 || receipt.ProviderAttempt < 1 || receipt.RiverJobID < 1 {
		return nil, fmt.Errorf("read matching fallback search receipt: saved receipt is invalid")
	}
	receipt.Payload, err = json.Marshal(json.RawMessage(payloadText))
	if err != nil {
		return nil, fmt.Errorf("read matching fallback search receipt: saved payload is invalid")
	}
	actualDigest := sha256.Sum256(receipt.Payload)
	if !equalDigest(digest, actualDigest[:]) {
		return nil, fmt.Errorf("read matching fallback search receipt: payload digest changed")
	}
	var intent MatchingAcquisitionIntent
	var plan policy.TypedFallbackQueryPlan
	if json.Unmarshal(intentBytes, &intent) != nil || intent.SchemaVersion != matchingAutomaticFallbackAcquisitionIntentVersion ||
		len(intent.InitialProviderInputs) != 1 || intent.InitialProviderInputs[0].OperationID != requestID ||
		intent.InitialProviderInputs[0].CacheKey != receipt.CacheKey || intent.InitialProviderInputs[0].Input == nil ||
		json.Unmarshal(intent.FallbackQueryPlan, &plan) != nil || len(plan.Stages) == 0 ||
		validateFallbackProviderInput(plan.Stages[0], &Operation{Kind: intent.InitialProviderInputs[0].Kind,
			InputSnapshot: intent.InitialProviderInputs[0].Input, ProviderCacheKey: fallbackStringPointer(receipt.CacheKey)}) != nil ||
		!matchingJSONEqual(intent.InitialProviderInputs[0].Input, receipt.RequestInput) ||
		runSourceID != receipt.ProviderSourceID || runConfigurationIdentity != receipt.ConfigurationIdentity ||
		intent.InitialProviderInputs[0].Kind != "provider_release_search" && intent.InitialProviderInputs[0].Kind != "provider_recording_search" {
		return nil, fmt.Errorf("read matching fallback search receipt: saved request scope is invalid")
	}
	receipt.PayloadSHA256 = hex.EncodeToString(digest)
	return receipt, nil
}

// ReplayAutomaticFallbackSearchReceipt returns only a receipt owned by this
// currently claimed initial fallback delivery. The receipt's original accepted
// fence is retained and intentionally need not equal the replay epoch.
func (repository *ProviderCacheRepository) ReplayAutomaticFallbackSearchReceipt(ctx context.Context, fence ProviderDeliveryFence) (*MatchingFallbackSearchReceipt, error) {
	if fence.OperationID == uuid.Nil || fence.Attempt < 1 || fence.RiverJobID < 1 || fence.ExecutionEpoch < 1 {
		return nil, fmt.Errorf("replay automatic fallback search receipt: delivery fence is required")
	}
	var runID uuid.UUID
	var kind, state, configurationIdentity, cacheKey string
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
		WHERE operation.id=?`, fence.OperationID).Scan(ctx, &kind, &state, &attempt, &jobID, &epoch, &sourceID, &configurationIdentity, &cacheKey, &runID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("replay automatic fallback search receipt: read delivery: %w", err)
	}
	if kind != "provider_release_search" && kind != "provider_recording_search" {
		return nil, nil
	}
	if state != "running" || attempt != fence.Attempt || !jobID.Valid || jobID.Int64 != fence.RiverJobID || epoch != fence.ExecutionEpoch {
		return nil, fmt.Errorf("replay automatic fallback search receipt: delivery is stale")
	}
	receipt, err := getMatchingFallbackSearchReceipt(ctx, repository.db, runID, fence.OperationID)
	if err != nil || receipt == nil {
		return receipt, err
	}
	if receipt.ProviderSourceID != sourceID || receipt.ConfigurationIdentity != configurationIdentity || receipt.CacheKey != cacheKey {
		return nil, fmt.Errorf("replay automatic fallback search receipt: provider scope changed")
	}
	return receipt, nil
}

// captureAutomaticFallbackSearchReceipt is called inside the provider
// acceptance transaction, before the worker can settle its dependency.
func (repository *ProviderCacheRepository) captureAutomaticFallbackSearchReceipt(
	ctx context.Context,
	tx bun.Tx,
	fence ProviderDeliveryFence,
	ticket ProviderRefreshTicket,
	payload []byte,
	revision string,
) error {
	var runID uuid.UUID
	var intentBytes []byte
	var sourceID uuid.UUID
	var configurationIdentity, cacheKey string
	var inputBytes []byte
	var operationKind string
	err := tx.NewRaw(`SELECT run.id,acquisition.intent,run.provider_source_id,run.provider_configuration_identity,
		dependency.cache_key,operation.input_snapshot,operation.kind
		FROM matching_run run
		JOIN matching_acquisition acquisition ON acquisition.run_id=run.id AND acquisition.intent_version=3
		JOIN matching_provider_dependency dependency ON dependency.parent_run_id=run.id AND dependency.request_id=?
		JOIN operation ON operation.id=dependency.request_id
		WHERE run.id=dependency.parent_run_id AND operation.id=?
		FOR SHARE OF run,acquisition,dependency`, fence.OperationID, fence.OperationID).
		Scan(ctx, &runID, &intentBytes, &sourceID, &configurationIdentity, &cacheKey, &inputBytes, &operationKind)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // This operation is not the registered initial automatic fallback dependency.
	}
	if err != nil {
		return err
	}
	if operationKind != "provider_release_search" && operationKind != "provider_recording_search" {
		return nil // Lookup receipts are captured by the independent lookup manifest.
	}
	var intent MatchingAcquisitionIntent
	if json.Unmarshal(intentBytes, &intent) != nil || intent.SchemaVersion != matchingAutomaticFallbackAcquisitionIntentVersion ||
		len(intent.InitialProviderInputs) != 1 || intent.InitialProviderInputs[0].OperationID != fence.OperationID ||
		intent.InitialProviderInputs[0].CacheKey != cacheKey || sourceID != ticket.ProviderSourceID ||
		configurationIdentity != ticket.ConfigurationIdentity || intent.InitialProviderInputs[0].Kind != "provider_release_search" && intent.InitialProviderInputs[0].Kind != "provider_recording_search" {
		return errors.New("accepted fallback response differs from its registered initial request")
	}
	var plan policy.TypedFallbackQueryPlan
	if json.Unmarshal(intent.FallbackQueryPlan, &plan) != nil || len(plan.Stages) == 0 {
		return errors.New("accepted fallback response has no initial plan stage")
	}
	if err := validateFallbackProviderInput(plan.Stages[0], &Operation{
		Kind: intent.InitialProviderInputs[0].Kind, InputSnapshot: intent.InitialProviderInputs[0].Input,
		ProviderCacheKey: fallbackStringPointer(cacheKey),
	}); err != nil {
		return fmt.Errorf("accepted fallback response request scope is invalid: %w", err)
	}
	canonicalInput, err := canonicalMatchingJSON(inputBytes)
	if err != nil || !matchingJSONEqual(canonicalInput, intent.InitialProviderInputs[0].Input) {
		return errors.New("accepted fallback response input differs from its immutable request")
	}
	var canonicalPayload string
	if err := tx.NewRaw(`SELECT ?::jsonb::text`, string(payload)).Scan(ctx, &canonicalPayload); err != nil {
		return err
	}
	apiPayload, err := json.Marshal(json.RawMessage(canonicalPayload))
	if err != nil {
		return err
	}
	digest := sha256.Sum256(apiPayload)
	receipt := MatchingFallbackSearchReceipt{
		RunID: runID, RequestID: fence.OperationID, StageIndex: 0, ProviderSourceID: ticket.ProviderSourceID,
		ConfigurationIdentity: ticket.ConfigurationIdentity, CacheKey: cacheKey, RequestInput: canonicalInput,
		Payload: apiPayload, PayloadSHA256: hex.EncodeToString(digest[:]), CacheRevision: nullablePointer(revision),
		CacheGeneration: ticket.Generation, FetchOrder: ticket.FetchOrder, ExecutionEpoch: fence.ExecutionEpoch,
		ProviderAttempt: fence.Attempt, RiverJobID: fence.RiverJobID,
	}
	return insertOrVerifyFallbackReceipt(ctx, tx, receipt, digest[:])
}

func insertOrVerifyFallbackReceipt(ctx context.Context, tx bun.Tx, receipt MatchingFallbackSearchReceipt, digest []byte) error {
	var prior MatchingFallbackSearchReceipt
	var priorDigest []byte
	var priorPayloadText string
	err := tx.NewRaw(`SELECT run_id,request_id,stage_index,provider_source_id,configuration_identity,cache_key,
		request_input,payload::text,payload_sha256,cache_revision,cache_generation,fetch_order,execution_epoch,
		provider_attempt,river_job_id FROM matching_fallback_search_receipt WHERE request_id=? FOR UPDATE`, receipt.RequestID).
		Scan(ctx, &prior.RunID, &prior.RequestID, &prior.StageIndex, &prior.ProviderSourceID,
			&prior.ConfigurationIdentity, &prior.CacheKey, &prior.RequestInput, &priorPayloadText, &priorDigest,
			&prior.CacheRevision, &prior.CacheGeneration, &prior.FetchOrder, &prior.ExecutionEpoch,
			&prior.ProviderAttempt, &prior.RiverJobID)
	if err == nil {
		prior.Payload, err = json.Marshal(json.RawMessage(priorPayloadText))
		if err != nil {
			return err
		}
		if prior.RunID != receipt.RunID || prior.StageIndex != receipt.StageIndex || prior.ProviderSourceID != receipt.ProviderSourceID ||
			prior.ConfigurationIdentity != receipt.ConfigurationIdentity || prior.CacheKey != receipt.CacheKey ||
			!matchingJSONEqual(prior.RequestInput, receipt.RequestInput) || !equalDigest(prior.Payload, receipt.Payload) ||
			!equalDigest(priorDigest, digest) || nullableString(prior.CacheRevision) != nullableString(receipt.CacheRevision) ||
			prior.CacheGeneration != receipt.CacheGeneration || prior.FetchOrder != receipt.FetchOrder ||
			prior.ExecutionEpoch != receipt.ExecutionEpoch || prior.ProviderAttempt != receipt.ProviderAttempt || prior.RiverJobID != receipt.RiverJobID {
			return errors.New("conflicting automatic fallback search receipt recapture")
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = tx.NewRaw(`INSERT INTO matching_fallback_search_receipt
		(run_id,request_id,stage_index,provider_source_id,configuration_identity,cache_key,request_input,payload,
		payload_sha256,cache_revision,cache_generation,fetch_order,execution_epoch,provider_attempt,river_job_id)
		VALUES (?,?,?,?,?,?,?::jsonb,?::jsonb,?,NULLIF(?,''),?,?,?,?,?)`, receipt.RunID, receipt.RequestID,
		receipt.StageIndex, receipt.ProviderSourceID, receipt.ConfigurationIdentity, receipt.CacheKey,
		string(receipt.RequestInput), string(receipt.Payload), digest, nullableString(receipt.CacheRevision),
		receipt.CacheGeneration, receipt.FetchOrder, receipt.ExecutionEpoch, receipt.ProviderAttempt, receipt.RiverJobID).Exec(ctx)
	return err
}

func nullablePointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func matchingJSONEqual(left, right json.RawMessage) bool {
	canonicalLeft, err := canonicalMatchingJSON(left)
	if err != nil {
		return false
	}
	canonicalRight, err := canonicalMatchingJSON(right)
	return err == nil && equalDigest(canonicalLeft, canonicalRight)
}
