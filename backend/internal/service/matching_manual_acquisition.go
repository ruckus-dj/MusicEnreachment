package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/matchingpolicy"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// PrepareManualReleaseLookup evaluates a single explicitly selected release
// from its accepted lookup response. It reuses the regular diagnostic kernel,
// but records manual authority separately from the kernel's decision.
func (evaluator *MatchingEvaluationService) PrepareManualReleaseLookup(
	ctx context.Context,
	snapshot persistence.ManualMatchingRunSnapshot,
) (PreparedMatchingEvaluation, error) {
	var prepared PreparedMatchingEvaluation
	if snapshot.RequestID == uuid.Nil || snapshot.RunID == uuid.Nil || snapshot.GroupID == uuid.Nil ||
		snapshot.SelectedReleaseMBID == uuid.Nil || snapshot.LookupOperationID == uuid.Nil ||
		snapshot.CacheKey != "release-lookup:"+strings.ToLower(snapshot.SelectedReleaseMBID.String()) ||
		snapshot.Pending || len(snapshot.AcceptedPayloadSHA256) != sha256.Size*2 {
		return prepared, fmt.Errorf("manual matching lookup is not accepted")
	}
	if _, err := hex.DecodeString(snapshot.AcceptedPayloadSHA256); err != nil {
		return prepared, fmt.Errorf("manual matching lookup payload digest is invalid")
	}
	if _, err := hex.DecodeString(snapshot.AcceptedProjectionSHA256); err != nil || len(snapshot.AcceptedProjectionSHA256) != sha256.Size*2 ||
		snapshot.AcceptedProviderSourceID == uuid.Nil || snapshot.AcceptedConfigurationID == "" {
		return prepared, fmt.Errorf("manual matching accepted projection seal is invalid")
	}
	config, err := evaluator.config.GetMusicBrainzConfig(ctx)
	if err != nil {
		return prepared, err
	}
	if config.Identity == "" {
		return prepared, fmt.Errorf("manual matching provider configuration is unavailable")
	}
	providerSnapshot, err := evaluator.provider.GetMusicBrainzMatchingSnapshot(ctx, musicBrainzEndpoint(config), config.Identity,
		[]string{snapshot.SelectedReleaseMBID.String()}, []string{snapshot.CacheKey})
	if err != nil {
		return prepared, err
	}
	response := providerSnapshot.Responses[snapshot.CacheKey]
	if len(providerSnapshot.Releases) != 1 || providerSnapshot.Releases[0].ProviderKey != snapshot.SelectedReleaseMBID.String() || !providerSnapshot.Releases[0].Complete || response == nil || len(response.Payload) == 0 {
		return prepared, fmt.Errorf("manual matching accepted release graph is incomplete")
	}
	if providerSnapshot.Source.ID != snapshot.AcceptedProviderSourceID || providerSnapshot.Source.ConfigurationIdentity != snapshot.AcceptedConfigurationID ||
		providerSnapshot.AcceptedProjectionRevision != strings.ToLower(snapshot.AcceptedProjectionSHA256) ||
		!sameOptionalRevision(response.Revision, snapshot.AcceptedCacheRevision) {
		return prepared, fmt.Errorf("manual matching accepted provider projection changed")
	}
	payloadDigest := sha256.Sum256(response.Payload)
	if hex.EncodeToString(payloadDigest[:]) != strings.ToLower(snapshot.AcceptedPayloadSHA256) {
		return prepared, fmt.Errorf("manual matching accepted release payload changed")
	}
	var members []persistence.IncomingMatchMemberFence
	if err := json.Unmarshal(snapshot.OriginalMembers, &members); err != nil || len(members) == 0 {
		return prepared, fmt.Errorf("manual matching captured member scope is invalid")
	}
	evidenceKeys := make(map[uuid.UUID][]string, len(members))
	for _, member := range members {
		if member.FileMemberID == uuid.Nil || member.WorkID == uuid.Nil || (len(member.SourceSHA256) != 0 && len(member.SourceSHA256) != sha256.Size) {
			return prepared, fmt.Errorf("manual matching captured member fence is invalid")
		}
		evidenceKeys[member.FileMemberID] = []string{snapshot.CacheKey}
	}
	input := persistence.MatchingAcquisitionEvaluationInput{
		SchemaVersion: 1, GroupIDs: []uuid.UUID{snapshot.GroupID},
		ReleaseIDs:              []string{snapshot.SelectedReleaseMBID.String()},
		EvidenceCacheKeysByFile: evidenceKeys,
	}
	prepared, err = evaluator.PrepareSealed(ctx, input)
	if err != nil {
		return prepared, err
	}
	if len(prepared.ExpectedRevisions) != 1 {
		return PreparedMatchingEvaluation{}, fmt.Errorf("manual matching evaluation revision scope is invalid")
	}
	if !sameOptionalRevision(prepared.ExpectedRevisions[snapshot.GroupID], snapshot.ExpectedEvaluationRevision) {
		return PreparedMatchingEvaluation{}, fmt.Errorf("manual matching evaluation header changed after admission")
	}
	if !matchingpolicy.Supported(prepared.evaluation.PolicyVersion) {
		return PreparedMatchingEvaluation{}, fmt.Errorf("manual matching policy is unsupported")
	}
	// The matching decision remains the actual output of the scoring kernel.
	// The user-selected release is additional, independent authority.
	for index := range prepared.evaluation.Groups {
		group := &prepared.evaluation.Groups[index]
		if group.GroupID != snapshot.GroupID {
			return PreparedMatchingEvaluation{}, fmt.Errorf("manual matching evaluation escaped its captured group")
		}
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(group.Evidence, &envelope); err != nil || envelope == nil {
			return PreparedMatchingEvaluation{}, fmt.Errorf("manual matching diagnostic evidence is invalid")
		}
		authority, err := json.Marshal(struct {
			Version             int       `json:"version"`
			ExecutionMode       string    `json:"execution_mode"`
			SelectedReleaseMBID uuid.UUID `json:"selected_release_mbid"`
		}{1, "manual_review", snapshot.SelectedReleaseMBID})
		if err != nil {
			return PreparedMatchingEvaluation{}, err
		}
		envelope["manual_authority"] = authority
		lookupEvidence, err := json.Marshal(struct {
			RequestID         uuid.UUID `json:"request_id"`
			GroupID           uuid.UUID `json:"group_id"`
			LookupOperationID uuid.UUID `json:"lookup_operation_id"`
			PayloadSHA256     string    `json:"payload_sha256"`
		}{snapshot.RequestID, snapshot.GroupID, snapshot.LookupOperationID, snapshot.AcceptedPayloadSHA256})
		if err != nil {
			return PreparedMatchingEvaluation{}, err
		}
		envelope["manual_lookup"] = lookupEvidence
		group.Evidence, err = json.Marshal(envelope)
		if err != nil {
			return PreparedMatchingEvaluation{}, fmt.Errorf("encode manual matching authority: %w", err)
		}
	}
	return prepared, nil
}

func sameOptionalRevision(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
