//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

func TestReleasePublicationInputHoldsPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo := readyPublicationInputHoldsFixture(t)
	if released, err := repo.ReleasePublicationInputHolds(ctx, f.guard); err != nil || released {
		t.Fatalf("release while executing = %t, %v", released, err)
	}
	if quiescent, err := repo.MarkPublicationQuiescent(ctx, f.guard); err != nil || !quiescent {
		t.Fatalf("quiesce = %t, %v", quiescent, err)
	}
	if released, err := repo.ReleasePublicationInputHolds(ctx, f.guard); err != nil || !released {
		t.Fatalf("release after quiescence = %t, %v", released, err)
	}
	var sourceHolds, toolHolds int
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_source_hold WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &sourceHolds); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM operation_tool_read_hold WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &toolHolds); err != nil || sourceHolds != 0 || toolHolds != 0 {
		t.Fatalf("holds after release source=%d tools=%d err=%v", sourceHolds, toolHolds, err)
	}
	var required bool
	if err := f.db.NewRaw(`SELECT tools_read_required FROM operation WHERE id=?`, f.guard.OperationID).Scan(ctx, &required); err != nil || required {
		t.Fatalf("tools_read_required = %t, %v", required, err)
	}
	var namespaceClaims, claims int
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_namespace_claim WHERE epoch=?`, f.guard.Epoch).Scan(ctx, &namespaceClaims); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_execution_claim WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &claims); err != nil || namespaceClaims == 0 || claims != 1 {
		t.Fatalf("namespace/execution claims = %d/%d, %v", namespaceClaims, claims, err)
	}
	var journalBefore, journalAfter string
	if err := f.db.NewRaw(`SELECT mutation_journal::text FROM track_publication_attempt WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &journalBefore); err != nil {
		t.Fatal(err)
	}
	if released, err := repo.ReleasePublicationInputHolds(ctx, f.guard); err != nil || !released {
		t.Fatalf("idempotent release = %t, %v", released, err)
	}
	if err := f.db.NewRaw(`SELECT mutation_journal::text FROM track_publication_attempt WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &journalAfter); err != nil || journalAfter != journalBefore {
		t.Fatalf("replay changed journal: %v", err)
	}
}

func readyPublicationInputHoldsFixture(t *testing.T) (publicationExecutionFixture, *SetupManagerRepository) {
	t.Helper()
	ctx := context.Background()
	f, _, _, repo := readyFirstPublicationCommitFixture(t)
	_, _, _, _ = pubSourceProtectionHoldWithFixture(t, f)
	attempt, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	input, err := DecodePublicationInputs(attempt.FrozenInputs)
	if err != nil {
		t.Fatal(err)
	}
	id := input.Tools[0].InstallationID
	if err := repo.CreateInstallation(ctx, &ToolInstallation{
		ID: id, PackageKind: "ffmpeg", PlatformGOOS: "linux", PlatformGOARCH: "amd64",
		SourceName: "test", ReleaseIdentity: "test-release", RelativePath: "ffmpeg/test-release", State: "ready",
		ExecutableVersions: json.RawMessage(`{"ffmpeg":"1","ffprobe":"1"}`), ArtifactIdentities: json.RawMessage(`[]`), VerifiedAt: ptrTime(time.Now().UTC()),
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw(`INSERT INTO operation_tool_read_hold(operation_id,installation_id) VALUES (?,?)`, f.guard.OperationID, id).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewRaw(`UPDATE operation SET tools_read_required=true WHERE id=?`, f.guard.OperationID).Exec(ctx)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if committed, err := repo.CommitFirstPublication(ctx, f.guard); err != nil || !committed {
		t.Fatalf("commit = %t, %v", committed, err)
	}
	return f, repo
}

func TestReleasePublicationInputHoldsRefusesStaleDeliveryPostgreSQL(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(context.Context, *testing.T, publicationExecutionFixture)
	}{
		{name: "stale boot", mutate: func(ctx context.Context, t *testing.T, f publicationExecutionFixture) {
			_, err := f.db.NewRaw(`UPDATE publication_execution_claim SET boot_uuid=? WHERE operation_id=?`, uuid.New(), f.guard.OperationID).Exec(ctx)
			if err != nil {
				t.Fatal(err)
			}
		}},
		{name: "changed current metadata", mutate: func(ctx context.Context, t *testing.T, f publicationExecutionFixture) {
			_, err := f.db.NewRaw(`UPDATE track_publication SET actual_metadata='{"title":"changed"}'::jsonb WHERE position_id=?`, f.guard.ExpectedPositionID).Exec(ctx)
			if err != nil {
				t.Fatal(err)
			}
		}},
		{name: "completed river job", mutate: func(ctx context.Context, t *testing.T, f publicationExecutionFixture) {
			_, err := f.db.NewRaw(`UPDATE river_job SET state='completed', finalized_at=now() WHERE id=?`, f.guard.RiverJobID).Exec(ctx)
			if err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f, repo := readyPublicationInputHoldsFixture(t)
			if quiescent, err := repo.MarkPublicationQuiescent(ctx, f.guard); err != nil || !quiescent {
				t.Fatalf("quiesce = %t, %v", quiescent, err)
			}
			tt.mutate(ctx, t, f)
			if released, err := repo.ReleasePublicationInputHolds(ctx, f.guard); err != nil || released {
				t.Fatalf("release = %t, %v", released, err)
			}
			assertPublicationInputHoldsRetained(t, ctx, f)
		})
	}
}

func TestReleasePublicationInputHoldsRollsBackOnFlagFailurePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo := readyPublicationInputHoldsFixture(t)
	if quiescent, err := repo.MarkPublicationQuiescent(ctx, f.guard); err != nil || !quiescent {
		t.Fatalf("quiesce = %t, %v", quiescent, err)
	}
	if _, err := f.db.ExecContext(ctx, `CREATE FUNCTION reject_tools_read_flag_clear() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.tools_read_required AND NOT NEW.tools_read_required THEN RAISE EXCEPTION 'injected flag-clear failure'; END IF; RETURN NEW; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `CREATE TRIGGER reject_tools_read_flag_clear BEFORE UPDATE ON operation FOR EACH ROW EXECUTE FUNCTION reject_tools_read_flag_clear()`); err != nil {
		t.Fatal(err)
	}
	var journalBefore, journalAfter string
	if err := f.db.NewRaw(`SELECT mutation_journal::text FROM track_publication_attempt WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &journalBefore); err != nil {
		t.Fatal(err)
	}
	released, err := repo.ReleasePublicationInputHolds(ctx, f.guard)
	if released || err == nil || !strings.Contains(err.Error(), "injected flag-clear failure") {
		t.Fatalf("release = %t, %v", released, err)
	}
	assertPublicationInputHoldsRetained(t, ctx, f)
	if err := f.db.NewRaw(`SELECT mutation_journal::text FROM track_publication_attempt WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &journalAfter); err != nil || journalAfter != journalBefore {
		t.Fatalf("rollback changed mutation journal: before=%s after=%s err=%v", journalBefore, journalAfter, err)
	}
}

func assertPublicationInputHoldsRetained(t *testing.T, ctx context.Context, f publicationExecutionFixture) {
	t.Helper()
	var sourceHolds, toolHolds int
	if err := f.db.NewRaw(`SELECT count(*) FROM publication_source_hold WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &sourceHolds); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT count(*) FROM operation_tool_read_hold WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &toolHolds); err != nil {
		t.Fatal(err)
	}
	var required bool
	if err := f.db.NewRaw(`SELECT tools_read_required FROM operation WHERE id=?`, f.guard.OperationID).Scan(ctx, &required); err != nil {
		t.Fatal(err)
	}
	var opState, opStage, claimState string
	if err := f.db.NewRaw(`SELECT state,stage FROM operation WHERE id=?`, f.guard.OperationID).Scan(ctx, &opState, &opStage); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT state FROM publication_execution_claim WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &claimState); err != nil {
		t.Fatal(err)
	}
	if sourceHolds != 1 || toolHolds != 1 || !required || opState != "running" || opStage != "committed" || claimState != "quiescent" {
		t.Fatalf("refusal changed durable holds/state: source=%d tools=%d required=%t operation=%s/%s claim=%s", sourceHolds, toolHolds, required, opState, opStage, claimState)
	}
}
