//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package tools

import (
	"context"
	"errors"
	"os"
	"os/exec"

	"golang.org/x/sys/unix"
)

// DuplicateCommandInput returns an independently closeable, close-on-exec
// descriptor referring to input. The descriptor shares input's file offset.
func DuplicateCommandInput(ctx context.Context, input *os.File) (*os.File, error) {
	if input == nil {
		return nil, errors.New("command input file is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fd, err := unix.FcntlInt(input.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	duplicate := os.NewFile(uintptr(fd), "")
	if err := ctx.Err(); err != nil {
		_ = duplicate.Close()
		return nil, err
	}
	return duplicate, nil
}

func configureFileCommandTransport(cmd *exec.Cmd, input *os.File) error {
	cmd.ExtraFiles = []*os.File{input}
	return nil
}
