//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

func TestPublicationStageAssetsRequireCurrentInputEvidencePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newPublicationExecutionFixture(t)
	repo := NewSetupManagerRepository(f.db)
	// Keep frozen inputs valid by using the current canonical contract fixture.
	setFrozenInputs := func(raw string) {
		t.Helper()
		if _, err := f.db.NewRaw(`UPDATE track_publication_attempt SET frozen_inputs=?::jsonb WHERE operation_id=?`, raw, f.guard.OperationID).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	publicationInputs := publicationInputsFixture(t, "audio")
	publicationInputs.Version = PublicationInputsVersion
	publicationInputs.QualityPolicyVersion = publication.QualityPolicyVersion
	rawInputs, err := json.Marshal(publicationInputs)
	if err != nil {
		t.Fatal(err)
	}
	setFrozenInputs(string(rawInputs))
	if outcome, err := repo.ClaimPublicationDelivery(ctx, f.guard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim = %q, %v", outcome, err)
	}
	base := newValidPublicationStageObservation()
	validAssetsJSON, err := json.Marshal(PublicationAssetOutcomes{
		Version: PublicationAssetOutcomesVersion,
		Cover:   PublicationCoverOutcome{State: PublicationAssetAbsent},
		Lyrics:  PublicationLyricsOutcome{State: PublicationAssetAbsent},
	})
	if err != nil {
		t.Fatal(err)
	}
	validAssets := json.RawMessage(validAssetsJSON)
	valid := base
	valid.ActualMetadata = json.RawMessage(`{"version":1,"tags":{"title":["Track"],"comment":[]}}`)
	valid.ActualAssets = validAssets
	valid.QualityPolicyVersion = publication.QualityPolicyVersion
	size, mtime := int64(10), int64(20)
	valid.Ownership = ArtifactOwnership{ExpectedSize: &size, ExpectedMTime: &mtime}
	recordValidInitialLyricsOutcome(t, f, repo, valid)
	if changed, err := repo.AdvancePublicationStage(ctx, f.guard, "preparing", valid); err != nil || !changed {
		t.Fatalf("preparing -> staged = %t, %v", changed, err)
	}
	if changed, err := repo.AdvancePublicationStage(ctx, f.guard, "staged", valid); err != nil || !changed {
		t.Fatalf("valid assets evidence = %t, %v", changed, err)
	}

	for _, tc := range []struct {
		name   string
		assets json.RawMessage
		policy int
	}{
		{"missing", nil, 1}, {"null", json.RawMessage(`null`), 1}, {"wrong policy", validAssets, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPublicationExecutionFixture(t)
			repo := NewSetupManagerRepository(f.db)
			inputs, _ := json.Marshal(publicationInputs)
			if _, err := f.db.NewRaw(`UPDATE track_publication_attempt SET frozen_inputs=?::jsonb WHERE operation_id=?`, string(inputs), f.guard.OperationID).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			if outcome, err := repo.ClaimPublicationDelivery(ctx, f.guard); err != nil || outcome != PublicationDeliveryAcquired {
				t.Fatalf("claim = %q, %v", outcome, err)
			}
			recordValidInitialLyricsOutcome(t, f, repo, valid)
			if changed, err := repo.AdvancePublicationStage(ctx, f.guard, "preparing", valid); err != nil || !changed {
				t.Fatalf("stage = %t, %v", changed, err)
			}
			observation := valid
			observation.ActualAssets = tc.assets
			observation.QualityPolicyVersion = tc.policy
			if changed, err := repo.AdvancePublicationStage(ctx, f.guard, "staged", observation); err == nil || changed {
				t.Fatalf("invalid evidence = %t, %v", changed, err)
			}
			attempt, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
			if err != nil || attempt.Phase != "staged" {
				t.Fatalf("failed advance mutated attempt: %+v, %v", attempt, err)
			}
		})
	}
}

func TestPublicationStageVersion1AcceptsIndependentAssetEvidencePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newPublicationExecutionFixture(t)
	repo := NewSetupManagerRepository(f.db)
	if outcome, err := repo.ClaimPublicationDelivery(ctx, f.guard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim = %q, %v", outcome, err)
	}
	base := newValidPublicationStageObservation()
	assets, err := json.Marshal(PublicationAssetOutcomes{
		Version: PublicationAssetOutcomesVersion,
		Cover:   PublicationCoverOutcome{State: PublicationAssetAbsent},
		Lyrics:  PublicationLyricsOutcome{State: PublicationAssetAbsent},
	})
	if err != nil {
		t.Fatal(err)
	}
	observation := base
	observation.ActualMetadata = json.RawMessage(`{"version":1,"tags":{"title":["Track"],"comment":[]}}`)
	observation.ActualAssets = assets
	observation.QualityPolicyVersion = publication.QualityPolicyVersion
	size, mtime := int64(10), int64(20)
	observation.Ownership = ArtifactOwnership{ExpectedSize: &size, ExpectedMTime: &mtime}
	recordValidInitialLyricsOutcome(t, f, repo, observation)
	if changed, err := repo.AdvancePublicationStage(ctx, f.guard, "preparing", observation); err != nil || !changed {
		t.Fatalf("preparing -> staged = %t, %v", changed, err)
	}
	if changed, err := repo.AdvancePublicationStage(ctx, f.guard, "staged", observation); err != nil || !changed {
		t.Fatalf("version 1 asset evidence = %t, %v", changed, err)
	}
	attempt, err := repo.ReadPublicationAttempt(ctx, f.guard.OperationID)
	if err != nil || attempt.Phase != "validated" {
		t.Fatalf("attempt = %+v, %v", attempt, err)
	}
}
