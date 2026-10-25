package service

import (
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

func TestValidateFirstAudioCoverTopology(t *testing.T) {
	tests := []struct {
		name      string
		container publication.AudioContainer
		cover     persistence.PublicationCoverOutcome
		raw       string
		wantErr   bool
	}{
		{name: "FLAC one attached picture", container: publication.AudioContainerFLAC, cover: persistence.PublicationCoverOutcome{State: persistence.PublicationAssetEmbedded, MIME: "image/png"}, raw: `{"streams":[{"codec_type":"audio"},{"codec_type":"video","codec_name":"png","disposition":{"attached_pic":1}}],"chapters":[]}`},
		{name: "MP3 one attached picture", container: publication.AudioContainerMP3, cover: persistence.PublicationCoverOutcome{State: persistence.PublicationAssetEmbedded, MIME: "image/jpeg"}, raw: `{"streams":[{"codec_type":"audio"},{"codec_type":"video","codec_name":"mjpeg","disposition":{"attached_pic":1}}],"chapters":[]}`},
		{name: "FLAC extra audio", container: publication.AudioContainerFLAC, cover: persistence.PublicationCoverOutcome{State: persistence.PublicationAssetEmbedded, MIME: "image/png"}, raw: `{"streams":[{"codec_type":"audio"},{"codec_type":"audio"},{"codec_type":"video","codec_name":"png","disposition":{"attached_pic":1}}]}`, wantErr: true},
		{name: "FLAC non-cover video", container: publication.AudioContainerFLAC, cover: persistence.PublicationCoverOutcome{State: persistence.PublicationAssetEmbedded, MIME: "image/png"}, raw: `{"streams":[{"codec_type":"audio"},{"codec_type":"video","codec_name":"png","disposition":{"attached_pic":0}}]}`, wantErr: true},
		{name: "FLAC extra attachment", container: publication.AudioContainerFLAC, cover: persistence.PublicationCoverOutcome{State: persistence.PublicationAssetEmbedded, MIME: "image/png"}, raw: `{"streams":[{"codec_type":"audio"},{"codec_type":"video","codec_name":"png","disposition":{"attached_pic":1}},{"codec_type":"attachment"}]}`, wantErr: true},
		{name: "Matroska typed image attachment", container: publication.AudioContainerMKA, cover: persistence.PublicationCoverOutcome{State: persistence.PublicationAssetEmbedded, MIME: "image/png"}, raw: `{"streams":[{"codec_type":"audio"},{"codec_type":"attachment","tags":{"filename":"cover.png","mimetype":"image/png"}}]}`},
		{name: "Matroska attached picture video", container: publication.AudioContainerMKA, cover: persistence.PublicationCoverOutcome{State: persistence.PublicationAssetEmbedded, MIME: "image/png"}, raw: `{"streams":[{"index":0,"codec_name":"flac","codec_type":"audio"},{"index":1,"codec_name":"png","codec_type":"video","width":128,"height":128,"disposition":{"attached_pic":1},"tags":{"filename":"cover.png","mimetype":"image/png"}}],"chapters":[]}`},
		{name: "Matroska attached picture wrong codec", container: publication.AudioContainerMKA, cover: persistence.PublicationCoverOutcome{State: persistence.PublicationAssetEmbedded, MIME: "image/png"}, raw: `{"streams":[{"codec_type":"audio"},{"codec_type":"video","codec_name":"mjpeg","disposition":{"attached_pic":1},"tags":{"filename":"cover.png","mimetype":"image/png"}}]}`, wantErr: true},
		{name: "Matroska attached picture wrong MIME", container: publication.AudioContainerMKA, cover: persistence.PublicationCoverOutcome{State: persistence.PublicationAssetEmbedded, MIME: "image/png"}, raw: `{"streams":[{"codec_type":"audio"},{"codec_type":"video","codec_name":"png","disposition":{"attached_pic":1},"tags":{"filename":"cover.png","mimetype":"image/jpeg"}}]}`, wantErr: true},
		{name: "Matroska attached picture wrong filename", container: publication.AudioContainerMKA, cover: persistence.PublicationCoverOutcome{State: persistence.PublicationAssetEmbedded, MIME: "image/png"}, raw: `{"streams":[{"codec_type":"audio"},{"codec_type":"video","codec_name":"png","disposition":{"attached_pic":1},"tags":{"filename":"cover.jpg","mimetype":"image/png"}}]}`, wantErr: true},
		{name: "Matroska attached picture without attached disposition", container: publication.AudioContainerMKA, cover: persistence.PublicationCoverOutcome{State: persistence.PublicationAssetEmbedded, MIME: "image/png"}, raw: `{"streams":[{"codec_type":"audio"},{"codec_type":"video","codec_name":"png","disposition":{"attached_pic":0},"tags":{"filename":"cover.png","mimetype":"image/png"}}]}`, wantErr: true},
		{name: "Matroska duplicate cover representations", container: publication.AudioContainerMKA, cover: persistence.PublicationCoverOutcome{State: persistence.PublicationAssetEmbedded, MIME: "image/png"}, raw: `{"streams":[{"codec_type":"audio"},{"codec_type":"video","codec_name":"png","disposition":{"attached_pic":1},"tags":{"filename":"cover.png","mimetype":"image/png"}},{"codec_type":"attachment","tags":{"filename":"cover.png","mimetype":"image/png"}}]}`, wantErr: true},
		{name: "Matroska attached picture without cover", container: publication.AudioContainerMKA, cover: persistence.PublicationCoverOutcome{State: persistence.PublicationAssetAbsent}, raw: `{"streams":[{"codec_type":"audio"},{"codec_type":"video","codec_name":"png","disposition":{"attached_pic":1},"tags":{"filename":"cover.png","mimetype":"image/png"}}]}`, wantErr: true},
		{name: "Matroska arbitrary attachment", container: publication.AudioContainerMKA, cover: persistence.PublicationCoverOutcome{State: persistence.PublicationAssetEmbedded, MIME: "image/png"}, raw: `{"streams":[{"codec_type":"audio"},{"codec_type":"attachment","tags":{"filename":"payload.bin","mimetype":"application/octet-stream"}}]}`, wantErr: true},
		{name: "Matroska second image", container: publication.AudioContainerMKA, cover: persistence.PublicationCoverOutcome{State: persistence.PublicationAssetEmbedded, MIME: "image/png"}, raw: `{"streams":[{"codec_type":"audio"},{"codec_type":"attachment","tags":{"filename":"cover.png","mimetype":"image/png"}},{"codec_type":"attachment","tags":{"filename":"other.png","mimetype":"image/png"}}]}`, wantErr: true},
		{name: "Matroska chapters", container: publication.AudioContainerMKA, cover: persistence.PublicationCoverOutcome{State: persistence.PublicationAssetAbsent}, raw: `{"streams":[{"codec_type":"audio"}],"chapters":[{}]}`, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateFirstAudioCoverTopology([]byte(test.raw), test.container, test.cover)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}
