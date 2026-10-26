//go:build integration

package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/uptrace/bun"
)

func newValidPublicationStageObservation() PublicationStageObservation {
	return PublicationStageObservation{
		Version: 1, RelativePath: "album/track.flac", PayloadSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ActualAudio:          json.RawMessage(`{"version":1,"facts":{"stream_index":0,"codec":"flac","lossless":true,"container":"flac","supported_source_container":true}}`),
		ActualMetadata:       json.RawMessage(`{"version":1,"tags":{"title":["Track"],"comment":[]}}`),
		ActualAssets:         json.RawMessage(`{"version":1,"cover":{"state":"absent"},"lyrics":{"state":"absent"}}`),
		QualityPolicyVersion: publication.QualityPolicyVersion,
	}
}

func publicationFixtureInt64(value int64) *int64 {
	return &value
}

// prepareManualLyricsCommitFixture seeds a current publication and a claimed
// rewrite attempt using only the live frozen-input/result contract.
func prepareManualLyricsCommitFixture(t *testing.T) (publicationExecutionFixture, *SetupManagerRepository, PublicationInputs, PublicationStageObservation) {
	t.Helper()
	ctx := context.Background()
	f := preparePublicationResultFixture(t)
	repository := NewSetupManagerRepository(f.db)
	input := publicationInputsFixture(t, "rewrite")
	currentGeneration := input.Rewrite.CurrentGeneration
	currentIdentity := PublicationFileIdentity{SizeBytes: publicationFixtureInt64(10), MTime: publicationFixtureInt64(20), SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}
	input.Version = PublicationInputsVersion
	input.QualityPolicyVersion = publication.QualityPolicyVersion
	input.IntentKind = "rewrite"
	input.Audio = nil
	input.InitialLyrics = nil
	input.Format = publication.AudioFormatSource
	input.Rewrite = &PublicationRewriteInput{
		CurrentGeneration: currentGeneration,
		RelativePath:      "root/final.flac",
		Ownership:         currentIdentity,
		AudioFacts:        input.Rewrite.AudioFacts,
	}
	input.DesiredTags = map[string][]string{"TITLE": {"published title"}}
	const lyrics = "[00:01.00]Привет\r\n[00:02.00]world\r\n"
	input.ManualLyrics = &PublicationManualLyricsInput{
		ExpectedMetadataRevision:   7,
		ConfirmedCurrentGeneration: currentGeneration,
		State:                      "value",
		Value:                      lyrics,
	}
	if err := input.Validate(); err != nil {
		t.Fatalf("manual lyrics fixture inputs are invalid: %v", err)
	}

	lyricsDigest := sha256.Sum256([]byte(lyrics))
	observation := newValidPublicationStageObservation()
	observation.RelativePath = input.Rewrite.RelativePath
	observation.Ownership = ArtifactOwnership{ExpectedSize: publicationFixtureInt64(10), ExpectedMTime: publicationFixtureInt64(20)}
	observation.ActualAudio = mustJSON(PublicationCurrentAudio{Version: 1, Facts: input.Rewrite.AudioFacts})
	observation.ActualMetadata = mustJSON(PublicationCommitMetadata{Version: 1, Tags: map[string][]string{
		"TITLE": {"published title"}, "LYRICS": {lyrics},
	}})
	observation.ActualAssets = mustJSON(PublicationAssetOutcomes{
		Version: PublicationAssetOutcomesVersion,
		Cover:   PublicationCoverOutcome{State: PublicationAssetAbsent},
		Lyrics:  PublicationLyricsOutcome{State: PublicationAssetEmbedded, SHA256: hex.EncodeToString(lyricsDigest[:]), Format: "lrc"},
	})
	observation.QualityPolicyVersion = input.QualityPolicyVersion

	nodes := []PublicationNamespaceNode{
		{CanonicalKey: "root", RelativePath: "root", NodeKind: "dir", DirectoryScope: "root"},
		{CanonicalKey: "root/final.flac", RelativePath: "root/final.flac", NodeKind: "file"},
	}
	manifest := mustJSON(struct {
		Version int                        `json:"version"`
		Nodes   []PublicationNamespaceNode `json:"nodes"`
	}{Version: 1, Nodes: nodes})
	metadata := mustJSON(map[string]any{"local_metadata": map[string]any{
		"version": 1, "scope": "position", "desired": map[string]any{"synced_lyrics": map[string]any{
			"state": "value", "value": lyrics, "authority": "manual", "provenance": map[string]any{"kind": "manual"},
		}},
	}})
	currentMetadata := mustJSON(PublicationCommitMetadata{Version: 1, Tags: map[string][]string{"TITLE": {"published title"}}})
	currentAssets := mustJSON(PublicationAssetOutcomes{
		Version: PublicationAssetOutcomesVersion,
		Cover:   PublicationCoverOutcome{State: PublicationAssetAbsent},
		Lyrics:  PublicationLyricsOutcome{State: PublicationAssetAbsent},
	})
	currentAudio := mustJSON(PublicationCurrentAudio{Version: 1, Facts: input.Rewrite.AudioFacts})
	currentOwnership := mustJSON(PublicationCurrentOwnership{Version: PublicationCurrentOwnershipVersion, Identity: currentIdentity})
	if err := f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE operation SET stage='preparing',input_snapshot=jsonb_set(input_snapshot,'{intent_kind}',?::jsonb,true) WHERE id=?`, string(mustJSON("rewrite")), f.guard.OperationID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE track_publication_attempt SET expected_generation=?,intent_kind='rewrite',phase='preparing',frozen_inputs=?::jsonb,namespace_manifest=?::jsonb WHERE operation_id=?`, currentGeneration, string(mustJSON(input)), string(manifest), f.guard.OperationID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at) VALUES (?,?,?,?,?::jsonb,?::jsonb,?::jsonb,?::jsonb,?,now())`,
			f.guard.ExpectedPositionID, f.guard.Epoch, currentGeneration, input.Rewrite.RelativePath, string(currentOwnership), string(currentAudio), string(currentMetadata), string(currentAssets), input.QualityPolicyVersion); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE release_track SET metadata_revision=7,metadata=?::jsonb WHERE id=?`, string(metadata), f.guard.ExpectedPositionID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO position_source_authority(position_id,mode,revision,publication_epoch,publication_generation,source_location_id,media_variant_id) VALUES (?,'manual',?,?,?,?,?)`,
			f.guard.ExpectedPositionID, uuid.New(), f.guard.Epoch, currentGeneration, uuid.New(), uuid.New()); err != nil {
			return err
		}
		for _, node := range nodes {
			digest := sha256.Sum256([]byte(node.CanonicalKey))
			claimID := uuid.New()
			var owner any = f.guard.ExpectedPositionID
			var directoryScope any
			if node.NodeKind == "dir" {
				owner = nil
				directoryScope = node.DirectoryScope
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO publication_namespace_claim(id,epoch,key_digest,canonical_key,relative_path,node_kind,owner_position_id,directory_scope_key) VALUES (?,?,?,?,?,?,?,?)`,
				claimID, f.guard.Epoch, digest[:], node.CanonicalKey, node.RelativePath, node.NodeKind, owner, directoryScope); err != nil {
				return err
			}
			for _, generation := range []uuid.UUID{currentGeneration, f.guard.CandidateGeneration} {
				if _, err := tx.ExecContext(ctx, `INSERT INTO publication_namespace_reservation(namespace_claim_id,position_id,generation) VALUES (?,?,?)`, claimID, f.guard.ExpectedPositionID, generation); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return f, repository, input, observation
}
