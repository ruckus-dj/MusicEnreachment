package artwork

/*
#cgo pkg-config: vips
#include <stdlib.h>
#include <vips/vips.h>
#include "thumbnail_vips.h"
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"unsafe"
)

const MaxPublishedDimension = 2048

var (
	vipsOnce    sync.Once
	vipsInitErr error
	vipsCalls   = make(chan struct{}, 1)
)

// Thumbnail writes a bounded, lossless PNG thumbnail from inputPath to outputPath.
// Go copies the input with bounded streaming I/O and inspects headers only;
// libvips processes pixels using its demand-driven pipeline. Native codec buffers
// still contribute to process RAM.
func Thumbnail(ctx context.Context, inputPath, outputPath string) (err error) {
	if ctx == nil {
		return errors.New("thumbnail context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !filepath.IsAbs(inputPath) || !filepath.IsAbs(outputPath) {
		return errors.New("thumbnail input and output paths must be absolute")
	}
	_, originalWidth, originalHeight, err := inspectImagePath(inputPath)
	if err != nil {
		return fmt.Errorf("inspect thumbnail input: %w", err)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case vipsCalls <- struct{}{}:
	}
	defer func() { <-vipsCalls }()
	if err := ctx.Err(); err != nil {
		return err
	}
	vipsOnce.Do(func() {
		programName := C.CString("melotrove-thumbnail")
		defer C.free(unsafe.Pointer(programName))
		if C.vips_init(programName) != 0 {
			vipsInitErr = errors.New(C.GoString(C.vips_error_buffer()))
			return
		}
		// Every input is private, single-use staging. Caching its operations would
		// retain pixels and open descriptors after return (blocking Windows cleanup).
		C.vips_cache_set_max(0)
	})
	if vipsInitErr != nil {
		return fmt.Errorf("initialize libvips: %w", vipsInitErr)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	privateDir, err := os.MkdirTemp(filepath.Dir(outputPath), ".thumbnail-input-*")
	if err != nil {
		return fmt.Errorf("create private thumbnail input directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(privateDir) }()
	privateInput := filepath.Join(privateDir, "source.image")
	input, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("open thumbnail input: %w", err)
	}
	stagedInput, err := os.OpenFile(privateInput, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		_ = input.Close()
		return fmt.Errorf("create private thumbnail input: %w", err)
	}
	_, copyErr := io.Copy(stagedInput, thumbnailContextReader{ctx: ctx, reader: input})
	closeInputErr := input.Close()
	closeStagedErr := stagedInput.Close()
	if err := errors.Join(copyErr, closeInputErr, closeStagedErr); err != nil {
		return fmt.Errorf("stage thumbnail input: %w", err)
	}
	_, copiedWidth, copiedHeight, err := inspectImagePath(privateInput)
	if err != nil {
		return fmt.Errorf("inspect staged thumbnail input: %w", err)
	}
	if copiedWidth != originalWidth || copiedHeight != originalHeight {
		return errors.New("thumbnail input changed while it was staged")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// Stage beside the destination, then copy into an exclusively created private
	// output. Do not require hard-link support from the publication filesystem.
	if _, err := os.Lstat(outputPath); err == nil {
		return fmt.Errorf("thumbnail output already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect thumbnail output: %w", err)
	}
	staged, err := os.CreateTemp(filepath.Dir(outputPath), ".thumbnail-*.png")
	if err != nil {
		return fmt.Errorf("create staged thumbnail: %w", err)
	}
	stagedPath := staged.Name()
	if err := staged.Close(); err != nil {
		_ = os.Remove(stagedPath)
		return fmt.Errorf("close staged thumbnail: %w", err)
	}
	published := false
	defer func() {
		_ = os.Remove(stagedPath)
		if err != nil && published {
			_ = os.Remove(outputPath)
		}
	}()

	cInput := C.CString(privateInput)
	cOutput := C.CString(stagedPath)
	defer C.free(unsafe.Pointer(cInput))
	defer C.free(unsafe.Pointer(cOutput))
	message := make([]C.char, 2048)
	if C.artwork_vips_thumbnail(cInput, cOutput, &message[0], C.size_t(len(message))) != 0 {
		return fmt.Errorf("create thumbnail: %s", C.GoString(&message[0]))
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	format, width, height, err := inspectImagePath(stagedPath)
	if err != nil {
		return fmt.Errorf("inspect generated thumbnail: %w", err)
	}
	if format != "png" {
		return fmt.Errorf("generated thumbnail is not PNG")
	}
	if width > MaxPublishedDimension || height > MaxPublishedDimension || width > originalWidth || height > originalHeight {
		return fmt.Errorf("generated thumbnail dimensions %dx%d exceed source or publication bounds", width, height)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	output, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create thumbnail without replacing existing output: %w", err)
	}
	published = true
	encoded, err := os.Open(stagedPath)
	if err != nil {
		return errors.Join(fmt.Errorf("open staged thumbnail: %w", err), output.Close())
	}
	_, copyErr = io.Copy(output, thumbnailContextReader{ctx: ctx, reader: encoded})
	if err := errors.Join(copyErr, encoded.Close(), output.Close(), ctx.Err()); err != nil {
		return fmt.Errorf("copy private thumbnail: %w", err)
	}
	return nil
}

type thumbnailContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader thumbnailContextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}

func inspectImagePath(path string) (format string, width, height int, err error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, 0, err
	}
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	return InspectReader(file)
}
