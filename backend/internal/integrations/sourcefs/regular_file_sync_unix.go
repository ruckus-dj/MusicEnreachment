//go:build !windows

package sourcefs

import (
	"context"
	"os"
)

func (file *regularFile) Sync(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	file.mu.Lock()
	defer file.mu.Unlock()
	if file.closed {
		return os.ErrClosed
	}
	return file.file.Sync()
}
