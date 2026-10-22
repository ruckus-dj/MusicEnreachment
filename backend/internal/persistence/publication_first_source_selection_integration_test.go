//go:build integration

package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

type firstSourceSelectionFixture struct {
	db       *bun.DB
	repo     *SetupManagerRepository
	position uuid.UUID
	epoch    uuid.UUID
	track    uuid.UUID
	old      PublicationSourceAssociation
	chosen   PublicationSourceAssociation
	source   PublicationSourceSelection
	frozen   PublicationAudioInput
	witness  FirstPublicationSourceSelection
}

func newFirstSourceSelectionFixture(t *testing.T) firstSourceSelectionFixture {
	t.Helper()
	ctx := context.Background()
	db := testpostgres.OpenMigrated(t)
	repo := NewSetupManagerRepository(db)
	releaseID, trackID, positionID := uuid.New(), uuid.New(), uuid.New()
	for _, query := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO local_release(id,title) VALUES (?,'source selection')`, []any{releaseID}},
		{`INSERT INTO local_track(id,title) VALUES (?,'source selection')`, []any{trackID}},
		{`INSERT INTO release_track(id,release_id,track_id,position) VALUES (?,?,?,0)`, []any{positionID, releaseID, trackID}},
	} {
		if _, err := db.ExecContext(ctx, query.query, query.args...); err != nil {
			t.Fatal(err)
		}
	}
	var epoch uuid.UUID
	if err := db.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &epoch); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2025, time.January, 2, 3, 4, 5, 123456000, time.UTC)
	rootID, locationID, oldWorkID, newWorkID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	oldVariantID, newVariantID, scanID := uuid.New(), uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO source_root(id,configured_path,display_name,scan_generation,inventory_path,enabled,status) VALUES (?,'/source','Source',1,'/source',true,'available')`, rootID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO operation(id,kind,state,stage,input_snapshot,finished_at) VALUES (?,'scan_source','succeeded','succeeded','{}'::jsonb,now())`, scanID); err != nil {
		t.Fatal(err)
	}
	for _, variantID := range []uuid.UUID{oldVariantID, newVariantID} {
		if _, err := db.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes) VALUES (?,42)`, variantID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO source_location(id,source_root_id,relative_path,size_bytes,mtime,last_seen_scan_generation,probe_status,media_variant_id) VALUES (? ,?,'track.flac',42,?,1,'audio',?)`, locationID, rootID, now, newVariantID); err != nil {
		t.Fatal(err)
	}
	oldLocationID := uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO source_location(id,source_root_id,relative_path,size_bytes,mtime,last_seen_scan_generation,probe_status,media_variant_id) VALUES (?,?,'old-track.flac',42,?,1,'audio',?)`, oldLocationID, rootID, now, oldVariantID); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct {
		work, variant, location uuid.UUID
		relative                string
	}{{oldWorkID, oldVariantID, oldLocationID, "old-track.flac"}, {newWorkID, newVariantID, locationID, "track.flac"}} {
		if _, err := db.ExecContext(ctx, `INSERT INTO source_analysis_work(id,location_id,source_root_id,configured_path,inventory_path,relative_path,size_bytes,mtime,sha256_enabled,origin_scan_operation_id,current_location_id) VALUES (?,?,?,'/source','/source',?,42,?,false,?,?)`, entry.work, entry.location, rootID, entry.relative, now, scanID, entry.location); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO source_work_variant_identity(work_id,media_variant_id) VALUES (?,?)`, entry.work, entry.variant); err != nil {
			t.Fatal(err)
		}
	}
	oldID, chosenID := uuid.New(), uuid.New()
	metadata := json.RawMessage(`{"owner":"kept"}`)
	policy := "policy-kept"
	if _, err := db.ExecContext(ctx, `INSERT INTO track_source(id,track_id,media_variant_id,selected,compatible,decision_method,policy_version,metadata) VALUES (?,?,?,true,true,'automatic',?,?)`, oldID, trackID, oldVariantID, policy, metadata); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO track_source(id,track_id,media_variant_id,selected,compatible,decision_method,policy_version,metadata) VALUES (?,?,?,false,true,'automatic',?,?)`, chosenID, trackID, newVariantID, "chosen-policy", json.RawMessage(`{"keep":true}`)); err != nil {
		t.Fatal(err)
	}
	var associations []TrackSource
	if err := db.NewSelect().Model(&associations).Where("track_id=?", trackID).Order("id ASC").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	byID := make(map[uuid.UUID]TrackSource, len(associations))
	witnesses := make([]PublicationSourceAssociation, 0, len(associations))
	for _, association := range associations {
		byID[association.ID] = association
		witnesses = append(witnesses, PublicationSourceAssociation{ID: association.ID, TrackID: association.TrackID, MediaVariantID: association.MediaVariantID, Selected: association.Selected, Compatible: association.Compatible, DecisionMethod: association.DecisionMethod, PolicyVersion: association.PolicyVersion, Metadata: association.Metadata, UpdatedAt: association.UpdatedAt})
	}
	old, chosen := byID[oldID], byID[chosenID]
	return firstSourceSelectionFixture{
		db: db, repo: repo, position: positionID, epoch: epoch, track: trackID,
		old:     PublicationSourceAssociation{ID: old.ID, TrackID: old.TrackID, MediaVariantID: old.MediaVariantID, Selected: old.Selected, Compatible: old.Compatible, DecisionMethod: old.DecisionMethod, PolicyVersion: old.PolicyVersion, Metadata: old.Metadata, UpdatedAt: old.UpdatedAt},
		chosen:  PublicationSourceAssociation{ID: chosen.ID, TrackID: chosen.TrackID, MediaVariantID: chosen.MediaVariantID, Selected: chosen.Selected, Compatible: chosen.Compatible, DecisionMethod: chosen.DecisionMethod, PolicyVersion: chosen.PolicyVersion, Metadata: chosen.Metadata, UpdatedAt: chosen.UpdatedAt},
		source:  PublicationSourceSelection{RootID: rootID, LocationID: locationID, WorkID: newWorkID, MediaVariantID: newVariantID, ConfiguredPath: "/source", InventoryPath: "/source", RelativePath: "track.flac", SizeBytes: 42, MTime: now},
		frozen:  PublicationAudioInput{RootID: rootID, LocationID: locationID, WorkID: newWorkID, MediaVariantID: newVariantID, ConfiguredRootPath: "/source", InventoryRootPath: "/source", RelativePath: "track.flac", SizeBytes: 42, MTime: now},
		witness: FirstPublicationSourceSelection{AssociationID: chosenID, Associations: witnesses},
	}
}

func TestFirstPublicationSourceSelectionPersistsExplicitChoiceAndReconcilesBorrowersPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newFirstSourceSelectionFixture(t)
	ctx := context.Background()
	// A second position borrows the same track and must also be reconciled.
	borrower := uuid.New()
	if _, err := f.db.ExecContext(ctx, `INSERT INTO release_track(id,release_id,track_id,position) SELECT ?,release_id,track_id,1 FROM release_track WHERE id=?`, borrower, f.position); err != nil {
		t.Fatal(err)
	}
	if err := f.db.RunInTx(ctx, &sql.TxOptions{}, func(ctx context.Context, tx bun.Tx) error {
		return f.repo.applyFirstPublicationSourceSelection(ctx, tx, f.position, f.epoch, f.source, f.frozen, f.witness)
	}); err != nil {
		t.Fatal(err)
	}
	var selected bool
	if err := f.db.NewRaw(`SELECT selected FROM track_source WHERE id=?`, f.chosen.ID).Scan(ctx, &selected); err != nil || !selected {
		t.Fatalf("chosen source selected=%v err=%v", selected, err)
	}
	var actual TrackSource
	if err := f.db.NewSelect().Model(&actual).Where("id=?", f.chosen.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if actual.DecisionMethod != f.chosen.DecisionMethod || actual.PolicyVersion == nil || *actual.PolicyVersion != *f.chosen.PolicyVersion || string(actual.Metadata) != string(f.chosen.Metadata) {
		t.Fatalf("selection changed unrelated decision fields: %+v", actual)
	}
	for _, position := range []uuid.UUID{f.position, borrower} {
		var count int
		if err := f.db.NewRaw(`SELECT count(*) FROM publication_reconciliation_request WHERE output_epoch=? AND position_id=?`, f.epoch, position).Scan(ctx, &count); err != nil || count != 1 {
			t.Fatalf("reconciliation request for %s count=%d err=%v", position, count, err)
		}
	}
}

func TestFirstPublicationSourceSelectionProtectsDeselectedCleanupOwnerPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repository, originalVariant := publicationSourceCleanupReadyFixture(t)
	if _, err := f.db.NewRaw(`UPDATE track_source SET selected=true WHERE media_variant_id=?`, originalVariant).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	owner, err := repository.AdmitPublicationSourceCleanup(ctx, PublicationSourceCleanupAdmission{PositionID: f.guard.ExpectedPositionID, ExpectedEpoch: f.guard.Epoch, ExpectedGeneration: f.guard.CandidateGeneration}, new(publicationSourceCleanupTestInserter))
	if err != nil || owner.OperationID == uuid.Nil {
		t.Fatalf("admit real cleanup owner=%+v,%v", owner, err)
	}
	var originalWork SourceAnalysisWork
	if err := f.db.NewSelect().Model(&originalWork).Where("id IN (SELECT work_id FROM source_work_variant_identity WHERE media_variant_id=?)", originalVariant).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	addCleanupRetrySourceCopies(t, ctx, f, originalWork, "source-choice-alternative.flac")
	var alternative SourceAnalysisWork
	if err := f.db.NewSelect().Model(&alternative).Where("relative_path=?", "source-choice-alternative.flac").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	var rows []TrackSource
	if err := f.db.NewSelect().Model(&rows).Where("track_id IN (SELECT track_id FROM release_track WHERE id=?)", f.guard.ExpectedPositionID).Order("id ASC").Scan(ctx); err != nil {
		t.Fatal(err)
	}
	selection := FirstPublicationSourceSelection{}
	var variant uuid.UUID
	for _, row := range rows {
		selection.Associations = append(selection.Associations, PublicationSourceAssociation{ID: row.ID, TrackID: row.TrackID, MediaVariantID: row.MediaVariantID, Selected: row.Selected, Compatible: row.Compatible, DecisionMethod: row.DecisionMethod, PolicyVersion: row.PolicyVersion, Metadata: row.Metadata, UpdatedAt: row.UpdatedAt})
		if row.MediaVariantID != originalVariant {
			selection.AssociationID, variant = row.ID, row.MediaVariantID
		}
	}
	err = f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return repository.applyFirstPublicationSourceSelection(ctx, tx, f.guard.ExpectedPositionID, f.guard.Epoch, PublicationSourceSelection{RootID: alternative.SourceRootID, LocationID: alternative.LocationID, WorkID: alternative.ID, MediaVariantID: variant}, PublicationAudioInput{MediaVariantID: variant}, selection)
	})
	if !errors.Is(err, ErrSourceCleanupOwned) || !errors.Is(err, ErrPublicationAdmissionConflict) {
		t.Fatalf("deselection of cleanup-owned variant error=%v", err)
	}
	var selected bool
	if err := f.db.NewRaw(`SELECT selected FROM track_source WHERE media_variant_id=?`, originalVariant).Scan(ctx, &selected); err != nil || !selected {
		t.Fatalf("cleanup-owned source selection changed=%t,%v", selected, err)
	}
}

func TestFirstPublicationSourceSelectionRejectsUpdatedAtWitnessABAPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newFirstSourceSelectionFixture(t)
	ctx := context.Background()
	if _, err := f.db.ExecContext(ctx, `UPDATE track_source SET updated_at=updated_at+interval '1 second' WHERE id=?`, f.chosen.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.db.RunInTx(ctx, &sql.TxOptions{}, func(ctx context.Context, tx bun.Tx) error {
		return f.repo.applyFirstPublicationSourceSelection(ctx, tx, f.position, f.epoch, f.source, f.frozen, f.witness)
	}); !errors.Is(err, ErrPublicationAdmissionConflict) {
		t.Fatalf("updated timestamp witness error=%v", err)
	}
	var selected bool
	if err := f.db.NewRaw(`SELECT selected FROM track_source WHERE id=?`, f.old.ID).Scan(ctx, &selected); err != nil || !selected {
		t.Fatalf("stale witness mutated selection=%v err=%v", selected, err)
	}
	var requests int
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_reconciliation_request WHERE position_id=?`, f.position).Scan(ctx, &requests); err != nil || requests != 0 {
		t.Fatalf("stale witness reconciliation requests=%d err=%v", requests, err)
	}
}

func TestFirstPublicationSourceSelectionTransactionRollbackRestoresSelectionAndReconciliationPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newFirstSourceSelectionFixture(t)
	ctx := context.Background()
	rollback := errors.New("injected failure after source-selection mutation")
	err := f.db.RunInTx(ctx, &sql.TxOptions{}, func(ctx context.Context, tx bun.Tx) error {
		if err := f.repo.applyFirstPublicationSourceSelection(ctx, tx, f.position, f.epoch, f.source, f.frozen, f.witness); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("transaction error=%v", err)
	}
	var selected bool
	if err := f.db.NewRaw(`SELECT selected FROM track_source WHERE id=?`, f.old.ID).Scan(ctx, &selected); err != nil || !selected {
		t.Fatalf("rollback left old selected=%v err=%v", selected, err)
	}
	if err := f.db.NewRaw(`SELECT selected FROM track_source WHERE id=?`, f.chosen.ID).Scan(ctx, &selected); err != nil || selected {
		t.Fatalf("rollback left new selected=%v err=%v", selected, err)
	}
	var requests int
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_reconciliation_request WHERE position_id=?`, f.position).Scan(ctx, &requests); err != nil || requests != 0 {
		t.Fatalf("rollback left reconciliation requests=%d err=%v", requests, err)
	}
}
