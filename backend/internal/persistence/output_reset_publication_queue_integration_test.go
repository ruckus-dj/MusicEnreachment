//go:build integration

package persistence

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestOutputResetCancelsAttemptedPublicationDeliveryOnlyPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationResetFixture(t)
	ctx := context.Background()
	if _, err := f.db.NewRaw(`UPDATE operation SET state='queued',stage='queued',finished_at=NULL WHERE id=?`, f.operation).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`UPDATE river_job SET state='available',attempt=1,attempted_at=now() WHERE id=?`, f.job).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`INSERT INTO track_publication_attempt
		(operation_id,position_id,epoch,candidate_generation,intent_kind,phase,frozen_inputs,namespace_manifest)
		VALUES (?,?,?,?,'audio','queued','{}','{}')`, f.operation, f.position, f.epoch, uuid.New()).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var unrelatedJob int64
	if err := f.db.NewRaw(`INSERT INTO river_job(kind,args,state,attempt,attempted_at)
		VALUES ('unrelated_reset_probe','{}','available',1,now()) RETURNING id`).Scan(ctx, &unrelatedJob); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot,river_job_id)
		VALUES (?,'matching_evaluation_v1','queued','queued','{}',?)`, uuid.New(), unrelatedJob).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	token, err := f.reset()
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range []struct {
		id    int64
		state string
	}{{f.job, "cancelled"}, {unrelatedJob, "available"}} {
		var state string
		if err := f.db.NewRaw(`SELECT state FROM river_job WHERE id=?`, job.id).Scan(ctx, &state); err != nil || state != job.state {
			t.Fatalf("job %d state = %q, %v; want %q", job.id, state, err, job.state)
		}
	}
	var attempts int
	if err := f.db.NewRaw(`SELECT count(*) FROM track_publication_attempt WHERE operation_id=?`, f.operation).Scan(ctx, &attempts); err != nil || attempts != 0 {
		t.Fatalf("queued attempts after reset = %d, %v", attempts, err)
	}
	var state, safeError string
	if err := f.db.NewRaw(`SELECT state,safe_error FROM operation WHERE id=?`, f.operation).Scan(ctx, &state, &safeError); err != nil || state != "failed" || safeError == "" {
		t.Fatalf("cancelled operation = %q %q, %v", state, safeError, err)
	}
	var epoch uuid.UUID
	if err := f.db.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton`).Scan(ctx, &epoch); err != nil || epoch != token {
		t.Fatalf("reset epoch = %s, %v; want %s", epoch, err, token)
	}
	var positions int
	if err := f.db.NewRaw(`SELECT count(*) FROM release_track WHERE id=?`, f.position).Scan(ctx, &positions); err != nil || positions != 1 {
		t.Fatalf("local position retained = %d, %v", positions, err)
	}
}

func TestOutputResetRejectsUnsafePublicationAttemptsPostgreSQL(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		phase    string
		terminal bool
	}{
		{"execution started", "preparing", false},
		{"candidate staged", "staged", false},
		{"candidate validated", "validated", false},
		{"candidate exposing", "exposing", false},
		{"terminal operation retains intent", "queued", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newPublicationResetFixture(t)
			ctx := context.Background()
			if !test.terminal {
				if _, err := f.db.NewRaw(`UPDATE operation SET state='queued',stage='queued',finished_at=NULL WHERE id=?`, f.operation).Exec(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.db.NewRaw(`INSERT INTO track_publication_attempt
				(operation_id,position_id,epoch,candidate_generation,intent_kind,phase,frozen_inputs,namespace_manifest)
				VALUES (?,?,?,?,'audio',?,'{}','{}')`, f.operation, f.position, f.epoch, uuid.New(), test.phase).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := f.reset(); err == nil {
				t.Fatal("reset ignored unresolved publication intent")
			}
			f.assertUnchanged(t, true)
			var attempts int
			if err := f.db.NewRaw(`SELECT count(*) FROM track_publication_attempt WHERE operation_id=?`, f.operation).Scan(ctx, &attempts); err != nil || attempts != 1 {
				t.Fatalf("blocked reset retained attempts = %d, %v", attempts, err)
			}
		})
	}
}
