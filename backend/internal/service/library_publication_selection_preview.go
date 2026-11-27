package service

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

// LibraryPublicationSelectionPreviewMaxPositions bounds one read-only preview
// request so a selection cannot multiply native CURRENT verification and
// namespace planning without limit.
const LibraryPublicationSelectionPreviewMaxPositions = 100

const (
	libraryPublicationSelectionPreviewWorkers           = 2
	libraryPublicationSelectionPreviewGlobalConcurrency = 4
	libraryPublicationSelectionPreviewTimeout           = 30 * time.Second
	// Cover preparation is an explicit side effect of a bulk preview. Its own
	// bounded budget keeps slow artwork work from consuming the audio planning
	// deadline; a partial progress survives and the caller can retry.
	libraryPublicationSelectionCoverPreparationTimeout = 20 * time.Second
)

// ErrLibraryPublicationSelectionPreviewInvalid marks a malformed selection
// preview request. It never carries internal planning detail.
var ErrLibraryPublicationSelectionPreviewInvalid = errors.New("library publication selection preview request is invalid")

var libraryPublicationSelectionPreviewSlots = make(chan struct{}, libraryPublicationSelectionPreviewGlobalConcurrency)

// Dispositions describe only what an admitted publication would do for one
// selected position.
const (
	LibraryPublicationSelectionDispositionCreate    = "create"
	LibraryPublicationSelectionDispositionUpdate    = "update"
	LibraryPublicationSelectionDispositionUnchanged = "unchanged"
	LibraryPublicationSelectionDispositionSkipped   = "skipped"
	LibraryPublicationSelectionDispositionBlocked   = "blocked"
	LibraryPublicationSelectionDispositionUnchecked = "unchecked"
)

// Reasons are typed, safe codes. They replace raw planner messages so the
// response never exposes filesystem or database detail.
const (
	LibraryPublicationSelectionReasonPositionNotFound      = "position_not_found"
	LibraryPublicationSelectionReasonForcedRequiresCurrent = "forced_requires_current"
	LibraryPublicationSelectionReasonAttemptActive         = "publication_attempt_active"
	LibraryPublicationSelectionReasonToolsUnavailable      = "managed_tools_unavailable"
	LibraryPublicationSelectionReasonSourceSelection       = "source_selection_required"
	LibraryPublicationSelectionReasonFormatUnavailable     = "publication_format_unavailable"
	LibraryPublicationSelectionReasonPathUnavailable       = "publication_path_unavailable"
	LibraryPublicationSelectionReasonResolvedPath          = "resolved_path_unavailable"
	LibraryPublicationSelectionReasonContextBlocked        = "publication_context_blocked"
	LibraryPublicationSelectionReasonNotEligible           = "source_republish_not_eligible"
	LibraryPublicationSelectionReasonContentUnchanged      = "content_unchanged"
	LibraryPublicationSelectionReasonAudioMissing          = "missing_audio"
	LibraryPublicationSelectionReasonDeadlineExceeded      = "preview_deadline_exceeded"
	LibraryPublicationSelectionReasonCanceled              = "preview_canceled"
	LibraryPublicationSelectionReasonFailed                = "preview_failed"
)

// LibraryPublicationSelectionPosition identifies one selected library position.
type LibraryPublicationSelectionPosition struct {
	ReleaseID  uuid.UUID
	PositionID uuid.UUID
}

type LibraryPublicationSelectionPreviewRequest struct {
	Mode      string
	Positions []LibraryPublicationSelectionPosition
	// PrepareCovers explicitly allows a POST preview to prepare absent
	// automatic first-publication cover authority once per release before
	// per-position planning. Read-only callers leave it false.
	PrepareCovers bool
}

type LibraryPublicationSelectionPreview struct {
	Mode  string
	Items []LibraryPublicationSelectionPreviewItem
}

// LibraryPublicationSelectionPreviewItem is a bounded, read-only disposition.
// It contains no publication token, admission authority, or absolute path.
type LibraryPublicationSelectionPreviewItem struct {
	ReleaseID          uuid.UUID
	PositionID         uuid.UUID
	Disposition        string
	Reason             string
	ResolvedPath       string
	PathClash          bool
	PathClashPositions []LibraryPublicationSelectionPreviewReference
}

type LibraryPublicationSelectionPreviewReference struct {
	ReleaseID  uuid.UUID
	PositionID uuid.UUID
}

// PreviewPublicationSelection plans each selected position against its own
// coherent context snapshot. It never admits, writes, or enqueues work.
func (service *LibraryPublications) PreviewPublicationSelection(ctx context.Context, request LibraryPublicationSelectionPreviewRequest) (LibraryPublicationSelectionPreview, error) {
	mode := request.Mode
	if mode == "" {
		mode = firstPublicationPreviewModeOrdinary
	}
	if mode != firstPublicationPreviewModeOrdinary && mode != firstPublicationPreviewModeForced {
		return LibraryPublicationSelectionPreview{}, fmt.Errorf("%w: mode must be ordinary or forced", ErrLibraryPublicationSelectionPreviewInvalid)
	}
	if len(request.Positions) == 0 || len(request.Positions) > LibraryPublicationSelectionPreviewMaxPositions {
		return LibraryPublicationSelectionPreview{}, fmt.Errorf("%w: positions must contain 1 to %d entries", ErrLibraryPublicationSelectionPreviewInvalid, LibraryPublicationSelectionPreviewMaxPositions)
	}
	seen := make(map[LibraryPublicationSelectionPosition]struct{}, len(request.Positions))
	for _, position := range request.Positions {
		if position.ReleaseID == uuid.Nil || position.PositionID == uuid.Nil {
			return LibraryPublicationSelectionPreview{}, fmt.Errorf("%w: positions must be nonzero", ErrLibraryPublicationSelectionPreviewInvalid)
		}
		if _, duplicate := seen[position]; duplicate {
			return LibraryPublicationSelectionPreview{}, fmt.Errorf("%w: positions must be distinct", ErrLibraryPublicationSelectionPreviewInvalid)
		}
		seen[position] = struct{}{}
	}
	if service == nil || service.repository == nil || service.settings == nil {
		return LibraryPublicationSelectionPreview{}, errors.New("library publication selection preview is unavailable")
	}
	repository, ok := service.repository.(firstPublicationContextReader)
	if !ok {
		return LibraryPublicationSelectionPreview{}, errors.New("first publication context is unavailable")
	}
	planningSettings, ok := service.settings.(firstPublicationFormatSettings)
	if !ok {
		return LibraryPublicationSelectionPreview{}, errors.New("first publication settings are unavailable")
	}
	// The whole preview shares one deadline, including the settings and tool
	// reads that precede per-position planning.
	planningContext, cancel := context.WithTimeout(ctx, libraryPublicationSelectionPreviewTimeout)
	defer cancel()

	runtimeSettings, err := planningSettings.ReadPublicationPlanningSettings(planningContext)
	if err != nil {
		return LibraryPublicationSelectionPreview{}, fmt.Errorf("read publication planning settings: %w", err)
	}
	// The preview helper treats an unavailable tool selection as a blocker, so
	// an error here still produces a safe per-position disposition.
	selectedTools, _ := service.selectedFFmpegPairForSettings(planningContext, runtimeSettings)

	// Preparation is explicit and only applies to ordinary selections. It runs
	// before any per-position planning snapshot so those snapshots see the cover
	// authority the preparation just froze. A pending provider answer is reported
	// to the caller instead of being converted into "no cover"; the retry can
	// resume from any originals already cached.
	if request.PrepareCovers && mode == firstPublicationPreviewModeOrdinary && service.artwork != nil {
		coverContext, cancelCover := context.WithTimeout(ctx, libraryPublicationSelectionCoverPreparationTimeout)
		err := service.prepareSelectionCovers(coverContext, request.Positions, repository)
		cancelCover()
		if err != nil {
			if ctx.Err() != nil {
				return LibraryPublicationSelectionPreview{}, ctx.Err()
			}
			return LibraryPublicationSelectionPreview{}, err
		}
	}

	items := make([]LibraryPublicationSelectionPreviewItem, len(request.Positions))
	indices := make(chan int)
	var workers sync.WaitGroup
	workerCount := libraryPublicationSelectionPreviewWorkers
	if len(request.Positions) < workerCount {
		workerCount = len(request.Positions)
	}
	for worker := 0; worker < workerCount; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range indices {
				items[index] = service.planSelectionPreviewPosition(ctx, planningContext, request.Positions[index], mode, repository, runtimeSettings, selectedTools)
			}
		}()
	}
dispatch:
	for index := range request.Positions {
		select {
		case indices <- index:
		case <-planningContext.Done():
			break dispatch
		}
	}
	close(indices)
	workers.Wait()

	// Positions that were never dispatched keep the zero disposition.
	for index := range items {
		if items[index].Disposition == "" {
			items[index] = selectionPreviewStatusItem(request.Positions[index], LibraryPublicationSelectionDispositionUnchecked, selectionPreviewContextReason(ctx, planningContext))
		}
	}
	markSelectionPreviewPathClashes(items)
	return LibraryPublicationSelectionPreview{Mode: mode, Items: items}, nil
}

// prepareSelectionCovers resolves the first eligible never-published position
// of each release and performs at most one artwork preparation per release.
// Pending or unavailable provider answers are returned so the caller can show a
// retryable warning; they are never treated as definitive cover absence. Only
// request cancellation aborts silently.
func (service *LibraryPublications) prepareSelectionCovers(ctx context.Context, positions []LibraryPublicationSelectionPosition, repository firstPublicationContextReader) error {
	prepared := make(map[uuid.UUID]struct{}, len(positions))
	for _, position := range positions {
		if _, done := prepared[position.ReleaseID]; done {
			continue
		}
		snapshot, err := repository.ReadLibraryFirstPublicationContext(ctx, position.ReleaseID, position.PositionID)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		if !firstPublicationCoverPreparationEligible(snapshot) {
			continue
		}
		prepared[position.ReleaseID] = struct{}{}
		if _, err := service.prepareFirstCover(ctx, position.ReleaseID, position.PositionID); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
	}
	return nil
}

func (service *LibraryPublications) planSelectionPreviewPosition(requestContext, ctx context.Context, position LibraryPublicationSelectionPosition, mode string, repository firstPublicationContextReader, runtimeSettings settings.RuntimeSettings, selectedTools []persistence.SourceAnalysisToolSelection) LibraryPublicationSelectionPreviewItem {
	if !acquireSelectionPreviewSlot(ctx) {
		return selectionPreviewStatusItem(position, LibraryPublicationSelectionDispositionUnchecked, selectionPreviewContextReason(requestContext, ctx))
	}
	defer releaseSelectionPreviewSlot()

	snapshot, err := repository.ReadLibraryFirstPublicationContext(ctx, position.ReleaseID, position.PositionID)
	if err != nil {
		if errors.Is(err, persistence.ErrPublicationPositionNotFound) {
			return selectionPreviewStatusItem(position, LibraryPublicationSelectionDispositionSkipped, LibraryPublicationSelectionReasonPositionNotFound)
		}
		return selectionPreviewStatusItem(position, LibraryPublicationSelectionDispositionUnchecked, selectionPreviewContextReason(requestContext, ctx))
	}
	if snapshot == nil || snapshot.Library == nil {
		return selectionPreviewStatusItem(position, LibraryPublicationSelectionDispositionUnchecked, LibraryPublicationSelectionReasonFailed)
	}
	if snapshot.Library.Current != nil {
		preview, previewErr := service.PreviewSourceRepublishRetainingSelection(ctx, position.ReleaseID, position.PositionID, mode)
		if previewErr != nil {
			if errors.Is(previewErr, persistence.ErrPublicationPositionNotFound) {
				return selectionPreviewStatusItem(position, LibraryPublicationSelectionDispositionSkipped, LibraryPublicationSelectionReasonPositionNotFound)
			}
			return selectionPreviewStatusItem(position, LibraryPublicationSelectionDispositionUnchecked, selectionPreviewContextReason(requestContext, ctx))
		}
		item := selectionPreviewItemFromRetainedPreview(position, preview)
		return confirmSelectionPreviewSource(requestContext, ctx, position, snapshot, preview.SelectedSource, item)
	}
	if mode == firstPublicationPreviewModeForced {
		return selectionPreviewStatusItem(position, LibraryPublicationSelectionDispositionSkipped, LibraryPublicationSelectionReasonForcedRequiresCurrent)
	}
	preview, err := service.previewFirstCaptured(ctx, snapshot, runtimeSettings, selectedTools, position.ReleaseID, position.PositionID, nil, firstPublicationPreviewModeOrdinary)
	if err != nil {
		return selectionPreviewStatusItem(position, LibraryPublicationSelectionDispositionUnchecked, selectionPreviewContextReason(requestContext, ctx))
	}
	item := selectionPreviewItemFromFirstPreview(position, preview, len(selectedTools) != 0)
	return confirmSelectionPreviewSource(requestContext, ctx, position, snapshot, preview.SelectedSource, item)
}

// confirmSelectionPreviewSource re-verifies the selected source through the
// safe native opener before a create or update disposition claims a usable
// input. A source that is proven absent blocks the position; any other failed
// check downgrades it to unchecked instead of asserting a plan whose input was
// never confirmed.
func confirmSelectionPreviewSource(requestContext, ctx context.Context, position LibraryPublicationSelectionPosition, snapshot *persistence.LibraryFirstPublicationContext, selected *FirstPublicationPreviewSource, item LibraryPublicationSelectionPreviewItem) LibraryPublicationSelectionPreviewItem {
	if item.Disposition != LibraryPublicationSelectionDispositionCreate && item.Disposition != LibraryPublicationSelectionDispositionUpdate {
		return item
	}
	switch selectionPreviewSourcePresence(ctx, sourcefs.NewOpener(), selectionPreviewSnapshotSource(snapshot, selected)) {
	case selectionPreviewSourceMissing:
		return selectionPreviewStatusItem(position, LibraryPublicationSelectionDispositionSkipped, LibraryPublicationSelectionReasonAudioMissing)
	case selectionPreviewSourcePresent:
		return item
	default:
		return selectionPreviewStatusItem(position, LibraryPublicationSelectionDispositionUnchecked, selectionPreviewContextReason(requestContext, ctx))
	}
}

type selectionPreviewSourceState uint8

const (
	// selectionPreviewSourceUnconfirmed means the native read did not prove
	// either presence or absence, so the position cannot claim a disposition.
	selectionPreviewSourceUnconfirmed selectionPreviewSourceState = iota
	selectionPreviewSourcePresent
	selectionPreviewSourceMissing
)

// selectionPreviewSnapshotSource resolves the selected preview candidate back
// to its inventory facts, including the retained source's native location.
func selectionPreviewSnapshotSource(snapshot *persistence.LibraryFirstPublicationContext, selected *FirstPublicationPreviewSource) *persistence.FirstPublicationSource {
	if snapshot == nil || selected == nil {
		return nil
	}
	for index := range snapshot.Sources {
		source := &snapshot.Sources[index]
		if source.Selection.LocationID == selected.LocationID &&
			source.Selection.MediaVariantID == selected.MediaVariantID &&
			source.Quality.StreamIndex == selected.StreamIndex {
			return source
		}
	}
	return nil
}

func selectionPreviewSourcePresence(ctx context.Context, opener sourcefs.Opener, source *persistence.FirstPublicationSource) selectionPreviewSourceState {
	if source == nil || opener == nil || source.Selection.InventoryPath == "" || source.Selection.RelativePath == "" {
		return selectionPreviewSourceUnconfirmed
	}
	root, err := opener.OpenRoot(ctx, source.Selection.InventoryPath)
	if err != nil {
		return selectionPreviewSourceOpenState(err)
	}
	defer func() { _ = root.Close() }()
	file, err := sourcefs.OpenRegularAt(ctx, root, source.Selection.RelativePath)
	if err != nil {
		return selectionPreviewSourceOpenState(err)
	}
	info, statErr := file.Stat(ctx)
	_ = file.Close()
	if statErr != nil || info == nil || !info.Mode().IsRegular() {
		return selectionPreviewSourceUnconfirmed
	}
	return selectionPreviewSourcePresent
}

func selectionPreviewSourceOpenState(err error) selectionPreviewSourceState {
	if errors.Is(err, fs.ErrNotExist) || os.IsNotExist(err) {
		return selectionPreviewSourceMissing
	}
	return selectionPreviewSourceUnconfirmed
}

func acquireSelectionPreviewSlot(ctx context.Context) bool {
	select {
	case libraryPublicationSelectionPreviewSlots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func releaseSelectionPreviewSlot() {
	<-libraryPublicationSelectionPreviewSlots
}

func selectionPreviewContextReason(requestContext, planningContext context.Context) string {
	switch {
	case errors.Is(requestContext.Err(), context.DeadlineExceeded), errors.Is(planningContext.Err(), context.DeadlineExceeded):
		return LibraryPublicationSelectionReasonDeadlineExceeded
	case requestContext.Err() != nil:
		return LibraryPublicationSelectionReasonCanceled
	default:
		return LibraryPublicationSelectionReasonFailed
	}
}

func selectionPreviewStatusItem(position LibraryPublicationSelectionPosition, disposition, reason string) LibraryPublicationSelectionPreviewItem {
	return LibraryPublicationSelectionPreviewItem{
		ReleaseID: position.ReleaseID, PositionID: position.PositionID,
		Disposition: disposition, Reason: reason,
	}
}

func selectionPreviewItemFromFirstPreview(position LibraryPublicationSelectionPosition, preview FirstPublicationPreview, toolsAvailable bool) LibraryPublicationSelectionPreviewItem {
	path := safeSelectionPreviewPath(preview.ResolvedPathComponents)
	if reason := selectionPreviewFirstBlockReason(preview, toolsAvailable, path); reason != "" {
		return selectionPreviewStatusItem(position, LibraryPublicationSelectionDispositionBlocked, reason)
	}
	item := selectionPreviewStatusItem(position, LibraryPublicationSelectionDispositionCreate, "")
	item.ResolvedPath = path
	return item
}

func selectionPreviewFirstBlockReason(preview FirstPublicationPreview, toolsAvailable bool, resolvedPath string) string {
	switch {
	case preview.ActiveAttempt != nil:
		return LibraryPublicationSelectionReasonAttemptActive
	case !toolsAvailable:
		return LibraryPublicationSelectionReasonToolsUnavailable
	case preview.SelectedSource == nil:
		return LibraryPublicationSelectionReasonSourceSelection
	case preview.Transform == nil:
		return LibraryPublicationSelectionReasonFormatUnavailable
	case len(preview.PathComponents) == 0:
		return LibraryPublicationSelectionReasonPathUnavailable
	case len(preview.Blockers) != 0:
		return LibraryPublicationSelectionReasonContextBlocked
	case resolvedPath == "":
		return LibraryPublicationSelectionReasonResolvedPath
	}
	return ""
}

func selectionPreviewItemFromRetainedPreview(position LibraryPublicationSelectionPosition, preview FirstPublicationPreview) LibraryPublicationSelectionPreviewItem {
	if preview.ActiveAttempt != nil {
		return selectionPreviewStatusItem(position, LibraryPublicationSelectionDispositionBlocked, LibraryPublicationSelectionReasonAttemptActive)
	}
	capability := preview.ConfirmationCapability
	if capability == nil {
		return selectionPreviewStatusItem(position, LibraryPublicationSelectionDispositionBlocked, LibraryPublicationSelectionReasonNotEligible)
	}
	path := safeSelectionPreviewPath(preview.ResolvedPathComponents)
	switch capability.Status {
	case "unchanged":
		item := selectionPreviewStatusItem(position, LibraryPublicationSelectionDispositionUnchanged, LibraryPublicationSelectionReasonContentUnchanged)
		item.ResolvedPath = path
		return item
	case "ready":
		if len(preview.Blockers) != 0 {
			return selectionPreviewStatusItem(position, LibraryPublicationSelectionDispositionBlocked, LibraryPublicationSelectionReasonContextBlocked)
		}
		if path == "" {
			return selectionPreviewStatusItem(position, LibraryPublicationSelectionDispositionBlocked, LibraryPublicationSelectionReasonResolvedPath)
		}
		item := selectionPreviewStatusItem(position, LibraryPublicationSelectionDispositionUpdate, "")
		item.ResolvedPath = path
		return item
	default:
		return selectionPreviewStatusItem(position, LibraryPublicationSelectionDispositionBlocked, selectionPreviewRetainedBlockReason(capability.ReasonCodes))
	}
}

func selectionPreviewRetainedBlockReason(codes []string) string {
	for _, code := range codes {
		switch code {
		case "source_republish_not_eligible":
			return LibraryPublicationSelectionReasonNotEligible
		case "content_unchanged":
			return LibraryPublicationSelectionReasonContentUnchanged
		}
	}
	return LibraryPublicationSelectionReasonNotEligible
}

// safeSelectionPreviewPath renders only a relative namespace path. Anything a
// publication renderer should never accept is omitted instead of exposed.
func safeSelectionPreviewPath(components []string) string {
	if len(components) == 0 {
		return ""
	}
	for _, component := range components {
		if component == "" || component == "." || component == ".." ||
			strings.HasPrefix(component, "/") || strings.ContainsAny(component, "\\\x00") {
			return ""
		}
	}
	return strings.Join(components, "/")
}

func markSelectionPreviewPathClashes(items []LibraryPublicationSelectionPreviewItem) {
	groups := make(map[string][]int, len(items))
	for index := range items {
		path := items[index].ResolvedPath
		if path == "" {
			continue
		}
		if items[index].Disposition != LibraryPublicationSelectionDispositionCreate && items[index].Disposition != LibraryPublicationSelectionDispositionUpdate {
			continue
		}
		groups[path] = append(groups[path], index)
	}
	for _, indexes := range groups {
		if len(indexes) < 2 {
			continue
		}
		for _, index := range indexes {
			others := make([]LibraryPublicationSelectionPreviewReference, 0, len(indexes)-1)
			for _, other := range indexes {
				if other == index {
					continue
				}
				others = append(others, LibraryPublicationSelectionPreviewReference{
					ReleaseID: items[other].ReleaseID, PositionID: items[other].PositionID,
				})
			}
			items[index].PathClash = true
			items[index].PathClashPositions = others
		}
	}
}
