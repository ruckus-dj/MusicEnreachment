//go:build integration

package persistence

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestMatchingMetadataCaptureDigestNormalizesJSONBTagKeyOrder(t *testing.T) {
	memberID, workID := uuid.New(), uuid.New()
	left := []MatchingAcquisitionCapturedMember{{
		MemberID: memberID, WorkID: workID,
		Tags: json.RawMessage(`{"ALBUM":"Album","ARTIST":"Artist","TITLE":"Title","TRACKNUMBER":["1","2"]}`),
	}}
	right := []MatchingAcquisitionCapturedMember{{
		MemberID: memberID, WorkID: workID,
		Tags: json.RawMessage(`{"TRACKNUMBER":["1","2"],"TITLE":"Title","ARTIST":"Artist","ALBUM":"Album"}`),
	}}
	leftDigest, err := matchingMetadataCaptureDigest(left)
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := matchingMetadataCaptureDigest(right)
	if err != nil {
		t.Fatal(err)
	}
	if leftDigest != rightDigest {
		t.Fatalf("equivalent JSONB tag objects have different capture digests: %s != %s", leftDigest, rightDigest)
	}
	equal, err := equalCapturedMatchingMembers(left, right)
	if err != nil || !equal {
		t.Fatalf("equivalent captures compare equal: equal=%v err=%v", equal, err)
	}
	right[0].Tags = json.RawMessage(`{"TRACKNUMBER":["2","1"],"TITLE":"Title","ARTIST":"Artist","ALBUM":"Album"}`)
	equal, err = equalCapturedMatchingMembers(left, right)
	if err != nil || equal {
		t.Fatalf("tag value array order is significant: equal=%v err=%v", equal, err)
	}
}

func TestMatchingMetadataCaptureNormalizationRejectsMalformedTags(t *testing.T) {
	members := []MatchingAcquisitionCapturedMember{{Tags: json.RawMessage(`{"ALBUM":`)}}
	if _, err := matchingMetadataCaptureDigest(members); err == nil {
		t.Fatal("malformed captured tags produced a digest")
	}
	if equal, err := equalCapturedMatchingMembers(members, members); err == nil || equal {
		t.Fatalf("malformed captured tags compared equal: equal=%v err=%v", equal, err)
	}
}
