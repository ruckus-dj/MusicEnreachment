package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/policy"
)

// MatchingFallbackApplicabilityInput is the immutable evidence boundary for the
// post-hydration fallback calculator. It intentionally carries no repositories,
// provider clients, settings, or filesystem-derived readers.
type MatchingFallbackApplicabilityInput struct {
	RunID                uuid.UUID
	AcquisitionRevision  int64
	StageIndex           int
	Intent               persistence.MatchingAcquisitionIntent
	Target               persistence.MatchingRunFenceSnapshot
	PhaseInput           json.RawMessage
	PhaseOutput          json.RawMessage
	PhaseProvenance      json.RawMessage
	SearchReceipt        persistence.MatchingFallbackSearchReceipt
	LookupReceipts       []persistence.MatchingFallbackLookupReceipt
	Authority            MatchingFallbackAuthority
	legacySearchEvidence *persistence.MatchingFallbackVerifiedLegacySearchEvidence
}

// MatchingFallbackAuthority is the immutable provider/run and registered-request
// scope supplied by the persistence loader. The calculator remains repository-free.
type MatchingFallbackAuthority struct {
	RunSourceID      uuid.UUID
	RunProviderID    uuid.UUID
	RunEndpoint      string
	RunConfiguration string
	SearchKind       string
	SearchCacheKey   string
	SearchInput      json.RawMessage
	Lookups          []MatchingFallbackLookupAuthority
}

type MatchingFallbackLookupAuthority struct {
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

type MatchingFallbackCandidate struct {
	MemberID            uuid.UUID
	ReleaseID           string
	RecordingID         string
	ProviderTrackUUID   uuid.UUID
	Medium              int
	Position            int
	MatchingInput       MatchingInput
	Score               MatchingScore
	ScoreFactors        []MatchingFactor
	FrozenWorkID        uuid.UUID
	MetadataInputSHA    string
	LookupPayloadSHA    string
	LookupProjectionSHA string
}

type MatchingFallbackApplicabilityResult struct {
	SchemaVersion    int
	InputDigest      string
	ReleaseLayouts   []persistence.MatchingRelease
	AllCandidates    []MatchingFallbackCandidate
	Candidates       []MatchingFallbackCandidate
	EligibleProfiles []string
	QualifyingEdges  int
}

type fallbackCapture struct {
	SchemaVersion     int       `json:"schema_version"`
	GroupID           uuid.UUID `json:"group_id"`
	GroupRevision     string    `json:"group_revision"`
	AuthorityRevision int64     `json:"authority_revision"`
	Members           []struct {
		VariantID      uuid.UUID                                    `json:"variant_id"`
		Ready          bool                                         `json:"ready"`
		Locations      []persistence.IncomingGroupingMemberLocation `json:"locations"`
		Captures       []persistence.IncomingGroupingCapture        `json:"captures"`
		AnalysisInputs []struct {
			AnalysisRecordID string          `json:"analysis_record_id"`
			WorkID           uuid.UUID       `json:"work_id"`
			Step             string          `json:"step"`
			InputSnapshot    json.RawMessage `json:"input_snapshot"`
			InputDigest      *string         `json:"input_digest"`
			ResultReference  *uuid.UUID      `json:"result_reference"`
		} `json:"analysis_inputs"`
	} `json:"members"`
	analysisStates map[string][]string
}

// UnmarshalJSON validates and reads the complete persisted target-capture
// schema. Locations and analysis state are authority-bearing persisted fields,
// even though applicability consumes only the capture and selected metadata.
func (capture *fallbackCapture) UnmarshalJSON(raw []byte) error {
	type analysisInput struct {
		AnalysisRecordID string          `json:"analysis_record_id"`
		WorkID           uuid.UUID       `json:"work_id"`
		Step             string          `json:"step"`
		State            string          `json:"state"`
		InputSnapshot    json.RawMessage `json:"input_snapshot"`
		InputDigest      *string         `json:"input_digest"`
		ResultReference  *uuid.UUID      `json:"result_reference"`
	}
	type member struct {
		VariantID      uuid.UUID                                    `json:"variant_id"`
		Ready          bool                                         `json:"ready"`
		Locations      []persistence.IncomingGroupingMemberLocation `json:"locations"`
		Captures       []persistence.IncomingGroupingCapture        `json:"captures"`
		AnalysisInputs []analysisInput                              `json:"analysis_inputs"`
	}
	var persisted struct {
		SchemaVersion     int       `json:"schema_version"`
		GroupID           uuid.UUID `json:"group_id"`
		GroupRevision     string    `json:"group_revision"`
		AuthorityRevision int64     `json:"authority_revision"`
		Members           []member  `json:"members"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&persisted); err != nil {
		return err
	}
	*capture = fallbackCapture{SchemaVersion: persisted.SchemaVersion, GroupID: persisted.GroupID, GroupRevision: persisted.GroupRevision, AuthorityRevision: persisted.AuthorityRevision}
	capture.analysisStates = make(map[string][]string)
	for _, saved := range persisted.Members {
		var inputs []struct {
			AnalysisRecordID string          `json:"analysis_record_id"`
			WorkID           uuid.UUID       `json:"work_id"`
			Step             string          `json:"step"`
			InputSnapshot    json.RawMessage `json:"input_snapshot"`
			InputDigest      *string         `json:"input_digest"`
			ResultReference  *uuid.UUID      `json:"result_reference"`
		}
		for _, input := range saved.AnalysisInputs {
			key := saved.VariantID.String() + ":" + input.AnalysisRecordID
			capture.analysisStates[key] = append(capture.analysisStates[key], input.State)
			inputs = append(inputs, struct {
				AnalysisRecordID string          `json:"analysis_record_id"`
				WorkID           uuid.UUID       `json:"work_id"`
				Step             string          `json:"step"`
				InputSnapshot    json.RawMessage `json:"input_snapshot"`
				InputDigest      *string         `json:"input_digest"`
				ResultReference  *uuid.UUID      `json:"result_reference"`
			}{input.AnalysisRecordID, input.WorkID, input.Step, input.InputSnapshot, input.InputDigest, input.ResultReference})
		}
		capture.Members = append(capture.Members, struct {
			VariantID      uuid.UUID                                    `json:"variant_id"`
			Ready          bool                                         `json:"ready"`
			Locations      []persistence.IncomingGroupingMemberLocation `json:"locations"`
			Captures       []persistence.IncomingGroupingCapture        `json:"captures"`
			AnalysisInputs []struct {
				AnalysisRecordID string          `json:"analysis_record_id"`
				WorkID           uuid.UUID       `json:"work_id"`
				Step             string          `json:"step"`
				InputSnapshot    json.RawMessage `json:"input_snapshot"`
				InputDigest      *string         `json:"input_digest"`
				ResultReference  *uuid.UUID      `json:"result_reference"`
			} `json:"analysis_inputs"`
		}{saved.VariantID, saved.Ready, saved.Locations, saved.Captures, inputs})
	}
	return nil
}

type fallbackHydrationInput struct {
	StageIndex              int             `json:"stage_index"`
	SearchRequestID         uuid.UUID       `json:"search_request_id"`
	OriginHydrationRevision int64           `json:"origin_hydration_revision"`
	ReleaseIDs              []string        `json:"release_ids"`
	SearchManifest          json.RawMessage `json:"search_manifest"`
}

type fallbackHydrationOutput struct {
	StageIndex              int       `json:"stage_index"`
	SearchRequestID         uuid.UUID `json:"search_request_id"`
	OriginHydrationRevision int64     `json:"origin_hydration_revision"`
	Lookups                 []struct {
		ReleaseID string    `json:"release_id"`
		RequestID uuid.UUID `json:"request_id"`
		CacheKey  string    `json:"cache_key"`
	} `json:"lookups"`
}

type fallbackSearchManifestDTO struct {
	Kind         string          `json:"kind"`
	AcceptedHits json.RawMessage `json:"accepted_hits"`
	ReleaseIDs   []string        `json:"release_ids"`
}

type fallbackResponseProvenance struct {
	RequestID  uuid.UUID `json:"request_id"`
	CacheKey   string    `json:"cache_key"`
	PayloadSHA string    `json:"payload_sha256"`
}

// CalculateFallbackApplicability scores every combined recording+release edge
// in the frozen hydrated inventory. It does not select assignments or invoke
// the final matching evaluator.
func CalculateFallbackApplicability(input MatchingFallbackApplicabilityInput) (MatchingFallbackApplicabilityResult, error) {
	if err := validateFallbackApplicabilityAuthority(input); err != nil {
		return MatchingFallbackApplicabilityResult{}, err
	}
	plan, err := policy.CanonicalFallbackQueryPlan(input.Intent.FallbackQueryPlan)
	if err != nil {
		return MatchingFallbackApplicabilityResult{}, fmt.Errorf("fallback applicability: invalid saved plan: %w", err)
	}
	var typedPlan policy.TypedFallbackQueryPlan
	if err := json.Unmarshal(plan, &typedPlan); err != nil || input.StageIndex < 0 || input.StageIndex >= len(typedPlan.Stages) {
		return MatchingFallbackApplicabilityResult{}, fmt.Errorf("fallback applicability: saved plan has no registered stage")
	}
	stage := typedPlan.Stages[input.StageIndex]
	initialStage := typedPlan.Stages[0]
	initialKind, initialPrefix, initialOK := policy.ProviderKindForFallbackStage(initialStage.Kind)
	var immutableInitialInput struct {
		SchemaVersion int    `json:"schema_version"`
		Query         string `json:"query"`
		Offset        int    `json:"offset"`
		Limit         int    `json:"limit"`
	}
	if !initialOK || len(input.Intent.InitialProviderInputs) != 1 ||
		input.Intent.InitialProviderInputs[0].Kind != initialKind ||
		decodeFallbackStrict(input.Intent.InitialProviderInputs[0].Input, &immutableInitialInput) != nil ||
		immutableInitialInput.SchemaVersion != 1 || immutableInitialInput.Query != initialStage.Query ||
		immutableInitialInput.Offset != initialStage.Offset || immutableInitialInput.Limit != initialStage.Limit ||
		input.Intent.InitialProviderInputs[0].CacheKey != fmt.Sprintf("%s:%d:%d:%s", initialPrefix, initialStage.Offset, initialStage.Limit, initialStage.Query) {
		return MatchingFallbackApplicabilityResult{}, fmt.Errorf("fallback applicability: immutable initial search intent is invalid")
	}
	providerKind, cachePrefix, ok := policy.ProviderKindForFallbackStage(stage.Kind)
	var initialQuery struct {
		SchemaVersion int    `json:"schema_version"`
		Query         string `json:"query"`
		Offset        int    `json:"offset"`
		Limit         int    `json:"limit"`
	}
	if !ok || decodeFallbackStrict(input.Authority.SearchInput, &initialQuery) != nil ||
		initialQuery.SchemaVersion != 1 || initialQuery.Query != stage.Query || initialQuery.Offset != stage.Offset || initialQuery.Limit != stage.Limit ||
		input.Authority.SearchKind != providerKind ||
		input.Authority.SearchCacheKey != fmt.Sprintf("%s:%d:%d:%s", cachePrefix, stage.Offset, stage.Limit, stage.Query) {
		return MatchingFallbackApplicabilityResult{}, fmt.Errorf("fallback applicability: stage does not match the accepted search request")
	}
	var phaseIn fallbackHydrationInput
	var phaseOut fallbackHydrationOutput
	if decodeFallbackStrict(input.PhaseInput, &phaseIn) != nil || decodeFallbackStrict(input.PhaseOutput, &phaseOut) != nil ||
		phaseIn.StageIndex != input.StageIndex || phaseOut.StageIndex != input.StageIndex ||
		phaseIn.SearchRequestID != input.SearchReceipt.RequestID || phaseOut.SearchRequestID != input.SearchReceipt.RequestID ||
		phaseIn.OriginHydrationRevision < 0 || phaseIn.OriginHydrationRevision != phaseOut.OriginHydrationRevision {
		return MatchingFallbackApplicabilityResult{}, fmt.Errorf("fallback applicability: hydration phase is malformed")
	}
	var manifest fallbackSearchManifestDTO
	if decodeFallbackStrict(phaseIn.SearchManifest, &manifest) != nil || manifest.Kind != providerKind ||
		!fallbackJSONEqual(manifest.AcceptedHits, input.SearchReceipt.Payload) {
		return MatchingFallbackApplicabilityResult{}, fmt.Errorf("fallback applicability: search manifest does not match accepted receipt")
	}
	var provenance []fallbackResponseProvenance
	if decodeFallbackStrict(input.PhaseProvenance, &provenance) != nil || len(provenance) != 1 ||
		provenance[0].RequestID != input.SearchReceipt.RequestID || provenance[0].CacheKey != input.SearchReceipt.CacheKey ||
		provenance[0].PayloadSHA != input.SearchReceipt.PayloadSHA256 {
		return MatchingFallbackApplicabilityResult{}, fmt.Errorf("fallback applicability: search provenance does not match accepted receipt")
	}
	searchIDs, err := acceptedSearchReleaseIDs(manifest.Kind, manifest.AcceptedHits)
	if err != nil || !sameStrings(searchIDs, manifest.ReleaseIDs) || !sameStrings(searchIDs, phaseIn.ReleaseIDs) {
		return MatchingFallbackApplicabilityResult{}, fmt.Errorf("fallback applicability: accepted release union is invalid")
	}
	if len(phaseOut.Lookups) != len(searchIDs) || len(input.LookupReceipts) != len(searchIDs) {
		return MatchingFallbackApplicabilityResult{}, fmt.Errorf("fallback applicability: lookup receipts do not cover the accepted release union")
	}
	receiptByRequest := make(map[uuid.UUID]persistence.MatchingFallbackLookupReceipt, len(input.LookupReceipts))
	for _, receipt := range input.LookupReceipts {
		if receipt.RequestID == uuid.Nil || receipt.RunID != input.RunID || receipt.StageIndex != input.StageIndex {
			return MatchingFallbackApplicabilityResult{}, fmt.Errorf("fallback applicability: lookup receipt scope is invalid")
		}
		if _, duplicate := receiptByRequest[receipt.RequestID]; duplicate {
			return MatchingFallbackApplicabilityResult{}, fmt.Errorf("fallback applicability: duplicate lookup receipt")
		}
		if err := validateReceiptDigests(2, receipt.Payload, receipt.PayloadSHA256, receipt.AcceptedProjection, receipt.ProjectionSHA256); err != nil {
			return MatchingFallbackApplicabilityResult{}, err
		}
		receiptByRequest[receipt.RequestID] = receipt
	}
	authorityByRequest := make(map[uuid.UUID]MatchingFallbackLookupAuthority, len(input.Authority.Lookups))
	for _, authority := range input.Authority.Lookups {
		if authority.RequestID == uuid.Nil || authority.Kind != "provider_release_lookup" || authority.ProviderSourceID != input.Authority.RunSourceID ||
			authority.ProviderID != input.Authority.RunProviderID || authority.Endpoint != input.Authority.RunEndpoint || authority.Configuration != input.Authority.RunConfiguration ||
			authority.ReleaseID == "" || authority.CacheKey != "release-lookup:"+authority.ReleaseID || len(authority.Input) == 0 {
			return MatchingFallbackApplicabilityResult{}, fmt.Errorf("fallback applicability: registered lookup authority is invalid")
		}
		if _, duplicate := authorityByRequest[authority.RequestID]; duplicate {
			return MatchingFallbackApplicabilityResult{}, fmt.Errorf("fallback applicability: duplicate registered lookup authority")
		}
		authorityByRequest[authority.RequestID] = authority
	}
	lookups := make(map[string]persistence.MatchingFallbackLookupReceipt, len(searchIDs))
	for i, descriptor := range phaseOut.Lookups {
		if descriptor.ReleaseID != searchIDs[i] || descriptor.CacheKey != "release-lookup:"+descriptor.ReleaseID || descriptor.RequestID == uuid.Nil {
			return MatchingFallbackApplicabilityResult{}, fmt.Errorf("fallback applicability: lookup manifest is invalid")
		}
		receipt, ok := receiptByRequest[descriptor.RequestID]
		if !ok || receipt.CacheKey != descriptor.CacheKey || receipt.PayloadSHA256 == "" || receipt.ProjectionSHA256 == "" {
			return MatchingFallbackApplicabilityResult{}, fmt.Errorf("fallback applicability: lookup receipt is missing or mismatched")
		}
		authority, registered := authorityByRequest[descriptor.RequestID]
		requestInput := json.RawMessage(fmt.Sprintf(`{"schema_version":1,"mbid":%q}`, descriptor.ReleaseID))
		if !registered || authority.ReleaseID != descriptor.ReleaseID || authority.CacheKey != descriptor.CacheKey ||
			receipt.ProviderSourceID != authority.ProviderSourceID || receipt.ConfigurationIdentity != authority.Configuration ||
			!fallbackJSONEqual(receipt.RequestInput, authority.Input) || !fallbackJSONEqual(receipt.RequestInput, requestInput) {
			return MatchingFallbackApplicabilityResult{}, fmt.Errorf("fallback applicability: lookup request is outside registered scope")
		}
		var projection persistence.ProviderMatchingSnapshot
		if err := decodeFallbackStrict(receipt.AcceptedProjection, &projection); err != nil || projection.Source.ID != receipt.ProviderSourceID || projection.Source.ConfigurationIdentity != receipt.ConfigurationIdentity ||
			receipt.ProviderSourceID != input.SearchReceipt.ProviderSourceID || receipt.ConfigurationIdentity != input.SearchReceipt.ConfigurationIdentity ||
			len(projection.Releases) != 1 || projection.Releases[0].ProviderKey != descriptor.ReleaseID ||
			projection.Source.ProviderID != input.Authority.RunProviderID || projection.Source.Endpoint != input.Authority.RunEndpoint {
			return MatchingFallbackApplicabilityResult{}, fmt.Errorf("fallback applicability: accepted lookup projection is invalid")
		}
		lookups[descriptor.ReleaseID] = receipt
	}
	if len(searchIDs) == 0 && (len(phaseOut.Lookups) != 0 || len(input.LookupReceipts) != 0) {
		return MatchingFallbackApplicabilityResult{}, fmt.Errorf("fallback applicability: empty search union has lookup receipts")
	}

	var frozen fallbackCapture
	if decodeFallbackStrict(input.Target.Capture, &frozen) != nil || frozen.SchemaVersion != 2 || frozen.GroupID != input.Target.GroupID || frozen.GroupRevision != input.Target.GroupRevision {
		return MatchingFallbackApplicabilityResult{}, fmt.Errorf("fallback applicability: frozen target capture is invalid")
	}
	canonicalTarget, err := canonicalFallbackJSON(input.Target.Capture)
	if err != nil {
		return MatchingFallbackApplicabilityResult{}, fmt.Errorf("fallback applicability: frozen target capture is invalid")
	}
	targetDigest := sha256.Sum256(canonicalTarget)
	if hex.EncodeToString(targetDigest[:]) != input.Target.CaptureDigest || len(frozen.Members) == 0 {
		return MatchingFallbackApplicabilityResult{}, fmt.Errorf("fallback applicability: frozen target digest or inventory is invalid")
	}
	captures, capturedByMember, metadataInputs, err := fallbackFrozenCaptures(frozen, input.Intent.CapturedMembers)
	if err != nil {
		return MatchingFallbackApplicabilityResult{}, err
	}

	result := MatchingFallbackApplicabilityResult{SchemaVersion: 2, AllCandidates: make([]MatchingFallbackCandidate, 0), Candidates: make([]MatchingFallbackCandidate, 0), EligibleProfiles: make([]string, 0)}
	for _, releaseID := range searchIDs {
		searchScores, err := acceptedFallbackSearchScores(input.SearchReceipt.CacheKey, manifest.Kind, manifest.AcceptedHits, []string{releaseID})
		if err != nil {
			return MatchingFallbackApplicabilityResult{}, err
		}
		receipt := lookups[releaseID]
		var snapshot persistence.ProviderMatchingSnapshot
		if err := decodeFallbackStrict(receipt.AcceptedProjection, &snapshot); err != nil {
			return MatchingFallbackApplicabilityResult{}, err
		}
		if len(snapshot.Releases) != 1 || snapshot.Releases[0].ProviderKey != releaseID {
			return MatchingFallbackApplicabilityResult{}, fmt.Errorf("fallback applicability: accepted release layout is invalid")
		}
		result.ReleaseLayouts = append(result.ReleaseLayouts, snapshot.Releases[0])
		if snapshot.SearchScores == nil {
			snapshot.SearchScores = make(map[string]map[string]persistence.MatchingSearchScore)
		}
		for key, scores := range searchScores {
			copyScores := make(map[string]persistence.MatchingSearchScore, len(scores))
			for id, score := range scores {
				copyScores[id] = score
			}
			snapshot.SearchScores[key] = copyScores
		}
		var releaseScoreKey, recordingScoreKey string
		if manifest.Kind == "provider_release_search" {
			releaseScoreKey = input.SearchReceipt.CacheKey
		} else {
			recordingScoreKey = input.SearchReceipt.CacheKey
		}
		if snapshot.Responses == nil {
			snapshot.Responses = make(map[string]*persistence.ProviderCachedResponse)
		}
		acceptedPayload := append(json.RawMessage(nil), input.SearchReceipt.Payload...)
		snapshot.Responses[input.SearchReceipt.CacheKey] = &persistence.ProviderCachedResponse{
			ProviderSourceID: input.SearchReceipt.ProviderSourceID,
			ProviderID:       snapshot.Source.ProviderID,
			CacheKey:         input.SearchReceipt.CacheKey,
			Payload:          acceptedPayload,
			Revision:         cloneFallbackString(input.SearchReceipt.CacheRevision),
			Generation:       input.SearchReceipt.CacheGeneration,
			FetchOrder:       input.SearchReceipt.FetchOrder,
		}
		keys := make(map[uuid.UUID]MatchingSearchKeys, len(captures))
		for _, capture := range captures {
			keys[capture.VariantID] = MatchingSearchKeys{Release: releaseScoreKey, Recording: recordingScoreKey}
		}
		inputs, err := BuildMatchingInputs(captures, snapshot, keys, nil)
		if err != nil {
			return MatchingFallbackApplicabilityResult{}, fmt.Errorf("fallback applicability: build matching inputs: %w", err)
		}
		for _, matchingInput := range inputs {
			score := matchingInput.AssignmentScore
			trackID, err := trackUUIDFor(snapshot.Releases, matchingInput.ReleaseID, matchingInput.Medium, matchingInput.Track)
			if err != nil {
				return MatchingFallbackApplicabilityResult{}, err
			}
			member := capturedByMember[matchingInput.FileID]
			candidate := MatchingFallbackCandidate{
				MemberID: matchingInput.FileID, ReleaseID: matchingInput.ReleaseID, RecordingID: matchingInput.RecordingID,
				ProviderTrackUUID: trackID,
				Medium:            matchingInput.Medium, Position: matchingInput.Track, MatchingInput: matchingInput, Score: score,
				ScoreFactors: append([]MatchingFactor(nil), score.Factors...), FrozenWorkID: member.WorkID,
				MetadataInputSHA: metadataInputs[matchingInput.FileID], LookupPayloadSHA: receipt.PayloadSHA256,
				LookupProjectionSHA: receipt.ProjectionSHA256,
			}
			result.AllCandidates = append(result.AllCandidates, candidate)
			if fallbackScoreQualifies(score.Confidence) {
				result.Candidates = append(result.Candidates, candidate)
			}
		}
	}
	sort.Slice(result.AllCandidates, func(i, j int) bool {
		a, b := result.AllCandidates[i], result.AllCandidates[j]
		if a.MemberID != b.MemberID {
			return a.MemberID.String() < b.MemberID.String()
		}
		if a.ReleaseID != b.ReleaseID {
			return a.ReleaseID < b.ReleaseID
		}
		if a.Medium != b.Medium {
			return a.Medium < b.Medium
		}
		if a.Position != b.Position {
			return a.Position < b.Position
		}
		return a.RecordingID < b.RecordingID
	})
	sort.Slice(result.Candidates, func(i, j int) bool {
		a, b := result.Candidates[i], result.Candidates[j]
		if a.MemberID != b.MemberID {
			return a.MemberID.String() < b.MemberID.String()
		}
		if a.ReleaseID != b.ReleaseID {
			return a.ReleaseID < b.ReleaseID
		}
		if a.Medium != b.Medium {
			return a.Medium < b.Medium
		}
		if a.Position != b.Position {
			return a.Position < b.Position
		}
		return a.RecordingID < b.RecordingID
	})
	if len(result.Candidates) > 0 {
		result.EligibleProfiles = []string{"recording_release"}
	}
	result.QualifyingEdges = len(result.Candidates)
	result.InputDigest, err = fallbackApplicabilityInputDigest(input)
	if err != nil {
		return MatchingFallbackApplicabilityResult{}, err
	}
	return result, nil
}

func validateFallbackApplicabilityAuthority(input MatchingFallbackApplicabilityInput) error {
	if input.RunID == uuid.Nil || input.AcquisitionRevision < 1 || input.StageIndex < 0 || input.Intent.SchemaVersion != 3 || input.Intent.FenceVersion != 2 ||
		input.Target.Version != 2 || input.Target.GroupID == uuid.Nil || input.Target.GroupRevision == "" || len(input.Target.Capture) == 0 ||
		input.Intent.TargetCaptureDigest == "" || input.Target.CaptureDigest != input.Intent.TargetCaptureDigest || len(input.Intent.Targets) != 1 ||
		input.Intent.Targets[0] != (persistence.MatchingTarget{GroupID: input.Target.GroupID, Revision: input.Target.GroupRevision}) {
		return fmt.Errorf("fallback applicability: run, intent, or frozen target authority is invalid")
	}
	if input.SearchReceipt.RunID != input.RunID || input.SearchReceipt.StageIndex != input.StageIndex || input.SearchReceipt.RequestID == uuid.Nil ||
		input.SearchReceipt.ProviderSourceID == uuid.Nil || input.SearchReceipt.ConfigurationIdentity == "" ||
		input.Authority.SearchCacheKey != input.SearchReceipt.CacheKey {
		return fmt.Errorf("fallback applicability: accepted search receipt is outside intent scope")
	}
	if input.Authority.RunSourceID == uuid.Nil || input.Authority.RunProviderID == uuid.Nil || input.Authority.RunEndpoint == "" ||
		input.Authority.RunSourceID != input.SearchReceipt.ProviderSourceID || input.Authority.RunConfiguration != input.SearchReceipt.ConfigurationIdentity ||
		len(input.SearchReceipt.RequestInput) == 0 || !fallbackJSONEqual(input.SearchReceipt.RequestInput, input.Authority.SearchInput) ||
		len(input.Authority.Lookups) != len(input.LookupReceipts) {
		return fmt.Errorf("fallback applicability: provider request authority is incomplete or mismatched")
	}
	if input.legacySearchEvidence != nil {
		if err := persistence.ValidateMatchingFallbackLegacySearchPayload(*input.legacySearchEvidence, input.SearchReceipt); err != nil {
			return fmt.Errorf("fallback applicability: historical search receipt provenance is invalid: %w", err)
		}
	} else if err := validateReceiptDigests(input.SearchReceipt.PayloadDigestVersion, input.SearchReceipt.Payload, input.SearchReceipt.PayloadSHA256, nil, ""); err != nil {
		return err
	}
	if len(input.PhaseProvenance) == 0 || !json.Valid(input.PhaseProvenance) {
		return fmt.Errorf("fallback applicability: phase provenance is required")
	}
	return nil
}

func validateReceiptDigests(payloadDigestVersion int, payload json.RawMessage, payloadDigest string, projection json.RawMessage, projectionDigest string) error {
	if len(payload) == 0 || !json.Valid(payload) || !isFallbackSHA256(payloadDigest) {
		return fmt.Errorf("fallback applicability: receipt payload is invalid")
	}
	var payloadDigestInput []byte
	switch payloadDigestVersion {
	case 0, 1:
		// Legacy payloads are checked against their exact compact bytes. If a
		// historical JSONB context round-trip changed those bytes, only the
		// persistence-validated sealed evidence path may authorize semantic hashing.
		payloadDigestInput = payload
	case 2:
		canonicalPayload, err := canonicalFallbackJSON(payload)
		if err != nil {
			return fmt.Errorf("fallback applicability: receipt payload is invalid")
		}
		payloadDigestInput = canonicalPayload
	default:
		return fmt.Errorf("fallback applicability: receipt payload digest version is unsupported")
	}
	hash := sha256.Sum256(payloadDigestInput)
	if hex.EncodeToString(hash[:]) != payloadDigest {
		return fmt.Errorf("fallback applicability: receipt payload digest changed")
	}
	if projection != nil {
		if !json.Valid(projection) || !isFallbackSHA256(projectionDigest) {
			return fmt.Errorf("fallback applicability: receipt projection is invalid")
		}
		canonicalProjection, err := canonicalFallbackJSON(projection)
		if err != nil {
			return fmt.Errorf("fallback applicability: receipt projection is invalid")
		}
		projectionHash := sha256.Sum256(canonicalProjection)
		if hex.EncodeToString(projectionHash[:]) != projectionDigest {
			return fmt.Errorf("fallback applicability: receipt projection digest changed")
		}
	}
	return nil
}

func fallbackFrozenCaptures(fallback fallbackCapture, captured []persistence.MatchingAcquisitionCapturedMember) ([]persistence.IncomingGroupingCapture, map[uuid.UUID]persistence.MatchingAcquisitionCapturedMember, map[uuid.UUID]string, error) {
	if len(fallback.Members) == 0 || len(captured) == 0 {
		return nil, nil, nil, fmt.Errorf("fallback applicability: frozen target inventory is missing")
	}
	intentMembers := make(map[uuid.UUID]persistence.MatchingAcquisitionCapturedMember, len(captured))
	for _, member := range captured {
		if member.MemberID == uuid.Nil {
			return nil, nil, nil, fmt.Errorf("fallback applicability: captured member identity is invalid")
		}
		if _, ok := intentMembers[member.MemberID]; ok {
			return nil, nil, nil, fmt.Errorf("fallback applicability: duplicate captured member")
		}
		intentMembers[member.MemberID] = member
	}
	seen := make(map[uuid.UUID]bool)
	allCaptures := make([]persistence.IncomingGroupingCapture, 0)
	byMember := make(map[uuid.UUID]persistence.MatchingAcquisitionCapturedMember)
	metadata := make(map[uuid.UUID]string)
	for _, member := range fallback.Members {
		intentMember, ok := intentMembers[member.VariantID]
		if !ok || seen[member.VariantID] || !member.Ready || len(member.Captures) == 0 {
			return nil, nil, nil, fmt.Errorf("fallback applicability: frozen member is missing, duplicated, or unready")
		}
		seen[member.VariantID] = true
		var metadataInput *struct {
			WorkID   uuid.UUID
			Digest   string
			Result   *uuid.UUID
			Snapshot json.RawMessage
			RecordID string
		}
		analysisByRecord := make(map[string]json.RawMessage)
		analysisRecordOccurrences := make(map[string]int)
		for _, analysis := range member.AnalysisInputs {
			if analysis.AnalysisRecordID == "" || analysis.WorkID == uuid.Nil || analysis.Step == "" {
				return nil, nil, nil, fmt.Errorf("fallback applicability: frozen analysis input identity is invalid")
			}
			state := ""
			stateKey := member.VariantID.String() + ":" + analysis.AnalysisRecordID
			if states := fallback.analysisStates[stateKey]; len(states) > analysisRecordOccurrences[analysis.AnalysisRecordID] {
				state = states[analysisRecordOccurrences[analysis.AnalysisRecordID]]
			}
			analysisRecordOccurrences[analysis.AnalysisRecordID]++
			encodedAnalysis, err := json.Marshal(struct {
				Analysis any
				State    string
			}{analysis, state})
			if err != nil {
				return nil, nil, nil, fmt.Errorf("fallback applicability: frozen analysis input is invalid")
			}
			if previous, exists := analysisByRecord[analysis.AnalysisRecordID]; exists {
				if !bytes.Equal(previous, encodedAnalysis) {
					return nil, nil, nil, fmt.Errorf("fallback applicability: conflicting duplicate analysis input")
				}
				continue
			}
			analysisByRecord[analysis.AnalysisRecordID] = encodedAnalysis
			if analysis.Step == "metadata" && analysis.WorkID == intentMember.WorkID {
				if metadataInput != nil {
					return nil, nil, nil, fmt.Errorf("fallback applicability: frozen metadata analysis is ambiguous")
				}
				if analysis.InputDigest == nil {
					return nil, nil, nil, fmt.Errorf("fallback applicability: frozen metadata input digest is missing")
				}
				metadataInput = &struct {
					WorkID   uuid.UUID
					Digest   string
					Result   *uuid.UUID
					Snapshot json.RawMessage
					RecordID string
				}{analysis.WorkID, *analysis.InputDigest, analysis.ResultReference, analysis.InputSnapshot, analysis.AnalysisRecordID}
			}
		}
		canonicalInput, inputErr := canonicalFallbackJSON(metadataInputSnapshot(metadataInput))
		canonicalHash := sha256.Sum256(canonicalInput)
		if metadataInput == nil || metadataInput.WorkID != intentMember.WorkID || !isFallbackSHA256(metadataInput.Digest) ||
			inputErr != nil || hex.EncodeToString(canonicalHash[:]) != metadataInput.Digest ||
			intentMember.AnalysisInputSHA256 == "" || intentMember.AnalysisInputSHA256 != metadataInput.Digest || metadataInput.RecordID == "" ||
			!fallbackJSONEqual(intentMember.Tags, member.Captures[0].Tags) {
			return nil, nil, nil, fmt.Errorf("fallback applicability: captured metadata input does not match frozen target")
		}
		for _, capture := range member.Captures {
			if !capture.Ready || capture.VariantID != member.VariantID || capture.WorkID != intentMember.WorkID ||
				capture.CaptureAnalysisID == nil || intentMember.CaptureAnalysisID == nil || *capture.CaptureAnalysisID != *intentMember.CaptureAnalysisID {
				return nil, nil, nil, fmt.Errorf("fallback applicability: frozen source capture disagrees with metadata member")
			}
			if metadataInput.Result == nil || capture.MetadataWinner == nil || *capture.MetadataWinner != *metadataInput.Result {
				return nil, nil, nil, fmt.Errorf("fallback applicability: frozen metadata result identity changed")
			}
			if !fallbackJSONEqual(intentMember.Tags, capture.Tags) {
				return nil, nil, nil, fmt.Errorf("fallback applicability: captured metadata tags changed")
			}
			allCaptures = append(allCaptures, capture)
		}
		byMember[member.VariantID] = intentMember
		metadata[member.VariantID] = metadataInput.Digest
	}
	if len(seen) != len(intentMembers) {
		return nil, nil, nil, fmt.Errorf("fallback applicability: intent members do not exactly cover frozen target")
	}
	return allCaptures, byMember, metadata, nil
}

func acceptedFallbackSearchScores(queryKey, kind string, payload json.RawMessage, releaseIDs []string) (map[string]map[string]persistence.MatchingSearchScore, error) {
	var envelope struct {
		Releases []struct {
			ID    string          `json:"id"`
			Score json.RawMessage `json:"score"`
		} `json:"releases"`
		Recordings []struct {
			ID       string          `json:"id"`
			Score    json.RawMessage `json:"score"`
			Releases []struct {
				ID string `json:"id"`
			} `json:"releases"`
		} `json:"recordings"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, fmt.Errorf("fallback applicability: malformed search response")
	}
	scores := make(map[string]map[string]persistence.MatchingSearchScore)
	put := func(id string, raw json.RawMessage) error {
		if id == "" {
			return fmt.Errorf("fallback applicability: search hit has no identity")
		}
		value := persistence.MatchingSearchScore{}
		if len(raw) != 0 && string(raw) != "null" {
			if json.Unmarshal(raw, &value.Value) != nil || math.IsNaN(value.Value) || math.IsInf(value.Value, 0) || value.Value < 0 || value.Value > 100 {
				return fmt.Errorf("fallback applicability: search score is invalid")
			}
			value.Present = true
		}
		if scores[queryKey] == nil {
			scores[queryKey] = make(map[string]persistence.MatchingSearchScore)
		}
		scores[queryKey][id] = value
		return nil
	}
	if kind == "provider_release_search" {
		allowed := make(map[string]bool, len(releaseIDs))
		for _, id := range releaseIDs {
			allowed[id] = true
		}
		for _, hit := range envelope.Releases {
			if !allowed[hit.ID] {
				continue
			}
			if err := put(hit.ID, hit.Score); err != nil {
				return nil, err
			}
		}
	} else {
		allowed := make(map[string]bool, len(releaseIDs))
		for _, id := range releaseIDs {
			allowed[id] = true
		}
		for _, hit := range envelope.Recordings {
			recording := persistence.MatchingSearchScore{}
			if len(hit.Score) != 0 && string(hit.Score) != "null" {
				if json.Unmarshal(hit.Score, &recording.Value) != nil || math.IsNaN(recording.Value) || math.IsInf(recording.Value, 0) || recording.Value < 0 || recording.Value > 100 {
					return nil, fmt.Errorf("fallback applicability: search score is invalid")
				}
				recording.Present = true
			}
			for _, release := range hit.Releases {
				if allowed[release.ID] {
					if scores[queryKey] == nil {
						scores[queryKey] = make(map[string]persistence.MatchingSearchScore)
					}
					scores[queryKey][hit.ID] = recording
				}
			}
		}
	}
	return scores, nil
}

func acceptedSearchReleaseIDs(kind string, payload json.RawMessage) ([]string, error) {
	var body struct {
		Releases []struct {
			ID string `json:"id"`
		} `json:"releases"`
		Recordings []struct {
			ID       string `json:"id"`
			Releases []struct {
				ID string `json:"id"`
			} `json:"releases"`
		} `json:"recordings"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return nil, err
	}
	set := make(map[string]bool)
	switch kind {
	case "provider_release_search":
		for _, hit := range body.Releases {
			set[hit.ID] = true
		}
	case "provider_recording_search":
		for _, hit := range body.Recordings {
			for _, release := range hit.Releases {
				set[release.ID] = true
			}
		}
	default:
		return nil, fmt.Errorf("unsupported search kind")
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		if id == "" {
			return nil, fmt.Errorf("missing release id")
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func fallbackApplicabilityInputDigest(input MatchingFallbackApplicabilityInput) (string, error) {
	input.LookupReceipts = append([]persistence.MatchingFallbackLookupReceipt(nil), input.LookupReceipts...)
	sort.Slice(input.LookupReceipts, func(i, j int) bool {
		left, right := input.LookupReceipts[i], input.LookupReceipts[j]
		if left.CacheKey != right.CacheKey {
			return left.CacheKey < right.CacheKey
		}
		return left.RequestID.String() < right.RequestID.String()
	})
	input.Authority.Lookups = append([]MatchingFallbackLookupAuthority(nil), input.Authority.Lookups...)
	sort.Slice(input.Authority.Lookups, func(i, j int) bool {
		left, right := input.Authority.Lookups[i], input.Authority.Lookups[j]
		if left.RequestID != right.RequestID {
			return left.RequestID.String() < right.RequestID.String()
		}
		if left.ReleaseID != right.ReleaseID {
			return left.ReleaseID < right.ReleaseID
		}
		leftJSON, _ := json.Marshal(left)
		rightJSON, _ := json.Marshal(right)
		return string(leftJSON) < string(rightJSON)
	})
	encoded, err := json.Marshal(struct {
		Version int                                `json:"version"`
		Input   MatchingFallbackApplicabilityInput `json:"input"`
	}{Version: 2, Input: input})
	if err != nil {
		return "", err
	}
	canonical, err := canonicalFallbackJSON(encoded)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(canonical)
	return hex.EncodeToString(hash[:]), nil
}

func trackUUIDFor(releases []persistence.MatchingRelease, releaseID string, medium, position int) (uuid.UUID, error) {
	var selected uuid.UUID
	releaseCount := 0
	for _, release := range releases {
		if release.ProviderKey == releaseID {
			releaseCount++
			trackIDs := make(map[uuid.UUID]struct{}, len(release.Tracks))
			for _, track := range release.Tracks {
				if track.ID == uuid.Nil || track.RecordingProviderKey == "" {
					return uuid.Nil, fmt.Errorf("fallback applicability: hydrated track identity is incomplete")
				}
				if _, duplicate := trackIDs[track.ID]; duplicate {
					return uuid.Nil, fmt.Errorf("fallback applicability: hydrated track identity is ambiguous")
				}
				trackIDs[track.ID] = struct{}{}
				if track.Medium == medium && track.Position == position {
					if selected != uuid.Nil {
						return uuid.Nil, fmt.Errorf("fallback applicability: hydrated track position is ambiguous")
					}
					selected = track.ID
				}
			}
		}
	}
	if releaseCount != 1 || selected == uuid.Nil {
		return uuid.Nil, fmt.Errorf("fallback applicability: hydrated track position is missing or ambiguous")
	}
	return selected, nil
}
func decodeFallbackStrict(raw json.RawMessage, output any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(output)
}
func fallbackJSONEqual(left, right json.RawMessage) bool {
	a, err := canonicalFallbackJSON(left)
	if err != nil {
		return false
	}
	b, err := canonicalFallbackJSON(right)
	return err == nil && string(a) == string(b)
}
func metadataInputSnapshot(input *struct {
	WorkID   uuid.UUID
	Digest   string
	Result   *uuid.UUID
	Snapshot json.RawMessage
	RecordID string
}) json.RawMessage {
	if input == nil {
		return nil
	}
	return input.Snapshot
}
func canonicalFallbackJSON(raw json.RawMessage) (json.RawMessage, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("JSON has trailing data")
	}
	return json.Marshal(value)
}
func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func fallbackScoreQualifies(confidence float64) bool { return confidence >= 0.70 }

func cloneFallbackString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func isFallbackSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}
