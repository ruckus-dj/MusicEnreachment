package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/ruckus/MusicEnreachment/backend/internal/tagencoding"
)

var (
	// ErrIncomingTagEncodingUnavailable means the group is not a currently
	// visible incoming group with at least one ready member.
	ErrIncomingTagEncodingUnavailable = errors.New("incoming group tag encoding is unavailable")
	// ErrIncomingTagEncodingConflict means the caller reviewed a different
	// grouping generation, group revision, capture set, or correction set.
	ErrIncomingTagEncodingConflict = errors.New("incoming group tag encoding changed")
	// ErrIncomingTagEncodingInvalid means the requested correction cannot be
	// expressed against the reviewed report.
	ErrIncomingTagEncodingInvalid = errors.New("incoming group tag encoding correction is invalid")
)

// IncomingTagEncodingMember is one ready group member with its selected
// metadata capture. Report is the effective report: the persisted
// tag_encoding.Report when present, or the pure Analyze(raw tags) preview when
// the capture has never been reviewed. Candidates are therefore visible even
// when detection found no automatic repair and persisted NULL.
type IncomingTagEncodingMember struct {
	VariantID        uuid.UUID
	MetadataResultID uuid.UUID
	Tags             map[string][]string
	Stored           bool
	Revision         string
	Report           tagencoding.Report

	// storedText retains the exact persisted JSON for the compare-and-swap
	// guard. It is empty when the capture has no persisted report.
	storedText string
	winner     uuid.UUID
}

// IncomingTagEncodingGroup is the review snapshot for one visible incoming
// group. Generation is the incoming grouping state revision and GroupRevision
// is the materialized group revision; both fence a later confirmation.
type IncomingTagEncodingGroup struct {
	GroupID            uuid.UUID
	Generation         int64
	GroupRevision      string
	Digest             string
	CorrectionRevision string
	Members            []IncomingTagEncodingMember
}

// IncomingTagEncodingCorrection is one member's explicit decision.
//
//   - An empty CandidateIndexes on a not-yet-stored capture with deterministic
//     automatic repairs explicitly saves the pure preview report. An already
//     stored capture, or a capture with no automatic detection, is a no-op.
//   - Non-empty CandidateIndexes confirms those pending candidates and keeps
//     the remaining candidates pending.
//   - Undo requires a saved repair: it clears the effective automatic entries,
//     keeps unresolved candidates, and leaves a non-NULL report that suppresses
//     automatic re-detection. Undo is not offered for a pure preview.
type IncomingTagEncodingCorrection struct {
	VariantID        uuid.UUID
	MetadataResultID uuid.UUID
	CandidateIndexes []int
	Undo             bool
}

// IncomingTagEncodingConfirmation is the whole-group compare-and-swap request
// shown by Preview.
type IncomingTagEncodingConfirmation struct {
	Generation         int64
	GroupRevision      string
	Digest             string
	CorrectionRevision string
	Members            []IncomingTagEncodingCorrection
}

// IncomingTagEncodingRepository reviews and records per-capture tag encoding
// corrections. It never rewrites observed_tags and never touches matching or
// display state; the effective projection is a separate reader.
type IncomingTagEncodingRepository struct{ db *bun.DB }

func NewIncomingTagEncodingRepository(db *bun.DB) *IncomingTagEncodingRepository {
	return &IncomingTagEncodingRepository{db: db}
}

// Preview reads the exact current group membership and selected analysis
// winners in one repeatable-read transaction. It performs no source IO.
func (repository *IncomingTagEncodingRepository) Preview(ctx context.Context, groupID uuid.UUID) (IncomingTagEncodingGroup, error) {
	var group IncomingTagEncodingGroup
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		loaded, err := loadIncomingTagEncodingGroup(ctx, tx, groupID)
		if err != nil {
			return err
		}
		group = loaded
		return nil
	})
	if err != nil {
		return IncomingTagEncodingGroup{}, err
	}
	return group, nil
}

// Confirm applies one transactional whole-group correction. It serializes with
// every publisher, selector, and retirer of digest-keyed fingerprint and
// metadata results, reloads the latest group/capture scope, and refuses a
// stale generation, group revision, digest, or correction revision.
func (repository *IncomingTagEncodingRepository) Confirm(ctx context.Context, groupID uuid.UUID, confirmation IncomingTagEncodingConfirmation) (IncomingTagEncodingGroup, error) {
	var group IncomingTagEncodingGroup
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("lock canonical source mutation gate: %w", err)
		}
		current, err := loadIncomingTagEncodingGroup(ctx, tx, groupID)
		if err != nil {
			return err
		}
		if current.Generation != confirmation.Generation ||
			current.GroupRevision != confirmation.GroupRevision ||
			current.Digest != confirmation.Digest ||
			current.CorrectionRevision != confirmation.CorrectionRevision {
			return ErrIncomingTagEncodingConflict
		}
		if err := applyIncomingTagEncodingConfirmations(ctx, tx, current, confirmation.Members); err != nil {
			return err
		}
		updated, err := loadIncomingTagEncodingGroup(ctx, tx, groupID)
		if err != nil {
			return err
		}
		group = updated
		return nil
	})
	if err != nil {
		return IncomingTagEncodingGroup{}, err
	}
	return group, nil
}

func loadIncomingTagEncodingGroup(ctx context.Context, database bun.IDB, groupID uuid.UUID) (IncomingTagEncodingGroup, error) {
	if groupID == uuid.Nil {
		return IncomingTagEncodingGroup{}, ErrIncomingTagEncodingUnavailable
	}
	var generation int64
	var needsRefresh bool
	if err := database.NewRaw(`SELECT revision, needs_refresh FROM incoming_grouping_state WHERE singleton=true`).Scan(ctx, &generation, &needsRefresh); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return IncomingTagEncodingGroup{}, ErrIncomingTagEncodingConflict
		}
		return IncomingTagEncodingGroup{}, fmt.Errorf("read incoming grouping state for tag encoding: %w", err)
	}
	if needsRefresh {
		return IncomingTagEncodingGroup{}, ErrIncomingTagEncodingConflict
	}

	var groupRevision string
	if err := database.NewRaw(`SELECT revision FROM incoming_group WHERE id=?`, groupID).Scan(ctx, &groupRevision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return IncomingTagEncodingGroup{}, ErrIncomingTagEncodingUnavailable
		}
		return IncomingTagEncodingGroup{}, fmt.Errorf("read incoming group for tag encoding: %w", err)
	}

	activity, err := ReadIncomingMembershipActivity(ctx, database, groupID)
	if err != nil {
		return IncomingTagEncodingGroup{}, err
	}
	memberIDs := activity.ActiveReady
	memberSet := make(map[uuid.UUID]struct{}, len(memberIDs))
	for _, member := range memberIDs {
		memberSet[member] = struct{}{}
	}
	if len(memberIDs) == 0 {
		return IncomingTagEncodingGroup{}, ErrIncomingTagEncodingUnavailable
	}
	groupRevision = IncomingActiveRevision(groupRevision, activity.ReadyCount, memberIDs)

	locationIDs := make([]uuid.UUID, 0, len(activity.ActiveLocations))
	for _, location := range activity.ActiveLocations {
		if _, ready := memberSet[location.VariantID]; ready {
			locationIDs = append(locationIDs, location.LocationID)
		}
	}
	if len(locationIDs) == 0 {
		return IncomingTagEncodingGroup{}, ErrIncomingTagEncodingConflict
	}
	captures, err := loadIncomingGroupingCapturesForLocations(ctx, database, locationIDs)
	if err != nil {
		return IncomingTagEncodingGroup{}, err
	}

	captureByVariant := make(map[uuid.UUID]IncomingGroupingCapture, len(memberIDs))
	for _, capture := range captures {
		if _, member := memberSet[capture.VariantID]; !member || !capture.Ready ||
			capture.CaptureAnalysisID == nil || capture.MetadataWinner == nil || *capture.MetadataWinner == uuid.Nil {
			continue
		}
		existing, exists := captureByVariant[capture.VariantID]
		if !exists {
			captureByVariant[capture.VariantID] = capture
			continue
		}
		if *existing.CaptureAnalysisID != *capture.CaptureAnalysisID ||
			*existing.MetadataWinner != *capture.MetadataWinner {
			return IncomingTagEncodingGroup{}, ErrIncomingTagEncodingConflict
		}
	}

	resultIDs := make([]uuid.UUID, 0, len(memberIDs))
	for _, memberID := range memberIDs {
		capture, exists := captureByVariant[memberID]
		if !exists {
			return IncomingTagEncodingGroup{}, ErrIncomingTagEncodingConflict
		}
		resultIDs = append(resultIDs, *capture.CaptureAnalysisID)
	}

	encodings := make(map[uuid.UUID]string, len(resultIDs))
	var encodingRows []struct {
		ID      uuid.UUID `bun:"id"`
		Encoded *string   `bun:"encoded"`
	}
	if err := database.NewRaw(`SELECT id, tag_encoding::text AS encoded FROM media_metadata_result WHERE id IN (?)`, bun.List(resultIDs)).Scan(ctx, &encodingRows); err != nil {
		return IncomingTagEncodingGroup{}, fmt.Errorf("read selected metadata tag encodings: %w", err)
	}
	for _, row := range encodingRows {
		if row.Encoded != nil {
			encodings[row.ID] = *row.Encoded
		}
	}

	group := IncomingTagEncodingGroup{
		GroupID:       groupID,
		Generation:    generation,
		GroupRevision: groupRevision,
		Members:       make([]IncomingTagEncodingMember, 0, len(memberIDs)),
	}
	for _, memberID := range memberIDs {
		capture := captureByVariant[memberID]
		var tags map[string][]string
		if err := json.Unmarshal(capture.Tags, &tags); err != nil {
			return IncomingTagEncodingGroup{}, fmt.Errorf("decode selected incoming tags: %w", err)
		}
		storedText, stored := encodings[*capture.CaptureAnalysisID]
		var storedTextPointer *string
		if stored {
			value := storedText
			storedTextPointer = &value
		}
		report, revision, err := incomingTagEncodingPreviewReport(tags, storedTextPointer)
		if err != nil {
			return IncomingTagEncodingGroup{}, err
		}
		group.Members = append(group.Members, IncomingTagEncodingMember{
			VariantID:        memberID,
			MetadataResultID: *capture.CaptureAnalysisID,
			Tags:             tags,
			Stored:           stored,
			Revision:         revision,
			Report:           report,
			storedText:       storedText,
			winner:           *capture.MetadataWinner,
		})
	}
	group.Digest, group.CorrectionRevision, err = incomingTagEncodingDigests(group.Members)
	if err != nil {
		return IncomingTagEncodingGroup{}, err
	}
	return group, nil
}

// incomingTagEncodingPreviewReport keeps a persisted report authoritative. A
// NULL capture is analyzed purely from its raw tags so candidate-only damage is
// surfaced before the first explicit confirmation.
func incomingTagEncodingPreviewReport(tags map[string][]string, storedText *string) (tagencoding.Report, string, error) {
	if storedText == nil {
		return normalizeIncomingTagEncodingReport(tagencoding.Analyze(tags)), "none", nil
	}
	var report tagencoding.Report
	if err := json.Unmarshal([]byte(*storedText), &report); err != nil {
		return tagencoding.Report{}, "", fmt.Errorf("decode stored tag encoding report: %w", err)
	}
	if report.Version != tagencoding.ReportVersion || report.Automatic.Version != tagencoding.OverlayVersion {
		return tagencoding.Report{}, "", fmt.Errorf("stored tag encoding report has an unsupported version")
	}
	report = normalizeIncomingTagEncodingReport(report)
	revision, err := incomingTagEncodingHash(report)
	if err != nil {
		return tagencoding.Report{}, "", err
	}
	return report, revision, nil
}

func normalizeIncomingTagEncodingReport(report tagencoding.Report) tagencoding.Report {
	if report.Version == 0 {
		report.Version = tagencoding.ReportVersion
	}
	if report.Automatic.Version == 0 {
		report.Automatic.Version = tagencoding.OverlayVersion
	}
	if report.Automatic.Entries == nil {
		report.Automatic.Entries = []tagencoding.Entry{}
	}
	if report.Candidates == nil {
		report.Candidates = []tagencoding.Candidate{}
	}
	return report
}

type incomingTagEncodingDigestMember struct {
	VariantID        string          `json:"variant_id"`
	MetadataResultID string          `json:"metadata_result_id"`
	WinningResultID  string          `json:"winning_result_id"`
	Tags             json.RawMessage `json:"tags"`
	Revision         string          `json:"revision"`
}

type incomingTagEncodingCorrectionDigestMember struct {
	VariantID        string `json:"variant_id"`
	MetadataResultID string `json:"metadata_result_id"`
	Revision         string `json:"revision"`
}

func incomingTagEncodingDigests(members []IncomingTagEncodingMember) (string, string, error) {
	digestMembers := make([]incomingTagEncodingDigestMember, 0, len(members))
	correctionMembers := make([]incomingTagEncodingCorrectionDigestMember, 0, len(members))
	for _, member := range members {
		encodedTags, err := json.Marshal(member.Tags)
		if err != nil {
			return "", "", fmt.Errorf("encode incoming tags for tag encoding digest: %w", err)
		}
		digestMembers = append(digestMembers, incomingTagEncodingDigestMember{
			VariantID:        member.VariantID.String(),
			MetadataResultID: member.MetadataResultID.String(),
			WinningResultID:  member.winner.String(),
			Tags:             encodedTags,
			Revision:         member.Revision,
		})
		correctionMembers = append(correctionMembers, incomingTagEncodingCorrectionDigestMember{
			VariantID:        member.VariantID.String(),
			MetadataResultID: member.MetadataResultID.String(),
			Revision:         member.Revision,
		})
	}
	digest, err := incomingTagEncodingHash(digestMembers)
	if err != nil {
		return "", "", err
	}
	correctionRevision, err := incomingTagEncodingHash(correctionMembers)
	if err != nil {
		return "", "", err
	}
	return digest, correctionRevision, nil
}

func incomingTagEncodingHash(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode tag encoding digest input: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func applyIncomingTagEncodingConfirmations(ctx context.Context, tx bun.IDB, group IncomingTagEncodingGroup, corrections []IncomingTagEncodingCorrection) error {
	byVariant := make(map[uuid.UUID]IncomingTagEncodingMember, len(group.Members))
	for _, member := range group.Members {
		byVariant[member.VariantID] = member
	}
	seen := make(map[uuid.UUID]struct{}, len(corrections))
	for _, correction := range corrections {
		if correction.VariantID == uuid.Nil {
			return fmt.Errorf("%w: member identity is required", ErrIncomingTagEncodingInvalid)
		}
		if _, duplicated := seen[correction.VariantID]; duplicated {
			return fmt.Errorf("%w: member is duplicated", ErrIncomingTagEncodingInvalid)
		}
		seen[correction.VariantID] = struct{}{}
		member, exists := byVariant[correction.VariantID]
		if !exists {
			return fmt.Errorf("%w: member is not in the group", ErrIncomingTagEncodingInvalid)
		}
		if member.MetadataResultID != correction.MetadataResultID {
			return ErrIncomingTagEncodingConflict
		}
		report, changed, err := correctedIncomingTagEncodingReport(member, correction)
		if err != nil {
			return err
		}
		if !changed {
			continue
		}
		encoded, err := json.Marshal(report)
		if err != nil {
			return fmt.Errorf("encode corrected tag encoding report: %w", err)
		}
		var result sql.Result
		if member.Stored {
			result, err = tx.NewRaw(`UPDATE media_metadata_result SET tag_encoding=?::jsonb
				WHERE id=? AND winning_result_id=? AND tag_encoding=?::jsonb`,
				string(encoded), member.MetadataResultID, member.winner, member.storedText).Exec(ctx)
		} else {
			result, err = tx.NewRaw(`UPDATE media_metadata_result SET tag_encoding=?::jsonb
				WHERE id=? AND winning_result_id=? AND tag_encoding IS NULL`,
				string(encoded), member.MetadataResultID, member.winner).Exec(ctx)
		}
		if err != nil {
			return fmt.Errorf("save corrected tag encoding report: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read corrected tag encoding result: %w", err)
		}
		if affected != 1 {
			return ErrIncomingTagEncodingConflict
		}
	}
	return nil
}

func correctedIncomingTagEncodingReport(member IncomingTagEncodingMember, correction IncomingTagEncodingCorrection) (tagencoding.Report, bool, error) {
	if correction.Undo {
		if len(correction.CandidateIndexes) != 0 {
			return tagencoding.Report{}, false, fmt.Errorf("%w: undo cannot confirm candidates", ErrIncomingTagEncodingInvalid)
		}
		// Undo only exists for a persisted repair. A NULL capture is a pure
		// preview and the UI does not offer undo for it.
		if !member.Stored {
			return tagencoding.Report{}, false, fmt.Errorf("%w: undo requires a saved repair", ErrIncomingTagEncodingInvalid)
		}
		if len(member.Report.Automatic.Entries) == 0 {
			return member.Report, false, nil
		}
		// Undo clears the effective repairs but retains unresolved candidates so
		// the user can still review them. The persisted report stays non-NULL,
		// which continues to suppress automatic re-detection.
		return tagencoding.Report{
			Version: tagencoding.ReportVersion,
			Automatic: tagencoding.Overlay{
				Version: tagencoding.OverlayVersion,
				Entries: []tagencoding.Entry{},
			},
			Candidates:   append([]tagencoding.Candidate{}, member.Report.Candidates...),
			UserRevision: uuid.NewString(),
		}, true, nil
	}
	if len(correction.CandidateIndexes) == 0 {
		// Zero indexes on a not-yet-stored capture with deterministic automatic
		// repairs is an explicit save of the existing observation. It turns the
		// pure preview into one persisted report without a mass backfill. An
		// already stored capture or a capture with no automatic detection is a
		// no-op and must not write.
		if member.Stored || len(member.Report.Automatic.Entries) == 0 {
			return member.Report, false, nil
		}
		report := member.Report
		report.UserRevision = uuid.NewString()
		return report, true, nil
	}

	type location struct {
		key   string
		index int
	}
	entries := append([]tagencoding.Entry(nil), member.Report.Automatic.Entries...)
	occupied := make(map[location]struct{}, len(entries))
	for _, entry := range entries {
		occupied[location{key: entry.Key, index: entry.Index}] = struct{}{}
	}
	selected := make(map[int]struct{}, len(correction.CandidateIndexes))
	for _, index := range correction.CandidateIndexes {
		if index < 0 || index >= len(member.Report.Candidates) {
			return tagencoding.Report{}, false, fmt.Errorf("%w: candidate index is out of range", ErrIncomingTagEncodingInvalid)
		}
		if _, duplicated := selected[index]; duplicated {
			return tagencoding.Report{}, false, fmt.Errorf("%w: candidate index is duplicated", ErrIncomingTagEncodingInvalid)
		}
		selected[index] = struct{}{}
		candidate := member.Report.Candidates[index]
		position := location{key: candidate.Key, index: candidate.Index}
		if _, exists := occupied[position]; exists {
			return tagencoding.Report{}, false, fmt.Errorf("%w: candidate is already repaired", ErrIncomingTagEncodingInvalid)
		}
		values, exists := member.Tags[candidate.Key]
		if !exists || candidate.Index < 0 || candidate.Index >= len(values) || values[candidate.Index] != candidate.Original {
			return tagencoding.Report{}, false, fmt.Errorf("%w: candidate no longer matches the saved capture", ErrIncomingTagEncodingInvalid)
		}
		occupied[position] = struct{}{}
		provenance := candidate.Provenance
		provenance.Decision = tagencoding.DecisionConfirmedCandidate
		entries = append(entries, tagencoding.Entry{
			Key:         candidate.Key,
			Index:       candidate.Index,
			Original:    candidate.Original,
			Replacement: candidate.Replacement,
			Provenance:  provenance,
		})
	}

	sort.SliceStable(entries, func(left, right int) bool {
		if entries[left].Key != entries[right].Key {
			return entries[left].Key < entries[right].Key
		}
		return entries[left].Index < entries[right].Index
	})
	candidates := make([]tagencoding.Candidate, 0, len(member.Report.Candidates)-len(selected))
	for index, candidate := range member.Report.Candidates {
		if _, confirmed := selected[index]; confirmed {
			continue
		}
		candidates = append(candidates, candidate)
	}
	return tagencoding.Report{
		Version: tagencoding.ReportVersion,
		Automatic: tagencoding.Overlay{
			Version: tagencoding.OverlayVersion,
			Entries: entries,
		},
		Candidates:   candidates,
		UserRevision: uuid.NewString(),
	}, true, nil
}
