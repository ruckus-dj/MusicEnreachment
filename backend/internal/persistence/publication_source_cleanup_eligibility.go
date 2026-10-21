package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// PublicationSourceCleanupCandidate is advisory inventory evidence for one
// source-work identity associated with a track at a publication position.
// Nothing in this type grants authority to remove a source file.
type PublicationSourceCleanupCandidate struct {
	TrackID           uuid.UUID
	MediaVariantID    uuid.UUID
	WorkID            uuid.UUID
	SourceRootID      uuid.UUID
	SourceLocationID  uuid.UUID
	RelativePath      string
	BorrowerPositions []uuid.UUID
	Borrowers         []PublicationSourceCleanupBorrower
	Held              bool
	Blockers          []string
}

// PublicationSourceCleanupBorrower describes each release position currently
// linked to the source variant and the publication witness observed for it.
type PublicationSourceCleanupBorrower struct {
	PositionID  uuid.UUID  `bun:"position_id"`
	ReleaseID   uuid.UUID  `bun:"release_id"`
	TrackID     uuid.UUID  `bun:"track_id"`
	Generation  *uuid.UUID `bun:"generation"`
	Epoch       *uuid.UUID `bun:"epoch"`
	ValidatedAt *time.Time `bun:"validated_at"`
}

// PublicationSourceCleanupAssessment is a read-only snapshot. The validated
// publication tuple and source inventory can change immediately after this
// method returns; callers must never use it as unlink authority.
type PublicationSourceCleanupAssessment struct {
	PositionID  uuid.UUID
	TrackID     uuid.UUID
	Generation  uuid.UUID
	Epoch       uuid.UUID
	ValidatedAt *time.Time
	Candidates  []PublicationSourceCleanupCandidate
	Blockers    []string
}

type publicationCleanupRow struct {
	TrackID            uuid.UUID  `bun:"track_id"`
	VariantID          uuid.UUID  `bun:"media_variant_id"`
	WorkID             *uuid.UUID `bun:"work_id"`
	WorkRootID         *uuid.UUID `bun:"source_root_id"`
	WorkLocationID     *uuid.UUID `bun:"work_location_id"`
	LocationID         *uuid.UUID `bun:"location_id"`
	LocationRootID     *uuid.UUID `bun:"location_root_id"`
	RelativePath       *string    `bun:"relative_path"`
	LocationVariantID  *uuid.UUID `bun:"location_variant_id"`
	WorkSizeBytes      *int64     `bun:"work_size_bytes"`
	LocationSizeBytes  *int64     `bun:"location_size_bytes"`
	WorkMtime          *time.Time `bun:"work_mtime"`
	LocationMtime      *time.Time `bun:"location_mtime"`
	WorkRelativePath   *string    `bun:"work_relative_path"`
	RootID             *uuid.UUID `bun:"root_id"`
	RootConfiguredPath *string    `bun:"root_configured_path"`
	RootInventoryPath  *string    `bun:"root_inventory_path"`
	WorkConfiguredPath *string    `bun:"work_configured_path"`
	WorkInventoryPath  *string    `bun:"work_inventory_path"`
}

// ReadPublicationSourceCleanupAssessment reads the current publication witness
// and every associated source alternative, including unselected alternatives.
func (repository *SetupManagerRepository) ReadPublicationSourceCleanupAssessment(ctx context.Context, positionID uuid.UUID) (PublicationSourceCleanupAssessment, error) {
	assessment := PublicationSourceCleanupAssessment{PositionID: positionID}
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		return readPublicationSourceCleanupAssessmentTx(ctx, tx, positionID, &assessment)
	})
	if err != nil {
		return assessment, err
	}
	return assessment, nil
}

func readPublicationSourceCleanupAssessmentTx(ctx context.Context, tx bun.IDB, positionID uuid.UUID, assessment *PublicationSourceCleanupAssessment) error {
	var trackID uuid.UUID
	if err := tx.NewRaw(`SELECT track_id FROM release_track WHERE id=?`, positionID).Scan(ctx, &trackID); err != nil {
		if err == sql.ErrNoRows {
			return err
		}
		return fmt.Errorf("read publication source cleanup position: %w", err)
	}
	assessment.TrackID = trackID
	var publication struct {
		Generation  uuid.UUID `bun:"generation"`
		Epoch       uuid.UUID `bun:"epoch"`
		ValidatedAt time.Time `bun:"validated_at"`
	}
	if err := tx.NewRaw(`SELECT generation,epoch,validated_at FROM track_publication WHERE position_id=?`, positionID).Scan(ctx, &publication); err != nil {
		if err == sql.ErrNoRows {
			assessment.Blockers = append(assessment.Blockers, "no current validated publication")
		} else {
			return fmt.Errorf("read current publication witness: %w", err)
		}
	} else {
		assessment.Generation, assessment.Epoch = publication.Generation, publication.Epoch
		validated := publication.ValidatedAt
		assessment.ValidatedAt = &validated
		if publication.Generation == uuid.Nil || publication.Epoch == uuid.Nil || publication.ValidatedAt.IsZero() {
			assessment.Blockers = append(assessment.Blockers, "current publication witness is incomplete")
		}
	}

	var rows []publicationCleanupRow
	if err := tx.NewRaw(`SELECT ts.track_id,ts.media_variant_id,swi.work_id,
		w.source_root_id,w.location_id AS work_location_id,w.current_location_id AS location_id,
		sl.source_root_id AS location_root_id,sl.relative_path,sl.media_variant_id AS location_variant_id,
		w.size_bytes AS work_size_bytes,sl.size_bytes AS location_size_bytes,w.mtime AS work_mtime,sl.mtime AS location_mtime,
		w.relative_path AS work_relative_path,r.id AS root_id,r.configured_path AS root_configured_path,r.inventory_path AS root_inventory_path,
		w.configured_path AS work_configured_path,w.inventory_path AS work_inventory_path
		FROM track_source ts
		LEFT JOIN source_work_variant_identity swi ON swi.media_variant_id=ts.media_variant_id
		LEFT JOIN source_analysis_work w ON w.id=swi.work_id
		LEFT JOIN source_location sl ON sl.id=w.current_location_id
		LEFT JOIN source_root r ON r.id=w.source_root_id
		WHERE ts.track_id=? ORDER BY ts.media_variant_id,swi.work_id`, trackID).Scan(ctx, &rows); err != nil {
		return fmt.Errorf("read publication source alternatives: %w", err)
	}
	for _, row := range rows {
		candidate := PublicationSourceCleanupCandidate{TrackID: row.TrackID, MediaVariantID: row.VariantID}
		if row.WorkID == nil {
			candidate.Blockers = append(candidate.Blockers, "source variant has no canonical work identity")
		} else {
			candidate.WorkID = *row.WorkID
			if row.WorkRootID != nil {
				candidate.SourceRootID = *row.WorkRootID
			}
			if row.LocationID != nil {
				candidate.SourceLocationID = *row.LocationID
			}
			if row.RelativePath != nil {
				candidate.RelativePath = *row.RelativePath
			}
			if row.LocationID == nil || row.RelativePath == nil {
				candidate.Blockers = append(candidate.Blockers, "source work has no current inventory location")
			} else if row.WorkLocationID == nil || *row.WorkLocationID != *row.LocationID || row.WorkRootID == nil || row.LocationRootID == nil || *row.WorkRootID != *row.LocationRootID {
				candidate.Blockers = append(candidate.Blockers, "source work current location identity is inconsistent")
			}
			if row.WorkRelativePath == nil || row.RelativePath == nil || *row.WorkRelativePath != *row.RelativePath {
				candidate.Blockers = append(candidate.Blockers, "source work relative path does not match current inventory location")
			}
			if row.LocationVariantID == nil || *row.LocationVariantID != row.VariantID {
				candidate.Blockers = append(candidate.Blockers, "source location variant does not match source association")
			}
			if row.WorkSizeBytes == nil || row.LocationSizeBytes == nil || *row.WorkSizeBytes != *row.LocationSizeBytes || row.WorkMtime == nil || row.LocationMtime == nil || !sourceAnalysisMtime(*row.WorkMtime).Equal(sourceAnalysisMtime(*row.LocationMtime)) {
				candidate.Blockers = append(candidate.Blockers, "source work file facts do not match current inventory location")
			}
			if row.RootID == nil || row.RootConfiguredPath == nil || row.RootInventoryPath == nil {
				candidate.Blockers = append(candidate.Blockers, "source root or its inventory identity is missing")
			} else {
				root := SourceRoot{ID: *row.RootID, ConfiguredPath: *row.RootConfiguredPath, InventoryPath: row.RootInventoryPath}
				// Stale uses the same inventory-versus-configured-path rule as source
				// analysis; stale paths never identify the current configured root.
				if root.Stale() {
					candidate.Blockers = append(candidate.Blockers, "source root inventory is stale for its configured path")
				}
				if row.WorkConfiguredPath == nil || row.WorkInventoryPath == nil || *row.WorkConfiguredPath != root.ConfiguredPath || *row.WorkInventoryPath != *root.InventoryPath {
					candidate.Blockers = append(candidate.Blockers, "source work root paths do not match current source root")
				}
			}
			if candidate.WorkID != uuid.Nil {
				held, err := sourceHoldExists(ctx, tx, candidate.SourceRootID, candidate.SourceLocationID, candidate.WorkID)
				if err != nil {
					return fmt.Errorf("check publication source cleanup hold: %w", err)
				}
				candidate.Held = held
				if held {
					candidate.Blockers = append(candidate.Blockers, "source is protected by a publication hold")
				}
			}
		}
		var borrowers []PublicationSourceCleanupBorrower
		if err := tx.NewRaw(`SELECT DISTINCT rt.id AS position_id,rt.release_id,rt.track_id,
			publication.generation,publication.epoch,publication.validated_at
			FROM track_source borrower JOIN release_track rt ON rt.track_id=borrower.track_id
			LEFT JOIN track_publication publication ON publication.position_id=rt.id
			WHERE borrower.media_variant_id=? ORDER BY rt.id`, row.VariantID).Scan(ctx, &borrowers); err != nil {
			return fmt.Errorf("read publication source borrowers: %w", err)
		}
		candidate.Borrowers = borrowers
		for _, borrower := range borrowers {
			candidate.BorrowerPositions = append(candidate.BorrowerPositions, borrower.PositionID)
			if borrower.Generation == nil || *borrower.Generation == uuid.Nil || borrower.Epoch == nil || *borrower.Epoch == uuid.Nil || borrower.ValidatedAt == nil || borrower.ValidatedAt.IsZero() {
				candidate.Blockers = append(candidate.Blockers, "a source borrower has no complete validated publication")
			}
		}
		assessment.Candidates = append(assessment.Candidates, candidate)
		assessment.Blockers = append(assessment.Blockers, candidate.Blockers...)
	}
	if len(rows) == 0 {
		assessment.Blockers = append(assessment.Blockers, "track has no source alternatives")
	}
	assessment.Blockers = uniquePublicationCleanupStrings(assessment.Blockers)
	sort.Slice(assessment.Candidates, func(i, j int) bool {
		a, b := assessment.Candidates[i], assessment.Candidates[j]
		if a.MediaVariantID != b.MediaVariantID {
			return a.MediaVariantID.String() < b.MediaVariantID.String()
		}
		return a.WorkID.String() < b.WorkID.String()
	})
	return nil
}

func uniquePublicationCleanupStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; !ok {
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}
