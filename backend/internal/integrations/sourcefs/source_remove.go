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
	if err := ctx.Err(); err != nil {
		return err
	}
	if expectedSize < 0 {
		return ErrInvalidPath
	}
	components, err := validateRelativePath(relativePath, true)
	if err != nil {
		return err
	}
	root, err := NewOpener().OpenRoot(ctx, rootPath)
	if err != nil {
		return err
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
				return closeErr
			}
		}
		if openErr != nil {
			return openErr
		}
		current, owned = next, true
	}
	if owned {
		defer func() { _ = current.Close() }()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	fullPath := filepath.Join(rootPath, filepath.FromSlash(strings.Join(components, "/")))
	info, err := os.Lstat(fullPath)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return ErrLink
	}
	if !info.Mode().IsRegular() {
		return ErrNotRegular
	}
	if info.Size() != expectedSize || !info.ModTime().Truncate(time.Microsecond).Equal(expectedMTime.Truncate(time.Microsecond)) {
		return ErrSourceChanged
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Remove(fullPath)
}
