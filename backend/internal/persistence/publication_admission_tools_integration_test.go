//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestPublicationAdmissionPinsFFmpegAndHoldsInstallationPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.OpenMigrated(t)
	initializeRiver(t, ctx, db)
	releaseID, trackID, positionID := uuid.New(), uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO local_release(id,title) VALUES (?,'tools admission')`, releaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?,'track')`, trackID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO release_track(id,release_id,track_id,position) VALUES (?,?,?,0)`, positionID, releaseID, trackID); err != nil {
		t.Fatal(err)
	}
	makePosition := func(position int) uuid.UUID {
		t.Helper()
		id, track := uuid.New(), uuid.New()
		if _, err := db.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?,'track')`, track); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO release_track(id,release_id,track_id,position) VALUES (?,?,?,?)`, id, releaseID, track, position); err != nil {
			t.Fatal(err)
		}
		return id
	}
	var epoch uuid.UUID
	if err := db.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &epoch); err != nil {
		t.Fatal(err)
	}
	installationID := uuid.New()
	verified := time.Now().UTC()
	executableNames := tools.ExpectedExecutables(tools.PackageFFmpeg, runtime.GOOS)
	versions, _ := json.Marshal(map[string]string{executableNames[0]: "ffmpeg version 7.0", executableNames[1]: "ffprobe version 7.0"})
	installation := &ToolInstallation{ID: installationID, PackageKind: "ffmpeg", PlatformGOOS: runtime.GOOS, PlatformGOARCH: runtime.GOARCH, SourceName: "fixture", ReleaseIdentity: "release-1", State: "ready", ExecutableVersions: versions, ArtifactIdentities: json.RawMessage(`{}`), VerifiedAt: &verified}
	separatorPath := "ffmpeg/release-1"
	if runtime.GOOS == "windows" {
		separatorPath = `ffmpeg\release-1`
	}
	installation.RelativePath = separatorPath
	if err := NewSetupManagerRepository(db).CreateInstallation(ctx, installation); err != nil {
		t.Fatal(err)
	}
	selections := []SourceAnalysisToolSelection{
		{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: separatorPath, Executable: "ffmpeg", Version: "7.0", VersionBanner: "ffmpeg version 7.0"},
		{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: separatorPath, Executable: "ffprobe", Version: "7.0", VersionBanner: "ffprobe version 7.0"},
	}
	manifest := []PublicationNamespaceNode{{CanonicalKey: "root", RelativePath: "root", NodeKind: "dir", DirectoryScope: "root"}, {CanonicalKey: "root/file", RelativePath: "root/file", NodeKind: "file"}}
	input := publicationInputsFixture(t, "rewrite")
	input.Tools = selections
	ownership, _ := json.Marshal(PublicationCurrentOwnership{Version: 1, Identity: input.Rewrite.Ownership})
	audio, _ := json.Marshal(PublicationCurrentAudio{Version: 1, Facts: input.Rewrite.AudioFacts})
	if _, err := db.ExecContext(ctx, `INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at) VALUES (?,?,?,'Artist/Album/track.mka',?::jsonb,?::jsonb,'{}'::jsonb,'{}'::jsonb,1,now())`, positionID, epoch, input.Rewrite.CurrentGeneration, string(ownership), string(audio)); err != nil {
		t.Fatal(err)
	}
	frozen, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	request := PublicationAdmission{PositionID: positionID, ExpectedEpoch: epoch, ExpectedGeneration: uuidPointer(input.Rewrite.CurrentGeneration), CandidateGeneration: uuid.New(), IntentKind: "rewrite", ToolsReadRequired: true, Tools: selections, FrozenInputs: frozen, NamespaceManifest: manifest}
	repo := NewSetupManagerRepository(db)
	operationID, err := repo.AdmitPublication(ctx, request, new(publicationAdmissionInserter))
	if err != nil {
		t.Fatal(err)
	}
	var holds int
	if err := db.NewRaw(`SELECT count(*) FROM operation_tool_read_hold WHERE operation_id=? AND installation_id=?`, operationID, installationID).Scan(ctx, &holds); err != nil || holds != 1 {
		t.Fatalf("expected one installation hold, got %d (%v)", holds, err)
	}
	var persistedFrozen []byte
	if err := db.NewRaw(`SELECT frozen_inputs FROM track_publication_attempt WHERE operation_id=?`, operationID).Scan(ctx, &persistedFrozen); err != nil {
		t.Fatal(err)
	}
	var frozenFields map[string]json.RawMessage
	if err := json.Unmarshal(persistedFrozen, &frozenFields); err != nil || frozenFields["rewrite"] == nil || frozenFields["tools"] == nil {
		t.Fatalf("frozen inputs lost typed rewrite data or pinned tools: %s (%v)", persistedFrozen, err)
	}

	invalidSelections := []struct {
		request   PublicationAdmission
		wantError string
	}{
		{func() PublicationAdmission {
			r := request
			r.PositionID = makePosition(1)
			r.CandidateGeneration = uuid.New()
			r.Tools = selections[:1]
			return r
		}(), "ffmpeg and ffprobe selections are required"},
		{func() PublicationAdmission {
			r := request
			r.PositionID = makePosition(2)
			r.CandidateGeneration = uuid.New()
			r.Tools = append([]SourceAnalysisToolSelection(nil), selections...)
			r.Tools[1].Version = "999"
			return r
		}(), "pinned executable version does not match verified installation metadata"},
	}
	for index, invalid := range invalidSelections {
		invalid.request.NamespaceManifest = []PublicationNamespaceNode{{CanonicalKey: "root", RelativePath: "root", NodeKind: "dir", DirectoryScope: "root"}, {CanonicalKey: "root/invalid-" + string(rune('a'+index)), RelativePath: "root/invalid-" + string(rune('a'+index)), NodeKind: "file"}}
		invalidInput, err := DecodePublicationInputs(invalid.request.FrozenInputs)
		if err != nil {
			t.Fatal(err)
		}
		invalidInput.Tools = invalid.request.Tools
		invalid.request.FrozenInputs, err = json.Marshal(invalidInput)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.AdmitPublication(ctx, invalid.request, new(publicationAdmissionInserter)); err == nil || !strings.Contains(err.Error(), invalid.wantError) {
			t.Fatalf("expected %q validation error, got %v", invalid.wantError, err)
		}
		assertNoAdmissionArtifacts(t, ctx, db, invalid.request, nil)
	}
	failed := request
	failed.PositionID, failed.CandidateGeneration = makePosition(3), uuid.New()
	failed.NamespaceManifest = []PublicationNamespaceNode{{CanonicalKey: "root", RelativePath: "root", NodeKind: "dir", DirectoryScope: "root"}, {CanonicalKey: "root/enqueue-failed", RelativePath: "root/enqueue-failed", NodeKind: "file"}}
	failedInput, err := DecodePublicationInputs(failed.FrozenInputs)
	if err != nil {
		t.Fatal(err)
	}
	failedOwnership, err := json.Marshal(PublicationCurrentOwnership{Version: 1, Identity: failedInput.Rewrite.Ownership})
	if err != nil {
		t.Fatal(err)
	}
	failedAudio, err := json.Marshal(PublicationCurrentAudio{Version: 1, Facts: failedInput.Rewrite.AudioFacts})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at) VALUES (?,?,?,?,?::jsonb,?::jsonb,'{}'::jsonb,'{}'::jsonb,1,now())`, failed.PositionID, failed.ExpectedEpoch, failedInput.Rewrite.CurrentGeneration, failedInput.Rewrite.RelativePath, string(failedOwnership), string(failedAudio)); err != nil {
		t.Fatal(err)
	}
	failedInserter := &publicationAdmissionInserter{fail: true}
	if _, err := repo.AdmitPublication(ctx, failed, failedInserter); err == nil || !strings.Contains(err.Error(), "insert River job") {
		t.Fatal("expected enqueue failure")
	}
	if failedInserter.args == nil {
		t.Fatal("failing River inserter was not reached")
	}
	failedOperationID := failedInserter.args.(publicationTrackArgs).OperationID
	assertNoAdmissionArtifacts(t, ctx, db, failed, &failedOperationID)
	var failedHolds int
	if err := db.NewRaw(`SELECT count(*) FROM operation_tool_read_hold WHERE installation_id=?`, installationID).Scan(ctx, &failedHolds); err != nil || failedHolds != 1 {
		t.Fatalf("failed enqueue leaked a tool hold: %d (%v)", failedHolds, err)
	}
}

func assertNoAdmissionArtifacts(t *testing.T, ctx context.Context, db *bun.DB, request PublicationAdmission, operationID *uuid.UUID) {
	t.Helper()
	checks := []struct {
		label string
		query string
		args  []any
	}{
		{"operation", `SELECT count(*) FROM operation WHERE kind='publish_track_v1' AND input_snapshot->>'position_id'=?`, []any{request.PositionID.String()}},
		{"attempt", `SELECT count(*) FROM track_publication_attempt WHERE position_id=?`, []any{request.PositionID}},
		{"reservation", `SELECT count(*) FROM publication_namespace_reservation WHERE position_id=? AND generation=?`, []any{request.PositionID, request.CandidateGeneration}},
	}
	if operationID != nil {
		checks = append(checks,
			struct {
				label, query string
				args         []any
			}{"tool hold", `SELECT count(*) FROM operation_tool_read_hold WHERE operation_id=?`, []any{*operationID}},
			struct {
				label, query string
				args         []any
			}{"River job", `SELECT count(*) FROM river_job WHERE args->>'operation_id'=?`, []any{operationID.String()}},
		)
	}
	for _, check := range checks {
		var count int
		if err := db.NewRaw(check.query, check.args...).Scan(ctx, &count); err != nil || count != 0 {
			t.Errorf("failed admission left %s rows: %d (%v)", check.label, count, err)
		}
	}
}
