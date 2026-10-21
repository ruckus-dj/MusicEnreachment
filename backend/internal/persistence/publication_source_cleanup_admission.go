package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/uptrace/bun"
)

// PublicationSourceCleanupAdmission pins the current publication witness that
// authorizes admission of source cleanup work. It is not execution authority.
type PublicationSourceCleanupAdmission struct {
	PositionID         uuid.UUID
	ExpectedEpoch      uuid.UUID
	ExpectedGeneration uuid.UUID
}

// PublicationSourceCleanupAdmissionResult contains either an admitted operation
// identity or blockers explaining why no work was admitted.
type PublicationSourceCleanupAdmissionResult struct {
	OperationID uuid.UUID
	Blockers    []string
}

type publicationSourceCleanupJobArgs struct {
	OperationID uuid.UUID `json:"operation_id"`
}

func (publicationSourceCleanupJobArgs) Kind() string { return "cleanup_publication_sources_v1" }

type publicationSourceCleanupOperationSnapshot struct {
	Version     int       `json:"version"`
	PositionID  uuid.UUID `json:"position_id"`
	OutputEpoch uuid.UUID `json:"output_epoch"`
}

type publicationSourceCleanupSnapshot struct {
	Version          int                               `json:"version"`
	WorkID           uuid.UUID                         `json:"work_id"`
	SourceRootID     uuid.UUID                         `json:"source_root_id"`
	SourceLocationID uuid.UUID                         `json:"source_location_id"`
	MediaVariantID   uuid.UUID                         `json:"media_variant_id"`
	ConfiguredPath   string                            `json:"configured_path"`
	InventoryPath    string                            `json:"inventory_path"`
	RelativePath     string                            `json:"relative_path"`
	SizeBytes        int64                             `json:"size_bytes"`
	Mtime            string                            `json:"mtime"`
	SourceLinks      []publicationSourceCleanupLink    `json:"source_links"`
	Borrowers        []publicationSourceCleanupWitness `json:"borrowers"`
}

type publicationSourceCleanupLink struct {
	ID             uuid.UUID `json:"id"`
	TrackID        uuid.UUID `json:"track_id"`
	MediaVariantID uuid.UUID `json:"media_variant_id"`
	Selected       bool      `json:"selected"`
	Compatible     bool      `json:"compatible"`
	DecisionMethod string    `json:"decision_method"`
	PolicyVersion  *string   `json:"policy_version"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type publicationSourceCleanupWitness struct {
	PositionID  uuid.UUID `json:"position_id"`
	ReleaseID   uuid.UUID `json:"release_id"`
	TrackID     uuid.UUID `json:"track_id"`
	Generation  uuid.UUID `json:"generation"`
	Epoch       uuid.UUID `json:"epoch"`
	ValidatedAt time.Time `json:"validated_at"`
}

// AdmitPublicationSourceCleanup validates all current borrowers and alternatives
// and atomically records operation, River delivery, item snapshots, and owners.
func (repository *SetupManagerRepository) AdmitPublicationSourceCleanup(ctx context.Context, request PublicationSourceCleanupAdmission, client RiverInserter) (PublicationSourceCleanupAdmissionResult, error) {
	result := PublicationSourceCleanupAdmissionResult{}
	if request.PositionID == uuid.Nil || request.ExpectedEpoch == uuid.Nil || request.ExpectedGeneration == uuid.Nil || client == nil {
		return result, fmt.Errorf("admit publication source cleanup: position, publication witness, and River client are required")
	}
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("admit publication source cleanup: lock output gate: %w", err)
		}
		if err := lockPublicationSourceCleanupPolicy(ctx, tx, true); err != nil {
			return fmt.Errorf("admit publication source cleanup: lock policy gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("admit publication source cleanup: lock source mutation gate: %w", err)
		}
		admissionResult, err := repository.admitPublicationSourceCleanupTx(ctx, tx, request, client)
		result = admissionResult
		if err != nil {
			return err
		}
		return nil
	})
	return result, err
}

func hasExistingSourceAnalysisWorkHold(ctx context.Context, tx bun.IDB, candidates []PublicationSourceCleanupCandidate) (bool, error) {
	for _, candidate := range candidates {
		exists, err := cleanupSourceConsumerExists(ctx, tx, candidate.WorkID)
		if err != nil {
			return false, err
		}
		if exists {
			return true, nil
		}
	}
	return false, nil
}

// cleanupSourceConsumerExists preserves both retained input ownership and live
// native deliveries. A generic terminal transition can clear work/step holds
// before the River worker returns, so operation state alone is not quiescence.
func cleanupSourceConsumerExists(ctx context.Context, tx bun.IDB, workID uuid.UUID) (bool, error) {
	var exists bool
	err := tx.NewRaw(`SELECT
		EXISTS (SELECT 1 FROM operation_source_work_hold WHERE work_id=?)
		OR EXISTS (SELECT 1 FROM source_analysis_step WHERE work_id=? AND execution_operation_id IS NOT NULL)
		OR EXISTS (
			SELECT 1 FROM operation o
			JOIN river_job j ON j.id=o.river_job_id OR j.args->>'operation_id'=o.id::text
			WHERE o.kind='analyze_source' AND j.kind='analyze_source_v1'
			AND j.state IN ('available','pending','retryable','scheduled','running')
			AND (
				EXISTS (SELECT 1 FROM source_analysis_step s WHERE s.work_id=? AND (s.last_operation_id=o.id OR s.execution_operation_id=o.id))
				OR (o.input_snapshot->'work_ids') @> jsonb_build_array(?::text)
			)
		)`, workID, workID, workID, workID).Scan(ctx, &exists)
	if err != nil {
		return false, fmt.Errorf("check source cleanup consumer liveness: %w", err)
	}
	return exists, nil
}

var errCleanupCandidateConflict = fmt.Errorf("cleanup candidate conflict")

func cleanupAdmissionBlockers(a PublicationSourceCleanupAssessment, epoch uuid.UUID) []string {
	blockers := append([]string(nil), a.Blockers...)
	if a.ValidatedAt == nil || a.Generation == uuid.Nil || a.Epoch != epoch {
		blockers = append(blockers, "current publication is not validated in the current output epoch")
	}
	for _, candidate := range a.Candidates {
		blockers = append(blockers, candidate.Blockers...)
		for _, borrower := range candidate.Borrowers {
			if borrower.Generation == nil || *borrower.Generation == uuid.Nil || borrower.Epoch == nil || *borrower.Epoch != epoch || borrower.ValidatedAt == nil || borrower.ValidatedAt.IsZero() {
				blockers = append(blockers, "a source borrower has no current validated publication")
			}
		}
	}
	return uniquePublicationCleanupStrings(blockers)
}

func lockCleanupCandidateRows(ctx context.Context, tx bun.IDB, candidates []PublicationSourceCleanupCandidate) error {
	roots, locations, works, variants := map[uuid.UUID]bool{}, map[uuid.UUID]bool{}, map[uuid.UUID]bool{}, map[uuid.UUID]bool{}
	for _, c := range candidates {
		if c.SourceRootID != uuid.Nil {
			roots[c.SourceRootID] = true
		}
		if c.SourceLocationID != uuid.Nil {
			locations[c.SourceLocationID] = true
		}
		if c.WorkID != uuid.Nil {
			works[c.WorkID] = true
		}
		if c.MediaVariantID != uuid.Nil {
			variants[c.MediaVariantID] = true
		}
	}
	for _, group := range []struct {
		table, key string
		ids        map[uuid.UUID]bool
	}{{"source_root", "id", roots}, {"source_location", "id", locations}, {"source_analysis_work", "id", works}, {"source_work_variant_identity", "media_variant_id", variants}} {
		ids := sortedCleanupIDs(group.ids)
		for _, id := range ids {
			if group.table == "source_work_variant_identity" {
				var locked []uuid.UUID
				if err := tx.NewRaw("SELECT work_id FROM "+group.table+" WHERE media_variant_id=? ORDER BY work_id FOR UPDATE", id).Scan(ctx, &locked); err != nil {
					return err
				}
				continue
			}
			var locked uuid.UUID
			if err := tx.NewRaw("SELECT "+group.key+" FROM "+group.table+" WHERE "+group.key+"=? FOR UPDATE", id).Scan(ctx, &locked); err != nil {
				return err
			}
		}
	}
	return nil
}

func lockCleanupBorrowerPositions(ctx context.Context, tx bun.IDB, candidates []PublicationSourceCleanupCandidate) error {
	ids := map[uuid.UUID]bool{}
	for _, c := range candidates {
		for _, b := range c.Borrowers {
			ids[b.PositionID] = true
		}
	}
	for _, id := range sortedCleanupIDs(ids) {
		var locked uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM release_track WHERE id=? FOR UPDATE`, id).Scan(ctx, &locked); err != nil {
			return err
		}
	}
	return nil
}

func sortedCleanupIDs(ids map[uuid.UUID]bool) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

func hasExistingCleanupOwner(ctx context.Context, tx bun.IDB, candidates []PublicationSourceCleanupCandidate) (bool, error) {
	for _, c := range candidates {
		var exists bool
		err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM source_cleanup_owner o WHERE o.work_id=? OR o.source_location_id=?)`, c.WorkID, c.SourceLocationID).Scan(ctx, &exists)
		if err != nil {
			return false, fmt.Errorf("admit publication source cleanup: inspect existing owners: %w", err)
		}
		if exists {
			return true, nil
		}
	}
	return false, nil
}

func verifyCleanupCandidateFacts(ctx context.Context, tx bun.IDB, candidates []PublicationSourceCleanupCandidate) error {
	seenWork, seenLocation := map[uuid.UUID]bool{}, map[uuid.UUID]bool{}
	for _, c := range candidates {
		if c.WorkID == uuid.Nil || c.SourceLocationID == uuid.Nil || seenWork[c.WorkID] || seenLocation[c.SourceLocationID] {
			return errCleanupCandidateConflict
		}
		seenWork[c.WorkID], seenLocation[c.SourceLocationID] = true, true
	}
	return nil
}

func insertCleanupOwner(ctx context.Context, tx bun.IDB, op *Operation, request PublicationSourceCleanupAdmission, epoch uuid.UUID, c PublicationSourceCleanupCandidate) error {
	snapshot, err := capturePublicationSourceCleanupSnapshot(ctx, tx, c)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	itemID, revision := uuid.New(), uuid.New()
	if _, err = tx.NewRaw(`INSERT INTO source_cleanup_item(id,operation_id,position_id,epoch,publication_generation,state,source_snapshot,borrower_revision) VALUES (?,?,?,?,?,'queued',?::jsonb,?)`, itemID, op.ID, request.PositionID, epoch, request.ExpectedGeneration, string(encoded), revision).Exec(ctx); err != nil {
		return err
	}
	if op.RiverJobID == nil {
		return fmt.Errorf("river job identity is required")
	}
	_, err = tx.NewRaw(`INSERT INTO source_cleanup_owner(item_id,operation_id,application_attempt,river_job_id,owner_uuid,source_root_id,source_location_id,work_id) VALUES (?,?,?,?,?,?,?,?)`, itemID, op.ID, op.Attempt, *op.RiverJobID, uuid.New(), c.SourceRootID, c.SourceLocationID, c.WorkID).Exec(ctx)
	return err
}

func capturePublicationSourceCleanupSnapshot(ctx context.Context, tx bun.IDB, c PublicationSourceCleanupCandidate) (publicationSourceCleanupSnapshot, error) {
	var snapshot publicationSourceCleanupSnapshot
	var root struct {
		ConfiguredPath string `bun:"configured_path"`
		InventoryPath  string `bun:"inventory_path"`
	}
	if err := tx.NewRaw(`SELECT configured_path,inventory_path FROM source_root WHERE id=?`, c.SourceRootID).Scan(ctx, &root); err != nil {
		return snapshot, err
	}
	var facts struct {
		Size  int64     `bun:"size_bytes"`
		Mtime time.Time `bun:"mtime"`
	}
	if err := tx.NewRaw(`SELECT size_bytes,mtime FROM source_location WHERE id=?`, c.SourceLocationID).Scan(ctx, &facts); err != nil {
		return snapshot, err
	}
	var links []publicationSourceCleanupLink
	if err := tx.NewRaw(`SELECT id,track_id,media_variant_id,selected,compatible,decision_method,policy_version,updated_at FROM track_source WHERE media_variant_id=? ORDER BY track_id,id`, c.MediaVariantID).Scan(ctx, &links); err != nil {
		return snapshot, err
	}
	borrowers := make([]publicationSourceCleanupWitness, 0, len(c.Borrowers))
	for _, b := range c.Borrowers {
		if b.Generation == nil || b.Epoch == nil || b.ValidatedAt == nil {
			return snapshot, errCleanupCandidateConflict
		}
		borrowers = append(borrowers, publicationSourceCleanupWitness{PositionID: b.PositionID, ReleaseID: b.ReleaseID, TrackID: b.TrackID, Generation: *b.Generation, Epoch: *b.Epoch, ValidatedAt: *b.ValidatedAt})
	}
	// The source snapshot is a frozen fact set; its token is intentionally not a
	// maintained borrower revision and must be revalidated by the executor.
	snapshot = publicationSourceCleanupSnapshot{Version: 1, WorkID: c.WorkID, SourceRootID: c.SourceRootID, SourceLocationID: c.SourceLocationID, MediaVariantID: c.MediaVariantID, ConfiguredPath: root.ConfiguredPath, InventoryPath: root.InventoryPath, RelativePath: c.RelativePath, SizeBytes: facts.Size, Mtime: facts.Mtime.UTC().Format(time.RFC3339Nano), SourceLinks: links, Borrowers: borrowers}
	return snapshot, nil
}

var _ river.JobArgs = publicationSourceCleanupJobArgs{}
