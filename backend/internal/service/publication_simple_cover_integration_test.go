//go:build integration

package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	integrationtools "github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/uptrace/bun"
)

func TestSimplePublicationEmbedsFrozenCoverDespiteLaterSelectionPostgreSQL(t *testing.T) {
	for _, outcome := range []string{"verified", "write_failed", "read_failed", "mismatch"} {
		t.Run(outcome, func(t *testing.T) { testSimpleFrozenCover(t, outcome) })
	}
}

func testSimpleFrozenCover(t *testing.T, outcome string) {
	t.Helper()
	const lyrics = automaticRetryLyrics
	imageA, imageB := simpleCoverPNG(t, color.RGBA{R: 255, A: 255}), simpleCoverPNG(t, color.RGBA{B: 255, A: 255})
	generationA := uuid.New()
	var releaseID uuid.UUID
	testPublicationSourceExecution(t, "Track.flac", true, publicationDeliveryScenario{
		beforeAdmission: func(ctx context.Context, db *bun.DB, _ *persistence.SetupManagerRepository, id uuid.UUID) {
			releaseID = id
			metadataID := uuid.New()
			if _, err := db.ExecContext(ctx, `INSERT INTO media_metadata_result(id,observed_tags,provenance,observed_at,winning_result_id,applied_operation_id,cover_observed,properties)
				VALUES (?,'{"TITLE":["Analysed track"],"ARTIST":["Analysed artist"]}'::jsonb,'{}'::jsonb,now(),?,?,true,'{"length":183375000000}'::jsonb)`, metadataID, metadataID, uuid.New()); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, `UPDATE track_source SET source_metadata_result_id=? WHERE track_id IN (SELECT track_id FROM release_track WHERE release_id=?)`, metadataID, id); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, `UPDATE media_variant SET ffprobe_json=jsonb_set(ffprobe_json,'{format,duration}','"183.456789"'::jsonb) WHERE id IN (SELECT media_variant_id FROM track_source WHERE track_id IN (SELECT track_id FROM release_track WHERE release_id=?))`, id); err != nil {
				t.Fatal(err)
			}
			library := persistence.NewLibraryRepository(db)
			pending, err := library.SetPendingReleaseCover(ctx, id, 1, generationA, imageA, "image/png")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := library.ApplyPendingReleaseCover(ctx, id, pending.Revision, generationA); err != nil {
				t.Fatal(err)
			}
		},
		run: func(ctx context.Context, db *bun.DB, manager *persistence.SetupManagerRepository, executor *PublicationExecutor, operationID uuid.UUID, jobID int64, source, output, target string, sourceBytes []byte) {
			attempt, err := manager.ReadPublicationAttempt(ctx, operationID)
			if err != nil || attempt == nil {
				t.Fatalf("read admitted metadata snapshot: %v", err)
			}
			frozen, err := persistence.DecodePublicationInputs(attempt.FrozenInputs)
			if err != nil || frozen.Audio == nil || frozen.Audio.AnalysedMetadata == nil || frozen.Audio.AnalysedMetadata.DurationSeconds == nil || *frozen.Audio.AnalysedMetadata.DurationSeconds != 183.456789 {
				t.Fatalf("admission lost exact stored probe duration: %+v, %v", frozen.Audio, err)
			}
			if _, err := db.ExecContext(ctx, `UPDATE media_metadata_result SET observed_tags='{"TITLE":["Refreshed title"],"ARTIST":["Refreshed artist"]}'::jsonb,winning_result_id=?,observed_at=now() WHERE id=?`, uuid.New(), frozen.Audio.AnalysedMetadata.MetadataResultID); err != nil {
				t.Fatal(err)
			}
			var frozenGeneration uuid.UUID
			if err := db.NewRaw(`SELECT generation FROM publication_attempt_cover_input WHERE operation_id=?`, operationID).Scan(ctx, &frozenGeneration); err != nil || frozenGeneration != generationA {
				t.Fatalf("admitted cover generation = %s, %v", frozenGeneration, err)
			}
			library := persistence.NewLibraryRepository(db)
			selected, err := library.ReadReleaseCover(ctx, releaseID)
			if err != nil {
				t.Fatal(err)
			}
			generationB := uuid.New()
			pending, err := library.SetPendingReleaseCover(ctx, releaseID, selected.Revision, generationB, imageB, "image/png")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := library.ApplyPendingReleaseCover(ctx, releaseID, pending.Revision, generationB); err != nil {
				t.Fatal(err)
			}
			executor.commands = simpleCoverDeliveryCommands{}
			lookup := &automaticRetryLookup{}
			executor.lyrics = NewPublicationLyricsResolver(automaticRetryEnabled{}, lookup)
			if outcome != "verified" {
				executor.metadata = simpleCoverFaultWriter{outcome: outcome}
			}
			if err := executor.ExecutePublicationDelivery(ctx, operationID, jobID, 1); err != nil {
				t.Fatal(err)
			}
			if len(lookup.requests) != 1 {
				t.Fatalf("cover handling repeated lyrics acquisition: %d calls", len(lookup.requests))
			}
			if request := lookup.requests[0]; request.Artist != "Analysed artist" || request.Title != "Analysed track" || request.Duration != 183.456789 {
				t.Fatalf("delivery did not use frozen analysed metadata: %+v", request)
			}
			published := filepath.Join(output, filepath.FromSlash(target))
			capture, err := (metadata.Reader{}).Read(ctx, published, metadata.ReadRequest{CoverInspection: true})
			if err != nil {
				t.Fatal(err)
			}
			if got := capture.Tags["LYRICS"]; len(got) != 1 || got[0] != lyrics {
				t.Fatalf("cover embedding changed verified lyrics: %q", got)
			}
			assets, err := ObservePublicationAssets(capture)
			digestA := sha256.Sum256(imageA)
			mime := "image/png"
			derivative, thumbnailErr := publicationCoverThumbnail(ctx, t.TempDir(), &persistence.PublicationAttemptCoverInput{Bytes: imageA, MIME: &mime})
			if thumbnailErr != nil {
				t.Fatal(thumbnailErr)
			}
			derivativeDigest := sha256.Sum256(derivative.Data)
			if err != nil || outcome == "verified" && assets.Cover.SHA256 != hex.EncodeToString(derivativeDigest[:]) || outcome != "verified" && assets.Cover.State != persistence.PublicationAssetAbsent {
				t.Fatalf("published frozen cover = %+v, %v", assets.Cover, err)
			}
			actual, err := os.ReadFile(published)
			if err != nil || !bytes.Equal(simpleCoverFLACFrames(t, actual), simpleCoverFLACFrames(t, sourceBytes)) {
				t.Fatalf("cover embedding changed encoded audio: %v", err)
			}
			var raw string
			if err := db.NewRaw(`SELECT cover_embedding::text FROM track_publication WHERE position_id=(SELECT id FROM release_track WHERE release_id=?)`, releaseID).Scan(ctx, &raw); err != nil {
				t.Fatal(err)
			}
			var projection persistence.PublicationCoverEmbeddingProjection
			warning := ""
			if outcome != "verified" {
				warning = "cover_" + outcome
			}
			if err := json.Unmarshal([]byte(raw), &projection); err != nil || projection.RequestedGeneration != generationA || projection.Outcome != outcome || projection.WarningCode != warning {
				t.Fatalf("current cover summary = %s, %v", raw, err)
			}
			if projection.RequestedSHA256 != hex.EncodeToString(digestA[:]) || projection.Version != persistence.PublicationCoverEmbeddingDerivativeProjectionVersion {
				t.Fatalf("original cover receipt binding changed: %+v", projection)
			}
			if outcome == "verified" && (projection.EmbeddedSHA256 != hex.EncodeToString(derivativeDigest[:]) || projection.EmbeddedMIME != "image/png" || projection.EmbeddedSize == nil || *projection.EmbeddedSize != int64(len(derivative.Data))) {
				t.Fatalf("embedded derivative facts were not committed: %+v", projection)
			}
			var count int
			if err := db.NewRaw(`SELECT count(*) FROM publication_attempt_cover_input WHERE operation_id=?`, operationID).Scan(ctx, &count); err != nil || count != 0 {
				t.Fatalf("consumed cover receipt count = %d, %v", count, err)
			}
			if err := os.Remove(source); err != nil {
				t.Fatal(err)
			}
			if err := executor.ExecutePublicationDelivery(ctx, operationID, jobID, 1); err != nil {
				t.Fatalf("completed cover redelivery without source: %v", err)
			}
			operation, err := manager.GetOperation(ctx, operationID)
			if err != nil || operation.State != "succeeded" {
				t.Fatalf("publication operation = %+v, %v", operation, err)
			}
		},
	})
}

type simpleCoverDeliveryCommands struct{ audioDeliveryTestCommands }

func (simpleCoverDeliveryCommands) RunFile(ctx context.Context, request integrationtools.FileCommandRequest) (PublicationCommandResult, error) {
	capture, err := (metadata.Reader{}).Read(ctx, request.Input.Name(), metadata.ReadRequest{CoverInspection: true})
	if err != nil {
		return PublicationCommandResult{QuiescenceEstablished: true}, err
	}
	assets, err := ObservePublicationAssets(capture)
	if err != nil {
		return PublicationCommandResult{QuiescenceEstablished: true}, err
	}
	streams := `{"index":0,"codec_type":"audio","codec_name":"flac","channels":2}`
	if assets.Cover.State == persistence.PublicationAssetEmbedded {
		streams += `,{"index":1,"codec_type":"video","codec_name":"png","disposition":{"attached_pic":1}}`
	}
	return PublicationCommandResult{QuiescenceEstablished: true, Output: []byte(`{"format":{"format_name":"flac"},"streams":[` + streams + `]}`)}, nil
}

type simpleCoverFaultWriter struct {
	metadata.Writer
	outcome string
}

func (writer simpleCoverFaultWriter) WriteWithOptionalAssets(ctx context.Context, path string, tags map[string][]string, cover *metadata.Cover) (metadata.OptionalAssetsWriteResult, error) {
	result, err := writer.Writer.WriteWithOptionalAssets(ctx, path, tags, cover)
	if err != nil {
		return result, err
	}
	// Deliberately leave a mutated candidate behind. The service must restore
	// its untouched baseline, not trust the partially successful optional write.
	switch writer.outcome {
	case "write_failed":
		result.CoverStatus = metadata.OptionalCoverWriteFailed
	case "read_failed":
		result.CoverStatus = metadata.OptionalCoverReadFailed
	case "mismatch":
		result.CoverStatus = metadata.OptionalCoverMismatch
	}
	return result, nil
}

func simpleCoverPNG(t *testing.T, pixel color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.SetRGBA(0, 0, pixel)
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func simpleCoverFLACFrames(t *testing.T, contents []byte) []byte {
	t.Helper()
	if len(contents) < 4 || string(contents[:4]) != "fLaC" {
		t.Fatal("invalid FLAC fixture")
	}
	for offset := 4; offset+4 <= len(contents); {
		last := contents[offset]&0x80 != 0
		size := int(contents[offset+1])<<16 | int(contents[offset+2])<<8 | int(contents[offset+3])
		offset += 4 + size
		if offset > len(contents) {
			t.Fatal("truncated FLAC metadata")
		}
		if last {
			return contents[offset:]
		}
	}
	t.Fatal("missing FLAC metadata terminator")
	return nil
}
