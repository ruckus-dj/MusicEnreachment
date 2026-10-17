package publication

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPlanAudioTransformAACBitrate(t *testing.T) {
	for _, test := range []struct {
		name     string
		codec    string
		channels int
		bitrate  *int
		want     int
	}{
		{name: "measured MP3 mono average", codec: "mp3", channels: 1, bitrate: intPointer(178321), want: 178321},
		{name: "measured MP3 stereo average", codec: "mp3", channels: 2, bitrate: intPointer(224000), want: 224000},
		{name: "fallback mono", codec: "vorbis", channels: 1, want: 160000},
		{name: "fallback stereo", codec: "vorbis", channels: 2, want: 320000},
		{name: "non-MP3 ignores measured rate", codec: "vorbis", channels: 2, bitrate: intPointer(192000), want: 320000},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan, err := PlanAudioTransform(AudioFormatM4A, facts(test.codec, false, test.channels, test.bitrate))
			if err != nil {
				t.Fatal(err)
			}
			if plan.Action != AudioCodecAAC || plan.Bitrate != test.want || plan.Extension != "m4a" {
				t.Fatalf("plan = %+v, want AAC at %d", plan, test.want)
			}
		})
	}
}

func TestPlanAudioTransformCopyAndTranscodeChannelRules(t *testing.T) {
	for _, codec := range []string{"aac", "alac"} {
		plan, err := PlanAudioTransform(AudioFormatM4A, facts(codec, codec == "alac", 6, nil))
		if err != nil || plan.Action != AudioCodecCopy {
			t.Fatalf("%s multichannel copy = %+v, %v", codec, plan, err)
		}
	}
	if _, err := PlanAudioTransform(AudioFormatM4A, facts("mp3", false, 6, intPointer(320000))); err == nil {
		t.Fatal("multichannel AAC transcode was accepted")
	}
	if _, err := PlanAudioTransform(AudioFormatM4A, facts("mp3", false, 0, nil)); err == nil {
		t.Fatal("missing-channel AAC transcode was accepted")
	}
}

func TestPlanAudioTransformSourceMKAAndLosslessCapability(t *testing.T) {
	source := facts("flac", true, 2, nil)
	source.Container = AudioContainerFLAC
	source.SupportedSourceContainer = true
	plan, err := PlanAudioTransform(AudioFormatSource, source)
	if err != nil || plan.Action != AudioCodecCopy || plan.Extension != "flac" {
		t.Fatalf("supported source plan = %+v, %v", plan, err)
	}

	plan, err = PlanAudioTransform(AudioFormatMKA, source)
	if err != nil || plan.Action != AudioCodecCopy || plan.OutputContainer != AudioContainerMKA {
		t.Fatalf("MKA remux plan = %+v, %v", plan, err)
	}

	for _, depth := range []int{16, 24} {
		lossless := facts("flac", true, 2, nil)
		lossless.BitDepth = intPointer(depth)
		if _, err := PlanAudioTransform(AudioFormatM4A, lossless); err == nil {
			t.Fatalf("unverified ALAC capability accepted at depth %d", depth)
		}
		lossless.ALACCapabilitiesVerified = true
		plan, err = PlanAudioTransform(AudioFormatM4A, lossless)
		if err != nil || plan.Action != AudioCodecALAC {
			t.Fatalf("verified ALAC depth %d plan = %+v, %v", depth, plan, err)
		}
	}
	unsupportedDepth := facts("flac", true, 2, nil)
	unsupportedDepth.BitDepth = intPointer(32)
	unsupportedDepth.ALACCapabilitiesVerified = true
	if _, err := PlanAudioTransform(AudioFormatM4A, unsupportedDepth); err == nil {
		t.Fatal("32-bit lossless ALAC transcode was accepted")
	}
	unknownDepth := facts("flac", true, 2, nil)
	unknownDepth.BitDepth = nil
	unknownDepth.ALACCapabilitiesVerified = true
	if _, err := PlanAudioTransform(AudioFormatM4A, unknownDepth); err == nil {
		t.Fatal("unknown lossless sample depth was accepted")
	}
}

func TestPlanAudioTransformFailsClosedForUnknownFacts(t *testing.T) {
	unknownCodec := facts("vendor_codec", true, 2, nil)
	if _, err := PlanAudioTransform(AudioFormatM4A, unknownCodec); err == nil {
		t.Fatal("unknown codec classification was accepted")
	}
	unknownLossless := facts("flac", false, 2, nil)
	unknownLossless.Lossless = nil
	if _, err := PlanAudioTransform(AudioFormatM4A, unknownLossless); err == nil {
		t.Fatal("unknown lossless classification was accepted")
	}
	unsupportedSource := facts("mp3", false, 2, nil)
	unsupportedSource.Container = AudioContainerMP3
	if _, err := PlanAudioTransform(AudioFormatSource, unsupportedSource); err == nil {
		t.Fatal("unverified source container was accepted")
	}
}

func TestFFmpegArgsSelectsOnlyAudioAndDropsInheritedMetadata(t *testing.T) {
	plan := AudioTransformPlan{Action: AudioCodecAAC, OutputContainer: AudioContainerM4A, Extension: "m4a", Bitrate: 224000}
	input := filepath.Join(t.TempDir(), "input file", "source.mp3")
	output := filepath.Join(t.TempDir(), "output file", "target.m4a")
	args, err := plan.FFmpegArgs(input, output, 3, AudioContainerMP3)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range [][]string{
		{"-i", input}, {"-map", "0:3"}, {"-map_metadata", "-1"},
		{"-map_chapters", "-1"}, {"-vn"}, {"-sn"}, {"-dn"}, {"-c:a", "aac"},
		{"-b:a", "224000"}, {"-f", "ipod"},
	} {
		if !containsSequence(args, want) {
			t.Errorf("args %q do not contain %q", args, want)
		}
	}
	if args[len(args)-1] != output {
		t.Fatalf("output path is not the final explicit argument: %q", args)
	}
	for _, arg := range args {
		if strings.Contains(arg, " ") && arg != input && arg != output {
			t.Errorf("unexpected joined argument %q", arg)
		}
	}
	for _, forbidden := range []string{"-metadata", "-map 0", "-ac", "-ar", "loudnorm", "-attach"} {
		if slices.Contains(args, forbidden) {
			t.Errorf("unexpected argument %q", forbidden)
		}
	}
}

func facts(codec string, lossless bool, channels int, bitrate *int) AudioSourceFacts {
	return AudioSourceFacts{StreamIndex: 3, Codec: codec, Lossless: &lossless, Channels: intPointer(channels), Bitrate: bitrate, BitDepth: intPointer(16)}
}

func containsSequence(haystack, needle []string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if slices.Equal(haystack[i:i+len(needle)], needle) {
			return true
		}
	}
	return false
}

func TestFFmpegArgsRejectsRelativePathsAndUsesADTSForAAC(t *testing.T) {
	plan := AudioTransformPlan{Action: AudioCodecCopy, OutputContainer: AudioContainerAAC, Extension: "aac"}
	input, err := filepath.Abs("input.aac")
	if err != nil {
		t.Fatal(err)
	}
	output, err := filepath.Abs("output.aac")
	if err != nil {
		t.Fatal(err)
	}
	for _, paths := range [][2]string{{input, "-option"}, {"relative.aac", output}, {input, "relative.aac"}} {
		if _, err := plan.FFmpegArgs(paths[0], paths[1], 0, AudioContainerMP3); err == nil {
			t.Fatalf("relative paths accepted: %v", paths)
		}
	}
	args, err := plan.FFmpegArgs(input, output, 0, AudioContainerAAC)
	if err != nil || !containsSequence(args, []string{"-f", "adts"}) {
		t.Fatalf("AAC args = %v, %v", args, err)
	}
}

func TestFFmpegArgsUsesVerifiedInputDemuxerBeforeInput(t *testing.T) {
	input, output := filepath.Join(t.TempDir(), "input"), filepath.Join(t.TempDir(), "output")
	plan := AudioTransformPlan{Action: AudioCodecCopy, OutputContainer: AudioContainerM4A, Extension: "m4a"}
	tests := []struct {
		container AudioContainer
		demuxer   string
	}{
		{AudioContainerMP3, "mp3"}, {AudioContainerFLAC, "flac"}, {AudioContainerWAV, "wav"},
		{AudioContainerOGG, "ogg"}, {AudioContainerOpus, "ogg"}, {AudioContainerAAC, "aac"},
		{AudioContainerM4A, "mov"}, {AudioContainerMKA, "matroska"},
	}
	for _, test := range tests {
		args, err := plan.FFmpegArgs(input, output, 0, test.container)
		if err != nil {
			t.Fatalf("container %q: %v", test.container, err)
		}
		index := slices.Index(args, "-i")
		if index < 2 || !slices.Contains(args[:index], test.demuxer) {
			t.Errorf("container %q did not select %q before -i: %v", test.container, test.demuxer, args)
		}
		if test.container == AudioContainerM4A && !containsSequence(args[:index], []string{"-protocol_whitelist", "file", "-f", "mov", "-enable_drefs", "0", "-use_absolute_path", "0"}) {
			t.Errorf("MOV safety options missing or after input: %v", args)
		}
	}
	if _, err := plan.FFmpegArgs(input, output, 0, "unknown"); err == nil {
		t.Fatal("unknown input container accepted")
	}
}
