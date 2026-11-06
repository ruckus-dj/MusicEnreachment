//go:build integration

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/uptrace/bun"
)

func TestFirstPublicationPersistsAndPublishesOrderedGenresPostgreSQL(t *testing.T) {
	genreReferences := []librarymetadata.GenreReference{
		{ID: "11111111-1111-4111-8111-111111111111", SourceName: "post-punk", Name: "Post-Punk"},
		{ID: "22222222-2222-4222-8222-222222222222", SourceName: "dream pop", Name: "Dream Pop"},
	}
	wantNames := []string{"Post-Punk", "Dream Pop"}
	scenario := publicationDeliveryScenario{
		beforeAdmission: func(ctx context.Context, db *bun.DB, _ *persistence.SetupManagerRepository, releaseID uuid.UUID) {
			encodedCatalog, err := json.Marshal(genreReferences)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.NewRaw(`UPDATE genre_catalog SET items=?::jsonb, updated_at=now() WHERE singleton=true`, string(encodedCatalog)).Exec(ctx); err != nil {
				t.Fatalf("seed genre catalog singleton: %v", err)
			}

			repository := persistence.NewLibraryRepository(db)
			metadataService := NewLibraryMetadataService(repository)
			current, err := repository.ReadMetadata(ctx, librarymetadata.ScopeRelease, releaseID, releaseID)
			if err != nil {
				t.Fatalf("read release metadata before genre update: %v", err)
			}
			genreValue, err := json.Marshal(map[string]any{"state": "value", "value": genreReferences})
			if err != nil {
				t.Fatal(err)
			}
			updated, err := metadataService.Update(ctx, librarymetadata.ScopeRelease, releaseID, releaseID, current.Revision, map[string]json.RawMessage{"genre": genreValue})
			if err != nil {
				t.Fatalf("save release genres through metadata service: %v", err)
			}
			if got := updated.Envelope.Desired["genre"].Value; !reflect.DeepEqual(got, genreReferences) {
				t.Fatalf("saved canonical genre references = %#v, want %#v", got, genreReferences)
			}
			var currentCount int
			if err := db.NewRaw(`SELECT count(*) FROM track_publication WHERE position_id IN (SELECT id FROM release_track WHERE release_id=?)`, releaseID).Scan(ctx, &currentCount); err != nil {
				t.Fatalf("check publication state after metadata save: %v", err)
			}
			if currentCount != 0 {
				t.Fatalf("saving desired genres unexpectedly created CURRENT publication(s): %d", currentCount)
			}
		},
		run: func(ctx context.Context, db *bun.DB, manager *persistence.SetupManagerRepository, executor *PublicationExecutor, operationID uuid.UUID, riverJobID int64, sourceFile, output, target string, sourceBytes []byte) {
			attempt, err := manager.ReadPublicationAttempt(ctx, operationID)
			if err != nil || attempt == nil {
				t.Fatalf("read admitted publication attempt = %+v, %v", attempt, err)
			}
			inputs, err := persistence.DecodePublicationInputs(attempt.FrozenInputs)
			if err != nil {
				t.Fatalf("decode frozen publication inputs: %v", err)
			}
			if got := inputs.DesiredTags["GENRE"]; !reflect.DeepEqual(got, wantNames) {
				t.Fatalf("frozen GENRE values = %#v, want %#v", got, wantNames)
			}
			if err := executor.ExecutePublicationDelivery(ctx, operationID, riverJobID, 1); err != nil {
				t.Fatalf("execute first publication: %v", err)
			}

			publishedPath := filepath.Join(output, filepath.FromSlash(target))
			capture, err := (metadata.Reader{}).Read(ctx, publishedPath, metadata.ReadRequest{})
			if err != nil {
				t.Fatalf("read native published metadata: %v", err)
			}
			if got := capture.Tags["GENRE"]; !reflect.DeepEqual(got, wantNames) {
				t.Fatalf("native published genres = %#v, want ordered %#v", got, wantNames)
			}

			var current persistence.TrackPublication
			if err := db.NewSelect().Model(&current).Where("generation=?", attempt.CandidateGeneration).Scan(ctx); err != nil {
				t.Fatalf("read committed CURRENT publication: %v", err)
			}
			var actual persistence.PublicationCommitMetadata
			if err := json.Unmarshal(current.ActualMetadata, &actual); err != nil {
				t.Fatalf("decode CURRENT metadata: %v", err)
			}
			if got := actual.Tags["GENRE"]; !reflect.DeepEqual(got, wantNames) {
				t.Fatalf("CURRENT GENRE values = %#v, want ordered %#v", got, wantNames)
			}

			actualSource, err := os.ReadFile(sourceFile)
			if err != nil {
				t.Fatalf("read source after publication: %v", err)
			}
			if !reflect.DeepEqual(actualSource, sourceBytes) {
				t.Fatal("source bytes changed during publication")
			}

			var publicationOperationsBefore int
			if err := db.NewRaw(`SELECT count(*) FROM operation WHERE kind='publish_track_v1'`).Scan(ctx, &publicationOperationsBefore); err != nil {
				t.Fatalf("count publication operations before genre override: %v", err)
			}
			metadataRepository := persistence.NewLibraryRepository(db)
			metadataService := NewLibraryMetadataService(metadataRepository)
			var releaseID uuid.UUID
			if err := db.NewRaw(`SELECT rt.release_id FROM release_track rt WHERE rt.id=(SELECT (input_snapshot->>'position_id')::uuid FROM operation WHERE id=?)`, operationID).Scan(ctx, &releaseID); err != nil {
				t.Fatalf("read publication release: %v", err)
			}
			desired, err := metadataRepository.ReadMetadata(ctx, librarymetadata.ScopeRelease, releaseID, releaseID)
			if err != nil {
				t.Fatalf("read desired release genres before override: %v", err)
			}
			got := decodeGenreReferences(t, desired.Envelope.Desired["genre"].Value)
			if !reflect.DeepEqual(got, genreReferences) {
				t.Fatalf("pre-override desired genres = %#v, want %#v", got, genreReferences)
			}
			changedGenres := []librarymetadata.GenreReference{genreReferences[0]}
			if reflect.DeepEqual(changedGenres, genreReferences) {
				t.Fatal("genre override did not change the desired selection")
			}
			changedValue, err := json.Marshal(map[string]any{"state": "value", "value": changedGenres})
			if err != nil {
				t.Fatal(err)
			}
			changed, err := metadataService.Update(ctx, librarymetadata.ScopeRelease, releaseID, releaseID, desired.Revision, map[string]json.RawMessage{"genre": changedValue})
			if err != nil {
				t.Fatalf("save changed release genre override: %v", err)
			}
			got = decodeGenreReferences(t, changed.Envelope.Desired["genre"].Value)
			if !reflect.DeepEqual(got, changedGenres) {
				t.Fatalf("changed desired genres = %#v, want %#v", got, changedGenres)
			}

			var afterOverride persistence.TrackPublication
			if err := db.NewSelect().Model(&afterOverride).Where("generation=?", attempt.CandidateGeneration).Scan(ctx); err != nil {
				t.Fatalf("read CURRENT after genre override: %v", err)
			}
			if afterOverride.Generation != current.Generation || !bytes.Equal(afterOverride.ActualMetadata, current.ActualMetadata) {
				t.Fatalf("CURRENT changed after desired genre edit: before generation=%s metadata=%s; after generation=%s metadata=%s", current.Generation, current.ActualMetadata, afterOverride.Generation, afterOverride.ActualMetadata)
			}
			captureAfterOverride, err := (metadata.Reader{}).Read(ctx, publishedPath, metadata.ReadRequest{})
			if err != nil {
				t.Fatalf("read native output after genre override: %v", err)
			}
			if got := captureAfterOverride.Tags["GENRE"]; !reflect.DeepEqual(got, wantNames) {
				t.Fatalf("native output changed after desired genre edit: got %#v, want published %#v", got, wantNames)
			}
			var publicationOperationsAfter int
			if err := db.NewRaw(`SELECT count(*) FROM operation WHERE kind='publish_track_v1'`).Scan(ctx, &publicationOperationsAfter); err != nil {
				t.Fatalf("count publication operations after genre override: %v", err)
			}
			if publicationOperationsAfter != publicationOperationsBefore {
				t.Fatalf("genre override admitted a publication operation: before=%d after=%d", publicationOperationsBefore, publicationOperationsAfter)
			}
		},
	}
	testPublicationSourceExecution(t, "Track.flac", true, scenario)
}

func decodeGenreReferences(t *testing.T, value any) []librarymetadata.GenreReference {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode genre references: %v", err)
	}
	var genres []librarymetadata.GenreReference
	if err := json.Unmarshal(encoded, &genres); err != nil {
		t.Fatalf("decode genre references: %v", err)
	}
	return genres
}

// This exercises the production projection and native TagLib write/readback
// path on a real FLAC, but not publication admission, freezing, or CURRENT
// persistence. Those lifecycle assertions belong to the executor integration
// tests; this bounded test isolates final genre serialization behavior.
func TestPublicationGenreProjectionNativeWriterReadback(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	path := filepath.Join(directory, "genre-publication.flac")
	fixture, err := os.ReadFile("../integrations/metadata/testdata/eg.flac")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, fixture, 0o600); err != nil {
		t.Fatal(err)
	}

	genreEnvelope, err := librarymetadata.DecodeEnvelope([]byte(`{"version":1,"scope":"release","desired":{"genre":{"state":"value","value":[{"id":"11111111-1111-4111-8111-111111111111","source_name":"post-punk","name":"Post-Punk"},{"id":"22222222-2222-4222-8222-222222222222","source_name":"dream pop","name":"Dream Pop"}],"authority":"manual","provenance":{"kind":"manual"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	project := func(envelope librarymetadata.Envelope) publication.TagProjection {
		t.Helper()
		projection, projectErr := publication.ProjectTags(publication.TagProjectionInput{
			Release:  publication.ReleaseTags{Title: "Genre album", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist"}}, Desired: &envelope},
			Position: publication.PositionTags{Title: "Genre track", Artist: []librarymetadata.ArtistCredit{{ArtistName: "Artist"}}},
		})
		if projectErr != nil {
			t.Fatal(projectErr)
		}
		return projection
	}

	projection := project(genreEnvelope)
	writer := metadata.Writer{}
	if _, err := writer.Write(ctx, path, projection.Tags, nil); err != nil {
		t.Fatalf("write projected genres: %v", err)
	}
	capture, err := (metadata.Reader{}).Read(ctx, path, metadata.ReadRequest{})
	if err != nil {
		t.Fatalf("read native genre tags: %v", err)
	}
	want := []string{"Post-Punk", "Dream Pop"}
	if got := capture.Tags["GENRE"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("native genre readback = %#v, want ordered %#v", got, want)
	}

	clearEnvelope, err := librarymetadata.DecodeEnvelope([]byte(`{"version":1,"scope":"release","desired":{"genre":{"state":"explicit_empty","authority":"manual","provenance":{"kind":"manual"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	clearProjection := project(clearEnvelope)
	if _, ok := clearProjection.Tags["GENRE"]; !ok {
		t.Fatal("explicit-empty genre projection omitted the deletion request")
	}
	if _, err := writer.Write(ctx, path, clearProjection.Tags, nil); err != nil {
		t.Fatalf("write explicit genre clear: %v", err)
	}
	cleared, err := (metadata.Reader{}).Read(ctx, path, metadata.ReadRequest{})
	if err != nil {
		t.Fatalf("read cleared native genre tags: %v", err)
	}
	if got, present := cleared.Tags["GENRE"]; present {
		t.Fatalf("explicit-empty genre remained in native readback: %#v", got)
	}
}
