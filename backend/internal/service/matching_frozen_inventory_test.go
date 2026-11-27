package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestPrepareSealedReviewOnlyRetainsFrozenInventoryWithoutProviderLookup(t *testing.T) {
	groupID, memberID, workID := uuid.New(), uuid.New(), uuid.New()
	captureAnalysisID, metadataResult := uuid.New(), uuid.New()
	tags := json.RawMessage(`{"ALBUM":["Album"],"ARTIST":["Artist"],"TITLE":["Song"]}`)
	captures := []persistence.IncomingGroupingCapture{{
		WorkID: workID, VariantID: memberID, CaptureAnalysisID: &captureAnalysisID,
		MetadataWinner: &metadataResult, Tags: tags, ProbeResult: json.RawMessage(`{}`), Ready: true,
	}}
	frozen := fallbackCapture{
		SchemaVersion: 2,
		GroupID:       groupID,
		GroupRevision: "revision-1",
		Members: []fallbackCaptureMember{{
			VariantID: memberID,
			Ready:     true,
			Locations: []persistence.IncomingGroupingMemberLocation{
				{VariantID: memberID, RootID: uuid.New(), LocationID: uuid.New(), RelativePath: "Album/song.flac"},
				{VariantID: memberID, RootID: uuid.New(), LocationID: uuid.New(), RelativePath: "alternate/track.flac"},
			},
			Captures: captures,
			AnalysisInputs: []fallbackCaptureAnalysisInput{{
				AnalysisRecordID: "metadata-1", WorkID: workID, Step: "metadata",
				InputSnapshot: json.RawMessage(`{"metadata":"frozen"}`), ResultReference: &metadataResult,
			}},
		}},
	}
	encoded, err := json.Marshal(frozen)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalFallbackJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var persisted fallbackCapture
	if err := decodeFallbackStrict(canonical, &persisted); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(canonical)
	captured := []persistence.MatchingAcquisitionCapturedMember{{
		MemberID: memberID, WorkID: workID, CaptureAnalysisID: &captureAnalysisID, Tags: tags,
	}}
	seal := persistence.MatchingFallbackSealAuthority{
		Target: persistence.MatchingRunFenceSnapshot{
			Version: 2, GroupID: groupID, GroupRevision: "revision-1",
			Capture: canonical, CaptureDigest: hex.EncodeToString(digest[:]),
		},
		CapturedMetadata: captured,
	}
	prepared, err := prepareSealedReviewOnly(context.Background(), seal)
	if err != nil {
		t.Fatal(err)
	}
	inventory := prepared.FrozenInventory
	if inventory.Group.ID != persisted.GroupID || inventory.Group.Revision != persisted.GroupRevision || inventory.Group.Manual {
		t.Fatalf("frozen group = %#v, want captured identity and no inferred manual authority", inventory.Group)
	}
	if len(inventory.Files) != len(persisted.Members) || len(inventory.MemberFences) != len(persisted.Members) || len(inventory.Group.Members) != len(persisted.Members) {
		t.Fatalf("inventory sizes: files=%d fences=%d members=%d, frozen=%d", len(inventory.Files), len(inventory.MemberFences), len(inventory.Group.Members), len(persisted.Members))
	}
	if !reflect.DeepEqual(inventory.CapturedMetadata, captured) {
		t.Fatalf("captured metadata was not retained: got %#v", inventory.CapturedMetadata)
	}
	wantCaptures := make([]persistence.IncomingGroupingCapture, 0)
	for index, frozenMember := range persisted.Members {
		file := inventory.Files[index]
		if inventory.Group.Members[index] != frozenMember.VariantID || file.VariantID != frozenMember.VariantID {
			t.Fatalf("member %d identity was not retained", index)
		}
		if len(file.Locations) != len(frozenMember.Locations) || len(file.Locations) != 2 {
			t.Fatalf("member %s locations = %d, want all %d frozen locations", file.VariantID, len(file.Locations), len(frozenMember.Locations))
		}
		if file.CaptureAnalysisID == uuid.Nil || file.CaptureIdentity == "" || len(file.Tags) == 0 {
			t.Fatalf("member %s lost capture identity or raw tags: %#v", file.VariantID, file)
		}
		var wantTags map[string][]string
		if err := json.Unmarshal(captured[index].Tags, &wantTags); err != nil {
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
