package persistence

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// sourceHoldExists checks durable publication and cleanup ownership without
// interpreting an owner's state. A publication hold or a retained cleanup owner
// remains a pin until its owner explicitly releases it, so no terminal state is
// assumed to free the source identity.
func sourceHoldExists(ctx context.Context, database bun.IDB, rootID, locationID, workID uuid.UUID) (bool, error) {
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
	condition := strings.Join(predicates, " OR ")
	// Both owner tables expose the same identity columns, so one predicate list
	// serves both branches. Each branch owns its placeholders, so the arguments
	// are appended twice; the table names are constants, not interpolated input.
	query := `SELECT EXISTS (SELECT 1 FROM publication_source_hold WHERE ` + condition +
		`) OR EXISTS (SELECT 1 FROM source_cleanup_owner WHERE ` + condition + `)`
	queryArgs := make([]any, 0, len(args)*2)
	queryArgs = append(queryArgs, args...)
	queryArgs = append(queryArgs, args...)
	var exists bool
	err := database.NewRaw(query, queryArgs...).Scan(ctx, &exists)
	return exists, err
}
