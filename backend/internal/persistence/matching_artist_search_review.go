package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// persistAutomaticArtistSearchReview turns only an accepted automatic artist
// search with no usable result into the existing evaluated review contract.
// It runs in the caller's park transaction so neither half can become visible
// without the other.
func persistAutomaticArtistSearchReview(
	ctx context.Context,
	tx bun.Tx,
	runID uuid.UUID,
	intent MatchingAcquisitionIntent,
	request json.RawMessage,
	provenance matchingAcquisitionResponseProvenance,
	payload json.RawMessage,
	source matchingAcquisitionSourceFence,
) error {
	if intent.SchemaVersion != matchingAutomaticAcquisitionIntentVersion || len(intent.CapturedMembers) == 0 ||
		!intent.EvaluationRevisionCaptured || provenance.RequestID == uuid.Nil || provenance.CacheKey == "" || len(payload) == 0 {
		return fmt.Errorf("automatic artist-search review evidence is incomplete")
	}
	if intent.ExpectedEvaluationRevision != nil && *intent.ExpectedEvaluationRevision == "" {
		return fmt.Errorf("expected evaluation revision is invalid")
	}
	var target struct {
		GroupID           uuid.UUID `json:"group_id"`
		Revision          string    `json:"group_revision"`
		AuthorityRevision int64     `json:"authority_revision"`
		Members           []struct {
			VariantID uuid.UUID                        `json:"variant_id"`
			Locations []IncomingGroupingMemberLocation `json:"locations"`
		} `json:"members"`
	}
	var storedTarget struct {
		TargetCapture json.RawMessage `bun:"target_capture"`
		TargetDigest  []byte          `bun:"target_capture_digest"`
	}
	if err := tx.NewRaw(`SELECT target_capture,target_capture_digest FROM matching_run_target WHERE run_id=?`, runID).Scan(ctx, &storedTarget); err != nil {
		return fmt.Errorf("read immutable target capture: %w", err)
	}
	targetRaw := storedTarget.TargetCapture
	targetDigest := storedTarget.TargetDigest
	canonicalTarget, digest, err := automaticArtistTargetCaptureDigest(targetRaw)
	if err != nil || !equalDigest(digest[:], targetDigest) || json.Unmarshal(canonicalTarget, &target) != nil || target.GroupID == uuid.Nil || target.Revision == "" || len(target.Members) == 0 {
		return fmt.Errorf("immutable target capture is invalid")
	}
	searchDigest := sha256.Sum256(payload)
	if fmt.Sprintf("%x", searchDigest[:]) != provenance.PayloadSHA {
		return fmt.Errorf("accepted search digest changed")
	}
	var namespace, cacheRevision string
	if err := tx.NewRaw(`SELECT source.namespace,cache.revision FROM provider_source source
		JOIN provider_response_cache cache ON cache.provider_source_id=source.id AND cache.cache_key=?
		WHERE source.id=?`, provenance.CacheKey, source.SourceID).Scan(ctx, &namespace, &cacheRevision); err != nil {
		return fmt.Errorf("read accepted artist-search cache fence: %w", err)
	}
	if namespace == "" || cacheRevision == "" {
		return fmt.Errorf("accepted artist-search cache fence is incomplete")
	}
	var currentMembers []uuid.UUID
	if err := tx.NewRaw(`SELECT variant_id FROM incoming_group_member WHERE group_id=? ORDER BY variant_id`, target.GroupID).Scan(ctx, &currentMembers); err != nil {
		return fmt.Errorf("read current group members: %w", err)
	}
	if len(currentMembers) != len(target.Members) {
		return fmt.Errorf("current group differs from target capture")
	}
	memberCapture := make(map[uuid.UUID]MatchingAcquisitionCapturedMember, len(intent.CapturedMembers))
	for _, member := range intent.CapturedMembers {
		if member.MemberID == uuid.Nil || member.WorkID == uuid.Nil || !json.Valid(member.Tags) {
			return fmt.Errorf("captured member metadata is invalid")
		}
		if _, exists := memberCapture[member.MemberID]; exists {
			return fmt.Errorf("captured member metadata is duplicated")
		}
		memberCapture[member.MemberID] = member
	}
	if len(memberCapture) != len(target.Members) {
		return fmt.Errorf("captured metadata does not cover target members")
	}
	proposalFiles := make([]map[string]any, 0, len(target.Members))
	locationsByMember := make(map[uuid.UUID][]IncomingGroupingMemberLocation, len(target.Members))
	for index, member := range target.Members {
		if member.VariantID != currentMembers[index] {
			return fmt.Errorf("current group member differs from target capture")
		}
		metadata, ok := memberCapture[member.VariantID]
		if !ok || len(member.Locations) == 0 {
			return fmt.Errorf("target member metadata or locations are missing")
		}
		for _, location := range member.Locations {
			if location.VariantID != member.VariantID || location.RootID == uuid.Nil || location.LocationID == uuid.Nil || location.WorkID == uuid.Nil {
				return fmt.Errorf("target member location fence is invalid")
			}
			locationsByMember[member.VariantID] = append(locationsByMember[member.VariantID], location)
		}
		var rawTags map[string][]string
		if err := json.Unmarshal(metadata.Tags, &rawTags); err != nil || rawTags == nil {
			return fmt.Errorf("captured raw tags are invalid")
		}
		proposalFiles = append(proposalFiles, map[string]any{"file_member_id": member.VariantID, "raw_tags": rawTags, "diagnostics": []any{}})
	}
	for memberID := range memberCapture {
		found := false
		for _, member := range target.Members {
			if member.VariantID == memberID {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("captured metadata contains non-target members")
		}
	}
	var requestObject map[string]any
	if err := json.Unmarshal(request, &requestObject); err != nil || requestObject == nil {
		return fmt.Errorf("accepted artist-search request is invalid")
	}
	reviewProposal, err := json.Marshal(map[string]any{"version": 1, "group_id": target.GroupID, "selected_release": nil, "releases": []any{}, "files": proposalFiles})
	if err != nil {
		return fmt.Errorf("encode review proposal: %w", err)
	}
	evidence, err := json.Marshal(map[string]any{
		"evaluation_decision":    map[string]any{"version": 1, "group_id": target.GroupID, "policy_version": intent.PolicyVersion, "outcome": "review", "selected_release": nil, "assignments": []any{}},
		"review_proposal":        json.RawMessage(reviewProposal),
		"accepted_artist_search": map[string]any{"request_id": provenance.RequestID, "cache_key": provenance.CacheKey, "request": requestObject, "payload_sha256": provenance.PayloadSHA, "payload_base64": base64.StdEncoding.EncodeToString(payload)},
	})
	if err != nil {
		return fmt.Errorf("encode artist-search review evidence: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext(?))`, "incoming-match-evaluation:"+target.GroupID.String()); err != nil {
		return fmt.Errorf("lock prior evaluation revision: %w", err)
	}
	var prior sql.NullString
	priorErr := tx.NewRaw(`SELECT evaluation_revision FROM incoming_match_evaluation WHERE group_id=? FOR UPDATE`, target.GroupID).Scan(ctx, &prior)
	if priorErr != nil && priorErr != sql.ErrNoRows {
		return fmt.Errorf("read prior evaluation revision: %w", priorErr)
	}
	if priorErr == sql.ErrNoRows {
		prior.Valid = false
	}
	if intent.ExpectedEvaluationRevision == nil {
		if prior.Valid {
			return fmt.Errorf("evaluation revision changed since automatic artist admission")
		}
	} else if !prior.Valid || prior.String != *intent.ExpectedEvaluationRevision {
		return fmt.Errorf("evaluation revision changed since automatic artist admission")
	}
	var groupingEpoch int64
	if err := tx.NewRaw(`SELECT revision FROM incoming_grouping_state WHERE singleton=true`).Scan(ctx, &groupingEpoch); err != nil {
		return fmt.Errorf("read grouping epoch: %w", err)
	}
	providerFences, _ := json.Marshal(map[string]any{"release_keys": []string{}, "accepted_artist_search": map[string]any{"request_id": provenance.RequestID, "cache_key": provenance.CacheKey, "payload_sha256": provenance.PayloadSHA}})
	if _, err := tx.NewRaw(`DELETE FROM incoming_match_evaluation WHERE group_id=?`, target.GroupID).Exec(ctx); err != nil {
		return fmt.Errorf("remove prior evaluation: %w", err)
	}
	if _, err := tx.NewRaw(`INSERT INTO incoming_match_evaluation
		(group_id,evaluation_revision,evaluation_evidence,group_revision,groups_revision,grouping_epoch,policy_version,
		provider_source_id,provider_id,provider_configuration_identity,provider_namespace,provider_endpoint,cache_key,cache_revision,
		provider_snapshot_revision,provider_fences,matching_authority_mode,matching_authority_revision,fence_version,target_run_id,target_capture,target_capture_digest)
		VALUES (?,?,?::jsonb,?,?,?,?,?,?,?,?,?,?,?,?,?::jsonb,'automatic',?,2,?,?,?)`, target.GroupID, uuid.NewString(), string(evidence), target.Revision,
		target.Revision, groupingEpoch, intent.PolicyVersion, source.SourceID, source.ProviderID, source.Identity, namespace, source.Endpoint,
		provenance.CacheKey, cacheRevision, provenance.PayloadSHA, string(providerFences), target.AuthorityRevision, runID, string(canonicalTarget), targetDigest).Exec(ctx); err != nil {
		return fmt.Errorf("insert evaluated review header: %w", err)
	}
	for _, member := range target.Members {
		metadata := memberCapture[member.VariantID]
		if _, err := tx.NewRaw(`INSERT INTO incoming_match_evaluation_member(group_id,file_member_id,work_id)
			VALUES (?,?,?)`, target.GroupID, member.VariantID, metadata.WorkID).Exec(ctx); err != nil {
			return fmt.Errorf("copy evaluated review member: %w", err)
		}
		for _, location := range locationsByMember[member.VariantID] {
			if _, err := tx.NewRaw(`INSERT INTO incoming_match_evaluation_location
				(group_id,file_member_id,source_root_id,location_id,configured_path,inventory_path,relative_path,size_bytes,mtime)
				VALUES (?,?,?,?,?,?,?,?,?)`, target.GroupID, member.VariantID, location.RootID, location.LocationID,
				location.ConfiguredPath, location.InventoryPath, location.RelativePath, location.SizeBytes, location.Mtime).Exec(ctx); err != nil {
				return fmt.Errorf("copy evaluated review location: %w", err)
			}
		}
	}
	return nil
}
