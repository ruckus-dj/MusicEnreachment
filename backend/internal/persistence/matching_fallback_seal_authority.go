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

const matchingFallbackSealAuthoritySchemaVersion = 3

// MatchingFallbackSealCalculationReference points at an immutable saved
// calculation. Version fields deliberately preserve the receipt's historical
// tuple; they are not upgraded to the current writer version.
type MatchingFallbackSealCalculationReference struct {
	AcquisitionRevision int64  `json:"acquisition_revision"`
	StageIndex          int    `json:"stage_index"`
	SchemaVersion       int    `json:"schema_version"`
	CalculatorVersion   int    `json:"calculator_version"`
	DigestVersion       int    `json:"digest_version"`
	InputDigest         string `json:"input_digest"`
}

// MatchingFallbackSealAuthority is a read-only, persistence-derived authority
// envelope. Its evaluation context is the exact accepted calculation context,
// not a caller-supplied projection or a fresh read of provider/source state.
type MatchingFallbackSealAuthority struct {
	SchemaVersion            int                                        `json:"schema_version"`
	Disposition              string                                     `json:"disposition"`
	RunID                    uuid.UUID                                  `json:"run_id"`
	WaitRevision             int64                                      `json:"wait_revision"`
	Intent                   MatchingAcquisitionIntent                  `json:"intent"`
	Target                   MatchingRunFenceSnapshot                   `json:"target"`
	CapturedMetadata         []MatchingAcquisitionCapturedMember        `json:"captured_metadata"`
	SourceID                 uuid.UUID                                  `json:"source_id"`
	ProviderID               uuid.UUID                                  `json:"provider_id"`
	Endpoint                 string                                     `json:"endpoint"`
	SourceConfiguration      string                                     `json:"source_configuration"`
	RunProviderConfiguration string                                     `json:"run_provider_configuration"`
	GroupAuthority           IncomingGroupMatchingAuthority             `json:"group_authority"`
	Plan                     json.RawMessage                            `json:"plan"`
	SelectedCalculation      *MatchingFallbackSealCalculationReference  `json:"selected_calculation,omitempty"`
	ProofHistory             []MatchingFallbackSealCalculationReference `json:"proof_history"`
	FullEvaluationContext    json.RawMessage                            `json:"full_evaluation_context,omitempty"`
	Digest                   string                                     `json:"digest"`
}

// BuildMatchingFallbackSealAuthority derives the authority for the current
// durable wait from one repeatable-read snapshot. It performs no writes and
// accepts no caller-provided authority payload.
func (repository *SetupManagerRepository) BuildMatchingFallbackSealAuthority(
	ctx context.Context, runID uuid.UUID, expectedWaitRevision int64,
) (MatchingFallbackSealAuthority, bool, error) {
	var result MatchingFallbackSealAuthority
	if runID == uuid.Nil || expectedWaitRevision < 1 {
		return result, false, fmt.Errorf("build fallback seal authority: run and wait revision are required")
	}
	tx, err := repository.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return result, false, fmt.Errorf("build fallback seal authority: begin snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	return buildMatchingFallbackSealAuthority(ctx, tx, runID, expectedWaitRevision, false)
}

func buildMatchingFallbackSealAuthority(
	ctx context.Context, database bun.IDB, runID uuid.UUID, expectedWaitRevision int64, historical bool,
) (MatchingFallbackSealAuthority, bool, error) {
	var result MatchingFallbackSealAuthority
	var phase string
	var revision int64
	var err error
	if historical {
		revision = expectedWaitRevision
		err = database.NewRaw(`SELECT phase FROM matching_acquisition_phase WHERE run_id=? AND revision=?`, runID, revision).Scan(ctx, &phase)
	} else {
		err = database.NewRaw(`SELECT current_phase,revision FROM matching_acquisition WHERE run_id=?`, runID).Scan(ctx, &phase, &revision)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return result, false, nil
	}
	if err != nil {
		return result, false, fmt.Errorf("build fallback seal authority: read current wait: %w", err)
	}
	if revision != expectedWaitRevision || (phase != "pending_seal" && phase != "fallback_review_wait") {
		return result, false, nil
	}
	var input, output string
	if err := database.NewRaw(`SELECT input,output FROM matching_acquisition_phase WHERE run_id=? AND revision=? AND phase=?`, runID, revision, phase).
		Scan(ctx, &input, &output); err != nil {
		return result, false, fmt.Errorf("build fallback seal authority: read wait phase: %w", err)
	}
	var wait struct {
		CalculationRevision int64  `json:"calculation_revision"`
		StageIndex          int    `json:"stage_index"`
		Disposition         string `json:"disposition"`
	}
	if err := json.Unmarshal([]byte(input), &wait); err != nil {
		return result, false, fmt.Errorf("build fallback seal authority: wait input is invalid: %w", err)
	}
	var waitOutput struct {
		Disposition string `json:"disposition"`
		Reason      string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(output), &waitOutput); err != nil {
		return result, false, fmt.Errorf("build fallback seal authority: wait output is invalid: %w", err)
	}

	var sealed MatchingFallbackCalculationContext
	var sealedContext json.RawMessage
	var found bool
	if wait.CalculationRevision > 0 {
		receipt, err := getMatchingFallbackCalculationReceipt(ctx, database, runID, wait.CalculationRevision)
		if err != nil {
			return result, false, fmt.Errorf("build fallback seal authority: validate selected calculation: %w", err)
		}
		if receipt == nil || receipt.StageIndex != wait.StageIndex {
			return result, false, fmt.Errorf("build fallback seal authority: wait calculation reference is missing or inconsistent")
		}
		if err := json.Unmarshal(receipt.Context, &sealed); err != nil {
			return result, false, fmt.Errorf("build fallback seal authority: decode selected calculation context: %w", err)
		}
		sealedContext = append(json.RawMessage(nil), receipt.Context...)
		found = true
		result.SelectedCalculation = &MatchingFallbackSealCalculationReference{
			AcquisitionRevision: receipt.AcquisitionRevision, StageIndex: receipt.StageIndex,
			SchemaVersion: receipt.SchemaVersion, CalculatorVersion: receipt.CalculatorVersion,
			DigestVersion: receipt.DigestVersion, InputDigest: receipt.InputDigest,
		}
	}
	if wait.CalculationRevision < 0 || wait.CalculationRevision >= revision {
		return result, false, fmt.Errorf("build fallback seal authority: wait calculation revision is invalid")
	}

	if !found {
		intent, target, sourceID, providerID, endpoint, runConfiguration, configuration, err := loadMatchingFallbackSealFrozenRun(ctx, database, runID)
		if err != nil {
			return result, false, err
		}
		sealed.Intent, sealed.Target = intent, target
		sealed.RunSourceID, sealed.RunProviderID = sourceID, providerID
		sealed.RunEndpoint, sealed.RunConfiguration = endpoint, configuration
		result.RunProviderConfiguration = runConfiguration
	} else {
		// A receipt-backed qualified/exhausted authority must carry the same
		// frozen provider identity as the immutable run. Read only matching_run:
		// never the current provider_source registry, which a later source
		// configuration change can rewrite.
		runSourceID, runConfiguration, err := loadMatchingFallbackSealFrozenRunSource(ctx, database, runID)
		if err != nil {
			return result, false, err
		}
		if runSourceID != sealed.RunSourceID || runConfiguration != sealed.RunConfiguration {
			return result, false, fmt.Errorf("build fallback seal authority: selected calculation source authority differs from the frozen run")
		}
		result.RunProviderConfiguration = runConfiguration
	}
	var plan json.RawMessage
	if len(sealed.Intent.FallbackQueryPlan) != 0 {
		canonical, err := policy.CanonicalFallbackQueryPlan(sealed.Intent.FallbackQueryPlan)
		if err != nil {
			return result, false, fmt.Errorf("build fallback seal authority: frozen plan is invalid: %w", err)
		}
		plan = canonical
	} else if found {
		return result, false, fmt.Errorf("build fallback seal authority: frozen plan is missing")
	}
	var typedPlan policy.TypedFallbackQueryPlan
	if len(plan) != 0 {
		if err := json.Unmarshal(plan, &typedPlan); err != nil {
			return result, false, fmt.Errorf("build fallback seal authority: decode frozen plan: %w", err)
		}
	}
	if !found {
		var registrations, dependencies int
		if err := database.NewRaw(`SELECT
			(SELECT count(*) FROM matching_fallback_request_registration WHERE run_id=?),
			(SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=?)`, runID, runID).
			Scan(ctx, &registrations, &dependencies); err != nil {
			return result, false, fmt.Errorf("build fallback seal authority: verify empty plan children: %w", err)
		}
		if len(typedPlan.Stages) != 0 || len(sealed.Intent.InitialProviderInputs) != 0 || registrations != 0 || dependencies != 0 {
			return result, false, fmt.Errorf("build fallback seal authority: no-query wait has provider work")
		}
		if waitOutput.Reason != "fallback_plan_empty" {
			return result, false, fmt.Errorf("build fallback seal authority: no-query wait reason is invalid")
		}
		result.Disposition = "no_query"
	} else {
		edges := make([]struct {
			Confidence float64 `json:"confidence"`
		}, 0)
		var rawEdges string
		if err := database.NewRaw(`SELECT edge_calculations::text FROM matching_fallback_calculation_receipt WHERE run_id=? AND acquisition_revision=?`, runID, wait.CalculationRevision).Scan(ctx, &rawEdges); err != nil {
			return result, false, fmt.Errorf("build fallback seal authority: read selected edges: %w", err)
		}
		if err := json.Unmarshal([]byte(rawEdges), &edges); err != nil {
			return result, false, fmt.Errorf("build fallback seal authority: selected edges are invalid: %w", err)
		}
		qualified := false
		for _, edge := range edges {
			if edge.Confidence >= 0.70 {
				qualified = true
			}
		}
		if qualified {
			if phase != "pending_seal" || waitOutput.Reason != "qualified_candidate" {
				return result, false, fmt.Errorf("build fallback seal authority: qualified calculation does not match wait reason")
			}
			result.Disposition = "qualified"
		} else {
			if phase != "fallback_review_wait" || waitOutput.Reason != "review_required" {
				return result, false, fmt.Errorf("build fallback seal authority: exhausted calculation does not match wait reason")
			}
			result.Disposition = "review_exhausted"
		}
	}

	// History is ordered by the immutable calculation revision. Each reference
	// is revalidated by the receipt getter, preserving its stored format tuple.
	var revisions []int64
	if err := database.NewRaw(`SELECT acquisition_revision FROM matching_fallback_calculation_receipt WHERE run_id=? AND acquisition_revision<? ORDER BY acquisition_revision`, runID, revision).Scan(ctx, &revisions); err != nil {
		return result, false, fmt.Errorf("build fallback seal authority: list calculation history: %w", err)
	}
	result.ProofHistory = make([]MatchingFallbackSealCalculationReference, 0, len(revisions))
	if !found && len(revisions) != 0 {
		return result, false, fmt.Errorf("build fallback seal authority: no-query wait has calculation history")
	}
	if found && result.Disposition == "review_exhausted" && len(typedPlan.Stages) != len(revisions) {
		return result, false, fmt.Errorf("build fallback seal authority: exhausted wait does not cover the finite plan")
	}
	for _, receiptRevision := range revisions {
		receipt, err := getMatchingFallbackCalculationReceipt(ctx, database, runID, receiptRevision)
		if err != nil || receipt == nil {
			if err == nil {
				err = fmt.Errorf("receipt disappeared from snapshot")
			}
			return result, false, fmt.Errorf("build fallback seal authority: validate proof history: %w", err)
		}
		result.ProofHistory = append(result.ProofHistory, MatchingFallbackSealCalculationReference{
			AcquisitionRevision: receipt.AcquisitionRevision, StageIndex: receipt.StageIndex,
			SchemaVersion: receipt.SchemaVersion, CalculatorVersion: receipt.CalculatorVersion,
			DigestVersion: receipt.DigestVersion, InputDigest: receipt.InputDigest,
		})
		if result.Disposition == "review_exhausted" && receipt.StageIndex != len(result.ProofHistory)-1 {
			return result, false, fmt.Errorf("build fallback seal authority: exhausted proof history has a stage gap")
		}
		if result.Disposition == "review_exhausted" {
			var historyEdges []struct {
				Confidence float64 `json:"confidence"`
			}
			if err := json.Unmarshal(receipt.Edges, &historyEdges); err != nil {
				return result, false, fmt.Errorf("build fallback seal authority: exhausted proof edges are invalid: %w", err)
			}
			for _, edge := range historyEdges {
				if edge.Confidence >= 0.70 {
					return result, false, fmt.Errorf("build fallback seal authority: exhausted history contains a qualifying edge")
				}
			}
		}
	}
	// An exhausted wait must select the final ordered receipt: every stage of the
	// saved finite plan, exactly, with the last stage's full accepted context.
	// Reject a duplicate or older selection before the evaluation context is
	// chosen so an earlier stage layout cannot be substituted.
	if result.Disposition == "review_exhausted" {
		if len(typedPlan.Stages) == 0 || len(result.ProofHistory) != len(typedPlan.Stages) {
			return result, false, fmt.Errorf("build fallback seal authority: exhausted wait does not cover the finite plan")
		}
		selected, final := result.SelectedCalculation, result.ProofHistory[len(result.ProofHistory)-1]
		if selected == nil || selected.AcquisitionRevision != final.AcquisitionRevision ||
			selected.StageIndex != final.StageIndex || selected.StageIndex != len(typedPlan.Stages)-1 ||
			selected.SchemaVersion != final.SchemaVersion || selected.CalculatorVersion != final.CalculatorVersion ||
			selected.DigestVersion != final.DigestVersion || selected.InputDigest != final.InputDigest {
			return result, false, fmt.Errorf("build fallback seal authority: exhausted wait selected an earlier calculation")
		}
	}

	result.SchemaVersion = matchingFallbackSealAuthoritySchemaVersion
	result.RunID, result.WaitRevision = runID, revision
	result.Intent = sealed.Intent
	result.Target = sealed.Target
	result.CapturedMetadata = append([]MatchingAcquisitionCapturedMember(nil), sealed.Intent.CapturedMembers...)
	result.SourceID, result.ProviderID = sealed.RunSourceID, sealed.RunProviderID
	result.Endpoint, result.SourceConfiguration = sealed.RunEndpoint, sealed.RunConfiguration
	result.Plan = plan
	if found {
		contextBytes, err := canonicalMatchingJSON(sealedContext)
		if err != nil {
			return result, false, fmt.Errorf("build fallback seal authority: canonicalize full evaluation context: %w", err)
		}
		result.FullEvaluationContext = contextBytes
	}
	if result.Target.GroupID != uuid.Nil {
		var authorityRevision int64
		var capture struct {
			AuthorityRevision int64 `json:"authority_revision"`
		}
		if json.Unmarshal(result.Target.Capture, &capture) != nil {
			return result, false, fmt.Errorf("build fallback seal authority: target authority capture is invalid")
		}
		authorityRevision = capture.AuthorityRevision
		result.GroupAuthority = IncomingGroupMatchingAuthority{GroupID: result.Target.GroupID, Mode: "automatic", Revision: authorityRevision}
	}
	canonical, err := canonicalMatchingFallbackSealAuthorityBytes(result)
	if err != nil {
		return result, false, fmt.Errorf("build fallback seal authority: canonicalize authority: %w", err)
	}
	digest := sha256.Sum256(canonical)
	result.Digest = hex.EncodeToString(digest[:])
	return result, true, nil
}

func loadMatchingFallbackSealFrozenRun(
	ctx context.Context, database bun.IDB, runID uuid.UUID,
) (MatchingAcquisitionIntent, MatchingRunFenceSnapshot, uuid.UUID, uuid.UUID, string, string, string, error) {
	var intent MatchingAcquisitionIntent
	var target MatchingRunFenceSnapshot
	var sourceID, providerID uuid.UUID
	var endpoint, runConfiguration, configuration string
	var intentBytes, intentDigest, planDigest, planBytes []byte
	var intentVersion int
	err := database.NewRaw(`SELECT acquisition.intent,acquisition.intent_digest,acquisition.intent_version,
		run.plan_digest,run.immutable_plan,run.provider_source_id,run.provider_configuration_identity
		FROM matching_acquisition acquisition JOIN matching_run run ON run.id=acquisition.run_id
		WHERE run.id=?`, runID).
		Scan(ctx, &intentBytes, &intentDigest, &intentVersion, &planDigest, &planBytes,
			&sourceID, &runConfiguration)
	if err != nil {
		return intent, target, sourceID, providerID, endpoint, runConfiguration, configuration, fmt.Errorf("build fallback seal authority: read frozen run: %w", err)
	}
	if json.Unmarshal(intentBytes, &intent) != nil || intent.SchemaVersion != intentVersion ||
		validateCanonicalMatchingAcquisitionIntent(intent, intentDigest, planDigest, planBytes) != nil {
		return intent, target, sourceID, providerID, endpoint, runConfiguration, configuration, fmt.Errorf("build fallback seal authority: frozen intent or plan is invalid")
	}
	if intent.FallbackProviderSource == nil || intent.FallbackProviderSource.ID != sourceID ||
		intent.FallbackProviderSource.ConfigurationIdentity != runConfiguration ||
		intent.FallbackProviderSource.ProviderID == uuid.Nil || intent.FallbackProviderSource.Endpoint == "" ||
		intent.FallbackProviderSource.ConfigurationIdentity == "" {
		return intent, target, sourceID, providerID, endpoint, runConfiguration, configuration, fmt.Errorf("build fallback seal authority: immutable provider source descriptor is missing or inconsistent")
	}
	providerID = intent.FallbackProviderSource.ProviderID
	endpoint = intent.FallbackProviderSource.Endpoint
	configuration = intent.FallbackProviderSource.ConfigurationIdentity
	var groupID uuid.UUID
	var groupRevision string
	var capture json.RawMessage
	var captureDigest []byte
	if err := database.NewRaw(`SELECT group_id,captured_group_revision,target_capture,target_capture_digest FROM matching_run_target WHERE run_id=?`, runID).
		Scan(ctx, &groupID, &groupRevision, &capture, &captureDigest); err != nil {
		return intent, target, sourceID, providerID, endpoint, runConfiguration, configuration, fmt.Errorf("build fallback seal authority: read frozen target: %w", err)
	}
	canonicalCapture, digest, err := automaticArtistTargetCaptureDigest(capture)
	if err != nil || !equalDigest(digest[:], captureDigest) || hex.EncodeToString(digest[:]) != intent.TargetCaptureDigest {
		return intent, target, sourceID, providerID, endpoint, runConfiguration, configuration, fmt.Errorf("build fallback seal authority: frozen target capture is invalid")
	}
	target = MatchingRunFenceSnapshot{Version: 2, GroupID: groupID, GroupRevision: groupRevision,
		Capture: canonicalCapture, CaptureDigest: hex.EncodeToString(digest[:])}
	return intent, target, sourceID, providerID, endpoint, runConfiguration, configuration, nil
}

// loadMatchingFallbackSealFrozenRunSource reads only provider identity captured
// on matching_run. The provider_source row is mutable, so receipt-backed seal
// authority must not substitute its current identity for the run's snapshot.
func loadMatchingFallbackSealFrozenRunSource(
	ctx context.Context, database bun.IDB, runID uuid.UUID,
) (uuid.UUID, string, error) {
	var sourceID uuid.UUID
	var configuration string
	err := database.NewRaw(`SELECT provider_source_id,provider_configuration_identity FROM matching_run WHERE id=?`, runID).
		Scan(ctx, &sourceID, &configuration)
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("build fallback seal authority: read frozen run source: %w", err)
	}
	if sourceID == uuid.Nil || configuration == "" {
		return uuid.Nil, "", fmt.Errorf("build fallback seal authority: frozen run source identity is invalid")
	}
	return sourceID, configuration, nil
}

func canonicalMatchingFallbackSealAuthorityBytes(authority MatchingFallbackSealAuthority) ([]byte, error) {
	authority.Digest = ""
	encoded, err := json.Marshal(authority)
	if err != nil {
		return nil, err
	}
	return canonicalMatchingJSON(encoded)
}
