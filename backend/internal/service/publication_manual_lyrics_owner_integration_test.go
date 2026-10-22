//go:build integration

package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
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
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

// Admission is seeded here; preparation and outcome records must be produced by
// the actual native service and PostgreSQL repository, never by this fixture.
func TestPreparedManualLyricsFLACOwnerPostgreSQL(t *testing.T) {
	for _, test := range []struct {
		name    string
		cover   bool
		removal bool
	}{
		{name: "no_cover_value"},
		{name: "cover_value", cover: true},
		{name: "cover_explicit_empty", cover: true, removal: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			testPreparedManualLyricsFLACOwner(t, manualLyricsScenario{withCover: test.cover, removal: test.removal})
		})
	}
}

func TestAdmittedPreparedManualLyricsFLACPostgreSQL(t *testing.T) {
	for _, test := range []struct {
		name    string
		cover   bool
		removal bool
	}{
		{name: "no_cover_value"},
		{name: "cover_value", cover: true},
		{name: "cover_explicit_empty", cover: true, removal: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			testPreparedManualLyricsFLACOwner(t, manualLyricsScenario{withCover: test.cover, removal: test.removal, continuous: true, throughAdmission: true})
		})
	}
}

func TestAdmittedPreparedManualLyricsFLACNestedPathPostgreSQL(t *testing.T) {
	testPreparedManualLyricsFLACOwner(t, manualLyricsScenario{withCover: true, continuous: true, throughAdmission: true, nestedPath: true})
}

func TestAdmittedManualLyricsReplacementJournalPostgreSQL(t *testing.T) {
	testPreparedManualLyricsFLACOwner(t, manualLyricsScenario{continuous: true, throughAdmission: true, dbReplacement: true})
}

func TestPreparedManualLyricsFLACNativeReplacementPostgreSQL(t *testing.T) {
	testPreparedManualLyricsFLACOwner(t, manualLyricsScenario{withCover: true, continuous: true, throughAdmission: true, nativeReplacement: true})
}

func TestPublicationExecutorManualLyricsDeliveryPostgreSQL(t *testing.T) {
	testPreparedManualLyricsFLACOwner(t, manualLyricsScenario{withCover: true, delivery: true})
}

func TestPreparedManualLyricsReplacementCommitFailurePostgreSQL(t *testing.T) {
	testPreparedManualLyricsFLACOwner(t, manualLyricsScenario{withCover: true, continuous: true, throughAdmission: true, nativeReplacement: true, commitFailure: true})
}

func TestPreparedManualLyricsReplacementInstalledAckLostPostgreSQL(t *testing.T) {
	testPreparedManualLyricsFLACOwner(t, manualLyricsScenario{withCover: true, continuous: true, throughAdmission: true, nativeReplacement: true, installedAckLost: true})
}

func TestPreparedManualLyricsReplacementCleanupIntentFailurePostgreSQL(t *testing.T) {
	testPreparedManualLyricsFLACOwner(t, manualLyricsScenario{withCover: true, continuous: true, throughAdmission: true, nativeReplacement: true, cleanupIntentFailure: true})
}

type manualLyricsScenario struct {
	mka, withCover, removal, continuous                bool
	throughAdmission, nestedPath, dbReplacement        bool
	nativeReplacement, commitFailure, installedAckLost bool
	cleanupIntentFailure, delivery, automaticRetry     bool
	editManualAuthorityDuringLookup                    bool
	retryLyricsMismatch                                bool
}

func testPreparedManualLyricsFLACOwner(t *testing.T, scenario manualLyricsScenario) {
	testPreparedManualLyricsOwner(t, scenario)
}

func testPreparedManualLyricsOwner(t *testing.T, scenario manualLyricsScenario) {
	ctx := context.Background()
	if scenario.automaticRetry && (!scenario.delivery || scenario.nativeReplacement || scenario.commitFailure || scenario.installedAckLost || scenario.cleanupIntentFailure || scenario.throughAdmission || scenario.continuous || scenario.removal) {
		t.Fatal("automatic retry scenario must exercise a successful full delivery only")
	}
	fixture, fakeStage, _ := preparedManualLyricsFLACStage(t, scenario.automaticRetry)
	t.Cleanup(func() { _ = fakeStage.Close() })
	mka, withCover, removal, continuous := scenario.mka, scenario.withCover, scenario.removal, scenario.continuous
	currentPath := filepath.Join(fixture.output, "current.flac")
	if mka {
		if withCover {
			t.Fatal("MKA fixture does not authorize artwork")
		}
		currentPath = filepath.Join(fixture.output, "current.mka")
		contents, err := os.ReadFile("testdata/manual-lyrics-mp3.mka")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(currentPath, contents, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := (metadata.Writer{}).Write(ctx, currentPath, map[string][]string{"TITLE": {"Track"}, "ARTIST": {"Artist"}}, nil); err != nil {
			t.Fatal(err)
		}
	}
	throughAdmission := scenario.throughAdmission
	nestedPath := scenario.nestedPath
	dbReplacement := scenario.dbReplacement
	nativeReplacement := scenario.nativeReplacement
	commitFailure := scenario.commitFailure
	installedAckLost := scenario.installedAckLost
	cleanupIntentFailure := scenario.cleanupIntentFailure
	delivery := scenario.delivery
	automaticRetry := scenario.automaticRetry
	if nestedPath {
		currentPath = filepath.Join(fixture.output, "Artist", "Album", "Current.FLAC")
		if err := os.MkdirAll(filepath.Dir(currentPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(fixture.output, "current.flac"), currentPath); err != nil {
			t.Fatal(err)
		}
	}
	if removal {
		if _, err := (metadata.Writer{}).Write(ctx, currentPath, map[string][]string{"TITLE": {"Track"}, "ARTIST": {"Artist"}, "LYRICS": {"[00:00.00]old current lyrics\n"}}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if withCover {
		current, err := os.ReadFile(currentPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(currentPath, appendNativeTestPicture(t, current), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Make the copied stage's native mtime differ from the frozen G0 ownership
	// timestamp; replacement evidence must continue to bind the old target.
	oldTargetMTime := time.Date(2020, time.January, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(currentPath, oldTargetMTime, oldTargetMTime); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	// Capture the exact target baseline only after all native metadata, cover,
	// path, and timestamp fixture mutations are complete.
	originalDigest := sha256.Sum256(original)
	input := fixture.repo.input.Clone()
	if nestedPath {
		input.Rewrite.RelativePath = "Artist/Album/Current.FLAC"
	}
	input.DesiredTags = map[string][]string{"TITLE": {"Track"}, "ARTIST": {"Artist"}}
	installationID := uuid.New()
	var automaticToolRoot string
	toolRelative := filepath.ToSlash(filepath.Join("ffmpeg", "7.0"))
	input.Tools = []persistence.SourceAnalysisToolSelection{
		{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: "bin/ffmpeg", Executable: "ffmpeg", Version: "1", VersionBanner: "ffmpeg version 1"},
		{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: "bin/ffprobe", Executable: "ffprobe", Version: "1", VersionBanner: "ffprobe version 1"},
	}
	if automaticRetry {
		input.Tools = []persistence.SourceAnalysisToolSelection{
			{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: toolRelative, Executable: "ffmpeg", Version: "7.0", VersionBanner: "ffmpeg version 7.0 build"},
			{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: toolRelative, Executable: "ffprobe", Version: "7.0", VersionBanner: "ffprobe version 7.0 build"},
		}
		automaticToolRoot = t.TempDir()
		packageDir := filepath.Join(automaticToolRoot, filepath.FromSlash(toolRelative))
		if err := os.MkdirAll(packageDir, 0o700); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"ffmpeg", "ffprobe"} {
			if runtime.GOOS == "windows" {
				name += ".exe"
			}
			if err := os.WriteFile(filepath.Join(packageDir, name), []byte("controlled integration command transport"), 0o700); err != nil {
				t.Fatal(err)
			}
		}
	}
	var mkaPinned *pinnedToolRepo
	if mka {
		_, input.Tools, mkaPinned = pinnedToolFixture(t, true)
		lossless, channels, bitrate, sampleRate := false, 2, 128000, 44100
		input.Rewrite.RelativePath = "current.mka"
		input.Rewrite.AudioFacts = persistence.PersistedAudioFacts{StreamIndex: 0, Codec: "mp3", Container: publication.AudioContainerMKA, Lossless: &lossless, Channels: &channels, Bitrate: &bitrate, SampleRate: &sampleRate, SupportedSourceContainer: true}
	}
	if !automaticRetry {
		input.ManualLyrics = &persistence.PublicationManualLyricsInput{ExpectedMetadataRevision: 7, ConfirmedCurrentGeneration: input.Rewrite.CurrentGeneration, State: "value", Value: "[00:01.00]native PostgreSQL lyrics\r\n"}
		if removal {
			input.ManualLyrics.State = "explicit_empty"
			input.ManualLyrics.Value = ""
		}
	} else {
		input.ManualLyrics = nil
	}
	info, err := os.Stat(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	size, mtime := info.Size(), info.ModTime().UnixMicro()
	digest := sha256.Sum256(original)
	input.Rewrite.Ownership = persistence.PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: fmt.Sprintf("%x", digest)}
	fixture.repo.input = input.Clone()
	capture, err := (metadata.Reader{}).Read(ctx, currentPath, metadata.ReadRequest{CoverInspection: true})
	if err != nil {
		t.Fatal(err)
	}
	var baselineArtwork metadata.FlacArtworkSnapshot
	var verifyAudioArtwork func(context.Context, string) error
	if mka {
		baselineFrames := manualLyricsMP3Frames(t, original)
		verifyAudioArtwork = func(_ context.Context, path string) error {
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if !bytes.Equal(baselineFrames, manualLyricsMP3Frames(t, contents)) {
				return errors.New("MKA rewrite changed encoded MP3 frames")
			}
			return nil
		}
	} else {
		baselineArtwork, err = metadata.SnapshotFlacArtwork(ctx, currentPath)
		if err != nil {
			t.Fatal(err)
		}
		verifyAudioArtwork = baselineArtwork.VerifyEquivalent
	}
	actualAssets, err := ObservePublicationAssets(capture)
	if err != nil {
		t.Fatal(err)
	}
	if withCover && (baselineArtwork.ApprovedCoverFacts().Count != 1 || actualAssets.Cover.State != persistence.PublicationAssetEmbedded) {
		t.Fatal("covered fixture has no verified native front cover")
	}
	if removal && (len(capture.Tags["LYRICS"]) != 1 || actualAssets.Lyrics.State != persistence.PublicationAssetEmbedded) {
		t.Fatal("removal fixture must contain actual current lyrics")
	}
	if automaticRetry {
		input.AutomaticLyricsRetry = &persistence.PublicationAutomaticLyricsRetryInput{ExpectedMetadataRevision: 7, ConfirmedCurrentGeneration: input.Rewrite.CurrentGeneration, CurrentAssets: actualAssets}
		fixture.repo.input = input.Clone()
		if input.AutomaticLyricsRetry == nil || input.ManualLyrics != nil || input.Audio != nil {
			t.Fatal("retry fixture did not freeze exclusive automatic retry authority")
		}
	}
	if err := input.Validate(); err != nil {
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
	var epoch uuid.UUID
	if err := db.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &epoch); err != nil {
		t.Fatal(err)
	}
	guard.Epoch = epoch
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
	exec(`INSERT INTO local_release(id,title) VALUES (?,'native lyrics')`, releaseID)
	exec(`INSERT INTO local_track(id,title) VALUES (?,'native lyrics')`, trackID)
	metadataRevision := int64(7)
	if automaticRetry {
		metadataRevision = input.AutomaticLyricsRetry.ExpectedMetadataRevision
	} else {
		metadataRevision = input.ManualLyrics.ExpectedMetadataRevision
	}
	exec(`INSERT INTO release_track(id,release_id,track_id,position,metadata_revision) VALUES (?,?,?,0,?)`, guard.ExpectedPositionID, releaseID, trackID, metadataRevision)
	if err := tx.QueryRowContext(ctx, `INSERT INTO river_job(kind,args,state,attempt) VALUES ('publish_track_v1','{}','running',1) RETURNING id`).Scan(&guard.RiverJobID); err != nil {
		t.Fatal(err)
	}
	guard.RiverAttempt, guard.ApplicationAttempt = 1, 1
	snapshot := encode(map[string]any{"version": 1, "position_id": guard.ExpectedPositionID, "output_epoch": epoch, "intent_kind": "rewrite"})
	nodes := []persistence.PublicationNamespaceNode{{CanonicalKey: input.Rewrite.RelativePath, RelativePath: input.Rewrite.RelativePath, NodeKind: "file"}}
	if nestedPath {
		nodes = []persistence.PublicationNamespaceNode{
			{CanonicalKey: "artist", RelativePath: "Artist", NodeKind: "dir", DirectoryScope: "artist"},
			{CanonicalKey: "artist/album", RelativePath: "Artist/Album", NodeKind: "dir", DirectoryScope: "album"},
			{CanonicalKey: "artist/album/current.flac", RelativePath: "Artist/Album/Current.FLAC", NodeKind: "file"},
		}
	}
	manifest := encode(map[string]any{"version": 1, "nodes": nodes})
	exec(`INSERT INTO operation(id,kind,state,stage,input_snapshot,attempt,river_job_id) VALUES (?,'publish_track_v1','queued','queued',?::jsonb,1,?)`, guard.OperationID, snapshot, guard.RiverJobID)
	exec(`INSERT INTO track_publication_attempt(operation_id,position_id,epoch,expected_generation,candidate_generation,intent_kind,phase,frozen_inputs,namespace_manifest,mutation_journal) VALUES (?,?,?,?,?,'rewrite','queued',?::jsonb,?::jsonb,'[]')`, guard.OperationID, guard.ExpectedPositionID, epoch, input.Rewrite.CurrentGeneration, guard.CandidateGeneration, encode(input), manifest)
	exec(`INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at) VALUES (?,?,?,?,?::jsonb,?::jsonb,?::jsonb,?::jsonb,1,now())`, guard.ExpectedPositionID, epoch, input.Rewrite.CurrentGeneration, input.Rewrite.RelativePath,
		encode(persistence.PublicationCurrentOwnership{Version: 1, Identity: input.Rewrite.Ownership}),
		encode(persistence.PublicationCurrentAudio{Version: 1, Facts: input.Rewrite.AudioFacts}),
		encode(persistence.PublicationCommitMetadata{Version: 1, Tags: capture.Tags}),
		encode(actualAssets))
	for _, node := range nodes {
		claimID := uuid.New()
		namespaceDigest := sha256.Sum256([]byte(node.CanonicalKey))
		var owner any
		if node.NodeKind == "file" {
			owner = guard.ExpectedPositionID
		}
		var directoryScope any
		if node.NodeKind == "dir" {
			directoryScope = node.DirectoryScope
		}
		exec(`INSERT INTO publication_namespace_claim(id,epoch,key_digest,canonical_key,relative_path,node_kind,owner_position_id,directory_scope_key) VALUES (?,?,?,?,?,?,?,?)`, claimID, epoch, namespaceDigest[:], node.CanonicalKey, node.RelativePath, node.NodeKind, owner, directoryScope)
		for _, generation := range []uuid.UUID{input.Rewrite.CurrentGeneration, guard.CandidateGeneration} {
			exec(`INSERT INTO publication_namespace_reservation(namespace_claim_id,position_id,generation) VALUES (?,?,?)`, claimID, guard.ExpectedPositionID, generation)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	repository := persistence.NewSetupManagerRepository(db)
	if automaticRetry {
		if err := persistence.NewSettingsRepository(db).Set(ctx, settings.ToolsDirectoryKey, automaticToolRoot); err != nil {
			t.Fatalf("seed managed tools root: %v", err)
		}
		versions := map[string]string{}
		for _, executable := range tools.ExpectedExecutables(tools.PackageFFmpeg, runtime.GOOS) {
			versions[executable] = strings.TrimSuffix(executable, ".exe") + " version 7.0 build"
		}
		versionJSON, err := json.Marshal(versions)
		if err != nil {
			t.Fatal(err)
		}
		verified := time.Now().UTC()
		if err := repository.CreateInstallation(ctx, &persistence.ToolInstallation{ID: installationID, PackageKind: "ffmpeg", PlatformGOOS: runtime.GOOS, PlatformGOARCH: runtime.GOARCH, SourceName: "integration fixture", ReleaseIdentity: "7.0", RelativePath: filepath.FromSlash(toolRelative), State: "ready", VerifiedAt: &verified, ArtifactIdentities: json.RawMessage(`{}`), ExecutableVersions: versionJSON}); err != nil {
			t.Fatalf("seed verified managed ffmpeg installation: %v", err)
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO operation_tool_read_hold(operation_id,installation_id) VALUES (?,?)`, guard.OperationID, installationID); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE operation SET tools_read_required=true WHERE id=?`, guard.OperationID); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	if delivery {
		store := persistence.NewSettingsRepository(db)
		registry := settings.New(store, nil)
		// This fixture restores an already populated publication root, not a new
		// setup directory. Observe its native semantics before seeding settings.
		semantics, err := settings.ProbeOutputDirectory(fixture.output, false)
		if err != nil {
			t.Fatal(err)
		}
		settingsValues := map[string]string{
			settings.OutputDirectoryKey:            fixture.output,
			settings.OutputCaseSensitiveKey:        fmt.Sprint(semantics.CaseSensitive),
			settings.OutputUnicodeNormalizationKey: semantics.UnicodeNormalization,
		}
		if err := store.SetMany(ctx, settingsValues); err != nil {
			t.Fatal(err)
		}
		commands, err := NewPublicationJobCommands(1<<20, 64<<10)
		if err != nil {
			t.Fatal(err)
		}
		executor := NewPublicationExecutor(repository, registry, tools.Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, publication.NewCoordinator(), uuid.New(), commands)
		var retryLookup *automaticRetryLookup
		if automaticRetry {
			retryLookup = &automaticRetryLookup{}
			if scenario.retryLyricsMismatch {
				executor.metadata = &automaticRetryMissingLyricsWriter{}
			}
			executor.commands = &automaticRetryCommands{probeJSON: automaticRetryProbeFixture(t)}
			if scenario.editManualAuthorityDuringLookup {
				retryLookup.onLookup = func() error {
					envelope := librarymetadata.Envelope{Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopePosition, Desired: map[string]librarymetadata.FieldValue{
						"synced_lyrics": librarymetadata.ManualFieldValue(librarymetadata.StateExplicitEmpty, nil, false),
					}}
					rawEnvelope, err := json.Marshal(envelope)
					if err != nil {
						return err
					}
					result, err := db.ExecContext(ctx, `UPDATE release_track SET metadata=jsonb_set(COALESCE(metadata,'{}'::jsonb),'{local_metadata}',?::jsonb,true),metadata_revision=metadata_revision+1 WHERE id=?`, string(rawEnvelope), guard.ExpectedPositionID)
					if err != nil {
						return err
					}
					rows, err := result.RowsAffected()
					if err != nil {
						return err
					}
					if rows != 1 {
						return fmt.Errorf("changed manual authority rows affected = %d, want 1", rows)
					}
					return nil
				}
			}
			executor.lyrics = NewPublicationLyricsResolver(automaticRetryEnabled{}, retryLookup)
		}
		err = executor.ExecutePublicationDelivery(ctx, guard.OperationID, guard.RiverJobID, guard.RiverAttempt)
		if automaticRetry && scenario.editManualAuthorityDuringLookup {
			if len(retryLookup.requests) != 1 || retryLookup.requests[0].Duration <= 0 || retryLookup.requests[0].Duration == math.Trunc(retryLookup.requests[0].Duration) {
				t.Fatalf("authority conflict did not follow exact fractional lookup: %+v", retryLookup.requests)
			}
			if err == nil {
				t.Fatal("retry exposed output after manual lyrics authority changed")
			}
			var revision int64
			var metadataRaw []byte
			if readErr := db.NewRaw(`SELECT metadata_revision,metadata FROM release_track WHERE id=?`, guard.ExpectedPositionID).Scan(ctx, &revision, &metadataRaw); readErr != nil || revision != 8 {
				t.Fatalf("manual authority callback did not persist revision 8: revision=%d err=%v", revision, readErr)
			}
			var metadataRoot map[string]json.RawMessage
			if json.Unmarshal(metadataRaw, &metadataRoot) != nil {
				t.Fatalf("decode changed authority metadata: %s", metadataRaw)
			}
			envelope, envelopeErr := librarymetadata.DecodeEnvelope(metadataRoot["local_metadata"])
			if envelopeErr != nil || envelope.Desired["synced_lyrics"].State != librarymetadata.StateExplicitEmpty {
				t.Fatalf("manual authority callback did not persist explicit-empty lyrics authority: metadata=%s", metadataRaw)
			}
			attempt, readErr := repository.ReadPublicationAttempt(ctx, guard.OperationID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			var journal []json.RawMessage
			if json.Unmarshal(attempt.MutationJournal, &journal) != nil {
				t.Fatal("decode retry journal after authority denial")
			}
			for _, raw := range journal {
				var header struct {
					Kind string `json:"kind"`
				}
				if json.Unmarshal(raw, &header) != nil || header.Kind == "publication_exposure" || header.Kind == "publication_replacement" {
					t.Fatalf("authority denial retained exposure/replacement evidence: %s", raw)
				}
			}
			got, readErr := os.ReadFile(currentPath)
			gotDigest := sha256.Sum256(got)
			if readErr != nil || !bytes.Equal(got, original) || gotDigest != originalDigest {
				t.Fatalf("authority conflict changed current artifact: baseline=%x current=%x err=%v", originalDigest, gotDigest, readErr)
			}
			var generation uuid.UUID
			if readErr := db.NewRaw(`SELECT generation FROM track_publication WHERE position_id=?`, guard.ExpectedPositionID).Scan(ctx, &generation); readErr != nil || generation != input.Rewrite.CurrentGeneration {
				t.Fatalf("authority conflict changed current generation: %s %v", generation, readErr)
			}
			assertReplacementDBCounts(t, ctx, db, guard.OperationID, guard.ExpectedPositionID, 1, 1, 0, 1, 0)
			return
		}
		if runtime.GOOS == "windows" {
			if !errors.Is(err, sourcefs.ErrUnsupported) {
				t.Fatalf("manual lyrics executor error = %v, want sourcefs.ErrUnsupported", err)
			}
			assertReplacementStillExposed(t, ctx, db, repository, guard, input)
			return
		}
		if err != nil {
			t.Fatalf("execute manual lyrics delivery: %v", err)
		}
		if _, err := os.Stat(currentPath); err != nil {
			t.Fatalf("published target unavailable: %v", err)
		}
		finalCapture, err := (metadata.Reader{}).Read(ctx, currentPath, metadata.ReadRequest{CoverInspection: true})
		wantLyrics := "[00:01.00]native PostgreSQL lyrics\r\n"
		if automaticRetry {
			wantLyrics = automaticRetryLyrics
			if len(retryLookup.requests) != 1 || retryLookup.requests[0].Duration <= 0 || retryLookup.requests[0].Duration == math.Trunc(retryLookup.requests[0].Duration) {
				t.Fatalf("retry exact lookup requests = %+v", retryLookup.requests)
			}
		} else {
			wantLyrics = input.ManualLyrics.Value
		}
		wantLyricValues := []string{wantLyrics}
		if scenario.retryLyricsMismatch {
			wantLyricValues = nil
			writer := executor.metadata.(*automaticRetryMissingLyricsWriter)
			if writer.calls != 2 {
				t.Fatalf("fallback native metadata writes=%d, want initial plus one rebuild", writer.calls)
			}
		}
		if err != nil || !reflect.DeepEqual(finalCapture.Tags["LYRICS"], wantLyricValues) {
			t.Fatalf("executor-installed lyrics = %#v, %v", finalCapture.Tags["LYRICS"], err)
		}
		if automaticRetry && (!reflect.DeepEqual(finalCapture.Tags["TITLE"], []string{"Track"}) || !reflect.DeepEqual(finalCapture.Tags["ARTIST"], []string{"Artist"})) {
			t.Fatalf("retry changed mandatory tags: %#v", finalCapture.Tags)
		}
		if automaticRetry {
			assertAutomaticRetrySettledInDB(t, ctx, db, guard, input, scenario.retryLyricsMismatch)
		}
		if err := baselineArtwork.VerifyEquivalent(ctx, currentPath); err != nil {
			t.Fatalf("executor replacement changed native artwork/audio: %v", err)
		}
		if !automaticRetry {
			projection, err := repository.ReadManualLyricsReceiptProjection(ctx, guard.ExpectedPositionID)
			if err != nil || projection.Pending || projection.Receipt == nil || projection.CurrentGeneration == nil || *projection.CurrentGeneration != guard.CandidateGeneration {
				t.Fatalf("executor receipt/current projection = %+v, %v", projection, err)
			}
			assertReplacementSettledInDB(t, ctx, db, repository, guard, input)
		}
		return
	}
	commitRepository := &manualLyricsReplacementCommitFailureRepository{SetupManagerRepository: repository}
	installedAckRepository := &manualLyricsInstalledAckLostRepository{SetupManagerRepository: repository}
	cleanupIntentRepository := &manualLyricsCleanupIntentFailureRepository{SetupManagerRepository: repository}
	claim, err := repository.ClaimPublicationDelivery(ctx, guard)
	if err != nil || claim != persistence.PublicationDeliveryAcquired {
		t.Fatalf("claim = %q, %v", claim, err)
	}
	preparer := fixture.preparer
	if commitFailure {
		preparer.Repository = commitRepository
	} else if installedAckLost {
		preparer.Repository = installedAckRepository
	} else if cleanupIntentFailure {
		preparer.Repository = cleanupIntentRepository
	} else {
		preparer.Repository = repository
	}
	value, err := preparer.prepareManualLyricsFLAC(ctx, guard, claim, true)
	if err != nil {
		t.Fatal(err)
	}
	stage := value.(*preparedPublicationStage)
	t.Cleanup(func() {
		if err := stage.Close(); err != nil {
			t.Errorf("close real stage: %v", err)
		}
	})
	scope, reservation := initialOutputCopyScope(t, guard)
	if commitFailure {
		scope.repository = commitRepository
	} else if installedAckLost {
		scope.repository = installedAckRepository
	} else if cleanupIntentFailure {
		scope.repository = cleanupIntentRepository
	} else {
		scope.repository = repository
	}
	if mka {
		raw, err := os.ReadFile("testdata/manual-lyrics-mp3.probe.json")
		if err != nil {
			t.Fatal(err)
		}
		commands := &manualLyricsMKACommands{raw: raw}
		scope.commands = commands
		pinned, err := NewScopeBoundPublicationPinnedTools(mkaPinned, scope, guard, tools.Platform{GOOS: "linux", GOARCH: "amd64"})
		if err != nil {
			t.Fatal(err)
		}
		stage.manualProbe, err = NewScopeBoundPublicationStageProbe(scope, pinned, input.Tools)
		if err != nil {
			t.Fatal(err)
		}
	}
	release := scope.release
	releaseCount := 0
	scope.release = func() {
		releaseCount++
		if nativeReplacement && !commitFailure && runtime.GOOS != "windows" {
			assertReplacementSettledInDB(t, ctx, db, repository, guard, input)
		}
		if automaticRetry && runtime.GOOS != "windows" {
			assertAutomaticRetrySettledInDB(t, ctx, db, guard, input, scenario.retryLyricsMismatch)
		}
		release()
	}
	if nativeReplacement && stage.sourceFileInfo.ModTime().UnixMicro() == *input.Rewrite.Ownership.MTime {
		t.Fatal("native stage copied the frozen target mtime; fixture cannot distinguish backup from G0")
	}
	proof, err := stage.initialOutputCopy(ctx, scope, reservation)
	if err != nil {
		t.Fatal(err)
	}
	if nativeReplacement {
		result, replaceErr := replacePreparedManualLyricsFLAC(ctx, scope, reservation, proof, input)
		if runtime.GOOS == "windows" {
			if !errors.Is(replaceErr, sourcefs.ErrUnsupported) {
				t.Fatalf("native Windows replacement error = %v, want sourcefs.ErrUnsupported", replaceErr)
			}
			if installedAckLost && (installedAckRepository.installedCalls != 0 || installedAckRepository.commitCalls != 0) {
				t.Fatalf("unsupported replacement reached installed-ack wrapper: %+v", installedAckRepository)
			}
			if cleanupIntentFailure && (cleanupIntentRepository.cleanupCalls != 0 || cleanupIntentRepository.commitCalls != 0) {
				t.Fatalf("unsupported replacement reached cleanup wrapper: %+v", cleanupIntentRepository)
			}
			assertReplacementStillExposed(t, ctx, db, repository, guard, input)
			attempt, readErr := repository.ReadPublicationAttempt(ctx, guard.OperationID)
			if readErr != nil || attempt == nil || attempt.Phase != "exposing" || len(attempt.MutationJournal) == 0 {
				t.Fatalf("unsupported replacement did not retain the exposed attempt: %#v, %v", attempt, readErr)
			}
			got, readErr := os.ReadFile(currentPath)
			if readErr != nil || !bytes.Equal(got, original) {
				t.Fatalf("unsupported replacement changed current target: %v", readErr)
			}
			if releaseCount != 0 {
				t.Fatalf("unsupported replacement released local execution resources %d times", releaseCount)
			}
			scope.mu.Lock()
			unknown, held := scope.unknown, scope.privateManualLyricsWork != nil
			scope.mu.Unlock()
			if !unknown || !held || result.OptionalDiagnostic != "" {
				t.Fatalf("unsupported replacement did not quarantine held private work: unknown=%v held=%v", unknown, held)
			}
			return
		}
		if installedAckLost {
			if !errors.Is(replaceErr, errInjectedInstalledAckLost) || installedAckRepository.installedCalls != 1 || installedAckRepository.commitCalls != 0 {
				t.Fatalf("installed acknowledgement loss = %v, installed calls %d, commits %d", replaceErr, installedAckRepository.installedCalls, installedAckRepository.commitCalls)
			}
			assertReplacementStillExposed(t, ctx, db, repository, guard, input)
			attempt, readErr := repository.ReadPublicationAttempt(ctx, guard.OperationID)
			var journal []struct {
				To string `json:"to"`
			}
			if readErr != nil || attempt == nil || attempt.Phase != "exposing" || json.Unmarshal(attempt.MutationJournal, &journal) != nil || len(journal) != 11 || journal[10].To != "installed" {
				t.Fatalf("lost installed acknowledgement did not retain installed journal: %s, %v", attempt.MutationJournal, readErr)
			}
			got, readErr := os.ReadFile(currentPath)
			if readErr != nil || bytes.Equal(got, original) {
				t.Fatalf("installed target bytes unavailable: %v", readErr)
			}
			candidateCapture, readErr := (metadata.Reader{}).Read(ctx, currentPath, metadata.ReadRequest{CoverInspection: true})
			if readErr != nil || !reflect.DeepEqual(candidateCapture.Tags["LYRICS"], []string{input.ManualLyrics.Value}) {
				t.Fatalf("installed target lyrics = %#v, %v", candidateCapture.Tags["LYRICS"], readErr)
			}
			if _, readErr := os.Stat(filepath.Dir(stage.paths.InputPath)); readErr != nil {
				t.Fatalf("ambiguous private stage removed: %v", readErr)
			}
			if _, readErr := os.Stat(stage.paths.InputPath); readErr != nil {
				t.Fatalf("ambiguous backup removed: %v", readErr)
			}
			scope.mu.Lock()
			unknown, held, token, syncWork := scope.unknown, scope.privateManualLyricsWork != nil, scope.replacement != nil, scope.syncWork
			scope.mu.Unlock()
			if !unknown || !held || token || syncWork != 1 || releaseCount != 0 {
				t.Fatalf("lost ACK ownership state: unknown=%v held=%v token=%v syncWork=%d releases=%d", unknown, held, token, syncWork, releaseCount)
			}
			return
		}
		if cleanupIntentFailure {
			if replaceErr == nil || cleanupIntentRepository.cleanupCalls != 1 || cleanupIntentRepository.commitCalls != 1 {
				t.Fatalf("cleanup intent failure = %v, cleanup calls %d commits %d", replaceErr, cleanupIntentRepository.cleanupCalls, cleanupIntentRepository.commitCalls)
			}
			projection, projectionErr := repository.ReadManualLyricsReceiptProjection(ctx, guard.ExpectedPositionID)
			if projectionErr != nil || projection.Pending || projection.CurrentGeneration == nil || *projection.CurrentGeneration != guard.CandidateGeneration || projection.Receipt == nil || projection.Receipt.ConfirmedGeneration != input.Rewrite.CurrentGeneration || projection.Receipt.PublishedGeneration != guard.CandidateGeneration {
				t.Fatalf("committed cleanup-failure receipt = %+v, %v", projection, projectionErr)
			}
			assertReplacementDBCounts(t, ctx, db, guard.OperationID, guard.ExpectedPositionID, 1, 1, 0, 0, 1)
			attempt, readErr := repository.ReadPublicationAttempt(ctx, guard.OperationID)
			var entries []struct {
				Kind string `json:"kind"`
				Step string `json:"step"`
			}
			if readErr != nil || attempt == nil || attempt.Phase != "committed" || json.Unmarshal(attempt.MutationJournal, &entries) != nil || len(entries) != 12 || entries[11].Kind != "publication_replacement_commit" {
				t.Fatalf("cleanup failure journal = %s, %v", attempt.MutationJournal, readErr)
			}
			if _, readErr := os.Stat(filepath.Dir(stage.paths.InputPath)); readErr != nil {
				t.Fatalf("cleanup failure removed private directory: %v", readErr)
			}
			if _, readErr := os.Stat(stage.paths.InputPath); readErr != nil {
				t.Fatalf("cleanup failure removed backup: %v", readErr)
			}
			backupBytes, readErr := os.ReadFile(stage.paths.InputPath)
			if readErr != nil || !bytes.Equal(backupBytes, original) {
				t.Fatalf("cleanup failure changed retained backup: %v", readErr)
			}
			if attempt.FrozenInputs != nil {
				t.Fatalf("committed cleanup-failure attempt retained frozen SQL inputs: %s", attempt.FrozenInputs)
			}
			var claimState string
			if err := db.NewRaw(`SELECT state FROM publication_execution_claim WHERE operation_id=?`, guard.OperationID).Scan(ctx, &claimState); err != nil || claimState != "executing" && claimState != "recovery_required" {
				t.Fatalf("cleanup-failure claim state = %q, %v", claimState, err)
			}
			scope.mu.Lock()
			unknown, finalizing, finalized := scope.unknown, scope.finalizing, scope.finalized
			held, token, syncWork := scope.privateManualLyricsWork != nil, scope.replacement != nil, scope.syncWork
			scope.mu.Unlock()
			if !unknown || !finalizing || finalized || held || !token || syncWork != 0 || releaseCount != 0 {
				t.Fatalf("cleanup failure ownership: unknown=%v finalizing=%v finalized=%v held=%v token=%v syncWork=%d releases=%d", unknown, finalizing, finalized, held, token, syncWork, releaseCount)
			}
			return
		}
		if commitFailure {
			if !errors.Is(replaceErr, errInjectedReplacementCommit) {
				t.Fatalf("native replacement commit error = %v, want injected commit failure", replaceErr)
			}
			if commitRepository.calls != 1 {
				t.Fatalf("replacement commit calls = %d, want 1", commitRepository.calls)
			}
			projection, readErr := repository.ReadManualLyricsReceiptProjection(ctx, guard.ExpectedPositionID)
			if readErr != nil || projection.CurrentGeneration == nil || *projection.CurrentGeneration != input.Rewrite.CurrentGeneration || projection.Receipt != nil {
				t.Fatalf("failed commit changed G0 ownership/receipt: %+v, %v", projection, readErr)
			}
			assertReplacementDBCounts(t, ctx, db, guard.OperationID, guard.ExpectedPositionID, 1, 1, 0, 0, 0)
			attempt, readErr := repository.ReadPublicationAttempt(ctx, guard.OperationID)
			var journal []struct {
				To string `json:"to"`
			}
			if readErr != nil || attempt == nil || attempt.Phase != "exposing" || json.Unmarshal(attempt.MutationJournal, &journal) != nil || len(journal) != 11 || journal[10].To != "installed" {
				t.Fatalf("failed commit did not retain installed exposure journal: %s, %v", attempt.MutationJournal, readErr)
			}
			var claimState string
			if err := db.NewRaw(`SELECT state FROM publication_execution_claim WHERE operation_id=?`, guard.OperationID).Scan(ctx, &claimState); err != nil || claimState != "executing" && claimState != "recovery_required" {
				t.Fatalf("failed commit claim state = %q, %v", claimState, err)
			}
			got, readErr := os.ReadFile(currentPath)
			if readErr != nil || bytes.Equal(got, original) {
				t.Fatalf("failed commit did not leave installed candidate bytes: %v", readErr)
			}
			finalCapture, readErr := (metadata.Reader{}).Read(ctx, currentPath, metadata.ReadRequest{CoverInspection: true})
			if readErr != nil || !reflect.DeepEqual(finalCapture.Tags["LYRICS"], []string{input.ManualLyrics.Value}) {
				t.Fatalf("failed commit candidate lyrics = %#v, %v", finalCapture.Tags["LYRICS"], readErr)
			}
			if err := verifyAudioArtwork(ctx, currentPath); err != nil {
				t.Fatalf("failed commit changed native artwork/audio: %v", err)
			}
			if _, err := os.Stat(filepath.Dir(stage.paths.InputPath)); err != nil {
				t.Fatalf("failed commit removed private stage: %v", err)
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(stage.paths.InputPath), filepath.Base(stage.paths.InputPath))); err != nil {
				t.Fatalf("failed commit removed private backup input: %v", err)
			}
			scope.mu.Lock()
			unknown, finalizing, finalized := scope.unknown, scope.finalizing, scope.finalized
			held, token, syncWork := scope.privateManualLyricsWork != nil, scope.replacement != nil, scope.syncWork
			scope.mu.Unlock()
			if !unknown || !finalizing || finalized || held || !token || syncWork != 0 || releaseCount != 0 {
				t.Fatalf("failed commit did not retain quarantined completion ownership: unknown=%v finalizing=%v finalized=%v work=%v token=%v syncWork=%d releases=%d", unknown, finalizing, finalized, held, token, syncWork, releaseCount)
			}
			return
		}
		if replaceErr != nil {
			t.Fatalf("native replacement: %v", replaceErr)
		}
		if _, err := os.Stat(filepath.Dir(stage.paths.InputPath)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("private stage still exists after replacement: %v", err)
		}
		projection, err := repository.ReadManualLyricsReceiptProjection(ctx, guard.ExpectedPositionID)
		if err != nil || projection.Pending || projection.Receipt == nil || projection.CurrentGeneration == nil || *projection.CurrentGeneration != guard.CandidateGeneration || projection.Receipt.ConfirmedGeneration != input.Rewrite.CurrentGeneration || projection.Receipt.PublishedGeneration != guard.CandidateGeneration {
			t.Fatalf("replacement receipt/current projection = %+v, %v", projection, err)
		}
		desiredDigest := sha256.Sum256([]byte(input.ManualLyrics.Value))
		if projection.Receipt.DesiredRevision != input.ManualLyrics.ExpectedMetadataRevision {
			t.Fatalf("replacement receipt does not bind requested lyrics revision/value: %+v", projection.Receipt)
		}
		if removal {
			if projection.Receipt.DesiredState != persistence.ManualLyricsExplicitEmpty || projection.Receipt.DesiredSHA256 != nil {
				t.Fatalf("replacement receipt does not bind explicit removal: %+v", projection.Receipt)
			}
		} else if projection.Receipt.DesiredState != persistence.ManualLyricsValue || projection.Receipt.DesiredSHA256 == nil || *projection.Receipt.DesiredSHA256 != fmt.Sprintf("%x", desiredDigest) {
			t.Fatalf("replacement receipt does not bind requested lyrics value: %+v", projection.Receipt)
		}
		finalCapture, err := (metadata.Reader{}).Read(ctx, currentPath, metadata.ReadRequest{CoverInspection: true})
		if err != nil {
			t.Fatalf("read installed target: %v", err)
		}
		if got, present := finalCapture.Tags["LYRICS"]; removal && present || !removal && (len(got) != 1 || got[0] != input.ManualLyrics.Value) {
			t.Fatalf("installed target lyrics = %#v", got)
		}
		installedBytes, err := os.ReadFile(currentPath)
		if err != nil || bytes.Equal(installedBytes, original) {
			t.Fatalf("replacement did not change current file bytes: %v", err)
		}
		if err := verifyAudioArtwork(ctx, currentPath); err != nil {
			t.Fatalf("installed replacement changed native artwork/audio: %v", err)
		}
		scope.mu.Lock()
		finalized, held, unknown := scope.finalized, scope.privateManualLyricsWork != nil, scope.unknown
		scope.mu.Unlock()
		if !finalized || held || unknown || releaseCount != 1 {
			t.Fatalf("replacement scope did not settle: finalized=%v held=%v unknown=%v releases=%d", finalized, held, unknown, releaseCount)
		}
		return
	}
	if throughAdmission {
		if _, err := admitPreparedManualLyricsFLACExposure(ctx, scope, reservation, proof, input); err != nil {
			t.Fatal(err)
		}
	} else if continuous {
		if _, err := validatePreparedManualLyricsFLAC(ctx, scope, reservation, proof, input); err != nil {
			t.Fatal(err)
		}
	} else {
		if _, err := applyAndRecordPreparedManualLyricsFLAC(ctx, scope, reservation, proof, input); err != nil {
			t.Fatal(err)
		}
	}
	attempt, err := repository.ReadPublicationAttempt(ctx, guard.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	var records []struct {
		Kind string `json:"kind"`
		Step string `json:"step"`
	}
	if err := json.Unmarshal(attempt.MutationJournal, &records); err != nil {
		t.Fatal(err)
	}
	var outcome persistence.PublicationOptionalLyricsOutcomeEvidence
	var entries []json.RawMessage
	if err := json.Unmarshal(attempt.MutationJournal, &entries); err != nil {
		t.Fatal(err)
	}
	for _, raw := range entries {
		var item struct {
			Kind             string                                   `json:"kind"`
			Outcome          string                                   `json:"outcome"`
			FinalObservation persistence.PublicationStageObservation  `json:"final_observation"`
			ManualLyrics     persistence.PublicationManualLyricsInput `json:"manual_lyrics"`
			Preparation      persistence.PublicationPreparationRecord `json:"preparation"`
			InputIdentity    persistence.PublicationFileIdentity      `json:"input_identity"`
			OutputIdentity   persistence.PublicationFileIdentity      `json:"output_identity"`
			Diagnostic       string                                   `json:"diagnostic"`
		}
		if json.Unmarshal(raw, &item) == nil && item.Kind == "publication_optional_lyrics_outcome" {
			outcome = persistence.PublicationOptionalLyricsOutcomeEvidence{Kind: item.Outcome, FinalObservation: item.FinalObservation, ManualLyrics: item.ManualLyrics, Preparation: item.Preparation, InputIdentity: item.InputIdentity, OutputIdentity: item.OutputIdentity, Diagnostic: item.Diagnostic}
		}
	}
	if outcome.Kind != persistence.PublicationOptionalLyricsExactRoundTrip {
		t.Fatalf("unexpected durable lyrics outcome: %q", outcome.Kind)
	}
	var finalAssets persistence.PublicationAssetOutcomes
	if err := json.Unmarshal(outcome.FinalObservation.ActualAssets, &finalAssets); err != nil {
		t.Fatalf("final assets: %v", err)
	}
	if finalAssets.Cover.State != actualAssets.Cover.State || finalAssets.Cover.SHA256 != actualAssets.Cover.SHA256 || finalAssets.Cover.MIME != actualAssets.Cover.MIME || !equalOptionalSize(finalAssets.Cover.SizeBytes, actualAssets.Cover.SizeBytes) {
		t.Fatalf("final cover outcome = %+v, want observed %+v", finalAssets.Cover, actualAssets.Cover)
	}
	var finalMetadata persistence.PublicationCommitMetadata
	if err := json.Unmarshal(outcome.FinalObservation.ActualMetadata, &finalMetadata); err != nil {
		t.Fatal(err)
	}
	if removal {
		if _, present := finalMetadata.Tags["LYRICS"]; present {
			t.Fatalf("removed LYRICS still present: %#v", finalMetadata.Tags["LYRICS"])
		}
	} else if got := finalMetadata.Tags["LYRICS"]; len(got) != 1 || got[0] != input.ManualLyrics.Value {
		t.Fatalf("final lyrics = %#v", got)
	}
	if err := verifyAudioArtwork(ctx, stage.paths.OutputPath); err != nil {
		t.Fatalf("private final output changed native FLAC picture/audio: %v", err)
	}
	wantPhase, wantRecords := "preparing", 4
	if throughAdmission {
		wantPhase, wantRecords = "exposing", 7
	} else if continuous {
		wantPhase, wantRecords = "validated", 6
	}
	if attempt.Phase != wantPhase || len(records) != wantRecords || records[0].Step != "intent" || records[1].Step != "directory_created" || records[2].Step != "input_ready" || records[3].Kind != "publication_optional_lyrics_outcome" {
		t.Fatalf("unexpected native preparation/outcome journal: %s", attempt.MutationJournal)
	}
	var generation uuid.UUID
	if err := db.NewRaw(`SELECT generation FROM track_publication WHERE position_id=?`, guard.ExpectedPositionID).Scan(ctx, &generation); err != nil || generation != input.Rewrite.CurrentGeneration {
		t.Fatalf("current generation changed: %s, %v", generation, err)
	}
	contents, err := os.ReadFile(filepath.Join(fixture.output, input.Rewrite.RelativePath))
	if err != nil || !bytes.Equal(contents, original) {
		t.Fatalf("current bytes changed: %v", err)
	}
	// These calls test DB transition authority only: the owner above has already
	// released its private work capability. They are not a continuous runtime handoff.
	if throughAdmission {
		if err := json.Unmarshal(attempt.MutationJournal, &entries); err != nil || len(entries) != 7 {
			t.Fatalf("admitted owner did not persist exposure admission: %s, %v", attempt.MutationJournal, err)
		}
		var exposure struct {
			Kind string `json:"kind"`
			From string `json:"from"`
			To   string `json:"to"`
		}
		if err := json.Unmarshal(entries[6], &exposure); err != nil || exposure.Kind != "publication_exposure" || exposure.From != "validated" || exposure.To != "exposing" {
			t.Fatalf("invalid native exposure admission record: %s, %v", entries[6], err)
		}
		frozen, err := persistence.DecodePublicationInputs(attempt.FrozenInputs)
		if err != nil || !reflect.DeepEqual(frozen, input) {
			t.Fatalf("admitted owner changed frozen publication inputs: %v", err)
		}
		// Exercise only the PostgreSQL journal contract after the real admitted
		// owner has persisted exposure. This deliberately does not perform a
		// filesystem replacement or call replacement commit.
		if dbReplacement {
			backupIdentity, installedIdentity, err := stage.optionalLyricsFileIdentities(ctx)
			if err != nil {
				t.Fatalf("read native admitted replacement identities: %v", err)
			}
			var exposure struct {
				Kind        string                                  `json:"kind"`
				Intent      persistence.PublicationExposureIntent   `json:"intent"`
				Observation persistence.PublicationStageObservation `json:"observation"`
			}
			if err := json.Unmarshal(entries[6], &exposure); err != nil || exposure.Kind != "publication_exposure" {
				t.Fatalf("decode admitted exposure observation: %v", err)
			}
			base := persistence.PublicationReplacementEvidence{
				Version: 1, Kind: "publication_replacement", Guard: guard,
				PositionID: guard.ExpectedPositionID, Epoch: guard.Epoch,
				CandidateGeneration: guard.CandidateGeneration,
				ExpectedGeneration:  &input.Rewrite.CurrentGeneration,
				Intent:              exposure.Intent,
				Staged:              outcome.FinalObservation, Identity: input.Rewrite.Ownership,
				BackupIdentity: backupIdentity, BackupPath: outcome.Preparation.InputRel,
			}
			// V3's generic guarded replacement API is intentionally not an owner
			// capability; it must reject before the first replacement transition.
			generic := base
			generic.From, generic.To = "exposing", "backup_intent"
			if got, callErr := repository.RecordPublicationReplacement(ctx, guard, generic); got != persistence.PublicationReplacementStale {
				t.Fatalf("generic V3 replacement = %q, %v; want stale", got, callErr)
			}
			for _, transition := range []struct{ from, to string }{
				{"exposing", "backup_intent"},
				{"backup_intent", "backup_ready"},
				{"backup_ready", "replacement_intent"},
				{"replacement_intent", "installed"},
			} {
				evidence := base
				evidence.From, evidence.To = transition.from, transition.to
				if transition.to == "installed" {
					evidence.Identity = installedIdentity
					if evidence.Identity.MTime == nil {
						t.Fatal("native installed identity has no modification time")
					}
					mutated := *evidence.Identity.MTime + 1
					evidence.Identity.MTime = &mutated
					before, readErr := repository.ReadPublicationAttempt(ctx, guard.OperationID)
					if readErr != nil {
						t.Fatal(readErr)
					}
					if got, callErr := repository.GuardedRecordManualLyricsReplacement(ctx, guard, evidence); callErr != nil || got != persistence.PublicationReplacementStale {
						t.Fatalf("installed replacement with altered mtime = %q, %v; want stale", got, callErr)
					}
					unchanged, readErr := repository.ReadPublicationAttempt(ctx, guard.OperationID)
					if readErr != nil || unchanged.Phase != before.Phase || !bytes.Equal(unchanged.MutationJournal, before.MutationJournal) {
						t.Fatalf("rejected installed mtime mutation changed phase/journal: %v", readErr)
					}
					evidence.Identity = installedIdentity
				}
				if got, callErr := repository.GuardedRecordManualLyricsReplacement(ctx, guard, evidence); callErr != nil || got != persistence.PublicationReplacementRecorded {
					t.Fatalf("native DB-only replacement %s->%s = %q, %v", transition.from, transition.to, got, callErr)
				}
			}
			completed, err := repository.ReadPublicationAttempt(ctx, guard.OperationID)
			if err != nil {
				t.Fatal(err)
			}
			var completedEntries []json.RawMessage
			if err := json.Unmarshal(completed.MutationJournal, &completedEntries); err != nil || len(completedEntries) != 11 {
				t.Fatalf("exposure plus replacement journal has %d entries, %v; want 11", len(completedEntries), err)
			}
			if got, callErr := repository.RecordPublicationReplacement(ctx, guard, generic); got != persistence.PublicationReplacementStale {
				t.Fatalf("replacement replay = %q, %v; want stale", got, callErr)
			}
			stillCompleted, err := repository.ReadPublicationAttempt(ctx, guard.OperationID)
			if err != nil || !bytes.Equal(stillCompleted.MutationJournal, completed.MutationJournal) {
				t.Fatalf("rejected replacement replay changed journal: %v", err)
			}
		}
		scope.releaseSynchronousWork(reservation)
		return
	}
	if continuous && !nestedPath {
		frozen, err := persistence.DecodePublicationInputs(attempt.FrozenInputs)
		if err != nil || !reflect.DeepEqual(frozen, input) {
			t.Fatalf("validated owner changed frozen publication inputs: %v", err)
		}
		var transitions []struct {
			Kind        string                                  `json:"kind"`
			From        string                                  `json:"from"`
			To          string                                  `json:"to"`
			Observation persistence.PublicationStageObservation `json:"observation"`
		}
		if err := json.Unmarshal(attempt.MutationJournal, &transitions); err != nil || len(transitions) != 6 || transitions[4].Kind != "stage_observation" || transitions[4].From != "preparing" || transitions[4].To != "staged" || transitions[5].Kind != "stage_observation" || transitions[5].From != "staged" || transitions[5].To != "validated" || !reflect.DeepEqual(transitions[4].Observation, outcome.FinalObservation) || !reflect.DeepEqual(transitions[5].Observation, outcome.FinalObservation) {
			t.Fatalf("validated owner did not persist both stage acknowledgements: %s, %v", attempt.MutationJournal, err)
		}
		// This is a DB-only admission test after the native owner returned its
		// validated evidence. It does not retain or exercise filesystem authority.
		intent := persistence.PublicationExposureIntent{
			Version: persistence.PublicationExposureVersion, StagedPath: stage.paths.RelativePath,
			TargetCanonicalKey: input.Rewrite.RelativePath, TargetRelativePath: input.Rewrite.RelativePath,
			Expectation: "expected_current", PrepOwner: stage.preparationRecord.OwnerToken,
			OutputRootParent: persistence.PublicationDirectoryIdentity{
				Platform: stage.parentIdentity.Platform, Volume: stage.parentIdentity.Volume, FileID: stage.parentIdentity.FileID,
			},
			TargetParent: persistence.PublicationDirectoryIdentity{
				Platform: stage.parentIdentity.Platform, Volume: stage.parentIdentity.Volume, FileID: stage.parentIdentity.FileID,
			},
		}
		observation := outcome.FinalObservation.Clone()
		currentInputs, currentValid, err := repository.PublicationManualLyricsCurrentInputs(ctx, guard)
		if err != nil || !currentValid || !reflect.DeepEqual(currentInputs, input) {
			t.Fatalf("validated current manual lyrics inputs = valid %v, err %v", currentValid, err)
		}
		currentInputs.DesiredTags["TITLE"][0] = "detached mutation"
		reloadedInputs, reloadedValid, err := repository.PublicationManualLyricsCurrentInputs(ctx, guard)
		if err != nil || !reloadedValid || !reflect.DeepEqual(reloadedInputs, input) {
			t.Fatalf("current manual lyrics inputs were not detached: valid %v, err %v", reloadedValid, err)
		}
		if _, valid, err := repository.PublicationPreparationInputs(ctx, guard); err != nil || valid {
			t.Fatalf("generic preparation reader accepted validated V3 inputs: valid %v, err %v", valid, err)
		}
		if _, valid, err := repository.CheckPublicationRewriteForDelivery(ctx, guard); err != nil || valid {
			t.Fatalf("preparation rewrite checker accepted validated stage: valid %v, err %v", valid, err)
		}
		var validatedEntries []json.RawMessage
		if err := json.Unmarshal(attempt.MutationJournal, &validatedEntries); err != nil {
			t.Fatal(err)
		}
		for _, count := range []int{4, 5} {
			if _, err := db.ExecContext(ctx, `UPDATE track_publication_attempt SET mutation_journal=?::jsonb WHERE operation_id=?`, encode(validatedEntries[:count]), guard.OperationID); err != nil {
				t.Fatal(err)
			}
			if got, err := repository.BeginManualLyricsExposure(ctx, guard, intent, observation); err != nil || got != persistence.PublicationExposureStale {
				t.Fatalf("truncated %d-entry exposure prefix = %q, %v", count, got, err)
			}
			unchanged, err := repository.ReadPublicationAttempt(ctx, guard.OperationID)
			if err != nil || unchanged == nil || unchanged.Phase != "validated" {
				t.Fatalf("truncated admission changed phase: %v", err)
			}
			var actual, expected any
			if json.Unmarshal(unchanged.MutationJournal, &actual) != nil || json.Unmarshal([]byte(encode(validatedEntries[:count])), &expected) != nil || !reflect.DeepEqual(actual, expected) {
				t.Fatal("truncated admission mutated journal")
			}
		}
		if _, err := db.ExecContext(ctx, `UPDATE track_publication_attempt SET mutation_journal=?::jsonb WHERE operation_id=?`, string(attempt.MutationJournal), guard.OperationID); err != nil {
			t.Fatal(err)
		}
		admitted, err := repository.BeginManualLyricsExposure(ctx, guard, intent, observation)
		if err != nil || admitted != persistence.PublicationExposureRecorded {
			t.Fatalf("manual lyrics exposure admission = %q, %v", admitted, err)
		}
		admittedAttempt, err := repository.ReadPublicationAttempt(ctx, guard.OperationID)
		if err != nil || admittedAttempt.Phase != "exposing" {
			t.Fatalf("manual lyrics exposure phase = %v, %v", admittedAttempt, err)
		}
		if err := json.Unmarshal(admittedAttempt.MutationJournal, &records); err != nil || len(records) != 7 {
			t.Fatalf("manual lyrics exposure journal length = %d, %v", len(records), err)
		}
		exposingInputs, exposingValid, err := repository.PublicationManualLyricsCurrentInputs(ctx, guard)
		if err != nil || !exposingValid || !reflect.DeepEqual(exposingInputs, input) {
			t.Fatalf("exposing current manual lyrics inputs = valid %v, err %v", exposingValid, err)
		}
		staleInputGuard := guard
		staleInputGuard.ClaimUUID = uuid.New()
		if _, valid, err := repository.PublicationManualLyricsCurrentInputs(ctx, staleInputGuard); err != nil || valid {
			t.Fatalf("current manual lyrics reader accepted stale delivery guard: valid %v, err %v", valid, err)
		}
		unchangedAfterRead, err := repository.ReadPublicationAttempt(ctx, guard.OperationID)
		if err != nil || unchangedAfterRead.Phase != admittedAttempt.Phase || !bytes.Equal(unchangedAfterRead.MutationJournal, admittedAttempt.MutationJournal) {
			t.Fatalf("current-input reads changed exposure phase/journal: %v", err)
		}
		replayed, err := repository.BeginManualLyricsExposure(ctx, guard, intent, observation)
		if err != nil || replayed != persistence.PublicationExposureAlreadyRecorded {
			t.Fatalf("manual lyrics exposure replay = %q, %v", replayed, err)
		}
		if replayed, err := repository.BeginPublicationExposure(ctx, guard, intent, observation); err != nil || replayed != persistence.PublicationExposureStale {
			t.Fatalf("generic exposure API accepted V3 replay = %q, %v", replayed, err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE track_publication_attempt SET frozen_inputs='{"version":3}'::jsonb WHERE operation_id=?`, guard.OperationID); err != nil {
			t.Fatal(err)
		}
		if replayed, err := repository.BeginPublicationExposure(ctx, guard, intent, observation); err != nil || replayed != persistence.PublicationExposureStale {
			t.Fatalf("generic exposure accepted malformed V3 replay = %q, %v", replayed, err)
		}
		malformedAttempt, err := repository.ReadPublicationAttempt(ctx, guard.OperationID)
		if err != nil || malformedAttempt.Phase != "exposing" || !bytes.Equal(malformedAttempt.MutationJournal, admittedAttempt.MutationJournal) {
			t.Fatalf("malformed generic replay mutated phase/journal: %v", err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE track_publication_attempt SET frozen_inputs=?::jsonb WHERE operation_id=?`, string(admittedAttempt.FrozenInputs), guard.OperationID); err != nil {
			t.Fatal(err)
		}
		assertRejected := func(label string, candidateGuard persistence.PublicationDeliveryGuard, candidateIntent persistence.PublicationExposureIntent, candidateObservation persistence.PublicationStageObservation) {
			t.Helper()
			got, err := repository.BeginManualLyricsExposure(ctx, candidateGuard, candidateIntent, candidateObservation)
			if err != nil || got != persistence.PublicationExposureStale {
				t.Fatalf("%s admission = %q, %v", label, got, err)
			}
		}
		alteredObservation := observation.Clone()
		alteredObservation.PayloadSHA256 = fmt.Sprintf("%064x", 2)
		assertRejected("altered observation", guard, intent, alteredObservation)
		alteredIntent := intent
		alteredIntent.PrepOwner = uuid.New()
		assertRejected("altered owner", guard, alteredIntent, observation)
		staleGuard := guard
		staleGuard.Epoch = uuid.New()
		assertRejected("stale guard", staleGuard, intent, observation)
		unchanged, err := repository.ReadPublicationAttempt(ctx, guard.OperationID)
		if err != nil || unchanged.Phase != "exposing" || !bytes.Equal(unchanged.MutationJournal, admittedAttempt.MutationJournal) {
			t.Fatalf("rejected admission changed exposure journal/phase: %v", err)
		}
		currentAfter, err := os.ReadFile(filepath.Join(fixture.output, input.Rewrite.RelativePath))
		if err != nil || !bytes.Equal(currentAfter, original) {
			t.Fatalf("DB-only admission changed current bytes: %v", err)
		}
		scope.releaseSynchronousWork(reservation)
		return
	}
	observation := outcome.FinalObservation.Clone()
	wrong := observation.Clone()
	wrong.PayloadSHA256 = fmt.Sprintf("%064x", 1)
	if ack, err := repository.GuardedAdvanceManualLyricsStage(ctx, guard, "preparing", wrong); err != nil || ack != persistence.ManualLyricsStageStale {
		t.Fatalf("mismatched stage observation = %q, %v", ack, err)
	}
	for _, from := range []string{"preparing", "staged"} {
		if ack, err := repository.GuardedAdvanceManualLyricsStage(ctx, guard, from, observation); err != nil || ack != persistence.ManualLyricsStageAdvanced {
			t.Fatalf("advance from %s = %q, %v", from, ack, err)
		}
		if ack, err := repository.GuardedAdvanceManualLyricsStage(ctx, guard, from, observation); err != nil || ack != persistence.ManualLyricsStageAlreadyRecorded {
			t.Fatalf("replay from %s = %q, %v", from, ack, err)
		}
		phase := "staged"
		if from == "staged" {
			phase = "validated"
		}
		before, err := repository.ReadPublicationAttempt(ctx, guard.OperationID)
		if err != nil {
			t.Fatal(err)
		}
		// Deliberately inconsistent test state must not authorize another append.
		resetPhase := func(phase string) {
			t.Helper()
			if _, err := db.ExecContext(ctx, `WITH changed AS (UPDATE track_publication_attempt SET phase=? WHERE operation_id=? RETURNING operation_id) UPDATE operation SET stage=? WHERE id IN (SELECT operation_id FROM changed)`, phase, guard.OperationID, phase); err != nil {
				t.Fatal(err)
			}
		}
		resetPhase("preparing")
		if ack, err := repository.GuardedAdvanceManualLyricsStage(ctx, guard, "preparing", observation); err != nil || ack != persistence.ManualLyricsStageStale {
			t.Fatalf("preparing with existing suffix = %q, %v", ack, err)
		}
		after, err := repository.ReadPublicationAttempt(ctx, guard.OperationID)
		if err != nil || !bytes.Equal(before.MutationJournal, after.MutationJournal) {
			t.Fatalf("invalid frontier mutated journal: %v", err)
		}
		resetPhase(phase)
	}
	advanced, err := repository.ReadPublicationAttempt(ctx, guard.OperationID)
	if err != nil || advanced.Phase != "validated" || !bytes.Equal(advanced.FrozenInputs, attempt.FrozenInputs) {
		t.Fatalf("invalid validated attempt: %+v, %v", advanced, err)
	}
	if err := json.Unmarshal(advanced.MutationJournal, &records); err != nil || len(records) != 6 {
		t.Fatalf("invalid validated journal: %s, %v", advanced.MutationJournal, err)
	}
	scope.releaseSynchronousWork(reservation)
}

var errInjectedReplacementCommit = errors.New("injected replacement commit failure")
var errInjectedInstalledAckLost = errors.New("injected installed acknowledgement loss")

type manualLyricsInstalledAckLostRepository struct {
	*persistence.SetupManagerRepository
	installedCalls int
	commitCalls    int
}

func (repository *manualLyricsInstalledAckLostRepository) GuardedRecordManualLyricsReplacement(ctx context.Context, guard persistence.PublicationDeliveryGuard, evidence persistence.PublicationReplacementEvidence) (persistence.PublicationReplacementOutcome, error) {
	if evidence.To == "installed" {
		repository.installedCalls++
		if outcome, err := repository.SetupManagerRepository.GuardedRecordManualLyricsReplacement(ctx, guard, evidence); err != nil || outcome != persistence.PublicationReplacementRecorded {
			return persistence.PublicationReplacementStale, err
		}
		return persistence.PublicationReplacementStale, errInjectedInstalledAckLost
	}
	return repository.SetupManagerRepository.GuardedRecordManualLyricsReplacement(ctx, guard, evidence)
}

func (repository *manualLyricsInstalledAckLostRepository) CommitReplacementPublication(ctx context.Context, guard persistence.PublicationDeliveryGuard) (bool, error) {
	repository.commitCalls++
	return repository.SetupManagerRepository.CommitReplacementPublication(ctx, guard)
}

type manualLyricsCleanupIntentFailureRepository struct {
	*persistence.SetupManagerRepository
	cleanupCalls int
	commitCalls  int
}

func (repository *manualLyricsCleanupIntentFailureRepository) BeginPublicationReplacementCleanup(context.Context, persistence.PublicationDeliveryGuard, persistence.PublicationReplacementCleanupWitness) (persistence.PublicationReplacementCleanupOutcome, error) {
	repository.cleanupCalls++
	return persistence.PublicationReplacementCleanupStale, errInjectedCleanupIntentFailure
}

func (repository *manualLyricsCleanupIntentFailureRepository) CommitReplacementPublication(ctx context.Context, guard persistence.PublicationDeliveryGuard) (bool, error) {
	repository.commitCalls++
	return repository.SetupManagerRepository.CommitReplacementPublication(ctx, guard)
}

var errInjectedCleanupIntentFailure = errors.New("injected cleanup intent failure")

type manualLyricsReplacementCommitFailureRepository struct {
	*persistence.SetupManagerRepository
	calls int
}

func (repository *manualLyricsReplacementCommitFailureRepository) CommitReplacementPublication(context.Context, persistence.PublicationDeliveryGuard) (bool, error) {
	repository.calls++
	return false, errInjectedReplacementCommit
}

func assertReplacementStillExposed(t *testing.T, ctx context.Context, db *bun.DB, repository *persistence.SetupManagerRepository, guard persistence.PublicationDeliveryGuard, input persistence.PublicationInputs) {
	t.Helper()
	projection, err := repository.ReadManualLyricsReceiptProjection(ctx, guard.ExpectedPositionID)
	if err != nil || projection.CurrentGeneration == nil || *projection.CurrentGeneration != input.Rewrite.CurrentGeneration || projection.Receipt != nil {
		t.Fatalf("unsupported replacement changed G0 or created a receipt: %+v, %v", projection, err)
	}
	assertReplacementDBCounts(t, ctx, db, guard.OperationID, guard.ExpectedPositionID, 1, 1, 0, 0, 0)
}

func assertReplacementSettledInDB(t *testing.T, ctx context.Context, db *bun.DB, repository *persistence.SetupManagerRepository, guard persistence.PublicationDeliveryGuard, input persistence.PublicationInputs) {
	t.Helper()
	projection, err := repository.ReadManualLyricsReceiptProjection(ctx, guard.ExpectedPositionID)
	if err != nil || projection.Pending || projection.CurrentGeneration == nil || *projection.CurrentGeneration != guard.CandidateGeneration || projection.Receipt == nil || projection.Receipt.ConfirmedGeneration != input.Rewrite.CurrentGeneration || projection.Receipt.PublishedGeneration != guard.CandidateGeneration {
		t.Errorf("settled replacement receipt/current projection = %+v, %v", projection, err)
	}
	assertReplacementDBCounts(t, ctx, db, guard.OperationID, guard.ExpectedPositionID, 0, 0, 0, 0, 1)
	snapshot, err := repository.LoadPublicationStartupSnapshot(ctx)
	if err != nil {
		t.Errorf("read settled publication startup snapshot: %v", err)
	} else {
		for _, record := range snapshot.Records {
			if record.OperationID == guard.OperationID {
				t.Errorf("settled operation remains in startup snapshot: %+v", record)
			}
		}
	}
}

func assertAutomaticRetrySettledInDB(t *testing.T, ctx context.Context, db *bun.DB, guard persistence.PublicationDeliveryGuard, input persistence.PublicationInputs, fallback bool) {
	t.Helper()
	if input.AutomaticLyricsRetry == nil || input.ManualLyrics != nil {
		t.Errorf("settled retry input mixed automatic and manual authority")
		return
	}
	var generation uuid.UUID
	var raw []byte
	if err := db.NewRaw(`SELECT generation,initial_lyrics FROM track_publication WHERE position_id=?`, guard.ExpectedPositionID).Scan(ctx, &generation, &raw); err != nil {
		t.Errorf("read settled automatic retry projection: %v", err)
		return
	}
	var projection persistence.PublicationInitialLyricsProjection
	digest := sha256.Sum256([]byte(automaticRetryLyrics))
	wantOutcome, wantWarning := persistence.PublicationOptionalLyricsExactRoundTrip, ""
	if fallback {
		wantOutcome, wantWarning = persistence.PublicationOptionalLyricsRebuiltWithoutLyrics, "readback_mismatch"
	}
	if err := json.Unmarshal(raw, &projection); err != nil || generation != guard.CandidateGeneration || projection.Version != 1 || projection.Generation != guard.CandidateGeneration || projection.Source != "lrclib" || projection.SHA256 != fmt.Sprintf("%x", digest) || projection.Outcome != wantOutcome || projection.OutcomeWarningCode != wantWarning || projection.AuthorityMetadataRevision != input.AutomaticLyricsRetry.ExpectedMetadataRevision || projection.Authority != "automatic" {
		t.Errorf("settled retry projection = %+v generation=%s err=%v", projection, generation, err)
	}
	for _, check := range []struct {
		name  string
		query string
	}{
		{name: "manual lyrics receipts", query: `SELECT count(*) FROM publication_manual_lyrics_receipt WHERE position_id=?`},
		{name: "source holds", query: `SELECT count(*) FROM publication_source_hold WHERE operation_id=?`},
	} {
		argument := any(guard.OperationID)
		if check.name == "manual lyrics receipts" {
			argument = guard.ExpectedPositionID
		}
		var count int
		if err := db.NewRaw(check.query, argument).Scan(ctx, &count); err != nil || count != 0 {
			t.Errorf("automatic retry %s count = %d, %v; want zero", check.name, count, err)
		}
	}
}

func assertReplacementDBCounts(t *testing.T, ctx context.Context, db *bun.DB, operationID, positionID uuid.UUID, wantAttempts, wantClaims, wantSourceHolds, wantToolHolds, wantReceipts int) {
	t.Helper()
	for _, check := range []struct {
		label string
		query string
		want  int
	}{
		{"attempts", `SELECT count(*) FROM track_publication_attempt WHERE operation_id=?`, wantAttempts},
		{"execution claims", `SELECT count(*) FROM publication_execution_claim WHERE operation_id=?`, wantClaims},
		{"source holds", `SELECT count(*) FROM publication_source_hold WHERE operation_id=?`, wantSourceHolds},
		{"tool holds", `SELECT count(*) FROM operation_tool_read_hold WHERE operation_id=?`, wantToolHolds},
		{"receipts", `SELECT count(*) FROM publication_manual_lyrics_receipt WHERE position_id=?`, wantReceipts},
	} {
		var got int
		argument := operationID
		if check.label == "receipts" {
			argument = positionID
		}
		if err := db.NewRaw(check.query, argument).Scan(ctx, &got); err != nil || got != check.want {
			t.Errorf("%s count = %d, %v; want %d", check.label, got, err, check.want)
		}
	}
}

func equalOptionalSize(a, b *int64) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

// appendNativeTestPicture serializes a FLAC PICTURE metadata block, leaving
// the original STREAMINFO/audio payload untouched.
func appendNativeTestPicture(t *testing.T, data []byte) []byte {
	t.Helper()
	var pngData bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	img.Set(1, 1, color.RGBA{B: 255, A: 255})
	if err := png.Encode(&pngData, img); err != nil {
		t.Fatal(err)
	}
	imageData := pngData.Bytes()
	var body bytes.Buffer
	_ = binary.Write(&body, binary.BigEndian, uint32(3))
	writeText := func(value string) {
		_ = binary.Write(&body, binary.BigEndian, uint32(len(value)))
		body.WriteString(value)
	}
	writeText("image/png")
	writeText("正面の絵🎵")
	for _, value := range []uint32{2, 2, 32, 1, uint32(len(imageData))} {
		_ = binary.Write(&body, binary.BigEndian, value)
	}
	body.Write(imageData)
	if len(data) < 8 || string(data[:4]) != "fLaC" {
		t.Fatal("invalid FLAC fixture")
	}
	out := append([]byte(nil), data[:4]...)
	for offset := 4; ; {
		header := data[offset]
		length := int(data[offset+1])<<16 | int(data[offset+2])<<8 | int(data[offset+3])
		end := offset + 4 + length
		if end > len(data) {
			t.Fatal("malformed FLAC metadata")
		}
		if header&0x80 != 0 {
			out = append(out, header&0x7f, data[offset+1], data[offset+2], data[offset+3])
			out = append(out, data[offset+4:end]...)
			data = data[end:]
			break
		}
		out = append(out, data[offset:end]...)
		offset = end
	}
	block := body.Bytes()
	out = append(out, 0x80|6, byte(len(block)>>16), byte(len(block)>>8), byte(len(block)))
	out = append(out, block...)
	return append(out, data...)
}
