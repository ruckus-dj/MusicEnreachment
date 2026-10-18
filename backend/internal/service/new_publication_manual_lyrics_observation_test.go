package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type manualLyricsObservationRepository struct {
	*scopeRepositoryFake
	*optionalLyricsV3Repository
}

func TestManualLyricsObservationRequiresNativeCopyProof(t *testing.T) {
	if _, err := newPublicationManualLyricsObservationAuthority(context.Background(), nil, nil, nil, persistence.PublicationInputs{}); err == nil {
		t.Fatal("expected native initial-copy proof requirement")
	}
	var authority *publicationManualLyricsObservationAuthority
	if _, err := authority.Observe(context.Background()); err == nil {
		t.Fatal("expected unavailable observation authority")
	}
}

func TestManualLyricsObservationAuthorityUsesNativePositiveEvidence(t *testing.T) {
	stage, input, authority, scope, reservation, _ := manualLyricsObservationFixture(t)
	beforeInput, err := os.ReadFile(stage.paths.InputPath)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := authority.Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if observation.PayloadSHA256 == "" || observation.ActualAudio == nil || observation.ActualMetadata == nil || observation.ActualAssets == nil {
		t.Fatalf("observation lacks native payload evidence: %#v", observation)
	}
	outputInfo, err := os.Stat(stage.paths.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	if observation.Version != 1 || observation.RelativePath != stage.paths.RelativePath ||
		observation.Ownership.ExpectedSize == nil || *observation.Ownership.ExpectedSize != outputInfo.Size() ||
		observation.Ownership.ExpectedMTime == nil || *observation.Ownership.ExpectedMTime != outputInfo.ModTime().UnixMicro() ||
		observation.QualityPolicyVersion != input.QualityPolicyVersion {
		t.Fatalf("native ownership facts = %#v, output info = %#v", observation, outputInfo)
	}
	output, err := os.ReadFile(stage.paths.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(output)
	if observation.PayloadSHA256 != hex.EncodeToString(digest[:]) {
		t.Fatal("observation payload digest does not match native output")
	}
	var audio persistence.PublicationCurrentAudio
	var tags persistence.PublicationCommitMetadata
	var assets persistence.PublicationAssetOutcomes
	if err := json.Unmarshal(observation.ActualAudio, &audio); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(observation.ActualMetadata, &tags); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(observation.ActualAssets, &assets); err != nil {
		t.Fatal(err)
	}
	if audio.Version != 1 || !reflect.DeepEqual(audio.Facts, input.Rewrite.AudioFacts) {
		t.Fatalf("audio evidence = %#v", audio)
	}
	wantTags := map[string][]string{"TITLE": {"Track"}, "ARTIST": {"Artist"}, "LYRICS": {input.ManualLyrics.Value}}
	if tags.Version != 1 || !reflect.DeepEqual(tags.Tags, wantTags) {
		t.Fatalf("metadata evidence = %#v", tags)
	}
	if err := assets.ValidateAgainstMetadata(tags.Tags, nil); err != nil {
		t.Fatalf("assets do not validate against observed metadata: %v", err)
	}
	if assets.Cover.State != persistence.PublicationAssetAbsent || assets.Lyrics.State != persistence.PublicationAssetEmbedded {
		t.Fatalf("asset evidence = %#v", assets)
	}
	afterInput, err := os.ReadFile(stage.paths.InputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeInput, afterInput) || !reflect.DeepEqual(input, authority.input) {
		t.Fatal("observation or metadata mutation changed frozen input")
	}
	scope.releaseSynchronousWork(reservation)
}

func TestApplyAndRecordPreparedManualLyricsFLACUsesNativeEvidenceAndFencesAckFailure(t *testing.T) {
	for _, test := range []struct {
		name    string
		ackErr  error
		wantErr bool
	}{
		{name: "recorded"},
		{name: "ack error", ackErr: errors.New("outcome acknowledgement unavailable"), wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			stage, input, _, scope, reservation, proof := manualLyricsObservationFixture(t)
			repo := stage.repository.(*optionalLyricsV3Repository)
			repo.outcome = persistence.PublicationOptionalLyricsRecorded
			repo.recordErr = test.ackErr
			ackSawWork := false
			repo.onRecord = func(persistence.PublicationOptionalLyricsOutcomeEvidence) {
				ackSawWork = reservation.borrowed && scope.validPrivateManualLyricsWork(scope.privateManualLyricsWork, stage)
			}
			result, err := applyAndRecordPreparedManualLyricsFLAC(context.Background(), scope, reservation, proof, input)
			if test.wantErr {
				if err == nil {
					t.Fatal("expected acknowledgement error")
				}
				if !ackSawWork {
					t.Fatal("failed acknowledgement ran without the exact borrowed work capability")
				}
				scope.mu.Lock()
				unknown, borrowed := scope.unknown, reservation.borrowed
				scope.mu.Unlock()
				if !unknown || !borrowed {
					t.Fatalf("ack error did not retain and quarantine work: unknown=%v borrowed=%v", unknown, borrowed)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.Lyrics.State != persistence.PublicationAssetEmbedded || repo.called != 1 || len(repo.records) != 1 {
				t.Fatalf("result=%#v records=%d calls=%d", result, len(repo.records), repo.called)
			}
			if !ackSawWork {
				t.Fatal("durable acknowledgement ran without the exact borrowed work capability")
			}
			evidence := repo.records[0]
			if evidence.InputIdentity.SHA256 != stage.rewriteIdentity.SHA256 || evidence.OutputIdentity.SHA256 == "" || evidence.OutputIdentity.SHA256 != evidence.FinalObservation.PayloadSHA256 {
				t.Fatalf("evidence is not bound to native input/output: %#v", evidence)
			}
			var tags persistence.PublicationCommitMetadata
			if err := json.Unmarshal(evidence.FinalObservation.ActualMetadata, &tags); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(tags.Tags, map[string][]string{"TITLE": {"Track"}, "ARTIST": {"Artist"}, "LYRICS": {input.ManualLyrics.Value}}) {
				t.Fatalf("final observation metadata = %#v", tags.Tags)
			}
			scope.mu.Lock()
			borrowed := reservation.borrowed
			scope.mu.Unlock()
			if borrowed {
				t.Fatal("successful durable acknowledgement retained the reservation borrow")
			}
		})
	}
}

func manualLyricsObservationFixture(t *testing.T) (*preparedPublicationStage, persistence.PublicationInputs, *publicationManualLyricsObservationAuthority, *PublicationExecutionScope, *publicationSyncReservation, *publicationInitialOutputCopy) {
	t.Helper()
	fixture, stage, _ := preparedManualLyricsFLACStage(t)
	repository := fixture.preparer.Repository.(*optionalLyricsV3Repository)
	input := repository.input.Clone()
	input.DesiredTags = map[string][]string{"TITLE": {"Track"}, "ARTIST": {"Artist"}}
	installationID := uuid.New()
	input.Tools = []persistence.SourceAnalysisToolSelection{
		{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: "bin/ffmpeg", Executable: "ffmpeg", Version: "1", VersionBanner: "ffmpeg 1"},
		{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: "bin/ffprobe", Executable: "ffprobe", Version: "1", VersionBanner: "ffprobe 1"},
	}
	input.ManualLyrics = &persistence.PublicationManualLyricsInput{State: "value", Value: "[00:00.00]Lyric", ConfirmedCurrentGeneration: input.Rewrite.CurrentGeneration}
	repository.input = input.Clone()
	scope, reservation := initialOutputCopyScope(t, fixture.guard)
	scope.repository = &manualLyricsObservationRepository{
		scopeRepositoryFake:        &scopeRepositoryFake{valid: true, marked: true},
		optionalLyricsV3Repository: repository,
	}
	proof, err := stage.initialOutputCopy(context.Background(), scope, reservation)
	if err != nil {
		t.Fatal(err)
	}
	// Bind authority to the copied, untouched native output before metadata changes.
	authority, err := newPublicationManualLyricsObservationAuthority(context.Background(), scope, reservation, proof, input)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := metadata.SnapshotFlacArtwork(context.Background(), stage.paths.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := metadata.WriteFlacTagsPreservingArtwork(context.Background(), stage.paths.OutputPath, map[string][]string{
		"TITLE": {"Track"}, "ARTIST": {"Artist"}, "LYRICS": {input.ManualLyrics.Value},
	}, baseline); err != nil {
		t.Fatal(err)
	}
	return stage, input, authority, scope, reservation, proof
}

func TestManualLyricsObservationRejectsMandatoryMetadataMismatch(t *testing.T) {
	stage, _, authority, scope, reservation, _ := manualLyricsObservationFixture(t)
	baseline, err := metadata.SnapshotFlacArtwork(context.Background(), stage.paths.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := metadata.WriteFlacTagsPreservingArtwork(context.Background(), stage.paths.OutputPath, map[string][]string{
		"TITLE": {"Wrong"}, "ARTIST": {"Artist"}, "LYRICS": {"[00:00.00]Lyric"},
	}, baseline); err != nil {
		t.Fatal(err)
	}
	if _, err := authority.Observe(context.Background()); err == nil {
		t.Fatal("accepted native output with mismatched mandatory TITLE")
	}
	scope.releaseSynchronousWork(reservation)
}

func TestManualLyricsObservationRejectsEncodedAudioMutation(t *testing.T) {
	stage, _, authority, scope, reservation, _ := manualLyricsObservationFixture(t)
	original, err := os.ReadFile(stage.paths.OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	mutated := append([]byte(nil), original...)
	mutated[len(mutated)-1] ^= 0x1 // FLAC audio frame, after the metadata blocks.
	if err := os.WriteFile(stage.paths.OutputPath, mutated, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := authority.Observe(context.Background()); err == nil {
		t.Fatal("accepted native output with changed encoded audio")
	}
	scope.releaseSynchronousWork(reservation)
}
