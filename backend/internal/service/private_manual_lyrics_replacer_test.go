package service

import (
	"context"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestPrivateManualLyricsReplacementRequiresPreparedProof(t *testing.T) {
	if _, err := replacePreparedManualLyricsFLAC(context.Background(), nil, nil, nil, persistence.PublicationInputs{}); err == nil {
		t.Fatal("replacement accepted missing native preparation proof")
	}
}
