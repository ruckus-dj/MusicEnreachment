// Package artwork provides bounded, header-only inspection of image data.
package artwork

import (
	"bytes"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

const MaxOriginalDimension = 10000

var ErrDimensionsExceeded = errors.New("artwork image dimensions exceed the maximum")

// Inspect validates an image header and returns its registered format and
// dimensions. It does not decode pixels or allocate an image-sized buffer.
func Inspect(data []byte) (format string, width, height int, err error) {
	if len(data) == 0 {
		return "", 0, 0, errors.New("artwork image is empty")
	}
	return InspectReader(bytes.NewReader(data))
}

// InspectReader reads the image header without retaining the encoded input.
func InspectReader(reader io.Reader) (format string, width, height int, err error) {
	config, format, err := image.DecodeConfig(reader)
	if err != nil {
		return "", 0, 0, errors.New("artwork image header is invalid")
	}
	if config.Width <= 0 || config.Height <= 0 {
		return "", 0, 0, errors.New("artwork image dimensions are invalid")
	}
	if config.Width > MaxOriginalDimension || config.Height > MaxOriginalDimension {
		return "", 0, 0, ErrDimensionsExceeded
	}
	return format, config.Width, config.Height, nil
}
