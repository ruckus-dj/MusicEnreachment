package service

import (
	"bytes"
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

func mkID3v1Tag(title string) []byte {
	tag := make([]byte, publicationID3v1TagSize)
	copy(tag, "TAG")
	copy(tag[3:], title)
	return tag
}

func flacStreamPrefix(totalSamples uint64) []byte {
	body := make([]byte, 34)
	binary.BigEndian.PutUint16(body[0:2], 4096)
	binary.BigEndian.PutUint16(body[2:4], 4096)
	packed := uint64(44100)<<44 | uint64(1)<<41 | uint64(15)<<36 | totalSamples
	binary.BigEndian.PutUint64(body[10:18], packed)
	return append([]byte{'f', 'L', 'a', 'C', 0x80, 0x00, 0x00, 34}, body...)
}

func TestPublicationFlacDecodeView(t *testing.T) {
	dir := t.TempDir()
	payload := append(flacStreamPrefix(1000), []byte("audio-bytes")...)
	taggedPath := filepath.Join(dir, "tagged.flac")
	tagged := append(append([]byte(nil), payload...), mkID3v1Tag("Title")...)
	if err := os.WriteFile(taggedPath, tagged, 0o600); err != nil {
		t.Fatal(err)
	}
	view, cleanup, err := publicationFlacDecodeView(taggedPath)
	if err != nil {
		t.Fatal(err)
	}
	if view == taggedPath {
		t.Fatal("tagged source did not produce a separate decode view")
	}
	got, err := os.ReadFile(view)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("decode view = %q, %v; want audio payload only", got, err)
	}
	cleanup()
	if _, err := os.Stat(view); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("decode view was not removed: %v", err)
	}
	unchanged, err := os.ReadFile(taggedPath)
	if err != nil || !bytes.Equal(unchanged, tagged) {
		t.Fatalf("private source changed: %v", err)
	}

	plainPath := filepath.Join(dir, "plain.flac")
	if err := os.WriteFile(plainPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if view, cleanup, err := publicationFlacDecodeView(plainPath); err != nil || view != plainPath {
		t.Fatalf("untagged source view = %q, %v; want %q", view, err, plainPath)
	} else {
		cleanup()
	}

	id3Path := filepath.Join(dir, "id3-prefixed.flac")
	id3Prefixed := append([]byte{'I', 'D', '3', 0x04, 0x00, 0x00, 0x00, 0x00, 0x00, 0x04}, []byte("pad!")...)
	id3Prefixed = append(id3Prefixed, payload...)
	id3Prefixed = append(id3Prefixed, mkID3v1Tag("Title")...)
	if err := os.WriteFile(id3Path, id3Prefixed, 0o600); err != nil {
		t.Fatal(err)
	}
	if view, cleanup, err := publicationFlacDecodeView(id3Path); err != nil {
		t.Fatal(err)
	} else if view == id3Path {
		cleanup()
		t.Fatal("ID3v2-prefixed source did not produce a separate decode view")
	} else if got, err := os.ReadFile(view); err != nil || !bytes.Equal(got, id3Prefixed[:len(id3Prefixed)-publicationID3v1TagSize]) {
		cleanup()
		t.Fatalf("ID3v2-prefixed decode view = %q, %v; want the ID3v1 trailer omitted", got, err)
	} else {
		cleanup()
	}

	shortPath := filepath.Join(dir, "short.flac")
	if err := os.WriteFile(shortPath, []byte("TAG"), 0o600); err != nil {
		t.Fatal(err)
	}
	if view, cleanup, err := publicationFlacDecodeView(shortPath); err != nil || view != shortPath {
		t.Fatalf("short source view = %q, %v; want %q", view, err, shortPath)
	} else {
		cleanup()
	}

	for _, tc := range []struct {
		name     string
		contents []byte
	}{
		{name: "tag on non-FLAC bytes", contents: append([]byte("not a FLAC stream"), mkID3v1Tag("Title")...)},
		{name: "tag after malformed STREAMINFO", contents: append([]byte("fLaC\x80\x00\x00\x22short"), mkID3v1Tag("Title")...)},
		{name: "tag with unknown sample count", contents: append(flacStreamPrefix(0), mkID3v1Tag("Title")...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "source.flac")
			if err := os.WriteFile(path, tc.contents, 0o600); err != nil {
				t.Fatal(err)
			}
			view, cleanup, err := publicationFlacDecodeView(path)
			if err != nil || view != path {
				t.Fatalf("malformed source view = %q, %v; want unchanged %q", view, err, path)
			}
			cleanup()
		})
	}

	if _, _, err := publicationFlacDecodeView("relative.flac"); err == nil {
		t.Fatal("relative private source path was accepted")
	}
}

type sourceDecodeRetryRunner struct {
	calls         int
	inputs        []string
	expectedView  []byte
	firstErr      error
	alwaysErr     bool
	unknownSecond bool
}

func (r *sourceDecodeRetryRunner) Run(_ context.Context, _ string, args ...string) (PublicationCommandResult, error) {
	r.calls++
	input := ""
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-i" {
			input = args[i+1]
			break
		}
	}
	r.inputs = append(r.inputs, input)
	if r.alwaysErr {
		return PublicationCommandResult{QuiescenceEstablished: true}, errors.New("strict decode failed")
	}
	if r.calls == 1 && r.firstErr != nil {
		return PublicationCommandResult{QuiescenceEstablished: true}, r.firstErr
	}
	if r.unknownSecond && r.calls == 2 {
		return PublicationCommandResult{Output: []byte("SHA256=" + strings.Repeat("b", 64) + "\n")}, nil
	}
	data, err := os.ReadFile(input)
	if err != nil {
		return PublicationCommandResult{}, err
	}
	if !bytes.Equal(data, r.expectedView) {
		return PublicationCommandResult{QuiescenceEstablished: true}, errors.New("decode view retained the ID3v1 trailer")
	}
	return PublicationCommandResult{Output: []byte("SHA256=" + strings.Repeat("b", 64) + "\n"), QuiescenceEstablished: true}, nil
}

func TestPublicationSourceDecodedAudioSHA256RetriesFlacID3v1View(t *testing.T) {
	dir := t.TempDir()
	payload := append(flacStreamPrefix(1000), []byte("audio")...)
	tagged := append(append([]byte(nil), payload...), mkID3v1Tag("Title")...)
	taggedPath := filepath.Join(dir, "tagged.flac")
	if err := os.WriteFile(taggedPath, tagged, 0o600); err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(dir, "ffmpeg")
	runner := &sourceDecodeRetryRunner{firstErr: errors.New("invalid sync code"), expectedView: payload}
	digest, err := publicationSourceDecodedAudioSHA256(context.Background(), runner, tool, taggedPath, publication.AudioContainerFLAC, 0)
	if err != nil || digest != strings.Repeat("b", 64) {
		t.Fatalf("tagged source digest = %q, %v", digest, err)
	}
	if runner.calls != 2 || len(runner.inputs) != 2 {
		t.Fatalf("decode calls = %d, inputs = %#v; want one retry", runner.calls, runner.inputs)
	}
	if runner.inputs[0] != taggedPath || runner.inputs[1] == taggedPath {
		t.Fatalf("decode inputs = %#v; want original followed by a separate view", runner.inputs)
	}
	if _, err := os.Stat(runner.inputs[1]); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("decode view was not removed after proof: %v", err)
	}
	unchanged, err := os.ReadFile(taggedPath)
	if err != nil || !bytes.Equal(unchanged, tagged) {
		t.Fatalf("private source changed during retry: %v", err)
	}

	plainPath := filepath.Join(dir, "plain.flac")
	if err := os.WriteFile(plainPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	plainRunner := &sourceDecodeRetryRunner{expectedView: payload}
	if digest, err := publicationSourceDecodedAudioSHA256(context.Background(), plainRunner, tool, plainPath, publication.AudioContainerFLAC, 0); err != nil || digest != strings.Repeat("b", 64) {
		t.Fatalf("untagged source digest = %q, %v", digest, err)
	}
	if plainRunner.calls != 1 || plainRunner.inputs[0] != plainPath {
		t.Fatalf("untagged source calls = %d, inputs = %#v; want a single direct decode", plainRunner.calls, plainRunner.inputs)
	}

	corruptRunner := &sourceDecodeRetryRunner{alwaysErr: true}
	if _, err := publicationSourceDecodedAudioSHA256(context.Background(), corruptRunner, tool, taggedPath, publication.AudioContainerFLAC, 0); err == nil {
		t.Fatal("corrupt tagged source passed the strict decode proof")
	}
	if corruptRunner.calls != 2 || len(corruptRunner.inputs) != 2 || corruptRunner.inputs[1] == taggedPath {
		t.Fatalf("corrupt source decode calls = %d, inputs = %#v; want original and one view attempt", corruptRunner.calls, corruptRunner.inputs)
	}
	if _, err := os.Stat(corruptRunner.inputs[1]); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed decode view was not removed: %v", err)
	}

	untaggedCorruptRunner := &sourceDecodeRetryRunner{alwaysErr: true}
	if _, err := publicationSourceDecodedAudioSHA256(context.Background(), untaggedCorruptRunner, tool, plainPath, publication.AudioContainerFLAC, 0); err == nil {
		t.Fatal("corrupt untagged source passed the strict decode proof")
	}
	if untaggedCorruptRunner.calls != 1 || untaggedCorruptRunner.inputs[0] != plainPath {
		t.Fatalf("untagged corrupt source calls = %d, inputs = %#v; want a single direct decode", untaggedCorruptRunner.calls, untaggedCorruptRunner.inputs)
	}

	secondUnknownRunner := &sourceDecodeRetryRunner{firstErr: errors.New("invalid sync code"), expectedView: payload, unknownSecond: true}
	digest, err = publicationSourceDecodedAudioSHA256(context.Background(), secondUnknownRunner, tool, taggedPath, publication.AudioContainerFLAC, 0)
	if err == nil || digest != "" {
		t.Fatalf("unknown view quiescence digest = %q, %v", digest, err)
	}
	if !errors.Is(err, errPublicationDecodedAudioQuiescence) {
		t.Fatalf("unknown view quiescence error = %v; want the typed sentinel", err)
	}
	if secondUnknownRunner.calls != 2 {
		t.Fatalf("unknown view quiescence made %d decode calls; want original and one view attempt", secondUnknownRunner.calls)
	}
	if _, err := os.Stat(secondUnknownRunner.inputs[1]); err != nil {
		t.Fatalf("decode view was removed while quiescence was unknown: %v", err)
	}

	nonFlacRunner := &sourceDecodeRetryRunner{firstErr: errors.New("invalid sync code")}
	if _, err := publicationSourceDecodedAudioSHA256(context.Background(), nonFlacRunner, tool, taggedPath, publication.AudioContainerM4A, 0); err == nil {
		t.Fatal("non-FLAC strict decode failure was accepted")
	}
	if nonFlacRunner.calls != 1 {
		t.Fatalf("non-FLAC container made %d decode calls; want no view retry", nonFlacRunner.calls)
	}
}

type sourceDecodeUnknownQuiescenceRunner struct{ calls int }

func (r *sourceDecodeUnknownQuiescenceRunner) Run(context.Context, string, ...string) (PublicationCommandResult, error) {
	r.calls++
	return PublicationCommandResult{Output: []byte("SHA256=" + strings.Repeat("b", 64) + "\n")}, nil
}

func TestPublicationSourceDecodedAudioSHA256DoesNotRetryUnknownQuiescence(t *testing.T) {
	dir := t.TempDir()
	tagged := append(flacStreamPrefix(1000), []byte("audio")...)
	path := filepath.Join(dir, "tagged.flac")
	if err := os.WriteFile(path, append(tagged, mkID3v1Tag("Title")...), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &sourceDecodeUnknownQuiescenceRunner{}
	digest, err := publicationSourceDecodedAudioSHA256(context.Background(), runner, filepath.Join(dir, "ffmpeg"), path, publication.AudioContainerFLAC, 0)
	if err == nil || digest != "" {
		t.Fatalf("unknown quiescence digest = %q, %v", digest, err)
	}
	if !errors.Is(err, errPublicationDecodedAudioQuiescence) {
		t.Fatalf("unknown quiescence error = %v; want the typed sentinel", err)
	}
	if runner.calls != 1 {
		t.Fatalf("unknown quiescence made %d decode calls; want no view retry", runner.calls)
	}
}

func TestPublicationSourceDecodedAudioSHA256RealFFmpegFLACTrailingID3v1(t *testing.T) {
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
	if output, err := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100:duration=0.5", "-ac", "2", "-c:a", "flac", "-y", source).CombinedOutput(); err != nil {
		t.Fatalf("create FLAC: %v: %s", err, output)
	}
	candidate := filepath.Join(dir, "candidate.m4a")
	if output, err := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-i", source, "-c:a", "alac", "-y", candidate).CombinedOutput(); err != nil {
		t.Fatalf("create ALAC: %v: %s", err, output)
	}
	taggedPath := filepath.Join(dir, "tagged.flac")
	contents, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	tagged := append(append([]byte(nil), contents...), mkID3v1Tag("Title")...)
	if err := os.WriteFile(taggedPath, tagged, 0o600); err != nil {
		t.Fatal(err)
	}
	facts := persistence.PersistedAudioFacts{Codec: "flac", Container: publication.AudioContainerFLAC, StreamIndex: 0}
	runner := decodedAudioExecRunner{}
	if err := proveALACCandidate(context.Background(), runner, ffmpeg, ffprobe, taggedPath, candidate, facts); err != nil {
		t.Fatalf("tagged FLAC source failed preservation proof: %v", err)
	}
	unchanged, err := os.ReadFile(taggedPath)
	if err != nil || !bytes.Equal(unchanged, tagged) {
		t.Fatalf("tagged source changed during proof: %v", err)
	}
	altered := filepath.Join(dir, "altered.m4a")
	if output, err := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-i", source, "-af", "volume=0.00001", "-c:a", "alac", "-y", altered).CombinedOutput(); err != nil {
		t.Fatalf("create altered ALAC: %v: %s", err, output)
	}
	if err := proveALACCandidate(context.Background(), runner, ffmpeg, ffprobe, taggedPath, altered, facts); err == nil {
		t.Fatal("altered ALAC candidate passed the preservation proof for a tagged source")
	}
	corruptPath := filepath.Join(dir, "corrupt.flac")
	corrupt := append(append([]byte(nil), contents[:len(contents)/2]...), mkID3v1Tag("Title")...)
	if err := os.WriteFile(corruptPath, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := proveALACCandidate(context.Background(), runner, ffmpeg, ffprobe, corruptPath, candidate, facts); err == nil {
		t.Fatal("truncated tagged source passed the preservation proof")
	}
}
