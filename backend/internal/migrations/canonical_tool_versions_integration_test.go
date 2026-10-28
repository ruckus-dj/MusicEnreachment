//go:build integration

package migrations_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/migrations"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
)

func TestCanonicalToolVersionsBackfillAndRetainedSelectionRoundTripPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.Open(t)
	testpostgres.Reset(t, db)
	collection, err := migrations.Collection()
	if err != nil {
		t.Fatal(err)
	}
	prior, target := migrate.NewMigrations(), migrate.NewMigrations()
	for _, m := range collection.Sorted() {
		if m.Name < "20261031000001" {
			prior.Add(m)
		} else if m.Name == "20261031000001" {
			target.Add(m)
		}
	}
	if len(target.Sorted()) != 1 {
		t.Fatalf("target migration count = %d", len(target.Sorted()))
	}
	pm := migrate.NewMigrator(db, prior, migrate.WithMarkAppliedOnSuccess(true))
	if err := pm.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pm.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	ffmpegID, windowsID, fpcalcID, inactiveID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	verified := time.Now().UTC()
	seed := func(id uuid.UUID, kind, goos, arch, release, path, state string, output any) {
		raw, e := json.Marshal(output)
		if e != nil {
			t.Fatal(e)
		}
		_, e = db.ExecContext(ctx, `INSERT INTO tool_installation(id,package_kind,platform_goos,platform_goarch,source_name,release_identity,relative_path,state,executable_versions,verified_at)
		VALUES (?,?,?,?,?,?,?, ?,?::jsonb,?)`, id, kind, goos, arch, "test", release, path, state, string(raw), verified)
		if e != nil {
			t.Fatalf("seed %s: %v", id, e)
		}
	}
	seed(ffmpegID, "ffmpeg", "linux", "amd64", "linux-one", "ffmpeg/linux-one", "ready", map[string]string{"ffmpeg": "ffmpeg version 7.1\nother\n", "ffprobe": "ffprobe version 6.9\nother\n"})
	seed(windowsID, "ffmpeg", "windows", "amd64", "win-one", `ffmpeg\win-one`, "ready", map[string]string{"ffmpeg.exe": "ffmpeg version 8.2\nother", "ffprobe.exe": "ffprobe version 8.1\nother"})
	seed(fpcalcID, "fpcalc", "linux", "amd64", "fp-one", "fpcalc/fp-one", "ready", map[string]string{"fpcalc": "fpcalc version 1.2\nother"})
	seed(inactiveID, "fpcalc", "linux", "amd64", "inactive", "fpcalc/inactive", "failed", map[string]string{})
	positionID, releaseID, trackID, opID, epoch := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, seed := range []struct {
		q    string
		args []any
	}{
		{`INSERT INTO local_release(id,title,metadata) VALUES (?,'release','{}')`, []any{releaseID}},
		{`INSERT INTO local_track(id,title,metadata) VALUES (?,'track','{}')`, []any{trackID}},
		{`INSERT INTO release_track(id,release_id,track_id,medium,position,metadata) VALUES (?,?,?,0,0,'{}')`, []any{positionID, releaseID, trackID}},
	} {
		if _, err := db.ExecContext(ctx, seed.q, seed.args...); err != nil {
			t.Fatalf("seed publication identity: %v", err)
		}
	}
	snapshot, _ := json.Marshal(map[string]any{"version": 1, "position_id": positionID.String(), "output_epoch": epoch.String(), "intent_kind": "audio"})
	selections := make([]map[string]any, 0, 2)
	for _, executable := range []string{"ffmpeg", "ffprobe"} {
		banner, version := "ffmpeg version 7.1\nother", "7.1"
		if executable == "ffprobe" {
			banner, version = "ffprobe version 6.9\nother", "6.9"
		}
		selections = append(selections, map[string]any{"package_kind": "ffmpeg", "installation_id": ffmpegID.String(), "relative_path": "ffmpeg/linux-one", "executable": executable, "version": version, "version_banner": banner})
	}
	toolsRaw, _ := json.Marshal(selections)
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO operation(id,kind,state,stage,input_snapshot) VALUES (?,'publish_track_v1','queued','queued',?::jsonb)`, opID, string(snapshot)); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO track_publication_attempt(operation_id,position_id,epoch,candidate_generation,intent_kind,phase,frozen_inputs,namespace_manifest)
			VALUES (?,?,?,?,'audio','queued',jsonb_build_object('tools',?::jsonb),'{}'::jsonb)`, opID, positionID, epoch, uuid.New(), string(toolsRaw))
		return err
	}); err != nil {
		t.Fatalf("seed retained selection and matching publication operation: %v", err)
	}
	tm := migrate.NewMigrator(db, target, migrate.WithMarkAppliedOnSuccess(true))
	if err := tm.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := tm.Migrate(ctx); err != nil {
		t.Fatalf("apply canonical tool migration: %v", err)
	}
	for id, want := range map[uuid.UUID]string{ffmpegID: "ffmpeg 7.1 + ffprobe 6.9", windowsID: "ffmpeg 8.2 + ffprobe 8.1", fpcalcID: "1.2"} {
		var got string
		if err := db.NewRaw(`SELECT version FROM tool_installation WHERE id=?`, id).Scan(ctx, &got); err != nil || got != want {
			t.Fatalf("version for %s = %q, %v; want %q", id, got, err, want)
		}
	}
	var inactive *string
	if err := db.NewRaw(`SELECT version FROM tool_installation WHERE id=?`, inactiveID).Scan(ctx, &inactive); err != nil || inactive != nil {
		t.Fatalf("inactive version = %v, %v; want NULL", inactive, err)
	}
	var migrated string
	if err := db.NewRaw(`SELECT frozen_inputs #>> '{tools,0,version}' FROM track_publication_attempt WHERE operation_id=?`, opID).Scan(ctx, &migrated); err != nil || migrated != "ffmpeg 7.1 + ffprobe 6.9" {
		t.Fatalf("migrated selection = %q, %v", migrated, err)
	}
	for _, tc := range []struct {
		name string
		sql  string
	}{
		{name: "omitted", sql: `frozen_inputs #- '{tools,0,version}'`},
		{name: "null", sql: `jsonb_set(frozen_inputs,'{tools,0,version}','null'::jsonb)`},
		{name: "mismatch", sql: `jsonb_set(frozen_inputs,'{tools,0,version}','"ffmpeg 99 + ffprobe 98"'::jsonb)`},
	} {
		if _, err := db.ExecContext(ctx, `UPDATE track_publication_attempt SET frozen_inputs=`+tc.sql+` WHERE operation_id=?`, opID); err != nil {
			t.Fatal(err)
		}
		if _, err := tm.Rollback(ctx); err == nil {
			t.Errorf("rollback accepted %s canonical selection", tc.name)
		}
		var currentVersionCount int
		if err := db.NewRaw(`SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='tool_installation' AND column_name='version'`).Scan(ctx, &currentVersionCount); err != nil {
			t.Fatal(err)
		}
		if currentVersionCount != 1 {
			t.Fatalf("failed rollback for %s changed schema", tc.name)
		}
		if _, err := db.ExecContext(ctx, `UPDATE track_publication_attempt SET frozen_inputs=jsonb_set(frozen_inputs,'{tools,0,version}','"ffmpeg 7.1 + ffprobe 6.9"'::jsonb) WHERE operation_id=?`, opID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tm.Rollback(ctx); err != nil {
		t.Fatalf("rollback canonical migration: %v", err)
	}
	var restored string
	if err := db.NewRaw(`SELECT frozen_inputs #>> '{tools,0,version}' FROM track_publication_attempt WHERE operation_id=?`, opID).Scan(ctx, &restored); err != nil || restored != "7.1" {
		t.Fatalf("restored selection = %q, %v", restored, err)
	}
	var legacy string
	if err := db.NewRaw(`SELECT executable_versions::text FROM tool_installation WHERE id=?`, ffmpegID).Scan(ctx, &legacy); err != nil || !strings.Contains(legacy, "ffprobe version 6.9") {
		t.Fatalf("raw evidence after rollback = %s, %v", legacy, err)
	}
	for _, tc := range []struct {
		name string
		sql  string
	}{
		{name: "omitted", sql: `frozen_inputs #- '{tools,0,version}'`},
		{name: "null", sql: `jsonb_set(frozen_inputs,'{tools,0,version}','null'::jsonb)`},
		{name: "mismatch", sql: `jsonb_set(frozen_inputs,'{tools,0,version}','"99"'::jsonb)`},
	} {
		if _, err := db.ExecContext(ctx, `UPDATE track_publication_attempt SET frozen_inputs=`+tc.sql+` WHERE operation_id=?`, opID); err != nil {
			t.Fatalf("set %s frozen version: %v", tc.name, err)
		}
		if _, err := tm.Migrate(ctx); err == nil {
			t.Errorf("upgrade accepted %s frozen version", tc.name)
		}
		var oldColumn, newColumn int
		if err := db.NewRaw(`SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='tool_installation' AND column_name='executable_versions'`).Scan(ctx, &oldColumn); err != nil {
			t.Fatal(err)
		}
		if err := db.NewRaw(`SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='tool_installation' AND column_name='version'`).Scan(ctx, &newColumn); err != nil {
			t.Fatal(err)
		}
		if oldColumn != 1 || newColumn != 0 {
			t.Fatalf("failed %s upgrade was not atomic: legacy column=%d new column=%d", tc.name, oldColumn, newColumn)
		}
		if _, err := db.ExecContext(ctx, `UPDATE track_publication_attempt SET frozen_inputs=jsonb_set(frozen_inputs,'{tools,0,version}','"7.1"'::jsonb) WHERE operation_id=?`, opID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCanonicalToolVersionsRejectsMalformedReadyEvidenceAtomicallyPostgreSQL(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		output string
	}{
		{name: "missing executable", output: `{}`},
		{name: "missing probe", output: `{"ffmpeg":"ffmpeg version 7.1"}`},
		{name: "null executable", output: `{"ffmpeg":null,"ffprobe":"ffprobe version 6.9"}`},
		{name: "null probe", output: `{"ffmpeg":"ffmpeg version 7.1","ffprobe":null}`},
		{name: "malformed banner", output: `{"ffmpeg":"no version token","ffprobe":"ffprobe version 6.9"}`},
		{name: "arbitrary version occurrence", output: `{"ffmpeg":"garbage version 7.1","ffprobe":"ffprobe version 6.9"}`},
		{name: "later line version", output: `{"ffmpeg":"garbage banner\nffmpeg version 7.1","ffprobe":"ffprobe version 6.9"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			db := testpostgres.Open(t)
			testpostgres.Reset(t, db)
			collection, err := migrations.Collection()
			if err != nil {
				t.Fatal(err)
			}
			prior, target := migrate.NewMigrations(), migrate.NewMigrations()
			for _, m := range collection.Sorted() {
				if m.Name < "20261031000001" {
					prior.Add(m)
				} else if m.Name == "20261031000001" {
					target.Add(m)
				}
			}
			pm := migrate.NewMigrator(db, prior, migrate.WithMarkAppliedOnSuccess(true))
			if err := pm.Init(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := pm.Migrate(ctx); err != nil {
				t.Fatalf("apply valid pre-migration baseline: %v", err)
			}
			id := uuid.New()
			if _, err := db.ExecContext(ctx, `INSERT INTO tool_installation
				(id,package_kind,platform_goos,platform_goarch,source_name,release_identity,relative_path,state,executable_versions,verified_at)
				VALUES (?,'ffmpeg','linux','amd64','test','release','ffmpeg/release','ready',?::jsonb,now())`, id, tc.output); err != nil {
				t.Fatal(err)
			}
			tm := migrate.NewMigrator(db, target, migrate.WithMarkAppliedOnSuccess(true))
			if err := tm.Init(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := tm.Migrate(ctx); err == nil {
				t.Fatal("migration accepted malformed ready verification evidence")
			}
			var oldColumn, newColumn int
			if err := db.NewRaw(`SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='tool_installation' AND column_name='executable_versions'`).Scan(ctx, &oldColumn); err != nil {
				t.Fatal(err)
			}
			if err := db.NewRaw(`SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='tool_installation' AND column_name='version'`).Scan(ctx, &newColumn); err != nil {
				t.Fatal(err)
			}
			if oldColumn != 1 || newColumn != 0 {
				t.Fatalf("failed migration was not atomic: legacy column=%d new column=%d", oldColumn, newColumn)
			}
		})
	}
}
