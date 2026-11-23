//go:build integration

package jobs

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/testpostgres"
)

var (
	fakeProgramsOnce sync.Once
	fakeProgramsDir  string
	fakeProgramsErr  error
)

type analysisProbeConfig struct {
	LogPath string `json:"log_path"`
	Fail    string `json:"fail"`
}

type scanningProbeConfig struct {
	Version string `json:"version"`
	LogPath string `json:"log_path"`
	NoAudio bool   `json:"no_audio"`
}

func cachedFakeProgram(name string) (string, error) {
	started := time.Now()
	defer func() {
		fmt.Fprintf(os.Stderr, "testhelper.phase program=%s phase=ready_wait duration=%s\n", name, time.Since(started))
	}()
	fakeProgramsOnce.Do(func() {
		fakeProgramsDir, fakeProgramsErr = os.MkdirTemp("", "melotrove-jobs-fakes-")
		if fakeProgramsErr != nil {
			return
		}
		testpostgres.AddCleanup(func() error { return os.RemoveAll(fakeProgramsDir) })
		for _, program := range []struct{ name, packagePath string }{
			{"analysisprobe", "./testdata/analysisprobe"},
			{"analysisfpcalc", "./testdata/analysisfpcalc"},
			{"scanningprobe", "./testdata/scanningprobe"},
		} {
			build := exec.Command("go", "build", "-o", filepath.Join(fakeProgramsDir, program.name), program.packagePath)
			buildStarted := time.Now()
			output, buildErr := build.CombinedOutput()
			fmt.Fprintf(os.Stderr, "testhelper.phase program=%s phase=compile duration=%s\n", program.name, time.Since(buildStarted))
			if buildErr != nil {
				fakeProgramsErr = fmt.Errorf("build %s helper: %w: %s", program.name, buildErr, output)
				return
			}
		}
	})
	if fakeProgramsErr != nil {
		return "", fakeProgramsErr
	}
	return filepath.Join(fakeProgramsDir, name), nil
}

func copyFakeExecutable(source, target string, config any) error {
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
