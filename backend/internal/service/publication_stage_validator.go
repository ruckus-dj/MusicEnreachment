package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

// PublicationStageProbe is injectable to make validation independent of a
// particular ffprobe installation in tests.
type PublicationStageProbe func(context.Context, string, sourcefs.RegularFile, publication.AudioContainer) ([]byte, error)

// RealPublicationStageValidator validates only a completed, private stage file.
// Tool paths are resolved from the frozen publication contract.
type RealPublicationStageValidator struct {
	Tools          PublicationPinnedTools
	MetadataReader interface {
		Read(context.Context, string, metadata.ReadRequest) (metadata.Capture, error)
	}
	Probe           PublicationStageProbe
	supervisedProbe *ScopeBoundPublicationStageProbe
}

func (validator RealPublicationStageValidator) validateSupervised(scope *PublicationExecutionScope) error {
	if validator.Probe != nil || validator.supervisedProbe == nil || validator.supervisedProbe.scope != scope || validator.Tools != validator.supervisedProbe.pinnedTools {
		return errors.New("supervised publication validator requires a scope-bound probe")
	}
	return nil
}

func (validator RealPublicationStageValidator) Validate(ctx context.Context, stagePath string, input persistence.PublicationInputs) (result PublicationStageObservation, resultErr error) {
	if !input.SupportsExecution() {
		return PublicationStageObservation{}, fmt.Errorf("publication inputs version %d does not support execution", input.Version)
	}
	if err := ctx.Err(); err != nil {
		return PublicationStageObservation{}, err
	}
	if stagePath == "" || !filepath.IsAbs(stagePath) {
		return PublicationStageObservation{}, errors.New("publication stage path must be absolute")
	}
	if err := input.Validate(); err != nil {
		return PublicationStageObservation{}, fmt.Errorf("invalid frozen publication input: %w", err)
	}
	before, err := os.Lstat(stagePath)
	if err != nil {
		return PublicationStageObservation{}, fmt.Errorf("stat publication stage: %w", err)
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return PublicationStageObservation{}, errors.New("publication stage is not a regular file")
	}
	file, err := os.Open(stagePath)
	if err != nil {
		return PublicationStageObservation{}, fmt.Errorf("open publication stage: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close publication validation file: %w", err))
		}
	}()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return PublicationStageObservation{}, errors.New("publication stage changed while opening")
	}
	anchored := sourcefs.RegularFile(publicationStageFile{file})
	probe := validator.Probe
	if validator.Tools == nil {
		return PublicationStageObservation{}, errors.New("publication pinned tools are unavailable")
	}
	executable, err := validator.Tools.Executable(ctx, input.Tools, "ffprobe")
	if err != nil {
		return PublicationStageObservation{}, fmt.Errorf("resolve pinned ffprobe: %w", err)
	}
	plan, err := publicationStageAudioPlan(input, inputAudioFacts(input))
	if err != nil {
		return PublicationStageObservation{}, fmt.Errorf("plan publication stage probe container: %w", err)
	}
	if probe == nil && validator.supervisedProbe == nil {
		probe = func(ctx context.Context, executable string, file sourcefs.RegularFile, container publication.AudioContainer) ([]byte, error) {
			ffprobe, err := tools.NewFFProbe(executable)
			if err != nil {
				return nil, err
			}
			demuxer, _ := publication.InputDemuxer(container)
			return ffprobe.ProbePublicationTechnicalFile(ctx, file, demuxer)
		}
	}
	var raw []byte
	if validator.supervisedProbe != nil {
		raw, err = validator.supervisedProbe.probe(ctx, input.Tools, executable, anchored, plan.OutputContainer)
	} else {
		raw, err = probe(ctx, executable, anchored, plan.OutputContainer)
	}
	if err != nil {
		return PublicationStageObservation{}, fmt.Errorf("probe publication stage: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return PublicationStageObservation{}, err
	}
	audio, err := ParseSourceTechnicalAnalysis(raw)
	if err != nil {
		return PublicationStageObservation{}, fmt.Errorf("parse publication stage probe: %w", err)
	}
	if err := validatePublicationTopology(raw); err != nil {
		return PublicationStageObservation{}, err
	}
	facts := inputAudioFacts(input)
	if len(audio.Streams) != 1 {
		return PublicationStageObservation{}, errors.New("publication stage must contain exactly one audio stream")
	}
	actual := audio.Streams[0]
	if actual.CodecName == nil || strings.TrimSpace(*actual.CodecName) == "" {
		return PublicationStageObservation{}, errors.New("publication stage audio codec is missing")
	}
	actualCodec := *actual.CodecName
	if audio.Container.Name == nil || strings.TrimSpace(*audio.Container.Name) == "" {
		return PublicationStageObservation{}, errors.New("publication stage container is missing")
	}
	actualContainer := *audio.Container.Name
	expectedCodec := facts.Codec
	if plan.Action == publication.AudioCodecALAC {
		expectedCodec = "alac"
	}
	if plan.Action == publication.AudioCodecAAC {
		expectedCodec = "aac"
	}
	if actualCodec != expectedCodec {
		return PublicationStageObservation{}, errors.New("publication stage audio codec does not match the frozen plan")
	}
	if !publicationContainerMatches(actualContainer, plan.OutputContainer) {
		return PublicationStageObservation{}, errors.New("publication stage container does not match the frozen plan")
	}
	if facts.Channels != nil && (actual.Channels == nil || *actual.Channels != int64(*facts.Channels)) {
		return PublicationStageObservation{}, errors.New("publication stage channel count does not match the frozen plan")
	}
	if !publicationStageSampleRateMatches(facts.SampleRate, actual.SampleRateHz) {
		return PublicationStageObservation{}, errors.New("publication stage sample rate does not match the frozen plan")
	}
	if facts.Lossless != nil && *facts.Lossless && facts.BitDepth != nil && (actual.BitsPerSample == nil || *actual.BitsPerSample != int64(*facts.BitDepth)) {
		return PublicationStageObservation{}, errors.New("publication stage bit depth does not preserve the frozen lossless plan")
	}
	if validator.MetadataReader == nil {
		return PublicationStageObservation{}, errors.New("publication metadata reader is unavailable")
	}
	request := metadata.ReadRequest{CoverInspection: true}
	capture, err := validator.MetadataReader.Read(ctx, stagePath, request)
	if err != nil {
		return PublicationStageObservation{}, fmt.Errorf("read publication stage metadata: %w", err)
	}
	if !equalTags(capture.Tags, input.DesiredTags) {
		return PublicationStageObservation{}, errors.New("publication stage metadata does not match requested tags")
	}
	if err := ctx.Err(); err != nil {
		return PublicationStageObservation{}, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return PublicationStageObservation{}, err
	}
	hash := sha256.New()
	buffer := make([]byte, 128*1024)
	for {
		if err := ctx.Err(); err != nil {
			return PublicationStageObservation{}, err
		}
		read, readErr := file.Read(buffer)
		if read > 0 {
			if _, err := hash.Write(buffer[:read]); err != nil {
				return PublicationStageObservation{}, fmt.Errorf("hash publication stage: %w", err)
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return PublicationStageObservation{}, fmt.Errorf("hash publication stage: %w", readErr)
		}
	}
	after, err := os.Lstat(stagePath)
	openedAfter, statErr := file.Stat()
	if err != nil || statErr != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) || !os.SameFile(openedAfter, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return PublicationStageObservation{}, errors.New("publication stage changed during validation")
	}
	if err := ctx.Err(); err != nil {
		return PublicationStageObservation{}, err
	}
	lossless, knownLossless := publicationCodecLossless(actualCodec)
	if !knownLossless {
		return PublicationStageObservation{}, errors.New("publication stage codec losslessness is unknown")
	}
	if actual.Index == nil || *actual.Index < 0 || int64(int(*actual.Index)) != *actual.Index {
		return PublicationStageObservation{}, errors.New("publication stage audio stream index is missing or out of range")
	}
	actualAudioFacts, err := persistedPublicationStageAudioFacts(int(*actual.Index), actualCodec, lossless, actual.Channels, actual.ChannelLayout, actual.BitRate, actual.BitsPerSample, actual.SampleRateHz, plan.OutputContainer)
	if err != nil {
		return PublicationStageObservation{}, err
	}
	actualAudio, err := json.Marshal(struct {
		Version int                             `json:"version"`
		Facts   persistence.PersistedAudioFacts `json:"facts"`
	}{Version: 1, Facts: actualAudioFacts})
	if err != nil {
		return PublicationStageObservation{}, fmt.Errorf("encode publication stage audio facts: %w", err)
	}
	actualMetadata, _ := json.Marshal(map[string]any{"version": 1, "tags": capture.Tags})
	size, mtime := after.Size(), after.ModTime().UnixMicro()
	result = PublicationStageObservation{Ownership: persistence.ArtifactOwnership{ExpectedSize: &size, ExpectedMTime: &mtime}, Audio: actualAudio, Metadata: actualMetadata, SHA256: hex.EncodeToString(hash.Sum(nil))}
	assets, err := ObservePublicationAssets(capture)
	if err != nil {
		return PublicationStageObservation{}, fmt.Errorf("observe publication stage assets: %w", err)
	}
	result.Assets, err = json.Marshal(assets)
	if err != nil {
		return PublicationStageObservation{}, fmt.Errorf("encode publication stage assets: %w", err)
	}
	result.QualityPolicyVersion = input.QualityPolicyVersion
	return result, nil
}

func publicationStageSampleRateMatches(expected *int, actual *int64) bool {
	return expected == nil || actual != nil && *actual == int64(*expected)
}

func publicationStageAudioPlan(input persistence.PublicationInputs, facts persistence.PersistedAudioFacts) (publication.AudioTransformPlan, error) {
	format := input.Format
	if input.Rewrite != nil {
		// A rewrite republishes the already-encoded artifact. Preserve its
		// verified codec/container rather than applying the requested format a
		// second time (which could cause another lossy encode).
		format = publication.AudioFormatSource
	}
	return publication.PlanAudioTransform(format, facts.AudioSourceFacts())
}

func publicationContainerMatches(probeName string, expected publication.AudioContainer) bool {
	for _, name := range strings.Split(probeName, ",") {
		name = strings.TrimSpace(name)
		switch expected {
		case publication.AudioContainerMKA:
			if name == "matroska" || name == "webm" {
				return true
			}
		case publication.AudioContainerM4A:
			if name == "mov" || name == "mp4" || name == "m4a" || name == "ipod" {
				return true
			}
		case publication.AudioContainerOGG, publication.AudioContainerOpus:
			if name == "ogg" {
				return true
			}
		default:
			if name == string(expected) {
				return true
			}
		}
	}
	return false
}

func publicationCodecLossless(codec string) (bool, bool) {
	switch codec {
	case "flac", "alac", "pcm_s16le", "pcm_s24le", "pcm_s32le", "pcm_f32le", "pcm_f64le":
		return true, true
	case "aac", "mp3", "opus", "vorbis", "ac3", "eac3":
		return false, true
	default:
		return false, false
	}
}

type publicationStageFile struct{ file *os.File }

func (f publicationStageFile) Stat(context.Context) (os.FileInfo, error) { return f.file.Stat() }
func (f publicationStageFile) Borrow(ctx context.Context, callback func(*os.File) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return callback(f.file)
}
func (f publicationStageFile) Close() error { return nil }

func inputAudioFacts(input persistence.PublicationInputs) persistence.PersistedAudioFacts {
	if input.Audio != nil {
		return input.Audio.AudioFacts
	}
	return input.Rewrite.AudioFacts
}

func persistedPublicationStageAudioFacts(index int, codec string, lossless bool, channels *int64, channelLayout *string, bitrate, bitDepth, sampleRate *int64, container publication.AudioContainer) (persistence.PersistedAudioFacts, error) {
	toInt := func(value *int64) (*int, error) {
		if value == nil {
			return nil, nil
		}
		converted := int(*value)
		if int64(converted) != *value {
			return nil, errors.New("publication stage audio measurement is out of range")
		}
		return &converted, nil
	}
	channelsValue, err := toInt(channels)
	if err != nil {
		return persistence.PersistedAudioFacts{}, err
	}
	bitrateValue, err := toInt(bitrate)
	if err != nil {
		return persistence.PersistedAudioFacts{}, err
	}
	bitDepthValue, err := toInt(bitDepth)
	if err != nil {
		return persistence.PersistedAudioFacts{}, err
	}
	sampleRateValue, err := toInt(sampleRate)
	if err != nil {
		return persistence.PersistedAudioFacts{}, err
	}
	losslessValue := lossless
	layout := ""
	if channelLayout != nil {
		layout = *channelLayout
	}
	return persistence.PersistedAudioFacts{
		StreamIndex: index, Codec: codec, Lossless: &losslessValue,
		Channels: channelsValue, ChannelLayout: layout, Bitrate: bitrateValue, BitDepth: bitDepthValue, SampleRate: sampleRateValue,
		Container: container, SupportedSourceContainer: publicationSourceFactsSupported(container, codec),
	}, nil
}

func publicationSourceFactsSupported(container publication.AudioContainer, codec string) bool {
	switch container {
	case publication.AudioContainerFLAC:
		return codec == "flac"
	case publication.AudioContainerMP3:
		return codec == "mp3"
	case publication.AudioContainerWAV:
		return strings.HasPrefix(codec, "pcm_") && (strings.HasSuffix(codec, "le") || strings.HasSuffix(codec, "be"))
	case publication.AudioContainerOGG:
		return codec == "vorbis"
	case publication.AudioContainerOpus:
		return codec == "opus"
	case publication.AudioContainerAAC:
		return codec == "aac"
	case publication.AudioContainerM4A:
		return codec == "aac" || codec == "alac"
	case publication.AudioContainerMKA:
		switch codec {
		case "flac", "opus", "vorbis", "aac", "mp3", "alac":
			return true
		}
	default:
		return false
	}
	return false
}

func equalTags(actual, desired map[string][]string) bool {
	for key, values := range desired {
		got, ok := actual[key]
		if len(values) == 0 {
			if ok && len(got) != 0 {
				return false
			}
			continue
		}
		if !ok || len(got) != len(values) {
			return false
		}
		for i := range values {
			if got[i] != values[i] {
				return false
			}
		}
	}
	for key, values := range actual {
		if _, requested := desired[key]; !requested && len(values) != 0 {
			return false
		}
	}
	return true
}
func validatePublicationTopology(raw []byte) error {
	var response struct {
		Streams []struct {
			Type string `json:"codec_type"`
		} `json:"streams"`
		Chapters []json.RawMessage `json:"chapters"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return fmt.Errorf("decode publication topology: %w", err)
	}
	count := 0
	for _, stream := range response.Streams {
		if stream.Type != "audio" {
			return errors.New("publication stage contains a non-audio stream")
		}
		count++
	}
	if count != 1 || len(response.Chapters) != 0 {
		return errors.New("publication stage has unexpected stream topology or chapters")
	}
	return nil
}
