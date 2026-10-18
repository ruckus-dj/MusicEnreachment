//go:build integration

package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
	"github.com/uptrace/bun"
)

func TestLoadPublicationStartupSnapshotOnFreshDatabasePostgreSQL(t *testing.T) {
	t.Parallel()
	db := testpostgres.OpenMigrated(t)

	snapshot, err := NewSetupManagerRepository(db).LoadPublicationStartupSnapshot(context.Background())
	if err != nil {
		t.Fatalf("load startup snapshot from empty migrated database: %v", err)
	}
	if len(snapshot.Records) != 0 {
		t.Fatalf("startup records = %+v, want none", snapshot.Records)
	}
}

func TestLoadPublicationStartupSnapshotQueuedAndClaimedPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	repository := NewSetupManagerRepository(f.db)
	ctx := context.Background()
	prepareQueuedStartupFixture(t, f)

	snapshot, err := repository.LoadPublicationStartupSnapshot(ctx)
	if err != nil {
		t.Fatalf("load queued startup snapshot: %v", err)
	}
	if len(snapshot.Records) != 1 {
		t.Fatalf("queued startup records = %+v, want one", snapshot.Records)
	}
	queued := snapshot.Records[0]
	if queued.OperationID != f.guard.OperationID || queued.Phase != "queued" || queued.OperationState != "queued" || queued.HasClaim || !queued.EpochKnown || !queued.EpochMatches || !queued.JobAvailable || !queued.QueuedContractValid || !queued.CurrentKnown {
		t.Fatalf("queued startup record = %+v, want coherent queued execution", queued)
	}
	if _, err := f.db.NewRaw(`UPDATE river_job SET state='running' WHERE id=?`, f.guard.RiverJobID).Exec(ctx); err != nil {
		t.Fatalf("restore claimed-delivery fixture state: %v", err)
	}

	if outcome, err := repository.ClaimPublicationDelivery(ctx, f.guard); err != nil || outcome != PublicationDeliveryAcquired {
		t.Fatalf("claim publication delivery = %q, %v", outcome, err)
	}
	snapshot, err = repository.LoadPublicationStartupSnapshot(ctx)
	if err != nil {
		t.Fatalf("load claimed startup snapshot: %v", err)
	}
	if len(snapshot.Records) != 1 || !snapshot.Records[0].HasClaim {
		t.Fatalf("claimed startup records = %+v, want retained claim", snapshot.Records)
	}
}

func prepareQueuedStartupFixture(t *testing.T, f publicationExecutionFixture) {
	t.Helper()
	ctx := context.Background()
	node := PublicationNamespaceNode{CanonicalKey: "output.mka", RelativePath: "output.mka", NodeKind: "file"}
	manifest, err := json.Marshal(struct {
		Version int                        `json:"version"`
		Nodes   []PublicationNamespaceNode `json:"nodes"`
	}{Version: 1, Nodes: []PublicationNamespaceNode{node}})
	if err != nil {
		t.Fatal(err)
	}
	args, err := json.Marshal(publicationTrackArgs{OperationID: f.guard.OperationID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.NewRaw(`UPDATE river_job SET kind=?, args=?::jsonb, state='available' WHERE id=?`, (publicationTrackArgs{}).Kind(), string(args), f.guard.RiverJobID).Exec(ctx); err != nil {
		t.Fatalf("prepare queued River job: %v", err)
	}
	if _, err := f.db.NewRaw(`UPDATE track_publication_attempt SET namespace_manifest=?::jsonb WHERE operation_id=?`, string(manifest), f.guard.OperationID).Exec(ctx); err != nil {
		t.Fatalf("prepare queued namespace manifest: %v", err)
	}
	digest := sha256.Sum256([]byte(node.CanonicalKey))
	claimID := uuid.New()
	if _, err := f.db.NewRaw(`INSERT INTO publication_namespace_claim(id,epoch,key_digest,canonical_key,relative_path,node_kind,owner_position_id) VALUES (?,?,?,?,?,'file',?)`, claimID, f.guard.Epoch, digest[:], node.CanonicalKey, node.RelativePath, f.guard.ExpectedPositionID).Exec(ctx); err != nil {
		t.Fatalf("prepare queued namespace claim: %v", err)
	}
	if _, err := f.db.NewRaw(`INSERT INTO publication_namespace_reservation(namespace_claim_id,position_id,generation) VALUES (?,?,?)`, claimID, f.guard.ExpectedPositionID, f.guard.CandidateGeneration).Exec(ctx); err != nil {
		t.Fatalf("prepare queued namespace reservation: %v", err)
	}
}

func TestLoadPublicationStartupSnapshotUsesRepeatableReadPostgreSQL(t *testing.T) {
	t.Parallel()
	f := newPublicationExecutionFixture(t)
	ctx := context.Background()
	barrier := &publicationSnapshotQueryBarrier{
		needle:   `FROM "track_publication_attempt"`,
		observed: make(chan struct{}),
		release:  make(chan struct{}),
	}
	f.db.AddQueryHook(barrier)

	result := make(chan struct {
		snapshot PublicationStartupSnapshot
		err      error
	}, 1)
	go func() {
		snapshot, err := NewSetupManagerRepository(f.db).LoadPublicationStartupSnapshot(ctx)
		result <- struct {
			snapshot PublicationStartupSnapshot
			err      error
		}{snapshot: snapshot, err: err}
	}()
	<-barrier.observed
	if _, err := f.db.NewRaw(`UPDATE operation SET state='running', stage='publishing' WHERE id=?`, f.guard.OperationID).Exec(ctx); err != nil {
		close(barrier.release)
		t.Fatalf("concurrently update operation: %v", err)
	}
	close(barrier.release)
	loaded := <-result
	if loaded.err != nil {
		t.Fatalf("load startup snapshot: %v", loaded.err)
	}
	if len(loaded.snapshot.Records) != 1 || loaded.snapshot.Records[0].OperationState != "queued" {
		t.Fatalf("snapshot after concurrent update = %+v, want the original queued state", loaded.snapshot.Records)
	}
}

type publicationSnapshotQueryBarrier struct {
	needle   string
	once     sync.Once
	observed chan struct{}
	release  chan struct{}
}

func (barrier *publicationSnapshotQueryBarrier) BeforeQuery(ctx context.Context, _ *bun.QueryEvent) context.Context {
	return ctx
}

func (barrier *publicationSnapshotQueryBarrier) AfterQuery(_ context.Context, event *bun.QueryEvent) {
	if strings.Contains(event.Query, barrier.needle) {
		barrier.once.Do(func() {
			close(barrier.observed)
			<-barrier.release
		})
	}
}
