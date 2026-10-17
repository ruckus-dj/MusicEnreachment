package service

import (
	"context"
	"errors"
	"os"
	"reflect"
	"sync"
	"testing"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
)

func TestPublicationJobCommandsSeparatesStreams(t *testing.T) {
	commands, err := newPublicationJobCommands(32, 32, func(_ context.Context, request tools.JobCommandRequest) (tools.JobCommandResult, error) {
		_, _ = request.Stdout.Write([]byte(`{"ok":true}`))
		_, _ = request.Stderr.Write([]byte("diagnostic"))
		return tools.JobCommandResult{QuiescenceEstablished: true}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := commands.Run(context.Background(), "/private/tool", "-json")
	if err != nil || !result.QuiescenceEstablished || string(result.Output) != `{"ok":true}` {
		t.Fatalf("Run() = (%+v, %v)", result, err)
	}
}

func TestPublicationJobCommandsOverflowPreservesQuiescence(t *testing.T) {
	for _, stream := range []string{"stdout", "stderr"} {
		t.Run(stream, func(t *testing.T) {
			commands, _ := newPublicationJobCommands(2, 2, func(_ context.Context, request tools.JobCommandRequest) (tools.JobCommandResult, error) {
				writer := request.Stdout
				if stream == "stderr" {
					writer = request.Stderr
				}
				_, _ = writer.Write([]byte("long"))
				return tools.JobCommandResult{QuiescenceEstablished: true}, nil
			})
			result, err := commands.Run(context.Background(), "/tool")
			if err == nil || !result.QuiescenceEstablished {
				t.Fatalf("Run() = (%+v, %v)", result, err)
			}
			if stream == "stdout" && string(result.Output) != "lo" {
				t.Fatalf("stdout = %q", result.Output)
			}
		})
	}
}

func TestPublicationJobCommandsPreservesUnknownBackendQuiescence(t *testing.T) {
	backendErr := errors.New("backend failed")
	commands, _ := newPublicationJobCommands(8, 8, func(context.Context, tools.JobCommandRequest) (tools.JobCommandResult, error) {
		return tools.JobCommandResult{}, backendErr
	})
	result, err := commands.Run(context.Background(), "/tool")
	if result.QuiescenceEstablished || !errors.Is(err, backendErr) {
		t.Fatalf("Run() = (%+v, %v)", result, err)
	}
}

func TestPublicationJobCommandsRejectsInvalidLimitsAndNilFileBeforeLaunch(t *testing.T) {
	for _, limits := range [][2]int{{0, 1}, {1, 0}, {-1, 1}} {
		if _, err := NewPublicationJobCommands(limits[0], limits[1]); err == nil {
			t.Fatalf("limits %v accepted", limits)
		}
	}
	calls := 0
	commands, _ := newPublicationJobCommands(1, 1, func(context.Context, tools.JobCommandRequest) (tools.JobCommandResult, error) {
		calls++
		return tools.JobCommandResult{}, nil
	})
	result, err := commands.RunFile(context.Background(), tools.FileCommandRequest{})
	if err == nil || !result.QuiescenceEstablished || calls != 0 {
		t.Fatalf("RunFile() = (%+v, %v), calls=%d", result, err, calls)
	}
}

func TestPublicationJobCommandsCanceledContextDoesNotLaunch(t *testing.T) {
	calls := 0
	commands, _ := newPublicationJobCommands(1, 1, func(context.Context, tools.JobCommandRequest) (tools.JobCommandResult, error) {
		calls++
		return tools.JobCommandResult{}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := commands.Run(ctx, "/tool")
	if err == nil || !result.QuiescenceEstablished || calls != 0 {
		t.Fatalf("Run() = (%+v, %v), calls=%d", result, err, calls)
	}
}

func TestPublicationJobCommandsRunFileBorrowsInputAndClonesArgs(t *testing.T) {
	input, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := input.Close(); err != nil {
			t.Errorf("close input: %v", err)
		}
	})
	args := []string{"one", "two"}
	commands, _ := newPublicationJobCommands(8, 8, func(_ context.Context, request tools.JobCommandRequest) (tools.JobCommandResult, error) {
		request.Args[0] = "changed"
		if request.Input != input {
			t.Fatal("input descriptor changed")
		}
		_, _ = request.Stdout.Write([]byte("ok"))
		return tools.JobCommandResult{QuiescenceEstablished: true}, nil
	})
	result, err := commands.RunFile(context.Background(), tools.FileCommandRequest{Executable: "/tool", Args: args, Input: input})
	if err != nil || string(result.Output) != "ok" || !reflect.DeepEqual(args, []string{"one", "two"}) {
		t.Fatalf("RunFile() = (%+v, %v), args=%v", result, err, args)
	}
	if _, err := input.Stat(); err != nil {
		t.Fatalf("borrowed input was closed: %v", err)
	}
}

func TestPublicationJobCommandsCollectorsAreIndependent(t *testing.T) {
	commands, _ := newPublicationJobCommands(8, 8, func(_ context.Context, request tools.JobCommandRequest) (tools.JobCommandResult, error) {
		_, _ = request.Stdout.Write([]byte(request.Args[0]))
		return tools.JobCommandResult{QuiescenceEstablished: true}, nil
	})
	var wg sync.WaitGroup
	for _, value := range []string{"first", "second"} {
		wg.Add(1)
		go func(value string) {
			defer wg.Done()
			result, err := commands.Run(context.Background(), "/tool", value)
			if err != nil || string(result.Output) != value {
				t.Errorf("Run(%q) = (%q, %v)", value, result.Output, err)
			}
		}(value)
	}
	wg.Wait()
}
