//go:build integration

package persistence_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

// This regression exercises the real v2 automatic-admission and delivery
// fences. The accepted-catalog seal is intentionally not fabricated here:
// the publisher's sealed-scope check is covered by the sealed-worker
// integration test, while this test targets the target-capture handoff.
func TestMatchingV2TargetFenceDeliverySurvivesUnrelatedEpochAndRejectsForgedDigestPostgreSQL(t *testing.T) {
	t.Run("unrelated grouping epoch does not invalidate admitted delivery", func(t *testing.T) {
		t.Parallel()
		fixture := newAutomaticMatchingTargetFenceFixture(t)
		before, exists, err := fixture.repository.GetMatchingAcquisition(t.Context(), fixture.runID)
		if err != nil || !exists || before.Intent.FenceVersion != 2 {
			t.Fatalf("read v2 admission: exists=%v err=%v", exists, err)
		}
		if _, err := fixture.db.NewRaw(`UPDATE incoming_grouping_state SET revision=revision+1,needs_refresh=false WHERE singleton=true`).Exec(fixture.ctx); err != nil {
			t.Fatalf("advance unrelated grouping epoch: %v", err)
		}
		if _, err := fixture.db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, fixture.jobID).Exec(fixture.ctx); err != nil {
			t.Fatalf("start matching delivery: %v", err)
		}
		_, outcome, err := fixture.repository.ClaimMatchingDelivery(fixture.ctx, fixture.runID, 1, fixture.jobID, 1)
		if err != nil || outcome != persistence.MatchingDeliveryClaimed {
			t.Fatalf("claim v2 delivery after unrelated epoch: outcome=%v err=%v", outcome, err)
		}
	})

	t.Run("forged target digest cannot publish", func(t *testing.T) {
		t.Parallel()
		fixture := newAutomaticMatchingTargetFenceFixture(t)
		if _, err := fixture.db.NewRaw(`UPDATE operation SET state='running',stage='evaluating',started_at=now() WHERE id=?`, fixture.runID).Exec(fixture.ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.db.NewRaw(`UPDATE matching_run SET state='running' WHERE id=?`, fixture.runID).Exec(fixture.ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.db.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, fixture.jobID).Exec(fixture.ctx); err != nil {
			t.Fatal(err)
		}
		claim, outcome, err := fixture.repository.ClaimMatchingDelivery(fixture.ctx, fixture.runID, 1, fixture.jobID, 1)
		if err != nil || outcome != persistence.MatchingDeliveryClaimed {
			t.Fatalf("claim current v2 delivery: outcome=%v err=%v", outcome, err)
		}
		var target struct {
			GroupID  uuid.UUID `bun:"group_id"`
			Revision string    `bun:"captured_group_revision"`
		}
		if err := fixture.db.NewRaw(`SELECT group_id,captured_group_revision FROM matching_run_target WHERE run_id=?`, fixture.runID).Scan(fixture.ctx, &target); err != nil {
			t.Fatal(err)
		}
		var members []persistence.IncomingMatchMemberFence
		if err := fixture.db.NewRaw(`SELECT member.variant_id AS file_member_id, location.work_id, variant.source_sha256
			FROM matching_run_target target
			JOIN incoming_group_member member ON member.group_id=target.group_id
			JOIN incoming_group_member_location location ON location.group_id=member.group_id AND location.variant_id=member.variant_id
			JOIN media_variant variant ON variant.id=member.variant_id
			WHERE target.run_id=? ORDER BY member.variant_id`, fixture.runID).Scan(fixture.ctx, &members); err != nil {
			t.Fatal(err)
		}
		var provider struct {
			ProviderID uuid.UUID `bun:"provider_id"`
			SourceID   uuid.UUID `bun:"provider_source_id"`
			Identity   string    `bun:"provider_configuration_identity"`
			Endpoint   string    `bun:"endpoint"`
			Namespace  string    `bun:"namespace"`
		}
		if err := fixture.db.NewRaw(`SELECT source.provider_id,run.provider_source_id,run.provider_configuration_identity,source.endpoint,source.namespace
			FROM matching_run run JOIN provider_source source ON source.id=run.provider_source_id WHERE run.id=?`, fixture.runID).Scan(fixture.ctx, &provider); err != nil {
			t.Fatal(err)
		}
		evaluation := persistence.IncomingMatchEvaluation{
			ScopeVersion: 4, TargetFenceVersion: 2, TargetCaptureDigest: strings.Repeat("0", 64),
			ExpectedGroupsRevision: "service-shaped", PolicyVersion: service.MatchingPolicyVersion,
			Provider: persistence.IncomingMatchProviderFence{
				ProviderID: provider.ProviderID, ProviderSourceID: provider.SourceID, Namespace: provider.Namespace,
				Endpoint: provider.Endpoint, ConfigurationIdentity: provider.Identity,
				CacheKey: "service-shaped", AcceptedProjectionRevision: "service-shaped",
			},
			Groups: []persistence.IncomingMatchGroupEvaluation{{GroupID: target.GroupID, GroupRevision: target.Revision, Members: members}},
		}
		// Keep the evaluation otherwise shaped like a service result; this test
		// deliberately forges only the target digest so the publisher's immutable
		// admitted-target comparison is what rejects publication.
		evaluation.Groups[0].Evidence = json.RawMessage(`{}`)
		changed, publishErr := fixture.repository.PublishMatchingEvaluationForDelivery(fixture.ctx, fixture.runID, 1, fixture.jobID, claim.ExecutionEpoch, evaluation, map[uuid.UUID]*string{target.GroupID: nil})
		if publishErr == nil || changed {
			t.Fatalf("forged target digest publication = changed:%v err:%v; want rejection", changed, publishErr)
		}
		header, candidates, err := persistence.NewIncomingMatchCandidateRepository(fixture.db).Get(fixture.ctx, target.GroupID)
		if err != nil || header != nil || len(candidates) != 0 {
			t.Fatalf("forged digest left published evaluation header=%+v candidates=%d err=%v", header, len(candidates), err)
		}
		var applyRequests, applyJobs int
		if err := fixture.db.NewRaw(`SELECT count(*) FROM matching_apply_request WHERE parent_run_id=?`, fixture.runID).Scan(fixture.ctx, &applyRequests); err != nil {
			t.Fatal(err)
		}
		if err := fixture.db.NewRaw(`SELECT count(*) FROM operation WHERE kind=? AND input_snapshot::text LIKE ?`, persistence.MatchingApplyOperationKind, "%"+fixture.runID.String()+"%").Scan(fixture.ctx, &applyJobs); err != nil {
			t.Fatal(err)
		}
		if applyRequests != 0 || applyJobs != 0 {
			t.Fatalf("forged digest admitted apply requests=%d jobs=%d", applyRequests, applyJobs)
		}
	})
}
