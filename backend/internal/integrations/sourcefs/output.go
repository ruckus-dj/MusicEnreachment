package sourcefs

import (
	"context"
	"io/fs"
	"os"
)

// OutputOpener safely opens an existing managed output directory by absolute path.
type OutputOpener interface {
	OpenRoot(context.Context, string) (OutputDirectory, error)
}

// OutputDirectory is a pinned, writable directory capability. Unlike Directory,
// it grants creation and mutation rights and is only for managed output trees.
type OutputDirectory interface {
	OpenOrCreateDir(context.Context, string) (OutputDirectory, error)
	CreateExclusive(context.Context, string) (OutputFile, error)
	Stat(context.Context) (fs.FileInfo, error)
	Verify(context.Context) error
	Close() error
}

// OutputFile is a newly created regular output file. BorrowRead loans the
// descriptor synchronously from offset zero; the callback must not retain it.
type OutputFile interface {
	Write(context.Context, []byte) (int, error)
	Sync(context.Context) error
	Stat(context.Context) (fs.FileInfo, error)
	BorrowRead(context.Context, func(*os.File) error) error
	Close() error
}

// NewOutputOpener returns the platform's handle-relative managed-output opener.
func NewOutputOpener() OutputOpener { return newPlatformOutputOpener() }
