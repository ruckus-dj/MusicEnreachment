//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

func TestCommitReplacementPublicationPostgreSQL(t *testing.T) {
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
	identity := PublicationFileIdentity{SizeBytes: int64Pointer(10), MTime: int64Pointer(20), SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}
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
	ownership, _ := json.Marshal(PublicationCurrentOwnership{Version: 1, Identity: identity})
	audio, _ := json.Marshal(PublicationCurrentAudio{Version: 1, Facts: inputs.Rewrite.AudioFacts})
	oldMetadata := json.RawMessage(`{"version":1,"tags":{"TITLE":["old"]}}`)
	oldAssets := json.RawMessage(`{"version":1,"cover":{"state":"absent"},"lyrics":{"state":"absent"}}`)
	if _, err := f.db.NewRaw(`INSERT INTO track_publication (position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at) VALUES (?,?,?,?,?::jsonb,?::jsonb,?::jsonb,?::jsonb,1,now())`, f.guard.ExpectedPositionID, f.guard.Epoch, currentGeneration, intent.TargetRelativePath, string(ownership), string(audio), string(oldMetadata), string(oldAssets)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	intent.Expectation = "expected_current"
	if outcome, err := repo.BeginPublicationExposure(ctx, f.guard, intent, staged); err != nil || outcome != PublicationExposureRecorded {
		t.Fatalf("exposure = %q, %v", outcome, err)
	}
	// The backup is the copied private input, not the original target's frozen
	// mtime. Keep the old target's frozen identity distinct from copy facts.
	preparedAttempt, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	var preparationJournal []json.RawMessage
	if err := json.Unmarshal(preparedAttempt.MutationJournal, &preparationJournal); err != nil {
		t.Fatal(err)
	}
	var copiedInput PublicationFileIdentity
	for _, raw := range preparationJournal {
		var record PublicationPreparationRecord
		if json.Unmarshal(raw, &record) == nil && record.Step == "input_ready" && record.CopiedInput != nil {
			copiedInput = *record.CopiedInput
		}
	}
	copiedInput.SHA256 = identity.SHA256
	backupIdentity := copiedInput
	evidence := PublicationReplacementEvidence{Version: 1, Kind: "publication_replacement", Guard: f.guard, PositionID: f.guard.ExpectedPositionID, Epoch: f.guard.Epoch, CandidateGeneration: f.guard.CandidateGeneration, ExpectedGeneration: &currentGeneration, Intent: intent, Staged: staged, Identity: identity, BackupIdentity: backupIdentity, BackupPath: "private/op/input.flac"}
	for _, transition := range [][2]string{{"exposing", "backup_intent"}, {"backup_intent", "backup_ready"}, {"backup_ready", "replacement_intent"}, {"replacement_intent", "installed"}} {
		evidence.From, evidence.To = transition[0], transition[1]
		if transition[1] == "installed" {
			evidence.Identity = PublicationFileIdentity{SizeBytes: int64Pointer(*staged.Ownership.ExpectedSize), MTime: int64Pointer(789), SHA256: staged.PayloadSHA256}
		}
		if outcome, err := repo.RecordPublicationReplacement(ctx, f.guard, evidence); err != nil || outcome != PublicationReplacementRecorded {
			t.Fatalf("replacement %s = %q, %v", transition[1], outcome, err)
		}
	}
	var canonicalJournal []json.RawMessage
	var canonicalJournalJSON string
	if err := f.db.NewRaw(`SELECT mutation_journal::text FROM track_publication_attempt WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &canonicalJournalJSON); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(canonicalJournalJSON), &canonicalJournal); err != nil {
		t.Fatal(err)
	}
	variants := map[string]func([]json.RawMessage) []json.RawMessage{
		"missing preparation record": func(entries []json.RawMessage) []json.RawMessage {
			return removeJournalKindAt(entries, "publication_preparation", 2)
		},
		"missing stage record": func(entries []json.RawMessage) []json.RawMessage {
			return removeJournalKindAt(entries, "stage_observation", 1)
		},
		"preparation interleaved with stages": func(entries []json.RawMessage) []json.RawMessage {
			preparations := journalKindIndices(entries, "publication_preparation")
			stages := journalKindIndices(entries, "stage_observation")
			return insertJournalEntry(entries, stages[1], entries[preparations[0]])
		},
		"wrong preparation owner": func(entries []json.RawMessage) []json.RawMessage {
			for _, index := range journalKindIndices(entries, "publication_preparation") {
				var record PublicationPreparationRecord
				if err := json.Unmarshal(entries[index], &record); err != nil {
					t.Fatal(err)
				}
				record.OwnerToken = uuid.New()
				entries[index] = mustJSON(record)
			}
			return entries
		},
		"substituted validated observation": func(entries []json.RawMessage) []json.RawMessage {
			indices := journalKindIndices(entries, "stage_observation")
			var record map[string]json.RawMessage
			if err := json.Unmarshal(entries[indices[1]], &record); err != nil {
				t.Fatal(err)
			}
			var observation PublicationStageObservation
			if err := json.Unmarshal(record["observation"], &observation); err != nil {
				t.Fatal(err)
			}
			observation.PayloadSHA256 = "1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
			record["observation"] = mustJSON(observation)
			entries[indices[1]] = mustJSON(record)
			return entries
		},
		"missing transition": func(entries []json.RawMessage) []json.RawMessage {
			return removeJournalKindAt(entries, "publication_replacement", 3)
		},
		"reordered transitions": func(entries []json.RawMessage) []json.RawMessage {
			indices := journalKindIndices(entries, "publication_replacement")
			entries[indices[0]], entries[indices[1]] = entries[indices[1]], entries[indices[0]]
			return entries
		},
		"extra transition": func(entries []json.RawMessage) []json.RawMessage {
			return append(entries, mustJSON(map[string]any{"kind": "unexpected"}))
		},
		"mismatched installed identity": func(entries []json.RawMessage) []json.RawMessage {
			indices := journalKindIndices(entries, "publication_replacement")
			var installed PublicationReplacementEvidence
			if err := json.Unmarshal(entries[indices[len(indices)-1]], &installed); err != nil {
				t.Fatalf("decode installed evidence: %v", err)
			}
			installed.Identity.SHA256 = "1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
			entries[indices[len(indices)-1]] = mustJSON(installed)
			return entries
		},
		"mismatched backup path": func(entries []json.RawMessage) []json.RawMessage {
			indices := journalKindIndices(entries, "publication_replacement")
			var record PublicationReplacementEvidence
			if err := json.Unmarshal(entries[indices[1]], &record); err != nil {
				t.Fatal(err)
			}
			record.BackupPath = "private/foreign/backup"
			entries[indices[1]] = mustJSON(record)
			return entries
		},
		"mismatched copied backup size": func(entries []json.RawMessage) []json.RawMessage {
			indices := journalKindIndices(entries, "publication_replacement")
			var record PublicationReplacementEvidence
			if err := json.Unmarshal(entries[indices[1]], &record); err != nil {
				t.Fatal(err)
			}
			record.BackupIdentity.SizeBytes = int64Pointer(31)
			entries[indices[1]] = mustJSON(record)
			return entries
		},
		"mismatched copied backup mtime": func(entries []json.RawMessage) []json.RawMessage {
			indices := journalKindIndices(entries, "publication_replacement")
			var record PublicationReplacementEvidence
			if err := json.Unmarshal(entries[indices[1]], &record); err != nil {
				t.Fatal(err)
			}
			record.BackupIdentity.MTime = int64Pointer(41)
			entries[indices[1]] = mustJSON(record)
			return entries
		},
		"mismatched backup hash": func(entries []json.RawMessage) []json.RawMessage {
			indices := journalKindIndices(entries, "publication_replacement")
			var record PublicationReplacementEvidence
			if err := json.Unmarshal(entries[indices[1]], &record); err != nil {
				t.Fatal(err)
			}
			record.BackupIdentity.SHA256 = "1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
			entries[indices[1]] = mustJSON(record)
			return entries
		},
		"substituted stage chain versus exposure": func(entries []json.RawMessage) []json.RawMessage {
			indices := journalKindIndices(entries, "publication_replacement")
			var record PublicationReplacementEvidence
			if err := json.Unmarshal(entries[indices[0]], &record); err != nil {
				t.Fatal(err)
			}
			record.Staged.PayloadSHA256 = "1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
			entries[indices[0]] = mustJSON(record)
			return entries
		},
		"foreign first-stage path": func(entries []json.RawMessage) []json.RawMessage {
			indices := journalKindIndices(entries, "publication_replacement")
			var record PublicationReplacementEvidence
			if err := json.Unmarshal(entries[indices[0]], &record); err != nil {
				t.Fatal(err)
			}
			record.BackupPath = "private/op/foreign"
			entries[indices[0]] = mustJSON(record)
			return entries
		},
		"mixed candidate-installed record": func(entries []json.RawMessage) []json.RawMessage {
			return append(entries, mustJSON(map[string]any{"kind": "publication_candidate_installed"}))
		},
	}
	for name, mutate := range variants {
		journal := mutate(append([]json.RawMessage(nil), canonicalJournal...))
		encoded, err := json.Marshal(journal)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.NewRaw(`UPDATE track_publication_attempt SET mutation_journal=?::jsonb WHERE operation_id=?`, string(encoded), f.guard.OperationID).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if committed, err := repo.CommitReplacementPublication(ctx, f.guard); err != nil || committed {
			t.Fatalf("commit with %s = %t, %v; want reject", name, committed, err)
		}
	}
	if _, err := f.db.NewRaw(`UPDATE track_publication_attempt SET mutation_journal=?::jsonb WHERE operation_id=?`, string(mustJSON(canonicalJournal)), f.guard.OperationID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if err := NewSettingsRepository(f.db).Set(ctx, "publication_remove_sources", "true"); err != nil {
		t.Fatal(err)
	}
	if committed, err := repo.CommitReplacementPublication(ctx, f.guard); err != nil || !committed {
		t.Fatalf("replacement commit = %t, %v", committed, err)
	}
	assertReplacementCleanupRequest(t, ctx, f)
	var current TrackPublication
	if err := f.db.NewSelect().Model(&current).Where("position_id=?", f.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if current.Generation != f.guard.CandidateGeneration || current.RelativePath != intent.TargetRelativePath || current.QualityPolicyVersion != 1 || !sameJSONValue(current.ActualAudio, audio) || !sameJSONValue(current.ActualMetadata, staged.ActualMetadata) || !sameJSONValue(current.ActualAssets, staged.ActualAssets) {
		t.Fatalf("committed replacement projection incorrect: %#v", current)
	}
	if !sameJSONValue(current.Ownership, mustJSON(PublicationCurrentOwnership{Version: 1, Identity: evidence.Identity})) {
		t.Fatal("replacement ownership does not describe installed audio")
	}
	var phase, stage, claim string
	var frozenIsNull bool
	if err := f.db.NewRaw(`SELECT a.phase,o.stage,c.state,a.frozen_inputs IS NULL FROM track_publication_attempt a JOIN operation o ON o.id=a.operation_id JOIN publication_execution_claim c ON c.operation_id=a.operation_id WHERE a.operation_id=?`, f.guard.OperationID).Scan(ctx, &phase, &stage, &claim, &frozenIsNull); err != nil {
		t.Fatal(err)
	}
	if phase != "committed" || stage != "committed" || claim != "executing" || !frozenIsNull {
		t.Fatalf("settled state phase/stage/claim/frozen-null = %q/%q/%q/%t", phase, stage, claim, frozenIsNull)
	}
	var reservations int
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_namespace_reservation WHERE position_id=? AND generation=?`, f.guard.ExpectedPositionID, f.guard.CandidateGeneration).Scan(ctx, &reservations); err != nil || reservations == 0 {
		t.Fatalf("candidate reservations after commit = %d, %v", reservations, err)
	}
	var journalBefore, journalAfter string
	if err := f.db.NewRaw(`SELECT mutation_journal::text FROM track_publication_attempt WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &journalBefore); err != nil {
		t.Fatal(err)
	}
	if committed, err := repo.CommitReplacementPublication(ctx, f.guard); err != nil || !committed {
		t.Fatalf("exact replay = %t, %v", committed, err)
	}
	// A replay must not recreate a cleanup request that was already consumed.
	if _, err := f.db.ExecContext(ctx, `DELETE FROM publication_source_cleanup_request WHERE position_id=?`, f.guard.ExpectedPositionID); err != nil {
		t.Fatal(err)
	}
	if committed, err := repo.CommitReplacementPublication(ctx, f.guard); err != nil || !committed {
		t.Fatalf("replay after request consumption = %t, %v", committed, err)
	}
	var requestCount int
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_source_cleanup_request WHERE position_id=?`, f.guard.ExpectedPositionID).Scan(ctx, &requestCount); err != nil || requestCount != 0 {
		t.Fatalf("replay recreated cleanup request: count=%d, err=%v", requestCount, err)
	}
	if _, err := f.db.NewRaw(`UPDATE track_publication SET actual_metadata='{"version":1,"tags":{"GENRE":["changed"]}}'::jsonb WHERE position_id=?`, f.guard.ExpectedPositionID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if committed, err := repo.CommitReplacementPublication(ctx, f.guard); err != nil || committed {
		t.Fatalf("replay after current metadata change = %t, %v; want reject", committed, err)
	}
	if err := f.db.NewRaw(`SELECT mutation_journal::text FROM track_publication_attempt WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &journalAfter); err != nil || journalAfter != journalBefore {
		t.Fatalf("replay changed commit journal: %v", err)
	}
}

func assertReplacementCleanupRequest(t *testing.T, ctx context.Context, f publicationExecutionFixture) {
	t.Helper()
	var operationID, positionID, epoch, generation uuid.UUID
	if err := f.db.NewRaw(`SELECT originating_operation_id,position_id,output_epoch,publication_generation FROM publication_source_cleanup_request WHERE position_id=?`, f.guard.ExpectedPositionID).Scan(ctx, &operationID, &positionID, &epoch, &generation); err != nil {
		t.Fatal(err)
	}
	if operationID != f.guard.OperationID || positionID != f.guard.ExpectedPositionID || epoch != f.guard.Epoch || generation != f.guard.CandidateGeneration {
		t.Fatalf("cleanup request = %s/%s/%s/%s; want %s/%s/%s/%s", operationID, positionID, epoch, generation, f.guard.OperationID, f.guard.ExpectedPositionID, f.guard.Epoch, f.guard.CandidateGeneration)
	}
}

func insertJournalEntry(entries []json.RawMessage, index int, entry json.RawMessage) []json.RawMessage {
	entries = append(entries, nil)
	copy(entries[index+1:], entries[index:])
	entries[index] = entry
	return entries
}

func journalKindIndices(entries []json.RawMessage, kind string) []int {
	var indices []int
	for i, entry := range entries {
		var header struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(entry, &header) == nil && header.Kind == kind {
			indices = append(indices, i)
		}
	}
	return indices
}

func removeJournalKindAt(entries []json.RawMessage, kind string, occurrence int) []json.RawMessage {
	indices := journalKindIndices(entries, kind)
	if occurrence >= len(indices) {
		return entries
	}
	index := indices[occurrence]
	return append(entries[:index], entries[index+1:]...)
}
