//go:build integration

package service

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/uptrace/bun"
)

type firstSourceSelectionFailingInserter struct {
	base     persistence.RiverInserter
	err      error
	inserted bool
}

func (inserter *firstSourceSelectionFailingInserter) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	result, err := inserter.base.InsertTx(ctx, tx, args, opts)
	if err != nil {
		return nil, err
	}
	inserter.inserted = result != nil && result.Job != nil && result.Job.ID > 0
	return nil, inserter.err
}

func TestFirstPublicationExplicitUnselectedSourceAdmissionPostgreSQL(t *testing.T) {
	for _, failEnqueue := range []bool{false, true} {
		name := "commit"
		if failEnqueue {
			name = "enqueue rollback"
		}
		t.Run(name, func(t *testing.T) {
			ran := false
			testPublicationSourceExecution(t, "Track.flac", true, false, func(db *bun.DB, releaseID, positionID, trackID, epoch uuid.UUID, publications *LibraryPublications) {
				ran = true
				ctx := context.Background()
				if _, err := db.ExecContext(ctx, `UPDATE track_source SET selected=false,updated_at=clock_timestamp() WHERE track_id=?`, trackID); err != nil {
					t.Fatal(err)
				}
				var selected bool
				var beforeUpdated time.Time
				if err := db.NewRaw(`SELECT selected,updated_at FROM track_source WHERE track_id=?`, trackID).Scan(ctx, &selected, &beforeUpdated); err != nil {
					t.Fatal(err)
				}
				if selected {
					t.Fatal("fixture source remains selected")
				}
				preview, err := publications.PreviewFirst(ctx, releaseID, positionID)
				if err != nil || preview.SelectedSource != nil || len(preview.SourceCandidates) != 1 || len(preview.Blockers) == 0 {
					t.Fatalf("unselected default preview=%+v,%v", preview, err)
				}
				candidate := preview.SourceCandidates[0]
				choice := &FirstPublicationSourceChoice{LocationID: candidate.LocationID, MediaVariantID: candidate.MediaVariantID, StreamIndex: candidate.StreamIndex}
				explicit, err := publications.PreviewFirstSelected(ctx, releaseID, positionID, choice)
				if err != nil || explicit.SelectedSource == nil || len(explicit.Blockers) != 0 {
					t.Fatalf("explicit preview=%+v,%v", explicit, err)
				}
				if err := db.NewRaw(`SELECT selected FROM track_source WHERE track_id=?`, trackID).Scan(ctx, &selected); err != nil {
					t.Fatal(err)
				}
				if selected {
					t.Fatal("preview mutated source selection")
				}
				beforeJobs, beforeOps, beforeClaims := integrationPublicationArtifacts(t, db)
				var beforeReconciliation int
				if err := db.NewRaw(`SELECT count(*) FROM publication_reconciliation_request`).Scan(ctx, &beforeReconciliation); err != nil {
					t.Fatal(err)
				}
				sentinel := errors.New("real source-selection enqueue failure")
				failure := &firstSourceSelectionFailingInserter{base: publications.river, err: sentinel}
				if failEnqueue {
					publications.river = failure
				}
				id, err := publications.AdmitFirst(ctx, releaseID, positionID, FirstPublicationConfirmation{PreviewDigest: explicit.ContextDigest, ExpectedEpoch: epoch, SourceChoice: choice})
				var afterUpdated time.Time
				if scanErr := db.NewRaw(`SELECT selected,updated_at FROM track_source WHERE track_id=?`, trackID).Scan(ctx, &selected, &afterUpdated); scanErr != nil {
					t.Fatal(scanErr)
				}
				if failEnqueue {
					if !errors.Is(err, sentinel) || id != uuid.Nil || !failure.inserted {
						t.Fatalf("failed admission id=%s error=%v inserted=%t", id, err, failure.inserted)
					}
					if selected || !beforeUpdated.Equal(afterUpdated) {
						t.Fatal("failed enqueue changed source selection or witness timestamp")
					}
					afterJobs, afterOps, afterClaims := integrationPublicationArtifacts(t, db)
					if beforeJobs != afterJobs || beforeOps != afterOps || beforeClaims != afterClaims {
						t.Fatal("failed admission retained job, operation or namespace reservation")
					}
					var attempts, holds, reconciliations int
					if scanErr := db.NewRaw(`SELECT count(*) FROM track_publication_attempt WHERE position_id=?`, positionID).Scan(ctx, &attempts); scanErr != nil {
						t.Fatal(scanErr)
					}
					if scanErr := db.NewRaw(`SELECT count(*) FROM publication_source_hold WHERE media_variant_id=?`, candidate.MediaVariantID).Scan(ctx, &holds); scanErr != nil {
						t.Fatal(scanErr)
					}
					if scanErr := db.NewRaw(`SELECT count(*) FROM publication_reconciliation_request`).Scan(ctx, &reconciliations); scanErr != nil {
						t.Fatal(scanErr)
					}
					if attempts != 0 || holds != 0 || reconciliations != beforeReconciliation {
						t.Fatalf("rollback retained attempts=%d holds=%d reconciliation=%d (before %d)", attempts, holds, reconciliations, beforeReconciliation)
					}
					return
				}
				if err != nil || id == uuid.Nil || !selected {
					t.Fatalf("explicit admission id=%s selected=%t error=%v", id, selected, err)
				}
				attempt, err := persistence.NewSetupManagerRepository(db).ReadPublicationAttempt(ctx, id)
				if err != nil || attempt == nil {
					t.Fatalf("read admitted attempt=%+v,%v", attempt, err)
				}
				inputs, err := persistence.DecodePublicationInputs(attempt.FrozenInputs)
				if err != nil || inputs.Audio == nil || inputs.Audio.LocationID != choice.LocationID || inputs.Audio.MediaVariantID != choice.MediaVariantID || inputs.SourceAuthorityMode != "manual" {
					t.Fatalf("frozen source=%+v,%v", inputs.Audio, err)
				}
				var jobs, holds int
				if err := db.NewRaw(`SELECT count(*) FROM river_job WHERE kind='publish_track_v1' AND args->>'operation_id'=?`, id.String()).Scan(ctx, &jobs); err != nil {
					t.Fatal(err)
				}
				if err := db.NewRaw(`SELECT count(*) FROM publication_source_hold WHERE operation_id=?`, id).Scan(ctx, &holds); err != nil {
					t.Fatal(err)
				}
				if jobs != 1 || holds != 1 {
					t.Fatalf("admitted jobs=%d holds=%d", jobs, holds)
				}
			})
			if !ran {
				t.Fatal("source selection fixture callback did not run")
			}
		})
	}
}
