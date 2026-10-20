//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

// Replacement evidence is intentionally exercised through the PostgreSQL
// journal API: unlike a pure validator test this verifies rejected transitions
// do not mutate the durable attempt journal.
func TestPublicationReplacementRejectsWrongGuardAndOutOfOrderPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, intent, staged := readyPublicationExposureFixture(t)
	repo := NewSetupManagerRepository(f.db)
	inputs, err := DecodePublicationInputs(func() json.RawMessage {
		attempt, readErr := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		return attempt.FrozenInputs
	}())
	if err != nil {
		t.Fatal(err)
	}
	currentGeneration := uuid.New()
	identity := PublicationFileIdentity{SizeBytes: int64Pointer(10), MTime: int64Pointer(20), SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}
	facts := inputs.Audio.AudioFacts
	inputs.IntentKind, inputs.Audio = "rewrite", nil
	inputs.InitialLyrics = nil
	inputs.Rewrite = &PublicationRewriteInput{CurrentGeneration: currentGeneration, RelativePath: intent.TargetRelativePath, Ownership: identity, AudioFacts: facts}
	rawInputs, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	ownership, _ := json.Marshal(PublicationCurrentOwnership{Version: 1, Identity: identity})
	audio, _ := json.Marshal(PublicationCurrentAudio{Version: 1, Facts: facts})
	if err := updatePublicationAttemptToRewrite(ctx, f, currentGeneration, rawInputs); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`INSERT INTO track_publication (position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at) VALUES (?,?,?,?,?::jsonb,?::jsonb,'{}'::jsonb,'{}'::jsonb,1,now())`, f.guard.ExpectedPositionID, f.guard.Epoch, currentGeneration, intent.TargetRelativePath, string(ownership), string(audio)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	intent.Expectation = "expected_current"
	if outcome, err := repo.BeginPublicationExposure(ctx, f.guard, intent, staged); err != nil || outcome != PublicationExposureRecorded {
		t.Fatalf("begin rewrite exposure = %q, %v", outcome, err)
	}
	if outcome, err := repo.BeginPublicationExposure(ctx, f.guard, intent, func() PublicationStageObservation {
		attempt, readErr := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		var entries []json.RawMessage
		if err := json.Unmarshal(attempt.MutationJournal, &entries); err != nil {
			t.Fatal(err)
		}
		var staged PublicationStageObservation
		for _, entry := range entries {
			var record struct {
				Kind        string                      `json:"kind"`
				Observation PublicationStageObservation `json:"observation"`
			}
			_ = json.Unmarshal(entry, &record)
			if record.Kind == "stage_observation" {
				staged = record.Observation
			}
		}
		return staged
	}()); err != nil || outcome != PublicationExposureAlreadyRecorded {
		t.Fatalf("exposure replay = %q, %v", outcome, err)
	}
	attempt, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	var replacement PublicationReplacementEvidence
	// This evidence is valid for the prepared rewrite; only its transition order
	// is invalid, so the rejection exercises the database journal guard.
	backupIdentity := copiedInputIdentity(t, attempt, identity.SHA256)
	replacement = PublicationReplacementEvidence{Version: 1, Kind: "publication_replacement", Guard: f.guard,
		PositionID: f.guard.ExpectedPositionID, Epoch: f.guard.Epoch,
		CandidateGeneration: f.guard.CandidateGeneration, ExpectedGeneration: func() *uuid.UUID { id := uuid.New(); return &id }(),
		Intent: intent, Staged: staged, Identity: identity, BackupIdentity: backupIdentity,
		BackupPath: "private/op/input.flac", From: "exposing", To: "backup_intent"}
	wrong := f.guard
	wrong.DeliveryToken += "-wrong"
	if outcome, err := repo.RecordPublicationReplacement(ctx, wrong, replacement); err == nil || outcome != PublicationReplacementStale {
		t.Fatalf("wrong guard = %q, %v; want stale error", outcome, err)
	}
	if outcome, err := repo.RecordPublicationReplacement(ctx, f.guard, replacement); err != nil || outcome != PublicationReplacementStale {
		t.Fatalf("out-of-order replacement = %q, %v; want stale", outcome, err)
	}
	after, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if string(after.MutationJournal) != string(attempt.MutationJournal) {
		t.Fatalf("rejected replacement changed journal: before=%s after=%s", attempt.MutationJournal, after.MutationJournal)
	}
}

// This exercises the successful rewrite path against a real current projection:
// every transition is accepted in order while the projection remains untouched.
func TestPublicationReplacementRecordsOrderedRewriteTransitionsPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, intent, staged := readyPublicationExposureFixture(t)
	repo := NewSetupManagerRepository(f.db)
	inputs, err := DecodePublicationInputs(func() json.RawMessage {
		attempt, readErr := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		return attempt.FrozenInputs
	}())
	if err != nil {
		t.Fatal(err)
	}
	currentGeneration := uuid.New()
	identity := PublicationFileIdentity{SizeBytes: int64Pointer(10), MTime: int64Pointer(20), SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}
	facts := inputs.Audio.AudioFacts
	inputs.IntentKind = "rewrite"
	inputs.Audio = nil
	inputs.InitialLyrics = nil
	inputs.Rewrite = &PublicationRewriteInput{CurrentGeneration: currentGeneration, RelativePath: intent.TargetRelativePath, Ownership: identity, AudioFacts: facts}
	rawInputs, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	ownership, _ := json.Marshal(PublicationCurrentOwnership{Version: 1, Identity: identity})
	audio, _ := json.Marshal(PublicationCurrentAudio{Version: 1, Facts: inputs.Rewrite.AudioFacts})
	if err := updatePublicationAttemptToRewrite(ctx, f, currentGeneration, rawInputs); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`INSERT INTO track_publication (position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at) VALUES (?,?,?,?,?::jsonb,?::jsonb,'{}'::jsonb,'{}'::jsonb,1,now())`, f.guard.ExpectedPositionID, f.guard.Epoch, currentGeneration, intent.TargetRelativePath, string(ownership), string(audio)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	intent.Expectation = "expected_current"
	if outcome, err := repo.BeginPublicationExposure(ctx, f.guard, intent, staged); err != nil || outcome != PublicationExposureRecorded {
		t.Fatalf("begin rewrite exposure = %q, %v", outcome, err)
	}
	preparedAttempt, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	before := new(TrackPublication)
	if err := f.db.NewSelect().Model(before).Where("position_id=?", f.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	backupIdentity := copiedInputIdentity(t, preparedAttempt, identity.SHA256)
	evidence := PublicationReplacementEvidence{Version: 1, Kind: "publication_replacement", From: "exposing", To: "backup_intent", Guard: f.guard, PositionID: f.guard.ExpectedPositionID, Epoch: f.guard.Epoch, CandidateGeneration: f.guard.CandidateGeneration, ExpectedGeneration: &currentGeneration, Intent: intent, Staged: staged, Identity: identity, BackupIdentity: backupIdentity, BackupPath: "private/op/input.flac"}
	for _, transition := range [][2]string{{"exposing", "backup_intent"}, {"backup_intent", "backup_ready"}, {"backup_ready", "replacement_intent"}, {"replacement_intent", "installed"}} {
		evidence.From, evidence.To = transition[0], transition[1]
		if evidence.To == "installed" {
			candidateIdentity := PublicationFileIdentity{SizeBytes: int64Pointer(*staged.Ownership.ExpectedSize), MTime: int64Pointer(789), SHA256: staged.PayloadSHA256}
			beforeRejectedAttempt, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
			if err != nil {
				t.Fatal(err)
			}
			beforeRejectedCurrent := new(TrackPublication)
			if err := f.db.NewSelect().Model(beforeRejectedCurrent).Where("position_id=?", f.guard.ExpectedPositionID).Scan(ctx); err != nil {
				t.Fatal(err)
			}
			wrongDigest := candidateIdentity
			wrongDigest.SHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			evidence.Identity = wrongDigest
			if outcome, err := repo.RecordPublicationReplacement(ctx, f.guard, evidence); outcome != PublicationReplacementStale || err == nil {
				t.Fatalf("installed replacement with wrong digest = %q, %v; want stale with a validation error", outcome, err)
			}
			assertRejectedReplacementUnchanged(t, ctx, f, repo, beforeRejectedAttempt, beforeRejectedCurrent)
			wrongSize := candidateIdentity
			wrongSize.SizeBytes = int64Pointer(*candidateIdentity.SizeBytes + 1)
			evidence.Identity = wrongSize
			if outcome, err := repo.RecordPublicationReplacement(ctx, f.guard, evidence); outcome != PublicationReplacementStale || err == nil {
				t.Fatalf("installed replacement with wrong size = %q, %v; want stale with a validation error", outcome, err)
			}
			assertRejectedReplacementUnchanged(t, ctx, f, repo, beforeRejectedAttempt, beforeRejectedCurrent)
			evidence.Identity = candidateIdentity
		}
		if outcome, err := repo.RecordPublicationReplacement(ctx, f.guard, evidence); err != nil || outcome != PublicationReplacementRecorded {
			t.Fatalf("replacement %s = %q, %v", evidence.To, outcome, err)
		}
	}
	after := new(TrackPublication)
	if err := f.db.NewSelect().Model(after).Where("position_id=?", f.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if before.Generation != after.Generation || before.RelativePath != after.RelativePath || string(before.Ownership) != string(after.Ownership) {
		t.Fatal("replacement journal transitions changed the durable current projection")
	}
}

func assertRejectedReplacementUnchanged(t *testing.T, ctx context.Context, f publicationExecutionFixture, repo *SetupManagerRepository, beforeAttempt *TrackPublicationAttempt, beforeCurrent *TrackPublication) {
	t.Helper()
	afterAttempt, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterAttempt.MutationJournal) != string(beforeAttempt.MutationJournal) {
		t.Fatal("rejected replacement appended to the mutation journal")
	}
	afterCurrent := new(TrackPublication)
	if err := f.db.NewSelect().Model(afterCurrent).Where("position_id=?", f.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if beforeCurrent.Generation != afterCurrent.Generation || beforeCurrent.RelativePath != afterCurrent.RelativePath || string(beforeCurrent.Ownership) != string(afterCurrent.Ownership) {
		t.Fatal("rejected replacement changed the durable current projection")
	}
}

func TestPublicationReplacementRejectsLostNamespaceReservationPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, intent, staged := readyPublicationExposureFixture(t)
	repo := NewSetupManagerRepository(f.db)
	attempt, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := DecodePublicationInputs(attempt.FrozenInputs)
	if err != nil {
		t.Fatal(err)
	}
	currentGeneration := uuid.New()
	identity := PublicationFileIdentity{SizeBytes: int64Pointer(10), MTime: int64Pointer(20), SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}
	facts := inputs.Audio.AudioFacts
	inputs.IntentKind = "rewrite"
	inputs.Audio = nil
	inputs.InitialLyrics = nil
	inputs.Rewrite = &PublicationRewriteInput{CurrentGeneration: currentGeneration, RelativePath: intent.TargetRelativePath, Ownership: identity, AudioFacts: facts}
	rawInputs, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	ownership, _ := json.Marshal(PublicationCurrentOwnership{Version: 1, Identity: identity})
	audio, _ := json.Marshal(PublicationCurrentAudio{Version: 1, Facts: inputs.Rewrite.AudioFacts})
	if err := updatePublicationAttemptToRewrite(ctx, f, currentGeneration, rawInputs); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`INSERT INTO track_publication (position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at) VALUES (?,?,?,?,?::jsonb,?::jsonb,'{}'::jsonb,'{}'::jsonb,1,now())`, f.guard.ExpectedPositionID, f.guard.Epoch, currentGeneration, intent.TargetRelativePath, string(ownership), string(audio)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	intent.Expectation = "expected_current"
	if outcome, err := repo.BeginPublicationExposure(ctx, f.guard, intent, staged); err != nil || outcome != PublicationExposureRecorded {
		t.Fatalf("begin rewrite exposure = %q, %v", outcome, err)
	}
	beforeAttempt, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	beforeCurrent := new(TrackPublication)
	if err := f.db.NewSelect().Model(beforeCurrent).Where("position_id=?", f.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`DELETE FROM publication_namespace_reservation WHERE position_id=? AND generation=?`, f.guard.ExpectedPositionID, f.guard.CandidateGeneration).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	backupIdentity := copiedInputIdentity(t, beforeAttempt, identity.SHA256)
	evidence := PublicationReplacementEvidence{Version: 1, Kind: "publication_replacement", From: "exposing", To: "backup_intent", Guard: f.guard, PositionID: f.guard.ExpectedPositionID, Epoch: f.guard.Epoch, CandidateGeneration: f.guard.CandidateGeneration, ExpectedGeneration: &currentGeneration, Intent: intent, Staged: staged, Identity: identity, BackupIdentity: backupIdentity, BackupPath: "private/op/input.flac"}
	if outcome, err := repo.RecordPublicationReplacement(ctx, f.guard, evidence); err != nil || outcome != PublicationReplacementStale {
		t.Fatalf("replacement with lost reservation = %q, %v; want stale", outcome, err)
	}
	afterAttempt, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	afterCurrent := new(TrackPublication)
	if err := f.db.NewSelect().Model(afterCurrent).Where("position_id=?", f.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if string(afterAttempt.MutationJournal) != string(beforeAttempt.MutationJournal) {
		t.Fatal("rejected replacement appended to the mutation journal")
	}
	if beforeCurrent.Generation != afterCurrent.Generation || beforeCurrent.RelativePath != afterCurrent.RelativePath || string(beforeCurrent.Ownership) != string(afterCurrent.Ownership) {
		t.Fatal("rejected replacement changed the durable current projection")
	}
}

func updatePublicationAttemptToRewrite(ctx context.Context, f publicationExecutionFixture, generation uuid.UUID, frozenInputs json.RawMessage) error {
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE track_publication_attempt SET expected_generation=?, intent_kind='rewrite', frozen_inputs=?::jsonb WHERE operation_id=?`, generation, string(frozenInputs), f.guard.OperationID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE operation SET input_snapshot=jsonb_set(input_snapshot, '{intent_kind}', '"rewrite"'::jsonb) WHERE id=?`, f.guard.OperationID); err != nil {
		return err
	}
	return tx.Commit()
}

func int64Pointer(value int64) *int64 { return &value }

func copiedInputIdentity(t *testing.T, attempt *TrackPublicationAttempt, frozenHash string) PublicationFileIdentity {
	t.Helper()
	var entries []json.RawMessage
	if err := json.Unmarshal(attempt.MutationJournal, &entries); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		var record PublicationPreparationRecord
		if json.Unmarshal(entry, &record) == nil && record.Step == "input_ready" && record.CopiedInput != nil {
			identity := *record.CopiedInput
			identity.SHA256 = frozenHash
			return identity
		}
	}
	t.Fatal("prepared copied-input identity is missing")
	return PublicationFileIdentity{}
}
