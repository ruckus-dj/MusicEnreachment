//go:build integration

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestPublicationAttemptInputsFollowUnfinishedLifecyclePostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	operationID, _ := insertMatchingCurrentSchemaScope(t, ctx, database)
	releaseID, trackID, positionID := uuid.New(), uuid.New(), uuid.New()
	for _, seed := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO local_release(id,title) VALUES (?,'release')`, []any{releaseID}},
		{`INSERT INTO local_track(id,title) VALUES (?,'track')`, []any{trackID}},
		{`INSERT INTO release_track(id,release_id,track_id,medium,position) VALUES (?,?,?,0,0)`, []any{positionID, releaseID, trackID}},
	} {
		if _, err := database.NewRaw(seed.query, seed.args...).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for _, phase := range []string{"queued", "preparing", "staged", "validated", "exposing"} {
		for _, inputs := range []any{nil, `{}`, `[]`, `null`} {
			t.Run(phase+"/"+publicationInputsTestName(inputs), func(t *testing.T) {
				wantValid := inputs == `{}`
				_, err := database.NewRaw(`INSERT INTO track_publication_attempt
					(operation_id,position_id,epoch,candidate_generation,intent_kind,phase,frozen_inputs,namespace_manifest)
					VALUES (?,?,?,?,'audio',?,?::jsonb,'{}'::jsonb)`,
					operationID, positionID, uuid.New(), uuid.New(), phase, inputs).Exec(ctx)
				if !wantValid {
					if err == nil || !strings.Contains(err.Error(), "track_publication_attempt_frozen_inputs_phase_check") {
						t.Fatalf("phase=%s inputs=%v: expected phase constraint, got %v", phase, inputs, err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if _, err := database.NewRaw(`DELETE FROM track_publication_attempt WHERE operation_id=?`, operationID).Exec(ctx); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
	for _, phase := range []string{"committed", "cleaning", "cleanup_pending", "recovery_required"} {
		t.Run("retired/"+phase, func(t *testing.T) {
			_, err := database.NewRaw(`INSERT INTO track_publication_attempt
				(operation_id,position_id,epoch,candidate_generation,intent_kind,phase,frozen_inputs,namespace_manifest)
				VALUES (?,?,?,?,'audio',?,'{}'::jsonb,'{}'::jsonb)`,
				operationID, positionID, uuid.New(), uuid.New(), phase).Exec(ctx)
			if err == nil || !strings.Contains(err.Error(), "track_publication_attempt_phase_check") {
				t.Fatalf("retired phase %s: expected phase constraint, got %v", phase, err)
			}
		})
	}
}

func publicationInputsTestName(inputs any) string {
	if inputs == nil {
		return "sql-null"
	}
	return inputs.(string)
}
