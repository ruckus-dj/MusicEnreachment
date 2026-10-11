package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// MatchingRunFenceSnapshot is the immutable target authority a v2 evaluator
// binds into its result. Capture is the admission-time capture, never rebuilt
// from the mutable grouping tables by this reader.
type MatchingRunFenceSnapshot struct {
	Version       int
	GroupID       uuid.UUID
	GroupRevision string
	Capture       json.RawMessage
	CaptureDigest string
}

func (repository *SetupManagerRepository) GetMatchingRunFence(ctx context.Context, runID uuid.UUID) (MatchingRunFenceSnapshot, bool, error) {
	var result MatchingRunFenceSnapshot
	if runID == uuid.Nil {
		return result, false, fmt.Errorf("read matching run fence: run id is required")
	}
	var row struct {
		IntentVersion int    `bun:"intent_version"`
		Intent        []byte `bun:"intent"`
		Plan          []byte `bun:"immutable_plan"`
		PlanDigest    []byte `bun:"plan_digest"`
	}
	err := repository.db.NewRaw(`SELECT COALESCE(a.intent_version,0) AS intent_version,COALESCE(a.intent,'null'::jsonb) AS intent,r.immutable_plan,r.plan_digest FROM matching_run r LEFT JOIN matching_acquisition a ON a.run_id=r.id WHERE r.id=?`, runID).Scan(ctx, &row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return result, false, nil
		}
		return result, false, fmt.Errorf("read matching run fence: %w", err)
	}
	var intent MatchingAcquisitionIntent
	var plan struct {
		SchemaVersion       int    `json:"schema_version"`
		Mode                string `json:"mode"`
		TargetCaptureDigest string `json:"target_capture_digest"`
	}
	canonicalPlan, err := canonicalMatchingJSON(row.Plan)
	if err != nil || json.Unmarshal(row.Plan, &plan) != nil {
		return result, false, fmt.Errorf("read matching run fence: immutable run is not a supported version-2 automatic target")
	}
	if plan.SchemaVersion == 2 || plan.SchemaVersion == 3 {
		return result, false, nil
	}
	if row.IntentVersion == matchingAutomaticAcquisitionIntentVersion {
		if json.Unmarshal(row.Intent, &intent) != nil || intent.SchemaVersion != matchingAutomaticAcquisitionIntentVersion || intent.FenceVersion != 2 {
			return result, false, fmt.Errorf("read matching run fence: version-2 acquisition intent is invalid")
		}
		if plan.SchemaVersion != 4 {
			return result, false, fmt.Errorf("read matching run fence: version-2 immutable plan is invalid")
		}
	} else {
		return result, false, fmt.Errorf("read matching run fence: immutable plan version is unsupported")
	}
	if plan.Mode != "automatic" {
		return result, false, fmt.Errorf("read matching run fence: version-2 acquisition intent is invalid or missing")
	}
	planDigest := sha256.Sum256(canonicalPlan)
	if !equalDigest(row.PlanDigest, planDigest[:]) {
		return result, false, fmt.Errorf("read matching run fence: immutable plan digest changed")
	}
	var targets []struct {
		GroupID  uuid.UUID       `bun:"group_id"`
		Revision string          `bun:"captured_group_revision"`
		Capture  json.RawMessage `bun:"target_capture"`
		Digest   []byte          `bun:"target_capture_digest"`
	}
	if err := repository.db.NewRaw(`SELECT group_id,captured_group_revision,target_capture,target_capture_digest FROM matching_run_target WHERE run_id=? ORDER BY group_id`, runID).Scan(ctx, &targets); err != nil {
		return result, false, fmt.Errorf("read matching run fence: %w", err)
	}
	if len(targets) != 1 {
		return result, false, fmt.Errorf("read matching run fence: version-2 target cardinality is invalid")
	}
	canonicalCapture, digest, err := automaticArtistTargetCaptureDigest(targets[0].Capture)
	if err != nil || !equalDigest(digest[:], targets[0].Digest) || hex.EncodeToString(digest[:]) != plan.TargetCaptureDigest {
		return result, false, fmt.Errorf("read matching run fence: target capture digest changed")
	}
	result = MatchingRunFenceSnapshot{Version: 2, GroupID: targets[0].GroupID, GroupRevision: targets[0].Revision, Capture: canonicalCapture, CaptureDigest: hex.EncodeToString(digest[:])}
	return result, true, nil
}

// validateMatchingRunFence validates the semantic target fence for a run. The
// caller has already acquired the provider-configuration and source-mutation
// gates. Version 1 deliberately retains the legacy global-epoch checks at its
// call sites; version 2 binds the original complete target capture instead.
func validateMatchingRunFence(ctx context.Context, tx bun.Tx, runID uuid.UUID, fenceVersion int) error {
	return validateMatchingRunFenceWithFreshness(ctx, tx, runID, fenceVersion, true)
}

// validateMatchingRunFenceCurrentTarget ignores unrelated grouping refreshes;
// exact target capture, authority, and source identities are still revalidated.
func validateMatchingRunFenceCurrentTarget(ctx context.Context, tx bun.Tx, runID uuid.UUID) error {
	return validateMatchingRunFenceWithFreshness(ctx, tx, runID, 2, false)
}

func validateMatchingRunFenceWithFreshness(ctx context.Context, tx bun.Tx, runID uuid.UUID, fenceVersion int, requireFreshGrouping bool) error {
	if fenceVersion != 2 {
		return fmt.Errorf("matching run fence version is unsupported")
	}
	var dirty bool
	if err := tx.NewRaw(`SELECT needs_refresh FROM incoming_grouping_state WHERE singleton=true`).Scan(ctx, &dirty); err != nil {
		return fmt.Errorf("validate matching target fence: read grouping freshness: %w", err)
	}
	if dirty && requireFreshGrouping {
		return ErrMatchingTargetNeedsRefresh
	}
	var run struct {
		PlanDigest    []byte `bun:"plan_digest"`
		Plan          []byte `bun:"immutable_plan"`
		IntentDigest  []byte `bun:"intent_digest"`
		Intent        []byte `bun:"intent"`
		IntentVersion int    `bun:"intent_version"`
	}
	if err := tx.NewRaw(`SELECT run.plan_digest,run.immutable_plan,acquisition.intent_digest,acquisition.intent,acquisition.intent_version
		FROM matching_run run JOIN matching_acquisition acquisition ON acquisition.run_id=run.id WHERE run.id=?`, runID).
		Scan(ctx, &run.PlanDigest, &run.Plan, &run.IntentDigest, &run.Intent, &run.IntentVersion); err != nil {
		return fmt.Errorf("validate matching target fence: read immutable plan: %w", err)
	}
	var plan struct {
		SchemaVersion          int             `json:"schema_version"`
		Intent                 string          `json:"intent"`
		Mode                   string          `json:"mode"`
		TargetCaptureDigest    string          `json:"target_capture_digest"`
		PolicyVersion          string          `json:"policy_version"`
		ArtistQueryPlanVersion int             `json:"artist_query_plan_version"`
		QueryDescriptor        json.RawMessage `json:"query_descriptor"`
	}
	if json.Unmarshal(run.Plan, &plan) != nil || plan.SchemaVersion != 4 ||
		plan.Intent != "automatic_artist_acquisition" || plan.Mode != "automatic" ||
		plan.TargetCaptureDigest == "" || plan.PolicyVersion == "" || plan.ArtistQueryPlanVersion < 1 || len(plan.QueryDescriptor) == 0 {
		return fmt.Errorf("validate matching target fence: version-2 plan is invalid")
	}
	canonicalPlan, err := canonicalMatchingJSON(run.Plan)
	if err != nil {
		return fmt.Errorf("validate matching target fence: canonicalize plan: %w", err)
	}
	planDigest := sha256.Sum256(canonicalPlan)
	if !equalDigest(run.PlanDigest, planDigest[:]) {
		return fmt.Errorf("validate matching target fence: immutable plan digest changed")
	}
	var intent MatchingAcquisitionIntent
	if json.Unmarshal(run.Intent, &intent) != nil || run.IntentVersion != matchingAutomaticAcquisitionIntentVersion ||
		intent.SchemaVersion != matchingAutomaticAcquisitionIntentVersion || intent.FenceVersion != 2 {
		return fmt.Errorf("validate matching target fence: automatic acquisition intent is invalid")
	}
	if err := validateCanonicalMatchingAcquisitionIntent(intent, run.IntentDigest, run.PlanDigest, run.Plan); err != nil {
		return fmt.Errorf("validate matching target fence: %w", err)
	}
	var targets []struct {
		GroupID       uuid.UUID       `bun:"group_id"`
		Revision      string          `bun:"captured_group_revision"`
		Capture       json.RawMessage `bun:"target_capture"`
		CaptureDigest []byte          `bun:"target_capture_digest"`
	}
	if err := tx.NewRaw(`SELECT group_id,captured_group_revision,target_capture,target_capture_digest
		FROM matching_run_target WHERE run_id=? ORDER BY group_id FOR UPDATE`, runID).Scan(ctx, &targets); err != nil {
		return fmt.Errorf("validate matching target fence: read immutable targets: %w", err)
	}
	if len(targets) != 1 || len(targets[0].Capture) == 0 || len(targets[0].CaptureDigest) != sha256.Size {
		return fmt.Errorf("validate matching target fence: version-2 target capture is missing")
	}
	target := targets[0]
	storedCanonical, storedHash, err := automaticArtistTargetCaptureDigest(target.Capture)
	if err != nil || !equalDigest(storedHash[:], target.CaptureDigest) || hex.EncodeToString(storedHash[:]) != plan.TargetCaptureDigest {
		return fmt.Errorf("validate matching target fence: immutable target digest differs")
	}
	var original automaticArtistTargetCapture
	if json.Unmarshal(storedCanonical, &original) != nil || original.SchemaVersion != 2 || original.GroupID != target.GroupID || original.GroupRevision != target.Revision {
		return fmt.Errorf("validate matching target fence: immutable target capture is invalid")
	}
	authority, err := lockIncomingGroupMatchingAuthority(ctx, tx, target.GroupID)
	if err != nil {
		return err
	}
	if authority.Mode != "automatic" || authority.Revision != original.AuthorityRevision {
		return ErrIncomingMatchConflict
	}
	var group struct {
		Revision string `bun:"revision"`
	}
	if err := tx.NewRaw(`SELECT revision FROM incoming_group WHERE id=? FOR UPDATE`, target.GroupID).Scan(ctx, &group); err != nil {
		return ErrIncomingMatchConflict
	}
	current, err := captureAutomaticArtistTarget(ctx, tx, target.GroupID, group.Revision, authority.Revision)
	if err != nil {
		return fmt.Errorf("validate matching target fence: capture current target: %w", err)
	}
	currentBytes, err := json.Marshal(current)
	if err != nil {
		return fmt.Errorf("validate matching target fence: encode current target: %w", err)
	}
	_, currentHash, err := automaticArtistTargetCaptureDigest(currentBytes)
	if err != nil || currentHash != storedHash {
		return ErrIncomingMatchConflict
	}
	return nil
}
