package persistence

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// PublishFrozenMatchingEvaluationForDelivery publishes only immutable review
// outcomes. Unique outcomes are deliberately rejected until frozen apply
// materialization is implemented; callers cannot route them through the legacy
// mutable-cache publisher.
func (repository *SetupManagerRepository) PublishFrozenMatchingEvaluationForDelivery(
	ctx context.Context,
	runID uuid.UUID,
	expectedOperationAttempt int,
	riverJobID int64,
	claim MatchingDeliveryClaim,
	publication FrozenMatchingPublication,
	expectedEvaluationCAS map[uuid.UUID]*string,
) (bool, error) {
	if repository == nil || runID == uuid.Nil || expectedOperationAttempt < 1 || riverJobID < 1 || claim.ExecutionEpoch < 1 || claim.RiverAttempt < 1 || expectedEvaluationCAS == nil {
		return false, fmt.Errorf("publish frozen matching evaluation: delivery and evaluation CAS are required")
	}
	if publication.Evaluation.Outcome == "unique" || publication.Evaluation.Decision == "apply" {
		return false, fmt.Errorf("publish frozen matching evaluation: unique apply materialization is not supported")
	}
	if publication.Evaluation.Outcome != "review" && publication.Evaluation.Decision != "review" {
		return false, fmt.Errorf("publish frozen matching evaluation: only review outcomes are supported")
	}
	changed := false
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("publish frozen matching evaluation: lock output gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("publish frozen matching evaluation: lock source mutations: %w", err)
		}
		seal, _, err := validateFrozenMatchingPublicationAuthority(ctx, tx, runID, publication)
		if err != nil {
			return err
		}
		if err := validateMatchingRunFenceCurrentTarget(ctx, tx, runID); err != nil {
			return err
		}
		var source struct {
			ProviderID    uuid.UUID `bun:"provider_id"`
			Endpoint      string    `bun:"endpoint"`
			Configuration string    `bun:"configuration_identity"`
		}
		if err := tx.NewRaw(`SELECT provider_id,endpoint,configuration_identity FROM provider_source WHERE id=? FOR SHARE`, seal.SourceID).Scan(ctx, &source); err != nil || source.ProviderID != seal.ProviderID || source.Endpoint != seal.Endpoint || source.Configuration != seal.SourceConfiguration {
			return fmt.Errorf("publish frozen matching evaluation: current provider source authority changed")
		}
		groupAuthority, err := lockIncomingGroupMatchingAuthority(ctx, tx, publication.Authority.GroupID)
		if err != nil || groupAuthority != seal.GroupAuthority {
			return fmt.Errorf("publish frozen matching evaluation: current group authority changed")
		}
		operation, err := repository.GetOperationForUpdate(ctx, tx, runID)
		if err != nil {
			return err
		}
		if operation.Kind != "matching_evaluation_v1" || operation.State != "running" || operation.Attempt != expectedOperationAttempt || operation.RiverJobID == nil || *operation.RiverJobID != riverJobID {
			return fmt.Errorf("publish frozen matching evaluation: operation delivery is no longer current")
		}
		var run struct {
			State                string `bun:"state"`
			ExecutionEpoch       int64  `bun:"execution_epoch"`
			CurrentRiverJobID    *int64 `bun:"current_river_job_id"`
			ActiveRiverJobID     *int64 `bun:"active_river_job_id"`
			ActiveRiverAttempt   *int   `bun:"active_river_attempt"`
			ActiveExecutionEpoch *int64 `bun:"active_execution_epoch"`
		}
		if err := tx.NewRaw(`SELECT state,execution_epoch,current_river_job_id,active_river_job_id,active_river_attempt,active_execution_epoch
			FROM matching_run WHERE id=? AND operation_id=? FOR UPDATE`, runID, runID).Scan(ctx, &run); err != nil {
			return fmt.Errorf("publish frozen matching evaluation: lock run: %w", err)
		}
		if run.State != "running" || run.ExecutionEpoch != claim.ExecutionEpoch || run.CurrentRiverJobID == nil || *run.CurrentRiverJobID != riverJobID ||
			run.ActiveRiverJobID == nil || *run.ActiveRiverJobID != riverJobID || run.ActiveRiverAttempt == nil || *run.ActiveRiverAttempt != claim.RiverAttempt ||
			run.ActiveExecutionEpoch == nil || *run.ActiveExecutionEpoch != claim.ExecutionEpoch {
			return fmt.Errorf("publish frozen matching evaluation: matching execution is no longer current")
		}
		var registered bool
		if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM matching_delivery WHERE run_id=? AND river_job_id=? AND operation_attempt=? AND consumed_at IS NULL)`, runID, riverJobID, expectedOperationAttempt).Scan(ctx, &registered); err != nil || !registered {
			return fmt.Errorf("publish frozen matching evaluation: delivery is not registered or was consumed")
		}
		var pending int
		if err := tx.NewRaw(`SELECT count(*) FROM matching_provider_dependency WHERE parent_run_id=? AND terminal_outcome IS NULL`, runID).Scan(ctx, &pending); err != nil || pending != 0 {
			return fmt.Errorf("publish frozen matching evaluation: provider dependencies remain unsettled")
		}
		groupID := publication.Authority.GroupID
		if len(expectedEvaluationCAS) != 1 {
			return fmt.Errorf("publish frozen matching evaluation: evaluation CAS must identify exactly the frozen group")
		}
		if _, exists := expectedEvaluationCAS[groupID]; !exists {
			return fmt.Errorf("publish frozen matching evaluation: evaluation CAS group differs from frozen target")
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext(?))`, "incoming-match-evaluation:"+groupID.String()); err != nil {
			return fmt.Errorf("publish frozen matching evaluation: lock evaluation CAS: %w", err)
		}
		var current sql.NullString
		err = tx.NewRaw(`SELECT evaluation_revision FROM incoming_match_evaluation WHERE group_id=? FOR UPDATE`, groupID).Scan(ctx, &current)
		if errorsIsNoRows(err) {
			if expectedEvaluationCAS[groupID] != nil {
				return ErrIncomingMatchConflict
			}
		} else if err != nil {
			return fmt.Errorf("publish frozen matching evaluation: read evaluation CAS: %w", err)
		} else if !current.Valid || expectedEvaluationCAS[groupID] == nil || current.String != *expectedEvaluationCAS[groupID] {
			return ErrIncomingMatchConflict
		}
		if err := persistFrozenReviewEvaluation(ctx, tx, publication, seal); err != nil {
			return err
		}
		now := time.Now().UTC()
		operation.State, operation.Stage = "succeeded", "succeeded"
		operation.SafeError = nil
		operation.FinishedAt = &now
		operation.ProviderDeliveryClaimedAt = nil
		operation.UpdatedAt = now
		if _, err := tx.NewUpdate().Model(operation).Column("state", "stage", "safe_error", "finished_at", "provider_delivery_claimed_at", "updated_at").WherePK().Exec(ctx); err != nil {
			return fmt.Errorf("publish frozen matching evaluation: complete operation: %w", err)
		}
		result, err := tx.NewRaw(`UPDATE matching_run SET state='succeeded',active_river_job_id=NULL,active_river_attempt=NULL,active_execution_epoch=NULL,updated_at=? WHERE id=? AND execution_epoch=?`, now, runID, claim.ExecutionEpoch).Exec(ctx)
		if err != nil {
			return fmt.Errorf("publish frozen matching evaluation: complete run: %w", err)
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			return fmt.Errorf("publish frozen matching evaluation: run completion was not applied")
		}
		result, err = tx.NewRaw(`UPDATE matching_delivery SET consumed_at=? WHERE run_id=? AND river_job_id=? AND operation_attempt=? AND consumed_at IS NULL`, now, runID, riverJobID, expectedOperationAttempt).Exec(ctx)
		if err != nil {
			return fmt.Errorf("publish frozen matching evaluation: consume delivery: %w", err)
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			return fmt.Errorf("publish frozen matching evaluation: delivery consumption was not applied")
		}
		changed = true
		return nil
	})
	return changed, err
}

func validateFrozenMatchingPublicationAuthority(ctx context.Context, tx bun.IDB, runID uuid.UUID, publication FrozenMatchingPublication) (MatchingFallbackSealAuthority, MatchingFallbackCalculationContext, error) {
	a := publication.Authority
	var seal MatchingFallbackSealAuthority
	if a.RunID != runID || a.WaitRevision < 1 || a.SealSchemaVersion != matchingFallbackSealAuthoritySchemaVersion || a.TargetFenceVersion != 2 {
		return seal, MatchingFallbackCalculationContext{}, fmt.Errorf("publish frozen matching evaluation: caller authority identity is invalid")
	}
	var handoff struct {
		Phase    string `bun:"current_phase"`
		Revision int64  `bun:"revision"`
		Version  int    `bun:"provider_authority_version"`
		Digest   string `bun:"seal_digest"`
	}
	err := tx.NewRaw(`SELECT acquisition.current_phase,acquisition.revision,handoff.provider_authority_version,encode(handoff.seal_digest,'hex') AS seal_digest
		FROM matching_acquisition acquisition JOIN matching_fallback_evaluation_handoff handoff ON handoff.run_id=acquisition.run_id
		WHERE acquisition.run_id=? AND handoff.wait_revision=?`, runID, a.WaitRevision).Scan(ctx, &handoff)
	if err != nil || handoff.Phase != MatchingAcquisitionFallbackEvaluationSealed || handoff.Revision != a.WaitRevision+1 || handoff.Version != matchingFallbackEvaluationProposalAuthorityVersion {
		return seal, MatchingFallbackCalculationContext{}, fmt.Errorf("publish frozen matching evaluation: persisted fallback evaluation handoff is not current")
	}
	derived, found, err := buildMatchingFallbackSealAuthority(ctx, tx, runID, a.WaitRevision, true)
	if err != nil || !found {
		return seal, MatchingFallbackCalculationContext{}, fmt.Errorf("publish frozen matching evaluation: persisted fallback wait is not current")
	}
	if handoff.Digest != derived.Digest {
		return seal, MatchingFallbackCalculationContext{}, fmt.Errorf("publish frozen matching evaluation: persisted fallback handoff seal identity is invalid")
	}
	var stored struct {
		Schema  int             `bun:"schema_version"`
		Payload json.RawMessage `bun:"authority"`
		Digest  []byte          `bun:"authority_digest"`
	}
	if err := tx.NewRaw(`SELECT schema_version,authority,authority_digest FROM matching_fallback_authority_seal WHERE run_id=? AND wait_revision=?`, runID, a.WaitRevision).Scan(ctx, &stored); err != nil {
		return seal, MatchingFallbackCalculationContext{}, fmt.Errorf("publish frozen matching evaluation: read persisted authority seal: %w", err)
	}
	if stored.Schema != matchingFallbackSealAuthoritySchemaVersion || !equalDigest(stored.Digest, mustDecodeSHA256(derived.Digest)) || derived.Digest != a.SealDigest {
		return seal, MatchingFallbackCalculationContext{}, fmt.Errorf("publish frozen matching evaluation: persisted seal digest does not match caller authority")
	}
	var storedAuthority MatchingFallbackSealAuthority
	if err := json.Unmarshal(stored.Payload, &storedAuthority); err != nil || storedAuthority.Digest != derived.Digest {
		return seal, MatchingFallbackCalculationContext{}, fmt.Errorf("publish frozen matching evaluation: persisted seal payload is invalid")
	}
	if !frozenAuthorityMatches(a, derived) {
		return seal, MatchingFallbackCalculationContext{}, fmt.Errorf("publish frozen matching evaluation: caller authority differs from persisted seal")
	}
	if derived.SelectedCalculation == nil || len(derived.FullEvaluationContext) == 0 {
		if derived.SelectedCalculation != nil || len(derived.FullEvaluationContext) != 0 || len(publication.Edges) != 0 || len(publication.Inventory.Releases) != 0 ||
			publication.Evaluation.SelectedRelease != "" || len(publication.Evaluation.Assignments) != 0 {
			return seal, MatchingFallbackCalculationContext{}, fmt.Errorf("publish frozen matching evaluation: sealed calculation context is unavailable")
		}
		return derived, MatchingFallbackCalculationContext{}, nil
	}
	calculation, err := getMatchingFallbackCalculationReceipt(ctx, tx, runID, derived.SelectedCalculation.AcquisitionRevision)
	var snapshot MatchingFallbackCalculationContext
	if err != nil || calculation == nil || calculation.StageIndex != derived.SelectedCalculation.StageIndex || json.Unmarshal(calculation.Context, &snapshot) != nil ||
		!matchingJSONEqual(calculation.Context, derived.FullEvaluationContext) {
		return seal, snapshot, fmt.Errorf("publish frozen matching evaluation: sealed calculation context is unavailable")
	}
	if len(calculation.Edges) == 0 || ValidateMatchingFallbackCalculationEdges(snapshot, calculation.Edges) != nil {
		return seal, snapshot, fmt.Errorf("publish frozen matching evaluation: persisted calculation edge universe is invalid")
	}
	if !frozenInventoryMatchesAcceptedProjections(publication.Inventory.Releases, snapshot.LookupReceipts) {
		return seal, snapshot, fmt.Errorf("publish frozen matching evaluation: review inventory differs from accepted release projections")
	}
	callerEdges, err := frozenPublicationCalculationEdges(publication.Edges, publication.Inventory.Members)
	if err != nil || ValidateMatchingFallbackCalculationEdges(snapshot, callerEdges) != nil {
		return seal, snapshot, fmt.Errorf("publish frozen matching evaluation: caller edges do not match the persisted calculation universe")
	}
	return derived, snapshot, nil
}

func frozenInventoryMatchesAcceptedProjections(inventory []MatchingRelease, receipts []MatchingFallbackLookupReceipt) bool {
	accepted := make([]MatchingRelease, 0, len(receipts))
	seen := make(map[string]bool, len(receipts))
	for _, receipt := range receipts {
		var projection ProviderMatchingSnapshot
		if json.Unmarshal(receipt.AcceptedProjection, &projection) != nil || len(projection.Releases) != 1 {
			return false
		}
		release := projection.Releases[0]
		if release.ID == uuid.Nil || release.ProviderKey == "" || seen[release.ProviderKey] {
			return false
		}
		seen[release.ProviderKey] = true
		for index := range release.Tracks {
			release.Tracks[index].Medium++
			release.Tracks[index].Position++
		}
		accepted = append(accepted, release)
	}
	if len(accepted) != len(inventory) {
		return false
	}
	sort.Slice(accepted, func(i, j int) bool { return accepted[i].ProviderKey < accepted[j].ProviderKey })
	actual := append([]MatchingRelease(nil), inventory...)
	sort.Slice(actual, func(i, j int) bool { return actual[i].ProviderKey < actual[j].ProviderKey })
	left, leftErr := json.Marshal(accepted)
	right, rightErr := json.Marshal(actual)
	return leftErr == nil && rightErr == nil && matchingJSONEqual(left, right)
}

func frozenAuthorityMatches(caller FrozenMatchingAuthorityIdentity, derived MatchingFallbackSealAuthority) bool {
	if caller.SealSchemaVersion != derived.SchemaVersion || caller.RunID != derived.RunID || caller.WaitRevision != derived.WaitRevision || caller.SealDigest != derived.Digest ||
		caller.SealDisposition != derived.Disposition || caller.TargetFenceVersion != derived.Target.Version || caller.TargetCaptureDigest != derived.Target.CaptureDigest ||
		caller.SourceID != derived.SourceID || caller.ProviderID != derived.ProviderID || caller.Endpoint != derived.Endpoint || caller.SourceConfiguration != derived.SourceConfiguration ||
		caller.RunProviderConfiguration != derived.RunProviderConfiguration || caller.GroupID != derived.Target.GroupID || caller.GroupRevision != derived.Target.GroupRevision ||
		caller.GroupAuthority != derived.GroupAuthority || !matchingJSONEqual(caller.TargetCapture, derived.Target.Capture) {
		return false
	}
	var context MatchingFallbackCalculationContext
	if len(derived.FullEvaluationContext) == 0 {
		if len(caller.FullEvaluationContext) != 0 || derived.SelectedCalculation != nil || caller.RunSourceID != derived.SourceID || caller.RunProviderID != derived.ProviderID ||
			caller.RunEndpoint != derived.Endpoint || caller.RunConfiguration != derived.RunProviderConfiguration {
			return false
		}
	} else if json.Unmarshal(derived.FullEvaluationContext, &context) != nil || caller.RunSourceID != context.RunSourceID || caller.RunProviderID != context.RunProviderID ||
		caller.RunEndpoint != context.RunEndpoint || caller.RunConfiguration != context.RunConfiguration || caller.CalculationStageIndex != context.StageIndex ||
		!matchingJSONEqual(caller.FullEvaluationContext, derived.FullEvaluationContext) {
		return false
	}
	leftIntent, _ := json.Marshal(caller.Intent)
	rightIntent, _ := json.Marshal(derived.Intent)
	return matchingJSONEqual(leftIntent, rightIntent)
}

func frozenPublicationCalculationEdges(edges []FrozenMatchingScoredEdge, members []FrozenMatchingReviewMember) (json.RawMessage, error) {
	type edge struct {
		MemberID            uuid.UUID `json:"member_id"`
		WorkID              uuid.UUID `json:"work_id"`
		MetadataSHA         string    `json:"metadata_sha256"`
		ReleaseID           string    `json:"release_id"`
		RecordingID         string    `json:"recording_id"`
		ProviderTrackID     uuid.UUID `json:"provider_track_id"`
		Medium              int       `json:"medium"`
		Position            int       `json:"position"`
		Profile             string    `json:"profile"`
		Confidence          float64   `json:"confidence"`
		LookupRequestID     uuid.UUID `json:"lookup_request_id"`
		LookupPayloadSHA    string    `json:"lookup_payload_sha256"`
		LookupProjectionSHA string    `json:"lookup_projection_sha256"`
	}
	result := make([]edge, 0, len(edges))
	workByMember := make(map[uuid.UUID]uuid.UUID, len(members))
	for _, member := range members {
		workByMember[member.FileMemberID] = member.WorkID
	}
	for _, item := range edges {
		var score struct {
			Profile    string  `json:"profile"`
			Confidence float64 `json:"confidence"`
		}
		if json.Unmarshal(item.AssignmentScore, &score) != nil || math.IsNaN(score.Confidence) || math.IsInf(score.Confidence, 0) || score.Confidence < 0 || score.Confidence > 1 {
			return nil, fmt.Errorf("frozen edge score is invalid")
		}
		workID, ok := workByMember[item.FileMemberID]
		if !ok || !json.Valid(item.ReleaseScore) {
			return nil, fmt.Errorf("frozen edge member or release score is invalid")
		}
		var releaseScore struct {
			Confidence float64 `json:"confidence"`
		}
		if json.Unmarshal(item.ReleaseScore, &releaseScore) != nil || math.IsNaN(releaseScore.Confidence) || math.IsInf(releaseScore.Confidence, 0) || releaseScore.Confidence < 0 || releaseScore.Confidence > 1 {
			return nil, fmt.Errorf("frozen release score is invalid")
		}
		result = append(result, edge{MemberID: item.FileMemberID, WorkID: workID, MetadataSHA: item.MetadataInputSHA, ReleaseID: item.ReleaseID, RecordingID: item.RecordingID,
			ProviderTrackID: item.ProviderTrackID, Medium: item.Medium, Position: item.Position, Profile: score.Profile, Confidence: score.Confidence,
			LookupRequestID: item.LookupRequestID, LookupPayloadSHA: item.LookupPayloadSHA, LookupProjectionSHA: item.LookupProjectionSHA})
	}
	encoded, err := json.Marshal(result)
	return encoded, err
}

func persistFrozenReviewEvaluation(ctx context.Context, tx bun.Tx, publication FrozenMatchingPublication, seal MatchingFallbackSealAuthority) error {
	groupID := publication.Authority.GroupID
	if groupID != seal.Target.GroupID || publication.Inventory.GroupID != groupID || publication.Inventory.GroupRevision != seal.Target.GroupRevision || len(publication.Inventory.Members) == 0 {
		return fmt.Errorf("publish frozen matching evaluation: frozen review inventory identity is invalid")
	}
	if !sameFrozenCapturedMetadata(publication.Inventory.CapturedMetadata, seal.CapturedMetadata) {
		return fmt.Errorf("publish frozen matching evaluation: captured member metadata differs from persisted seal")
	}
	if !frozenInventoryMatchesTarget(publication.Inventory, seal.Target.Capture, seal.CapturedMetadata) {
		return fmt.Errorf("publish frozen matching evaluation: frozen target inventory differs from persisted capture")
	}
	if _, err := tx.NewRaw(`DELETE FROM incoming_match_evaluation WHERE group_id=?`, groupID).Exec(ctx); err != nil {
		return fmt.Errorf("publish frozen matching evaluation: replace prior review: %w", err)
	}
	providerFences, _ := json.Marshal(map[string]any{"frozen_authority_version": 3, "run_id": seal.RunID, "wait_revision": seal.WaitRevision, "seal_digest": seal.Digest})
	evidence, err := json.Marshal(map[string]any{"frozen_publication": publication, "review_proposal": frozenReviewProposal(publication)})
	if err != nil {
		return fmt.Errorf("publish frozen matching evaluation: encode review evidence: %w", err)
	}
	evaluationRevision := uuid.NewString()
	sealDigest, _ := hex.DecodeString(seal.Digest)
	if _, err := tx.NewRaw(`INSERT INTO incoming_match_evaluation
		(group_id,evaluation_revision,evaluation_evidence,group_revision,groups_revision,grouping_epoch,policy_version,
		provider_source_id,provider_id,provider_configuration_identity,provider_namespace,provider_endpoint,cache_key,cache_revision,
		provider_snapshot_revision,provider_fences,matching_authority_mode,matching_authority_revision,fence_version,target_run_id,target_capture,target_capture_digest,
		frozen_authority_version,frozen_run_id,frozen_wait_revision,frozen_seal_digest)
		VALUES(?,?,?::jsonb,?,?,0,?,?,?,?,?,?,NULL,NULL,NULL,?::jsonb,?,?,2,?,?,?,3,?,?,?)`, groupID, evaluationRevision, string(evidence), seal.Target.GroupRevision,
		"frozen:"+seal.Digest, seal.Intent.PolicyVersion, seal.SourceID, seal.ProviderID, seal.RunProviderConfiguration, "frozen", seal.Endpoint, string(providerFences), seal.GroupAuthority.Mode, seal.GroupAuthority.Revision,
		seal.RunID, string(seal.Target.Capture), mustDecodeSHA256(seal.Target.CaptureDigest), seal.RunID, seal.WaitRevision, sealDigest).Exec(ctx); err != nil {
		return fmt.Errorf("publish frozen matching evaluation: insert frozen header: %w", err)
	}
	memberByID := make(map[uuid.UUID]FrozenMatchingReviewMember, len(publication.Inventory.Members))
	for _, member := range publication.Inventory.Members {
		if member.FileMemberID == uuid.Nil || member.WorkID == uuid.Nil {
			return fmt.Errorf("publish frozen matching evaluation: invalid review member")
		}
		if _, exists := memberByID[member.FileMemberID]; exists {
			return fmt.Errorf("publish frozen matching evaluation: duplicate review member")
		}
		memberByID[member.FileMemberID] = member
		var captured *MatchingAcquisitionCapturedMember
		for i := range seal.CapturedMetadata {
			if seal.CapturedMetadata[i].MemberID == member.FileMemberID {
				captured = &seal.CapturedMetadata[i]
				break
			}
		}
		if captured == nil || captured.WorkID != member.WorkID || !matchingJSONEqual(captured.Tags, member.Tags) {
			return fmt.Errorf("publish frozen matching evaluation: member fence differs from persisted capture")
		}
		if _, err := tx.NewRaw(`INSERT INTO incoming_match_evaluation_member(group_id,file_member_id,work_id,source_sha256) VALUES(?,?,?,?)`, groupID, member.FileMemberID, member.WorkID, member.SourceSHA256).Exec(ctx); err != nil {
			return fmt.Errorf("publish frozen matching evaluation: insert review member: %w", err)
		}
	}
	if len(memberByID) != len(seal.Intent.CapturedMembers) {
		return fmt.Errorf("publish frozen matching evaluation: review members differ from sealed intent")
	}
	trackByID := make(map[uuid.UUID]MatchingReleaseTrack)
	releaseByKey := make(map[string]MatchingRelease)
	for _, release := range publication.Inventory.Releases {
		releaseByKey[release.ProviderKey] = release
		for _, track := range release.Tracks {
			trackByID[track.ID] = track
		}
	}
	byCandidate := map[string][]FrozenMatchingScoredEdge{}
	for _, edge := range publication.Edges {
		_, ok := memberByID[edge.FileMemberID]
		release, releaseOK := releaseByKey[edge.ReleaseID]
		track, trackOK := trackByID[edge.ProviderTrackID]
		var score struct {
			Confidence float64 `json:"confidence"`
		}
		if !ok || !releaseOK || !trackOK || track.Position < 1 || track.Medium < 1 || track.Position-1 != edge.Position || track.Medium-1 != edge.Medium || !json.Valid(edge.AssignmentScore) || json.Unmarshal(edge.AssignmentScore, &score) != nil || math.IsNaN(score.Confidence) || math.IsInf(score.Confidence, 0) || score.Confidence < 0 || score.Confidence > 1 {
			return fmt.Errorf("publish frozen matching evaluation: review edge identity or score is invalid")
		}
		key := edge.FileMemberID.String() + "/" + release.ID.String()
		byCandidate[key] = append(byCandidate[key], edge)
	}
	for _, edges := range byCandidate {
		e := edges[0]
		release := releaseByKey[e.ReleaseID]
		var score struct {
			Confidence float64 `json:"confidence"`
		}
		for _, edge := range edges {
			var releaseScore struct {
				Confidence float64 `json:"confidence"`
			}
			if json.Unmarshal(edge.ReleaseScore, &releaseScore) != nil || math.IsNaN(releaseScore.Confidence) || math.IsInf(releaseScore.Confidence, 0) || releaseScore.Confidence < 0 || releaseScore.Confidence > 1 {
				return fmt.Errorf("publish frozen matching evaluation: review release score is invalid")
			}
			if releaseScore.Confidence > score.Confidence {
				score.Confidence = releaseScore.Confidence
			}
		}
		edgeEvidence := make([]map[string]any, 0, len(edges))
		for _, edge := range edges {
			var score struct {
				Profile    string          `json:"profile"`
				Confidence float64         `json:"confidence"`
				Factors    json.RawMessage `json:"factors"`
			}
			if json.Unmarshal(edge.AssignmentScore, &score) != nil || score.Profile == "" {
				return fmt.Errorf("publish frozen matching evaluation: review edge score is invalid")
			}
			edgeEvidence = append(edgeEvidence, map[string]any{"schema_version": 1, "medium": edge.Medium + 1, "position": edge.Position + 1,
				"assignment_profile": map[string]any{"profile": score.Profile, "confidence": score.Confidence, "factors": score.Factors}})
		}
		candidateEvidence, _ := json.Marshal(map[string]any{"edge_evidence": edgeEvidence, "outcome": publication.Evaluation.Outcome, "decision": publication.Evaluation.Decision})
		if _, err := tx.NewRaw(`INSERT INTO incoming_match_candidate(group_id,file_member_id,provider_id,provider_source_id,release_id,score,evidence) VALUES(?,?,?,?,?,?,?::jsonb)`, groupID, e.FileMemberID, seal.ProviderID, seal.SourceID, release.ID, score.Confidence, string(candidateEvidence)).Exec(ctx); err != nil {
			return fmt.Errorf("publish frozen matching evaluation: insert review candidate: %w", err)
		}
		for _, edge := range edges {
			track := trackByID[edge.ProviderTrackID]
			if _, err := tx.NewRaw(`INSERT INTO incoming_match_candidate_track(group_id,file_member_id,provider_source_id,provider_id,release_id,track_id,medium,position) VALUES(?,?,?,?,?,?,?,?)`, groupID, e.FileMemberID, seal.SourceID, seal.ProviderID, release.ID, track.ID, track.Medium, track.Position).Exec(ctx); err != nil {
				return fmt.Errorf("publish frozen matching evaluation: insert review layout: %w", err)
			}
		}
	}
	return nil
}

func frozenInventoryMatchesTarget(inventory FrozenMatchingReviewInventory, target json.RawMessage, metadata []MatchingAcquisitionCapturedMember) bool {
	var capture struct {
		Members []struct {
			VariantID uuid.UUID                        `json:"variant_id"`
			Locations []IncomingGroupingMemberLocation `json:"locations"`
			Captures  []IncomingGroupingCapture        `json:"captures"`
		} `json:"members"`
	}
	if json.Unmarshal(target, &capture) != nil || len(capture.Members) != len(inventory.Members) {
		return false
	}
	expectedCaptures := make([]IncomingGroupingCapture, 0)
	memberByID := make(map[uuid.UUID]FrozenMatchingReviewMember, len(inventory.Members))
	for _, member := range inventory.Members {
		if member.FileMemberID == uuid.Nil || memberByID[member.FileMemberID].FileMemberID != uuid.Nil {
			return false
		}
		memberByID[member.FileMemberID] = member
	}
	metadataByID := make(map[uuid.UUID]MatchingAcquisitionCapturedMember, len(metadata))
	for _, item := range metadata {
		metadataByID[item.MemberID] = item
	}
	for _, targetMember := range capture.Members {
		member, exists := memberByID[targetMember.VariantID]
		captured, hasMetadata := metadataByID[targetMember.VariantID]
		if !exists || !hasMetadata || len(targetMember.Captures) == 0 || captured.WorkID != member.WorkID || !matchingJSONEqual(captured.Tags, member.Tags) {
			return false
		}
		first := targetMember.Captures[0]
		if !bytes.Equal(first.SourceSHA256, member.SourceSHA256) {
			return false
		}
		var locations []struct {
			RootID         uuid.UUID `json:"RootID"`
			WorkID         uuid.UUID `json:"WorkID"`
			LocationID     uuid.UUID `json:"LocationID"`
			ConfiguredPath string    `json:"ConfiguredPath"`
			InventoryPath  string    `json:"InventoryPath"`
			RelativePath   string    `json:"RelativePath"`
			SizeBytes      int64     `json:"SizeBytes"`
			Mtime          string    `json:"Mtime"`
		}
		if json.Unmarshal(member.Locations, &locations) != nil || len(locations) != len(targetMember.Locations) {
			return false
		}
		for index, expected := range targetMember.Locations {
			actual := locations[index]
			if actual.RootID != expected.RootID || actual.WorkID != expected.WorkID || actual.LocationID != expected.LocationID ||
				actual.ConfiguredPath != expected.ConfiguredPath || actual.InventoryPath != expected.InventoryPath || actual.RelativePath != expected.RelativePath || actual.SizeBytes != expected.SizeBytes ||
				actual.Mtime != expected.Mtime.Format(time.RFC3339Nano) {
				return false
			}
		}
		expectedCaptures = append(expectedCaptures, targetMember.Captures...)
		delete(memberByID, targetMember.VariantID)
	}
	left, leftErr := json.Marshal(expectedCaptures)
	right, rightErr := json.Marshal(inventory.Captures)
	return len(memberByID) == 0 && leftErr == nil && rightErr == nil && matchingJSONEqual(left, right)
}

func frozenReviewProposal(publication FrozenMatchingPublication) json.RawMessage {
	type layout struct {
		ProviderReleaseID uuid.UUID        `json:"provider_release_id"`
		ReleaseID         string           `json:"release_id"`
		Title             string           `json:"title"`
		TracklistComplete bool             `json:"tracklist_complete"`
		Positions         []map[string]any `json:"positions"`
	}
	releases := make([]layout, 0, len(publication.Inventory.Releases))
	for _, release := range publication.Inventory.Releases {
		positions := make([]map[string]any, 0, len(release.Tracks))
		for _, track := range release.Tracks {
			positions = append(positions, map[string]any{"track_id": track.ID, "medium": track.Medium, "position": track.Position,
				"displayed_number": track.DisplayedNumber, "recording_id": track.RecordingProviderKey, "title": track.RecordingTitle})
		}
		releases = append(releases, layout{ProviderReleaseID: release.ID, ReleaseID: release.ProviderKey, Title: release.Name, TracklistComplete: release.Complete, Positions: positions})
	}
	selected := any(nil)
	if publication.Evaluation.SelectedRelease != "" {
		for _, release := range releases {
			if release.ReleaseID == publication.Evaluation.SelectedRelease {
				selected = release
				break
			}
		}
	}
	files := make([]map[string]any, 0, len(publication.Inventory.Members))
	for _, member := range publication.Inventory.Members {
		var rawTags map[string][]string
		if json.Unmarshal(member.Tags, &rawTags) != nil {
			rawTags = map[string][]string{}
		}
		files = append(files, map[string]any{"file_member_id": member.FileMemberID, "raw_tags": rawTags, "diagnostics": []any{}})
	}
	encoded, _ := json.Marshal(map[string]any{"version": 1, "group_id": publication.Authority.GroupID, "selected_release": selected, "releases": releases, "files": files})
	return encoded
}

func sameFrozenCapturedMetadata(left, right []MatchingAcquisitionCapturedMember) bool {
	if len(left) != len(right) {
		return false
	}
	leftByID := make(map[uuid.UUID]MatchingAcquisitionCapturedMember, len(left))
	for _, item := range left {
		if item.MemberID == uuid.Nil {
			return false
		}
		if _, exists := leftByID[item.MemberID]; exists {
			return false
		}
		leftByID[item.MemberID] = item
	}
	for _, item := range right {
		other, exists := leftByID[item.MemberID]
		if !exists {
			return false
		}
		leftJSON, leftErr := json.Marshal(other)
		rightJSON, rightErr := json.Marshal(item)
		if leftErr != nil || rightErr != nil || !matchingJSONEqual(leftJSON, rightJSON) {
			return false
		}
		delete(leftByID, item.MemberID)
	}
	return len(leftByID) == 0
}

func errorsIsNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }
