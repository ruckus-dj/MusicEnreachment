package persistence

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type sourceCleanupRootAbandonmentEvidence struct {
	Items          []json.RawMessage `json:"items"`
	Owners         []json.RawMessage `json:"owners"`
	RetryGrant     json.RawMessage   `json:"retry_grant"`
	RetryOperation *struct {
		ID uuid.UUID `json:"id"`
	} `json:"retry_operation"`
	RetryClaim       json.RawMessage   `json:"retry_claim"`
	RetryItems       []json.RawMessage `json:"retry_items"`
	RetryResolutions []json.RawMessage `json:"retry_resolutions"`
	RecoveryState    json.RawMessage   `json:"recovery_state"`
	RecoveryItems    []json.RawMessage `json:"recovery_items"`
	OriginalClaim    json.RawMessage   `json:"original_claim"`
	RiverJobs        []struct {
		ID    int64  `json:"id"`
		State string `json:"state"`
	} `json:"river_jobs"`
}

// abandonFailedSourceCleanupForRoot retires cleanup-recovery graphs that own
// any source in rootID. SQL captures and validates the complete graph before
// any evidence is removed; a graph may own sources in other roots as well.
func abandonFailedSourceCleanupForRoot(ctx context.Context, tx bun.Tx, rootID uuid.UUID, confirmedPath string, confirmedLocations int64) error {
	var operations []uuid.UUID
	if err := tx.NewRaw(`SELECT DISTINCT operation_id FROM source_cleanup_owner
		WHERE source_root_id=? ORDER BY operation_id`, rootID).Scan(ctx, &operations); err != nil {
		return fmt.Errorf("list root cleanup owners: %w", err)
	}
	if len(operations) == 0 {
		return nil
	}

	for _, operationID := range operations {
		if _, err := tx.NewRaw(`INSERT INTO source_cleanup_root_abandonment
			(original_operation_id,root_id,confirmed_path,confirmed_location_count)
			VALUES (?,?,?,?)`, operationID, rootID, confirmedPath, confirmedLocations).Exec(ctx); err != nil {
			return fmt.Errorf("capture cleanup abandonment for operation %s: %w", operationID, err)
		}
	}

	for _, operationID := range operations {
		var rawEvidence []byte
		if err := tx.NewRaw(`SELECT evidence FROM source_cleanup_root_abandonment
			WHERE original_operation_id=? AND root_id=? AND transaction_id=pg_current_xact_id()`, operationID, rootID).Scan(ctx, &rawEvidence); err != nil {
			return fmt.Errorf("read canonical cleanup abandonment evidence for operation %s: %w", operationID, err)
		}
		var evidence sourceCleanupRootAbandonmentEvidence
		if err := json.Unmarshal(rawEvidence, &evidence); err != nil {
			return fmt.Errorf("decode canonical cleanup abandonment evidence for operation %s: %w", operationID, err)
		}
		if len(evidence.Items) == 0 || len(evidence.Owners) != len(evidence.Items) || len(evidence.OriginalClaim) == 0 {
			return fmt.Errorf("cleanup abandonment evidence for operation %s is incomplete", operationID)
		}

		var retryOperationIDs []uuid.UUID
		var recoveryClaims int64
		if evidence.RetryOperation != nil {
			if evidence.RetryOperation.ID == uuid.Nil || len(evidence.RetryGrant) == 0 || string(evidence.RetryGrant) == "null" {
				return fmt.Errorf("cleanup abandonment retry evidence for operation %s is incomplete", operationID)
			}
			retryOperationIDs = append(retryOperationIDs, evidence.RetryOperation.ID)
			if len(evidence.RetryClaim) != 0 && string(evidence.RetryClaim) != "null" {
				recoveryClaims = 1
			}
		}

		var expectedRunnableJobs int64
		for _, job := range evidence.RiverJobs {
			if job.State == "available" || job.State == "pending" || job.State == "retryable" || job.State == "scheduled" {
				expectedRunnableJobs++
			}
		}
		if expectedRunnableJobs > 0 {
			result, err := tx.NewRaw(`UPDATE river_job SET state='cancelled',finalized_at=now()
				WHERE id IN (SELECT (entry->>'id')::bigint
					FROM source_cleanup_root_abandonment receipt,
						LATERAL jsonb_array_elements(receipt.evidence->'river_jobs') entry
					WHERE receipt.original_operation_id=? AND receipt.root_id=?
						AND receipt.transaction_id=pg_current_xact_id()
						AND entry->>'state' IN ('available','pending','retryable','scheduled'))
				AND state IN ('available','pending','retryable','scheduled')`, operationID, rootID).Exec(ctx)
			if err != nil {
				return fmt.Errorf("cancel captured cleanup deliveries for operation %s: %w", operationID, err)
			}
			if err := requireRowsAffected(result, expectedRunnableJobs, "cancel captured cleanup deliveries"); err != nil {
				return err
			}
		}

		deletions := []struct {
			query    string
			label    string
			args     []any
			expected int64
		}{
			{`DELETE FROM source_cleanup_explicit_retry_resolution WHERE retry_id IN
				(SELECT id FROM source_cleanup_explicit_retry WHERE original_operation_id=?)`, "delete retry resolutions", []any{operationID}, int64(len(evidence.RetryResolutions))},
			{`DELETE FROM source_cleanup_explicit_retry_item WHERE original_operation_id=?`, "delete retry item bindings", []any{operationID}, int64(len(evidence.RetryItems))},
			{`DELETE FROM source_cleanup_explicit_retry WHERE original_operation_id=?`, "delete retry grant", []any{operationID}, boolCount(evidence.RetryOperation != nil)},
			{`DELETE FROM source_cleanup_owner WHERE operation_id=?`, "delete cleanup owners", []any{operationID}, int64(len(evidence.Owners))},
			{`DELETE FROM source_cleanup_recovery_item WHERE original_operation_id=?`, "delete recovery frontier items", []any{operationID}, int64(len(evidence.RecoveryItems))},
			{`DELETE FROM source_cleanup_recovery_state WHERE original_operation_id=?`, "delete recovery frontier state", []any{operationID}, boolCount(len(evidence.RecoveryState) != 0 && string(evidence.RecoveryState) != "null")},
			{`DELETE FROM source_cleanup_item WHERE operation_id=?`, "delete cleanup items", []any{operationID}, int64(len(evidence.Items))},
			{`DELETE FROM publication_execution_claim WHERE operation_id=?`, "delete original recovery claim", []any{operationID}, 1},
		}
		if evidence.RetryOperation != nil {
			deletions = append(deletions, struct {
				query    string
				label    string
				args     []any
				expected int64
			}{`DELETE FROM publication_execution_claim WHERE operation_id=?`, "delete retry recovery claim", []any{evidence.RetryOperation.ID}, recoveryClaims})
		}
		for _, deletion := range deletions {
			result, err := tx.NewRaw(deletion.query, deletion.args...).Exec(ctx)
			if err != nil {
				return fmt.Errorf("%s for cleanup operation %s: %w", deletion.label, operationID, err)
			}
			if err := requireRowsAffected(result, deletion.expected, deletion.label); err != nil {
				return err
			}
		}

		result, err := tx.NewRaw(`DELETE FROM operation WHERE id=?`, operationID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("delete failed cleanup operation %s: %w", operationID, err)
		}
		if err := requireRowsAffected(result, 1, "delete failed cleanup operation"); err != nil {
			return err
		}
		for _, retryID := range retryOperationIDs {
			result, err := tx.NewRaw(`DELETE FROM operation WHERE id=? AND kind='retry_publication_sources_v1' AND state='failed'`, retryID).Exec(ctx)
			if err != nil {
				return fmt.Errorf("delete failed cleanup retry operation %s: %w", retryID, err)
			}
			if err := requireRowsAffected(result, 1, "delete failed cleanup retry operation"); err != nil {
				return err
			}
		}
	}
	return nil
}

func boolCount(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
