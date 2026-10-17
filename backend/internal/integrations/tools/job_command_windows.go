//go:build windows

package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const jobObjectBasicAccountingInformation = 1

var jobCommandQuarantine = struct {
	sync.Mutex
	items []jobCommandNative
}{}

type jobCommandNative struct {
	job, process, thread, input windows.Handle
}

func runPlatformJobCommand(ctx context.Context, request JobCommandRequest) (result JobCommandResult, returnedErr error) {
	if request.Executable == "" || !filepath.IsAbs(request.Executable) {
		return result, errors.New("job command executable must be an absolute path")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	line, err := windows.UTF16PtrFromString(composeWindowsCommandLine(request.Executable, request.Args))
	if err != nil {
		return result, fmt.Errorf("encode job command line: %w", err)
	}
	app, err := windows.UTF16PtrFromString(request.Executable)
	if err != nil {
		return result, fmt.Errorf("encode job command executable: %w", err)
	}

	native := jobCommandNative{job: windows.InvalidHandle, process: windows.InvalidHandle, thread: windows.InvalidHandle, input: windows.InvalidHandle}
	var parentReads []*os.File
	var stdoutDone, stderrDone chan error
	var temporaryChildHandles []windows.Handle
	spawned := false
	quarantined := false
	readersJoined := false
	defer func() {
		if recovered := recover(); recovered != nil {
			if spawned && !quarantined {
				_ = windows.TerminateJobObject(native.job, 1)
				_ = windows.TerminateProcess(native.process, 1)
				_, _ = windows.WaitForSingleObject(native.process, windows.INFINITE)
				quarantineJobCommand(native)
				quarantined = true
			}
			returnedErr = errors.Join(returnedErr, fmt.Errorf("job command panicked: %v", recovered))
		}
		if spawned && !result.QuiescenceEstablished && !quarantined {
			quarantineJobCommand(native)
			quarantined = true
		}
		if quarantined {
			for _, file := range parentReads {
				_ = file.Close()
			}
			for _, handle := range temporaryChildHandles {
				_ = windows.CloseHandle(handle)
			}
			if !readersJoined && stdoutDone != nil && stderrDone != nil {
				<-stdoutDone
				<-stderrDone
			}
			return
		}
		for _, file := range parentReads {
			if closeErr := file.Close(); closeErr != nil {
				returnedErr = errors.Join(returnedErr, fmt.Errorf("close job command output pipe: %w", closeErr))
			}
		}
		for _, handle := range temporaryChildHandles {
			if closeErr := windows.CloseHandle(handle); closeErr != nil {
				returnedErr = errors.Join(returnedErr, fmt.Errorf("close temporary job command handle: %w", closeErr))
			}
		}
		for _, handle := range []windows.Handle{native.thread, native.process, native.job, native.input} {
			if handle == 0 || handle == windows.InvalidHandle {
				continue
			}
			if closeErr := windows.CloseHandle(handle); closeErr != nil {
				returnedErr = errors.Join(returnedErr, fmt.Errorf("close job command native handle: %w", closeErr))
			}
		}
	}()

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return result, fmt.Errorf("create job command job: %w", err)
	}
	native.job = job
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return result, fmt.Errorf("configure job command job limits: %w", err)
	}

	inputSource := windows.Handle(windows.Stdin)
	if request.Input != nil {
		inputSource = windows.Handle(request.Input.Fd())
	} else {
		nul, openErr := windows.CreateFile(syscall.StringToUTF16Ptr(`NUL`), windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
		if openErr != nil {
			return result, fmt.Errorf("open null job command input: %w", openErr)
		}
		inputSource = nul
		defer func() {
			if closeErr := windows.CloseHandle(nul); closeErr != nil {
				returnedErr = errors.Join(returnedErr, fmt.Errorf("close null job command input: %w", closeErr))
			}
		}()
	}
	inputChild, err := duplicateInheritable(inputSource)
	if err != nil {
		return result, fmt.Errorf("duplicate job command input: %w", err)
	}
	native.input = inputChild

	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		return result, fmt.Errorf("create job command stdout pipe: %w", err)
	}
	defer func() { _ = stdoutWrite.Close() }()
	parentReads = append(parentReads, stdoutRead)
	if err := makeHandleNonInheritable(windows.Handle(stdoutRead.Fd())); err != nil {
		return result, fmt.Errorf("protect job command stdout read handle: %w", err)
	}
	if err := makeHandleNonInheritable(windows.Handle(stdoutWrite.Fd())); err != nil {
		return result, fmt.Errorf("protect job command stdout write handle: %w", err)
	}
	stdoutChild, err := duplicateInheritable(windows.Handle(stdoutWrite.Fd()))
	if err == nil {
		temporaryChildHandles = append(temporaryChildHandles, stdoutChild)
	}
	closeStdoutWriteErr := stdoutWrite.Close()
	if err != nil || closeStdoutWriteErr != nil {
		if err != nil {
			return result, fmt.Errorf("duplicate job command stdout: %w", err)
		}
		return result, fmt.Errorf("close job command stdout pipe: %w", closeStdoutWriteErr)
	}

	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		return result, fmt.Errorf("create job command stderr pipe: %w", err)
	}
	defer func() { _ = stderrWrite.Close() }()
	parentReads = append(parentReads, stderrRead)
	if err := makeHandleNonInheritable(windows.Handle(stderrRead.Fd())); err != nil {
		return result, fmt.Errorf("protect job command stderr read handle: %w", err)
	}
	if err := makeHandleNonInheritable(windows.Handle(stderrWrite.Fd())); err != nil {
		return result, fmt.Errorf("protect job command stderr write handle: %w", err)
	}
	stderrChild, err := duplicateInheritable(windows.Handle(stderrWrite.Fd()))
	if err == nil {
		temporaryChildHandles = append(temporaryChildHandles, stderrChild)
	}
	closeStderrWriteErr := stderrWrite.Close()
	if err != nil || closeStderrWriteErr != nil {
		if err != nil {
			return result, fmt.Errorf("duplicate job command stderr: %w", err)
		}
		return result, fmt.Errorf("close job command stderr pipe: %w", closeStderrWriteErr)
	}

	attributeList, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return result, fmt.Errorf("create job command handle list: %w", err)
	}
	defer attributeList.Delete()
	handleList := []windows.Handle{inputChild, stdoutChild, stderrChild}
	if err := attributeList.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handleList[0]), unsafe.Sizeof(handleList[0])*uintptr(len(handleList))); err != nil {
		return result, fmt.Errorf("configure job command handle list: %w", err)
	}
	startup := windows.StartupInfoEx{StartupInfo: windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{})), Flags: windows.STARTF_USESTDHANDLES, StdInput: inputChild, StdOutput: stdoutChild, StdErr: stderrChild}, ProcThreadAttributeList: attributeList.List()}
	var processInfo windows.ProcessInformation
	if err := windows.CreateProcess(app, line, nil, nil, true, windows.CREATE_SUSPENDED|windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT, nil, nil, &startup.StartupInfo, &processInfo); err != nil {
		return result, fmt.Errorf("create suspended job command process: %w", err)
	}
	spawned = true
	native.process = processInfo.Process
	native.thread = processInfo.Thread
	for _, handle := range temporaryChildHandles {
		if closeErr := windows.CloseHandle(handle); closeErr != nil {
			returnedErr = errors.Join(returnedErr, fmt.Errorf("close parent job command pipe handle: %w", closeErr))
		}
	}
	temporaryChildHandles = nil
	if closeErr := windows.CloseHandle(processInfo.Thread); closeErr != nil {
		terminateErr := windows.TerminateProcess(processInfo.Process, 1)
		_, waitErr := windows.WaitForSingleObject(processInfo.Process, windows.INFINITE)
		return result, errors.Join(fmt.Errorf("close job command initial thread: %w", closeErr), wrapIfError("terminate suspended job command", terminateErr), wrapIfError("wait for suspended job command", waitErr))
	}
	native.thread = windows.InvalidHandle
	if err := windows.AssignProcessToJobObject(job, processInfo.Process); err != nil {
		_ = windows.TerminateProcess(processInfo.Process, 1)
		_, _ = windows.WaitForSingleObject(processInfo.Process, windows.INFINITE)
		return result, fmt.Errorf("assign job command process to job: %w", err)
	}
	thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, processInfo.ThreadId)
	if err != nil {
		_ = windows.TerminateJobObject(job, 1)
		_, _ = windows.WaitForSingleObject(processInfo.Process, windows.INFINITE)
		return result, fmt.Errorf("open job command initial thread: %w", err)
	}
	native.thread = thread
	resumeCount, resumeErr := windows.ResumeThread(thread)
	threadCloseErr := windows.CloseHandle(thread)
	native.thread = windows.InvalidHandle
	if resumeErr != nil || resumeCount == 0xFFFFFFFF || threadCloseErr != nil {
		_ = windows.TerminateJobObject(job, 1)
		_, _ = windows.WaitForSingleObject(processInfo.Process, windows.INFINITE)
		resumeError := wrapIfError("resume job command process", resumeErr)
		if resumeCount == 0xFFFFFFFF {
			resumeError = errors.Join(resumeError, errors.New("resume job command process returned an invalid thread count"))
		}
		return result, errors.Join(resumeError, threadCloseErr)
	}

	stdoutDone = make(chan error, 1)
	stderrDone = make(chan error, 1)
	go func() { stdoutDone <- drainJobCommandPipe(stdoutRead, request.Stdout) }()
	go func() { stderrDone <- drainJobCommandPipe(stderrRead, request.Stderr) }()
	waitDone := make(chan error, 1)
	go func() {
		_, waitErr := windows.WaitForSingleObject(processInfo.Process, windows.INFINITE)
		waitDone <- waitErr
	}()
	var waitErr error
	waited := false
	select {
	case waitErr = <-waitDone:
		waited = true
	case <-ctx.Done():
	}
	terminateErr := windows.TerminateJobObject(job, 1)
	if !waited {
		// Cancellation must not bypass the required accounting proof.
		if _, waitProcessErr := windows.WaitForSingleObject(processInfo.Process, windows.INFINITE); waitProcessErr != nil {
			waitErr = errors.Join(waitErr, waitProcessErr)
		}
		waitErr = <-waitDone
	}
	if waitErr != nil {
		quarantineJobCommand(native)
		quarantined = true
		closeAndJoinReaders(parentReads, stdoutDone, stderrDone)
		readersJoined = true
		return result, waitErr
	}
	returnedErr = errors.Join(returnedErr, wrapIfError("terminate job command job", terminateErr))
	quiescent, accountingErr := waitForJobQuiescence(terminateErr != nil, func() (uint32, error) {
		var accounting [48]byte
		if err := windows.QueryInformationJobObject(job, jobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&accounting[0])), uint32(len(accounting)), nil); err != nil {
			return 0, err
		}
		return *(*uint32)(unsafe.Pointer(&accounting[40])), nil
	}, time.Sleep)
	if accountingErr != nil {
		quarantineJobCommand(native)
		quarantined = true
		closeAndJoinReaders(parentReads, stdoutDone, stderrDone)
		readersJoined = true
		return result, errors.Join(returnedErr, fmt.Errorf("query job command accounting: %w", accountingErr))
	}
	if !quiescent {
		quarantineJobCommand(native)
		quarantined = true
		closeAndJoinReaders(parentReads, stdoutDone, stderrDone)
		readersJoined = true
		return result, returnedErr
	}
	result.QuiescenceEstablished = true
	if err := <-stdoutDone; err != nil {
		returnedErr = errors.Join(returnedErr, fmt.Errorf("read job command stdout: %w", err))
	}
	if err := <-stderrDone; err != nil {
		returnedErr = errors.Join(returnedErr, fmt.Errorf("read job command stderr: %w", err))
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		returnedErr = errors.Join(returnedErr, ctxErr)
	}
	if err := windows.GetExitCodeProcess(processInfo.Process, &result.ExitCode); err != nil {
		returnedErr = errors.Join(returnedErr, fmt.Errorf("read job command exit code: %w", err))
	}
	return result, returnedErr
}

// waitForJobQuiescence polls after successful termination. If termination fails,
// one accounting query may still prove quiescence, but active members cannot be
// assumed to stop and must be handled by the caller as unknown.
func waitForJobQuiescence(terminationFailed bool, query func() (uint32, error), sleep func(time.Duration)) (bool, error) {
	for {
		active, err := query()
		if err != nil {
			return false, err
		}
		if active == 0 {
			return true, nil
		}
		if terminationFailed {
			return false, nil
		}
		sleep(10 * time.Millisecond)
	}
}

func duplicateInheritable(source windows.Handle) (windows.Handle, error) {
	var duplicate windows.Handle
	err := windows.DuplicateHandle(windows.CurrentProcess(), source, windows.CurrentProcess(), &duplicate, 0, true, windows.DUPLICATE_SAME_ACCESS)
	return duplicate, err
}

func makeHandleNonInheritable(handle windows.Handle) error {
	return windows.SetHandleInformation(handle, windows.HANDLE_FLAG_INHERIT, 0)
}

func drainJobCommandPipe(file *os.File, writer io.Writer) error {
	if writer == nil {
		writer = io.Discard
	}
	output := jobCommandOutputWriter{writer: writer}
	_, copyErr := io.Copy(&output, file)
	return errors.Join(copyErr, output.err)
}

// jobCommandOutputWriter remembers downstream failures but keeps consuming the
// child pipe so a broken consumer cannot block the child on a full pipe.
type jobCommandOutputWriter struct {
	writer io.Writer
	err    error
}

func (writer *jobCommandOutputWriter) Write(p []byte) (int, error) {
	if writer.err != nil {
		return len(p), nil
	}
	n, err := writer.writer.Write(p)
	if err != nil {
		writer.err = err
	} else if n != len(p) {
		writer.err = io.ErrShortWrite
	}
	return len(p), nil
}

func closeAndJoinReaders(files []*os.File, stdoutDone, stderrDone <-chan error) {
	for _, file := range files {
		_ = file.Close()
	}
	<-stdoutDone
	<-stderrDone
}

func quarantineJobCommand(native jobCommandNative) {
	jobCommandQuarantine.Lock()
	jobCommandQuarantine.items = append(jobCommandQuarantine.items, native)
	jobCommandQuarantine.Unlock()
}

func wrapIfError(message string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", message, err)
}

func composeWindowsCommandLine(executable string, args []string) string {
	parts := make([]string, 1, len(args)+1)
	parts[0] = quoteWindowsArgument(executable)
	for _, arg := range args {
		parts = append(parts, quoteWindowsArgument(arg))
	}
	return strings.Join(parts, " ")
}

func quoteWindowsArgument(argument string) string {
	if argument != "" && !strings.ContainsAny(argument, " \t\n\v\"") {
		return argument
	}
	var quoted strings.Builder
	quoted.WriteByte('"')
	backslashes := 0
	for _, char := range argument {
		if char == '\\' {
			backslashes++
			continue
		}
		if char == '"' {
			quoted.WriteString(strings.Repeat("\\", backslashes*2+1))
			quoted.WriteRune(char)
			backslashes = 0
			continue
		}
		quoted.WriteString(strings.Repeat("\\", backslashes))
		quoted.WriteRune(char)
		backslashes = 0
	}
	quoted.WriteString(strings.Repeat("\\", backslashes*2))
	quoted.WriteByte('"')
	return quoted.String()
}
