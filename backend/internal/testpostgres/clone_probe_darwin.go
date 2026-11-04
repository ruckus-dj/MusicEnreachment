//go:build darwin

package testpostgres

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func cloneProbeCleanupFailure(primary, cleanup error) error {
	return cloneProbeCleanupError{err: errors.Join(primary, cleanup)}
}

func probeCloneFileCopy(workDir string) (bool, string, error) {
	probeDir, err := os.MkdirTemp(workDir, "melotrove-clone-probe-")
	if err != nil {
		return false, "", fmt.Errorf("create private clone-probe directory: %w", err)
	}
	cleanup := func(primary error) error {
		if err := os.RemoveAll(probeDir); err != nil {
			return cloneProbeCleanupFailure(primary, fmt.Errorf("remove private clone-probe directory: %w", err))
		}
		return primary
	}
	sourcePath := filepath.Join(probeDir, "source")
	destinationPath := filepath.Join(probeDir, "clone")
	source, err := os.OpenFile(sourcePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return false, "", cleanup(fmt.Errorf("create ordinary-file source: %w", err))
	}
	if _, err := source.Write([]byte("PostgreSQL clone capability probe\n")); err != nil {
		_ = source.Close()
		return false, "", cleanup(fmt.Errorf("write ordinary-file source: %w", err))
	}
	if err := source.Close(); err != nil {
		return false, "", cleanup(fmt.Errorf("close ordinary-file source: %w", err))
	}
	if err := unix.Clonefile(sourcePath, destinationPath, 0); err != nil {
		if cleanupErr := cleanup(nil); cleanupErr != nil {
			return false, "", cleanupErr
		}
		return false, fmt.Sprintf("macOS filesystem does not support file cloning: %v", err), nil
	}
	if err := cleanup(nil); err != nil {
		return false, "", err
	}
	return true, "", nil
}
