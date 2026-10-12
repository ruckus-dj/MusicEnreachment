package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestPrepareSealedReviewOnlyRetainsFrozenInventoryWithoutProviderLookup(t *testing.T) {
	input := validFallbackApplicabilityInput(t, 2, false)
	var frozen fallbackCapture
	if err := decodeFallbackStrict(input.Target.Capture, &frozen); err != nil {
		t.Fatal(err)
	}
	if len(frozen.Members) == 0 || len(frozen.Members[0].Locations) == 0 {
		t.Fatal("fixture has no frozen members or locations")
	}
	additionalLocation := frozen.Members[0].Locations[0]
	additionalLocation.LocationID = uuid.New()
	additionalLocation.RelativePath = "alternate/track.flac"
	frozen.Members[0].Locations = append(frozen.Members[0].Locations, additionalLocation)
	// The helper consumes the same captured schema as the review-only prepare
	// branch; re-encode to keep the frozen inventory and target digest consistent.
	input.Target.Capture, _ = json.Marshal(frozen)
	canonical, err := canonicalFallbackJSON(input.Target.Capture)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(canonical)
	input.Target.CaptureDigest = hex.EncodeToString(digest[:])
	seal := persistence.MatchingFallbackSealAuthority{
		Target: input.Target, CapturedMetadata: input.Intent.CapturedMembers,
	}
	prepared, err := prepareSealedReviewOnly(context.Background(), seal)
	if err != nil {
		t.Fatal(err)
	}
	inventory := prepared.FrozenInventory
	if inventory.Group.ID != frozen.GroupID || inventory.Group.Revision != frozen.GroupRevision || inventory.Group.Manual {
		t.Fatalf("frozen group = %#v, want captured identity and no inferred manual authority", inventory.Group)
	}
	if len(inventory.Files) != len(frozen.Members) || len(inventory.MemberFences) != len(frozen.Members) || len(inventory.Group.Members) != len(frozen.Members) {
		t.Fatalf("inventory sizes: files=%d fences=%d members=%d, frozen=%d", len(inventory.Files), len(inventory.MemberFences), len(inventory.Group.Members), len(frozen.Members))
	}
	if !reflect.DeepEqual(inventory.CapturedMetadata, input.Intent.CapturedMembers) {
		t.Fatalf("captured metadata was not retained: got %#v", inventory.CapturedMetadata)
	}
	wantCaptures := make([]persistence.IncomingGroupingCapture, 0)
	for index, frozenMember := range frozen.Members {
		file := inventory.Files[index]
		if inventory.Group.Members[index] != frozenMember.VariantID || file.VariantID != frozenMember.VariantID {
			t.Fatalf("member %d identity was not retained", index)
		}
		if len(file.Locations) != len(frozenMember.Locations) {
			t.Fatalf("member %s locations = %d, want all %d frozen locations", file.VariantID, len(file.Locations), len(frozenMember.Locations))
		}
		if index == 0 && len(file.Locations) != 2 {
			t.Fatalf("member %s locations = %d, want both frozen locations", file.VariantID, len(file.Locations))
		}
		if file.CaptureAnalysisID == uuid.Nil || file.CaptureIdentity == "" || len(file.Tags) == 0 {
			t.Fatalf("member %s lost capture identity or raw tags: %#v", file.VariantID, file)
		}
		var wantTags map[string][]string
		if err := json.Unmarshal(input.Intent.CapturedMembers[index].Tags, &wantTags); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(file.Tags, wantTags) || file.CaptureAnalysisID != *frozenMember.Captures[0].CaptureAnalysisID {
			t.Fatalf("member %s capture metadata was not retained", file.VariantID)
		}
		wantCaptures = append(wantCaptures, frozenMember.Captures...)
	}
	if !reflect.DeepEqual(inventory.Captures, wantCaptures) {
		t.Fatalf("captured source metadata was not retained: got %d captures, want %d", len(inventory.Captures), len(wantCaptures))
	}
}

func TestEvaluateFrozenFallbackInventoryPropagatesCancellationToMatchingKernel(t *testing.T) {
	input := validFallbackApplicabilityInput(t, 2, false)
	var frozen fallbackCapture
	if err := decodeFallbackStrict(input.Target.Capture, &frozen); err != nil {
		t.Fatal(err)
	}
	if len(frozen.Members) == 0 {
		t.Fatal("fixture has no frozen members")
	}
	trackID := uuid.New()
	result := MatchingFallbackApplicabilityResult{
		ReleaseLayouts: []persistence.MatchingRelease{{ProviderKey: "release", Tracks: []persistence.MatchingReleaseTrack{{ID: trackID, Medium: 0, Position: 0}}}},
		AllCandidates:  []MatchingFallbackCandidate{{MatchingInput: kernelEdge(frozen.Members[0].VariantID, "release", 0, 0, 0.8)}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := evaluateFrozenFallbackInventory(ctx, input.Target, input.Intent.CapturedMembers, result)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("evaluateFrozenFallbackInventory() error = %v, want context.Canceled", err)
	}
}
