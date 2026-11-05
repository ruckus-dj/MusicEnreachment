//go:build integration

package persistence

import (
	"context"
	"encoding/json"
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
	db := testpostgres.OpenMigratedWithRiver(t)
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
