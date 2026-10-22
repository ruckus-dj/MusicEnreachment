//go:build integration

package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

func prepareManualLyricsCommitFixture(t *testing.T) (publicationExecutionFixture, *SetupManagerRepository, PublicationInputs, PublicationStageObservation, []json.RawMessage) {
	t.Helper()
	ctx := context.Background()
	f, intent, staged := readyPublicationExposureFixture(t)
	repo := NewSetupManagerRepository(f.db)
	attempt, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	input, err := DecodePublicationInputs(attempt.FrozenInputs)
	if err != nil {
		t.Fatal(err)
	}
	oldGeneration := uuid.New()
	oldIdentity := PublicationFileIdentity{SizeBytes: int64Pointer(10), MTime: int64Pointer(20), SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}
	facts := input.Audio.AudioFacts
	input.Version, input.QualityPolicyVersion, input.IntentKind, input.Audio = PublicationInputsVersion, publication.QualityPolicyVersion, "rewrite", nil
	input.InitialLyrics = nil
	input.Rewrite = &PublicationRewriteInput{CurrentGeneration: oldGeneration, RelativePath: intent.TargetRelativePath, Ownership: oldIdentity, AudioFacts: facts}
	input.DesiredTags = map[string][]string{"TITLE": {"published title"}}
	const lyrics = "[00:01.00]Привет\r\n[00:02.00]world\r\n"
	lyricsDigest := sha256.Sum256([]byte(lyrics))
	lyricsHash := hex.EncodeToString(lyricsDigest[:])
	staged.ActualAudio = mustJSON(PublicationCurrentAudio{Version: 1, Facts: facts})
	staged.ActualMetadata = mustJSON(PublicationCommitMetadata{Version: 1, Tags: map[string][]string{"TITLE": {"published title"}, "LYRICS": {lyrics}}})
	staged.ActualAssets = mustJSON(PublicationAssetOutcomes{Version: 1, Cover: PublicationCoverOutcome{State: PublicationAssetAbsent}, Lyrics: PublicationLyricsOutcome{State: PublicationAssetEmbedded, SHA256: lyricsHash, Format: "lrc"}})
	staged.QualityPolicyVersion = publication.QualityPolicyVersion
	entries, err := decodeJournal(attempt.MutationJournal)
	if err != nil {
		t.Fatal(err)
	}
	var ready PublicationPreparationRecord
	for i, raw := range entries {
		var header struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(raw, &header) != nil {
			t.Fatal("invalid fixture journal")
		}
		if header.Kind == "stage_observation" {
			var record map[string]json.RawMessage
			if json.Unmarshal(raw, &record) != nil {
				t.Fatal("invalid stage fixture")
			}
			record["observation"] = mustJSON(staged)
			entries[i] = mustJSON(record)
		}
		var prep PublicationPreparationRecord
		if json.Unmarshal(raw, &prep) == nil && prep.Step == "input_ready" {
			ready = prep
		}
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE track_publication_attempt SET mutation_journal=?::jsonb WHERE operation_id=?`, string(mustJSON(entries)), f.guard.OperationID); err != nil {
		t.Fatal(err)
	}
	if err := updatePublicationAttemptToRewrite(ctx, f, oldGeneration, mustJSON(input)); err != nil {
		t.Fatal(err)
	}
	oldAudio := mustJSON(PublicationCurrentAudio{Version: 1, Facts: facts})
	oldMetadata := mustJSON(PublicationCommitMetadata{Version: 1, Tags: input.DesiredTags})
	oldAssets := json.RawMessage(`{"version":1,"cover":{"state":"absent"},"lyrics":{"state":"absent"}}`)
	if _, err := f.db.ExecContext(ctx, `INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at) VALUES (?,?,?,?,?::jsonb,?::jsonb,?::jsonb,?::jsonb,1,now())`, f.guard.ExpectedPositionID, f.guard.Epoch, oldGeneration, intent.TargetRelativePath, string(mustJSON(PublicationCurrentOwnership{Version: 1, Identity: oldIdentity})), string(oldAudio), string(oldMetadata), string(oldAssets)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `INSERT INTO position_source_authority(position_id,mode,revision,publication_epoch,publication_generation,source_location_id,media_variant_id) VALUES (?,'manual',?,?,?,?,?)`, f.guard.ExpectedPositionID, uuid.New(), f.guard.Epoch, oldGeneration, uuid.New(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	intent.Expectation = "expected_current"
	if result, err := repo.BeginPublicationExposure(ctx, f.guard, intent, staged); err != nil || result != PublicationExposureRecorded {
		t.Fatalf("fixture exposure = %q, %v", result, err)
	}
	backup := clonePublicationFileIdentity(*ready.CopiedInput)
	backup.SHA256 = oldIdentity.SHA256
	evidence := PublicationReplacementEvidence{Version: 1, Kind: "publication_replacement", Guard: f.guard, PositionID: f.guard.ExpectedPositionID, Epoch: f.guard.Epoch, CandidateGeneration: f.guard.CandidateGeneration, ExpectedGeneration: &oldGeneration, Intent: intent, Staged: staged, Identity: oldIdentity, BackupIdentity: backup, BackupPath: ready.InputRel}
	for _, transition := range [][2]string{{"exposing", "backup_intent"}, {"backup_intent", "backup_ready"}, {"backup_ready", "replacement_intent"}, {"replacement_intent", "installed"}} {
		evidence.From, evidence.To = transition[0], transition[1]
		if transition[1] == "installed" {
			evidence.Identity = PublicationFileIdentity{SizeBytes: staged.Ownership.ExpectedSize, MTime: staged.Ownership.ExpectedMTime, SHA256: staged.PayloadSHA256}
		}
		if result, err := repo.RecordPublicationReplacement(ctx, f.guard, evidence); err != nil || result != PublicationReplacementRecorded {
			t.Fatalf("fixture %s = %q, %v", evidence.To, result, err)
		}
	}
	// V3 filesystem execution is intentionally disabled. Seed its canonical
	// evidence explicitly, without weakening execution gates, to test the DB commit.
	input.Version = PublicationInputsVersion
	input.ManualLyrics = &PublicationManualLyricsInput{ExpectedMetadataRevision: 7, ConfirmedCurrentGeneration: oldGeneration, State: "value", Value: lyrics}
	attempt, err = repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	entries, err = decodeJournal(attempt.MutationJournal)
	if err != nil {
		t.Fatal(err)
	}
	outcome := publicationOptionalLyricsOutcomeJournalRecord{Version: 1, Kind: "publication_optional_lyrics_outcome", Outcome: PublicationOptionalLyricsExactRoundTrip, ManualLyrics: *input.ManualLyrics, Preparation: ready, InputIdentity: backup, OutputIdentity: evidence.Identity, FinalObservation: staged}
	entries = insertJournalEntry(entries, 3, mustJSON(outcome))
	if _, err := f.db.ExecContext(ctx, `UPDATE track_publication_attempt SET frozen_inputs=?::jsonb, mutation_journal=?::jsonb WHERE operation_id=?`, string(mustJSON(input)), string(mustJSON(entries)), f.guard.OperationID); err != nil {
		t.Fatal(err)
	}
	return f, repo, input, staged, entries
}

func TestManualLyricsCommitWritesReceiptAndDoesNotRepairReplayPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, input, _, _ := prepareManualLyricsCommitFixture(t)
	if err := NewSettingsRepository(f.db).Set(ctx, "publication_remove_sources", "true"); err != nil {
		t.Fatal(err)
	}
	if committed, err := repo.CommitReplacementPublication(ctx, f.guard); err != nil || !committed {
		t.Fatalf("manual commit = %v, %v", committed, err)
	}
	currentAuthority, err := repo.ReadPositionSourceAuthority(ctx, f.guard.ExpectedPositionID)
	if err != nil || currentAuthority == nil || currentAuthority.Mode != "manual" || currentAuthority.PublicationGeneration != f.guard.CandidateGeneration {
		t.Fatalf("manual lyrics commit did not carry current source authority: %+v, %v", currentAuthority, err)
	}
	assertReplacementCleanupRequest(t, ctx, f)
	var receipt PublicationManualLyricsReceipt
	if err := f.db.NewSelect().Model(&receipt).Where("position_id=?", f.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(input.ManualLyrics.Value))
	lyricsHash := hex.EncodeToString(digest[:])
	if receipt.DesiredRevision != 7 || receipt.ConfirmedGeneration != input.Rewrite.CurrentGeneration || receipt.PublishedGeneration != f.guard.CandidateGeneration || receipt.DesiredSHA256 == nil || *receipt.DesiredSHA256 != lyricsHash {
		t.Fatalf("wrong receipt: %+v", receipt)
	}
	attempt, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil || attempt.FrozenInputs != nil || attempt.Phase != "committed" {
		t.Fatalf("unconsumed frozen inputs: %+v, %v", attempt, err)
	}
	if replayed, err := repo.CommitReplacementPublication(ctx, f.guard); err != nil || !replayed {
		t.Fatalf("manual replay = %v, %v", replayed, err)
	}
	if _, err := f.db.ExecContext(ctx, `DELETE FROM publication_source_cleanup_request WHERE position_id=?`, f.guard.ExpectedPositionID); err != nil {
		t.Fatal(err)
	}
	if replayed, err := repo.CommitReplacementPublication(ctx, f.guard); err != nil || !replayed {
		t.Fatalf("manual replay after request consumption = %v, %v", replayed, err)
	}
	var requestCount int
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_source_cleanup_request WHERE position_id=?`, f.guard.ExpectedPositionID).Scan(ctx, &requestCount); err != nil || requestCount != 0 {
		t.Fatalf("manual replay recreated cleanup request: count=%d, err=%v", requestCount, err)
	}
	for _, generation := range []uuid.UUID{input.Rewrite.CurrentGeneration, f.guard.CandidateGeneration} {
		projection := PublicationInitialLyricsProjection{
			Version: PublicationInitialLyricsVersion, Generation: generation,
			Source:  "none",
			Outcome: PublicationInitialLyricsNotRequested, ResolutionWarningCode: "", OutcomeWarningCode: "",
			AuthorityMetadataRevision: 7, AuthorityState: "absent", Authority: "automatic", AuthorityProvenance: "source_metadata",
		}
		if _, err := f.db.ExecContext(ctx, `UPDATE track_publication SET initial_lyrics=?::jsonb WHERE position_id=?`, string(mustJSON(projection)), f.guard.ExpectedPositionID); err != nil {
			t.Fatal(err)
		}
		if replayed, err := repo.CommitReplacementPublication(ctx, f.guard); err != nil || replayed {
			t.Fatalf("replay accepted initial-lyrics projection for generation %s: %v, %v", generation, replayed, err)
		}
		var retained TrackPublication
		if err := f.db.NewSelect().Model(&retained).Where("position_id=?", f.guard.ExpectedPositionID).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		if !sameInitialLyricsProjectionJSON(retained.InitialLyrics, &projection) {
			t.Fatal("replay repaired residual initial-lyrics evidence")
		}
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE track_publication SET initial_lyrics=NULL WHERE position_id=?`, f.guard.ExpectedPositionID); err != nil {
		t.Fatal(err)
	}
	if replayed, err := repo.CommitReplacementPublication(ctx, f.guard); err != nil || !replayed {
		t.Fatalf("replay after restoring null initial lyrics = %v, %v", replayed, err)
	}
	if _, err := f.db.ExecContext(ctx, `DELETE FROM publication_manual_lyrics_receipt WHERE position_id=?`, f.guard.ExpectedPositionID); err != nil {
		t.Fatal(err)
	}
	if replayed, err := repo.CommitReplacementPublication(ctx, f.guard); err != nil || replayed {
		t.Fatalf("replay repaired missing receipt: %v, %v", replayed, err)
	}
}

func TestManualLyricsCommitDoesNotRequestSourceCleanupWhenDisabledPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, _, _, _ := prepareManualLyricsCommitFixture(t)
	if err := NewSettingsRepository(f.db).Set(ctx, "publication_remove_sources", "false"); err != nil {
		t.Fatal(err)
	}
	if committed, err := repo.CommitReplacementPublication(ctx, f.guard); err != nil || !committed {
		t.Fatalf("manual commit with cleanup disabled = %v, %v", committed, err)
	}
	var requestCount int
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_source_cleanup_request WHERE position_id=?`, f.guard.ExpectedPositionID).Scan(ctx, &requestCount); err != nil || requestCount != 0 {
		t.Fatalf("cleanup requests with setting disabled = %d, err=%v; want 0", requestCount, err)
	}
	var receiptCount int
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_manual_lyrics_receipt WHERE position_id=?`, f.guard.ExpectedPositionID).Scan(ctx, &receiptCount); err != nil || receiptCount != 1 {
		t.Fatalf("manual lyrics receipts with cleanup disabled = %d, err=%v; want 1", receiptCount, err)
	}
}

func TestManualLyricsCommitRemovalAndRebuiltOutcomesPostgreSQL(t *testing.T) {
	for _, test := range []struct {
		name, state string
		rebuilt     bool
	}{
		{"removal", "explicit_empty", false},
		{"value fallback", "value", true},
		{"removal fallback", "explicit_empty", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f, repo, input, staged, entries := prepareManualLyricsCommitFixture(t)
			if err := NewSettingsRepository(f.db).Set(ctx, "publication_remove_sources", "true"); err != nil {
				t.Fatal(err)
			}
			input.ManualLyrics.State = test.state
			if test.state == "explicit_empty" {
				input.ManualLyrics.Value = ""
			}
			staged.ActualMetadata = mustJSON(PublicationCommitMetadata{Version: 1, Tags: input.DesiredTags})
			staged.ActualAssets = json.RawMessage(`{"version":1,"cover":{"state":"absent"},"lyrics":{"state":"absent"}}`)
			var outcome publicationOptionalLyricsOutcomeJournalRecord
			if decodeStrictJSON(entries[3], &outcome) != nil {
				t.Fatal("invalid fixture outcome")
			}
			outcome.ManualLyrics, outcome.FinalObservation = *input.ManualLyrics, staged
			if test.rebuilt {
				outcome.Outcome = PublicationOptionalLyricsRebuiltWithoutLyrics
				outcome.Diagnostic = "optional lyrics did not round-trip; rebuilt output without lyrics"
			}
			entries[3] = mustJSON(outcome)
			for i := 4; i < len(entries); i++ {
				var header struct {
					Kind string `json:"kind"`
				}
				if json.Unmarshal(entries[i], &header) != nil {
					t.Fatal("invalid fixture journal")
				}
				switch header.Kind {
				case "stage_observation":
					var record map[string]json.RawMessage
					if json.Unmarshal(entries[i], &record) != nil {
						t.Fatal("invalid stage record")
					}
					record["observation"] = mustJSON(staged)
					entries[i] = mustJSON(record)
				case "publication_exposure":
					var record publicationExposureJournalRecord
					if decodeStrictJSON(entries[i], &record) != nil {
						t.Fatal("invalid exposure record")
					}
					record.Observation = staged
					entries[i] = mustJSON(record)
				case "publication_replacement":
					var record PublicationReplacementEvidence
					if decodeStrictJSON(entries[i], &record) != nil {
						t.Fatal("invalid replacement record")
					}
					record.Staged = staged
					entries[i] = mustJSON(record)
				}
			}
			if test.rebuilt {
				prep := outcome.Preparation
				before := clonePublicationFileIdentity(outcome.OutputIdentity)
				after := clonePublicationFileIdentity(outcome.InputIdentity)
				rebuild := PublicationCandidateRebuildRecord{Version: PublicationCandidateRebuildVersion, Kind: "publication_candidate_rebuild", Step: "intent", Guard: f.guard,
					OwnerToken: prep.OwnerToken, PrivateDirRel: prep.PrivateDirRel, InputRel: prep.InputRel, OutputRel: prep.OutputRel, DirectoryIdentity: *prep.DirectoryIdentity, Input: prep.CopiedInput, Candidate: &before}
				entries = insertJournalEntry(entries, 3, mustJSON(rebuild))
				rebuild.Step, rebuild.Candidate = "complete", &after
				entries = insertJournalEntry(entries, 4, mustJSON(rebuild))
			}
			if _, err := f.db.ExecContext(ctx, `UPDATE track_publication_attempt SET frozen_inputs=?::jsonb, mutation_journal=?::jsonb WHERE operation_id=?`, string(mustJSON(input)), string(mustJSON(entries)), f.guard.OperationID); err != nil {
				t.Fatal(err)
			}
			if committed, err := repo.CommitReplacementPublication(ctx, f.guard); err != nil || !committed {
				t.Fatalf("commit = %v, %v", committed, err)
			}
			assertReplacementCleanupRequest(t, ctx, f)
			var receipt PublicationManualLyricsReceipt
			if err := f.db.NewSelect().Model(&receipt).Where("position_id=?", f.guard.ExpectedPositionID).Scan(ctx); err != nil {
				t.Fatal(err)
			}
			if string(receipt.DesiredState) != test.state || (receipt.WarningCode != nil) != test.rebuilt || (receipt.DesiredSHA256 != nil) != (test.state == "value") {
				t.Fatalf("incorrect receipt: %+v", receipt)
			}
			if test.rebuilt && *receipt.WarningCode != manualLyricsReadbackMismatchWarning {
				t.Fatal("unexpected warning code")
			}
			if replayed, err := repo.CommitReplacementPublication(ctx, f.guard); err != nil || !replayed {
				t.Fatalf("replay = %v, %v", replayed, err)
			}
		})
	}
}

func TestManualLyricsCommitReceiptInsertFailureRollsBackPostgreSQL(t *testing.T) {
	t.Parallel()
	manualLyricsCommitFailureRollsBack(t, "reject_manual_lyrics_receipt_insert", `CREATE TRIGGER reject_manual_lyrics_receipt BEFORE INSERT ON publication_manual_lyrics_receipt FOR EACH ROW EXECUTE FUNCTION reject_manual_lyrics_receipt_insert()`)
}

func TestManualLyricsCommitOperationUpdateFailureRollsBackPostgreSQL(t *testing.T) {
	t.Parallel()
	manualLyricsCommitFailureRollsBack(t, "reject_manual_lyrics_operation_update", `CREATE TRIGGER reject_manual_lyrics_committed_stage BEFORE UPDATE ON operation FOR EACH ROW WHEN (NEW.stage = 'committed') EXECUTE FUNCTION reject_manual_lyrics_operation_update()`)
}

func manualLyricsCommitFailureRollsBack(t *testing.T, fn, trigger string) {
	t.Helper()
	ctx := context.Background()
	f, repo, _, _, _ := prepareManualLyricsCommitFixture(t)
	if _, err := f.db.ExecContext(ctx, `CREATE FUNCTION `+fn+`() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected commit failure'; END $$`); err != nil {
		t.Fatal(err)
	}
	defer f.db.ExecContext(ctx, `DROP FUNCTION `+fn+`() CASCADE`)
	if _, err := f.db.ExecContext(ctx, trigger); err != nil {
		t.Fatal(err)
	}
	if committed, err := repo.CommitReplacementPublication(ctx, f.guard); err == nil || committed {
		t.Fatalf("commit with injected failure = %v, %v; want error and false", committed, err)
	}
	assertManualLyricsCommitUnchanged(t, ctx, f)
}

func assertManualLyricsCommitUnchanged(t *testing.T, ctx context.Context, f publicationExecutionFixture) {
	t.Helper()
	var current TrackPublication
	if err := f.db.NewSelect().Model(&current).Where("position_id=?", f.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	var attempt TrackPublicationAttempt
	if err := f.db.NewSelect().Model(&attempt).Where("operation_id=?", f.guard.OperationID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if attempt.ExpectedGeneration == nil || current.Generation != *attempt.ExpectedGeneration {
		t.Fatalf("current generation after rollback = %s, want %v", current.Generation, attempt.ExpectedGeneration)
	}
	var authority PositionSourceAuthority
	if err := f.db.NewSelect().Model(&authority).Where("position_id=?", f.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if attempt.ExpectedGeneration == nil || authority.PublicationGeneration != *attempt.ExpectedGeneration || authority.PublicationEpoch != current.Epoch {
		t.Fatalf("authority after rolled-back lyrics commit = %+v", authority)
	}
	var count int
	if err := f.db.NewSelect().Table("publication_manual_lyrics_receipt").ColumnExpr("count(*)").Where("position_id=?", f.guard.ExpectedPositionID).Scan(ctx, &count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("receipt count after rollback = %d, want 0", count)
	}
	if attempt.Phase != "exposing" || len(attempt.FrozenInputs) == 0 {
		t.Fatalf("attempt after rollback = phase %q, frozen inputs %d bytes", attempt.Phase, len(attempt.FrozenInputs))
	}
	var operation Operation
	if err := f.db.NewSelect().Model(&operation).Where("id=?", f.guard.OperationID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if operation.Stage != "exposing" {
		t.Fatalf("operation stage after rollback = %q, want exposing", operation.Stage)
	}
}

func TestManualLyricsCommitReplayRejectsTamperedReceiptPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, _, _, _ := prepareManualLyricsCommitFixture(t)
	if committed, err := repo.CommitReplacementPublication(ctx, f.guard); err != nil || !committed {
		t.Fatalf("manual commit = %v, %v", committed, err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE publication_manual_lyrics_receipt SET desired_sha256='aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' WHERE position_id=?`, f.guard.ExpectedPositionID); err != nil {
		t.Fatal(err)
	}
	var before PublicationManualLyricsReceipt
	if err := f.db.NewSelect().Model(&before).Where("position_id=?", f.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if replayed, err := repo.CommitReplacementPublication(ctx, f.guard); err != nil || replayed {
		t.Fatalf("tampered receipt replay = %v, %v; want false", replayed, err)
	}
	var after PublicationManualLyricsReceipt
	if err := f.db.NewSelect().Model(&after).Where("position_id=?", f.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if before.DesiredSHA256 == nil || after.DesiredSHA256 == nil || *before.DesiredSHA256 != *after.DesiredSHA256 || !before.RecordedAt.Equal(after.RecordedAt) {
		t.Fatalf("replay mutated tampered receipt: before=%+v after=%+v", before, after)
	}
}

func TestManualLyricsCleanupRequiresReceiptAfterIntentPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, _, _, _ := prepareManualLyricsCommitFixture(t)
	if committed, err := repo.CommitReplacementPublication(ctx, f.guard); err != nil || !committed {
		t.Fatalf("manual commit = %v, %v", committed, err)
	}
	attempt, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := decodeJournal(attempt.MutationJournal)
	if err != nil {
		t.Fatal(err)
	}
	var prep PublicationPreparationRecord
	var installed PublicationReplacementEvidence
	for _, raw := range entries {
		var header struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(raw, &header) != nil {
			t.Fatal("invalid journal")
		}
		if header.Kind == "publication_preparation" {
			var r PublicationPreparationRecord
			if decodeStrictJSON(raw, &r) == nil && r.Step == "input_ready" {
				prep = r
			}
		}
		if header.Kind == "publication_replacement" {
			var r PublicationReplacementEvidence
			if decodeStrictJSON(raw, &r) == nil && r.To == "installed" {
				installed = r
			}
		}
	}
	w := PublicationReplacementCleanupWitness{PrivateDirRel: prep.PrivateDirRel, BackupRel: prep.InputRel, Directory: *prep.DirectoryIdentity, Parent: prep.ParentIdentity, Backup: installed.BackupIdentity}
	if out, err := repo.BeginPublicationReplacementCleanup(ctx, f.guard, w); err != nil || out != PublicationReplacementCleanupRecorded {
		t.Fatalf("cleanup intent = %q, %v", out, err)
	}
	if _, err := f.db.ExecContext(ctx, `DELETE FROM publication_manual_lyrics_receipt WHERE position_id=?`, f.guard.ExpectedPositionID); err != nil {
		t.Fatal(err)
	}
	if out, err := repo.RecordPublicationReplacementCleanupResult(ctx, f.guard, w, "backup", "already_absent"); err != nil || out != PublicationReplacementCleanupStale {
		t.Fatalf("cleanup result without receipt = %q, %v", out, err)
	}
	if out, err := repo.BeginPublicationReplacementCleanup(ctx, f.guard, w); err != nil || out != PublicationReplacementCleanupStale {
		t.Fatalf("already-recorded intent without receipt = %q, %v", out, err)
	}
}

func TestManualLyricsCleanupSettlesWithReceiptPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, _, _, _ := prepareManualLyricsCommitFixture(t)
	if committed, err := repo.CommitReplacementPublication(ctx, f.guard); err != nil || !committed {
		t.Fatalf("manual commit = %v, %v", committed, err)
	}
	var receiptBefore PublicationManualLyricsReceipt
	if err := f.db.NewSelect().Model(&receiptBefore).Where("position_id=?", f.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE release_track SET metadata_revision=metadata_revision+1 WHERE id=?`, f.guard.ExpectedPositionID); err != nil {
		t.Fatal(err)
	}
	attempt, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := decodeJournal(attempt.MutationJournal)
	if err != nil {
		t.Fatal(err)
	}
	var prep PublicationPreparationRecord
	var installed PublicationReplacementEvidence
	for _, raw := range entries {
		var h struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(raw, &h) != nil {
			t.Fatal("invalid journal")
		}
		if h.Kind == "publication_preparation" {
			var r PublicationPreparationRecord
			if decodeStrictJSON(raw, &r) == nil && r.Step == "input_ready" {
				prep = r
			}
		}
		if h.Kind == "publication_replacement" {
			var r PublicationReplacementEvidence
			if decodeStrictJSON(raw, &r) == nil && r.To == "installed" {
				installed = r
			}
		}
	}
	w := PublicationReplacementCleanupWitness{PrivateDirRel: prep.PrivateDirRel, BackupRel: prep.InputRel, Directory: *prep.DirectoryIdentity, Parent: prep.ParentIdentity, Backup: installed.BackupIdentity}
	if out, err := repo.BeginPublicationReplacementCleanup(ctx, f.guard, w); err != nil || out != PublicationReplacementCleanupRecorded {
		t.Fatalf("backup intent = %q, %v", out, err)
	}
	if out, err := repo.RecordPublicationReplacementCleanupResult(ctx, f.guard, w, "backup", "already_absent"); err != nil || out != PublicationReplacementCleanupRecorded {
		t.Fatalf("backup result = %q, %v", out, err)
	}
	if out, err := repo.BeginPublicationReplacementDirectoryCleanup(ctx, f.guard, w); err != nil || out != PublicationReplacementCleanupRecorded {
		t.Fatalf("directory intent = %q, %v", out, err)
	}
	if out, err := repo.RecordPublicationReplacementCleanupResult(ctx, f.guard, w, "directory", "already_absent"); err != nil || out != PublicationReplacementCleanupRecorded {
		t.Fatalf("directory result = %q, %v", out, err)
	}
	if settled, err := repo.SettleReplacementPublication(ctx, f.guard, w, true); err != nil || !settled {
		t.Fatalf("settle = %v, %v", settled, err)
	}
	var receiptAfter PublicationManualLyricsReceipt
	if err := f.db.NewSelect().Model(&receiptAfter).Where("position_id=?", f.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if receiptBefore.DesiredRevision != receiptAfter.DesiredRevision || !receiptBefore.RecordedAt.Equal(receiptAfter.RecordedAt) {
		t.Fatalf("receipt changed during cleanup: before=%+v after=%+v", receiptBefore, receiptAfter)
	}
	var count int
	if err := f.db.NewSelect().Table("track_publication_attempt").ColumnExpr("count(*)").Where("operation_id=?", f.guard.OperationID).Scan(ctx, &count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("attempt count after settle = %d, want 0", count)
	}
	if err := f.db.NewSelect().Table("publication_source_hold").ColumnExpr("count(*)").Where("operation_id=?", f.guard.OperationID).Scan(ctx, &count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("source hold count after settle = %d, want 0", count)
	}
	var epoch uuid.UUID
	if err := f.db.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &epoch); err != nil {
		t.Fatal(err)
	}
	if epoch != f.guard.Epoch {
		t.Fatalf("namespace epoch after settle = %s, want %s", epoch, f.guard.Epoch)
	}
}
