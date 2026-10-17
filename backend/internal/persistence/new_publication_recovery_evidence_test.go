package persistence

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

func recoveryEvidenceFixture() PublicationRecoverySnapshot {
	operation, position, epoch, generation := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	claim := PublicationExecutionClaim{OperationID: operation, ApplicationAttempt: 1, RiverJobID: 1, RiverAttempt: 1, ClaimUUID: uuid.New(), BootUUID: uuid.New(), DeliveryToken: "delivery", Epoch: epoch, State: "quiescent"}
	return PublicationRecoverySnapshot{
		Attempt:   TrackPublicationAttempt{OperationID: operation, PositionID: position, Epoch: epoch, CandidateGeneration: generation, IntentKind: "audio", Phase: "preparing", MutationJournal: json.RawMessage(`[]`)},
		Operation: PublicationCommitOperationSnapshot{State: "failed", Stage: "preparing"}, Claim: claim, OperationID: operation, ApplicationAttempt: 1, RiverJobID: 1, CurrentEpoch: epoch, CurrentKnown: true,
	}
}

func TestAssessPublicationRecoveryEvidenceAcceptsRealJournalFrontiers(t *testing.T) {
	for _, test := range []struct {
		name  string
		count int
		phase string
		want  string
	}{
		{"preparation intent", 1, "preparing", "prep_1"},
		{"directory created", 2, "preparing", "prep_2"},
		{"input ready", 3, "preparing", "prep_3"},
		{"staged", 4, "staged", "staged"},
		{"validated", 5, "validated", "validated"},
		{"exposing", 6, "exposing", "exposing"},
		{"candidate installed", 7, "exposing", "candidate_installed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			attempt, operation, guard := commitEvidenceFixture(t)
			var entries []json.RawMessage
			if err := json.Unmarshal(attempt.MutationJournal, &entries); err != nil {
				t.Fatal(err)
			}
			attempt.MutationJournal, _ = json.Marshal(entries[:test.count])
			attempt.Phase = test.phase
			operation.Stage = test.phase
			snapshot := recoveryEvidenceFromFixture(attempt, operation, guard)
			assessment, err := AssessPublicationRecoveryEvidence(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if assessment.JournalFrontier != test.want || len(assessment.RequiredObservations) == 0 || assessment.RequiredObservations[0] != "old_execution_quiescence" {
				t.Fatalf("assessment = %#v", assessment)
			}
		})
	}
}

func TestAssessPublicationRecoveryEvidenceRequiresOriginalBootClaim(t *testing.T) {
	attempt, operation, guard := commitEvidenceFixture(t)
	snapshot := recoveryEvidenceFromFixture(attempt, operation, guard)
	snapshot.Claim.BootUUID = uuid.New()
	if _, err := AssessPublicationRecoveryEvidence(snapshot); err == nil {
		t.Fatal("journal from a different boot claim was accepted")
	}
}

func TestAssessPublicationRecoveryEvidenceRequiresOperationIdentity(t *testing.T) {
	snapshot := recoveryEvidenceFixture()
	for _, mutate := range []func(*PublicationRecoverySnapshot){
		func(s *PublicationRecoverySnapshot) { s.OperationID = uuid.New() },
		func(s *PublicationRecoverySnapshot) { s.ApplicationAttempt++ },
		func(s *PublicationRecoverySnapshot) { s.RiverJobID++ },
	} {
		changed := snapshot
		mutate(&changed)
		if _, err := AssessPublicationRecoveryEvidence(changed); err == nil {
			t.Fatal("mismatched operation identity was accepted")
		}
	}
}

func TestAssessPublicationRecoveryEvidenceRequiresUniqueCommittedWitness(t *testing.T) {
	assets := json.RawMessage(`{"version":1,"cover":{"state":"absent"},"lyrics":{"state":"absent"}}`)
	attempt, operation, guard := publicationCommitEvidenceV2Fixture(t, PublicationInputsVersion2, publication.QualityPolicyVersion, assets, publication.QualityPolicyVersion)
	projection, err := AssessFirstPublicationCommit(attempt, operation, nil, guard)
	if err != nil {
		t.Fatal(err)
	}
	validatedAt := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	witness := firstPublicationCommitWitness{
		Version: firstPublicationCommitVersion, Kind: "publication_commit", Guard: guard,
		PositionID: attempt.PositionID, Epoch: attempt.Epoch, CandidateGeneration: attempt.CandidateGeneration,
		RelativePath: projection.TargetRelativePath, Ownership: projection.Ownership, Audio: projection.CurrentAudio,
		Metadata: projection.Metadata, Assets: projection.ActualAssets, QualityPolicyVersion: projection.QualityPolicyVersion,
		ValidatedAt: validatedAt,
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(attempt.MutationJournal, &entries); err != nil {
		t.Fatal(err)
	}
	commit, err := json.Marshal(witness)
	if err != nil {
		t.Fatal(err)
	}
	entries = append(entries, commit)
	attempt.MutationJournal, err = json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	attempt.Phase = "committed"
	attempt.FrozenInputs = nil
	operation.Stage = "committed"
	current := TrackPublication{
		PositionID: attempt.PositionID, Epoch: attempt.Epoch, Generation: attempt.CandidateGeneration,
		RelativePath: witness.RelativePath, Ownership: mustPublicationCommitJSON(t, witness.Ownership),
		ActualAudio: mustPublicationCommitJSON(t, witness.Audio), ActualMetadata: mustPublicationCommitJSON(t, witness.Metadata),
		ActualAssets: mustPublicationCommitJSON(t, witness.Assets), QualityPolicyVersion: witness.QualityPolicyVersion, ValidatedAt: validatedAt,
	}
	snapshot := recoveryEvidenceFromFixture(attempt, operation, guard)
	snapshot.Current = &current
	assessment, err := AssessPublicationRecoveryEvidence(snapshot)
	if err != nil || assessment.JournalFrontier != "committed" {
		t.Fatalf("committed assessment = %#v, %v", assessment, err)
	}

	mutations := []struct {
		name   string
		change func(*TrackPublication, *PublicationRecoverySnapshot)
	}{
		{"position", func(row *TrackPublication, _ *PublicationRecoverySnapshot) { row.PositionID = uuid.New() }},
		{"epoch", func(row *TrackPublication, _ *PublicationRecoverySnapshot) { row.Epoch = uuid.New() }},
		{"generation", func(row *TrackPublication, _ *PublicationRecoverySnapshot) { row.Generation = uuid.New() }},
		{"path", func(row *TrackPublication, _ *PublicationRecoverySnapshot) { row.RelativePath += ".other" }},
		{"timestamp", func(row *TrackPublication, _ *PublicationRecoverySnapshot) {
			row.ValidatedAt = row.ValidatedAt.Add(time.Second)
		}},
		{"ownership", func(row *TrackPublication, _ *PublicationRecoverySnapshot) { row.Ownership = json.RawMessage(`{}`) }},
		{"audio", func(row *TrackPublication, _ *PublicationRecoverySnapshot) { row.ActualAudio = json.RawMessage(`{}`) }},
		{"metadata", func(row *TrackPublication, _ *PublicationRecoverySnapshot) {
			row.ActualMetadata = json.RawMessage(`{}`)
		}},
		{"assets", func(row *TrackPublication, _ *PublicationRecoverySnapshot) { row.ActualAssets = json.RawMessage(`{}`) }},
		{"policy", func(row *TrackPublication, _ *PublicationRecoverySnapshot) { row.QualityPolicyVersion++ }},
		{"frozen inputs", func(_ *TrackPublication, snapshot *PublicationRecoverySnapshot) {
			snapshot.Attempt.FrozenInputs = json.RawMessage(`null`)
		}},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			changed := snapshot
			row := current
			changed.Current = &row
			mutation.change(&row, &changed)
			if _, err := AssessPublicationRecoveryEvidence(changed); err == nil {
				t.Fatal("mismatched committed witness was accepted")
			}
		})
	}
}

func recoveryEvidenceFromFixture(attempt TrackPublicationAttempt, operation PublicationCommitOperationSnapshot, guard PublicationDeliveryGuard) PublicationRecoverySnapshot {
	return PublicationRecoverySnapshot{
		Attempt: attempt, Operation: operation,
		Claim:       PublicationExecutionClaim{OperationID: guard.OperationID, ApplicationAttempt: guard.ApplicationAttempt, RiverJobID: guard.RiverJobID, RiverAttempt: guard.RiverAttempt, ClaimUUID: guard.ClaimUUID, BootUUID: guard.BootUUID, DeliveryToken: guard.DeliveryToken, Epoch: guard.Epoch, State: "executing"},
		OperationID: guard.OperationID, ApplicationAttempt: guard.ApplicationAttempt, RiverJobID: guard.RiverJobID, CurrentEpoch: attempt.Epoch, CurrentKnown: true,
	}
}

func TestAssessPublicationRecoveryEvidenceRequiresKnownStatesAndQuiescence(t *testing.T) {
	for _, state := range []string{"executing", "recovery_required", "quiescent"} {
		t.Run(state, func(t *testing.T) {
			snapshot := recoveryEvidenceFixture()
			snapshot.Claim.State = state
			assessment, err := AssessPublicationRecoveryEvidence(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if assessment.Classification != "evidence_consistency_only" || len(assessment.RequiredObservations) == 0 || assessment.RequiredObservations[0] != "old_execution_quiescence" {
				t.Fatalf("unexpected assessment: %#v", assessment)
			}
		})
	}
	for _, state := range []string{"", "unknown"} {
		snapshot := recoveryEvidenceFixture()
		snapshot.Claim.State = state
		if _, err := AssessPublicationRecoveryEvidence(snapshot); err == nil {
			t.Fatalf("unsupported claim state %q accepted", state)
		}
	}
	for _, state := range []string{"", "unknown"} {
		snapshot := recoveryEvidenceFixture()
		snapshot.Operation.State = state
		if _, err := AssessPublicationRecoveryEvidence(snapshot); err == nil {
			t.Fatalf("unsupported operation state %q accepted", state)
		}
	}
}

func TestAssessPublicationRecoveryEvidenceDistinguishesUnknownCurrentFromAbsent(t *testing.T) {
	snapshot := recoveryEvidenceFixture()
	snapshot.CurrentKnown = false
	if _, err := AssessPublicationRecoveryEvidence(snapshot); err == nil {
		t.Fatal("unknown current row accepted as absence")
	}
	snapshot.CurrentKnown = true
	if _, err := AssessPublicationRecoveryEvidence(snapshot); err != nil {
		t.Fatalf("known absence rejected: %v", err)
	}
}

func TestPublicationRecoveryJournalRejectsDuplicatePreparation(t *testing.T) {
	snapshot := recoveryEvidenceFixture()
	r := PublicationPreparationRecord{Version: PublicationPreparationVersion, Kind: "publication_preparation", Step: "intent", OperationID: snapshot.Attempt.OperationID, ClaimUUID: snapshot.Claim.ClaimUUID, OwnerToken: uuid.New(), PrivateDirRel: "private", InputRel: "private/input", OutputRel: "private/output", ParentIdentity: PublicationDirectoryIdentity{Platform: "darwin", Volume: "vol", FileID: "parent"}}
	entry, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	journal, _ := json.Marshal([]json.RawMessage{entry, entry})
	snapshot.Attempt.MutationJournal = journal
	if _, err := AssessPublicationRecoveryEvidence(snapshot); err == nil {
		t.Fatal("duplicate preparation accepted")
	}
}

func TestPublicationRecoveryJournalPreparationFrontiersAreOrdered(t *testing.T) {
	for count, want := range map[int]string{0: "empty", 1: "prep_1", 2: "prep_2", 3: "prep_3"} {
		t.Run(want, func(t *testing.T) {
			snapshot := recoveryEvidenceFixture()
			owner := uuid.New()
			parent := PublicationDirectoryIdentity{Platform: "darwin", Volume: "vol", FileID: "parent"}
			dir := PublicationDirectoryIdentity{Platform: "darwin", Volume: "vol", FileID: "private"}
			size := int64(42)
			mtime := time.Unix(1, 0).UnixMicro()
			records := []PublicationPreparationRecord{
				{Version: PublicationPreparationVersion, Kind: "publication_preparation", Step: "intent", OperationID: snapshot.Attempt.OperationID, ClaimUUID: snapshot.Claim.ClaimUUID, OwnerToken: owner, PrivateDirRel: "private", InputRel: "private/input", OutputRel: "private/output", ParentIdentity: parent},
				{Version: PublicationPreparationVersion, Kind: "publication_preparation", Step: "directory_created", OperationID: snapshot.Attempt.OperationID, ClaimUUID: snapshot.Claim.ClaimUUID, OwnerToken: owner, PrivateDirRel: "private", InputRel: "private/input", OutputRel: "private/output", ParentIdentity: parent, DirectoryIdentity: &dir},
				{Version: PublicationPreparationVersion, Kind: "publication_preparation", Step: "input_ready", OperationID: snapshot.Attempt.OperationID, ClaimUUID: snapshot.Claim.ClaimUUID, OwnerToken: owner, PrivateDirRel: "private", InputRel: "private/input", OutputRel: "private/output", ParentIdentity: parent, DirectoryIdentity: &dir, InputSize: &size, InputMTime: &mtime},
			}
			entries := make([]json.RawMessage, 0, count)
			for _, record := range records[:count] {
				entry, err := json.Marshal(record)
				if err != nil {
					t.Fatal(err)
				}
				entries = append(entries, entry)
			}
			snapshot.Attempt.MutationJournal, _ = json.Marshal(entries)
			got, err := publicationRecoveryJournalFrontier(snapshot.Attempt, snapshot.Claim)
			if err != nil || got != want {
				t.Fatalf("frontier = %q, %v; want %q", got, err, want)
			}
		})
	}
}
