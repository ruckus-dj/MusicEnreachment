//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestAdmitManualLyricsRewriteUsesCurrentMetadataAndCASPostgreSQL(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testpostgres.OpenMigrated(t)
	initializeRiver(t, ctx, db)
	repo := NewSetupManagerRepository(db)
	installationID := uuid.New()
	verified := time.Now().UTC()
	versions, ffmpegBanner, ffprobeBanner := publicationFFmpegVersionFixture()
	installation := &ToolInstallation{
		ID: installationID, PackageKind: "ffmpeg", PlatformGOOS: runtime.GOOS, PlatformGOARCH: runtime.GOARCH,
		SourceName: "fixture", ReleaseIdentity: "release-1", State: "ready", ExecutableVerificationOutput: versions, Version: "ffmpeg 7.0 + ffprobe 7.0",
		ArtifactIdentities: json.RawMessage(`{}`), VerifiedAt: &verified,
	}
	installation.RelativePath = filepath.Join("ffmpeg", "release-1")
	if err := repo.CreateInstallation(ctx, installation); err != nil {
		t.Fatal(err)
	}
	frozenRelativePath := filepath.ToSlash(installation.RelativePath)
	selections := []SourceAnalysisToolSelection{
		{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: frozenRelativePath, Executable: "ffmpeg", Version: installation.Version, VersionBanner: ffmpegBanner},
		{PackageKind: "ffmpeg", InstallationID: installationID, RelativePath: frozenRelativePath, Executable: "ffprobe", Version: installation.Version, VersionBanner: ffprobeBanner},
	}

	type fixture struct {
		position, release, generation, epoch uuid.UUID
		revision                             int64
		lyrics                               string
		state                                librarymetadata.ValueState
	}
	makeFixture := func(state librarymetadata.ValueState, lyrics string) fixture {
		t.Helper()
		f := fixture{position: uuid.New(), generation: uuid.New(), revision: 7, lyrics: lyrics, state: state}
		if err := db.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true`).Scan(ctx, &f.epoch); err != nil {
			t.Fatal(err)
		}
		release, track := uuid.New(), uuid.New()
		f.release = release
		if _, err := db.ExecContext(ctx, `INSERT INTO local_release(id,title) VALUES (?,'release')`, release); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO local_track(id,title) VALUES (?,'track')`, track); err != nil {
			t.Fatal(err)
		}
		var metadataLyrics any
		if state == librarymetadata.StateValue {
			metadataLyrics = lyrics
		}
		envelope := librarymetadata.Envelope{Version: librarymetadata.EnvelopeVersion, Scope: librarymetadata.ScopePosition, Desired: map[string]librarymetadata.FieldValue{
			"synced_lyrics": librarymetadata.ManualFieldValue(state, metadataLyrics, state == librarymetadata.StateValue),
		}}
		metadata, err := json.Marshal(map[string]any{"local_metadata": envelope})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO release_track(id,release_id,track_id,position,metadata,metadata_revision) VALUES (?,?,?,0,?::jsonb,?)`, f.position, release, track, string(metadata), f.revision); err != nil {
			t.Fatal(err)
		}
		input := publicationInputsFixture(t, "rewrite")
		input.Version = PublicationInputsVersion
		input.QualityPolicyVersion = publication.QualityPolicyVersion
		input.Tools = append([]SourceAnalysisToolSelection(nil), selections...)
		input.Rewrite.CurrentGeneration = f.generation
		input.ManualLyrics = &PublicationManualLyricsInput{ExpectedMetadataRevision: f.revision, ConfirmedCurrentGeneration: f.generation, State: string(state), Value: lyrics}
		ownership, _ := json.Marshal(PublicationCurrentOwnership{Version: 1, Identity: input.Rewrite.Ownership})
		audio, _ := json.Marshal(PublicationCurrentAudio{Version: 1, Facts: input.Rewrite.AudioFacts})
		actualMetadata := json.RawMessage(`{"version":1,"tags":{"TITLE":["published title"],"ARTIST":["current artist"],"LYRICS":["old lyrics"]}}`)
		if _, err := db.ExecContext(ctx, `INSERT INTO track_publication(position_id,epoch,generation,relative_path,ownership,actual_audio,actual_metadata,actual_assets,quality_policy_version,validated_at) VALUES (?,?,?,'Artist/Album/track.mka',?::jsonb,?::jsonb,?::jsonb,'{}'::jsonb,1,now())`, f.position, f.epoch, f.generation, string(ownership), string(audio), string(actualMetadata)); err != nil {
			t.Fatal(err)
		}
		return f
	}
	newRequest := func(f fixture) ManualLyricsAdmission {
		input := publicationInputsFixture(t, "rewrite")
		input.Version = PublicationInputsVersion
		input.QualityPolicyVersion = publication.QualityPolicyVersion
		input.Tools = append([]SourceAnalysisToolSelection(nil), selections...)
		input.Rewrite.CurrentGeneration = f.generation
		input.ManualLyrics = &PublicationManualLyricsInput{ExpectedMetadataRevision: f.revision, ConfirmedCurrentGeneration: f.generation, State: string(f.state), Value: f.lyrics}
		frozen, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		return ManualLyricsAdmission{ExpectedReleaseID: f.release, PublicationAdmission: PublicationAdmission{
			PositionID: f.position, ExpectedReleaseID: f.release, ExpectedEpoch: f.epoch, ExpectedGeneration: uuidPointer(f.generation), CandidateGeneration: uuid.New(), IntentKind: "rewrite", ToolsReadRequired: true, Tools: append([]SourceAnalysisToolSelection(nil), selections...), FrozenInputs: frozen,
			NamespaceManifest: []PublicationNamespaceNode{{CanonicalKey: "root", RelativePath: "root", NodeKind: "dir", DirectoryScope: "root"}, {CanonicalKey: "root/lyrics.mka", RelativePath: "root/lyrics.mka", NodeKind: "file"}},
		}, ExpectedMetadataRevision: f.revision}
	}

	lyrics := "[ar:日本語]\r\n[00:01.00]Привет — café\r\n"
	f := makeFixture(librarymetadata.StateValue, lyrics)
	req := newRequest(f)
	// Deliberately forged claims must be replaced by the locked DB facts.
	var forged PublicationInputs
	if err := json.Unmarshal(req.FrozenInputs, &forged); err != nil {
		t.Fatal(err)
	}
	forged.DesiredTags = map[string][]string{"TITLE": {"caller title"}, "ARTIST": {"caller artist"}}
	req.FrozenInputs, _ = json.Marshal(forged)
	inserter := new(publicationAdmissionInserter)
	op, err := repo.AdmitManualLyricsRewrite(ctx, req, inserter)
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := db.NewRaw(`SELECT frozen_inputs FROM track_publication_attempt WHERE operation_id=?`, op).Scan(ctx, &raw); err != nil {
		t.Fatal(err)
	}
	got, err := DecodePublicationInputs(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.ManualLyrics.Value != lyrics {
		t.Fatalf("persisted lyrics = %q, want exact DB value %q", got.ManualLyrics.Value, lyrics)
	}
	if got.DesiredTags["TITLE"][0] != "published title" || got.DesiredTags["ARTIST"][0] != "current artist" {
		t.Fatalf("desired tags were not rebuilt from published metadata: %#v", got.DesiredTags)
	}
	for key := range got.DesiredTags {
		if key == "LYRICS" {
			t.Fatalf("lyrics leaked into mandatory tags: %#v", got.DesiredTags)
		}
	}
	var holds int
	if err := db.NewRaw(`SELECT count(*) FROM publication_source_hold WHERE operation_id=?`, op).Scan(ctx, &holds); err != nil {
		t.Fatal(err)
	}
	if holds != 0 {
		t.Fatalf("source holds = %d, want none", holds)
	}

	for _, mismatch := range []string{"revision", "generation", "release"} {
		t.Run(mismatch, func(t *testing.T) {
			bad := makeFixture(librarymetadata.StateValue, lyrics)
			request := newRequest(bad)
			switch mismatch {
			case "revision":
				if _, err := db.ExecContext(ctx, `UPDATE release_track SET metadata_revision=metadata_revision+1 WHERE id=?`, bad.position); err != nil {
					t.Fatal(err)
				}
			case "generation":
				request.ExpectedGeneration = uuidPointer(uuid.New())
			case "release":
				other := makeFixture(librarymetadata.StateValue, lyrics)
				request.ExpectedReleaseID = other.release
			}
			if _, err := repo.AdmitManualLyricsRewrite(ctx, request, new(publicationAdmissionInserter)); err == nil {
				t.Fatal("mismatched CAS admitted")
			}
			var attempts, operations, jobs, holds int
			if err := db.NewRaw(`SELECT count(*) FROM track_publication_attempt WHERE position_id=?`, bad.position).Scan(ctx, &attempts); err != nil {
				t.Fatal(err)
			}
			if err := db.NewRaw(`SELECT count(*) FROM operation WHERE id NOT IN (SELECT operation_id FROM track_publication_attempt)`).Scan(ctx, &operations); err != nil {
				t.Fatal(err)
			}
			if err := db.NewRaw(`SELECT count(*) FROM river_job WHERE kind='publish_track_v1'`).Scan(ctx, &jobs); err != nil {
				t.Fatal(err)
			}
			if err := db.NewRaw(`SELECT count(*) FROM publication_source_hold`).Scan(ctx, &holds); err != nil {
				t.Fatal(err)
			}
			if attempts != 0 || operations != 0 || jobs != 1 || holds != 0 {
				t.Fatalf("rejection left attempts=%d unrelated operations=%d jobs=%d holds=%d", attempts, operations, jobs, holds)
			}
		})
	}
	forgedLyrics := makeFixture(librarymetadata.StateValue, lyrics)
	forgedRequest := newRequest(forgedLyrics)
	var forgedInput PublicationInputs
	if err := json.Unmarshal(forgedRequest.FrozenInputs, &forgedInput); err != nil {
		t.Fatal(err)
	}
	forgedInput.ManualLyrics.Value = "[00:02.00]forged but valid LRC"
	forgedRequest.FrozenInputs, _ = json.Marshal(forgedInput)
	if _, err := repo.AdmitManualLyricsRewrite(ctx, forgedRequest, new(publicationAdmissionInserter)); err == nil {
		t.Fatal("forged valid LRC was admitted")
	}
	var forgedArtifacts int
	if err := db.NewRaw(`SELECT (SELECT count(*) FROM track_publication_attempt WHERE position_id=?) + (SELECT count(*) FROM operation WHERE id NOT IN (SELECT operation_id FROM track_publication_attempt))`, forgedLyrics.position).Scan(ctx, &forgedArtifacts); err != nil {
		t.Fatal(err)
	}
	if forgedArtifacts != 0 {
		t.Fatalf("forged lyrics rejection left %d admission artifacts", forgedArtifacts)
	}

	// Generic admission cannot bypass the named validated-manual path.
	direct := newRequest(makeFixture(librarymetadata.StateValue, lyrics))
	if _, err := repo.AdmitPublication(ctx, direct.PublicationAdmission, new(publicationAdmissionInserter)); err == nil {
		t.Fatal("generic V3 admission bypassed manual lyrics validation")
	}

	empty := makeFixture(librarymetadata.StateExplicitEmpty, "")
	emptyReq := newRequest(empty)
	emptyReq.NamespaceManifest = []PublicationNamespaceNode{{CanonicalKey: "root", RelativePath: "root", NodeKind: "dir", DirectoryScope: "root"}, {CanonicalKey: "root/empty-lyrics.mka", RelativePath: "root/empty-lyrics.mka", NodeKind: "file"}}
	var emptyInput PublicationInputs
	if err := json.Unmarshal(emptyReq.FrozenInputs, &emptyInput); err != nil {
		t.Fatal(err)
	}
	emptyReq.FrozenInputs, _ = json.Marshal(emptyInput)
	if _, err := repo.AdmitManualLyricsRewrite(ctx, emptyReq, new(publicationAdmissionInserter)); err != nil {
		t.Fatalf("explicit_empty admission failed: %v", err)
	}
}
