//go:build integration

package persistence_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestCollectionFoundationLinksCreditsAndTrackSourcesWithPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()

	artist := uuid.New()
	localTrack := uuid.New()
	providerA, providerB := uuid.New(), uuid.New()
	providerArtistA, providerArtistB := uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO local_artist(id,name) VALUES (?, 'Artist')`, artist); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?, 'Track')`, localTrack); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO track_artist(track_id,position,artist_id) VALUES (?,0,?),(?,1,?)`, localTrack, artist, localTrack, artist); err != nil {
		t.Fatalf("insert repeated ordered credits: %v", err)
	}
	for index, id := range []uuid.UUID{providerA, providerB} {
		if _, err := db.ExecContext(ctx, `INSERT INTO provider(id,code) VALUES (?,?)`, id, []string{"provider-a", "provider-b"}[index]); err != nil {
			t.Fatal(err)
		}
	}
	for index, item := range []struct{ provider, artist uuid.UUID }{{providerA, providerArtistA}, {providerB, providerArtistB}} {
		if _, err := db.ExecContext(ctx, `INSERT INTO provider_artist(id,provider_id,provider_key,name) VALUES (?,?,?,?)`, item.artist, item.provider, []string{"a", "b"}[index], "Artist"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO provider_local_artist(provider_id,provider_artist_id,local_artist_id,method) VALUES (?,?,?,'manual')`, item.provider, item.artist, artist); err != nil {
			t.Fatalf("map provider artist within provider: %v", err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO provider_local_artist(provider_id,provider_artist_id,local_artist_id,method) VALUES (?,? ,?,'manual')`, providerA, providerArtistB, artist); err == nil {
		t.Fatal("provider identity link accepted another provider's artist ID")
	}
	automaticPolicy := "matching-go-v1"
	repository := persistence.NewCollectionRepository(db)
	if err := repository.UpsertProviderIdentityLink(ctx, "artist", persistence.ProviderIdentityLink{
		ProviderID: providerA, ProviderEntityID: providerArtistA, LocalEntityID: artist,
		Method: "automatic", PolicyVersion: &automaticPolicy,
	}); err == nil {
		t.Fatal("automatic provider mapping replaced manual mapping")
	}
	var credits int
	if err := db.NewRaw(`SELECT count(*) FROM track_artist WHERE track_id=? AND artist_id=?`, localTrack, artist).Scan(ctx, &credits); err != nil || credits != 2 {
		t.Fatalf("repeated ordered credits = %d, %v; want 2", credits, err)
	}

	variantWithoutSHA, variantWithSHA := uuid.New(), uuid.New()
	// A variant is only an identity here: the digestless row keeps every probe
	// column NULL, which media_variant_probe_group_all_or_none accepts, and no
	// invented ffprobe result is needed to reference it from a decision.
	if _, err := db.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes) VALUES (?,123)`, variantWithoutSHA); err != nil {
		t.Fatal(err)
	}
	digest := make([]byte, 32)
	digest[0] = 0x42
	if _, err := db.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes,source_sha256,sha256_calculated_at,sha256_algorithm,sha256_applied_operation_id) VALUES (?,123,?,now(),'SHA-256',?)`, variantWithSHA, digest, uuid.New()); err != nil {
		t.Fatalf("insert SHA-enabled media variant: %v", err)
	}
	if err := repository.UpsertTrackSourceDecision(ctx, persistence.TrackSourceDecision{
		TrackID: localTrack, MediaVariantID: variantWithoutSHA, Method: "manual", Compatible: true,
		Metadata: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("associate digestless variant: %v", err)
	}
	secondTrack := uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?,'Second track')`, secondTrack); err != nil {
		t.Fatal(err)
	}
	if err := repository.UpsertTrackSourceDecision(ctx, persistence.TrackSourceDecision{
		TrackID: secondTrack, MediaVariantID: variantWithoutSHA, Method: "manual", Compatible: true,
		Metadata: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("associate one variant to multiple tracks: %v", err)
	}
	if err := repository.UpsertTrackSourceDecision(ctx, persistence.TrackSourceDecision{
		TrackID: localTrack, MediaVariantID: variantWithoutSHA, Method: "automatic", PolicyVersion: &automaticPolicy,
		Selected: true, Compatible: true, Metadata: json.RawMessage(`{}`),
	}); err == nil {
		t.Fatal("automatic update replaced manual decision")
	}
	if err := repository.RemoveTrackSourceDecision(ctx, localTrack, variantWithoutSHA, "automatic"); err == nil {
		t.Fatal("automatic removal deleted manual decision")
	}
	if err := repository.UpsertTrackSourceDecision(ctx, persistence.TrackSourceDecision{
		TrackID: localTrack, MediaVariantID: variantWithSHA, Method: "manual", Compatible: true,
		Metadata: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("associate SHA-enabled variant: %v", err)
	}
	var selected int
	if err := db.NewRaw(`SELECT count(*) FROM track_source WHERE track_id=? AND selected`, localTrack).Scan(ctx, &selected); err != nil || selected != 0 {
		t.Fatalf("new associations selected by default = %d, %v; want none", selected, err)
	}
	if err := repository.UpsertTrackSourceDecision(ctx, persistence.TrackSourceDecision{
		TrackID: localTrack, MediaVariantID: variantWithSHA, Method: "manual", Selected: true, Compatible: true,
		Metadata: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := repository.UpsertTrackSourceDecision(ctx, persistence.TrackSourceDecision{
		TrackID: localTrack, MediaVariantID: variantWithoutSHA, Method: "manual", Selected: true, Compatible: true,
		Metadata: json.RawMessage(`{}`),
	}); err == nil {
		t.Fatal("multiple selected sources for one track succeeded")
	}
	if err := db.NewRaw(`SELECT count(*) FROM track_source WHERE track_id=? AND selected`, localTrack).Scan(ctx, &selected); err != nil || selected != 1 {
		t.Fatalf("selected source after failed replacement = %d, %v; want prior selection retained", selected, err)
	}
}

type collectionProviderFixtures struct {
	manualProvider    uuid.UUID
	automaticProvider uuid.UUID

	localArtistFirst, localArtistSecond       uuid.UUID
	localReleaseFirst, localReleaseSecond     uuid.UUID
	localRecordingFirst, localRecordingSecond uuid.UUID
	localTrackFirst, localTrackSecond         uuid.UUID

	manualArtist, manualRelease, manualRecording, manualTrack             uuid.UUID
	automaticArtist, automaticRelease, automaticRecording, automaticTrack uuid.UUID

	mediaVariant uuid.UUID
}

// seedCollectionProviderFixtures materializes one provider entity of every
// supported kind for two providers, plus two local rows of every local kind.
// Manual decisions use manualProvider; automatic decisions use
// automaticProvider, so local rows may be reused without tripping the
// per-provider local uniqueness constraint.
func seedCollectionProviderFixtures(t *testing.T, db *bun.DB) collectionProviderFixtures {
	t.Helper()
	ctx := context.Background()
	fixtures := collectionProviderFixtures{
		manualProvider:       uuid.New(),
		automaticProvider:    uuid.New(),
		localArtistFirst:     uuid.New(),
		localArtistSecond:    uuid.New(),
		localReleaseFirst:    uuid.New(),
		localReleaseSecond:   uuid.New(),
		localRecordingFirst:  uuid.New(),
		localRecordingSecond: uuid.New(),
		localTrackFirst:      uuid.New(),
		localTrackSecond:     uuid.New(),
		manualArtist:         uuid.New(),
		manualRelease:        uuid.New(),
		manualRecording:      uuid.New(),
		manualTrack:          uuid.New(),
		automaticArtist:      uuid.New(),
		automaticRelease:     uuid.New(),
		automaticRecording:   uuid.New(),
		automaticTrack:       uuid.New(),
		mediaVariant:         uuid.New(),
	}
	for index, provider := range []uuid.UUID{fixtures.manualProvider, fixtures.automaticProvider} {
		if _, err := db.ExecContext(ctx, `INSERT INTO provider(id,code) VALUES (?,?)`, provider, []string{"provider-manual", "provider-automatic"}[index]); err != nil {
			t.Fatalf("insert provider: %v", err)
		}
	}
	for index, id := range []uuid.UUID{fixtures.localArtistFirst, fixtures.localArtistSecond} {
		if _, err := db.ExecContext(ctx, `INSERT INTO local_artist(id,name) VALUES (?,?)`, id, []string{"First artist", "Second artist"}[index]); err != nil {
			t.Fatalf("insert local artist: %v", err)
		}
	}
	for index, id := range []uuid.UUID{fixtures.localReleaseFirst, fixtures.localReleaseSecond} {
		if _, err := db.ExecContext(ctx, `INSERT INTO local_release(id,title) VALUES (?,?)`, id, []string{"First release", "Second release"}[index]); err != nil {
			t.Fatalf("insert local release: %v", err)
		}
	}
	for index, id := range []uuid.UUID{fixtures.localRecordingFirst, fixtures.localRecordingSecond} {
		if _, err := db.ExecContext(ctx, `INSERT INTO local_recording(id,title) VALUES (?,?)`, id, []string{"First recording", "Second recording"}[index]); err != nil {
			t.Fatalf("insert local recording: %v", err)
		}
	}
	for index, id := range []uuid.UUID{fixtures.localTrackFirst, fixtures.localTrackSecond} {
		if _, err := db.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?,?)`, id, []string{"First track", "Second track"}[index]); err != nil {
			t.Fatalf("insert local track: %v", err)
		}
	}
	for _, entities := range []struct {
		provider  uuid.UUID
		artist    uuid.UUID
		release   uuid.UUID
		recording uuid.UUID
		track     uuid.UUID
	}{
		{fixtures.manualProvider, fixtures.manualArtist, fixtures.manualRelease, fixtures.manualRecording, fixtures.manualTrack},
		{fixtures.automaticProvider, fixtures.automaticArtist, fixtures.automaticRelease, fixtures.automaticRecording, fixtures.automaticTrack},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO provider_artist(id,provider_id,provider_key,name) VALUES (?,?,?, 'Artist')`, entities.artist, entities.provider, uuid.NewString()); err != nil {
			t.Fatalf("insert provider artist: %v", err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO provider_release(id,provider_id,provider_key,title) VALUES (?,?,?, 'Release')`, entities.release, entities.provider, uuid.NewString()); err != nil {
			t.Fatalf("insert provider release: %v", err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO provider_recording(id,provider_id,provider_key,title) VALUES (?,?,?, 'Recording')`, entities.recording, entities.provider, uuid.NewString()); err != nil {
			t.Fatalf("insert provider recording: %v", err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO provider_release_track(id,provider_id,release_id,recording_id,medium,position,displayed_number) VALUES (?,?,?,?,1,1,'1')`, entities.track, entities.provider, entities.release, entities.recording); err != nil {
			t.Fatalf("insert provider release track: %v", err)
		}
	}
	// Identity only: every probe column stays NULL, so the all-or-none probe
	// group holds without fabricating an ffprobe result for a decision fixture.
	if _, err := db.ExecContext(ctx, `INSERT INTO media_variant(id,size_bytes) VALUES (?,123)`, fixtures.mediaVariant); err != nil {
		t.Fatalf("insert media variant: %v", err)
	}
	return fixtures
}

// TestCollectionFoundationProviderIdentityLinkWritesWithPostgreSQL exercises the
// successful insert and conflict-update paths for every supported entity kind,
// which the manual-replacement rejection case never reaches.
func TestCollectionFoundationProviderIdentityLinkWritesWithPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	fixtures := seedCollectionProviderFixtures(t, db)
	repository := persistence.NewCollectionRepository(db)
	policy := "matching-go-v1"

	manual := []struct {
		kind          string
		providerID    uuid.UUID
		first, second uuid.UUID
	}{
		{"artist", fixtures.manualArtist, fixtures.localArtistFirst, fixtures.localArtistSecond},
		{"release", fixtures.manualRelease, fixtures.localReleaseFirst, fixtures.localReleaseSecond},
		{"recording", fixtures.manualRecording, fixtures.localRecordingFirst, fixtures.localRecordingSecond},
		{"track", fixtures.manualTrack, fixtures.localTrackFirst, fixtures.localTrackSecond},
	}
	for _, testCase := range manual {
		if err := repository.UpsertProviderIdentityLink(ctx, testCase.kind, persistence.ProviderIdentityLink{
			ProviderID: fixtures.manualProvider, ProviderEntityID: testCase.providerID, LocalEntityID: testCase.first, Method: "manual",
		}); err != nil {
			t.Fatalf("%s: insert manual provider link: %v", testCase.kind, err)
		}
		if err := repository.UpsertProviderIdentityLink(ctx, testCase.kind, persistence.ProviderIdentityLink{
			ProviderID: fixtures.manualProvider, ProviderEntityID: testCase.providerID, LocalEntityID: testCase.second, Method: "manual",
		}); err != nil {
			t.Fatalf("%s: update manual provider link: %v", testCase.kind, err)
		}
	}

	automatic := []struct {
		kind          string
		providerID    uuid.UUID
		first, second uuid.UUID
	}{
		{"artist", fixtures.automaticArtist, fixtures.localArtistFirst, fixtures.localArtistSecond},
		{"release", fixtures.automaticRelease, fixtures.localReleaseFirst, fixtures.localReleaseSecond},
		{"recording", fixtures.automaticRecording, fixtures.localRecordingFirst, fixtures.localRecordingSecond},
		{"track", fixtures.automaticTrack, fixtures.localTrackFirst, fixtures.localTrackSecond},
	}
	for _, testCase := range automatic {
		if err := repository.UpsertProviderIdentityLink(ctx, testCase.kind, persistence.ProviderIdentityLink{
			ProviderID: fixtures.automaticProvider, ProviderEntityID: testCase.providerID, LocalEntityID: testCase.first, Method: "automatic", PolicyVersion: &policy,
		}); err != nil {
			t.Fatalf("%s: insert automatic provider link: %v", testCase.kind, err)
		}
		if err := repository.UpsertProviderIdentityLink(ctx, testCase.kind, persistence.ProviderIdentityLink{
			ProviderID: fixtures.automaticProvider, ProviderEntityID: testCase.providerID, LocalEntityID: testCase.second, Method: "automatic", PolicyVersion: &policy,
		}); err != nil {
			t.Fatalf("%s: update automatic provider link: %v", testCase.kind, err)
		}
	}
}

// TestCollectionFoundationDecisionConstraintsWithPostgreSQL inserts rows directly
// to prove the database, not only the repository, enforces the decision policy
// invariant: an automatic decision must carry the policy version and a manual
// decision must not carry one. A NULL policy version must not slip past the
// automatic branch of the CHECK.
func TestCollectionFoundationDecisionConstraintsWithPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	fixtures := seedCollectionProviderFixtures(t, db)

	automaticWithoutPolicy := []struct {
		table string
		query string
		args  []any
	}{
		{"provider_local_artist", `INSERT INTO provider_local_artist(provider_id,provider_artist_id,local_artist_id,method,policy_version) VALUES (?,?,?,'automatic',NULL)`, []any{fixtures.manualProvider, fixtures.manualArtist, fixtures.localArtistFirst}},
		{"provider_local_release", `INSERT INTO provider_local_release(provider_id,provider_release_id,local_release_id,method,policy_version) VALUES (?,?,?,'automatic',NULL)`, []any{fixtures.manualProvider, fixtures.manualRelease, fixtures.localReleaseFirst}},
		{"provider_local_recording", `INSERT INTO provider_local_recording(provider_id,provider_recording_id,local_recording_id,method,policy_version) VALUES (?,?,?,'automatic',NULL)`, []any{fixtures.manualProvider, fixtures.manualRecording, fixtures.localRecordingFirst}},
		{"provider_local_track", `INSERT INTO provider_local_track(provider_id,provider_track_id,local_track_id,method,policy_version) VALUES (?,?,?,'automatic',NULL)`, []any{fixtures.manualProvider, fixtures.manualTrack, fixtures.localTrackFirst}},
		{"track_source", `INSERT INTO track_source(id,track_id,media_variant_id,decision_method,policy_version) VALUES (?,?,?,'automatic',NULL)`, []any{uuid.New(), fixtures.localTrackFirst, fixtures.mediaVariant}},
	}
	for _, testCase := range automaticWithoutPolicy {
		if _, err := db.ExecContext(ctx, testCase.query, testCase.args...); err == nil {
			t.Fatalf("%s accepted an automatic decision with no policy version", testCase.table)
		}
	}

	manualWithPolicy := []struct {
		table string
		query string
		args  []any
	}{
		{"provider_local_artist", `INSERT INTO provider_local_artist(provider_id,provider_artist_id,local_artist_id,method,policy_version) VALUES (?,?,?,'manual','matching-go-v1')`, []any{fixtures.manualProvider, fixtures.manualArtist, fixtures.localArtistFirst}},
		{"provider_local_release", `INSERT INTO provider_local_release(provider_id,provider_release_id,local_release_id,method,policy_version) VALUES (?,?,?,'manual','matching-go-v1')`, []any{fixtures.manualProvider, fixtures.manualRelease, fixtures.localReleaseFirst}},
		{"provider_local_recording", `INSERT INTO provider_local_recording(provider_id,provider_recording_id,local_recording_id,method,policy_version) VALUES (?,?,?,'manual','matching-go-v1')`, []any{fixtures.manualProvider, fixtures.manualRecording, fixtures.localRecordingFirst}},
		{"provider_local_track", `INSERT INTO provider_local_track(provider_id,provider_track_id,local_track_id,method,policy_version) VALUES (?,?,?,'manual','matching-go-v1')`, []any{fixtures.manualProvider, fixtures.manualTrack, fixtures.localTrackFirst}},
		{"track_source", `INSERT INTO track_source(id,track_id,media_variant_id,decision_method,policy_version) VALUES (?,?,?,'manual','matching-go-v1')`, []any{uuid.New(), fixtures.localTrackFirst, fixtures.mediaVariant}},
	}
	for _, testCase := range manualWithPolicy {
		if _, err := db.ExecContext(ctx, testCase.query, testCase.args...); err == nil {
			t.Fatalf("%s accepted a manual decision with a policy version", testCase.table)
		}
	}
}

// TestCollectionFoundationPolicyVersionIsOpaqueWithPostgreSQL proves the
// decision policy version is an opaque, caller-owned algorithm token rather
// than a fixed constant: the schema and repository accept any non-blank text
// (not only the currently shipped matching-go-v1 value), a manual decision
// carries no version, and a blank or whitespace-only version is still rejected.
func TestCollectionFoundationPolicyVersionIsOpaqueWithPostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)
	ctx := context.Background()
	fixtures := seedCollectionProviderFixtures(t, db)

	manual := []struct {
		table string
		query string
		args  []any
	}{
		{"provider_local_artist", `INSERT INTO provider_local_artist(provider_id,provider_artist_id,local_artist_id,method) VALUES (?,?,?,'manual')`, []any{fixtures.manualProvider, fixtures.manualArtist, fixtures.localArtistFirst}},
		{"provider_local_release", `INSERT INTO provider_local_release(provider_id,provider_release_id,local_release_id,method) VALUES (?,?,?,'manual')`, []any{fixtures.manualProvider, fixtures.manualRelease, fixtures.localReleaseFirst}},
		{"provider_local_recording", `INSERT INTO provider_local_recording(provider_id,provider_recording_id,local_recording_id,method) VALUES (?,?,?,'manual')`, []any{fixtures.manualProvider, fixtures.manualRecording, fixtures.localRecordingFirst}},
		{"provider_local_track", `INSERT INTO provider_local_track(provider_id,provider_track_id,local_track_id,method) VALUES (?,?,?,'manual')`, []any{fixtures.manualProvider, fixtures.manualTrack, fixtures.localTrackFirst}},
		{"track_source", `INSERT INTO track_source(id,track_id,media_variant_id,decision_method) VALUES (?,?,?,'manual')`, []any{uuid.New(), fixtures.localTrackFirst, fixtures.mediaVariant}},
	}
	for _, testCase := range manual {
		if _, err := db.ExecContext(ctx, testCase.query, testCase.args...); err != nil {
			t.Fatalf("%s rejected a manual decision with no policy version: %v", testCase.table, err)
		}
	}

	// A version no shipped algorithm emits must still persist: the field records
	// which algorithm produced the decision, not a fixed constant.
	policy := "matching-go-v2-test"
	automatic := []struct {
		table string
		query string
		args  []any
	}{
		{"provider_local_artist", `INSERT INTO provider_local_artist(provider_id,provider_artist_id,local_artist_id,method,policy_version) VALUES (?,?,?,'automatic',?)`, []any{fixtures.automaticProvider, fixtures.automaticArtist, fixtures.localArtistFirst, policy}},
		{"provider_local_release", `INSERT INTO provider_local_release(provider_id,provider_release_id,local_release_id,method,policy_version) VALUES (?,?,?,'automatic',?)`, []any{fixtures.automaticProvider, fixtures.automaticRelease, fixtures.localReleaseFirst, policy}},
		{"provider_local_recording", `INSERT INTO provider_local_recording(provider_id,provider_recording_id,local_recording_id,method,policy_version) VALUES (?,?,?,'automatic',?)`, []any{fixtures.automaticProvider, fixtures.automaticRecording, fixtures.localRecordingFirst, policy}},
		{"provider_local_track", `INSERT INTO provider_local_track(provider_id,provider_track_id,local_track_id,method,policy_version) VALUES (?,?,?,'automatic',?)`, []any{fixtures.automaticProvider, fixtures.automaticTrack, fixtures.localTrackFirst, policy}},
		{"track_source", `INSERT INTO track_source(id,track_id,media_variant_id,decision_method,policy_version) VALUES (?,?,?,'automatic',?)`, []any{uuid.New(), fixtures.localTrackSecond, fixtures.mediaVariant, policy}},
	}
	for _, testCase := range automatic {
		if _, err := db.ExecContext(ctx, testCase.query, testCase.args...); err != nil {
			t.Fatalf("%s rejected automatic policy version %q: %v", testCase.table, policy, err)
		}
	}

	// The repository must accept the same opaque version and reject blank text.
	repository := persistence.NewCollectionRepository(db)
	if err := repository.UpsertProviderIdentityLink(ctx, "artist", persistence.ProviderIdentityLink{
		ProviderID: fixtures.automaticProvider, ProviderEntityID: fixtures.automaticArtist, LocalEntityID: fixtures.localArtistSecond,
		Method: "automatic", PolicyVersion: &policy,
	}); err != nil {
		t.Fatalf("repository rejected automatic policy version %q: %v", policy, err)
	}
	blank := ""
	if err := repository.UpsertProviderIdentityLink(ctx, "artist", persistence.ProviderIdentityLink{
		ProviderID: fixtures.automaticProvider, ProviderEntityID: fixtures.automaticArtist, LocalEntityID: fixtures.localArtistSecond,
		Method: "automatic", PolicyVersion: &blank,
	}); err == nil {
		t.Fatal("repository accepted a blank automatic policy version")
	}

	// Blank and whitespace-only versions remain invalid at the schema level. A
	// fresh provider keeps the rejection from being mistaken for a primary-key
	// collision with the accepted rows above.
	blankProvider := uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO provider(id,code) VALUES (?,'provider-blank')`, blankProvider); err != nil {
		t.Fatal(err)
	}
	blankArtist, blankRelease, blankRecording, blankTrack := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO provider_artist(id,provider_id,provider_key,name) VALUES (?,?,?,'Artist')`, blankArtist, blankProvider, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO provider_release(id,provider_id,provider_key,title) VALUES (?,?,?,'Release')`, blankRelease, blankProvider, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO provider_recording(id,provider_id,provider_key,title) VALUES (?,?,?,'Recording')`, blankRecording, blankProvider, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO provider_release_track(id,provider_id,release_id,recording_id,medium,position,displayed_number) VALUES (?,?,?,?,1,1,'1')`, blankTrack, blankProvider, blankRelease, blankRecording); err != nil {
		t.Fatal(err)
	}
	blankTrackSource := uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?,'Blank source track')`, blankTrackSource); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"", "   ", "\t\n", "\u00a0", "\u2003"} {
		blankRows := []struct {
			table string
			query string
			args  []any
		}{
			{"provider_local_artist", `INSERT INTO provider_local_artist(provider_id,provider_artist_id,local_artist_id,method,policy_version) VALUES (?,?,?,'automatic',?)`, []any{blankProvider, blankArtist, fixtures.localArtistFirst, version}},
			{"provider_local_release", `INSERT INTO provider_local_release(provider_id,provider_release_id,local_release_id,method,policy_version) VALUES (?,?,?,'automatic',?)`, []any{blankProvider, blankRelease, fixtures.localReleaseFirst, version}},
			{"provider_local_recording", `INSERT INTO provider_local_recording(provider_id,provider_recording_id,local_recording_id,method,policy_version) VALUES (?,?,?,'automatic',?)`, []any{blankProvider, blankRecording, fixtures.localRecordingFirst, version}},
			{"provider_local_track", `INSERT INTO provider_local_track(provider_id,provider_track_id,local_track_id,method,policy_version) VALUES (?,?,?,'automatic',?)`, []any{blankProvider, blankTrack, fixtures.localTrackFirst, version}},
			{"track_source", `INSERT INTO track_source(id,track_id,media_variant_id,decision_method,policy_version) VALUES (?,?,?,'automatic',?)`, []any{uuid.New(), blankTrackSource, fixtures.mediaVariant, version}},
		}
		for _, testCase := range blankRows {
			if _, err := db.ExecContext(ctx, testCase.query, testCase.args...); err == nil {
				t.Fatalf("%s accepted a blank automatic policy version %q", testCase.table, version)
			}
		}
	}
}
