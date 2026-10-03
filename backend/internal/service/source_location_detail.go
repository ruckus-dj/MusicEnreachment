package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// SourceLocationDetailRepository is the read-only persistence contract of one
// location inspector read. It serves the published inventory row, the stored
// variant of its last successful analysis and the analysis operation that is
// still active, and it writes nothing: the inspector never probes a file and
// never touches the stored result.
type SourceLocationDetailRepository interface {
	ReadSourceLocationDetail(context.Context, uuid.UUID, uuid.UUID) (*persistence.SourceLocationDetailSnapshot, error)
}

// SourceLocationRootState is the availability of the owning root, reported
// beside the file facts so a client can keep a stale inventory or an
// unavailable root apart from the technical result itself.
type SourceLocationRootState struct {
	// Status is the availability a completed attempt last recorded.
	Status string
	// SafeError is the operator-facing reason of an unavailable root, nil when
	// the root is available or never checked.
	SafeError *string
	// Enabled is the operator switch of the root.
	Enabled bool
	// Stale is true when the last inventory describes a configured path other
	// than the current one.
	Stale bool
	// InventoryPath is the path the served inventory was read from, nil before a
	// first successful scan.
	InventoryPath *string
}

// SourceTechnicalResult is the stored result of one successful analysis,
// projected through the typed parser. The raw response is preserved next to the
// normalized facts and is never replaced by them.
type SourceTechnicalResult struct {
	AnalysisPolicyVersion int
	FFProbeVersion        string
	InspectedAt           time.Time
	AppliedOperationID    uuid.UUID
	Analysis              SourceTechnicalAnalysis
}

// SourceLocationDetail is the complete read-model of one inspected location: the
// inventory identity of the file, the availability of its root, the optional
// stored technical result and the analysis operation that is currently active.
// A location without a stored result has a nil MediaVariantID and a nil Result;
// unknown technical values stay nil inside the result instead of becoming zero
// or empty.
type SourceLocationDetail struct {
	RootID         uuid.UUID
	Root           SourceLocationRootState
	LocationID     uuid.UUID
	RelativePath   string
	SizeBytes      int64
	Mtime          time.Time
	ProbeStatus    string
	SafeError      *string
	MediaVariantID *uuid.UUID
	// Result is the last successful technical analysis, or nil when the file was
	// never analyzed or its result was unlinked by a changed inventory.
	Result *SourceTechnicalResult
	// ActiveAnalysisOperationID is the analysis of this exact location that is
	// still queued or running, or nil when none is. It is not the last analysis,
	// so a client can adopt an ongoing operation instead of showing the previous
	// result alone.
	ActiveAnalysisOperationID *uuid.UUID
}

// SourceLocationDetails reads one location of a root for the inspector. It never
// walks the source, never probes a file and never writes: it serves the last
// published inventory together with the stored result and the active operation.
type SourceLocationDetails struct {
	repository SourceLocationDetailRepository
}

func NewSourceLocationDetails(repository SourceLocationDetailRepository) *SourceLocationDetails {
	return &SourceLocationDetails{repository: repository}
}

// Read returns the detail of one location. A location of another root is
// reported with persistence.ErrSourceLocationNotFound, exactly like a location
// that does not exist, so a caller cannot address a file through a root it does
// not belong to.
func (s *SourceLocationDetails) Read(ctx context.Context, rootID, locationID uuid.UUID) (SourceLocationDetail, error) {
	if rootID == uuid.Nil || locationID == uuid.Nil {
		return SourceLocationDetail{}, fmt.Errorf("read source location detail: a root and a location are required")
	}
	snapshot, err := s.repository.ReadSourceLocationDetail(ctx, rootID, locationID)
	if err != nil {
		if IsSourceRootNotFound(err) {
			return SourceLocationDetail{}, fmt.Errorf("read source location detail: %w", ErrSourceRootNotFound)
		}
		return SourceLocationDetail{}, fmt.Errorf("read source location detail: %w", err)
	}
	root, location := snapshot.Root, snapshot.Location
	detail := SourceLocationDetail{
		RootID: root.ID,
		Root: SourceLocationRootState{
			Status: root.Status, SafeError: root.SafeError, Enabled: root.Enabled,
			Stale: root.Stale(), InventoryPath: root.InventoryPath,
		},
		LocationID: location.ID, RelativePath: location.RelativePath, SizeBytes: location.SizeBytes,
		Mtime: location.Mtime, ProbeStatus: location.ProbeStatus, SafeError: location.SafeError,
		MediaVariantID: location.MediaVariantID,
	}
	if variant := snapshot.Variant; variant != nil {
		analysis, err := ParseSourceTechnicalAnalysis(variant.FFProbeJSON)
		if err != nil {
			return SourceLocationDetail{}, fmt.Errorf("read source location detail: the stored technical result cannot be read: %w", err)
		}
		detail.Result = &SourceTechnicalResult{
			AnalysisPolicyVersion: variant.AnalysisPolicyVersion,
			FFProbeVersion:        variant.FFProbeVersion,
			InspectedAt:           variant.InspectedAt,
			AppliedOperationID:    variant.AppliedOperationID,
			Analysis:              analysis,
		}
	}
	detail.ActiveAnalysisOperationID = snapshot.ActiveOperationID
	return detail, nil
}
