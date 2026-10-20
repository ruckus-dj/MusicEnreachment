package persistence

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
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
		Version: PublicationInputsVersion, QualityPolicyVersion: publication.QualityPolicyVersion, IntentKind: intent,
		Format:      publication.AudioFormatMKA,
		DesiredTags: map[string][]string{"title": {"Track"}, "comment": {}},
		Tools: []SourceAnalysisToolSelection{
			{PackageKind: "ffmpeg", InstallationID: installation, RelativePath: "bin/ffmpeg", Executable: "ffmpeg", Version: "7.1", VersionBanner: "ffmpeg version 7.1"},
			{PackageKind: "ffmpeg", InstallationID: installation, RelativePath: "bin/ffprobe", Executable: "ffprobe", Version: "7.1", VersionBanner: "ffprobe version 7.1"},
		},
	}
	facts := PersistedAudioFacts{StreamIndex: 1, Codec: "flac", Lossless: &lossless, Channels: &channels, BitDepth: &bitDepth, Container: publication.AudioContainerFLAC, SupportedSourceContainer: true}
	if intent == "audio" {
		input.InitialLyrics = &PublicationInitialLyricsInput{ExpectedMetadataRevision: 0}
		input.Audio = &PublicationAudioInput{
			WorkID: uuid.New(), RootID: uuid.New(), LocationID: uuid.New(), MediaVariantID: uuid.New(), ProbeResultID: uuid.New(),
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

func TestPublicationInitialLyricsAuthorityContract(t *testing.T) {
	for _, test := range []struct {
		name  string
		field *librarymetadata.FieldValue
	}{
		{name: "missing"},
		{name: "absent", field: ptrLyricsValue(librarymetadata.FieldValue{State: librarymetadata.StateAbsent, Authority: librarymetadata.AuthorityAutomatic, Provenance: librarymetadata.Provenance{Kind: librarymetadata.ProvenanceProvider}})},
		{name: "automatic provider value", field: ptrLyricsValue(librarymetadata.FieldValue{State: librarymetadata.StateValue, Value: "[00:01.00]line", Authority: librarymetadata.AuthorityAutomatic, Provenance: librarymetadata.Provenance{Kind: librarymetadata.ProvenanceProvider, Reference: "lrclib"}})},
		{name: "manual value", field: ptrLyricsValue(librarymetadata.FieldValue{State: librarymetadata.StateValue, Value: "[00:01.00]manual", Authority: librarymetadata.AuthorityManual, Provenance: librarymetadata.Provenance{Kind: librarymetadata.ProvenanceProvider, Reference: "lrclib"}})},
		{name: "manual empty", field: ptrLyricsValue(librarymetadata.FieldValue{State: librarymetadata.StateExplicitEmpty, Authority: librarymetadata.AuthorityManual, Provenance: librarymetadata.Provenance{Kind: librarymetadata.ProvenanceManual}})},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := publicationInputsFixture(t, "audio")
			input.InitialLyrics.Desired = test.field
			if err := input.Validate(); err != nil {
				t.Fatalf("valid authority snapshot rejected: %v", err)
			}
		})
	}
	for _, field := range []*librarymetadata.FieldValue{
		{State: "bad", Authority: librarymetadata.AuthorityManual, Provenance: librarymetadata.Provenance{Kind: librarymetadata.ProvenanceManual}},
		{State: librarymetadata.StateExplicitEmpty, Value: "fabricated", Authority: librarymetadata.AuthorityManual, Provenance: librarymetadata.Provenance{Kind: librarymetadata.ProvenanceManual}},
	} {
		input := publicationInputsFixture(t, "audio")
		input.InitialLyrics.Desired = field
		if err := input.Validate(); err == nil {
			t.Fatal("invalid initial lyrics authority snapshot accepted")
		}
	}
	input := publicationInputsFixture(t, "audio")
	input.InitialLyrics = nil
	if err := input.Validate(); err == nil {
		t.Fatal("audio contract without initial lyrics evidence accepted")
	}
}

func ptrLyricsValue(value librarymetadata.FieldValue) *librarymetadata.FieldValue { return &value }

func TestPublicationInputsCloneDetachesInitialLyricsValue(t *testing.T) {
	input := publicationInputsFixture(t, "audio")
	raw := json.RawMessage(`{"nested":["original"]}`)
	input.InitialLyrics.Desired = &librarymetadata.FieldValue{State: librarymetadata.StateValue, Value: map[string]any{"raw": raw, "items": []any{"original"}}, Authority: librarymetadata.AuthorityManual, Provenance: librarymetadata.Provenance{Kind: librarymetadata.ProvenanceManual}}
	clone := input.Clone()
	cloneValue := clone.InitialLyrics.Desired.Value.(map[string]any)
	cloneValue["items"].([]any)[0] = "changed"
	cloneValue["raw"].(json.RawMessage)[2] = 'X'
	original := input.InitialLyrics.Desired.Value.(map[string]any)
	if original["items"].([]any)[0] != "original" || string(original["raw"].(json.RawMessage)) != string(raw) {
		t.Fatal("initial lyrics clone shares mutable metadata value storage")
	}
}

func TestPublicationInputsManualLyricsRequiresExactNonEmptySyncedLyrics(t *testing.T) {
	input := publicationInputsFixture(t, "rewrite")
	input.ManualLyrics = &PublicationManualLyricsInput{
		State: "value", Value: "[ar:日本語]\r\n[00:01.00]Привет\r\n",
		ExpectedMetadataRevision: 4, ConfirmedCurrentGeneration: input.Rewrite.CurrentGeneration,
	}

	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePublicationInputs(raw)
	if err != nil {
		t.Fatalf("decode valid manual-lyrics inputs: %v", err)
	}
	if decoded.ManualLyrics.Value != input.ManualLyrics.Value {
		t.Fatalf("manual lyrics changed during decode: got %q, want %q", decoded.ManualLyrics.Value, input.ManualLyrics.Value)
	}
	clone := decoded.Clone()
	if clone.ManualLyrics.Value != input.ManualLyrics.Value {
		t.Fatalf("manual lyrics changed during clone: got %q, want %q", clone.ManualLyrics.Value, input.ManualLyrics.Value)
	}

	for _, value := range []string{"", " \r\n\t", "plain lyrics", "[00:60.00]invalid", "[ar:Artist]\nplain lyrics"} {
		t.Run(fmt.Sprintf("reject %q", value), func(t *testing.T) {
			invalid := input.Clone()
			invalid.ManualLyrics.Value = value
			if err := invalid.Validate(); err == nil {
				t.Fatalf("Validate accepted manual lyrics %q", value)
			}
		})
	}
}

func TestPublicationInputsExecutionSupport(t *testing.T) {
	input := publicationInputsFixture(t, "audio")
	if !input.SupportsExecution() {
		t.Error("canonical audio inputs should support execution")
	}
	manual := publicationInputsFixture(t, "rewrite")
	manual.ManualLyrics = &PublicationManualLyricsInput{State: "explicit_empty", ConfirmedCurrentGeneration: manual.Rewrite.CurrentGeneration}
	if manual.SupportsExecution() {
		t.Error("manual-lyrics inputs must use private execution capability")
	}
}

func TestPublicationInputsCanonicalManualLyricsReceipt(t *testing.T) {
	input := publicationInputsFixture(t, "rewrite")
	input.ManualLyrics = &PublicationManualLyricsInput{
		State: "explicit_empty", ExpectedMetadataRevision: 0,
		ConfirmedCurrentGeneration: input.Rewrite.CurrentGeneration,
	}
	if err := input.Validate(); err != nil {
		t.Fatalf("explicit-empty manual receipt rejected: %v", err)
	}

	input.DesiredTags["LYRICS"] = []string{"mandatory lyrics"}
	if err := input.Validate(); err == nil {
		t.Fatal("manual lyrics were allowed in mandatory desired tags")
	}

	ordinary := publicationInputsFixture(t, "audio")
	ordinary.DesiredTags["LYRICS"] = []string{"ordinary lyrics tag"}
	if err := ordinary.Validate(); err != nil {
		t.Fatalf("ordinary lyrics desired tag rejected without manual receipt: %v", err)
	}
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

func TestPublicationInputsRequireCurrentMarkerAndQualityPolicy(t *testing.T) {
	for _, marker := range []int{0, 2, 3, 4} {
		input := publicationInputsFixture(t, "audio")
		input.Version = marker
		if err := input.Validate(); err == nil {
			t.Errorf("accepted unsupported marker %d", marker)
		}
	}
	for _, policy := range []int{0, publication.QualityPolicyVersion - 1, publication.QualityPolicyVersion + 1} {
		input := publicationInputsFixture(t, "audio")
		input.QualityPolicyVersion = policy
		if err := input.Validate(); err == nil {
			t.Errorf("accepted quality policy %d", policy)
		}
	}
}

func TestPublicationInputsCloneDetachesTagMapAndPreservesEmptyList(t *testing.T) {
	input := publicationInputsFixture(t, "audio")
	rate := 44100
	input.Audio.AudioFacts.SampleRate = &rate
	clone := input.Clone()
	*clone.Audio.AudioFacts.SampleRate = 48000
	if *input.Audio.AudioFacts.SampleRate != 44100 {
		t.Fatal("clone shares sample rate storage")
	}
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

func TestPersistedAudioFactsSampleRateMustBePositiveWhenKnown(t *testing.T) {
	for _, rate := range []int{0, -1} {
		facts := publicationInputsFixture(t, "audio").Audio.AudioFacts
		facts.SampleRate = &rate
		if err := facts.Validate(); err == nil {
			t.Errorf("accepted sample rate %d", rate)
		}
	}
	facts := publicationInputsFixture(t, "audio").Audio.AudioFacts
	facts.SampleRate = nil
	if err := facts.Validate(); err != nil {
		t.Fatalf("unknown optional sample rate rejected: %v", err)
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

func TestPublicationProbeFactsAreDerivedFromSelectedJSON(t *testing.T) {
	policy, version := 3, "ffprobe 7.1"
	inspected, applied := time.Now().UTC(), uuid.New()
	probe := SourceMediaVariant{SizeBytes: 42, AnalysisPolicyVersion: &policy, FFProbeVersion: &version, InspectedAt: &inspected, AppliedOperationID: &applied,
		AudioStreamCount: intPointer(1), FFProbeJSON: json.RawMessage(`{"format":{"format_name":"mov,mp4,m4a,3gp,3g2,mj2"},"streams":[{"index":0,"codec_type":"video","codec_name":"h264"},{"index":"2","codec_type":"audio","codec_name":"alac","channels":"2","bit_rate":"900000","bits_per_raw_sample":"24"}]}`)}
	facts, err := publicationAudioFactsFromProbe(probe, 2)
	if err != nil {
		t.Fatal(err)
	}
	if facts.StreamIndex != 2 || facts.Codec != "alac" || !*facts.Lossless || facts.Container != publication.AudioContainerM4A || !facts.SupportedSourceContainer || facts.Bitrate == nil || *facts.Bitrate != 900000 || facts.BitDepth == nil || *facts.BitDepth != 24 {
		t.Fatalf("unexpected derived probe facts: %#v", facts)
	}
	for _, raw := range []string{
		`{"format":{"format_name":"flac"},"streams":[{"index":0,"codec_type":"audio","codec_name":"wavpack"}]}`,
		`{"format":{"format_name":"flac"},"streams":[{"index":0.5,"codec_type":"audio","codec_name":"flac"}]}`,
		`{"format":{"format_name":"flac"},"streams":[{"index":0,"codec_type":"audio","codec_name":"flac"},{"index":0,"codec_type":"audio","codec_name":"flac"}]}`,
		`{"format":{"format_name":"flac"},"streams":[{"index":0,"codec_type":"audio","codec_name":"flac","bit_rate":"-1"}]}`,
	} {
		probe.FFProbeJSON = json.RawMessage(raw)
		if _, err := publicationAudioFactsFromProbe(probe, 0); err == nil {
			t.Fatalf("accepted untrusted probe JSON: %s", raw)
		}
	}
}

func TestPublicationCurrentContractsRequireVersionAndExactEvidence(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`{}`), json.RawMessage(`{"version":2,"facts":{}}`), json.RawMessage(`{"version":1,"facts":{},"future":true}`), json.RawMessage(`{"version":1,"facts":{}} {}`)} {
		var current PublicationCurrentAudio
		if err := decodePublicationCurrentContract(raw, &current); err == nil && (current.Version != 1 || current.Facts.Lossless == nil) {
			t.Fatalf("accepted incomplete current audio contract: %s", raw)
		} else if err == nil {
			t.Fatalf("accepted current audio lacking facts: %s", raw)
		}
	}
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"version":2,"identity":{"size_bytes":1}}`),
		json.RawMessage(`{"version":1,"identity":{}}`),
		json.RawMessage(`{"version":1,"identity":{"size_bytes":-1}}`),
	} {
		var current PublicationCurrentOwnership
		if err := decodePublicationCurrentContract(raw, &current); err == nil {
			t.Fatalf("accepted incomplete current ownership contract: %s", raw)
		}
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
