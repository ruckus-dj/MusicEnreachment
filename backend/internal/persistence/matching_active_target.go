package persistence

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// ErrIncomingMatchingTargetInactive distinguishes an exact automatic target
// whose captured members have all acquired a live source link from a stale
// revision or another matching conflict. Current reconciliation settles the
// observed generation without rewriting the raw group, its publication album
// receipts, or any queued run that targets a disjoint group.
var ErrIncomingMatchingTargetInactive = errors.New("incoming matching target is inactive")

// requireActiveIncomingMembers binds the exact ready subset. A formerly active
// member becoming dormant never licenses applying just the surviving subset of
// an old evaluation. A new capture must be evaluated for that subset instead.
func requireActiveIncomingMembers(activity IncomingMembershipActivity, memberIDs []uuid.UUID) error {
	if len(activity.ActiveReady) == 0 {
		return fmt.Errorf("incoming matching target has no active ready member: %w", ErrIncomingMatchingTargetInactive)
	}
	if len(activity.ActiveReady) != len(memberIDs) {
		return fmt.Errorf("incoming matching target membership changed: %w", ErrIncomingMatchConflict)
	}
	active := make(map[uuid.UUID]struct{}, len(activity.ActiveReady))
	for _, memberID := range activity.ActiveReady {
		active[memberID] = struct{}{}
	}
	for _, memberID := range memberIDs {
		if _, ok := active[memberID]; !ok {
			return fmt.Errorf("incoming matching target has a stale member: %w", ErrIncomingMatchConflict)
		}
		delete(active, memberID)
	}
	return nil
}
