package persistence

import (
	"encoding/json"
	"testing"
)

func TestDecodeLibraryPublicationListAudioProjectsValidContract(t *testing.T) {
	audio, err := decodeLibraryPublicationListAudio(json.RawMessage(`{"version":1,"facts":{"stream_index":1,"codec":"flac","lossless":true,"channels":2,"bitrate":900000,"bit_depth":16,"sample_rate":44100,"container":"flac","supported_source_container":true}}`))
	if err != nil {
		t.Fatalf("decode valid current audio: %v", err)
	}
	if audio.StreamIndex != 1 || audio.Codec != "flac" || audio.Lossless == nil || !*audio.Lossless ||
		audio.Channels == nil || *audio.Channels != 2 || audio.Bitrate == nil || *audio.Bitrate != 900000 ||
		audio.BitDepth == nil || *audio.BitDepth != 16 || audio.SampleRate == nil || *audio.SampleRate != 44100 ||
		audio.Container != "flac" || !audio.SupportedSourceContainer || audio.ALACCapabilitiesVerified {
		t.Fatalf("unexpected projected audio %+v", audio)
	}
}

func TestDecodeLibraryPublicationListAudioRejectsIncompleteContract(t *testing.T) {
	tests := map[string]json.RawMessage{
		"nil":                 nil,
		"empty object":        json.RawMessage(`{}`),
		"null":                json.RawMessage(`null`),
		"null facts":          json.RawMessage(`{"version":1,"facts":null}`),
		"absent facts":        json.RawMessage(`{"version":1}`),
		"empty facts":         json.RawMessage(`{"version":1,"facts":{}}`),
		"missing lossless":    json.RawMessage(`{"version":1,"facts":{"stream_index":0,"codec":"flac","container":"flac"}}`),
		"missing codec":       json.RawMessage(`{"version":1,"facts":{"stream_index":0,"lossless":true,"container":"flac"}}`),
		"invalid channels":    json.RawMessage(`{"version":1,"facts":{"stream_index":0,"codec":"flac","lossless":true,"channels":0,"container":"flac"}}`),
		"unsupported version": json.RawMessage(`{"version":2,"facts":{"stream_index":0,"codec":"flac","lossless":true,"container":"flac"}}`),
		"unknown field":       json.RawMessage(`{"version":1,"facts":{"stream_index":0,"codec":"flac","lossless":true,"container":"flac"},"extra":true}`),
		"trailing data":       json.RawMessage(`{"version":1,"facts":{"stream_index":0,"codec":"flac","lossless":true,"container":"flac"}}{}`),
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeLibraryPublicationListAudio(raw); err == nil {
				t.Fatalf("expected incomplete current audio %s to be rejected", name)
			}
		})
	}
}
