package service

import (
	"errors"
	"os"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

func TestSourceAnalysisPathFailuresAreStaleOnlyWhenFilesystemProvesIt(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		err       error
		wantStale bool
	}{
		{name: "permission", err: os.ErrPermission},
		{name: "unsupported", err: sourcefs.ErrUnsupported},
		{name: "malformed path", err: sourcefs.ErrInvalidPath, wantStale: true},
		{name: "missing", err: os.ErrNotExist, wantStale: true},
		{name: "link", err: sourcefs.ErrLink, wantStale: true},
		{name: "wrong type", err: sourcefs.ErrNotRegular, wantStale: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := sourceAnalysisPathError("open source", testCase.err)
			if got := errors.Is(err, persistence.ErrSourceAnalysisStale); got != testCase.wantStale {
				t.Fatalf("stale classification = %v, want %v (error %v)", got, testCase.wantStale, err)
			}
		})
	}
}
