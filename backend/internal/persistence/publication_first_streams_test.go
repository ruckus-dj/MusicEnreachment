package persistence

import (
	"encoding/json"
	"testing"
)

func TestPublicationAudioStreamsExposeVerifiedStreamMetadata(t *testing.T) {
	raw := json.RawMessage(`{"streams":[{"index":0,"codec_type":"video"},{"index":2,"codec_type":"audio","tags":{"title":"Main","language":"eng"}},{"index":5,"codec_type":"audio","tags":{"title":"Commentary","language":"fra"}}]}`)
	streams, err := publicationAudioStreams(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(streams) != 2 || streams[0] != (publicationAudioStream{Index: 2, Title: "Main", Language: "eng"}) || streams[1] != (publicationAudioStream{Index: 5, Title: "Commentary", Language: "fra"}) {
		t.Fatalf("audio streams = %#v", streams)
	}
	one, err := publicationAudioStreams(json.RawMessage(`{"streams":[{"index":2,"codec_type":"audio"}]}`))
	if err != nil || len(one) != 1 || one[0].Index != 2 {
		t.Fatalf("single stream parse = %#v, %v", one, err)
	}
}
