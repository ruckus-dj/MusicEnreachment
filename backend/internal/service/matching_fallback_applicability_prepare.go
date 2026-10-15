package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type matchingFallbackCalculationContextReader interface {
	LoadMatchingFallbackCalculationContext(context.Context, uuid.UUID, int64) (persistence.MatchingFallbackCalculationContext, bool, error)
}

type matchingFallbackCalculationReceiptWriter interface {
	RecordMatchingFallbackCalculationForDelivery(context.Context, uuid.UUID, int, int64, persistence.MatchingDeliveryClaim, int64, persistence.MatchingFallbackCalculationReceipt) (bool, error)
}

type matchingFallbackSealReader interface {
	GetMatchingFallbackAuthoritySeal(context.Context, uuid.UUID, int64) (persistence.MatchingFallbackSealAuthority, bool, error)
}

// FrozenFallbackEvaluationAuthority identifies the exact persisted seal used to
// calculate the result. It is intentionally not an application UUID and cannot
// be passed to the legacy evaluation publisher.
type FrozenFallbackEvaluationAuthority struct {
	RunID                 uuid.UUID
	WaitRevision          int64
	Digest                string
	Disposition           string
	TargetFenceVersion    int
	TargetCaptureDigest   string
	TargetCapture         json.RawMessage
	Intent                persistence.MatchingAcquisitionIntent
	SourceID              uuid.UUID
	ProviderID            uuid.UUID
	Endpoint              string
	SourceConfiguration   string
	RunProviderConfig     string
	GroupAuthority        persistence.IncomingGroupMatchingAuthority
	FullEvaluationContext json.RawMessage
	RunSourceID           uuid.UUID
	RunProviderID         uuid.UUID
	RunEndpoint           string
	RunConfiguration      string
	CalculationStageIndex int
	CalculationLookups    []persistence.MatchingFallbackCalculationLookupAuthority
	CalculationReceipts   []persistence.MatchingFallbackLookupReceipt
}

// MatchingFrozenGroupInventory is the complete frozen grouping inventory used by
// the shared matching decision kernel. It carries the typed group, files and
// member fences so a caller can reuse the exact frozen inputs without regrouping
// or rereading current source state.
type MatchingFrozenGroupInventory struct {
	Group            IncomingGroup
	Files            []IncomingFile
	MemberFences     []persistence.IncomingMatchMemberFence
	CapturedMetadata []persistence.MatchingAcquisitionCapturedMember
	Captures         []persistence.IncomingGroupingCapture
}

type PreparedFrozenFallbackEvaluation struct {
	Authority       FrozenFallbackEvaluationAuthority
	Calculation     *persistence.MatchingFallbackSealCalculationReference
	Result          MatchingFallbackApplicabilityResult
	KernelResult    MatchingGroupEvaluationDecisionResult
	ReviewInventory []MatchingFallbackCandidate
	FrozenInventory MatchingFrozenGroupInventory
}

// PrepareFrozenFallbackEvaluation evaluates only the full context embedded in
// the persisted authority seal. It does not consult current provider/cache/source
// state and does not publish, enqueue, or mutate anything.
func PrepareFrozenFallbackEvaluation(
	ctx context.Context,
	reader matchingFallbackSealReader,
	runID uuid.UUID,
	waitRevision int64,
) (PreparedFrozenFallbackEvaluation, bool, error) {
	if reader == nil || runID == uuid.Nil || waitRevision < 1 {
		return PreparedFrozenFallbackEvaluation{}, false, fmt.Errorf("frozen fallback evaluation: seal reader and identity are required")
	}
	seal, exists, err := reader.GetMatchingFallbackAuthoritySeal(ctx, runID, waitRevision)
	if err != nil || !exists {
		return PreparedFrozenFallbackEvaluation{}, exists, err
	}
	if seal.SchemaVersion != 3 || seal.RunID != runID || seal.WaitRevision != waitRevision || !isFallbackSHA256(seal.Digest) {
		return PreparedFrozenFallbackEvaluation{}, false, fmt.Errorf("frozen fallback evaluation: sealed calculation context is unavailable")
	}
	sealDigest := seal.Digest
	seal.Digest = ""
	sealBytes, err := json.Marshal(seal)
	canonicalSeal, canonicalErr := canonicalFallbackJSON(sealBytes)
	actualSealDigest := sha256.Sum256(canonicalSeal)
	if err != nil || canonicalErr != nil || hex.EncodeToString(actualSealDigest[:]) != sealDigest {
		return PreparedFrozenFallbackEvaluation{}, false, fmt.Errorf("frozen fallback evaluation: seal digest is invalid")
	}
	seal.Digest = sealDigest
	if seal.SelectedCalculation == nil || len(seal.FullEvaluationContext) == 0 {
		prepared, err := prepareSealedReviewOnly(ctx, seal)
		if err != nil {
			return PreparedFrozenFallbackEvaluation{}, false, err
		}
		prepared.Authority = frozenFallbackAuthorityView(seal)
		return prepared, true, nil
	}
	ref := seal.SelectedCalculation
	if ref.AcquisitionRevision < 1 || ref.StageIndex < 0 {
		return PreparedFrozenFallbackEvaluation{}, false, fmt.Errorf("frozen fallback evaluation: selected calculation identity is invalid")
	}
	var snapshot persistence.MatchingFallbackCalculationContext
	if err := decodeFallbackStrict(seal.FullEvaluationContext, &snapshot); err != nil {
		return PreparedFrozenFallbackEvaluation{}, false, fmt.Errorf("frozen fallback evaluation: decode sealed context: %w", err)
	}
	if snapshot.RunID != runID || snapshot.AcquisitionRevision != ref.AcquisitionRevision || snapshot.StageIndex != ref.StageIndex {
		return PreparedFrozenFallbackEvaluation{}, false, fmt.Errorf("frozen fallback evaluation: selected context identity does not match seal")
	}
	var digest string
	if ref.SchemaVersion != 3 || ref.CalculatorVersion != 3 || ref.DigestVersion != 3 {
		return PreparedFrozenFallbackEvaluation{}, false, fmt.Errorf("frozen fallback evaluation: unsupported selected calculation version tuple")
	}
	digest, err = persistence.MatchingFallbackCalculationInputDigest(snapshot)
	if err != nil || digest != ref.InputDigest {
		return PreparedFrozenFallbackEvaluation{}, false, fmt.Errorf("frozen fallback evaluation: selected context digest is invalid")
	}
	if snapshot.SearchReceipt.PayloadDigestVersion != 2 {
		return PreparedFrozenFallbackEvaluation{}, false, fmt.Errorf("frozen fallback evaluation: unsupported search receipt version")
	}
	input := fallbackApplicabilityInputFromContext(snapshot)
	result, err := CalculateFallbackApplicability(input)
	if err != nil {
		return PreparedFrozenFallbackEvaluation{}, false, err
	}
	kernelResult, frozenInventory, err := evaluateFrozenFallbackInventory(ctx, snapshot.Target, snapshot.Intent.CapturedMembers, result)
	if err != nil {
		return PreparedFrozenFallbackEvaluation{}, false, err
	}
	return PreparedFrozenFallbackEvaluation{
		Authority:   frozenFallbackAuthorityView(seal),
		Calculation: ref, Result: result, KernelResult: kernelResult,
		ReviewInventory: append([]MatchingFallbackCandidate(nil), result.AllCandidates...), FrozenInventory: frozenInventory,
	}, true, nil
}

func frozenFallbackAuthorityView(seal persistence.MatchingFallbackSealAuthority) FrozenFallbackEvaluationAuthority {
	authority := FrozenFallbackEvaluationAuthority{
		RunID: seal.RunID, WaitRevision: seal.WaitRevision, Digest: seal.Digest, Disposition: seal.Disposition,
		TargetFenceVersion:  seal.Target.Version,
		TargetCaptureDigest: seal.Target.CaptureDigest, TargetCapture: append(json.RawMessage(nil), seal.Target.Capture...),
		Intent: seal.Intent, SourceID: seal.SourceID, ProviderID: seal.ProviderID, Endpoint: seal.Endpoint,
		SourceConfiguration: seal.SourceConfiguration, RunProviderConfig: seal.RunProviderConfiguration,
		RunSourceID: seal.SourceID, RunProviderID: seal.ProviderID, RunEndpoint: seal.Endpoint,
		RunConfiguration:      seal.RunProviderConfiguration,
		GroupAuthority:        seal.GroupAuthority,
		FullEvaluationContext: append(json.RawMessage(nil), seal.FullEvaluationContext...),
	}
	if len(seal.FullEvaluationContext) != 0 {
		var context persistence.MatchingFallbackCalculationContext
		if decodeFallbackStrict(seal.FullEvaluationContext, &context) == nil {
			authority.RunSourceID = context.RunSourceID
			authority.RunProviderID = context.RunProviderID
			authority.RunEndpoint = context.RunEndpoint
			authority.RunConfiguration = context.RunConfiguration
			authority.CalculationStageIndex = context.StageIndex
			authority.CalculationLookups = append([]persistence.MatchingFallbackCalculationLookupAuthority(nil), context.Lookups...)
			authority.CalculationReceipts = append([]persistence.MatchingFallbackLookupReceipt(nil), context.LookupReceipts...)
		}
	}
	return authority
}

func prepareSealedReviewOnly(ctx context.Context, seal persistence.MatchingFallbackSealAuthority) (PreparedFrozenFallbackEvaluation, error) {
	var frozen fallbackCapture
	if len(seal.Target.Capture) == 0 || decodeFallbackStrict(seal.Target.Capture, &frozen) != nil || frozen.SchemaVersion != 2 ||
		frozen.GroupID != seal.Target.GroupID || frozen.GroupRevision != seal.Target.GroupRevision {
		return PreparedFrozenFallbackEvaluation{}, fmt.Errorf("frozen fallback evaluation: sealed target capture is invalid")
	}
	canonical, err := canonicalFallbackJSON(seal.Target.Capture)
	if err != nil {
		return PreparedFrozenFallbackEvaluation{}, fmt.Errorf("frozen fallback evaluation: sealed target capture is invalid")
	}
	digest := sha256.Sum256(canonical)
	if hex.EncodeToString(digest[:]) != seal.Target.CaptureDigest || len(frozen.Members) == 0 {
		return PreparedFrozenFallbackEvaluation{}, fmt.Errorf("frozen fallback evaluation: sealed target digest or inventory is invalid")
	}
	inventory, err := frozenReviewInventory(frozen, seal.CapturedMetadata)
	if err != nil {
		return PreparedFrozenFallbackEvaluation{}, err
	}
	decision, err := EvaluateMatchingGroupFromInputs(ctx, MatchingGroupEvaluationInput{Members: inventory.MemberFences, Files: inventory.Files})
	if err != nil {
		return PreparedFrozenFallbackEvaluation{}, err
	}
	return PreparedFrozenFallbackEvaluation{KernelResult: decision, ReviewInventory: []MatchingFallbackCandidate{}, FrozenInventory: inventory}, nil
}

func evaluateFrozenFallbackInventory(ctx context.Context, target persistence.MatchingRunFenceSnapshot, captured []persistence.MatchingAcquisitionCapturedMember, result MatchingFallbackApplicabilityResult) (MatchingGroupEvaluationDecisionResult, MatchingFrozenGroupInventory, error) {
	var frozen fallbackCapture
	if decodeFallbackStrict(target.Capture, &frozen) != nil {
		return MatchingGroupEvaluationDecisionResult{}, MatchingFrozenGroupInventory{}, fmt.Errorf("frozen fallback evaluation: target capture is invalid")
	}
	inventory, err := frozenReviewInventory(frozen, captured)
	if err != nil {
		return MatchingGroupEvaluationDecisionResult{}, MatchingFrozenGroupInventory{}, err
	}
	inputs := make([]MatchingInput, 0, len(result.AllCandidates))
	for _, candidate := range result.AllCandidates {
		inputs = append(inputs, candidate.MatchingInput)
	}
	decision, err := EvaluateMatchingGroupFromInputs(ctx, MatchingGroupEvaluationInput{Members: inventory.MemberFences, Inputs: inputs, Files: inventory.Files, ReleaseLayouts: result.ReleaseLayouts})
	if err != nil {
		return MatchingGroupEvaluationDecisionResult{}, MatchingFrozenGroupInventory{}, err
	}
	return decision, inventory, nil
}

func frozenReviewInventory(frozen fallbackCapture, captured []persistence.MatchingAcquisitionCapturedMember) (MatchingFrozenGroupInventory, error) {
	capturedByID := make(map[uuid.UUID]persistence.MatchingAcquisitionCapturedMember, len(captured))
	for _, member := range captured {
		if member.MemberID == uuid.Nil || capturedByID[member.MemberID].MemberID != uuid.Nil {
			return MatchingFrozenGroupInventory{}, fmt.Errorf("frozen fallback evaluation: captured member identity is invalid")
		}
		capturedByID[member.MemberID] = member
	}
	inventory := MatchingFrozenGroupInventory{
		Group:            IncomingGroup{ID: frozen.GroupID, Revision: frozen.GroupRevision, Members: make([]uuid.UUID, 0, len(frozen.Members))},
		Files:            make([]IncomingFile, 0, len(frozen.Members)),
		MemberFences:     make([]persistence.IncomingMatchMemberFence, 0, len(frozen.Members)),
		CapturedMetadata: append([]persistence.MatchingAcquisitionCapturedMember(nil), captured...),
		Captures:         make([]persistence.IncomingGroupingCapture, 0),
	}
	for _, member := range frozen.Members {
		metadata, ok := capturedByID[member.VariantID]
		if !ok || !member.Ready || len(member.Captures) == 0 {
			return MatchingFrozenGroupInventory{}, fmt.Errorf("frozen fallback evaluation: target member metadata is incomplete")
		}
		var tags map[string][]string
		if err := json.Unmarshal(metadata.Tags, &tags); err != nil {
			return MatchingFrozenGroupInventory{}, fmt.Errorf("frozen fallback evaluation: decode captured tags: %w", err)
		}
		first := member.Captures[0]
		if first.CaptureAnalysisID == nil {
			return MatchingFrozenGroupInventory{}, fmt.Errorf("frozen fallback evaluation: capture identity is missing")
		}
		captureIdentity, err := incomingCaptureIdentity(first)
		if err != nil {
			return MatchingFrozenGroupInventory{}, err
		}
		file := IncomingFile{VariantID: member.VariantID, CaptureAnalysisID: *first.CaptureAnalysisID, CaptureIdentity: captureIdentity, Tags: tags}
		for _, location := range member.Locations {
			file.Locations = append(file.Locations, IncomingLocation{RootID: location.RootID, WorkID: location.WorkID, LocationID: location.LocationID, ConfiguredPath: location.ConfiguredPath, InventoryPath: location.InventoryPath, RelativePath: location.RelativePath, SizeBytes: location.SizeBytes, Mtime: location.Mtime.Format(time.RFC3339Nano)})
		}
		inventory.Group.Members = append(inventory.Group.Members, member.VariantID)
		inventory.Files = append(inventory.Files, file)
		inventory.MemberFences = append(inventory.MemberFences, persistence.IncomingMatchMemberFence{FileMemberID: member.VariantID, WorkID: metadata.WorkID, SourceSHA256: append([]byte(nil), first.SourceSHA256...)})
		inventory.Captures = append(inventory.Captures, member.Captures...)
	}
	if len(inventory.Files) != len(capturedByID) {
		return MatchingFrozenGroupInventory{}, fmt.Errorf("frozen fallback evaluation: captured metadata does not exactly cover target")
	}
	return inventory, nil
}

// PrepareFallbackApplicability loads one immutable persistence snapshot and
// passes an explicit service-owned conversion to the pure calculator.
func PrepareFallbackApplicability(
	ctx context.Context,
	reader matchingFallbackCalculationContextReader,
	runID uuid.UUID,
	expectedRevision int64,
) (MatchingFallbackApplicabilityResult, bool, error) {
	if reader == nil {
		return MatchingFallbackApplicabilityResult{}, false, fmt.Errorf("fallback applicability: context reader is unavailable")
	}
	snapshot, exists, err := reader.LoadMatchingFallbackCalculationContext(ctx, runID, expectedRevision)
	if err != nil || !exists {
		return MatchingFallbackApplicabilityResult{}, exists, err
	}
	input := fallbackApplicabilityInputFromContext(snapshot)
	result, err := CalculateFallbackApplicability(input)
	return result, true, err
}

// PrepareFallbackApplicabilityReceipt calculates and records stage-scoped evidence
// from the same validated persisted context. It intentionally leaves delivery
// consumption and acquisition advancement to their existing owners.
func PrepareFallbackApplicabilityReceipt(
	ctx context.Context,
	reader matchingFallbackCalculationContextReader,
	writer matchingFallbackCalculationReceiptWriter,
	runID uuid.UUID,
	attempt int,
	riverJobID int64,
	claim persistence.MatchingDeliveryClaim,
	expectedRevision int64,
) (MatchingFallbackApplicabilityResult, bool, error) {
	if reader == nil || writer == nil {
		return MatchingFallbackApplicabilityResult{}, false, fmt.Errorf("fallback applicability: receipt persistence is unavailable")
	}
	snapshot, exists, err := reader.LoadMatchingFallbackCalculationContext(ctx, runID, expectedRevision)
	if err != nil || !exists {
		return MatchingFallbackApplicabilityResult{}, exists, err
	}
	result, err := CalculateFallbackApplicability(fallbackApplicabilityInputFromContext(snapshot))
	if err != nil {
		return MatchingFallbackApplicabilityResult{}, true, err
	}
	digest, err := persistence.MatchingFallbackCalculationInputDigest(snapshot)
	if err != nil {
		return MatchingFallbackApplicabilityResult{}, true, err
	}
	contextJSON, err := json.Marshal(snapshot)
	if err != nil {
		return MatchingFallbackApplicabilityResult{}, true, fmt.Errorf("fallback applicability: serialize receipt context: %w", err)
	}
	edges := make([]matchingFallbackReceiptEdge, len(result.AllCandidates))
	for index, candidate := range result.AllCandidates {
		requestID := uuid.Nil
		for _, lookup := range snapshot.Lookups {
			if lookup.ReleaseID == candidate.ReleaseID {
				requestID = lookup.RequestID
				break
			}
		}
		if requestID == uuid.Nil {
			return MatchingFallbackApplicabilityResult{}, true, fmt.Errorf("fallback applicability: candidate lacks registered lookup authority")
		}
		edges[index] = matchingFallbackReceiptEdge{MemberID: candidate.MemberID, WorkID: candidate.FrozenWorkID,
			MetadataSHA: candidate.MetadataInputSHA, ReleaseID: candidate.ReleaseID, RecordingID: candidate.RecordingID,
			ProviderTrackID: candidate.ProviderTrackUUID, Medium: candidate.Medium, Position: candidate.Position,
			Profile: string(candidate.Score.Profile), Confidence: candidate.Score.Confidence, LookupRequestID: requestID,
			LookupPayloadSHA: candidate.LookupPayloadSHA, LookupProjectionSHA: candidate.LookupProjectionSHA}
	}
	edgesJSON, err := json.Marshal(edges)
	if err != nil {
		return MatchingFallbackApplicabilityResult{}, true, fmt.Errorf("fallback applicability: serialize receipt edges: %w", err)
	}
	recorded, err := writer.RecordMatchingFallbackCalculationForDelivery(ctx, runID, attempt, riverJobID, claim, expectedRevision,
		persistence.MatchingFallbackCalculationReceipt{RunID: runID, AcquisitionRevision: expectedRevision,
			StageIndex: snapshot.StageIndex, InputDigest: digest, Context: contextJSON, Edges: edgesJSON})
	return result, recorded, err
}

type matchingFallbackReceiptEdge struct {
	MemberID            uuid.UUID `json:"member_id"`
	WorkID              uuid.UUID `json:"work_id"`
	MetadataSHA         string    `json:"metadata_sha256"`
	ReleaseID           string    `json:"release_id"`
	RecordingID         string    `json:"recording_id"`
	ProviderTrackID     uuid.UUID `json:"provider_track_id"`
	Medium              int       `json:"medium"`
	Position            int       `json:"position"`
	Profile             string    `json:"profile"`
	Confidence          float64   `json:"confidence"`
	LookupRequestID     uuid.UUID `json:"lookup_request_id"`
	LookupPayloadSHA    string    `json:"lookup_payload_sha256"`
	LookupProjectionSHA string    `json:"lookup_projection_sha256"`
}

func fallbackApplicabilityInputFromContext(snapshot persistence.MatchingFallbackCalculationContext) MatchingFallbackApplicabilityInput {
	lookups := make([]MatchingFallbackLookupAuthority, len(snapshot.Lookups))
	for index, lookup := range snapshot.Lookups {
		lookups[index] = MatchingFallbackLookupAuthority{
			RequestID: lookup.RequestID, ReleaseID: lookup.ReleaseID, Kind: lookup.Kind,
			ProviderSourceID: lookup.ProviderSourceID, ProviderID: lookup.ProviderID,
			Endpoint: lookup.Endpoint, Configuration: lookup.Configuration,
			CacheKey: lookup.CacheKey, Input: append([]byte(nil), lookup.Input...),
		}
	}
	return MatchingFallbackApplicabilityInput{
		RunID: snapshot.RunID, AcquisitionRevision: snapshot.AcquisitionRevision, StageIndex: snapshot.StageIndex,
		Intent: snapshot.Intent, Target: snapshot.Target,
		PhaseInput: append([]byte(nil), snapshot.PhaseInput...), PhaseOutput: append([]byte(nil), snapshot.PhaseOutput...),
		PhaseProvenance: append([]byte(nil), snapshot.PhaseProvenance...), SearchReceipt: snapshot.SearchReceipt,
		LookupReceipts: snapshot.LookupReceipts,
		Authority: MatchingFallbackAuthority{
			RunSourceID: snapshot.RunSourceID, RunProviderID: snapshot.RunProviderID,
			RunEndpoint: snapshot.RunEndpoint, RunConfiguration: snapshot.RunConfiguration,
			SearchKind: snapshot.SearchKind, SearchCacheKey: snapshot.SearchCacheKey,
			SearchInput: append([]byte(nil), snapshot.SearchInput...), Lookups: lookups,
		},
	}
}
