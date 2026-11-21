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

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// recheckFrozenFallbackProviderHistory validates the immutable proof prefix
// named by the seal against the current provider scope. The caller must not
// already hold only a shared lock on the provider source row.
func recheckFrozenFallbackProviderHistory(ctx context.Context, tx bun.Tx, seal MatchingFallbackSealAuthority) error {
	if seal.Disposition == "no_query" && len(seal.ProofHistory) == 0 {
		return nil
	}
	history := make([]MatchingFallbackCalculationContext, 0, len(seal.ProofHistory))
	for _, proof := range seal.ProofHistory {
		receipt, err := getMatchingFallbackCalculationReceipt(ctx, tx, seal.RunID, proof.AcquisitionRevision)
		if err != nil {
			return err
		}
		if receipt == nil || proof.StageIndex != receipt.StageIndex || proof.InputDigest != receipt.InputDigest ||
			proof.DecisionDigest != receipt.DecisionDigest || proof.SchemaVersion != receipt.SchemaVersion ||
			proof.CalculatorVersion != receipt.CalculatorVersion || proof.DigestVersion != receipt.DigestVersion {
			return errors.New("fallback layout proof differs from its saved calculation")
		}
		var stage MatchingFallbackCalculationContext
		if err := json.Unmarshal(receipt.Context, &stage); err != nil {
			return fmt.Errorf("decode fallback layout proof context: %w", err)
		}
		history = append(history, stage)
	}
	if len(seal.ReviewLookups) != 0 {
		// These receipts are additional freshness inputs for terminal review,
		// never inputs to the decision or its calculation digest.
		last := &history[len(history)-1]
		last.Lookups = append(last.Lookups, seal.ReviewLookups...)
		last.LookupReceipts = append(last.LookupReceipts, seal.ReviewLookupReceipts...)
	}
	return ValidateMatchingFallbackProviderLayoutHistoryTx(ctx, tx, seal.RunID, history)
}

// ValidateMatchingFallbackProviderLayoutHistoryTx checks the accepted
// MusicBrainz evidence for each contiguous stage from the start of a run.
// The caller supplies the immutable contexts referenced by its decision seal
// and independently verifies that this is the complete required prefix.
// Neither this check nor the provider receipts prove a decision's optimality.
//
// Call after the output/source fences, but before Local graph locks. The
// source row stays write-locked through the caller's commit so materialization
// cannot require a lock upgrade, and refreshes cannot move accepted evidence.
func ValidateMatchingFallbackProviderLayoutHistoryTx(
	ctx context.Context, tx bun.Tx, runID uuid.UUID, history []MatchingFallbackCalculationContext,
) error {
	if runID == uuid.Nil || len(history) == 0 {
		return errors.New("matching fallback layout requires a run and at least one accepted stage")
	}
	if err := requireMatchingLayoutEvidenceReadCommitted(ctx, tx); err != nil {
		return err
	}
	first := history[0]
	scope := MatchingFallbackProviderSource{
		ID: first.RunSourceID, ProviderID: first.RunProviderID,
		Endpoint: first.RunEndpoint, ConfigurationIdentity: first.RunConfiguration,
	}
	if scope.ID == uuid.Nil || scope.ProviderID == uuid.Nil || scope.Endpoint == "" || scope.ConfigurationIdentity == "" {
		return errors.New("matching fallback layout provider scope is incomplete")
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(hashtext(?))`, "musicbrainz-config"); err != nil {
		return fmt.Errorf("validate fallback layout history: lock provider configuration: %w", err)
	}
	var source ProviderSource
	err := tx.NewRaw(`SELECT source.id,source.provider_id,source.namespace,source.endpoint,source.configuration_identity
		FROM provider_source source JOIN provider ON provider.id=source.provider_id
		JOIN app_setting setting ON setting.setting_name='musicbrainz_config_identity'
			AND setting.setting_value=source.configuration_identity
		WHERE source.id=? AND provider.id=? AND provider.code='musicbrainz'
			AND source.endpoint=? AND source.configuration_identity=? FOR UPDATE OF source`,
		scope.ID, scope.ProviderID, scope.Endpoint, scope.ConfigurationIdentity).Scan(ctx, &source)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrIncomingMatchConflict
	}
	if err != nil {
		return fmt.Errorf("validate fallback layout history: lock provider source: %w", err)
	}
	var previousRevision int64
	for index, stage := range history {
		if stage.RunID != runID || stage.StageIndex != index || stage.AcquisitionRevision <= previousRevision ||
			stage.RunSourceID != scope.ID || stage.RunProviderID != scope.ProviderID ||
			stage.RunEndpoint != scope.Endpoint || stage.RunConfiguration != scope.ConfigurationIdentity {
			return errors.New("matching fallback layout history has a mismatched stage")
		}
		if err := validateFallbackLayoutStageSearchTx(ctx, tx, scope, stage); err != nil {
			return err
		}
		if err := validateFallbackLayoutStageLookupsTx(ctx, tx, source, scope, stage); err != nil {
			return err
		}
		previousRevision = stage.AcquisitionRevision
	}
	return nil
}

func validateFallbackLayoutStageSearchTx(ctx context.Context, tx bun.Tx, scope MatchingFallbackProviderSource, stage MatchingFallbackCalculationContext) error {
	receipt := stage.SearchReceipt
	if receipt.RunID != stage.RunID || receipt.StageIndex != stage.StageIndex || receipt.RequestID == uuid.Nil ||
		receipt.ProviderSourceID != scope.ID || receipt.ConfigurationIdentity != scope.ConfigurationIdentity ||
		receipt.CacheKey == "" || receipt.CacheKey != stage.SearchCacheKey || receipt.PayloadDigestVersion != 2 ||
		len(mustDecodeSHA256(receipt.PayloadSHA256)) != sha256.Size || !jsonObject(receipt.Payload) {
		return errors.New("matching fallback layout history has invalid accepted search evidence")
	}
	accepted, err := canonicalMatchingJSON(receipt.Payload)
	if err != nil {
		return fmt.Errorf("validate fallback layout history: decode saved search response: %w", err)
	}
	expectedDigest := sha256.Sum256(accepted)
	if hex.EncodeToString(expectedDigest[:]) != receipt.PayloadSHA256 {
		return errors.New("matching fallback layout history search receipt digest changed")
	}
	var payload json.RawMessage
	var revision *string
	err = tx.NewRaw(`SELECT payload,revision FROM provider_response_cache
		WHERE provider_source_id=? AND provider_id=? AND cache_key=? AND payload IS NOT NULL`,
		scope.ID, scope.ProviderID, receipt.CacheKey).Scan(ctx, &payload, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrIncomingMatchConflict
	}
	if err != nil {
		return fmt.Errorf("validate fallback layout history: read accepted search response: %w", err)
	}
	canonical, err := canonicalMatchingJSON(payload)
	if err != nil {
		return fmt.Errorf("validate fallback layout history: decode accepted search response: %w", err)
	}
	digest := sha256.Sum256(canonical)
	if !equalOptionalString(revision, receipt.CacheRevision) || hex.EncodeToString(digest[:]) != receipt.PayloadSHA256 {
		return fmt.Errorf("validate fallback layout history: accepted search changed: %w", ErrIncomingMatchConflict)
	}
	return nil
}

func validateFallbackLayoutStageLookupsTx(ctx context.Context, tx bun.Tx, source ProviderSource, scope MatchingFallbackProviderSource, stage MatchingFallbackCalculationContext) error {
	requests := make(map[uuid.UUID]MatchingFallbackCalculationLookupAuthority, len(stage.Lookups))
	releases := make(map[string]bool, len(stage.Lookups))
	for _, lookup := range stage.Lookups {
		if lookup.RequestID == uuid.Nil || requests[lookup.RequestID].RequestID != uuid.Nil || lookup.StageIndex != stage.StageIndex ||
			lookup.Kind != "provider_release_lookup" || lookup.ProviderSourceID != scope.ID || lookup.ProviderID != scope.ProviderID ||
			lookup.Endpoint != scope.Endpoint || lookup.Configuration != scope.ConfigurationIdentity || releases[lookup.ReleaseID] {
			return errors.New("matching fallback layout history has invalid lookup authority")
		}
		requests[lookup.RequestID] = lookup
		releases[lookup.ReleaseID] = true
	}
	for _, receipt := range stage.LookupReceipts {
		lookup, exists := requests[receipt.RequestID]
		if !exists || receipt.RunID != stage.RunID || receipt.StageIndex != stage.StageIndex || receipt.CacheKey != lookup.CacheKey {
			return errors.New("matching fallback layout history has an unexpected lookup receipt")
		}
		frozen, proof, err := decodeFallbackLayoutLookup(receipt, scope, lookup.ReleaseID)
		if err != nil {
			return err
		}
		if proof.Namespace != source.Namespace {
			return ErrIncomingMatchConflict
		}
		current, err := readMusicBrainzMatchingSnapshot(ctx, tx, scope.Endpoint, scope.ConfigurationIdentity, proof.ReleaseKeys, proof.HydrationKeys, false)
		if err != nil {
			return fmt.Errorf("validate fallback layout history: read current projection: %w", err)
		}
		response := current.Responses[receipt.CacheKey]
		if current.Source.ID != proof.ProviderSourceID || current.Source.Namespace != proof.Namespace ||
			response == nil || !equalOptionalString(response.Revision, receipt.CacheRevision) ||
			!matchingJSONEqual(response.Payload, receipt.Payload) {
			return fmt.Errorf("validate fallback layout history: accepted lookup response changed: %w", ErrIncomingMatchConflict)
		}
		if current.AcceptedProjectionRevision != proof.AcceptedProjectionRevision {
			if err := compareFallbackLayoutProjections(frozen, current); err != nil {
				return err
			}
		}
		delete(requests, receipt.RequestID)
	}
	if len(requests) != 0 {
		return errors.New("matching fallback layout history is missing accepted lookup receipts")
	}
	return nil
}

// A sibling lookup can reaccept an identical shared recording or artist and
// advance its observation order. The immutable receipt retains that exact
// provenance; freshness here compares normalized data and field authority, not
// order or the raw transport fragment that happened to supply those values.
func compareFallbackLayoutProjections(frozen, current ProviderMatchingSnapshot) error {
	expected, err := fallbackLayoutProjectionValues(frozen)
	if err != nil {
		return fmt.Errorf("validate fallback layout history: normalize saved projection: %w", err)
	}
	actual, err := fallbackLayoutProjectionValues(current)
	if err != nil {
		return fmt.Errorf("validate fallback layout history: normalize current projection: %w", err)
	}
	if !bytes.Equal(expected, actual) {
		return fmt.Errorf("validate fallback layout history: accepted lookup projection changed: %w", ErrIncomingMatchConflict)
	}
	return nil
}

func fallbackLayoutProjectionValues(snapshot ProviderMatchingSnapshot) ([]byte, error) {
	// Snapshots passed here are private to validation; the receipt bytes and
	// their exact projection digest are never rewritten.
	for i := range snapshot.Releases {
		release := &snapshot.Releases[i]
		release.FetchOrder = 0
		release.RawSource = nil
		omitFallbackLayoutFieldFetchOrder(release.FieldEvidence)
		for j := range release.Credits {
			release.Credits[j].FetchOrder = 0
		}
		for j := range release.Tracks {
			release.Tracks[j].FetchOrder = 0
		}
	}
	for i := range snapshot.ProjectionEntities {
		entity := &snapshot.ProjectionEntities[i]
		entity.FetchOrder = 0
		entity.RawSource = nil
		if len(entity.FieldEvidence) == 0 {
			continue
		}
		var fields map[string]ProviderFieldEvidence
		if err := json.Unmarshal(entity.FieldEvidence, &fields); err != nil {
			return nil, err
		}
		omitFallbackLayoutFieldFetchOrder(fields)
		normalized, err := json.Marshal(fields)
		if err != nil {
			return nil, err
		}
		entity.FieldEvidence = normalized
	}
	encoded, err := json.Marshal(struct {
		Releases []MatchingRelease
		Entities []MatchingProjectionEntity
	}{snapshot.Releases, snapshot.ProjectionEntities})
	if err != nil {
		return nil, err
	}
	return canonicalMatchingJSON(encoded)
}

func omitFallbackLayoutFieldFetchOrder(fields map[string]ProviderFieldEvidence) {
	for name, evidence := range fields {
		evidence.FetchOrder = 0
		fields[name] = evidence
	}
}
