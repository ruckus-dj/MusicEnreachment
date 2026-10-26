package service

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

type decodedAudioProofRunner struct {
	result PublicationCommandResult
	err    error
	args   []string
}

func (r *decodedAudioProofRunner) Run(_ context.Context, _ string, args ...string) (PublicationCommandResult, error) {
	r.args = append([]string(nil), args...)
	return r.result, r.err
}

func TestPublicationDecodedAudioSHA256CommandAndDigest(t *testing.T) {
	dir := t.TempDir()
	tool, input := filepath.Join(dir, "ffmpeg"), filepath.Join(dir, "candidate.mov")
	runner := &decodedAudioProofRunner{result: PublicationCommandResult{Output: []byte("SHA256=" + strings.Repeat("a", 64) + "\n"), QuiescenceEstablished: true}}
	got, err := publicationDecodedAudioSHA256(context.Background(), runner, tool, input, publication.AudioContainerM4A, 3)
	if err != nil || got != strings.Repeat("a", 64) {
		t.Fatalf("digest = %q, err = %v", got, err)
	}
	want := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-protocol_whitelist", "file", "-f", "mov", "-enable_drefs", "0", "-use_absolute_path", "0", "-xerror", "-i", input, "-map", "0:3", "-vn", "-sn", "-dn", "-c:a", "pcm_s32le", "-f", "hash", "-hash", "sha256", "-"}
	if !reflect.DeepEqual(runner.args, want) {
		t.Fatalf("argv = %#v, want %#v", runner.args, want)
	}
	for _, forbidden := range []string{"-ac", "-ar"} {
		if slices.Contains(runner.args, forbidden) {
			t.Fatalf("unexpected channel/rate override %s in argv %#v", forbidden, runner.args)
		}
	}
}

func TestPublicationDecodedAudioSHA256RejectsInvalidProof(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ffmpeg")
	input := filepath.Join(t.TempDir(), "input.flac")
	for _, tc := range []struct {
		name  string
		out   string
		err   error
		quiet bool
	}{
		{name: "uppercase", out: "SHA256=" + strings.Repeat("A", 64), quiet: true},
		{name: "extra line", out: "SHA256=" + strings.Repeat("a", 64) + "\nextra", quiet: true},
		{name: "malformed", out: "SHA256=not-a-digest", quiet: true},
		{name: "decode error", out: "SHA256=" + strings.Repeat("a", 64), err: errors.New("decode failed"), quiet: true},
		{name: "unknown quiescence", out: "SHA256=" + strings.Repeat("a", 64)},
		{name: "cancelled", out: "SHA256=" + strings.Repeat("a", 64), err: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &decodedAudioProofRunner{result: PublicationCommandResult{Output: []byte(tc.out), QuiescenceEstablished: tc.quiet}, err: tc.err}
			if got, err := publicationDecodedAudioSHA256(context.Background(), r, path, input, publication.AudioContainerFLAC, 0); err == nil || got != "" {
				t.Fatalf("proof accepted: %q, %v", got, err)
			}
		})
	}
}

func TestPublicationDecodedAudioSHA256RejectsIncompleteDependencies(t *testing.T) {
	r := &decodedAudioProofRunner{result: PublicationCommandResult{QuiescenceEstablished: true}}
	for _, tc := range []struct {
		tool, path string
		runner     PublicationCommandRunner
		container  publication.AudioContainer
		index      int
	}{{filepath.Join(t.TempDir(), "ffmpeg"), filepath.Join(t.TempDir(), "in.flac"), nil, publication.AudioContainerFLAC, 0}, {"", filepath.Join(t.TempDir(), "in.flac"), r, publication.AudioContainerFLAC, 0}, {filepath.Join(t.TempDir(), "ffmpeg"), "relative.flac", r, publication.AudioContainerFLAC, 0}, {filepath.Join(t.TempDir(), "ffmpeg"), filepath.Join(t.TempDir(), "in"), r, publication.AudioContainer("unknown"), 0}, {filepath.Join(t.TempDir(), "ffmpeg"), filepath.Join(t.TempDir(), "in.flac"), r, publication.AudioContainerFLAC, -1}} {
		if _, err := publicationDecodedAudioSHA256(context.Background(), tc.runner, tc.tool, tc.path, tc.container, tc.index); err == nil {
			t.Fatalf("accepted incomplete input %+v", tc)
		}
	}
}

type decodedAudioExecRunner struct{}

func (decodedAudioExecRunner) Run(ctx context.Context, tool string, args ...string) (PublicationCommandResult, error) {
	out, err := exec.CommandContext(ctx, tool, args...).CombinedOutput()
	return PublicationCommandResult{Output: out, QuiescenceEstablished: true}, err
}

func TestPublicationDecodedAudioSHA256RealFFmpegFLACALACRoundTrip(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg unavailable")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe unavailable")
	}
	dir := t.TempDir()
	for _, bits := range []int{16, 24} {
		depth := strconv.Itoa(bits)
		t.Run(depth, func(t *testing.T) {
			source, candidate, altered := filepath.Join(dir, depth+".flac"), filepath.Join(dir, depth+".m4a"), filepath.Join(dir, depth+"-altered.m4a")
			width, format, sampleFormat := 2, "s16le", "s16"
			if bits == 24 {
				width, format, sampleFormat = 4, "s32le", "s32"
			}
			// Distinct interleaved channel samples exercise significant low bits;
			// 24-bit samples occupy the high 24 bits of signed 32-bit PCM.
			pcm := make([]byte, 2048*width)
			for i := 0; i < 2048; i++ {
				if bits == 16 {
					binary.LittleEndian.PutUint16(pcm[i*width:], uint16(i*7919))
				} else {
					value := int32((i*7919)%16777216 - 8388608)
					binary.LittleEndian.PutUint32(pcm[i*width:], uint32(value<<8))
				}
			}
			raw := filepath.Join(dir, depth+".pcm")
			if err := os.WriteFile(raw, pcm, 0o600); err != nil {
				t.Fatal(err)
			}
			args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-f", format, "-ar", "44100", "-ac", "2", "-i", raw, "-sample_fmt", sampleFormat, "-bits_per_raw_sample", depth, "-c:a", "flac", "-y", source}
			if output, e := exec.Command(ffmpeg, args...).CombinedOutput(); e != nil {
				t.Fatalf("create FLAC: %v: %s", e, output)
			}
			if output, e := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-i", source, "-c:a", "alac", "-y", candidate).CombinedOutput(); e != nil {
				t.Fatalf("create ALAC: %v: %s", e, output)
			}
			if output, e := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-i", source, "-af", "volume=0.00001", "-c:a", "alac", "-y", altered).CombinedOutput(); e != nil {
				t.Fatalf("create altered ALAC: %v: %s", e, output)
			}
			runner := decodedAudioExecRunner{}
			analysis, err := publicationALACTechnicalAnalysis(context.Background(), runner, ffprobe, source, publication.AudioContainerFLAC)
			if err != nil || len(analysis.Streams) != 1 || analysis.Streams[0].BitsPerSample == nil || *analysis.Streams[0].BitsPerSample != int64(bits) {
				t.Fatalf("source did not preserve fixture depth %d: %+v, %v", bits, analysis, err)
			}
			hash := func(path string, container publication.AudioContainer) string {
				v, e := publicationDecodedAudioSHA256(context.Background(), runner, ffmpeg, path, container, 0)
				if e != nil {
					t.Fatalf("hash %s: %v", path, e)
				}
				return v
			}
			original, roundTrip := hash(source, publication.AudioContainerFLAC), hash(candidate, publication.AudioContainerM4A)
			if original != roundTrip {
				t.Fatalf("lossless FLAC/ALAC PCM differs: %s != %s", original, roundTrip)
			}
			if hash(altered, publication.AudioContainerM4A) == original {
				t.Fatal("altered low-level PCM was accepted as identical")
			}
			facts := persistence.PersistedAudioFacts{Codec: "flac", Container: publication.AudioContainerFLAC, StreamIndex: 0}
			if err := proveALACCandidate(context.Background(), runner, ffmpeg, ffprobe, source, candidate, facts); err != nil {
				t.Fatalf("real ALAC candidate proof: %v", err)
			}
			if err := proveALACCandidate(context.Background(), runner, ffmpeg, ffprobe, source, altered, facts); err == nil {
				t.Fatal("altered ALAC candidate passed the preservation proof")
			}
			if _, e := os.Stat(candidate); e != nil {
				t.Fatal(e)
			}
		})
	}
}
