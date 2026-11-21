package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// loadFallbackReviewHydration reads the registered review-only hydration pass
// for one terminal fallback calculation. It binds the exact phase revision and
// terminal calculation identity, then proves that every registered lookup is
// the canonical complement of the ranked shortlist and that each child settled
// successfully with an accepted, decodable receipt.
func loadFallbackReviewHydration(
	ctx context.Context,
	database bun.IDB,
	terminal MatchingFallbackCalculationContext,
	calculationDigest string,
	hydrationRevision int64,
) ([]MatchingFallbackCalculationLookupAuthority, []MatchingFallbackLookupReceipt, error) {
	scope := MatchingFallbackProviderSource{
		ID: terminal.RunSourceID, ProviderID: terminal.RunProviderID,
		Endpoint: terminal.RunEndpoint, ConfigurationIdentity: terminal.RunConfiguration,
	}
	if database == nil || terminal.RunID == uuid.Nil || terminal.AcquisitionRevision < 1 || terminal.StageIndex < 0 ||
		hydrationRevision <= terminal.AcquisitionRevision || len(mustDecodeSHA256(calculationDigest)) != sha256.Size ||
		scope.ID == uuid.Nil || scope.ProviderID == uuid.Nil || scope.Endpoint == "" || scope.ConfigurationIdentity == "" ||
		terminal.Intent.SchemaVersion != matchingAutomaticFallbackAcquisitionIntentVersion ||
		terminal.Intent.FallbackProviderSource == nil || *terminal.Intent.FallbackProviderSource != scope {
		return nil, nil, fmt.Errorf("load fallback review hydration: terminal authority is invalid")
	}
	if err := terminal.AcceptedShortlist.ValidateFallbackSearch(terminal.SearchKind, terminal.SearchReceipt); err != nil {
		return nil, nil, fmt.Errorf("load fallback review hydration: accepted shortlist is invalid: %w", err)
	}

	var phase MatchingAcquisitionPhase
	err := database.NewRaw(`SELECT revision,phase,input,output FROM matching_acquisition_phase
		WHERE run_id=? AND revision=? AND phase=?`, terminal.RunID, hydrationRevision, MatchingAcquisitionFallbackHydration).Scan(ctx,
		&phase.Revision, &phase.Phase, &phase.Input, &phase.Output)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, fmt.Errorf("load fallback review hydration: registered hydration manifest is missing")
	}
	if err != nil {
		return nil, nil, fmt.Errorf("load fallback review hydration: read registered hydration manifest: %w", err)
	}
	if phase.Revision != hydrationRevision || phase.Phase != MatchingAcquisitionFallbackHydration {
		return nil, nil, fmt.Errorf("load fallback review hydration: registered hydration manifest identity is invalid")
	}

	var input struct {
		StageIndex          int      `json:"stage_index"`
		CalculationRevision int64    `json:"calculation_revision"`
		DecisionDigest      string   `json:"decision_digest"`
		ReleaseIDs          []string `json:"release_ids"`
	}
	var output struct {
		StageIndex int `json:"stage_index"`
		Lookups    []struct {
			ReleaseID string    `json:"release_id"`
			RequestID uuid.UUID `json:"request_id"`
			CacheKey  string    `json:"cache_key"`
		} `json:"lookups"`
	}
	expected := terminal.AcceptedShortlist.ReviewOnlyReleaseIDs()
	if json.Unmarshal(phase.Input, &input) != nil || json.Unmarshal(phase.Output, &output) != nil ||
		input.StageIndex != terminal.StageIndex || input.CalculationRevision != terminal.AcquisitionRevision ||
		input.DecisionDigest != calculationDigest || !equalStringSlices(input.ReleaseIDs, expected) ||
		output.StageIndex != input.StageIndex || len(output.Lookups) != len(expected) {
		return nil, nil, fmt.Errorf("load fallback review hydration: registered review manifest is invalid")
	}

	lookups := make([]MatchingFallbackCalculationLookupAuthority, 0, len(output.Lookups))
	receipts := make([]MatchingFallbackLookupReceipt, 0, len(output.Lookups))
	seen := make(map[uuid.UUID]bool, len(output.Lookups))
	for index, descriptor := range output.Lookups {
		releaseID, parseErr := uuid.Parse(descriptor.ReleaseID)
		if parseErr != nil || releaseID.String() != descriptor.ReleaseID ||
			descriptor.ReleaseID != expected[index] || descriptor.RequestID == uuid.Nil ||
			descriptor.CacheKey != "release-lookup:"+descriptor.ReleaseID || seen[descriptor.RequestID] {
			return nil, nil, fmt.Errorf("load fallback review hydration: registered review lookup descriptor is invalid")
		}
		seen[descriptor.RequestID] = true

		var child struct {
			Kind               string          `bun:"kind"`
			State              string          `bun:"state"`
			SourceID           uuid.UUID       `bun:"provider_source_id"`
			ProviderID         uuid.UUID       `bun:"provider_id"`
			Endpoint           string          `bun:"endpoint"`
			Configuration      string          `bun:"provider_configuration_identity"`
			CacheKey           string          `bun:"provider_cache_key"`
			Explicit           bool            `bun:"provider_explicit_refresh"`
			Input              json.RawMessage `bun:"input_snapshot"`
			Outcome            string          `bun:"terminal_outcome"`
			DependencyCacheKey string          `bun:"dependency_cache_key"`
		}
		err = database.NewRaw(`SELECT operation.kind,operation.state,operation.provider_source_id,source.provider_id,source.endpoint,
			operation.provider_configuration_identity,operation.provider_cache_key,operation.provider_explicit_refresh,
			operation.input_snapshot,dependency.terminal_outcome,dependency.cache_key AS dependency_cache_key
			FROM operation JOIN provider_source source ON source.id=operation.provider_source_id
			JOIN matching_provider_dependency dependency ON dependency.request_id=operation.id AND dependency.parent_run_id=?
			WHERE operation.id=?`, terminal.RunID, descriptor.RequestID).Scan(ctx,
			&child.Kind, &child.State, &child.SourceID, &child.ProviderID, &child.Endpoint, &child.Configuration,
			&child.CacheKey, &child.Explicit, &child.Input, &child.Outcome, &child.DependencyCacheKey)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, fmt.Errorf("load fallback review hydration: registered review lookup child is missing")
		}
		if err != nil {
			return nil, nil, fmt.Errorf("load fallback review hydration: read registered review lookup child: %w", err)
		}
		if child.Kind != "provider_release_lookup" || child.State != "succeeded" || child.Outcome != "succeeded" || child.Explicit ||
			child.SourceID != scope.ID || child.ProviderID != scope.ProviderID || child.Endpoint != scope.Endpoint ||
			child.Configuration != scope.ConfigurationIdentity || child.CacheKey != descriptor.CacheKey ||
			child.DependencyCacheKey != child.CacheKey {
			return nil, nil, fmt.Errorf("load fallback review hydration: registered review lookup child differs from terminal authority")
		}

		registration, registrationErr := loadFallbackRequestRegistration(ctx, database, terminal.RunID, terminal.StageIndex, "lookup", descriptor.RequestID)
		if registrationErr != nil || registration.RequestID != descriptor.RequestID ||
			registration.StageIndex != terminal.StageIndex || registration.Role != "lookup" ||
			validateFallbackRequestRegistration(registration, "lookup", terminal.Intent, terminal.RunID, descriptor.RequestID,
				scope.ID, scope.ConfigurationIdentity, child.Kind, child.CacheKey, child.Input) != nil {
			return nil, nil, fmt.Errorf("load fallback review hydration: registered review lookup request is unavailable or invalid")
		}

		receipt, readErr := getMatchingFallbackLookupReceipt(ctx, database, terminal.RunID, descriptor.RequestID)
		if readErr != nil {
			return nil, nil, fmt.Errorf("load fallback review hydration: read accepted review lookup receipt: %w", readErr)
		}
		if receipt == nil || receipt.RunID != terminal.RunID || receipt.StageIndex != terminal.StageIndex ||
			receipt.CacheKey != descriptor.CacheKey || receipt.ProviderSourceID != scope.ID ||
			receipt.ConfigurationIdentity != scope.ConfigurationIdentity ||
			!matchingJSONEqual(child.Input, receipt.RequestInput) {
			return nil, nil, fmt.Errorf("load fallback review hydration: accepted review lookup receipt differs from registered authority")
		}
		if _, _, decodeErr := DecodeFallbackLayoutLookup(*receipt, scope, descriptor.ReleaseID); decodeErr != nil {
			return nil, nil, fmt.Errorf("load fallback review hydration: decode accepted review lookup: %w", decodeErr)
		}

		lookups = append(lookups, MatchingFallbackCalculationLookupAuthority{
			RequestID: descriptor.RequestID, StageIndex: terminal.StageIndex, ReleaseID: descriptor.ReleaseID,
			Kind: child.Kind, ProviderSourceID: child.SourceID, ProviderID: child.ProviderID,
			Endpoint: child.Endpoint, Configuration: child.Configuration, CacheKey: child.CacheKey,
			Input: append(json.RawMessage(nil), child.Input...),
		})
		receipts = append(receipts, *receipt)
	}
	return lookups, receipts, nil
}
