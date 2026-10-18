//go:build linux || darwin

package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRunJobCommandUnixHelper(t *testing.T) {
	if os.Getenv("GO_WANT_UNIX_JOB_COMMAND_HELPER") != "1" {
		return
	}
	mode := os.Getenv("GO_UNIX_JOB_COMMAND_MODE")
	if len(os.Args) > 1 && os.Args[len(os.Args)-1] == "sleep" {
		mode = "sleep"
	}
	switch mode {
	case "success":
		_, _ = io.Copy(os.Stdout, os.Stdin)
		_, _ = io.WriteString(os.Stderr, "stderr")
	case "output":
		_, _ = io.WriteString(os.Stdout, "output")
	case "nonzero":
		os.Exit(7)
	case "sleep":
		time.Sleep(10 * time.Second)
	case "pipe-child":
		child := exec.Command(os.Args[0], "-test.run=^TestRunJobCommandUnixHelper$", "--", "sleep")
		child.Env = make([]string, 0, len(os.Environ())+2)
		for _, item := range os.Environ() {
			if !strings.HasPrefix(item, "GO_WANT_UNIX_JOB_COMMAND_HELPER=") && !strings.HasPrefix(item, "GO_UNIX_JOB_COMMAND_MODE=") {
				child.Env = append(child.Env, item)
			}
		}
		child.Env = append(child.Env, "GO_WANT_UNIX_JOB_COMMAND_HELPER=1", "GO_UNIX_JOB_COMMAND_MODE=sleep")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		_, _ = fmt.Fprintf(os.Stdout, "child-pid=%d\n", child.Process.Pid)
	}
	os.Exit(0)
	t.SkipNow()
}

func unixJobCommand(t *testing.T, _ string, args ...string) JobCommandRequest {
	t.Helper()
	executable, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	return JobCommandRequest{
		Executable: executable,
		Args:       append([]string{"-test.run=^TestRunJobCommandUnixHelper$"}, args...),
	}
}

func runUnixJobHelper(t *testing.T, mode string, request JobCommandRequest) (JobCommandResult, error) {
	t.Helper()
	t.Setenv("GO_WANT_UNIX_JOB_COMMAND_HELPER", "1")
	t.Setenv("GO_UNIX_JOB_COMMAND_MODE", mode)
	return RunJobCommand(context.Background(), request)
}

func TestRunJobCommandUnixStreamsInputAndReportsExit(t *testing.T) {
	input, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close() }()
	_, _ = input.WriteString("borrowed")
	_, _ = input.Seek(0, io.SeekStart)
	var stdout, stderr bytes.Buffer
	request := unixJobCommand(t, "success")
	request.Input, request.Stdout, request.Stderr = input, &stdout, &stderr
	result, err := runUnixJobHelper(t, "success", request)
	if err != nil || !result.QuiescenceEstablished || result.ExitCode != 0 {
		t.Fatalf("RunJobCommand() = (%+v, %v), want completed exit 0", result, err)
	}
	if !strings.HasPrefix(stdout.String(), "borrowed") || stderr.String() != "stderr" {
		t.Fatalf("stdout/stderr = %q/%q", stdout.String(), stderr.String())
	}
	if _, err := input.Seek(0, io.SeekCurrent); err != nil {
		t.Fatalf("borrowed input was closed: %v", err)
	}

	result, err = runUnixJobHelper(t, "nonzero", unixJobCommand(t, "nonzero"))
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || !result.QuiescenceEstablished || result.ExitCode != 7 {
		t.Fatalf("nonzero command = (%+v, %v), want completed exit 7", result, err)
	}
}

func TestRunJobCommandUnixCancellationAndInheritedPipeAreBounded(t *testing.T) {
	request := unixJobCommand(t, "sleep")
	t.Setenv("GO_WANT_UNIX_JOB_COMMAND_HELPER", "1")
	t.Setenv("GO_UNIX_JOB_COMMAND_MODE", "sleep")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	result, err := RunJobCommand(ctx, request)
	if !errors.Is(err, context.DeadlineExceeded) || !result.QuiescenceEstablished {
		t.Fatalf("canceled command = (%+v, %v), want completed main process and deadline error", result, err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("canceled command did not return promptly")
	}

	request = unixJobCommand(t, "pipe-child")
	var output bytes.Buffer
	request.Stdout = &output
	started = time.Now()
	result, err = runUnixJobHelper(t, "pipe-child", request)
	if !result.QuiescenceEstablished {
		t.Fatalf("parent with inherited output pipe = (%+v, %v)", result, err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("inherited output pipe delayed return for %s", elapsed)
	}
	line, _, _ := strings.Cut(output.String(), "\n")
	var childProcess *os.Process
	if strings.HasPrefix(line, "child-pid=") {
		if pid, parseErr := strconv.Atoi(strings.TrimPrefix(line, "child-pid=")); parseErr == nil {
			childProcess, _ = os.FindProcess(pid)
			if childProcess != nil {
				t.Cleanup(func() { _ = childProcess.Kill() })
			}
		}
	}
	if !strings.HasPrefix(line, "child-pid=") || strings.TrimSuffix(output.String(), "\n") != line {
		t.Fatalf("child PID output = %q, want exactly one child-pid line", output.String())
	}
	if childProcess == nil {
		t.Fatalf("parse or find helper child from output %q", output.String())
	}
	if _, err := strconv.Atoi(strings.TrimPrefix(line, "child-pid=")); err != nil {
		t.Fatalf("parse helper child PID: %v", err)
	}
}

func TestRunJobCommandUnixRejectsRelativeExecutable(t *testing.T) {
	result, err := RunJobCommand(context.Background(), JobCommandRequest{Executable: "not-absolute"})
	if err == nil || result.QuiescenceEstablished || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("RunJobCommand() = (%+v, %v), want absolute path validation error", result, err)
	}
}

func TestRunJobCommandUnixRejectsMissingAndNonExecutablePaths(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	result, err := RunJobCommand(context.Background(), JobCommandRequest{Executable: missing})
	if err == nil || result.QuiescenceEstablished {
		t.Fatalf("missing executable = (%+v, %v), want start failure without established completion", result, err)
	}

	nonExecutable := filepath.Join(t.TempDir(), "not-executable")
	if err := os.WriteFile(nonExecutable, []byte("not executable"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err = RunJobCommand(context.Background(), JobCommandRequest{Executable: nonExecutable})
	if err == nil || result.QuiescenceEstablished {
		t.Fatalf("non-executable path = (%+v, %v), want start failure without established completion", result, err)
	}
}

func TestRunJobCommandUnixCanceledBeforeStartDoesNotEstablishCompletion(t *testing.T) {
	request := unixJobCommand(t, "success")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := RunJobCommand(ctx, request)
	if !errors.Is(err, context.Canceled) || result.QuiescenceEstablished {
		t.Fatalf("canceled-before-start command = (%+v, %v), want cancellation without established completion", result, err)
	}
}

type unixFailingOutputWriter struct{ err error }

func (writer unixFailingOutputWriter) Write([]byte) (int, error) { return 0, writer.err }

func TestRunJobCommandUnixRetainsOutputWriterFailureAfterProcessExit(t *testing.T) {
	writerErr := errors.New("output writer failed")
	request := unixJobCommand(t, "output")
	request.Stdout = unixFailingOutputWriter{err: writerErr}
	result, err := runUnixJobHelper(t, "output", request)
	if !errors.Is(err, writerErr) || !result.QuiescenceEstablished || result.ExitCode != 0 {
		t.Fatalf("writer failure command = (%+v, %v), want completed exit 0 and writer error", result, err)
	}
}
