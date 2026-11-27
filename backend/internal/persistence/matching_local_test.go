package persistence

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestMatchingLocalReceiptIntentHashReplaysSameCanonicalIntent(t *testing.T) {
	groupID := uuid.New()
	releaseID := uuid.New()
	memberA := uuid.MustParse("00000000-0000-4000-8000-000000000001")
	memberB := uuid.MustParse("00000000-0000-4000-8000-000000000002")
	trackA := uuid.MustParse("00000000-0000-4000-8000-000000000003")
	trackB := uuid.MustParse("00000000-0000-4000-8000-000000000004")
	first, err := canonicalMatchingLocalIntent(MatchingLocalIntent{
		ProviderReleaseID: releaseID, ExpectedGroupRevision: "group-r1", ExpectedAuthorityRevision: 2,
		Assignments: []MatchingLocalAssignment{{FileMemberID: memberB, ProviderTrackID: trackB}, {FileMemberID: memberA, ProviderTrackID: trackA}},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := canonicalMatchingLocalIntent(MatchingLocalIntent{
		ProviderReleaseID: releaseID, ExpectedGroupRevision: "group-r1", ExpectedAuthorityRevision: 2,
		Assignments: []MatchingLocalAssignment{{FileMemberID: memberA, ProviderTrackID: trackA}, {FileMemberID: memberB, ProviderTrackID: trackB}},
	})
	if err != nil {
		t.Fatal(err)
	}
	firstHash, err := matchingLocalReceiptIntentHash(groupID, first, "preview-digest")
	if err != nil {
		t.Fatal(err)
	}
	secondHash, err := matchingLocalReceiptIntentHash(groupID, second, "preview-digest")
	if err != nil {
		t.Fatal(err)
	}
	if firstHash != secondHash {
		t.Fatalf("canonical same intent hashes differ: %q != %q", firstHash, secondHash)
	}
	replayedHash, err := matchingLocalReceiptIntentHash(groupID, first, "preview-digest")
	if err != nil {
		t.Fatal(err)
	}
	if replayedHash != firstHash {
		t.Fatalf("same confirmation intent did not replay: %q != %q", replayedHash, firstHash)
	}
}

func TestMatchingLocalReceiptIntentHashChangesWithIntentOrDigest(t *testing.T) {
	groupID := uuid.New()
	intent, err := canonicalMatchingLocalIntent(MatchingLocalIntent{
		ProviderReleaseID: uuid.New(), ExpectedGroupRevision: "group-r1", ExpectedAuthorityRevision: 2,
		Assignments: []MatchingLocalAssignment{{
			FileMemberID:    uuid.MustParse("00000000-0000-4000-8000-000000000001"),
			ProviderTrackID: uuid.MustParse("00000000-0000-4000-8000-000000000002"),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	base, err := matchingLocalReceiptIntentHash(groupID, intent, "preview-digest")
	if err != nil {
		t.Fatal(err)
	}
	changedAuthority, err := matchingLocalReceiptIntentHash(groupID, MatchingLocalIntent{
		ProviderReleaseID: intent.ProviderReleaseID, ExpectedGroupRevision: intent.ExpectedGroupRevision,
		ExpectedAuthorityRevision: intent.ExpectedAuthorityRevision + 1, Assignments: intent.Assignments,
	}, "preview-digest")
	if err != nil {
		t.Fatal(err)
	}
	changedDigest, err := matchingLocalReceiptIntentHash(groupID, intent, "other-preview-digest")
	if err != nil {
		t.Fatal(err)
	}
	changedGroup, err := matchingLocalReceiptIntentHash(uuid.New(), intent, "preview-digest")
	if err != nil {
		t.Fatal(err)
	}
	if changedAuthority == base || changedDigest == base || changedGroup == base {
		t.Fatalf("changed confirmation evidence reused a receipt hash: base=%q", base)
	}
}

func TestMatchingLocalIntentJSONHasNoRetiredLegacyFields(t *testing.T) {
	intent, err := canonicalMatchingLocalIntent(MatchingLocalIntent{
		ProviderReleaseID: uuid.New(), ExpectedGroupRevision: "group-r1",
		Assignments: []MatchingLocalAssignment{{
			FileMemberID:    uuid.MustParse("00000000-0000-4000-8000-000000000001"),
			ProviderTrackID: uuid.MustParse("00000000-0000-4000-8000-000000000002"),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(intent)
	if err != nil {
		t.Fatal(err)
	}
	for _, legacy := range []string{`"local_release_id"`, `"release_title"`, `"release_artists"`, `"tracks"`} {
		if strings.Contains(string(encoded), legacy) {
			t.Fatalf("canonical intent still serializes %s: %s", legacy, encoded)
		}
	}
}
