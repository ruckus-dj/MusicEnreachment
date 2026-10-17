package persistence

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// sourceHoldExists checks durable publication ownership without interpreting the
// operation's state. A hold remains a pin until its owner explicitly releases it.
func sourceHoldExists(ctx context.Context, database bun.IDB, rootID, locationID, workID uuid.UUID) (bool, error) {
	var exists bool
	var predicates []string
	var args []any
	// A root-only request protects the entire source tree (for example, root
	// deletion). When a location or work identity is available, match that
	// narrower identity instead so a hold elsewhere under the same root does not
	// block an unrelated mutation.
	if locationID == uuid.Nil && workID == uuid.Nil && rootID != uuid.Nil {
		predicates = append(predicates, "source_root_id = ?")
		args = append(args, rootID)
	}
	if locationID != uuid.Nil {
		predicates = append(predicates, "source_location_id = ?")
		args = append(args, locationID)
	}
	if workID != uuid.Nil {
		predicates = append(predicates, "work_id = ?")
		args = append(args, workID)
	}
	if len(predicates) == 0 {
		return false, nil
	}
	query := `SELECT EXISTS (SELECT 1 FROM publication_source_hold WHERE ` + strings.Join(predicates, " OR ") + ")"
	err := database.NewRaw(query, args...).Scan(ctx, &exists)
	return exists, err
}
