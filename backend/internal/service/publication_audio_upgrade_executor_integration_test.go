//go:build integration

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image/color"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	integrationtools "github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/uptrace/bun"
	"go.senan.xyz/taglib"
)

// The command transport supplies technical observations, not a real codec
// upgrade. Admission, filesystem replacement, metadata and DB settlement are real.
func TestAudioUpgradeExecutorRetriesAfterInstalledResultRollbackPostgreSQL(t *testing.T) {
	testPublicationSourceExecution(t, "Track.flac", true, false, publicationDeliveryScenario{run: func(ctx context.Context, db *bun.DB, repository *persistence.SetupManagerRepository, executor *PublicationExecutor, firstID uuid.UUID, firstJob int64, source, output, target string, _ []byte) {
		initial, err := repository.ReadPublicationAttempt(ctx, firstID)
		if err != nil || initial == nil {
			t.Fatalf("read initial attempt: %+v, %v", initial, err)
		}
		frozen, err := persistence.DecodePublicationInputs(initial.FrozenInputs)
		if err != nil {
			t.Fatal(err)
		}
		_, nodes, err := publicationTarget(initial.NamespaceManifest)
		if err != nil {
			t.Fatal(err)
		}
		if err := executor.ExecutePublicationDelivery(ctx, firstID, firstJob, 1); err != nil {
			t.Fatalf("first publication: %v", err)
		}
		marshal := func(value any) string {
			t.Helper()
			data, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			return string(data)
		}
		destination := filepath.Join(output, filepath.FromSlash(target))
		baseline, err := (metadata.Reader{}).Read(ctx, destination, metadata.ReadRequest{})
		if err != nil {
			t.Fatal(err)
		}
		baseline.Tags["LYRICS"] = []string{"[00:00.00]Preserved CURRENT lyrics\n"}
		if _, err := (metadata.Writer{}).Write(ctx, destination, baseline.Tags, nil); err != nil {
			t.Fatal(err)
		}
		for _, image := range []metadata.ArtworkSnapshotImage{
			{Index: 0, Type: "Front Cover", Description: "retained front", MIME: "image/jpeg", Data: audioUpgradeImage(t, "image/jpeg", color.RGBA{R: 220, A: 255})},
			{Index: 1, Type: "Back Cover", Description: "retained back", MIME: "image/png", Data: audioUpgradeImage(t, "image/png", color.RGBA{B: 220, A: 255})},
		} {
			if err := taglib.WriteImageOptions(destination, image.Data, image.Index, image.Type, image.Description, image.MIME); err != nil {
				t.Fatal(err)
			}
		}
		baseline, err = (metadata.Reader{}).Read(ctx, destination, metadata.ReadRequest{CoverInspection: true})
		if err != nil {
			t.Fatal(err)
		}
		baselineAssets, err := ObservePublicationAssets(baseline)
		if err != nil {
			t.Fatal(err)
		}
		original, err := os.ReadFile(destination)
		if err != nil {
			t.Fatal(err)
		}
		// A valid extra FLAC PADDING block makes CURRENT physically distinct from
		// the next source-derived candidate without changing its audio or assets.
		padded := audioUpgradePaddedFLAC(t, original)
		if err := os.WriteFile(destination, padded, 0o600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(destination)
		if err != nil {
			t.Fatal(err)
		}
		size, mtime := info.Size(), info.ModTime().UnixMicro()
		digest := sha256.Sum256(padded)
		identity := persistence.PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: hex.EncodeToString(digest[:])}
		oldFacts := frozen.Audio.AudioFacts
		oldFacts.ChannelLayout = "stereo"
		oldFacts.BitDepth, oldFacts.SampleRate = preparationIntPointer(16), preparationIntPointer(44100)
		if _, err := db.ExecContext(ctx, `UPDATE track_publication SET ownership=?::jsonb,actual_audio=?::jsonb,actual_metadata=?::jsonb,actual_assets=?::jsonb WHERE position_id=?`,
			marshal(persistence.PublicationCurrentOwnership{Version: 1, Identity: identity}),
			marshal(persistence.PublicationCurrentAudio{Version: 1, Facts: oldFacts}),
			marshal(persistence.PublicationCommitMetadata{Version: 1, Tags: baseline.Tags}), marshal(baselineAssets), initial.PositionID); err != nil {
			t.Fatal(err)
		}
		probe := `{"format":{"format_name":"flac"},"streams":[{"index":0,"codec_type":"audio","codec_name":"flac","channels":2,"channel_layout":"stereo","bits_per_raw_sample":"24","sample_rate":"48000"}]}`
		if _, err := db.ExecContext(ctx, `UPDATE media_variant SET ffprobe_json=?::jsonb WHERE id=?`, probe, frozen.Audio.MediaVariantID); err != nil {
			t.Fatal(err)
		}
		var releaseID uuid.UUID
		if err := db.NewRaw(`SELECT release_id FROM release_track WHERE id=?`, initial.PositionID).Scan(ctx, &releaseID); err != nil {
			t.Fatal(err)
		}
		snapshot, err := repository.ReadLibraryFirstPublicationContext(ctx, releaseID, initial.PositionID)
		if err != nil || snapshot == nil || len(snapshot.Sources) != 1 || snapshot.Library.Current == nil {
			t.Fatalf("read upgrade context: %+v, %v", snapshot, err)
		}
		selected := snapshot.Sources[0]
		current := snapshot.Library.Current
		authority, err := repository.ReadPositionSourceAuthority(ctx, initial.PositionID)
		if err != nil || authority == nil || authority.Mode != "automatic" {
			t.Fatalf("initial automatic authority: %+v, %v", authority, err)
		}
		artwork, err := metadata.ReadArtworkSnapshot(ctx, destination)
		if err != nil {
			t.Fatal(err)
		}
		input := persistence.PublicationInputs{
			Version: persistence.PublicationAudioUpgradeInputsVersion, QualityPolicyVersion: publication.QualityPolicyVersion,
			IntentKind: "audio", SourceAuthorityMode: "automatic", Format: publication.AudioFormatSource,
			Audio: &selected.Input, Tools: frozen.Tools, DesiredTags: current.Metadata.Tags,
			AudioUpgrade: &persistence.PublicationAudioUpgradeInput{SourceAuthorityRevision: authority.Revision,
				Current: persistence.PublicationCurrentWitness{Generation: current.Record.Generation, RelativePath: current.Record.RelativePath,
					Ownership: identity, AudioFacts: oldFacts, Assets: current.Assets, Tags: current.Metadata.Tags,
					ArtworkInventory: audioUpgradeArtworkWitnesses(artwork.ArtworkInventory())}},
		}
		generation := current.Record.Generation
		operationID, err := repository.AdmitAutomaticAudioUpgrade(ctx, persistence.PublicationAdmission{
			PositionID: initial.PositionID, ExpectedReleaseID: releaseID, ExpectedEpoch: initial.Epoch,
			ExpectedGeneration: &generation, CandidateGeneration: uuid.New(), IntentKind: "audio",
			ToolsReadRequired: true, Tools: frozen.Tools, FrozenInputs: json.RawMessage(marshal(input)), NamespaceManifest: nodes,
			Source: &persistence.PublicationSourceSelection{RootID: selected.Input.RootID, LocationID: selected.Input.LocationID,
				WorkID: selected.Input.WorkID, MediaVariantID: selected.Input.MediaVariantID, ConfiguredPath: selected.Input.ConfiguredRootPath,
				InventoryPath: selected.Input.InventoryRootPath, RelativePath: selected.Input.RelativePath,
				SizeBytes: selected.Input.SizeBytes, MTime: selected.Input.MTime, HoldUUID: uuid.New()},
		}, libraryPublicationTestInserter{})
		if err != nil {
			t.Fatalf("admit upgrade: %v", err)
		}
		op, err := repository.GetOperation(ctx, operationID)
		if err != nil || op == nil || op.RiverJobID == nil {
			t.Fatalf("read upgrade operation: %+v, %v", op, err)
		}
		jobID := *op.RiverJobID
		if _, err := db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=1,attempted_at=now() WHERE id=?`, jobID); err != nil {
			t.Fatal(err)
		}
		executor.commands = audioUpgradeExecutorCommands{}
		writer := &countingPublicationMetadataWriter{delegate: metadata.Writer{}}
		executor.metadata = writer
		if _, err := db.ExecContext(ctx, `CREATE FUNCTION reject_audio_upgrade_result() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected upgrade result failure'; END $$`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `CREATE TRIGGER reject_audio_upgrade_result BEFORE UPDATE ON track_publication FOR EACH ROW EXECUTE FUNCTION reject_audio_upgrade_result()`); err != nil {
			t.Fatal(err)
		}
		err = executor.ExecutePublicationDelivery(ctx, operationID, jobID, 1)
		if err == nil || !strings.Contains(err.Error(), "injected upgrade result failure") {
			t.Fatalf("upgrade result failure: %v", err)
		}
		installed, err := os.ReadFile(destination)
		if err != nil || sha256.Sum256(installed) == digest {
			t.Fatalf("upgrade did not replace CURRENT before DB failure: %v", err)
		}
		retained, err := repository.ReadPublicationAttempt(ctx, operationID)
		if err != nil || retained == nil {
			t.Fatalf("failed result lost attempt: %+v, %v", retained, err)
		}
		var failedCurrent persistence.TrackPublication
		if err := db.NewSelect().Model(&failedCurrent).Where("position_id=?", initial.PositionID).Scan(ctx); err != nil || failedCurrent.Generation != generation {
			t.Fatalf("failed result advanced CURRENT: %+v, %v", failedCurrent, err)
		}
		failedAuthority, err := repository.ReadPositionSourceAuthority(ctx, initial.PositionID)
		if err != nil || failedAuthority == nil || failedAuthority.Revision != authority.Revision {
			t.Fatalf("failed result changed source authority: %+v, %v", failedAuthority, err)
		}
		var holds int
		if err := db.NewRaw(`SELECT count(*) FROM publication_source_hold WHERE operation_id=?`, operationID).Scan(ctx, &holds); err != nil || holds != 1 {
			t.Fatalf("failed result source holds=%d, err=%v", holds, err)
		}
		if _, err := db.ExecContext(ctx, `DROP TRIGGER reject_audio_upgrade_result ON track_publication; DROP FUNCTION reject_audio_upgrade_result()`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE river_job SET state='running',attempt=2 WHERE id=?`, jobID); err != nil {
			t.Fatal(err)
		}
		// The failed result transaction left the candidate installed. The retry
		// must rebuild it and pass through native replacement even though its
		// resulting bytes are identical to the already-installed candidate.
		installedBeforeRetry, err := os.ReadFile(destination)
		if err != nil {
			t.Fatal(err)
		}
		beforeRetryFile, err := os.Open(destination)
		if err != nil {
			t.Fatal(err)
		}
		// File.Stat captures the handle's identity eagerly on Windows; os.Stat
		// may defer that lookup until SameFile, after the target was replaced.
		beforeRetryInfo, statErr := beforeRetryFile.Stat()
		closeErr := beforeRetryFile.Close()
		if err := errors.Join(statErr, closeErr); err != nil {
			t.Fatal(err)
		}
		beforeRetryDigest := sha256.Sum256(installedBeforeRetry)
		if err := executor.ExecutePublicationDelivery(ctx, operationID, jobID, 2); err != nil {
			t.Fatalf("upgrade retry after installed result rollback: %v", err)
		}
		if writer.calls != 2 {
			t.Fatalf("preparation writes=%d, want two complete preparations", writer.calls)
		}
		if attempt, err := repository.ReadPublicationAttempt(ctx, operationID); err != nil || attempt != nil {
			t.Fatalf("successful upgrade retained attempt: %+v, %v", attempt, err)
		}
		updatedAuthority, err := repository.ReadPositionSourceAuthority(ctx, initial.PositionID)
		if err != nil || updatedAuthority == nil || updatedAuthority.PublicationGeneration != retained.CandidateGeneration || updatedAuthority.Revision == authority.Revision {
			t.Fatalf("successful upgrade did not advance source authority: %+v, %v", updatedAuthority, err)
		}
		settled, err := repository.ReadLibraryPublicationContext(ctx, releaseID, initial.PositionID)
		if err != nil || settled == nil || settled.Current == nil || settled.Current.Record.Generation != retained.CandidateGeneration ||
			publication.CompareQuality(audioUpgradeQualityCandidate(settled.Current.Audio.Facts), audioUpgradeQualityCandidate(oldFacts)) != publication.QualityBetter {
			t.Fatalf("successful upgrade CURRENT projection: %+v, %v", settled, err)
		}
		installed, err = os.ReadFile(destination)
		if err != nil {
			t.Fatal(err)
		}
		afterRetryInfo, err := os.Stat(destination)
		if err != nil {
			t.Fatal(err)
		}
		installedDigest := sha256.Sum256(installed)
		if installedDigest != beforeRetryDigest {
			t.Fatalf("retry candidate changed bytes: before=%x after=%x", beforeRetryDigest, installedDigest)
		}
		if os.SameFile(beforeRetryInfo, afterRetryInfo) {
			t.Fatal("retry did not replace the byte-identical installed candidate")
		}
		if settled.Current.Ownership.Identity.SHA256 != hex.EncodeToString(installedDigest[:]) {
			t.Fatal("successful CURRENT ownership differs from installed output")
		}
		capture, err := (metadata.Reader{}).Read(ctx, destination, metadata.ReadRequest{CoverInspection: true})
		if err != nil || !reflect.DeepEqual(capture.Tags, current.Metadata.Tags) {
			t.Fatalf("upgrade retry changed CURRENT tags: %+v, %v", capture.Tags, err)
		}
		finalArtwork, err := metadata.ReadArtworkSnapshot(ctx, destination)
		if err != nil || !reflect.DeepEqual(finalArtwork, artwork) || len(finalArtwork.Images) != 2 {
			t.Fatalf("upgrade retry changed CURRENT artwork: %+v, %v", finalArtwork.ArtworkInventory(), err)
		}
		for _, table := range []string{"publication_source_hold", "operation_tool_read_hold", "publication_execution_claim"} {
			if err := db.NewRaw(`SELECT count(*) FROM `+table+` WHERE operation_id=?`, operationID).Scan(ctx, &holds); err != nil || holds != 0 {
				t.Fatalf("successful upgrade %s=%d, err=%v", table, holds, err)
			}
		}
		if op, err := repository.GetOperation(ctx, operationID); err != nil || op == nil || op.State != "succeeded" {
			t.Fatalf("successful upgrade operation: %+v, %v", op, err)
		}
		if err := os.Remove(source); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(destination); err != nil {
			t.Fatal(err)
		}
		if err := executor.ExecutePublicationDelivery(ctx, operationID, jobID, 2); err != nil || writer.calls != 2 {
			t.Fatalf("completed upgrade touched missing source/output: writes=%d err=%v", writer.calls, err)
		}
	}})
}

func audioUpgradePaddedFLAC(t *testing.T, data []byte) []byte {
	t.Helper()
	if len(data) < 8 || string(data[:4]) != "fLaC" {
		t.Fatal("invalid FLAC fixture")
	}
	result := append([]byte(nil), data...)
	for offset := 4; offset+4 <= len(result); {
		length := int(result[offset+1])<<16 | int(result[offset+2])<<8 | int(result[offset+3])
		end := offset + 4 + length
		if end > len(result) {
			t.Fatal("truncated FLAC fixture metadata")
		}
		if result[offset]&0x80 != 0 {
			result[offset] &^= 0x80
			padding := make([]byte, 20)
			padding[0], padding[3] = 0x81, 16
			return append(append(result[:end:end], padding...), result[end:]...)
		}
		offset = end
	}
	t.Fatal("FLAC fixture has no final metadata block")
	return nil
}

type audioUpgradeExecutorCommands struct{ audioDeliveryTestCommands }

func (audioUpgradeExecutorCommands) RunFile(_ context.Context, request integrationtools.FileCommandRequest) (PublicationCommandResult, error) {
	if request.Input == nil {
		return PublicationCommandResult{}, os.ErrInvalid
	}
	return PublicationCommandResult{QuiescenceEstablished: true, Output: []byte(`{"format":{"format_name":"flac"},"streams":[{"index":0,"codec_type":"audio","codec_name":"flac","channels":2,"channel_layout":"stereo","bits_per_raw_sample":"24","sample_rate":"48000"},{"index":1,"codec_type":"video","disposition":{"attached_pic":1},"tags":{"mimetype":"image/jpeg"}},{"index":2,"codec_type":"video","disposition":{"attached_pic":1},"tags":{"mimetype":"image/png"}}]}`)}, nil
}
