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
	f, repo, input, _ := prepareManualLyricsCommitFixture(t)
	input.Version = PublicationInputsVersion
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
		if _, err := tx.ExecContext(ctx, `UPDATE track_publication_attempt SET phase='preparing', frozen_inputs=?::jsonb WHERE operation_id=?`, string(mustJSON(input)), f.guard.OperationID); err != nil {
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
	f, repo, input := preparingManualLyricsFLACFixture(t)
	readManual := func() (PublicationInputs, bool) {
		t.Helper()
		got, ok, err := repo.PublicationManualLyricsPreparationInputs(ctx, f.guard)
		if err != nil {
			t.Fatalf("manual preparation: %v", err)
		}
		return got, ok
	}
	assertManualAccepted := func(label string) PublicationInputs {
		t.Helper()
		got, ok := readManual()
		if !ok {
			t.Fatalf("%s: valid manual preparation was rejected", label)
		}
		return got
	}
	assertManualRejected := func(label string) {
		t.Helper()
		if _, ok := readManual(); ok {
			t.Fatalf("%s: manual preparation accepted invalid current state", label)
		}
	}
	assertCurrentState := func(label string) {
		t.Helper()
		if _, ok, err := repo.PublicationPreparationInputs(ctx, f.guard); err == nil || ok {
			t.Fatalf("%s: generic preparation accepted V3: ok=%v err=%v", label, ok, err)
		}
		got := assertManualAccepted(label)
		if got.Version != PublicationInputsVersion || got.Rewrite == nil || got.Rewrite.AudioFacts.Container != publication.AudioContainerFLAC {
			t.Fatalf("%s: manual preparation returned unexpected inputs: %+v", label, got)
		}
	}

	// A single fixture carries all positive and negative controls so each drift
	// is tested against an accepted baseline and then restored before the next.
	assertCurrentState("initial baseline")

	currentOwnership := mustJSON(PublicationCurrentOwnership{Version: 1, Identity: input.Rewrite.Ownership})
	currentAudio := mustJSON(PublicationCurrentAudio{Version: 1, Facts: input.Rewrite.AudioFacts})
	restore := func(query string, args ...any) {
		t.Helper()
		if _, err := f.db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, drift := range []struct {
		name    string
		mutate  func() error
		restore func()
	}{
		{
			name: "stale current generation",
			mutate: func() error {
				_, err := f.db.ExecContext(ctx, `UPDATE track_publication SET generation=? WHERE position_id=?`, uuid.New(), f.guard.ExpectedPositionID)
				return err
			},
			restore: func() {
				restore(`UPDATE track_publication SET generation=? WHERE position_id=?`, input.Rewrite.CurrentGeneration, f.guard.ExpectedPositionID)
			},
		},
		{
			name: "same-generation ownership drift",
			mutate: func() error {
				_, err := f.db.ExecContext(ctx, `UPDATE track_publication SET ownership=jsonb_set(ownership,'{identity,size_bytes}',to_jsonb((ownership #>> '{identity,size_bytes}')::bigint+1)) WHERE position_id=?`, f.guard.ExpectedPositionID)
				return err
			},
			restore: func() {
				restore(`UPDATE track_publication SET ownership=?::jsonb WHERE position_id=?`, string(currentOwnership), f.guard.ExpectedPositionID)
			},
		},
		{
			name: "same-generation audio facts drift",
			mutate: func() error {
				_, err := f.db.ExecContext(ctx, `UPDATE track_publication SET actual_audio=jsonb_set(actual_audio,'{facts,stream_index}',to_jsonb((actual_audio #>> '{facts,stream_index}')::int+1)) WHERE position_id=?`, f.guard.ExpectedPositionID)
				return err
			},
			restore: func() {
				restore(`UPDATE track_publication SET actual_audio=?::jsonb WHERE position_id=?`, string(currentAudio), f.guard.ExpectedPositionID)
			},
		},
	} {
		assertCurrentState(drift.name + " baseline")
		if err := drift.mutate(); err != nil {
			t.Fatalf("%s: mutate current state: %v", drift.name, err)
		}
		assertManualRejected(drift.name)
		drift.restore()
		assertCurrentState(drift.name + " restored baseline")
	}

	// This is intentionally last: unlike the current-state drifts above, the
	// missing reservation is not restored within this fixture.
	assertCurrentState("reservation baseline")
	if _, err := f.db.ExecContext(ctx, `DELETE FROM publication_namespace_reservation WHERE generation=?`, f.guard.CandidateGeneration); err != nil {
		t.Fatal(err)
	}
	assertManualRejected("missing candidate namespace reservation")

	if _, ok, err := repo.PublicationPreparationInputs(ctx, f.guard); err == nil || ok {
		t.Fatalf("generic preparation accepted V3 after rejection checks: ok=%v err=%v", ok, err)
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
