package persistence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/policy"
)

// ValidateMatchingFallbackCalculationEdges independently verifies the compact
// stage-zero edge identities against the frozen capture and accepted lookup
// projections. It deliberately does not recalculate scores.
func ValidateMatchingFallbackCalculationEdges(snapshot MatchingFallbackCalculationContext, raw json.RawMessage) error {
	type edge struct {
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
	type frozenMember struct {
		VariantID      uuid.UUID                 `json:"variant_id"`
		Ready          bool                      `json:"ready"`
		Captures       []IncomingGroupingCapture `json:"captures"`
		AnalysisInputs []struct {
			AnalysisRecordID string          `json:"analysis_record_id"`
			WorkID           uuid.UUID       `json:"work_id"`
			Step             string          `json:"step"`
			State            string          `json:"state"`
			InputSnapshot    json.RawMessage `json:"input_snapshot"`
			InputDigest      *string         `json:"input_digest"`
			ResultReference  *uuid.UUID      `json:"result_reference"`
		} `json:"analysis_inputs"`
	}
	var capture struct {
		SchemaVersion int            `json:"schema_version"`
		GroupID       uuid.UUID      `json:"group_id"`
		GroupRevision string         `json:"group_revision"`
		Members       []frozenMember `json:"members"`
	}
	if err := json.Unmarshal(snapshot.Target.Capture, &capture); err != nil || capture.SchemaVersion != 2 || capture.GroupID != snapshot.Target.GroupID || capture.GroupRevision != snapshot.Target.GroupRevision {
		return fmt.Errorf("frozen capture is invalid")
	}
	selected := make(map[uuid.UUID]MatchingAcquisitionCapturedMember, len(snapshot.Intent.CapturedMembers))
	for _, member := range snapshot.Intent.CapturedMembers {
		if member.MemberID == uuid.Nil || member.WorkID == uuid.Nil || member.AnalysisInputSHA256 == "" {
			return fmt.Errorf("selected capture identity is invalid")
		}
		if _, duplicate := selected[member.MemberID]; duplicate {
			return fmt.Errorf("selected capture identity is duplicated")
		}
		selected[member.MemberID] = member
	}
	if len(selected) != len(capture.Members) || len(selected) == 0 {
		return fmt.Errorf("selected capture does not exactly cover frozen members")
	}
	if err := validateFallbackLookupUniverse(snapshot); err != nil {
		return err
	}
	type expectedEdge struct {
		work                      uuid.UUID
		metadata, recording       string
		track                     uuid.UUID
		medium, position          int
		request                   uuid.UUID
		payloadSHA, projectionSHA string
	}
	want := make(map[string]expectedEdge)
	for _, member := range capture.Members {
		chosen, ok := selected[member.VariantID]
		if !ok || !member.Ready || member.VariantID == uuid.Nil || len(member.Captures) == 0 {
			return fmt.Errorf("frozen member is missing or not ready")
		}
		var metadataRecord *struct {
			digest string
			result uuid.UUID
		}
		seenRecords := map[string]json.RawMessage{}
		for _, input := range member.AnalysisInputs {
			if input.AnalysisRecordID == "" || input.WorkID == uuid.Nil || input.Step == "" {
				return fmt.Errorf("frozen analysis identity is invalid")
			}
			encoded, _ := json.Marshal(input)
			if previous, exists := seenRecords[input.AnalysisRecordID]; exists {
				if string(previous) != string(encoded) {
					return fmt.Errorf("conflicting duplicate frozen analysis identity")
				}
				continue
			}
			seenRecords[input.AnalysisRecordID] = encoded
			if input.Step == "metadata" && input.WorkID == chosen.WorkID {
				if input.State != "succeeded" || input.InputDigest == nil || input.ResultReference == nil || metadataRecord != nil {
					return fmt.Errorf("selected metadata analysis is missing or ambiguous")
				}
				canonical, err := canonicalMatchingJSON(input.InputSnapshot)
				if err != nil {
					return fmt.Errorf("selected metadata input is invalid")
				}
				digest := sha256.Sum256(canonical)
				if hex.EncodeToString(digest[:]) != *input.InputDigest || *input.InputDigest != chosen.AnalysisInputSHA256 {
					return fmt.Errorf("selected metadata input digest differs from frozen intent")
				}
				metadataRecord = &struct {
					digest string
					result uuid.UUID
				}{*input.InputDigest, *input.ResultReference}
			}
		}
		if metadataRecord == nil || chosen.CaptureAnalysisID == nil || *chosen.CaptureAnalysisID != metadataRecord.result {
			return fmt.Errorf("selected metadata result differs from frozen intent")
		}
		for _, source := range member.Captures {
			if !source.Ready || source.VariantID != member.VariantID || source.WorkID != chosen.WorkID || source.CaptureAnalysisID == nil || *source.CaptureAnalysisID != metadataRecord.result || source.MetadataWinner == nil || *source.MetadataWinner != metadataRecord.result || !matchingJSONEqual(source.Tags, chosen.Tags) {
				return fmt.Errorf("frozen source capture differs from selected metadata identity")
			}
		}
		seenLookupIDs := make(map[uuid.UUID]bool, len(snapshot.LookupReceipts))
		seenReleaseIDs := make(map[string]bool, len(snapshot.LookupReceipts))
		for _, lookup := range snapshot.LookupReceipts {
			if lookup.StageIndex != snapshot.StageIndex || lookup.RunID != snapshot.RunID {
				return fmt.Errorf("accepted lookup scope is invalid")
			}
			releaseID := lookupReleaseID(snapshot, lookup.RequestID)
			canonicalPayload, err := canonicalMatchingJSON(lookup.Payload)
			if err != nil {
				return fmt.Errorf("accepted lookup payload is invalid")
			}
			payloadDigest := sha256.Sum256(canonicalPayload)
			canonicalProjection, err := canonicalMatchingJSON(lookup.AcceptedProjection)
			if err != nil {
				return fmt.Errorf("accepted lookup projection is invalid")
			}
			projectionDigest := sha256.Sum256(canonicalProjection)
			if lookup.RequestID == uuid.Nil || releaseID == "" || seenLookupIDs[lookup.RequestID] || seenReleaseIDs[releaseID] ||
				hex.EncodeToString(payloadDigest[:]) != lookup.PayloadSHA256 || hex.EncodeToString(projectionDigest[:]) != lookup.ProjectionSHA256 {
				return fmt.Errorf("accepted lookup receipt references are invalid")
			}
			seenLookupIDs[lookup.RequestID], seenReleaseIDs[releaseID] = true, true
			var projection ProviderMatchingSnapshot
			if json.Unmarshal(lookup.AcceptedProjection, &projection) != nil || len(projection.Releases) != 1 || projection.Releases[0].ProviderKey != releaseID ||
				projection.Source.ID != lookup.ProviderSourceID || projection.Source.ConfigurationIdentity != lookup.ConfigurationIdentity ||
				projection.Source.ProviderID != snapshot.RunProviderID || projection.Source.Endpoint != snapshot.RunEndpoint ||
				lookup.ProviderSourceID != snapshot.RunSourceID || lookup.ConfigurationIdentity != snapshot.RunConfiguration {
				return fmt.Errorf("accepted lookup projection is invalid")
			}
			for _, track := range projection.Releases[0].Tracks {
				if track.ID == uuid.Nil || track.RecordingProviderKey == "" || track.Medium < 0 || track.Position < 0 {
					return fmt.Errorf("accepted track identity is invalid")
				}
				key := edgeIdentity(member.VariantID, releaseID, track.Medium, track.Position, track.RecordingProviderKey)
				candidate := expectedEdge{work: chosen.WorkID, metadata: metadataRecord.digest, recording: track.RecordingProviderKey, track: track.ID, medium: track.Medium, position: track.Position,
					request: lookup.RequestID, payloadSHA: lookup.PayloadSHA256, projectionSHA: lookup.ProjectionSHA256}
				if prior, exists := want[key]; exists && prior != candidate {
					return fmt.Errorf("ambiguous accepted track identity")
				}
				want[key] = candidate
			}
		}
	}
	edges := make([]edge, 0)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&edges); err != nil || edges == nil {
		return fmt.Errorf("edge calculations are invalid")
	}
	if len(edges) != len(want) {
		return fmt.Errorf("edge calculations do not cover the complete identity universe")
	}
	seen := make(map[string]bool, len(edges))
	for index, value := range edges {
		if index > 0 && !fallbackReceiptEdgeLess(edges[index-1], value) {
			return fmt.Errorf("edge calculations are not in canonical order")
		}
		key := edgeIdentity(value.MemberID, value.ReleaseID, value.Medium, value.Position, value.RecordingID)
		expected, ok := want[key]
		if !ok || seen[key] {
			return fmt.Errorf("edge calculation is extra or duplicated")
		}
		seen[key] = true
		if value.WorkID != expected.work || value.MetadataSHA != expected.metadata || value.ProviderTrackID != expected.track || value.LookupRequestID != expected.request || value.LookupPayloadSHA != expected.payloadSHA || value.LookupProjectionSHA != expected.projectionSHA || value.Profile != "recording_release" || math.IsNaN(value.Confidence) || math.IsInf(value.Confidence, 0) || value.Confidence < 0 || value.Confidence > 1 {
			return fmt.Errorf("edge calculation identity or score envelope is invalid")
		}
	}
	return nil
}

func validateFallbackLookupUniverse(snapshot MatchingFallbackCalculationContext) error {
	var plan policy.TypedFallbackQueryPlan
	if snapshot.StageIndex < 0 || json.Unmarshal(snapshot.Intent.FallbackQueryPlan, &plan) != nil ||
		snapshot.StageIndex >= len(plan.Stages) || snapshot.SearchReceipt.StageIndex != snapshot.StageIndex ||
		!fallbackStageMatchesRequest(snapshot.Intent, "search", snapshot.SearchKind, snapshot.SearchInput, snapshot.StageIndex) {
		return fmt.Errorf("accepted search scope does not match registered fallback stage")
	}
	var hits struct {
		Releases []struct {
			ID string `json:"id"`
		} `json:"releases"`
		Recordings []struct {
			Releases []struct {
				ID string `json:"id"`
			} `json:"releases"`
		} `json:"recordings"`
	}
	if err := json.Unmarshal(snapshot.SearchReceipt.Payload, &hits); err != nil {
		return fmt.Errorf("accepted search payload is invalid")
	}
	want := make(map[string]bool)
	switch snapshot.SearchKind {
	case "provider_release_search":
		for _, hit := range hits.Releases {
			if hit.ID == "" {
				return fmt.Errorf("accepted search release identity is invalid")
			}
			want[hit.ID] = true
		}
	case "provider_recording_search":
		for _, recording := range hits.Recordings {
			for _, release := range recording.Releases {
				if release.ID == "" {
					return fmt.Errorf("accepted search release identity is invalid")
				}
				want[release.ID] = true
			}
		}
	default:
		return fmt.Errorf("accepted search kind is invalid")
	}
	if len(want) != len(snapshot.Lookups) || len(want) != len(snapshot.LookupReceipts) {
		return fmt.Errorf("accepted lookup set does not cover the search release universe")
	}
	authorities := make(map[uuid.UUID]MatchingFallbackCalculationLookupAuthority, len(snapshot.Lookups))
	for _, lookup := range snapshot.Lookups {
		if lookup.RequestID == uuid.Nil || lookup.ReleaseID == "" || lookup.Kind != "provider_release_lookup" || lookup.CacheKey != "release-lookup:"+lookup.ReleaseID ||
			lookup.ProviderSourceID != snapshot.RunSourceID || lookup.ProviderID != snapshot.RunProviderID || lookup.Endpoint != snapshot.RunEndpoint || lookup.Configuration != snapshot.RunConfiguration ||
			!want[lookup.ReleaseID] || authorities[lookup.RequestID].RequestID != uuid.Nil {
			return fmt.Errorf("registered lookup authority is invalid")
		}
		authorities[lookup.RequestID] = lookup
	}
	for _, receipt := range snapshot.LookupReceipts {
		authority, ok := authorities[receipt.RequestID]
		if !ok || authority.StageIndex != snapshot.StageIndex || authority.ReleaseID != lookupReleaseID(snapshot, receipt.RequestID) ||
			receipt.StageIndex != snapshot.StageIndex || receipt.RunID != snapshot.RunID {
			return fmt.Errorf("accepted lookup receipts do not match registered authorities")
		}
		delete(authorities, receipt.RequestID)
	}
	if len(authorities) != 0 {
		return fmt.Errorf("accepted lookup receipts are incomplete")
	}
	return nil
}

func fallbackReceiptEdgeLess(left, right struct {
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
}) bool {
	if left.MemberID != right.MemberID {
		return left.MemberID.String() < right.MemberID.String()
	}
	if left.ReleaseID != right.ReleaseID {
		return left.ReleaseID < right.ReleaseID
	}
	if left.Medium != right.Medium {
		return left.Medium < right.Medium
	}
	if left.Position != right.Position {
		return left.Position < right.Position
	}
	return left.RecordingID < right.RecordingID
}

func edgeIdentity(member uuid.UUID, release string, medium, position int, recording string) string {
	return fmt.Sprintf("%s\x00%s\x00%d\x00%d\x00%s", member, release, medium, position, recording)
}

func lookupReleaseID(snapshot MatchingFallbackCalculationContext, requestID uuid.UUID) string {
	for _, lookup := range snapshot.Lookups {
		if lookup.RequestID == requestID {
			return lookup.ReleaseID
		}
	}
	return ""
}
