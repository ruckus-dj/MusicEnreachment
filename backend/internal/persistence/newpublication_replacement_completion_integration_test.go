//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

func TestSettleReplacementPublicationPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, intent, staged := readyPublicationExposureFixture(t)
	repo := NewSetupManagerRepository(f.db)
	staged.ActualAssets = json.RawMessage(`{"version":1,"cover":{"state":"absent"},"lyrics":{"state":"absent"}}`)
	staged.QualityPolicyVersion = publication.QualityPolicyVersion
	staged.ActualMetadata = json.RawMessage(`{"version":1,"tags":{"TITLE":["replacement"]}}`)
	attempt, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	var journal []json.RawMessage
	if err := json.Unmarshal(attempt.MutationJournal, &journal); err != nil {
		t.Fatal(err)
	}
	for i, raw := range journal {
		var record map[string]json.RawMessage
		if err := json.Unmarshal(raw, &record); err != nil {
			t.Fatal(err)
		}
		var kind string
		if err := json.Unmarshal(record["kind"], &kind); err != nil {
			t.Fatal(err)
		}
		if kind == "stage_observation" {
			record["observation"], err = json.Marshal(staged)
			if err != nil {
				t.Fatal(err)
			}
			journal[i], err = json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
		} else if kind == "publication_preparation" {
			var preparation PublicationPreparationRecord
			if err := json.Unmarshal(raw, &preparation); err != nil {
				t.Fatal(err)
			}
			if preparation.Step == "input_ready" && preparation.CopiedInput != nil {
				oldSize := *staged.Ownership.ExpectedSize + 1
				preparation.InputSize = int64Pointer(oldSize)
				preparation.CopiedInput.SizeBytes = int64Pointer(oldSize)
				journal[i], err = json.Marshal(preparation)
				if err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	attempt.MutationJournal, err = json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := DecodePublicationInputs(attempt.FrozenInputs)
	if err != nil {
		t.Fatal(err)
	}
	inputs.Version, inputs.QualityPolicyVersion = PublicationInputsVersion, publication.QualityPolicyVersion
	currentGeneration := uuid.New()
	oldSize := *staged.Ownership.ExpectedSize + 1
	inputs.Audio.SizeBytes = oldSize
	identity := PublicationFileIdentity{SizeBytes: int64Pointer(oldSize), MTime: int64Pointer(20), SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}
	facts := inputs.Audio.AudioFacts
	inputs.IntentKind, inputs.Audio = "rewrite", nil
	inputs.InitialLyrics = nil
	inputs.Rewrite = &PublicationRewriteInput{CurrentGeneration: currentGeneration, RelativePath: intent.TargetRelativePath, Ownership: identity, AudioFacts: facts}
	frozen, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`UPDATE track_publication_attempt SET mutation_journal=?::jsonb WHERE operation_id=?`, string(attempt.MutationJournal), f.guard.OperationID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := updatePublicationAttemptToRewrite(ctx, f, currentGeneration, frozen); err != nil {
		t.Fatal(err)
	}
	ownership := mustJSON(PublicationCurrentOwnership{Version: 1, Identity: identity})
	audio := mustJSON(PublicationCurrentAudio{Version: 1, Facts: inputs.Rewrite.AudioFacts})
	if _, err := f.db.NewRaw(`INSERT INTO track_publication (position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at) VALUES (?,?,?,?,?::jsonb,?::jsonb,?::jsonb,?::jsonb,1,now())`, f.guard.ExpectedPositionID, f.guard.Epoch, currentGeneration, intent.TargetRelativePath, string(ownership), string(audio), `{"version":1,"tags":{"TITLE":["old"]}}`, `{"version":1,"cover":{"state":"absent"},"lyrics":{"state":"absent"}}`).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	intent.Expectation = "expected_current"
	if outcome, err := repo.BeginPublicationExposure(ctx, f.guard, intent, staged); err != nil || outcome != PublicationExposureRecorded {
		t.Fatalf("exposure = %q, %v", outcome, err)
	}
	prepared, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	var preparations []PublicationPreparationRecord
	if err := json.Unmarshal(prepared.MutationJournal, &journal); err != nil {
		t.Fatal(err)
	}
	for _, raw := range journal {
		var record PublicationPreparationRecord
		if json.Unmarshal(raw, &record) == nil && record.Kind == "publication_preparation" {
			preparations = append(preparations, record)
		}
	}
	var copied PublicationFileIdentity
	var plan, created PublicationPreparationRecord
	for _, record := range preparations {
		switch record.Step {
		case "intent":
			plan = record
		case "directory_created":
			created = record
		case "input_ready":
			if record.CopiedInput != nil {
				copied = *record.CopiedInput
			}
		}
	}
	if plan.PrivateDirRel == "" || created.DirectoryIdentity == nil || copied.SizeBytes == nil || copied.MTime == nil {
		t.Fatal("preparation journal did not contain authoritative private-directory and backup observations")
	}
	backupRel := plan.InputRel
	backupIdentity := copied
	backupIdentity.SHA256 = identity.SHA256
	evidence := PublicationReplacementEvidence{Version: 1, Kind: "publication_replacement", Guard: f.guard, PositionID: f.guard.ExpectedPositionID, Epoch: f.guard.Epoch, CandidateGeneration: f.guard.CandidateGeneration, ExpectedGeneration: &currentGeneration, Intent: intent, Staged: staged, Identity: identity, BackupIdentity: backupIdentity, BackupPath: backupRel}
	for _, transition := range [][2]string{{"exposing", "backup_intent"}, {"backup_intent", "backup_ready"}, {"backup_ready", "replacement_intent"}, {"replacement_intent", "installed"}} {
		evidence.From, evidence.To = transition[0], transition[1]
		if transition[1] == "installed" {
			evidence.Identity = PublicationFileIdentity{SizeBytes: int64Pointer(*staged.Ownership.ExpectedSize), MTime: int64Pointer(789), SHA256: staged.PayloadSHA256}
		}
		if outcome, err := repo.RecordPublicationReplacement(ctx, f.guard, evidence); err != nil || outcome != PublicationReplacementRecorded {
			t.Fatalf("replacement %s = %q, %v", transition[1], outcome, err)
		}
	}
	if committed, err := repo.CommitReplacementPublication(ctx, f.guard); err != nil || !committed {
		t.Fatalf("commit = %t, %v", committed, err)
	}
	if committed, err := repo.CommitReplacementPublication(ctx, f.guard); err != nil || !committed {
		t.Fatalf("commit replay before cleanup = %t, %v", committed, err)
	}
	witness := PublicationReplacementCleanupWitness{PrivateDirRel: plan.PrivateDirRel, BackupRel: backupRel, Directory: *created.DirectoryIdentity, Parent: plan.ParentIdentity, Backup: backupIdentity}
	var sourceHolds int
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_source_hold WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &sourceHolds); err != nil || sourceHolds != 0 {
		t.Fatalf("source holds before settle = %d, %v; rewrite must have authoritative source-hold absence", sourceHolds, err)
	}
	for _, step := range []string{"backup", "directory"} {
		begin := repo.BeginPublicationReplacementCleanup
		if step == "directory" {
			begin = repo.BeginPublicationReplacementDirectoryCleanup
		}
		if got, err := begin(ctx, f.guard, witness); err != nil || got != PublicationReplacementCleanupRecorded {
			t.Fatalf("%s cleanup intent = %q, %v", step, got, err)
		}
		if step == "backup" {
			if committed, err := repo.CommitReplacementPublication(ctx, f.guard); err != nil || !committed {
				t.Fatalf("commit replay after cleanup intent = %t, %v", committed, err)
			}
		}
		if got, err := repo.RecordPublicationReplacementCleanupResult(ctx, f.guard, witness, step, "already_absent"); err != nil || got != PublicationReplacementCleanupRecorded {
			t.Fatalf("%s cleanup result = %q, %v", step, got, err)
		}
	}
	wrongGuard := f.guard
	wrongGuard.DeliveryToken = uuid.NewString()
	if settled, err := repo.SettleReplacementPublication(ctx, wrongGuard, witness, true); err != nil || settled {
		t.Fatalf("wrong-guard settle = %t, %v; want rejected", settled, err)
	}
	wrongWitness := witness
	wrongWitness.BackupRel += ".suffix"
	if settled, err := repo.SettleReplacementPublication(ctx, f.guard, wrongWitness, true); err != nil || settled {
		t.Fatalf("wrong-suffix settle = %t, %v; want rejected", settled, err)
	}
	if _, err := f.db.NewRaw(`UPDATE track_publication SET relative_path=? WHERE position_id=?`, intent.TargetRelativePath+".foreign", f.guard.ExpectedPositionID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if settled, err := repo.SettleReplacementPublication(ctx, f.guard, witness, true); err != nil || settled {
		t.Fatalf("changed current namespace settle = %t, %v; want rejected", settled, err)
	}
	if _, err := f.db.NewRaw(`UPDATE track_publication SET relative_path=? WHERE position_id=?`, intent.TargetRelativePath, f.guard.ExpectedPositionID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	const candidateReservationsQuery = `SELECT count(*) FROM publication_namespace_reservation r
		JOIN publication_namespace_claim c ON c.id=r.namespace_claim_id
		WHERE r.position_id=? AND r.generation=? AND c.canonical_key=? AND c.relative_path=? AND c.node_kind='file'`
	var candidateReservationsBefore int
	if err := f.db.NewRaw(candidateReservationsQuery, f.guard.ExpectedPositionID, f.guard.CandidateGeneration, intent.TargetCanonicalKey, intent.TargetRelativePath).Scan(ctx, &candidateReservationsBefore); err != nil {
		t.Fatal(err)
	}
	if settled, err := repo.SettleReplacementPublication(ctx, f.guard, witness, true); err != nil || !settled {
		t.Fatalf("settle = %t, %v", settled, err)
	}
	if attempt, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID); err != nil || attempt != nil {
		t.Fatalf("settled replacement retained its publication attempt: attempt=%#v err=%v", attempt, err)
	}
	var state, stage string
	if err := f.db.NewRaw(`SELECT state,stage FROM operation WHERE id=?`, f.guard.OperationID).Scan(ctx, &state, &stage); err != nil || state != "succeeded" || stage != "finished" {
		t.Fatalf("operation state/stage = %q/%q, %v", state, stage, err)
	}
	for _, query := range []string{
		`SELECT count(*) FROM publication_execution_claim WHERE operation_id=?`,
		`SELECT count(*) FROM operation_tool_read_hold WHERE operation_id=?`,
	} {
		var count int
		if err := f.db.NewRaw(query, f.guard.OperationID).Scan(ctx, &count); err != nil || count != 0 {
			t.Fatalf("remaining state for %q = %d, %v", query, count, err)
		}
	}
	var current TrackPublication
	if err := f.db.NewSelect().Model(&current).Where("position_id=?", f.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if current.Generation != f.guard.CandidateGeneration || current.RelativePath != intent.TargetRelativePath || current.QualityPolicyVersion != 1 || !sameJSONValue(current.ActualAudio, audio) || !sameJSONValue(current.ActualMetadata, staged.ActualMetadata) || !sameJSONValue(current.ActualAssets, staged.ActualAssets) || !sameJSONValue(current.Ownership, mustJSON(PublicationCurrentOwnership{Version: 1, Identity: evidence.Identity})) {
		t.Fatalf("settlement changed current publication projection: %#v", current)
	}
	var reservations int
	if err := f.db.NewRaw(candidateReservationsQuery, f.guard.ExpectedPositionID, f.guard.CandidateGeneration, intent.TargetCanonicalKey, intent.TargetRelativePath).Scan(ctx, &reservations); err != nil || reservations != candidateReservationsBefore {
		t.Fatalf("preserved candidate namespace reservations = %d, before settlement %d, %v", reservations, candidateReservationsBefore, err)
	}
}
