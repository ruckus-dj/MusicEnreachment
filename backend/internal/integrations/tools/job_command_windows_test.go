//go:build windows

package tools

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"
)

func TestJobCommandAccountingABI(t *testing.T) {
	var accounting [48]byte
	type basicAccountingInformation struct {
		TotalUserTime, TotalKernelTime, ThisPeriodUserTime, ThisPeriodKernelTime       int64
		TotalPageFaultCount, TotalProcesses, ActiveProcesses, TotalTerminatedProcesses uint32
	}
	if got := unsafe.Offsetof(basicAccountingInformation{}.ActiveProcesses); got != 40 {
		t.Fatalf("active process count offset = %d, want 40", got)
	}
	*(*uint32)(unsafe.Pointer(&accounting[40])) = 3
	if got := *(*uint32)(unsafe.Pointer(&accounting[40])); got != 3 {
		t.Fatalf("active process count decoded from accounting ABI = %d, want 3", got)
	}
}

func TestWaitForJobQuiescenceDoesNotPollAfterTerminationFailure(t *testing.T) {
	queries, sleeps := 0, 0
	quiescent, err := waitForJobQuiescence(true, func() (uint32, error) {
		queries++
		return 1, nil
	}, func(time.Duration) { sleeps++ })
	if err != nil {
		t.Fatalf("waitForJobQuiescence() error = %v, want nil", err)
	}
	if quiescent {
		t.Fatal("waitForJobQuiescence() reported quiescence with an active process")
	}
	if queries != 1 || sleeps != 0 {
		t.Fatalf("waitForJobQuiescence() queries = %d, sleeps = %d, want one query and no sleeps", queries, sleeps)
	}
}

func TestComposeWindowsCommandLineQuotesArguments(t *testing.T) {
	got := composeWindowsCommandLine(`C:\program files\tool.exe`, []string{"", `a\b\`, `say"hi`})
	want := `"C:\program files\tool.exe" "" a\b\ "say\"hi"`
	if got != want {
		t.Fatalf("composeWindowsCommandLine() = %q, want %q", got, want)
	}
}

func TestRunJobCommandStreamsInputAndOutput(t *testing.T) {
	if os.Getenv("GO_WANT_JOB_COMMAND_HELPER") == "1" {
		data, _ := io.ReadAll(os.Stdin)
		_, _ = os.Stdout.Write([]byte(strings.Join(os.Args[len(os.Args)-2:], "|") + ":"))
		_, _ = os.Stdout.Write(data)
		_, _ = os.Stderr.Write([]byte("helper-stderr"))
		os.Exit(7)
	}
	t.Setenv("GO_WANT_JOB_COMMAND_HELPER", "1")
	input, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := input.WriteString("borrowed-input"); err != nil {
		t.Fatal(err)
	}
	if _, err := input.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	executable, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	result, err := RunJobCommand(context.Background(), JobCommandRequest{
		Executable: executable,
		Args:       []string{"-test.run=^TestRunJobCommandStreamsInputAndOutput$", "--", "arg with spaces", "quote\"arg"},
		Input:      input,
		Stdout:     stdout,
		Stderr:     stderr,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.QuiescenceEstablished || result.ExitCode != 7 {
		t.Fatalf("RunJobCommand() result = %+v, want quiescent exit 7", result)
	}
	if got, want := stdout.String(), `arg with spaces|quote"arg:borrowed-input`; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr.String(), "helper-stderr"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
	if _, err := input.Seek(0, io.SeekCurrent); err != nil {
		t.Fatalf("borrowed input was closed: %v", err)
	}
}

func TestRunJobCommandQuiescesChildThatOutlivesPrimary(t *testing.T) {
	if os.Getenv("GO_WANT_JOB_COMMAND_SURVIVOR_HELPER") == "1" {
		mode := os.Args[len(os.Args)-1]
		if mode == "great-grandchild" {
			time.Sleep(10 * time.Second)
			return
		}
		next := "child"
		if mode == "child" {
			next = "great-grandchild"
		}
		child := exec.Command(os.Args[0], "-test.run=^TestRunJobCommandQuiescesChildThatOutlivesPrimary$", "--", next)
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Setenv("GO_WANT_JOB_COMMAND_SURVIVOR_HELPER", "1")
	executable, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for attempt := 0; attempt < 2; attempt++ {
		result, err := RunJobCommand(ctx, JobCommandRequest{
			Executable: executable,
			Args:       []string{"-test.run=^TestRunJobCommandQuiescesChildThatOutlivesPrimary$", "--", "parent"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if !result.QuiescenceEstablished {
			t.Fatalf("RunJobCommand() result = %+v, want quiescent descendant cleanup", result)
		}
	}
}

func TestRunJobCommandDrainsOutputAfterWriterFailure(t *testing.T) {
	if os.Getenv("GO_WANT_JOB_COMMAND_WRITER_FAILURE_HELPER") == "1" {
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("flood"), 2*1024*1024))
		return
	}
	t.Setenv("GO_WANT_JOB_COMMAND_WRITER_FAILURE_HELPER", "1")
	executable, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	writerErr := errors.New("output writer failed")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := RunJobCommand(ctx, JobCommandRequest{
		Executable: executable,
		Args:       []string{"-test.run=^TestRunJobCommandDrainsOutputAfterWriterFailure$"},
		Stdout:     failingJobCommandWriter{err: writerErr},
	})
	if !errors.Is(err, writerErr) {
		t.Fatalf("RunJobCommand() error = %v, want writer error", err)
	}
	if !result.QuiescenceEstablished {
		t.Fatalf("RunJobCommand() result = %+v, want proven quiescence", result)
	}
}

type failingJobCommandWriter struct{ err error }

func (writer failingJobCommandWriter) Write([]byte) (int, error) { return 0, writer.err }
