//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
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
