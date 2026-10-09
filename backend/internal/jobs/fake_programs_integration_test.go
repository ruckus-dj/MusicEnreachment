//go:build integration

package jobs

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
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
			if output, err := build.CombinedOutput(); err != nil {
				fakeProgramsErr = fmt.Errorf("build %s helper: %w: %s", program.name, err, output)
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

func configureAnalysisProbe(t *testing.T, executablePath, fail string) {
	t.Helper()
	configuration := analysisProbeConfig{LogPath: filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(executablePath))), "probe-log"), Fail: fail}
	for _, name := range tools.ExpectedExecutables(tools.PackageFFmpeg, runtime.GOOS) {
		path := filepath.Join(filepath.Dir(executablePath), name+".json")
		if err := writeFakeConfig(path, configuration); err != nil {
			t.Fatalf("configure fake %s: %v", name, err)
		}
	}
}

func writeFakeConfig(path string, config any) error {
	encoded, err := json.Marshal(config)
	if err != nil {
		return err
	}
	return os.WriteFile(path, encoded, 0o644)
}
