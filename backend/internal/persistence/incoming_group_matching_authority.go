package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type IncomingGroupMatchingAuthority struct {
	GroupID  uuid.UUID `json:"group_id"`
	Mode     string    `json:"mode"`
	Revision int64     `json:"revision"`
}

type IncomingGroupUnlinkResult struct {
	GroupID           uuid.UUID   `json:"group_id"`
	Mode              string      `json:"mode"`
	AuthorityRevision int64       `json:"authority_revision"`
	GroupRevision     string      `json:"group_revision"`
	RemovedSourceIDs  []uuid.UUID `json:"removed_source_ids"`
}

// lockIncomingGroupMatchingAuthority serializes authority changes with new
// matching admissions and materialization. Missing rows mean the original
// automatic authority at revision zero.
func lockIncomingGroupMatchingAuthority(ctx context.Context, tx bun.Tx, groupID uuid.UUID) (IncomingGroupMatchingAuthority, error) {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext(?))`, "incoming-group-matching-authority:"+groupID.String()); err != nil {
		return IncomingGroupMatchingAuthority{}, fmt.Errorf("lock incoming group matching authority: %w", err)
	}
	authority := IncomingGroupMatchingAuthority{GroupID: groupID, Mode: "automatic"}
	err := tx.NewRaw(`SELECT mode, revision FROM incoming_group_matching_authority WHERE group_id=? FOR UPDATE`, groupID).Scan(ctx, &authority.Mode, &authority.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return authority, nil
	}
	if err != nil {
		return IncomingGroupMatchingAuthority{}, fmt.Errorf("read incoming group matching authority: %w", err)
	}
	return authority, nil
}

func requireAutomaticMatchingAuthority(ctx context.Context, tx bun.Tx, groupID uuid.UUID) (int64, error) {
	authority, err := lockIncomingGroupMatchingAuthority(ctx, tx, groupID)
	if err != nil {
		return 0, err
	}
	if authority.Mode != "automatic" {
		return 0, ErrIncomingMatchConflict
	}
	return authority.Revision, nil
}

func promoteIncomingGroupMatchingManual(ctx context.Context, tx bun.Tx, groupID uuid.UUID) (int64, error) {
	authority, err := lockIncomingGroupMatchingAuthority(ctx, tx, groupID)
	if err != nil {
		return 0, err
	}
	if authority.Mode != "manual" {
		authority.Mode = "manual"
		authority.Revision++
	}
	if _, err := tx.NewRaw(`INSERT INTO incoming_group_matching_authority(group_id,mode,revision,updated_at) VALUES (?,'manual',?,now())
		ON CONFLICT (group_id) DO UPDATE SET mode='manual',revision=EXCLUDED.revision,updated_at=now()`, groupID, authority.Revision).Exec(ctx); err != nil {
		return 0, fmt.Errorf("promote incoming group matching authority: %w", err)
	}
	return authority.Revision, nil
}

// UnlinkIncomingGroupMatching removes only explicitly selected manual
// track_source associations. The local/provider catalog and source files are
// deliberately untouched. requestID makes a successful intent replay-safe.
func (repository *IncomingMatchCandidateRepository) UnlinkIncomingGroupMatching(
	ctx context.Context,
	groupID uuid.UUID,
	expectedGroupRevision string,
	expectedAuthorityRevision int64,
	sourceIDs []uuid.UUID,
	requestID uuid.UUID,
) (IncomingGroupUnlinkResult, error) {
	if repository == nil || groupID == uuid.Nil || expectedGroupRevision == "" || expectedAuthorityRevision < 0 || requestID == uuid.Nil || len(sourceIDs) == 0 {
		return IncomingGroupUnlinkResult{}, errors.New("unlink matching intent is incomplete")
	}
	canonicalIDs := append([]uuid.UUID(nil), sourceIDs...)
	sort.Slice(canonicalIDs, func(i, j int) bool { return canonicalIDs[i].String() < canonicalIDs[j].String() })
	for i, id := range canonicalIDs {
		if id == uuid.Nil || i > 0 && id == canonicalIDs[i-1] {
			return IncomingGroupUnlinkResult{}, errors.New("unlink source identities are invalid")
		}
	}
	intent, err := json.Marshal(struct {
		GroupID           uuid.UUID   `json:"group_id"`
		GroupRevision     string      `json:"group_revision"`
		AuthorityRevision int64       `json:"authority_revision"`
		SourceIDs         []uuid.UUID `json:"source_ids"`
	}{groupID, expectedGroupRevision, expectedAuthorityRevision, canonicalIDs})
	if err != nil {
		return IncomingGroupUnlinkResult{}, err
	}
	hash := sha256.Sum256(intent)
	intentHash := hex.EncodeToString(hash[:])
	var result IncomingGroupUnlinkResult
	err = repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("unlink matching: lock output admission gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("unlink matching: lock source mutations: %w", err)
		}
		var saved struct {
			IntentHash string `bun:"intent_hash"`
			Response   []byte `bun:"response"`
		}
		readErr := tx.NewRaw(`SELECT intent_hash,response FROM incoming_group_unlink_receipt WHERE request_id=?`, requestID).Scan(ctx, &saved)
		if readErr == nil {
			if saved.IntentHash != intentHash {
				return ErrIncomingMatchConflict
			}
			return json.Unmarshal(saved.Response, &result)
		}
		if !errors.Is(readErr, sql.ErrNoRows) {
			return fmt.Errorf("unlink matching: read receipt: %w", readErr)
		}
		authority, err := lockIncomingGroupMatchingAuthority(ctx, tx, groupID)
		if err != nil {
			return err
		}
		if authority.Mode != "manual" || authority.Revision != expectedAuthorityRevision {
			return ErrIncomingMatchConflict
		}
		var groupRevision string
		if err := tx.NewRaw(`SELECT revision FROM incoming_group WHERE id=? FOR UPDATE`, groupID).Scan(ctx, &groupRevision); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrIncomingMatchConflict
			}
			return fmt.Errorf("unlink matching: read current group: %w", err)
		}
		if groupRevision != expectedGroupRevision {
			return ErrIncomingMatchConflict
		}
		var memberWorkIDs []uuid.UUID
		if err := tx.NewRaw(`SELECT work_id FROM incoming_group_member_location WHERE group_id=? ORDER BY work_id,location_id FOR UPDATE`, groupID).Scan(ctx, &memberWorkIDs); err != nil {
			return fmt.Errorf("unlink matching: read current group members: %w", err)
		}
		// Group member identities may be work IDs or SHA-coalesced media IDs.
		// Locations retain the actual work IDs for both forms of grouping.
		var memberMediaIDs []uuid.UUID
		if err := tx.NewRaw(`SELECT media_variant_id FROM source_work_variant_identity WHERE work_id IN (?) ORDER BY media_variant_id FOR SHARE`, bun.List(memberWorkIDs)).Scan(ctx, &memberMediaIDs); err != nil {
			return fmt.Errorf("unlink matching: resolve current member identities: %w", err)
		}
		memberSet := make(map[uuid.UUID]bool, len(memberMediaIDs))
		for _, member := range memberMediaIDs {
			memberSet[member] = true
		}
		for _, sourceID := range canonicalIDs {
			if !memberSet[sourceID] {
				return ErrIncomingMatchConflict
			}
		}
		if err := lockIncomingGroupMatchingAuthoritySourceLinks(ctx, tx, canonicalIDs); err != nil {
			return err
		}
		var removed []uuid.UUID
		for _, sourceID := range canonicalIDs {
			var exists bool
			if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM track_source WHERE media_variant_id=? AND decision_method='manual')`, sourceID).Scan(ctx, &exists); err != nil {
				return fmt.Errorf("unlink matching: inspect source association: %w", err)
			}
			if !exists {
				return ErrIncomingMatchConflict
			}
			if _, err := tx.NewRaw(`DELETE FROM track_source WHERE media_variant_id=? AND decision_method='manual'`, sourceID).Exec(ctx); err != nil {
				return fmt.Errorf("unlink matching: remove manual source association: %w", err)
			}
			removed = append(removed, sourceID)
		}
		result = IncomingGroupUnlinkResult{GroupID: groupID, Mode: "manual", AuthorityRevision: authority.Revision + 1, GroupRevision: groupRevision, RemovedSourceIDs: removed}
		if _, err := tx.NewRaw(`UPDATE incoming_group_matching_authority SET revision=?,updated_at=now() WHERE group_id=? AND revision=? AND mode='manual'`, result.AuthorityRevision, groupID, authority.Revision).Exec(ctx); err != nil {
			return fmt.Errorf("unlink matching: advance authority revision: %w", err)
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("unlink matching: encode receipt: %w", err)
		}
		if _, err := tx.NewRaw(`INSERT INTO incoming_group_unlink_receipt(request_id,group_id,intent_hash,response) VALUES (?,?,?,?::jsonb)`, requestID, groupID, intentHash, string(encoded)).Exec(ctx); err != nil {
			return fmt.Errorf("unlink matching: write receipt: %w", err)
		}
		return nil
	})
	return result, err
}

func lockIncomingGroupMatchingAuthoritySourceLinks(ctx context.Context, tx bun.Tx, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	var rows []struct {
		ID uuid.UUID `bun:"id"`
	}
	if err := tx.NewRaw(`SELECT id FROM track_source WHERE media_variant_id IN (?) ORDER BY media_variant_id,track_id FOR UPDATE`, bun.List(ids)).Scan(ctx, &rows); err != nil {
		return fmt.Errorf("unlink matching: lock source associations: %w", err)
	}
	return nil
}
