//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/uptrace/bun"
)

// A failed publication may retain source ownership after its delivery has
// quiesced. Removing that source root archives the frozen attempt before
// retiring the live attempt and its authority.
func TestDeleteSourceRootArchivesFailedPublicationWithRetainedHold(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newPublicationExecutionFixture(t)
	repo, rootID, _, operationID := pubSourceProtectionHoldWithFixture(t, f)
	if outcome, err := NewSetupManagerRepository(f.db).ClaimPublicationDelivery(ctx, f.guard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim publication delivery = %q, %v", outcome, err)
	}
	insertAbandonmentNamespaceFixture(t, f)
	stale := f.guard
	stale.DeliveryToken = uuid.NewString()
	if err := NewSetupManagerRepository(f.db).MarkPublicationDeliveryReturned(ctx, stale); err != nil {
		t.Fatal(err)
	}
	var claimState string
	if err := f.db.NewRaw(`SELECT state FROM publication_execution_claim WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &claimState); err != nil || claimState != "executing" {
		t.Fatalf("stale return changed claim state to %q, %v; want executing", claimState, err)
	}

	if _, err := f.db.ExecContext(ctx, `UPDATE river_job SET state='cancelled', finalized_at=now() WHERE id=? AND kind='publish_track_v1'`, f.guard.RiverJobID); err != nil {
		t.Fatal(err)
	}
	if changed, err := NewSetupManagerRepository(f.db).FailTerminalPublication(ctx, TerminalPublicationCandidate{
		OperationID: operationID, ApplicationAttempt: f.guard.ApplicationAttempt,
		RiverJobID: f.guard.RiverJobID, RiverState: "cancelled",
	}); err != nil || !changed {
		t.Fatalf("terminalize publication = %v, %v; want true", changed, err)
	}
	if err := NewSetupManagerRepository(f.db).MarkPublicationDeliveryReturned(ctx, f.guard); err != nil {
		t.Fatalf("mark delivery returned: %v", err)
	}
	var originalAttempt string
	if err := f.db.NewRaw(`SELECT to_jsonb(a)::text FROM track_publication_attempt a WHERE operation_id=?`, operationID).Scan(ctx, &originalAttempt); err != nil {
		t.Fatal(err)
	}
	var originalNamespaceIDs []uuid.UUID
	if err := f.db.NewRaw(`SELECT namespace_claim_id FROM publication_namespace_reservation WHERE position_id=? AND generation=? ORDER BY namespace_claim_id`, f.guard.ExpectedPositionID, f.guard.CandidateGeneration).Scan(ctx, &originalNamespaceIDs); err != nil {
		t.Fatal(err)
	}

	var before int
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_source_hold WHERE operation_id=? AND source_root_id=?`, operationID, rootID).Scan(ctx, &before); err != nil || before != 1 {
		t.Fatalf("retained hold baseline = %d, %v; want 1", before, err)
	}
	// A stale delivery fence must not return the active claim; an executing
	// claim independently prevents abandonment even after the delivery returned.
	if _, err := f.db.ExecContext(ctx, `UPDATE publication_execution_claim SET state='executing' WHERE operation_id=?`, operationID); err != nil {
		t.Fatal(err)
	}
	root, err := repo.GetSourceRoot(ctx, rootID)
	if err != nil {
		t.Fatal(err)
	}
	locations, err := repo.CountSourceLocations(ctx, rootID)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteSourceRootWithCleanup(ctx, rootID, root.ConfiguredPath, locations, func(context.Context, string, []SourceAnalysisArtifact) error { return nil }); err == nil {
		t.Fatal("root deletion succeeded with an executing publication claim")
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE publication_execution_claim SET state='quiescent' WHERE operation_id=?`, operationID); err != nil {
		t.Fatal(err)
	}
	injected := context.DeadlineExceeded
	callbackReached := false
	if err := repo.DeleteSourceRootWithCleanup(ctx, rootID, root.ConfiguredPath, locations, func(context.Context, string, []SourceAnalysisArtifact) error {
		callbackReached = true
		return injected
	}); !errors.Is(err, injected) || !callbackReached {
		t.Fatalf("cleanup failure = %v, callback reached = %v; want injected failure", err, callbackReached)
	}
	var rolledBack int
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_root_abandonment WHERE operation_id=?`, operationID).Scan(ctx, &rolledBack); err != nil || rolledBack != 0 {
		t.Fatalf("archive after injected cleanup failure = %d, %v; want 0", rolledBack, err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_namespace_reservation WHERE position_id=? AND generation=?`, f.guard.ExpectedPositionID, f.guard.CandidateGeneration).Scan(ctx, &rolledBack); err != nil || rolledBack != len(originalNamespaceIDs) {
		t.Fatalf("candidate authority after rollback = %d, %v; want %d", rolledBack, err, len(originalNamespaceIDs))
	}
	if err := repo.DeleteSourceRootWithCleanup(ctx, rootID, root.ConfiguredPath, locations, func(context.Context, string, []SourceAnalysisArtifact) error { return nil }); err != nil {
		t.Fatalf("delete root with quiesced failed publication: %v", err)
	}

	for label, query := range map[string]string{
		"root":             `SELECT count(*) FROM source_root WHERE id=?`,
		"failed operation": `SELECT count(*) FROM operation WHERE id=?`,
		"attempt":          `SELECT count(*) FROM track_publication_attempt WHERE operation_id=?`,
		"hold":             `SELECT count(*) FROM publication_source_hold WHERE operation_id=?`,
		"execution claim":  `SELECT count(*) FROM publication_execution_claim WHERE operation_id=?`,
	} {
		var count int
		arg := operationID
		if label == "root" {
			arg = rootID
		}
		if err := f.db.NewRaw(query, arg).Scan(ctx, &count); err != nil || count != 0 {
			t.Errorf("%s after root abandonment = %d, %v; want 0", label, count, err)
		}
	}
	var archived int
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_root_abandonment WHERE operation_id=? AND position_id=?`, operationID, f.guard.ExpectedPositionID).Scan(ctx, &archived); err != nil || archived != 1 {
		t.Fatalf("archived frozen publication = %d, %v; want 1", archived, err)
	}
	var frozenInputs string
	if err := f.db.NewRaw(`SELECT evidence->'attempt'->>'frozen_inputs' FROM publication_root_abandonment WHERE operation_id=? AND position_id=?`, operationID, f.guard.ExpectedPositionID).Scan(ctx, &frozenInputs); err != nil {
		t.Fatal(err)
	}
	var archivedAttempt string
	if err := f.db.NewRaw(`SELECT (evidence->'attempt')::text FROM publication_root_abandonment WHERE operation_id=? AND position_id=?`, operationID, f.guard.ExpectedPositionID).Scan(ctx, &archivedAttempt); err != nil {
		t.Fatal(err)
	}
	var archivedAttemptValue, originalAttemptValue any
	if err := json.Unmarshal([]byte(archivedAttempt), &archivedAttemptValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(originalAttempt), &originalAttemptValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(archivedAttemptValue, originalAttemptValue) || frozenInputs == "" || frozenInputs == "{}" {
		t.Fatalf("archive did not preserve original attempt: archived=%s original=%s", archivedAttempt, originalAttempt)
	}
	var archivedNamespaceIDs []uuid.UUID
	if err := f.db.NewRaw(`SELECT (item->>'namespace_claim_id')::uuid FROM publication_root_abandonment, jsonb_array_elements(evidence->'candidate_reservations') item WHERE operation_id=? ORDER BY 1`, operationID).Scan(ctx, &archivedNamespaceIDs); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(archivedNamespaceIDs, originalNamespaceIDs) {
		t.Fatalf("archived namespace reservations = %v; original = %v", archivedNamespaceIDs, originalNamespaceIDs)
	}
	for _, statement := range []string{
		`UPDATE publication_root_abandonment SET operation_id=? WHERE operation_id=?`,
		`DELETE FROM publication_root_abandonment WHERE operation_id=?`,
	} {
		args := []any{operationID}
		if statement[:6] == "UPDATE" {
			args = []any{uuid.New(), operationID}
		}
		if _, err := f.db.ExecContext(ctx, statement, args...); err == nil {
			t.Errorf("archive mutation unexpectedly succeeded: %s", statement)
		}
	}
}

func TestInterruptedSourceRepublishAcquiresStickyRecoveryClaimAndPreservesLegacyAbandonment(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, _, _, _ := prepareManualLyricsCommitFixture(t)
	repo := NewSetupManagerRepository(f.db)
	var current TrackPublication
	if err := f.db.NewSelect().Model(&current).Where("position_id=?", f.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	var ownership PublicationCurrentOwnership
	if err := decodePublicationCurrentContract(current.Ownership, &ownership); err != nil {
		t.Fatal(err)
	}
	audioFacts, ok := publicationAudioFactsFromObservation(current.ActualAudio)
	if !ok {
		t.Fatal("decode current audio facts")
	}
	assets, err := DecodePublicationAssetOutcomes(current.ActualAssets)
	if err != nil {
		t.Fatal(err)
	}
	var metadata PublicationCommitMetadata
	if err := strictMetadata(current.ActualMetadata, &metadata); err != nil {
		t.Fatal(err)
	}
	witness := PublicationCurrentWitness{
		Generation: current.Generation, RelativePath: current.RelativePath, Ownership: ownership.Identity,
		AudioFacts: audioFacts, Assets: assets, Tags: metadata.Tags, ArtworkInventory: []PublicationArtworkWitness{},
	}
	var authority PositionSourceAuthority
	if err := f.db.NewSelect().Model(&authority).Where("position_id=?", f.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	var releaseID uuid.UUID
	var positionMetadata []byte
	var metadataRevision int64
	if err := f.db.NewRaw(`SELECT release_id,metadata,metadata_revision FROM release_track WHERE id=?`, f.guard.ExpectedPositionID).
		Scan(ctx, &releaseID, &positionMetadata, &metadataRevision); err != nil {
		t.Fatal(err)
	}
	var positionMetadataRoot map[string]json.RawMessage
	if err := json.Unmarshal(positionMetadata, &positionMetadataRoot); err != nil {
		t.Fatalf("decode saved position metadata: %v", err)
	}
	lyricsEnvelope, err := librarymetadata.DecodeEnvelope(positionMetadataRoot["local_metadata"])
	if err != nil || lyricsEnvelope.Scope != librarymetadata.ScopePosition {
		t.Fatalf("decode saved lyrics authority: %+v, %v", lyricsEnvelope, err)
	}
	lyricsAuthority, ok := lyricsEnvelope.Desired["synced_lyrics"]
	lyricsText, textOK := lyricsAuthority.Value.(string)
	if !ok || !textOK || lyricsAuthority.State != librarymetadata.StateValue || lyricsAuthority.Authority != librarymetadata.AuthorityManual {
		t.Fatalf("saved lyrics authority = %+v", lyricsAuthority)
	}
	lyricsAuthorityCopy := lyricsAuthority
	input := publicationInputsFixture(t, "audio")
	input.Version = PublicationSourceRepublishInputsVersion
	input.SourceAuthorityMode = ""
	input.InitialLyrics, input.AudioUpgrade, input.ProviderTags = nil, nil, nil
	input.DesiredTags = metadata.Tags
	rootPath := t.TempDir()
	input.Audio.ConfiguredRootPath = rootPath
	input.Audio.InventoryRootPath = filepath.Join(rootPath, "inventory")
	input.SourceRepublish = &PublicationSourceRepublishInput{
		Current: witness, Mode: "ordinary",
		SourceAuthority: PublicationSourceAuthority{
			Present: true, PositionID: authority.PositionID, Mode: authority.Mode, Revision: authority.Revision,
			PublicationEpoch: authority.PublicationEpoch, PublicationGeneration: authority.PublicationGeneration,
			SourceLocationID: input.Audio.LocationID, MediaVariantID: input.Audio.MediaVariantID,
		},
		Association:     PublicationRepublishAssociation{ID: uuid.New(), LocationID: input.Audio.LocationID, MediaVariantID: input.Audio.MediaVariantID},
		Lyrics:          PublicationRepublishLyrics{State: "value", Origin: "manual", Text: lyricsText, SHA256: testAssetDigest(lyricsText)},
		LyricsAuthority: PublicationInitialLyricsInput{ExpectedMetadataRevision: metadataRevision, Desired: &lyricsAuthorityCopy},
		Cover:           PublicationRepublishCover{ReleaseID: releaseID, AuthorityRevision: 1, AuthorityMode: "automatic", Derivation: "absent"},
	}
	if err := input.Validate(); err != nil {
		t.Fatalf("validate v4 frozen inputs: %v", err)
	}
	if err := f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE track_publication_attempt SET intent_kind='audio',frozen_inputs=?::jsonb WHERE operation_id=?`,
			string(mustJSON(input)), f.guard.OperationID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE operation SET input_snapshot=jsonb_set(input_snapshot,'{intent_kind}',?::jsonb,true) WHERE id=?`,
			string(mustJSON("audio")), f.guard.OperationID); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rootID := input.Audio.RootID
	locationID, workID, variantID := input.Audio.LocationID, input.Audio.WorkID, input.Audio.MediaVariantID
	if _, err := f.db.ExecContext(ctx, `INSERT INTO source_root(id,configured_path,display_name,scan_generation,inventory_path) VALUES (?,?, 'Republish source',1,?)`,
		rootID, rootPath, input.Audio.InventoryRootPath); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes) VALUES (?,?)`, variantID, input.Audio.SizeBytes); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `INSERT INTO source_location(id,source_root_id,relative_path,size_bytes,mtime,last_seen_scan_generation,probe_status,media_variant_id) VALUES (?,?,?,?,?,1,'audio',?)`,
		locationID, rootID, input.Audio.RelativePath, input.Audio.SizeBytes, input.Audio.MTime, variantID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE position_source_authority SET source_location_id=?,media_variant_id=? WHERE position_id=?`,
		locationID, variantID, f.guard.ExpectedPositionID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `INSERT INTO source_analysis_work(id,location_id,source_root_id,configured_path,inventory_path,relative_path,size_bytes,mtime,sha256_enabled,origin_scan_operation_id,current_location_id) VALUES (?,?,?,?,?,?,?,?,false,?,?)`,
		workID, locationID, rootID, rootPath, input.Audio.InventoryRootPath, input.Audio.RelativePath, input.Audio.SizeBytes, input.Audio.MTime, f.guard.OperationID, locationID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `INSERT INTO publication_source_hold(operation_id,application_attempt,river_job_id,epoch,hold_uuid,source_root_id,source_location_id,work_id,media_variant_id,configured_path,inventory_path,relative_path,size_bytes,mtime) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		f.guard.OperationID, f.guard.ApplicationAttempt, f.guard.RiverJobID, f.guard.Epoch, uuid.New(), rootID, locationID, workID, variantID,
		rootPath, input.Audio.InventoryRootPath, input.Audio.RelativePath, input.Audio.SizeBytes, input.Audio.MTime); err != nil {
		t.Fatal(err)
	}
	redelivery := f.guard
	redelivery.RiverAttempt++
	redelivery.ClaimUUID, redelivery.BootUUID, redelivery.DeliveryToken = uuid.New(), uuid.New(), uuid.NewString()
	if _, err := f.db.ExecContext(ctx, `UPDATE river_job SET attempt=? WHERE id=?`, redelivery.RiverAttempt, f.guard.RiverJobID); err != nil {
		t.Fatal(err)
	}
	if outcome, err := repo.ClaimPublicationDelivery(ctx, redelivery); err != nil || outcome != PublicationDeliverySourceRepublishRecovery {
		t.Fatalf("interrupted v4 delivery = %q, %v; want source-republish recovery", outcome, err)
	}
	var claim PublicationExecutionClaim
	if err := f.db.NewSelect().Model(&claim).Where("operation_id=?", f.guard.OperationID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	attempt, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if claim.State != "executing" || claim.ClaimUUID != redelivery.ClaimUUID || claim.BootUUID != redelivery.BootUUID ||
		claim.DeliveryToken != redelivery.DeliveryToken || claim.RiverAttempt != redelivery.RiverAttempt ||
		attempt.ResumeMode != "inspect_source_republish_target" {
		t.Fatalf("recovery successor = claim:%+v resume mode:%q", claim, attempt.ResumeMode)
	}
	recoveryInputs, live, err := repo.PublicationPreparationInputs(ctx, redelivery)
	if err != nil || !live || recoveryInputs.SourceRepublish == nil {
		t.Fatalf("read guarded source-republish recovery inputs = live:%t inputs:%+v err:%v", live, recoveryInputs, err)
	}
	if resumed, err := repo.ResumeMissingSourceRepublishPreparation(ctx, f.guard); err != nil || resumed {
		t.Fatalf("stale guard resumed missing-target recovery = %t, %v", resumed, err)
	}
	attempt, err = repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil || attempt.ResumeMode != PublicationResumeModeInspectSourceRepublishTarget {
		t.Fatalf("stale missing-target guard changed resume mode: attempt:%+v err:%v", attempt, err)
	}
	if resumed, err := repo.ResumeMissingSourceRepublishPreparation(ctx, redelivery); err != nil || !resumed {
		t.Fatalf("live guard did not resume after missing-target proof = %t, %v", resumed, err)
	}
	attempt, err = repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil || attempt.ResumeMode != PublicationResumeModePrepare {
		t.Fatalf("missing-target resume did not restore preparation mode: attempt:%+v err:%v", attempt, err)
	}
	if ok, err := repo.CheckPublicationDelivery(ctx, f.guard); err != nil || ok {
		t.Fatalf("old delivery guard is still live = %t, %v", ok, err)
	}
	if ok, err := repo.CheckPublicationDelivery(ctx, redelivery); err != nil || !ok {
		t.Fatalf("successor recovery guard is not live = %t, %v", ok, err)
	}
	if err := repo.MarkPublicationDeliveryReturned(ctx, redelivery); err != nil {
		t.Fatalf("return first recovery successor: %v", err)
	}
	reprepare := redelivery
	reprepare.ClaimUUID, reprepare.BootUUID, reprepare.DeliveryToken = uuid.New(), uuid.New(), uuid.NewString()
	if outcome, err := repo.ClaimPublicationDelivery(ctx, reprepare); err != nil || outcome != PublicationDeliveryReprepare {
		t.Fatalf("quiescent v4 preparation retry = %q, %v; want reprepare", outcome, err)
	}
	if ok, err := repo.CheckPublicationDelivery(ctx, redelivery); err != nil || ok {
		t.Fatalf("superseded recovery guard is still live = %t, %v", ok, err)
	}
	if ok, err := repo.CheckPublicationDelivery(ctx, reprepare); err != nil || !ok {
		t.Fatalf("reprepare successor guard is not live = %t, %v", ok, err)
	}
	preparationInputs, valid, err := repo.PublicationPreparationInputs(ctx, reprepare)
	if err != nil || !valid || preparationInputs.SourceRepublish == nil {
		t.Fatalf("reprepare did not restore normal preparation inputs = live:%t inputs:%+v err:%v", valid, preparationInputs, err)
	}
	oldBoot := uuid.New()
	if oldBoot == reprepare.BootUUID {
		t.Fatal("simulated previous boot matches the reprepare delivery")
	}
	nextRecovery := reprepare
	nextRecovery.RiverAttempt++
	nextRecovery.ClaimUUID, nextRecovery.BootUUID, nextRecovery.DeliveryToken = uuid.New(), uuid.New(), uuid.NewString()
	if nextRecovery.BootUUID == oldBoot {
		nextRecovery.BootUUID = uuid.New()
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE publication_execution_claim SET boot_uuid=? WHERE operation_id=? AND state='executing'`, oldBoot, f.guard.OperationID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE river_job SET attempt=? WHERE id=?`, nextRecovery.RiverAttempt, f.guard.RiverJobID); err != nil {
		t.Fatal(err)
	}
	if outcome, err := repo.ClaimPublicationDelivery(ctx, nextRecovery); err != nil || outcome != PublicationDeliverySourceRepublishRecovery {
		t.Fatalf("cross-boot interruption after reprepare = %q, %v; want source-republish recovery", outcome, err)
	}
	redelivery = nextRecovery
	if err := repo.MarkPublicationDeliveryReturned(ctx, redelivery); err != nil {
		t.Fatalf("return current recovery successor: %v", err)
	}
	secondRecovery := redelivery
	secondRecovery.ClaimUUID, secondRecovery.BootUUID, secondRecovery.DeliveryToken = uuid.New(), uuid.New(), uuid.NewString()
	if outcome, err := repo.ClaimPublicationDelivery(ctx, secondRecovery); err != nil || outcome != PublicationDeliverySourceRepublishRecovery {
		t.Fatalf("sticky source-republish recovery claim = %q, %v", outcome, err)
	}
	if ok, err := repo.CheckPublicationDelivery(ctx, redelivery); err != nil || ok {
		t.Fatalf("superseded recovery guard is still live = %t, %v", ok, err)
	}
	if ok, err := repo.CheckPublicationDelivery(ctx, secondRecovery); err != nil || !ok {
		t.Fatalf("second recovery successor guard is not live = %t, %v", ok, err)
	}
	if err := repo.MarkPublicationDeliveryReturned(ctx, secondRecovery); err != nil {
		t.Fatalf("return sticky recovery successor: %v", err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE river_job SET state='cancelled',finalized_at=now() WHERE id=?`, f.guard.RiverJobID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE operation SET state='failed',stage='source_republish_execution_failed',safe_error='recovery delivery stopped',finished_at=now(),updated_at=now() WHERE id=?`, f.guard.OperationID); err != nil {
		t.Fatal(err)
	}
	var jobsBefore int
	if err := f.db.NewRaw(`SELECT count(*) FROM river_job`).Scan(ctx, &jobsBefore); err != nil {
		t.Fatal(err)
	}
	client := openPublicationRetryRiver(t, ctx, f.db)
	if _, err := f.db.ExecContext(ctx, `UPDATE river_job SET attempt=? WHERE id=?`, secondRecovery.RiverAttempt+1, secondRecovery.RiverJobID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RetryPublicationOperationAndEnqueue(ctx, f.guard.OperationID, client); err == nil {
		t.Fatal("retry accepted terminal River attempt that differs from the recovery claim")
	}
	var jobsAfterMismatch int
	if err := f.db.NewRaw(`SELECT count(*) FROM river_job`).Scan(ctx, &jobsAfterMismatch); err != nil || jobsAfterMismatch != jobsBefore {
		t.Fatalf("mismatched terminal retry changed River jobs: before=%d after=%d err=%v", jobsBefore, jobsAfterMismatch, err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE river_job SET attempt=? WHERE id=?`, secondRecovery.RiverAttempt, secondRecovery.RiverJobID); err != nil {
		t.Fatal(err)
	}
	retried, err := repo.RetryPublicationOperationAndEnqueue(ctx, f.guard.OperationID, client)
	if err != nil {
		t.Fatalf("retry guarded source-republish inspection: %v", err)
	}
	var jobsAfter int
	if err := f.db.NewRaw(`SELECT count(*) FROM river_job`).Scan(ctx, &jobsAfter); err != nil || jobsAfter != jobsBefore+1 {
		t.Fatalf("guarded retry jobs = before:%d after:%d err:%v", jobsBefore, jobsAfter, err)
	}
	attempt, err = repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil || attempt.ResumeMode != PublicationResumeModeInspectSourceRepublishTarget {
		t.Fatalf("retry cleared recovery resume mode: attempt:%+v err:%v", attempt, err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE river_job SET state='cancelled',finalized_at=now() WHERE id=?`, *retried.RiverJobID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE operation SET state='failed',stage='source_republish_execution_failed',safe_error='recovery delivery stopped',finished_at=now(),updated_at=now() WHERE id=?`, f.guard.OperationID); err != nil {
		t.Fatal(err)
	}
	// This explicitly seeds the pre-resume-mode parked-v4 shape so the legacy
	// root-abandonment path remains covered independently of inspection retries.
	if _, err := f.db.ExecContext(ctx, `UPDATE track_publication_attempt SET resume_mode=? WHERE operation_id=?`, PublicationResumeModePrepare, f.guard.OperationID); err != nil {
		t.Fatal(err)
	}
	legacyClaim := PublicationExecutionClaim{
		OperationID: f.guard.OperationID, ApplicationAttempt: retried.Attempt, RiverJobID: *retried.RiverJobID,
		RiverAttempt: 1, ClaimUUID: uuid.New(), BootUUID: uuid.New(), DeliveryToken: uuid.NewString(),
		Epoch: f.guard.Epoch, State: "recovery_required",
	}
	if _, err := f.db.NewInsert().Model(&legacyClaim).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE river_job SET attempt=1,state='cancelled',finalized_at=now() WHERE id=?`, *retried.RiverJobID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE operation SET stage='source_republish_recovery_required' WHERE id=?`, f.guard.OperationID); err != nil {
		t.Fatal(err)
	}
	var legacyJobsBefore int
	if err := f.db.NewRaw(`SELECT count(*) FROM river_job`).Scan(ctx, &legacyJobsBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RetryPublicationOperationAndEnqueue(ctx, f.guard.OperationID, client); err == nil {
		t.Fatal("retry accepted a legacy parked source republish")
	}
	var legacyJobsAfter int
	if err := f.db.NewRaw(`SELECT count(*) FROM river_job`).Scan(ctx, &legacyJobsAfter); err != nil || legacyJobsAfter != legacyJobsBefore {
		t.Fatalf("legacy parked retry changed River jobs: before=%d after=%d err=%v", legacyJobsBefore, legacyJobsAfter, err)
	}
	root, err := NewSourceInventoryRepository(f.db).GetSourceRoot(ctx, rootID)
	if err != nil {
		t.Fatal(err)
	}
	locations, err := NewSourceInventoryRepository(f.db).CountSourceLocations(ctx, rootID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE river_job SET state='running',finalized_at=NULL WHERE id=?`, *retried.RiverJobID); err != nil {
		t.Fatal(err)
	}
	if err := NewSourceInventoryRepository(f.db).DeleteSourceRootWithCleanup(ctx, rootID, root.ConfiguredPath, locations,
		func(context.Context, string, []SourceAnalysisArtifact) error { return nil }); err == nil {
		t.Fatal("root abandonment accepted a running source-republish delivery")
	}
	var stillPresent int
	if err := f.db.NewRaw(`SELECT count(*) FROM source_root WHERE id=?`, rootID).Scan(ctx, &stillPresent); err != nil || stillPresent != 1 {
		t.Fatalf("running-delivery rejection changed source root: count=%d err=%v", stillPresent, err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE river_job SET state='cancelled',finalized_at=now() WHERE id=?`, *retried.RiverJobID); err != nil {
		t.Fatal(err)
	}
	if err := NewSourceInventoryRepository(f.db).DeleteSourceRootWithCleanup(ctx, rootID, root.ConfiguredPath, locations,
		func(context.Context, string, []SourceAnalysisArtifact) error { return nil }); err != nil {
		t.Fatalf("abandon returned failed v4 publication: %v", err)
	}
	var retained TrackPublication
	if err := f.db.NewSelect().Model(&retained).Where("position_id=?", f.guard.ExpectedPositionID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if retained.Generation != current.Generation || retained.RelativePath != current.RelativePath {
		t.Fatalf("root abandonment changed CURRENT: %+v", retained)
	}
	var archiveCount, holdCount int
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_root_abandonment WHERE operation_id=? AND root_id=?`, f.guard.OperationID, rootID).Scan(ctx, &archiveCount); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_source_hold WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &holdCount); err != nil {
		t.Fatal(err)
	}
	if archiveCount != 1 || holdCount != 0 {
		t.Fatalf("abandonment evidence/hold = %d/%d; want 1/0", archiveCount, holdCount)
	}
	var archivedResumeMode string
	if err := f.db.NewRaw(`SELECT evidence->'attempt'->>'resume_mode' FROM publication_root_abandonment WHERE operation_id=? AND root_id=?`, f.guard.OperationID, rootID).
		Scan(ctx, &archivedResumeMode); err != nil {
		t.Fatal(err)
	}
	if archivedResumeMode != PublicationResumeModePrepare {
		t.Fatalf("archived resume mode = %q", archivedResumeMode)
	}
	var archivedLegacyClaimState string
	if err := f.db.NewRaw(`SELECT evidence->'source_republish_return'->'original_claim'->>'state' FROM publication_root_abandonment WHERE operation_id=? AND root_id=?`, f.guard.OperationID, rootID).
		Scan(ctx, &archivedLegacyClaimState); err != nil || archivedLegacyClaimState != "recovery_required" {
		t.Fatalf("legacy recovery claim evidence = %q, %v", archivedLegacyClaimState, err)
	}
	if _, err := repo.RetryPublicationOperationAndEnqueue(ctx, f.guard.OperationID, client); err == nil {
		t.Fatal("retry accepted root-abandoned source republish")
	}
}

func insertAbandonmentNamespaceFixture(t *testing.T, f publicationExecutionFixture) {
	t.Helper()
	ctx := context.Background()
	directoryID, fileID := uuid.New(), uuid.New()
	directoryKey, fileKey := uuid.NewString(), uuid.NewString()
	for _, node := range []struct {
		id              uuid.UUID
		key, path, kind string
	}{
		{directoryID, directoryKey, "Album", "dir"},
		{fileID, directoryKey + "/" + fileKey, "Album/Track.flac", "file"},
	} {
		var owner any = f.guard.ExpectedPositionID
		var scope any
		if node.kind == "dir" {
			owner = nil
			scope = "album-scope"
		}
		digest := publicationNamespaceKeyDigest(node.key)
		if _, err := f.db.ExecContext(ctx, `INSERT INTO publication_namespace_claim(id,epoch,key_digest,canonical_key,relative_path,node_kind,owner_position_id,directory_scope_key) VALUES (?,?,?,?,?,?,?,?)`, node.id, f.guard.Epoch, digest[:], node.key, node.path, node.kind, owner, scope); err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.ExecContext(ctx, `INSERT INTO publication_namespace_reservation(namespace_claim_id,position_id,generation) VALUES (?,?,?)`, node.id, f.guard.ExpectedPositionID, f.guard.CandidateGeneration); err != nil {
			t.Fatal(err)
		}
	}
	manifest, err := json.Marshal(map[string]any{"version": 1, "nodes": []any{
		map[string]any{"canonical_key": directoryKey, "relative_path": "Album", "node_kind": "dir", "directory_scope": "album-scope"},
		map[string]any{"canonical_key": directoryKey + "/" + fileKey, "relative_path": "Album/Track.flac", "node_kind": "file"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE track_publication_attempt SET namespace_manifest=?::jsonb WHERE operation_id=?`, string(manifest), f.guard.OperationID); err != nil {
		t.Fatal(err)
	}
}
