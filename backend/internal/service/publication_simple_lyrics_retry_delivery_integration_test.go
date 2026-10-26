//go:build integration

package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	riverdatabasesql "github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestPublicationExecutorAutomaticLyricsRetryDeliveryPostgreSQL(t *testing.T) {
	for _, scenario := range []struct {
		name          string
		mismatch      bool
		authorityRace bool
	}{
		{name: "found"},
		{name: "readback_mismatch", mismatch: true},
		{name: "authority_race", authorityRace: true},
	} {
		for _, authority := range []string{"manual", "automatic", "unknown"} {
			t.Run(scenario.name+"/source_authority_"+authority, func(t *testing.T) {
				testSimpleAutomaticLyricsRetryDelivery(t, scenario.mismatch, scenario.authorityRace, authority)
			})
		}
	}
}

func testSimpleAutomaticLyricsRetryDelivery(t *testing.T, mismatch, authorityRace bool, authorityMode string) {
	t.Helper()
	ctx := context.Background()
	output, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	currentPath := filepath.Join(output, "current.flac")
	contents, err := os.ReadFile("../integrations/metadata/testdata/eg.flac")
	if err != nil {
		t.Fatal(err)
	}
	contents, err = flacFixtureWithoutPictures(contents)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(currentPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	cover := &metadata.Cover{Data: simpleCoverPNG(t, color.RGBA{R: 255, A: 255}), MIME: "image/png"}
	if _, err := (metadata.Writer{}).Write(ctx, currentPath, map[string][]string{"TITLE": {"Track"}, "ARTIST": {"Artist"}}, cover); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	originalDigest := sha256.Sum256(original)
	info, err := os.Stat(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	size, mtime := info.Size(), info.ModTime().UnixMicro()
	lossless := true
	installationID := uuid.New()
	toolRelative := filepath.ToSlash(filepath.Join("ffmpeg", "7.0"))
	input := persistence.PublicationInputs{
		Version: persistence.PublicationInputsVersion, QualityPolicyVersion: publication.QualityPolicyVersion,
		IntentKind: "rewrite", Format: "source", DesiredTags: map[string][]string{"TITLE": {"Track"}, "ARTIST": {"Artist"}},
		Tools: []persistence.SourceAnalysisToolSelection{
			{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: toolRelative, Executable: "ffmpeg", Version: "7.0", VersionBanner: "ffmpeg version 7.0 build"},
			{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: toolRelative, Executable: "ffprobe", Version: "7.0", VersionBanner: "ffprobe version 7.0 build"},
		},
		Rewrite: &persistence.PublicationRewriteInput{
			CurrentGeneration: uuid.New(), RelativePath: "current.flac",
			Ownership:  persistence.PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: hex.EncodeToString(originalDigest[:])},
			AudioFacts: persistence.PersistedAudioFacts{StreamIndex: 0, Codec: "flac", Lossless: &lossless, Container: "flac", SupportedSourceContainer: true},
		},
	}
	capture, err := (metadata.Reader{}).Read(ctx, currentPath, metadata.ReadRequest{CoverInspection: true})
	if err != nil {
		t.Fatal(err)
	}
	assets, err := ObservePublicationAssets(capture)
	if err != nil {
		t.Fatal(err)
	}
	input.AutomaticLyricsRetry = &persistence.PublicationAutomaticLyricsRetryInput{
		ExpectedMetadataRevision: 7, ConfirmedCurrentGeneration: input.Rewrite.CurrentGeneration, CurrentAssets: assets,
	}
	if err := input.Validate(); err != nil {
		t.Fatalf("validate frozen retry input: %v", err)
	}
	artwork, err := metadata.SnapshotFlacArtwork(ctx, currentPath)
	if err != nil {
		t.Fatal(err)
	}
	db := testpostgres.OpenMigrated(t)
	migrator, err := rivermigrate.New(riverdatabasesql.New(db.DB), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	encode := func(value any) string {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	guard := persistence.PublicationDeliveryGuard{
		OperationID: uuid.New(), ExpectedPositionID: uuid.New(), CandidateGeneration: uuid.New(),
		ClaimUUID: uuid.New(), BootUUID: uuid.New(), DeliveryToken: uuid.NewString(),
	}
	if err := db.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &guard.Epoch); err != nil {
		t.Fatal(err)
	}
	releaseID, trackID := uuid.New(), uuid.New()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO local_release(id,title) VALUES (?,'retry fixture')`, releaseID)
	exec(`INSERT INTO local_track(id,title) VALUES (?,'retry fixture')`, trackID)
	exec(`INSERT INTO release_track(id,release_id,track_id,position,metadata,metadata_revision) VALUES (?,?,?,0,'{}'::jsonb,7)`, guard.ExpectedPositionID, releaseID, trackID)
	if err := tx.QueryRowContext(ctx, `INSERT INTO river_job(kind,args,state,attempt) VALUES ('publish_track_v1','{}','running',1) RETURNING id`).Scan(&guard.RiverJobID); err != nil {
		t.Fatal(err)
	}
	guard.RiverAttempt, guard.ApplicationAttempt = 1, 1
	snapshot := encode(map[string]any{"version": 1, "position_id": guard.ExpectedPositionID, "output_epoch": guard.Epoch, "intent_kind": "rewrite"})
	node := persistence.PublicationNamespaceNode{CanonicalKey: "current.flac", RelativePath: "current.flac", NodeKind: "file"}
	manifest := encode(map[string]any{"version": 1, "nodes": []persistence.PublicationNamespaceNode{node}})
	exec(`INSERT INTO operation(id,kind,state,stage,input_snapshot,attempt,river_job_id) VALUES (?,'publish_track_v1','queued','queued',?::jsonb,1,?)`, guard.OperationID, snapshot, guard.RiverJobID)
	exec(`INSERT INTO track_publication_attempt(operation_id,position_id,epoch,expected_generation,candidate_generation,intent_kind,phase,frozen_inputs,namespace_manifest,mutation_journal) VALUES (?,?,?, ?,?,'rewrite','queued',?::jsonb,?::jsonb,'[]')`, guard.OperationID, guard.ExpectedPositionID, guard.Epoch, input.Rewrite.CurrentGeneration, guard.CandidateGeneration, encode(input), manifest)
	exec(`INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at) VALUES (?,?,?,?,?::jsonb,?::jsonb,?::jsonb,?::jsonb,1,now())`, guard.ExpectedPositionID, guard.Epoch, input.Rewrite.CurrentGeneration, input.Rewrite.RelativePath,
		encode(persistence.PublicationCurrentOwnership{Version: 1, Identity: input.Rewrite.Ownership}),
		encode(persistence.PublicationCurrentAudio{Version: 1, Facts: input.Rewrite.AudioFacts}),
		encode(persistence.PublicationCommitMetadata{Version: 1, Tags: capture.Tags}), encode(assets))
	var seededAuthority *persistence.PositionSourceAuthority
	if authorityMode != "unknown" {
		authority := persistence.PositionSourceAuthority{PositionID: guard.ExpectedPositionID, Mode: authorityMode, Revision: uuid.New(), PublicationEpoch: guard.Epoch, PublicationGeneration: input.Rewrite.CurrentGeneration, SourceLocationID: uuid.New(), MediaVariantID: uuid.New()}
		exec(`INSERT INTO position_source_authority(position_id,mode,revision,publication_epoch,publication_generation,source_location_id,media_variant_id) VALUES (?,?,?,?,?,?,?)`, authority.PositionID, authority.Mode, authority.Revision, authority.PublicationEpoch, authority.PublicationGeneration, authority.SourceLocationID, authority.MediaVariantID)
		seededAuthority = &authority
	}
	claimID := uuid.New()
	keyDigest := sha256.Sum256([]byte(node.CanonicalKey))
	exec(`INSERT INTO publication_namespace_claim(id,epoch,key_digest,canonical_key,relative_path,node_kind,owner_position_id) VALUES (?,?,?,?,?,'file',?)`, claimID, guard.Epoch, keyDigest[:], node.CanonicalKey, node.RelativePath, guard.ExpectedPositionID)
	for _, generation := range []uuid.UUID{input.Rewrite.CurrentGeneration, guard.CandidateGeneration} {
		exec(`INSERT INTO publication_namespace_reservation(namespace_claim_id,position_id,generation) VALUES (?,?,?)`, claimID, guard.ExpectedPositionID, generation)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	packageRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	toolDir := filepath.Join(packageRoot, filepath.FromSlash(toolRelative))
	if err := os.MkdirAll(toolDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		if err := os.WriteFile(filepath.Join(toolDir, name), []byte("controlled integration command transport"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	repository := persistence.NewSetupManagerRepository(db)
	if err := persistence.NewSettingsRepository(db).Set(ctx, settings.ToolsDirectoryKey, packageRoot); err != nil {
		t.Fatal(err)
	}
	versions := map[string]string{}
	for _, executable := range tools.ExpectedExecutables(tools.PackageFFmpeg, runtime.GOOS) {
		versions[executable] = "ffmpeg version 7.0 build"
		if strings.HasPrefix(executable, "ffprobe") {
			versions[executable] = "ffprobe version 7.0 build"
		}
	}
	versionJSON, err := json.Marshal(versions)
	if err != nil {
		t.Fatal(err)
	}
	verified := time.Now().UTC()
	if err := repository.CreateInstallation(ctx, &persistence.ToolInstallation{ID: installationID, PackageKind: "ffmpeg", PlatformGOOS: runtime.GOOS, PlatformGOARCH: runtime.GOARCH, SourceName: "integration fixture", ReleaseIdentity: "7.0", RelativePath: filepath.FromSlash(toolRelative), State: "ready", VerifiedAt: &verified, ArtifactIdentities: json.RawMessage(`{}`), ExecutableVersions: versionJSON}); err != nil {
		t.Fatal(err)
	}
	holdTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holdTx.Rollback() }()
	if _, err := holdTx.ExecContext(ctx, `INSERT INTO operation_tool_read_hold(operation_id,installation_id) VALUES (?,?)`, guard.OperationID, installationID); err != nil {
		t.Fatal(err)
	}
	if _, err := holdTx.ExecContext(ctx, `UPDATE operation SET tools_read_required=true WHERE id=?`, guard.OperationID); err != nil {
		t.Fatal(err)
	}
	if err := holdTx.Commit(); err != nil {
		t.Fatal(err)
	}
	store := persistence.NewSettingsRepository(db)
	registry := settings.New(store, nil)
	semantics, err := settings.ProbeOutputDirectory(output, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetMany(ctx, map[string]string{
		settings.OutputDirectoryKey:            output,
		settings.OutputCaseSensitiveKey:        fmt.Sprint(semantics.CaseSensitive),
		settings.OutputUnicodeNormalizationKey: semantics.UnicodeNormalization,
	}); err != nil {
		t.Fatal(err)
	}
	commands := &automaticRetryCommands{probeJSON: automaticRetryProbeFixture(t)}
	jobCommands, err := NewPublicationJobCommands(1<<20, 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	executor := NewPublicationExecutor(repository, registry, tools.Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, publication.NewCoordinator(), uuid.New(), jobCommands)
	executor.commands = commands
	lookup := &automaticRetryLookup{}
	if mismatch {
		executor.metadata = &automaticRetryMissingLyricsWriter{}
	}
	if authorityRace {
		lookup.onLookup = func() error {
			envelope := librarymetadata.Envelope{Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopePosition, Desired: map[string]librarymetadata.FieldValue{
				"synced_lyrics": librarymetadata.ManualFieldValue(librarymetadata.StateExplicitEmpty, nil, false),
			}}
			raw, err := json.Marshal(envelope)
			if err != nil {
				return err
			}
			result, err := db.ExecContext(ctx, `UPDATE release_track SET metadata=jsonb_set(COALESCE(metadata,'{}'::jsonb),'{local_metadata}',?::jsonb,true),metadata_revision=metadata_revision+1 WHERE id=?`, string(raw), guard.ExpectedPositionID)
			if err != nil {
				return err
			}
			rows, err := result.RowsAffected()
			if err != nil || rows != 1 {
				return fmt.Errorf("authority callback rows affected=%d err=%v", rows, err)
			}
			return nil
		}
	}
	executor.lyrics = NewPublicationLyricsResolver(automaticRetryEnabled{}, lookup)
	deliveryErr := executor.ExecutePublicationDelivery(ctx, guard.OperationID, guard.RiverJobID, guard.RiverAttempt)
	if len(lookup.requests) != 1 || lookup.requests[0].Artist != "Artist" || lookup.requests[0].Title != "Track" || lookup.requests[0].Duration <= 0 || lookup.requests[0].Duration == math.Trunc(lookup.requests[0].Duration) {
		t.Fatalf("automatic lookup requests = %+v", lookup.requests)
	}
	if authorityRace {
		if deliveryErr == nil {
			t.Fatal("retry exposed output after manual lyrics authority changed")
		}
		var revision int64
		var raw []byte
		if err := db.NewRaw(`SELECT metadata_revision,metadata FROM release_track WHERE id=?`, guard.ExpectedPositionID).Scan(ctx, &revision, &raw); err != nil || revision != 8 {
			t.Fatalf("authority mutation revision=%d err=%v", revision, err)
		}
		var root map[string]json.RawMessage
		if json.Unmarshal(raw, &root) != nil {
			t.Fatalf("decode mutated metadata: %s", raw)
		}
		envelope, err := librarymetadata.DecodeEnvelope(root["local_metadata"])
		if err != nil || envelope.Desired["synced_lyrics"].State != librarymetadata.StateExplicitEmpty {
			t.Fatalf("lookup did not persist manual explicit-empty authority: %+v %v", envelope, err)
		}
		got, readErr := os.ReadFile(currentPath)
		if readErr != nil || !bytes.Equal(got, original) || sha256.Sum256(got) != originalDigest {
			t.Fatalf("authority conflict changed output bytes: %v", readErr)
		}
		var generation uuid.UUID
		if err := db.NewRaw(`SELECT generation FROM track_publication WHERE position_id=?`, guard.ExpectedPositionID).Scan(ctx, &generation); err != nil || generation != input.Rewrite.CurrentGeneration {
			t.Fatalf("authority conflict changed current generation=%s err=%v", generation, err)
		}
		assertSimpleRetryDeliverySettled(t, ctx, db, repository, guard, input, seededAuthority, false, true)
		return
	}
	if deliveryErr != nil {
		t.Fatalf("execute automatic lyrics retry delivery: %v", deliveryErr)
	}
	finalCapture, err := (metadata.Reader{}).Read(ctx, currentPath, metadata.ReadRequest{CoverInspection: true})
	wantLyrics := []string{automaticRetryLyrics}
	if mismatch {
		wantLyrics = nil
		writer, ok := executor.metadata.(*automaticRetryMissingLyricsWriter)
		if !ok || writer.calls != 2 {
			t.Fatalf("fallback metadata writer=%T calls=%d, want two writes", executor.metadata, writer.calls)
		}
	}
	if err != nil || !reflect.DeepEqual(finalCapture.Tags["LYRICS"], wantLyrics) {
		t.Fatalf("published lyrics=%#v err=%v", finalCapture.Tags["LYRICS"], err)
	}
	if !reflect.DeepEqual(finalCapture.Tags["TITLE"], []string{"Track"}) || !reflect.DeepEqual(finalCapture.Tags["ARTIST"], []string{"Artist"}) {
		t.Fatalf("mandatory tags changed: %#v", finalCapture.Tags)
	}
	if err := artwork.VerifyEquivalent(ctx, currentPath); err != nil {
		t.Fatalf("automatic lyrics retry changed audio/artwork: %v", err)
	}
	assertSimpleRetryDeliverySettled(t, ctx, db, repository, guard, input, seededAuthority, mismatch, false)
}

func assertSimpleRetryDeliverySettled(t *testing.T, ctx context.Context, db *bun.DB, repository *persistence.SetupManagerRepository, guard persistence.PublicationDeliveryGuard, input persistence.PublicationInputs, before *persistence.PositionSourceAuthority, fallback, authorityRace bool) {
	t.Helper()
	if authorityRace {
		var generation uuid.UUID
		if err := db.NewRaw(`SELECT generation FROM track_publication WHERE position_id=?`, guard.ExpectedPositionID).Scan(ctx, &generation); err != nil || generation != input.Rewrite.CurrentGeneration {
			t.Errorf("denied retry generation=%s err=%v", generation, err)
		}
		after, err := repository.ReadPositionSourceAuthority(ctx, guard.ExpectedPositionID)
		if err != nil {
			t.Errorf("read source authority after denied retry: %v", err)
		} else if before == nil && after != nil {
			t.Errorf("denied retry changed unknown source authority: %+v", after)
		} else if before != nil && (after == nil || after.Mode != before.Mode || after.Revision != before.Revision || after.SourceLocationID != before.SourceLocationID || after.MediaVariantID != before.MediaVariantID || after.PublicationEpoch != before.PublicationEpoch || after.PublicationGeneration != input.Rewrite.CurrentGeneration) {
			t.Errorf("denied retry changed source authority: got=%+v before=%+v", after, before)
		}
		return
	}
	if input.AutomaticLyricsRetry == nil || input.ManualLyrics != nil {
		t.Errorf("retry frozen inputs do not contain exclusive automatic authority")
	}
	var generation uuid.UUID
	var raw []byte
	if err := db.NewRaw(`SELECT generation,initial_lyrics FROM track_publication WHERE position_id=?`, guard.ExpectedPositionID).Scan(ctx, &generation, &raw); err != nil {
		t.Errorf("read settled automatic retry: %v", err)
		return
	}
	var projection persistence.PublicationInitialLyricsProjection
	digest := sha256.Sum256([]byte(automaticRetryLyrics))
	wantOutcome, wantWarning := persistence.PublicationOptionalLyricsExactRoundTrip, ""
	if fallback {
		wantOutcome, wantWarning = persistence.PublicationOptionalLyricsRebuiltWithoutLyrics, "readback_mismatch"
	}
	if err := json.Unmarshal(raw, &projection); err != nil || generation != guard.CandidateGeneration || projection.Version != 1 || projection.Generation != guard.CandidateGeneration || projection.Source != "lrclib" || projection.SHA256 != fmt.Sprintf("%x", digest) || projection.Outcome != wantOutcome || projection.OutcomeWarningCode != wantWarning || projection.AuthorityMetadataRevision != input.AutomaticLyricsRetry.ExpectedMetadataRevision || projection.Authority != "automatic" {
		t.Errorf("settled retry projection=%+v generation=%s err=%v", projection, generation, err)
	}
	for _, check := range []struct {
		name, query string
		arg         any
	}{
		{name: "manual receipts", query: `SELECT count(*) FROM publication_manual_lyrics_receipt WHERE position_id=?`, arg: guard.ExpectedPositionID},
		{name: "source holds", query: `SELECT count(*) FROM publication_source_hold WHERE operation_id=?`, arg: guard.OperationID},
		{name: "execution claims", query: `SELECT count(*) FROM publication_execution_claim WHERE operation_id=?`, arg: guard.OperationID},
		{name: "attempts", query: `SELECT count(*) FROM track_publication_attempt WHERE operation_id=?`, arg: guard.OperationID},
		{name: "tool holds", query: `SELECT count(*) FROM operation_tool_read_hold WHERE operation_id=?`, arg: guard.OperationID},
	} {
		var count int
		if err := db.NewRaw(check.query, check.arg).Scan(ctx, &count); err != nil || count != 0 {
			t.Errorf("settled retry %s=%d err=%v", check.name, count, err)
		}
	}
	after, err := repository.ReadPositionSourceAuthority(ctx, guard.ExpectedPositionID)
	if err != nil {
		t.Errorf("read settled source authority: %v", err)
	} else if before == nil && after != nil {
		t.Errorf("unknown source authority became known: %+v", after)
	} else if before != nil && (after == nil || after.Mode != before.Mode || after.Revision != before.Revision || after.SourceLocationID != before.SourceLocationID || after.MediaVariantID != before.MediaVariantID || after.PublicationEpoch != before.PublicationEpoch || after.PublicationGeneration != guard.CandidateGeneration) {
		t.Errorf("settled source authority=%+v; prior=%+v", after, before)
	}
	var operationState, operationStage string
	if err := db.NewRaw(`SELECT state,stage FROM operation WHERE id=?`, guard.OperationID).Scan(ctx, &operationState, &operationStage); err != nil || operationState != "succeeded" || operationStage != "succeeded" {
		t.Errorf("settled operation=%s/%s err=%v", operationState, operationStage, err)
	}
}
