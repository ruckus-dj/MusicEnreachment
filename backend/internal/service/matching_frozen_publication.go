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

// SerializeFrozenFallbackEvaluation renders the sealed choice and the review
// inventory. It never scores or selects an alternative assignment.
func SerializeFrozenFallbackEvaluation(prepared PreparedFrozenFallbackEvaluation) (persistence.FrozenMatchingPublication, error) {
	a, inventory := prepared.Authority, prepared.FrozenInventory
	if a.RunID == uuid.Nil || a.WaitRevision < 1 || !isFallbackSHA256(a.Digest) ||
		a.TargetFenceVersion != 2 || inventory.Group.ID == uuid.Nil || inventory.Group.Revision == "" {
		return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: authority or target identity is incomplete")
	}
	target, err := canonicalFallbackJSON(a.TargetCapture)
	if err != nil {
		return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: target capture is invalid: %w", err)
	}
	targetDigest := sha256.Sum256(target)
	if hex.EncodeToString(targetDigest[:]) != a.TargetCaptureDigest {
		return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: target capture digest does not match authority")
	}
	releases := frozenInventoryReleases(prepared.Releases)
	evaluation, err := persistence.BuildFrozenMatchingLayoutEvaluation(prepared.LayoutDecision, releases)
	if err != nil {
		return persistence.FrozenMatchingPublication{}, err
	}
	members := make([]persistence.FrozenMatchingReviewMember, 0, len(inventory.MemberFences))
	files := make(map[uuid.UUID]IncomingFile, len(inventory.Files))
	for _, file := range inventory.Files {
		files[file.VariantID] = file
	}
	for _, fence := range inventory.MemberFences {
		file, ok := files[fence.FileMemberID]
		if !ok {
			return persistence.FrozenMatchingPublication{}, fmt.Errorf("frozen matching publication: captured member has no file")
		}
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
		Authority: persistence.FrozenMatchingAuthorityIdentity{
			SealSchemaVersion: 3, RunID: a.RunID, WaitRevision: a.WaitRevision, SealDigest: a.Digest, SealDisposition: a.Disposition,
			TargetFenceVersion: a.TargetFenceVersion, TargetCaptureDigest: a.TargetCaptureDigest, TargetCapture: a.TargetCapture,
			Intent: a.Intent, SourceID: a.SourceID, ProviderID: a.ProviderID, Endpoint: a.Endpoint, SourceConfiguration: a.SourceConfiguration,
			RunProviderConfiguration: a.RunProviderConfig, RunSourceID: a.RunSourceID, RunProviderID: a.RunProviderID,
			RunEndpoint: a.RunEndpoint, RunConfiguration: a.RunConfiguration, CalculationStageIndex: a.CalculationStageIndex,
			FullEvaluationContext: a.FullEvaluationContext, GroupID: inventory.Group.ID, GroupRevision: inventory.Group.Revision, GroupAuthority: a.GroupAuthority,
		},
		Evaluation: evaluation,
		Inventory: persistence.FrozenMatchingReviewInventory{
			GroupID: inventory.Group.ID, GroupRevision: inventory.Group.Revision, Members: members, Releases: releases,
			CapturedMetadata: inventory.CapturedMetadata, Captures: inventory.Captures,
		},
	}, nil
}

// frozenInventoryReleases converts persisted zero-based ordinals to one-based
// presentation coordinates without changing the accepted layouts.
func frozenInventoryReleases(releases []persistence.MatchingRelease) []persistence.MatchingRelease {
	copied := append([]persistence.MatchingRelease(nil), releases...)
	for index := range copied {
		copied[index].Tracks = append([]persistence.MatchingReleaseTrack(nil), releases[index].Tracks...)
		for trackIndex := range copied[index].Tracks {
			copied[index].Tracks[trackIndex].Medium++
			copied[index].Tracks[trackIndex].Position++
		}
	}
	return copied
}
