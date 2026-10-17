//go:build windows

package tools

import (
	"context"
	"errors"
	"os"
	"os/exec"

	"golang.org/x/sys/windows"
)

// DuplicateCommandInput returns an independently closeable, non-inheritable
// handle referring to input. The handle shares input's file position.
func DuplicateCommandInput(ctx context.Context, input *os.File) (*os.File, error) {
	if input == nil {
		return nil, errors.New("command input file is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var duplicate windows.Handle
	if err := windows.DuplicateHandle(windows.CurrentProcess(), windows.Handle(input.Fd()), windows.CurrentProcess(), &duplicate, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	f := os.NewFile(uintptr(duplicate), "")
	if err := ctx.Err(); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func configureFileCommandTransport(cmd *exec.Cmd, input *os.File) error {
	cmd.Stdin = input
	return nil
}
