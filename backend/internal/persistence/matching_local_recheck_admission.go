package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// MatchingLayoutTarget is one frozen automatic layout target in calculator
// input form. It is evidence for calculation, not an apply authorization.
type MatchingLayoutTarget struct {
	Evidence  json.RawMessage
	Captures  []IncomingGroupingCapture
	Threshold float64
}

// CaptureMatchingLayoutTarget freezes one automatic group's current layout
// target in a short read-committed transaction. Calculation happens outside
// that transaction; the eventual apply rechecks this exact evidence.
func (repository *IncomingMatchCandidateRepository) CaptureMatchingLayoutTarget(ctx context.Context, groupID uuid.UUID, policyVersion string) (MatchingLayoutTarget, error) {
	if repository == nil || repository.db == nil {
		return MatchingLayoutTarget{}, errors.New("matching layout target capture is unavailable")
	}
	var target MatchingLayoutTarget
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		var err error
		target.Evidence, target.Captures, target.Threshold, err = CaptureMatchingLayoutTargetTx(ctx, tx, groupID, policyVersion)
		return err
	})
	if err != nil {
		return MatchingLayoutTarget{}, err
	}
	return target, nil
}

// ApplyAutomaticLocalFallbackTx materializes a complete, already authorized
// Local layout and acquires cleanup protection in the same transaction. The
// caller must hold the output, source, and catalog fences and roll back on any
// error. Neither a preview nor this helper authorizes publication.
func ApplyAutomaticLocalFallbackTx(ctx context.Context, tx bun.Tx, operationID uuid.UUID, preview MatchingLocalPreview, captures []IncomingGroupingCapture) (uuid.UUID, uuid.UUID, error) {
	if operationID == uuid.Nil || len(captures) == 0 {
		return uuid.Nil, uuid.Nil, ErrIncomingMatchConflict
	}
	localReleaseID, sources, err := materializeAutomaticMatchingLocalTx(ctx, tx, preview)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	recheckID, err := createMatchingLocalRecheckTx(ctx, tx, matchingLocalRecheckAdmission{
		OperationID: operationID, ProviderReleaseID: preview.ProviderReleaseID, LocalReleaseID: localReleaseID,
		Captures: captures, Sources: sources,
	})
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	return localReleaseID, recheckID, nil
}

// AutomaticLocalFallbackCommit is one calculated complete Local layout and the
// exact outage/generation evidence observed before calculation. LayoutTarget is
// the frozen target used by the calculator; this transaction rechecks it against
// current sources, threshold, corrections and catalog evidence instead of
// trusting the caller's captures.
type AutomaticLocalFallbackCommit struct {
	GroupID                   uuid.UUID
	OutageRunID               uuid.UUID
	ObservedRequestGeneration int64
	ProviderReleaseID         uuid.UUID
	Assignments               []MatchingLocalAssignment
	LayoutTarget              json.RawMessage
	HeaderDigest              string
	ReleaseIDs                []uuid.UUID
	LayoutDigest              string
}

// AutomaticLocalFallbackResult distinguishes a first application from an exact
// replay of the same committed apply operation.
type AutomaticLocalFallbackResult struct {
	Applied        bool
	Replayed       bool
	LocalReleaseID uuid.UUID
	RecheckID      uuid.UUID
}

// ApplyAutomaticLocalFallback commits one complete automatic Local layout under
// output, source and catalog fences. It rechecks the frozen layout target, requires the exact
// pending generation to still belong to the classified MusicBrainz outage, and
// only then materializes assignments and the durable source veto. The observed
// generation and the source veto are committed atomically.
//
// A repeat delivery is accepted only when it exactly replays the same committed
// operation (same group, outage run, observed generation, provider release,
// assignments and layout target). Any other existing recheck for the group, any
// stale evidence, and any already-acknowledged non-replay generation return
// ErrIncomingMatchConflict without acknowledging anything.
//
// Assignments are per ready member, not per capture. A member backed by several
// captures still produces one preview source with all media variants, while
// createMatchingLocalRecheckTx expands every current capture into its own
// immutable source witness.
func (repository *IncomingMatchCandidateRepository) ApplyAutomaticLocalFallback(
	ctx context.Context, commit AutomaticLocalFallbackCommit,
) (AutomaticLocalFallbackResult, error) {
	if repository == nil || repository.db == nil {
		return AutomaticLocalFallbackResult{}, errors.New("automatic Local fallback commit is unavailable")
	}
	if err := commit.validate(); err != nil {
		return AutomaticLocalFallbackResult{}, err
	}
	var result AutomaticLocalFallbackResult
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("apply automatic Local fallback: lock output admission gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("apply automatic Local fallback: lock source mutation gate: %w", err)
		}
		if err := lockToolsMoveGateShared(ctx, tx); err != nil {
			return fmt.Errorf("apply automatic Local fallback: lock settings gate: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(hashtext(?))`, "musicbrainz-config"); err != nil {
			return fmt.Errorf("apply automatic Local fallback: lock MusicBrainz configuration: %w", err)
		}
		var sourceID uuid.UUID
		if err := tx.NewRaw(`SELECT source.id FROM matching_run run JOIN provider_source source ON source.id=run.provider_source_id
			WHERE run.id=? AND source.configuration_identity=run.provider_configuration_identity
			FOR UPDATE OF source`, commit.OutageRunID).Scan(ctx, &sourceID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrIncomingMatchConflict
			}
			return fmt.Errorf("apply automatic Local fallback: lock outage provider source: %w", err)
		}
		generation, err := lockAutomaticLocalFallbackGeneration(ctx, tx, commit.GroupID, commit.ObservedRequestGeneration)
		if err != nil {
			return err
		}
		if generation.CurrentRunID == nil || *generation.CurrentRunID != commit.OutageRunID {
			return fmt.Errorf("apply automatic Local fallback: current run is not the observed outage: %w", ErrIncomingMatchConflict)
		}
		// The pending generation must still be the dedicated generation written
		// by the outage failure, not a later independent request. The marker and
		// the original-admission-generation relation are both required.
		outageRunID, err := currentMatchingMusicBrainzOutageFallback(ctx, tx, commit.GroupID, commit.ObservedRequestGeneration)
		if err != nil {
			return err
		}
		if outageRunID == uuid.Nil || outageRunID != commit.OutageRunID {
			return fmt.Errorf("apply automatic Local fallback: MusicBrainz outage evidence changed: %w", ErrIncomingMatchConflict)
		}
		captures, err := RecheckMatchingLayoutTargetTx(ctx, tx, commit.GroupID, MatchingLayoutPolicyVersion, commit.LayoutTarget)
		if err != nil {
			return fmt.Errorf("apply automatic Local fallback: recheck layout target: %w", err)
		}
		groupRevision, authorityRevision, err := automaticLocalFallbackTargetRevisions(commit.LayoutTarget)
		if err != nil {
			return err
		}
		intent, err := canonicalMatchingLocalIntent(MatchingLocalIntent{
			ProviderReleaseID:         commit.ProviderReleaseID,
			ExpectedGroupRevision:     groupRevision,
			ExpectedAuthorityRevision: authorityRevision,
			Assignments:               append([]MatchingLocalAssignment(nil), commit.Assignments...),
		})
		if err != nil {
			return fmt.Errorf("apply automatic Local fallback: intent is invalid: %w", err)
		}
		decisionDigest, err := automaticLocalFallbackDecisionDigest(commit.LayoutTarget, intent.ProviderReleaseID, intent.Assignments)
		if err != nil {
			return err
		}
		replay, replayReleaseID, replayRecheckID, err := matchingLocalFallbackExactReplay(ctx, tx, commit, decisionDigest)
		if err != nil {
			return err
		}
		if replay {
			if generation.ReconciledGeneration < commit.ObservedRequestGeneration {
				if err := NewSourceInventoryRepository(repository.db).AcknowledgeCurrentRequestWith(ctx, tx, commit.GroupID, commit.ObservedRequestGeneration); err != nil {
					return fmt.Errorf("apply automatic Local fallback: acknowledge exact replay: %w", err)
				}
			}
			result = AutomaticLocalFallbackResult{Replayed: true, LocalReleaseID: replayReleaseID, RecheckID: replayRecheckID}
			return nil
		}
		if generation.ReconciledGeneration >= commit.ObservedRequestGeneration {
			return fmt.Errorf("apply automatic Local fallback: generation was already acknowledged: %w", ErrIncomingMatchConflict)
		}
		if err := ValidateMatchingLocalLayoutEvidenceTx(ctx, tx, commit.HeaderDigest, commit.ReleaseIDs, commit.LayoutDigest); err != nil {
			return err
		}
		preview, err := previewMatchingLocalTx(ctx, tx, commit.GroupID, intent, false)
		if err != nil {
			return fmt.Errorf("apply automatic Local fallback: validate layout preview: %w", err)
		}
		if preview.GroupRevision != groupRevision || preview.AuthorityRevision != authorityRevision ||
			preview.ProviderReleaseID != commit.ProviderReleaseID || preview.PreviewDigest == "" ||
			len(preview.OmittedFileMemberIDs) != 0 || len(preview.Sources) != len(preview.Assignments) {
			return fmt.Errorf("apply automatic Local fallback: preview evidence changed: %w", ErrIncomingMatchConflict)
		}
		operationID := uuid.New()
		snapshot, err := json.Marshal(automaticLocalFallbackOperationSnapshot{
			SchemaVersion:             1,
			GroupID:                   commit.GroupID,
			OutageRunID:               commit.OutageRunID,
			ObservedRequestGeneration: commit.ObservedRequestGeneration,
			ProviderReleaseID:         commit.ProviderReleaseID,
			DecisionDigest:            decisionDigest,
			PolicyVersion:             MatchingLayoutPolicyVersion,
		})
		if err != nil {
			return fmt.Errorf("apply automatic Local fallback: encode operation snapshot: %w", err)
		}
		snapshot, err = canonicalMatchingJSON(snapshot)
		if err != nil {
			return fmt.Errorf("apply automatic Local fallback: canonicalize operation snapshot: %w", err)
		}
		if _, err := tx.NewRaw(`INSERT INTO operation (id,kind,state,stage,input_snapshot,finished_at) VALUES (?, 'matching_evaluation_v1','succeeded','local_fallback_applied',?::jsonb,now())`, operationID, string(snapshot)).Exec(ctx); err != nil {
			return fmt.Errorf("apply automatic Local fallback: create apply operation: %w", err)
		}
		localReleaseID, recheckID, err := ApplyAutomaticLocalFallbackTx(ctx, tx, operationID, preview, captures)
		if err != nil {
			return fmt.Errorf("apply automatic Local fallback: materialize layout and source veto: %w", err)
		}
		if err := NewSourceInventoryRepository(repository.db).AcknowledgeCurrentRequestWith(ctx, tx, commit.GroupID, commit.ObservedRequestGeneration); err != nil {
			return fmt.Errorf("apply automatic Local fallback: acknowledge observed request: %w", err)
		}
		result = AutomaticLocalFallbackResult{Applied: true, LocalReleaseID: localReleaseID, RecheckID: recheckID}
		return nil
	})
	if err != nil {
		return AutomaticLocalFallbackResult{}, err
	}
	return result, nil
}

func (commit AutomaticLocalFallbackCommit) validate() error {
	if commit.GroupID == uuid.Nil || commit.OutageRunID == uuid.Nil || commit.ObservedRequestGeneration < 1 ||
		commit.ProviderReleaseID == uuid.Nil || len(commit.Assignments) == 0 || len(commit.LayoutTarget) == 0 ||
		commit.HeaderDigest == "" || !slices.Contains(commit.ReleaseIDs, commit.ProviderReleaseID) || commit.LayoutDigest == "" {
		return errors.New("automatic Local fallback commit is incomplete")
	}
	return nil
}

type automaticLocalFallbackGeneration struct {
	RequestedGeneration  int64      `bun:"requested_generation"`
	ReconciledGeneration int64      `bun:"reconciled_generation"`
	CurrentRunID         *uuid.UUID `bun:"current_run_id"`
}

// lockAutomaticLocalFallbackGeneration requires the exact observed request to
// still be current. A concurrent later request is not silently absorbed.
func lockAutomaticLocalFallbackGeneration(ctx context.Context, tx bun.Tx, groupID uuid.UUID, observedGeneration int64) (automaticLocalFallbackGeneration, error) {
	var generation automaticLocalFallbackGeneration
	if err := tx.NewRaw(`SELECT requested_generation,reconciled_generation,current_run_id
		FROM incoming_matching_generation WHERE group_id=? FOR UPDATE`, groupID).Scan(ctx, &generation); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return generation, fmt.Errorf("apply automatic Local fallback: request generation is missing: %w", ErrIncomingMatchConflict)
		}
		return generation, fmt.Errorf("apply automatic Local fallback: read request generation: %w", err)
	}
	if generation.RequestedGeneration != observedGeneration || generation.ReconciledGeneration > generation.RequestedGeneration {
		return generation, fmt.Errorf("apply automatic Local fallback: observed request generation changed: %w", ErrIncomingMatchConflict)
	}
	return generation, nil
}

type automaticLocalFallbackOperationSnapshot struct {
	SchemaVersion             int       `json:"schema_version"`
	GroupID                   uuid.UUID `json:"group_id"`
	OutageRunID               uuid.UUID `json:"outage_run_id"`
	ObservedRequestGeneration int64     `json:"observed_request_generation"`
	ProviderReleaseID         uuid.UUID `json:"provider_release_id"`
	DecisionDigest            string    `json:"decision_digest"`
	PolicyVersion             string    `json:"policy_version"`
}

// matchingLocalFallbackExactReplay accepts only the same committed operation.
// An old recheck for the same group never authorizes a new acknowledgement.
func matchingLocalFallbackExactReplay(
	ctx context.Context, tx bun.Tx, commit AutomaticLocalFallbackCommit, decisionDigest string,
) (bool, uuid.UUID, uuid.UUID, error) {
	var rows []struct {
		RecheckID          uuid.UUID `bun:"recheck_id"`
		LocalReleaseIDText string    `bun:"local_release_id"`
		Snapshot           []byte    `bun:"input_snapshot"`
	}
	if err := tx.NewRaw(`SELECT recheck.id AS recheck_id,
			COALESCE(recheck.incumbent_snapshot->>'local_release_id','') AS local_release_id,
			operation.input_snapshot
		FROM matching_local_recheck recheck
		JOIN operation operation ON operation.id=recheck.local_apply_operation_id
		WHERE recheck.state IN ('waiting','publishing')
		AND operation.input_snapshot->>'group_id'=?`, commit.GroupID.String()).Scan(ctx, &rows); err != nil {
		return false, uuid.Nil, uuid.Nil, fmt.Errorf("apply automatic Local fallback: read existing rechecks: %w", err)
	}
	found := false
	var localReleaseID, recheckID uuid.UUID
	for _, row := range rows {
		var stored automaticLocalFallbackOperationSnapshot
		if err := json.Unmarshal(row.Snapshot, &stored); err != nil ||
			stored.SchemaVersion != 1 || stored.GroupID != commit.GroupID ||
			stored.OutageRunID != commit.OutageRunID ||
			stored.ObservedRequestGeneration != commit.ObservedRequestGeneration ||
			stored.ProviderReleaseID != commit.ProviderReleaseID ||
			stored.DecisionDigest != decisionDigest ||
			stored.PolicyVersion != MatchingLayoutPolicyVersion || found {
			return false, uuid.Nil, uuid.Nil, fmt.Errorf("apply automatic Local fallback: existing recheck is not an exact replay: %w", ErrIncomingMatchConflict)
		}
		parsedReleaseID, err := uuid.Parse(row.LocalReleaseIDText)
		if err != nil || parsedReleaseID == uuid.Nil {
			return false, uuid.Nil, uuid.Nil, fmt.Errorf("apply automatic Local fallback: existing recheck release identity is invalid: %w", ErrIncomingMatchConflict)
		}
		found = true
		localReleaseID, recheckID = parsedReleaseID, row.RecheckID
	}
	return found, localReleaseID, recheckID, nil
}

func automaticLocalFallbackTargetRevisions(layoutTarget json.RawMessage) (string, int64, error) {
	canonical, err := canonicalMatchingJSON(layoutTarget)
	if err != nil {
		return "", 0, fmt.Errorf("apply automatic Local fallback: canonicalize layout target: %w", err)
	}
	var evidence matchingLayoutTargetEvidence
	if err := json.Unmarshal(canonical, &evidence); err != nil ||
		evidence.SchemaVersion != matchingLayoutTargetEvidenceVersion ||
		evidence.Target.GroupRevision == "" || evidence.Target.AuthorityRevision < 0 {
		return "", 0, fmt.Errorf("apply automatic Local fallback: layout target is invalid: %w", ErrIncomingMatchConflict)
	}
	return evidence.Target.GroupRevision, evidence.Target.AuthorityRevision, nil
}

func automaticLocalFallbackDecisionDigest(layoutTarget json.RawMessage, providerReleaseID uuid.UUID, assignments []MatchingLocalAssignment) (string, error) {
	canonicalTarget, err := canonicalMatchingJSON(layoutTarget)
	if err != nil {
		return "", fmt.Errorf("apply automatic Local fallback: canonicalize layout target: %w", err)
	}
	encodedAssignments, err := json.Marshal(assignments)
	if err != nil {
		return "", fmt.Errorf("apply automatic Local fallback: encode assignments: %w", err)
	}
	canonicalAssignments, err := canonicalMatchingJSON(encodedAssignments)
	if err != nil {
		return "", fmt.Errorf("apply automatic Local fallback: canonicalize assignments: %w", err)
	}
	payload, err := json.Marshal(struct {
		LayoutTarget      json.RawMessage `json:"layout_target"`
		ProviderReleaseID uuid.UUID       `json:"provider_release_id"`
		Assignments       json.RawMessage `json:"assignments"`
		PolicyVersion     string          `json:"policy_version"`
	}{canonicalTarget, providerReleaseID, canonicalAssignments, MatchingLayoutPolicyVersion})
	if err != nil {
		return "", fmt.Errorf("apply automatic Local fallback: encode decision identity: %w", err)
	}
	canonical, err := canonicalMatchingJSON(payload)
	if err != nil {
		return "", fmt.Errorf("apply automatic Local fallback: canonicalize decision identity: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

type matchingLocalRecheckAdmission struct {
	OperationID       uuid.UUID
	ProviderReleaseID uuid.UUID
	LocalReleaseID    uuid.UUID
	// Captures are the exact current incoming grouping captures that backed the
	// layout. They are re-read below and only the authoritative persisted values
	// are written to the snapshot.
	Captures []IncomingGroupingCapture
	// Sources identifies the exact materialized association and library
	// position for every (file member, media variant) pair.
	Sources []matchingLocalMaterializedSource
}

// matchingLocalRecheckSnapshot is the immutable version-1 incumbent evidence.
// It is a witness, not editable recheck progress: the migration guard rejects
// any later change to this column.
type matchingLocalRecheckSnapshot struct {
	Version               int                                  `json:"version"`
	LocalApplyOperationID uuid.UUID                            `json:"local_apply_operation_id"`
	ProviderReleaseID     uuid.UUID                            `json:"provider_release_id"`
	LocalReleaseID        uuid.UUID                            `json:"local_release_id"`
	PolicyVersion         string                               `json:"policy_version"`
	Sources               []matchingLocalRecheckSnapshotSource `json:"sources"`
}

// matchingLocalRecheckSnapshotSource pins one exact physical capture to the
// association the automatic layout materialized. Several captures may share a
// media variant and therefore the same track_source row; each capture still
// gets its own immutable entry.
type matchingLocalRecheckSnapshotSource struct {
	WorkID              uuid.UUID               `json:"work_id"`
	SourceLocationID    uuid.UUID               `json:"source_location_id"`
	SourceRootID        uuid.UUID               `json:"source_root_id"`
	MediaVariantID      uuid.UUID               `json:"media_variant_id"`
	ProviderTrackID     uuid.UUID               `json:"provider_track_id"`
	ReleaseTrackID      uuid.UUID               `json:"release_track_id"`
	LocalTrackID        uuid.UUID               `json:"local_track_id"`
	TrackSourceID       uuid.UUID               `json:"track_source_id"`
	TrackSourceSelected bool                    `json:"track_source_selected"`
	TrackSourceInserted bool                    `json:"track_source_inserted"`
	TrackSourceMetadata *uuid.UUID              `json:"track_source_metadata_result_id"`
	Capture             IncomingGroupingCapture `json:"capture"`
	TagEncoding         json.RawMessage         `json:"tag_encoding"`
}

type matchingLocalRecheckCaptureIdentity struct {
	VariantID      uuid.UUID
	WorkID         uuid.UUID
	RootID         uuid.UUID
	LocationID     uuid.UUID
	MediaVariantID uuid.UUID
}

type matchingLocalRecheckSourceIdentity struct {
	FileMemberID   uuid.UUID
	MediaVariantID uuid.UUID
}

type matchingLocalRecheckValidatedSource struct {
	providerTrackID uuid.UUID
	releaseTrackID  uuid.UUID
	localTrackID    uuid.UUID
	trackSourceID   uuid.UUID
	selected        bool
	inserted        bool
	metadataResult  *uuid.UUID
}

type matchingLocalRecheckChild struct {
	workID           uuid.UUID
	sourceLocationID uuid.UUID
	sourceRootID     uuid.UUID
	mediaVariantID   uuid.UUID
	unlinkTarget     string
}

// createMatchingLocalRecheckTx writes the durable source veto for one automatic
// Local layout. It persists evidence only; it never schedules, enqueues or
// otherwise activates a recheck.
//
// The caller must already hold the output admission gate, the source mutation
// gate and the provider/catalog locks. The supplied captures must be the
// current prevalidated matching captures; this function re-reads them by
// location and refuses any scope or fact drift.
func createMatchingLocalRecheckTx(ctx context.Context, tx bun.Tx, input matchingLocalRecheckAdmission) (uuid.UUID, error) {
	if err := ctx.Err(); err != nil {
		return uuid.Nil, err
	}
	if input.OperationID == uuid.Nil || input.ProviderReleaseID == uuid.Nil || input.LocalReleaseID == uuid.Nil {
		return uuid.Nil, errors.New("create Local recheck: operation and release identities are required")
	}
	if len(input.Captures) == 0 || len(input.Sources) == 0 {
		return uuid.Nil, errors.New("create Local recheck: sources and captures are required")
	}

	sourceByKey := make(map[matchingLocalRecheckSourceIdentity]matchingLocalMaterializedSource, len(input.Sources))
	for _, source := range input.Sources {
		if source.FileMemberID == uuid.Nil || source.MediaVariantID == uuid.Nil || source.ProviderTrackID == uuid.Nil ||
			source.LocalTrackID == uuid.Nil || source.ReleaseTrackID == uuid.Nil || source.TrackSourceID == uuid.Nil {
			return uuid.Nil, errors.New("create Local recheck: materialized source identity is invalid")
		}
		key := matchingLocalRecheckSourceIdentity{FileMemberID: source.FileMemberID, MediaVariantID: source.MediaVariantID}
		if _, exists := sourceByKey[key]; exists {
			return uuid.Nil, errors.New("create Local recheck: materialized sources are ambiguous")
		}
		sourceByKey[key] = source
	}

	locationIDs := make([]uuid.UUID, 0, len(input.Captures))
	seenLocations := make(map[uuid.UUID]bool, len(input.Captures))
	requested := make(map[matchingLocalRecheckCaptureIdentity]IncomingGroupingCapture, len(input.Captures))
	for _, capture := range input.Captures {
		identity, ok := matchingLocalRecheckCaptureIdentityOf(capture)
		if !ok {
			return uuid.Nil, fmt.Errorf("create Local recheck: capture identity is incomplete: %w", ErrIncomingMatchConflict)
		}
		if _, exists := requested[identity]; exists {
			return uuid.Nil, fmt.Errorf("create Local recheck: capture scope is ambiguous: %w", ErrIncomingMatchConflict)
		}
		requested[identity] = capture
		if !seenLocations[capture.LocationID] {
			seenLocations[capture.LocationID] = true
			locationIDs = append(locationIDs, capture.LocationID)
		}
	}

	loaded, err := loadIncomingGroupingCapturesForLocations(ctx, tx, locationIDs)
	if err != nil {
		return uuid.Nil, fmt.Errorf("create Local recheck: read current captures: %w", err)
	}
	if len(loaded) == 0 {
		return uuid.Nil, fmt.Errorf("create Local recheck: current capture scope is empty: %w", ErrIncomingMatchConflict)
	}
	authoritative := make(map[matchingLocalRecheckCaptureIdentity]IncomingGroupingCapture, len(loaded))
	for _, capture := range loaded {
		identity, ok := matchingLocalRecheckCaptureIdentityOf(capture)
		if !ok {
			continue
		}
		if _, exists := authoritative[identity]; exists {
			return uuid.Nil, fmt.Errorf("create Local recheck: current capture scope is ambiguous: %w", ErrIncomingMatchConflict)
		}
		authoritative[identity] = capture
	}
	if err := validateMatchingLocalRecheckReleaseBinding(ctx, tx, input.ProviderReleaseID, input.LocalReleaseID); err != nil {
		return uuid.Nil, err
	}
	validatedSources := make(map[matchingLocalRecheckSourceIdentity]matchingLocalRecheckValidatedSource, len(sourceByKey))
	for key, source := range sourceByKey {
		validated, err := validateMatchingLocalRecheckSourceBinding(ctx, tx, input.ProviderReleaseID, input.LocalReleaseID, source)
		if err != nil {
			return uuid.Nil, err
		}
		validatedSources[key] = validated
	}

	usedSources := make(map[matchingLocalRecheckSourceIdentity]bool, len(sourceByKey))
	children := make([]matchingLocalRecheckChild, 0, len(input.Captures))
	snapshotSources := make([]matchingLocalRecheckSnapshotSource, 0, len(input.Captures))
	for _, expected := range input.Captures {
		identity, ok := matchingLocalRecheckCaptureIdentityOf(expected)
		if !ok {
			return uuid.Nil, fmt.Errorf("create Local recheck: capture identity is incomplete: %w", ErrIncomingMatchConflict)
		}
		current, ok := authoritative[identity]
		if !ok || !matchingLocalRecheckCaptureEqual(expected, current) {
			return uuid.Nil, fmt.Errorf("create Local recheck: current capture facts changed: %w", ErrIncomingMatchConflict)
		}
		sourceKey := matchingLocalRecheckSourceIdentity{
			FileMemberID:   current.VariantID,
			MediaVariantID: *current.MediaVariantID,
		}
		validated, ok := validatedSources[sourceKey]
		if !ok {
			return uuid.Nil, fmt.Errorf("create Local recheck: capture has no materialized source: %w", ErrIncomingMatchConflict)
		}
		usedSources[sourceKey] = true

		unlinkTarget, err := matchingLocalRecheckUnlinkTarget(current.ConfiguredPath, current.RelativePath)
		if err != nil {
			return uuid.Nil, fmt.Errorf("create Local recheck: resolve unlink target: %w", err)
		}
		if unlinkTarget == "" {
			return uuid.Nil, errors.New("create Local recheck: unlink target is empty")
		}
		owned, err := matchingLocalRecheckCleanupOwnerExists(ctx, tx, current.WorkID, current.LocationID, unlinkTarget)
		if err != nil {
			return uuid.Nil, fmt.Errorf("create Local recheck: check cleanup owner: %w", err)
		}
		if owned {
			return uuid.Nil, fmt.Errorf("create Local recheck: source is owned by cleanup: %w", ErrIncomingMatchConflict)
		}
		var completed bool
		if err := tx.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM source_cleanup_completion
			WHERE work_id=? AND source_root_id=? AND source_location_id=? AND media_variant_id=?
		)`, current.WorkID, current.RootID, current.LocationID, *current.MediaVariantID).Scan(ctx, &completed); err != nil {
			return uuid.Nil, fmt.Errorf("create Local recheck: check completed cleanup: %w", err)
		}
		if completed {
			return uuid.Nil, fmt.Errorf("create Local recheck: source cleanup already completed: %w", ErrIncomingMatchConflict)
		}

		children = append(children, matchingLocalRecheckChild{
			workID:           current.WorkID,
			sourceLocationID: current.LocationID,
			sourceRootID:     current.RootID,
			mediaVariantID:   *current.MediaVariantID,
			unlinkTarget:     unlinkTarget,
		})
		snapshotSources = append(snapshotSources, matchingLocalRecheckSnapshotSource{
			WorkID:              current.WorkID,
			SourceLocationID:    current.LocationID,
			SourceRootID:        current.RootID,
			MediaVariantID:      *current.MediaVariantID,
			ProviderTrackID:     validated.providerTrackID,
			ReleaseTrackID:      validated.releaseTrackID,
			LocalTrackID:        validated.localTrackID,
			TrackSourceID:       validated.trackSourceID,
			TrackSourceSelected: validated.selected,
			TrackSourceInserted: validated.inserted,
			TrackSourceMetadata: validated.metadataResult,
			Capture:             current,
			TagEncoding:         append(json.RawMessage(nil), current.TagEncoding...),
		})
	}
	for key := range sourceByKey {
		if !usedSources[key] {
			return uuid.Nil, fmt.Errorf("create Local recheck: materialized source has no current capture: %w", ErrIncomingMatchConflict)
		}
	}

	snapshot := matchingLocalRecheckSnapshot{
		Version:               1,
		LocalApplyOperationID: input.OperationID,
		ProviderReleaseID:     input.ProviderReleaseID,
		LocalReleaseID:        input.LocalReleaseID,
		PolicyVersion:         MatchingLayoutPolicyVersion,
		Sources:               snapshotSources,
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return uuid.Nil, fmt.Errorf("create Local recheck: encode incumbent snapshot: %w", err)
	}
	recheckID := uuid.New()
	if _, err := tx.NewRaw(`INSERT INTO matching_local_recheck
		(id,local_apply_operation_id,state,next_recheck_at,incumbent_snapshot)
		VALUES (?,?,'waiting',now(),?::jsonb)`, recheckID, input.OperationID, string(encoded)).Exec(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("create Local recheck: insert incumbent: %w", err)
	}
	for _, child := range children {
		if _, err := tx.NewRaw(`INSERT INTO matching_local_recheck_source
			(recheck_id,work_id,source_location_id,source_root_id,media_variant_id,unlink_target)
			VALUES (?,?,?,?,?,?)`,
			recheckID, child.workID, child.sourceLocationID, child.sourceRootID, child.mediaVariantID, child.unlinkTarget).Exec(ctx); err != nil {
			return uuid.Nil, fmt.Errorf("create Local recheck: insert source hold: %w", err)
		}
	}
	return recheckID, nil
}

func validateMatchingLocalRecheckReleaseBinding(ctx context.Context, tx bun.Tx, providerReleaseID, localReleaseID uuid.UUID) error {
	var boundReleaseID uuid.UUID
	err := tx.NewRaw(`SELECT local_release_id FROM provider_local_release WHERE provider_id=? AND provider_release_id=?`,
		localProviderID, providerReleaseID).Scan(ctx, &boundReleaseID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("create Local recheck: Local provider release binding is missing: %w", ErrIncomingMatchConflict)
	}
	if err != nil {
		return fmt.Errorf("create Local recheck: read Local provider release binding: %w", err)
	}
	if boundReleaseID != localReleaseID {
		return fmt.Errorf("create Local recheck: Local provider release binding changed: %w", ErrIncomingMatchConflict)
	}
	return nil
}

// validateMatchingLocalRecheckSourceBinding requires the exact Local provider
// tracks, library position and automatic association. It deliberately accepts a
// manual provider binding: manual Local edition creation must not hide the
// automatic association this recheck owns.
func validateMatchingLocalRecheckSourceBinding(ctx context.Context, tx bun.Tx, providerReleaseID, localReleaseID uuid.UUID, source matchingLocalMaterializedSource) (matchingLocalRecheckValidatedSource, error) {
	result := matchingLocalRecheckValidatedSource{
		providerTrackID: source.ProviderTrackID,
		releaseTrackID:  source.ReleaseTrackID,
		localTrackID:    source.LocalTrackID,
		trackSourceID:   source.TrackSourceID,
		inserted:        source.Inserted,
	}
	var providerTrack struct {
		ReleaseID uuid.UUID `bun:"release_id"`
		Medium    int       `bun:"medium"`
		Position  int       `bun:"position"`
	}
	err := tx.NewRaw(`SELECT release_id,medium,position FROM provider_release_track
		WHERE provider_id=? AND id=? AND present IS TRUE`, localProviderID, source.ProviderTrackID).Scan(ctx, &providerTrack)
	if errors.Is(err, sql.ErrNoRows) {
		return result, fmt.Errorf("create Local recheck: chosen provider release track is missing: %w", ErrIncomingMatchConflict)
	}
	if err != nil {
		return result, fmt.Errorf("create Local recheck: read chosen provider release track: %w", err)
	}
	if providerTrack.ReleaseID != providerReleaseID {
		return result, fmt.Errorf("create Local recheck: chosen provider release track changed release: %w", ErrIncomingMatchConflict)
	}

	var boundTrackID uuid.UUID
	err = tx.NewRaw(`SELECT local_track_id FROM provider_local_track WHERE provider_id=? AND provider_track_id=?`,
		localProviderID, source.ProviderTrackID).Scan(ctx, &boundTrackID)
	if errors.Is(err, sql.ErrNoRows) {
		return result, fmt.Errorf("create Local recheck: Local provider track binding is missing: %w", ErrIncomingMatchConflict)
	}
	if err != nil {
		return result, fmt.Errorf("create Local recheck: read Local provider track binding: %w", err)
	}
	if boundTrackID != source.LocalTrackID {
		return result, fmt.Errorf("create Local recheck: Local provider track binding changed: %w", ErrIncomingMatchConflict)
	}

	var position struct {
		ReleaseID uuid.UUID `bun:"release_id"`
		TrackID   uuid.UUID `bun:"track_id"`
		Medium    int       `bun:"medium"`
		Position  int       `bun:"position"`
	}
	err = tx.NewRaw(`SELECT release_id,track_id,medium,position FROM release_track WHERE id=?`, source.ReleaseTrackID).Scan(ctx, &position)
	if errors.Is(err, sql.ErrNoRows) {
		return result, fmt.Errorf("create Local recheck: library position is missing: %w", ErrIncomingMatchConflict)
	}
	if err != nil {
		return result, fmt.Errorf("create Local recheck: read library position: %w", err)
	}
	if position.ReleaseID != localReleaseID || position.TrackID != source.LocalTrackID ||
		position.Medium != providerTrack.Medium || position.Position != providerTrack.Position {
		return result, fmt.Errorf("create Local recheck: library position changed: %w", ErrIncomingMatchConflict)
	}

	var association struct {
		TrackID                uuid.UUID  `bun:"track_id"`
		MediaVariantID         uuid.UUID  `bun:"media_variant_id"`
		Selected               bool       `bun:"selected"`
		Compatible             bool       `bun:"compatible"`
		DecisionMethod         string     `bun:"decision_method"`
		PolicyVersion          *string    `bun:"policy_version"`
		SourceMetadataResultID *uuid.UUID `bun:"source_metadata_result_id"`
	}
	err = tx.NewRaw(`SELECT track_id,media_variant_id,selected,compatible,decision_method,policy_version,source_metadata_result_id
		FROM track_source WHERE id=? FOR UPDATE`, source.TrackSourceID).Scan(ctx, &association)
	if errors.Is(err, sql.ErrNoRows) {
		return result, fmt.Errorf("create Local recheck: track source association is missing: %w", ErrIncomingMatchConflict)
	}
	if err != nil {
		return result, fmt.Errorf("create Local recheck: read track source association: %w", err)
	}
	if association.TrackID != source.LocalTrackID || association.MediaVariantID != source.MediaVariantID ||
		!association.Compatible || association.DecisionMethod != "automatic" ||
		association.PolicyVersion == nil || *association.PolicyVersion != MatchingLayoutPolicyVersion {
		return result, fmt.Errorf("create Local recheck: track source association changed: %w", ErrIncomingMatchConflict)
	}
	result.selected = association.Selected
	if association.SourceMetadataResultID != nil {
		metadataResult := *association.SourceMetadataResultID
		result.metadataResult = &metadataResult
	}
	return result, nil
}

func matchingLocalRecheckCaptureIdentityOf(capture IncomingGroupingCapture) (matchingLocalRecheckCaptureIdentity, bool) {
	if !capture.Ready || !capture.RootEnabled || capture.RootStale || capture.CaptureAnalysisID == nil ||
		*capture.CaptureAnalysisID == uuid.Nil || capture.VariantID == uuid.Nil || capture.WorkID == uuid.Nil || capture.RootID == uuid.Nil ||
		capture.LocationID == uuid.Nil || capture.MediaVariantID == nil || *capture.MediaVariantID == uuid.Nil {
		return matchingLocalRecheckCaptureIdentity{}, false
	}
	return matchingLocalRecheckCaptureIdentity{
		VariantID:      capture.VariantID,
		WorkID:         capture.WorkID,
		RootID:         capture.RootID,
		LocationID:     capture.LocationID,
		MediaVariantID: *capture.MediaVariantID,
	}, true
}

func matchingLocalRecheckCaptureEqual(left, right IncomingGroupingCapture) bool {
	leftJSON, err := json.Marshal(left)
	if err != nil {
		return false
	}
	rightJSON, err := json.Marshal(right)
	if err != nil {
		return false
	}
	return sameJSONValue(leftJSON, rightJSON) && matchingLocalRecheckJSONEqual(left.TagEncoding, right.TagEncoding)
}

func matchingLocalRecheckJSONEqual(left, right json.RawMessage) bool {
	if len(left) == 0 || len(right) == 0 {
		return len(left) == 0 && len(right) == 0
	}
	return sameJSONValue(left, right)
}
