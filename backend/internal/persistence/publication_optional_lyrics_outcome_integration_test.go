//go:build integration

package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestGuardedRecordPublicationOptionalLyricsOutcomePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.OpenMigrated(t)
	initializeRiver(t, ctx, db)
	repo := NewSetupManagerRepository(db)

	positionID, releaseID, trackID := uuid.New(), uuid.New(), uuid.New()
	operationID, candidateID, generationID := uuid.New(), uuid.New(), uuid.New()
	var epoch uuid.UUID
	if err := db.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &epoch); err != nil {
		t.Fatal(err)
	}
	const lyrics = "[ar:Artist]\r\n[00:01.00]Words\r\n"
	const revision = int64(7)
	input := publicationInputsFixture(t, "rewrite")
	input.Version = PublicationInputsVersion
	input.QualityPolicyVersion = publication.QualityPolicyVersion
	input.Rewrite.CurrentGeneration = generationID
	input.ManualLyrics = &PublicationManualLyricsInput{ExpectedMetadataRevision: revision, ConfirmedCurrentGeneration: generationID, State: "value", Value: lyrics}
	input.DesiredTags = map[string][]string{"TITLE": {"Track"}}
	inputHash := sha256.Sum256([]byte("prepared copied input"))
	inputSHA := hex.EncodeToString(inputHash[:])
	input.Rewrite.Ownership.SHA256 = inputSHA
	versions, ffmpegBanner, ffprobeBanner := publicationFFmpegVersionFixture()
	installationID := input.Tools[0].InstallationID
	verified := time.Now().UTC()
	installation := &ToolInstallation{ID: installationID, PackageKind: "ffmpeg", PlatformGOOS: runtime.GOOS, PlatformGOARCH: runtime.GOARCH, SourceName: "fixture", ReleaseIdentity: "release-1", State: "ready", ExecutableVersions: versions, ArtifactIdentities: json.RawMessage(`{}`), VerifiedAt: &verified}
	installation.RelativePath = filepath.Join("ffmpeg", "release-1")
	if err := repo.CreateInstallation(ctx, installation); err != nil {
		t.Fatal(err)
	}
	input.Tools = []SourceAnalysisToolSelection{
		{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: filepath.ToSlash(installation.RelativePath), Executable: "ffmpeg", Version: "7.0", VersionBanner: ffmpegBanner},
		{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: filepath.ToSlash(installation.RelativePath), Executable: "ffprobe", Version: "7.0", VersionBanner: ffprobeBanner},
	}
	frozen, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	ownership, err := json.Marshal(PublicationCurrentOwnership{Version: 1, Identity: input.Rewrite.Ownership})
	if err != nil {
		t.Fatal(err)
	}
	audio, err := json.Marshal(PublicationCurrentAudio{Version: 1, Facts: input.Rewrite.AudioFacts})
	if err != nil {
		t.Fatal(err)
	}
	metadata := json.RawMessage(`{"version":1,"tags":{"TITLE":["Track"]}}`)
	if _, err := db.ExecContext(ctx, `INSERT INTO local_release(id,title) VALUES (?, 'lyrics outcome')`, releaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?, 'lyrics outcome')`, trackID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO release_track(id,release_id,track_id,position,metadata,metadata_revision) VALUES (?,?,?,0,'{}'::jsonb,?)`, positionID, releaseID, trackID, revision); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at) VALUES (?,?,?,'Artist/Album/track.mka',?::jsonb,?::jsonb,?::jsonb,'{}'::jsonb,1,now())`, positionID, epoch, generationID, string(ownership), string(audio), string(metadata)); err != nil {
		t.Fatal(err)
	}
	var jobID int64
	if err := db.NewRaw(`INSERT INTO river_job(kind,args,state,attempt) VALUES ('publish_track_v1','{}','running',1) RETURNING id`).Scan(ctx, &jobID); err != nil {
		t.Fatal(err)
	}
	snapshot := `{"version":1,"position_id":"` + positionID.String() + `","output_epoch":"` + epoch.String() + `","intent_kind":"rewrite"}`
	manifest := []PublicationNamespaceNode{
		{CanonicalKey: "Artist", RelativePath: "Artist", NodeKind: "dir", DirectoryScope: "artist"},
		{CanonicalKey: "Artist/Album", RelativePath: "Artist/Album", NodeKind: "dir", DirectoryScope: "album"},
		{CanonicalKey: input.Rewrite.RelativePath, RelativePath: input.Rewrite.RelativePath, NodeKind: "file"},
	}
	manifestJSON, err := json.Marshal(struct {
		Version int                        `json:"version"`
		Nodes   []PublicationNamespaceNode `json:"nodes"`
	}{Version: 1, Nodes: manifest})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO operation(id,kind,state,stage,input_snapshot,attempt,river_job_id) VALUES (?,'publish_track_v1','queued','queued',?::jsonb,1,?)`, operationID, snapshot, jobID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO track_publication_attempt(operation_id,position_id,epoch,expected_generation,candidate_generation,intent_kind,phase,frozen_inputs,namespace_manifest,mutation_journal) VALUES (?,?,?,?,?,'rewrite','queued',?::jsonb,?::jsonb,'[]'::jsonb)`, operationID, positionID, epoch, generationID, candidateID, string(frozen), string(manifestJSON))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, node := range manifest {
		digest := sha256.Sum256([]byte(node.CanonicalKey))
		var owner any = positionID
		var scope any
		if node.NodeKind == "dir" {
			owner, scope = nil, node.DirectoryScope
		}
		var claimID = uuid.New()
		if _, err := db.ExecContext(ctx, `INSERT INTO publication_namespace_claim(id,epoch,key_digest,canonical_key,relative_path,node_kind,owner_position_id,directory_scope_key) VALUES (?,?,?,?,?,?,?,?)`, claimID, epoch, digest[:], node.CanonicalKey, node.RelativePath, node.NodeKind, owner, scope); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO publication_namespace_reservation(namespace_claim_id,position_id,generation) VALUES (?,?,?)`, claimID, positionID, candidateID); err != nil {
			t.Fatal(err)
		}
	}
	guard := PublicationDeliveryGuard{OperationID: operationID, ApplicationAttempt: 1, RiverJobID: jobID, RiverAttempt: 1, ClaimUUID: uuid.New(), BootUUID: uuid.New(), DeliveryToken: uuid.NewString(), Epoch: epoch, CandidateGeneration: candidateID, ExpectedPositionID: positionID}
	if outcome, err := repo.ClaimPublicationDelivery(ctx, guard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim = %q, %v", outcome, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE track_publication_attempt SET phase='preparing' WHERE operation_id=?`, operationID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE operation SET stage='preparing' WHERE id=?`, operationID); err != nil {
		t.Fatal(err)
	}

	prepOwner := uuid.New()
	parent := PublicationDirectoryIdentity{Platform: "darwin", Volume: "fixture-volume", FileID: "output-root"}
	directory := PublicationDirectoryIdentity{Platform: "darwin", Volume: "fixture-volume", FileID: "private-dir"}
	base := PublicationPreparationRecord{Version: PublicationPreparationVersion, Kind: "publication_preparation", OperationID: operationID, ClaimUUID: guard.ClaimUUID, OwnerToken: prepOwner, PrivateDirRel: "private/op", InputRel: "private/op/input.mka", OutputRel: "private/op/output.mka", ParentIdentity: parent}
	ready := base
	size, mtime, copiedMtime := *input.Rewrite.Ownership.SizeBytes, int64(200), int64(300)
	ready.Step, ready.DirectoryIdentity, ready.InputSize, ready.InputMTime = "input_ready", &directory, &size, &mtime
	ready.CopiedInput = &PublicationFileIdentity{SizeBytes: &size, MTime: &copiedMtime, SHA256: inputSHA}
	preparations := []PublicationPreparationRecord{base, base, ready}
	preparations[0].Step = "intent"
	preparations[1].Step, preparations[1].DirectoryIdentity = "directory_created", &directory
	for _, record := range preparations {
		encoded, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE track_publication_attempt SET mutation_journal=mutation_journal || ?::jsonb WHERE operation_id=?`, `[`+string(encoded)+`]`, operationID); err != nil {
			t.Fatal(err)
		}
	}

	outputDigest := sha256.Sum256([]byte("observed output"))
	outputSHA := hex.EncodeToString(outputDigest[:])
	outputSize, outputMtime := int64(100), int64(200)
	actualLyricsDigest := sha256.Sum256([]byte(lyrics))
	assets, err := json.Marshal(PublicationAssetOutcomes{Version: PublicationAssetOutcomesVersion, Cover: PublicationCoverOutcome{State: PublicationAssetAbsent}, Lyrics: PublicationLyricsOutcome{State: PublicationAssetEmbedded, SHA256: hex.EncodeToString(actualLyricsDigest[:]), Format: "lrc"}})
	if err != nil {
		t.Fatal(err)
	}
	actualMetadata, err := json.Marshal(PublicationCommitMetadata{Version: 1, Tags: map[string][]string{"TITLE": {"Track"}, "LYRICS": {lyrics}}})
	if err != nil {
		t.Fatal(err)
	}
	actualAudio, err := json.Marshal(PublicationCurrentAudio{Version: 1, Facts: input.Rewrite.AudioFacts})
	if err != nil {
		t.Fatal(err)
	}
	observation := PublicationStageObservation{Version: 1, RelativePath: ready.OutputRel, Ownership: ArtifactOwnership{ExpectedSize: &outputSize, ExpectedMTime: &outputMtime}, PayloadSHA256: outputSHA, ActualAudio: actualAudio, ActualMetadata: actualMetadata, ActualAssets: assets, QualityPolicyVersion: input.QualityPolicyVersion}
	evidence := PublicationOptionalLyricsOutcomeEvidence{Kind: PublicationOptionalLyricsExactRoundTrip, ManualLyrics: *input.ManualLyrics, Preparation: ready, InputIdentity: PublicationFileIdentity{SizeBytes: &size, MTime: &copiedMtime, SHA256: inputSHA}, OutputIdentity: PublicationFileIdentity{SizeBytes: &outputSize, MTime: &outputMtime, SHA256: outputSHA}, FinalObservation: observation}

	assertPreparationOnly := func(label string, attempt *TrackPublicationAttempt) {
		t.Helper()
		var entries []json.RawMessage
		if err := json.Unmarshal(attempt.MutationJournal, &entries); err != nil {
			t.Fatal(err)
		}
		if len(entries) != 3 {
			t.Fatalf("%s journal entries = %d, want preparation-only 3", label, len(entries))
		}
	}
	initial, err := repo.ReadPublicationAttempt(ctx, operationID)
	if err != nil {
		t.Fatal(err)
	}
	assertPreparationOnly("initial", initial)
	// A new desired-metadata revision after admission must not invalidate the
	// frozen work; recording its receipt is still tied to the admitted snapshot.
	if _, err := db.ExecContext(ctx, `UPDATE release_track SET metadata_revision=metadata_revision+1 WHERE id=?`, positionID); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *PublicationDeliveryGuard, *PublicationOptionalLyricsOutcomeEvidence)
	}{
		{name: "wrong guard", mutate: func(_ *testing.T, g *PublicationDeliveryGuard, _ *PublicationOptionalLyricsOutcomeEvidence) {
			g.ClaimUUID = uuid.New()
		}},
		{name: "prefix owner", mutate: func(_ *testing.T, _ *PublicationDeliveryGuard, e *PublicationOptionalLyricsOutcomeEvidence) {
			e.Preparation.OwnerToken = uuid.New()
		}},
		{name: "missing quality policy", mutate: func(_ *testing.T, _ *PublicationDeliveryGuard, e *PublicationOptionalLyricsOutcomeEvidence) {
			e.FinalObservation.QualityPolicyVersion = 0
		}},
		{name: "foreign quality policy", mutate: func(_ *testing.T, _ *PublicationDeliveryGuard, e *PublicationOptionalLyricsOutcomeEvidence) {
			e.FinalObservation.QualityPolicyVersion++
		}},
		{name: "missing candidate reservation", mutate: func(t *testing.T, g *PublicationDeliveryGuard, _ *PublicationOptionalLyricsOutcomeEvidence) {
			if _, err := db.ExecContext(ctx, `DELETE FROM publication_namespace_reservation WHERE generation=? AND position_id=?`, g.CandidateGeneration, g.ExpectedPositionID); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			badGuard, badEvidence := guard, evidence
			test.mutate(t, &badGuard, &badEvidence)
			outcome, callErr := repo.GuardedRecordPublicationOptionalLyricsOutcome(ctx, badGuard, badEvidence)
			if outcome != PublicationOptionalLyricsStale {
				t.Fatalf("rejected evidence outcome = %q, want stale (err %v)", outcome, callErr)
			}
			current, err := repo.ReadPublicationAttempt(ctx, operationID)
			if err != nil {
				t.Fatal(err)
			}
			if string(current.MutationJournal) != string(initial.MutationJournal) {
				t.Fatal("rejection appended to journal")
			}
			assertPreparationOnly("negative", current)
		})
	}
	// Restore the reservation deleted by its negative case before the positive
	// append; each rejection above leaves the journal untouched.
	for _, node := range manifest {
		var claimID uuid.UUID
		if err := db.NewRaw(`SELECT id FROM publication_namespace_claim WHERE epoch=? AND canonical_key=?`, epoch, node.CanonicalKey).Scan(ctx, &claimID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO publication_namespace_reservation(namespace_claim_id,position_id,generation) VALUES (?,?,?)`, claimID, positionID, candidateID); err != nil {
			t.Fatal(err)
		}
	}
	if outcome, err := repo.GuardedRecordPublicationOptionalLyricsOutcome(ctx, guard, evidence); err != nil || outcome != PublicationOptionalLyricsRecorded {
		t.Fatalf("record = %q, %v", outcome, err)
	}
	first, err := repo.ReadPublicationAttempt(ctx, operationID)
	if err != nil {
		t.Fatal(err)
	}
	var journal []json.RawMessage
	if err := json.Unmarshal(first.MutationJournal, &journal); err != nil || len(journal) != 4 {
		t.Fatalf("journal entries = %d, %v", len(journal), err)
	}
	for _, test := range []struct {
		name    string
		entries []json.RawMessage
	}{
		{name: "trailing unknown", entries: append(append([]json.RawMessage(nil), journal...), json.RawMessage(`{"kind":"unknown"}`))},
		{name: "interleaved after receipt", entries: []json.RawMessage{journal[0], journal[1], journal[2], journal[3], journal[3]}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, _, valid := parseOptionalLyricsOutcomeJournal(test.entries, evidence, guard); valid {
				t.Fatal("malformed journal accepted")
			}
		})
	}
	var got publicationOptionalLyricsOutcomeJournalRecord
	if _, _, valid := parseOptionalLyricsOutcomeJournal(journal, evidence, guard); !valid {
		t.Fatal("recorded journal does not replay against supplied evidence")
	}
	if err := decodeStrictJSON(journal[3], &got); err != nil || got.Kind != "publication_optional_lyrics_outcome" || got.Outcome != evidence.Kind {
		t.Fatalf("journal outcome = %#v, %v", got, err)
	}
	if outcome, err := repo.GuardedRecordPublicationOptionalLyricsOutcome(ctx, guard, evidence); err != nil || outcome != PublicationOptionalLyricsAlreadyRecorded {
		t.Fatalf("replay = %q, %v", outcome, err)
	}
	afterReplay, err := repo.ReadPublicationAttempt(ctx, operationID)
	if err != nil || string(afterReplay.MutationJournal) != string(first.MutationJournal) {
		t.Fatalf("replay changed journal: %v", err)
	}

}
