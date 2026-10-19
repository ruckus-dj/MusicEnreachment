package sourcefs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// NativeCompareComponents reports whether a and b resolve to the same regular
// file in a private scratch directory created beneath parent. It probes only
// that scratch namespace; it is not a global filename canonicalizer and does
// not prove that another directory inherits the same filesystem semantics.
func NativeCompareComponents(ctx context.Context, parent OutputDirectory, a, b string) (bool, error) {
	if err := validateNamespaceProbeComponent(a); err != nil {
		return false, err
	}
	if err := validateNamespaceProbeComponent(b); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if parent == nil {
		return false, errors.Join(ErrUnsupported, errors.New("sourcefs: output cleanup capabilities are unavailable"))
	}
	cleanup, cleanupOK := parent.(OutputCleanup)
	identifier, identityOK := parent.(DirectoryIdentifier)
	if !cleanupOK || !identityOK {
		return false, ErrUnsupported
	}
	parentID, err := identifier.Identity(ctx)
	if err != nil {
		return false, err
	}

	var scratch OutputDirectory
	var scratchName string
	for attempts := 0; attempts < 8; attempts++ {
		scratchName = ".namespace-probe-" + uuid.NewString()
		scratch, err = parent.CreateDirExclusive(ctx, scratchName)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrExist) {
			return false, err
		}
	}
	if scratch == nil {
		return false, fmt.Errorf("sourcefs: unable to allocate private namespace probe directory")
	}

	return compareInOwnedScratch(ctx, parent, cleanup, scratchName, parentID, scratch, a, b)
}

func validateNamespaceProbeComponent(name string) error {
	if !utf8.ValidString(name) || name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\:\x00") {
		return ErrInvalidPath
	}
	return nil
}

func compareInOwnedScratch(ctx context.Context, parent OutputDirectory, parentCleanup OutputCleanup, scratchName string, parentID DirectoryIdentity, scratch OutputDirectory, a, b string) (equivalent bool, resultErr error) {
	var scratchID DirectoryIdentity
	identityReady := false
	cleanup, cleanupOK := scratch.(OutputCleanup)
	var files []OutputFile
	var regularFiles []RegularFile
	owned := make(map[string]fs.FileInfo)
	workCtx := context.WithoutCancel(ctx)
	defer func() {
		cleanCtx := context.WithoutCancel(ctx)
		for _, file := range files {
			resultErr = errors.Join(resultErr, file.Close())
		}
		for _, file := range regularFiles {
			resultErr = errors.Join(resultErr, file.Close())
		}
		var cleanupErr error
		if cleanupOK && identityReady {
			for name, info := range owned {
				cleanupErr = errors.Join(cleanupErr, cleanup.RemoveOwnedRegular(cleanCtx, name, scratchID, info))
			}
		} else if !cleanupOK {
			cleanupErr = errors.Join(cleanupErr, ErrUnsupported)
		}
		closeErr := scratch.Close()
		if identityReady {
			verifyErr := parent.VerifyChild(cleanCtx, scratchName, scratchID)
			var removeDirErr error
			if verifyErr == nil {
				removeDirErr = parentCleanup.RemoveEmptyChildDir(cleanCtx, scratchName, parentID, scratchID)
			}
			cleanupErr = errors.Join(cleanupErr, closeErr, verifyErr, removeDirErr)
		} else {
			cleanupErr = errors.Join(cleanupErr, closeErr)
		}
		resultErr = errors.Join(resultErr, cleanupErr)
	}()
	var err error
	scratchID, err = scratch.Identity(workCtx)
	if err != nil {
		return false, fmt.Errorf("sourcefs: identify owned namespace probe directory; scratch retained: %w", err)
	}
	identityReady = true
	if !cleanupOK {
		return false, ErrUnsupported
	}

	if err := ctx.Err(); err != nil {
		return false, err
	}
	first, err := scratch.CreateExclusive(workCtx, a)
	if err != nil {
		return false, err
	}
	files = append(files, first)
	firstInfo, err := first.Stat(workCtx)
	if err != nil {
		return false, err
	}
	if firstInfo == nil || !firstInfo.Mode().IsRegular() {
		return false, ErrNotRegular
	}
	owned[a] = firstInfo
	if err := ctx.Err(); err != nil {
		return false, err
	}

	mutation, ok := scratch.(OutputMutation)
	if !ok {
		return false, ErrUnsupported
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	second, openErr := mutation.OpenExistingRegular(workCtx, b)
	if openErr == nil {
		regularFiles = append(regularFiles, second)
		if err := ctx.Err(); err != nil {
			return false, err
		}
		secondInfo, statErr := second.Stat(workCtx)
		if statErr != nil {
			return false, statErr
		}
		if secondInfo == nil || !secondInfo.Mode().IsRegular() {
			return false, ErrNotRegular
		}
		equivalent = os.SameFile(firstInfo, secondInfo)
	} else if errors.Is(openErr, fs.ErrNotExist) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		secondFile, createErr := scratch.CreateExclusive(workCtx, b)
		if createErr != nil {
			return false, createErr
		}
		files = append(files, secondFile)
		secondInfo, statErr := secondFile.Stat(workCtx)
		if statErr != nil {
			return false, statErr
		}
		if secondInfo == nil || !secondInfo.Mode().IsRegular() {
			return false, ErrNotRegular
		}
		owned[b] = secondInfo
		if err := ctx.Err(); err != nil {
			return false, err
		}
		equivalent = os.SameFile(firstInfo, secondInfo)
	} else {
		return false, openErr
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return equivalent, resultErr
}
