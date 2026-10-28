//go:build integration

package persistence

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
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

type publicationAdmissionInserter struct {
	fail            bool
	failAfterInsert bool
	id              int64
	args            river.JobArgs
}

func (i *publicationAdmissionInserter) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, _ *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	i.args = args
	if i.fail {
		return nil, errors.New("injected enqueue failure")
	}
	var id int64
	err := tx.QueryRowContext(ctx, `INSERT INTO river_job(kind,args,state,attempt) VALUES ($1,$2::jsonb,'available',0) RETURNING id`, args.Kind(), `{"operation_id":"`+args.(publication.TrackArgs).OperationID.String()+`"}`).Scan(&id)
	if err != nil {
		return nil, err
	}
	i.id = id
	if i.failAfterInsert {
		return nil, errors.New("injected failure after River row insertion")
	}
	return &rivertype.JobInsertResult{Job: &rivertype.JobRow{ID: id}}, nil
}

func TestPublicationAdmissionAtomicNamespaceAndFencesPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.OpenMigrated(t)
	initializeRiver(t, ctx, db)
	positions := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	release, tracks := uuid.New(), []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	if _, err := db.ExecContext(ctx, `INSERT INTO local_release(id,title) VALUES (?,'admission')`, release); err != nil {
		t.Fatal(err)
	}
	for i, id := range positions {
		if _, err := db.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?,'track')`, tracks[i]); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO release_track(id,release_id,track_id,position) VALUES (?,?,?,?)`, id, release, tracks[i], i); err != nil {
			t.Fatal(err)
		}
	}
	var epoch uuid.UUID
	if err := db.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &epoch); err != nil {
		t.Fatal(err)
	}
	manifest := func(path string) []PublicationNamespaceNode {
		return []PublicationNamespaceNode{{CanonicalKey: "root", RelativePath: "root", NodeKind: "dir", DirectoryScope: "root"}, {CanonicalKey: path, RelativePath: path, NodeKind: "file"}}
	}
	installationID := uuid.New()
	verified := time.Now().UTC()
	versions, ffmpegBanner, ffprobeBanner := publicationFFmpegVersionFixture()
	separatorPath := "ffmpeg/release-1"
	if runtime.GOOS == "windows" {
		separatorPath = `ffmpeg\release-1`
	}
	frozenRelativePath := filepath.ToSlash(separatorPath)
	if err := NewSetupManagerRepository(db).CreateInstallation(ctx, &ToolInstallation{ID: installationID, PackageKind: "ffmpeg", PlatformGOOS: runtime.GOOS, PlatformGOARCH: runtime.GOARCH, SourceName: "fixture", ReleaseIdentity: "release-1", RelativePath: separatorPath, State: "ready", ExecutableVerificationOutput: versions, Version: "ffmpeg 7.0 + ffprobe 7.0", ArtifactIdentities: json.RawMessage(`{}`), VerifiedAt: &verified}); err != nil {
		t.Fatal(err)
	}
	selections := []SourceAnalysisToolSelection{
		{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: frozenRelativePath, Executable: "ffmpeg", Version: "ffmpeg 7.0 + ffprobe 7.0", VersionBanner: ffmpegBanner},
		{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: frozenRelativePath, Executable: "ffprobe", Version: "ffmpeg 7.0 + ffprobe 7.0", VersionBanner: ffprobeBanner},
	}
	newRewriteRequest := func(position uuid.UUID, path string) PublicationAdmission {
		input := publicationInputsFixture(t, "rewrite")
		input.Tools = selections
		input.Rewrite.CurrentGeneration = uuid.New()
		ownership, _ := json.Marshal(PublicationCurrentOwnership{Version: 1, Identity: input.Rewrite.Ownership})
		audio, _ := json.Marshal(PublicationCurrentAudio{Version: 1, Facts: input.Rewrite.AudioFacts})
		if _, err := db.ExecContext(ctx, `INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at) VALUES (?,?,?,'Artist/Album/track.mka',?::jsonb,?::jsonb,'{}'::jsonb,'{}'::jsonb,1,now()) ON CONFLICT(position_id) DO UPDATE SET epoch=excluded.epoch,generation=excluded.generation,relative_path=excluded.relative_path,ownership=excluded.ownership,actual_audio=excluded.actual_audio`, position, epoch, input.Rewrite.CurrentGeneration, string(ownership), string(audio)); err != nil {
			t.Fatal(err)
		}
		frozen, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		return PublicationAdmission{PositionID: position, ExpectedEpoch: epoch, ExpectedGeneration: uuidPointer(input.Rewrite.CurrentGeneration), CandidateGeneration: uuid.New(), IntentKind: "rewrite", ToolsReadRequired: true, Tools: selections, FrozenInputs: frozen, NamespaceManifest: manifest(path)}
	}
	req := newRewriteRequest(positions[0], "root/a.flac")
	inserter := new(publicationAdmissionInserter)
	repo := NewSetupManagerRepository(db)
	op, err := repo.AdmitPublication(ctx, req, inserter)
	if err != nil {
		t.Fatal(err)
	}
	if inserter.args.Kind() != "publish_track_v1" || inserter.args.(publication.TrackArgs).OperationID != op {
		t.Fatal("River args did not contain only operation ID")
	}
	jobArgs, err := json.Marshal(inserter.args)
	if err != nil {
		t.Fatal(err)
	}
	var jobFields map[string]json.RawMessage
	if err := json.Unmarshal(jobArgs, &jobFields); err != nil || len(jobFields) != 1 || jobFields["operation_id"] == nil {
		t.Fatalf("unexpected River payload: %s (%v)", jobArgs, err)
	}
	var snapshot []byte
	if err := db.NewRaw(`SELECT input_snapshot FROM operation WHERE id=?`, op).Scan(ctx, &snapshot); err != nil {
		t.Fatal(err)
	}
	var snapshotFields map[string]json.RawMessage
	if err := json.Unmarshal(snapshot, &snapshotFields); err != nil {
		t.Fatal(err)
	}
	if len(snapshotFields) != 4 || snapshotFields["position_id"] == nil || snapshotFields["output_epoch"] == nil || snapshotFields["intent_kind"] == nil {
		t.Fatalf("unexpected source-free snapshot: %s", snapshot)
	}
	var namespaceManifest []byte
	if err := db.NewRaw(`SELECT namespace_manifest FROM track_publication_attempt WHERE operation_id=?`, op).Scan(ctx, &namespaceManifest); err != nil {
		t.Fatal(err)
	}
	var namespaceFields map[string]json.RawMessage
	if err := json.Unmarshal(namespaceManifest, &namespaceFields); err != nil || namespaceFields["version"] == nil || namespaceFields["nodes"] == nil {
		t.Fatalf("namespace manifest must be a versioned object: %s (%v)", namespaceManifest, err)
	}
	// Same owner may replace its generation; another owner may not claim its file.
	badCurrent := newRewriteRequest(positions[1], "root/bad-current.flac")
	if _, err := db.ExecContext(ctx, `UPDATE track_publication SET relative_path='forged.mka' WHERE position_id=?`, positions[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AdmitPublication(ctx, badCurrent, new(publicationAdmissionInserter)); err == nil || !strings.Contains(err.Error(), "frozen rewrite facts do not match current publication") {
		t.Fatalf("rewritten path was not checked against the locked publication: %v", err)
	}
	badCurrent = newRewriteRequest(positions[1], "root/bad-ownership.flac")
	if _, err := db.ExecContext(ctx, `UPDATE track_publication SET ownership='{}'::jsonb WHERE position_id=?`, positions[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.AdmitPublication(ctx, badCurrent, new(publicationAdmissionInserter)); err == nil || !strings.Contains(err.Error(), "frozen rewrite facts do not match current publication") {
		t.Fatalf("forged current ownership was admitted: %v", err)
	}
	req.CandidateGeneration = uuid.New()
	if _, err = repo.AdmitPublication(ctx, req, new(publicationAdmissionInserter)); err == nil {
		t.Fatal("unfinished same-position attempt was admitted")
	}
	other := req
	other.PositionID = positions[1]
	other.CandidateGeneration = uuid.New()
	if _, err = repo.AdmitPublication(ctx, other, new(publicationAdmissionInserter)); err == nil {
		t.Fatal("file collision admitted")
	}
	// A compatible shared directory alone is reservable by another position.
	shared := newRewriteRequest(positions[1], "root/shared")
	shared.NamespaceManifest = []PublicationNamespaceNode{{CanonicalKey: "shared", RelativePath: "shared", NodeKind: "dir", DirectoryScope: "same"}}
	if _, err = repo.AdmitPublication(ctx, shared, new(publicationAdmissionInserter)); err != nil {
		t.Fatalf("shared directory reservation: %v", err)
	}
	stale := newRewriteRequest(positions[2], "root/stale.flac")
	stale.ExpectedEpoch = uuid.New()
	if _, err = repo.AdmitPublication(ctx, stale, new(publicationAdmissionInserter)); err == nil || !strings.Contains(err.Error(), "output epoch changed") {
		t.Fatalf("expected output epoch fence error, got %v", err)
	}
	stale = newRewriteRequest(positions[2], "root/stale.flac")
	staleGeneration := uuid.New()
	stale.ExpectedGeneration = uuidPointer(staleGeneration)
	staleInput, err := DecodePublicationInputs(stale.FrozenInputs)
	if err != nil {
		t.Fatal(err)
	}
	staleInput.Rewrite.CurrentGeneration = staleGeneration
	stale.FrozenInputs, err = json.Marshal(staleInput)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.AdmitPublication(ctx, stale, new(publicationAdmissionInserter)); err == nil || !strings.Contains(err.Error(), "current generation changed") {
		t.Fatalf("expected current generation fence error, got %v", err)
	}
	prefix := newRewriteRequest(positions[2], "root/prefix")
	prefix.NamespaceManifest = []PublicationNamespaceNode{{CanonicalKey: "root", RelativePath: "root", NodeKind: "dir", DirectoryScope: "root"}, {CanonicalKey: "root/a.flac", RelativePath: "root/a.flac", NodeKind: "dir", DirectoryScope: "child"}, {CanonicalKey: "root/a.flac/child", RelativePath: "root/a.flac/child", NodeKind: "file"}}
	if _, err = repo.AdmitPublication(ctx, prefix, new(publicationAdmissionInserter)); err == nil {
		t.Fatal("file/directory prefix conflict admitted")
	}
	// Enqueue failure rolls back operation, attempt and reservations.
	failed := newRewriteRequest(positions[2], "root/fail.flac")
	if _, err = repo.AdmitPublication(ctx, failed, &publicationAdmissionInserter{fail: true}); err == nil {
		t.Fatal("expected enqueue failure")
	}
	var count int
	checks := []struct {
		table, query string
		args         []any
	}{{"operation", `SELECT count(*) FROM operation WHERE kind='publish_track_v1' AND input_snapshot->>'position_id'=?`, []any{positions[2].String()}}, {"track_publication_attempt", `SELECT count(*) FROM track_publication_attempt WHERE position_id=?`, []any{positions[2]}}, {"publication_namespace_claim", `SELECT count(*) FROM publication_namespace_claim WHERE relative_path LIKE 'root/fail%'`, nil}}
	for _, check := range checks {
		if err := db.NewRaw(check.query, check.args...).Scan(ctx, &count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("enqueue rollback left %s rows: %d", check.table, count)
		}
	}
}

func TestValidatePublicationManifestRejectsUnsafePaths(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/root/file", `root\\file`, "C:/root/file", "root/C:file", "root//file", "root/./file", "root/../file", "root/file:", "root/file\x00name"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			manifest := []PublicationNamespaceNode{{CanonicalKey: "root", RelativePath: "root", NodeKind: "dir", DirectoryScope: "root"}, {CanonicalKey: path, RelativePath: path, NodeKind: "file"}}
			if err := validatePublicationManifest(manifest); err == nil {
				t.Fatalf("unsafe path %q accepted", path)
			}
		})
	}
	badParent := []PublicationNamespaceNode{{CanonicalKey: "root", RelativePath: "root", NodeKind: "file"}, {CanonicalKey: "root/child", RelativePath: "root/child", NodeKind: "file"}}
	if err := validatePublicationManifest(badParent); err == nil {
		t.Fatal("file used as parent directory")
	}
}
