//go:build integration

package app

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

var (
	appFakeProgramOnce sync.Once
	appFakeProgramDir  string
	appFakeProgramErr  error
)

func cachedAppScanProbe() (string, error) {
	appFakeProgramOnce.Do(func() {
		appFakeProgramDir, appFakeProgramErr = os.MkdirTemp("", "melotrove-app-fakes-")
		if appFakeProgramErr != nil {
			return
		}
		testpostgres.AddCleanup(func() error { return os.RemoveAll(appFakeProgramDir) })
		build := exec.Command("go", "build", "-o", filepath.Join(appFakeProgramDir, "scanningprobe"), "../jobs/testdata/scanningprobe")
		if output, err := build.CombinedOutput(); err != nil {
			appFakeProgramErr = fmt.Errorf("build scan probe helper: %w: %s", err, output)
		}
	})
	if appFakeProgramErr != nil {
		return "", appFakeProgramErr
	}
	return filepath.Join(appFakeProgramDir, "scanningprobe"), nil
}

func copyAppScanProbe(source, target string, config any) error {
	contents, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err := os.WriteFile(target, contents, 0o755); err != nil {
		return err
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return err
	}
	return os.WriteFile(target+".json", encoded, 0o644)
}
