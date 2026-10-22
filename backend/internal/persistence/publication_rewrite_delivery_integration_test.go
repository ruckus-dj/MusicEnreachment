//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/uptrace/bun"
)

func TestCheckPublicationRewriteForDeliveryTracksCurrentArtifactAndClaimStatePostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	ctx := context.Background()
	repo := NewSetupManagerRepository(f.db)
	currentGeneration := uuid.New()
	identity := PublicationFileIdentity{SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}
	lossless := false
	facts := PersistedAudioFacts{StreamIndex: 0, Codec: "mp3", Lossless: &lossless, Container: publication.AudioContainerMP3, SupportedSourceContainer: true}
	installationID := uuid.New()
	input := PublicationInputs{
		Version: PublicationInputsVersion, QualityPolicyVersion: publication.QualityPolicyVersion, IntentKind: "rewrite",
		Rewrite: &PublicationRewriteInput{CurrentGeneration: currentGeneration, RelativePath: "album/current.mp3", Ownership: identity, AudioFacts: facts},
		Format:  publication.AudioFormatSource, DesiredTags: map[string][]string{},
		Tools: []SourceAnalysisToolSelection{
			{PackageKind: "ffmpeg", InstallationID: installationID, Executable: "ffmpeg", RelativePath: "bin/ffmpeg", Version: "1", VersionBanner: "ffmpeg version 1"},
			{PackageKind: "ffmpeg", InstallationID: installationID, Executable: "ffprobe", RelativePath: "bin/ffprobe", Version: "1", VersionBanner: "ffprobe version 1"},
		},
	}
	if err := input.Validate(); err != nil {
		t.Fatalf("invalid frozen publication input: %v", err)
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	ownership, _ := json.Marshal(PublicationCurrentOwnership{Version: 1, Identity: identity})
	actualAudio, _ := json.Marshal(PublicationCurrentAudio{Version: 1, Facts: facts})
	if _, err := f.db.NewRaw(`INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at) VALUES (?,?,?,?,?::jsonb,?::jsonb,'{}','{}',1,now())`, f.guard.ExpectedPositionID, f.guard.Epoch, currentGeneration, "album/current.mp3", string(ownership), string(actualAudio)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot, err := json.Marshal(struct {
		Version     int       `json:"version"`
		PositionID  uuid.UUID `json:"position_id"`
		OutputEpoch uuid.UUID `json:"output_epoch"`
		IntentKind  string    `json:"intent_kind"`
	}{Version: 1, PositionID: f.guard.ExpectedPositionID, OutputEpoch: f.guard.Epoch, IntentKind: "rewrite"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewRaw(`UPDATE operation SET input_snapshot=?::jsonb WHERE id=?`, string(snapshot), f.guard.OperationID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`UPDATE track_publication_attempt SET expected_generation=?,intent_kind='rewrite',frozen_inputs=?::jsonb WHERE operation_id=?`, currentGeneration, string(raw), f.guard.OperationID).Exec(ctx); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.ClaimPublicationDelivery(ctx, f.guard); err != nil || got != PublicationDeliveryAcquired {
		t.Fatalf("claim = %q, %v", got, err)
	}
	check := func(want bool) {
		t.Helper()
		facts, ok, err := repo.CheckPublicationRewriteForDelivery(ctx, f.guard)
		if err != nil || ok != want {
			t.Fatalf("rewrite check = %t, %v; want %t", ok, err, want)
		}
		if ok && facts.Input.Rewrite.CurrentGeneration != currentGeneration {
			t.Fatalf("rewrite facts = %+v", facts.Input.Rewrite)
		}
	}
	check(true)
	for _, test := range []struct {
		name   string
		column string
		value  func() (string, error)
	}{
		{
			name:   "ownership identity drift at same generation",
			column: "ownership",
			value: func() (string, error) {
				changed := identity
				changed.SHA256 = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
				raw, err := json.Marshal(PublicationCurrentOwnership{Version: 1, Identity: changed})
				return string(raw), err
			},
		},
		{
			name:   "audio facts drift at same generation",
			column: "actual_audio",
			value: func() (string, error) {
				changed := facts
				changed.StreamIndex = 1
				raw, err := json.Marshal(PublicationCurrentAudio{Version: 1, Facts: changed})
				return string(raw), err
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed, err := test.value()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.NewRaw(`UPDATE track_publication SET `+test.column+`=?::jsonb WHERE position_id=? AND epoch=? AND generation=?`, changed, f.guard.ExpectedPositionID, f.guard.Epoch, currentGeneration).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			var generation uuid.UUID
			if err := f.db.NewRaw(`SELECT generation FROM track_publication WHERE position_id=?`, f.guard.ExpectedPositionID).Scan(ctx, &generation); err != nil || generation != currentGeneration {
				t.Fatalf("drift fixture changed generation: %s, %v", generation, err)
			}
			check(false)
			baseline := string(ownership)
			if test.column == "actual_audio" {
				baseline = string(actualAudio)
			}
			if _, err := f.db.NewRaw(`UPDATE track_publication SET `+test.column+`=?::jsonb WHERE position_id=? AND epoch=? AND generation=?`, baseline, f.guard.ExpectedPositionID, f.guard.Epoch, currentGeneration).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			check(true)
		})
	}
	if ok, err := repo.AdvancePublicationStage(ctx, f.guard, "preparing", newValidPublicationStageObservation()); err != nil || !ok {
		t.Fatalf("stage = %t, %v", ok, err)
	}
	check(true)
	if changed, err := repo.MarkPublicationQuiescent(ctx, f.guard); err != nil || !changed {
		t.Fatalf("quiesce = %t, %v", changed, err)
	}
	check(false)
}
