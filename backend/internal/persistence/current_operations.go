package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

const currentOperationsPageSize = 50

type CurrentOperationsFilter struct {
	Status    string
	Kind      string
	Query     string
	Sort      string
	Direction string
	// Locale selects the localized wording used to rank operations when Sort is
	// "operation". Target labels are user data and are ordered without it.
	Locale string
	Offset int
}

// currentOperationsOrder is the validated ordering of the read model. The
// direction is the SQL keyword, so it is only ever assembled from this switch.
type currentOperationsOrder struct {
	field     string
	direction string
	locale    string
}

type OperationTargetRef struct {
	Kind       string
	RootID     *uuid.UUID
	LocationID *uuid.UUID
	ReleaseID  *uuid.UUID
	PositionID *uuid.UUID
	GroupID    *uuid.UUID
	ArtistID   *uuid.UUID
}

type CurrentOperationRow struct {
	Operation             `bun:",extend"`
	TargetLabel           string              `bun:"target_label"`
	TargetRef             *OperationTargetRef `bun:"-"`
	MatchingRecheckMember bool                `bun:"matching_recheck_member"`
}

type currentOperationTargetRow struct {
	CurrentOperationRow `bun:",extend"`
	RootID              *uuid.UUID `bun:"resolved_root_id"`
	LocationID          *uuid.UUID `bun:"resolved_location_id"`
	ReleaseID           *uuid.UUID `bun:"resolved_release_id"`
	PositionID          *uuid.UUID `bun:"resolved_position_id"`
	GroupID             *uuid.UUID `bun:"resolved_group_id"`
	ArtistID            *uuid.UUID `bun:"resolved_artist_id"`
}

// ListCurrentOperations is a read-only, bounded list for the Operations screen.
// Quiesced runs are already known to be superseded; the projection neither
// rewrites their history nor interprets a River job state as an operation state.
// Ordering applies to the whole filtered set before the page is cut.
func (repo *SetupManagerRepository) ListCurrentOperations(
	ctx context.Context, filter CurrentOperationsFilter,
) ([]CurrentOperationRow, int, error) {
	states, kinds, err := currentOperationsScope(filter)
	if err != nil {
		return nil, 0, err
	}
	order, err := currentOperationsOrderFor(filter)
	if err != nil {
		return nil, 0, err
	}
	query := strings.TrimSpace(filter.Query)
	needsTargets := query != "" || order.field == "target"
	offset := int64(filter.Offset)
	rows := make([]currentOperationTargetRow, 0, currentOperationsPageSize)
	var total int64
	err = repo.db.RunInTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelRepeatableRead,
		ReadOnly:  true,
	}, func(ctx context.Context, tx bun.Tx) error {
		filtered := func(withTargets bool) *bun.SelectQuery {
			base := tx.NewSelect().TableExpr("operation AS op").
				Where("op.state IN (?)", bun.List(states)).
				Where(`NOT EXISTS (SELECT 1 FROM matching_run retired
					WHERE retired.operation_id=op.id AND retired.quiesced_at IS NOT NULL)`)
			if len(kinds) != 0 {
				base.Where("op.kind IN (?)", bun.List(kinds))
			}
			if withTargets {
				base.Join(currentOperationTargets)
			}
			if query != "" {
				base.Where("strpos(lower("+currentOperationTargetLabel+"), lower(?)) > 0", query)
			}
			return base
		}
		var countErr error
		total, countErr = filtered(query != "").Count(ctx)
		if countErr != nil {
			return fmt.Errorf("count current operations: %w", countErr)
		}
		if offset >= total {
			return nil
		}
		// Target labels are resolved for the entire filtered set when the order
		// or the query depends on them. Otherwise they are resolved only for the
		// chosen page after the cheap columns have ordered and cut it.
		var page *bun.SelectQuery
		if needsTargets {
			page = filtered(true).ColumnExpr("op.*").
				ColumnExpr(currentOperationTargetLabel + " AS target_label").
				ColumnExpr(currentOperationTargetRefs)
		} else {
			page = filtered(false).ColumnExpr("op.*")
			applyCurrentOperationsOrder(page, order).
				Limit(currentOperationsPageSize).Offset(offset)
		}
		selectPage := tx.NewSelect().TableExpr("(?) AS op", page).ColumnExpr("op.*").
			ColumnExpr(`op.state='failed' AND EXISTS (SELECT 1 FROM matching_local_recheck_publication m
				WHERE m.operation_id=op.id AND m.committed_at IS NULL AND EXISTS (
					SELECT 1 FROM matching_local_recheck_publication done
					WHERE done.recheck_id=m.recheck_id AND done.committed_at IS NOT NULL)) AS matching_recheck_member`)
		if !needsTargets {
			selectPage.Join(currentOperationTargets).
				ColumnExpr(currentOperationTargetLabel + " AS target_label").
				ColumnExpr(currentOperationTargetRefs)
		}
		applyCurrentOperationsOrder(selectPage, order)
		if needsTargets {
			selectPage.Limit(currentOperationsPageSize).Offset(offset)
		}
		return selectPage.Scan(ctx, &rows)
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list current operations: %w", err)
	}
	result := make([]CurrentOperationRow, 0, len(rows))
	for _, row := range rows {
		projected := row.CurrentOperationRow
		projected.TargetRef = row.targetRef()
		result = append(result, projected)
	}
	return result, int(total), nil
}

func currentOperationsOrderFor(filter CurrentOperationsFilter) (currentOperationsOrder, error) {
	order := currentOperationsOrder{field: "created", direction: "DESC", locale: "en"}
	switch filter.Sort {
	case "":
	case "created", "target", "operation", "state":
		order.field = filter.Sort
	default:
		return currentOperationsOrder{}, fmt.Errorf("invalid current operations sort")
	}
	switch filter.Direction {
	case "", "desc":
	case "asc":
		order.direction = "ASC"
	default:
		return currentOperationsOrder{}, fmt.Errorf("invalid current operations direction")
	}
	switch filter.Locale {
	case "":
	case "en", "ru":
		order.locale = filter.Locale
	default:
		return currentOperationsOrder{}, fmt.Errorf("invalid current operations locale")
	}
	return order, nil
}

// applyCurrentOperationsOrder orders a query that exposes the read model as
// `op`, including `target_label` once it has been resolved. Equal ordering keys
// fall back to the newest operation so pages stay deterministic.
func applyCurrentOperationsOrder(query *bun.SelectQuery, order currentOperationsOrder) *bun.SelectQuery {
	switch order.field {
	case "state":
		return query.OrderExpr("CASE op.state WHEN 'failed' THEN 0 WHEN 'running' THEN 1 WHEN 'queued' THEN 2 ELSE 3 END " + order.direction).
			OrderExpr("op.created_at DESC, op.id DESC")
	case "operation":
		expression, args := currentOperationKindSortExpr(order.locale)
		return query.OrderExpr("("+expression+") "+order.direction, args...).
			OrderExpr("op.created_at DESC, op.id DESC")
	case "target":
		return query.OrderExpr("lower(NULLIF(op.target_label, '')) " + order.direction + " NULLS LAST").
			OrderExpr("op.created_at DESC, op.id DESC")
	default:
		return query.OrderExpr("op.created_at " + order.direction + ", op.id " + order.direction)
	}
}

func currentOperationsScope(filter CurrentOperationsFilter) ([]string, []string, error) {
	if filter.Offset < 0 {
		return nil, nil, fmt.Errorf("current operations offset must be nonnegative")
	}
	var states []string
	switch filter.Status {
	case "", "attention":
		states = []string{"failed", "running"}
	case "all":
		states = []string{"failed", "running", "queued"}
	case "failed", "running", "queued":
		states = []string{filter.Status}
	default:
		return nil, nil, fmt.Errorf("invalid current operations status")
	}
	var kinds []string
	switch filter.Kind {
	case "":
	case "publication":
		kinds = []string{"publish_track_v1", "publication_reconcile_v1"}
	case "analysis":
		kinds = []string{"scan_source", "analyze_source"}
	case "removal":
		kinds = []string{LibraryDeletionOperationKind, "cleanup_source_analysis_artifacts",
			"cleanup_publication_sources_v1", "retry_publication_sources_v1", "obsolete_publication_output_cleanup_v1"}
	case "matching":
		kinds = []string{"matching_evaluation_v1", "matching_apply_v1"}
	default:
		return nil, nil, fmt.Errorf("invalid current operations kind")
	}
	return states, kinds, nil
}

func (row currentOperationTargetRow) targetRef() *OperationTargetRef {
	switch {
	case row.PositionID != nil && row.ReleaseID != nil:
		return &OperationTargetRef{Kind: "position", PositionID: row.PositionID, ReleaseID: row.ReleaseID}
	case row.ReleaseID != nil:
		return &OperationTargetRef{Kind: "release", ReleaseID: row.ReleaseID}
	case row.ArtistID != nil:
		return &OperationTargetRef{Kind: "artist", ArtistID: row.ArtistID}
	case row.GroupID != nil:
		return &OperationTargetRef{Kind: "group", GroupID: row.GroupID}
	case row.LocationID != nil && row.RootID != nil:
		return &OperationTargetRef{Kind: "source_location", RootID: row.RootID, LocationID: row.LocationID}
	case row.RootID != nil:
		return &OperationTargetRef{Kind: "source_root", RootID: row.RootID}
	default:
		return nil
	}
}

// Compare saved JSON IDs as text after validating their form for an indexed
// UUID lookup. Missing, historical, or malformed snapshots are not links.
const currentOperationIDPattern = `'^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'`

const currentOperationTargets = `
	LEFT JOIN LATERAL (
		SELECT op.input_snapshot->>'position_id' AS position_key,
			op.input_snapshot->'scope'->>'release_id' AS release_key,
			op.input_snapshot->'scope'->>'artist_id' AS artist_key,
			op.input_snapshot->>'source_root_id' AS root_key,
			CASE WHEN jsonb_typeof(op.input_snapshot->'scope'->'publication_ids')='array'
				THEN CASE WHEN jsonb_array_length(op.input_snapshot->'scope'->'publication_ids')=1
					THEN op.input_snapshot->'scope'->'publication_ids'->>0 END END AS removal_position_key,
			CASE WHEN jsonb_typeof(op.input_snapshot->'work_ids')='array'
				THEN CASE WHEN jsonb_array_length(op.input_snapshot->'work_ids')=1
					THEN op.input_snapshot->'work_ids'->>0 END END AS work_key
	) AS keys ON true
	LEFT JOIN release_track AS pos ON pos.id=CASE
		WHEN op.kind IN ('publish_track_v1','publication_reconcile_v1')
			AND keys.position_key ~ ` + currentOperationIDPattern + ` THEN keys.position_key::uuid
		WHEN op.kind='delete_library_v1' AND op.input_snapshot->'scope'->>'kind'='publication'
			AND pos.release_id::text=keys.release_key
			AND keys.removal_position_key ~ ` + currentOperationIDPattern + ` THEN keys.removal_position_key::uuid END
	LEFT JOIN local_track AS track ON track.id=pos.track_id
	LEFT JOIN local_release AS release ON release.id=COALESCE(pos.release_id,
		CASE WHEN op.kind='delete_library_v1' AND op.input_snapshot->'scope'->>'kind'='release'
			AND keys.release_key ~ ` + currentOperationIDPattern + ` THEN keys.release_key::uuid END)
	LEFT JOIN local_artist AS artist ON artist.catalog_visible AND artist.id=CASE WHEN op.kind='delete_library_v1'
		AND op.input_snapshot->'scope'->>'kind'='artist'
		AND keys.artist_key ~ ` + currentOperationIDPattern + ` THEN keys.artist_key::uuid END
	LEFT JOIN source_analysis_work AS work ON op.kind='analyze_source' AND work.invalidated_at IS NULL
		AND work.id=CASE WHEN keys.work_key ~ ` + currentOperationIDPattern + ` THEN keys.work_key::uuid END
	LEFT JOIN source_root AS root ON root.id=COALESCE(
		CASE WHEN op.kind='scan_source' THEN COALESCE(op.target_source_root_id,
			CASE WHEN keys.root_key ~ ` + currentOperationIDPattern + ` THEN keys.root_key::uuid END) END,
		work.source_root_id)
	LEFT JOIN source_location AS location ON location.id=work.current_location_id
		AND location.source_root_id=root.id AND location.relative_path=work.relative_path
		AND location.size_bytes=work.size_bytes AND location.mtime=work.mtime
	LEFT JOIN matching_run AS run ON op.kind='matching_evaluation_v1' AND run.operation_id=op.id
	LEFT JOIN LATERAL (
		SELECT min(target.group_id::text)::uuid AS group_id, min(target.captured_group_revision) AS revision
		FROM matching_run_target target WHERE target.run_id=run.id HAVING count(*)=1
	) AS target ON run.id IS NOT NULL
	LEFT JOIN incoming_group AS grp ON grp.id=target.group_id AND grp.revision=target.revision
	LEFT JOIN LATERAL (
		SELECT min(NULLIF(btrim(variant.observed_tags->'ALBUM'->>0), '')) AS title
		FROM incoming_group_member member JOIN media_variant variant ON variant.id=member.variant_id
		WHERE member.group_id=grp.id
		HAVING count(DISTINCT lower(NULLIF(btrim(variant.observed_tags->'ALBUM'->>0), '')))=1
	) AS album ON grp.id IS NOT NULL`

// currentOperationTargetRefs resolves the identities of a linked target. It is
// shared by the target-label join and the page projection.
const currentOperationTargetRefs = `root.id AS resolved_root_id, location.id AS resolved_location_id,
	release.id AS resolved_release_id, pos.id AS resolved_position_id,
	grp.id AS resolved_group_id, artist.id AS resolved_artist_id`

const currentOperationTargetLabel = `COALESCE(
	NULLIF(concat_ws(' · ', NULLIF(release.title, ''), NULLIF(track.title, '')), ''),
	NULLIF(artist.name, ''), album.title,
	NULLIF(concat_ws(' · ', NULLIF(root.display_name, ''),
		NULLIF(regexp_replace(location.relative_path, '^.*/', ''), '')), ''),
	'')`
