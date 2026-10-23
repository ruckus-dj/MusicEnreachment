//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCheckPublicationResultCurrentManualLyricsPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, repository, input, _, _ := prepareManualLyricsCommitFixture(t)
	seedManualPublicationResultChoice(t, ctx, f)
	input.Format = "source"
	if err := input.Validate(); err != nil {
		t.Fatalf("manual fixture input is invalid: %v", err)
	}
	if !validManualLyricsRewrite(input) {
		t.Fatalf("manual fixture cannot execute: facts=%+v", input.Rewrite.AudioFacts)
	}
	current, err := repository.CheckPublicationResultCurrent(ctx, f.guard)
	if err != nil || !current {
		t.Fatalf("manual lyrics freshness baseline = %v, %v", current, err)
	}

	if _, err := f.db.ExecContext(ctx, `UPDATE release_track SET metadata_revision=metadata_revision+1 WHERE id=?`, f.guard.ExpectedPositionID); err != nil {
		t.Fatal(err)
	}
	current, err = repository.CheckPublicationResultCurrent(ctx, f.guard)
	if err != nil || current {
		t.Fatalf("manual lyrics stale revision = %v, %v", current, err)
	}

	// Restore the frozen revision, then change only the desired value to prove
	// freshness checks the current authority value rather than revision alone.
	if _, err := f.db.ExecContext(ctx, `UPDATE release_track SET metadata_revision=7 WHERE id=?`, f.guard.ExpectedPositionID); err != nil {
		t.Fatal(err)
	}
	changed := map[string]any{
		"local_metadata": map[string]any{
			"version": 1, "scope": "position", "desired": map[string]any{
				"synced_lyrics": map[string]any{
					"state": "value", "value": "changed", "authority": "manual",
					"provenance": map[string]any{"kind": "manual"},
				},
			},
		},
	}
	metadata, err := json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, `UPDATE release_track SET metadata=?::jsonb WHERE id=?`, string(metadata), f.guard.ExpectedPositionID); err != nil {
		t.Fatal(err)
	}
	current, err = repository.CheckPublicationResultCurrent(ctx, f.guard)
	if err != nil || current {
		t.Fatalf("manual lyrics changed desired value = %v, %v", current, err)
	}
}
