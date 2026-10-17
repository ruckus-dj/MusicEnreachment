package publication

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
)

// AudioContainer is a closed set of containers understood by the publication
// planner. Callers must translate probed container names to these values.
type AudioContainer string

const (
	AudioContainerMP3  AudioContainer = "mp3"
	AudioContainerFLAC AudioContainer = "flac"
	AudioContainerWAV  AudioContainer = "wav"
	AudioContainerOGG  AudioContainer = "ogg"
	AudioContainerOpus AudioContainer = "opus"
	AudioContainerAAC  AudioContainer = "aac"
	AudioContainerM4A  AudioContainer = "m4a"
	AudioContainerMKA  AudioContainer = "mka"
)

// AudioPublicationFormat selects the prepared audio container.
type AudioPublicationFormat string

const (
	AudioFormatSource AudioPublicationFormat = "source"
	AudioFormatMKA    AudioPublicationFormat = "mka"
	AudioFormatM4A    AudioPublicationFormat = "m4a"
)

// AudioCodecAction is the audio-stream operation ffmpeg performs.
type AudioCodecAction string

const (
	AudioCodecCopy AudioCodecAction = "copy"
	AudioCodecALAC AudioCodecAction = "alac"
	AudioCodecAAC  AudioCodecAction = "aac"
)

// AudioSourceFacts contains only verified facts about the selected stream.
// Lossless is nil when the analysis did not establish classification. Bitrate
// is the measured average stream bitrate in bits per second.
type AudioSourceFacts struct {
	StreamIndex              int
	Codec                    string
	Lossless                 *bool
	Channels                 *int
	Bitrate                  *int
	BitDepth                 *int
	Container                AudioContainer
	SupportedSourceContainer bool
	ALACCapabilitiesVerified bool
}

// AudioTransformPlan is a shell-free ffmpeg argv plan. Paths are individual
// arguments and must be supplied by the caller; they are never interpolated.
type AudioTransformPlan struct {
	Action          AudioCodecAction
	OutputContainer AudioContainer
	Extension       string
	Bitrate         int
}

var errUnsupportedAudioPlan = errors.New("audio transform is unsupported")

// PlanAudioTransform applies the publication format rules to a verified stream.
func PlanAudioTransform(format AudioPublicationFormat, source AudioSourceFacts) (AudioTransformPlan, error) {
	if source.StreamIndex < 0 || source.Codec == "" || !knownAudioCodec(source.Codec) {
		return AudioTransformPlan{}, fmt.Errorf("%w: selected stream codec or index is invalid", errUnsupportedAudioPlan)
	}
	if source.Lossless == nil {
		return AudioTransformPlan{}, fmt.Errorf("%w: selected stream lossless classification is unknown", errUnsupportedAudioPlan)
	}
	if source.Channels != nil && *source.Channels <= 0 {
		return AudioTransformPlan{}, fmt.Errorf("%w: selected stream channel count is invalid", errUnsupportedAudioPlan)
	}

	if format == AudioFormatSource {
		if !source.SupportedSourceContainer || !validSourceContainer(source.Container) {
			return AudioTransformPlan{}, fmt.Errorf("%w: source container is not verified as supported", errUnsupportedAudioPlan)
		}
		return AudioTransformPlan{Action: AudioCodecCopy, OutputContainer: source.Container, Extension: string(source.Container)}, nil
	}

	switch format {
	case AudioFormatMKA:
		return AudioTransformPlan{Action: AudioCodecCopy, OutputContainer: AudioContainerMKA, Extension: "mka"}, nil
	case AudioFormatM4A:
		if source.Codec == "aac" || source.Codec == "alac" {
			return AudioTransformPlan{Action: AudioCodecCopy, OutputContainer: AudioContainerM4A, Extension: "m4a"}, nil
		}
		if *source.Lossless {
			if source.Channels == nil || source.BitDepth == nil || !validALACBitDepth(*source.BitDepth) || !source.ALACCapabilitiesVerified {
				return AudioTransformPlan{}, fmt.Errorf("%w: verified ALAC channel and sample-depth capability is required", errUnsupportedAudioPlan)
			}
			return AudioTransformPlan{Action: AudioCodecALAC, OutputContainer: AudioContainerM4A, Extension: "m4a"}, nil
		}
		if source.Channels == nil {
			return AudioTransformPlan{}, fmt.Errorf("%w: channel count is required for AAC transcoding", errUnsupportedAudioPlan)
		}
		if *source.Channels > 2 {
			return AudioTransformPlan{}, fmt.Errorf("%w: multichannel AAC transcoding is unsupported", errUnsupportedAudioPlan)
		}
		bitrate := aacBitrate(source)
		return AudioTransformPlan{Action: AudioCodecAAC, OutputContainer: AudioContainerM4A, Extension: "m4a", Bitrate: bitrate}, nil
	default:
		return AudioTransformPlan{}, fmt.Errorf("%w: publication format %q is unknown", errUnsupportedAudioPlan, format)
	}
}

// FFmpegArgs builds explicit argv, never a shell command. Metadata, chapters,
// attachments and non-audio streams are deliberately excluded.
func (p AudioTransformPlan) FFmpegArgs(inputPath, outputPath string, streamIndex int) ([]string, error) {
	if !filepath.IsAbs(inputPath) || !filepath.IsAbs(outputPath) || streamIndex < 0 || p.Extension == "" {
		return nil, fmt.Errorf("%w: ffmpeg input and output paths must be absolute and the selected stream valid", errUnsupportedAudioPlan)
	}
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "warning", "-n", "-i", inputPath,
		"-map", "0:" + strconv.Itoa(streamIndex), "-map_metadata", "-1", "-map_chapters", "-1",
		"-vn", "-sn", "-dn", "-map_metadata:s:a", "-1", "-map_chapters", "-1"}
	switch p.Action {
	case AudioCodecCopy:
		args = append(args, "-c:a", "copy")
	case AudioCodecALAC:
		args = append(args, "-c:a", "alac")
	case AudioCodecAAC:
		if p.Bitrate <= 0 {
			return nil, fmt.Errorf("%w: AAC bitrate is not set", errUnsupportedAudioPlan)
		}
		args = append(args, "-c:a", "aac", "-b:a", strconv.Itoa(p.Bitrate))
	default:
		return nil, fmt.Errorf("%w: codec action is unknown", errUnsupportedAudioPlan)
	}
	switch p.OutputContainer {
	case AudioContainerMKA:
		args = append(args, "-f", "matroska")
	case AudioContainerM4A:
		args = append(args, "-f", "ipod")
	default:
		if !validSourceContainer(p.OutputContainer) {
			return nil, fmt.Errorf("%w: output container is unknown", errUnsupportedAudioPlan)
		}
		args = append(args, "-f", ffmpegFormat(p.OutputContainer))
	}
	return append(args, outputPath), nil
}

func aacBitrate(source AudioSourceFacts) int {
	if source.Codec == "mp3" && source.Bitrate != nil && *source.Bitrate > 0 && source.Channels != nil && *source.Channels <= 2 {
		return *source.Bitrate
	}
	if *source.Channels == 1 {
		return 160000
	}
	return 320000
}

func knownAudioCodec(codec string) bool {
	switch codec {
	case "mp3", "flac", "wavpack", "alac", "aac", "vorbis", "opus", "ac3", "eac3", "dts", "pcm_s16le", "pcm_s24le", "pcm_s32le", "pcm_f32le", "pcm_f64le":
		return true
	default:
		return false
	}
}

func validSourceContainer(container AudioContainer) bool {
	switch container {
	case AudioContainerMP3, AudioContainerFLAC, AudioContainerWAV, AudioContainerOGG, AudioContainerOpus, AudioContainerAAC, AudioContainerM4A, AudioContainerMKA:
		return true
	default:
		return false
	}
}

// validALACBitDepth accepts only sample depths ALAC preserves without a hidden
// loss: ffmpeg's 32-bit ALAC path stores 24-bit samples, so 32 is not lossless.
func validALACBitDepth(depth int) bool { return depth == 16 || depth == 24 }

func ffmpegFormat(container AudioContainer) string {
	if container == AudioContainerMP3 {
		return "mp3"
	}
	if container == AudioContainerFLAC {
		return "flac"
	}
	if container == AudioContainerWAV {
		return "wav"
	}
	if container == AudioContainerOGG || container == AudioContainerOpus {
		return "ogg"
	}
	if container == AudioContainerAAC {
		return "adts"
	}
	return string(container)
}
