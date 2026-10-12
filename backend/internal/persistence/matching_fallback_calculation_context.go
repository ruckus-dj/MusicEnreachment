package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// MatchingFallbackCalculationLookupAuthority is the registered immutable scope
// for one lookup child, independent of the mutable provider cache.
type MatchingFallbackCalculationLookupAuthority struct {
	RequestID        uuid.UUID       `json:"request_id"`
	ReleaseID        string          `json:"release_id"`
	Kind             string          `json:"kind"`
	ProviderSourceID uuid.UUID       `json:"provider_source_id"`
	ProviderID       uuid.UUID       `json:"provider_id"`
	Endpoint         string          `json:"endpoint"`
	Configuration    string          `json:"configuration_identity"`
	CacheKey         string          `json:"cache_key"`
	Input            json.RawMessage `json:"input"`
}

// MatchingFallbackCalculationContext is a persistence-owned, read-only snapshot
// of the evidence needed by the pure fallback applicability calculator.
type MatchingFallbackCalculationContext struct {
	RunID               uuid.UUID
	AcquisitionRevision int64
	Intent              MatchingAcquisitionIntent
	Target              MatchingRunFenceSnapshot
	PhaseInput          json.RawMessage
	PhaseOutput         json.RawMessage
	PhaseProvenance     json.RawMessage
	SearchReceipt       MatchingFallbackSearchReceipt
	LookupReceipts      []MatchingFallbackLookupReceipt
	RunSourceID         uuid.UUID
	RunProviderID       uuid.UUID
	RunEndpoint         string
	RunConfiguration    string
	SearchKind          string
	SearchCacheKey      string
	SearchInput         json.RawMessage
	Lookups             []MatchingFallbackCalculationLookupAuthority
}

// LoadMatchingFallbackCalculationContext loads one coherent snapshot of the
// frozen run, its current applicability phase, registered children, and receipts.
func (repository *SetupManagerRepository) LoadMatchingFallbackCalculationContext(
	ctx context.Context, runID uuid.UUID, expectedRevision int64,
) (MatchingFallbackCalculationContext, bool, error) {
	var result MatchingFallbackCalculationContext
	if runID == uuid.Nil || expectedRevision < 1 {
		return result, false, fmt.Errorf("load fallback calculation context: run and revision are required")
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return result, false, fmt.Errorf("load fallback calculation context: begin snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var run struct {
		OperationID   uuid.UUID `bun:"operation_id"`
		Kind          string    `bun:"kind"`
		Plan          []byte    `bun:"immutable_plan"`
		PlanDigest    []byte    `bun:"plan_digest"`
		FenceVersion  int       `bun:"fence_version"`
		SourceID      uuid.UUID `bun:"provider_source_id"`
		Configuration string    `bun:"provider_configuration_identity"`
		ProviderID    uuid.UUID `bun:"provider_id"`
		Endpoint      string    `bun:"endpoint"`
		SourceConfig  string    `bun:"configuration_identity"`
	}
	var intentBytes, intentDigest []byte
	var intentVersion int
	var currentPhase string
	var revision int64
	err = tx.NewRaw(`SELECT run.operation_id,operation.kind,run.immutable_plan,run.plan_digest,run.fence_version,
		run.provider_source_id,run.provider_configuration_identity,source.provider_id,source.endpoint,source.configuration_identity,
		acquisition.intent,acquisition.intent_digest,acquisition.intent_version,acquisition.current_phase,acquisition.revision
		FROM matching_run run JOIN operation ON operation.id=run.operation_id
		JOIN provider_source source ON source.id=run.provider_source_id
		JOIN matching_acquisition acquisition ON acquisition.run_id=run.id WHERE run.id=?`, runID).Scan(ctx,
		&run.OperationID, &run.Kind, &run.Plan, &run.PlanDigest, &run.FenceVersion, &run.SourceID, &run.Configuration, &run.ProviderID, &run.Endpoint, &run.SourceConfig,
		&intentBytes, &intentDigest, &intentVersion, &currentPhase, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return result, false, nil
	}
	if err != nil {
		return result, false, fmt.Errorf("load fallback calculation context: read run: %w", err)
	}
	if revision != expectedRevision {
		return result, false, nil
	}
	if run.OperationID != runID || run.Kind != "matching_evaluation_v1" || run.FenceVersion != 2 ||
		intentVersion != matchingAutomaticFallbackAcquisitionIntentVersion || currentPhase != MatchingAcquisitionFallbackApplicability {
		return result, false, fmt.Errorf("load fallback calculation context: run is not in fallback applicability")
	}
	var intent MatchingAcquisitionIntent
	if json.Unmarshal(intentBytes, &intent) != nil || intent.SchemaVersion != intentVersion ||
		validateCanonicalMatchingAcquisitionIntent(intent, intentDigest, run.PlanDigest, run.Plan) != nil {
		return result, false, fmt.Errorf("load fallback calculation context: saved intent or plan is invalid")
	}
	var phase MatchingAcquisitionPhase
	if err := tx.NewRaw(`SELECT revision,phase,input,output,response_provenance FROM matching_acquisition_phase WHERE run_id=? AND revision=?`, runID, revision).Scan(ctx,
		&phase.Revision, &phase.Phase, &phase.Input, &phase.Output, &phase.ResponseProvenance); err != nil {
		return result, false, fmt.Errorf("load fallback calculation context: read applicability phase: %w", err)
	}
	if phase.Revision != revision || phase.Phase != currentPhase {
		return result, false, fmt.Errorf("load fallback calculation context: current phase ledger differs")
	}
	var target struct {
		GroupID  uuid.UUID       `bun:"group_id"`
		Revision string          `bun:"captured_group_revision"`
		Capture  json.RawMessage `bun:"target_capture"`
		Digest   []byte          `bun:"target_capture_digest"`
	}
	if err := tx.NewRaw(`SELECT group_id,captured_group_revision,target_capture,target_capture_digest FROM matching_run_target WHERE run_id=?`, runID).Scan(ctx,
		&target.GroupID, &target.Revision, &target.Capture, &target.Digest); err != nil {
		return result, false, fmt.Errorf("load fallback calculation context: read frozen target: %w", err)
	}
	canonicalCapture, digest, err := automaticArtistTargetCaptureDigest(target.Capture)
	if err != nil || !equalDigest(digest[:], target.Digest) || fmt.Sprintf("%x", digest[:]) != intent.TargetCaptureDigest ||
		len(intent.Targets) != 1 || intent.Targets[0] != (MatchingTarget{GroupID: target.GroupID, Revision: target.Revision}) {
		return result, false, fmt.Errorf("load fallback calculation context: frozen target authority is invalid")
	}
	targetFence := MatchingRunFenceSnapshot{Version: 2, GroupID: target.GroupID, GroupRevision: target.Revision, Capture: canonicalCapture, CaptureDigest: fmt.Sprintf("%x", digest[:])}

	var searchRef []matchingAcquisitionResponseProvenance
	if json.Unmarshal(phase.ResponseProvenance, &searchRef) != nil || len(searchRef) != 1 || searchRef[0].RequestID == uuid.Nil {
		return result, false, fmt.Errorf("load fallback calculation context: search receipt reference is invalid")
	}
	searchReceipt, err := getMatchingFallbackSearchReceipt(ctx, tx, runID, searchRef[0].RequestID)
	if err != nil || searchReceipt == nil {
		if err == nil {
			err = fmt.Errorf("accepted search receipt is missing")
		}
		return result, false, fmt.Errorf("load fallback calculation context: read search receipt: %w", err)
	}
	if searchRef[0].CacheKey != searchReceipt.CacheKey || searchRef[0].PayloadSHA != searchReceipt.PayloadSHA256 ||
		run.SourceConfig != run.Configuration || searchReceipt.ProviderSourceID != run.SourceID || searchReceipt.ConfigurationIdentity != run.Configuration {
		return result, false, fmt.Errorf("load fallback calculation context: search receipt differs from frozen source authority")
	}
	if len(intent.InitialProviderInputs) != 1 || intent.InitialProviderInputs[0].OperationID != searchReceipt.RequestID {
		return result, false, fmt.Errorf("load fallback calculation context: initial search intent is invalid")
	}
	var searchOperation struct {
		ID            uuid.UUID       `bun:"id"`
		Kind          string          `bun:"kind"`
		State         string          `bun:"state"`
		Input         json.RawMessage `bun:"input_snapshot"`
		SourceID      uuid.UUID       `bun:"provider_source_id"`
		ProviderID    uuid.UUID       `bun:"provider_id"`
		Endpoint      string          `bun:"endpoint"`
		Configuration string          `bun:"provider_configuration_identity"`
		CacheKey      string          `bun:"provider_cache_key"`
		Explicit      bool            `bun:"provider_explicit_refresh"`
		Outcome       string          `bun:"terminal_outcome"`
	}
	err = tx.NewRaw(`SELECT operation.id,operation.kind,operation.state,operation.input_snapshot,
		operation.provider_source_id,source.provider_id,source.endpoint,operation.provider_configuration_identity,
		operation.provider_cache_key,operation.provider_explicit_refresh,dependency.terminal_outcome
		FROM operation JOIN provider_source source ON source.id=operation.provider_source_id
		JOIN matching_provider_dependency dependency ON dependency.request_id=operation.id AND dependency.parent_run_id=?
		WHERE operation.id=?`, runID, searchReceipt.RequestID).Scan(ctx,
		&searchOperation.ID, &searchOperation.Kind, &searchOperation.State, &searchOperation.Input,
		&searchOperation.SourceID, &searchOperation.ProviderID, &searchOperation.Endpoint, &searchOperation.Configuration,
		&searchOperation.CacheKey, &searchOperation.Explicit, &searchOperation.Outcome)
	if err != nil {
		return result, false, fmt.Errorf("load fallback calculation context: read registered search operation: %w", err)
	}
	initialSearch := intent.InitialProviderInputs[0]
	if searchOperation.ID != searchReceipt.RequestID || searchOperation.State != "succeeded" || searchOperation.Outcome != "succeeded" ||
		searchOperation.Explicit || searchOperation.Kind != initialSearch.Kind ||
		searchOperation.Kind != "provider_release_search" && searchOperation.Kind != "provider_recording_search" ||
		searchOperation.SourceID != run.SourceID || searchOperation.ProviderID != run.ProviderID || searchOperation.Endpoint != run.Endpoint ||
		searchOperation.Configuration != run.Configuration || searchOperation.CacheKey != initialSearch.CacheKey ||
		!matchingJSONEqual(searchOperation.Input, initialSearch.Input) || !matchingJSONEqual(searchOperation.Input, searchReceipt.RequestInput) ||
		searchOperation.CacheKey != searchReceipt.CacheKey {
		return result, false, fmt.Errorf("load fallback calculation context: registered search operation differs from accepted authority")
	}

	var phaseOut struct {
		SearchReceipt matchingAcquisitionResponseProvenance `json:"search_receipt"`
		Lookups       []struct {
			ReleaseID string    `json:"release_id"`
			RequestID uuid.UUID `json:"request_id"`
			CacheKey  string    `json:"cache_key"`
		} `json:"lookups"`
		LookupReceipts []struct {
			ReleaseID     string    `json:"release_id"`
			RequestID     uuid.UUID `json:"request_id"`
			CacheKey      string    `json:"cache_key"`
			PayloadSHA    string    `json:"payload_sha256"`
			ProjectionSHA string    `json:"projection_sha256"`
		} `json:"lookup_receipts"`
	}
	if json.Unmarshal(phase.Output, &phaseOut) != nil {
		return result, false, fmt.Errorf("load fallback calculation context: applicability manifest is invalid")
	}
	// Applicability output records accepted receipts; the registered lookup set
	// comes from its original hydration input/output phase, never latest cache state.
	var hydration MatchingAcquisitionPhase
	err = tx.NewRaw(`SELECT revision,phase,input,output FROM matching_acquisition_phase WHERE run_id=? AND phase=? ORDER BY revision DESC LIMIT 1`, runID, MatchingAcquisitionFallbackHydration).Scan(ctx,
		&hydration.Revision, &hydration.Phase, &hydration.Input, &hydration.Output)
	if errors.Is(err, sql.ErrNoRows) {
		// Stage zero can go straight to applicability when it names no releases.
		// Preserve that original descriptor rather than manufacturing hydration.
		if len(phaseOut.Lookups) != 0 || len(phaseOut.LookupReceipts) != 0 {
			return result, false, fmt.Errorf("load fallback calculation context: registered hydration manifest is missing")
		}
		hydration = MatchingAcquisitionPhase{Revision: revision, Phase: currentPhase, Input: phase.Input, Output: phase.Output}
	} else if err != nil {
		return result, false, fmt.Errorf("load fallback calculation context: read registered hydration manifest: %w", err)
	}
	var hydrationInput struct {
		ReleaseIDs     []string        `json:"release_ids"`
		SearchManifest json.RawMessage `json:"search_manifest"`
	}
	var hydrationOutput struct {
		Lookups []struct {
			ReleaseID string    `json:"release_id"`
			RequestID uuid.UUID `json:"request_id"`
			CacheKey  string    `json:"cache_key"`
		} `json:"lookups"`
	}
	if json.Unmarshal(hydration.Input, &hydrationInput) != nil || json.Unmarshal(hydration.Output, &hydrationOutput) != nil ||
		len(hydrationInput.ReleaseIDs) != len(hydrationOutput.Lookups) {
		return result, false, fmt.Errorf("load fallback calculation context: registered hydration manifest is invalid")
	}
	if hydration.Phase == MatchingAcquisitionFallbackHydration && hydration.Revision+1 != revision {
		return result, false, fmt.Errorf("load fallback calculation context: applicability phase is detached from its registered hydration")
	}
	lookups := make([]MatchingFallbackCalculationLookupAuthority, 0, len(hydrationOutput.Lookups))
	receipts := make([]MatchingFallbackLookupReceipt, 0, len(hydrationOutput.Lookups))
	for index, descriptor := range hydrationOutput.Lookups {
		if descriptor.ReleaseID != hydrationInput.ReleaseIDs[index] || descriptor.RequestID == uuid.Nil || descriptor.CacheKey != "release-lookup:"+descriptor.ReleaseID {
			return result, false, fmt.Errorf("load fallback calculation context: registered lookup descriptor is invalid")
		}
		var child struct {
			Kind          string          `bun:"kind"`
			SourceID      uuid.UUID       `bun:"provider_source_id"`
			ProviderID    uuid.UUID       `bun:"provider_id"`
			Endpoint      string          `bun:"provider_endpoint"`
			Configuration string          `bun:"provider_configuration_identity"`
			CacheKey      string          `bun:"provider_cache_key"`
			Input         json.RawMessage `bun:"input_snapshot"`
		}
		if err := tx.NewRaw(`SELECT operation.kind,operation.provider_source_id,source.provider_id,source.endpoint,operation.provider_configuration_identity,operation.provider_cache_key,operation.input_snapshot
			FROM operation JOIN provider_source source ON source.id=operation.provider_source_id WHERE operation.id=?`, descriptor.RequestID).Scan(ctx,
			&child.Kind, &child.SourceID, &child.ProviderID, &child.Endpoint, &child.Configuration, &child.CacheKey, &child.Input); err != nil {
			return result, false, fmt.Errorf("load fallback calculation context: read registered lookup operation: %w", err)
		}
		if child.SourceID != run.SourceID || child.ProviderID != run.ProviderID || child.Endpoint != run.Endpoint || child.Configuration != run.Configuration || child.CacheKey != descriptor.CacheKey {
			return result, false, fmt.Errorf("load fallback calculation context: lookup operation authority differs from run")
		}
		receipt, readErr := getMatchingFallbackLookupReceipt(ctx, tx, runID, descriptor.RequestID)
		if readErr != nil || receipt == nil {
			if readErr == nil {
				readErr = fmt.Errorf("accepted lookup receipt is missing")
			}
			return result, false, fmt.Errorf("load fallback calculation context: read lookup receipt: %w", readErr)
		}
		if index >= len(phaseOut.LookupReceipts) || phaseOut.LookupReceipts[index].ReleaseID != descriptor.ReleaseID ||
			phaseOut.LookupReceipts[index].RequestID != descriptor.RequestID || phaseOut.LookupReceipts[index].CacheKey != descriptor.CacheKey ||
			phaseOut.LookupReceipts[index].PayloadSHA != receipt.PayloadSHA256 || phaseOut.LookupReceipts[index].ProjectionSHA != receipt.ProjectionSHA256 {
			return result, false, fmt.Errorf("load fallback calculation context: applicability receipt reference differs from registered lookup")
		}
		lookups = append(lookups, MatchingFallbackCalculationLookupAuthority{RequestID: descriptor.RequestID, ReleaseID: descriptor.ReleaseID, Kind: child.Kind,
			ProviderSourceID: child.SourceID, ProviderID: child.ProviderID, Endpoint: child.Endpoint, Configuration: child.Configuration, CacheKey: child.CacheKey, Input: child.Input})
		receipts = append(receipts, *receipt)
	}
	if len(phaseOut.LookupReceipts) != 0 && len(phaseOut.LookupReceipts) != len(lookups) ||
		len(phaseOut.LookupReceipts) == 0 && len(lookups) != 0 || phaseOut.SearchReceipt != (matchingAcquisitionResponseProvenance{}) && phaseOut.SearchReceipt != searchRef[0] {
		return result, false, fmt.Errorf("load fallback calculation context: applicability receipt set is incomplete")
	}
	result = MatchingFallbackCalculationContext{RunID: runID, AcquisitionRevision: revision, Intent: intent, Target: targetFence,
		PhaseInput: append(json.RawMessage(nil), hydration.Input...), PhaseOutput: append(json.RawMessage(nil), hydration.Output...),
		PhaseProvenance: append(json.RawMessage(nil), phase.ResponseProvenance...), SearchReceipt: *searchReceipt, LookupReceipts: receipts,
		RunSourceID: run.SourceID, RunProviderID: run.ProviderID, RunEndpoint: run.Endpoint, RunConfiguration: run.Configuration,
		SearchKind: searchOperation.Kind, SearchCacheKey: searchOperation.CacheKey, SearchInput: append(json.RawMessage(nil), searchOperation.Input...), Lookups: lookups}
	return result, true, nil
}
