package persistence

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestSameCandidateRebuildPlanAcrossJSONRoundTrip(t *testing.T) {
	size, mtime := int64(42), int64(1700000000)
	intent := PublicationCandidateRebuildRecord{
		Version: PublicationCandidateRebuildVersion, Kind: "publication_candidate_rebuild", Step: "intent",
		Guard: PublicationDeliveryGuard{OperationID: uuid.New(), ClaimUUID: uuid.New()}, OwnerToken: uuid.New(),
		PrivateDirRel: "private/op", InputRel: "private/op/input.flac", OutputRel: "private/op/output.flac",
		DirectoryIdentity: PublicationDirectoryIdentity{Platform: "darwin", Volume: "disk1", FileID: "dir-1"},
		Candidate:         &PublicationFileIdentity{},
		Input:             &PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: "0123456789abcdef"},
	}
	encoded, err := json.Marshal(intent)
	if err != nil {
		t.Fatal(err)
	}
	var decodedIntent PublicationCandidateRebuildRecord
	if err := json.Unmarshal(encoded, &decodedIntent); err != nil {
		t.Fatal(err)
	}
	complete := decodedIntent
	complete.Step = "complete"
	complete.Candidate = &PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: "fedcba9876543210"}
	encoded, err = json.Marshal(complete)
	if err != nil {
		t.Fatal(err)
	}
	var decodedComplete PublicationCandidateRebuildRecord
	if err := json.Unmarshal(encoded, &decodedComplete); err != nil {
		t.Fatal(err)
	}
	if !sameCandidateRebuildPlan(decodedIntent, decodedComplete) {
		t.Fatal("intent and completion with the same plan differ after JSON round-trip")
	}

	mutations := map[string]func(*PublicationCandidateRebuildRecord){
		"size": func(record *PublicationCandidateRebuildRecord) {
			value := *record.Input.SizeBytes + 1
			record.Input.SizeBytes = &value
		},
		"mtime": func(record *PublicationCandidateRebuildRecord) {
			value := *record.Input.MTime + 1
			record.Input.MTime = &value
		},
		"hash": func(record *PublicationCandidateRebuildRecord) {
			record.Input.SHA256 += "x"
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			mismatch := decodedComplete
			input := clonePublicationFileIdentity(*decodedComplete.Input)
			mismatch.Input = &input
			mutate(&mismatch)
			if sameCandidateRebuildPlan(decodedIntent, mismatch) {
				t.Fatal("plan unexpectedly matched after input identity changed")
			}
		})
	}
}
