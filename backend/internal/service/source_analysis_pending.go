package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type sourceAnalysisPendingRepository interface {
	ListPendingSourceAnalysisRoots(context.Context) ([]persistence.SourceRoot, error)
	ListPendingSourceAnalysisWork(context.Context, uuid.UUID) ([]persistence.SourceAnalysisPendingWork, error)
	ReadPendingSourceAnalysisWorkBatch(context.Context, uuid.UUID, []uuid.UUID) ([]persistence.SourceAnalysisPendingWork, error)
	FailPendingSourceAnalysisStep(context.Context, uuid.UUID, uuid.UUID, persistence.SourceStepName, string) (bool, error)
}

type sourceAnalysisPendingCacheRepository interface {
	SourceAnalysisCacheLookup
	ReusePendingSourceAnalysisCache(context.Context, uuid.UUID, uuid.UUID, persistence.SourceStepName, uuid.UUID, string) (bool, error)
}

// AdmitPending admits the current pending steps of one enabled source root. An
// empty or already-active root is a no-op; database admission remains the
// authoritative concurrency fence.
func (s *SourceAnalysisOperations) AdmitPending(ctx context.Context, rootID uuid.UUID) (*persistence.Operation, error) {
	if rootID == uuid.Nil {
		return nil, fmt.Errorf("admit pending source analysis: source root is required")
	}
	repository, ok := s.repository.(sourceAnalysisPendingRepository)
	if !ok {
		return nil, fmt.Errorf("admit pending source analysis: pending-work repository is unavailable")
	}
	var firstOperation *persistence.Operation
	works, err := repository.ListPendingSourceAnalysisWork(ctx, rootID)
	if err != nil {
		return nil, fmt.Errorf("admit pending source analysis: %w", err)
	}
	if len(works) == 0 {
		return nil, nil
	}
	if err := s.ready(ctx); err != nil {
		return nil, err
	}
	root := works[0].Root
	if !root.Enabled || root.Stale() || root.InventoryPath == nil || *root.InventoryPath != root.ConfiguredPath {
		return nil, nil
	}
	// Snapshot source identities once; siblings created after this point are left
	// for the next wake or periodic dispatch.
	current := currentPendingSourceAnalysisCohorts(ctx, works)
	if len(current) == 0 {
		return nil, nil
	}
	ordinary, retained, malformed := separateRetainedSourceAnalysisSteps(current)
	for _, pending := range malformed {
		applied, failErr := repository.FailPendingSourceAnalysisStep(ctx, rootID, pending.Pending.Work.ID, persistence.SourceStepName(pending.Step.Step), "The retained analysis request is invalid; retry this step manually.")
		if failErr != nil {
			if errors.Is(failErr, persistence.ErrToolsRootMoveActive) {
				return nil, nil
			}
			return nil, fmt.Errorf("record invalid retained source analysis input: %w", failErr)
		}
		_ = applied
	}
	// Retained cache reuse is phase-wide and precedes every availability check.
	owners := make(map[uuid.UUID]struct{})
	for _, intent := range retained {
		if owner := retainedRecoveryOwner(intent); owner != uuid.Nil {
			owners[owner] = struct{}{}
		}
		if *intent.Snapshot.RerunTarget {
			continue
		}
		step := persistence.SourceStepName(intent.Step.Step)
		selection, version := retainedCacheSelection(intent.Snapshot, step)
		if version != "" {
			if _, err := s.reusePendingCache(ctx, []persistence.SourceAnalysisPendingWork{intent.Pending}, step, selection); err != nil {
				return nil, err
			}
		}
	}
	retainedIDs := uniquePendingWorkIDs(retained)
	latest, err := repository.ReadPendingSourceAnalysisWorkBatch(ctx, rootID, retainedIDs)
	if err != nil {
		return nil, fmt.Errorf("reread retained pending source analysis: %w", err)
	}
	_, latestRetained, _ := separateRetainedSourceAnalysisSteps(latest)
	// Index the fresh retained tuples once by durable owner so every owner group
	// is checked in a single linear pass instead of rescanning per owner.
	recoveryGroups := make(map[uuid.UUID][]retainedSourceAnalysisIntent)
	indexed := make(map[string]struct{})
	for _, intent := range latestRetained {
		owner := retainedRecoveryOwner(intent)
		if owner == uuid.Nil {
			continue
		}
		key := intent.Pending.Work.ID.String() + ":" + intent.Step.Step
		if _, exists := indexed[key]; exists {
			continue
		}
		indexed[key] = struct{}{}
		recoveryGroups[owner] = append(recoveryGroups[owner], intent)
	}
	for owner := range owners {
		for _, intent := range recoveryGroups[owner] {
			if *intent.Snapshot.CacheOnlyReuse || intent.Step.Step == string(persistence.SourceStepSHA256) {
				continue
			}
			available, checkErr := s.retainedToolAvailable(ctx, intent.Snapshot, persistence.SourceStepName(intent.Step.Step))
			if checkErr != nil {
				return nil, checkErr
			}
			if !available {
				if _, failErr := repository.FailPendingSourceAnalysisStep(ctx, rootID, intent.Pending.Work.ID, persistence.SourceStepName(intent.Step.Step), "The retained analysis tool is unavailable; retry this step manually."); failErr != nil {
					if errors.Is(failErr, persistence.ErrToolsRootMoveActive) {
						return firstOperation, nil
					}
					return nil, fmt.Errorf("record unavailable retained analysis tool: %w", failErr)
				}
			}
		}
	}
	for _, intent := range latestRetained {
		if retainedRecoveryOwner(intent) != uuid.Nil || *intent.Snapshot.CacheOnlyReuse || intent.Step.Step == string(persistence.SourceStepSHA256) {
			continue
		}
		available, checkErr := s.retainedToolAvailable(ctx, intent.Snapshot, persistence.SourceStepName(intent.Step.Step))
		if checkErr != nil {
			return nil, checkErr
		}
		if !available {
			if _, failErr := repository.FailPendingSourceAnalysisStep(ctx, rootID, intent.Pending.Work.ID, persistence.SourceStepName(intent.Step.Step), "The retained analysis tool is unavailable; retry this step manually."); failErr != nil {
				if errors.Is(failErr, persistence.ErrToolsRootMoveActive) {
					return firstOperation, nil
				}
				return nil, fmt.Errorf("record unavailable retained analysis tool: %w", failErr)
			}
		}
	}
	// Refresh identities after cache/prerequisite updates and admit retained steps
	// independently, without importing historical owner configuration.
	latest, err = repository.ReadPendingSourceAnalysisWorkBatch(ctx, rootID, retainedIDs)
	if err != nil {
		return nil, fmt.Errorf("reread retained pending source analysis: %w", err)
	}
	latestByID := pendingWorkMap(latest)
	for _, intent := range retained {
		fresh, exists := latestByID[intent.Pending.Work.ID]
		if !exists || !hasPendingStep(fresh, intent.Step.Step) {
			continue
		}
		operation, enqueueErr := s.enqueueFromSnapshot(ctx, intent.Snapshot, intent.Step.Step, *intent.Snapshot.RerunTarget, fresh.Root.ID, fresh.Location.ID)
		if errors.Is(enqueueErr, ErrSourceAnalysisMoveActive) {
			return firstOperation, nil
		}
		// Busy is work-specific: another retained step of this same work already
		// holds admission. Skip only this tuple and keep admitting independent
		// works and ordinary steps.
		if errors.Is(enqueueErr, ErrSourceAnalysisBusy) {
			continue
		}
		if enqueueErr != nil {
			return firstOperation, enqueueErr
		}
		if operation != nil && firstOperation == nil {
			firstOperation = operation
		}
	}
	// Run ordinary prerequisites and cache checks once per SHA-policy cohort.
	for _, cohort := range [][]persistence.SourceAnalysisPendingWork{filterPendingPolicy(ordinary, false), filterPendingPolicy(ordinary, true)} {
		if len(cohort) == 0 {
			continue
		}
		steps := pendingSteps(cohort)
		selections := make(map[persistence.SourceStepName][]persistence.SourceAnalysisToolSelection)
		for _, step := range []persistence.SourceStepName{persistence.SourceStepProbe, persistence.SourceStepFingerprint} {
			if !steps[step] {
				continue
			}
			selection, selectErr := s.selectedTools(ctx, step)
			if selectErr == nil {
				selections[step] = selection
				if _, cacheErr := s.reusePendingCache(ctx, cohort, step, selection[0]); cacheErr != nil {
					return nil, cacheErr
				}
				continue
			}
			if !errors.Is(selectErr, ErrSourceAnalysisToolUnavailable) {
				return nil, selectErr
			}
			for _, pending := range cohort {
				for _, row := range pending.Steps {
					if row.Step == string(step) {
						if _, failErr := repository.FailPendingSourceAnalysisStep(ctx, rootID, pending.Work.ID, step, "The selected analysis tool is unavailable."); failErr != nil {
							if errors.Is(failErr, persistence.ErrToolsRootMoveActive) {
								return firstOperation, nil
							}
							return nil, fmt.Errorf("record pending source analysis prerequisite failure: %w", failErr)
						}
					}
				}
			}
		}
		for _, candidate := range cohort {
			freshRows, readErr := repository.ReadPendingSourceAnalysisWorkBatch(ctx, rootID, []uuid.UUID{candidate.Work.ID})
			if readErr != nil {
				return firstOperation, fmt.Errorf("reread pending source analysis work: %w", readErr)
			}
			freshOrdinary, _, _ := separateRetainedSourceAnalysisSteps(freshRows)
			if len(freshOrdinary) == 0 {
				continue
			}
			fresh := freshOrdinary[0]
			// Revalidate the exact current size+mtime at the admission boundary:
			// the initial population snapshot may predate a long cache preflight.
			if fresh.Work.SHA256Enabled != candidate.Work.SHA256Enabled || !samePendingSourceIdentity(candidate, fresh) || !pendingSourceFileCurrent(ctx, fresh) {
				continue
			}
			// The selected operation contains only the fresh ordinary steps of this
			// original work identity.
			cacheOnly, rerun := false, false
			snapshot := persistence.SourceAnalysisOperationSnapshot{SchemaVersion: persistence.SourceAnalysisOperationSnapshotVersion, Mode: persistence.SourceAnalysisModeBatch, WorkIDs: []uuid.UUID{fresh.Work.ID}, RerunTarget: &rerun, SHA256Enabled: &fresh.Work.SHA256Enabled, CacheOnlyReuse: &cacheOnly}
			for _, row := range fresh.Steps {
				snapshot.SelectedSteps = append(snapshot.SelectedSteps, persistence.SourceAnalysisStepSelection{WorkID: fresh.Work.ID, Step: persistence.SourceStepName(row.Step)})
			}
			for step, selection := range selections {
				if hasPendingStep(fresh, string(step)) {
					snapshot.Tools = append(snapshot.Tools, selection...)
				}
			}
			snapshot.ToolsReadRequired = len(snapshot.Tools) > 0
			raw, marshalErr := json.Marshal(snapshot)
			if marshalErr != nil {
				return firstOperation, fmt.Errorf("encode pending source analysis snapshot: %w", marshalErr)
			}
			operation := &persistence.Operation{ID: uuid.New(), Kind: SourceAnalysisOperationKind, State: "queued", Stage: SourceAnalysisStageQueued, InputSnapshot: raw, Attempt: 1, SourceAnalysisMode: snapshot.Mode, TargetSourceRootID: &rootID, ToolsReadRequired: snapshot.ToolsReadRequired, SourceAnalysisTools: snapshot.Tools}
			operation, admitErr := s.admit(ctx, operation)
			if errors.Is(admitErr, ErrSourceAnalysisBusy) || errors.Is(admitErr, ErrSourceAnalysisMoveActive) {
				return firstOperation, nil
			}
			if admitErr != nil {
				return firstOperation, admitErr
			}
			if operation != nil && firstOperation == nil {
				firstOperation = operation
			}
		}
	}
	return firstOperation, nil
}

func retainedRecoveryOwner(intent retainedSourceAnalysisIntent) uuid.UUID {
	if intent.Step.LastOperationID == nil || *intent.Step.LastOperationID == uuid.Nil ||
		intent.Snapshot.RerunTarget == nil || *intent.Snapshot.RerunTarget {
		return uuid.Nil
	}
	return *intent.Step.LastOperationID
}

func retainedRecoveryGroup(intents []retainedSourceAnalysisIntent, owner uuid.UUID) []retainedSourceAnalysisIntent {
	if owner == uuid.Nil {
		return nil
	}
	group := make([]retainedSourceAnalysisIntent, 0, len(intents))
	seen := make(map[string]struct{})
	for _, intent := range intents {
		if retainedRecoveryOwner(intent) != owner {
			continue
		}
		key := intent.Pending.Work.ID.String() + ":" + intent.Step.Step
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		group = append(group, intent)
	}
	return group
}

func retainedRecoveryBatchSnapshot(group []retainedSourceAnalysisIntent) (persistence.SourceAnalysisOperationSnapshot, bool) {
	// Retained work is re-admitted one step at a time using current tools and
	// settings. Grouping it would accidentally copy historical execution config.
	return persistence.SourceAnalysisOperationSnapshot{}, false
}

func (s *SourceAnalysisOperations) retainedToolAvailable(ctx context.Context, snapshot persistence.SourceAnalysisOperationSnapshot, step persistence.SourceStepName) (bool, error) {
	_, err := s.selectedTools(ctx, step)
	if errors.Is(err, ErrSourceAnalysisToolUnavailable) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func separateRetainedSourceAnalysisSteps(works []persistence.SourceAnalysisPendingWork) ([]persistence.SourceAnalysisPendingWork, []retainedSourceAnalysisIntent, []retainedSourceAnalysisIntent) {
	ordinary := make([]persistence.SourceAnalysisPendingWork, 0, len(works))
	var retained, malformed []retainedSourceAnalysisIntent
	for _, pending := range works {
		copyPending := pending
		copyPending.Steps = nil
		for _, step := range pending.Steps {
			if len(step.InputSnapshot) == 0 || string(step.InputSnapshot) == "null" {
				copyPending.Steps = append(copyPending.Steps, step)
				continue
			}
			intent, err := retainedSourceAnalysisStepIntent(pending, step)
			if err != nil {
				malformed = append(malformed, retainedSourceAnalysisIntent{Pending: pending, Step: step})
				continue
			}
			retained = append(retained, intent)
		}
		if len(copyPending.Steps) > 0 {
			ordinary = append(ordinary, copyPending)
		}
	}
	return ordinary, retained, malformed
}

func currentPendingSourceAnalysisCohorts(ctx context.Context, works []persistence.SourceAnalysisPendingWork) []persistence.SourceAnalysisPendingWork {
	current := make([]persistence.SourceAnalysisPendingWork, 0, len(works))
	for _, pending := range works {
		if pendingSourceFileCurrent(ctx, pending) {
			current = append(current, pending)
		}
	}
	return current
}

func uniquePendingWorkIDs(intents []retainedSourceAnalysisIntent) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(intents))
	ids := make([]uuid.UUID, 0, len(intents))
	for _, intent := range intents {
		id := intent.Pending.Work.ID
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

func pendingWorkMap(works []persistence.SourceAnalysisPendingWork) map[uuid.UUID]persistence.SourceAnalysisPendingWork {
	byID := make(map[uuid.UUID]persistence.SourceAnalysisPendingWork, len(works))
	for _, work := range works {
		byID[work.Work.ID] = work
	}
	return byID
}

func hasPendingStep(work persistence.SourceAnalysisPendingWork, step string) bool {
	for _, row := range work.Steps {
		if row.Step == step {
			return true
		}
	}
	return false
}

func samePendingSourceIdentity(expected, actual persistence.SourceAnalysisPendingWork) bool {
	return expected.Work.ID == actual.Work.ID && expected.Work.LocationID == actual.Work.LocationID &&
		expected.Work.ConfiguredPath == actual.Work.ConfiguredPath && expected.Work.InventoryPath == actual.Work.InventoryPath &&
		expected.Work.RelativePath == actual.Work.RelativePath && expected.Work.SizeBytes == actual.Work.SizeBytes &&
		expected.Work.Mtime.Equal(actual.Work.Mtime) && expected.Location.ID == actual.Location.ID &&
		expected.Location.RelativePath == actual.Location.RelativePath && expected.Location.SizeBytes == actual.Location.SizeBytes &&
		expected.Location.Mtime.Equal(actual.Location.Mtime)
}

func filterPendingPolicy(works []persistence.SourceAnalysisPendingWork, enabled bool) []persistence.SourceAnalysisPendingWork {
	filtered := make([]persistence.SourceAnalysisPendingWork, 0, len(works))
	for _, work := range works {
		if work.Work.SHA256Enabled == enabled {
			filtered = append(filtered, work)
		}
	}
	return filtered
}

// DispatchPending sweeps roots independently: a stale root or transient tools
// move does not prevent another root from being admitted.
func (s *SourceAnalysisOperations) DispatchPending(ctx context.Context) error {
	repository, ok := s.repository.(sourceAnalysisPendingRepository)
	if !ok {
		return fmt.Errorf("dispatch pending source analysis: pending-work repository is unavailable")
	}
	roots, err := repository.ListPendingSourceAnalysisRoots(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, root := range roots {
		if _, err := s.AdmitPending(ctx, root.ID); err != nil {
			if errors.Is(err, ErrSourceAnalysisMoveActive) || errors.Is(err, ErrSourceAnalysisBusy) || errors.Is(err, ErrSourceAnalysisStale) || errors.Is(err, ErrSourceAnalysisNotReady) {
				continue
			}
			failures = append(failures, fmt.Errorf("dispatch pending source analysis for root %s: %w", root.ID, err))
		}
	}
	return errors.Join(failures...)
}

func (s *SourceAnalysisOperations) reusePendingCache(ctx context.Context, works []persistence.SourceAnalysisPendingWork, step persistence.SourceStepName, selection persistence.SourceAnalysisToolSelection) (bool, error) {
	repository, ok := s.repository.(sourceAnalysisPendingCacheRepository)
	if !ok {
		return false, fmt.Errorf("reuse pending source analysis: cache repository is unavailable")
	}
	applied := false
	for _, pending := range works {
		requested := false
		for _, row := range pending.Steps {
			requested = requested || row.Step == string(step)
		}
		if !requested {
			continue
		}
		detail, err := s.repository.ReadSourceLocationDetail(ctx, pending.Root.ID, pending.Location.ID)
		if err != nil {
			return false, fmt.Errorf("read pending cache identity: %w", err)
		}
		if detail.Work == nil || detail.Work.ID != pending.Work.ID || detail.SHAVariant == nil || len(detail.SHAVariant.SourceSHA256) != sha256.Size {
			continue
		}
		var digest [sha256.Size]byte
		copy(digest[:], detail.SHAVariant.SourceSHA256)
		var resultID uuid.UUID
		version := selection.Version
		if step == persistence.SourceStepProbe {
			version = selection.VersionBanner
			result, hit, err := repository.LookupSourceProbe(ctx, digest, version, persistence.SourceAnalysisPolicyVersion)
			if err != nil {
				return false, fmt.Errorf("lookup pending probe cache: %w", err)
			}
			if hit && result != nil {
				resultID = result.ID
			}
		} else {
			result, hit, err := repository.LookupSourceFingerprint(ctx, digest)
			if err != nil {
				return false, fmt.Errorf("lookup pending fingerprint cache: %w", err)
			}
			if hit && result != nil {
				resultID = result.ID
				version = result.FPCalcVersion
			}
		}
		if resultID != uuid.Nil {
			changed, err := repository.ReusePendingSourceAnalysisCache(ctx, pending.Root.ID, pending.Work.ID, step, resultID, version)
			if err != nil {
				return false, fmt.Errorf("reuse pending analysis cache: %w", err)
			}
			applied = applied || changed
		}
	}
	return applied, nil
}

func retainedCacheSelection(snapshot persistence.SourceAnalysisOperationSnapshot, step persistence.SourceStepName) (persistence.SourceAnalysisToolSelection, string) {
	selection := persistence.SourceAnalysisToolSelection{}
	version := ""
	for _, tool := range snapshot.Tools {
		if step == persistence.SourceStepProbe && tool.Executable == "ffprobe" {
			selection = tool
			version = tool.VersionBanner
			break
		}
		if step == persistence.SourceStepFingerprint && tool.Executable == "fpcalc" {
			selection = tool
			version = tool.Version
			break
		}
	}
	if version == "" {
		switch step {
		case persistence.SourceStepProbe:
			version = snapshot.CacheOnlyFFProbeVersion
		case persistence.SourceStepFingerprint:
			version = snapshot.CacheOnlyFPCalcVersion
		}
		selection.VersionBanner = version
		selection.Version = version
	}
	return selection, version
}

func pendingSteps(works []persistence.SourceAnalysisPendingWork) map[persistence.SourceStepName]bool {
	steps := make(map[persistence.SourceStepName]bool)
	for _, work := range works {
		for _, step := range work.Steps {
			steps[persistence.SourceStepName(step.Step)] = true
		}
	}
	return steps
}

func pendingSourceFileCurrent(ctx context.Context, pending persistence.SourceAnalysisPendingWork) bool {
	if pending.Root.InventoryPath == nil || !pending.Root.Enabled || pending.Root.Stale() ||
		pending.Work.ConfiguredPath != pending.Root.ConfiguredPath || pending.Work.InventoryPath != *pending.Root.InventoryPath ||
		pending.Work.RelativePath != pending.Location.RelativePath {
		return false
	}
	opener := sourcefs.NewOpener()
	root, err := opener.OpenRoot(ctx, pending.Work.InventoryPath)
	if err != nil {
		return false
	}
	defer func() { _ = root.Close() }()
	file, err := sourcefs.OpenRegularAt(ctx, root, pending.Work.RelativePath)
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat(ctx)
	if err != nil || !info.Mode().IsRegular() || info.Size() != pending.Work.SizeBytes || info.Size() != pending.Location.SizeBytes {
		return false
	}
	mtime := func(value time.Time) time.Time { return value.UTC().Truncate(time.Microsecond) }
	return mtime(info.ModTime()).Equal(mtime(pending.Work.Mtime)) && mtime(info.ModTime()).Equal(mtime(pending.Location.Mtime))
}
