package service

import (
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
)

func TestValidateAudioUpgradeTopologyPreservesExactArtworkInventory(t *testing.T) {
	inventory := func(mimes ...string) []metadata.ArtworkInventoryEntry {
		entries := make([]metadata.ArtworkInventoryEntry, len(mimes))
		for index, mime := range mimes {
			entries[index] = metadata.ArtworkInventoryEntry{Index: index, MIME: mime}
		}
		return entries
	}
	tests := []struct {
		name      string
		raw       string
		inventory []metadata.ArtworkInventoryEntry
		wantErr   bool
	}{
		{
			name:      "FLAC audio with two attached pictures",
			raw:       `{"streams":[{"codec_type":"audio"},{"codec_type":"video","codec_name":"mjpeg","disposition":{"attached_pic":1}},{"codec_type":"video","codec_name":"png","disposition":{"attached_pic":1}}]}`,
			inventory: inventory("image/jpeg", "image/png"),
		},
		{
			name:      "Matroska image attachments",
			raw:       `{"streams":[{"codec_type":"audio"},{"codec_type":"attachment","tags":{"filename":"cover.jpg","mimetype":"image/jpeg"}},{"codec_type":"attachment","tags":{"filename":"back.png","mimetype":"image/png"}}]}`,
			inventory: inventory("image/jpeg", "image/png"),
		},
		{
			name:      "Matroska duplicate MIME attachments",
			raw:       `{"streams":[{"codec_type":"audio"},{"codec_type":"attachment","tags":{"mimetype":"image/jpeg"}},{"codec_type":"attachment","tags":{"mimetype":"image/jpeg"}}]}`,
			inventory: inventory("image/jpeg", "image/jpeg"),
		},
		{
			name: "audio without artwork",
			raw:  `{"streams":[{"codec_type":"audio"}]}`,
		},
		{
			name:      "unknown attached picture format without recorded MIME",
			raw:       `{"streams":[{"codec_type":"audio"},{"codec_type":"video","codec_name":"webp","disposition":{"attached_pic":1}}]}`,
			inventory: inventory(""),
		},
		{
			name:      "plain video stream",
			raw:       `{"streams":[{"codec_type":"audio"},{"codec_type":"video","codec_name":"h264"}]}`,
			inventory: inventory(""),
			wantErr:   true,
		},
		{
			name:      "subtitle stream",
			raw:       `{"streams":[{"codec_type":"audio"},{"codec_type":"subtitle","codec_name":"srt"}]}`,
			inventory: inventory("image/jpeg"),
			wantErr:   true,
		},
		{
			name:      "data stream",
			raw:       `{"streams":[{"codec_type":"audio"},{"codec_type":"data","codec_name":"bin_data"}]}`,
			inventory: inventory("image/jpeg"),
			wantErr:   true,
		},
		{
			name:      "non-image attachment",
			raw:       `{"streams":[{"codec_type":"audio"},{"codec_type":"attachment","tags":{"filename":"booklet.pdf","mimetype":"application/pdf"}}]}`,
			inventory: inventory("application/pdf"),
			wantErr:   true,
		},
		{
			name:      "attachment MIME absent",
			raw:       `{"streams":[{"codec_type":"audio"},{"codec_type":"attachment","tags":{"filename":"cover.jpg"}}]}`,
			inventory: inventory("image/jpeg"),
			wantErr:   true,
		},
		{
			name:      "attachment MIME differs from inventory",
			raw:       `{"streams":[{"codec_type":"audio"},{"codec_type":"attachment","tags":{"mimetype":"image/png"}}]}`,
			inventory: inventory("image/jpeg"),
			wantErr:   true,
		},
		{
			name:      "fewer images than inventory",
			raw:       `{"streams":[{"codec_type":"audio"},{"codec_type":"video","disposition":{"attached_pic":1}}]}`,
			inventory: inventory("image/jpeg", "image/png"),
			wantErr:   true,
		},
		{
			name:      "more images than inventory",
			raw:       `{"streams":[{"codec_type":"audio"},{"codec_type":"video","disposition":{"attached_pic":1}},{"codec_type":"video","disposition":{"attached_pic":1}}]}`,
			inventory: inventory("image/jpeg"),
			wantErr:   true,
		},
		{
			name:      "chapters",
			raw:       `{"streams":[{"codec_type":"audio"}],"chapters":[{}]}`,
			inventory: inventory(),
			wantErr:   true,
		},
		{
			name:    "two audio streams",
			raw:     `{"streams":[{"codec_type":"audio"},{"codec_type":"audio"}]}`,
			wantErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateAudioUpgradeTopology([]byte(test.raw), test.inventory)
			if test.wantErr && err == nil {
				t.Fatal("audio-upgrade topology accepted an unauthorized stream layout")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("audio-upgrade topology rejected a preserved layout: %v", err)
			}
		})
	}
}
