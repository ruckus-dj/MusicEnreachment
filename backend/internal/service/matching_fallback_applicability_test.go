package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type immutableFallbackSealReader struct {
	seal persistence.MatchingFallbackSealAuthority
}

func (reader immutableFallbackSealReader) GetMatchingFallbackAuthoritySeal(context.Context, uuid.UUID, int64) (persistence.MatchingFallbackSealAuthority, bool, error) {
	return reader.seal, true, nil
}

func TestValidateReceiptDigestsCanonicalJSONB(t *testing.T) {
	acceptedPayload := json.RawMessage(`{"unknown":{"z":1,"a":1.0},"items":[{"id":"a"},{"id":"b"}]}`)
	acceptedProjection := json.RawMessage(`{"unknown_field":true,"tracks":[1,2]}`)
	payloadCanonical, err := canonicalFallbackJSON(acceptedPayload)
	if err != nil {
		t.Fatal(err)
	}
	projectionCanonical, err := canonicalFallbackJSON(acceptedProjection)
	if err != nil {
		t.Fatal(err)
	}
	payloadHash := sha256.Sum256(payloadCanonical)
	projectionHash := sha256.Sum256(projectionCanonical)
	payloadDigest := hex.EncodeToString(payloadHash[:])
	projectionDigest := hex.EncodeToString(projectionHash[:])

	// PostgreSQL jsonb may reorder object keys and normalize whitespace. Those
	// storage-level changes must not invalidate the digest of accepted content.
	persistedPayload := json.RawMessage(` { "items" : [ {"id":"a"}, {"id":"b"} ], "unknown" : { "a":1.0, "z":1 } } `)
	persistedProjection := json.RawMessage(` {"tracks" : [1, 2], "unknown_field" : true} `)
	if err := validateReceiptDigests(2, persistedPayload, payloadDigest, persistedProjection, projectionDigest); err != nil {
		t.Fatalf("canonical JSONB receipt was rejected: %v", err)
	}

	// Canonicalization is only for representation differences: it must continue
	// to reject semantic edits rather than accepting a digest of the edited data.
	tamperedPayload := json.RawMessage(`{"items":[{"id":"a"},{"id":"changed"}],"unknown":{"a":1.0,"z":1}}`)
	if err := validateReceiptDigests(2, tamperedPayload, payloadDigest, persistedProjection, projectionDigest); err == nil {
		t.Fatal("semantically changed payload matched the accepted digest")
	}
	tamperedProjection := json.RawMessage(`{"tracks":[1,3],"unknown_field":true}`)
	if err := validateReceiptDigests(2, persistedPayload, payloadDigest, tamperedProjection, projectionDigest); err == nil {
		t.Fatal("semantically changed projection matched the accepted digest")
	}
	for _, unsupportedVersion := range []int{0, 1} {
		if err := validateReceiptDigests(unsupportedVersion, persistedPayload, payloadDigest, nil, ""); err == nil {
			t.Fatalf("unsupported search payload digest version %d was accepted", unsupportedVersion)
		}
	}
}

func TestPrepareFrozenFallbackEvaluationUsesSealedCalculationContext(t *testing.T) {
	input := validFallbackApplicabilityInput(t, 12, false)
	lookups := make([]persistence.MatchingFallbackCalculationLookupAuthority, len(input.Authority.Lookups))
	for index, lookup := range input.Authority.Lookups {
		lookups[index] = persistence.MatchingFallbackCalculationLookupAuthority{
			RequestID: lookup.RequestID, ReleaseID: lookup.ReleaseID, Kind: lookup.Kind,
			ProviderSourceID: lookup.ProviderSourceID, ProviderID: lookup.ProviderID,
			Endpoint: lookup.Endpoint, Configuration: lookup.Configuration, CacheKey: lookup.CacheKey,
			Input: append(json.RawMessage(nil), lookup.Input...),
		}
	}
	snapshot := persistence.MatchingFallbackCalculationContext{
		RunID: input.RunID, AcquisitionRevision: input.AcquisitionRevision, StageIndex: 0,
		Intent: input.Intent, Target: input.Target,
		PhaseInput: input.PhaseInput, PhaseOutput: input.PhaseOutput, PhaseProvenance: input.PhaseProvenance,
		SearchReceipt: input.SearchReceipt, LookupReceipts: input.LookupReceipts,
		RunSourceID: input.Authority.RunSourceID, RunProviderID: input.Authority.RunProviderID,
		RunEndpoint: input.Authority.RunEndpoint, RunConfiguration: input.Authority.RunConfiguration,
		SearchKind: input.Authority.SearchKind, SearchCacheKey: input.Authority.SearchCacheKey,
		SearchInput: input.Authority.SearchInput, Lookups: lookups,
	}
	contextDigest, err := persistence.MatchingFallbackCalculationInputDigest(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	contextBytes, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	seal := persistence.MatchingFallbackSealAuthority{
		SchemaVersion: 3, RunID: input.RunID, WaitRevision: 2,
		SelectedCalculation: &persistence.MatchingFallbackSealCalculationReference{
			AcquisitionRevision: input.AcquisitionRevision, StageIndex: 0,
			SchemaVersion: 3, CalculatorVersion: 3, DigestVersion: 3, InputDigest: contextDigest,
		},
		FullEvaluationContext: contextBytes,
	}
	sealBytes, err := json.Marshal(seal)
	if err != nil {
		t.Fatal(err)
	}
	canonicalSeal, err := canonicalFallbackJSON(sealBytes)
	if err != nil {
		t.Fatal(err)
	}
	sealDigest := sha256.Sum256(canonicalSeal)
	seal.Digest = hex.EncodeToString(sealDigest[:])
	prepared, exists, err := PrepareFrozenFallbackEvaluation(context.Background(), immutableFallbackSealReader{seal: seal}, input.RunID, seal.WaitRevision)
	if err != nil || !exists {
		t.Fatalf("PrepareFrozenFallbackEvaluation() = exists %t, err %v", exists, err)
	}
	if prepared.Authority.RunID != input.RunID || prepared.Authority.WaitRevision != seal.WaitRevision || prepared.Authority.Digest != seal.Digest {
		t.Fatalf("prepared authority = %#v, want exact seal identity", prepared.Authority)
	}
	want, err := CalculateFallbackApplicability(input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(prepared.Result, want) {
		t.Fatalf("prepared result differs from sealed context calculation")
	}
	if len(prepared.ReviewInventory) != len(want.AllCandidates) || !reflect.DeepEqual(prepared.ReviewInventory, want.AllCandidates) {
		t.Fatalf("review inventory omitted frozen candidates: got=%d want=%d", len(prepared.ReviewInventory), len(want.AllCandidates))
	}
	if prepared.KernelResult.Decision != MatchingDecisionReview || prepared.KernelResult.Outcome != MatchingDecisionOutcomeTie ||
		len(prepared.KernelResult.AssignmentCandidates) != 0 || len(prepared.KernelResult.PositionsByFileRelease) != len(want.AllCandidates) {
		t.Fatalf("prepared shared-kernel tie result lost decision or review inventory: %+v", prepared.KernelResult)
	}
}

func TestFrozenFallbackAuthorityViewPopulatesNoQueryRunAuthority(t *testing.T) {
	seal := persistence.MatchingFallbackSealAuthority{
		SourceID: uuid.New(), ProviderID: uuid.New(), Endpoint: "https://provider.example",
		RunProviderConfiguration: "{}",
	}

	authority := frozenFallbackAuthorityView(seal)
	if authority.RunSourceID != seal.SourceID || authority.RunProviderID != seal.ProviderID ||
		authority.RunEndpoint != seal.Endpoint || authority.RunConfiguration != seal.RunProviderConfiguration {
		t.Fatalf("no-query run authority = %#v, want values copied from seal", authority)
	}
	if authority.CalculationStageIndex != 0 || len(authority.FullEvaluationContext) != 0 ||
		len(authority.CalculationLookups) != 0 || len(authority.CalculationReceipts) != 0 {
		t.Fatalf("no-query authority contains fabricated calculation data: %#v", authority)
	}
}

func TestFallbackScoreQualifiesAtExactExistingThreshold(t *testing.T) {
	for _, test := range []struct {
		confidence float64
		want       bool
	}{{0.699999, false}, {0.7, true}, {0.700001, true}} {
		if got := fallbackScoreQualifies(test.confidence); got != test.want {
			t.Errorf("fallbackScoreQualifies(%0.6f) = %t, want %t", test.confidence, got, test.want)
		}
	}
}

func TestAcceptedFallbackSearchScoresScopesRecordingScoresAndPreservesMissing(t *testing.T) {
	payload := json.RawMessage(`{"recordings":[{"id":"recording-a","score":0,"releases":[{"id":"release-a"},{"id":"release-other"}]},{"id":"recording-b","releases":[{"id":"release-a"}]}]}`)
	scores, err := acceptedFallbackSearchScores("recording-search:key", "provider_recording_search", payload, []string{"release-a"})
	if err != nil {
		t.Fatal(err)
	}
	byRecording := scores["recording-search:key"]
	if got := byRecording["recording-a"]; !got.Present || got.Value != 0 {
		t.Fatalf("explicit zero score = %#v, want present zero", got)
	}
	if got := byRecording["recording-b"]; got.Present || got.Value != 0 {
		t.Fatalf("missing score = %#v, want absent zero", got)
	}
	if _, invented := byRecording["release-other"]; invented {
		t.Fatal("nested release was incorrectly treated as a top-level scored candidate")
	}
}

func TestAcceptedSearchReleaseUnionIncludesEveryHitWithoutTenItemCap(t *testing.T) {
	var releases []string
	for index := 0; index < 12; index++ {
		releases = append(releases, fmt.Sprintf(`{"id":"release-%02d"}`, index))
	}
	ids, err := acceptedSearchReleaseIDs("provider_release_search", json.RawMessage(`{"releases":[`+joinJSON(releases)+`]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 12 {
		t.Fatalf("accepted union has %d IDs, want all 12", len(ids))
	}
}

func joinJSON(items []string) string {
	result := ""
	for index, item := range items {
		if index != 0 {
			result += ","
		}
		result += item
	}
	return result
}

func TestCalculateFallbackApplicabilityScoresWholeFrozenUnionDeterministically(t *testing.T) {
	input := validFallbackApplicabilityInput(t, 12, false)
	original, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	first, err := CalculateFallbackApplicability(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CalculateFallbackApplicability(input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) || first.SchemaVersion != 2 || first.QualifyingEdges != 12 || len(first.Candidates) != 12 || len(first.AllCandidates) != 12 || len(first.EligibleProfiles) != 1 {
		t.Fatalf("unexpected repeatable applicability: first=%#v second=%#v", first, second)
	}
	for _, candidate := range first.Candidates {
		if candidate.ProviderTrackUUID == uuid.Nil || candidate.Position != 0 || candidate.Medium != 0 || candidate.FrozenWorkID == uuid.Nil || candidate.MetadataInputSHA == "" || candidate.LookupPayloadSHA == "" || candidate.LookupProjectionSHA == "" {
			t.Fatalf("candidate omitted typed identity/evidence: %#v", candidate)
		}
	}
	after, err := json.Marshal(input)
	if err != nil || !reflect.DeepEqual(original, after) {
		t.Fatalf("calculator mutated its input: err=%v", err)
	}
}

func TestCalculateFallbackApplicabilityKeepsSharedRecordingPerReleaseProjection(t *testing.T) {
	input := validFallbackApplicabilityInput(t, 2, true)
	first, err := CalculateFallbackApplicability(input)
	if err != nil {
		t.Fatal(err)
	}
	input.LookupReceipts[0], input.LookupReceipts[1] = input.LookupReceipts[1], input.LookupReceipts[0]
	second, err := CalculateFallbackApplicability(input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) || len(first.Candidates) != 2 || first.Candidates[0].ReleaseID == first.Candidates[1].ReleaseID {
		t.Fatalf("release projections leaked across edges or depended on receipt order: %#v / %#v", first, second)
	}
	for _, candidate := range first.Candidates {
		want := "Album"
		if candidate.ReleaseID == "release-01" {
			want = "Different Album"
		}
		if got := candidate.MatchingInput.AssignmentInput.ReleaseTitle.Candidate[0]; got != want {
			t.Errorf("release %s used projection title %q, want %q", candidate.ReleaseID, got, want)
		}
	}
}

func TestCalculateFallbackApplicabilityDoesNotLeakRecordingScoreToUnreferencedHydratedRelease(t *testing.T) {
	input := validFallbackApplicabilityInput(t, 2, true)
	var payload struct {
		Recordings []struct {
			ID       string `json:"id"`
			Score    int    `json:"score"`
			Releases []struct {
				ID string `json:"id"`
			} `json:"releases"`
		} `json:"recordings"`
	}
	payload.Recordings = append(payload.Recordings, struct {
		ID       string `json:"id"`
		Score    int    `json:"score"`
		Releases []struct {
			ID string `json:"id"`
		} `json:"releases"`
	}{ID: "same-recording", Score: 99, Releases: []struct {
		ID string `json:"id"`
	}{{ID: "release-00"}}})
	payload.Recordings = append(payload.Recordings, struct {
		ID       string `json:"id"`
		Score    int    `json:"score"`
		Releases []struct {
			ID string `json:"id"`
		} `json:"releases"`
	}{ID: "other-recording", Releases: []struct {
		ID string `json:"id"`
	}{{ID: "release-01"}}})
	input.SearchReceipt.Payload, _ = json.Marshal(payload)
	input.SearchReceipt.PayloadDigestVersion = 2
	canonicalSearchPayload, err := canonicalFallbackJSON(input.SearchReceipt.Payload)
	if err != nil {
		t.Fatal(err)
	}
	searchHash := sha256.Sum256(canonicalSearchPayload)
	input.SearchReceipt.PayloadSHA256 = hex.EncodeToString(searchHash[:])
	input.Intent.InitialProviderInputs[0].Kind = "provider_recording_search"
	input.Intent.InitialProviderInputs[0].CacheKey = `recording-search:0:10:recording:"Song"`
	input.Intent.InitialProviderInputs[0].Input = json.RawMessage(`{"schema_version":1,"query":"recording:\"Song\"","offset":0,"limit":10}`)
	input.SearchReceipt.CacheKey = input.Intent.InitialProviderInputs[0].CacheKey
	input.SearchReceipt.RequestInput = input.Intent.InitialProviderInputs[0].Input
	input.Authority.SearchKind = input.Intent.InitialProviderInputs[0].Kind
	input.Authority.SearchCacheKey = input.Intent.InitialProviderInputs[0].CacheKey
	input.Authority.SearchInput = input.Intent.InitialProviderInputs[0].Input
	input.Intent.FallbackQueryPlan = json.RawMessage(`{"schema_version":1,"stages":[{"kind":"track","query":"recording:\"Song\"","offset":0,"limit":10}]}`)
	input.PhaseProvenance, _ = json.Marshal([]fallbackResponseProvenance{{RequestID: input.SearchReceipt.RequestID, CacheKey: input.SearchReceipt.CacheKey, PayloadSHA: input.SearchReceipt.PayloadSHA256}})
	var hydration fallbackHydrationInput
	_ = json.Unmarshal(input.PhaseInput, &hydration)
	var manifest fallbackSearchManifestDTO
	_ = json.Unmarshal(hydration.SearchManifest, &manifest)
	manifest.Kind, manifest.AcceptedHits = "provider_recording_search", input.SearchReceipt.Payload
	hydration.SearchManifest, _ = json.Marshal(manifest)
	input.PhaseInput, _ = json.Marshal(hydration)
	result, err := CalculateFallbackApplicability(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) != 2 {
		t.Fatalf("want both hydrated release edges, got %#v", result.Candidates)
	}
	referenced, err := acceptedFallbackSearchScores(input.SearchReceipt.CacheKey, "provider_recording_search", input.SearchReceipt.Payload, []string{"release-00"})
	if err != nil {
		t.Fatal(err)
	}
	if got := referenced[input.SearchReceipt.CacheKey]["same-recording"]; !got.Present || got.Value != 99 {
		t.Fatalf("same-recording score for its referenced release-00 = %#v, want present 99", got)
	}
	if got := referenced[input.SearchReceipt.CacheKey]["other-recording"]; got.Present || got.Value != 0 {
		t.Fatalf("other-recording does not reference release-00 but was scored: %#v", got)
	}
	unreferenced, err := acceptedFallbackSearchScores(input.SearchReceipt.CacheKey, "provider_recording_search", input.SearchReceipt.Payload, []string{"release-01"})
	if err != nil {
		t.Fatal(err)
	}
	byRecording := unreferenced[input.SearchReceipt.CacheKey]
	if _, leaked := byRecording["same-recording"]; leaked {
		t.Fatalf("same-recording score leaked to unreferenced release-01: %#v", byRecording)
	}
	if _, ok := byRecording["other-recording"]; !ok {
		t.Fatalf("other-recording reference to release-01 was dropped: %#v", byRecording)
	}
}

func TestCalculateFallbackApplicabilityDoesNotRequireGroupCoverageOrReleaseProfilePass(t *testing.T) {
	input := validFallbackApplicabilityInput(t, 1, false)
	var frozen fallbackCapture
	if err := json.Unmarshal(input.Target.Capture, &frozen); err != nil {
		t.Fatal(err)
	}
	first := frozen.Members[0]
	first.Captures[0].Tags = json.RawMessage(`{"ALBUM":["Album"],"ARTIST":["Artist"],"TITLE":["Song"]}`)
	frozen.Members[0] = first
	secondMemberID, secondWorkID, secondAnalysisID, secondMetadataID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	secondInput := json.RawMessage(`{"metadata":"unmatched"}`)
	secondInputHash := sha256.Sum256(secondInput)
	secondDigest := hex.EncodeToString(secondInputHash[:])
	frozen.Members = append(frozen.Members, struct {
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
	}{VariantID: secondMemberID, Ready: true, Captures: []persistence.IncomingGroupingCapture{{
		WorkID: secondWorkID, VariantID: secondMemberID, CaptureAnalysisID: &secondAnalysisID, MetadataWinner: &secondMetadataID,
		Tags: json.RawMessage(`{"ALBUM":["Unrelated"],"ARTIST":["Nobody"],"TITLE":["Unmatched"]}`), ProbeResult: json.RawMessage(`{}`), Ready: true,
	}}, AnalysisInputs: []struct {
		AnalysisRecordID string          `json:"analysis_record_id"`
		WorkID           uuid.UUID       `json:"work_id"`
		Step             string          `json:"step"`
		InputSnapshot    json.RawMessage `json:"input_snapshot"`
		InputDigest      *string         `json:"input_digest"`
		ResultReference  *uuid.UUID      `json:"result_reference"`
	}{{AnalysisRecordID: "metadata-2", WorkID: secondWorkID, Step: "metadata", InputSnapshot: secondInput, InputDigest: &secondDigest, ResultReference: &secondMetadataID}}})
	frozenBytes, err := json.Marshal(frozen)
	if err != nil {
		t.Fatal(err)
	}
	frozenBytes, err = canonicalFallbackJSON(frozenBytes)
	if err != nil {
		t.Fatal(err)
	}
	targetHash := sha256.Sum256(frozenBytes)
	input.Target.Capture = frozenBytes
	input.Target.CaptureDigest = hex.EncodeToString(targetHash[:])
	input.Intent.TargetCaptureDigest = input.Target.CaptureDigest
	input.Intent.CapturedMembers = append(input.Intent.CapturedMembers, persistence.MatchingAcquisitionCapturedMember{
		MemberID: secondMemberID, WorkID: secondWorkID, CaptureAnalysisID: &secondAnalysisID, AnalysisInputSHA256: secondDigest,
		Tags: json.RawMessage(`{"ALBUM":["Unrelated"],"ARTIST":["Nobody"],"TITLE":["Unmatched"]}`),
	})
	var phaseInput fallbackHydrationInput
	if err := json.Unmarshal(input.PhaseInput, &phaseInput); err != nil {
		t.Fatal(err)
	}
	input.PhaseInput, _ = json.Marshal(phaseInput)
	input.Intent.FallbackQueryPlan = json.RawMessage(`{"schema_version":1,"stages":[{"kind":"album","query":"release:\"Album\"","offset":0,"limit":10}]}`)
	input.Intent.InitialProviderInputs[0].Input = json.RawMessage(`{"schema_version":1,"query":"release:\"Album\"","offset":0,"limit":10}`)
	input.Intent.InitialProviderInputs[0].CacheKey = "release-search:0:10:release:\"Album\""
	input.SearchReceipt.CacheKey = input.Intent.InitialProviderInputs[0].CacheKey
	var provenance []fallbackResponseProvenance
	if err := json.Unmarshal(input.PhaseProvenance, &provenance); err != nil {
		t.Fatal(err)
	}
	provenance[0].CacheKey = input.SearchReceipt.CacheKey
	input.PhaseProvenance, _ = json.Marshal(provenance)
	var accepted struct {
		Releases []struct {
			ID    string          `json:"id"`
			Score json.RawMessage `json:"score"`
		} `json:"releases"`
	}
	if err := json.Unmarshal(input.SearchReceipt.Payload, &accepted); err != nil {
		t.Fatal(err)
	}
	accepted.Releases[0].Score = json.RawMessage(`95`)
	input.SearchReceipt.Payload, _ = json.Marshal(accepted)
	input.SearchReceipt.PayloadDigestVersion = 2
	canonicalSearchPayload, err := canonicalFallbackJSON(input.SearchReceipt.Payload)
	if err != nil {
		t.Fatal(err)
	}
	searchHash := sha256.Sum256(canonicalSearchPayload)
	input.SearchReceipt.PayloadSHA256 = hex.EncodeToString(searchHash[:])
	provenance[0].PayloadSHA = input.SearchReceipt.PayloadSHA256
	input.PhaseProvenance, _ = json.Marshal(provenance)
	var manifest fallbackSearchManifestDTO
	if err := json.Unmarshal(phaseInput.SearchManifest, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.AcceptedHits = input.SearchReceipt.Payload
	phaseInput.SearchManifest, _ = json.Marshal(manifest)
	input.PhaseInput, _ = json.Marshal(phaseInput)
	var projection persistence.ProviderMatchingSnapshot
	if err := json.Unmarshal(input.LookupReceipts[0].AcceptedProjection, &projection); err != nil {
		t.Fatal(err)
	}
	projection.Releases[0].Name = "Different Album"
	input.LookupReceipts[0].AcceptedProjection, _ = json.Marshal(projection)
	canonicalProjection, err := canonicalFallbackJSON(input.LookupReceipts[0].AcceptedProjection)
	if err != nil {
		t.Fatal(err)
	}
	projectionHash := sha256.Sum256(canonicalProjection)
	input.LookupReceipts[0].ProjectionSHA256 = hex.EncodeToString(projectionHash[:])
	result, err := CalculateFallbackApplicability(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.AllCandidates) != 2 || len(result.Candidates) != 1 {
		t.Fatalf("all edges including the below-threshold edge must be retained: all=%#v qualifying=%#v", result.AllCandidates, result.Candidates)
	}
	for _, candidate := range result.Candidates {
		if candidate.MemberID == first.VariantID {
			return
		}
	}
	t.Fatalf("weak release-profile evidence with a passing combined edge was incorrectly vetoed: %#v", result)
}

func TestCalculateFallbackApplicabilityAcceptsEmptyUnionButRequiresReceiptCoverage(t *testing.T) {
	input := validFallbackApplicabilityInput(t, 0, false)
	result, err := CalculateFallbackApplicability(input)
	if err != nil || result.QualifyingEdges != 0 || len(result.EligibleProfiles) != 0 {
		t.Fatalf("empty accepted union: result=%#v err=%v", result, err)
	}
	input = validFallbackApplicabilityInput(t, 1, false)
	input.LookupReceipts = nil
	if _, err := CalculateFallbackApplicability(input); err == nil {
		t.Fatal("missing accepted lookup receipt was accepted")
	}
}

func TestCalculateFallbackApplicabilityRejectsMalformedAuthorityAndEvidence(t *testing.T) {
	tests := map[string]func(*MatchingFallbackApplicabilityInput){
		"stage plan": func(input *MatchingFallbackApplicabilityInput) {
			input.Intent.FallbackQueryPlan = json.RawMessage(`{"schema_version":1,"stages":[{"kind":"album","query":"","offset":0,"limit":10}]}`)
		},
		"query cache key": func(input *MatchingFallbackApplicabilityInput) {
			input.Intent.InitialProviderInputs[0].CacheKey += "-changed"
		},
		"search payload": func(input *MatchingFallbackApplicabilityInput) {
			input.SearchReceipt.Payload = json.RawMessage(`{"releases":[]}`)
		},
		"metadata input digest": func(input *MatchingFallbackApplicabilityInput) {
			input.Intent.CapturedMembers[0].AnalysisInputSHA256 = "different"
		},
		"metadata snapshot changed with stale digest": func(input *MatchingFallbackApplicabilityInput) {
			input.Target.Capture = replaceFallbackMetadataSnapshot(t, input.Target.Capture, json.RawMessage(`{"metadata":"changed"}`))
			refreshFallbackTargetDigest(input)
		},
		"intent metadata tags changed": func(input *MatchingFallbackApplicabilityInput) {
			input.Intent.CapturedMembers[0].Tags = json.RawMessage(`{"ALBUM":["Other"],"ARTIST":["Artist"],"TITLE":["Song"]}`)
		},
		"metadata result identity": func(input *MatchingFallbackApplicabilityInput) {
			input.Target.Capture = replaceFallbackMetadataResult(t, input.Target.Capture)
		},
		"lookup payload hash": func(input *MatchingFallbackApplicabilityInput) { input.LookupReceipts[0].PayloadSHA256 = "bad" },
		"projection hash":     func(input *MatchingFallbackApplicabilityInput) { input.LookupReceipts[0].ProjectionSHA256 = "bad" },
		"foreign source": func(input *MatchingFallbackApplicabilityInput) {
			input.LookupReceipts[0].ConfigurationIdentity = "other"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			input := validFallbackApplicabilityInput(t, 1, false)
			mutate(&input)
			if _, err := CalculateFallbackApplicability(input); err == nil {
				t.Fatal("malformed evidence was accepted")
			}
		})
	}
}

func validFallbackApplicabilityInput(t *testing.T, releaseCount int, sharedRecording bool) MatchingFallbackApplicabilityInput {
	t.Helper()
	runID, groupID, memberID, workID, analysisID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	metadataResult := uuid.New()
	sourceID, providerID, requestID := uuid.New(), uuid.New(), uuid.New()
	queryKey := "release-search:0:10:release:\"Album\""
	plan := json.RawMessage(`{"schema_version":1,"stages":[{"kind":"album","query":"release:\"Album\"","offset":0,"limit":10}]}`)
	tags, _ := json.Marshal(map[string][]string{"ALBUM": {"Album"}, "ARTIST": {"Artist"}, "TITLE": {"Song"}})
	inputSnapshot := json.RawMessage(`{"metadata":"frozen"}`)
	metadataDigestBytes := sha256.Sum256(inputSnapshot)
	metadataDigest := hex.EncodeToString(metadataDigestBytes[:])
	target := fallbackCapture{SchemaVersion: 2, GroupID: groupID, GroupRevision: "revision-1"}
	target.Members = append(target.Members, struct {
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
	}{VariantID: memberID, Ready: true, Locations: []persistence.IncomingGroupingMemberLocation{{
		VariantID: memberID, RootID: uuid.New(), RelativePath: "Album/song.flac",
	}}, Captures: []persistence.IncomingGroupingCapture{{
		WorkID: workID, VariantID: memberID, CaptureAnalysisID: &analysisID, MetadataWinner: &metadataResult,
		Tags: tags, ProbeResult: json.RawMessage(`{}`), Ready: true,
	}}, AnalysisInputs: []struct {
		AnalysisRecordID string          `json:"analysis_record_id"`
		WorkID           uuid.UUID       `json:"work_id"`
		Step             string          `json:"step"`
		InputSnapshot    json.RawMessage `json:"input_snapshot"`
		InputDigest      *string         `json:"input_digest"`
		ResultReference  *uuid.UUID      `json:"result_reference"`
	}{{AnalysisRecordID: "metadata-1", WorkID: workID, Step: "metadata", InputSnapshot: inputSnapshot, InputDigest: &metadataDigest, ResultReference: &metadataResult}}})
	targetBytes, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	targetBytes, err = canonicalFallbackJSON(targetBytes)
	if err != nil {
		t.Fatal(err)
	}
	targetHash := sha256.Sum256(targetBytes)
	targetDigest := hex.EncodeToString(targetHash[:])
	payloadReleases := make([]string, releaseCount)
	projectionReceipts := make([]persistence.MatchingFallbackLookupReceipt, 0, releaseCount)
	lookups := make([]struct {
		ReleaseID string    `json:"release_id"`
		RequestID uuid.UUID `json:"request_id"`
		CacheKey  string    `json:"cache_key"`
	}, 0, releaseCount)
	for i := 0; i < releaseCount; i++ {
		releaseID := fmt.Sprintf("release-%02d", i)
		recordingID := fmt.Sprintf("recording-%02d", i)
		if sharedRecording {
			recordingID = "same-recording"
		}
		payloadReleases[i] = fmt.Sprintf(`{"id":%q,"score":95}`, releaseID)
		releaseName := "Album"
		if sharedRecording && i == 1 {
			releaseName = "Different Album"
		}
		projection, err := json.Marshal(persistence.ProviderMatchingSnapshot{Source: persistence.ProviderSource{ID: sourceID, ProviderID: providerID, Endpoint: "https://musicbrainz.example", ConfigurationIdentity: "config-1"}, Releases: []persistence.MatchingRelease{{
			ProviderKey: releaseID, Name: releaseName, Credits: []persistence.MatchingReleaseCredit{{Name: "Artist"}}, Tracks: []persistence.MatchingReleaseTrack{{ID: uuid.New(), RecordingProviderKey: recordingID, RecordingTitle: "Song", RecordingArtists: []string{"Artist"}}},
		}}})
		if err != nil {
			t.Fatal(err)
		}
		payload := json.RawMessage(`{"release":"` + releaseID + `"}`)
		canonicalPayload, err := canonicalFallbackJSON(payload)
		if err != nil {
			t.Fatal(err)
		}
		canonicalProjection, err := canonicalFallbackJSON(projection)
		if err != nil {
			t.Fatal(err)
		}
		payloadHash, projectionHash := sha256.Sum256(canonicalPayload), sha256.Sum256(canonicalProjection)
		lookupID := uuid.New()
		cacheKey := "release-lookup:" + releaseID
		requestInput := json.RawMessage(fmt.Sprintf(`{"schema_version":1,"mbid":%q}`, releaseID))
		lookups = append(lookups, struct {
			ReleaseID string    `json:"release_id"`
			RequestID uuid.UUID `json:"request_id"`
			CacheKey  string    `json:"cache_key"`
		}{releaseID, lookupID, cacheKey})
		projectionReceipts = append(projectionReceipts, persistence.MatchingFallbackLookupReceipt{
			RunID: runID, RequestID: lookupID, StageIndex: 0, ProviderSourceID: sourceID, ConfigurationIdentity: "config-1", CacheKey: cacheKey,
			RequestInput: requestInput, Payload: payload, PayloadSHA256: hex.EncodeToString(payloadHash[:]), AcceptedProjection: projection, ProjectionSHA256: hex.EncodeToString(projectionHash[:]),
		})
	}
	searchPayload, err := json.Marshal(map[string]any{"releases": json.RawMessage("[" + joinJSON(payloadReleases) + "]")})
	if err != nil {
		t.Fatal(err)
	}
	canonicalSearchPayload, err := canonicalFallbackJSON(searchPayload)
	if err != nil {
		t.Fatal(err)
	}
	searchHash := sha256.Sum256(canonicalSearchPayload)
	searchManifest, _ := json.Marshal(fallbackSearchManifestDTO{Kind: "provider_release_search", AcceptedHits: searchPayload, ReleaseIDs: func() []string {
		ids := make([]string, releaseCount)
		for i := range ids {
			ids[i] = fmt.Sprintf("release-%02d", i)
		}
		return ids
	}()})
	phaseInput, _ := json.Marshal(fallbackHydrationInput{StageIndex: 0, SearchRequestID: requestID, OriginHydrationRevision: 3, ReleaseIDs: func() []string {
		ids := make([]string, releaseCount)
		for i := range ids {
			ids[i] = fmt.Sprintf("release-%02d", i)
		}
		return ids
	}(), SearchManifest: searchManifest})
	phaseOutput, _ := json.Marshal(fallbackHydrationOutput{StageIndex: 0, SearchRequestID: requestID, OriginHydrationRevision: 3, Lookups: lookups})
	provenance, _ := json.Marshal([]fallbackResponseProvenance{{RequestID: requestID, CacheKey: queryKey, PayloadSHA: hex.EncodeToString(searchHash[:])}})
	initialInput := json.RawMessage(`{"schema_version":1,"query":"release:\"Album\"","offset":0,"limit":10}`)
	lookupAuthorities := make([]MatchingFallbackLookupAuthority, 0, len(lookups))
	for _, lookup := range lookups {
		lookupAuthorities = append(lookupAuthorities, MatchingFallbackLookupAuthority{
			RequestID: lookup.RequestID, ReleaseID: lookup.ReleaseID, Kind: "provider_release_lookup", ProviderSourceID: sourceID,
			ProviderID: providerID, Endpoint: "https://musicbrainz.example", Configuration: "config-1", CacheKey: lookup.CacheKey,
			Input: json.RawMessage(fmt.Sprintf(`{"schema_version":1,"mbid":%q}`, lookup.ReleaseID)),
		})
	}
	return MatchingFallbackApplicabilityInput{
		RunID: runID, AcquisitionRevision: 4,
		Intent: persistence.MatchingAcquisitionIntent{SchemaVersion: 3, FenceVersion: 2, TargetCaptureDigest: targetDigest,
			FallbackQueryPlan: plan, Targets: []persistence.MatchingTarget{{GroupID: groupID, Revision: "revision-1"}},
			CapturedMembers:       []persistence.MatchingAcquisitionCapturedMember{{MemberID: memberID, WorkID: workID, CaptureAnalysisID: &analysisID, AnalysisInputSHA256: metadataDigest, Tags: tags}},
			InitialProviderInputs: []persistence.MatchingAcquisitionProviderRequest{{OperationID: requestID, Kind: "provider_release_search", CacheKey: queryKey, Input: initialInput}}},
		Target:     persistence.MatchingRunFenceSnapshot{Version: 2, GroupID: groupID, GroupRevision: "revision-1", Capture: targetBytes, CaptureDigest: targetDigest},
		PhaseInput: phaseInput, PhaseOutput: phaseOutput, PhaseProvenance: provenance,
		SearchReceipt:  persistence.MatchingFallbackSearchReceipt{RunID: runID, RequestID: requestID, StageIndex: 0, ProviderSourceID: sourceID, ConfigurationIdentity: "config-1", CacheKey: queryKey, RequestInput: initialInput, Payload: searchPayload, PayloadSHA256: hex.EncodeToString(searchHash[:]), PayloadDigestVersion: 2, CacheGeneration: 1, FetchOrder: 1},
		LookupReceipts: projectionReceipts,
		Authority:      MatchingFallbackAuthority{RunSourceID: sourceID, RunProviderID: providerID, RunEndpoint: "https://musicbrainz.example", RunConfiguration: "config-1", SearchKind: "provider_release_search", SearchCacheKey: queryKey, SearchInput: initialInput, Lookups: lookupAuthorities},
	}
}

func replaceFallbackMetadataResult(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	var capture fallbackCapture
	if err := json.Unmarshal(raw, &capture); err != nil {
		t.Fatal(err)
	}
	capture.Members[0].Captures[0].MetadataWinner = nil
	encoded, err := json.Marshal(capture)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalFallbackJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func replaceFallbackMetadataSnapshot(t *testing.T, raw, snapshot json.RawMessage) json.RawMessage {
	t.Helper()
	var capture fallbackCapture
	if err := json.Unmarshal(raw, &capture); err != nil {
		t.Fatal(err)
	}
	capture.Members[0].AnalysisInputs[0].InputSnapshot = snapshot
	encoded, err := json.Marshal(capture)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalFallbackJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func refreshFallbackTargetDigest(input *MatchingFallbackApplicabilityInput) {
	hash := sha256.Sum256(input.Target.Capture)
	input.Target.CaptureDigest = hex.EncodeToString(hash[:])
	input.Intent.TargetCaptureDigest = input.Target.CaptureDigest
}
