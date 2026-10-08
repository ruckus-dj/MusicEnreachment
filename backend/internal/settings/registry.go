package settings

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
)

type settingDefinition[T any] struct {
	name       string
	kind       string
	parse      func(string) (T, error)
	serialize  func(T) string
	validate   func(T) error
	defaultVal *T
	mutable    bool
	sensitive  bool
}

type settingMetadata struct {
	name       string
	kind       string
	hasDefault bool
	mutable    bool
	sensitive  bool
}

type registeredSetting interface {
	metadata() settingMetadata
}

func (definition settingDefinition[T]) metadata() settingMetadata {
	return settingMetadata{
		name: definition.name, kind: definition.kind, hasDefault: definition.defaultVal != nil,
		mutable: definition.mutable, sensitive: definition.sensitive,
	}
}

func readSetting[T any](ctx context.Context, store Store, definition settingDefinition[T]) (T, bool, error) {
	raw, exists, err := store.Get(ctx, definition.name)
	if err != nil {
		var zero T
		return zero, false, err
	}
	if !exists {
		if definition.defaultVal != nil {
			return *definition.defaultVal, false, nil
		}
		var zero T
		return zero, false, nil
	}
	value, err := definition.parse(raw)
	if err == nil && definition.validate != nil {
		err = definition.validate(value)
	}
	if err != nil {
		var zero T
		return zero, true, fmt.Errorf("invalid stored %s: %w", definition.name, err)
	}
	return value, true, nil
}

func serializeSetting[T any](definition settingDefinition[T], value T) (string, error) {
	if definition.validate != nil {
		if err := definition.validate(value); err != nil {
			return "", err
		}
	}
	raw := definition.serialize(value)
	if _, err := definition.parse(raw); err != nil {
		return "", err
	}
	return raw, nil
}

func parseIdentity(value string) (string, error) {
	if _, err := uuid.Parse(value); err != nil {
		return "", fmt.Errorf("invalid configuration identity: %w", err)
	}
	return value, nil
}

func parseDirectory(value string) (string, error) { return NormalizePath(value) }

func parsePublicationFormat(value string) (string, error) {
	if value != "source" && value != "mka" {
		return "", fmt.Errorf("publication format must be 'source' or 'mka'")
	}
	return value, nil
}

func parseMusicBrainzMode(value string) (string, error) {
	if value != "public" && value != "self-hosted" {
		return "", fmt.Errorf("musicbrainz mode must be 'public' or 'self-hosted'")
	}
	return value, nil
}

func parseMusicBrainzURL(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("self-hosted mode requires valid HTTP(S) base URL")
	}
	return value, nil
}

func parseUnicodeNormalization(value string) (string, error) {
	switch value {
	case "none", "nfc", "nfd", "unknown":
		return value, nil
	default:
		return "", fmt.Errorf("invalid filesystem Unicode normalization")
	}
}

func parseLevel(value string) (string, error) {
	if _, ok := parseLogLevel(value); !ok {
		return "", fmt.Errorf("invalid log level %q", value)
	}
	return value, nil
}

func parsePlatformOS(value string) (string, error) {
	if value != "linux" && value != "darwin" && value != "windows" {
		return "", fmt.Errorf("unsupported instance OS")
	}
	return value, nil
}

func parsePlatformArch(value string) (string, error) {
	if value != "amd64" && value != "arm64" {
		return "", fmt.Errorf("unsupported instance architecture")
	}
	return value, nil
}

func textDefinition(name, kind string, parse func(string) (string, error), mutable bool) settingDefinition[string] {
	return settingDefinition[string]{
		name: name, kind: kind, parse: parse, serialize: func(value string) string { return value },
		mutable: mutable,
	}
}

func boolDefinition(name string) settingDefinition[bool] {
	return settingDefinition[bool]{
		name: name, kind: "bool", parse: strconv.ParseBool, serialize: strconv.FormatBool,
		mutable: true,
	}
}

func timeDefinition(name string, mutable bool) settingDefinition[time.Time] {
	return settingDefinition[time.Time]{
		name: name, kind: "timestamp", parse: func(value string) (time.Time, error) {
			return time.Parse(time.RFC3339Nano, value)
		},
		serialize: func(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) },
		mutable:   mutable,
	}
}

var (
	defaultPublic          = "public"
	defaultInfo            = "info"
	defaultTrue            = true
	defaultFileConcurrency = 4

	platformOSSetting      = textDefinition(PlatformGOOSKey, "enum", parsePlatformOS, false)
	platformArchSetting    = textDefinition(PlatformGOARCHKey, "enum", parsePlatformArch, false)
	toolsRootSetting       = textDefinition(ToolsDirectoryKey, "path", parseDirectory, true)
	outputRootSetting      = textDefinition(OutputDirectoryKey, "path", parseDirectory, true)
	outputCaseSetting      = boolDefinition(OutputCaseSensitiveKey)
	outputUnicodeSetting   = textDefinition(OutputUnicodeNormalizationKey, "enum", parseUnicodeNormalization, true)
	publicationSetting     = textDefinition(PublicationFormatKey, "enum", parsePublicationFormat, true)
	musicBrainzModeSetting = settingDefinition[string]{
		name: MusicBrainzModeKey, kind: "enum", parse: parseMusicBrainzMode,
		serialize: func(value string) string { return value }, defaultVal: &defaultPublic, mutable: true,
	}
	musicBrainzURLSetting      = textDefinition(MusicBrainzBaseURLKey, "url", parseMusicBrainzURL, true)
	musicBrainzIdentitySetting = textDefinition(MusicBrainzConfigIdentityKey, "uuid", parseIdentity, true)
	musicBrainzVerifiedSetting = timeDefinition(MusicBrainzVerifiedAtKey, true)
	lrclibSetting              = settingDefinition[bool]{
		name: LRCLIBEnabledKey, kind: "bool", parse: strconv.ParseBool,
		serialize: strconv.FormatBool, defaultVal: &defaultTrue, mutable: true,
	}
	sha256Setting = settingDefinition[bool]{
		name: SHA256EnabledKey, kind: "bool", parse: strconv.ParseBool,
		serialize: strconv.FormatBool, defaultVal: &defaultTrue, mutable: true,
	}
	sourceFileConcurrencySetting = settingDefinition[int]{
		name: SourceFileConcurrencyKey, kind: "int",
		parse: strconv.Atoi, serialize: strconv.Itoa,
		validate: func(value int) error {
			if value < 1 {
				return fmt.Errorf("source file concurrency must be positive")
			}
			return nil
		},
		defaultVal: &defaultFileConcurrency, mutable: true,
	}
	logSetting = settingDefinition[string]{
		name: LogLevelKey, kind: "enum", parse: parseLevel,
		serialize: func(value string) string { return value }, defaultVal: &defaultInfo, mutable: true,
	}
	activeFFmpegSetting = settingDefinition[uuid.UUID]{
		name: ActiveFFmpegInstallationKey, kind: "uuid", parse: uuid.Parse,
		serialize: uuid.UUID.String, mutable: true,
		validate: func(id uuid.UUID) error {
			if id == uuid.Nil {
				return fmt.Errorf("active installation ID must not be nil")
			}
			return nil
		},
	}
	activeFPCalcSetting = settingDefinition[uuid.UUID]{
		name: ActiveFPCalcInstallationKey, kind: "uuid", parse: uuid.Parse,
		serialize: uuid.UUID.String, mutable: true,
		validate: func(id uuid.UUID) error {
			if id == uuid.Nil {
				return fmt.Errorf("active installation ID must not be nil")
			}
			return nil
		},
	}
	setupCompletedSetting = timeDefinition(SetupCompletedAtKey, false)
)

var registeredSettings = []registeredSetting{
	platformOSSetting, platformArchSetting, toolsRootSetting, outputRootSetting,
	outputCaseSetting, outputUnicodeSetting, publicationSetting,
	musicBrainzModeSetting, musicBrainzURLSetting, musicBrainzIdentitySetting,
	musicBrainzVerifiedSetting, lrclibSetting, sha256Setting, logSetting,
	activeFFmpegSetting, activeFPCalcSetting, sourceFileConcurrencySetting, setupCompletedSetting,
}
