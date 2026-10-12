package persistence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// SealMatchingFallbackAuthority persists the authority derived from the durable
// wait. It is receipt-only: it does not consume a delivery or advance a phase.
func (repository *SetupManagerRepository) SealMatchingFallbackAuthority(
	ctx context.Context, runID uuid.UUID, expectedWaitRevision int64,
) (MatchingFallbackSealAuthority, bool, error) {
	var result MatchingFallbackSealAuthority
	if repository == nil || runID == uuid.Nil || expectedWaitRevision < 1 {
		return result, false, fmt.Errorf("seal fallback authority: run and wait revision are required")
	}
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("seal fallback authority: lock output gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("seal fallback authority: lock source mutation gate: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(hashtext(?))`, "musicbrainz-config"); err != nil {
			return fmt.Errorf("seal fallback authority: lock provider configuration: %w", err)
		}
		var run struct {
			State     string    `bun:"state"`
			SourceID  uuid.UUID `bun:"provider_source_id"`
			Identity  string    `bun:"provider_configuration_identity"`
			Fence     int       `bun:"fence_version"`
			ActiveJob *int64    `bun:"active_river_job_id"`
		}
		if err := tx.NewRaw(`SELECT state,provider_source_id,provider_configuration_identity,fence_version,active_river_job_id
			FROM matching_run WHERE id=? FOR UPDATE`, runID).Scan(ctx, &run); err != nil {
			return fmt.Errorf("seal fallback authority: read run: %w", err)
		}
		if run.State != "waiting_policy" || run.ActiveJob != nil {
			return fmt.Errorf("seal fallback authority: run is not waiting for policy")
		}
		var configIdentity, sourceIdentity string
		if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &configIdentity); err != nil || configIdentity != run.Identity {
			return fmt.Errorf("seal fallback authority: provider configuration changed")
		}
		if err := tx.NewRaw(`SELECT configuration_identity FROM provider_source WHERE id=? FOR SHARE`, run.SourceID).Scan(ctx, &sourceIdentity); err != nil || sourceIdentity != run.Identity {
			return fmt.Errorf("seal fallback authority: provider source changed")
		}
		var operation *Operation
		operation, operationErr := repository.GetOperationForUpdate(ctx, tx, runID)
		if operationErr != nil {
			return operationErr
		}
		if operation == nil || operation.State != "running" {
			return fmt.Errorf("seal fallback authority: operation attempt is not current")
		}
		var currentRevision int64
		if err := tx.NewRaw(`SELECT revision FROM matching_acquisition WHERE run_id=? FOR UPDATE`, runID).Scan(ctx, &currentRevision); err != nil || currentRevision != expectedWaitRevision {
			return fmt.Errorf("seal fallback authority: durable wait revision changed")
		}
		// Validate current target generations and manual-authority fences before
		// deduplication. This prevents an old seal from being replayed after a
		// current target has become stale.
		if run.Fence != 2 {
			return fmt.Errorf("seal fallback authority: unsupported target fence version %d", run.Fence)
		}
		var groups []uuid.UUID
		if err := tx.NewRaw(`SELECT generation.group_id FROM incoming_matching_generation generation
			JOIN matching_run_target target ON target.group_id=generation.group_id AND target.run_id=?
			JOIN incoming_group current_group ON current_group.id=target.group_id
			ORDER BY generation.group_id FOR UPDATE OF generation,current_group`, runID).Scan(ctx, &groups); err != nil {
			return fmt.Errorf("seal fallback authority: lock target generations: %w", err)
		}
		for _, groupID := range groups {
			groupAuthority, err := lockIncomingGroupMatchingAuthority(ctx, tx, groupID)
			if err != nil {
				return err
			}
			var expectedRevision int64
			if err := tx.NewRaw(`SELECT authority_revision FROM matching_run_target WHERE run_id=? AND group_id=?`, runID, groupID).Scan(ctx, &expectedRevision); err != nil ||
				groupAuthority.Mode != "automatic" || groupAuthority.Revision != expectedRevision {
				return fmt.Errorf("seal fallback authority: current group authority changed")
			}
		}
		if err := validateMatchingRunFence(ctx, tx, runID, run.Fence); err != nil {
			return err
		}
		var stale bool
		if err := tx.NewRaw(`SELECT EXISTS (
			SELECT 1 FROM matching_run_target target
			LEFT JOIN incoming_matching_generation generation ON generation.group_id=target.group_id
			LEFT JOIN incoming_group current_group ON current_group.id=target.group_id
			LEFT JOIN incoming_group_matching_authority authority ON authority.group_id=target.group_id
			WHERE target.run_id=? AND (generation.current_run_id IS DISTINCT FROM target.run_id OR
				generation.high_water IS DISTINCT FROM target.target_generation OR
				current_group.revision IS DISTINCT FROM target.captured_group_revision OR
				COALESCE(authority.mode,'automatic') <> 'automatic' OR
				COALESCE(authority.revision,0) <> target.authority_revision))`, runID).Scan(ctx, &stale); err != nil {
			return fmt.Errorf("seal fallback authority: validate current target: %w", err)
		}
		if stale {
			return fmt.Errorf("seal fallback authority: current target authority changed")
		}
		// Derive once inside this same transaction, from persisted inputs only.
		authority, found, err := buildMatchingFallbackSealAuthority(ctx, tx, runID, expectedWaitRevision, false)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("seal fallback authority: expected durable wait is not current")
		}
		encoded, err := json.Marshal(authority)
		if err != nil {
			return fmt.Errorf("seal fallback authority: encode authority: %w", err)
		}
		digest, err := hex.DecodeString(authority.Digest)
		if err != nil || len(digest) != sha256.Size {
			return fmt.Errorf("seal fallback authority: derived digest is invalid")
		}
		var existing struct {
			Schema  int             `bun:"schema_version"`
			Payload json.RawMessage `bun:"authority"`
			Digest  []byte          `bun:"authority_digest"`
		}
		err = tx.NewRaw(`SELECT schema_version,authority,authority_digest FROM matching_fallback_authority_seal
			WHERE run_id=? AND wait_revision=?`, runID, expectedWaitRevision).Scan(ctx, &existing)
		if err == nil {
			existingCanonical, canonicalErr := canonicalMatchingJSON(existing.Payload)
			newCanonical, newCanonicalErr := canonicalMatchingJSON(encoded)
			if existing.Schema != matchingFallbackSealAuthoritySchemaVersion || canonicalErr != nil || newCanonicalErr != nil || !bytes.Equal(existingCanonical, newCanonical) || !equalDigest(existing.Digest, digest) {
				return fmt.Errorf("seal fallback authority: existing seal conflicts with derived authority")
			}
			result = authority
			return nil
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("seal fallback authority: read existing seal: %w", err)
		}
		if _, err := tx.NewRaw(`INSERT INTO matching_fallback_authority_seal
			(run_id,wait_revision,schema_version,authority,authority_digest) VALUES (?,?,3,?::jsonb,?)`,
			runID, expectedWaitRevision, string(encoded), digest).Exec(ctx); err != nil {
			return fmt.Errorf("seal fallback authority: persist seal: %w", err)
		}
		result = authority
		return nil
	})
	return result, err == nil, err
}

// GetMatchingFallbackAuthoritySeal reads only the immutable seal and its
// immutable historical wait/receipt provenance; it never consults current
// provider, source, configuration, or cache state.
func (repository *SetupManagerRepository) GetMatchingFallbackAuthoritySeal(
	ctx context.Context, runID uuid.UUID, waitRevision int64,
) (MatchingFallbackSealAuthority, bool, error) {
	var result MatchingFallbackSealAuthority
	if repository == nil || runID == uuid.Nil || waitRevision < 1 {
		return result, false, fmt.Errorf("read fallback authority seal: run and wait revision are required")
	}
	var schema int
	var payload json.RawMessage
	var digest []byte
	err := repository.db.NewRaw(`SELECT schema_version,authority,authority_digest FROM matching_fallback_authority_seal
		WHERE run_id=? AND wait_revision=?`, runID, waitRevision).Scan(ctx, &schema, &payload, &digest)
	if err == sql.ErrNoRows {
		return result, false, nil
	}
	if err != nil {
		return result, false, fmt.Errorf("read fallback authority seal: %w", err)
	}
	if schema != matchingFallbackSealAuthoritySchemaVersion {
		return result, false, fmt.Errorf("read fallback authority seal: unsupported schema version %d", schema)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return MatchingFallbackSealAuthority{}, false, fmt.Errorf("read fallback authority seal: decode payload: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF || result.SchemaVersion != schema || result.RunID != runID || result.WaitRevision != waitRevision {
		return MatchingFallbackSealAuthority{}, false, fmt.Errorf("read fallback authority seal: payload identity is invalid")
	}
	canonical, err := canonicalMatchingFallbackSealAuthorityBytes(result)
	if err != nil {
		return MatchingFallbackSealAuthority{}, false, fmt.Errorf("read fallback authority seal: canonicalize payload: %w", err)
	}
	want := sha256.Sum256(canonical)
	if !equalDigest(digest, want[:]) || result.Digest != hex.EncodeToString(want[:]) {
		return MatchingFallbackSealAuthority{}, false, fmt.Errorf("read fallback authority seal: digest mismatch")
	}
	// Reconstruct the expected authority from the original immutable wait and
	// receipts. Historical mode intentionally avoids the mutable current cursor.
	expected, found, err := buildMatchingFallbackSealAuthority(ctx, repository.db, runID, waitRevision, true)
	if err != nil {
		return MatchingFallbackSealAuthority{}, false, err
	}
	if !found {
		return MatchingFallbackSealAuthority{}, false, fmt.Errorf("read fallback authority seal: original wait provenance is missing")
	}
	expectedBytes, err := canonicalMatchingFallbackSealAuthorityBytes(expected)
	if err != nil || !bytes.Equal(canonical, expectedBytes) {
		return MatchingFallbackSealAuthority{}, false, fmt.Errorf("read fallback authority seal: payload differs from original wait authority")
	}
	return result, true, nil
}
