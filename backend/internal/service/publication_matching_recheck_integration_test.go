//go:build integration

package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/uptrace/bun"
)

func TestMatchingRecheckNativeInstallAndRecoveryPostgreSQL(t *testing.T) {
	t.Run("same path", func(t *testing.T) { testMatchingRecheckNativeInstall(t, true) })
	t.Run("different path", func(t *testing.T) { testMatchingRecheckNativeInstall(t, false) })
}

func testMatchingRecheckNativeInstall(t *testing.T, samePath bool) {
	t.Helper()
	testPublicationSourceExecution(t, "Track.flac", true, publicationDeliveryScenario{
		run: func(ctx context.Context, db *bun.DB, manager *persistence.SetupManagerRepository, executor *PublicationExecutor, operationID uuid.UUID, jobID int64, source, output, target string, original []byte) {
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := db.NewRaw(query, args...).Exec(ctx); err != nil {
					t.Fatal(err)
				}
			}
			encode := func(value any) string {
				t.Helper()
				data, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				return string(data)
			}
			attempt, err := manager.ReadPublicationAttempt(ctx, operationID)
			if err != nil || attempt == nil {
				t.Fatalf("read admitted publication: %v", err)
			}
			input, err := persistence.DecodePublicationInputs(attempt.FrozenInputs)
			if err != nil || input.Audio == nil || input.SourceAuthorityMode != "automatic" {
				t.Fatalf("read admitted automatic source: %v", err)
			}
			exec(`UPDATE track_source SET decision_method='automatic',policy_version=?
				WHERE media_variant_id=? AND track_id=(SELECT track_id FROM release_track WHERE id=?)`,
				persistence.MatchingLayoutPolicyVersion, input.Audio.MediaVariantID, attempt.PositionID)
			track, release, artist, position, generation := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
			exec(`WITH release AS (INSERT INTO local_release(id,title) VALUES (?,'Old release') RETURNING id),
				artist AS (INSERT INTO local_artist(id,name) VALUES (?,'Artist') RETURNING id)
				INSERT INTO release_artist(release_id,artist_id,position,name) SELECT release.id,artist.id,0,'Artist' FROM release,artist`, release, artist)
			exec(`INSERT INTO local_track(id,title) VALUES (?,'Old track')`, track)
			exec(`INSERT INTO release_track(id,release_id,track_id,position) VALUES (?,?,?,0)`, position, release, track)
			association := uuid.New()
			exec(`INSERT INTO track_source(id,track_id,media_variant_id,decision_method,policy_version,compatible,selected,metadata)
				VALUES (?,?,?,'automatic',?,true,true,'{}')`, association, track, input.Audio.MediaVariantID, persistence.MatchingLayoutPolicyVersion)
			destination := filepath.Join(output, filepath.FromSlash(target))
			predecessorPath := target
			if !samePath {
				predecessorPath = "Local.flac"
			}
			predecessor := filepath.Join(output, filepath.FromSlash(predecessorPath))
			if err := os.WriteFile(predecessor, original, 0o600); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(predecessor)
			if err != nil {
				t.Fatal(err)
			}
			capture, err := (metadata.Reader{}).Read(ctx, predecessor, metadata.ReadRequest{CoverInspection: true})
			if err != nil {
				t.Fatal(err)
			}
			assets, err := ObservePublicationAssets(capture)
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(original)
			size, mtime := info.Size(), info.ModTime().UnixMicro()
			identity := persistence.PublicationFileIdentity{SizeBytes: &size, MTime: &mtime, SHA256: hex.EncodeToString(digest[:])}
			exec(`INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at)
				VALUES (?,?,?,?,?::jsonb,?::jsonb,?::jsonb,?::jsonb,?,now())`, position, attempt.Epoch, generation, predecessorPath,
				encode(persistence.PublicationCurrentOwnership{Version: 1, Identity: identity}),
				encode(persistence.PublicationCurrentAudio{Version: 1, Facts: input.Audio.AudioFacts}),
				encode(persistence.PublicationCommitMetadata{Version: 1, Tags: capture.Tags}), encode(assets), input.QualityPolicyVersion)
			var claim uuid.UUID
			if err := db.NewRaw(`SELECT id FROM publication_namespace_claim WHERE epoch=? AND owner_position_id=? AND node_kind='file'`,
				attempt.Epoch, attempt.PositionID).Scan(ctx, &claim); err != nil {
				t.Fatal(err)
			}
			if samePath {
				exec(`UPDATE publication_namespace_claim SET owner_position_id=? WHERE id=?`, position, claim)
			} else {
				claim = uuid.New()
				key := strings.ToLower(predecessorPath)
				hash := sha256.Sum256([]byte(key))
				exec(`INSERT INTO publication_namespace_claim(id,epoch,key_digest,canonical_key,relative_path,node_kind,owner_position_id)
					VALUES (?,?,?,?,?,'file',?)`, claim, attempt.Epoch, hash[:], key, predecessorPath, position)
			}
			exec(`INSERT INTO publication_namespace_reservation(namespace_claim_id,position_id,generation) VALUES (?,?,?)`, claim, position, generation)
			applied, recheck := uuid.New(), uuid.New()
			exec(`INSERT INTO operation(id,kind,state,stage,input_snapshot,finished_at) VALUES (?,'matching_apply_v1','succeeded','succeeded','{}',now())`, applied)
			exec(`INSERT INTO matching_local_recheck(id,local_apply_operation_id,state,next_recheck_at,incumbent_snapshot)
				VALUES (?,?,'publishing',now(),jsonb_build_object('version',1,'sources',jsonb_build_array(jsonb_build_object(
					'release_track_id',?::uuid,'local_track_id',?::uuid,'track_source_id',?::uuid,'media_variant_id',?::uuid,'track_source_inserted',true))))`,
				recheck, applied, position, track, association, input.Audio.MediaVariantID)
			witness := persistence.PublicationCurrentWitness{Generation: generation, RelativePath: predecessorPath,
				Ownership: identity, AudioFacts: input.Audio.AudioFacts, Assets: assets, Tags: capture.Tags}
			exec(`INSERT INTO matching_local_recheck_publication(operation_id,recheck_id,output_epoch,position_id,candidate_generation,
				predecessor_position_id,predecessor_generation,predecessor_claim_id,same_path,predecessor_snapshot)
				VALUES (?,?,?,?,?,?,?,?,?,jsonb_build_object('version',1,'position_id',?::uuid,'output_epoch',?::uuid,
					'generation',?::uuid,'namespace_claim_id',?::uuid,'relative_path',?::text,'current',?::jsonb))`,
				operationID, recheck, attempt.Epoch, attempt.PositionID, attempt.CandidateGeneration,
				position, generation, claim, samePath, position, attempt.Epoch, generation, claim, predecessorPath, encode(witness))

			delivery := 1
			if !samePath {
				if err := os.WriteFile(destination, original, 0o600); err != nil {
					t.Fatal(err)
				}
				if _, err := (metadata.Writer{}).Write(ctx, destination, input.DesiredTags, nil); err != nil {
					t.Fatal(err)
				}
				foreign, err := os.ReadFile(destination)
				if err != nil {
					t.Fatal(err)
				}
				for range 2 {
					exec(`UPDATE river_job SET attempt=? WHERE id=?`, delivery, jobID)
					if err := executor.ExecutePublicationDelivery(ctx, operationID, jobID, delivery); err == nil {
						t.Fatal("adopted an occupied different-path destination")
					}
					delivery++
				}
				if data, err := os.ReadFile(destination); err != nil || !bytes.Equal(data, foreign) {
					t.Fatalf("different-path conflict modified the foreign output: %v", err)
				}
				if err := os.Remove(destination); err != nil {
					t.Fatal(err)
				}
			}
			exec(`CREATE FUNCTION reject_matching_install() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected matching settlement failure'; END $$`)
			exec(`CREATE TRIGGER reject_matching_install BEFORE INSERT ON track_publication FOR EACH ROW EXECUTE FUNCTION reject_matching_install()`)
			exec(`UPDATE river_job SET attempt=? WHERE id=?`, delivery, jobID)
			if err := executor.ExecutePublicationDelivery(ctx, operationID, jobID, delivery); err == nil || !strings.Contains(err.Error(), "injected matching settlement failure") {
				t.Fatalf("replacement did not reach injected result failure: %v", err)
			}
			replaced, err := os.ReadFile(destination)
			if err != nil || bytes.Equal(replaced, original) {
				t.Fatalf("native replacement did not happen: %v", err)
			}
			exec(`DROP TRIGGER reject_matching_install ON track_publication`)
			delivery++
			exec(`UPDATE river_job SET attempt=? WHERE id=?`, delivery, jobID)
			if err := executor.ExecutePublicationDelivery(ctx, operationID, jobID, delivery); err != nil {
				t.Fatalf("recover replaced matching output: %v", err)
			}
			var settled bool
			owner := position
			if samePath {
				owner = attempt.PositionID
			}
			if err := db.NewRaw(`SELECT EXISTS(SELECT 1 FROM track_publication WHERE position_id=? AND generation=?)
				AND EXISTS(SELECT 1 FROM track_publication WHERE position_id=?)=?
				AND EXISTS(SELECT 1 FROM matching_local_recheck WHERE id=?)=?
				AND EXISTS(SELECT 1 FROM publication_namespace_claim WHERE id=? AND owner_position_id=?)`,
				attempt.PositionID, attempt.CandidateGeneration, position, !samePath, recheck, !samePath, claim, owner).Scan(ctx, &settled); err != nil || !settled {
				t.Fatalf("recovered matching output did not settle: %v, %v", settled, err)
			}
			if data, err := os.ReadFile(source); err != nil || !bytes.Equal(data, original) {
				t.Fatalf("matching publication modified its source: %v", err)
			}
			if !samePath {
				if data, err := os.ReadFile(predecessor); err != nil || !bytes.Equal(data, original) {
					t.Fatalf("different-path move modified the predecessor before batch settlement: %v", err)
				}
			}
		},
	})
}
