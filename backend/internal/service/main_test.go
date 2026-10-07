package service

import (
	"os"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

func TestMain(m *testing.M) {
	os.Exit(testpostgres.Run(m))
}
