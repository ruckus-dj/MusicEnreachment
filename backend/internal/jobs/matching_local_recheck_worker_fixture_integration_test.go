//go:build integration

package jobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/uptrace/bun"
)

// bindRecheckPositionToMusicBrainz establishes an exact existing provider
// binding without changing the Local assignment. The provider track UUID is
// taken from the accepted source projection, not the transport MBID.
func bindRecheckPositionToMusicBrainz(t *testing.T, ctx context.Context, db *bun.DB, position uuid.UUID, releaseMBID string) {
	t.Helper()
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		q := `SELECT release.id, release.provider_id, track.track_id, track.recording_id
			FROM provider_release release
			JOIN provider_source_release_track track ON track.provider_id=release.provider_id AND track.release_id=release.id
			WHERE release.provider_key=? AND track.present`
		var release, provider, track, recording uuid.UUID
		if err := tx.NewRaw(q, releaseMBID).Scan(ctx, &release, &provider, &track, &recording); err != nil {
			return err
		}
		var localRelease, localTrack, localRecording uuid.UUID
		if err := tx.NewRaw(`SELECT release_id,track_id,recording_id FROM release_track WHERE id=?`, position).
			Scan(ctx, &localRelease, &localTrack, &localRecording); err != nil {
			return err
		}
		for _, binding := range []struct {
			query string
			id    uuid.UUID
			local uuid.UUID
		}{
			{`INSERT INTO provider_local_release(provider_id,provider_release_id,local_release_id,method,policy_version) VALUES (?,?,?,'automatic',?)`, release, localRelease},
			{`INSERT INTO provider_local_track(provider_id,provider_track_id,local_track_id,method,policy_version) VALUES (?,?,?,'automatic',?)`, track, localTrack},
			{`INSERT INTO provider_local_recording(provider_id,provider_recording_id,local_recording_id,method,policy_version) VALUES (?,?,?,'automatic',?)`, recording, localRecording},
		} {
			if _, err := tx.NewRaw(binding.query, provider, binding.id, binding.local, persistence.MatchingLayoutPolicyVersion).Exec(ctx); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("bind exact MusicBrainz position: %v", err)
	}
}

type matchingRecheckHashCommands struct{}

type captureLocalFallbackCommit struct {
	*persistence.IncomingMatchCandidateRepository
	commit persistence.AutomaticLocalFallbackCommit
}

func (capture *captureLocalFallbackCommit) ApplyAutomaticLocalFallback(ctx context.Context, commit persistence.AutomaticLocalFallbackCommit) (persistence.AutomaticLocalFallbackResult, error) {
	capture.commit = commit
	return capture.IncomingMatchCandidateRepository.ApplyAutomaticLocalFallback(ctx, commit)
}

const matchingRecheckAudio = `{"index":0,"codec_type":"audio","codec_name":"flac","sample_rate":"48000","channels":2,"channel_layout":"stereo","bits_per_raw_sample":"24"}`
const matchingRecheckProbe = `{"format":{"format_name":"flac","duration":"1.500"},"streams":[` + matchingRecheckAudio + `]}`

func (matchingRecheckHashCommands) Run(_ context.Context, executable string, args ...string) (service.PublicationCommandResult, error) {
	result := service.PublicationCommandResult{QuiescenceEstablished: true}
	switch {
	case slices.Contains(args, "hash"):
		result.Output = []byte("SHA256=" + strings.Repeat("a", 64))
	case slices.Contains(args, "-version"):
		name := strings.TrimSuffix(filepath.Base(executable), ".exe")
		result.Output = []byte(name + " version " + analysisDispatchRelease)
	case slices.Contains(args, "-protocols"):
		result.Output = []byte("Input:\n fd\n file\nOutput:\n file\n")
	default:
		return result, fmt.Errorf("unexpected publication command: %v", args)
	}
	return result, nil
}

func (matchingRecheckHashCommands) RunFile(context.Context, tools.FileCommandRequest) (service.PublicationCommandResult, error) {
	// The CURRENT FLAC fixture contains two attached pictures.
	const probe = `{"format":{"format_name":"flac"},"streams":[` + matchingRecheckAudio + `,
		{"index":1,"codec_type":"video","codec_name":"png","disposition":{"attached_pic":1}},
		{"index":2,"codec_type":"video","codec_name":"mjpeg","disposition":{"attached_pic":1}}]}`
	return service.PublicationCommandResult{QuiescenceEstablished: true, Output: []byte(probe)}, nil
}

func seedLocalRecheckCurrent(t *testing.T, ctx context.Context, fixture analysisDispatchFixture,
	position uuid.UUID,
) (native, digest string) {
	t.Helper()
	root, configured, err := fixture.registry.GetOutputDirectory(ctx)
	if err != nil || !configured {
		t.Fatalf("read fixture output root: %v", err)
	}
	const relative = "local.flac"
	native = filepath.Join(root, relative)
	audio, err := os.ReadFile("../integrations/metadata/testdata/eg.flac")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(native, audio, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (metadata.Writer{}).Write(ctx, native, map[string][]string{"TITLE": {"Local before recovery"}}, nil); err != nil {
		t.Fatal(err)
	}
	capture, err := (metadata.Reader{}).Read(ctx, native, metadata.ReadRequest{CoverInspection: true})
	if err != nil {
		t.Fatal(err)
	}
	assets, err := service.ObservePublicationAssets(capture)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(native)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(native)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	digest = hex.EncodeToString(sum[:])
	size, mtime := info.Size(), info.ModTime().UnixMicro()
	lossless, channels, rate, depth := true, 2, 48000, 24
	enc := func(value any) string {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	ownership := enc(persistence.PublicationCurrentOwnership{Version: 1,
		Identity: persistence.PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: digest}})
	observed := enc(persistence.PublicationCurrentAudio{Version: 1, Facts: persistence.PersistedAudioFacts{
		StreamIndex: 0, Codec: "flac", Lossless: &lossless, Channels: &channels, ChannelLayout: "stereo",
		SampleRate: &rate, BitDepth: &depth, Container: "flac", SupportedSourceContainer: true,
	}})
	keyHash := sha256.Sum256([]byte(relative))
	claim, generation := uuid.New(), uuid.New()
	if _, err := fixture.database.NewRaw(`WITH current AS (
		INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,
			actual_assets,quality_policy_version,validated_at)
		SELECT ?,output_epoch,?,?,?::jsonb,?::jsonb,?::jsonb,?::jsonb,?,now() FROM publication_namespace_state
		RETURNING position_id,epoch,generation
	), file_claim AS (
		INSERT INTO publication_namespace_claim(id,epoch,key_digest,canonical_key,relative_path,node_kind,owner_position_id)
		SELECT ?,epoch,?,?,?,'file',position_id FROM current RETURNING id
	) INSERT INTO publication_namespace_reservation(namespace_claim_id,position_id,generation)
	SELECT file_claim.id,current.position_id,current.generation FROM file_claim,current`,
		position, generation, relative, ownership, observed, enc(persistence.PublicationCommitMetadata{Version: 1, Tags: capture.Tags}),
		enc(assets), publication.QualityPolicyVersion, claim, keyHash[:], relative, relative).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	return native, digest
}

func prepareMatchingMoveAdmissionFixture(t *testing.T, ctx context.Context, fixture analysisDispatchFixture) {
	t.Helper()
	result, err := fixture.database.NewRaw(`UPDATE media_variant SET ffprobe_json=?::jsonb
		WHERE id=(SELECT success_probe_variant_id FROM source_analysis_step WHERE work_id=? AND step='probe')`,
		matchingRecheckProbe, fixture.work.ID).Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 1 {
		t.Fatalf("complete saved probe facts for the exact work: rows=%d err=%v", rows, err)
	}
	if err := fixture.settings.SetMany(ctx, map[string]string{
		settings.PublicationFormatKey: "source", settings.PublicationPathTemplateKey: "MusicBrainz.{EXT}",
	}); err != nil {
		t.Fatal(err)
	}
}

func grantMatchingMovePredecessorSource(t *testing.T, ctx context.Context, fixture analysisDispatchFixture, association, position uuid.UUID) {
	t.Helper()
	if _, err := fixture.database.NewRaw(`WITH selected AS (
		UPDATE track_source SET selected=true WHERE id=? RETURNING media_variant_id
	) INSERT INTO position_source_authority(position_id,mode,revision,publication_epoch,publication_generation,source_location_id,media_variant_id)
	SELECT p.position_id,'automatic',?,p.epoch,p.generation,?,s.media_variant_id
	FROM track_publication p,selected s WHERE p.position_id=?`, association, uuid.New(), fixture.track.ID, position).Exec(ctx); err != nil {
		t.Fatal(err)
	}
}

func verifyMatchingMoveAdmission(t *testing.T, ctx context.Context, fixture analysisDispatchFixture, recheck, predecessor uuid.UUID, predecessorHash string) {
	t.Helper()
	var admitted bool
	if err := fixture.database.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM matching_local_recheck r
		JOIN matching_local_recheck_publication m ON m.recheck_id=r.id
		JOIN operation o ON o.id=m.operation_id
		JOIN river_job j ON j.id=o.river_job_id
		JOIN track_publication_attempt a ON a.operation_id=m.operation_id
		JOIN publication_source_hold h ON h.operation_id=m.operation_id
		JOIN track_publication old ON old.position_id=m.predecessor_position_id AND old.generation=m.predecessor_generation
		JOIN matching_local_recheck_source s ON s.recheck_id=r.id AND s.work_id=h.work_id AND s.source_location_id=h.source_location_id
			AND s.media_variant_id=h.media_variant_id
		JOIN operation_tool_read_hold tool ON tool.operation_id=m.operation_id
		WHERE r.id=? AND r.state='publishing' AND m.predecessor_position_id=? AND m.position_id<>old.position_id AND NOT m.same_path
		AND old.ownership->'identity'->>'sha256'=?
		AND a.position_id=m.position_id AND a.candidate_generation=m.candidate_generation
		AND a.expected_generation IS NULL AND a.frozen_inputs->>'source_authority_mode'='automatic'
		AND o.state='queued' AND j.kind='publish_track_v1' AND a.intent_kind='audio'
		AND h.work_id=? AND h.source_location_id=? AND tool.installation_id=?
		AND NOT EXISTS(SELECT 1 FROM track_publication WHERE position_id=m.position_id)
	)`, recheck, predecessor, predecessorHash, fixture.work.ID, fixture.track.ID, fixture.installationID).Scan(ctx, &admitted); err != nil || !admitted {
		t.Fatalf("winning recheck did not atomically admit the moved result and hold: admitted=%t err=%v", admitted, err)
	}
}

func verifyMatchingRecheckSamePositionPublication(t *testing.T, ctx context.Context, fixture analysisDispatchFixture,
	coordinator *publication.Coordinator, recheck, association, position uuid.UUID, outputPath, beforeHash string,
) {
	t.Helper()
	var task struct {
		OperationID uuid.UUID `bun:"operation_id"`
		JobID       int64     `bun:"river_job_id"`
		Generation  uuid.UUID `bun:"candidate_generation"`
	}
	if err := fixture.database.NewRaw(`SELECT m.operation_id,o.river_job_id,m.candidate_generation FROM matching_local_recheck_publication m
		JOIN matching_local_recheck r ON r.id=m.recheck_id AND r.state='publishing'
		JOIN operation o ON o.id=m.operation_id JOIN river_job j ON j.id=o.river_job_id
		JOIN track_publication_attempt a ON a.operation_id=o.id AND a.expected_generation=m.predecessor_generation
		JOIN track_publication p ON p.position_id=m.position_id AND p.generation=m.predecessor_generation
		JOIN publication_namespace_reservation n ON n.namespace_claim_id=m.predecessor_claim_id AND n.generation=p.generation AND n.position_id=p.position_id
		WHERE m.recheck_id=? AND m.position_id=? AND m.predecessor_position_id=m.position_id AND m.same_path
		AND p.ownership->'identity'->>'sha256'=? AND a.intent_kind='rewrite' AND jsonb_typeof(a.frozen_inputs->'provider_tags')='object'
		AND j.kind='publish_track_v1' AND o.state='queued'
		AND EXISTS(SELECT 1 FROM matching_local_recheck_source s WHERE s.recheck_id=r.id AND s.work_id=?)
		AND NOT EXISTS(SELECT 1 FROM publication_source_hold h WHERE h.operation_id=m.operation_id)`,
		recheck, position, beforeHash, fixture.work.ID).Scan(ctx, &task); err != nil {
		t.Fatalf("same position did not admit an exact provider-tag rewrite: %v", err)
	}
	setMatchingRiverJobRunning(t, ctx, fixture.database, task.JobID)
	executor := service.NewPublicationExecutor(fixture.setup, fixture.registry,
		tools.Platform{GOOS: fixture.platform.Platform.GOOS, GOARCH: fixture.platform.Platform.GOARCH},
		coordinator, uuid.New(), matchingRecheckHashCommands{})
	if err := executor.ExecutePublicationDelivery(ctx, task.OperationID, task.JobID, 1); err != nil {
		t.Fatalf("execute same-position provider-tag rewrite: %v", err)
	}
	var settled bool
	if err := fixture.database.NewRaw(`SELECT EXISTS(SELECT 1 FROM track_publication WHERE position_id=? AND generation=?
		AND ownership->'identity'->>'sha256'<>?)
		AND EXISTS(SELECT 1 FROM track_source WHERE id=?)
		AND NOT EXISTS(SELECT 1 FROM matching_local_recheck WHERE id=?)
		AND NOT EXISTS(SELECT 1 FROM track_publication_attempt WHERE operation_id=?)`,
		position, task.Generation, beforeHash, association, recheck, task.OperationID).Scan(ctx, &settled); err != nil || !settled {
		t.Fatalf("same-position CURRENT was not rewritten with the Local association intact: %v, %v", settled, err)
	}
	capture, err := (metadata.Reader{}).Read(ctx, outputPath, metadata.ReadRequest{})
	if err != nil || !slices.Equal(capture.Tags["TITLE"], []string{"Fixture Track"}) {
		t.Fatalf("physical same-position tags=%v, %v", capture.Tags, err)
	}
}
