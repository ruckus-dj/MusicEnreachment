package persistence

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

func publicationInputsFixture(t *testing.T, intent string) PublicationInputs {
	t.Helper()
	root := filepath.Join(t.TempDir(), "incoming")
	lossless := true
	channels := 2
	bitDepth := 16
	installation := uuid.New()
	input := PublicationInputs{
		Version: PublicationInputsVersion, IntentKind: intent,
		Format:      publication.AudioFormatMKA,
		DesiredTags: map[string][]string{"title": {"Track"}, "comment": {}},
		Tools: []SourceAnalysisToolSelection{
			{PackageKind: "ffmpeg", InstallationID: installation, RelativePath: "bin/ffmpeg", Executable: "ffmpeg", Version: "7.1", VersionBanner: "ffmpeg version 7.1"},
			{PackageKind: "ffmpeg", InstallationID: installation, RelativePath: "bin/ffprobe", Executable: "ffprobe", Version: "7.1", VersionBanner: "ffprobe version 7.1"},
		},
	}
	facts := PersistedAudioFacts{StreamIndex: 1, Codec: "pcm_s16le", Lossless: &lossless, Channels: &channels, BitDepth: &bitDepth, Container: publication.AudioContainerFLAC, SupportedSourceContainer: true}
	if intent == "audio" {
		input.Audio = &PublicationAudioInput{
			RootID: uuid.New(), LocationID: uuid.New(), MediaVariantID: uuid.New(),
			ConfiguredRootPath: root, InventoryRootPath: root,
			RelativePath: "disc/track.flac", SizeBytes: 0,
			MTime:                 time.Date(2026, 10, 1, 12, 0, 0, 123456000, time.UTC),
			AnalysisPolicyVersion: 1, FFProbeVersion: "7.1", AudioFacts: facts,
		}
	} else {
		size := int64(4096)
		input.Rewrite = &PublicationRewriteInput{CurrentGeneration: uuid.New(), RelativePath: "Artist/Album/track.mka", Ownership: PublicationFileIdentity{SizeBytes: &size}, AudioFacts: facts}
	}
	return input
}

func TestPublicationInputsRoundTripAudioAndRewrite(t *testing.T) {
	for _, intent := range []string{"audio", "rewrite"} {
		t.Run(intent, func(t *testing.T) {
			original := publicationInputsFixture(t, intent)
			encoded, err := json.Marshal(original)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := DecodePublicationInputs(encoded)
			if err != nil {
				t.Fatal(err)
			}
			if decoded.IntentKind != original.IntentKind || decoded.Format != original.Format || len(decoded.Tools) != 2 {
				t.Fatalf("decoded contract differs: %#v", decoded)
			}
			if decoded.DesiredTags["comment"] == nil || len(decoded.DesiredTags["comment"]) != 0 {
				t.Fatalf("explicit empty tag list was not preserved: %#v", decoded.DesiredTags)
			}
			if intent == "audio" && decoded.Audio == nil || intent == "rewrite" && decoded.Rewrite == nil {
				t.Fatalf("intent payload missing: %#v", decoded)
			}
			if intent == "rewrite" && decoded.Audio != nil {
				t.Fatalf("rewrite retained source identity: %#v", decoded.Audio)
			}
			var facts PersistedAudioFacts
			if intent == "rewrite" {
				facts = decoded.Rewrite.AudioFacts
			} else {
				facts = decoded.Audio.AudioFacts
			}
			if facts.AudioSourceFacts().StreamIndex != 1 {
				t.Fatal("audio facts conversion lost selected stream index")
			}
		})
	}
}

func TestPublicationInputsCloneDetachesTagMapAndPreservesEmptyList(t *testing.T) {
	input := publicationInputsFixture(t, "audio")
	clone := input.Clone()
	clone.DesiredTags["title"][0] = "Changed"
	clone.DesiredTags["new"] = []string{}
	if input.DesiredTags["title"][0] != "Track" {
		t.Fatal("clone shares tag value storage")
	}
	if input.DesiredTags["new"] != nil {
		t.Fatal("clone shares the tag map")
	}
	if clone.DesiredTags["new"] == nil {
		t.Fatal("explicit empty tag list became nil")
	}
	input.DesiredTags["invalid"] = nil
	invalidClone := input.Clone()
	if invalidClone.DesiredTags["invalid"] != nil || invalidClone.Validate() == nil {
		t.Fatal("clone changed invalid nil tags into valid explicit-empty tags")
	}
}

func TestDecodePublicationInputsRejectsMalformedContracts(t *testing.T) {
	valid, err := json.Marshal(publicationInputsFixture(t, "audio"))
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(valid, &object); err != nil {
		t.Fatal(err)
	}
	remove := func(key string) []byte {
		copy := make(map[string]json.RawMessage, len(object))
		for name, value := range object {
			copy[name] = value
		}
		delete(copy, key)
		encoded, _ := json.Marshal(copy)
		return encoded
	}
	for _, test := range []struct {
		name string
		raw  []byte
	}{
		{"missing tags", remove("desired_tags")},
		{"missing tools", remove("tools")},
		{"null", []byte("null")},
		{"trailing document", append(append([]byte(nil), valid...), []byte(` {}`)...)},
		{"unknown field", append(append([]byte(nil), valid[:len(valid)-1]...), []byte(`,"future":true}`)...)},
		{"missing source selector", mutatePublicationJSON(t, object, "audio", "location_id")},
		{"windows drive path", mutatePublicationJSONValue(t, object, "audio", "relative_path", `"C:\\music\\track.flac"`)},
		{"path traversal", mutatePublicationJSONValue(t, object, "audio", "relative_path", `"../track.flac"`)},
		{"intent mismatch", mutatePublicationJSONValue(t, object, "", "intent_kind", `"rewrite"`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := DecodePublicationInputs(test.raw); err == nil {
				t.Fatalf("accepted malformed inputs: %s", test.raw)
			}
		})
	}
}

func TestPublicationInputSelectorsAndToolSelectionsAreExact(t *testing.T) {
	input := publicationInputsFixture(t, "audio")
	input.Audio.RootID = uuid.Nil
	if err := input.Validate(); err == nil {
		t.Fatal("accepted missing root selector")
	}

	input = publicationInputsFixture(t, "audio")
	input.Tools[1] = input.Tools[0]
	if err := input.Validate(); err == nil {
		t.Fatal("accepted duplicate executable selection")
	}

	input = publicationInputsFixture(t, "audio")
	input.Tools = input.Tools[:1]
	if err := input.Validate(); err == nil {
		t.Fatal("accepted missing ffprobe selection")
	}

	input = publicationInputsFixture(t, "rewrite")
	input.Rewrite.Ownership.MTime = new(int64)
	if err := input.Validate(); err != nil {
		t.Fatalf("optional factual ownership fields must be allowed: %v", err)
	}
	input.Rewrite.Ownership = PublicationFileIdentity{}
	if err := input.Validate(); err == nil {
		t.Fatal("accepted rewrite without ownership identity")
	}
}

func mutatePublicationJSON(t *testing.T, object map[string]json.RawMessage, parent, key string) []byte {
	t.Helper()
	var nested map[string]json.RawMessage
	if err := json.Unmarshal(object[parent], &nested); err != nil {
		t.Fatal(err)
	}
	delete(nested, key)
	objectCopy := cloneRawObject(object)
	objectCopy[parent], _ = json.Marshal(nested)
	encoded, _ := json.Marshal(objectCopy)
	return encoded
}

func mutatePublicationJSONValue(t *testing.T, object map[string]json.RawMessage, parent, key string, value ...string) []byte {
	t.Helper()
	objectCopy := cloneRawObject(object)
	if parent == "" {
		objectCopy[key] = json.RawMessage(value[0])
	} else {
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(object[parent], &nested); err != nil {
			t.Fatal(err)
		}
		nested[key] = json.RawMessage(value[0])
		objectCopy[parent], _ = json.Marshal(nested)
	}
	encoded, _ := json.Marshal(objectCopy)
	return encoded
}

func cloneRawObject(object map[string]json.RawMessage) map[string]json.RawMessage {
	copy := make(map[string]json.RawMessage, len(object))
	for key, value := range object {
		copy[key] = append(json.RawMessage(nil), value...)
	}
	return copy
}
