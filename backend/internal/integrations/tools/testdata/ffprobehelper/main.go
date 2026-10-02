// Command ffprobehelper is a real subprocess stand-in for the technical-probe
// tests. It reproduces the process-boundary facts a fake cannot prove - the
// actual byte volume read from a child's pipes, the exit status and the argv
// the child receives - without depending on a managed ffprobe installation.
//
// Behaviour is selected with FFPROBE_HELPER_MODE:
//
//	valid              print a small valid multi-stream JSON response
//	oversize-stdout    write more than the stdout bound
//	oversize-stderr    write more than the stderr bound
//	nonzero            write a diagnostic to stderr and exit non-zero
//	block-until-killed dial FFPROBE_HELPER_READY_ADDR, send a readiness byte,
//	                   then block on the connection until the parent kills it
//
// When FFPROBE_HELPER_ARGV_FILE is set it records its own argv there, so a test
// can prove the filename arrived as one argument instead of a shell command.
// FFPROBE_HELPER_READY_ADDR is a test-only loopback address and is never used
// outside the tests.
package main

import (
	"encoding/json"
	"io"
	"net"
	"os"
)

func main() {
	if path := os.Getenv("FFPROBE_HELPER_ARGV_FILE"); path != "" {
		if encoded, err := json.Marshal(os.Args); err == nil {
			_ = os.WriteFile(path, encoded, 0o644)
		}
	}

	switch os.Getenv("FFPROBE_HELPER_MODE") {
	case "valid":
		_, _ = os.Stdout.WriteString(`{"format":{"format_name":"flac"},"streams":[{"index":0,"codec_type":"audio","codec_name":"flac"},{"index":1,"codec_type":"video","codec_name":"mjpeg"}]}`)
	case "oversize-stdout":
		writeRepeated(os.Stdout, (1<<20)+1024, 'x')
	case "oversize-stderr":
		writeRepeated(os.Stderr, (64<<10)+1024, 'e')
	case "nonzero":
		_, _ = os.Stderr.WriteString("secret diagnostic that must never reach the user")
		os.Exit(3)
	case "block-until-killed":
		conn, err := net.Dial("tcp", os.Getenv("FFPROBE_HELPER_READY_ADDR"))
		if err != nil {
			os.Exit(4)
		}
		if _, err := conn.Write([]byte{'R'}); err != nil {
			os.Exit(5)
		}
		// The readiness byte is the exact event the parent waits on; block on
		// the connection until the parent kills this process.
		_, _ = io.Copy(io.Discard, conn)
	default:
		os.Exit(2)
	}
}

func writeRepeated(file *os.File, count int, value byte) {
	chunk := make([]byte, 4096)
	for i := range chunk {
		chunk[i] = value
	}
	for count > 0 {
		size := len(chunk)
		if count < size {
			size = count
		}
		if _, err := file.Write(chunk[:size]); err != nil {
			return
		}
		count -= size
	}
}
