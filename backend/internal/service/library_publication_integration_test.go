//go:build integration

package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	riverdatabasesql "github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

type libraryPublicationTestInserter struct{}

func (libraryPublicationTestInserter) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, _ *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO river_job(kind,args,state) VALUES ($1,$2::jsonb,'available') RETURNING id`, args.Kind(), string(raw)).Scan(&id); err != nil {
		return nil, err
	}
	return &rivertype.JobInsertResult{Job: &rivertype.JobRow{ID: id}}, nil
}

// This proves server-owned admission, not filesystem execution; delivery has
// separate native PostgreSQL integration coverage.
func TestLibraryPublicationsApplySavedLyricsPostgreSQL(t *testing.T) {
	for _, state := range []librarymetadata.ValueState{librarymetadata.StateValue, librarymetadata.StateExplicitEmpty} {
		t.Run(string(state), func(t *testing.T) {
			ctx := context.Background()
			db := testpostgres.OpenMigrated(t)
			migrator, err := rivermigrate.New(riverdatabasesql.New(db.DB), nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
				t.Fatal(err)
			}
			repo := persistence.NewSetupManagerRepository(db)
			encode := func(v any) string {
				t.Helper()
				raw, err := json.Marshal(v)
				if err != nil {
					t.Fatal(err)
				}
				return string(raw)
			}
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := db.ExecContext(ctx, query, args...); err != nil {
					t.Fatal(err)
				}
			}
			release, track, position, generation := uuid.New(), uuid.New(), uuid.New(), uuid.New()
			var epoch uuid.UUID
			if err := db.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &epoch); err != nil {
				t.Fatal(err)
			}
			exec(`INSERT INTO local_release(id,title) VALUES (?,'release')`, release)
			exec(`INSERT INTO local_track(id,title) VALUES (?,'track')`, track)
			lyrics := "[00:01.00]saved manual lyrics\r\n"
			var value any = lyrics
			if state == librarymetadata.StateExplicitEmpty {
				value = nil
			}
			envelope := librarymetadata.Envelope{Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopePosition, Desired: map[string]librarymetadata.FieldValue{
				"synced_lyrics": librarymetadata.ManualFieldValue(state, value, state == librarymetadata.StateValue),
			}}
			exec(`INSERT INTO release_track(id,release_id,track_id,position,metadata,metadata_revision) VALUES (?,?,?,0,?::jsonb,7)`, position, release, track, encode(map[string]any{"local_metadata": envelope}))
			size, mtime := int64(42), int64(100)
			lossless := true
			ownership := persistence.PublicationCurrentOwnership{Version: 1, Identity: persistence.PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: strings.Repeat("a", 64)}}
			audio := persistence.PublicationCurrentAudio{Version: 1, Facts: persistence.PersistedAudioFacts{StreamIndex: 0, Codec: "flac", Lossless: &lossless, Container: "flac", SupportedSourceContainer: true}}
			tags := map[string][]string{"TITLE": {"actual current title"}, "ARTIST": {"Artist", "Guest"}, "COMMENT": {}}
			assets := persistence.PublicationAssetOutcomes{Version: 1, Cover: persistence.PublicationCoverOutcome{State: persistence.PublicationAssetAbsent}, Lyrics: persistence.PublicationLyricsOutcome{State: persistence.PublicationAssetAbsent}}
			exec(`INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at) VALUES (?,?,?,'Current.flac',?::jsonb,?::jsonb,?::jsonb,?::jsonb,1,now())`, position, epoch, generation, encode(ownership), encode(audio), encode(persistence.PublicationCommitMetadata{Version: 1, Tags: tags}), encode(assets))
			claim := uuid.New()
			exec(`INSERT INTO publication_namespace_claim(id,epoch,key_digest,canonical_key,relative_path,node_kind,owner_position_id) VALUES (?,?,decode(repeat('ab',32),'hex'),'current.flac','Current.flac','file',?)`, claim, epoch, position)
			exec(`INSERT INTO publication_namespace_reservation(namespace_claim_id,position_id,generation) VALUES (?,?,?)`, claim, position, generation)
			installationID := uuid.New()
			verified := time.Now().UTC()
			installation := &persistence.ToolInstallation{ID: installationID, PackageKind: "ffmpeg", PlatformGOOS: runtime.GOOS, PlatformGOARCH: runtime.GOARCH, SourceName: "fixture", ReleaseIdentity: "1", RelativePath: filepath.Join("ffmpeg", "1"), State: "ready", VerifiedAt: &verified, ArtifactIdentities: json.RawMessage(`{}`), ExecutableVersions: json.RawMessage(`{"ffmpeg":"ffmpeg version 1","ffprobe":"ffprobe version 1"}`)}
			if err := repo.CreateInstallation(ctx, installation); err != nil {
				t.Fatal(err)
			}
			exec(`INSERT INTO app_setting(setting_name,setting_value,updated_at) VALUES (?, ?, now())`, settings.ActiveFFmpegInstallationKey, installationID.String())
			registry := settings.New(persistence.NewSettingsRepository(db), nil)
			service := NewLibraryPublications(repo, registry, libraryPublicationTestInserter{}, tools.Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH})
			view, err := service.Read(ctx, release, position)
			if err != nil || !view.CanApplyManualLyrics || view.ManualLyrics == nil || !view.ManualLyrics.Pending {
				t.Fatalf("pending saved lyrics: %+v, %v", view, err)
			}
			request := LibraryPublicationApplyManualLyricsRequest{ExpectedEpoch: epoch, ExpectedGeneration: generation, ExpectedMetadataRevision: 7}
			stale := request
			stale.ExpectedMetadataRevision--
			if _, err := service.ApplyManualLyrics(ctx, release, position, stale); !errors.Is(err, ErrLibraryPublicationConflict) {
				t.Fatalf("stale revision: %v", err)
			}
			operationID, err := service.ApplyManualLyrics(ctx, release, position, request)
			if err != nil {
				t.Fatal(err)
			}
			attempt, err := repo.ReadPublicationAttempt(ctx, operationID)
			if err != nil || attempt == nil {
				t.Fatalf("admitted attempt: %v", err)
			}
			frozen, err := persistence.DecodePublicationInputs(attempt.FrozenInputs)
			if err != nil || frozen.Audio != nil || frozen.ManualLyrics == nil || frozen.ManualLyrics.State != string(state) || frozen.ManualLyrics.ConfirmedCurrentGeneration != generation || frozen.ManualLyrics.ExpectedMetadataRevision != 7 || len(frozen.Tools) != 2 || frozen.DesiredTags["TITLE"][0] != tags["TITLE"][0] {
				t.Fatalf("source-free frozen admission: %+v, %v", frozen, err)
			}
			if state == librarymetadata.StateValue && frozen.ManualLyrics.Value != lyrics {
				t.Fatal("saved lyrics bytes changed")
			}
			if _, err := service.ApplyManualLyrics(ctx, release, position, request); !errors.Is(err, ErrLibraryPublicationConflict) {
				t.Fatalf("duplicate admission: %v", err)
			}
			view, err = service.Read(ctx, release, position)
			if err != nil || view.ActiveAttempt == nil || view.CanApplyManualLyrics {
				t.Fatalf("busy publication: %+v, %v", view, err)
			}
			for table, want := range map[string]int{"publication_source_hold": 0, "operation_tool_read_hold": 1, "publication_execution_claim": 0} {
				var count int
				if err := db.NewRaw(`SELECT count(*) FROM `+table+` WHERE operation_id=?`, operationID).Scan(ctx, &count); err != nil || count != want {
					t.Fatalf("%s count=%d, %v", table, count, err)
				}
			}
		})
	}
}
