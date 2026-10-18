package persistence

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestExactCleanupJournalParsesWitnessValuesAndResults(t *testing.T) {
	size, mtime := int64(42), int64(1234)
	witness := PublicationFirstAbsentCleanupWitness{
		PrivateDirRel: "private/attempt",
		InputRel:      "private/attempt/input.flac",
		Input: PublicationFileIdentity{
			SizeBytes: &size,
			MTime:     &mtime,
			SHA256:    "abc123",
		},
	}
	// Journal decoding creates fresh pointer values, so equality must compare
	// identity contents rather than pointer addresses.
	encodedSize, encodedMTime := int64(42), int64(1234)
	journalWitness := witness
	journalWitness.Input.SizeBytes = &encodedSize
	journalWitness.Input.MTime = &encodedMTime
	records := []publicationFirstAbsentCleanupRecord{
		{Version: publicationFirstAbsentCleanupVersion, Kind: "publication_first_absent_cleanup", Step: "input_intent", Witness: journalWitness, Name: witness.InputRel},
		{Version: publicationFirstAbsentCleanupVersion, Kind: "publication_first_absent_cleanup", Step: "input_result", Witness: journalWitness, Name: witness.InputRel, Result: "removed"},
		{Version: publicationFirstAbsentCleanupVersion, Kind: "publication_first_absent_cleanup", Step: "directory_intent", Witness: journalWitness, Name: witness.PrivateDirRel},
		{Version: publicationFirstAbsentCleanupVersion, Kind: "publication_first_absent_cleanup", Step: "directory_result", Witness: journalWitness, Name: witness.PrivateDirRel, Result: "already_absent"},
	}
	entries := []json.RawMessage{json.RawMessage(`{"kind":"publication_commit"}`)}
	for _, record := range records {
		encoded, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, encoded)
	}
	if !exactCleanupJournal(entries, PublicationDeliveryGuard{}, witness, len(records)) {
		t.Fatal("expected valid cleanup journal to match")
	}
	if !cleanupResultRecorded(entries, "input", witness.InputRel) || !cleanupResultRecorded(entries, "directory", witness.PrivateDirRel) {
		t.Fatal("expected valid cleanup results to be recorded")
	}
	changedSHA := witness
	changedSHA.Input.SHA256 = "different"
	if exactCleanupJournal(entries, PublicationDeliveryGuard{}, changedSHA, len(records)) {
		t.Fatal("witness with different SHA-256 unexpectedly matched")
	}
}

func TestPreparationWitnessMatchesCopiedInputFactsNotSourceMTime(t *testing.T) {
	op, claim, owner := uuid.New(), uuid.New(), uuid.New()
	sourceSize, sourceMTime := int64(42), int64(1000)
	copiedSize, copiedMTime := int64(42), int64(2000)
	directory := PublicationDirectoryIdentity{Platform: "darwin", Volume: "1", FileID: "2"}
	guard := PublicationDeliveryGuard{OperationID: op, ClaimUUID: claim}
	record := PublicationPreparationRecord{
		Version: PublicationPreparationVersion, Kind: "publication_preparation", Step: "input_ready",
		OperationID: op, ClaimUUID: claim, OwnerToken: owner, PrivateDirRel: "private",
		InputRel: "private/input.flac", OutputRel: "private/output.flac", ParentIdentity: directory,
		DirectoryIdentity: &directory, InputSize: &sourceSize, InputMTime: &sourceMTime,
		CopiedInput: &PublicationFileIdentity{SizeBytes: &copiedSize, MTime: &copiedMTime},
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	witness := PublicationFirstAbsentCleanupWitness{
		PrivateDirRel: "private", InputRel: "private/input.flac", Directory: directory,
		Input: PublicationFileIdentity{SizeBytes: &copiedSize, MTime: &copiedMTime},
	}
	if !preparationWitnessMatches([]json.RawMessage{encoded}, guard, witness) {
		t.Fatal("copied-input facts did not authorize cleanup when source mtime differed")
	}
	legacy := record
	legacy.CopiedInput = nil
	legacyJSON, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if preparationWitnessMatches([]json.RawMessage{legacyJSON}, guard, witness) {
		t.Fatal("legacy input_ready record without copied-input evidence authorized cleanup")
	}
}

func TestExactCleanupJournalRejectsMalformedResultsAndIntents(t *testing.T) {
	size, mtime := int64(42), int64(1234)
	witness := PublicationFirstAbsentCleanupWitness{
		PrivateDirRel: "private/attempt",
		InputRel:      "private/attempt/input.flac",
		Input:         PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: "abc123"},
	}
	for _, test := range []struct {
		name   string
		step   string
		result string
	}{
		{name: "unknown result", step: "input_result", result: "ignored"},
		{name: "result on intent", step: "input_intent", result: "removed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			name := witness.InputRel
			if test.step == "directory_result" || test.step == "directory_intent" {
				name = witness.PrivateDirRel
			}
			record := publicationFirstAbsentCleanupRecord{
				Version: publicationFirstAbsentCleanupVersion, Kind: "publication_first_absent_cleanup",
				Step: test.step, Witness: witness, Name: name, Result: test.result,
			}
			encoded, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			entries := []json.RawMessage{json.RawMessage(`{"kind":"publication_commit"}`), encoded}
			if exactCleanupJournal(entries, PublicationDeliveryGuard{}, witness, 1) {
				t.Fatal("malformed cleanup suffix unexpectedly matched")
			}
			if cleanupResultRecorded(entries, "input", witness.InputRel) {
				t.Fatal("malformed result unexpectedly allowed continuation")
			}
		})
	}
}
