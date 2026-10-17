//go:build integration

package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

type publicationAdmissionInserter struct {
	fail bool
	id   int64
	args river.JobArgs
}

func (i *publicationAdmissionInserter) InsertTx(ctx context.Context, tx *sql.Tx, args river.JobArgs, _ *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	i.args = args
	if i.fail {
		return nil, errors.New("injected enqueue failure")
	}
	var id int64
	err := tx.QueryRowContext(ctx, `INSERT INTO river_job(kind,args,state,attempt) VALUES ($1,$2::jsonb,'available',0) RETURNING id`, args.Kind(), `{"operation_id":"`+args.(publicationTrackArgs).OperationID.String()+`"}`).Scan(&id)
	if err != nil {
		return nil, err
	}
	i.id = id
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
	req := PublicationAdmission{PositionID: positions[0], ExpectedEpoch: epoch, CandidateGeneration: uuid.New(), IntentKind: "audio", FrozenInputs: json.RawMessage(`{"input":"frozen"}`), NamespaceManifest: manifest("root/a.flac")}
	inserter := new(publicationAdmissionInserter)
	repo := NewSetupManagerRepository(db)
	op, err := repo.AdmitPublication(ctx, req, inserter)
	if err != nil {
		t.Fatal(err)
	}
	if inserter.args.Kind() != "publish_track_v1" || inserter.args.(publicationTrackArgs).OperationID != op {
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
	shared := PublicationAdmission{PositionID: positions[1], ExpectedEpoch: epoch, CandidateGeneration: uuid.New(), IntentKind: "audio", FrozenInputs: json.RawMessage(`{}`), NamespaceManifest: []PublicationNamespaceNode{{CanonicalKey: "shared", RelativePath: "shared", NodeKind: "dir", DirectoryScope: "same"}}}
	if _, err = repo.AdmitPublication(ctx, shared, new(publicationAdmissionInserter)); err != nil {
		t.Fatalf("shared directory reservation: %v", err)
	}
	stale := PublicationAdmission{PositionID: positions[2], ExpectedEpoch: uuid.New(), CandidateGeneration: uuid.New(), IntentKind: "audio", FrozenInputs: json.RawMessage(`{}`), NamespaceManifest: manifest("root/stale.flac")}
	if _, err = repo.AdmitPublication(ctx, stale, new(publicationAdmissionInserter)); err == nil || !strings.Contains(err.Error(), "output epoch changed") {
		t.Fatalf("expected output epoch fence error, got %v", err)
	}
	stale = PublicationAdmission{PositionID: positions[2], ExpectedEpoch: epoch, ExpectedGeneration: uuidPointer(uuid.New()), CandidateGeneration: uuid.New(), IntentKind: "audio", FrozenInputs: json.RawMessage(`{}`), NamespaceManifest: manifest("root/stale.flac")}
	if _, err = repo.AdmitPublication(ctx, stale, new(publicationAdmissionInserter)); err == nil || !strings.Contains(err.Error(), "current generation changed") {
		t.Fatalf("expected current generation fence error, got %v", err)
	}
	prefix := PublicationAdmission{PositionID: positions[2], ExpectedEpoch: epoch, CandidateGeneration: uuid.New(), IntentKind: "audio", FrozenInputs: json.RawMessage(`{}`), NamespaceManifest: []PublicationNamespaceNode{{CanonicalKey: "root", RelativePath: "root", NodeKind: "dir", DirectoryScope: "root"}, {CanonicalKey: "root/a.flac", RelativePath: "root/a.flac", NodeKind: "dir", DirectoryScope: "child"}, {CanonicalKey: "root/a.flac/child", RelativePath: "root/a.flac/child", NodeKind: "file"}}}
	if _, err = repo.AdmitPublication(ctx, prefix, new(publicationAdmissionInserter)); err == nil {
		t.Fatal("file/directory prefix conflict admitted")
	}
	// Enqueue failure rolls back operation, attempt and reservations.
	failed := PublicationAdmission{PositionID: positions[2], ExpectedEpoch: epoch, CandidateGeneration: uuid.New(), IntentKind: "audio", FrozenInputs: json.RawMessage(`{}`), NamespaceManifest: manifest("root/fail.flac")}
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
