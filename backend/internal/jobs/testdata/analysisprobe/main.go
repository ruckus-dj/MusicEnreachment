// Command analysisprobe is a real, platform-native stand-in for the managed
// ffmpeg package an analysis worker runs. The integration tests compile it for
// the test platform and copy it to every executable name the platform expects,
// so the worker runs a genuine native executable on Linux, macOS and Windows -
// never a shell script and never a cross-build.
//
// It answers the lifecycle's -version query with a matching version line and,
// for a technical probe, appends one line to the file named by ANALYSIS_PROBE_LOG
// (so a test counts probes without a shell redirection and without a path that
// could break on spaces) and prints a minimal valid technical response. Setting
// ANALYSIS_PROBE_FAIL to "nonzero" or "malformed" makes the technical probe fail
// (a nonzero exit or invalid JSON) while the -version query stays valid, so a
// test can exercise a real probe failure distinct from a missing executable.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// release is the release identity the fixture installs; the version line must
// echo it for the lifecycle's read-only verification to accept the executable.
const release = "1.6.1"

type config struct {
	LogPath string `json:"log_path"`
	Fail    string `json:"fail"`
}

func readConfig() config {
	file, err := os.Open(os.Args[0] + ".json")
	if err != nil {
		return config{}
	}
	defer file.Close()
	var result config
	_ = json.NewDecoder(file).Decode(&result)
	return result
}

func main() {
	for _, argument := range os.Args[1:] {
		if argument == "-version" {
			name := strings.TrimSuffix(filepath.Base(os.Args[0]), filepath.Ext(os.Args[0]))
			fmt.Printf("%s version %s\n", name, release)
			return
		}
	}
	if hasArgument("-protocols") {
		_, _ = os.Stdout.WriteString("Input:\n fd\n file\nOutput:\n file\n")
		return
	}
	if hasArgument("-show_packets") {
		writeSeekWitnessPackets()
		return
	}
	configuration := readConfig()
	if path := configuration.LogPath; path != "" {
		if file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			_, _ = file.WriteString("probe\n")
			_ = file.Close()
		}
	}
	switch configuration.Fail {
	case "nonzero":
		os.Exit(3)
	case "malformed":
		_, _ = os.Stdout.WriteString("{not json")
		return
	}
	_, _ = os.Stdout.WriteString(`{"format":{"format_name":"flac","duration":"1.500"},"streams":[{"index":0,"codec_type":"audio","codec_name":"flac","sample_rate":"44100","channels":2}]}`)
}

func hasArgument(want string) bool {
	for _, argument := range os.Args[1:] {
		if argument == want {
			return true
		}
	}
	return false
}

func writeSeekWitnessPackets() {
	_, _ = os.Stdout.WriteString(`{"packets":[{"pts_time":"8.0","data_hash":"SHA256:0000000000000000000000000000000000000000000000000000000000000000"},{"pts_time":"0.0","data_hash":"SHA256:1111111111111111111111111111111111111111111111111111111111111111"}]}`)
}
