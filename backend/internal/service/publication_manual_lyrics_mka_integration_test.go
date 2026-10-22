//go:build integration

package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

func TestPreparedManualLyricsMKANativeReplacementPostgreSQL(t *testing.T) {
	for _, removal := range []bool{false, true} {
		name := "value"
		if removal {
			name = "explicit_empty"
		}
		t.Run(name, func(t *testing.T) {
			testPreparedManualLyricsOwner(t, true, false, removal, true, true, false, false, true)
		})
	}
}

// The media and technical response are real captured fixtures. Process transport
// is controlled here; the acceptance stand independently exercises real ffprobe.
type manualLyricsMKACommands struct {
	raw      []byte
	runs     int
	fileRuns int
}

func (commands *manualLyricsMKACommands) Run(_ context.Context, executable string, args ...string) (PublicationCommandResult, error) {
	commands.runs++
	if len(args) != 1 || args[0] != "-version" {
		return PublicationCommandResult{QuiescenceEstablished: true}, errors.New("MKA fixture permits only version queries")
	}
	version := "ffmpeg version 7.0 build"
	if strings.Contains(executable, "ffprobe") {
		version = "ffprobe version 7.0 build"
	}
	return PublicationCommandResult{Output: []byte(version), QuiescenceEstablished: true}, nil
}

func (commands *manualLyricsMKACommands) RunFile(_ context.Context, request tools.FileCommandRequest) (PublicationCommandResult, error) {
	commands.fileRuns++
	if request.Input == nil || !strings.Contains(strings.Join(request.Args, " "), "-f matroska") {
		return PublicationCommandResult{QuiescenceEstablished: true}, errors.New("MKA probe did not borrow its fixed-container input")
	}
	return PublicationCommandResult{Output: append([]byte(nil), commands.raw...), QuiescenceEstablished: true}, nil
}

func TestPrivateManualLyricsVersionRunnerRejectsUnboundAndCustomLaunches(t *testing.T) {
	commands := &manualLyricsMKACommands{}
	scope := &PublicationExecutionScope{commands: commands}
	runner := privateManualLyricsVersionRunner{scope: scope}
	for _, args := range [][]string{nil, {"-version", "other"}, {"-i", "unowned"}, {"-version"}} {
		if _, err := runner.Run(context.Background(), "ffprobe", args...); err == nil {
			t.Fatalf("unbound private runner accepted %v", args)
		}
	}
	if commands.runs != 0 || commands.fileRuns != 0 {
		t.Fatal("invalid private work launched a command")
	}
}

func TestManualLyricsMKAObservationRejectsTopologyAndAudioChanges(t *testing.T) {
	raw, err := os.ReadFile("testdata/manual-lyrics-mp3.probe.json")
	if err != nil {
		t.Fatal(err)
	}
	lossless, channels, bitrate, sampleRate := false, 2, 128000, 44100
	frozen := persistence.PersistedAudioFacts{StreamIndex: 0, Codec: "mp3", Container: publication.AudioContainerMKA, Lossless: &lossless, Channels: &channels, Bitrate: &bitrate, SampleRate: &sampleRate, SupportedSourceContainer: true}
	analysis, err := ParseSourceTechnicalAnalysis(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := observedManualLyricsMKAAudioFacts(raw, analysis, frozen); err != nil {
		t.Fatalf("valid MKA fixture: %v", err)
	}
	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"extra audio", func(doc map[string]any) {
			streams := doc["streams"].([]any)
			doc["streams"] = append(streams, streams[0])
		}},
		{"attachment", func(doc map[string]any) {
			doc["streams"] = append(doc["streams"].([]any), map[string]any{"index": 1, "codec_type": "attachment", "codec_name": "png"})
		}},
		{"attached picture", func(doc map[string]any) {
			doc["streams"] = append(doc["streams"].([]any), map[string]any{"index": 1, "codec_type": "video", "codec_name": "mjpeg", "disposition": map[string]any{"attached_pic": 1}})
		}},
		{"chapters", func(doc map[string]any) { doc["chapters"] = []any{map[string]any{"id": 1, "start_time": "0"}} }},
		{"changed audio", func(doc map[string]any) { doc["streams"].([]any)[0].(map[string]any)["sample_rate"] = "48000" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			var doc map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			test.mutate(doc)
			changed, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := ParseSourceTechnicalAnalysis(changed)
			if err == nil {
				_, err = observedManualLyricsMKAAudioFacts(changed, parsed, frozen)
			}
			if err == nil {
				t.Fatal("modified MKA topology/audio was accepted")
			}
		})
	}
}

// Extract the fixed synthetic fixture's MPEG-1 Layer III 128k/44.1k frames.
// This is a test-only byte-preservation check, not publication authority.
func manualLyricsMP3Frames(t *testing.T, contents []byte) []byte {
	t.Helper()
	var frames []byte
	for offset := 0; offset+4 <= len(contents); {
		header := contents[offset : offset+4]
		if header[0] != 0xff || header[1]&0xfe != 0xfa || header[2]>>4 != 9 || header[2]&0x0c != 0 {
			offset++
			continue
		}
		size := 144*128000/44100 + int((header[2]>>1)&1)
		if offset+size > len(contents) {
			t.Fatal("truncated synthetic MP3 frame")
		}
		frames = append(frames, contents[offset:offset+size]...)
		offset += size
	}
	if len(frames) == 0 {
		t.Fatal("synthetic MKA fixture contains no expected MP3 frames")
	}
	return frames
}
