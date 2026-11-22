package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

const (
	// matchingLocalRecheckRunLimit bounds provider work per tick. Later ticks
	// continue with the next earliest due obligations, so a large backlog never
	// monopolizes one worker.
	matchingLocalRecheckRunLimit = 4

	// matchingLocalRecheckOutageDelay asks the provider again soon enough to
	// notice recovery without treating the outage as a decision.
	matchingLocalRecheckOutageDelay = 15 * time.Minute

	// matchingLocalRecheckRetryDelay leaves the exact source veto in place while
	// accepted evidence cannot decide the comparison.
	matchingLocalRecheckRetryDelay = time.Hour
)

// matchingLocalRecheckRepository is the persistence boundary for the independent
// due recheck. Settlement revalidates the exact target, the accepted provider
// evidence and the Local catalog inside its own transaction; this service never
// writes those facts itself. Every method keeps the exact source veto unless an
// explicit settlement succeeds.
type matchingLocalRecheckRepository interface {
	ListDueMatchingLocalRechecks(context.Context, int) ([]uuid.UUID, error)
	RetireSupersededMatchingLocalRecheck(context.Context, uuid.UUID) (bool, error)
	ReadDueMatchingLocalRecheck(context.Context, uuid.UUID) (*persistence.MatchingLocalRecheckTarget, error)
	ReadMatchingLocalRecheckPublicationCandidates(context.Context, persistence.MatchingLocalRecheckTarget, persistence.MatchingLayoutDecision, uuid.UUID) ([]persistence.MatchingLocalRecheckPublicationCandidate, error)
	RetainMatchingLocalAssignment(context.Context, persistence.MatchingLocalRecheckTarget, []persistence.MatchingProviderLayoutEvidence) error
	PostponeMatchingLocalRecheck(context.Context, persistence.MatchingLocalRecheckTarget, time.Time) error
	ApplyMatchingLocalRecheck(context.Context, persistence.MatchingLocalRecheckTarget, []persistence.MatchingProviderLayoutEvidence, persistence.MatchingLayoutDecision, persistence.MatchingRecheckPublicationPlan, persistence.RiverInserter) error
}

// matchingLocalRecheckPublicationPreparer freezes one bounded source-free
// provider-tag rewrite (or an exact no-op proof) for an already-published
// incumbent position. It performs no admission; only the winning comparison
// transaction may bind the preparation to a durable owner.
type matchingLocalRecheckPublicationPreparer interface {
	PrepareMatchingRecheckPublication(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*persistence.MatchingRecheckPublicationPreparation, error)
	PrepareMatchingRecheckMovedPublication(context.Context, persistence.MatchingRecheckMoveSnapshot) (persistence.PublicationAdmission, error)
	FinalizeMatchingRecheckMovedOutputs(context.Context) error
}

// matchingLocalRecheckMusicBrainzRetriever recovers the exact accepted
// MusicBrainz evidence for a saved Local recheck. It performs no writes and
// never applies a layout.
type matchingLocalRecheckMusicBrainzRetriever interface {
	RetrieveMatchingLocalRecheckMusicBrainz(context.Context, []persistence.IncomingGroupingCapture, float64) (MatchingLocalRecheckMusicBrainzRetrieval, error)
}

// MatchingLocalRecheckService advances due Local matching rechecks. For each
// obligation it recovers MusicBrainz evidence, compares the exact incumbent
// Local sum with the new layout on the same saved inputs, and either settles the
// veto (retain or apply) or postpones it. An outage, a definitive no-result
// without accepted evidence, an indeterminate provider answer, and a conflict
// are all distinct from a proved comparison and never release the veto.
type MatchingLocalRecheckService struct {
	repository   matchingLocalRecheckRepository
	retriever    matchingLocalRecheckMusicBrainzRetriever
	publications matchingLocalRecheckPublicationPreparer
	river        persistence.RiverInserter
	now          func() time.Time
}

func NewMatchingLocalRecheckService(repository matchingLocalRecheckRepository, retriever matchingLocalRecheckMusicBrainzRetriever) *MatchingLocalRecheckService {
	return &MatchingLocalRecheckService{repository: repository, retriever: retriever, now: time.Now}
}

// WithPublicationAdmission enables the same-position republication bridge. The
// route stays disabled until both a preparer and a River inserter are supplied:
// without them a winning comparison that meets an existing CURRENT postpones
// instead of silently skipping the provider rewrite.
func (service *MatchingLocalRecheckService) WithPublicationAdmission(preparer matchingLocalRecheckPublicationPreparer, river persistence.RiverInserter) *MatchingLocalRecheckService {
	if service != nil {
		service.publications = preparer
		service.river = river
	}
	return service
}

// RunDueMatchingLocalRechecks processes a bounded batch of due obligations. A
// broken row does not stop the rest of the batch; unexpected failures are
// aggregated so the periodic job is retried as a whole.
func (service *MatchingLocalRecheckService) RunDueMatchingLocalRechecks(ctx context.Context) error {
	if service == nil || service.repository == nil || service.retriever == nil {
		return fmt.Errorf("local matching recheck service is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ids, err := service.repository.ListDueMatchingLocalRechecks(ctx, matchingLocalRecheckRunLimit)
	if err != nil {
		return fmt.Errorf("list due Local matching rechecks: %w", err)
	}
	problems := make([]error, 0)
	if service.publications != nil {
		if err := service.publications.FinalizeMatchingRecheckMovedOutputs(ctx); err != nil {
			problems = append(problems, fmt.Errorf("finalize Local recheck moved outputs: %w", err))
		}
	}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			problems = append(problems, err)
			break
		}
		retired, err := service.repository.RetireSupersededMatchingLocalRecheck(ctx, id)
		if err != nil {
			problems = append(problems, fmt.Errorf("retire superseded Local matching recheck %s: %w", id, err))
			continue
		}
		if retired {
			continue
		}
		target, err := service.repository.ReadDueMatchingLocalRecheck(ctx, id)
		if err != nil {
			problems = append(problems, fmt.Errorf("read due Local matching recheck %s: %w", id, err))
			continue
		}
		if target == nil {
			continue // Already settled, postponed or superseded by another writer.
		}
		if err := service.recheck(ctx, target); err != nil {
			problems = append(problems, fmt.Errorf("recheck Local matching assignment %s: %w", id, err))
		}
	}
	return errors.Join(problems...)
}

func (service *MatchingLocalRecheckService) recheck(ctx context.Context, target *persistence.MatchingLocalRecheckTarget) error {
	retrieval, err := service.retriever.RetrieveMatchingLocalRecheckMusicBrainz(ctx, target.Captures, target.Threshold)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// A provider outage and an incomplete answer both leave the exact veto in
		// place; only how soon the provider is asked again differs.
		delay := matchingLocalRecheckOutageDelay
		if errors.Is(err, ErrMatchingLocalRecheckMusicBrainzIncomplete) {
			delay = matchingLocalRecheckRetryDelay
		}
		return errors.Join(fmt.Errorf("retrieve MusicBrainz Local recheck evidence: %w", err), service.postpone(ctx, *target, delay))
	}
	if len(retrieval.Evidence) == 0 {
		// A no-result without accepted provider evidence cannot retire the veto:
		// settlement must be able to revalidate that exact evidence later.
		return service.postpone(ctx, *target, matchingLocalRecheckRetryDelay)
	}
	if retrieval.NoResult {
		return service.retain(ctx, *target, retrieval.Evidence)
	}
	result, err := RecheckMatchingLayout(ctx, MatchingLayoutRecheckInput{
		AuthorityMode:           "automatic",
		Captures:                target.Captures,
		MusicBrainzShortlist:    retrieval.Shortlist,
		MusicBrainzSnapshot:     retrieval.Snapshot,
		MusicBrainzSearchKeys:   retrieval.SearchKeys,
		CurrentLocal:            target.Local,
		CurrentLocalAssignments: target.Assignments,
		Threshold:               target.Threshold,
	})
	if err != nil {
		return errors.Join(fmt.Errorf("compare Local and MusicBrainz layouts: %w", err), service.postpone(ctx, *target, matchingLocalRecheckRetryDelay))
	}
	switch result.Status {
	case MatchingLayoutRecheckMusicBrainz:
		decision, err := result.MusicBrainz.DurableDecision()
		if err != nil {
			return errors.Join(fmt.Errorf("project MusicBrainz layout decision: %w", err), service.postpone(ctx, *target, matchingLocalRecheckRetryDelay))
		}
		if err := decision.Validate(MatchingLayoutPolicyVersion, target.Threshold, matchingLocalRecheckMembers(target.Captures), retrieval.Snapshot.Releases); err != nil {
			return errors.Join(fmt.Errorf("validate MusicBrainz layout decision: %w", err), service.postpone(ctx, *target, matchingLocalRecheckRetryDelay))
		}
		return service.apply(ctx, *target, retrieval.Evidence, decision)
	case MatchingLayoutRecheckRetained:
		return service.retain(ctx, *target, retrieval.Evidence)
	default:
		// Complete accepted tracklists may prove that no candidate fits. A
		// malformed/incomplete provider answer or missing Local baseline
		// cannot make that decision.
		if result.MusicBrainz.Decision.Status == MatchingLayoutSelectionNone {
			return service.retain(ctx, *target, retrieval.Evidence)
		}
		return service.postpone(ctx, *target, matchingLocalRecheckRetryDelay)
	}
}

func (service *MatchingLocalRecheckService) retain(ctx context.Context, target persistence.MatchingLocalRecheckTarget, evidence []persistence.MatchingProviderLayoutEvidence) error {
	if err := service.repository.RetainMatchingLocalAssignment(ctx, target, evidence); err != nil {
		if errors.Is(err, persistence.ErrIncomingMatchConflict) {
			return service.postpone(ctx, target, matchingLocalRecheckRetryDelay)
		}
		return fmt.Errorf("retain Local matching assignment %s: %w", target.ID, err)
	}
	return nil
}

func (service *MatchingLocalRecheckService) apply(ctx context.Context, target persistence.MatchingLocalRecheckTarget, evidence []persistence.MatchingProviderLayoutEvidence, decision persistence.MatchingLayoutDecision) error {
	plan, err := service.publicationPlan(ctx, target, decision, evidence[len(evidence)-1].ProviderSourceID)
	if err != nil {
		if errors.Is(err, persistence.ErrPublicationAdmissionConflict) {
			return service.postpone(ctx, target, matchingLocalRecheckRetryDelay)
		}
		return fmt.Errorf("prepare same-position republication for Local matching recheck %s: %w", target.ID, err)
	}
	if err := service.repository.ApplyMatchingLocalRecheck(ctx, target, evidence, decision, plan, service.river); err != nil {
		if errors.Is(err, persistence.ErrIncomingMatchConflict) || errors.Is(err, persistence.ErrPublicationAdmissionConflict) {
			slog.DebugContext(ctx, "postpone Local recheck after winning layout lost authority", "recheck", target.ID, "cause", err)
			return service.postpone(ctx, target, matchingLocalRecheckRetryDelay)
		}
		return fmt.Errorf("apply MusicBrainz layout for Local matching recheck %s: %w", target.ID, err)
	}
	return nil
}

// publicationPlan runs the bounded, read-only same-position preflight. With the
// bridge disabled it returns an empty plan, so a winning comparison that meets
// an existing CURRENT still postpones instead of claiming a no-op it did not
// prove.
func (service *MatchingLocalRecheckService) publicationPlan(ctx context.Context, target persistence.MatchingLocalRecheckTarget, decision persistence.MatchingLayoutDecision, providerSourceID uuid.UUID) (persistence.MatchingRecheckPublicationPlan, error) {
	plan := persistence.MatchingRecheckPublicationPlan{}
	if service.publications == nil {
		return plan, nil
	}
	if service.river == nil {
		return plan, errors.New("same-position republication requires a River inserter")
	}
	plan.PrepareMove = service.publications.PrepareMatchingRecheckMovedPublication
	candidates, err := service.repository.ReadMatchingLocalRecheckPublicationCandidates(ctx, target, decision, providerSourceID)
	if err != nil {
		return plan, fmt.Errorf("read same-position CURRENT candidates: %w", err)
	}
	for _, candidate := range candidates {
		preparation, err := service.publications.PrepareMatchingRecheckPublication(ctx, candidate.ReleaseID, candidate.PositionID, candidate.Generation)
		if err != nil {
			return plan, fmt.Errorf("prepare same-position publication at %s: %w", candidate.PositionID, err)
		}
		if preparation == nil {
			return plan, fmt.Errorf("prepare same-position publication at %s returned no preparation", candidate.PositionID)
		}
		switch {
		case preparation.Admission != nil && preparation.NoOp != nil:
			return plan, fmt.Errorf("prepare same-position publication at %s returned several preparations", candidate.PositionID)
		case preparation.Admission != nil:
			plan.Publications = append(plan.Publications, *preparation.Admission)
		case preparation.NoOp != nil:
			plan.NoOps = append(plan.NoOps, *preparation.NoOp)
		default:
			return plan, fmt.Errorf("prepare same-position publication at %s returned no decision", candidate.PositionID)
		}
	}
	return plan, nil
}

// postpone advances only the due time, so the exact source veto remains intact.
func (service *MatchingLocalRecheckService) postpone(ctx context.Context, target persistence.MatchingLocalRecheckTarget, delay time.Duration) error {
	now := service.now
	if now == nil {
		now = time.Now
	}
	next := now().UTC().Add(delay)
	if err := service.repository.PostponeMatchingLocalRecheck(ctx, target, next); err != nil {
		return fmt.Errorf("postpone Local matching recheck %s: %w", target.ID, err)
	}
	return nil
}

// matchingLocalRecheckMembers is the exact saved file set used by both sides of
// the comparison. Several captures may share one member.
func matchingLocalRecheckMembers(captures []persistence.IncomingGroupingCapture) []uuid.UUID {
	members := make([]uuid.UUID, 0, len(captures))
	seen := make(map[uuid.UUID]struct{}, len(captures))
	for _, capture := range captures {
		if _, ok := seen[capture.VariantID]; ok {
			continue
		}
		seen[capture.VariantID] = struct{}{}
		members = append(members, capture.VariantID)
	}
	return members
}
