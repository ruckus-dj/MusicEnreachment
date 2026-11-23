package persistence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// IncomingActiveRevision binds a revision from the raw grouping projection to
// the exact ready subset that can still be matched. Keeping the raw revision
// inside this witness avoids a second version of the capture identity hash.
func IncomingActiveRevision(raw string, readyCount int, active []uuid.UUID) string {
	if len(active) == readyCount {
		return raw
	}
	members := slices.Clone(active)
	slices.SortFunc(members, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	hash := sha256.New()
	_, _ = hash.Write([]byte("melo:incoming-active:v1\x00" + raw + "\x00"))
	for _, member := range members {
		_, _ = hash.Write(member[:])
	}
	return hex.EncodeToString(hash.Sum(nil))
}

type incomingActiveGroupFence struct {
	RawRevision          string
	ActiveRevision       string
	ActiveMembers        []uuid.UUID
	ActiveUnreadyMembers []uuid.UUID
}

func incomingEvaluationMatchesActiveMembers(ctx context.Context, db bun.IDB, header IncomingMatchEvaluationHeader, fence incomingActiveGroupFence) (bool, error) {
	revision := fence.RawRevision
	if header.MatchingAuthorityMode == "manual_review" && header.FrozenAuthorityVersion == nil {
		revision = fence.ActiveRevision
	}
	if revision != header.GroupRevision || len(fence.ActiveMembers) == 0 {
		return false, nil
	}
	var evaluated []uuid.UUID
	if err := db.NewRaw(`SELECT file_member_id FROM incoming_match_evaluation_member WHERE group_id=? ORDER BY file_member_id`, header.GroupID).Scan(ctx, &evaluated); err != nil {
		return false, fmt.Errorf("read members of incoming evaluation: %w", err)
	}
	return slices.Equal(evaluated, fence.ActiveMembers), nil
}

// The caller holds the source mutation gate before taking the group row lock.
// Raw membership remains the witness for historical seals and unlink scope.
func readIncomingActiveGroupFence(ctx context.Context, tx bun.Tx, groupID uuid.UUID, locking bool) (incomingActiveGroupFence, error) {
	var fence incomingActiveGroupFence
	if groupID == uuid.Nil {
		return fence, ErrIncomingMatchConflict
	}
	query := `SELECT revision FROM incoming_group WHERE id=? FOR SHARE`
	if locking {
		query = `SELECT revision FROM incoming_group WHERE id=? FOR UPDATE`
	}
	if err := tx.NewRaw(query, groupID).Scan(ctx, &fence.RawRevision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fence, ErrIncomingMatchConflict
		}
		return fence, fmt.Errorf("read incoming active group revision: %w", err)
	}
	activity, err := ReadIncomingMembershipActivity(ctx, tx, groupID)
	if err != nil {
		return fence, err
	}
	fence.ActiveMembers = activity.ActiveReady
	fence.ActiveRevision = IncomingActiveRevision(fence.RawRevision, activity.ReadyCount, activity.ActiveReady)
	ready := make(map[uuid.UUID]bool, len(fence.ActiveMembers))
	for _, id := range fence.ActiveMembers {
		ready[id] = true
	}
	for _, id := range activity.ActiveMembers {
		if !ready[id] {
			fence.ActiveUnreadyMembers = append(fence.ActiveUnreadyMembers, id)
		}
	}
	return fence, nil
}

func activeIncomingGroupMemberIDs(fence incomingActiveGroupFence) []uuid.UUID {
	members := make([]uuid.UUID, 0, len(fence.ActiveMembers)+len(fence.ActiveUnreadyMembers))
	members = append(members, fence.ActiveMembers...)
	members = append(members, fence.ActiveUnreadyMembers...)
	slices.SortFunc(members, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	return members
}

func activeIncomingGroupingGroup(group IncomingGroupingGroup, fence incomingActiveGroupFence) IncomingGroupingGroup {
	active := make(map[uuid.UUID]bool, len(fence.ActiveMembers)+len(fence.ActiveUnreadyMembers))
	for _, id := range activeIncomingGroupMemberIDs(fence) {
		active[id] = true
	}
	group.Members = fence.ActiveMembers
	group.UnreadyMembers = fence.ActiveUnreadyMembers
	locations := make([]IncomingGroupingMemberLocation, 0, len(group.Locations))
	for _, location := range group.Locations {
		if active[location.VariantID] {
			locations = append(locations, location)
		}
	}
	group.Locations = locations
	return group
}
