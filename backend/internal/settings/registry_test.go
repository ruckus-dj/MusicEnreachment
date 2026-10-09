package settings

import (
	"testing"

	"github.com/google/uuid"
)

func TestRegistryDefinesEveryTypedSetting(t *testing.T) {
	expected := map[string]struct {
		kind       string
		hasDefault bool
		mutable    bool
	}{
		PlatformGOOSKey:                  {"enum", false, false},
		PlatformGOARCHKey:                {"enum", false, false},
		ToolsDirectoryKey:                {"path", false, true},
		OutputDirectoryKey:               {"path", false, true},
		OutputCaseSensitiveKey:           {"bool", false, true},
		OutputUnicodeNormalizationKey:    {"enum", false, true},
		PublicationFormatKey:             {"enum", false, true},
		MusicBrainzModeKey:               {"enum", true, true},
		MusicBrainzBaseURLKey:            {"url", false, true},
		MusicBrainzConfigIdentityKey:     {"uuid", false, true},
		MusicBrainzVerifiedAtKey:         {"timestamp", false, true},
		LRCLIBEnabledKey:                 {"bool", true, true},
		SHA256EnabledKey:                 {"bool", true, true},
		LogLevelKey:                      {"enum", true, true},
		ActiveFFmpegInstallationKey:      {"uuid", false, true},
		ActiveFPCalcInstallationKey:      {"uuid", false, true},
		SourceFileConcurrencyKey:         {"int", true, true},
		MusicBrainzSelfHostedThrottleKey: {"bool", false, true},
		MusicBrainzSelfHostedDelayKey:    {"float", true, true},
		SetupCompletedAtKey:              {"timestamp", false, false},
	}
	for _, definition := range registeredSettings {
		meta := definition.metadata()
		want, exists := expected[meta.name]
		if !exists {
			t.Errorf("unexpected setting %q", meta.name)
			continue
		}
		if meta.kind != want.kind || meta.hasDefault != want.hasDefault || meta.mutable != want.mutable || meta.sensitive {
			t.Errorf("metadata for %s: %+v, want %+v and not sensitive", meta.name, meta, want)
		}
		delete(expected, meta.name)
	}
	if len(expected) != 0 {
		t.Fatalf("missing setting definitions: %v", expected)
	}
}

func TestRegistryRejectsInvalidTypedValues(t *testing.T) {
	for _, value := range []string{"broken", "", "SOURCE"} {
		if _, err := serializeSetting(publicationSetting, value); err == nil {
			t.Errorf("publication format %q was accepted", value)
		}
	}
	if _, err := outputCaseSetting.parse("not-a-bool"); err == nil {
		t.Fatal("invalid stored filesystem boolean was accepted")
	}
	if _, err := activeFFmpegSetting.parse("not-a-uuid"); err == nil {
		t.Fatal("invalid stored active installation ID was accepted")
	}
	id := uuid.New()
	value, err := serializeSetting(activeFFmpegSetting, id)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := activeFFmpegSetting.parse(value)
	if err != nil || decoded != id {
		t.Fatalf("active ID round trip = %s, %v; want %s", decoded, err, id)
	}
}
