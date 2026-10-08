//go:build integration

package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestCanonicalFingerprintWinnerKeepsStableIDAndUnknownRerunCleansUp(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	digest := make([]byte, 32)
	digest[0] = 1
	insertFingerprintDigestVariant(t, ctx, db, digest)
	stamp := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	canonicalID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	winnerID := uuid.MustParse("00000000-0000-0000-0000-000000000003")
	loserID := uuid.MustParse("00000000-0000-0000-0000-000000000002")

	apply := func(id uuid.UUID, value string) *SourceFingerprintResult {
		t.Helper()
		candidate := SourceFingerprintResult{
			ID: id, FPCalcVersion: "fpcalc-1", VersionBanner: "fpcalc test",
			AlgorithmNamespace: "chromaprint", AlgorithmID: 1, Fingerprint: value,
			ReportedDuration: 1, CalculatedAt: stamp, AppliedOperationID: uuid.New(),
			ParserContractVersion: 1,
		}
		var selected *SourceFingerprintResult
		if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			var err error
			selected, err = upsertCanonicalFingerprintResult(ctx, tx, digest, candidate)
			return err
		}); err != nil {
			t.Fatalf("upsert canonical fingerprint %q: %v", value, err)
		}
		return selected
	}

	first := apply(canonicalID, "A")
	if first.ID != canonicalID {
		t.Fatalf("initial canonical ID = %s; want %s", first.ID, canonicalID)
	}
	selected := apply(winnerID, "C")
	if selected.ID != canonicalID || selected.WinningResultID != winnerID || selected.Fingerprint != "C" {
		t.Fatalf("equal-time winner = %+v; want stable ID %s and candidate %s", selected, canonicalID, winnerID)
	}
	selected = apply(loserID, "B")
	if selected.ID != canonicalID || selected.WinningResultID != winnerID || selected.Fingerprint != "C" {
		t.Fatalf("later lower-UUID candidate replaced winner: %+v", selected)
	}

	oldID, newID := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{oldID, newID} {
		if _, err := db.ExecContext(ctx, `INSERT INTO media_fingerprint_result
			(id,winning_result_id,fpcalc_version,version_banner,algorithm_namespace,algorithm_id,fingerprint,reported_duration,calculated_at,applied_operation_id,parser_contract_version)
			VALUES(?,?,'fpcalc-1','fpcalc test','chromaprint',1,'unknown',1,?,?,1)`, id, id, stamp, uuid.New()); err != nil {
			t.Fatalf("insert unknown-digest result: %v", err)
		}
	}
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return deleteUnreferencedSourceFingerprintResult(ctx, tx, oldID)
	}); err != nil {
		t.Fatalf("clean up replaced unknown-digest result: %v", err)
	}
	var physical int
	if err := db.NewRaw(`SELECT count(*) FROM media_fingerprint_result WHERE source_sha256 IS NULL`).Scan(ctx, &physical); err != nil || physical != 1 {
		t.Fatalf("physical unknown-digest results = %d, %v; want one", physical, err)
	}
}

func TestPromoteDigestlessFingerprintAfterSHA(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	stamp := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

	for _, existingCanonical := range []bool{false, true} {
		name := "without existing canonical"
		if existingCanonical {
			name = "newer candidate replaces canonical payload"
		}
		t.Run(name, func(t *testing.T) {
			digest := make([]byte, 32)
			digest[0] = byte(10 + boolToInt(existingCanonical))
			canonicalVariantID := insertFingerprintDigestVariant(t, ctx, db, digest)
			if existingCanonical {
				current := fingerprintCandidate(uuid.New(), stamp, "old payload")
				if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
					_, err := upsertCanonicalFingerprintResult(ctx, tx, digest, current)
					return err
				}); err != nil {
					t.Fatalf("insert initial canonical fingerprint: %v", err)
				}
			}

			candidateID := uuid.New()
			candidate := fingerprintCandidate(candidateID, stamp.Add(time.Hour), "new payload")
			if _, err := db.NewInsert().Model(&candidate).Exec(ctx); err != nil {
				t.Fatalf("insert digestless fingerprint candidate: %v", err)
			}
			workID := insertFingerprintPromotionWork(t, ctx, db, candidateID, "promote")
			otherWorkID := uuid.Nil
			if existingCanonical {
				otherWorkID = insertFingerprintPromotionWork(t, ctx, db, candidateID, "other")
			}

			locked := &lockedSourceStep{Work: SourceAnalysisWork{ID: workID}}
			canonical := &SourceMediaVariant{ID: canonicalVariantID, SourceSHA256: digest}
			if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				return promoteSourceResults(ctx, tx, locked, canonical)
			}); err != nil {
				t.Fatalf("promote digestless fingerprint after SHA: %v", err)
			}

			var selectedID, winnerID uuid.UUID
			var fingerprint string
			if err := db.NewRaw(`SELECT result.id,result.winning_result_id,result.fingerprint
				FROM media_fingerprint_result result WHERE result.source_sha256=?`, digest).Scan(ctx, &selectedID, &winnerID, &fingerprint); err != nil {
				t.Fatalf("read promoted canonical fingerprint: %v", err)
			}
			if winnerID != candidateID || fingerprint != "new payload" {
				t.Fatalf("promoted winner = %s / %q; want provenance %s / new payload", winnerID, fingerprint, candidateID)
			}
			if existingCanonical && selectedID == candidateID {
				t.Fatalf("promoted canonical identity = candidate ID %s; want existing stable ID", selectedID)
			}
			if !existingCanonical && selectedID != candidateID {
				t.Fatalf("new canonical identity = %s; want existing candidate ID %s", selectedID, candidateID)
			}
			var selectedWorkCount int
			if err := db.NewRaw(`SELECT count(*) FROM source_analysis_step WHERE step='fingerprint' AND success_fingerprint_result_id=?`, selectedID).Scan(ctx, &selectedWorkCount); err != nil {
				t.Fatalf("count canonical selections: %v", err)
			}
			wantSelections := 1
			if existingCanonical {
				wantSelections = 2
			}
			if selectedWorkCount != wantSelections {
				t.Fatalf("canonical selections = %d; want %d", selectedWorkCount, wantSelections)
			}
			if existingCanonical {
				var discardedExists bool
				if err := db.NewRaw(`SELECT EXISTS(SELECT 1 FROM media_fingerprint_result WHERE id=?)`, candidateID).Scan(ctx, &discardedExists); err != nil || discardedExists {
					t.Fatalf("replaced digestless candidate exists = %v, %v; want deleted after remapping all refs", discardedExists, err)
				}
				var otherSelection uuid.UUID
				if err := db.NewRaw(`SELECT success_fingerprint_result_id FROM source_analysis_step WHERE work_id=? AND step='fingerprint'`, otherWorkID).Scan(ctx, &otherSelection); err != nil || otherSelection != selectedID {
					t.Fatalf("other work selection = %s, %v; want canonical %s", otherSelection, err, selectedID)
				}
			}
		})
	}
}

func fingerprintCandidate(id uuid.UUID, calculated time.Time, fingerprint string) SourceFingerprintResult {
	return SourceFingerprintResult{
		ID: id, WinningResultID: id, FPCalcVersion: "fpcalc-1", VersionBanner: "fpcalc test",
		AlgorithmNamespace: "chromaprint", AlgorithmID: 1, Fingerprint: fingerprint,
		ReportedDuration: 1, CalculatedAt: calculated, AppliedOperationID: uuid.New(),
		ParserContractVersion: 1,
	}
}

func insertFingerprintDigestVariant(t *testing.T, ctx context.Context, db *bun.DB, digest []byte) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes,source_sha256,sha256_calculated_at,sha256_algorithm,sha256_applied_operation_id) VALUES(?,1,?,?,?,?)`, id, digest, time.Now().UTC(), "SHA-256", uuid.New()); err != nil {
		t.Fatalf("insert fingerprint digest variant: %v", err)
	}
	return id
}

func insertFingerprintPromotionWork(t *testing.T, ctx context.Context, db *bun.DB, resultID uuid.UUID, suffix string) uuid.UUID {
	t.Helper()
	rootID, locationID, workID := uuid.New(), uuid.New(), uuid.New()
	rootPath := "/fingerprint-promotion-" + rootID.String()
	if _, err := db.ExecContext(ctx, `INSERT INTO source_root(id,configured_path,display_name,scan_generation,inventory_path) VALUES(?,?,?,1,'/inventory')`, rootID, rootPath, "test"); err != nil {
		t.Fatalf("insert fingerprint promotion root: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO source_location(id,source_root_id,relative_path,size_bytes,mtime,last_seen_scan_generation,probe_status) VALUES(?,?,?,1,?,1,'audio')`, locationID, rootID, suffix+".flac", time.Now().UTC()); err != nil {
		t.Fatalf("insert fingerprint promotion location: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO source_analysis_work(id,location_id,source_root_id,configured_path,inventory_path,relative_path,size_bytes,mtime,sha256_enabled,origin_scan_operation_id) VALUES(?,?,?,?,?,?,1,?,true,?)`, workID, locationID, rootID, rootPath, "/inventory", suffix+".flac", time.Now().UTC(), uuid.New()); err != nil {
		t.Fatalf("insert fingerprint promotion work: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO source_analysis_step(work_id,step,state,success_fingerprint_result_id,success_reuse_origin) VALUES(?,'fingerprint','succeeded',?,'executed')`, workID, resultID); err != nil {
		t.Fatalf("insert fingerprint promotion selection: %v", err)
	}
	return workID
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
