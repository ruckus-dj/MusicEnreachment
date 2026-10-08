package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type sourceAnalysisPendingRepository interface {
	ListPendingSourceAnalysisRoots(context.Context) ([]persistence.SourceRoot, error)
	ListPendingSourceAnalysisWork(context.Context, uuid.UUID) ([]persistence.SourceAnalysisPendingWork, error)
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
	seen := make(map[string]struct{})
	var firstOperation *persistence.Operation
	for {
		works, err := repository.ListPendingSourceAnalysisWork(ctx, rootID)
		if err != nil {
			return nil, fmt.Errorf("admit pending source analysis: %w", err)
		}
		if len(works) == 0 {
			return firstOperation, nil
		}
		if err := s.ready(ctx); err != nil {
			return nil, err
		}
		root := works[0].Root
		if !root.Enabled || root.Stale() || root.InventoryPath == nil || *root.InventoryPath != root.ConfiguredPath {
			return firstOperation, nil
		}
		current := currentPendingSourceAnalysisCohort(ctx, works)
		if len(current) == 0 {
			return firstOperation, nil
		}
		selectedWorkID := current[0].Work.ID
		fingerprint := pendingSourceAnalysisFingerprint(current)
		if _, duplicate := seen[fingerprint]; duplicate {
			return firstOperation, nil
		}
		seen[fingerprint] = struct{}{}
		shaPolicy := current[0].Work.SHA256Enabled
		progress := false
		ordinary, retained, malformed := separateRetainedSourceAnalysisSteps(current)
		for _, pending := range malformed {
			applied, err := repository.FailPendingSourceAnalysisStep(ctx, rootID, pending.Pending.Work.ID, persistence.SourceStepName(pending.Step.Step), "The retained analysis request is invalid; retry this step manually.")
			if err != nil {
				if errors.Is(err, persistence.ErrToolsRootMoveActive) {
					return firstOperation, nil
				}
				return nil, fmt.Errorf("record invalid retained source analysis input: %w", err)
			}
			progress = progress || applied
		}
		for _, intent := range retained {
			step := persistence.SourceStepName(intent.Step.Step)
			if !*intent.Snapshot.RerunTarget {
				selection, version := retainedCacheSelection(intent.Snapshot, step)
				if version != "" {
					applied, err := s.reusePendingCache(ctx, []persistence.SourceAnalysisPendingWork{intent.Pending}, step, selection)
					if err != nil {
						return nil, err
					}
					progress = progress || applied
				}
			}
			latest, err := repository.ListPendingSourceAnalysisWork(ctx, rootID)
			if err != nil {
				return nil, err
			}
			stillPending := false
			for _, row := range latest {
				if row.Work.ID != intent.Pending.Work.ID {
					continue
				}
				for _, pendingStep := range row.Steps {
					stillPending = stillPending || pendingStep.Step == intent.Step.Step
				}
			}
			if !stillPending {
				continue
			}
			if retainedRecoveryOwner(intent) == uuid.Nil && !*intent.Snapshot.CacheOnlyReuse && step != persistence.SourceStepSHA256 {
				available, checkErr := s.retainedToolAvailable(ctx, intent.Snapshot, step)
				if checkErr != nil {
					return nil, checkErr
				}
				if !available {
					applied, failErr := repository.FailPendingSourceAnalysisStep(ctx, rootID, intent.Pending.Work.ID, step, "The retained analysis tool is unavailable; retry this step manually.")
					if failErr != nil {
						if errors.Is(failErr, persistence.ErrToolsRootMoveActive) {
							return firstOperation, nil
						}
						return nil, fmt.Errorf("record unavailable retained analysis tool: %w", failErr)
					}
					progress = progress || applied
					continue
				}
			}
			if recoveryOwner := retainedRecoveryOwner(intent); recoveryOwner != uuid.Nil {
				latest, err := repository.ListPendingSourceAnalysisWork(ctx, rootID)
				if err != nil {
					return nil, err
				}
				latest = currentPendingSourceAnalysisCohort(ctx, latest)
				_, latestRetained, _ := separateRetainedSourceAnalysisSteps(latest)
				group := retainedRecoveryGroup(latestRetained, recoveryOwner)
				if len(group) > 1 {
					for _, member := range group {
						memberStep := persistence.SourceStepName(member.Step.Step)
						// The current intent's cache lookup already ran above. For every
						// other member, resolve a pinned-version cache hit before asking
						// whether its runner is still installed.
						if member.Pending.Work.ID != intent.Pending.Work.ID || member.Step.Step != intent.Step.Step {
							if !*member.Snapshot.RerunTarget {
								selection, version := retainedCacheSelection(member.Snapshot, memberStep)
								if version != "" {
									applied, cacheErr := s.reusePendingCache(ctx, []persistence.SourceAnalysisPendingWork{member.Pending}, memberStep, selection)
									if cacheErr != nil {
										return nil, cacheErr
									}
									progress = progress || applied
								}
							}
						}
						// Cache reuse and failures can change the pending group. Re-read
						// before checking availability so completed members need no tool.
						latest, err = repository.ListPendingSourceAnalysisWork(ctx, rootID)
						if err != nil {
							return nil, err
						}
					}
					latest = currentPendingSourceAnalysisCohort(ctx, latest)
					_, latestRetained, _ = separateRetainedSourceAnalysisSteps(latest)
					group = retainedRecoveryGroup(latestRetained, recoveryOwner)
					for _, member := range group {
						memberStep := persistence.SourceStepName(member.Step.Step)
						if *member.Snapshot.CacheOnlyReuse || memberStep == persistence.SourceStepSHA256 {
							continue
						}
						available, checkErr := s.retainedToolAvailable(ctx, member.Snapshot, memberStep)
						if checkErr != nil {
							return nil, checkErr
						}
						if !available {
							applied, failErr := repository.FailPendingSourceAnalysisStep(ctx, rootID, member.Pending.Work.ID, memberStep, "The retained analysis tool is unavailable; retry this step manually.")
							if failErr != nil {
								if errors.Is(failErr, persistence.ErrToolsRootMoveActive) {
									return firstOperation, nil
								}
								return nil, fmt.Errorf("record unavailable retained analysis tool: %w", failErr)
							}
							progress = progress || applied
						}
					}
					latest, err = repository.ListPendingSourceAnalysisWork(ctx, rootID)
					if err != nil {
						return nil, err
					}
					latest = currentPendingSourceAnalysisCohort(ctx, latest)
					_, latestRetained, _ = separateRetainedSourceAnalysisSteps(latest)
					group = retainedRecoveryGroup(latestRetained, recoveryOwner)
					currentPending := false
					for _, member := range group {
						currentPending = currentPending || (member.Pending.Work.ID == intent.Pending.Work.ID && member.Step.Step == intent.Step.Step)
					}
					if !currentPending {
						continue
					}
					latest, err = repository.ListPendingSourceAnalysisWork(ctx, rootID)
					if err != nil {
						return nil, err
					}
					latest = currentPendingSourceAnalysisCohort(ctx, latest)
					_, latestRetained, _ = separateRetainedSourceAnalysisSteps(latest)
					group = retainedRecoveryGroup(latestRetained, recoveryOwner)
				}
				if snapshot, ok := retainedRecoveryBatchSnapshot(group); ok {
					raw, marshalErr := json.Marshal(snapshot)
					if marshalErr != nil {
						return nil, fmt.Errorf("encode retained source analysis batch snapshot: %w", marshalErr)
					}
					operation := &persistence.Operation{
						ID: uuid.New(), Kind: SourceAnalysisOperationKind, State: "queued", Stage: SourceAnalysisStageQueued,
						SourceAnalysisMode: snapshot.Mode, ToolsReadRequired: snapshot.ToolsReadRequired,
						InputSnapshot: raw, Attempt: 1, TargetSourceRootID: &rootID,
					}
					operation, err = s.admit(ctx, operation)
					if errors.Is(err, ErrSourceAnalysisMoveActive) || errors.Is(err, ErrSourceAnalysisBusy) {
						return firstOperation, nil
					}
					if err != nil || operation != nil {
						return operation, err
					}
				}
			}
			operation, err := s.enqueueFromSnapshot(ctx, intent.Snapshot, intent.Step.Step, *intent.Snapshot.RerunTarget, intent.Pending.Root.ID, intent.Pending.Location.ID)
			if errors.Is(err, ErrSourceAnalysisMoveActive) || errors.Is(err, ErrSourceAnalysisBusy) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if operation != nil {
				if firstOperation == nil {
					firstOperation = operation
				}
				continue
			}
		}
		valid := ordinary
		if len(valid) > 0 {
			steps := pendingSteps(valid)
			selected := make([]persistence.SourceAnalysisToolSelection, 0, 2)
			for _, step := range []persistence.SourceStepName{persistence.SourceStepProbe, persistence.SourceStepFingerprint} {
				if !steps[step] {
					continue
				}
				selection, selectErr := s.selectedTools(ctx, step)
				if selectErr == nil {
					applied, err := s.reusePendingCache(ctx, valid, step, selection[0])
					if err != nil {
						return nil, err
					}
					progress = progress || applied
				}
				if selectErr != nil {
					if !errors.Is(selectErr, ErrSourceAnalysisToolUnavailable) {
						return nil, selectErr
					}
					for _, pending := range valid {
						for _, row := range pending.Steps {
							if persistence.SourceStepName(row.Step) != step {
								continue
							}
							applied, failErr := repository.FailPendingSourceAnalysisStep(ctx, rootID, pending.Work.ID, step, "The selected analysis tool is unavailable.")
							if failErr != nil {
								if errors.Is(failErr, persistence.ErrToolsRootMoveActive) {
									return firstOperation, nil
								}
								return nil, fmt.Errorf("record pending source analysis prerequisite failure: %w", failErr)
							}
							progress = progress || applied
						}
					}
					continue
				}
				selected = append(selected, selection...)
			}
			latest, err := repository.ListPendingSourceAnalysisWork(ctx, rootID)
			if err != nil {
				return nil, err
			}
			latestCurrent := currentPendingSourceAnalysisCohort(ctx, latest)
			selectedStillCurrent := false
			for _, pending := range latestCurrent {
				if pending.Work.ID == selectedWorkID {
					selectedStillCurrent = pending.Work.SHA256Enabled == shaPolicy
					break
				}
			}
			if !selectedStillCurrent {
				if progress {
					continue
				}
				return firstOperation, nil
			}
			valid, _, _ = separateRetainedSourceAnalysisSteps(latestCurrent)
			if len(valid) == 0 {
				if progress {
					continue
				}
				return firstOperation, nil
			}
			// Preserve the originally selected work across the admission reread.
			// New or reordered siblings must not silently join this operation.
			var selectedWork *persistence.SourceAnalysisPendingWork
			for index := range valid {
				if valid[index].Work.ID == selectedWorkID {
					selectedWork = &valid[index]
					break
				}
			}
			if selectedWork == nil {
				if progress {
					continue
				}
				return firstOperation, nil
			}
			valid = []persistence.SourceAnalysisPendingWork{*selectedWork}
			workIDs := make([]uuid.UUID, 0, len(valid))
			for _, pending := range valid {
				workIDs = append(workIDs, pending.Work.ID)
			}
			cacheOnly, rerun := false, false
			snapshot := persistence.SourceAnalysisOperationSnapshot{
				SchemaVersion: persistence.SourceAnalysisOperationSnapshotVersion,
				Mode:          persistence.SourceAnalysisModeBatch, WorkIDs: workIDs,
				RerunTarget: &rerun, SHA256Enabled: &shaPolicy, CacheOnlyReuse: &cacheOnly,
				ToolsReadRequired: false,
			}
			for _, pendingStep := range selectedWork.Steps {
				snapshot.SelectedSteps = append(snapshot.SelectedSteps, persistence.SourceAnalysisStepSelection{
					WorkID: selectedWork.Work.ID,
					Step:   persistence.SourceStepName(pendingStep.Step),
				})
			}
			remaining := pendingSteps(valid)
			for _, selection := range selected {
				step := persistence.SourceStepProbe
				if selection.Executable == "fpcalc" {
					step = persistence.SourceStepFingerprint
				}
				if remaining[step] {
					snapshot.Tools = append(snapshot.Tools, selection)
				}
			}
			snapshot.ToolsReadRequired = len(snapshot.Tools) > 0
			raw, err := json.Marshal(snapshot)
			if err != nil {
				return nil, fmt.Errorf("encode pending source analysis snapshot: %w", err)
			}
			operation := &persistence.Operation{
				ID: uuid.New(), Kind: SourceAnalysisOperationKind, State: "queued", Stage: SourceAnalysisStageQueued,
				InputSnapshot: raw, Attempt: 1, SourceAnalysisMode: snapshot.Mode,
				TargetSourceRootID: &rootID, ToolsReadRequired: snapshot.ToolsReadRequired, SourceAnalysisTools: snapshot.Tools,
			}
			operation, err = s.admit(ctx, operation)
			if errors.Is(err, ErrSourceAnalysisBusy) || errors.Is(err, ErrSourceAnalysisMoveActive) {
				return firstOperation, nil
			}
			if err != nil {
				return firstOperation, err
			}
			if operation != nil {
				if firstOperation == nil {
					firstOperation = operation
				}
				continue
			}
		}
		if !progress {
			return firstOperation, nil
		}
	}
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

func pendingSourceAnalysisFingerprint(works []persistence.SourceAnalysisPendingWork) string {
	var fingerprint strings.Builder
	for _, work := range works {
		fmt.Fprintf(&fingerprint, "%s:%t:", work.Work.ID, work.Work.SHA256Enabled)
		for _, step := range work.Steps {
			fmt.Fprintf(&fingerprint, "%s,", step.Step)
		}
		fingerprint.WriteByte(';')
	}
	return fingerprint.String()
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

// pendingSourceAnalysisCohort selects one immutable SHA policy from current
// work. Separate cohorts are admitted on subsequent dispatch passes.
func pendingSourceAnalysisCohort(works []persistence.SourceAnalysisPendingWork) []persistence.SourceAnalysisPendingWork {
	if len(works) == 0 {
		return nil
	}
	shaEnabled := works[0].Work.SHA256Enabled
	cohort := make([]persistence.SourceAnalysisPendingWork, 0, len(works))
	for _, pending := range works {
		if pending.Work.SHA256Enabled == shaEnabled {
			cohort = append(cohort, pending)
		}
	}
	return cohort
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

func currentPendingSourceAnalysisCohort(ctx context.Context, works []persistence.SourceAnalysisPendingWork) []persistence.SourceAnalysisPendingWork {
	current := make([]persistence.SourceAnalysisPendingWork, 0, len(works))
	for _, pending := range works {
		if pendingSourceFileCurrent(ctx, pending) {
			current = append(current, pending)
		}
	}
	return pendingSourceAnalysisCohort(current)
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
			result, hit, err := repository.LookupSourceFingerprint(ctx, digest, version)
			if err != nil {
				return false, fmt.Errorf("lookup pending fingerprint cache: %w", err)
			}
			if hit && result != nil {
				resultID = result.ID
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
