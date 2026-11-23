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
	WorkID            uuid.UUID
	VariantID         uuid.UUID
	MediaVariantID    *uuid.UUID
	CaptureAnalysisID *uuid.UUID
	RootID            uuid.UUID
	RootEnabled       bool
	RootStatus        string
	RootStale         bool
	LocationID        uuid.UUID
	ConfiguredPath    string
	InventoryPath     string
	RelativePath      string
	SizeBytes         int64
	Mtime             time.Time
	Tags              json.RawMessage
	// TagEncoding is the saved full tagencoding.Report for the selected metadata
	// result. It is view-only: the field is excluded from JSON so the canonical
	// capture bytes and existing matching target digests are unchanged.
	TagEncoding                   json.RawMessage `bun:"tag_encoding,type:jsonb,nullzero" json:"-"`
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
	FingerprintResultID           *uuid.UUID
	FingerprintWinner             *uuid.UUID
	FingerprintDuration           *float64
	FingerprintVersion            *string
	FingerprintBanner             *string
	FingerprintAlgorithmNamespace *string
	FingerprintAlgorithmID        *int16
	FingerprintCalculatedAt       *time.Time
	FingerprintParserVersion      *int
	Ready                         bool
	// NonAudioAnalysisSettled is true only for the current location of a work
	// whose probe succeeded as 'no_audio' with no step still in flight or failed.
	// Audio captures are never classified here; consumers combine it with the
	// normal Ready capture requirement.
	NonAudioAnalysisSettled bool `bun:"non_audio_analysis_settled" json:"-"`
}

type IncomingMatchingReconcileGroup struct {
	Group          IncomingGroupingGroup
	Authority      IncomingGroupMatchingAuthority
	MemberCaptures map[uuid.UUID][]IncomingGroupingCapture
}

type IncomingMatchingReconcileSnapshot struct {
	Groups []IncomingMatchingReconcileGroup
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
	// Fence current work for groups that are about to become stale.
	if err := requestAllAutomaticIncomingMatchingReconciliationWith(ctx, tx, true); err != nil {
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

// ReadVisibleCount only trusts the materialized groups while their state is
// current. The flag and count are read in one statement, in the same snapshot.
func (repository *IncomingGroupingRepository) ReadVisibleCount(ctx context.Context) (count int, current bool, err error) {
	err = repository.db.NewRaw(`SELECT CASE WHEN needs_refresh THEN 0 ELSE (
		SELECT count(DISTINCT group_id) FROM incoming_group_member WHERE ready IS TRUE
	) END, NOT needs_refresh FROM incoming_grouping_state WHERE singleton=true`).Scan(ctx, &count, &current)
	if err != nil {
		return 0, false, fmt.Errorf("read visible incoming group count: %w", err)
	}
	return count, current, nil
}

type IncomingGroupingPartition struct {
	State    IncomingGroupingState
	Captures []IncomingGroupingCapture
	Groups   []IncomingGroupingGroup
}

// ReadPartition binds the grouping epoch to the membership and evidence read
// from the same database snapshot. It never invokes the refresh writer.
func (repository *IncomingGroupingRepository) ReadPartition(ctx context.Context) (IncomingGroupingPartition, error) {
	var partition IncomingGroupingPartition
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, func(ctx context.Context, tx bun.Tx) error {
		if err := tx.NewRaw(`SELECT revision, needs_refresh FROM incoming_grouping_state WHERE singleton=true`).
			Scan(ctx, &partition.State.Revision, &partition.State.NeedsRefresh); err != nil {
			return fmt.Errorf("read incoming grouping partition state: %w", err)
		}
		var err error
		partition.Captures, err = loadIncomingGroupingCaptures(ctx, tx)
		if err != nil {
			return err
		}
		partition.Groups, err = loadIncomingGroupingGroups(ctx, tx)
		return err
	})
	return partition, err
}

func (repository *IncomingGroupingRepository) List(ctx context.Context) ([]IncomingGroupingGroup, error) {
	return loadIncomingGroupingGroups(ctx, repository.db)
}

// CurrentCaptures returns every current inventory member, with persisted analysis
// selections when available and an explicit readiness bit. It performs no source IO.
func (repository *IncomingGroupingRepository) CurrentCaptures(ctx context.Context) ([]IncomingGroupingCapture, error) {
	return loadIncomingGroupingCaptures(ctx, repository.db)
}

// MatchingReconcileSnapshot combines saved grouping state, current persisted
// captures, and authority. It never reads source paths or derives authority from
// the group's manual-correction bit.
func (repository *IncomingGroupingRepository) MatchingReconcileSnapshot(ctx context.Context) (IncomingMatchingReconcileSnapshot, error) {
	groups, err := loadIncomingGroupingGroups(ctx, repository.db)
	if err != nil {
		return IncomingMatchingReconcileSnapshot{}, err
	}
	captures, err := loadIncomingGroupingCaptures(ctx, repository.db)
	if err != nil {
		return IncomingMatchingReconcileSnapshot{}, err
	}
	var rows []IncomingGroupMatchingAuthority
	if err := repository.db.NewRaw(`SELECT group_id, mode, revision FROM incoming_group_matching_authority ORDER BY group_id`).Scan(ctx, &rows); err != nil {
		return IncomingMatchingReconcileSnapshot{}, fmt.Errorf("load incoming group matching authority: %w", err)
	}
	authorities := make(map[uuid.UUID]IncomingGroupMatchingAuthority, len(rows))
	for _, authority := range rows {
		authorities[authority.GroupID] = authority
	}
	byVariant := make(map[uuid.UUID][]IncomingGroupingCapture)
	for _, capture := range captures {
		byVariant[capture.VariantID] = append(byVariant[capture.VariantID], capture)
	}
	snapshot := IncomingMatchingReconcileSnapshot{Groups: make([]IncomingMatchingReconcileGroup, 0, len(groups))}
	for _, group := range groups {
		authority, ok := authorities[group.ID]
		if !ok {
			authority = IncomingGroupMatchingAuthority{GroupID: group.ID, Mode: "automatic"}
		}
		memberCaptures := make(map[uuid.UUID][]IncomingGroupingCapture, len(group.Members)+len(group.UnreadyMembers))
		for _, variantID := range append(append([]uuid.UUID(nil), group.Members...), group.UnreadyMembers...) {
			memberCaptures[variantID] = append([]IncomingGroupingCapture(nil), byVariant[variantID]...)
		}
		snapshot.Groups = append(snapshot.Groups, IncomingMatchingReconcileGroup{Group: group, Authority: authority, MemberCaptures: memberCaptures})
	}
	return snapshot, nil
}

func loadIncomingGroupingCaptures(ctx context.Context, database bun.IDB) ([]IncomingGroupingCapture, error) {
	return loadIncomingGroupingCapturesForLocations(ctx, database, nil)
}

// loadIncomingGroupingCapturesForLocations preserves the global reader when no
// scope is supplied, while allowing target captures to be constrained in SQL.
func loadIncomingGroupingCapturesForLocations(ctx context.Context, database bun.IDB, locationIDs []uuid.UUID) ([]IncomingGroupingCapture, error) {
	captures := make([]IncomingGroupingCapture, 0)
	query := `
		SELECT COALESCE(sha.success_sha_variant_id, work.id, location.id) AS variant_id,
			work_identity.media_variant_id AS media_variant_id,
			COALESCE(work.id, location.id) AS work_id, metadata.success_metadata_result_id AS capture_analysis_id,
			root.id AS root_id, root.enabled AS root_enabled, root.status AS root_status,
			(root.inventory_path IS NOT NULL AND root.inventory_path<>root.configured_path) AS root_stale,
			location.id AS location_id, root.configured_path, root.inventory_path,
			location.relative_path, location.size_bytes, location.mtime,
			metadata_result.observed_tags AS tags, metadata_result.tag_encoding AS tag_encoding,
			metadata_result.winning_result_id AS metadata_winner,
			metadata_result.observed_at AS metadata_observed_at, metadata_result.provenance AS metadata_provenance,
			COALESCE(probe_variant.ffprobe_json, '{}'::jsonb) AS probe_result,
			probe_variant.id AS probe_winner, probe_variant.ffprobe_version AS probe_version,
			probe_variant.analysis_policy_version AS probe_policy, probe_variant.inspected_at AS probe_inspected_at,
			sha_variant.source_sha256,
			fingerprint_result.fingerprint,
			fingerprint_result.id AS fingerprint_result_id,
			fingerprint_result.winning_result_id AS fingerprint_winner,
			fingerprint_result.reported_duration AS fingerprint_duration,
			fingerprint_result.fpcalc_version AS fingerprint_version,
			fingerprint_result.version_banner AS fingerprint_banner,
			fingerprint_result.algorithm_namespace AS fingerprint_algorithm_namespace,
			fingerprint_result.algorithm_id AS fingerprint_algorithm_id,
			fingerprint_result.calculated_at AS fingerprint_calculated_at,
			fingerprint_result.parser_contract_version AS fingerprint_parser_version,
			COALESCE(((metadata.state='succeeded' AND metadata.success_metadata_result_id IS NOT NULL)
				OR (metadata.input_snapshot IS NULL AND (metadata.state='not_requested'
					OR (metadata.state='skipped' AND metadata.skip_reason IN ('disabled','not_requested')))))
				AND NOT EXISTS (SELECT 1 FROM source_analysis_step analysis_step
					WHERE analysis_step.work_id=work.id AND analysis_step.step<>'metadata'
					AND NOT (
						(analysis_step.state='succeeded' AND CASE analysis_step.step
							WHEN 'sha256' THEN analysis_step.success_sha_variant_id IS NOT NULL
							WHEN 'probe' THEN analysis_step.success_probe_variant_id IS NOT NULL
							WHEN 'fingerprint' THEN analysis_step.success_fingerprint_result_id IS NOT NULL
							ELSE false
						END)
						OR (analysis_step.input_snapshot IS NULL AND (
							analysis_step.state='not_requested'
							OR (analysis_step.state='skipped' AND analysis_step.skip_reason IN ('disabled','not_requested'))
						))
					)), false) AS ready,
			COALESCE(location.probe_status='no_audio'
				AND work.id IS NOT NULL
				AND probe.state='succeeded' AND probe.success_probe_variant_id IS NOT NULL
				AND NOT EXISTS (SELECT 1 FROM source_analysis_step blocking_step
					WHERE blocking_step.work_id=work.id
					AND blocking_step.state IN ('pending','queued','running','failed')), false) AS non_audio_analysis_settled
		FROM source_root root
		JOIN source_location location ON location.source_root_id=root.id
		LEFT JOIN source_analysis_work work ON work.current_location_id=location.id AND work.location_id=location.id
			AND work.invalidated_at IS NULL
			AND work.configured_path=root.configured_path AND work.inventory_path=root.inventory_path
			AND work.relative_path=location.relative_path AND work.size_bytes=location.size_bytes AND work.mtime=location.mtime
		LEFT JOIN source_analysis_step metadata ON metadata.work_id=work.id AND metadata.step='metadata'
		LEFT JOIN media_metadata_result metadata_result ON metadata_result.id=metadata.success_metadata_result_id
		LEFT JOIN source_analysis_step sha ON sha.work_id=work.id AND sha.step='sha256' AND sha.state='succeeded'
		LEFT JOIN source_analysis_step probe ON probe.work_id=work.id AND probe.step='probe' AND probe.state='succeeded'
		LEFT JOIN source_analysis_step fingerprint ON fingerprint.work_id=work.id AND fingerprint.step='fingerprint' AND fingerprint.state='succeeded'
		LEFT JOIN source_work_variant_identity work_identity ON work_identity.work_id=work.id
		LEFT JOIN media_variant probe_variant ON probe_variant.id=probe.success_probe_variant_id
		LEFT JOIN media_variant sha_variant ON sha_variant.id=sha.success_sha_variant_id
		LEFT JOIN media_fingerprint_result fingerprint_result ON fingerprint_result.id=fingerprint.success_fingerprint_result_id
		`
	args := make([]any, 0, 1)
	if len(locationIDs) > 0 {
		query += ` WHERE location.id IN (?)`
		args = append(args, bun.List(locationIDs))
	}
	query += ` ORDER BY variant_id, root.id, location.relative_path, work.id`
	err := database.NewRaw(query, args...).Scan(ctx, &captures)
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
		currentByID := make(map[uuid.UUID]IncomingGroupingGroup, len(current))
		for _, group := range current {
			currentByID[group.ID] = group
		}
		nextByID := make(map[uuid.UUID]IncomingGroupingGroup, len(next))
		for _, group := range next {
			nextByID[group.ID] = group
		}
		changedAutomatic := make([]uuid.UUID, 0)
		changedOldAutomatic := make([]uuid.UUID, 0)
		for _, group := range next {
			if group.Manual || len(group.Members) == 0 {
				continue
			}
			previous, exists := currentByID[group.ID]
			if !exists || previous.Manual || previous.Revision != group.Revision || incomingGroupReadinessChanged(previous, group) {
				changedAutomatic = append(changedAutomatic, group.ID)
			}
		}
		if expectedEpoch != nil {
			for _, group := range current {
				if group.Manual {
					continue
				}
				replacement, exists := nextByID[group.ID]
				if !exists || replacement.Revision != group.Revision || replacement.Manual || incomingGroupReadinessChanged(group, replacement) {
					changedOldAutomatic = append(changedOldAutomatic, group.ID)
				}
			}
		}
		if len(changedOldAutomatic) != 0 {
			oldIDs := make([]uuid.UUID, 0, len(changedOldAutomatic))
			if err := tx.NewRaw(`SELECT groups.id FROM incoming_group groups
			LEFT JOIN incoming_group_matching_authority authority ON authority.group_id=groups.id
			WHERE groups.id IN (?) AND groups.manual=false
			AND COALESCE(authority.mode, 'automatic')='automatic'
			ORDER BY groups.id`, bun.List(changedOldAutomatic)).Scan(ctx, &oldIDs); err != nil {
				return fmt.Errorf("select changed old automatic incoming groups: %w", err)
			}
			if err := RequestIncomingMatchingReconciliationWith(ctx, tx, oldIDs); err != nil {
				return fmt.Errorf("fence changed old automatic incoming groups: %w", err)
			}
		}
		if _, err := tx.NewRaw(`DELETE FROM incoming_group WHERE id IS NOT NULL`).Exec(ctx); err != nil {
			return fmt.Errorf("replace incoming groups: %w", err)
		}
		for _, group := range next {
			// A manual correction may legitimately retain only unready members,
			// for example after splitting the ready part away from a group whose
			// remaining files lost their analysis. Such a group still needs its
			// identity and unready membership; everything else requires a ready
			// member.
			unreadyOnly := group.Manual && len(group.UnreadyMembers) > 0
			if group.ID == uuid.Nil || (len(group.Members) == 0 && !unreadyOnly) {
				return fmt.Errorf("computed incoming group is invalid")
			}
			if err := insertIncomingGroupingGroup(ctx, tx, group); err != nil {
				return err
			}
		}
		if err := requestChangedAutomaticIncomingGroupsWith(ctx, tx, changedAutomatic); err != nil {
			return fmt.Errorf("request changed automatic incoming groups: %w", err)
		}
		if _, err := tx.NewRaw(`UPDATE incoming_grouping_state SET revision=revision+1, needs_refresh=false, updated_at=now() WHERE singleton=true AND revision=?`, state.Revision).Exec(ctx); err != nil {
			return fmt.Errorf("advance incoming grouping state: %w", err)
		}
		return nil
	})
}

func incomingGroupReadinessChanged(previous, next IncomingGroupingGroup) bool {
	if len(previous.Members) != len(next.Members) || len(previous.UnreadyMembers) != len(next.UnreadyMembers) {
		return true
	}
	ready := make(map[uuid.UUID]bool, len(previous.Members))
	for _, member := range previous.Members {
		ready[member] = true
	}
	for _, member := range next.Members {
		if !ready[member] {
			return true
		}
	}
	unready := make(map[uuid.UUID]bool, len(previous.UnreadyMembers))
	for _, member := range previous.UnreadyMembers {
		unready[member] = true
	}
	for _, member := range next.UnreadyMembers {
		if !unready[member] {
			return true
		}
	}
	return false
}

func requestChangedAutomaticIncomingGroupsWith(ctx context.Context, tx bun.IDB, groupIDs []uuid.UUID) error {
	if len(groupIDs) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(groupIDs))
	if err := tx.NewRaw(`SELECT groups.id FROM incoming_group groups
		LEFT JOIN incoming_group_matching_authority authority ON authority.group_id=groups.id
		WHERE groups.id IN (?) AND groups.manual=false
		AND COALESCE(authority.mode, 'automatic')='automatic'
		AND EXISTS (SELECT 1 FROM incoming_group_member member WHERE member.group_id=groups.id AND member.ready=true)
		ORDER BY groups.id`, bun.List(groupIDs)).Scan(ctx, &ids); err != nil {
		return fmt.Errorf("select changed automatic incoming groups: %w", err)
	}
	return RequestIncomingMatchingReconciliationWith(ctx, tx, ids)
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

// incomingGroupingGroupRow is one ordered group/member/location row from the
// single projection read. Only member and location presence are pointers;
// joined payload columns are non-null whenever their presence ID is present.
type incomingGroupingGroupRow struct {
	GroupID                uuid.UUID       `bun:"group_id"`
	Manual                 bool            `bun:"manual"`
	Revision               string          `bun:"revision"`
	Diagnostics            json.RawMessage `bun:"diagnostics"`
	MemberVariantID        *uuid.UUID      `bun:"member_variant_id"`
	MemberReady            bool            `bun:"member_ready"`
	LocationID             *uuid.UUID      `bun:"location_id"`
	LocationRootID         uuid.UUID       `bun:"location_root_id"`
	LocationWorkID         uuid.UUID       `bun:"location_work_id"`
	LocationConfiguredPath string          `bun:"location_configured_path"`
	LocationInventoryPath  string          `bun:"location_inventory_path"`
	LocationRelativePath   string          `bun:"location_relative_path"`
	LocationSizeBytes      int64           `bun:"location_size_bytes"`
	LocationMtime          time.Time       `bun:"location_mtime"`
}

// loadIncomingGroupingGroups reads the whole projection in one ordered,
// read-only statement on the supplied db or transaction. Folding the ordered
// rows keeps deterministic group, member, and location order in a single
// snapshot without per-group reads or locks. Group diagnostics travel only on
// the first ordered row per group, so JSON is not repeated per location.
func loadIncomingGroupingGroups(ctx context.Context, database bun.IDB) ([]IncomingGroupingGroup, error) {
	rows := make([]incomingGroupingGroupRow, 0)
	if err := database.NewRaw(`SELECT g.id AS group_id, g.manual AS manual, g.revision AS revision,
		CASE WHEN row_number() OVER (PARTITION BY g.id ORDER BY m.variant_id, l.source_root_id, l.relative_path)=1
			THEN g.diagnostics END AS diagnostics,
		m.variant_id AS member_variant_id, m.ready AS member_ready,
		l.source_root_id AS location_root_id, l.work_id AS location_work_id,
		l.location_id AS location_id, l.configured_path AS location_configured_path,
		l.inventory_path AS location_inventory_path, l.relative_path AS location_relative_path,
		l.size_bytes AS location_size_bytes, l.mtime AS location_mtime
		FROM incoming_group g
		LEFT JOIN incoming_group_member m ON m.group_id=g.id
		LEFT JOIN incoming_group_member_location l ON l.group_id=m.group_id AND l.variant_id=m.variant_id
		ORDER BY g.id, m.variant_id, l.source_root_id, l.relative_path`).Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("load incoming groups: %w", err)
	}
	groups := make([]IncomingGroupingGroup, 0)
	var previousMember *uuid.UUID
	for _, row := range rows {
		if len(groups) == 0 || groups[len(groups)-1].ID != row.GroupID {
			groups = append(groups, IncomingGroupingGroup{
				ID:          row.GroupID,
				Manual:      row.Manual,
				Revision:    row.Revision,
				Diagnostics: row.Diagnostics,
			})
			previousMember = nil
		}
		if row.MemberVariantID == nil {
			continue
		}
		group := &groups[len(groups)-1]
		memberID := *row.MemberVariantID
		if previousMember == nil || memberID != *previousMember {
			previousMember = row.MemberVariantID
			if row.MemberReady {
				group.Members = append(group.Members, memberID)
			} else {
				group.UnreadyMembers = append(group.UnreadyMembers, memberID)
			}
		}
		if row.LocationID == nil {
			continue
		}
		group.Locations = append(group.Locations, IncomingGroupingMemberLocation{
			VariantID:      memberID,
			RootID:         row.LocationRootID,
			WorkID:         row.LocationWorkID,
			LocationID:     *row.LocationID,
			ConfiguredPath: row.LocationConfiguredPath,
			InventoryPath:  row.LocationInventoryPath,
			RelativePath:   row.LocationRelativePath,
			SizeBytes:      row.LocationSizeBytes,
			Mtime:          row.LocationMtime,
		})
	}
	return groups, nil
}

func insertIncomingGroupingGroup(ctx context.Context, tx bun.Tx, group IncomingGroupingGroup) error {
	if len(group.Diagnostics) == 0 || string(group.Diagnostics) == "null" {
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
