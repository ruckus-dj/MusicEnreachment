package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type incomingGroupingStore interface {
	State(context.Context) (persistence.IncomingGroupingState, error)
	CurrentCaptures(context.Context) ([]persistence.IncomingGroupingCapture, error)
	List(context.Context) ([]persistence.IncomingGroupingGroup, error)
	Refresh(context.Context, persistence.IncomingGroupingCompute) error
	Confirm(context.Context, int64, persistence.IncomingGroupingCompute) error
}

type IncomingGroupingSnapshot struct {
	Epoch  int64
	Groups []IncomingGroup
	Files  []IncomingFile
}

// IncomingGroups provides stateless previews and persisted refresh/confirmation.
// Draft operations are always calculated from a caller's snapshot; this service
// never writes page drafts.
type IncomingGroups struct{ store incomingGroupingStore }

func NewIncomingGroups(store incomingGroupingStore) *IncomingGroups {
	return &IncomingGroups{store: store}
}

func (groups *IncomingGroups) Snapshot(ctx context.Context) (IncomingGroupingSnapshot, error) {
	if groups == nil || groups.store == nil {
		return IncomingGroupingSnapshot{}, fmt.Errorf("incoming grouping store is unavailable")
	}
	state, err := groups.store.State(ctx)
	if err != nil {
		return IncomingGroupingSnapshot{}, err
	}
	if state.NeedsRefresh {
		if err := groups.Refresh(ctx); err != nil {
			return IncomingGroupingSnapshot{}, err
		}
		state, err = groups.store.State(ctx)
		if err != nil {
			return IncomingGroupingSnapshot{}, err
		}
	}
	captures, err := groups.store.CurrentCaptures(ctx)
	if err != nil {
		return IncomingGroupingSnapshot{}, err
	}
	persisted, err := groups.store.List(ctx)
	if err != nil {
		return IncomingGroupingSnapshot{}, err
	}
	view, err := incomingFilesFromCaptures(captures)
	if err != nil {
		return IncomingGroupingSnapshot{}, err
	}
	current, err := reconcileIncomingGroups(view.Files, view.Locations, view.Ready, persisted)
	if err != nil {
		return IncomingGroupingSnapshot{}, err
	}
	return IncomingGroupingSnapshot{Epoch: state.Revision, Groups: visibleIncomingGroups(current), Files: view.Files}, nil
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
		merged, err := reconcileIncomingGroups(view.Files, view.Locations, view.Ready, persisted)
		if err != nil {
			return nil, err
		}
		return persistableIncomingGroups(merged, view.Locations, view.Files)
	})
}

func DraftIncomingMergePage(snapshot IncomingGroupingSnapshot, ids ...uuid.UUID) (IncomingGroupDraft, error) {
	base, err := IncomingGroupsRevision(snapshot.Groups)
	if err != nil {
		return IncomingGroupDraft{}, err
	}
	return DraftIncomingMerge(snapshot.Groups, base, ids...)
}

func DraftIncomingSplitPage(snapshot IncomingGroupingSnapshot, groupID uuid.UUID, members []uuid.UUID) (IncomingGroupDraft, error) {
	base, err := IncomingGroupsRevision(snapshot.Groups)
	if err != nil {
		return IncomingGroupDraft{}, err
	}
	return DraftIncomingSplit(snapshot.Groups, base, groupID, members)
}

func DraftIncomingMovePage(snapshot IncomingGroupingSnapshot, members []uuid.UUID, target uuid.UUID) (IncomingGroupDraft, error) {
	base, err := IncomingGroupsRevision(snapshot.Groups)
	if err != nil {
		return IncomingGroupDraft{}, err
	}
	return DraftIncomingMove(snapshot.Groups, base, members, target)
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
		current, err := reconcileIncomingGroups(view.Files, view.Locations, view.Ready, persisted)
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
		return persistableIncomingGroups(confirmed, view.Locations, view.Files)
	})
}

type incomingCaptureView struct {
	Files     []IncomingFile
	Locations map[uuid.UUID][]IncomingLocation
	Ready     map[uuid.UUID]bool
}

func incomingFilesFromCaptures(captures []persistence.IncomingGroupingCapture) (incomingCaptureView, error) {
	filesByID := make(map[uuid.UUID]*IncomingFile)
	locations := make(map[uuid.UUID][]IncomingLocation)
	ready := make(map[uuid.UUID]bool)
	for _, capture := range captures {
		technicalIdentity, err := incomingTechnicalIdentity(capture)
		if err != nil {
			return incomingCaptureView{}, err
		}
		location := IncomingLocation{
			RootID: capture.RootID, WorkID: capture.WorkID, LocationID: capture.LocationID,
			ConfiguredPath: capture.ConfiguredPath, InventoryPath: capture.InventoryPath,
			TechnicalIdentity: technicalIdentity,
			RelativePath:      capture.RelativePath, SizeBytes: capture.SizeBytes, Mtime: normalizedIncomingMtime(capture.Mtime),
		}
		locations[capture.VariantID] = append(locations[capture.VariantID], location)
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
		if file == nil {
			file = &IncomingFile{VariantID: capture.VariantID, CaptureAnalysisID: *capture.CaptureAnalysisID, CaptureIdentity: identity, Tags: tags}
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
	return incomingCaptureView{Files: files, Locations: locations, Ready: ready}, nil
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

func reconcileIncomingGroups(files []IncomingFile, physical map[uuid.UUID][]IncomingLocation, ready map[uuid.UUID]bool, persisted []persistence.IncomingGroupingGroup) ([]IncomingGroup, error) {
	derived, err := GroupIncomingFiles(files)
	if err != nil {
		return nil, err
	}
	filesByID := incomingFilesByVariant(files)
	occupied := make(map[uuid.UUID]bool)
	manual := make([]IncomingGroup, 0)
	for _, group := range persisted {
		if !group.Manual {
			continue
		}
		kept := make([]uuid.UUID, 0, len(group.Members))
		unready := make([]uuid.UUID, 0, len(group.UnreadyMembers))
		allMembers := append(append([]uuid.UUID(nil), group.Members...), group.UnreadyMembers...)
		for _, member := range allMembers {
			currentLocations, current := physical[member]
			if !current || !hasSurvivingLocation(member, group.Locations, currentLocations) {
				continue
			}
			if ready[member] {
				if _, hasReadyCapture := filesByID[member]; hasReadyCapture {
					kept = append(kept, member)
					occupied[member] = true
					continue
				}
			}
			unready = append(unready, member)
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
		for _, member := range group.Members {
			if !occupied[member] {
				kept = append(kept, member)
			}
		}
		if len(kept) > 0 {
			group.Members = kept
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
