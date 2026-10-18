package persistence

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/lrclib"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

const PublicationInputsVersion = 1
const PublicationInputsVersion2 = 2
const PublicationInputsVersion3 = 3

// PublicationInputs is the versioned, immutable execution contract retained by
// a publication attempt. Intent-specific data is deliberately disjoint.
type PublicationInputs struct {
	Version              int                                `json:"version"`
	QualityPolicyVersion int                                `json:"quality_policy_version,omitempty"`
	IntentKind           string                             `json:"intent_kind"`
	Audio                *PublicationAudioInput             `json:"audio,omitempty"`
	Rewrite              *PublicationRewriteInput           `json:"rewrite,omitempty"`
	ManualLyrics         *PublicationManualLyricsInput      `json:"manual_lyrics,omitempty"`
	Format               publication.AudioPublicationFormat `json:"format"`
	DesiredTags          map[string][]string                `json:"desired_tags"`
	Tools                []SourceAnalysisToolSelection      `json:"tools"`
}

// PublicationManualLyricsInput freezes explicit manual lyrics admission. It is
// a metadata authority receipt, not an instruction that the current writer can
// execute. V3 remains deliberately non-executable until a lyrics writer exists.
type PublicationManualLyricsInput struct {
	ExpectedMetadataRevision   int64     `json:"expected_metadata_revision"`
	ConfirmedCurrentGeneration uuid.UUID `json:"confirmed_current_generation"`
	State                      string    `json:"state"`
	Value                      string    `json:"value,omitempty"`
}

// PublicationAudioInput identifies the source file and verified audio result
// selected at admission. No codec classification is inferred from the path.
type PublicationAudioInput struct {
	WorkID                uuid.UUID           `json:"work_id"`
	RootID                uuid.UUID           `json:"root_id"`
	LocationID            uuid.UUID           `json:"location_id"`
	MediaVariantID        uuid.UUID           `json:"media_variant_id"`
	ProbeResultID         uuid.UUID           `json:"probe_result_id"`
	ConfiguredRootPath    string              `json:"configured_root_path"`
	InventoryRootPath     string              `json:"inventory_root_path"`
	RelativePath          string              `json:"relative_path"`
	SizeBytes             int64               `json:"size_bytes"`
	MTime                 time.Time           `json:"mtime"`
	AnalysisPolicyVersion int                 `json:"analysis_policy_version"`
	FFProbeVersion        string              `json:"ffprobe_version"`
	AudioFacts            PersistedAudioFacts `json:"audio_facts"`
}

// PublicationRewriteInput pins an already-published current artifact. It does
// not retain or require original-source identity.
type PublicationRewriteInput struct {
	CurrentGeneration uuid.UUID               `json:"current_generation"`
	RelativePath      string                  `json:"relative_path"`
	Ownership         PublicationFileIdentity `json:"ownership"`
	AudioFacts        PersistedAudioFacts     `json:"audio_facts"`
}

// PublicationFileIdentity records factual ownership evidence for a published
// file. Measurements and digest are optional when unavailable.
type PublicationFileIdentity struct {
	SizeBytes *int64 `json:"size_bytes,omitempty"`
	MTime     *int64 `json:"mtime,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
}

const PublicationCurrentOwnershipVersion = 1

type PublicationCurrentAudio struct {
	Version int                 `json:"version"`
	Facts   PersistedAudioFacts `json:"facts"`
}

type PublicationCurrentOwnership struct {
	Version  int                     `json:"version"`
	Identity PublicationFileIdentity `json:"identity"`
}

// PersistedAudioFacts is the stable JSON representation of verified stream
// facts; publication.AudioSourceFacts intentionally has no JSON wire contract.
type PersistedAudioFacts struct {
	StreamIndex              int                        `json:"stream_index"`
	Codec                    string                     `json:"codec"`
	Lossless                 *bool                      `json:"lossless"`
	Channels                 *int                       `json:"channels,omitempty"`
	Bitrate                  *int                       `json:"bitrate,omitempty"`
	BitDepth                 *int                       `json:"bit_depth,omitempty"`
	SampleRate               *int                       `json:"sample_rate,omitempty"`
	Container                publication.AudioContainer `json:"container"`
	SupportedSourceContainer bool                       `json:"supported_source_container"`
	ALACCapabilitiesVerified bool                       `json:"alac_capabilities_verified"`
}

var publicationDrivePath = regexp.MustCompile(`^[A-Za-z]:`)

// DecodePublicationInputs strictly decodes and validates the retained contract.
func DecodePublicationInputs(raw json.RawMessage) (PublicationInputs, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	var input PublicationInputs
	if err := decoder.Decode(&input); err != nil {
		return PublicationInputs{}, fmt.Errorf("decode publication inputs: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return PublicationInputs{}, fmt.Errorf("decode publication inputs: trailing JSON document")
		}
		return PublicationInputs{}, fmt.Errorf("decode publication inputs: %w", err)
	}
	if err := input.Validate(); err != nil {
		return PublicationInputs{}, err
	}
	return input, nil
}

// Validate enforces version, discriminated intent, selectors and portable paths.
func (input PublicationInputs) Validate() error {
	if input.Version != PublicationInputsVersion && input.Version != PublicationInputsVersion2 && input.Version != PublicationInputsVersion3 {
		return fmt.Errorf("unsupported publication inputs version %d", input.Version)
	}
	if input.Version == PublicationInputsVersion && input.QualityPolicyVersion != 0 {
		return errors.New("version 1 publication inputs cannot claim a quality policy version")
	}
	if input.Version == PublicationInputsVersion2 && input.QualityPolicyVersion != publication.QualityPolicyVersion {
		return fmt.Errorf("unsupported publication quality policy version %d", input.QualityPolicyVersion)
	}
	if input.Version == PublicationInputsVersion3 {
		if input.QualityPolicyVersion != publication.QualityPolicyVersion {
			return errors.New("version 3 publication inputs require the current quality policy")
		}
		if input.IntentKind != "rewrite" || input.Rewrite == nil || input.Audio != nil || input.ManualLyrics == nil {
			return errors.New("version 3 publication inputs require a manual lyrics rewrite")
		}
		if input.ManualLyrics.ExpectedMetadataRevision < 0 || input.ManualLyrics.ConfirmedCurrentGeneration == uuid.Nil || input.ManualLyrics.ConfirmedCurrentGeneration != input.Rewrite.CurrentGeneration {
			return errors.New("manual lyrics rewrite requires a metadata revision and confirmed current generation")
		}
		switch input.ManualLyrics.State {
		case "value":
			validated, err := lrclib.ValidateSyncedLyrics(input.ManualLyrics.Value)
			if err != nil || validated == "" || validated != input.ManualLyrics.Value {
				return errors.New("manual lyrics value must be non-empty valid synced LRC")
			}
		case "explicit_empty":
			if input.ManualLyrics.Value != "" {
				return errors.New("explicitly empty manual lyrics cannot include a value")
			}
		default:
			return errors.New("manual lyrics rewrite requires value or explicit_empty")
		}
	} else if input.ManualLyrics != nil {
		return errors.New("manual lyrics are only valid in version 3 publication inputs")
	}
	if input.IntentKind != "audio" && input.IntentKind != "rewrite" {
		return fmt.Errorf("unsupported publication intent %q", input.IntentKind)
	}
	if (input.IntentKind == "audio" && (input.Audio == nil || input.Rewrite != nil)) ||
		(input.IntentKind == "rewrite" && (input.Rewrite == nil || input.Audio != nil)) {
		return fmt.Errorf("publication intent does not match its input")
	}
	if input.Format != publication.AudioFormatSource && input.Format != publication.AudioFormatMKA && input.Format != publication.AudioFormatM4A {
		return fmt.Errorf("unsupported publication audio format %q", input.Format)
	}
	if input.DesiredTags == nil {
		return fmt.Errorf("publication desired tags are required")
	}
	for name, values := range input.DesiredTags {
		if strings.TrimSpace(name) == "" || values == nil {
			return fmt.Errorf("publication desired tags must contain named, non-nil value lists")
		}
		if input.Version == PublicationInputsVersion3 && strings.EqualFold(strings.TrimSpace(name), "LYRICS") {
			return errors.New("manual lyrics must not be encoded in mandatory desired tags")
		}
	}
	if err := validateFrozenPublicationTools(input.Tools); err != nil {
		return err
	}
	if input.Audio != nil {
		if err := input.Audio.Validate(); err != nil {
			return fmt.Errorf("invalid publication audio input: %w", err)
		}
	}
	if input.Rewrite != nil {
		if err := input.Rewrite.Validate(); err != nil {
			return fmt.Errorf("invalid publication rewrite input: %w", err)
		}
	}
	return nil
}

// SupportsExecution excludes V3 admission receipts until a supported writer is
// implemented. Do not infer capability from an input version being recognized.
func (input PublicationInputs) SupportsExecution() bool {
	return input.Version == PublicationInputsVersion || input.Version == PublicationInputsVersion2
}

func (input PublicationAudioInput) Validate() error {
	if input.WorkID == uuid.Nil || input.RootID == uuid.Nil || input.LocationID == uuid.Nil || input.MediaVariantID == uuid.Nil || input.ProbeResultID == uuid.Nil {
		return fmt.Errorf("work, root, location, canonical media variant and probe result selectors are required")
	}
	if !filepath.IsAbs(input.ConfiguredRootPath) || !filepath.IsAbs(input.InventoryRootPath) {
		return fmt.Errorf("configured and inventory root paths must be absolute paths")
	}
	if !frozenPublicationRelativePath(input.RelativePath) {
		return fmt.Errorf("source relative path is not portable and lexical")
	}
	if input.SizeBytes < 0 || input.MTime.IsZero() || input.MTime.Location() != time.UTC || input.MTime.Nanosecond()%int(time.Microsecond) != 0 {
		return fmt.Errorf("source size and UTC microsecond modification time are required")
	}
	if input.AnalysisPolicyVersion <= 0 || strings.TrimSpace(input.FFProbeVersion) == "" {
		return fmt.Errorf("verified analysis policy and ffprobe version are required")
	}
	return input.AudioFacts.Validate()
}

func (input PublicationRewriteInput) Validate() error {
	if input.CurrentGeneration == uuid.Nil || !frozenPublicationRelativePath(input.RelativePath) {
		return fmt.Errorf("current generation and portable published path are required")
	}
	if input.Ownership.SizeBytes == nil && input.Ownership.MTime == nil && input.Ownership.SHA256 == "" {
		return fmt.Errorf("published file ownership identity is required")
	}
	if input.Ownership.SizeBytes != nil && *input.Ownership.SizeBytes < 0 {
		return fmt.Errorf("published file size identity is invalid")
	}
	if input.Ownership.SHA256 != "" && !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(input.Ownership.SHA256) {
		return fmt.Errorf("published file digest identity is invalid")
	}
	return input.AudioFacts.Validate()
}

func (facts PersistedAudioFacts) Validate() error {
	if facts.StreamIndex < 0 || strings.TrimSpace(facts.Codec) == "" || facts.Lossless == nil {
		return fmt.Errorf("verified selected audio stream facts are required")
	}
	if facts.Channels != nil && *facts.Channels <= 0 || facts.Bitrate != nil && *facts.Bitrate < 0 || facts.BitDepth != nil && *facts.BitDepth <= 0 || facts.SampleRate != nil && *facts.SampleRate <= 0 {
		return fmt.Errorf("audio stream measurements are invalid")
	}
	if facts.ALACCapabilitiesVerified {
		return fmt.Errorf("ALAC capabilities have no verified tool evidence")
	}
	return nil
}

func validateFrozenPublicationTools(selections []SourceAnalysisToolSelection) error {
	if len(selections) != 2 {
		return fmt.Errorf("publication ffmpeg and ffprobe selections are required")
	}
	seen := make(map[string]bool, 2)
	var installation uuid.UUID
	for _, selection := range selections {
		if selection.PackageKind != "ffmpeg" || selection.InstallationID == uuid.Nil || !frozenPublicationRelativePath(selection.RelativePath) || selection.Version == "" || selection.VersionBanner == "" {
			return fmt.Errorf("publication tool selection is incomplete")
		}
		if (selection.Executable != "ffmpeg" && selection.Executable != "ffprobe") || seen[selection.Executable] {
			return fmt.Errorf("publication ffmpeg and ffprobe selections must be unique")
		}
		if installation != uuid.Nil && installation != selection.InstallationID {
			return fmt.Errorf("publication tools must use the same installation")
		}
		installation = selection.InstallationID
		seen[selection.Executable] = true
	}
	if !seen["ffmpeg"] || !seen["ffprobe"] {
		return fmt.Errorf("publication ffmpeg and ffprobe selections are required")
	}
	return nil
}

func frozenPublicationRelativePath(value string) bool {
	if value == "" || strings.Contains(value, `\`) || strings.ContainsRune(value, 0) || strings.HasPrefix(value, "/") || publicationDrivePath.MatchString(value) {
		return false
	}
	clean := path.Clean(value)
	return clean == value && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

// Clone returns a detached copy, preserving non-nil empty tag lists.
func (input PublicationInputs) Clone() PublicationInputs {
	clone := input
	if input.DesiredTags != nil {
		clone.DesiredTags = make(map[string][]string, len(input.DesiredTags))
		for key, values := range input.DesiredTags {
			if values == nil {
				clone.DesiredTags[key] = nil
				continue
			}
			clone.DesiredTags[key] = append(make([]string, 0, len(values)), values...)
		}
	}
	clone.Tools = append([]SourceAnalysisToolSelection(nil), input.Tools...)
	if input.Audio != nil {
		audio := *input.Audio
		audio.AudioFacts = clonePersistedAudioFacts(audio.AudioFacts)
		clone.Audio = &audio
	}
	if input.Rewrite != nil {
		rewrite := *input.Rewrite
		rewrite.AudioFacts = clonePersistedAudioFacts(rewrite.AudioFacts)
		rewrite.Ownership = clonePublicationFileIdentity(rewrite.Ownership)
		clone.Rewrite = &rewrite
	}
	if input.ManualLyrics != nil {
		manual := *input.ManualLyrics
		clone.ManualLyrics = &manual
	}
	return clone
}

func (facts PersistedAudioFacts) AudioSourceFacts() publication.AudioSourceFacts {
	copy := clonePersistedAudioFacts(facts)
	return publication.AudioSourceFacts{
		StreamIndex: copy.StreamIndex, Codec: copy.Codec, Lossless: copy.Lossless, Channels: copy.Channels,
		Bitrate: copy.Bitrate, BitDepth: copy.BitDepth, Container: copy.Container,
		SupportedSourceContainer: copy.SupportedSourceContainer, ALACCapabilitiesVerified: copy.ALACCapabilitiesVerified,
	}
}

func clonePersistedAudioFacts(facts PersistedAudioFacts) PersistedAudioFacts {
	if facts.Lossless != nil {
		value := *facts.Lossless
		facts.Lossless = &value
	}
	if facts.Channels != nil {
		value := *facts.Channels
		facts.Channels = &value
	}
	if facts.Bitrate != nil {
		value := *facts.Bitrate
		facts.Bitrate = &value
	}
	if facts.BitDepth != nil {
		value := *facts.BitDepth
		facts.BitDepth = &value
	}
	if facts.SampleRate != nil {
		value := *facts.SampleRate
		facts.SampleRate = &value
	}
	return facts
}

func clonePublicationFileIdentity(identity PublicationFileIdentity) PublicationFileIdentity {
	if identity.SizeBytes != nil {
		value := *identity.SizeBytes
		identity.SizeBytes = &value
	}
	if identity.MTime != nil {
		value := *identity.MTime
		identity.MTime = &value
	}
	return identity
}
