//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

// A late-arriving capture with an older observation time must not replace the
// newest observation. Canonical selection uses maximum observation time, not
// first arrival.
func TestSourceMetadataLateOlderCannotReplaceNewerWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(database)
	client := openScanEnqueueRiver(t, database)
	digest := make([]byte, 32)
	digest[0] = 0x5d
	observedAt := time.Now().UTC().Truncate(time.Microsecond)

	applyCapture := func(path, tags string, at time.Time) *persistence.SourceMetadataResult {
		t.Helper()
		root := createInventoryRoot(t, ctx, repository, path)
		location := insertAnalysisLocation(t, ctx, database, root.ID, "track.flac", 4096, probeMtime())
		establishInventory(t, ctx, database, root)
		work := normalizedWork(t, ctx, repository, root, location, true,
			persistence.SourceAnalysisStepInput{Step: persistence.SourceStepSHA256, State: "pending"},
			persistence.SourceAnalysisStepInput{Step: persistence.SourceStepMetadata, State: "pending"},
		)
		fixture := newAnalysisStepFixture(t, ctx, database, repository, client, root, location, work, persistence.SourceAnalysisModeBatch, nil, nil, false, nil)
		shaAttempt := fixture.claim(persistence.SourceStepSHA256)
		if _, err := repository.ApplySourceSHA256(ctx, persistence.SourceSHA256Apply{
			WorkID: work.ID, OperationID: fixture.operation.ID, OperationAttempt: fixture.operation.Attempt,
			JobID: *fixture.operation.RiverJobID, StepAttempt: shaAttempt, SHA256: digest,
			CalculatedAt: at, Algorithm: "SHA-256",
		}); err != nil {
			t.Fatalf("apply SHA-256: %v", err)
		}
		attempt := fixture.claim(persistence.SourceStepMetadata)
		result, err := repository.ApplySourceMetadata(ctx, persistence.SourceMetadataApply{
			WorkID: work.ID, OperationID: fixture.operation.ID, OperationAttempt: fixture.operation.Attempt,
			JobID: *fixture.operation.RiverJobID, StepAttempt: attempt,
			ObservedTags: json.RawMessage(`{"ARTIST":["` + tags + `"]}`),
			Provenance:   json.RawMessage(`{"reader":"regression"}`), ObservedAt: at,
		})
		if err != nil {
			t.Fatalf("apply metadata capture %q: %v", tags, err)
		}
		return result
	}

	newestAt := observedAt.Add(time.Second)
	newer := applyCapture("/srv/metadata-newer", "newer", newestAt)
	lateOlder := applyCapture("/srv/metadata-older", "older", observedAt)
	if lateOlder.ID != newer.ID {
		t.Fatalf("late older capture returned ID %s; canonical ID should remain stable at %s", lateOlder.ID, newer.ID)
	}
	var stored persistence.SourceMetadataResult
	if err := database.NewRaw(`SELECT * FROM media_metadata_result WHERE source_sha256=?`, digest).Scan(ctx, &stored); err != nil {
		t.Fatalf("read canonical metadata: %v", err)
	}
	if !sameJSON(stored.ObservedTags, json.RawMessage(`{"ARTIST":["newer"]}`)) || !stored.ObservedAt.Equal(newestAt) {
		t.Fatalf("canonical capture = tags %s at %s; want newest observation %s at %s", stored.ObservedTags, stored.ObservedAt, `{"ARTIST":["newer"]}`, newestAt)
	}
	var referenceCount int
	if err := database.NewRaw(`SELECT count(*) FROM source_analysis_step WHERE success_metadata_result_id=?`, stored.ID).Scan(ctx, &referenceCount); err != nil {
		t.Fatalf("count canonical step references: %v", err)
	}
	if referenceCount != 2 {
		t.Fatalf("canonical metadata references = %d; want both successful captures to point at the canonical row", referenceCount)
	}
}

// Equal observation instants are resolved by the greatest capture UUID. Check
// the result identity explicitly: timestamps alone cannot distinguish captures
// after database timestamp precision normalization.
func TestSourceMetadataEqualObservationTimeUsesGreatestUUIDWithPostgreSQL(t *testing.T) {
	t.Parallel()
	database := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	repository := persistence.NewSourceInventoryRepository(database)
	client := openScanEnqueueRiver(t, database)
	digest := make([]byte, 32)
	digest[0] = 0x6e
	observedAt := time.Now().UTC().Truncate(time.Microsecond)
	var winnerID uuid.UUID
	var canonicalID uuid.UUID
	type capture struct {
		fixture *analysisStepFixture
		work    *persistence.SourceAnalysisWork
		result  *persistence.SourceMetadataResult
		index   int
	}
	captures := make([]capture, 0, 3)
	for index := 0; index < 3; index++ {
		root := createInventoryRoot(t, ctx, repository, fmt.Sprintf("/srv/metadata-tie-%d", index))
		location := insertAnalysisLocation(t, ctx, database, root.ID, "track.flac", 4096, probeMtime())
		establishInventory(t, ctx, database, root)
		work := normalizedWork(t, ctx, repository, root, location, true,
			persistence.SourceAnalysisStepInput{Step: persistence.SourceStepSHA256, State: "pending"},
			persistence.SourceAnalysisStepInput{Step: persistence.SourceStepMetadata, State: "pending"},
		)
		fixture := newAnalysisStepFixture(t, ctx, database, repository, client, root, location, work, persistence.SourceAnalysisModeBatch, nil, nil, false, nil)
		attempt := fixture.claim(persistence.SourceStepMetadata)
		result, err := repository.ApplySourceMetadata(ctx, persistence.SourceMetadataApply{
			WorkID: work.ID, OperationID: fixture.operation.ID, OperationAttempt: fixture.operation.Attempt,
			JobID: *fixture.operation.RiverJobID, StepAttempt: attempt,
			ObservedTags: json.RawMessage(fmt.Sprintf(`{"ARTIST":["capture-%d"]}`, index)),
			Provenance:   json.RawMessage(`{"reader":"tie-regression"}`), ObservedAt: observedAt,
		})
		if err != nil {
			t.Fatalf("apply equal-time metadata capture %d: %v", index, err)
		}
		captures = append(captures, capture{fixture: fixture, work: work, result: result, index: index})
	}
	// Promote from the smallest UUID to the largest. The first promoted row
	// establishes canonical identity, while the last must win the UUID tie-break.
	sort.Slice(captures, func(i, j int) bool { return captures[i].result.ID.String() < captures[j].result.ID.String() })
	canonicalID = captures[0].result.ID
	winnerPayload := json.RawMessage(nil)
	for _, item := range captures {
		if item.result.WinningResultID.String() > winnerID.String() {
			winnerID = item.result.WinningResultID
			winnerPayload = json.RawMessage(fmt.Sprintf(`{"ARTIST":["capture-%d"]}`, item.index))
		}
	}
	for _, item := range captures {
		shaAttempt := item.fixture.claim(persistence.SourceStepSHA256)
		_, err := repository.ApplySourceSHA256(ctx, persistence.SourceSHA256Apply{
			WorkID: item.work.ID, OperationID: item.fixture.operation.ID, OperationAttempt: item.fixture.operation.Attempt,
			JobID: *item.fixture.operation.RiverJobID, StepAttempt: shaAttempt, SHA256: digest,
			CalculatedAt: observedAt, Algorithm: "SHA-256",
		})
		if err != nil {
			t.Fatalf("promote equal-time metadata capture %d: %v", item.index, err)
		}
		var selectedID uuid.UUID
		if err := database.NewRaw(`SELECT success_metadata_result_id FROM source_analysis_step WHERE work_id=? AND step='metadata'`, item.work.ID).Scan(ctx, &selectedID); err != nil {
			t.Fatalf("read selected metadata after promoting capture %d: %v", item.index, err)
		}
		if selectedID != canonicalID {
			t.Fatalf("canonical ID changed while promoting capture %d: got %s want first-promoted ID %s", item.index, selectedID, canonicalID)
		}
	}
	var stored persistence.SourceMetadataResult
	if err := database.NewRaw(`SELECT * FROM media_metadata_result WHERE source_sha256=?`, digest).Scan(ctx, &stored); err != nil {
		t.Fatalf("read tied canonical metadata: %v", err)
	}
	if stored.ID != canonicalID || stored.WinningResultID != winnerID || !sameJSON(stored.ObservedTags, winnerPayload) || !stored.ObservedAt.Equal(observedAt) {
		t.Fatalf("tied canonical metadata = %+v; want first-promoted ID %s and maximum UUID winner %s with shared observation time", stored, canonicalID, winnerID)
	}
}

func sameJSON(left, right json.RawMessage) bool {
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}
