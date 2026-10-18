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
	VerifyChild(context.Context, string, DirectoryIdentity) error
	Close() error
}

// OutputDirectoryTraversal is an optional read-only capability for opening an
// existing child directory relative to a pinned output directory.
type OutputDirectoryTraversal interface {
	OpenExistingDir(context.Context, string) (OutputDirectory, error)
}

// OutputMutation is an optional capability implemented only by native output
// directories that support handle-relative, no-replace publication operations.
// Keeping it separate avoids requiring test doubles and wrapped capabilities to
// expose mutation primitives they cannot safely implement.
type OutputMutation interface {
	RenameNoReplace(context.Context, OutputDirectory, string, OutputDirectory, string) error
	OpenExistingRegular(context.Context, string) (RegularFile, error)
}

// OutputRegularFileSync flushes an identity-matched file through a writable
// handle opened relative to the pinned output directory. The expected snapshot
// must come from the already-open native file, never from a pathname lookup.
type OutputRegularFileSync interface {
	SyncExistingRegular(context.Context, string, fs.FileInfo) error
}

// OutputAtomicReplace is a separate optional capability for replacing an
// existing regular output with an expected regular candidate. expectedSource
// and expectedTarget must be snapshots taken from open native file handles.
// The caller must have created and verified any required private backup first:
// this operation is not by itself authority to discard the old contents.
// Implementations provide atomic namespace replacement, not hostile-writer CAS.
type OutputAtomicReplace interface {
	AtomicReplaceExisting(context.Context, OutputDirectory, string, fs.FileInfo, string, fs.FileInfo) error
}

// OutputDirectorySync is an optional capability for flushing directory-entry
// changes through a pinned directory handle. This improves crash durability but
// does not guarantee survival of every filesystem/device power-loss scenario.
type OutputDirectorySync interface {
	SyncDirectory(context.Context) error
}

// OutputCleanup is an optional native capability for deleting only an
// identity-matched regular file or an identity-matched empty child directory.
// expectedParent binds the requested name to the caller's pinned parent.
// The expected file info passed to RemoveOwnedRegular must be a native,
// handle-derived snapshot (for example, from OutputFile.Stat), not os.Stat on a
// pathname. Call Stat while the file handle is open, before closing it.
type OutputCleanup interface {
	RemoveOwnedRegular(context.Context, string, DirectoryIdentity, fs.FileInfo) error
	RemoveEmptyChildDir(context.Context, string, DirectoryIdentity, DirectoryIdentity) error
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
