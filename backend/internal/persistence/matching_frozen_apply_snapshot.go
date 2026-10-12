package persistence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/matchingpolicy"
)

// MatchingApplyInputSnapshotV2 is the immutable provider-input snapshot for a
// future automatic matching apply. It intentionally contains no publication
// instructions or local database identities beyond the accepted provider IDs.
type MatchingApplyInputSnapshotV2 struct {
	SchemaVersion int                                  `json:"schema_version"`
	Authority     MatchingApplySnapshotAuthorityV2     `json:"authority"`
	Source        MatchingApplySnapshotSourceV2        `json:"source"`
	Captured      []MatchingAcquisitionCapturedMember  `json:"captured_members"`
	TargetCapture json.RawMessage                      `json:"target_capture"`
	Evaluation    json.RawMessage                      `json:"evaluation_context"`
	Selected      MatchingApplySnapshotSelectedRelease `json:"selected_release"`
	Assignments   []MatchingApplySnapshotAssignment    `json:"assignments"`
}

type MatchingApplySnapshotAuthorityV2 struct {
	ParentRunID         uuid.UUID `json:"parent_run_id"`
	GroupID             uuid.UUID `json:"group_id"`
	SealDisposition     string    `json:"seal_disposition"`
	EvaluationRevision  string    `json:"evaluation_revision"`
	ExecutionEpoch      int64     `json:"execution_epoch"`
	WaitRevision        int64     `json:"wait_revision"`
	SealDigest          string    `json:"seal_digest"`
	TargetFenceVersion  int       `json:"target_fence_version"`
	TargetCaptureDigest string    `json:"target_capture_digest"`
	PolicyVersion       string    `json:"policy_version"`
	AuthorityVersion    int       `json:"authority_version"`
}

type MatchingApplySnapshotSourceV2 struct {
	ID                    uuid.UUID `json:"id"`
	ProviderID            uuid.UUID `json:"provider_id"`
	Endpoint              string    `json:"endpoint"`
	ConfigurationIdentity string    `json:"configuration_identity"`
	RunConfiguration      string    `json:"run_configuration_identity"`
}

type MatchingApplySnapshotLookupV2 struct {
	RequestID        uuid.UUID `json:"request_id"`
	ReleaseID        string    `json:"release_id"`
	CacheKey         string    `json:"cache_key"`
	PayloadSHA256    string    `json:"payload_sha256"`
	ProjectionSHA256 string    `json:"projection_sha256"`
}

type MatchingApplySnapshotSelectedRelease struct {
	ProviderReleaseID uuid.UUID                     `json:"provider_release_id"`
	ProviderKey       string                        `json:"provider_key"`
	OriginalOrdinal   int                           `json:"original_ordinal"`
	Lookup            MatchingApplySnapshotLookupV2 `json:"lookup"`
	Projection        FrozenMaterialization         `json:"projection"`
}

type MatchingApplySnapshotAssignment struct {
	FileMemberID    uuid.UUID `json:"file_member_id"`
	WorkID          uuid.UUID `json:"work_id"`
	SourceSHA256    string    `json:"source_sha256"`
	ProviderTrackID uuid.UUID `json:"provider_track_id"`
	RecordingKey    string    `json:"recording_key"`
	Medium          int       `json:"medium"`
	Position        int       `json:"position"`
}

// BuildMatchingApplyInputSnapshotV2 freezes one selected accepted lookup
// projection. Inputs are the persistence-derived seal/context and the persisted
// unique decision; callers must not substitute provider cache inventory.
func BuildMatchingApplyInputSnapshotV2(
	seal MatchingFallbackSealAuthority,
	context MatchingFallbackCalculationContext,
	evaluationRevision string,
	decision persistedMatchingDecision,
) (MatchingApplyInputSnapshotV2, string, error) {
	var result MatchingApplyInputSnapshotV2
	if seal.SchemaVersion != matchingFallbackSealAuthoritySchemaVersion || seal.Disposition != "qualified" || seal.RunID == uuid.Nil || seal.WaitRevision < 1 ||
		seal.Digest == "" || seal.Target.Version != 2 || seal.Target.GroupID == uuid.Nil || seal.Target.GroupRevision == "" ||
		seal.SourceID == uuid.Nil || seal.ProviderID == uuid.Nil || seal.Endpoint == "" || seal.SourceConfiguration == "" ||
		seal.RunProviderConfiguration == "" || seal.Intent.SchemaVersion != matchingAutomaticFallbackAcquisitionIntentVersion ||
		context.RunID != seal.RunID || context.Intent.SchemaVersion != seal.Intent.SchemaVersion ||
		decision.Version != 1 || decision.Outcome != "unique" || decision.GroupID != seal.Target.GroupID ||
		!matchingpolicy.Supported(decision.PolicyVersion) || decision.SelectedRelease == nil || evaluationRevision == "" {
		return result, "", fmt.Errorf("build matching apply snapshot: sealed authority or selected decision is invalid")
	}
	if len(seal.FullEvaluationContext) == 0 || !matchingJSONEqual(seal.FullEvaluationContext, mustJSON(context)) {
		return result, "", fmt.Errorf("build matching apply snapshot: calculation context differs from seal")
	}
	if !matchingApplySnapshotCaptureCovers(seal.Target.Capture, seal.CapturedMetadata) {
		return result, "", fmt.Errorf("build matching apply snapshot: original target capture does not cover captured members")
	}
	selectedKey := strings.ToLower(decision.SelectedRelease.ProviderReleaseKey)
	if selectedKey == "" || len(context.Lookups) != len(context.LookupReceipts) {
		return result, "", fmt.Errorf("build matching apply snapshot: selected lookup is unavailable")
	}
	var selectedReceipt *MatchingFallbackLookupReceipt
	var selectedAuthority *MatchingFallbackCalculationLookupAuthority
	for i := range context.Lookups {
		authority := &context.Lookups[i]
		receipt := lookupReceiptByID(context.LookupReceipts, authority.RequestID)
		if receipt == nil || authority.ReleaseID != receiptReleaseID(receipt.CacheKey) || authority.ReleaseID != strings.ToLower(authority.ReleaseID) ||
			authority.ProviderSourceID != seal.SourceID || authority.ProviderID != seal.ProviderID || authority.Endpoint != seal.Endpoint ||
			authority.Configuration != seal.RunProviderConfiguration || authority.CacheKey != receipt.CacheKey ||
			receipt.RunID != seal.RunID || receipt.RequestID != authority.RequestID || receipt.ProviderSourceID != seal.SourceID ||
			receipt.ConfigurationIdentity != seal.RunProviderConfiguration || !validSnapshotDigest(receipt.PayloadSHA256) || !validSnapshotDigest(receipt.ProjectionSHA256) {
			return result, "", fmt.Errorf("build matching apply snapshot: registered lookup receipt is inconsistent")
		}
		payloadDigest, payloadErr := matchingSnapshotJSONDigest(receipt.Payload)
		projectionDigest, projectionErr := matchingSnapshotJSONDigest(receipt.AcceptedProjection)
		if payloadErr != nil || projectionErr != nil || payloadDigest != receipt.PayloadSHA256 || projectionDigest != receipt.ProjectionSHA256 {
			return result, "", fmt.Errorf("build matching apply snapshot: registered lookup content digest is invalid (payload %s/%s, projection %s/%s)", payloadDigest, receipt.PayloadSHA256, projectionDigest, receipt.ProjectionSHA256)
		}
		if authority.ReleaseID == selectedKey {
			if selectedReceipt != nil {
				return result, "", fmt.Errorf("build matching apply snapshot: selected lookup is ambiguous")
			}
			selectedReceipt, selectedAuthority = receipt, authority
		}
	}
	if selectedReceipt == nil || selectedAuthority == nil || decision.SelectedRelease.ProviderReleaseID == uuid.Nil {
		return result, "", fmt.Errorf("build matching apply snapshot: selected lookup is not registered")
	}
	var providerSnapshot ProviderMatchingSnapshot
	if err := json.Unmarshal(selectedReceipt.AcceptedProjection, &providerSnapshot); err != nil ||
		providerSnapshot.Source.ID != seal.SourceID || providerSnapshot.Source.ProviderID != seal.ProviderID ||
		providerSnapshot.Source.Endpoint != seal.Endpoint || providerSnapshot.Source.ConfigurationIdentity != seal.RunProviderConfiguration ||
		len(providerSnapshot.Releases) != 1 || !strings.EqualFold(providerSnapshot.Releases[0].ProviderKey, selectedKey) ||
		providerSnapshot.Releases[0].ID != decision.SelectedRelease.ProviderReleaseID {
		return result, "", fmt.Errorf("build matching apply snapshot: accepted selected projection is invalid")
	}
	assignments, err := snapshotAssignments(seal, providerSnapshot.Releases[0], decision)
	if err != nil {
		return result, "", err
	}
	materialized, err := acceptedProjectionToMaterialization(providerSnapshot, selectedKey, frozenAssignments(assignments, providerSnapshot.Releases[0]))
	if err != nil {
		return result, "", fmt.Errorf("build matching apply snapshot: materialize accepted projection: %w", err)
	}
	ordinal, err := matchingApplySnapshotSelectedOrdinal(context.Lookups, selectedKey)
	if err != nil {
		return result, "", fmt.Errorf("build matching apply snapshot: %w", err)
	}
	result = MatchingApplyInputSnapshotV2{
		SchemaVersion: 2,
		Authority: MatchingApplySnapshotAuthorityV2{ParentRunID: seal.RunID, GroupID: seal.Target.GroupID, SealDisposition: seal.Disposition,
			EvaluationRevision: evaluationRevision, ExecutionEpoch: context.SearchReceipt.ExecutionEpoch,
			WaitRevision: seal.WaitRevision, SealDigest: seal.Digest, TargetFenceVersion: seal.Target.Version,
			TargetCaptureDigest: seal.Target.CaptureDigest, PolicyVersion: seal.Intent.PolicyVersion, AuthorityVersion: 3},
		Source: MatchingApplySnapshotSourceV2{ID: seal.SourceID, ProviderID: seal.ProviderID, Endpoint: seal.Endpoint,
			ConfigurationIdentity: seal.SourceConfiguration, RunConfiguration: seal.RunProviderConfiguration},
		Captured: cloneCapturedMembers(seal.CapturedMetadata), TargetCapture: cloneRawJSON(seal.Target.Capture),
		Evaluation: cloneRawJSON(seal.FullEvaluationContext),
		Selected: MatchingApplySnapshotSelectedRelease{ProviderReleaseID: providerSnapshot.Releases[0].ID,
			ProviderKey: providerSnapshot.Releases[0].ProviderKey, OriginalOrdinal: ordinal,
			Lookup: MatchingApplySnapshotLookupV2{RequestID: selectedReceipt.RequestID, ReleaseID: selectedAuthority.ReleaseID,
				CacheKey: selectedReceipt.CacheKey, PayloadSHA256: selectedReceipt.PayloadSHA256, ProjectionSHA256: selectedReceipt.ProjectionSHA256},
			Projection: materialized},
		Assignments: assignments,
	}
	if err := ValidateMatchingApplyInputSnapshotV2(result, seal, context, evaluationRevision, decision); err != nil {
		return MatchingApplyInputSnapshotV2{}, "", err
	}
	encoded, err := canonicalMatchingJSON(mustJSON(result))
	if err != nil {
		return MatchingApplyInputSnapshotV2{}, "", err
	}
	digest := sha256.Sum256(encoded)
	return result, hex.EncodeToString(digest[:]), nil
}

// ValidateMatchingApplyInputSnapshotV2 binds parsed snapshot content back to
// original authority; its content digest is not itself an authorization token.
func ValidateMatchingApplyInputSnapshotV2(snapshot MatchingApplyInputSnapshotV2, seal MatchingFallbackSealAuthority, context MatchingFallbackCalculationContext, evaluationRevision string, decision persistedMatchingDecision) error {
	if decision.SelectedRelease == nil {
		return fmt.Errorf("validate matching apply snapshot: selected decision release is missing")
	}
	if snapshot.SchemaVersion != 2 || seal.Disposition != "qualified" || snapshot.Authority.ParentRunID != seal.RunID || snapshot.Authority.GroupID != seal.Target.GroupID || snapshot.Authority.SealDisposition != seal.Disposition ||
		snapshot.Authority.WaitRevision != seal.WaitRevision || snapshot.Authority.SealDigest != seal.Digest ||
		snapshot.Authority.TargetFenceVersion != 2 || snapshot.Authority.TargetCaptureDigest != seal.Target.CaptureDigest ||
		snapshot.Authority.PolicyVersion != seal.Intent.PolicyVersion || snapshot.Authority.AuthorityVersion != 3 ||
		snapshot.Source.ID != seal.SourceID || snapshot.Source.ProviderID != seal.ProviderID || snapshot.Source.Endpoint != seal.Endpoint ||
		snapshot.Source.ConfigurationIdentity != seal.SourceConfiguration || snapshot.Source.RunConfiguration != seal.RunProviderConfiguration ||
		!matchingJSONEqual(snapshot.TargetCapture, seal.Target.Capture) || !matchingJSONEqual(snapshot.Evaluation, seal.FullEvaluationContext) ||
		!sameFrozenCapturedMetadata(snapshot.Captured, seal.CapturedMetadata) || snapshot.Selected.ProviderReleaseID != decision.SelectedRelease.ProviderReleaseID ||
		!strings.EqualFold(snapshot.Selected.ProviderKey, decision.SelectedRelease.ProviderReleaseKey) || snapshot.Authority.EvaluationRevision != evaluationRevision ||
		snapshot.Authority.ExecutionEpoch != context.SearchReceipt.ExecutionEpoch || snapshot.Selected.Lookup.PayloadSHA256 == "" ||
		snapshot.Selected.Lookup.ProjectionSHA256 == "" {
		return fmt.Errorf("validate matching apply snapshot: snapshot differs from original authority")
	}
	if !matchingApplySnapshotCaptureCovers(seal.Target.Capture, seal.CapturedMetadata) {
		return fmt.Errorf("validate matching apply snapshot: original target capture does not cover captured members")
	}
	if decision.GroupID != seal.Target.GroupID || decision.PolicyVersion != seal.Intent.PolicyVersion ||
		len(decision.Assignments) != len(seal.CapturedMetadata) || len(decision.Assignments) != len(snapshot.Assignments) {
		return fmt.Errorf("validate matching apply snapshot: assignment coverage is incomplete")
	}
	wanted := make(map[uuid.UUID]struct {
		track            uuid.UUID
		medium, position int
	}, len(decision.Assignments))
	for _, item := range decision.Assignments {
		wanted[item.FileMemberID] = struct {
			track            uuid.UUID
			medium, position int
		}{item.ProviderTrackID, item.Medium - 1, item.Position - 1}
	}
	seenMembers := make(map[uuid.UUID]bool, len(wanted))
	for _, assignment := range snapshot.Assignments {
		expected, exists := wanted[assignment.FileMemberID]
		captured := capturedMemberByID(seal.CapturedMetadata, assignment.FileMemberID)
		if !exists || expected.track != assignment.ProviderTrackID || expected.medium != assignment.Medium || expected.position != assignment.Position ||
			assignment.FileMemberID == uuid.Nil || assignment.WorkID == uuid.Nil || assignment.ProviderTrackID == uuid.Nil || assignment.RecordingKey == "" ||
			captured == nil || captured.WorkID != assignment.WorkID || assignment.SourceSHA256 != memberSourceHash(seal.Target.Capture, assignment.FileMemberID) ||
			assignment.Medium < 0 || assignment.Position < 0 || seenMembers[assignment.FileMemberID] {
			return fmt.Errorf("validate matching apply snapshot: assignment is invalid or duplicated")
		}
		seenMembers[assignment.FileMemberID] = true
	}
	var selectedReceipt *MatchingFallbackLookupReceipt
	for _, receipt := range context.LookupReceipts {
		if receipt.RequestID == snapshot.Selected.Lookup.RequestID {
			selectedReceipt = &receipt
			break
		}
	}
	if selectedReceipt == nil || selectedReceipt.RunID != seal.RunID || selectedReceipt.CacheKey != snapshot.Selected.Lookup.CacheKey ||
		selectedReceipt.PayloadSHA256 != snapshot.Selected.Lookup.PayloadSHA256 || selectedReceipt.ProjectionSHA256 != snapshot.Selected.Lookup.ProjectionSHA256 ||
		snapshot.Selected.Lookup.ReleaseID != snapshot.Selected.ProviderKey || snapshot.Selected.Lookup.CacheKey != "release-lookup:"+snapshot.Selected.Lookup.ReleaseID {
		return fmt.Errorf("validate matching apply snapshot: selected lookup differs from original receipt")
	}
	ordinal, err := matchingApplySnapshotSelectedOrdinal(context.Lookups, snapshot.Selected.ProviderKey)
	if err != nil || ordinal != snapshot.Selected.OriginalOrdinal {
		return fmt.Errorf("validate matching apply snapshot: selected release ordinal differs from original lookup layout")
	}
	var providerSnapshot ProviderMatchingSnapshot
	if json.Unmarshal(selectedReceipt.AcceptedProjection, &providerSnapshot) != nil || len(providerSnapshot.Releases) != 1 ||
		providerSnapshot.Releases[0].ID != snapshot.Selected.ProviderReleaseID || !strings.EqualFold(providerSnapshot.Releases[0].ProviderKey, snapshot.Selected.ProviderKey) {
		return fmt.Errorf("validate matching apply snapshot: selected projection differs from original receipt")
	}
	expectedAssignments, err := snapshotAssignments(seal, providerSnapshot.Releases[0], decision)
	if err != nil || !matchingJSONEqual(mustJSON(expectedAssignments), mustJSON(snapshot.Assignments)) {
		return fmt.Errorf("validate matching apply snapshot: assignments differ from original decision")
	}
	expectedProjection, err := acceptedProjectionToMaterialization(providerSnapshot, snapshot.Selected.ProviderKey, frozenAssignments(expectedAssignments, providerSnapshot.Releases[0]))
	if err != nil || !matchingJSONEqual(mustJSON(expectedProjection), mustJSON(snapshot.Selected.Projection)) {
		return fmt.Errorf("validate matching apply snapshot: materialized projection differs from accepted receipt")
	}
	return nil
}

// matchingApplySnapshotSelectedOrdinal derives the immutable position of the
// selected release from the original registered lookup layout. The snapshot
// builder and its validator share this exact derivation so a recomputed
// snapshot can never relocate the saved provenance.
func matchingApplySnapshotSelectedOrdinal(lookups []MatchingFallbackCalculationLookupAuthority, releaseKey string) (int, error) {
	ordinal := -1
	for i, lookup := range lookups {
		if !strings.EqualFold(lookup.ReleaseID, releaseKey) {
			continue
		}
		if ordinal >= 0 {
			return -1, fmt.Errorf("selected release ordinal is ambiguous")
		}
		ordinal = i
	}
	if ordinal < 0 {
		return -1, fmt.Errorf("selected release is outside original layout")
	}
	return ordinal, nil
}

// DecodeMatchingApplyInputSnapshotV2 rejects unknown fields, trailing JSON and
// legacy schema payloads rather than silently interpreting newer authority.
func DecodeMatchingApplyInputSnapshotV2(raw []byte) (MatchingApplyInputSnapshotV2, error) {
	var value MatchingApplyInputSnapshotV2
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return value, fmt.Errorf("decode matching apply snapshot: trailing data")
	}
	if value.SchemaVersion != 2 {
		return value, fmt.Errorf("decode matching apply snapshot: unsupported schema")
	}
	return value, nil
}

func MatchingApplyInputSnapshotV2Digest(snapshot MatchingApplyInputSnapshotV2) (string, error) {
	encoded, err := canonicalMatchingJSON(mustJSON(snapshot))
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func snapshotAssignments(seal MatchingFallbackSealAuthority, release MatchingRelease, decision persistedMatchingDecision) ([]MatchingApplySnapshotAssignment, error) {
	if decision.SelectedRelease == nil || decision.GroupID != seal.Target.GroupID || decision.PolicyVersion != seal.Intent.PolicyVersion ||
		len(decision.Assignments) != len(seal.CapturedMetadata) || len(seal.CapturedMetadata) == 0 {
		return nil, fmt.Errorf("build matching apply snapshot: decision does not cover original group")
	}
	captured := make(map[uuid.UUID]MatchingAcquisitionCapturedMember, len(seal.CapturedMetadata))
	for _, member := range seal.CapturedMetadata {
		if member.MemberID == uuid.Nil || member.WorkID == uuid.Nil {
			return nil, fmt.Errorf("build matching apply snapshot: captured member identity is invalid")
		}
		if _, ok := captured[member.MemberID]; ok {
			return nil, fmt.Errorf("build matching apply snapshot: captured member is duplicated")
		}
		captured[member.MemberID] = member
	}
	assignments := make([]MatchingApplySnapshotAssignment, 0, len(decision.Assignments))
	seen := make(map[uuid.UUID]bool)
	for _, item := range decision.Assignments {
		member, ok := captured[item.FileMemberID]
		if !ok || seen[item.FileMemberID] || item.ProviderTrackID == uuid.Nil || item.Medium < 1 || item.Position < 1 {
			return nil, fmt.Errorf("build matching apply snapshot: foreign or invalid decision assignment")
		}
		sourceSHA := memberSourceHash(seal.Target.Capture, item.FileMemberID)
		if !validSnapshotDigest(sourceSHA) {
			return nil, fmt.Errorf("build matching apply snapshot: original member source SHA-256 is unavailable")
		}
		seen[item.FileMemberID] = true
		track := MatchingReleaseTrack{}
		if release.ID != uuid.Nil {
			for _, candidate := range release.Tracks {
				if candidate.ID == item.ProviderTrackID && candidate.Medium == item.Medium-1 && candidate.Position == item.Position-1 {
					track = candidate
					break
				}
			}
			if track.ID == uuid.Nil || track.RecordingProviderKey == "" {
				return nil, fmt.Errorf("build matching apply snapshot: assignment does not match selected release layout")
			}
		}
		assignments = append(assignments, MatchingApplySnapshotAssignment{FileMemberID: item.FileMemberID, WorkID: member.WorkID,
			SourceSHA256: sourceSHA, ProviderTrackID: item.ProviderTrackID,
			RecordingKey: track.RecordingProviderKey, Medium: item.Medium - 1, Position: item.Position - 1})
	}
	if len(seen) != len(captured) {
		return nil, fmt.Errorf("build matching apply snapshot: decision omits original group member")
	}
	sort.Slice(assignments, func(i, j int) bool {
		return assignments[i].FileMemberID.String() < assignments[j].FileMemberID.String()
	})
	return assignments, nil
}

func frozenAssignments(in []MatchingApplySnapshotAssignment, selected MatchingRelease) []FrozenTrackAssignment {
	out := make([]FrozenTrackAssignment, 0, len(in))
	for _, item := range in {
		out = append(out, FrozenTrackAssignment{ProviderTrackID: item.ProviderTrackID, ProviderReleaseID: selected.ID, ReleaseKey: selected.ProviderKey, RecordingKey: item.RecordingKey, Medium: item.Medium, Position: item.Position})
	}
	return out
}

func lookupReceiptByID(receipts []MatchingFallbackLookupReceipt, id uuid.UUID) *MatchingFallbackLookupReceipt {
	for i := range receipts {
		if receipts[i].RequestID == id {
			return &receipts[i]
		}
	}
	return nil
}
func receiptReleaseID(key string) string { return strings.TrimPrefix(key, "release-lookup:") }
func validSnapshotDigest(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == sha256.Size
}

func matchingSnapshotJSONDigest(raw json.RawMessage) (string, error) {
	canonical, err := canonicalMatchingJSON(raw)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}
func mustJSON(value any) []byte { data, _ := json.Marshal(value); return data }
func cloneCapturedMembers(in []MatchingAcquisitionCapturedMember) []MatchingAcquisitionCapturedMember {
	out := append([]MatchingAcquisitionCapturedMember(nil), in...)
	for i := range out {
		out[i].Tags = cloneRawJSON(out[i].Tags)
	}
	return out
}

func capturedMemberByID(members []MatchingAcquisitionCapturedMember, id uuid.UUID) *MatchingAcquisitionCapturedMember {
	for i := range members {
		if members[i].MemberID == id {
			return &members[i]
		}
	}
	return nil
}

func matchingApplySnapshotCaptureCovers(capture json.RawMessage, members []MatchingAcquisitionCapturedMember) bool {
	var target struct {
		Members []struct {
			VariantID uuid.UUID `json:"variant_id"`
		} `json:"members"`
	}
	if json.Unmarshal(capture, &target) != nil || len(target.Members) != len(members) {
		return false
	}
	ids := make(map[uuid.UUID]bool, len(target.Members))
	for _, member := range target.Members {
		if member.VariantID == uuid.Nil || ids[member.VariantID] {
			return false
		}
		ids[member.VariantID] = true
	}
	for _, member := range members {
		if !ids[member.MemberID] {
			return false
		}
		delete(ids, member.MemberID)
	}
	return len(ids) == 0
}
func memberSourceHash(capture json.RawMessage, memberID uuid.UUID) string {
	var target struct {
		Members []struct {
			VariantID    uuid.UUID         `json:"variant_id"`
			SourceSHA256 string            `json:"source_sha256"`
			Captures     []json.RawMessage `json:"captures"`
		} `json:"members"`
	}
	_ = json.Unmarshal(capture, &target)
	for _, member := range target.Members {
		if member.VariantID == memberID {
			for _, rawCapture := range member.Captures {
				var sourceCapture map[string]json.RawMessage
				if json.Unmarshal(rawCapture, &sourceCapture) != nil {
					continue
				}
				variantRaw := matchingSnapshotRawField(sourceCapture, "variantid")
				var variantID uuid.UUID
				if json.Unmarshal(variantRaw, &variantID) != nil || variantID != memberID {
					continue
				}
				digestRaw := matchingSnapshotRawField(sourceCapture, "sourcesha256")
				var digestBytes []byte
				if json.Unmarshal(digestRaw, &digestBytes) == nil && len(digestBytes) == sha256.Size {
					return hex.EncodeToString(digestBytes)
				}
				var digestString string
				if json.Unmarshal(digestRaw, &digestString) == nil && validSnapshotDigest(digestString) {
					return strings.ToLower(digestString)
				}
			}
			if validSnapshotDigest(member.SourceSHA256) {
				return strings.ToLower(member.SourceSHA256)
			}
		}
	}
	return ""
}

func matchingSnapshotRawField(fields map[string]json.RawMessage, normalizedName string) json.RawMessage {
	for key, value := range fields {
		if strings.EqualFold(strings.ReplaceAll(key, "_", ""), normalizedName) {
			return value
		}
	}
	return nil
}
