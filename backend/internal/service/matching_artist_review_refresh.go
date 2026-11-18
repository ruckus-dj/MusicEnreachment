package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

const (
	// artistMatchingReviewRefreshPageSize and
	// artistMatchingReviewRefreshPagesPerRun bound one detector tick. The scans
	// are keyset pages, so a large library is never traversed in one pass and a
	// slow tick cannot monopolize the worker beyond these limits.
	artistMatchingReviewRefreshPageSize    = 10
	artistMatchingReviewRefreshPagesPerRun = 3
)

// artistMatchingReviewRefreshStore is the persistence boundary for the runtime
// artist-review refresh detector. The listing is keyset-bounded; the loader
// returns the exact historical version-2 sealed acquisition scope; the
// publisher replaces review data only, without ordinary reconciliation or
// apply work.
type artistMatchingReviewRefreshStore interface {
	ListArtistMatchingReviewRefreshCandidates(context.Context, uuid.UUID, int) ([]persistence.ArtistMatchingReviewRefreshCandidate, error)
	LoadArtistMatchingReviewRefreshScope(context.Context, persistence.ArtistMatchingReviewRefreshCandidate) (persistence.MatchingAcquisitionEvaluationInput, bool, error)
	PublishArtistMatchingReviewRefresh(context.Context, persistence.ArtistMatchingReviewRefreshCandidate, persistence.IncomingMatchEvaluation) (bool, error)
}

// ArtistMatchingReviewRefreshDetector re-evaluates saved artist matching
// reviews after their accepted provider projection or cache dependencies
// change. It reads only committed provider caches, never performs provider
// requests, and never enqueues ordinary reconciliation or apply work: only
// review rows are replaced.
//
// The keyset cursor is process-local. Each tick continues where the previous
// one stopped, scans a bounded number of pages, and wraps to the beginning
// after a completed pass. Failing rows advance the cursor too, so one broken
// review cannot starve the rest of the library.
type ArtistMatchingReviewRefreshDetector struct {
	mu         sync.Mutex
	candidates artistMatchingReviewRefreshStore
	runs       matchingRunFenceReader
	evaluator  *MatchingEvaluationService
	cursor     uuid.UUID
	pageSize   int
	maxPages   int
}

func NewArtistMatchingReviewRefreshDetector(candidates artistMatchingReviewRefreshStore, evaluator *MatchingEvaluationService, runs matchingRunFenceReader) *ArtistMatchingReviewRefreshDetector {
	return &ArtistMatchingReviewRefreshDetector{
		candidates: candidates,
		evaluator:  evaluator,
		runs:       runs,
		pageSize:   artistMatchingReviewRefreshPageSize,
		maxPages:   artistMatchingReviewRefreshPagesPerRun,
	}
}

// Refresh advances the detector cursor through bounded candidate pages. Errors
// for individual rows are aggregated; the cursor still advances past them, so a
// persistently failing row is retried on a later pass instead of blocking the
// scan. Matching conflicts and deferred evaluations are benign: another
// authority, grouping, or evaluation already superseded the saved review.
func (detector *ArtistMatchingReviewRefreshDetector) Refresh(ctx context.Context) error {
	if detector == nil || detector.candidates == nil || detector.runs == nil || detector.evaluator == nil ||
		detector.evaluator.groups == nil || detector.evaluator.captures == nil || detector.evaluator.provider == nil ||
		detector.evaluator.results == nil || detector.evaluator.config == nil {
		return fmt.Errorf("artist matching review refresh detector is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	detector.mu.Lock()
	defer detector.mu.Unlock()
	pageSize, maxPages := detector.pageSize, detector.maxPages
	if pageSize < 1 {
		pageSize = artistMatchingReviewRefreshPageSize
	}
	if maxPages < 1 {
		maxPages = artistMatchingReviewRefreshPagesPerRun
	}
	var problems []error
	cursor, reachedEnd := detector.cursor, false
scan:
	for page := 0; page < maxPages; page++ {
		candidates, err := detector.candidates.ListArtistMatchingReviewRefreshCandidates(ctx, cursor, pageSize)
		if err != nil {
			problems = append(problems, fmt.Errorf("list artist matching review refresh candidates after %s: %w", cursor, err))
			break
		}
		if len(candidates) == 0 {
			reachedEnd = true
			break
		}
		for _, candidate := range candidates {
			if err := ctx.Err(); err != nil {
				problems = append(problems, err)
				break scan
			}
			if err := detector.refreshCandidate(ctx, candidate); err != nil {
				problems = append(problems, err)
			}
			cursor = candidate.GroupID
			detector.cursor = cursor
		}
		if len(candidates) < pageSize {
			reachedEnd = true
			break
		}
	}
	if reachedEnd {
		// A completed pass restarts from the first group so rows behind the
		// original cursor are examined on the next tick.
		detector.cursor = uuid.Nil
	}
	return errors.Join(problems...)
}

func (detector *ArtistMatchingReviewRefreshDetector) refreshCandidate(ctx context.Context, candidate persistence.ArtistMatchingReviewRefreshCandidate) error {
	if candidate.GroupID == uuid.Nil || candidate.RunID == uuid.Nil || candidate.EvaluationRevision == "" {
		return fmt.Errorf("artist matching review refresh candidate identity is invalid")
	}
	if err := validateArtistReviewProviderFence(candidate.Provider); err != nil {
		return fmt.Errorf("artist matching review refresh candidate for group %s is invalid: %w", candidate.GroupID, err)
	}
	// Checking the scoped projection first avoids reloading grouping captures
	// and scoring every unchanged album on every pass.
	provider := candidate.Provider
	cacheKeys := make([]string, 0, len(provider.CacheRevisions))
	for key := range provider.CacheRevisions {
		cacheKeys = append(cacheKeys, key)
	}
	snapshot, err := detector.evaluator.provider.GetMusicBrainzMatchingSnapshot(
		ctx, provider.Endpoint, provider.ConfigurationIdentity, provider.ReleaseKeys, cacheKeys,
	)
	if err != nil {
		return fmt.Errorf("check artist matching review projection for group %s: %w", candidate.GroupID, err)
	}
	if sameProvider, unchanged := artistReviewProviderDependencies(provider, snapshot); !sameProvider || unchanged {
		return nil
	}
	input, exists, err := detector.candidates.LoadArtistMatchingReviewRefreshScope(ctx, candidate)
	if err != nil {
		if isBenignArtistMatchingReviewRefreshError(err) {
			return nil
		}
		return fmt.Errorf("load artist matching review refresh scope for group %s: %w", candidate.GroupID, err)
	}
	if !exists {
		return nil
	}
	if input.SchemaVersion != 2 || len(input.GroupIDs) != 1 || input.GroupIDs[0] != candidate.GroupID {
		return fmt.Errorf("artist matching review refresh scope for group %s is not the sealed version-2 target", candidate.GroupID)
	}
	// Evaluate from fresh reads. The publisher checks the exact sealed key set
	// and revalidates the evaluated projection, not the probe snapshot above.
	evaluator := *detector.evaluator
	groups, ok := evaluator.groups.(*IncomingGroups)
	if !ok {
		return fmt.Errorf("artist matching review refresh requires read-only grouping")
	}
	evaluator.groups = artistReviewGroupingReader{groups: groups}
	prepared, err := evaluator.PrepareSealedForRun(ctx, detector.runs, candidate.RunID, input)
	if err != nil {
		if isBenignArtistMatchingReviewRefreshError(err) {
			return nil
		}
		return fmt.Errorf("evaluate artist matching review refresh for group %s: %w", candidate.GroupID, err)
	}
	revision, revisionExists := prepared.ExpectedRevisions[candidate.GroupID]
	if len(prepared.ExpectedRevisions) != 1 || !revisionExists || !sameOptionalRevision(revision, &candidate.EvaluationRevision) {
		// A newer evaluation superseded this candidate while it was prepared.
		return nil
	}
	if len(prepared.evaluation.Groups) != 1 || prepared.evaluation.Groups[0].GroupID != candidate.GroupID {
		return fmt.Errorf("artist matching review refresh evaluation for group %s escaped its sealed scope", candidate.GroupID)
	}
	if _, err := detector.candidates.PublishArtistMatchingReviewRefresh(ctx, candidate, prepared.evaluation); err != nil {
		if isBenignArtistMatchingReviewRefreshError(err) {
			return nil
		}
		return fmt.Errorf("publish artist matching review refresh for group %s: %w", candidate.GroupID, err)
	}
	return nil
}

type artistReviewGroupingReader struct {
	groups *IncomingGroups
}

func (reader artistReviewGroupingReader) Snapshot(ctx context.Context) (IncomingGroupingSnapshot, error) {
	return reader.groups.snapshot(ctx, false)
}

// artistReviewProviderDependencies compares the saved provider fence with the
// committed snapshot read for the same historical scope. sameProvider is false
// when the snapshot belongs to another source or configuration; such a snapshot
// must never replace the saved review. With the same provider, unchanged
// reports whether both the accepted projection digest and every exact cache
// dependency revision still match.
func artistReviewProviderDependencies(fence persistence.IncomingMatchProviderFence, snapshot persistence.ProviderMatchingSnapshot) (sameProvider, unchanged bool) {
	if snapshot.Source.ID == uuid.Nil ||
		snapshot.Source.ID != fence.ProviderSourceID ||
		snapshot.Source.ProviderID != fence.ProviderID ||
		snapshot.Source.Namespace != fence.Namespace ||
		snapshot.Source.Endpoint != fence.Endpoint ||
		snapshot.Source.ConfigurationIdentity != fence.ConfigurationIdentity {
		return false, false
	}
	if snapshot.AcceptedProjectionRevision != fence.AcceptedProjectionRevision {
		return true, false
	}
	expected := fence.CacheRevisions
	if len(expected) == 0 && fence.CacheKey != "" {
		expected = map[string]*string{fence.CacheKey: fence.CacheRevision}
	}
	if len(snapshot.Responses) != len(expected) {
		return true, false
	}
	for key, revision := range expected {
		response, present := snapshot.Responses[key]
		if !present || !sameOptionalRevision(matchingCachedRevision(response), revision) {
			return true, false
		}
	}
	return true, true
}

func validateArtistReviewProviderFence(fence persistence.IncomingMatchProviderFence) error {
	if fence.ProviderID == uuid.Nil || fence.ProviderSourceID == uuid.Nil || fence.Namespace == "" ||
		fence.Endpoint == "" || fence.ConfigurationIdentity == "" || fence.AcceptedProjectionRevision == "" {
		return fmt.Errorf("provider identity is incomplete")
	}
	digest, err := hex.DecodeString(fence.AcceptedProjectionRevision)
	if err != nil || len(digest) != sha256.Size {
		return fmt.Errorf("provider projection digest is invalid")
	}
	return nil
}

// isBenignArtistMatchingReviewRefreshError treats a superseded authority or a
// deferred evaluation as a normal outcome of a runtime refresh: another writer
// already owns the current state, so this candidate must simply be skipped.
func isBenignArtistMatchingReviewRefreshError(err error) bool {
	return errors.Is(err, persistence.ErrIncomingMatchConflict) ||
		errors.Is(err, persistence.ErrMatchingTargetNeedsRefresh) ||
		errors.Is(err, ErrMatchingEvaluationDeferred)
}
