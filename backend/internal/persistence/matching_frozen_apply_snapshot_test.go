package persistence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/matchingpolicy"
)

func TestBuildMatchingApplyInputSnapshotV2UsesSelectedAcceptedProjection(t *testing.T) {
	seal, context, decision, releaseA, releaseB := matchingApplySnapshotFixture()
	for _, tc := range []struct {
		key     string
		release MatchingRelease
		ordinal int
	}{{"release-a", releaseA, 0}, {"release-b", releaseB, 1}} {
		t.Run(tc.key, func(t *testing.T) {
			decision.Assignments[0].ProviderTrackID = tc.release.Tracks[0].ID
			decision.Assignments[1].ProviderTrackID = tc.release.Tracks[0].ID
			projection := ProviderMatchingSnapshot{Source: ProviderSource{ID: seal.SourceID, ProviderID: seal.ProviderID, Endpoint: seal.Endpoint, ConfigurationIdentity: seal.RunProviderConfiguration}, Releases: []MatchingRelease{tc.release}}
			context.LookupReceipts[tc.ordinal].AcceptedProjection = testJSON(projection)
			context.LookupReceipts[tc.ordinal].ProjectionSHA256 = testDigest(context.LookupReceipts[tc.ordinal].AcceptedProjection)
			context.LookupReceipts[tc.ordinal].Payload = json.RawMessage(`{"accepted":true}`)
			context.LookupReceipts[tc.ordinal].PayloadSHA256 = testDigest(context.LookupReceipts[tc.ordinal].Payload)
			context.Lookups[tc.ordinal].ReleaseID = tc.key
			context.Lookups[tc.ordinal].CacheKey = "release-lookup:" + tc.key
			context.LookupReceipts[tc.ordinal].CacheKey = "release-lookup:" + tc.key
			seal.FullEvaluationContext = testJSON(context)
			decision.SelectedRelease.ProviderReleaseID = tc.release.ID
			decision.SelectedRelease.ProviderReleaseKey = tc.key
			snapshot, digest, err := BuildMatchingApplyInputSnapshotV2(seal, context, "eval-rev", decision)
			if err != nil {
				t.Fatal(err)
			}
			if digest == "" || snapshot.Selected.ProviderKey != tc.key || snapshot.Selected.OriginalOrdinal != tc.ordinal {
				t.Fatalf("wrong selected release snapshot: %+v", snapshot.Selected)
			}
			if string(snapshot.Selected.Projection.Release.Fields) != string(tc.release.Fields) {
				t.Fatalf("selected release fields came from another lookup: %s", snapshot.Selected.Projection.Release.Fields)
			}
			if snapshot.Assignments[0].SourceSHA256 != strings.Repeat("12", 32) || snapshot.Assignments[0].Medium != 0 || snapshot.Assignments[0].Position != 0 {
				t.Fatalf("assignment did not preserve original zero-based source data: %+v", snapshot.Assignments[0])
			}
			again, repeatedDigest, err := BuildMatchingApplyInputSnapshotV2(seal, context, "eval-rev", decision)
			if err != nil || digest != repeatedDigest || !matchingJSONEqual(testJSON(snapshot), testJSON(again)) {
				t.Fatalf("snapshot digest is unstable: %v", err)
			}
			mutated := snapshot
			mutated.Selected.Projection.Release.Fields = json.RawMessage(`{"title":"tampered"}`)
			if err := ValidateMatchingApplyInputSnapshotV2(mutated, seal, context, "eval-rev", decision); err == nil {
				t.Fatal("tampered provider fields were accepted")
			}
			encoded := testJSON(snapshot)
			if _, err := DecodeMatchingApplyInputSnapshotV2(encoded); err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeMatchingApplyInputSnapshotV2([]byte(strings.TrimSuffix(string(encoded), "}") + `,"unknown":true}`)); err == nil {
				t.Fatal("unknown schema field was accepted")
			}
		})
	}
}

func TestMatchingApplyInputSnapshotV2RejectsNilSelectedRelease(t *testing.T) {
	seal, context, decision, _, _ := matchingApplySnapshotFixture()
	snapshot, _, err := BuildMatchingApplyInputSnapshotV2(seal, context, "eval-rev", decision)
	if err != nil {
		t.Fatal(err)
	}
	withoutSelected := decision
	withoutSelected.SelectedRelease = nil
	if err := ValidateMatchingApplyInputSnapshotV2(snapshot, seal, context, "eval-rev", withoutSelected); err == nil {
		t.Fatal("nil selected decision release was accepted")
	}
}

func TestMatchingApplyInputSnapshotV2RejectsTamperedAuthorityBindings(t *testing.T) {
	seal, context, decision, _, _ := matchingApplySnapshotFixture()
	snapshot, _, err := BuildMatchingApplyInputSnapshotV2(seal, context, "eval-rev", decision)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateMatchingApplyInputSnapshotV2(snapshot, seal, context, "eval-rev", decision); err != nil {
		t.Fatalf("original valid snapshot was rejected: %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*MatchingApplyInputSnapshotV2)
	}{
		{"execution_epoch", func(mutated *MatchingApplyInputSnapshotV2) { mutated.Authority.ExecutionEpoch++ }},
		{"original_ordinal", func(mutated *MatchingApplyInputSnapshotV2) { mutated.Selected.OriginalOrdinal++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tampered := snapshot
			tc.mutate(&tampered)
			digest, err := MatchingApplyInputSnapshotV2Digest(tampered)
			if err != nil || digest == "" {
				t.Fatalf("recomputed snapshot digest failed: %v", err)
			}
			if err := ValidateMatchingApplyInputSnapshotV2(tampered, seal, context, "eval-rev", decision); err == nil {
				t.Fatal("tampered authority binding was accepted")
			}
		})
	}
}

func TestMatchingApplyInputSnapshotV2RejectsInvalidDecisionAssignments(t *testing.T) {
	seal, context, decision, release, _ := matchingApplySnapshotFixture()
	projection := ProviderMatchingSnapshot{Source: ProviderSource{ID: seal.SourceID, ProviderID: seal.ProviderID, Endpoint: seal.Endpoint, ConfigurationIdentity: seal.RunProviderConfiguration}, Releases: []MatchingRelease{release}}
	context.LookupReceipts[0].AcceptedProjection = testJSON(projection)
	context.LookupReceipts[0].ProjectionSHA256 = testDigest(context.LookupReceipts[0].AcceptedProjection)
	context.LookupReceipts[0].PayloadSHA256 = testDigest([]byte(`{}`))
	context.Lookups[0].ReleaseID = release.ProviderKey
	context.Lookups[0].CacheKey = "release-lookup:" + release.ProviderKey
	context.LookupReceipts[0].CacheKey = context.Lookups[0].CacheKey
	seal.FullEvaluationContext = testJSON(context)
	decision.SelectedRelease.ProviderReleaseID = release.ID
	decision.SelectedRelease.ProviderReleaseKey = release.ProviderKey
	foreign := decision
	foreign.Assignments = append([]struct {
		FileMemberID    uuid.UUID `json:"file_member_id"`
		ProviderTrackID uuid.UUID `json:"provider_track_id"`
		Medium          int       `json:"medium"`
		Position        int       `json:"position"`
	}(nil), decision.Assignments...)
	foreign.Assignments[0].FileMemberID = uuid.New()
	if _, _, err := BuildMatchingApplyInputSnapshotV2(seal, context, "eval-rev", foreign); err == nil {
		t.Fatal("foreign member assignment was accepted")
	}
	partial := decision
	partial.Assignments = partial.Assignments[:1]
	if _, _, err := BuildMatchingApplyInputSnapshotV2(seal, context, "eval-rev", partial); err == nil {
		t.Fatal("partial group was accepted")
	}
	wrongLayout := decision
	wrongLayout.Assignments = append([]struct {
		FileMemberID    uuid.UUID `json:"file_member_id"`
		ProviderTrackID uuid.UUID `json:"provider_track_id"`
		Medium          int       `json:"medium"`
		Position        int       `json:"position"`
	}(nil), decision.Assignments...)
	wrongLayout.Assignments[0].Position = 2
	if _, _, err := BuildMatchingApplyInputSnapshotV2(seal, context, "eval-rev", wrongLayout); err == nil {
		t.Fatal("out-of-layout coordinate was accepted")
	}
	wrongTrack := decision
	wrongTrack.Assignments = append([]struct {
		FileMemberID    uuid.UUID `json:"file_member_id"`
		ProviderTrackID uuid.UUID `json:"provider_track_id"`
		Medium          int       `json:"medium"`
		Position        int       `json:"position"`
	}(nil), decision.Assignments...)
	wrongTrack.Assignments[0].ProviderTrackID = uuid.New()
	if _, _, err := BuildMatchingApplyInputSnapshotV2(seal, context, "eval-rev", wrongTrack); err == nil {
		t.Fatal("foreign provider track assignment was accepted")
	}
}

func matchingApplySnapshotFixture() (MatchingFallbackSealAuthority, MatchingFallbackCalculationContext, persistedMatchingDecision, MatchingRelease, MatchingRelease) {
	runID, groupID, sourceID, providerID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	memberA, memberB, workA, workB, trackA, trackB := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	releaseA := MatchingRelease{ID: uuid.New(), ProviderKey: "release-a", Name: "A", Fields: json.RawMessage(`{"title":"A"}`), FieldEvidence: map[string]ProviderFieldEvidence{}, Tracks: []MatchingReleaseTrack{{ID: trackA, Medium: 0, Position: 0, RecordingProviderKey: "recording-a", RecordingTitle: "Track A"}}}
	releaseB := MatchingRelease{ID: uuid.New(), ProviderKey: "release-b", Name: "B", Fields: json.RawMessage(`{"title":"B"}`), FieldEvidence: map[string]ProviderFieldEvidence{}, Tracks: []MatchingReleaseTrack{{ID: trackB, Medium: 0, Position: 0, RecordingProviderKey: "recording-b", RecordingTitle: "Track B"}}}
	seal := MatchingFallbackSealAuthority{SchemaVersion: matchingFallbackSealAuthoritySchemaVersion, Disposition: "qualified", RunID: runID, WaitRevision: 5,
		Intent:           MatchingAcquisitionIntent{SchemaVersion: matchingAutomaticFallbackAcquisitionIntentVersion, PolicyVersion: matchingpolicy.CurrentVersion},
		Target:           MatchingRunFenceSnapshot{Version: 2, GroupID: groupID, GroupRevision: "group-rev", CaptureDigest: strings.Repeat("a", 64), Capture: json.RawMessage(`{"schema_version":2,"group_id":"` + groupID.String() + `","members":[{"variant_id":"` + memberA.String() + `","source_sha256":"` + strings.Repeat("12", 32) + `"},{"variant_id":"` + memberB.String() + `","source_sha256":"` + strings.Repeat("12", 32) + `"}]}`)},
		CapturedMetadata: []MatchingAcquisitionCapturedMember{{MemberID: memberA, WorkID: workA, Tags: json.RawMessage(`{}`)}, {MemberID: memberB, WorkID: workB, Tags: json.RawMessage(`{}`)}},
		SourceID:         sourceID, ProviderID: providerID, Endpoint: "https://provider.invalid", SourceConfiguration: "source-config", RunProviderConfiguration: "source-config", Digest: strings.Repeat("b", 64)}
	lookupA, lookupB := uuid.New(), uuid.New()
	ctx := MatchingFallbackCalculationContext{RunID: runID, Intent: seal.Intent, SearchReceipt: MatchingFallbackSearchReceipt{ExecutionEpoch: 9},
		RunSourceID: sourceID, RunProviderID: providerID, RunEndpoint: seal.Endpoint, RunConfiguration: seal.RunProviderConfiguration,
		Lookups:        []MatchingFallbackCalculationLookupAuthority{{RequestID: lookupA, ReleaseID: releaseA.ProviderKey, ProviderSourceID: sourceID, ProviderID: providerID, Endpoint: seal.Endpoint, Configuration: seal.RunProviderConfiguration, CacheKey: "release-lookup:release-a"}, {RequestID: lookupB, ReleaseID: releaseB.ProviderKey, ProviderSourceID: sourceID, ProviderID: providerID, Endpoint: seal.Endpoint, Configuration: seal.RunProviderConfiguration, CacheKey: "release-lookup:release-b"}},
		LookupReceipts: []MatchingFallbackLookupReceipt{{RunID: runID, RequestID: lookupA, ProviderSourceID: sourceID, ConfigurationIdentity: seal.RunProviderConfiguration, CacheKey: "release-lookup:release-a", PayloadSHA256: strings.Repeat("c", 64), ProjectionSHA256: strings.Repeat("d", 64)}, {RunID: runID, RequestID: lookupB, ProviderSourceID: sourceID, ConfigurationIdentity: seal.RunProviderConfiguration, CacheKey: "release-lookup:release-b", PayloadSHA256: strings.Repeat("e", 64), ProjectionSHA256: strings.Repeat("f", 64)}}}
	for i, release := range []MatchingRelease{releaseA, releaseB} {
		projection := ProviderMatchingSnapshot{Source: ProviderSource{ID: sourceID, ProviderID: providerID, Endpoint: seal.Endpoint, ConfigurationIdentity: seal.RunProviderConfiguration}, Releases: []MatchingRelease{release}}
		ctx.LookupReceipts[i].Payload = json.RawMessage(`{}`)
		ctx.LookupReceipts[i].PayloadSHA256 = testDigest(ctx.LookupReceipts[i].Payload)
		ctx.LookupReceipts[i].AcceptedProjection = testJSON(projection)
		ctx.LookupReceipts[i].ProjectionSHA256 = testDigest(ctx.LookupReceipts[i].AcceptedProjection)
	}
	seal.FullEvaluationContext = testJSON(ctx)
	decision := persistedMatchingDecision{Version: 1, GroupID: groupID, PolicyVersion: matchingpolicy.CurrentVersion, Outcome: "unique",
		SelectedRelease: &struct {
			ProviderReleaseID  uuid.UUID `json:"provider_release_id"`
			ProviderReleaseKey string    `json:"provider_release_key"`
		}{ProviderReleaseKey: releaseA.ProviderKey},
		Assignments: []struct {
			FileMemberID    uuid.UUID `json:"file_member_id"`
			ProviderTrackID uuid.UUID `json:"provider_track_id"`
			Medium          int       `json:"medium"`
			Position        int       `json:"position"`
		}{{FileMemberID: memberA, ProviderTrackID: trackA, Medium: 1, Position: 1}, {FileMemberID: memberB, ProviderTrackID: trackA, Medium: 1, Position: 1}}}
	decision.SelectedRelease.ProviderReleaseID = releaseA.ID
	return seal, ctx, decision, releaseA, releaseB
}

func testJSON(value any) []byte { encoded, _ := json.Marshal(value); return encoded }
func testDigest(raw []byte) string {
	canonical, err := canonicalMatchingJSON(raw)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}
