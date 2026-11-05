//go:build integration

package service

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	integrationtools "github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

type simpleCoverDeliveryCommands struct{ audioDeliveryTestCommands }

func (simpleCoverDeliveryCommands) RunFile(ctx context.Context, request integrationtools.FileCommandRequest) (PublicationCommandResult, error) {
	capture, err := (metadata.Reader{}).Read(ctx, request.Input.Name(), metadata.ReadRequest{CoverInspection: true})
	if err != nil {
		return PublicationCommandResult{QuiescenceEstablished: true}, err
	}
	assets, err := ObservePublicationAssets(capture)
	if err != nil {
		return PublicationCommandResult{QuiescenceEstablished: true}, err
	}
	streams := `{"index":0,"codec_type":"audio","codec_name":"flac","channels":2}`
	if assets.Cover.State == persistence.PublicationAssetEmbedded {
		streams += `,{"index":1,"codec_type":"video","codec_name":"png","disposition":{"attached_pic":1}}`
	}
	return PublicationCommandResult{QuiescenceEstablished: true, Output: []byte(`{"format":{"format_name":"flac"},"streams":[` + streams + `]}`)}, nil
}

func simpleCoverPNG(t *testing.T, pixel color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.SetRGBA(0, 0, pixel)
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func simpleCoverFLACFrames(t *testing.T, contents []byte) []byte {
	t.Helper()
	if len(contents) < 4 || string(contents[:4]) != "fLaC" {
		t.Fatal("invalid FLAC fixture")
	}
	for offset := 4; offset+4 <= len(contents); {
		last := contents[offset]&0x80 != 0
		size := int(contents[offset+1])<<16 | int(contents[offset+2])<<8 | int(contents[offset+3])
		offset += 4 + size
		if offset > len(contents) {
			t.Fatal("truncated FLAC metadata")
		}
		if last {
			return contents[offset:]
		}
	}
	t.Fatal("missing FLAC metadata terminator")
	return nil
}
