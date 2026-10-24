//go:build integration

package persistence

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// This exercises the database boundary directly: application validation is not
// sufficient because cleanup authority may be written by future maintenance
// code or a broken transaction path.
func TestObsoletePublicationOutputCleanupSchemaRejectsMalformedAuthorityPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f, _, _, _ := readyFirstPublicationCommitFixture(t)
	db := f.db
	// The publication fixture supplies a real release_track, active epoch,
	// namespace claim and reservation. Reuse those values so every attempted
	// insert reaches the schema checks under test rather than failing an FK.
	var position, epoch, generation, claim uuid.UUID
	var relative string
	if err := db.NewRaw(`SELECT c.owner_position_id, c.epoch, r.generation, c.id, c.relative_path
		FROM publication_namespace_claim c JOIN publication_namespace_reservation r
		ON r.namespace_claim_id=c.id WHERE c.node_kind='file' LIMIT 1`).Scan(ctx, &position, &epoch, &generation, &claim, &relative); err != nil {
		t.Fatal(err)
	}
	if position == uuid.Nil || epoch == uuid.Nil || generation == uuid.Nil || claim == uuid.Nil {
		t.Fatal("fixture did not provide a complete namespace claim and reservation")
	}
	operationID := uuid.New()
	if _, err := db.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot) VALUES (?,?,'queued','queued',jsonb_build_object('operation_id',?::text))`, operationID, ObsoletePublicationOutputCleanupOperationKind, operationID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	valid := `INSERT INTO obsolete_publication_output_cleanup
	(operation_id,epoch,position_id,generation,relative_path,native_path,output_root_identity,parent_identity,namespace_claim_id,file_size,modified_at,sha256,state)
	VALUES (?,?,?,?,?,?,?,?,?,0,now(),decode(?,'hex'),'queued')`
	root := `{"platform":"unix","volume":"1","file_id":"2"}`
	parent := `{"platform":"unix","volume":"1","file_id":"3"}`
	args := []any{operationID, epoch, position, generation, relative, "/music/track.flac", root, parent, claim, strings.Repeat("00", 32)}
	if _, err := db.NewRaw(valid, args...).Exec(ctx); err != nil {
		t.Fatalf("valid baseline authority rejected: %v", err)
	}
	if _, err := db.NewRaw(`UPDATE publication_namespace_claim SET relative_path='rebound.flac' WHERE id=?`, claim).Exec(ctx); err == nil {
		t.Fatal("cleanup-owned namespace claim was rebound through SQL")
	}
	if _, err := db.NewRaw(`DELETE FROM obsolete_publication_output_cleanup WHERE operation_id=?`, operationID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	// Each rejection is attempted in its own transaction after the valid row is
	// established, proving malformed variants are not merely failing on an
	// absent parent fixture.
	cases := []struct {
		name, relative, native, root, parent string
		size                                 int64
		sha                                  string
	}{
		{"missing root key", "track.flac", "/music/track.flac", `{"platform":"unix","volume":"1"}`, parent, 0, strings.Repeat("00", 32)},
		{"null root key", "track.flac", "/music/track.flac", `{"platform":"unix","volume":"1","file_id":null}`, parent, 0, strings.Repeat("00", 32)},
		{"numeric parent key", "track.flac", "/music/track.flac", root, `{"platform":"unix","volume":"1","file_id":2}`, 0, strings.Repeat("00", 32)},
		{"array identity", "track.flac", "/music/track.flac", `[]`, parent, 0, strings.Repeat("00", 32)},
		{"absolute relative path", "/track.flac", "/music/track.flac", root, parent, 0, strings.Repeat("00", 32)},
		{"traversal", "../track.flac", "/music/track.flac", root, parent, 0, strings.Repeat("00", 32)},
		{"non-native absolute path", "track.flac", "track.flac", root, parent, 0, strings.Repeat("00", 32)},
		{"negative size", "track.flac", "/music/track.flac", root, parent, -1, strings.Repeat("00", 32)},
		{"short digest", "track.flac", "/music/track.flac", root, parent, 0, strings.Repeat("00", 31)},
		{"wrong claimed path", "unrelated.flac", "/music/unrelated.flac", root, parent, 0, strings.Repeat("00", 32)},
	}
	for i, tc := range cases {
		if tc.relative == "track.flac" {
			tc.relative = relative
		}
		t.Run(tc.name, func(t *testing.T) {
			id := uuid.New()
			if _, err := db.NewRaw(`INSERT INTO operation(id,kind,state,stage,input_snapshot) VALUES (?,?,'queued','queued',jsonb_build_object('operation_id',?::text))`, id, ObsoletePublicationOutputCleanupOperationKind, id).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			query := `INSERT INTO obsolete_publication_output_cleanup
			(operation_id,epoch,position_id,generation,relative_path,native_path,output_root_identity,parent_identity,namespace_claim_id,file_size,modified_at,sha256,state)
			VALUES (?,?,?,?,?,?,?::jsonb,?::jsonb,?,?,now(),decode(?,'hex'),'queued')`
			_, err := db.NewRaw(query, id, epoch, position, generation, tc.relative, tc.native, tc.root, tc.parent, claim, tc.size, tc.sha).Exec(ctx)
			if err == nil {
				_, _ = db.NewRaw(`DELETE FROM obsolete_publication_output_cleanup WHERE operation_id=?`, id).Exec(ctx)
				t.Fatalf("case %d: malformed authority accepted", i)
			}
		})
	}
}
