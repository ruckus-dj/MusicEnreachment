//go:build integration

package persistence_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestIncomingMatchingReconcileSnapshotUsesPersistedMetadataAndAuthority(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	newIncomingMatchFixture(t, ctx, db)
	repository := persistence.NewIncomingGroupingRepository(db)
	groups, err := repository.List(ctx)
	if err != nil || len(groups) != 1 {
		t.Fatalf("fixture groups = %+v, %v", groups, err)
	}
	// A manual grouping correction does not itself revoke automatic matching.
	if _, err := db.NewRaw(`UPDATE incoming_group SET manual=true WHERE id=?`, groups[0].ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	manualAuthorityGroup := uuid.New()
	if _, err := db.NewRaw(`INSERT INTO incoming_group(id,manual,revision,diagnostics) VALUES(?,false,'manual-authority','[]'::jsonb)`, manualAuthorityGroup).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewRaw(`INSERT INTO incoming_group_matching_authority(group_id,mode,revision) VALUES(?,'manual',7)`, manualAuthorityGroup).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot, err := repository.MatchingReconcileSnapshot(ctx)
	if err != nil {
		t.Fatalf("read matching reconcile projection: %v", err)
	}
	if len(snapshot.Groups) != 2 {
		t.Fatalf("projected groups = %d, want 2", len(snapshot.Groups))
	}
	var foundCorrected, foundManualAuthority bool
	for _, projected := range snapshot.Groups {
		switch projected.Group.ID {
		case groups[0].ID:
			foundCorrected = true
			if !projected.Group.Manual || projected.Authority.Mode != "automatic" || projected.Authority.Revision != 0 {
				t.Fatalf("corrected group projection = %+v", projected)
			}
			if len(projected.MemberCaptures[groups[0].Members[0]]) == 0 {
				t.Fatalf("saved member capture missing: %+v", projected)
			}
			for _, captures := range projected.MemberCaptures {
				for _, capture := range captures {
					if len(capture.Tags) == 0 || len(capture.SourceSHA256) != 0 {
						t.Fatalf("metadata-only persisted capture = tags %s SHA %x", capture.Tags, capture.SourceSHA256)
					}
				}
			}
		case manualAuthorityGroup:
			foundManualAuthority = true
			if projected.Authority.Mode != "manual" || projected.Authority.Revision != 7 {
				t.Fatalf("manual authority projection = %+v", projected.Authority)
			}
		}
	}
	if !foundCorrected || !foundManualAuthority {
		t.Fatalf("projection missing groups: corrected=%t manual-authority=%t", foundCorrected, foundManualAuthority)
	}
}
