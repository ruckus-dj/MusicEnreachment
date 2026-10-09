package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

var ErrIncomingGroupingConflict = errors.New("incoming grouping changed")

type IncomingGroupingState struct {
	Revision     int64
	NeedsRefresh bool
}

type IncomingGroupingGroup struct {
	ID             uuid.UUID
	Manual         bool
	Revision       string
	Members        []uuid.UUID
	UnreadyMembers []uuid.UUID
	Diagnostics    json.RawMessage
	Locations      []IncomingGroupingMemberLocation
}

type IncomingGroupingMemberLocation struct {
	VariantID      uuid.UUID
	RootID         uuid.UUID
	WorkID         uuid.UUID
	LocationID     uuid.UUID
	ConfiguredPath string
	InventoryPath  string
	RelativePath   string
	SizeBytes      int64
	Mtime          time.Time
}

// IncomingGroupingCapture is read only from persisted analysis results. The
// technical payloads are retained for later consumers; grouping itself uses only
// captured tags and current inventory identity.
type IncomingGroupingCapture struct {
	WorkID                        uuid.UUID
	VariantID                     uuid.UUID
	CaptureAnalysisID             *uuid.UUID
	RootID                        uuid.UUID
	LocationID                    uuid.UUID
	ConfiguredPath                string
	InventoryPath                 string
	RelativePath                  string
	SizeBytes                     int64
	Mtime                         time.Time
	Tags                          json.RawMessage
	ProbeResult                   json.RawMessage
	ProbeWinner                   *uuid.UUID
	ProbeVersion                  *string
	ProbePolicy                   *int
	ProbeInspectedAt              *time.Time
	SourceSHA256                  []byte
	MetadataWinner                *uuid.UUID
	MetadataObservedAt            *time.Time
	MetadataProvenance            json.RawMessage
	Fingerprint                   *string
	FingerprintWinner             *uuid.UUID
	FingerprintDuration           *float64
	FingerprintVersion            *string
	FingerprintBanner             *string
	FingerprintAlgorithmNamespace *string
	FingerprintAlgorithmID        *int16
	FingerprintCalculatedAt       *time.Time
	FingerprintParserVersion      *int
	Ready                         bool
}

type IncomingGroupingRepository struct{ db *bun.DB }

func NewIncomingGroupingRepository(db *bun.DB) *IncomingGroupingRepository {
	return &IncomingGroupingRepository{db: db}
}

// InvalidateIncomingGrouping marks the materialized grouping stale and advances
// its epoch. The caller MUST hold the source mutation gate and use the same
// transaction as the inventory/analysis mutation. Lock order is canonical source
// row(s), then this grouping state row. It intentionally does not request analysis.
func InvalidateIncomingGrouping(ctx context.Context, tx bun.IDB) error {
	if _, err := tx.NewRaw(`UPDATE incoming_grouping_state
		SET revision=revision+1, needs_refresh=true, updated_at=now() WHERE singleton=true`).Exec(ctx); err != nil {
		return fmt.Errorf("invalidate incoming grouping: %w", err)
	}
	return nil
}

func (repository *IncomingGroupingRepository) State(ctx context.Context) (IncomingGroupingState, error) {
	var state IncomingGroupingState
	err := repository.db.NewRaw(`SELECT revision, needs_refresh FROM incoming_grouping_state WHERE singleton=true`).Scan(ctx, &state.Revision, &state.NeedsRefresh)
	if err != nil {
		return state, fmt.Errorf("read incoming grouping state: %w", err)
	}
	return state, nil
}

func (repository *IncomingGroupingRepository) List(ctx context.Context) ([]IncomingGroupingGroup, error) {
	return loadIncomingGroupingGroups(ctx, repository.db)
}

// CurrentCaptures returns every current inventory member, with persisted analysis
// selections when available and an explicit readiness bit. It performs no source IO.
func (repository *IncomingGroupingRepository) CurrentCaptures(ctx context.Context) ([]IncomingGroupingCapture, error) {
	return loadIncomingGroupingCaptures(ctx, repository.db)
}

func loadIncomingGroupingCaptures(ctx context.Context, database bun.IDB) ([]IncomingGroupingCapture, error) {
	captures := make([]IncomingGroupingCapture, 0)
	err := database.NewRaw(`
		SELECT COALESCE(sha.success_sha_variant_id, work.id, location.id) AS variant_id,
			COALESCE(work.id, location.id) AS work_id, metadata.success_metadata_result_id AS capture_analysis_id,
			root.id AS root_id, location.id AS location_id, root.configured_path, root.inventory_path,
			location.relative_path, location.size_bytes, location.mtime,
			metadata_result.observed_tags AS tags, metadata_result.winning_result_id AS metadata_winner,
			metadata_result.observed_at AS metadata_observed_at, metadata_result.provenance AS metadata_provenance,
			COALESCE(probe_variant.ffprobe_json, '{}'::jsonb) AS probe_result,
			probe_variant.id AS probe_winner, probe_variant.ffprobe_version AS probe_version,
			probe_variant.analysis_policy_version AS probe_policy, probe_variant.inspected_at AS probe_inspected_at,
			sha_variant.source_sha256,
			fingerprint_result.fingerprint,
			fingerprint_result.winning_result_id AS fingerprint_winner,
			fingerprint_result.reported_duration AS fingerprint_duration,
			fingerprint_result.fpcalc_version AS fingerprint_version,
			fingerprint_result.version_banner AS fingerprint_banner,
			fingerprint_result.algorithm_namespace AS fingerprint_algorithm_namespace,
			fingerprint_result.algorithm_id AS fingerprint_algorithm_id,
			fingerprint_result.calculated_at AS fingerprint_calculated_at,
			fingerprint_result.parser_contract_version AS fingerprint_parser_version,
			COALESCE(metadata.state='succeeded' AND metadata.success_metadata_result_id IS NOT NULL
				AND 'metadata'=ANY(COALESCE(binding.requested_steps, '{}'::text[]))
				AND NOT EXISTS (SELECT 1 FROM unnest(COALESCE(binding.requested_steps, '{}'::text[])) requested(step)
					LEFT JOIN source_analysis_step requested_step ON requested_step.work_id=work.id AND requested_step.step=requested.step
					WHERE requested_step.state IS DISTINCT FROM 'succeeded'), false) AS ready
		FROM source_root root
		JOIN source_location location ON location.source_root_id=root.id
		LEFT JOIN source_analysis_work work ON work.current_location_id=location.id AND work.location_id=location.id
			AND work.configured_path=root.configured_path AND work.inventory_path=root.inventory_path
			AND work.relative_path=location.relative_path AND work.size_bytes=location.size_bytes AND work.mtime=location.mtime
		LEFT JOIN source_analysis_work_artifact_binding binding ON binding.work_id=work.id
		LEFT JOIN source_analysis_step metadata ON metadata.work_id=work.id AND metadata.step='metadata'
		LEFT JOIN media_metadata_result metadata_result ON metadata_result.id=metadata.success_metadata_result_id
		LEFT JOIN source_analysis_step sha ON sha.work_id=work.id AND sha.step='sha256' AND sha.state='succeeded'
		LEFT JOIN source_analysis_step probe ON probe.work_id=work.id AND probe.step='probe' AND probe.state='succeeded'
		LEFT JOIN source_analysis_step fingerprint ON fingerprint.work_id=work.id AND fingerprint.step='fingerprint' AND fingerprint.state='succeeded'
		LEFT JOIN media_variant probe_variant ON probe_variant.id=probe.success_probe_variant_id
		LEFT JOIN media_variant sha_variant ON sha_variant.id=sha.success_sha_variant_id
		LEFT JOIN media_fingerprint_result fingerprint_result ON fingerprint_result.id=fingerprint.success_fingerprint_result_id
		ORDER BY variant_id, root.id, location.relative_path, work.id`).Scan(ctx, &captures)
	if err != nil {
		return nil, fmt.Errorf("read current incoming grouping captures: %w", err)
	}
	return captures, nil
}

type IncomingGroupingCompute func([]IncomingGroupingCapture, []IncomingGroupingGroup) ([]IncomingGroupingGroup, error)

// Refresh computes and replaces the read model while fencing it against source
// mutations. The callback is pure service logic over the fresh persisted snapshot.
func (repository *IncomingGroupingRepository) Refresh(ctx context.Context, compute IncomingGroupingCompute) error {
	return repository.runGroupingCompute(ctx, nil, compute)
}

// Confirm computes an explicitly requested correction against a freshly locked
// snapshot; no page draft is persisted between preview and this call.
func (repository *IncomingGroupingRepository) Confirm(ctx context.Context, expectedEpoch int64, compute IncomingGroupingCompute) error {
	return repository.runGroupingCompute(ctx, &expectedEpoch, compute)
}

func (repository *IncomingGroupingRepository) runGroupingCompute(ctx context.Context, expectedEpoch *int64, compute IncomingGroupingCompute) error {
	if compute == nil {
		return fmt.Errorf("incoming grouping computation is required")
	}
	return repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("lock canonical source mutation gate: %w", err)
		}
		if err := lockCurrentIncomingSourceRows(ctx, tx); err != nil {
			return err
		}
		var state IncomingGroupingState
		if err := tx.NewRaw(`SELECT revision, needs_refresh FROM incoming_grouping_state WHERE singleton=true FOR UPDATE`).Scan(ctx, &state.Revision, &state.NeedsRefresh); err != nil {
			return fmt.Errorf("lock incoming grouping state: %w", err)
		}
		if expectedEpoch != nil && state.Revision != *expectedEpoch {
			return ErrIncomingGroupingConflict
		}
		captures, err := loadIncomingGroupingCaptures(ctx, tx)
		if err != nil {
			return err
		}
		current, err := loadIncomingGroupingGroups(ctx, tx)
		if err != nil {
			return err
		}
		next, err := compute(captures, current)
		if err != nil {
			return err
		}
		if _, err := tx.NewRaw(`DELETE FROM incoming_group WHERE id IS NOT NULL`).Exec(ctx); err != nil {
			return fmt.Errorf("replace incoming groups: %w", err)
		}
		for _, group := range next {
			if group.ID == uuid.Nil || len(group.Members) == 0 {
				return fmt.Errorf("computed incoming group is invalid")
			}
			if err := insertIncomingGroupingGroup(ctx, tx, group); err != nil {
				return err
			}
		}
		if _, err := tx.NewRaw(`UPDATE incoming_grouping_state SET revision=revision+1, needs_refresh=false, updated_at=now() WHERE singleton=true AND revision=?`, state.Revision).Exec(ctx); err != nil {
			return fmt.Errorf("advance incoming grouping state: %w", err)
		}
		return nil
	})
}

func lockCurrentIncomingSourceRows(ctx context.Context, tx bun.Tx) error {
	queries := []struct {
		table string
		query string
	}{
		{table: "source_root", query: `SELECT id FROM source_root ORDER BY id`},
		{table: "source_location", query: `SELECT id FROM source_location ORDER BY id`},
		{table: "source_analysis_work", query: `SELECT id FROM source_analysis_work ORDER BY id`},
	}
	for _, item := range queries {
		var ids []uuid.UUID
		if err := tx.NewRaw(item.query).Scan(ctx, &ids); err != nil {
			return fmt.Errorf("list %s for incoming grouping locks: %w", item.table, err)
		}
		for _, id := range ids {
			var locked uuid.UUID
			if err := tx.NewRaw(`SELECT id FROM `+item.table+` WHERE id=? FOR UPDATE`, id).Scan(ctx, &locked); err != nil {
				return fmt.Errorf("lock %s for incoming grouping: %w", item.table, err)
			}
		}
	}
	return nil
}

func loadIncomingGroupingGroups(ctx context.Context, database bun.IDB) ([]IncomingGroupingGroup, error) {
	groups := make([]IncomingGroupingGroup, 0)
	if err := database.NewRaw(`SELECT id, manual, revision, diagnostics FROM incoming_group ORDER BY id`).Scan(ctx, &groups); err != nil {
		return nil, fmt.Errorf("load incoming groups: %w", err)
	}
	for i := range groups {
		var members []struct {
			VariantID uuid.UUID `bun:"variant_id"`
			Ready     bool      `bun:"ready"`
		}
		if err := database.NewRaw(`SELECT variant_id, ready FROM incoming_group_member WHERE group_id=? ORDER BY variant_id`, groups[i].ID).Scan(ctx, &members); err != nil {
			return nil, fmt.Errorf("load incoming group members: %w", err)
		}
		for _, member := range members {
			if member.Ready {
				groups[i].Members = append(groups[i].Members, member.VariantID)
			} else {
				groups[i].UnreadyMembers = append(groups[i].UnreadyMembers, member.VariantID)
			}
		}
		if err := database.NewRaw(`SELECT variant_id, source_root_id AS root_id, work_id, location_id, configured_path, inventory_path, relative_path, size_bytes, mtime FROM incoming_group_member_location WHERE group_id=? ORDER BY variant_id, source_root_id, relative_path`, groups[i].ID).Scan(ctx, &groups[i].Locations); err != nil {
			return nil, fmt.Errorf("load incoming group location fences: %w", err)
		}
	}
	return groups, nil
}

func insertIncomingGroupingGroup(ctx context.Context, tx bun.Tx, group IncomingGroupingGroup) error {
	if len(group.Diagnostics) == 0 {
		group.Diagnostics = json.RawMessage(`[]`)
	}
	if _, err := tx.NewRaw(`INSERT INTO incoming_group(id, manual, revision, diagnostics) VALUES (?, ?, ?, ?::jsonb)`, group.ID, group.Manual, group.Revision, string(group.Diagnostics)).Exec(ctx); err != nil {
		return fmt.Errorf("insert incoming group: %w", err)
	}
	members := append([]uuid.UUID(nil), group.Members...)
	members = append(members, group.UnreadyMembers...)
	sort.Slice(members, func(i, j int) bool { return members[i].String() < members[j].String() })
	unready := make(map[uuid.UUID]bool, len(group.UnreadyMembers))
	for _, member := range group.UnreadyMembers {
		unready[member] = true
	}
	for i, member := range members {
		if member == uuid.Nil || i > 0 && member == members[i-1] {
			return fmt.Errorf("incoming group member identity is invalid")
		}
		if _, err := tx.NewRaw(`INSERT INTO incoming_group_member(group_id, variant_id, ready) VALUES (?, ?, ?)`, group.ID, member, !unready[member]).Exec(ctx); err != nil {
			return fmt.Errorf("insert incoming group member: %w", err)
		}
	}
	for _, location := range group.Locations {
		if location.VariantID == uuid.Nil || location.RootID == uuid.Nil || location.WorkID == uuid.Nil || location.LocationID == uuid.Nil || location.ConfiguredPath == "" || location.InventoryPath == "" || location.RelativePath == "" {
			return fmt.Errorf("incoming group location fence is invalid")
		}
		if _, err := tx.NewRaw(`INSERT INTO incoming_group_member_location(group_id, variant_id, source_root_id, work_id, location_id, configured_path, inventory_path, relative_path, size_bytes, mtime) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, group.ID, location.VariantID, location.RootID, location.WorkID, location.LocationID, location.ConfiguredPath, location.InventoryPath, location.RelativePath, location.SizeBytes, location.Mtime).Exec(ctx); err != nil {
			return fmt.Errorf("insert incoming group location fence: %w", err)
		}
	}
	return nil
}
