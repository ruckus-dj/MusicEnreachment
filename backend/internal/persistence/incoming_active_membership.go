package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// IncomingMembershipActivity is the transaction-local active/dormant split of a
// persisted incoming group. A member is dormant when its canonical media
// identity (the source work's source_work_variant_identity anchor) already has
// a live track_source association. Activity is independent of selected,
// compatible, and decision_method: any live association hides the identity.
//
// All persisted location fences of a coalesced member follow the member's
// activity, including locations that have not yet acquired a canonical anchor.
type IncomingMembershipActivity struct {
	GroupID          uuid.UUID
	ActiveMembers    []uuid.UUID
	ActiveReady      []uuid.UUID
	ReadyCount       int
	DormantMembers   []uuid.UUID
	ActiveLocations  []IncomingGroupingMemberLocation
	DormantLocations []IncomingGroupingMemberLocation
}

// ReadIncomingMembershipActivity classifies one group's persisted membership.
// It performs no writes and does not advance incoming_grouping_state; callers
// may supply their own transaction so the check is consistent with the
// mutation it fences.
func ReadIncomingMembershipActivity(ctx context.Context, db bun.IDB, groupID uuid.UUID) (IncomingMembershipActivity, error) {
	result := IncomingMembershipActivity{
		GroupID:          groupID,
		ActiveMembers:    []uuid.UUID{},
		ActiveReady:      []uuid.UUID{},
		DormantMembers:   []uuid.UUID{},
		ActiveLocations:  []IncomingGroupingMemberLocation{},
		DormantLocations: []IncomingGroupingMemberLocation{},
	}
	if groupID == uuid.Nil {
		return result, errors.New("incoming membership activity requires a group")
	}
	var rows []struct {
		VariantID      uuid.UUID  `bun:"variant_id"`
		RootID         uuid.UUID  `bun:"source_root_id"`
		WorkID         uuid.UUID  `bun:"work_id"`
		LocationID     *uuid.UUID `bun:"location_id"`
		ConfiguredPath string     `bun:"configured_path"`
		InventoryPath  string     `bun:"inventory_path"`
		RelativePath   string     `bun:"relative_path"`
		SizeBytes      int64      `bun:"size_bytes"`
		Mtime          time.Time  `bun:"mtime"`
		Linked         bool       `bun:"linked"`
		Ready          bool       `bun:"ready"`
	}
	if err := db.NewRaw(`WITH member_activity AS (
			SELECT member.group_id, member.variant_id, member.ready, EXISTS (
				SELECT 1 FROM incoming_group_member_location witness
				JOIN source_work_variant_identity identity ON identity.work_id=witness.work_id
				JOIN track_source link ON link.media_variant_id=identity.media_variant_id
				WHERE witness.group_id=member.group_id AND witness.variant_id=member.variant_id
			) AS linked
			FROM incoming_group_member member WHERE member.group_id=?
		)
		SELECT member.variant_id, member.ready, member.linked, location.source_root_id, location.work_id,
			location.location_id, location.configured_path, location.inventory_path, location.relative_path,
			location.size_bytes, location.mtime
		FROM member_activity member
		LEFT JOIN incoming_group_member_location location
			ON location.group_id=member.group_id AND location.variant_id=member.variant_id
		ORDER BY member.variant_id, location.source_root_id, location.relative_path`, groupID).Scan(ctx, &rows); err != nil {
		return result, fmt.Errorf("read incoming membership activity: %w", err)
	}
	var previous uuid.UUID
	for i, location := range rows {
		if i == 0 || previous != location.VariantID {
			previous = location.VariantID
			if location.Ready {
				result.ReadyCount++
			}
			if location.Linked {
				result.DormantMembers = append(result.DormantMembers, location.VariantID)
			} else {
				result.ActiveMembers = append(result.ActiveMembers, location.VariantID)
				if location.Ready {
					result.ActiveReady = append(result.ActiveReady, location.VariantID)
				}
			}
		}
		if location.LocationID == nil {
			continue
		}
		fence := IncomingGroupingMemberLocation{
			VariantID:      location.VariantID,
			RootID:         location.RootID,
			WorkID:         location.WorkID,
			LocationID:     *location.LocationID,
			ConfiguredPath: location.ConfiguredPath,
			InventoryPath:  location.InventoryPath,
			RelativePath:   location.RelativePath,
			SizeBytes:      location.SizeBytes,
			Mtime:          location.Mtime,
		}
		if location.Linked {
			result.DormantLocations = append(result.DormantLocations, fence)
		} else {
			result.ActiveLocations = append(result.ActiveLocations, fence)
		}
	}
	return result, nil
}

// ReadIncomingWorkActivity reports whether each supplied source work still has
// an active incoming identity. A work is active when none of its canonical
// media identities has a live track_source association. Missing works carry no
// identity and are reported active; callers must still validate the source
// rows they intend to mutate through their normal locks.
func ReadIncomingWorkActivity(ctx context.Context, db bun.IDB, workIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	active := make(map[uuid.UUID]bool, len(workIDs))
	for _, workID := range workIDs {
		active[workID] = true
	}
	if len(workIDs) == 0 {
		return active, nil
	}
	var rows []struct {
		WorkID uuid.UUID `bun:"work_id"`
		Linked bool      `bun:"linked"`
	}
	if err := db.NewRaw(`SELECT work.id AS work_id,
		EXISTS (
			SELECT 1 FROM source_work_variant_identity identity
			JOIN track_source link ON link.media_variant_id=identity.media_variant_id
			WHERE identity.work_id=work.id
		) AS linked
		FROM source_analysis_work work
		WHERE work.id IN (?)
		ORDER BY work.id`, bun.List(workIDs)).Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("read incoming work activity: %w", err)
	}
	for _, row := range rows {
		active[row.WorkID] = !row.Linked
	}
	return active, nil
}
