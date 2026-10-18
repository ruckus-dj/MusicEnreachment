//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestLoadPublicationStartupSnapshotRejectsInvalidQueuedContractsPostgreSQL(t *testing.T) {
	tests := []struct {
		name   string
		change func(*testing.T, publicationExecutionFixture)
	}{
		{
			name: "wrong job kind",
			change: func(t *testing.T, f publicationExecutionFixture) {
				_, err := f.db.NewRaw(`UPDATE river_job SET kind='not_publish_track' WHERE id=?`, f.guard.RiverJobID).Exec(context.Background())
				if err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "wrong operation identity",
			change: func(t *testing.T, f publicationExecutionFixture) {
				args, err := json.Marshal(publicationTrackArgs{OperationID: uuid.New()})
				if err != nil {
					t.Fatal(err)
				}
				_, err = f.db.NewRaw(`UPDATE river_job SET args=?::jsonb WHERE id=?`, string(args), f.guard.RiverJobID).Exec(context.Background())
				if err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "missing namespace reservation",
			change: func(t *testing.T, f publicationExecutionFixture) {
				_, err := f.db.NewRaw(`DELETE FROM publication_namespace_reservation WHERE position_id=? AND generation=?`, f.guard.ExpectedPositionID, f.guard.CandidateGeneration).Exec(context.Background())
				if err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "conflicting namespace owner",
			change: func(t *testing.T, f publicationExecutionFixture) {
				ctx := context.Background()
				releaseID, trackID, otherPositionID := uuid.New(), uuid.New(), uuid.New()
				if _, err := f.db.NewRaw(`INSERT INTO local_release(id,title) VALUES (?,'other position')`, releaseID).Exec(ctx); err != nil {
					t.Fatal(err)
				}
				if _, err := f.db.NewRaw(`INSERT INTO local_track(id,title) VALUES (?,'other position')`, trackID).Exec(ctx); err != nil {
					t.Fatal(err)
				}
				if _, err := f.db.NewRaw(`INSERT INTO release_track(id,release_id,track_id,position) VALUES (?,?,?,0)`, otherPositionID, releaseID, trackID).Exec(ctx); err != nil {
					t.Fatal(err)
				}
				_, err := f.db.NewRaw(`UPDATE publication_namespace_claim SET owner_position_id=? WHERE canonical_key='output.mka' AND epoch=?`, otherPositionID, f.guard.Epoch).Exec(context.Background())
				if err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newPublicationExecutionFixture(t)
			prepareQueuedStartupFixture(t, f)
			tt.change(t, f)

			snapshot, err := NewSetupManagerRepository(f.db).LoadPublicationStartupSnapshot(context.Background())
			if err != nil {
				t.Fatalf("load startup snapshot: %v", err)
			}
			if len(snapshot.Records) != 1 || snapshot.Records[0].QueuedContractValid {
				t.Fatalf("queued contract record = %+v, want invalid contract", snapshot.Records)
			}
		})
	}
}

func TestLoadPublicationStartupSnapshotAllowsEquivalentNamespacePathSpellingPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	prepareQueuedStartupFixture(t, f)
	ctx := context.Background()

	// Admission reuses a file claim by canonical key and owner position; the
	// manifest path spelling need not match the original claim's spelling.
	_, err := f.db.NewRaw(`UPDATE track_publication_attempt SET namespace_manifest=jsonb_set(namespace_manifest, '{nodes,0,relative_path}', '"OUTPUT.mka"') WHERE operation_id=?`, f.guard.OperationID).Exec(ctx)
	if err != nil {
		t.Fatalf("change queued manifest path spelling: %v", err)
	}

	snapshot, err := NewSetupManagerRepository(f.db).LoadPublicationStartupSnapshot(ctx)
	if err != nil {
		t.Fatalf("load startup snapshot: %v", err)
	}
	if len(snapshot.Records) != 1 || !snapshot.Records[0].QueuedContractValid {
		t.Fatalf("queued contract record = %+v, want equivalent canonical path accepted", snapshot.Records)
	}
}
