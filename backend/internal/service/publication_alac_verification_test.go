package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

type alacProbeRunner struct {
	outputs map[string][]byte
	unquiet bool
	tools   []string
	args    [][]string
}

func (r *alacProbeRunner) Run(_ context.Context, tool string, args ...string) (PublicationCommandResult, error) {
	r.tools = append(r.tools, tool)
	r.args = append(r.args, append([]string(nil), args...))
	input := ""
	if len(args) != 0 {
		input = args[len(args)-1]
	}
	output, ok := r.outputs[input]
	if !ok {
		return PublicationCommandResult{}, fmt.Errorf("unexpected technical probe input %q", input)
	}
	return PublicationCommandResult{Output: output, QuiescenceEstablished: !r.unquiet}, nil
}

func alacProbeResponse(format string, streams ...string) []byte {
	return []byte(fmt.Sprintf(`{"format":{"format_name":%q},"streams":[%s]}`, format, strings.Join(streams, ",")))
}

func alacProbeAudioStream(index int, codec string, channels, rate, depth int, layout string) string {
	stream := fmt.Sprintf(`{"index":%d,"codec_type":"audio","codec_name":%q,"channels":%d,"sample_rate":%d,"channel_layout":%q}`, index, codec, channels, rate, layout)
	if depth > 0 {
		stream = fmt.Sprintf(`{"index":%d,"codec_type":"audio","codec_name":%q,"channels":%d,"sample_rate":%d,"bits_per_raw_sample":%d,"channel_layout":%q}`, index, codec, channels, rate, depth, layout)
	}
	return stream
}

func alacProbeStreamDuration(index int, codec string, channels, rate, depth int, layout string, durationMS int) string {
	stream := alacProbeAudioStream(index, codec, channels, rate, depth, layout)
	return strings.TrimSuffix(stream, "}") + fmt.Sprintf(`,"duration":"%.6f"}`, float64(durationMS)/1000)
}

func alacProbeResponseContainerDuration(format string, durationMS int, streams ...string) []byte {
	return []byte(fmt.Sprintf(`{"format":{"format_name":%q,"duration":"%.6f"},"streams":[%s]}`, format, float64(durationMS)/1000, strings.Join(streams, ",")))
}

func alacProbeFacts() persistence.PersistedAudioFacts {
	channels, rate, depth := 2, 44100, 24
	return persistence.PersistedAudioFacts{
		Codec: "flac", Container: publication.AudioContainerFLAC, StreamIndex: 0,
		ChannelLayout: "stereo", Channels: &channels, SampleRate: &rate, BitDepth: &depth,
	}
}

func TestValidateALACCandidateUsesFFprobeTechnicalChecksOnly(t *testing.T) {
	dir := t.TempDir()
	ffprobe := filepath.Join(dir, "ffprobe")
	source, candidate := filepath.Join(dir, "source.flac"), filepath.Join(dir, "candidate.m4a")
	runner := &alacProbeRunner{outputs: map[string][]byte{
		source:    alacProbeResponse("flac", alacProbeAudioStream(0, "flac", 2, 44100, 24, "stereo")),
		candidate: alacProbeResponse("mov", alacProbeAudioStream(0, "alac", 2, 44100, 24, "stereo")),
	}}
	if err := validateALACCandidate(context.Background(), runner, ffprobe, source, candidate, alacProbeFacts()); err != nil {
		t.Fatalf("technical ALAC validation: %v", err)
	}
	if len(runner.tools) != 2 {
		t.Fatalf("technical validation ran %d commands, want source and candidate probes only", len(runner.tools))
	}
	for _, tool := range runner.tools {
		if tool != ffprobe {
			t.Fatalf("technical validation invoked %q, want only ffprobe", tool)
		}
	}
	for _, args := range runner.args {
		joined := strings.Join(args, " ")
		for _, required := range []string{"-protocol_whitelist file", "-show_format", "-show_streams", "-of json"} {
			if !strings.Contains(joined, required) {
				t.Fatalf("technical probe lacks %q in %#v", required, args)
			}
		}
		for _, forbidden := range []string{"pcm_s32le", "-f hash", "-hash sha256", "-xerror", "-map"} {
			if strings.Contains(joined, forbidden) {
				t.Fatalf("technical validation still runs decode/hash argument %q in %#v", forbidden, args)
			}
		}
	}
}

func TestValidateALACCandidateRetainsTechnicalShapeChecks(t *testing.T) {
	dir := t.TempDir()
	ffprobe := filepath.Join(dir, "ffprobe")
	source, candidate := filepath.Join(dir, "source.flac"), filepath.Join(dir, "candidate.m4a")
	validSource := alacProbeResponse("flac", alacProbeAudioStream(0, "flac", 2, 44100, 24, "stereo"))
	for _, testCase := range []struct {
		name      string
		source    []byte
		candidate []byte
		facts     persistence.PersistedAudioFacts
	}{
		{
			name:      "candidate codec",
			source:    validSource,
			candidate: alacProbeResponse("mov", alacProbeAudioStream(0, "flac", 2, 44100, 24, "stereo")),
			facts:     alacProbeFacts(),
		},
		{
			name:      "candidate channel count",
			source:    validSource,
			candidate: alacProbeResponse("mov", alacProbeAudioStream(0, "alac", 1, 44100, 24, "stereo")),
			facts:     alacProbeFacts(),
		},
		{
			name:      "candidate sample rate",
			source:    validSource,
			candidate: alacProbeResponse("mov", alacProbeAudioStream(0, "alac", 2, 48000, 24, "stereo")),
			facts:     alacProbeFacts(),
		},
		{
			name:      "candidate sample depth",
			source:    validSource,
			candidate: alacProbeResponse("mov", alacProbeAudioStream(0, "alac", 2, 44100, 16, "stereo")),
			facts:     alacProbeFacts(),
		},
		{
			name:      "candidate unknown depth",
			source:    validSource,
			candidate: alacProbeResponse("mov", alacProbeAudioStream(0, "alac", 2, 44100, 0, "stereo")),
			facts:     alacProbeFacts(),
		},
		{
			name:      "candidate channel layout",
			source:    validSource,
			candidate: alacProbeResponse("mov", alacProbeAudioStream(0, "alac", 2, 44100, 24, "mono")),
			facts:     alacProbeFacts(),
		},
		{
			name:   "candidate extra audio stream",
			source: validSource,
			candidate: alacProbeResponse("mov",
				alacProbeAudioStream(0, "alac", 2, 44100, 24, "stereo"),
				alacProbeAudioStream(1, "alac", 2, 44100, 24, "stereo"),
			),
			facts: alacProbeFacts(),
		},
		{
			name:      "source shape differs from frozen facts",
			source:    alacProbeResponse("flac", alacProbeAudioStream(0, "flac", 2, 48000, 24, "stereo")),
			candidate: alacProbeResponse("mov", alacProbeAudioStream(0, "alac", 2, 48000, 24, "stereo")),
			facts:     alacProbeFacts(),
		},
		{
			name:      "selected source stream missing",
			source:    alacProbeResponse("flac", alacProbeAudioStream(5, "flac", 2, 44100, 24, "stereo")),
			candidate: alacProbeResponse("mov", alacProbeAudioStream(0, "alac", 2, 44100, 24, "stereo")),
			facts:     alacProbeFacts(),
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			runner := &alacProbeRunner{outputs: map[string][]byte{source: testCase.source, candidate: testCase.candidate}}
			if err := validateALACCandidate(context.Background(), runner, ffprobe, source, candidate, testCase.facts); err == nil {
				t.Fatal("mismatched ALAC technical shape was accepted")
			}
		})
	}
}

func TestValidateALACCandidateRequiresProbeQuiescence(t *testing.T) {
	dir := t.TempDir()
	ffprobe := filepath.Join(dir, "ffprobe")
	source, candidate := filepath.Join(dir, "source.flac"), filepath.Join(dir, "candidate.m4a")
	runner := &alacProbeRunner{
		outputs: map[string][]byte{
			source:    alacProbeResponse("flac", alacProbeAudioStream(0, "flac", 2, 44100, 24, "stereo")),
			candidate: alacProbeResponse("mov", alacProbeAudioStream(0, "alac", 2, 44100, 24, "stereo")),
		},
		unquiet: true,
	}
	if err := validateALACCandidate(context.Background(), runner, ffprobe, source, candidate, alacProbeFacts()); err == nil {
		t.Fatal("technical probe without established quiescence was accepted")
	}
}

func TestValidateALACCandidateDurationGuard(t *testing.T) {
	dir := t.TempDir()
	ffprobe := filepath.Join(dir, "ffprobe")
	source, candidate := filepath.Join(dir, "source.flac"), filepath.Join(dir, "candidate.m4a")
	flac := func(durationMS int) string {
		return alacProbeStreamDuration(0, "flac", 2, 44100, 24, "stereo", durationMS)
	}
	alac := func(durationMS int) string {
		return alacProbeStreamDuration(0, "alac", 2, 44100, 24, "stereo", durationMS)
	}
	nonFLAC := alacProbeFacts()
	nonFLAC.Codec, nonFLAC.Container = "pcm_s24le", publication.AudioContainerWAV
	for _, testCase := range []struct {
		name      string
		source    []byte
		candidate []byte
		facts     persistence.PersistedAudioFacts
		wantErr   bool
	}{
		{
			name:      "demonstrably shortened candidate",
			source:    alacProbeResponse("flac", flac(2000)),
			candidate: alacProbeResponse("mov", alac(1137)),
			facts:     alacProbeFacts(),
			wantErr:   true,
		},
		{
			name:      "rounding within tolerance",
			source:    alacProbeResponse("flac", flac(2000)),
			candidate: alacProbeResponse("mov", alac(1950)),
			facts:     alacProbeFacts(),
		},
		{
			name:      "shorter beyond tolerance",
			source:    alacProbeResponse("flac", flac(2000)),
			candidate: alacProbeResponse("mov", alac(1949)),
			facts:     alacProbeFacts(),
			wantErr:   true,
		},
		{
			name:      "source stream duration unknown",
			source:    alacProbeResponse("flac", alacProbeAudioStream(0, "flac", 2, 44100, 24, "stereo")),
			candidate: alacProbeResponse("mov", alac(1137)),
			facts:     alacProbeFacts(),
		},
		{
			name:      "candidate stream duration unknown",
			source:    alacProbeResponse("flac", flac(2000)),
			candidate: alacProbeResponse("mov", alacProbeAudioStream(0, "alac", 2, 44100, 24, "stereo")),
			facts:     alacProbeFacts(),
		},
		{
			name:      "container durations are not substituted",
			source:    alacProbeResponseContainerDuration("flac", 2000, alacProbeAudioStream(0, "flac", 2, 44100, 24, "stereo")),
			candidate: alacProbeResponseContainerDuration("mov", 1137, alacProbeAudioStream(0, "alac", 2, 44100, 24, "stereo")),
			facts:     alacProbeFacts(),
		},
		{
			name:      "non-FLAC source",
			source:    alacProbeResponse("wav", alacProbeStreamDuration(0, "pcm_s24le", 2, 44100, 24, "stereo", 2000)),
			candidate: alacProbeResponse("mov", alac(1137)),
			facts:     nonFLAC,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			runner := &alacProbeRunner{outputs: map[string][]byte{source: testCase.source, candidate: testCase.candidate}}
			err := validateALACCandidate(context.Background(), runner, ffprobe, source, candidate, testCase.facts)
			if testCase.wantErr && !errors.Is(err, errALACCandidateStreamShortened) {
				t.Fatalf("shortened ALAC error = %v; want stream duration error", err)
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("technical validation: %v", err)
			}
		})
	}
}

func TestValidateALACStreamDurationNearMaxDoesNotOverflow(t *testing.T) {
	sourceMS, candidateMS := int64(math.MaxInt64), int64(math.MaxInt64-10)
	source := SourceTechnicalAudioStream{DurationMS: &sourceMS}
	candidate := SourceTechnicalAudioStream{DurationMS: &candidateMS}
	if err := validateALACStreamDuration(source, candidate, alacProbeFacts()); err != nil {
		t.Fatalf("near-equal maximum durations were rejected: %v", err)
	}
	candidateMS = 1
	if err := validateALACStreamDuration(source, candidate, alacProbeFacts()); !errors.Is(err, errALACCandidateStreamShortened) {
		t.Fatalf("shortened maximum duration error = %v; want stream duration error", err)
	}
}

type ffprobeOnlyRunner struct {
	t     *testing.T
	tools []string
}

func (r *ffprobeOnlyRunner) Run(ctx context.Context, tool string, args ...string) (PublicationCommandResult, error) {
	r.t.Helper()
	r.tools = append(r.tools, tool)
	if filepath.Base(tool) != "ffprobe" {
		return PublicationCommandResult{}, fmt.Errorf("technical ALAC validation invoked unmocked tool %q", tool)
	}
	output, err := exec.CommandContext(ctx, tool, args...).CombinedOutput()
	return PublicationCommandResult{Output: output, QuiescenceEstablished: true}, err
}

func TestValidateALACCandidateRealFFmpegTaggedFLAC(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe unavailable")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "source.flac")
	if output, err := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100:duration=0.25", "-ac", "2", "-c:a", "flac", "-y", source).CombinedOutput(); err != nil {
		t.Fatalf("create FLAC: %v: %s", err, output)
	}
	contents, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	taggedPath := filepath.Join(dir, "tagged.flac")
	tagged := append(append([]byte(nil), contents...), mkID3v1Tag("Title")...)
	if err := os.WriteFile(taggedPath, tagged, 0o600); err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(dir, "candidate.m4a")
	if output, err := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-i", source, "-c:a", "alac", "-y", candidate).CombinedOutput(); err != nil {
		t.Fatalf("create ALAC: %v: %s", err, output)
	}
	facts := alacProbeFacts()
	facts.Channels, facts.SampleRate, facts.BitDepth, facts.ChannelLayout = nil, nil, nil, ""
	runner := &ffprobeOnlyRunner{t: t}
	if err := validateALACCandidate(context.Background(), runner, ffprobe, taggedPath, candidate, facts); err != nil {
		t.Fatalf("tagged FLAC technical validation: %v", err)
	}
	if len(runner.tools) != 2 {
		t.Fatalf("technical validation ran %d tools, want only source and candidate ffprobe calls", len(runner.tools))
	}
	unchanged, err := os.ReadFile(taggedPath)
	if err != nil || !bytes.Equal(unchanged, tagged) {
		t.Fatalf("tagged source changed during technical validation: %v", err)
	}
	resampled := filepath.Join(dir, "resampled.m4a")
	if output, err := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-i", source, "-ar", "48000", "-c:a", "alac", "-y", resampled).CombinedOutput(); err != nil {
		t.Fatalf("create resampled ALAC: %v: %s", err, output)
	}
	if err := validateALACCandidate(context.Background(), &ffprobeOnlyRunner{t: t}, ffprobe, taggedPath, resampled, facts); err == nil {
		t.Fatal("ALAC candidate with a different sample rate passed technical validation")
	}
}

func TestValidateALACCandidateRealFFmpegTruncatedFLAC(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe unavailable")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "source.flac")
	if output, err := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100:duration=2", "-ac", "2", "-c:a", "flac", "-y", source).CombinedOutput(); err != nil {
		t.Fatalf("create FLAC: %v: %s", err, output)
	}
	contents, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	truncatedPath := filepath.Join(dir, "truncated.flac")
	if err := os.WriteFile(truncatedPath, contents[:len(contents)*2/5], 0o600); err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(dir, "truncated.m4a")
	if output, err := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-i", truncatedPath, "-c:a", "alac", "-y", candidate).CombinedOutput(); err != nil {
		t.Skipf("ffmpeg did not produce a truncated ALAC fixture: %v: %s", err, output)
	}
	facts := alacProbeFacts()
	facts.Channels, facts.SampleRate, facts.BitDepth, facts.ChannelLayout = nil, nil, nil, ""
	if err := validateALACCandidate(context.Background(), &ffprobeOnlyRunner{t: t}, ffprobe, truncatedPath, candidate, facts); !errors.Is(err, errALACCandidateStreamShortened) {
		t.Fatalf("truncated FLAC validation error = %v; want stream duration error", err)
	}
}

func mkID3v1Tag(title string) []byte {
	const publicationID3v1TagSize = 128
	tag := make([]byte, publicationID3v1TagSize)
	copy(tag, "TAG")
	copy(tag[3:], title)
	return tag
}
