package tools

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestBuildTechnicalFileCommandRequestUsesOnlyFDTransport(t *testing.T) {
	input, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := input.Close(); err != nil {
			t.Errorf("close input: %v", err)
		}
	})
	request := BuildTechnicalFileCommandRequest("/managed/ffprobe", input)
	if request.Executable != "/managed/ffprobe" || request.Input != input {
		t.Fatalf("unexpected request: %#v", request)
	}
	joined := strings.Join(request.Args, " ")
	for _, required := range []string{"-protocol_whitelist fd", "-show_format", "-show_streams", "-show_chapters", "-of json", "fd:"} {
		if !strings.Contains(joined, required) {
			t.Errorf("arguments %q missing %q", joined, required)
		}
	}
	if !strings.Contains(joined, "-fd "+fileTransportDescriptor()) {
		t.Errorf("arguments %q lack platform descriptor", joined)
	}
	for _, forbidden := range []string{input.Name(), "file:", "http", "concat", "pipe:"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("arguments contain forbidden %q: %q", forbidden, joined)
		}
	}
	if slices.Contains(request.Args, "-show_entries") {
		t.Fatalf("technical request unexpectedly filters fields: %q", request.Args)
	}
}

func TestBuildPublicationFileCommandRequestUsesClosedDemuxerAllowlist(t *testing.T) {
	input, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := input.Close(); err != nil {
			t.Errorf("close publication probe input: %v", err)
		}
	})

	for _, demuxer := range []string{"mp3", "flac", "wav", "ogg", "aac", "mov", "matroska"} {
		t.Run(demuxer, func(t *testing.T) {
			request, err := BuildPublicationFileCommandRequest("/managed/ffprobe", demuxer, input)
			if err != nil {
				t.Fatal(err)
			}
			if request.Input != input {
				t.Fatal("request did not retain borrowed input")
			}
			args := request.Args
			whitelist := slices.Index(args, "-protocol_whitelist")
			if whitelist < 0 || whitelist+1 >= len(args) || args[whitelist+1] != "fd" {
				t.Fatalf("protocol whitelist missing or broadened: %q", args)
			}
			fdInput := slices.Index(args, "fd:")
			format := slices.Index(args, "-f")
			if fdInput < 0 || format < 0 || format+1 >= fdInput || args[format+1] != demuxer {
				t.Fatalf("forced demuxer not applied before fd input: %q", args)
			}
			if demuxer == "mov" {
				for _, flag := range []string{"-enable_drefs", "-use_absolute_path"} {
					if slices.Index(args, flag) < 0 {
						t.Errorf("MOV arguments missing %s: %q", flag, args)
					}
				}
			}
			if demuxer == "aac" && slices.Contains(args, "-f") && args[slices.Index(args, "-f")+1] != "aac" {
				t.Fatalf("AAC output demuxer was unexpectedly selected: %q", args)
			}
		})
	}

	for _, demuxer := range []string{"", "aac_latm", "file", "http", "../../mov"} {
		if request, err := BuildPublicationFileCommandRequest("/managed/ffprobe", demuxer, input); err == nil || request.Executable != "" || request.Args != nil {
			t.Errorf("unsupported demuxer %q returned request %#v, error %v", demuxer, request, err)
		}
	}
}

func TestConfigureFileCommandUsesExistingTransportWithoutStarting(t *testing.T) {
	input, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := input.Close(); err != nil {
			t.Errorf("close input: %v", err)
		}
	})
	request := BuildTechnicalFileCommandRequest("/managed/ffprobe", input)
	cmd := &exec.Cmd{}
	if err := ConfigureFileCommand(context.Background(), cmd, request); err != nil {
		t.Fatal(err)
	}
	if cmd.Path != request.Executable || len(cmd.Args) != len(request.Args)+1 || cmd.Args[0] != request.Executable {
		t.Fatalf("command not configured: %#v", cmd)
	}
	if runtime.GOOS == "windows" {
		if cmd.Stdin != input || len(cmd.ExtraFiles) != 0 {
			t.Fatalf("unexpected Windows transport: stdin=%v extra=%v", cmd.Stdin, cmd.ExtraFiles)
		}
	} else if len(cmd.ExtraFiles) != 1 || cmd.ExtraFiles[0] != input {
		t.Fatalf("unexpected Unix transport: %v", cmd.ExtraFiles)
	}
	if _, err := input.Stat(); err != nil {
		t.Fatalf("configuration closed borrowed input: %v", err)
	}
}

func TestDuplicateCommandInputLifetimeAndCancellation(t *testing.T) {
	if _, err := DuplicateCommandInput(context.Background(), nil); err == nil {
		t.Fatal("nil input accepted")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	input, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := input.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			t.Errorf("close input: %v", err)
		}
	})
	if duplicate, err := DuplicateCommandInput(canceled, input); duplicate != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled duplicate = %v, %v", duplicate, err)
	}
	if duplicate, err := DuplicateCommandInput(context.Background(), input); err != nil {
		t.Fatal(err)
	} else {
		if err := duplicate.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := input.Stat(); err != nil {
			t.Fatalf("closing duplicate closed original: %v", err)
		}
	}
	duplicate, err := DuplicateCommandInput(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := duplicate.Stat(); err != nil {
		t.Fatalf("duplicate unusable after original close: %v", err)
	}
	if err := duplicate.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := duplicate.Stat(); err == nil {
		t.Fatal("closed duplicate remained usable")
	}
	if _, err := input.Stat(); err == nil {
		t.Fatal("closed original remained usable")
	}
}
