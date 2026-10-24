//go:build integration

package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/uptrace/bun"
)

func TestFirstPublicationExplicitAudioStreamAdmissionPostgreSQL(t *testing.T) {
	ran := false
	testPublicationSourceExecution(t, "Track.flac", true, false, func(db *bun.DB, releaseID, positionID, trackID, epoch uuid.UUID, publications *LibraryPublications) {
		ran = true
		ctx := context.Background()
		var locationID, variantID uuid.UUID
		if err := db.NewRaw(`SELECT sl.id,mv.id FROM track_source ts JOIN media_variant mv ON mv.id=ts.media_variant_id JOIN source_location sl ON sl.media_variant_id=mv.id WHERE ts.track_id=?`, trackID).Scan(ctx, &locationID, &variantID); err != nil {
			t.Fatal(err)
		}
		probe := `{"format":{"format_name":"flac"},"streams":[{"index":0,"codec_type":"video","codec_name":"mjpeg"},{"index":2,"codec_type":"audio","codec_name":"flac","channels":6,"channel_layout":"5.1"},{"index":5,"codec_type":"audio","codec_name":"flac","channels":2,"channel_layout":"stereo","sample_rate":"96000"}]}`
		if _, err := db.ExecContext(ctx, `UPDATE media_variant SET ffprobe_json=?::jsonb,audio_stream_count=2 WHERE id=?`, probe, variantID); err != nil {
			t.Fatal(err)
		}
		choice := &FirstPublicationSourceChoice{LocationID: locationID, MediaVariantID: variantID, StreamIndex: 5}
		preview, err := publications.PreviewFirstSelected(ctx, releaseID, positionID, choice)
		if err != nil || len(preview.Blockers) != 0 || preview.SelectedSource == nil || preview.SelectedSource.StreamIndex != 5 || preview.SelectedSource.Quality.ChannelLayout != "stereo" {
			t.Fatalf("selected preview = %+v, %v", preview, err)
		}
		// A different stream's confirmation digest must fail before any durable
		// admission artifact is created.
		otherChoice := &FirstPublicationSourceChoice{LocationID: locationID, MediaVariantID: variantID, StreamIndex: 2}
		otherPreview, err := publications.PreviewFirstSelected(ctx, releaseID, positionID, otherChoice)
		if err != nil || otherPreview.ContextDigest == preview.ContextDigest {
			t.Fatalf("substitute preview digest = %q, original %q, err %v", otherPreview.ContextDigest, preview.ContextDigest, err)
		}
		beforeJobs, beforeOps, beforeClaims := integrationPublicationArtifacts(t, db)
		_, err = publications.AdmitFirst(ctx, releaseID, positionID, FirstPublicationConfirmation{PreviewDigest: preview.ContextDigest, ExpectedEpoch: epoch, SourceChoice: otherChoice})
		if err == nil {
			t.Fatal("admitted confirmation with substituted stream choice")
		}
		afterJobs, afterOps, afterClaims := integrationPublicationArtifacts(t, db)
		if beforeJobs != afterJobs || beforeOps != afterOps || beforeClaims != afterClaims {
			t.Fatalf("rejected substitution mutated artifacts: before jobs/ops/claims=%v/%v/%v after=%v/%v/%v", beforeJobs, beforeOps, beforeClaims, afterJobs, afterOps, afterClaims)
		}
		id, err := publications.AdmitFirst(ctx, releaseID, positionID, FirstPublicationConfirmation{PreviewDigest: preview.ContextDigest, ExpectedEpoch: epoch, SourceChoice: choice})
		if err != nil {
			t.Fatalf("admit selected stream: %v", err)
		}
		attempt, err := persistence.NewSetupManagerRepository(db).ReadPublicationAttempt(ctx, id)
		if err != nil || attempt == nil {
			t.Fatalf("read attempt = %+v, %v", attempt, err)
		}
		inputs, err := persistence.DecodePublicationInputs(attempt.FrozenInputs)
		if err != nil {
			t.Fatal(err)
		}
		if inputs.Audio == nil || inputs.Audio.LocationID != locationID || inputs.Audio.MediaVariantID != variantID || inputs.Audio.AudioFacts.StreamIndex != 5 || inputs.Audio.AudioFacts.ChannelLayout != "stereo" || inputs.Audio.AudioFacts.SampleRate == nil || *inputs.Audio.AudioFacts.SampleRate != 96000 || inputs.SourceAuthorityMode != "manual" {
			t.Fatalf("frozen selected stream = %+v", inputs.Audio)
		}
	})
	if !ran {
		t.Fatal("explicit stream admission fixture callback did not run")
	}
}

func TestFirstPublicationAdmitsSingleStreamMatroskaFLACStereoPostgreSQL(t *testing.T) {
	ran := false
	testPublicationSourceExecution(t, "Track.flac", true, false, func(db *bun.DB, releaseID, positionID, trackID, epoch uuid.UUID, publications *LibraryPublications) {
		ran = true
		ctx := context.Background()
		var locationID, variantID uuid.UUID
		if err := db.NewRaw(`SELECT sl.id,mv.id FROM track_source ts JOIN media_variant mv ON mv.id=ts.media_variant_id JOIN source_location sl ON sl.media_variant_id=mv.id WHERE ts.track_id=?`, trackID).Scan(ctx, &locationID, &variantID); err != nil {
			t.Fatal(err)
		}
		probe := `{"format":{"format_name":"matroska,webm"},"streams":[{"index":0,"codec_type":"audio","codec_name":"flac","channels":2,"channel_layout":"stereo","sample_rate":"44100"}]}`
		result, err := db.ExecContext(ctx, `UPDATE media_variant SET ffprobe_json=?::jsonb,audio_stream_count=1 WHERE id=?`, probe, variantID)
		if err != nil {
			t.Fatal(err)
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			t.Fatalf("update probe row count = %d, %v", count, err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE track_source SET selected=false WHERE track_id=? AND media_variant_id=? AND decision_method='manual'`, trackID, variantID); err != nil {
			t.Fatal(err)
		}
		choice := &FirstPublicationSourceChoice{LocationID: locationID, MediaVariantID: variantID, StreamIndex: 0}
		preview, err := publications.PreviewFirstSelected(ctx, releaseID, positionID, choice)
		if err != nil || len(preview.Blockers) != 0 || preview.SelectedSource == nil || preview.SelectedSource.StreamIndex != 0 || preview.SelectedSource.Quality.ChannelLayout != "stereo" {
			t.Fatalf("single-stream Matroska FLAC preview = %+v, %v", preview, err)
		}
		if preview.SelectedSource.Selected || preview.SelectedSource.RequiresExplicitStreamSelection || preview.SelectedSource.AudioStreamCount != 1 {
			t.Fatalf("single-stream explicit source choice changed association or stream semantics: %+v", preview.SelectedSource)
		}
		id, err := publications.AdmitFirst(ctx, releaseID, positionID, FirstPublicationConfirmation{PreviewDigest: preview.ContextDigest, ExpectedEpoch: epoch, SourceChoice: choice})
		if err != nil {
			t.Fatalf("admit single-stream Matroska FLAC: %v", err)
		}
		attempt, err := persistence.NewSetupManagerRepository(db).ReadPublicationAttempt(ctx, id)
		if err != nil || attempt == nil {
			t.Fatalf("read admitted attempt = %+v, %v", attempt, err)
		}
		inputs, err := persistence.DecodePublicationInputs(attempt.FrozenInputs)
		if err != nil {
			t.Fatal(err)
		}
		if inputs.Audio == nil || inputs.Audio.AudioFacts.StreamIndex != 0 || inputs.Audio.AudioFacts.ChannelLayout != "stereo" || inputs.SourceAuthorityMode != "manual" {
			t.Fatalf("frozen explicit stereo audio facts = %+v, authority=%q", inputs.Audio, inputs.SourceAuthorityMode)
		}
		jobs, operations, _ := integrationPublicationArtifacts(t, db)
		if jobs == 0 || operations == 0 {
			t.Fatalf("admission artifacts missing: jobs=%d operations=%d", jobs, operations)
		}
	})
	if !ran {
		t.Fatal("single-stream Matroska FLAC admission fixture callback did not run")
	}
}

func TestFirstPublicationRejectsChangedNativeDestinationPostgreSQL(t *testing.T) {
	ran := false
	testPublicationSourceExecution(t, "Track.flac", true, false, func(db *bun.DB, releaseID, positionID, _ uuid.UUID, epoch uuid.UUID, publications *LibraryPublications) {
		ran = true
		ctx := context.Background()
		// The helper removes its synthetic attempt/reservations; discard its
		// namespace claims too so this is genuinely a first, unowned destination.
		if _, err := db.ExecContext(ctx, `DELETE FROM publication_namespace_claim WHERE epoch=?`, epoch); err != nil {
			t.Fatal(err)
		}
		runtimeSettings, err := publications.settings.ReadRuntimeSettings(ctx)
		if err != nil {
			t.Fatal(err)
		}
		preview, err := publications.PreviewFirst(ctx, releaseID, positionID)
		if err != nil || len(preview.Blockers) != 0 || strings.Join(preview.ResolvedPathComponents, "/") != "Track.flac" {
			t.Fatalf("initial native preview = %+v, %v", preview, err)
		}
		unmanaged := filepath.Join(runtimeSettings.OutputDirectory, "Track.flac")
		if err := os.WriteFile(unmanaged, []byte("unmanaged"), 0o600); err != nil {
			t.Fatal(err)
		}
		beforeJobs, beforeOps, beforeClaims := integrationPublicationArtifacts(t, db)
		_, err = publications.AdmitFirst(ctx, releaseID, positionID, FirstPublicationConfirmation{PreviewDigest: preview.ContextDigest, ExpectedEpoch: epoch})
		if !errors.Is(err, persistence.ErrPublicationAdmissionConflict) {
			t.Fatalf("changed destination must invalidate confirmation: %v", err)
		}
		afterJobs, afterOps, afterClaims := integrationPublicationArtifacts(t, db)
		if beforeJobs != afterJobs || beforeOps != afterOps || beforeClaims != afterClaims {
			t.Fatal("stale native confirmation created admission artifacts")
		}
		renewed, err := publications.PreviewFirst(ctx, releaseID, positionID)
		if err != nil || len(renewed.Blockers) != 0 || renewed.ContextDigest == preview.ContextDigest || strings.Join(renewed.ResolvedPathComponents, "/") == "Track.flac" {
			t.Fatalf("renewed collision preview = %+v, %v", renewed, err)
		}
		repeated, err := publications.PreviewFirst(ctx, releaseID, positionID)
		if err != nil || repeated.ContextDigest != renewed.ContextDigest {
			t.Fatalf("unchanged native resolution must have stable digest: %v", err)
		}
		id, err := publications.AdmitFirst(ctx, releaseID, positionID, FirstPublicationConfirmation{PreviewDigest: renewed.ContextDigest, ExpectedEpoch: epoch})
		if err != nil {
			t.Fatalf("confirm renewed destination: %v", err)
		}
		attempt, err := persistence.NewSetupManagerRepository(db).ReadPublicationAttempt(ctx, id)
		if err != nil || attempt == nil {
			t.Fatalf("read admitted attempt: %v", err)
		}
		var manifest struct {
			Version int                                    `json:"version"`
			Nodes   []persistence.PublicationNamespaceNode `json:"nodes"`
		}
		if err := json.Unmarshal(attempt.NamespaceManifest, &manifest); err != nil {
			t.Fatal(err)
		}
		if manifest.Version != 1 || strings.Join(resolvedFileComponents(manifest.Nodes), "/") != strings.Join(renewed.ResolvedPathComponents, "/") {
			t.Fatalf("admission changed confirmed native path: %+v", manifest)
		}
		contents, err := os.ReadFile(unmanaged)
		if err != nil || string(contents) != "unmanaged" {
			t.Fatalf("unmanaged collision changed: %q, %v", contents, err)
		}
	})
	if !ran {
		t.Fatal("native destination fixture callback did not run")
	}
}

func TestFirstPublicationCoverAuthorityInvalidatesPreviewPostgreSQL(t *testing.T) {
	ran := false
	testPublicationSourceExecution(t, "Track.flac", true, false, func(db *bun.DB, releaseID, positionID, _ uuid.UUID, epoch uuid.UUID, publications *LibraryPublications) {
		ran = true
		ctx := context.Background()
		if _, err := db.ExecContext(ctx, `DELETE FROM publication_namespace_claim WHERE epoch=?`, epoch); err != nil {
			t.Fatal(err)
		}
		var data bytes.Buffer
		if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
			t.Fatal(err)
		}
		covers := persistence.NewLibraryRepository(db)
		initial, err := covers.ReadReleaseCover(ctx, releaseID)
		if err != nil {
			t.Fatal(err)
		}
		pending, err := covers.SetPendingReleaseCover(ctx, releaseID, initial.Revision, uuid.New(), data.Bytes(), "image/png")
		if err != nil {
			t.Fatal(err)
		}
		preview, err := publications.PreviewFirst(ctx, releaseID, positionID)
		if err != nil || len(preview.Blockers) != 0 {
			t.Fatalf("preview = %+v, %v", preview, err)
		}
		alternate := image.NewRGBA(image.Rect(0, 0, 1, 1))
		alternate.Set(0, 0, color.White)
		var alternateData bytes.Buffer
		if err := png.Encode(&alternateData, alternate); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(alternateData.Bytes())
		if _, err := db.ExecContext(ctx, `UPDATE release_publication_cover SET pending_bytes=?,pending_sha256=?,pending_size=? WHERE release_id=?`, alternateData.Bytes(), digest[:], alternateData.Len(), releaseID); err != nil {
			t.Fatal(err)
		}
		pendingOnly, err := publications.PreviewFirst(ctx, releaseID, positionID)
		if err != nil || pendingOnly.ContextDigest != preview.ContextDigest {
			t.Fatalf("pending bytes changed preview freshness: %q != %q, %v", pendingOnly.ContextDigest, preview.ContextDigest, err)
		}
		beforeJobs, beforeOps, beforeClaims := integrationPublicationArtifacts(t, db)
		if _, err := covers.ApplyPendingReleaseCover(ctx, releaseID, pending.Revision, *pending.PendingGeneration); err != nil {
			t.Fatal(err)
		}
		_, err = publications.AdmitFirst(ctx, releaseID, positionID, FirstPublicationConfirmation{PreviewDigest: preview.ContextDigest, ExpectedEpoch: epoch})
		if !errors.Is(err, persistence.ErrPublicationAdmissionConflict) {
			t.Fatalf("stale cover preview admission error = %v", err)
		}
		afterJobs, afterOps, afterClaims := integrationPublicationArtifacts(t, db)
		if beforeJobs != afterJobs || beforeOps != afterOps || beforeClaims != afterClaims {
			t.Fatal("stale cover preview created admission artifacts")
		}
		fresh, err := publications.PreviewFirst(ctx, releaseID, positionID)
		if err != nil || fresh.ContextDigest == preview.ContextDigest {
			t.Fatalf("cover authority change did not refresh preview digest: %q -> %q (%v)", preview.ContextDigest, fresh.ContextDigest, err)
		}
	})
	if !ran {
		t.Fatal("cover preview fixture callback did not run")
	}
}

func integrationPublicationArtifacts(t *testing.T, db *bun.DB) (int, int, int) {
	t.Helper()
	var jobs, operations, claims int
	if err := db.NewRaw(`SELECT count(*) FROM river_job WHERE kind='publish_track_v1'`).Scan(context.Background(), &jobs); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM operation WHERE kind='publish_track_v1'`).Scan(context.Background(), &operations); err != nil {
		t.Fatal(err)
	}
	if err := db.NewRaw(`SELECT count(*) FROM publication_namespace_reservation`).Scan(context.Background(), &claims); err != nil {
		t.Fatal(err)
	}
	return jobs, operations, claims
}
