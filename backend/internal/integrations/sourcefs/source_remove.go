package sourcefs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var ErrSourceChanged = errors.New("sourcefs: source file changed")

// SourceRemovalOutcome classifies the native terminal-file observation made by
// SourceRemover. It does not authorize removal or treat absence as completion.
type SourceRemovalOutcome uint8

const (
	SourceRemovalUnresolved SourceRemovalOutcome = iota
	SourceRemovalRemoved
	SourceRemovalMissingLeaf
)

// SourceRemover removes one regular source file only when its current size and
// microsecond-resolution mtime still match the recorded source identity. This
// helper does not authorize deletion: callers must complete permission,
// borrower, and durable-claim checks first.
//
// The validation and os.Remove are intentionally sequential, not atomic. An
// external rename between them may change the named object; that residual race
// is accepted by the source-removal contract.
type SourceRemover struct{}

// Remove checks the root and parent path without following links, validates the
// current terminal object, then removes the named file. Missing files are
// returned as os.ErrNotExist for the caller to classify.
func (SourceRemover) Remove(ctx context.Context, rootPath, relativePath string, expectedSize int64, expectedMTime time.Time) error {
	_, err := (SourceRemover{}).RemoveWithOutcome(ctx, rootPath, relativePath, expectedSize, expectedMTime)
	return err
}

// RemoveWithOutcome performs the same validated, sequential source removal as
// Remove and reports whether os.Remove succeeded or the terminal leaf was
// already absent at the initial Lstat. Missing roots and parents remain errors.
func (SourceRemover) RemoveWithOutcome(ctx context.Context, rootPath, relativePath string, expectedSize int64, expectedMTime time.Time) (SourceRemovalOutcome, error) {
	if err := ctx.Err(); err != nil {
		return SourceRemovalUnresolved, err
	}
	if expectedSize < 0 {
		return SourceRemovalUnresolved, ErrInvalidPath
	}
	components, err := validateRelativePath(relativePath, true)
	if err != nil {
		return SourceRemovalUnresolved, err
	}
	root, err := NewOpener().OpenRoot(ctx, rootPath)
	if err != nil {
		return SourceRemovalUnresolved, err
	}
	defer func() { _ = root.Close() }()
	current := root
	owned := false
	for _, component := range components[:len(components)-1] {
		next, openErr := current.OpenDir(ctx, component)
		if owned {
			closeErr := current.Close()
			if openErr == nil && closeErr != nil {
				_ = next.Close()
				return SourceRemovalUnresolved, closeErr
			}
		}
		if openErr != nil {
			return SourceRemovalUnresolved, openErr
		}
		current, owned = next, true
	}
	if owned {
		defer func() { _ = current.Close() }()
	}
	if err := ctx.Err(); err != nil {
		return SourceRemovalUnresolved, err
	}
	fullPath := filepath.Join(rootPath, filepath.FromSlash(strings.Join(components, "/")))
	info, err := os.Lstat(fullPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return SourceRemovalMissingLeaf, err
		}
		return SourceRemovalUnresolved, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return SourceRemovalUnresolved, ErrLink
	}
	if !info.Mode().IsRegular() {
		return SourceRemovalUnresolved, ErrNotRegular
	}
	if info.Size() != expectedSize || !info.ModTime().Truncate(time.Microsecond).Equal(expectedMTime.Truncate(time.Microsecond)) {
		return SourceRemovalUnresolved, ErrSourceChanged
	}
	if err := ctx.Err(); err != nil {
		return SourceRemovalUnresolved, err
	}
	if err := os.Remove(fullPath); err != nil {
		return SourceRemovalUnresolved, err
	}
	return SourceRemovalRemoved, nil
}
