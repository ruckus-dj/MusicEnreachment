// Command scanningprobe is a native, deterministic ffmpeg-package stand-in for
// source scan integration tests. It exercises descriptor protocol capability
// checks as well as the scan's per-file probe without relying on a shell.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const seekPackets = `{"packets":[{"pts_time":"8.0","data_hash":"SHA256:0000000000000000000000000000000000000000000000000000000000000000"},{"pts_time":"0.0","data_hash":"SHA256:1111111111111111111111111111111111111111111111111111111111111111"}]}`

type config struct {
	Version string `json:"version"`
	LogPath string `json:"log_path"`
	NoAudio bool   `json:"no_audio"`
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
	configuration := readConfig()
	for _, argument := range os.Args[1:] {
		switch argument {
		case "-version":
			name := strings.TrimSuffix(filepath.Base(os.Args[0]), filepath.Ext(os.Args[0]))
			version := configuration.Version
			if version == "" {
				version = "1.6.1"
			}
			fmt.Printf("%s version %s\n", name, version)
			return
		case "-protocols":
			_, _ = os.Stdout.WriteString("Input:\n fd\n file\nOutput:\n file\n")
			return
		case "-show_packets":
			_, _ = os.Stdout.WriteString(seekPackets)
			return
		}
	}

	if path := configuration.LogPath; path != "" {
		if file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			_, _ = file.WriteString("probe\n")
			_ = file.Close()
		}
	}
	data := readInput()
	switch {
	case strings.Contains(string(data), "unreadable") || strings.Contains(string(data), "broken"):
		_, _ = os.Stderr.WriteString("the probe failed\n")
		os.Exit(1)
	case configuration.NoAudio || strings.Contains(string(data), "video only"):
		_, _ = os.Stdout.WriteString(`{"format":{},"streams":[{"codec_type":"video"}]}`)
	default:
		_, _ = os.Stdout.WriteString(`{"format":{},"streams":[{"codec_type":"audio"}]}`)
	}
}

func readInput() []byte {
	file := os.Stdin
	if filepath.Separator != '\\' {
		file = os.NewFile(3, "ffprobe-input")
	}
	if file == nil {
		return nil
	}
	data, _ := io.ReadAll(file)
	return data
}
