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
	CreateDirExclusive(context.Context, string) (OutputDirectory, error)
	CreateExclusive(context.Context, string) (OutputFile, error)
	Identity(context.Context) (DirectoryIdentity, error)
	Stat(context.Context) (fs.FileInfo, error)
	Verify(context.Context) error
	Close() error
}

// DirectoryIdentity identifies the directory referenced by an open output
// handle. It is suitable for persistence preparation, not proof against
// hostile concurrent actors or filesystem identifier reuse.
type DirectoryIdentity struct {
	Platform string `json:"platform"`
	Volume   string `json:"volume"`
	FileID   string `json:"file_id"`
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
