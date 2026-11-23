package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/tagencoding"
)

var ErrIncomingGroupingInvalid = errors.New("incoming grouping correction is invalid")

type incomingGroupingStore interface {
	State(context.Context) (persistence.IncomingGroupingState, error)
	ReadVisibleCount(context.Context) (int, bool, error)
	ReadPartition(context.Context) (persistence.IncomingGroupingPartition, error)
	Refresh(context.Context, persistence.IncomingGroupingCompute) error
	Confirm(context.Context, int64, persistence.IncomingGroupingCompute) error
}

type IncomingGroupingSnapshot struct {
	Epoch  int64
	Groups []IncomingGroup
	Files  []IncomingFile
}

// IncomingGroupingAction is a client-requested edit, replayed against the
// server's current partition for both preview and confirmation.
type IncomingGroupingAction struct {
	Kind          string
	GroupIDs      []uuid.UUID
	GroupID       uuid.UUID
	MemberIDs     []uuid.UUID
	TargetGroupID uuid.UUID
}

// IncomingGroups provides stateless previews and persisted refresh/confirmation.
// Draft operations are always calculated from a caller's snapshot; this service
// never writes page drafts.
type IncomingGroups struct{ store incomingGroupingStore }

func NewIncomingGroups(store incomingGroupingStore) *IncomingGroups {
	return &IncomingGroups{store: store}
}

func (groups *IncomingGroups) Snapshot(ctx context.Context) (IncomingGroupingSnapshot, error) {
	return groups.snapshot(ctx, true)
}

// Count returns the number of visible groups without refreshing or writing the
// stored partition. When invalidated, use the same reconciliation as the list.
func (groups *IncomingGroups) Count(ctx context.Context) (int, error) {
	if groups == nil || groups.store == nil {
		return 0, fmt.Errorf("incoming grouping store is unavailable")
	}
	count, current, err := groups.store.ReadVisibleCount(ctx)
	if err != nil || current {
		return count, err
	}
	partition, err := groups.store.ReadPartition(ctx)
	if err != nil {
		return 0, err
	}
	snapshot, err := buildIncomingSnapshot(partition.State.Revision, partition.Captures, partition.Groups)
	if err != nil {
		return 0, err
	}
	return len(snapshot.Groups), nil
}

func (groups *IncomingGroups) snapshot(ctx context.Context, refresh bool) (IncomingGroupingSnapshot, error) {
	if groups == nil || groups.store == nil {
		return IncomingGroupingSnapshot{}, fmt.Errorf("incoming grouping store is unavailable")
	}
	partition, err := groups.store.ReadPartition(ctx)
	if err != nil {
		return IncomingGroupingSnapshot{}, err
	}
	if partition.State.NeedsRefresh {
		if !refresh {
			return IncomingGroupingSnapshot{}, ErrMatchingEvaluationDeferred
		}
		if err := groups.Refresh(ctx); err != nil {
			return IncomingGroupingSnapshot{}, err
		}
		partition, err = groups.store.ReadPartition(ctx)
		if err != nil {
			return IncomingGroupingSnapshot{}, err
		}
	}
	return buildIncomingSnapshot(partition.State.Revision, partition.Captures, partition.Groups)
}

func buildIncomingSnapshot(epoch int64, captures []persistence.IncomingGroupingCapture, persisted []persistence.IncomingGroupingGroup) (IncomingGroupingSnapshot, error) {
	view, err := incomingFilesFromCaptures(captures)
	if err != nil {
		return IncomingGroupingSnapshot{}, err
	}
	current, err := reconcileActiveIncomingGroups(view, persisted)
	if err != nil {
		return IncomingGroupingSnapshot{}, err
	}
	groups := visibleIncomingGroups(current)
	files := slices.DeleteFunc(view.Files, func(file IncomingFile) bool { return view.Dormant[file.VariantID] })
	return IncomingGroupingSnapshot{Epoch: epoch, Groups: groups, Files: files}, nil
}

// Refresh recomputes automatic groups from persisted current inventory and
// analysis. Manual membership is retained only for files with a surviving exact
// location/stat identity; new files stay in automatic groups.
func (groups *IncomingGroups) Refresh(ctx context.Context) error {
	if groups == nil || groups.store == nil {
		return fmt.Errorf("incoming grouping store is unavailable")
	}
	return groups.store.Refresh(ctx, func(captures []persistence.IncomingGroupingCapture, persisted []persistence.IncomingGroupingGroup) ([]persistence.IncomingGroupingGroup, error) {
		view, err := incomingFilesFromCaptures(captures)
		if err != nil {
			return nil, err
		}
		merged, err := reconcileIncomingGroups(view, persisted)
		if err != nil {
			return nil, err
		}
		return persistableIncomingGroups(merged, view.Locations, view.Files)
	})
}

// RefreshIfNeeded completes a durable invalidation without doing any source IO.
func (groups *IncomingGroups) RefreshIfNeeded(ctx context.Context) error {
	if groups == nil || groups.store == nil {
		return fmt.Errorf("incoming grouping store is unavailable")
	}
	state, err := groups.store.State(ctx)
	if err != nil {
		return err
	}
	if !state.NeedsRefresh {
		return nil
	}
	return groups.Refresh(ctx)
}

func ReplayIncomingGroupingActions(groups []IncomingGroup, baseRevision string, actions []IncomingGroupingAction) (IncomingGroupDraft, error) {
	if err := validateIncomingGroupsRevision(groups, baseRevision); err != nil {
		return IncomingGroupDraft{}, persistence.ErrIncomingGroupingConflict
	}
	current := cloneIncomingGroups(groups)
	for _, action := range actions {
		revision, err := IncomingGroupsRevision(current)
		if err != nil {
			return IncomingGroupDraft{}, err
		}
		var draft IncomingGroupDraft
		switch action.Kind {
		case "merge":
			draft, err = DraftIncomingMerge(current, revision, action.GroupIDs...)
		case "split":
			draft, err = DraftIncomingSplit(current, revision, action.GroupID, action.MemberIDs)
		case "move":
			draft, err = DraftIncomingMove(current, revision, action.MemberIDs, action.TargetGroupID)
		default:
			return IncomingGroupDraft{}, ErrIncomingGroupingInvalid
		}
		if err != nil {
			if errors.Is(err, persistence.ErrIncomingGroupingConflict) {
				return IncomingGroupDraft{}, err
			}
			return IncomingGroupDraft{}, fmt.Errorf("%w: %v", ErrIncomingGroupingInvalid, err)
		}
		current = draft.Groups
	}
	return IncomingGroupDraft{BaseRevision: baseRevision, Groups: current}, nil
}

// Confirm applies a stateless page draft only if its base and draft revisions
// still match a fresh database snapshot acquired under source/grouping locks.
func (groups *IncomingGroups) Confirm(ctx context.Context, epoch int64, draft IncomingGroupDraft, baseRevision, draftRevision string) error {
	if groups == nil || groups.store == nil {
		return fmt.Errorf("incoming grouping store is unavailable")
	}
	return groups.store.Confirm(ctx, epoch, func(captures []persistence.IncomingGroupingCapture, persisted []persistence.IncomingGroupingGroup) ([]persistence.IncomingGroupingGroup, error) {
		view, err := incomingFilesFromCaptures(captures)
		if err != nil {
			return nil, err
		}
		current, err := reconcileActiveIncomingGroups(view, persisted)
		if err != nil {
			return nil, err
		}
		visible := visibleIncomingGroups(current)
		actualBase, err := IncomingGroupsRevision(visible)
		if err != nil {
			return nil, err
		}
		if baseRevision == "" || draft.BaseRevision != baseRevision || actualBase != baseRevision {
			return nil, persistence.ErrIncomingGroupingConflict
		}
		actualDraft, err := IncomingGroupsRevision(draft.Groups)
		if err != nil {
			return nil, err
		}
		if draftRevision == "" || actualDraft != draftRevision {
			return nil, persistence.ErrIncomingGroupingConflict
		}
		if err := validateIncomingPartition(visible, draft.Groups); err != nil {
			return nil, err
		}
		confirmed, err := applyIncomingDraft(visible, draft.Groups)
		if err != nil {
			return nil, err
		}
		confirmed = retainUnreadyMembers(current, confirmed)
		confirmed = retainDormantMembers(persisted, view, confirmed)
		return persistableIncomingGroups(confirmed, view.Locations, view.Files)
	})
}

func (groups *IncomingGroups) ConfirmActions(ctx context.Context, epoch int64, baseRevision, draftRevision string, actions []IncomingGroupingAction) error {
	if groups == nil || groups.store == nil {
		return fmt.Errorf("incoming grouping store is unavailable")
	}
	return groups.store.Confirm(ctx, epoch, func(captures []persistence.IncomingGroupingCapture, persisted []persistence.IncomingGroupingGroup) ([]persistence.IncomingGroupingGroup, error) {
		view, err := incomingFilesFromCaptures(captures)
		if err != nil {
			return nil, err
		}
		current, err := reconcileActiveIncomingGroups(view, persisted)
		if err != nil {
			return nil, err
		}
		visible := visibleIncomingGroups(current)
		draft, err := ReplayIncomingGroupingActions(visible, baseRevision, actions)
		if err != nil {
			return nil, err
		}
		actualDraft, err := IncomingGroupsRevision(draft.Groups)
		if err != nil {
			return nil, err
		}
		if draftRevision == "" || actualDraft != draftRevision {
			return nil, persistence.ErrIncomingGroupingConflict
		}
		if err := validateIncomingPartition(visible, draft.Groups); err != nil {
			return nil, err
		}
		confirmed, err := applyIncomingDraft(visible, draft.Groups)
		if err != nil {
			return nil, err
		}
		confirmed = retainUnreadyMembers(current, confirmed)
		confirmed = retainDormantMembers(persisted, view, confirmed)
		return persistableIncomingGroups(confirmed, view.Locations, view.Files)
	})
}

type incomingCaptureView struct {
	Files     []IncomingFile
	Locations map[uuid.UUID][]IncomingLocation
	Ready     map[uuid.UUID]bool
	Blocked   map[uuid.UUID]bool
	// Dormant marks members whose canonical media identity already has a live
	// track_source association. The raw partition keeps them and their exact
	// location fences; only the active read projection hides them.
	Dormant map[uuid.UUID]bool
}

type incomingAnalysisFolder struct {
	root uuid.UUID
	dir  string
}

func incomingFilesFromCaptures(captures []persistence.IncomingGroupingCapture) (incomingCaptureView, error) {
	filesByID := make(map[uuid.UUID]*IncomingFile)
	locations := make(map[uuid.UUID][]IncomingLocation)
	ready := make(map[uuid.UUID]bool)
	dormant := make(map[uuid.UUID]bool)
	pendingFolders := make(map[incomingAnalysisFolder]bool)
	currentFolders := make(map[uuid.UUID][]incomingAnalysisFolder)
	for _, capture := range captures {
		technicalIdentity, err := incomingTechnicalIdentity(capture)
		if err != nil {
			return incomingCaptureView{}, err
		}
		if capture.Linked {
			dormant[capture.VariantID] = true
		}
		location := IncomingLocation{
			RootID: capture.RootID, WorkID: capture.WorkID, LocationID: capture.LocationID,
			ConfiguredPath: capture.ConfiguredPath, InventoryPath: capture.InventoryPath,
			TechnicalIdentity: technicalIdentity,
			RelativePath:      capture.RelativePath, SizeBytes: capture.SizeBytes, Mtime: normalizedIncomingMtime(capture.Mtime),
		}
		locations[capture.VariantID] = append(locations[capture.VariantID], location)
		if capture.RootEnabled && !capture.RootStale {
			folder := incomingAnalysisFolder{root: capture.RootID, dir: path.Dir(capture.RelativePath)}
			currentFolders[capture.VariantID] = append(currentFolders[capture.VariantID], folder)
			if (!capture.Ready || capture.CaptureAnalysisID == nil) && !capture.NonAudioAnalysisSettled {
				pendingFolders[folder] = true
			}
		}
		if !capture.Ready || capture.CaptureAnalysisID == nil {
			continue
		}
		file := filesByID[capture.VariantID]
		var tags map[string][]string
		if err := json.Unmarshal(capture.Tags, &tags); err != nil {
			return incomingCaptureView{}, fmt.Errorf("decode captured incoming tags: %w", err)
		}
		identity, err := incomingCaptureIdentity(capture)
		if err != nil {
			return incomingCaptureView{}, err
		}
		displayTags, correction := incomingTagDisplay(tags, capture.TagEncoding)
		if file == nil {
			file = &IncomingFile{VariantID: capture.VariantID, CaptureAnalysisID: *capture.CaptureAnalysisID, CaptureIdentity: identity, Tags: tags, DisplayTags: displayTags, TagCorrection: correction}
			filesByID[capture.VariantID] = file
		} else if file.CaptureAnalysisID != *capture.CaptureAnalysisID || file.CaptureIdentity != identity {
			return incomingCaptureView{}, fmt.Errorf("incoming variant has conflicting selected metadata captures")
		}
		file.Locations = append(file.Locations, location)
		ready[capture.VariantID] = true
	}
	files := make([]IncomingFile, 0, len(filesByID))
	for variantID, file := range filesByID {
		file.Locations = uniqueIncomingLocations(locations[variantID])
		files = append(files, *file)
	}
	for variant := range locations {
		locations[variant] = uniqueIncomingLocations(locations[variant])
	}
	sort.Slice(files, func(i, j int) bool { return files[i].VariantID.String() < files[j].VariantID.String() })
	blocked := make(map[uuid.UUID]bool)
	for member, folders := range currentFolders {
		for _, folder := range folders {
			if pendingFolders[folder] {
				blocked[member] = true
				break
			}
		}
	}
	return incomingCaptureView{Files: files, Locations: locations, Ready: ready, Blocked: blocked, Dormant: dormant}, nil
}

// Membership is projected after applying the same grouping and folder
// readiness rules as refresh. A saved link must not synthesize a new group ID
// or bypass a readiness block that the persisted matching target still sees.
func reconcileActiveIncomingGroups(view incomingCaptureView, persisted []persistence.IncomingGroupingGroup) ([]IncomingGroup, error) {
	current, err := reconcileIncomingGroups(view, persisted)
	if err != nil || len(view.Dormant) == 0 {
		return current, err
	}
	isDormant := func(member uuid.UUID) bool { return view.Dormant[member] }
	for i := range current {
		group := &current[i]
		readyCount := len(group.Members)
		group.Members = slices.DeleteFunc(group.Members, isDormant)
		group.UnreadyMembers = slices.DeleteFunc(group.UnreadyMembers, isDormant)
		group.Revision = persistence.IncomingActiveRevision(group.Revision, readyCount, group.Members)
	}
	trimIncomingDiagnostics(current)
	return current, nil
}

// trimIncomingDiagnostics keeps only diagnostics belonging to the group's
// current members, so a dormant member does not leak through a manual group's
// stored diagnostics.
func trimIncomingDiagnostics(groups []IncomingGroup) {
	for i := range groups {
		members := make(map[uuid.UUID]bool, len(groups[i].Members)+len(groups[i].UnreadyMembers))
		for _, member := range groups[i].Members {
			members[member] = true
		}
		for _, member := range groups[i].UnreadyMembers {
			members[member] = true
		}
		kept := groups[i].Diagnostics[:0]
		for _, diagnostic := range groups[i].Diagnostics {
			if members[diagnostic.VariantID] {
				kept = append(kept, diagnostic)
			}
		}
		if len(kept) == 0 {
			groups[i].Diagnostics = nil
		} else {
			groups[i].Diagnostics = kept
		}
	}
}

// incomingTagDisplay projects the saved automatic tag-encoding corrections onto
// the raw captured tags for presentation. Tags are never modified, and the
// projection does not participate in grouping or revisions. A missing report,
// an unsupported schema, or one that no longer matches the captured values
// falls back to the raw text rather than failing the read-only snapshot.
func incomingTagDisplay(tags map[string][]string, encoded json.RawMessage) (map[string][]string, *IncomingTagCorrection) {
	if len(tags) == 0 || len(encoded) == 0 || string(encoded) == "null" {
		return nil, nil
	}
	var report tagencoding.Report
	if err := json.Unmarshal(encoded, &report); err != nil || report.Version != tagencoding.ReportVersion {
		return nil, nil
	}
	automatic, candidates := len(report.Automatic.Entries), len(report.Candidates)
	if automatic == 0 && candidates == 0 {
		return nil, nil
	}
	correction := &IncomingTagCorrection{Automatic: automatic, Candidates: candidates}
	if automatic == 0 {
		return nil, correction
	}
	display, err := report.Automatic.Apply(tags)
	if err != nil {
		return nil, nil
	}
	return display, correction
}

func incomingCaptureIdentity(capture persistence.IncomingGroupingCapture) (string, error) {
	identity := struct {
		MetadataWinner *uuid.UUID      `json:"metadata_winner"`
		ObservedAt     string          `json:"observed_at"`
		Provenance     json.RawMessage `json:"provenance"`
		Tags           json.RawMessage `json:"tags"`
		SHA256         []byte          `json:"sha256,omitempty"`
	}{capture.MetadataWinner, incomingTimeValue(capture.MetadataObservedAt), capture.MetadataProvenance, capture.Tags, capture.SourceSHA256}
	encoded, err := json.Marshal(identity)
	if err != nil {
		return "", fmt.Errorf("encode incoming capture identity: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func incomingTechnicalIdentity(capture persistence.IncomingGroupingCapture) (string, error) {
	identity := struct {
		Probe              json.RawMessage `json:"probe"`
		ProbeWinner        *uuid.UUID      `json:"probe_winner,omitempty"`
		ProbeVersion       *string         `json:"probe_version,omitempty"`
		ProbePolicy        *int            `json:"probe_policy,omitempty"`
		ProbeInspectedAt   string          `json:"probe_inspected_at,omitempty"`
		Fingerprint        *string         `json:"fingerprint,omitempty"`
		FingerprintID      *uuid.UUID      `json:"fingerprint_winner,omitempty"`
		Duration           *float64        `json:"fingerprint_duration,omitempty"`
		Version            *string         `json:"fingerprint_version,omitempty"`
		Banner             *string         `json:"fingerprint_banner,omitempty"`
		AlgorithmNamespace *string         `json:"fingerprint_algorithm_namespace,omitempty"`
		AlgorithmID        *int16          `json:"fingerprint_algorithm_id,omitempty"`
		CalculatedAt       string          `json:"fingerprint_calculated_at,omitempty"`
		ParserVersion      *int            `json:"fingerprint_parser_version,omitempty"`
	}{capture.ProbeResult, capture.ProbeWinner, capture.ProbeVersion, capture.ProbePolicy, incomingTimeValue(capture.ProbeInspectedAt),
		capture.Fingerprint, capture.FingerprintWinner, capture.FingerprintDuration, capture.FingerprintVersion, capture.FingerprintBanner,
		capture.FingerprintAlgorithmNamespace, capture.FingerprintAlgorithmID, incomingTimeValue(capture.FingerprintCalculatedAt), capture.FingerprintParserVersion}
	encoded, err := json.Marshal(identity)
	if err != nil {
		return "", fmt.Errorf("encode incoming technical identity: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func incomingTimeValue(value *time.Time) string {
	if value == nil {
		return ""
	}
	return normalizedIncomingMtime(*value)
}

func normalizedIncomingMtime(value time.Time) string {
	return value.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano)
}

func reconcileIncomingGroups(view incomingCaptureView, persisted []persistence.IncomingGroupingGroup) ([]IncomingGroup, error) {
	derived, err := GroupIncomingFiles(view.Files)
	if err != nil {
		return nil, err
	}
	filesByID := incomingFilesByVariant(view.Files)
	occupied := make(map[uuid.UUID]bool)
	manual := make([]IncomingGroup, 0)
	for _, group := range persisted {
		if !group.Manual {
			continue
		}
		kept := make([]uuid.UUID, 0, len(group.Members))
		unready := make([]uuid.UUID, 0, len(group.UnreadyMembers))
		allMembers := append(append([]uuid.UUID(nil), group.Members...), group.UnreadyMembers...)
		blocked := false
		for _, member := range allMembers {
			currentLocations, current := view.Locations[member]
			if !current || !hasSurvivingLocation(member, group.Locations, currentLocations) {
				continue
			}
			blocked = blocked || view.Blocked[member]
			if view.Ready[member] {
				if _, hasReadyCapture := filesByID[member]; hasReadyCapture {
					kept = append(kept, member)
					occupied[member] = true
					continue
				}
			}
			unready = append(unready, member)
		}
		if blocked {
			unready = append(unready, kept...)
			kept = nil
		}
		if len(kept) == 0 && len(unready) == 0 {
			continue
		}
		revision, err := incomingGroupRevision(kept, filesByID)
		if err != nil {
			if len(kept) > 0 {
				return nil, err
			}
			revision = group.Revision
		}
		diagnostics := make([]IncomingGroupingDiagnostic, 0)
		_ = json.Unmarshal(group.Diagnostics, &diagnostics)
		manual = append(manual, IncomingGroup{ID: group.ID, Revision: revision, Manual: true, Members: kept, UnreadyMembers: unready, Diagnostics: diagnostics})
	}
	filtered := make([]IncomingGroup, 0, len(derived))
	for _, group := range derived {
		kept := make([]uuid.UUID, 0, len(group.Members))
		blocked := false
		for _, member := range group.Members {
			if !occupied[member] {
				kept = append(kept, member)
				blocked = blocked || view.Blocked[member]
			}
		}
		if len(kept) > 0 && !blocked {
			group.Members = kept
			group.Revision, err = incomingGroupRevision(kept, filesByID)
			if err != nil {
				return nil, err
			}
			filtered = append(filtered, group)
		}
	}
	return append(filtered, manual...), nil
}

func visibleIncomingGroups(groups []IncomingGroup) []IncomingGroup {
	visible := make([]IncomingGroup, 0, len(groups))
	for _, group := range groups {
		if len(group.Members) > 0 {
			visible = append(visible, group)
		}
	}
	return visible
}

func retainUnreadyMembers(previous, proposed []IncomingGroup) []IncomingGroup {
	byID := make(map[uuid.UUID]IncomingGroup, len(proposed))
	for _, group := range proposed {
		byID[group.ID] = group
	}
	for _, prior := range previous {
		if len(prior.UnreadyMembers) == 0 {
			continue
		}
		if group, exists := byID[prior.ID]; exists {
			group.UnreadyMembers = append(group.UnreadyMembers, prior.UnreadyMembers...)
			byID[prior.ID] = group
			continue
		}
		byID[prior.ID] = IncomingGroup{ID: prior.ID, Revision: prior.Revision, Manual: true, UnreadyMembers: append([]uuid.UUID(nil), prior.UnreadyMembers...), Diagnostics: prior.Diagnostics}
	}
	result := make([]IncomingGroup, 0, len(byID))
	for _, group := range byID {
		result = append(result, group)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID.String() < result[j].ID.String() })
	return result
}

// retainDormantMembers re-attaches raw membership hidden by a live
// track_source association to the group that owns it. A confirmed draft only
// edits active membership, so dormant members must never be deleted or moved
// merely because their association exists. When the active part of a group was
// reassigned or merged, its dormant members retain the original group identity
// and become invisible until their source identity is unlinked again.
func retainDormantMembers(raw []persistence.IncomingGroupingGroup, view incomingCaptureView, confirmed []IncomingGroup) []IncomingGroup {
	if len(view.Dormant) == 0 {
		return confirmed
	}
	byID := make(map[uuid.UUID]IncomingGroup, len(confirmed))
	order := make([]uuid.UUID, 0, len(confirmed))
	for _, group := range confirmed {
		byID[group.ID] = group
		order = append(order, group.ID)
	}
	for _, prior := range raw {
		var ready, unready []uuid.UUID
		for _, member := range prior.Members {
			if view.Dormant[member] && hasSurvivingLocation(member, prior.Locations, view.Locations[member]) {
				ready = append(ready, member)
			}
		}
		for _, member := range prior.UnreadyMembers {
			if view.Dormant[member] && hasSurvivingLocation(member, prior.Locations, view.Locations[member]) {
				unready = append(unready, member)
			}
		}
		if len(ready) == 0 && len(unready) == 0 {
			continue
		}
		var priorDiagnostics []IncomingGroupingDiagnostic
		_ = json.Unmarshal(prior.Diagnostics, &priorDiagnostics)
		diagnostics := dormantIncomingDiagnostics(priorDiagnostics, view.Dormant)
		group, exists := byID[prior.ID]
		if exists {
			group.Members = uniqueUUIDs(append(append([]uuid.UUID(nil), group.Members...), ready...))
			group.UnreadyMembers = uniqueUUIDs(append(append([]uuid.UUID(nil), group.UnreadyMembers...), unready...))
			sort.Slice(group.Members, func(i, j int) bool { return group.Members[i].String() < group.Members[j].String() })
			sort.Slice(group.UnreadyMembers, func(i, j int) bool { return group.UnreadyMembers[i].String() < group.UnreadyMembers[j].String() })
			group.Diagnostics = append(group.Diagnostics, diagnostics...)
			byID[prior.ID] = group
			continue
		}
		byID[prior.ID] = IncomingGroup{
			ID:             prior.ID,
			Revision:       prior.Revision,
			Manual:         prior.Manual,
			Members:        ready,
			UnreadyMembers: unready,
			Diagnostics:    diagnostics,
		}
		order = append(order, prior.ID)
	}
	result := make([]IncomingGroup, 0, len(byID))
	for _, id := range order {
		result = append(result, byID[id])
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID.String() < result[j].ID.String() })
	return result
}

func dormantIncomingDiagnostics(diagnostics []IncomingGroupingDiagnostic, dormant map[uuid.UUID]bool) []IncomingGroupingDiagnostic {
	var kept []IncomingGroupingDiagnostic
	for _, diagnostic := range diagnostics {
		if dormant[diagnostic.VariantID] {
			kept = append(kept, diagnostic)
		}
	}
	return kept
}

func hasSurvivingLocation(member uuid.UUID, old []persistence.IncomingGroupingMemberLocation, current []IncomingLocation) bool {
	for _, prior := range old {
		if prior.VariantID != member {
			continue
		}
		for _, location := range current {
			if prior.RootID == location.RootID && prior.ConfiguredPath == location.ConfiguredPath && prior.InventoryPath == location.InventoryPath && prior.RelativePath == location.RelativePath && prior.SizeBytes == location.SizeBytes && normalizedIncomingMtime(prior.Mtime) == location.Mtime {
				return true
			}
		}
	}
	return false
}

func incomingMemberLocations(member uuid.UUID, locations []IncomingLocation) []persistence.IncomingGroupingMemberLocation {
	result := make([]persistence.IncomingGroupingMemberLocation, 0, len(locations))
	for _, location := range locations {
		stamp, _ := time.Parse(time.RFC3339Nano, location.Mtime)
		result = append(result, persistence.IncomingGroupingMemberLocation{
			VariantID: member, RootID: location.RootID, WorkID: location.WorkID, LocationID: location.LocationID,
			ConfiguredPath: location.ConfiguredPath, InventoryPath: location.InventoryPath,
			RelativePath: location.RelativePath, SizeBytes: location.SizeBytes, Mtime: stamp,
		})
	}
	return result
}

func validateIncomingPartition(current, proposed []IncomingGroup) error {
	want := make(map[uuid.UUID]struct{})
	for _, group := range current {
		for _, member := range group.Members {
			if member == uuid.Nil {
				return fmt.Errorf("current incoming grouping contains an invalid member")
			}
			want[member] = struct{}{}
		}
	}
	got := make(map[uuid.UUID]struct{}, len(want))
	for _, group := range proposed {
		if len(group.Members) == 0 {
			return fmt.Errorf("incoming group draft contains an empty group")
		}
		for _, member := range group.Members {
			if _, exists := want[member]; !exists {
				return fmt.Errorf("incoming group draft contains a new member")
			}
			if _, duplicate := got[member]; duplicate {
				return fmt.Errorf("incoming group draft duplicates a member")
			}
			got[member] = struct{}{}
		}
	}
	if len(got) != len(want) {
		return fmt.Errorf("incoming group draft omits current members")
	}
	return nil
}

func applyIncomingDraft(current, proposed []IncomingGroup) ([]IncomingGroup, error) {
	oldByMembers := make(map[string]IncomingGroup, len(current))
	for _, group := range current {
		oldByMembers[stringsJoinUUIDs(group.Members)] = group
	}
	result := cloneIncomingGroups(proposed)
	for i := range result {
		result[i].UnreadyMembers = nil
		if old, unchanged := oldByMembers[stringsJoinUUIDs(result[i].Members)]; unchanged {
			result[i].ID = old.ID
			result[i].Revision = old.Revision
			result[i].Manual = old.Manual
			continue
		}
		id, err := uuid.NewRandom()
		if err != nil {
			return nil, fmt.Errorf("create confirmed manual group identity: %w", err)
		}
		result[i].ID = id
		result[i].Manual = true
	}
	return result, nil
}

func stringsJoinUUIDs(ids []uuid.UUID) string { return fmt.Sprint(uuidStrings(ids)) }

func persistableIncomingGroups(groups []IncomingGroup, physical map[uuid.UUID][]IncomingLocation, files []IncomingFile) ([]persistence.IncomingGroupingGroup, error) {
	filesByID := incomingFilesByVariant(files)
	result := make([]persistence.IncomingGroupingGroup, 0, len(groups))
	for _, group := range groups {
		revision := group.Revision
		if len(group.Members) > 0 {
			var err error
			revision, err = incomingGroupRevision(group.Members, filesByID)
			if err != nil {
				return nil, err
			}
		}
		diagnostics, err := json.Marshal(group.Diagnostics)
		if err != nil {
			return nil, fmt.Errorf("encode incoming diagnostics: %w", err)
		}
		locations := make([]persistence.IncomingGroupingMemberLocation, 0)
		allMembers := append(append([]uuid.UUID(nil), group.Members...), group.UnreadyMembers...)
		for _, member := range allMembers {
			locations = append(locations, incomingMemberLocations(member, physical[member])...)
		}
		result = append(result, persistence.IncomingGroupingGroup{
			ID: group.ID, Manual: group.Manual, Revision: revision,
			Members: append([]uuid.UUID(nil), group.Members...), UnreadyMembers: append([]uuid.UUID(nil), group.UnreadyMembers...),
			Diagnostics: diagnostics, Locations: locations,
		})
	}
	return result, nil
}
