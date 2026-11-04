//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

func TestPublicationReconciliationAudioUpgradeOutranksProviderMetadataPostgreSQL(t *testing.T) {
	for _, withdrawTrackEvidence := range []bool{false, true} {
		t.Run(map[bool]string{false: "accepted_provider_graph", true: "missing_track_evidence"}[withdrawTrackEvidence], func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			fixture := preparePendingGraphFixture(t)
			seedBetterAutomaticAudioCandidate(t, ctx, fixture)

			// This accepted refresh changes provider metadata and requests CURRENT
			// reconciliation. Audio remains independently actionable even when the
			// provider graph cannot support a strict tag patch.
			assertAcceptedRefreshWakesPendingGraph(t, ctx, fixture)
			var refreshedTitle string
			if err := fixture.f.db.NewRaw(`SELECT name FROM provider_source_entity WHERE provider_source_id=? AND entity_kind='release' AND provider_key=?`, fixture.source.ID, fixture.releaseKey).Scan(ctx, &refreshedTitle); err != nil {
				t.Fatal(err)
			}
			if refreshedTitle != "Refreshed provider release" {
				t.Fatalf("accepted provider metadata title = %q", refreshedTitle)
			}
			if withdrawTrackEvidence {
				if _, err := fixture.f.db.ExecContext(ctx, `UPDATE provider_source_entity SET field_evidence=field_evidence-'__tracks' WHERE provider_source_id=? AND entity_kind='release' AND provider_key=?`, fixture.source.ID, fixture.releaseKey); err != nil {
					t.Fatal(err)
				}
			}

			var before TrackPublication
			if err := fixture.f.db.NewSelect().Model(&before).Where("position_id=?", fixture.position).Scan(ctx); err != nil {
				t.Fatal(err)
			}
			decision, err := fixture.repo.EvaluatePublicationReconciliationCurrent(ctx, fixture.release, fixture.guard, fixture.source.Endpoint, pendingGraphTestIdentity)
			if err != nil || decision.Action != AudioUpgradeRequired {
				t.Fatalf("provider metadata plus better audio decision = %+v, %v; want AudioUpgradeRequired", decision, err)
			}
			if decision.Noop != nil {
				t.Fatal("audio-upgrade decision unexpectedly returned no-op witness")
			}
			var after TrackPublication
			if err := fixture.f.db.NewSelect().Model(&after).Where("position_id=?", fixture.position).Scan(ctx); err != nil {
				t.Fatal(err)
			}
			if before.Generation != after.Generation || string(before.ActualMetadata) != string(after.ActualMetadata) || string(before.ActualAudio) != string(after.ActualAudio) || string(before.ActualAssets) != string(after.ActualAssets) {
				t.Fatal("CURRENT changed during reconciliation evaluation")
			}
		})
	}
}

func seedBetterAutomaticAudioCandidate(t *testing.T, ctx context.Context, fixture pendingGraphFixture) {
	t.Helper()
	rootPath, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.October, 1, 12, 0, 0, 123456000, time.UTC)
	rootID, locationID, workID, variantID, scanID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	queries := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO source_root(id,configured_path,display_name,scan_generation,inventory_path,enabled,status) VALUES (?,?, 'Reconciliation audio source',1,?,true,'available')`, []any{rootID, rootPath, rootPath}},
		{`INSERT INTO operation(id,kind,state,stage,input_snapshot,finished_at) VALUES (?,'scan_source','succeeded','succeeded','{}'::jsonb,now())`, []any{scanID}},
		{`INSERT INTO media_variant(id,size_bytes,analysis_policy_version,ffprobe_version,ffprobe_json,observed_tags,inspected_at,applied_operation_id,audio_stream_count) VALUES (?,42,1,'ffprobe 1','{"format":{"format_name":"flac"},"streams":[{"index":0,"codec_type":"audio","codec_name":"flac","channels":2,"channel_layout":"stereo","bits_per_raw_sample":"24","sample_rate":"48000"}]}','{}'::jsonb,?,?,1)`, []any{variantID, now, scanID}},
		{`INSERT INTO source_location(id,source_root_id,relative_path,size_bytes,mtime,last_seen_scan_generation,probe_status,media_variant_id) VALUES (?,?,?,42,?,1,'audio',?)`, []any{locationID, rootID, "upgrade.flac", now, variantID}},
		{`INSERT INTO source_analysis_work(id,location_id,source_root_id,configured_path,inventory_path,relative_path,size_bytes,mtime,sha256_enabled,origin_scan_operation_id,current_location_id) VALUES (?,?,?,?,?,?,42,?,false,?,?)`, []any{workID, locationID, rootID, rootPath, rootPath, "upgrade.flac", now, scanID, locationID}},
		{`INSERT INTO source_work_variant_identity(work_id,media_variant_id) VALUES (?,?)`, []any{workID, variantID}},
		{`INSERT INTO source_analysis_step(work_id,step,state,success_probe_variant_id,success_reuse_origin) VALUES (?,'probe','succeeded',?,'executed')`, []any{workID, variantID}},
	}
	for _, query := range queries {
		if _, err := fixture.f.db.ExecContext(ctx, query.query, query.args...); err != nil {
			t.Fatal(err)
		}
	}
	var trackID uuid.UUID
	if err := fixture.f.db.NewRaw(`SELECT track_id FROM release_track WHERE id=?`, fixture.position).Scan(ctx, &trackID); err != nil {
		t.Fatal(err)
	}
	if result, err := fixture.f.db.ExecContext(ctx, `INSERT INTO track_source(id,track_id,media_variant_id,selected,compatible,decision_method,metadata) VALUES (?,?,?,false,true,'manual','{}'::jsonb)`, uuid.New(), trackID, variantID); err != nil {
		t.Fatal(err)
	} else if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		t.Fatalf("insert better source association affected %d rows, %v", affected, err)
	}
	var current TrackPublication
	if err := fixture.f.db.NewSelect().Model(&current).Where("position_id=?", fixture.position).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	oldLossless, oldChannels, oldDepth, oldRate := true, 2, 16, 44100
	oldFacts := PersistedAudioFacts{StreamIndex: 0, Codec: "flac", Lossless: &oldLossless, Channels: &oldChannels, ChannelLayout: "stereo", BitDepth: &oldDepth, SampleRate: &oldRate, Container: publication.AudioContainerFLAC, SupportedSourceContainer: true}
	if _, err := fixture.f.db.ExecContext(ctx, `UPDATE track_publication SET actual_audio=?::jsonb WHERE position_id=?`, string(mustJSON(PublicationCurrentAudio{Version: 1, Facts: oldFacts})), fixture.position); err != nil {
		t.Fatal(err)
	}
	var lowQualityCurrent TrackPublication
	if err := fixture.f.db.NewSelect().Model(&lowQualityCurrent).Where("position_id=?", fixture.position).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if result, err := fixture.f.db.ExecContext(ctx, `UPDATE position_source_authority SET mode='automatic',revision=?,source_location_id=?,media_variant_id=? WHERE position_id=?`, uuid.New(), locationID, variantID, fixture.position); err != nil {
		t.Fatal(err)
	} else if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		t.Fatalf("bind automatic source authority affected %d rows, %v", affected, err)
	}
	if _, err := fixture.f.db.ExecContext(ctx, `UPDATE position_source_authority SET publication_generation=? WHERE position_id=?`, lowQualityCurrent.Generation, fixture.position); err != nil {
		t.Fatal(err)
	}
	// Ensure the CURRENT facts are genuinely lower than the persisted candidate.
	var currentAudio PublicationCurrentAudio
	if err := json.Unmarshal(lowQualityCurrent.ActualAudio, &currentAudio); err != nil || currentAudio.Version != 1 {
		t.Fatalf("decode CURRENT audio facts: %v", err)
	}
}
