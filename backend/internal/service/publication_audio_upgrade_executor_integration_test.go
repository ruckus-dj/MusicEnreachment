//go:build integration

package service

import (
	"context"
	"os"
	"testing"

	integrationtools "github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
)

// The command transport supplies technical observations, not a real codec
// upgrade. Admission, filesystem replacement, metadata and DB settlement are real.

func audioUpgradePaddedFLAC(t *testing.T, data []byte) []byte {
	t.Helper()
	if len(data) < 8 || string(data[:4]) != "fLaC" {
		t.Fatal("invalid FLAC fixture")
	}
	result := append([]byte(nil), data...)
	for offset := 4; offset+4 <= len(result); {
		length := int(result[offset+1])<<16 | int(result[offset+2])<<8 | int(result[offset+3])
		end := offset + 4 + length
		if end > len(result) {
			t.Fatal("truncated FLAC fixture metadata")
		}
		if result[offset]&0x80 != 0 {
			result[offset] &^= 0x80
			padding := make([]byte, 20)
			padding[0], padding[3] = 0x81, 16
			return append(append(result[:end:end], padding...), result[end:]...)
		}
		offset = end
	}
	t.Fatal("FLAC fixture has no final metadata block")
	return nil
}

type audioUpgradeExecutorCommands struct{ audioDeliveryTestCommands }

func (audioUpgradeExecutorCommands) RunFile(_ context.Context, request integrationtools.FileCommandRequest) (PublicationCommandResult, error) {
	if request.Input == nil {
		return PublicationCommandResult{}, os.ErrInvalid
	}
	return PublicationCommandResult{QuiescenceEstablished: true, Output: []byte(`{"format":{"format_name":"flac"},"streams":[{"index":0,"codec_type":"audio","codec_name":"flac","channels":2,"channel_layout":"stereo","bits_per_raw_sample":"24","sample_rate":"48000"},{"index":1,"codec_type":"video","disposition":{"attached_pic":1},"tags":{"mimetype":"image/jpeg"}},{"index":2,"codec_type":"video","disposition":{"attached_pic":1},"tags":{"mimetype":"image/png"}}]}`)}, nil
}
