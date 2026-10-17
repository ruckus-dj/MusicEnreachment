//go:build !windows && !(aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris)

package tools

import (
	"context"
	"errors"
	"os"
	"os/exec"
)

func DuplicateCommandInput(context.Context, *os.File) (*os.File, error) {
	return nil, errors.New("file command descriptor transport is unsupported")
}

func configureFileCommandTransport(*exec.Cmd, *os.File) error {
	return errors.New("file command descriptor transport is unsupported")
}
