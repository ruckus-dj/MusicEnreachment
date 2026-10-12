package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// SerializeFrozenFallbackEvaluation renders the already-calculated frozen
// inventory and kernel decision. It never scores, queries, or merges provider
// recording identities across releases.
func SerializeFrozenFallbackEvaluation(prepared PreparedFrozenFallbackEvaluation) (persistence.FrozenMatchingPublication, error) {
	inventory := prepared.FrozenInventory
	if prepared.Authority.RunID == uuid.Nil || prepared.Authority.WaitRevision < 1 || !isFallbackSHA256(prepared.Authority.Digest) ||
		prepared.Authority.TargetFenceVersion != 2 ||
		!isFallbackSHA256(prepared.Authority.TargetCaptureDigest) || len(prepared.Authority.TargetCapture) == 0 ||
		inventory.Group.ID == uuid.Nil || inventory.Group.Revision == "" {
		return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: authority or target identity is incomplete")
	}
	canonicalTarget, err := canonicalFallbackJSON(prepared.Authority.TargetCapture)
	if err != nil {
		return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: target capture is invalid")
	}
	targetDigest := sha256.Sum256(canonicalTarget)
	if hex.EncodeToString(targetDigest[:]) != prepared.Authority.TargetCaptureDigest {
		return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: target capture digest does not match authority")
	}
	var targetCapture fallbackCapture
	if decodeFallbackStrict(prepared.Authority.TargetCapture, &targetCapture) != nil || targetCapture.SchemaVersion != 2 ||
		targetCapture.GroupID != inventory.Group.ID || targetCapture.GroupRevision != inventory.Group.Revision || len(targetCapture.Members) != len(inventory.MemberFences) {
		return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: target capture identity or membership is invalid")
	}
	targetMembers := make(map[uuid.UUID]struct{}, len(targetCapture.Members))
	for _, member := range targetCapture.Members {
		if member.VariantID == uuid.Nil {
			return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: target capture member identity is invalid")
		}
		if _, exists := targetMembers[member.VariantID]; exists {
			return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: target capture has duplicate members")
		}
		targetMembers[member.VariantID] = struct{}{}
	}
	memberSet := make(map[uuid.UUID]struct{}, len(inventory.MemberFences))
	for _, member := range inventory.MemberFences {
		if member.FileMemberID == uuid.Nil || member.WorkID == uuid.Nil {
			return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: member identity is incomplete")
		}
		if _, exists := memberSet[member.FileMemberID]; exists {
			return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: duplicate member identity")
		}
		memberSet[member.FileMemberID] = struct{}{}
		if _, exists := targetMembers[member.FileMemberID]; !exists {
			return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: target capture membership differs from frozen inventory")
		}
	}
	files := make(map[uuid.UUID]IncomingFile, len(inventory.Files))
	for _, file := range inventory.Files {
		if _, exists := memberSet[file.VariantID]; !exists || file.VariantID == uuid.Nil {
			return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: file is outside frozen membership")
		}
		if _, exists := files[file.VariantID]; exists {
			return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: duplicate frozen file")
		}
		files[file.VariantID] = file
	}
	if len(files) != len(memberSet) {
		return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: frozen files do not cover membership")
	}

	releases := append([]persistence.MatchingRelease(nil), prepared.Result.ReleaseLayouts...)
	if len(releases) == 0 && len(prepared.Result.AllCandidates) != 0 {
		return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: candidate edges have no release layouts")
	}
	releaseByKey := make(map[string]persistence.MatchingRelease, len(releases))
	trackByCoordinate := make(map[string]persistence.MatchingReleaseTrack)
	for _, release := range releases {
		if release.ID == uuid.Nil || release.ProviderKey == "" {
			return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: release identity is incomplete")
		}
		if _, exists := releaseByKey[release.ProviderKey]; exists {
			return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: duplicate release identity")
		}
		releaseByKey[release.ProviderKey] = release
		seenTrackIDs := make(map[uuid.UUID]struct{}, len(release.Tracks))
		for _, track := range release.Tracks {
			if track.ID == uuid.Nil || track.Medium < 0 || track.Position < 0 || track.RecordingProviderKey == "" {
				return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: release track identity is incomplete")
			}
			if _, exists := seenTrackIDs[track.ID]; exists {
				return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: duplicate provider track identity")
			}
			seenTrackIDs[track.ID] = struct{}{}
			key := frozenTrackCoordinate(release.ProviderKey, track.Medium, track.Position)
			if _, exists := trackByCoordinate[key]; exists {
				return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: duplicate release coordinate")
			}
			trackByCoordinate[key] = track
		}
	}
	lookupByRelease := make(map[string]persistence.MatchingFallbackCalculationLookupAuthority, len(prepared.Authority.CalculationLookups))
	for _, lookup := range prepared.Authority.CalculationLookups {
		if lookup.RequestID == uuid.Nil || lookup.ReleaseID == "" || lookup.StageIndex != prepared.Authority.CalculationStageIndex ||
			lookup.Kind == "" || lookup.ProviderSourceID == uuid.Nil || lookup.ProviderID == uuid.Nil || lookup.Endpoint == "" ||
			lookup.Configuration == "" || lookup.CacheKey == "" || len(lookup.Input) == 0 {
			return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: registered lookup identity is incomplete")
		}
		if lookup.ProviderSourceID != prepared.Authority.RunSourceID || lookup.ProviderID != prepared.Authority.RunProviderID ||
			lookup.Endpoint != prepared.Authority.RunEndpoint || lookup.Configuration != prepared.Authority.RunConfiguration ||
			lookup.CacheKey != "release-lookup:"+lookup.ReleaseID {
			return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: lookup does not match frozen run source scope")
		}
		if _, exists := lookupByRelease[lookup.ReleaseID]; exists {
			return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: duplicate release lookup identity")
		}
		lookupByRelease[lookup.ReleaseID] = lookup
	}
	receiptByRequest := make(map[uuid.UUID]persistence.MatchingFallbackLookupReceipt, len(prepared.Authority.CalculationReceipts))
	for _, receipt := range prepared.Authority.CalculationReceipts {
		if receipt.RequestID == uuid.Nil || receipt.RunID != prepared.Authority.RunID || !isFallbackSHA256(receipt.PayloadSHA256) ||
			!isFallbackSHA256(receipt.ProjectionSHA256) || len(receipt.Payload) == 0 || len(receipt.AcceptedProjection) == 0 {
			return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: accepted lookup receipt is incomplete")
		}
		payload, payloadErr := canonicalFallbackJSON(receipt.Payload)
		projection, projectionErr := canonicalFallbackJSON(receipt.AcceptedProjection)
		payloadDigest, projectionDigest := sha256.Sum256(payload), sha256.Sum256(projection)
		if payloadErr != nil || projectionErr != nil || hex.EncodeToString(payloadDigest[:]) != receipt.PayloadSHA256 ||
			hex.EncodeToString(projectionDigest[:]) != receipt.ProjectionSHA256 {
			return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: accepted lookup receipt hashes are invalid")
		}
		if _, exists := receiptByRequest[receipt.RequestID]; exists {
			return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: duplicate lookup receipt")
		}
		receiptByRequest[receipt.RequestID] = receipt
	}

	edges := make([]persistence.FrozenMatchingScoredEdge, 0, len(prepared.Result.AllCandidates))
	seenEdges := make(map[string]struct{}, len(prepared.Result.AllCandidates))
	for _, candidate := range prepared.Result.AllCandidates {
		if _, exists := memberSet[candidate.MemberID]; !exists {
			return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: edge references unknown member")
		}
		release, exists := releaseByKey[candidate.ReleaseID]
		if !exists {
			return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: edge references unknown release")
		}
		track, exists := trackByCoordinate[frozenTrackCoordinate(candidate.ReleaseID, candidate.Medium, candidate.Position)]
		if !exists || track.ID != candidate.ProviderTrackUUID || track.RecordingProviderKey != candidate.RecordingID {
			return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: edge track identity does not match release layout")
		}
		lookup, exists := lookupByRelease[candidate.ReleaseID]
		if !exists {
			return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: edge has no registered release lookup")
		}
		receipt, exists := receiptByRequest[lookup.RequestID]
		if !exists || receipt.StageIndex != lookup.StageIndex || receipt.CacheKey != lookup.CacheKey ||
			receipt.ProviderSourceID != lookup.ProviderSourceID || receipt.ConfigurationIdentity != lookup.Configuration ||
			!fallbackJSONEqual(receipt.RequestInput, lookup.Input) ||
			receipt.PayloadSHA256 != candidate.LookupPayloadSHA || receipt.ProjectionSHA256 != candidate.LookupProjectionSHA {
			return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: edge lookup receipt does not match registered authority")
		}
		if candidate.MatchingInput.FileID != candidate.MemberID || candidate.MatchingInput.ReleaseID != release.ProviderKey ||
			candidate.MatchingInput.RecordingID != candidate.RecordingID || candidate.MatchingInput.Medium != candidate.Medium || candidate.MatchingInput.Track != candidate.Position {
			return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: edge scoring coordinates do not match identity")
		}
		key := fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%d", candidate.MemberID, candidate.ReleaseID, candidate.RecordingID, candidate.Medium, candidate.Position)
		if _, exists := seenEdges[key]; exists {
			return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: duplicate scored edge")
		}
		seenEdges[key] = struct{}{}
		releaseScore, err := json.Marshal(candidate.MatchingInput.ReleaseScore)
		if err != nil {
			return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: encode release score: %w", err)
		}
		releaseInput, err := json.Marshal(candidate.MatchingInput.ReleaseScoreInput)
		if err != nil {
			return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: encode release score input: %w", err)
		}
		assignmentScore, err := json.Marshal(candidate.MatchingInput.AssignmentScore)
		if err != nil {
			return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: encode assignment score: %w", err)
		}
		assignmentInput, err := json.Marshal(candidate.MatchingInput.AssignmentInput)
		if err != nil {
			return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: encode assignment score input: %w", err)
		}
		edges = append(edges, persistence.FrozenMatchingScoredEdge{
			FileMemberID: candidate.MemberID, ReleaseID: candidate.ReleaseID, ProviderReleaseID: release.ID, RecordingID: candidate.RecordingID,
			ProviderTrackID: candidate.ProviderTrackUUID, Medium: candidate.Medium, Position: candidate.Position,
			RecordingLookupKey: candidate.RecordingID,
			LookupRequestID:    lookup.RequestID, LookupStageIndex: lookup.StageIndex,
			LookupRequestInput: append(json.RawMessage(nil), lookup.Input...),
			MetadataInputSHA:   candidate.MetadataInputSHA, LookupPayloadSHA: candidate.LookupPayloadSHA,
			LookupProjectionSHA: candidate.LookupProjectionSHA, ReleaseScore: releaseScore,
			ReleaseScoreInput: releaseInput, AssignmentScore: assignmentScore, AssignmentScoreInput: assignmentInput,
		})
	}
	if len(releases) != 0 && len(edges) != len(memberSet)*len(trackByCoordinate) {
		return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: scored edges do not cover the complete member and track universe")
	}
	sort.Slice(edges, func(i, j int) bool {
		a, b := edges[i], edges[j]
		if a.FileMemberID != b.FileMemberID {
			return a.FileMemberID.String() < b.FileMemberID.String()
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

	decision := persistence.FrozenMatchingEvaluation{
		Outcome: string(prepared.KernelResult.Outcome), Decision: string(prepared.KernelResult.Decision),
		Reason: prepared.KernelResult.Reason, SelectedRelease: prepared.KernelResult.SelectedReleaseID,
		Assignments: []persistence.FrozenMatchingAssignment{},
	}
	automaticIntent := prepared.Authority.Intent.SchemaVersion == 3 && prepared.Authority.Intent.FenceVersion == 2 &&
		prepared.Authority.Disposition == "qualified" &&
		prepared.Authority.GroupAuthority.Mode == "automatic" && prepared.Authority.GroupAuthority.GroupID == inventory.Group.ID &&
		prepared.Authority.Intent.TargetCaptureDigest == prepared.Authority.TargetCaptureDigest &&
		len(prepared.Authority.Intent.Targets) == 1 && prepared.Authority.Intent.Targets[0].GroupID == inventory.Group.ID &&
		prepared.Authority.Intent.Targets[0].Revision == inventory.Group.Revision &&
		len(prepared.Authority.Intent.CapturedMembers) == len(memberSet) &&
		len(inventory.CapturedMetadata) == len(memberSet)
	if automaticIntent {
		capturedByMember := make(map[uuid.UUID]persistence.MatchingAcquisitionCapturedMember, len(inventory.CapturedMetadata))
		for _, captured := range inventory.CapturedMetadata {
			if captured.MemberID == uuid.Nil || captured.WorkID == uuid.Nil {
				automaticIntent = false
				break
			}
			if _, exists := capturedByMember[captured.MemberID]; exists {
				automaticIntent = false
				break
			}
			capturedByMember[captured.MemberID] = captured
		}
		for _, captured := range prepared.Authority.Intent.CapturedMembers {
			inventoryCapture, exists := capturedByMember[captured.MemberID]
			intentBytes, intentErr := json.Marshal(captured)
			inventoryBytes, inventoryErr := json.Marshal(inventoryCapture)
			if !exists || intentErr != nil || inventoryErr != nil || !fallbackJSONEqual(intentBytes, inventoryBytes) {
				automaticIntent = false
				break
			}
		}
		for memberID := range memberSet {
			if _, exists := capturedByMember[memberID]; !exists {
				automaticIntent = false
				break
			}
		}
	}
	// A selected assignment is publishable only when the full kernel decision is
	// unique, bound to automatic intent and covers every captured member. All other states remain review.
	if automaticIntent && prepared.KernelResult.Outcome == MatchingDecisionOutcomeUnique && prepared.KernelResult.Assignment.Unique &&
		len(prepared.KernelResult.Assignment.Choices) == len(memberSet) {
		assigned := make(map[uuid.UUID]struct{}, len(memberSet))
		for _, edge := range prepared.KernelResult.AssignmentCandidates {
			fileID, err := uuid.Parse(edge.FileVariantID)
			if err != nil {
				continue
			}
			candidateID := prepared.KernelResult.Assignment.Choices[edge.FileVariantID]
			if candidateID == "" || candidateID != edge.CandidateID {
				continue
			}
			var selected *MatchingFallbackCandidate
			for i := range prepared.Result.AllCandidates {
				candidate := &prepared.Result.AllCandidates[i]
				if candidate.MemberID == fileID && fmt.Sprintf("%s/%d/%d", candidate.ReleaseID, candidate.Medium, candidate.Position) == edge.PositionID {
					selected = candidate
					break
				}
			}
			if selected == nil {
				continue
			}
			if _, duplicate := assigned[fileID]; duplicate {
				return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: duplicate selected member")
			}
			assigned[fileID] = struct{}{}
			decision.Assignments = append(decision.Assignments, persistence.FrozenMatchingAssignment{
				FileMemberID: fileID, ProviderTrackID: selected.ProviderTrackUUID,
				Medium: selected.Medium + 1, Position: selected.Position + 1,
			})
		}
		if len(decision.Assignments) != len(memberSet) {
			decision.Outcome = string(MatchingDecisionOutcomeReview)
			decision.Decision = string(MatchingDecisionReview)
			decision.SelectedRelease = ""
			decision.Assignments = []persistence.FrozenMatchingAssignment{}
		} else {
			sort.Slice(decision.Assignments, func(i, j int) bool {
				return decision.Assignments[i].FileMemberID.String() < decision.Assignments[j].FileMemberID.String()
			})
		}
	} else {
		decision.Outcome = string(MatchingDecisionOutcomeReview)
		decision.Decision = string(MatchingDecisionReview)
		decision.SelectedRelease = ""
	}

	members := make([]persistence.FrozenMatchingReviewMember, 0, len(inventory.MemberFences))
	for _, fence := range inventory.MemberFences {
		file := files[fence.FileMemberID]
		tags, err := json.Marshal(file.Tags)
		if err != nil {
			return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: encode member tags: %w", err)
		}
		locations, err := json.Marshal(file.Locations)
		if err != nil {
			return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: encode member locations: %w", err)
		}
		members = append(members, persistence.FrozenMatchingReviewMember{FileMemberID: fence.FileMemberID, WorkID: fence.WorkID,
			SourceSHA256: append([]byte(nil), fence.SourceSHA256...), Tags: tags, Locations: locations})
	}
	sort.Slice(members, func(i, j int) bool { return members[i].FileMemberID.String() < members[j].FileMemberID.String() })
	return persistence.FrozenMatchingPublication{
		Authority: persistence.FrozenMatchingAuthorityIdentity{SealSchemaVersion: 3, RunID: prepared.Authority.RunID,
			WaitRevision: prepared.Authority.WaitRevision, SealDigest: prepared.Authority.Digest, SealDisposition: prepared.Authority.Disposition,
			TargetFenceVersion:  prepared.Authority.TargetFenceVersion,
			TargetCaptureDigest: prepared.Authority.TargetCaptureDigest, TargetCapture: append(json.RawMessage(nil), prepared.Authority.TargetCapture...),
			Intent: prepared.Authority.Intent, SourceID: prepared.Authority.SourceID, ProviderID: prepared.Authority.ProviderID,
			Endpoint: prepared.Authority.Endpoint, SourceConfiguration: prepared.Authority.SourceConfiguration,
			RunProviderConfiguration: prepared.Authority.RunProviderConfig, RunSourceID: prepared.Authority.RunSourceID,
			RunProviderID: prepared.Authority.RunProviderID, RunEndpoint: prepared.Authority.RunEndpoint,
			RunConfiguration: prepared.Authority.RunConfiguration, CalculationStageIndex: prepared.Authority.CalculationStageIndex,
			FullEvaluationContext: append(json.RawMessage(nil), prepared.Authority.FullEvaluationContext...),
			GroupID:               inventory.Group.ID, GroupRevision: inventory.Group.Revision, GroupAuthority: prepared.Authority.GroupAuthority},
		Evaluation: decision, Edges: edges,
		Inventory: persistence.FrozenMatchingReviewInventory{GroupID: inventory.Group.ID, GroupRevision: inventory.Group.Revision,
			Members: members, Releases: frozenInventoryReleases(releases), CapturedMetadata: append([]persistence.MatchingAcquisitionCapturedMember(nil), inventory.CapturedMetadata...),
			Captures: append([]persistence.IncomingGroupingCapture(nil), inventory.Captures...)},
	}, nil
}

// frozenInventoryReleases deep-copies the frozen release layouts for the
// publication review inventory and converts their persisted zero-based medium
// and track ordinals to the one-based presentation coordinates used by
// persisted assignments and the raw provider lookup. The conversion happens
// exactly once per serialization and never mutates the caller's prepared
// layouts, so repeated serialization is stable and the scored edges keep their
// zero-based kernel coordinates.
func frozenInventoryReleases(releases []persistence.MatchingRelease) []persistence.MatchingRelease {
	if releases == nil {
		return nil
	}
	copied := make([]persistence.MatchingRelease, len(releases))
	for index, release := range releases {
		clone := release
		clone.Fields = append(json.RawMessage(nil), release.Fields...)
		clone.RawSource = append(json.RawMessage(nil), release.RawSource...)
		if release.FieldEvidence != nil {
			evidence := make(map[string]persistence.ProviderFieldEvidence, len(release.FieldEvidence))
			for key, value := range release.FieldEvidence {
				evidence[key] = value
			}
			clone.FieldEvidence = evidence
		}
		clone.Credits = append([]persistence.MatchingReleaseCredit(nil), release.Credits...)
		clone.Tracks = make([]persistence.MatchingReleaseTrack, len(release.Tracks))
		for trackIndex, track := range release.Tracks {
			trackClone := track
			trackClone.RecordingArtists = append([]string(nil), track.RecordingArtists...)
			if track.RecordingLengthMS != nil {
				length := *track.RecordingLengthMS
				trackClone.RecordingLengthMS = &length
			}
			if track.ReleaseLengthMS != nil {
				length := *track.ReleaseLengthMS
				trackClone.ReleaseLengthMS = &length
			}
			trackClone.Medium = track.Medium + 1
			trackClone.Position = track.Position + 1
			clone.Tracks[trackIndex] = trackClone
		}
		copied[index] = clone
	}
	return copied
}

func frozenTrackCoordinate(release string, medium, position int) string {
	return fmt.Sprintf("%s\x00%d\x00%d", release, medium, position)
}
