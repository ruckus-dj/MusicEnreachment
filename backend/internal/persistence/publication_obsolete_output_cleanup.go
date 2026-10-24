package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/uptrace/bun"
)

const (
	ObsoletePublicationOutputCleanupOperationKind = "obsolete_publication_output_cleanup_v1"
	ObsoletePublicationOutputCleanupJobKind       = "obsolete_publication_output_cleanup_v1"
)

// ObsoletePublicationOutputCleanupJobArgs deliberately carries no cleanup
// authority; the worker must reload it by operation ID.
type ObsoletePublicationOutputCleanupJobArgs struct {
	OperationID uuid.UUID `json:"operation_id"`
}

func (ObsoletePublicationOutputCleanupJobArgs) Kind() string {
	return ObsoletePublicationOutputCleanupJobKind
}

// AdmitObsoletePublicationOutputCleanupTx creates the operation, live cleanup
// authority, and River job atomically in the caller's transaction. The caller
// must hold the output-admission gate and pass an args factory that encodes only
// operationID.
func (repository *SetupManagerRepository) AdmitObsoletePublicationOutputCleanupTx(
	ctx context.Context,
	tx bun.Tx,
	request ObsoletePublicationOutputCleanupAdmission,
	client RiverInserter,
	argsFactory func(uuid.UUID) river.JobArgs,
	options *river.InsertOpts,
) (*Operation, error) {
	if client == nil || argsFactory == nil {
		return nil, errors.New("admit obsolete publication output cleanup: River client and args factory are required")
	}
	if request.Epoch == uuid.Nil || request.PositionID == uuid.Nil || request.Generation == uuid.Nil ||
		request.NamespaceClaimID == uuid.Nil || request.RelativePath == "" || request.NativePath == "" ||
		request.FileSize < 0 || len(request.SHA256) != sha256.Size || request.ModifiedAt.IsZero() {
		return nil, errors.New("admit obsolete publication output cleanup: invalid output authority")
	}
	if !frozenPublicationRelativePath(request.RelativePath) || !filepath.IsAbs(request.NativePath) ||
		!validCleanupDirectoryIdentity(request.OutputRootIdentity) || !validCleanupDirectoryIdentity(request.ParentIdentity) ||
		request.OutputRootIdentity.Platform != request.ParentIdentity.Platform {
		return nil, errors.New("admit obsolete publication output cleanup: invalid path or directory identity")
	}
	if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
		return nil, fmt.Errorf("admit obsolete publication output cleanup: lock output admission gate: %w", err)
	}
	var currentGeneration uuid.UUID
	err := tx.NewRaw(`SELECT generation FROM track_publication WHERE position_id=? FOR UPDATE`, request.PositionID).Scan(ctx, &currentGeneration)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("admit obsolete publication output cleanup: lock current publication: %w", err)
	}
	if err == nil && currentGeneration == request.Generation {
		return nil, errors.New("admit obsolete publication output cleanup: output is still current")
	}
	var claim PublicationNamespaceClaim
	if err := tx.NewSelect().Model(&claim).Where("id=? AND epoch=? AND relative_path=? AND node_kind='file' AND owner_position_id=?", request.NamespaceClaimID, request.Epoch, request.RelativePath, request.PositionID).For("UPDATE").Scan(ctx); err != nil {
		return nil, fmt.Errorf("admit obsolete publication output cleanup: lock namespace claim: %w", err)
	}
	var reservation PublicationNamespaceReservation
	if err := tx.NewSelect().Model(&reservation).Where("namespace_claim_id=? AND position_id=? AND generation=?", request.NamespaceClaimID, request.PositionID, request.Generation).For("UPDATE").Scan(ctx); err != nil {
		return nil, fmt.Errorf("admit obsolete publication output cleanup: lock old reservation: %w", err)
	}
	var protected bool
	if err := tx.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM publication_namespace_reservation r
		JOIN track_publication p ON p.position_id=r.position_id AND p.epoch=? AND p.generation=r.generation
		WHERE r.namespace_claim_id=?
		UNION ALL
		SELECT 1 FROM publication_namespace_reservation r
		JOIN track_publication_attempt a ON a.position_id=r.position_id AND a.epoch=? AND a.candidate_generation=r.generation
		WHERE r.namespace_claim_id=?
		UNION ALL
		SELECT 1 FROM publication_namespace_reservation r WHERE r.namespace_claim_id=? AND NOT (r.position_id=? AND r.generation=?)
	)`, request.Epoch, request.NamespaceClaimID, request.Epoch, request.NamespaceClaimID, request.NamespaceClaimID, request.PositionID, request.Generation).Scan(ctx, &protected); err != nil {
		return nil, fmt.Errorf("admit obsolete publication output cleanup: check namespace consumers: %w", err)
	}
	if protected {
		return nil, errors.New("admit obsolete publication output cleanup: namespace still has a live consumer")
	}
	var activeEpoch uuid.UUID
	if err := tx.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true FOR UPDATE`).Scan(ctx, &activeEpoch); err != nil {
		return nil, fmt.Errorf("admit obsolete publication output cleanup: read namespace epoch: %w", err)
	}
	if activeEpoch != request.Epoch {
		return nil, errors.New("admit obsolete publication output cleanup: namespace epoch changed")
	}
	operationID := uuid.New()
	args := argsFactory(operationID)
	if args == nil || args.Kind() != ObsoletePublicationOutputCleanupJobKind {
		return nil, errors.New("admit obsolete publication output cleanup: invalid job kind")
	}
	encodedArgs, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("admit obsolete publication output cleanup: encode job args: %w", err)
	}
	var fields map[string]json.RawMessage
	var encodedOperationID uuid.UUID
	if json.Unmarshal(encodedArgs, &fields) != nil || len(fields) != 1 || json.Unmarshal(fields["operation_id"], &encodedOperationID) != nil || encodedOperationID != operationID {
		return nil, errors.New("admit obsolete publication output cleanup: job args must contain only the operation ID")
	}
	input, err := json.Marshal(struct {
		OperationID uuid.UUID `json:"operation_id"`
	}{OperationID: operationID})
	if err != nil {
		return nil, fmt.Errorf("admit obsolete publication output cleanup: encode operation snapshot: %w", err)
	}
	operation := &Operation{ID: operationID, Kind: ObsoletePublicationOutputCleanupOperationKind, State: "queued", Stage: "queued", InputSnapshot: input, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := repository.CreateOperationWith(ctx, tx, operation); err != nil {
		return nil, fmt.Errorf("admit obsolete publication output cleanup: create operation: %w", err)
	}
	rootIdentity, err := json.Marshal(request.OutputRootIdentity)
	if err != nil {
		return nil, fmt.Errorf("admit obsolete publication output cleanup: encode output root identity: %w", err)
	}
	parentIdentity, err := json.Marshal(request.ParentIdentity)
	if err != nil {
		return nil, fmt.Errorf("admit obsolete publication output cleanup: encode parent identity: %w", err)
	}
	row := &ObsoletePublicationOutputCleanup{
		OperationID: operationID, Epoch: request.Epoch, PositionID: request.PositionID,
		Generation: request.Generation, RelativePath: request.RelativePath, NativePath: request.NativePath,
		OutputRootIdentity: rootIdentity, ParentIdentity: parentIdentity,
		NamespaceClaimID: request.NamespaceClaimID, FileSize: request.FileSize,
		ModifiedAt: request.ModifiedAt.UTC(), SHA256: append([]byte(nil), request.SHA256...), State: "queued",
	}
	if _, err := tx.NewInsert().Model(row).Exec(ctx); err != nil {
		return nil, fmt.Errorf("admit obsolete publication output cleanup: create authority: %w", err)
	}
	result, err := client.InsertTx(ctx, tx.Tx, args, options)
	if err != nil {
		return nil, fmt.Errorf("admit obsolete publication output cleanup: insert River job: %w", err)
	}
	if result == nil || result.Job == nil || result.Job.ID <= 0 {
		return nil, errors.New("admit obsolete publication output cleanup: River job identity is missing")
	}
	operation.RiverJobID = &result.Job.ID
	if _, err := tx.NewUpdate().Model(operation).Column("river_job_id").WherePK().Exec(ctx); err != nil {
		return nil, fmt.Errorf("admit obsolete publication output cleanup: bind River job: %w", err)
	}
	return operation, nil
}

func validCleanupDirectoryIdentity(identity PublicationDirectoryIdentity) bool {
	return identity.Platform != "" && identity.Volume != "" && identity.FileID != ""
}
