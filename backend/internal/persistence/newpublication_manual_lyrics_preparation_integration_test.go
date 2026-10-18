//go:build integration

package persistence

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/uptrace/bun"
)

func preparingManualLyricsFLACFixture(t *testing.T) (publicationExecutionFixture, *SetupManagerRepository, PublicationInputs) {
	t.Helper()
	ctx := context.Background()
	f, repo, input, _, _ := prepareManualLyricsCommitFixture(t)
	input.Version = PublicationInputsVersion3
	input.QualityPolicyVersion = publication.QualityPolicyVersion
	input.Format = publication.AudioFormatSource
	input.ManualLyrics = &PublicationManualLyricsInput{
		ExpectedMetadataRevision:   7,
		ConfirmedCurrentGeneration: input.Rewrite.CurrentGeneration,
		State:                      "explicit_empty",
	}
	input.Rewrite.AudioFacts.Container = publication.AudioContainerFLAC
	input.Rewrite.AudioFacts.Codec = "flac"
	input.Rewrite.AudioFacts.SupportedSourceContainer = true
	if err := input.Validate(); err != nil {
		t.Fatalf("FLAC manual-lyrics input is invalid: %v", err)
	}
	currentAudio := mustJSON(PublicationCurrentAudio{Version: 1, Facts: input.Rewrite.AudioFacts})
	if err := f.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE track_publication_attempt SET phase='preparing', mutation_journal='[]'::jsonb, frozen_inputs=?::jsonb WHERE operation_id=?`, string(mustJSON(input)), f.guard.OperationID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE operation SET stage='preparing' WHERE id=?`, f.guard.OperationID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE track_publication SET actual_audio=?::jsonb WHERE position_id=?`, string(currentAudio), f.guard.ExpectedPositionID); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return f, repo, input
}

// This exercises the private V3 capability through the PostgreSQL delivery
// guard, rather than treating a recognized input version as authorization.
func TestPublicationManualLyricsPreparationInputsExplicitV3GatePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, _ := preparingManualLyricsFLACFixture(t)
	if _, ok, err := repo.PublicationPreparationInputs(ctx, f.guard); err == nil || ok {
		t.Fatalf("generic preparation accepted V3: ok=%v err=%v", ok, err)
	}
	// The shared fixture is a positive control: the private read capability
	// accepts the complete preparing state before testing its refusal cases.
	if _, ok, err := repo.PublicationManualLyricsPreparationInputs(ctx, f.guard); err != nil || !ok {
		t.Fatalf("manual preparation positive control = ok:%v err:%v", ok, err)
	}
	var phase, operationStage string
	if err := f.db.NewRaw(`SELECT phase FROM track_publication_attempt WHERE operation_id=?`, f.guard.OperationID).Scan(ctx, &phase); err != nil {
		t.Fatal(err)
	}
	if err := f.db.NewRaw(`SELECT stage FROM operation WHERE id=?`, f.guard.OperationID).Scan(ctx, &operationStage); err != nil {
		t.Fatal(err)
	}
	if phase != "preparing" || operationStage != "preparing" {
		t.Fatalf("reader mutated state: phase=%s stage=%s", phase, operationStage)
	}
}

func TestPublicationManualLyricsPreparationInputsAcceptsCurrentFLACRewritePostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repo, _ := preparingManualLyricsFLACFixture(t)

	got, ok, err := repo.PublicationManualLyricsPreparationInputs(ctx, f.guard)
	if err != nil || !ok {
		t.Fatalf("manual preparation = ok:%v err:%v", ok, err)
	}
	if got.Version != PublicationInputsVersion3 || got.Rewrite == nil || got.Rewrite.AudioFacts.Container != publication.AudioContainerFLAC {
		t.Fatalf("manual preparation returned unexpected inputs: %+v", got)
	}
	// Returned snapshots must not alias the decoded/frozen input's mutable maps.
	got.DesiredTags["TITLE"][0] = "mutated"
	again, ok, err := repo.PublicationManualLyricsPreparationInputs(ctx, f.guard)
	if err != nil || !ok || again.DesiredTags["TITLE"][0] == "mutated" {
		t.Fatalf("manual preparation result was not detached: ok:%v err:%v tags:%v", ok, err, again.DesiredTags)
	}
	if _, ok, err := repo.PublicationPreparationInputs(ctx, f.guard); err == nil || ok {
		t.Fatalf("generic reader accepted V3 manual lyrics: ok:%v err:%v", ok, err)
	}
}

func TestPublicationManualLyricsPreparationInputsRejectsStaleCurrentAndMissingReservationPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		mutate func(*publicationExecutionFixture) error
	}{
		{
			name: "stale current generation",
			mutate: func(f *publicationExecutionFixture) error {
				_, err := f.db.ExecContext(ctx, `UPDATE track_publication SET generation=? WHERE position_id=?`, uuid.New(), f.guard.ExpectedPositionID)
				return err
			},
		},
		{
			name: "missing candidate namespace reservation",
			mutate: func(f *publicationExecutionFixture) error {
				_, err := f.db.ExecContext(ctx, `DELETE FROM publication_namespace_reservation WHERE generation=?`, f.guard.CandidateGeneration)
				return err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, repo, _ := preparingManualLyricsFLACFixture(t)
			if err := tc.mutate(&f); err != nil {
				t.Fatal(err)
			}
			if _, ok, err := repo.PublicationManualLyricsPreparationInputs(ctx, f.guard); err != nil || ok {
				t.Fatalf("manual preparation accepted invalid current state: ok:%v err:%v", ok, err)
			}
		})
	}
}
