package sourcefs

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
)

// SHA256 hashes the bytes of an already-open regular file. It borrows the
// descriptor exclusively and never opens or stages a pathname.
func SHA256(ctx context.Context, file RegularFile) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	if file == nil {
		return digest, errors.New("source file is required")
	}
	err := file.Borrow(ctx, func(handle *os.File) error {
		hash := sha256.New()
		buffer := make([]byte, 32*1024)
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			n, readErr := handle.Read(buffer)
			if n > 0 {
				_, _ = hash.Write(buffer[:n])
			}
			if readErr != nil {
				if errors.Is(readErr, io.EOF) {
					if err := ctx.Err(); err != nil {
						return err
					}
					copy(digest[:], hash.Sum(nil))
					return nil
				}
				return readErr
			}
			if n == 0 {
				return io.ErrNoProgress
			}
		}
	})
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return digest, nil
}
