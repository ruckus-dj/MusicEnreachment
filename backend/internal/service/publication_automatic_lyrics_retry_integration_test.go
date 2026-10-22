//go:build integration

package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/lrclib"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
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
	return PublicationCommandResult{Output: append([]byte(nil), commands.probeJSON...), QuiescenceEstablished: true}, nil
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

func TestAutomaticLyricsRetryRejectsChangedManualAuthorityBeforeExposurePostgreSQL(t *testing.T) {
	testPreparedManualLyricsFLACOwner(t, manualLyricsScenario{
		withCover: true, delivery: true, automaticRetry: true, editManualAuthorityDuringLookup: true,
	})
}

func TestAutomaticLyricsRetryReplacesOwnedNativeCurrentArtifactPostgreSQL(t *testing.T) {
	for _, mode := range []string{"manual", "automatic", ""} {
		name := mode
		if name == "" {
			name = "legacy_unknown"
		}
		t.Run(name, func(t *testing.T) {
			testPreparedManualLyricsFLACOwner(t, manualLyricsScenario{
				withCover: true, delivery: true, automaticRetry: true, sourceAuthorityMode: mode,
			})
		})
	}
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

func TestAutomaticLyricsRetryMismatchRebuildSettlesNativeReplacementPostgreSQL(t *testing.T) {
	for _, mode := range []string{"manual", "automatic", ""} {
		name := mode
		if name == "" {
			name = "legacy_unknown"
		}
		t.Run(name, func(t *testing.T) {
			testPreparedManualLyricsFLACOwner(t, manualLyricsScenario{
				withCover: true, delivery: true, automaticRetry: true, retryLyricsMismatch: true, sourceAuthorityMode: mode,
			})
		})
	}
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
