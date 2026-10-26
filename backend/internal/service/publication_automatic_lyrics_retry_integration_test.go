//go:build integration

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/lrclib"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/uptrace/bun"
)

const automaticRetryLyrics = "[00:01.250]replacement lyrics\n"

type automaticRetryEnabled struct{}

func (automaticRetryEnabled) GetLRCLIBEnabled(context.Context) (bool, error) { return true, nil }

type automaticRetryLookup struct {
	requests []lrclib.Metadata
	onLookup func() error
}

// automaticRetryCommands controls only process transport. The current FLAC
// bytes and TagLib reads/writes remain native; ffmpeg is modeled as stream-copy.
type automaticRetryCommands struct{ probeJSON []byte }

func automaticRetryProbeFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/technical/flac.json")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Streams  []json.RawMessage `json:"streams"`
		Format   json.RawMessage   `json:"format"`
		Chapters []json.RawMessage `json:"chapters,omitempty"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	kept := make([]json.RawMessage, 0, 2)
	for _, streamRaw := range document.Streams {
		var stream struct {
			Index       int    `json:"index"`
			CodecType   string `json:"codec_type"`
			Disposition struct {
				AttachedPicture int `json:"attached_pic"`
			} `json:"disposition"`
			Codec string `json:"codec_name"`
		}
		if err := json.Unmarshal(streamRaw, &stream); err != nil {
			t.Fatal(err)
		}
		if stream.CodecType == "audio" && stream.Index != 0 || stream.CodecType != "audio" && stream.Disposition.AttachedPicture != 1 {
			continue
		}
		if stream.CodecType == "video" {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(streamRaw, &fields); err != nil {
				t.Fatal(err)
			}
			fields["codec_name"] = json.RawMessage(`"png"`)
			streamRaw, err = json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
		}
		kept = append(kept, streamRaw)
	}
	document.Streams = kept
	result, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func (commands *automaticRetryCommands) Run(_ context.Context, executable string, args ...string) (PublicationCommandResult, error) {
	if len(args) == 1 && args[0] == "-version" {
		name := filepath.Base(executable)
		if strings.Contains(name, "ffprobe") {
			return PublicationCommandResult{Output: []byte("ffprobe version 7.0 build"), QuiescenceEstablished: true}, nil
		}
		return PublicationCommandResult{Output: []byte("ffmpeg version 7.0 build"), QuiescenceEstablished: true}, nil
	}
	if strings.Contains(filepath.Base(executable), "ffprobe") && containsArg(args, "format=duration") {
		var probe struct {
			Format struct {
				Duration string `json:"duration"`
			} `json:"format"`
		}
		if err := json.Unmarshal(commands.probeJSON, &probe); err != nil {
			return PublicationCommandResult{QuiescenceEstablished: true}, err
		}
		if probe.Format.Duration == "" {
			return PublicationCommandResult{QuiescenceEstablished: true}, errors.New("controlled ffprobe fixture has no measured duration")
		}
		output, err := json.Marshal(struct {
			Format struct {
				Duration string `json:"duration"`
			} `json:"format"`
		}{Format: struct {
			Duration string `json:"duration"`
		}{Duration: probe.Format.Duration}})
		return PublicationCommandResult{Output: output, QuiescenceEstablished: true}, err
	}
	input := ""
	for index := 0; index+1 < len(args); index++ {
		if args[index] == "-i" {
			input = args[index+1]
			break
		}
	}
	if input == "" || len(args) == 0 {
		return PublicationCommandResult{QuiescenceEstablished: true}, errors.New("controlled ffmpeg transport requires a frozen input")
	}
	contents, err := os.ReadFile(input)
	if err != nil {
		return PublicationCommandResult{QuiescenceEstablished: true}, err
	}
	if err := os.WriteFile(args[len(args)-1], contents, 0o600); err != nil {
		return PublicationCommandResult{QuiescenceEstablished: true}, err
	}
	return PublicationCommandResult{QuiescenceEstablished: true}, nil
}

func (commands *automaticRetryCommands) RunFile(_ context.Context, request tools.FileCommandRequest) (PublicationCommandResult, error) {
	if request.Input == nil || !strings.Contains(filepath.Base(request.Executable), "ffprobe") {
		return PublicationCommandResult{QuiescenceEstablished: true}, errors.New("controlled ffprobe transport requires the borrowed current FLAC")
	}
	if containsArg(request.Args, "format=duration") {
		return commands.Run(context.Background(), request.Executable, "-v", "error", "-show_entries", "format=duration", "-of", "json", "borrowed-current-flac")
	}
	return PublicationCommandResult{Output: append([]byte(nil), commands.probeJSON...), QuiescenceEstablished: true}, nil
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func (lookup *automaticRetryLookup) Lookup(_ context.Context, request lrclib.Metadata) (lrclib.LookupResult, error) {
	lookup.requests = append(lookup.requests, request)
	if lookup.onLookup != nil {
		if err := lookup.onLookup(); err != nil {
			return lrclib.LookupResult{}, err
		}
	}
	return lrclib.LookupResult{Found: true, Lyrics: automaticRetryLyrics}, nil
}

type automaticRetryMissingLyricsWriter struct {
	metadata.Writer
	calls int
}

func (writer *automaticRetryMissingLyricsWriter) WriteFlacTagsPreservingArtwork(ctx context.Context, path string, tags map[string][]string, before metadata.FlacArtworkSnapshot) (metadata.WriteResult, error) {
	writer.calls++
	mandatory := cloneTagMap(tags)
	delete(mandatory, "LYRICS")
	// Model a native writer that omits the optional field, using a real native
	// mutation and fresh readback rather than fabricating the optional status.
	return metadata.WriteFlacTagsPreservingArtwork(ctx, path, mandatory, before)
}

func TestSimpleInitialLyricsMismatchRebuildSettlesWithoutOptionalLyricsPostgreSQL(t *testing.T) {
	const title, artist = "Analysed track", "Analysed artist"
	var expectedGeneration uuid.UUID
	lyricsDigest := sha256.Sum256([]byte(automaticRetryLyrics))
	testPublicationSourceExecution(t, "Track.flac", true, publicationDeliveryScenario{
		beforeAdmission: func(ctx context.Context, db *bun.DB, _ *persistence.SetupManagerRepository, id uuid.UUID) {
			metadataID := uuid.New()
			if _, err := db.ExecContext(ctx, `INSERT INTO media_metadata_result(id,observed_tags,provenance,observed_at,winning_result_id,applied_operation_id,properties)
				VALUES (?,'{"TITLE":["Analysed track"],"ARTIST":["Analysed artist"]}'::jsonb,'{}'::jsonb,now(),?,?, '{"length":183375000000}'::jsonb)`, metadataID, metadataID, uuid.New()); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, `UPDATE track_source SET source_metadata_result_id=? WHERE track_id IN (SELECT track_id FROM release_track WHERE release_id=?)`, metadataID, id); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, `UPDATE media_variant SET ffprobe_json=jsonb_set(ffprobe_json,'{format,duration}','"183.456789"'::jsonb) WHERE id IN (SELECT media_variant_id FROM track_source WHERE track_id IN (SELECT track_id FROM release_track WHERE release_id=?))`, id); err != nil {
				t.Fatal(err)
			}
		},
		run: func(ctx context.Context, db *bun.DB, manager *persistence.SetupManagerRepository, executor *PublicationExecutor, operationID uuid.UUID, riverJobID int64, _, output, target string, _ []byte) {
			attempt, err := manager.ReadPublicationAttempt(ctx, operationID)
			if err != nil || attempt == nil {
				t.Fatalf("read frozen initial-lyrics attempt: %+v, %v", attempt, err)
			}
			input, err := persistence.DecodePublicationInputs(attempt.FrozenInputs)
			if err != nil || input.InitialLyrics == nil || input.Audio == nil || input.Audio.AnalysedMetadata == nil || input.Audio.AnalysedMetadata.DurationSeconds == nil {
				t.Fatalf("frozen automatic-lyrics inputs: %+v, %v", input, err)
			}
			if len(input.DesiredTags["TITLE"]) != 1 {
				t.Fatalf("frozen publication title is missing: %#v", input.DesiredTags)
			}
			frozenTitle := input.DesiredTags["TITLE"][0]
			expectedGeneration = attempt.CandidateGeneration
			// Delivery must use the admitted snapshot even if canonical source metadata
			// is refreshed after admission.
			if _, err := db.ExecContext(ctx, `UPDATE media_metadata_result SET observed_tags=?::jsonb,winning_result_id=?,observed_at=now() WHERE id=?`, `{"TITLE":["Refreshed title"],"ARTIST":["Refreshed artist"]}`, uuid.New(), input.Audio.AnalysedMetadata.MetadataResultID); err != nil {
				t.Fatal(err)
			}
			executor.commands = simpleCoverDeliveryCommands{}
			lookup := &automaticRetryLookup{}
			executor.lyrics = NewPublicationLyricsResolver(automaticRetryEnabled{}, lookup)
			writer := &automaticRetryMissingLyricsWriter{}
			executor.metadata = writer
			if err := executor.ExecutePublicationDelivery(ctx, operationID, riverJobID, 1); err != nil {
				t.Fatalf("initial-lyrics publication: %v", err)
			}
			if len(lookup.requests) != 1 || lookup.requests[0].Artist != artist || lookup.requests[0].Title != title || lookup.requests[0].Duration != 183.456789 {
				t.Fatalf("automatic lyrics lookup did not use frozen metadata: %+v", lookup.requests)
			}
			if writer.calls != 2 {
				t.Fatalf("metadata writes = %d, want original candidate and lyric-free rebuild", writer.calls)
			}
			published := filepath.Join(output, filepath.FromSlash(target))
			capture, err := (metadata.Reader{}).Read(ctx, published, metadata.ReadRequest{})
			if err != nil || len(capture.Tags["LYRICS"]) != 0 {
				t.Fatalf("mismatched optional lyrics survived fallback: %#v, %v", capture.Tags["LYRICS"], err)
			}
			var raw string
			if err := db.NewRaw(`SELECT initial_lyrics::text FROM track_publication WHERE position_id=?`, attempt.PositionID).Scan(ctx, &raw); err != nil {
				t.Fatal(err)
			}
			var projection persistence.PublicationInitialLyricsProjection
			if err := json.Unmarshal([]byte(raw), &projection); err != nil {
				t.Fatalf("decode initial lyrics projection %q: %v", raw, err)
			}
			if projection.Generation != expectedGeneration || projection.Source != "lrclib" || projection.SHA256 != hex.EncodeToString(lyricsDigest[:]) ||
				projection.Outcome != persistence.PublicationOptionalLyricsRebuiltWithoutLyrics || projection.OutcomeWarningCode != "readback_mismatch" || projection.ResolutionWarningCode != "" {
				t.Fatalf("settled optional lyrics projection = %+v", projection)
			}
			var currentTitle string
			if err := db.NewRaw(`SELECT actual_metadata->'tags'->'TITLE'->>0 FROM track_publication WHERE position_id=?`, attempt.PositionID).Scan(ctx, &currentTitle); err != nil || currentTitle != frozenTitle {
				t.Fatalf("publication ignored frozen metadata after source refresh: title=%q err=%v", currentTitle, err)
			}
		},
	})
}

func TestAutomaticLyricsRetryTopologyAllowsOnlyFrozenAttachedCover(t *testing.T) {
	cover := persistence.PublicationCoverOutcome{State: persistence.PublicationAssetEmbedded, MIME: "image/png"}
	valid := `{"streams":[{"index":0,"codec_type":"audio","codec_name":"flac"},{"index":1,"codec_type":"video","codec_name":"png","disposition":{"attached_pic":1}}]}`
	if err := validateAutomaticLyricsRetryTopology([]byte(valid), cover); err != nil {
		t.Fatalf("verified attached cover rejected: %v", err)
	}
	absent := persistence.PublicationCoverOutcome{State: persistence.PublicationAssetAbsent}
	if err := validateAutomaticLyricsRetryTopology([]byte(`{"streams":[{"index":0,"codec_type":"audio","codec_name":"flac"}]}`), absent); err != nil {
		t.Fatalf("coverless current artifact rejected: %v", err)
	}
	for _, test := range []struct {
		name  string
		raw   string
		cover persistence.PublicationCoverOutcome
	}{
		{name: "non-attached video", raw: `{"streams":[{"index":0,"codec_type":"audio"},{"index":1,"codec_type":"video","codec_name":"png"}]}`, cover: cover},
		{name: "subtitle", raw: `{"streams":[{"index":0,"codec_type":"audio"},{"index":1,"codec_type":"subtitle","codec_name":"subrip"}]}`, cover: cover},
		{name: "cover without frozen cover authority", raw: valid, cover: absent},
		{name: "cover codec mismatch", raw: strings.Replace(valid, `"png"`, `"mjpeg"`, 1), cover: cover},
		{name: "multiple attached pictures", raw: `{"streams":[{"index":0,"codec_type":"audio"},{"index":1,"codec_type":"video","codec_name":"png","disposition":{"attached_pic":1}},{"index":2,"codec_type":"video","codec_name":"png","disposition":{"attached_pic":1}}]}`, cover: cover},
		{name: "multiple audio streams", raw: `{"streams":[{"index":0,"codec_type":"audio"},{"index":1,"codec_type":"audio"},{"index":2,"codec_type":"video","codec_name":"png","disposition":{"attached_pic":1}}]}`, cover: cover},
		{name: "chapters", raw: `{"streams":[{"index":0,"codec_type":"audio"}],"chapters":[{}]}`, cover: absent},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateAutomaticLyricsRetryTopology([]byte(test.raw), test.cover); err == nil {
				t.Fatal("unapproved topology accepted")
			}
		})
	}
}
