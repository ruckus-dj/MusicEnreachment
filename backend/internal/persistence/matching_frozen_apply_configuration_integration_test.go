//go:build integration

package persistence_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

func TestFrozenApplyConfigurationRotationRequiresReviewPostgreSQL(t *testing.T) {
	callbackExecuted := false
	testAutomaticFallbackFiniteFlowWithPublicationCallback(t, false, false, false, true, false, func(fixture uniquePublicationApplyFixture) {
		callbackExecuted = true
		ctx := context.Background()
		registry := settings.New(persistence.NewSettingsRepository(fixture.Database), nil)
		rotatedEndpoint := "https://musicbrainz-rotated.invalid/ws/2"
		if err := registry.SetMusicBrainzConfig(ctx, "self-hosted", rotatedEndpoint); err != nil {
			t.Fatalf("rotate MusicBrainz configuration: %v", err)
		}
		rotated, err := registry.GetMusicBrainzConfig(ctx)
		if err != nil || rotated.BaseURL != rotatedEndpoint || rotated.Identity == fixture.Source.ConfigurationIdentity {
			t.Fatalf("read rotated MusicBrainz configuration: config=%+v err=%v", rotated, err)
		}

		var sourceBefore struct {
			Endpoint string `bun:"endpoint"`
			Identity string `bun:"configuration_identity"`
		}
		if err := fixture.Database.NewRaw(`SELECT endpoint,configuration_identity FROM provider_source WHERE id=?`, fixture.Source.ID).
			Scan(ctx, &sourceBefore.Endpoint, &sourceBefore.Identity); err != nil {
			t.Fatalf("read frozen provider source row: %v", err)
		}
		if sourceBefore.Endpoint != fixture.Source.Endpoint || sourceBefore.Identity != fixture.Source.ConfigurationIdentity {
			t.Fatalf("rotation unexpectedly changed original provider source: row=%+v source=%+v", sourceBefore, fixture.Source)
		}

		var beforeSnapshot []byte
		if err := fixture.Database.NewRaw(`SELECT input_snapshot FROM operation WHERE id=?`, fixture.ChildOperation.ID).Scan(ctx, &beforeSnapshot); err != nil {
			t.Fatalf("read immutable apply snapshot before apply: %v", err)
		}
		var beforeLocalWrites int
		if err := fixture.Database.NewRaw(`SELECT
			(SELECT count(*) FROM local_artist) + (SELECT count(*) FROM local_release) +
			(SELECT count(*) FROM local_recording) + (SELECT count(*) FROM local_track) +
			(SELECT count(*) FROM provider_local_release) + (SELECT count(*) FROM provider_local_recording) +
			(SELECT count(*) FROM provider_local_track) + (SELECT count(*) FROM release_track) +
			(SELECT count(*) FROM track_source)`).Scan(ctx, &beforeLocalWrites); err != nil {
			t.Fatalf("count local entities and source links before apply: %v", err)
		}

		jobID := *fixture.ChildOperation.RiverJobID
		// The current River worker intentionally rejects schema-2 materialization;
		// exercise the real persistence gate with its registered, live job and claim.
		if _, err := fixture.Database.NewRaw(`UPDATE river_job SET state='running',attempt=1 WHERE id=?`, jobID).Exec(ctx); err != nil {
			t.Fatalf("make registered apply job live: %v", err)
		}
		epoch, claimed, err := fixture.Repository.ClaimProviderOperationDelivery(ctx, fixture.ChildOperation.ID, 1, jobID)
		if err != nil || !claimed || epoch < 1 {
			t.Fatalf("claim registered apply delivery: epoch=%d claimed=%v err=%v", epoch, claimed, err)
		}
		changed, err := fixture.Repository.ApplyMatchingOperation(ctx, fixture.ChildOperation.ID, jobID, epoch)
		if err != nil || !changed {
			t.Fatalf("apply frozen request after configuration rotation: changed=%v err=%v", changed, err)
		}

		operation, err := fixture.Repository.GetOperation(ctx, fixture.ChildOperation.ID)
		const safeError = "The matching provider configuration changed; review this group manually."
		if err != nil || operation.State != "succeeded" || operation.Stage != "review_required" || operation.SafeError == nil || *operation.SafeError != safeError {
			t.Fatalf("rotated configuration did not settle as safe review: operation=%+v err=%v", operation, err)
		}
		var receipt struct {
			Outcome  string `bun:"outcome"`
			InputSHA []byte `bun:"input_sha256"`
			Count    int    `bun:"count"`
			Bound    bool   `bun:"bound"`
		}
		if err := fixture.Database.NewRaw(`SELECT outcome,input_sha256,count(*) OVER (),input_sha256=(
			SELECT input_sha256 FROM matching_apply_request WHERE apply_operation_id=matching_apply_receipt.apply_operation_id)
			FROM matching_apply_receipt WHERE apply_operation_id=?`, fixture.ChildOperation.ID).
			Scan(ctx, &receipt.Outcome, &receipt.InputSHA, &receipt.Count, &receipt.Bound); err != nil || receipt.Count != 1 || receipt.Outcome != "review_required" || len(receipt.InputSHA) == 0 || !receipt.Bound {
			t.Fatalf("read one immutable review receipt: receipt=%+v err=%v", receipt, err)
		}
		var afterSnapshot []byte
		if err := fixture.Database.NewRaw(`SELECT input_snapshot FROM operation WHERE id=?`, fixture.ChildOperation.ID).Scan(ctx, &afterSnapshot); err != nil {
			t.Fatalf("reread frozen apply snapshot: %v", err)
		}
		if !bytes.Equal(beforeSnapshot, afterSnapshot) {
			t.Fatalf("configuration rotation changed immutable apply snapshot")
		}
		var afterLocalWrites int
		if err := fixture.Database.NewRaw(`SELECT
			(SELECT count(*) FROM local_artist) + (SELECT count(*) FROM local_release) +
			(SELECT count(*) FROM local_recording) + (SELECT count(*) FROM local_track) +
			(SELECT count(*) FROM provider_local_release) + (SELECT count(*) FROM provider_local_recording) +
			(SELECT count(*) FROM provider_local_track) + (SELECT count(*) FROM release_track) +
			(SELECT count(*) FROM track_source)`).Scan(ctx, &afterLocalWrites); err != nil || afterLocalWrites != beforeLocalWrites {
			t.Fatalf("stale apply wrote local entities or source links: before=%d after=%d err=%v", beforeLocalWrites, afterLocalWrites, err)
		}
		var sourceAfter struct {
			Endpoint string `bun:"endpoint"`
			Identity string `bun:"configuration_identity"`
		}
		if err := fixture.Database.NewRaw(`SELECT endpoint,configuration_identity FROM provider_source WHERE id=?`, fixture.Source.ID).
			Scan(ctx, &sourceAfter.Endpoint, &sourceAfter.Identity); err != nil || sourceAfter != sourceBefore {
			t.Fatalf("apply changed the original provider source row: before=%+v after=%+v err=%v", sourceBefore, sourceAfter, err)
		}
	}, true)
	if !callbackExecuted {
		t.Fatal("frozen apply configuration callback was not executed")
	}
}
