//go:build integration

package persistence_test

import (
	"context"

	"fmt"
	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"

	"testing"
)

func TestApplyCurrentMatchingEvaluationRollsBackIntermediateWritesPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigratedWithRiver(t)
	ctx := context.Background()
	fixture := newIncomingMatchFixture(t, ctx, db)
	repository := persistence.NewIncomingMatchCandidateRepository(db)
	evaluation := fixture.evaluation([]persistence.IncomingMatchCandidate{
		fixture.candidate(fixture.memberA, .95), fixture.candidate(fixture.memberB, .95),
	})
	evaluation.Groups[0].Evidence = matchingApplyEvidence(t, fixture, fixture.trackID)
	if err := repository.Replace(ctx, evaluation); err != nil {
		t.Fatalf("persist unique matching evaluation: %v", err)
	}
	header, candidates := mustIncomingMatchResult(t, ctx, repository, fixture.group.ID)
	before := matchingApplyPersistedCounts(t, ctx, db, fixture.provider)

	// Fail the second source attachment: one assignment and all of its local
	// entity/provider-link/release-track writes must already have happened.
	trigger := fmt.Sprintf("matching_apply_fail_%s", fixture.provider.String()[:8])
	counter := trigger + "_counter"
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`CREATE TABLE %s (insert_count integer NOT NULL)`, counter)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s VALUES (0)`, counter)); err != nil {
		t.Fatal(err)
	}
	function := trigger + "_function"
	functionBody := fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			UPDATE %s SET insert_count=insert_count+1;
			IF (SELECT insert_count FROM %s) = 2 THEN RAISE EXCEPTION 'injected second source attachment failure'; END IF;
			RETURN NEW;
		END $$`, function, counter, counter)
	if _, err := db.ExecContext(ctx, functionBody); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`CREATE TRIGGER %s BEFORE INSERT ON track_source FOR EACH ROW EXECUTE FUNCTION %s()`, trigger, function)); err != nil {
		t.Fatal(err)
	}
	err := repository.ApplyCurrentMatchingEvaluation(ctx, fixture.group.ID, header.EvaluationRevision)
	if err == nil {
		t.Fatal("apply succeeded despite injected second track-source insert failure")
	}
	after := matchingApplyPersistedCounts(t, ctx, db, fixture.provider)
	if before != after {
		t.Fatalf("failed apply changed persisted counts: before=%+v after=%+v", before, after)
	}
	afterHeader, afterCandidates := mustIncomingMatchResult(t, ctx, repository, fixture.group.ID)
	if afterHeader.EvaluationRevision != header.EvaluationRevision || string(afterHeader.EvaluationEvidence) != string(header.EvaluationEvidence) || len(afterCandidates) != len(candidates) {
		t.Fatalf("failed apply changed evaluation/candidates: header=%+v candidates=%+v", afterHeader, afterCandidates)
	}
	for i := range candidates {
		if candidates[i].FileMemberID != afterCandidates[i].FileMemberID || candidates[i].Score != afterCandidates[i].Score || string(candidates[i].Evidence) != string(afterCandidates[i].Evidence) {
			t.Fatalf("failed apply changed candidates: before=%+v after=%+v", candidates, afterCandidates)
		}
	}
}

type matchingApplyCounts struct {
	LocalArtists, LocalReleases, LocalRecordings, LocalTracks             int
	ProviderArtists, ProviderReleases, ProviderRecordings, ProviderTracks int
	ReleaseTracks, TrackSources                                           int
}

func matchingApplyPersistedCounts(t *testing.T, ctx context.Context, db *bun.DB, provider uuid.UUID) matchingApplyCounts {
	t.Helper()
	var counts matchingApplyCounts
	queries := []struct {
		query  string
		target *int
	}{
		{`SELECT count(*) FROM local_artist WHERE ?::uuid IS NOT NULL`, &counts.LocalArtists},
		{`SELECT count(*) FROM local_release WHERE ?::uuid IS NOT NULL`, &counts.LocalReleases},
		{`SELECT count(*) FROM local_recording WHERE ?::uuid IS NOT NULL`, &counts.LocalRecordings},
		{`SELECT count(*) FROM local_track WHERE ?::uuid IS NOT NULL`, &counts.LocalTracks},
		{`SELECT count(*) FROM provider_local_artist WHERE provider_id=?`, &counts.ProviderArtists},
		{`SELECT count(*) FROM provider_local_release WHERE provider_id=?`, &counts.ProviderReleases},
		{`SELECT count(*) FROM provider_local_recording WHERE provider_id=?`, &counts.ProviderRecordings},
		{`SELECT count(*) FROM provider_local_track WHERE provider_id=?`, &counts.ProviderTracks},
		{`SELECT count(*) FROM release_track rt JOIN provider_local_release plr ON plr.local_release_id=rt.release_id WHERE plr.provider_id=?`, &counts.ReleaseTracks},
		{`SELECT count(*) FROM track_source ts JOIN provider_local_track plt ON plt.local_track_id=ts.track_id WHERE plt.provider_id=?`, &counts.TrackSources},
	}
	for _, item := range queries {
		if err := db.NewRaw(item.query, provider).Scan(ctx, item.target); err != nil {
			t.Fatalf("count apply rows: %v", err)
		}
	}
	return counts
}
